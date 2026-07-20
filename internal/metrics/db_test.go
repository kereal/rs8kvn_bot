package metrics

import (
	"context"
	"testing"

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
