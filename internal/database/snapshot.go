package database

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/logger"

	"go.uber.org/zap"
)

// rebuildMigrations перечисляет миграции, которые пересоздают таблицу
// (CREATE _new → DROP → RENAME). Ошибка в середине такой миграции оставляет
// схему частично применённой, поэтому перед их применением снимается копия
// файла БД.
var rebuildMigrations = map[int]string{
	26: "remove_subscription_nodes_cascade",
	27: "add_pending_update_sync_status",
	32: "rebuild_subscription_nodes",
	33: "rebuild_broadcasts",
	34: "orders_subscription_cascade",
}

// pendingRebuildMigration сообщает номер ближайшей rebuild-миграции в
// диапазоне (current, max], либо 0, если таких нет.
func pendingRebuildMigration(current, max int) int {
	next := 0

	for version := range rebuildMigrations {
		if version > current && version <= max && (next == 0 || version < next) {
			next = version
		}
	}

	return next
}

// snapshotBeforeRebuild сохраняет копию файла БД перед деструктивной
// миграцией. Best-effort: отсутствие снапшота логируется, но не блокирует
// старт — бэкап штатно делается и помимо этого, а отказ старта из-за
// невозможности скопировать файл был бы хуже риска, который снимает копия.
func snapshotBeforeRebuild(sqlDB *sql.DB, dbPath string, current, max int) {
	version := pendingRebuildMigration(current, max)
	if version == 0 || dbPath == "" {
		return
	}

	// WAL содержит ещё не применённые к основному файлу страницы: без
	// чекпойнта копия одного файла БД оказалась бы устаревшей.
	_, checkpointErr := sqlDB.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
	if checkpointErr != nil {
		logger.Warn("snapshot before rebuild migration: wal checkpoint failed",
			zap.Int("migration", version),
			zap.Error(checkpointErr))
	}

	// #nosec G304 -- путь приходит из собственной конфигурации (PRAGMA
	// database_list текущего соединения), а не от пользователя.
	data, readErr := os.ReadFile(dbPath)
	if readErr != nil {
		logger.Warn("snapshot before rebuild migration: read database failed",
			zap.Int("migration", version),
			zap.String("path", dbPath),
			zap.Error(readErr))

		return
	}

	snapshotPath := fmt.Sprintf("%s.pre-migration.%d.%s", dbPath,
		version, time.Now().UTC().Format("20060102T150405Z"))

	// #nosec G703 -- snapshotPath построен из того же доверенного пути БД.
	writeErr := os.WriteFile(snapshotPath, data, 0o600)
	if writeErr != nil {
		logger.Warn("snapshot before rebuild migration: write snapshot failed",
			zap.Int("migration", version),
			zap.String("path", snapshotPath),
			zap.Error(writeErr))

		return
	}

	logger.Info("database snapshot taken before rebuild migration",
		zap.Int("migration", version),
		zap.String("name", rebuildMigrations[version]),
		zap.String("path", filepath.Clean(snapshotPath)),
		zap.Int("bytes", len(data)))
}
