package subserver

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// ==================== DetectOutputFormat ====================

func TestDetectOutputFormat(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ua   string
		arg  string
		want OutputFormat
	}{
		{"unknown UA defaults to xray", "SomeCustom/1.0", "", OutputXray},
		{"empty UA defaults to xray", "", "", OutputXray},
		{"v2rayNG stays on xray", "v2rayNG/1.8.5", "", OutputXray},
		{"sing-box UA", "sing-box/1.10.0 (Windows)", "", OutputSingbox},
		{"singbox UA", "singbox/1.0.0", "", OutputSingbox},
		{"clash UA", "clash-verge/v1.3.8", "", OutputClash},
		{"mihomo UA", "Mihomo/1.18.0", "", OutputClash},
		{"explicit singbox wins over any UA", "clash-verge/v1.3.8", "singbox", OutputSingbox},
		{"explicit clash wins over sing-box UA", "sing-box/1.10.0", "clash", OutputClash},
		{"explicit format is case-insensitive", "v2rayNG/1.8.5", "SING-BOX", OutputSingbox},
		{"unknown explicit format falls back to UA", "", "bogus", OutputXray},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DetectOutputFormat(tt.arg, tt.ua)
			assert.Equal(t, tt.want, got)
			assert.NotEmpty(t, got.String())
			assert.NotEmpty(t, got.ContentType())
		})
	}
}

// ==================== Sing-box serializer ====================

func TestConvertJSONConfigsToSingbox(t *testing.T) {
	t.Parallel()

	items := []json.RawMessage{
		json.RawMessage(`{"type":"vless","address":"one.example","port":443,"uuid":"id-1","flow":"xtls-rprx-vision","security":"tls","sni":"one.example","network":"ws","path":"/ws","host":"cdn.example","remark":"node-one"}`),
		json.RawMessage(`{"type":"vmess","address":"two.example","port":8443,"uuid":"id-2","aid":"2","tls":"true","sni":"two.example","remark":"node-two"}`),
		json.RawMessage(`{"type":"shadowsocks","address":"ss.example","port":8388,"method":"aes-128-gcm","password":"pass","remark":"node-ss"}`),
		json.RawMessage(`{"type":"unsupported","address":"x.example","port":1,"remark":"skip"}`),
	}

	outbounds, err := ConvertJSONConfigsToSingbox(items)
	require.NoError(t, err)
	require.Len(t, outbounds, 3)

	vless := outbounds[0]
	require.Equal(t, "vless", vless["type"])
	require.Equal(t, "one.example", vless["server"])
	require.Equal(t, 443, vless["server_port"])
	require.Equal(t, "xtls-rprx-vision", vless["flow"])
	tls, ok := vless["tls"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, true, tls["enabled"])
	require.Equal(t, "one.example", tls["server_name"])
	transport, ok := vless["transport"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "ws", transport["type"])
	require.Equal(t, "/ws", transport["path"])

	vmess := outbounds[1]
	require.Equal(t, "vmess", vmess["type"])
	require.Equal(t, 2, vmess["alter_id"])

	ss := outbounds[2]
	require.Equal(t, "shadowsocks", ss["type"])
	require.Equal(t, "aes-128-gcm", ss["method"])
}

func TestBuildSingboxConfig_AppendsSelector(t *testing.T) {
	t.Parallel()

	outbounds := []map[string]any{
		{"type": "vless", "tag": "node-one"},
		{"type": "vless", "tag": "node-two"},
	}
	doc := BuildSingboxConfig(outbounds)

	obs, ok := doc["outbounds"].([]map[string]any)
	require.True(t, ok)
	require.Len(t, obs, 5, "2 nodes + selector + direct + block")
	require.Equal(t, "selector", obs[2]["type"])
	require.Equal(t, "proxy", obs[2]["tag"])
	require.Equal(t, []string{"node-one", "node-two"}, obs[2]["outbounds"])
	require.Equal(t, "direct", obs[3]["tag"])
	require.Equal(t, "block", obs[4]["tag"])
}

