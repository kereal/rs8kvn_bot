package subserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kereal/rs8kvn_bot/internal/testutil"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// Synthetic data shaped like a real panel response: a Hysteria2 node with
// SALAMANDER obfuscation and a VLESS Reality node, both carrying emoji
// remarks. Never paste live share links here — the repo is public. The
// sing-box and Clash renderers must preserve every node, its readable name
// and its credentials.
const (
	liveHy2Link   = "hysteria2://11111111-1111-1111-1111-111111111111@node1.example.net:443?sni=node1.example.net&fp=firefox&alpn=h3&obfs=salamander&obfs-password=example-obfs-pass#%F0%9F%87%AB%F0%9F%87%AE%20Finland%20Hysteria%20%F0%9F%94%A5"
	liveVLESSLink = "vless://22222222-2222-2222-2222-222222222222@node1.example.net:443?security=reality&pbk=EXAMPLE_REALITY_PUBLIC_KEY_0000000000000000000&sid=0000000000000000&sni=www.github.com&fp=firefox#🇫🇮%20Finland%20vless%20🎈"

	liveHy2Tag   = "🇫🇮 Finland Hysteria 🔥"
	liveVLESSTag = "🇫🇮 Finland vless 🎈"
)

// newMultiNodeSub spins a backend serving the two live-shaped share links and
// mocks the DB for the given subscription id.
func newMultiNodeSub(t *testing.T, subID string) (*Service, *testutil.SubscriptionRepositoryFake) {
	t.Helper()

	body := liveHy2Link + "\n" + liveVLESSLink
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(backend.Close)

	return newActiveSubDB(t, backend.URL+"/")
}

// TestHandleSubscription_SingboxParity_MultiNode proves the sing-box renderer
// keeps BOTH live-shaped nodes with their readable names and full credentials.
// Routing/DNS/inbound policy is covered by TestBuildSingboxConfig_ParityWithXray.
func TestHandleSubscription_SingboxParity_MultiNode(t *testing.T) {
	t.Parallel()

	svc, mockDB := newMultiNodeSub(t, "parity-sb")
	result, _, _, err := HandleSubscription(context.Background(), mockDB, svc, "parity-sb", "192.0.2.1",
		map[string]string{"user-agent": "sing-box/1.10.0"}, "")
	require.NoError(t, err)
	require.Equal(t, "application/json; charset=utf-8", result.Headers["content-type"])

	var doc struct {
		Outbounds []map[string]any `json:"outbounds"`
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
	require.Equal(t, "11111111-1111-1111-1111-111111111111", hy2["password"])
	hy2Obfs, ok := hy2["obfs"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "salamander", hy2Obfs["type"])
	require.Equal(t, "example-obfs-pass", hy2Obfs["password"])
	hy2TLS, ok := hy2["tls"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "node1.example.net", hy2TLS["server_name"])
	require.Equal(t, []any{"h3"}, hy2TLS["alpn"])
	require.Equal(t, true, hy2TLS["enabled"])

	// VLESS Reality credentials survive: uuid + pbk + sid + sni.
	require.Equal(t, "22222222-2222-2222-2222-222222222222", vless["uuid"])
	vlessTLS, ok := vless["tls"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, true, vlessTLS["enabled"])
	reality, ok := vlessTLS["reality"].(map[string]any)
	require.True(t, ok, "reality block must survive the round-trip")
	require.Equal(t, "EXAMPLE_REALITY_PUBLIC_KEY_0000000000000000000", reality["public_key"])
	require.Equal(t, "0000000000000000", reality["short_id"])
	require.Equal(t, "www.github.com", vlessTLS["server_name"])

	// sing-box rejects a reality outbound without uTLS, so utls must always be
	// present and enabled, carrying the advertised fingerprint.
	utls, ok := vlessTLS["utls"].(map[string]any)
	require.True(t, ok, "reality outbound must carry utls, sing-box refuses it otherwise")
	require.Equal(t, true, utls["enabled"])
	require.Equal(t, "firefox", utls["fingerprint"])
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

	// Full profile: mixed port, manual selector group and routing policy.
	require.Equal(t, 7890, parsed.MixedPort)
	require.Equal(t, "rule", parsed.Mode)
	require.Len(t, parsed.ProxyGroups, 1)
	selector, ok := parsed.ProxyGroups[0]["type"].(string)
	require.True(t, ok)
	require.Equal(t, "select", selector, "node switching must be manual, no automatic load balancing")
	require.Equal(t, "PROXY", parsed.ProxyGroups[0]["name"])
	groupProxies := parsed.ProxyGroups[0]["proxies"].([]any)
	require.Equal(t, []any{liveHy2Tag, liveVLESSTag}, groupProxies, "selector lists both readable node names")
	require.Contains(t, parsed.Rules, "MATCH,PROXY", "default route targets the manual group")
	require.Contains(t, parsed.Rules, "GEOSITE,category-ads-all,REJECT", "ads blocked like Xray/sing-box")
	require.Contains(t, parsed.Rules, "GEOSITE,category-ru,DIRECT")
	require.NotContains(t, parsed.Rules, "GEOIP,LAN", "private ranges stay explicit IP-CIDR, not db-dependent")

	byType := map[string]map[string]any{}
	for _, p := range parsed.Proxies {
		proxyType, _ := p["type"].(string)
		byName, _ := p["name"].(string)
		_ = byName
		byType[proxyType] = p
	}

	hy2, ok := byType["hysteria2"]
	require.True(t, ok)
	require.Equal(t, "11111111-1111-1111-1111-111111111111", hy2["password"])
	require.Equal(t, "salamander", hy2["obfs"])
	require.Equal(t, "example-obfs-pass", hy2["obfs-password"])

	vless, ok := byType["vless"]
	require.True(t, ok)
	require.Equal(t, liveVLESSTag, vless["name"])
	require.Equal(t, "22222222-2222-2222-2222-222222222222", vless["uuid"])
	reality, ok := vless["reality-opts"].(map[string]any)
	require.True(t, ok, "reality-opts must survive the round-trip")
	require.Equal(t, "EXAMPLE_REALITY_PUBLIC_KEY_0000000000000000000", reality["public-key"])
}
