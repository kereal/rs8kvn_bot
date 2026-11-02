package metrics

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestRegisterDBMetrics_Create(t *testing.T) {
	db := openTestDB(t)
	defer closeDB(t, db)

	RegisterDBMetrics(db)

	type TestModel struct {
		ID  uint
		Val string
	}

	require.NoError(t, db.AutoMigrate(&TestModel{}))
	require.NoError(t, db.WithContext(context.Background()).Create(&TestModel{Val: "a"}).Error)
}

func TestRegisterDBMetrics_Query(t *testing.T) {
	db := openTestDB(t)
	defer closeDB(t, db)

	RegisterDBMetrics(db)

	type TestModel struct {
		ID  uint
		Val string
	}

	require.NoError(t, db.AutoMigrate(&TestModel{}))
	require.NoError(t, db.Create(&TestModel{Val: "a"}).Error)

	var results []TestModel
	require.NoError(t, db.WithContext(context.Background()).Find(&results).Error)
}

func TestRegisterDBMetrics_Update(t *testing.T) {
	db := openTestDB(t)
	defer closeDB(t, db)

	RegisterDBMetrics(db)

	type TestModel struct {
		ID  uint
		Val string
	}

	require.NoError(t, db.AutoMigrate(&TestModel{}))
	require.NoError(t, db.Create(&TestModel{Val: "a"}).Error)

	require.NoError(t, db.WithContext(context.Background()).Model(&TestModel{}).Where("id = ?", 1).Update("val", "b").Error)
}

func TestRegisterDBMetrics_Delete(t *testing.T) {
	db := openTestDB(t)
	defer closeDB(t, db)

	RegisterDBMetrics(db)

	type TestModel struct {
		ID  uint
		Val string
	}

	require.NoError(t, db.AutoMigrate(&TestModel{}))
	require.NoError(t, db.Create(&TestModel{Val: "a"}).Error)

	require.NoError(t, db.WithContext(context.Background()).Delete(&TestModel{}, 1).Error)
}

// Тест поведения: метка table называет таблицу, по которой ошибся запрос —
// иначе разбор инцидента упирается в «query error» без адреса.
func TestRegisterDBMetrics_RecordsTableLabel(t *testing.T) {
	db := openTestDB(t)
	defer closeDB(t, db)

	RegisterDBMetrics(db)

	type LabelModel struct {
		ID  uint
		Val string
	}

	require.NoError(t, db.AutoMigrate(&LabelModel{}))

	before := testutil.ToFloat64(DBQueriesTotal.WithLabelValues("create", "success", "label_models"))
	require.NoError(t, db.WithContext(context.Background()).Create(&LabelModel{Val: "a"}).Error)
	after := testutil.ToFloat64(DBQueriesTotal.WithLabelValues("create", "success", "label_models"))

	if after <= before {
		t.Fatalf("ожидалась серия create/success/label_models, до=%v после=%v", before, after)
	}
}

func TestCollectDBPoolMetrics_ExportsWaitStats(t *testing.T) {
	db := openTestDB(t)
	defer closeDB(t, db)

	sqlDB, err := db.DB()
	require.NoError(t, err)

	waitsBefore := testutil.ToFloat64(DBPoolWaitsTotal)
	secondsBefore := testutil.ToFloat64(DBPoolWaitSecondsTotal)

	CollectDBPoolMetrics(context.Background(), db)
	CollectDBPoolMetrics(context.Background(), db)

	// Приращение, а не абсолют: повторный сбор не должен удваивать счётчик.
	assert.InDelta(t, sqlDB.Stats().WaitCount, testutil.ToFloat64(DBPoolWaitsTotal)-waitsBefore, 0.0001)
	assert.InDelta(t, sqlDB.Stats().WaitDuration.Seconds(),
		testutil.ToFloat64(DBPoolWaitSecondsTotal)-secondsBefore, 0.0001)
}

func openTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	require.NoError(t, err)

	return db
}

func closeDB(t *testing.T, db *gorm.DB) {
	t.Helper()

	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())
}

// Тест поведения: размеры файла БД и WAL публикуются в gauge — иначе рост БД
// виден только по диску.
func TestCollectDatabaseFileSize(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	require.NoError(t, os.WriteFile(dbPath, []byte("0123456789"), 0o600))
	require.NoError(t, os.WriteFile(dbPath+"-wal", []byte("wal"), 0o600))

	CollectDatabaseFileSize(dbPath)

	require.Equal(t, float64(10), testutil.ToFloat64(DBFileBytes))
	require.Equal(t, float64(3), testutil.ToFloat64(DBWALBytes))
}