// TestBuildSingboxConfig_ParityWithXray guards sing-box/Xray config parity: the
// subscription must deliver the same routing policy, DNS setup and local
// listener the Xray profile carries — not a bare list of servers.
func TestBuildSingboxConfig_ParityWithXray(t *testing.T) {
	t.Parallel()

	outbounds := []map[string]any{
		{"type": "hysteria2", "tag": "🇫🇮 Finland Hysteria 🔥"},
		{"type": "vless", "tag": "🇩🇪 Germany VLESS"},
	}
	doc := BuildSingboxConfig(outbounds)

	// log mirrors the Xray loglevel.
	log, ok := doc["log"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "warn", log["level"])

	// dns: DoH default + Russian resolver for ru/private domains (skipFallback
	// semantics: a rule steers ru/private to ru-dns, everything else hits the
	// first server, the DoH one).
	dns, ok := doc["dns"].(map[string]any)
	require.True(t, ok)
	dnsServers, ok := dns["servers"].([]map[string]any)
	require.True(t, ok)
	addresses := make([]string, 0, len(dnsServers))
	for _, s := range dnsServers {
		addresses = append(addresses, s["address"].(string))
	}
	require.Contains(t, addresses, "https://1.1.1.1/dns-query")
	require.Contains(t, addresses, "77.88.8.8")
	dnsRules, ok := dns["rules"].([]map[string]any)
	require.True(t, ok)
	require.Len(t, dnsRules, 1, "ru/private domains resolve via ru-dns")
	require.Equal(t, []string{"category-ru", "private"}, dnsRules[0]["geosite"])
	require.Equal(t, "ru-dns", dnsRules[0]["server"])

	// inbounds: mixed socks/http listener mirroring Xray 10808/10809.
	inbounds, ok := doc["inbounds"].([]map[string]any)
	require.True(t, ok)
	require.Len(t, inbounds, 1)
	require.Equal(t, "mixed", inbounds[0]["type"])
	require.Equal(t, "127.0.0.1", inbounds[0]["listen"])
	require.Equal(t, 10808, inbounds[0]["listen_port"])
	require.Equal(t, true, inbounds[0]["sniff"])

	// outbounds: every node + selector + direct + block.
	obs, ok := doc["outbounds"].([]map[string]any)
	require.True(t, ok)
	require.Len(t, obs, 5)
	sel := obs[2]
	require.Equal(t, "selector", sel["type"])
	require.Equal(t, "proxy", sel["tag"])
	require.Equal(t, []string{"🇫🇮 Finland Hysteria 🔥", "🇩🇪 Germany VLESS"}, sel["outbounds"],
		"selector must list every node by its remark")
	require.Equal(t, "direct", obs[3]["tag"])
	require.Equal(t, "block", obs[4]["tag"])

	// route: same policy as the Xray template, catch-all → selector.
	route, ok := doc["route"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "proxy", route["final"], "catch-all must target the selector")
	require.Equal(t, true, route["auto_detect_interface"])
	rules, ok := route["rules"].([]map[string]any)
	require.True(t, ok)
	require.Len(t, rules, 5)

	outs := make([]string, 0, len(rules))
	for _, r := range rules {
		outbound, _ := r["outbound"].(string)
		outs = append(outs, outbound)
	}
	require.Equal(t, []string{"direct", "block", "direct", "direct", "direct"}, outs,
		"rule order must mirror the Xray template: checkip→direct, ads→block, private→direct, geoip-ru→direct, geosite-ru→direct")

	require.Contains(t, rules[0]["domain_suffix"], "checkip.amazonaws.com")
	require.Contains(t, rules[0]["domain_suffix"], "ifconfig.me")
	require.Equal(t, []string{"category-ads-all"}, rules[1]["geosite"])
	require.Equal(t, true, rules[2]["ip_is_private"])
	require.Equal(t, []string{"ru"}, rules[3]["geoip"])
	require.Equal(t, []string{"category-ru", "private"}, rules[4]["geosite"])
}

