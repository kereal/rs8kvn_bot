package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/kereal/rs8kvn_bot/internal/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeNodeLiveSource — стаб живого опроса ноды: возвращают заготовленный
// снимок или ошибку, тело запроса не проверяют.
type fakeNodeLiveSource struct {
	info *service.NodeLiveInfo
	err  error
}

func (f *fakeNodeLiveSource) GetNodeLive(ctx context.Context, nodeID uint) (*service.NodeLiveInfo, error) {
	if f.err != nil {
		return nil, f.err
	}

	info := *f.info
	info.NodeID = nodeID

	return &info, nil
}

func TestNodesLive_503WithoutSource(t *testing.T) {
	t.Parallel()

	srv, _ := newTestServer(t)

	// Без SetNodeLiveSource эндпоинт отвечает 503, а не падает.
	rec := do(t, srv, http.MethodGet, "/admin/api/nodes/1/live", testToken, "")
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)

	var resp struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, "internal", resp.Code)
}

func TestNodesLive_404UnknownNode(t *testing.T) {
	t.Parallel()

	srv, _ := newTestServer(t)
	srv.SetNodeLiveSource(&fakeNodeLiveSource{
		info: &service.NodeLiveInfo{Available: true, Clients: 3, TrafficBytes: 1024},
	})

	rec := do(t, srv, http.MethodGet, "/admin/api/nodes/424242/live", testToken, "")
	require.Equal(t, http.StatusNotFound, rec.Code)

	var resp struct {
		Code string `json:"code"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, "not_found", resp.Code)
}

func TestNodesLive_ResponseShape(t *testing.T) {
	t.Parallel()

	srv, _ := newTestServer(t)

	id, _ := createNodeViaAPI(t, srv, nodePayloadBody)
	srv.SetNodeLiveSource(&fakeNodeLiveSource{
		info: &service.NodeLiveInfo{
			Available:     true,
			PingLatencyMS: 42,
			Clients:       3,
			Disabled:      1,
			TrafficBytes:  5 * 1024 * 1024 * 1024,
			LastOnline:    1700000000000,
		},
	})

	rec := do(t, srv, http.MethodGet, fmt.Sprintf("/admin/api/nodes/%d/live", id), testToken, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var result struct {
		NodeID        uint   `json:"node_id"`
		Available     bool   `json:"available"`
		PingLatencyMS int64  `json:"ping_latency_ms"`
		PingError     string `json:"ping_error"`
		Clients       int    `json:"clients"`
		Disabled      int    `json:"disabled"`
		TrafficBytes  int64  `json:"traffic_bytes"`
		LastOnline    int64  `json:"last_online"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &result))
	assert.Equal(t, uint(id), result.NodeID)
	assert.True(t, result.Available)
	assert.Equal(t, int64(42), result.PingLatencyMS)
	assert.Equal(t, 3, result.Clients)
	assert.Equal(t, 1, result.Disabled)
	assert.Equal(t, int64(5*1024*1024*1024), result.TrafficBytes)
	assert.Equal(t, int64(1700000000000), result.LastOnline)
}

func TestNodesLive_UnavailablePanelIsData(t *testing.T) {
	t.Parallel()

	srv, _ := newTestServer(t)

	id, _ := createNodeViaAPI(t, srv, nodePayloadBody)
	srv.SetNodeLiveSource(&fakeNodeLiveSource{
		info: &service.NodeLiveInfo{Available: false, PingError: "connection refused"},
	})

	rec := do(t, srv, http.MethodGet, fmt.Sprintf("/admin/api/nodes/%d/live", id), testToken, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var result struct {
		Available bool   `json:"available"`
		PingError string `json:"ping_error"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &result))
	assert.False(t, result.Available)
	assert.Equal(t, "connection refused", result.PingError)
}
