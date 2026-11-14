package database

import (
	"context"
	"fmt"
	"os"
	"time"

	"gorm.io/gorm"
)

// OverviewSnapshot is the aggregate set behind the panel's "state" screen.
// Every number is a COUNT over a column that already exists: the snapshot adds
// no bookkeeping and no new table, so it can never drift from the domain data.
type OverviewSnapshot struct {
	SubscriptionsByStatus map[string]int64
	OrdersByStatus        map[string]int64
	BroadcastsByStatus    map[string]int64
	// BindingsByStatus counts subscription↔node pairs by sync status. Non-zero
	// pending_* rows are the queue SyncPendingNodes drains.
	BindingsByStatus map[string]int64
	NodesTotal       int64
	// OldestPendingBinding is when the oldest queued binding was last touched.
	// A fresh pending row is normal; one several days old means the worker is
	// stuck (that is exactly the state that blocks subscription deletion).
	OldestPendingBinding *time.Time
	// LastSubRequest is the newest /sub/:id hit. It tells whether the
	// subscription server receives traffic at all.
	LastSubRequest *time.Time
	// Expiring counts active subscriptions whose expires_at falls in the next
	// 30 days, bucketed per day for the state screen mini-chart.
	Expiring           ExpiringBuckets
	AuditEventsLast24h int64
	DatabasePath       string
	DatabaseSizeBytes  int64
}

// ExpiringDay — число активных подписок, истекающих в данный день. Дата —
// строка UTC "YYYY-MM-DD": панель рисует гистограмму по ней без обработки.
type ExpiringDay struct {
	Date  string
	Count int64
}

// ExpiringBuckets — сводка истечения для экрана «Состояние»: total за окно и
// по дню на каждый день от сегодня до +30 включительно (нулевые дни есть).
type ExpiringBuckets struct {
	Total int64
	Days  []ExpiringDay
}

// expiringWindowDays — глубина окна истечения на «Состоянии».
const expiringWindowDays = 30

// SubscriptionsTotal sums the closed status set: a subscription always has one.
func (o *OverviewSnapshot) SubscriptionsTotal() int64 {
	var total int64
	for _, count := range o.SubscriptionsByStatus {
		total += count
	}

	return total
}

// PendingBindingsTotal sums pending_add/pending_update/pending_remove.
func (o *OverviewSnapshot) PendingBindingsTotal() int64 {
	var total int64
	for status, count := range o.BindingsByStatus {
		if status != string(SyncStatusActive) {
			total += count
		}
	}

	return total
}

// GetOverviewSnapshot reads the aggregates for the admin state screen in a
// handful of GROUP BY queries. The database file size comes from the file
// system; a failure there is not an error (in-memory test databases have no
// file), it just leaves the size zero.
func (s *Service) GetOverviewSnapshot(ctx context.Context) (*OverviewSnapshot, error) {
	db := s.db.WithContext(ctx)

	subs, err := countByColumn(db, &Subscription{}, "status")
	if err != nil {
		return nil, err
	}

	orders, err := countByColumn(db, &Order{}, "status")
	if err != nil {
		return nil, err
	}

	broadcasts, err := countByColumn(db, &Broadcast{}, "status")
	if err != nil {
		return nil, err
	}

	bindings, err := countByColumn(db, &SubscriptionNode{}, "status")
	if err != nil {
		return nil, err
	}

	var nodesTotal int64

	err = db.Model(&Node{}).Count(&nodesTotal).Error
	if err != nil {
		return nil, fmt.Errorf("count nodes: %w", err)
	}

	oldestPending, err := s.oldestPendingBinding(ctx)
	if err != nil {
		return nil, err
	}

	lastSubRequest, err := s.lastSubscriptionRequest(ctx)
	if err != nil {
		return nil, err
	}

	expiring, err := s.expiringBuckets(ctx)
	if err != nil {
		return nil, err
	}

	var auditEvents int64
	err = db.Model(&AuditLog{}).
		Where("created_at >= ?", time.Now().UTC().Add(-24*time.Hour)).
		Count(&auditEvents).Error
	if err != nil {
		return nil, fmt.Errorf("count recent audit events: %w", err)
	}

	snapshot := &OverviewSnapshot{
		SubscriptionsByStatus: subs,
		OrdersByStatus:        orders,
		BroadcastsByStatus:    broadcasts,
		BindingsByStatus:      bindings,
		NodesTotal:            nodesTotal,
		OldestPendingBinding:  oldestPending,
		LastSubRequest:        lastSubRequest,
		Expiring:              expiring,
		AuditEventsLast24h:    auditEvents,
		DatabasePath:          s.path,
	}

	if s.path != "" {
		info, statErr := os.Stat(s.path)
		if statErr == nil {
			snapshot.DatabaseSizeBytes = info.Size()
		}
	}

	return snapshot, nil
}

