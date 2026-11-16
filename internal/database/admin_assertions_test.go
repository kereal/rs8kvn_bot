package database

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// assertNoPageOverlap проверяет, что страницы листинга не пересекаются.
// Общий helper для экранов панели (заказы, подписки, рассылки, аудит):
// без production-абстракций, только повторяющийся тестовый обход страниц.
func assertNoPageOverlap[T any](t *testing.T, id func(T) uint, pages ...[]T) {
	t.Helper()

	seen := make(map[uint]bool)
	for _, page := range pages {
		for _, row := range page {
			rowID := id(row)
			require.False(t, seen[rowID], "pages must not overlap: id %d repeats", rowID)
			seen[rowID] = true
		}
	}
}
