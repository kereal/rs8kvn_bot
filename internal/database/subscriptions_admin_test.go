package database

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gorm.io/gorm/logger"
)

// seedSubscriptionFixtures creates two plans, one node, subscriptions with
// distinct statuses/expiries and subscription_nodes bindings. Returns the
// created subscriptions.
func seedSubscriptionFixtures(t *testing.T, svc *Service) []*Subscription {
	t.Helper()
	ctx := context.Background()

	plan := &Plan{Name: "premium", IsActive: true, DevicesLimit: 3, TrafficLimit: 100 << 30}
	require.NoError(t, svc.CreatePlan(ctx, plan))

	node1 := &Node{Name: "nl-1", Host: "http://n1", Type: NodeType3xUI, IsActive: true, InboundIDs: `[1]`}
	node2 := &Node{Name: "nl-2", Host: "http://n2", Type: NodeType3xUI, IsActive: true, InboundIDs: `[1]`}
	node3 := &Node{Name: "nl-3", Host: "http://n3", Type: NodeType3xUI, IsActive: true, InboundIDs: `[1]`}
	require.NoError(t, svc.db.Create(node1).Error)
	require.NoError(t, svc.db.Create(node2).Error)
	require.NoError(t, svc.db.Create(node3).Error)

	now := time.Now().UTC()
	in6h := now.Add(6 * time.Hour)
	in30d := now.Add(30 * 24 * time.Hour)

	rows := []*Subscription{
		{
			TelegramID: 111000111, ClientID: "c-111000111", SubscriptionID: "s-111000111",
			Status: string(SubscriptionStatusActive), PlanID: plan.ID, ExpiresAt: &in6h,
		},
		{
			TelegramID: 222000222, ClientID: "c-222000222", SubscriptionID: "s-222000222",
			Status: string(SubscriptionStatusActive), PlanID: plan.ID, ExpiresAt: &in30d,
		},
		{
			TelegramID: 333000333, ClientID: "c-333000333", SubscriptionID: "s-333000333",
			Status: string(SubscriptionStatusRevoked), PlanID: plan.ID,
		},
		{
			TelegramID: -555, ClientID: "c-trial", SubscriptionID: "s-trial",
			Status: string(SubscriptionStatusActive), PlanID: plan.ID,
		},
	}

	for i := range rows {
		require.NoError(t, svc.db.Create(rows[i]).Error)
	}

	// Четвёртая подписка — trial-подобная без плана: FK требует существующий
	// план при вставке, поэтому NULL ставится отдельным UPDATE (FK на NULL
	// разрешён).
	require.NoError(t, svc.db.Model(&Subscription{}).
		Where("id = ?", rows[3].ID).Update("plan_id", nil).Error)

	// 2 active из 3 привязок для первой подписки: счётчик 2/3.
	bindings := []SubscriptionNode{
		{SubscriptionID: rows[0].ID, NodeID: node1.ID, Status: SyncStatusActive},
		{SubscriptionID: rows[0].ID, NodeID: node2.ID, Status: SyncStatusActive},
		{SubscriptionID: rows[0].ID, NodeID: node3.ID, Status: SyncStatusPendingRemove},
		{SubscriptionID: rows[1].ID, NodeID: node1.ID, Status: SyncStatusActive},
	}
	for i := range bindings {
		require.NoError(t, svc.db.Create(&bindings[i]).Error)
	}

	return rows
}

func TestListSubscriptionOverviews_NoFilters(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()
	rows := seedSubscriptionFixtures(t, svc)

	items, total, err := svc.ListSubscriptionOverviews(ctx, SubscriptionFilter{Desc: true, Limit: 50})
	require.NoError(t, err)

	assert.Equal(t, int64(len(rows)), total)
	require.Len(t, items, len(rows))

	// Default order: id desc.
	for i := 1; i < len(items); i++ {
		assert.Greater(t, items[i-1].ID, items[i].ID, "items must be sorted by id desc")
	}
}

func TestListSubscriptionOverviews_StatusFilter(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()
	seedSubscriptionFixtures(t, svc)

	items, total, err := svc.ListSubscriptionOverviews(ctx, SubscriptionFilter{
		Status: string(SubscriptionStatusActive),
		Limit:  50,
	})
	require.NoError(t, err)

	assert.Equal(t, int64(len(items)), total, "total and items share the same WHERE")
	for i := range items {
		assert.Equal(t, string(SubscriptionStatusActive), items[i].Status)
	}
}

