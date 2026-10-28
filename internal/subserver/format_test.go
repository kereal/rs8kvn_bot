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
		{"stash UA", "Stash/2.4.5", "", OutputClash},
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
	require.Len(t, obs, 4, "2 nodes + selector + direct (no legacy block outbound)")
	require.Equal(t, "selector", obs[2]["type"])
	require.Equal(t, "proxy", obs[2]["tag"])
	require.Equal(t, []string{"node-one", "node-two"}, obs[2]["outbounds"])
	require.Equal(t, "direct", obs[3]["tag"])
}

// TestBuildSingboxConfig_ParityWithXray guards sing-box/Xray config parity under the
// modern sing-box 1.12+ schema: rule-set-based rules replace the removed geosite/geoip
// fields, block outbound is replaced by action:reject, sniff moves from inbound to route
// rules, and DNS addresses use URI forms (legacy formats removed in 1.14).
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

	// dns: URI-form addresses required since sing-box 1.14.
	dns, ok := doc["dns"].(map[string]any)
	require.True(t, ok)
	dnsServers, ok := dns["servers"].([]map[string]any)
	require.True(t, ok)
	addresses := make([]string, 0, len(dnsServers))
	for _, s := range dnsServers {
		addresses = append(addresses, s["server"].(string))
	}
	require.Contains(t, addresses, "1.1.1.1", "proxy-dns must be the typed https server")
	require.Contains(t, addresses, "77.88.8.8", "ru-dns must be the typed udp server")

	// ru/private domains steer to ru-dns via rule_set (modern replacement of geosite field).
	dnsRules, ok := dns["rules"].([]map[string]any)
	require.True(t, ok)
	require.Len(t, dnsRules, 1)
	dnsRule := dnsRules[0]
	require.Equal(t, []string{"geosite-ru", "geosite-private"}, dnsRule["rule_set"])
	require.Equal(t, "ru-dns", dnsRule["server"])

	// inbounds: mixed listener on 10808, sniff removed from inbound (migrated to route action).
	inbounds, ok := doc["inbounds"].([]map[string]any)
	require.True(t, ok)
	require.Len(t, inbounds, 1)
	require.Equal(t, "mixed", inbounds[0]["type"])
	require.Equal(t, "127.0.0.1", inbounds[0]["listen"])
	require.Equal(t, 10808, inbounds[0]["listen_port"])
	_, hasSniff := inbounds[0]["sniff"]
	require.False(t, hasSniff, "sniff must not be in inbound (migrated to route rule)")

	// outbounds: 2 nodes + selector + direct (block outbound removed in 1.13).
	obs, ok := doc["outbounds"].([]map[string]any)
	require.True(t, ok)
	require.Len(t, obs, 4)
	sel := obs[2]
	require.Equal(t, "selector", sel["type"])
	require.Equal(t, "proxy", sel["tag"])
	require.Equal(t, []string{"🇫🇮 Finland Hysteria 🔥", "🇩🇪 Germany VLESS"}, sel["outbounds"])
	require.Equal(t, "direct", obs[3]["tag"])

	// route: modern schema: rule_set present (geosite/geoip removed in 1.12), no geoip/geosite fields in rules.
	route, ok := doc["route"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "proxy", route["final"])
	require.Equal(t, true, route["auto_detect_interface"])
	require.Equal(t, "ru-dns", route["default_domain_resolver"], "node domains resolve direct, required since 1.14 with 2+ DNS servers")

	// rule_set definitions are loaded (4 sets covering ads, ru, private, ru-ips).
	rsDefs, ok := route["rule_set"].([]map[string]any)
	require.True(t, ok)
	require.Len(t, rsDefs, 4)
	rsTags := make([]string, 0, len(rsDefs))
	for _, r := range rsDefs {
		rsTags = append(rsTags, r["tag"].(string))
	}
	require.Contains(t, rsTags, "ads-all")
	require.Contains(t, rsTags, "geosite-ru")
	require.Contains(t, rsTags, "geosite-private")
	require.Contains(t, rsTags, "geoip-ru")

	// 6 rules: sniff, reject, checkip, private, geoip-ru, geosite-ru/private.
	rulesAny, ok := route["rules"].([]map[string]any)
	require.True(t, ok)
	require.Len(t, rulesAny, 6)
	rules := make([]map[string]any, len(rulesAny))
	for i, r := range rulesAny {
		rules[i] = r
	}

	// Rule 0: sniff (migrated from legacy inbound sniff).
	require.Equal(t, "sniff", rules[0]["action"])
	require.Equal(t, []string{"mixed-in"}, rules[0]["inbound"])

	// Rule 1: reject for ads (block outbound removed in 1.13).
	require.Equal(t, []string{"ads-all"}, rules[1]["rule_set"])
	require.Equal(t, "reject", rules[1]["action"])

	// Rule 2: IP-check domains route direct.
	require.Equal(t, "direct", rules[2]["outbound"])
	require.Contains(t, rules[2]["domain_suffix"], "checkip.amazonaws.com")
	require.Contains(t, rules[2]["domain_suffix"], "ifconfig.me")

	// Rule 3: private IPs route direct.
	require.Equal(t, true, rules[3]["ip_is_private"])
	require.Equal(t, "direct", rules[3]["outbound"])

	// Rule 4: Russian IPs via rule_set (geoip removed in 1.12).
	require.Equal(t, []string{"geoip-ru"}, rules[4]["rule_set"])
	require.Equal(t, "direct", rules[4]["outbound"])

	// Rule 5: Russian/private sites via rule_set (geosite removed in 1.12).
	require.Equal(t, []string{"geosite-ru", "geosite-private"}, rules[5]["rule_set"])
	require.Equal(t, "direct", rules[5]["outbound"])
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
	require.Equal(t, "proxy-1", outbounds[0]["tag"], "reserved direct tag renamed")
	require.NotEmpty(t, outbounds[1]["tag"])
	require.NotEqual(t, "proxy", outbounds[1]["tag"], "server outbound must not take the selector tag")
	require.Equal(t, "proxy-2", outbounds[1]["tag"], "empty tag takes the first free proxy-N")
	require.NotEqual(t, "direct", outbounds[1]["tag"], "server outbound must not take the built-in direct tag")
	require.Equal(t, "proxy-3", outbounds[2]["tag"], "proxy-1 was taken by the direct-tag fallback")
	require.Equal(t, "node", outbounds[3]["tag"], "non-reserved tag kept as-is")
}

