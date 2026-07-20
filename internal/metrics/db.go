package metrics

import (
	"context"
	"time"

	"gorm.io/gorm"
)

// RegisterDBMetrics registers GORM callbacks to instrument database queries
// with db_queries_total and db_query_duration_seconds metrics.
func RegisterDBMetrics(db *gorm.DB) {
	startTimeKey := "metrics:start_time"

	db.Callback().Create().Before("gorm:create").Register("metrics:before_create", func(tx *gorm.DB) {
		tx.Statement.Settings.Store(startTimeKey, time.Now())
	})
	db.Callback().Create().After("gorm:create").Register("metrics:after_create", func(tx *gorm.DB) {
		recordDBMetric(tx, startTimeKey, "create")
	})

	db.Callback().Query().Before("gorm:query").Register("metrics:before_query", func(tx *gorm.DB) {
		tx.Statement.Settings.Store(startTimeKey, time.Now())
	})
	db.Callback().Query().After("gorm:query").Register("metrics:after_query", func(tx *gorm.DB) {
		recordDBMetric(tx, startTimeKey, "query")
	})

	db.Callback().Update().Before("gorm:update").Register("metrics:before_update", func(tx *gorm.DB) {
		tx.Statement.Settings.Store(startTimeKey, time.Now())
	})
	db.Callback().Update().After("gorm:update").Register("metrics:after_update", func(tx *gorm.DB) {
		recordDBMetric(tx, startTimeKey, "update")
	})

	db.Callback().Delete().Before("gorm:delete").Register("metrics:before_delete", func(tx *gorm.DB) {
		tx.Statement.Settings.Store(startTimeKey, time.Now())
	})
	db.Callback().Delete().After("gorm:delete").Register("metrics:after_delete", func(tx *gorm.DB) {
		recordDBMetric(tx, startTimeKey, "delete")
	})
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