func TestListSubscriptionOverviews_SearchByTelegramID(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()
	seedSubscriptionFixtures(t, svc)

	items, total, err := svc.ListSubscriptionOverviews(ctx, SubscriptionFilter{Query: "222000222", Limit: 50})
	require.NoError(t, err)

	assert.Equal(t, int64(1), total)
	require.Len(t, items, 1)
	assert.Equal(t, int64(222000222), items[0].TelegramID)
}

func TestListSubscriptionOverviews_SearchByUsernamePartial(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()
	rows := seedSubscriptionFixtures(t, svc)

	require.NoError(t, svc.db.Model(&Subscription{}).
		Where("id = ?", rows[0].ID).Update("username", "kereal").Error)

	items, total, err := svc.ListSubscriptionOverviews(ctx, SubscriptionFilter{Query: "kere", Limit: 50})
	require.NoError(t, err)

	assert.Equal(t, int64(1), total)
	require.Len(t, items, 1)
	assert.Equal(t, "kereal", items[0].Username)
}

func TestListSubscriptionOverviews_SearchBySubscriptionIDExact(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()
	seedSubscriptionFixtures(t, svc)

	items, total, err := svc.ListSubscriptionOverviews(ctx, SubscriptionFilter{Query: "s-222000222", Limit: 50})
	require.NoError(t, err)

	assert.Equal(t, int64(1), total)
	require.Len(t, items, 1)
	assert.Equal(t, "s-222000222", items[0].SubscriptionID)
}

func TestListSubscriptionOverviews_ExpiringIn(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()
	rows := seedSubscriptionFixtures(t, svc)

	items, total, err := svc.ListSubscriptionOverviews(ctx, SubscriptionFilter{
		ExpiringIn: 24 * time.Hour,
		Limit:      50,
	})
	require.NoError(t, err)

	// Только подписка с expires_at в ближайшие сутки: срок в 30 дней и
	// подписки без expires_at не попадают.
	assert.Equal(t, int64(1), total)
	require.Len(t, items, 1)
	require.NotNil(t, items[0].ExpiresAt)
	assert.Equal(t, rows[0].ID, items[0].ID)
}

func TestListSubscriptionOverviews_Pagination(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()
	seedSubscriptionFixtures(t, svc)

	page1, total, err := svc.ListSubscriptionOverviews(ctx, SubscriptionFilter{Limit: 2, Offset: 0, Desc: true})
	require.NoError(t, err)
	page2, total2, err := svc.ListSubscriptionOverviews(ctx, SubscriptionFilter{Limit: 2, Offset: 2, Desc: true})
	require.NoError(t, err)

	assert.Equal(t, total, total2, "total must not depend on the page")
	require.Len(t, page1, 2)
	require.Len(t, page2, 2)

	assertNoPageOverlap(t, func(row SubscriptionOverview) uint { return row.ID }, page1, page2)
}

func TestListSubscriptionOverviews_PlanIDNull(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()
	seedSubscriptionFixtures(t, svc)

	items, total, err := svc.ListSubscriptionOverviews(ctx, SubscriptionFilter{Query: "s-trial", Limit: 50})
	require.NoError(t, err)

	assert.Equal(t, int64(1), total)
	require.Len(t, items, 1)
	assert.Empty(t, items[0].PlanName, "plan_id NULL must yield an empty plan_name")
	assert.Nil(t, items[0].PlanID)
}

func TestListSubscriptionOverviews_NodeCounters(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()
	rows := seedSubscriptionFixtures(t, svc)

	// Строки с привязками: одна active+pending_remove (2/3), одна active (1/1),
	// без привязок — 0/0 без паники.
	items, _, err := svc.ListSubscriptionOverviews(ctx, SubscriptionFilter{Limit: 50, Desc: true})
	require.NoError(t, err)
	require.Len(t, items, 4)

	byID := make(map[uint]SubscriptionOverview, len(items))
	for i := range items {
		byID[items[i].ID] = items[i]
	}

	withBindings := byID[rows[0].ID]
	assert.Equal(t, int64(2), withBindings.NodesActive,
		"pending_remove must not count as active")
	assert.Equal(t, int64(3), withBindings.NodesTotal)

	oneNode := byID[rows[1].ID]
	assert.Equal(t, int64(1), oneNode.NodesActive)
	assert.Equal(t, int64(1), oneNode.NodesTotal)

	noNodes := byID[rows[2].ID]
	assert.Zero(t, noNodes.NodesActive)
	assert.Zero(t, noNodes.NodesTotal)
}

