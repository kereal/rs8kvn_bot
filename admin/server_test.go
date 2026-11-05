package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testToken = "test-admin-token"

// TestMain initialises the logger once: logger.Init mutates package state, so
// calling it from parallel subtests is a data race.
func TestMain(m *testing.M) {
	err := testutil.InitLogger(nil)
	if err != nil {
		panic(err)
	}

	os.Exit(m.Run())
}

func newTestServer(t *testing.T) (*Server, *database.Service) {
	t.Helper()

	db, err := testutil.NewTestDatabaseService(t)
	require.NoError(t, err)

	t.Cleanup(func() {
		err := db.Close()
		if err != nil {
			t.Logf("close db: %v", err)
		}
	})

	return NewServer("127.0.0.1:0", testToken, db), db
}

func do(t *testing.T, srv *Server, method, path, token, body string) *httptest.ResponseRecorder {
	t.Helper()

	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}

	req := httptest.NewRequest(method, path, reader)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	return rec
}

func TestAPI_RequiresToken(t *testing.T) {
	t.Parallel()

	srv, _ := newTestServer(t)

	for _, token := range []string{"", "wrong"} {
		rec := do(t, srv, http.MethodGet, "/admin/api/plans", token, "")
		assert.Equal(t, http.StatusUnauthorized, rec.Code, "token=%q", token)
	}
}

