package metrics

import (
	"context"
	"log"
	"time"

	"gorm.io/gorm"
)

// RegisterDBMetrics registers GORM callbacks to instrument database queries
// with db_queries_total and db_query_duration_seconds metrics.
func RegisterDBMetrics(db *gorm.DB) {
	startTimeKey := "metrics:start_time"

	if err := db.Callback().Create().Before("gorm:create").Register("metrics:before_create", func(tx *gorm.DB) {
		tx.Statement.Settings.Store(startTimeKey, time.Now())
	}); err != nil {
		log.Printf("register DB create-before metrics callback: %v", err)
	}
	if err := db.Callback().Create().After("gorm:create").Register("metrics:after_create", func(tx *gorm.DB) {
		recordDBMetric(tx, startTimeKey, "create")
	}); err != nil {
		log.Printf("register DB create-after metrics callback: %v", err)
	}

	if err := db.Callback().Query().Before("gorm:query").Register("metrics:before_query", func(tx *gorm.DB) {
		tx.Statement.Settings.Store(startTimeKey, time.Now())
	}); err != nil {
		log.Printf("register DB query-before metrics callback: %v", err)
	}
	if err := db.Callback().Query().After("gorm:query").Register("metrics:after_query", func(tx *gorm.DB) {
		recordDBMetric(tx, startTimeKey, "query")
	}); err != nil {
		log.Printf("register DB query-after metrics callback: %v", err)
	}

	if err := db.Callback().Update().Before("gorm:update").Register("metrics:before_update", func(tx *gorm.DB) {
		tx.Statement.Settings.Store(startTimeKey, time.Now())
	}); err != nil {
		log.Printf("register DB update-before metrics callback: %v", err)
	}
	if err := db.Callback().Update().After("gorm:update").Register("metrics:after_update", func(tx *gorm.DB) {
		recordDBMetric(tx, startTimeKey, "update")
	}); err != nil {
		log.Printf("register DB update-after metrics callback: %v", err)
	}

	if err := db.Callback().Delete().Before("gorm:delete").Register("metrics:before_delete", func(tx *gorm.DB) {
		tx.Statement.Settings.Store(startTimeKey, time.Now())
	}); err != nil {
		log.Printf("register DB delete-before metrics callback: %v", err)
	}
	if err := db.Callback().Delete().After("gorm:delete").Register("metrics:after_delete", func(tx *gorm.DB) {
		recordDBMetric(tx, startTimeKey, "delete")
	}); err != nil {
		log.Printf("register DB delete-after metrics callback: %v", err)
	}
}

func recordDBMetric(tx *gorm.DB, startTimeKey, operation string) {
	startValue, ok := tx.Statement.Settings.Load(startTimeKey)
	if !ok {
		return
	}
	start, ok := startValue.(time.Time)
	if !ok {
		return
	}
	duration := time.Since(start).Seconds()
	result := "success"
	if tx.Error != nil {
		result = "error"
	}
	DBQueryDuration.WithLabelValues(operation).Observe(duration)
	DBQueriesTotal.WithLabelValues(operation, result).Inc()
}

// CollectDBPoolMetrics collects database connection pool statistics.
func CollectDBPoolMetrics(ctx context.Context, db *gorm.DB) {
	sqlDB, err := db.DB()
	if err != nil {
		return
	}

	stats := sqlDB.Stats()
	DBPoolOpen.Set(float64(stats.OpenConnections))
	DBPoolInUse.Set(float64(stats.InUse))
	DBPoolIdle.Set(float64(stats.Idle))
	DBPoolWait.Set(float64(stats.WaitCount))
}