// expiringBuckets считает активные подписки с expires_at в ближайшие 30 дней
// и раскладывает их по дням UTC от сегодня до +30 включительно. Дни без
// истечений заполняются нулями в Go — SQL не порождает пустые дни (образец —
// GetRevenueStats). Верхняя граница — конец дня +30 (исключающая), чтобы
// подписка, истекающая в последний день окна, попала в его бакет.
func (s *Service) expiringBuckets(ctx context.Context) (ExpiringBuckets, error) {
	today := time.Now().UTC().Truncate(24 * time.Hour)
	from := today
	to := today.AddDate(0, 0, expiringWindowDays+1)

	var rows []struct {
		Day   string
		Count int64
	}

	err := s.db.WithContext(ctx).Raw(`
		SELECT date(expires_at) AS day, COUNT(*) AS count
		FROM subscriptions
		WHERE status = ? AND expires_at >= ? AND expires_at < ?
		GROUP BY date(expires_at)`,
		string(SubscriptionStatusActive), from, to).Scan(&rows).Error
	if err != nil {
		return ExpiringBuckets{}, fmt.Errorf("count expiring subscriptions: %w", err)
	}

	byDay := make(map[string]int64, len(rows))
	var total int64
	for _, row := range rows {
		byDay[row.Day] = row.Count
		total += row.Count
	}

	days := make([]ExpiringDay, 0, expiringWindowDays+1)
	for d := from; d.Before(to); d = d.AddDate(0, 0, 1) {
		days = append(days, ExpiringDay{Date: d.Format("2006-01-02"), Count: byDay[d.Format("2006-01-02")]})
	}

	return ExpiringBuckets{Total: total, Days: days}, nil
}

// countByColumn groups rows by one column. The column name is a package
// literal, never caller input.
func countByColumn(db *gorm.DB, model any, column string) (map[string]int64, error) {
	var rows []struct {
		Key   string
		Count int64
	}

	err := db.Model(model).
		Select(column + " AS key, COUNT(*) AS count").
		Group(column).
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("count by %s: %w", column, err)
	}

	counts := make(map[string]int64, len(rows))
	for i := range rows {
		counts[rows[i].Key] = rows[i].Count
	}

	return counts, nil
}

func (s *Service) oldestPendingBinding(ctx context.Context) (*time.Time, error) {
	pending := []SyncStatus{SyncStatusPendingAdd, SyncStatusPendingUpdate, SyncStatusPendingRemove}

	// Read the oldest row instead of MIN(updated_at): the SQLite driver hands
	// aggregates back as strings, and this way the value arrives typed.
	var row SubscriptionNode

	result := s.db.WithContext(ctx).
		Where("status IN ?", pending).
		Order("updated_at ASC").
		Limit(1).
		Find(&row)
	if result.Error != nil {
		return nil, fmt.Errorf("oldest pending binding: %w", result.Error)
	}

	if result.RowsAffected == 0 {
		return nil, nil
	}

	utc := row.UpdatedAt.UTC()

	return &utc, nil
}

func (s *Service) lastSubscriptionRequest(ctx context.Context) (*time.Time, error) {
	var row Subscription

	result := s.db.WithContext(ctx).
		Where("last_request IS NOT NULL").
		Order("last_request DESC").
		Limit(1).
		Find(&row)
	if result.Error != nil {
		return nil, fmt.Errorf("last subscription request: %w", result.Error)
	}

	if result.RowsAffected == 0 || row.LastRequest == nil {
		return nil, nil
	}

	utc := row.LastRequest.UTC()

	return &utc, nil
}