func TestAPI_SessionAcceptsValidToken(t *testing.T) {
	t.Parallel()

	srv, _ := newTestServer(t)

	rec := do(t, srv, http.MethodGet, "/admin/api/session", testToken, "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"ok":true`)
}

func TestAPI_PlansListIncludesSeededPlans(t *testing.T) {
	t.Parallel()

	srv, _ := newTestServer(t)

	rec := do(t, srv, http.MethodGet, "/admin/api/plans", testToken, "")
	require.Equal(t, http.StatusOK, rec.Code)

	var payload struct {
		Items []planItem `json:"items"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	require.Len(t, payload.Items, 2, "trial and free plans are seeded")

	assert.Equal(t, database.TrialPlanName, payload.Items[0].Name)
	assert.Equal(t, database.FreePlanName, payload.Items[1].Name)
}

func TestAPI_PlanLifecycle(t *testing.T) {
	t.Parallel()

	srv, _ := newTestServer(t)

	create := do(t, srv, http.MethodPost, "/admin/api/plans", testToken,
		`{"name":"  monthly  ","devices_limit":3,"traffic_limit":107374182400,"is_active":true}`)
	require.Equal(t, http.StatusCreated, create.Code, create.Body.String())

	var created struct {
		ID uint `json:"id"`
	}
	require.NoError(t, json.Unmarshal(create.Body.Bytes(), &created))
	require.NotZero(t, created.ID)

	update := do(t, srv, http.MethodPut, "/admin/api/plans/"+strconv.FormatUint(uint64(created.ID), 10), testToken,
		`{"name":"monthly","devices_limit":5,"traffic_limit":0,"is_active":false}`)
	require.Equal(t, http.StatusOK, update.Code, update.Body.String())

	list := do(t, srv, http.MethodGet, "/admin/api/plans", testToken, "")
	require.Equal(t, http.StatusOK, list.Code)

	var payload struct {
		Items []planItem `json:"items"`
	}
	require.NoError(t, json.Unmarshal(list.Body.Bytes(), &payload))

	var found *planItem
	for i := range payload.Items {
		if payload.Items[i].ID == created.ID {
			found = &payload.Items[i]
		}
	}
	require.NotNil(t, found)
	assert.Equal(t, "monthly", found.Name)
	assert.Equal(t, 5, found.DevicesLimit)
	assert.Equal(t, int64(0), found.TrafficLimit)
	assert.False(t, found.IsActive)

	del := do(t, srv, http.MethodDelete, "/admin/api/plans/"+strconv.FormatUint(uint64(created.ID), 10), testToken, "")
	require.Equal(t, http.StatusOK, del.Code, del.Body.String())

	missing := do(t, srv, http.MethodDelete, "/admin/api/plans/"+strconv.FormatUint(uint64(created.ID), 10), testToken, "")
	assert.Equal(t, http.StatusNotFound, missing.Code)
}

func TestAPI_PlanGuardsMapToConflict(t *testing.T) {
	t.Parallel()

	srv, db := newTestServer(t)
	ctx := context.Background()

	duplicate := do(t, srv, http.MethodPost, "/admin/api/plans", testToken,
		`{"name":"free","devices_limit":1,"traffic_limit":0,"is_active":true}`)
	require.Equal(t, http.StatusConflict, duplicate.Code)
	assert.Contains(t, duplicate.Body.String(), `"name_taken"`)

	trial, err := db.GetPlanByName(ctx, database.TrialPlanName)
	require.NoError(t, err)

	rename := do(t, srv, http.MethodPut, "/admin/api/plans/"+strconv.FormatUint(uint64(trial.ID), 10), testToken,
		`{"name":"trial-2","devices_limit":1,"traffic_limit":0,"is_active":true}`)
	require.Equal(t, http.StatusConflict, rename.Code)
	assert.Contains(t, rename.Body.String(), `"protected"`)

	removeDefault := do(t, srv, http.MethodDelete, "/admin/api/plans/"+strconv.FormatUint(uint64(trial.ID), 10), testToken, "")
	require.Equal(t, http.StatusConflict, removeDefault.Code)
	assert.Contains(t, removeDefault.Body.String(), `"protected"`)
}

func TestAPI_PlanValidationAndBadIDs(t *testing.T) {
	t.Parallel()

	srv, _ := newTestServer(t)

	emptyName := do(t, srv, http.MethodPost, "/admin/api/plans", testToken,
		`{"name":"   ","devices_limit":1,"traffic_limit":0,"is_active":true}`)
	assert.Equal(t, http.StatusBadRequest, emptyName.Code)

	unknownField := do(t, srv, http.MethodPost, "/admin/api/plans", testToken,
		`{"name":"x","devices_limit":1,"traffic_limit":0,"is_active":true,"extra":1}`)
	assert.Equal(t, http.StatusBadRequest, unknownField.Code)

	badID := do(t, srv, http.MethodDelete, "/admin/api/plans/not-a-number", testToken, "")
	assert.Equal(t, http.StatusBadRequest, badID.Code)

	zeroID := do(t, srv, http.MethodPut, "/admin/api/plans/0", testToken,
		`{"name":"x","devices_limit":1,"traffic_limit":0,"is_active":true}`)
	assert.Equal(t, http.StatusBadRequest, zeroID.Code)
}

func TestSPA_ServesIndexAndFallsBackForDeepLinks(t *testing.T) {
	t.Parallel()

	srv, _ := newTestServer(t)

	for _, path := range []string{"/admin/", "/admin/plans", "/admin/does-not-exist"} {
		rec := do(t, srv, http.MethodGet, path, "", "")
		require.Equal(t, http.StatusOK, rec.Code, "path=%s", path)
		assert.Contains(t, rec.Body.String(), "<div id=\"app\">", "path=%s", path)
		assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
	}

	// A traversal attempt must not reach Go sources outside the embedded tree.
	for _, path := range []string{"/admin/../server.go", "/admin/%2e%2e/server.go", "/admin/assets/../../server.go"} {
		traversal := do(t, srv, http.MethodGet, path, "", "")
		assert.NotContains(t, traversal.Body.String(), "package admin", "path=%s", path)
	}

	// ServeFileFS canonicalises an explicit index.html to the directory URL.
	index := do(t, srv, http.MethodGet, "/admin/index.html", "", "")
	assert.Equal(t, http.StatusMovedPermanently, index.Code)
	assert.Equal(t, "./", index.Header().Get("Location"))

	rec := do(t, srv, http.MethodPost, "/admin/", "", "")
	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}

func TestStart_RequiresToken(t *testing.T) {
	t.Parallel()

	srv := NewServer("127.0.0.1:0", "", nil)
	require.Error(t, srv.Start(context.Background()))
}
