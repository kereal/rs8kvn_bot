package subserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/testutil"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// Live-shaped data: reproduces subscription 9c0d8a3ee6313b — a Hysteria2 node
// with SALAMANDER obfuscation and a VLESS Reality node, both carrying emoji
// remarks. The sing-box and Clash renderers must preserve every node, its
// readable name and its credentials.
const (
	liveHy2Link   = "hysteria2://3662e46033d94414a3749b0cf2e2edd6@st01.kereal.qzz.io:443?sni=st01.kereal.qzz.io&fp=firefox&alpn=h3&obfs=salamander&obfs-password=qv24m6vhniw2pg2m#%F0%9F%87%AB%F0%9F%87%AE%20Finland%20Hysteria%20%F0%9F%94%A5"
	liveVLESSLink = "vless://69b28568-1edb-3ee6-0d8a-0d8a3ee62034@st01.kereal.qzz.io:443?security=reality&pbk=oTWaJuUIEQQOvBErcJEL4lzj6jf9wdGnDB2ocEkeA0c&sid=d27e335d2b1679&sni=www.github.com&fp=firefox#🇫🇮%20Finland%20vless%20🎈"

	liveHy2Tag  = "🇫🇮 Finland Hysteria 🔥"
	liveVLESSTag = "🇫🇮 Finland vless 🎈"
)

// newMultiNodeSub spins a backend serving the two live-shaped share links and
// mocks the DB for the given subscription id.
func newMultiNodeSub(t *testing.T, subID string) (*Service, *testutil.DatabaseService) {
	t.Helper()

	body := liveHy2Link + "\n" + liveVLESSLink
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(backend.Close)

	mockDB := testutil.NewDatabaseService()
	mockDB.GetWithPlanAndNodesFunc = func(ctx context.Context, id string) (*database.SubscriptionFull, error) {
		return &database.SubscriptionFull{
			Subscription: database.Subscription{ID: 1, SubscriptionID: id, Status: "active"},
			Plan:         database.Plan{ID: 1},
			Nodes:        []database.Node{{ID: 1, IsActive: true, SubscriptionURL: backend.URL + "/"}},
		}, nil
	}
	mockDB.UpdateDevicesFunc = func(context.Context, uint, string) error { return nil }
	mockDB.UpdateIPsFunc = func(context.Context, uint, string) error { return nil }
	mockDB.UpdateLastRequestFunc = func(context.Context, string) error { return nil }

	return newTestSubSvc(t), mockDB
}

