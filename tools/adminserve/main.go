// Command adminserve runs only the admin panel against a throwaway copy of a
// database. It exists for agents and for manual inspection: the panel starts
// without the Telegram bot, without node clients, and without touching any real
// infrastructure (node calls become no-ops), so UI work can be reviewed live.
//
//	go run ./tools/adminserve -seed
//
// It is not part of the shipped binary and is never imported by it.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/kereal/rs8kvn_bot/admin"
	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/logger"
	"github.com/kereal/rs8kvn_bot/internal/service"

	"go.uber.org/zap"
)

func main() {
	var (
		srcPath = flag.String("db", "data/rs8kvn.db", "database file to copy; ignored with -empty")
		dstPath = flag.String("out", "/tmp/rs8kvn-admin-dev.db", "throwaway copy the panel reads and writes")
		addr    = flag.String("addr", config.DefaultAdminListen, "listen address")
		token   = flag.String("token", "admin", "bearer token for the panel")
		seed    = flag.Bool("seed", false, "insert demo rows when the copy has no subscriptions")
		empty   = flag.Bool("empty", false, "start from an empty database instead of copying one")
	)
	flag.Parse()

	// The ready line is the harness contract with whoever launches it (agent
	// or operator), so the log level must let Info through.
	_, err := logger.Init("", "info")
	if err != nil {
		panic(err)
	}

	if !*empty {
		err = copyFile(*srcPath, *dstPath)
		if err != nil {
			panic(err)
		}
	}

	store, err := database.NewService(*dstPath)
	if err != nil {
		panic(err)
	}

	defer func() { _ = store.Close() }()

	if *seed {
		seedDemo(store)
	}

	// The panel's service seams are real services over an empty node registry:
	// revocation, traffic reset and re-sync work against the database, but no
	// request ever leaves the process.
	syncService := service.NewSyncService(store, service.NodeClients{}, nil)
	subService := service.NewSubscriptionService(store, service.NodeClients{}, nil, &config.Config{})
	subService.SetSyncService(syncService)

	server := admin.NewServer(*addr, *token, store)
	server.SetSubscriptionServices(store, subService, syncService)
	// Живой трафик карточки подписки — тот же SubscriptionService.
	server.SetTrafficSource(subService)
	server.SetNodeLiveSource(subService)
	server.SetBroadcastDeactivator(subService)
	server.SetBuildInfo(admin.BuildInfo{
		Version:   "dev",
		Commit:    "local",
		BuildTime: time.Now().UTC().Format(time.RFC3339),
	})

	err = server.Start()
	if err != nil {
		panic(err)
	}

	logger.Info("ADMIN_READY",
		zap.String("url", "http://"+*addr+"/admin/"),
		zap.String("token", *token),
		zap.String("db", *dstPath))

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_ = server.Stop(ctx)
}

func copyFile(src, dst string) error {
	// #nosec G304 -- both paths come from the operator's own flags.
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open source database: %w", err)
	}

	defer func() { _ = in.Close() }()

	// #nosec G304 -- see above: the destination is the -out flag.
	out, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("create database copy: %w", err)
	}

	_, err = io.Copy(out, in)
	if err != nil {
		_ = out.Close()

		return fmt.Errorf("copy database: %w", err)
	}

	return out.Close()
}

// seedDemo fills an empty copy so every screen has realistic rows to render.
// It is a no-op when subscriptions already exist.
func seedDemo(store *database.Service) {
	db := store.GetDB()

	var count int64

	err := db.Model(&database.Subscription{}).Count(&count).Error
	if err != nil || count > 0 {
		return
	}

	statements := []string{
		// A node and a product are prerequisites: an empty database seeds only
		// the built-in plans, and the ORDER/binding rows carry foreign keys.
		`INSERT INTO nodes (name, host, subscription_url, inbound_ids, type, is_active)
		 SELECT 'demo-node', 'http://127.0.0.1:9999', 'http://127.0.0.1:9999/sub', '[1]', '3x-ui', 1
		 WHERE NOT EXISTS (SELECT 1 FROM nodes)`,
		`INSERT INTO products (plan_id, name, duration_days, price_cents, currency, is_active)
		 SELECT (SELECT id FROM plans ORDER BY traffic_limit DESC LIMIT 1), 'demo-30d', 30, 29900, 'RUB', 1
		 WHERE NOT EXISTS (SELECT 1 FROM products)`,
		`INSERT INTO subscriptions (telegram_id, client_id, subscription_id, status, plan_id, created_at, expires_at, last_request)
		 VALUES (990001,'c-ov1','sub-ov-1','active',(SELECT id FROM plans ORDER BY traffic_limit DESC LIMIT 1),
		         datetime('now','-10 days'),datetime('now','+20 days'),datetime('now','-2 hours')),
		        (990002,'c-ov2','sub-ov-2','revoked',(SELECT id FROM plans ORDER BY traffic_limit DESC LIMIT 1),
		         datetime('now','-40 days'),datetime('now','-5 days'),NULL),
		        (990003,'c-ov3','sub-ov-3','expired',(SELECT id FROM plans ORDER BY traffic_limit ASC LIMIT 1),
		         datetime('now','-90 days'),datetime('now','-10 days'),datetime('now','-3 days'))`,
		`INSERT INTO subscription_nodes (subscription_id, node_id, status, retry_count, updated_at)
		 SELECT id, (SELECT MIN(id) FROM nodes), 'active', 0, datetime('now') FROM subscriptions WHERE telegram_id = 990001`,
		// retry_count stays 0: the schema requires retry_at as soon as a retry
		// was counted, and the point here is a visibly stuck deletion.
		`INSERT INTO subscription_nodes (subscription_id, node_id, status, retry_count, updated_at)
		 SELECT id, (SELECT MIN(id) FROM nodes), 'pending_remove', 0, datetime('now','-2 days')
		 FROM subscriptions WHERE telegram_id = 990002`,
		`INSERT INTO orders (subscription_id, product_id, status, amount_cents, currency, created_at)
		 SELECT s.id, (SELECT MIN(id) FROM products), 'paid', 29900, 'RUB', datetime('now','-10 days')
		 FROM subscriptions s WHERE s.telegram_id = 990001`,
		`INSERT INTO broadcasts (name, filters, message_text, status, recipients_total, created_at, updated_at)
		 VALUES ('Анонс новых узлов','{}','Текст анонса','scheduled',41,datetime('now','-1 day'),datetime('now','-1 day'))`,
		`INSERT INTO audit_log (entity_type, entity_id, event, actor_type, created_at)
		 VALUES ('subscription','1','subscription_renewed','admin',datetime('now','-3 hours'))`,
	}

	for _, statement := range statements {
		execErr := db.Exec(statement).Error
		if execErr != nil {
			logger.Warn("seed statement failed",
				zap.String("statement", strings.SplitN(statement, "\n", 2)[0]),
				zap.Error(execErr))
		}
	}
}