func TestListSubscriptionOverviews_SortByExpiresAt(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	plan := &Plan{Name: "sort-expires-plan", IsActive: true, DevicesLimit: 1, TrafficLimit: 1024}
	require.NoError(t, svc.CreatePlan(ctx, plan))

	// Известные expires_at; строки вставляются не в порядке сортировки,
	// чтобы случайное совпадение с ORDER BY id не маскировало сортировку.
	d1 := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	d2 := d1.Add(24 * time.Hour)
	d3 := d1.Add(48 * time.Hour)

	// Порядок вставки: d2, d1, d3, NULL — id растут иначе, чем expires_at ASC.
	rows := []*Subscription{
		{TelegramID: 411001, ClientID: "c-sort-2d", SubscriptionID: "s-sort-2d", Status: string(SubscriptionStatusActive), PlanID: plan.ID, ExpiresAt: &d2},
		{TelegramID: 411002, ClientID: "c-sort-1d", SubscriptionID: "s-sort-1d", Status: string(SubscriptionStatusActive), PlanID: plan.ID, ExpiresAt: &d1},
		{TelegramID: 411003, ClientID: "c-sort-3d", SubscriptionID: "s-sort-3d", Status: string(SubscriptionStatusActive), PlanID: plan.ID, ExpiresAt: &d3},
		{TelegramID: 411004, ClientID: "c-sort-null", SubscriptionID: "s-sort-null", Status: string(SubscriptionStatusRevoked), PlanID: plan.ID},
	}
	for _, row := range rows {
		require.NoError(t, svc.db.Create(row).Error)
	}

	wantIDsByExpiry := map[time.Time]uint{
		d1: rows[1].ID,
		d2: rows[0].ID,
		d3: rows[2].ID,
	}

	list := func(desc bool) []SubscriptionOverview {
		t.Helper()
		items, total, err := svc.ListSubscriptionOverviews(ctx, SubscriptionFilter{Sort: "expires_at", Desc: desc, Limit: 50})
		require.NoError(t, err)
		require.Equal(t, int64(4), total)
		require.Len(t, items, 4)
		return items
	}

	// ASC: в SQLite NULL первыми, затем по возрастанию expires_at.
	asc := list(false)
	assert.Nil(t, asc[0].ExpiresAt, "asc: the NULL row must come first in SQLite ordering")
	assert.Equal(t, []uint{wantIDsByExpiry[d1], wantIDsByExpiry[d2], wantIDsByExpiry[d3]},
		[]uint{asc[1].ID, asc[2].ID, asc[3].ID}, "asc: non-NULL rows must follow in ascending order")

	// DESC: по убыванию expires_at, NULL последним.
	desc := list(true)
	assert.Equal(t, []uint{wantIDsByExpiry[d3], wantIDsByExpiry[d2], wantIDsByExpiry[d1]},
		[]uint{desc[0].ID, desc[1].ID, desc[2].ID}, "desc: non-NULL rows must follow in descending order")
	assert.Nil(t, desc[3].ExpiresAt, "desc: the NULL row must come last")
}