// TestHandleSubscription_SingboxParity_MultiNode proves the sing-box renderer
// keeps BOTH live-shaped nodes with their readable names and full credentials,
// and delivers the same routing/DNS policy the Xray profile carries.
func TestHandleSubscription_SingboxParity_MultiNode(t *testing.T) {
	t.Parallel()

	svc, mockDB := newMultiNodeSub(t, "parity-sb")
	result, _, _, err := HandleSubscription(context.Background(), mockDB, svc, "parity-sb", "192.0.2.1",
		map[string]string{"user-agent": "sing-box/1.10.0"}, "")
	require.NoError(t, err)
	require.Equal(t, "application/json; charset=utf-8", result.Headers["content-type"])

	var doc struct {
		Log       map[string]any   `json:"log"`
		DNS       map[string]any   `json:"dns"`
		Inbounds  []map[string]any `json:"inbounds"`
		Outbounds []map[string]any `json:"outbounds"`
		Route     map[string]any   `json:"route"`
	}
	require.NoError(t, json.Unmarshal(result.Body, &doc))

	// Two live nodes + selector + direct (block outbound removed in sing-box 1.13).
	obs := doc.Outbounds
	require.Len(t, obs, 4)

	var hy2, vless map[string]any
	for _, ob := range obs[:2] {
		switch ob["type"] {
		case "hysteria2":
			hy2 = ob
		case "vless":
			vless = ob
		}
	}
	require.NotNil(t, hy2)
	require.NotNil(t, vless)

	// Readable tags from remarks — never positional proxy-N.
	require.Equal(t, liveHy2Tag, hy2["tag"])
	require.Equal(t, liveVLESSTag, vless["tag"])
	require.NotEqual(t, "proxy-1", hy2["tag"], "node tag must be the upstream remark")

	// Hysteria2 credentials survive: auth password, SALAMANDER obfs, TLS.
	require.Equal(t, "3662e46033d94414a3749b0cf2e2edd6", hy2["password"])
	hy2Obfs, ok := hy2["obfs"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "salamander", hy2Obfs["type"])
	require.Equal(t, "qv24m6vhniw2pg2m", hy2Obfs["password"])
	hy2TLS, ok := hy2["tls"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "st01.kereal.qzz.io", hy2TLS["server_name"])
	require.Equal(t, []any{"h3"}, hy2TLS["alpn"])
	require.Equal(t, true, hy2TLS["enabled"])

	// VLESS Reality credentials survive: uuid + pbk + sid + sni.
	require.Equal(t, "69b28568-1edb-3ee6-0d8a-0d8a3ee62034", vless["uuid"])
	vlessTLS, ok := vless["tls"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, true, vlessTLS["enabled"])
	reality, ok := vlessTLS["reality"].(map[string]any)
	require.True(t, ok, "reality block must survive the round-trip")
	require.Equal(t, "oTWaJuUIEQQOvBErcJEL4lzj6jf9wdGnDB2ocEkeA0c", reality["public_key"])
	require.Equal(t, "d27e335d2b1679", reality["short_id"])
	require.Equal(t, "www.github.com", vlessTLS["server_name"])

	// selector lists both readable tags; direct replaces the removed block outbound.
	sel := obs[2]
	require.Equal(t, "selector", sel["type"])
	require.Equal(t, []any{liveHy2Tag, liveVLESSTag}, sel["outbounds"])
	require.Equal(t, "direct", obs[3]["tag"])

	// Modern routing: rule_set definitions + 6 rules (sniff, reject, checkip,
	// private, geoip-ru, geosite-ru/private), final → selector.
	route := doc.Route
	require.Equal(t, "proxy", route["final"])
	rsAny, ok := route["rule_set"].([]any)
	require.True(t, ok)
	require.Len(t, rsAny, 4, "ads-all, geosite-ru, geosite-private, geoip-ru")
	rulesAny, ok := route["rules"].([]any)
	require.True(t, ok)
	require.Len(t, rulesAny, 6)
	ads, _ := rulesAny[1].(map[string]any)
	require.Equal(t, "reject", ads["action"], "ads block via action:reject (block outbound removed)")
	checkip, _ := rulesAny[2].(map[string]any)
	require.Equal(t, "direct", checkip["outbound"], "IP-check domains route direct")
	ruGeosite, _ := rulesAny[5].(map[string]any)
	require.Equal(t, []any{"geosite-ru", "geosite-private"}, ruGeosite["rule_set"])

	// Modern DNS: URI-form addresses; ru/private rules via rule_set.
	dnsServersAny, ok := doc.DNS["servers"].([]any)
	require.True(t, ok)
	addresses := make([]string, 0, len(dnsServersAny))
	for _, s := range dnsServersAny {
		addresses = append(addresses, s.(map[string]any)["address"].(string))
	}
	require.Contains(t, addresses, "https://1.1.1.1/dns-query")
	require.Contains(t, addresses, "udp://77.88.8.8")

	require.Len(t, doc.Inbounds, 1)
	require.Equal(t, "mixed", doc.Inbounds[0]["type"])
	require.Equal(t, float64(10808), doc.Inbounds[0]["listen_port"])
	_, hasSniff := doc.Inbounds[0]["sniff"]
	require.False(t, hasSniff, "sniff must live in a route rule, not the inbound")
}

// TestHandleSubscription_ClashParity_MultiNode proves the Clash/Mihomo renderer
// keeps BOTH live-shaped nodes with readable names and credentials.
func TestHandleSubscription_ClashParity_MultiNode(t *testing.T) {
	t.Parallel()

	svc, mockDB := newMultiNodeSub(t, "parity-cl")
	result, _, _, err := HandleSubscription(context.Background(), mockDB, svc, "parity-cl", "192.0.2.1",
		map[string]string{"user-agent": "mihomo/1.18.0"}, "")
	require.NoError(t, err)
	require.Equal(t, "text/yaml; charset=utf-8", result.Headers["content-type"])

	var parsed clashConfig
	require.NoError(t, yaml.Unmarshal(result.Body, &parsed))
	require.Len(t, parsed.Proxies, 2, "both live nodes must survive the Clash render")

	byType := map[string]map[string]any{}
	for _, p := range parsed.Proxies {
		proxyType, _ := p["type"].(string)
		byName, _ := p["name"].(string)
		_ = byName
		byType[proxyType] = p
	}

	hy2, ok := byType["hysteria2"]
	require.True(t, ok)
	require.Equal(t, "3662e46033d94414a3749b0cf2e2edd6", hy2["password"])
	require.Equal(t, "salamander", hy2["obfs"])
	require.Equal(t, "qv24m6vhniw2pg2m", hy2["obfs-password"])

	vless, ok := byType["vless"]
	require.True(t, ok)
	require.Equal(t, liveVLESSTag, vless["name"])
	require.Equal(t, "69b28568-1edb-3ee6-0d8a-0d8a3ee62034", vless["uuid"])
	reality, ok := vless["reality-opts"].(map[string]any)
	require.True(t, ok, "reality-opts must survive the round-trip")
	require.Equal(t, "oTWaJuUIEQQOvBErcJEL4lzj6jf9wdGnDB2ocEkeA0c", reality["public-key"])
}
