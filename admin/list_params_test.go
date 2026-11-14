package admin

import (
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Тесты общего контракта limit/offset/sort/order (P7/P8): единый парсер —
// единое поведение для всех списков панели.

var listParamsSortColumns = map[string]bool{"id": true, "created_at": true}

func TestParseListParams(t *testing.T) {
	t.Parallel()

	t.Run("defaults", func(t *testing.T) {
		t.Parallel()

		r := httptest.NewRequest("GET", "/admin/api/x", nil)
		p, ok := parseListParams(httptest.NewRecorder(), r, 50, 200, listParamsSortColumns)
		require.True(t, ok)
		assert.Equal(t, listParams{Limit: 50, Desc: true}, p)
	})

	t.Run("clamps oversized limit", func(t *testing.T) {
		t.Parallel()

		r := httptest.NewRequest("GET", "/admin/api/x?limit=99999&offset=7&sort=id&order=asc", nil)
		p, ok := parseListParams(httptest.NewRecorder(), r, 50, 200, listParamsSortColumns)
		require.True(t, ok)
		assert.Equal(t, listParams{Limit: 200, Offset: 7, Sort: "id"}, p)
	})

	t.Run("rejects malformed values", func(t *testing.T) {
		t.Parallel()

		for _, query := range []string{"limit=0", "limit=-1", "limit=abc", "offset=-1", "offset=abc", "sort=evil", "order=sideways"} {
			r := httptest.NewRequest("GET", "/admin/api/x?"+query, nil)
			_, ok := parseListParams(httptest.NewRecorder(), r, 50, 200, listParamsSortColumns)
			assert.False(t, ok, query)
		}
	})
}