func TestTagSingboxOutbounds(t *testing.T) {
	t.Parallel()

	outbounds := []map[string]any{
		{"tag": "direct"},
		{"tag": ""},
		{"tag": "proxy-1"},
		{"tag": "node"},
	}
	tagSingboxOutbounds(outbounds)
	require.Equal(t, "direct", outbounds[0]["tag"], "non-reserved tag kept as-is")
	require.NotEmpty(t, outbounds[1]["tag"])
	require.NotEqual(t, "proxy", outbounds[1]["tag"], "server outbound must not take the selector tag")
	require.Equal(t, "proxy-2", outbounds[2]["tag"], "proxy-1 was taken by the empty-tag fallback")
	require.Equal(t, "node", outbounds[3]["tag"])
}

// ==================== Clash serializer ====================

func TestConvertJSONConfigsToClash(t *testing.T) {
	t.Parallel()

	items := []json.RawMessage{
		json.RawMessage(`{"type":"vless","address":"one.example","port":443,"uuid":"id-1","security":"tls","sni":"one.example","network":"ws","path":"/ws","host":"cdn.example","remark":"node-one"}`),
		json.RawMessage(`{"type":"tuic","address":"tuic.example","port":443,"uuid":"id-3","password":"pass","remark":"node-tuic"}`),
		json.RawMessage(`{"type":"unsupported","address":"x.example","port":1,"remark":"skip"}`),
	}

	proxies, err := ConvertJSONConfigsToClash(items)
	require.NoError(t, err)
	require.Len(t, proxies, 2)

	// YAML-round-trip through the existing parser stays lossless for the fields
	// both directions know about.
	doc := clashConfig{Proxies: proxies}
	data, err := yaml.Marshal(doc)
	require.NoError(t, err)
	require.True(t, bytes.Contains(data, []byte("proxies:")))

	var parsed clashConfig
	require.NoError(t, yaml.Unmarshal(data, &parsed))
	require.Len(t, parsed.Proxies, 2)
}

// ==================== UA-driven dispatch (wiring) ====================

func TestHandleSubscription_FormatByUA(t *testing.T) {
	t.Parallel()

	const link = "vless://uuid@server.example:443?security=tls#Node"
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(link))
	}))
	defer backend.Close()

	withBackend := func(t *testing.T, subID string) (*Service, *testutil.DatabaseService, string) {
		t.Helper()

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
		return newTestSubSvc(t), mockDB, link
	}

	t.Run("default stays Xray", func(t *testing.T) {
		svc, mockDB, _ := withBackend(t, "sub-def")
		ctx := context.Background()

		result, _, _, err := HandleSubscription(ctx, mockDB, svc, "sub-def", "1.2.3.4", nil, "")
		require.NoError(t, err)
		require.Equal(t, "application/json; charset=utf-8", result.Headers["content-type"])

		var configs []struct {
			Remarks string `json:"remarks"`
		}
		require.NoError(t, json.Unmarshal(result.Body, &configs))
		require.Len(t, configs, 1)
		require.Equal(t, "Node", configs[0].Remarks)
	})

	t.Run("sing-box UA yields selector config", func(t *testing.T) {
		svc, mockDB, _ := withBackend(t, "sub-sb")
		result, _, _, err := HandleSubscription(context.Background(), mockDB, svc, "sub-sb", "1.2.3.4",
			map[string]string{"user-agent": "sing-box/1.10.0"}, "")
		require.NoError(t, err)
		require.Equal(t, "application/json; charset=utf-8", result.Headers["content-type"])

		var doc struct {
			Outbounds []any `json:"outbounds"`
		}
		require.NoError(t, json.Unmarshal(result.Body, &doc))
		require.Len(t, doc.Outbounds, 4, "1 node + selector + direct + block")
		sel, ok := doc.Outbounds[1].(map[string]any)
		require.True(t, ok)
		require.Equal(t, "selector", sel["type"])
		require.Equal(t, "proxy", sel["tag"])
	})

	t.Run("clash UA yields YAML", func(t *testing.T) {
		svc, mockDB, _ := withBackend(t, "sub-cl")
		result, _, _, err := HandleSubscription(context.Background(), mockDB, svc, "sub-cl", "1.2.3.4",
			map[string]string{"user-agent": "clash-verge/v1.3.8"}, "")
		require.NoError(t, err)
		require.Equal(t, "text/yaml; charset=utf-8", result.Headers["content-type"])
		require.Contains(t, string(result.Body), "proxies:")
	})

	t.Run("explicit format overrides UA", func(t *testing.T) {
		svc, mockDB, _ := withBackend(t, "sub-fmt")
		result, _, _, err := HandleSubscription(context.Background(), mockDB, svc, "sub-fmt", "1.2.3.4",
			map[string]string{"user-agent": "clash-verge/v1.3.8"}, "singbox")
		require.NoError(t, err)
		require.Equal(t, "application/json; charset=utf-8", result.Headers["content-type"])

		var doc struct {
			Outbounds []any `json:"outbounds"`
		}
		require.NoError(t, json.Unmarshal(result.Body, &doc))
		require.GreaterOrEqual(t, len(doc.Outbounds), 2)
	})
}

