package subserver

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFetchFromNode_ResponseSizeLimit(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", config.MaxResponseSize+1)))
	}))
	defer srv.Close()

	_, err := FetchFromNode(context.Background(), srv.URL)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds")
}

func TestAggregateFormat_RawBase64(t *testing.T) {
	t.Parallel()

	plain := "vless://one@example.com:443\ntrojan://pass@example.com:443"
	encoded := base64.RawStdEncoding.EncodeToString([]byte(plain))
	var agg aggregatedSources

	aggregateFormat(&agg, FormatBase64, []byte(encoded), database.Node{Name: "raw-base64"}, "sub-raw")

	assert.Equal(t, []string{"vless://one@example.com:443", "trojan://pass@example.com:443"}, agg.items)
}