func TestListSubscriptionOverviews_SingleSQLPerPhase(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()
	seedSubscriptionFixtures(t, svc)

	// 5000 строк: объём данных не должен менять число SQL (N+1 запрещён).
	freePlanID := testFreePlanID(t, svc)
	bulk := make([]Subscription, 0, 5000)
	for i := range 5000 {
		bulk = append(bulk, Subscription{
			TelegramID:     int64(1000000 + i),
			ClientID:       fmt.Sprintf("c-bulk-%d", i),
			SubscriptionID: fmt.Sprintf("s-bulk-%d", i),
			Status:         string(SubscriptionStatusActive),
			PlanID:         freePlanID,
		})
	}
	require.NoError(t, svc.db.CreateInBatches(bulk, 500).Error)

	counter := &queryCountLogger{queries: map[string]int{}}
	previous := svc.GetDB().Logger
	svc.GetDB().Logger = counter
	t.Cleanup(func() {
		svc.GetDB().Logger = previous
	})

	_, total, err := svc.ListSubscriptionOverviews(ctx, SubscriptionFilter{Limit: 50, Desc: true})
	require.NoError(t, err)
	assert.Greater(t, total, int64(5000))

	// Ровно два SELECT со словом subscriptions: COUNT + выборка items.
	// Инициализация/миграции и пр. — до reset() и не считаются.
	assert.Equal(t, 2, counter.count("subscriptions"),
		"expected 2 SQL queries (count + items), got: %s", counter.summary())
	// Оба содержат подзапрос узлов.
	assert.Equal(t, 2, counter.count("subscription_nodes"))
}

func TestGetSubscriptionDetail_Full(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()
	rows := seedSubscriptionFixtures(t, svc)

	var node2 Node
	require.NoError(t, svc.db.Where("name = ?", "nl-2").First(&node2).Error)

	sub := rows[0]
	require.NoError(t, svc.db.Model(&SubscriptionNode{}).
		Where("subscription_id = ? AND node_id = ?", sub.ID, node2.ID).
		Update("last_error", "panel timeout").Error)

	row, nodes, err := svc.GetSubscriptionDetail(ctx, sub.ID)
	require.NoError(t, err)

	assert.Equal(t, sub.ID, row.ID)
	assert.Equal(t, "s-111000111", row.SubscriptionID)
	assert.Equal(t, "premium", row.PlanName)
	assert.Equal(t, int64(2), row.NodesActive)
	assert.Equal(t, int64(3), row.NodesTotal)

	require.Len(t, nodes, 3)
	byNode := make(map[uint]SubscriptionNodeView, len(nodes))
	for i := range nodes {
		byNode[nodes[i].NodeID] = nodes[i]
	}

	view, ok := byNode[node2.ID]
	require.True(t, ok, "seeded node must be present in the detail")
	assert.Equal(t, "nl-2", view.NodeName)
	assert.Equal(t, string(SyncStatusActive), view.Status)
	require.NotNil(t, view.LastError)
	assert.Equal(t, "panel timeout", *view.LastError)

	var node3 Node
	require.NoError(t, svc.db.Where("name = ?", "nl-3").First(&node3).Error)

	// Привязка с pending_remove отдаёт свой статус и имя узла.
	pending := byNode[node3.ID]
	assert.Equal(t, string(SyncStatusPendingRemove), pending.Status)
	assert.Equal(t, "nl-3", pending.NodeName)
}

func TestGetSubscriptionDetail_NotFound(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx := context.Background()

	_, _, err := svc.GetSubscriptionDetail(ctx, 99999)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrSubscriptionNotFound)
}

// queryCountLogger считает SQL-запросы, проходящие через GORM logger. Это
// приёмочный тест против N+1: список обязан оставаться двумя запросами.
type queryCountLogger struct {
	queries map[string]int
}

func (l *queryCountLogger) LogMode(level logger.LogLevel) logger.Interface { return l }

func (l *queryCountLogger) Info(_ context.Context, _ string, _ ...any)  {}
func (l *queryCountLogger) Warn(_ context.Context, _ string, _ ...any)  {}
func (l *queryCountLogger) Error(_ context.Context, _ string, _ ...any) {}

func (l *queryCountLogger) Trace(_ context.Context, _ time.Time, fc func() (string, int64), _ error) {
	sql, _ := fc()
	if strings.Contains(sql, "subscription_nodes") {
		l.queries["subscription_nodes"]++
	}
	// Считаем запросы к subscriptions: и обратные кавычки (SQLite в GORM по
	// умолчанию), и двойные кавычки (если драйвер цитирует иначе).
	if strings.Contains(sql, "`subscriptions`") || strings.Contains(sql, `"subscriptions"`) {
		l.queries["subscriptions"]++
	}
}

func (l *queryCountLogger) count(table string) int {
	return l.queries[table]
}

func (l *queryCountLogger) summary() string {
	return fmt.Sprintf("%+v", l.queries)
}