func TestHandleSubscription_PerFormatCacheKeys(t *testing.T) {
	t.Parallel()

	const link = "vless://uuid@server.example:443?security=tls#Node"
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(link))
	}))
	defer backend.Close()

	mockDB := testutil.NewDatabaseService()
	mockDB.GetWithPlanAndNodesFunc = func(ctx context.Context, subID string) (*database.SubscriptionFull, error) {
		return &database.SubscriptionFull{
			Subscription: database.Subscription{ID: 1, SubscriptionID: subID, Status: "active"},
			Plan:         database.Plan{ID: 1},
			Nodes:        []database.Node{{ID: 1, IsActive: true, SubscriptionURL: backend.URL + "/"}},
		}, nil
	}
	mockDB.UpdateDevicesFunc = func(context.Context, uint, string) error { return nil }
	mockDB.UpdateIPsFunc = func(context.Context, uint, string) error { return nil }
	mockDB.UpdateLastRequestFunc = func(context.Context, string) error { return nil }
	svc := newTestSubSvc(t)
	ctx := context.Background()

	// Xray default keeps the bare subID cache key.
	_, _, _, err := HandleSubscription(ctx, mockDB, svc, "sub-fmt", "1.2.3.4", nil, "") //nolint:dogsled // only err matters
	require.NoError(t, err)
	require.True(t, cacheHas(svc, "sub-fmt"), "default Xray must use the bare cache key")

	// sing-box uses a suffixed key, so the two formats never share a body.
	_, _, _, err = HandleSubscription(ctx, mockDB, svc, "sub-fmt", "1.2.3.4", //nolint:dogsled // only err matters
		map[string]string{"user-agent": "sing-box/1.10.0"}, "")
	require.NoError(t, err)
	require.True(t, cacheHas(svc, "sub-fmt:singbox"))
	require.False(t, cacheHas(svc, "sub-fmt:clash"))

	// Revision check: without the per-format key the sing-box body would have
	// overwritten (or been served by) the Xray entry.
	bodyXray, _, _ := svc.GetCache("sub-fmt")
	bodySB, _, _ := svc.GetCache("sub-fmt:singbox")
	require.NotEqual(t, bodyXray, bodySB)
}

func cacheHas(svc *Service, key string) bool {
	body, _, ok := svc.GetCache(key)
	return ok && len(body) > 0
}