// TestRenderSingboxSubscriptionReservedTags is the sing-box counterpart of
// TestBuildResponseReservedTagsFallback: a node remarked "direct" or "proxy"
// must not keep that tag, otherwise it duplicates the built-in direct outbound
// or the "proxy" selector that BuildSingboxConfig always appends, and the route
// rules (which resolve "direct" by name) reach the wrong outbound.
func TestRenderSingboxSubscriptionReservedTags(t *testing.T) {
	t.Parallel()

	result, err := buildResponse(newTestSubSvc(t), "singbox-reserved", aggregatedSources{
		jsonConfigs: []json.RawMessage{
			json.RawMessage(`{"type":"vless","address":"one.example","port":443,"uuid":"id-1","remark":"direct"}`),
			json.RawMessage(`{"type":"vless","address":"two.example","port":443,"uuid":"id-2","remark":"proxy"}`),
			json.RawMessage(`{"type":"vless","address":"three.example","port":443,"uuid":"id-3","remark":"node-three"}`),
		},
	}, 0, "", OutputSingbox)
	require.NoError(t, err)

	var doc struct {
		Outbounds []struct {
			Type      string   `json:"type"`
			Tag       string   `json:"tag"`
			Outbounds []string `json:"outbounds"`
		} `json:"outbounds"`
	}
	require.NoError(t, json.Unmarshal(result.Body, &doc))
	require.Len(t, doc.Outbounds, 5, "3 nodes + selector + direct")

	serverTags := make([]string, 0, 3)
	for _, ob := range doc.Outbounds[:3] {
		serverTags = append(serverTags, ob.Tag)
	}
	require.Equal(t, []string{"proxy-1", "proxy-2", "node-three"}, serverTags)

	seen := make(map[string]struct{}, len(doc.Outbounds))
	for _, ob := range doc.Outbounds {
		_, dup := seen[ob.Tag]
		require.False(t, dup, "tag %q assigned twice", ob.Tag)
		seen[ob.Tag] = struct{}{}
	}

	require.Equal(t, "selector", doc.Outbounds[3].Type)
	require.Equal(t, "proxy", doc.Outbounds[3].Tag)
	require.Equal(t, serverTags, doc.Outbounds[3].Outbounds, "selector lists only server outbounds")
	require.Equal(t, "direct", doc.Outbounds[4].Type)
	require.Equal(t, "direct", doc.Outbounds[4].Tag)
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

// TestHysteriaV1UnsupportedInSingboxAndClash pins the protocol split: hysteria2
// and its hy2 share-link alias render as a sing-box/Clash hysteria2 outbound,
// while legacy hysteria (v1) is not silently upgraded to v2 — both serializers
// reject it so the node is skipped instead of producing a profile that cannot
// handshake.
func TestHysteriaV1UnsupportedInSingboxAndClash(t *testing.T) {
	t.Parallel()

	legacy := &serverConfig{Type: "hysteria", Address: "hy1.example", Port: 36712, Password: "v1-token"}

	_, err := buildSingboxOutbound(legacy)
	require.Error(t, err, "hysteria v1 must not be rendered as a sing-box hysteria2 outbound")
	assert.Contains(t, err.Error(), "unsupported protocol")

	_, err = buildClashProxy(legacy)
	require.Error(t, err, "hysteria v1 must not be rendered as a Clash hysteria2 proxy")
	assert.Contains(t, err.Error(), "unsupported protocol")

	for _, typ := range []string{"hysteria2", "hy2"} {
		cfg := &serverConfig{Type: typ, Address: "hy2.example", Port: 443, Password: "hy2-pass"}

		singboxOut, err := buildSingboxOutbound(cfg)
		require.NoError(t, err, "type %q must stay supported in sing-box", typ)
		assert.Equal(t, "hysteria2", singboxOut["type"])

		clashOut, err := buildClashProxy(cfg)
		require.NoError(t, err, "type %q must stay supported in Clash", typ)
		assert.Equal(t, "hysteria2", clashOut["type"])
	}

	// End to end: a legacy hysteria item is dropped from a mixed sing-box
	// subscription, and is a hard error when it is the only item.
	items := []json.RawMessage{
		json.RawMessage(`{"type":"hysteria","address":"hy1.example","port":36712,"password":"v1-token","remark":"legacy"}`),
		json.RawMessage(`{"type":"hysteria2","address":"hy2.example","port":443,"password":"hy2-pass","remark":"modern"}`),
	}

	outbounds, err := ConvertJSONConfigsToSingbox(items)
	require.NoError(t, err)
	require.Len(t, outbounds, 1, "only the hysteria2 node survives")
	assert.Equal(t, "modern", outbounds[0]["tag"])

	_, err = ConvertJSONConfigsToSingbox(items[:1])
	require.Error(t, err, "a subscription of only hysteria v1 nodes must not render")
}

// TestSingboxRejectsXHTTPTransport pins that xhttp/splithttp — an Xray-only
// transport with no sing-box equivalent — is rejected before an outbound is
// built, instead of emitting transport.type=xhttp that sing-box cannot parse.
func TestSingboxRejectsXHTTPTransport(t *testing.T) {
	t.Parallel()

	for _, network := range []string{"xhttp", "splithttp", "XHTTP"} {
		cfg := &serverConfig{
			Type:    "vless",
			Address: "x.example",
			Port:    443,
			UUID:    "id-1",
			Network: network,
			Path:    "/x",
			Host:    "x.example.com",
			Mode:    "stream-up",
		}

		out, err := buildSingboxOutbound(cfg)
		require.Error(t, err, "network %q must be rejected for sing-box", network)
		assert.Contains(t, err.Error(), "unsupported transport")
		assert.Nil(t, out)
	}

	// Mixed subscription: the xhttp node is skipped, the ws node survives with
	// its transport intact, and the document contains no xhttp type.
	items := []json.RawMessage{
		json.RawMessage(`{"type":"vless","address":"x.example","port":443,"uuid":"id-1","network":"xhttp","path":"/x","remark":"xhttp-node"}`),
		json.RawMessage(`{"type":"vless","address":"ws.example","port":443,"uuid":"id-2","security":"tls","network":"ws","path":"/ws","remark":"ws-node"}`),
	}

	outbounds, err := ConvertJSONConfigsToSingbox(items)
	require.NoError(t, err)
	require.Len(t, outbounds, 1, "only the ws node survives")
	assert.Equal(t, "ws-node", outbounds[0]["tag"])

	transport, ok := outbounds[0]["transport"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "ws", transport["type"])

	_, err = ConvertJSONConfigsToSingbox(items[:1])
	require.Error(t, err, "a subscription of only xhttp nodes must not render")
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
		require.Len(t, doc.Outbounds, 3, "1 node + selector + direct (no legacy block outbound)")
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

		// Full profile: local listener + manual selector + terminal rule.
		require.Contains(t, string(result.Body), "mixed-port: 7890")
		require.Contains(t, string(result.Body), "proxy-groups:")
		require.Contains(t, string(result.Body), "- DOMAIN-SUFFIX,api.ipify.org,DIRECT")
		require.Contains(t, string(result.Body), "- MATCH,PROXY")
		require.NotContains(t, string(result.Body), "rule-providers:", "no remote rule-set dependency")
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
