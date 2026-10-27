package subserver

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// xrayConnection is one element of the subscription array: a complete Xray-core
// client config that carries exactly one proxy outbound.
type xrayConnection struct {
	Remarks string `json:"remarks"`
	Log     struct {
		Loglevel string `json:"loglevel"`
	} `json:"log"`
	DNS struct {
		Servers []json.RawMessage `json:"servers"`
	} `json:"dns"`
	Inbounds []struct {
		Tag      string `json:"tag"`
		Protocol string `json:"protocol"`
	} `json:"inbounds"`
	Outbounds []struct {
		Protocol string `json:"protocol"`
		Tag      string `json:"tag"`
	} `json:"outbounds"`
	Routing struct {
		DomainStrategy string `json:"domainStrategy"`
		Balancers      []struct {
			Tag      string   `json:"tag"`
			Selector []string `json:"selector"`
		} `json:"balancers"`
		Rules []struct {
			Domain      []string `json:"domain"`
			IP          []string `json:"ip"`
			Network     string   `json:"network"`
			Port        int      `json:"port"`
			OutboundTag string   `json:"outboundTag"`
			BalancerTag string   `json:"balancerTag"`
		} `json:"rules"`
	} `json:"routing"`
}

func decodeSubscription(t *testing.T, body []byte) []xrayConnection {
	t.Helper()

	var configs []xrayConnection
	require.NoError(t, json.Unmarshal(body, &configs), "subscription body must be a JSON array")

	return configs
}

func TestBuildResponseAlwaysRendersXrayJSON(t *testing.T) {
	t.Parallel()

	const jsonNode = `{"type":"vless","address":"vpn.example","port":443,"uuid":"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee","security":"reality","sni":"front.example","pbk":"public-key","sid":"abcd","remark":"node-one"}`
	const clashNode = `proxies:
  - name: clash-node
    type: trojan
    server: clash.example
    port: 443
    password: secret
    sni: front.example
    tls: true
`
	const link = "vless://bbbbbbbb-bbbb-cccc-dddd-eeeeeeeeeeee@link.example:443?security=tls&sni=front.example&type=ws&path=%2Fws#link-node"
	tests := []struct {
		name    string
		configs []json.RawMessage
		items   []string
		wantTag string
	}{
		{
			name: "JSON source",
			configs: []json.RawMessage{
				json.RawMessage(jsonNode),
			},
			wantTag: "node-one",
		},
		{
			name: "Clash normalized source",
			configs: func() []json.RawMessage {
				got, err := ExtractClashConfigs([]byte(clashNode))
				require.NoError(t, err)
				return got
			}(),
			wantTag: "clash-node",
		},
		{
			name:    "plain share link",
			items:   []string{link},
			wantTag: "link-node",
		},
		{
			name:    "share link source",
			items:   []string{"vless://cccccccc-bbbb-cccc-dddd-eeeeeeeeeeee@base64.example:443?security=tls#Base64"},
			wantTag: "Base64",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			svc := newTestSubSvc(t)
			result, err := buildResponse(svc, "subscription", aggregatedSources{
				jsonConfigs:        tt.configs,
				items:              tt.items,
				firstSourceHeaders: map[string]string{"profile-title": "Test"},
				firstExpire:        "1900000000",
				totalUpload:        12,
				totalDownload:      34,
			}, 56, "")
			require.NoError(t, err)
			require.Equal(t, "application/json; charset=utf-8", result.Headers["content-type"])

			configs := decodeSubscription(t, result.Body)
			require.Len(t, configs, 1, "one source node must produce exactly one connection profile")

			conn := configs[0]
			require.Equal(t, tt.wantTag, conn.Remarks, "client profile name must come from the node remark")
			require.Len(t, conn.Outbounds, 3)
			require.Equal(t, tt.wantTag, conn.Outbounds[0].Tag)
			require.Equal(t, "direct", conn.Outbounds[1].Tag)
			require.Equal(t, "block", conn.Outbounds[2].Tag)
			require.Equal(t, tt.wantTag, conn.Routing.Rules[len(conn.Routing.Rules)-1].OutboundTag,
				"default route must point at the node's own tag")

			require.Contains(t, result.Headers["subscription-userinfo"], "upload=12")
			require.Contains(t, result.Headers["subscription-userinfo"], "download=34")
			require.Contains(t, result.Headers["subscription-userinfo"], "total=56")

			cached, headers, ok := svc.GetCache("subscription")
			require.True(t, ok)
			require.Equal(t, result.Body, cached)
			require.Equal(t, result.Headers, headers)
		})
	}
}

// TestBuildResponseOneProfilePerNode guards the array contract: every node in a
// multi-node source becomes its own connection object, so clients that import a
// single profile per array element list all nodes as selectable configs.
func TestBuildResponseOneProfilePerNode(t *testing.T) {
	t.Parallel()

	svc := newTestSubSvc(t)
	result, err := buildResponse(svc, "multi", aggregatedSources{
		jsonConfigs: []json.RawMessage{
			json.RawMessage(`{"type":"vless","address":"one.example","port":443,"uuid":"id-1","remark":"node-one"}`),
			json.RawMessage(`{"type":"vless","address":"two.example","port":443,"uuid":"id-2","remark":"node-two"}`),
			json.RawMessage(`{"type":"trojan","address":"three.example","port":443,"password":"secret","remark":"node-three"}`),
		},
	}, 0, "")
	require.NoError(t, err)

	configs := decodeSubscription(t, result.Body)
	require.Len(t, configs, 3)
	require.Equal(t, []string{"node-one", "node-two", "node-three"}, []string{
		configs[0].Remarks, configs[1].Remarks, configs[2].Remarks,
	})

	for i, conn := range configs {
		require.Len(t, conn.Outbounds, 3, "each profile carries exactly one proxy outbound")
		require.Equal(t, conn.Remarks, conn.Outbounds[0].Tag,
			"profile %d: proxy outbound tag must match its remarks", i)
		require.Equal(t, conn.Remarks, conn.Routing.Rules[len(conn.Routing.Rules)-1].OutboundTag,
			"profile %d: catch-all rule must target its own node", i)
	}

	require.Equal(t, "one.example", proxyAddress(t, result.Body, 0))
	require.Equal(t, "two.example", proxyAddress(t, result.Body, 1))
	require.Equal(t, "three.example", proxyAddress(t, result.Body, 2))
}

// TestTagProxyOutboundsReservesDirectAndBlock guards the reserved tags: a node
// remarked "direct" or "block" must never keep that tag, otherwise it collides
// with the freedom/blackhole outbounds renderXrayConnection always adds and the
// routing rules resolve to the wrong outbound.
func TestTagProxyOutboundsReservesDirectAndBlock(t *testing.T) {
	t.Parallel()

	outbounds := []json.RawMessage{
		json.RawMessage(`{"protocol":"vless","tag":"direct"}`),
		json.RawMessage(`{"protocol":"vless","tag":"block"}`),
		json.RawMessage(`{"protocol":"vless","tag":"node-one"}`),
		json.RawMessage(`{"protocol":"vless","tag":"proxy-2"}`),
	}
	require.NoError(t, tagProxyOutbounds(outbounds))

	got := make([]string, 0, len(outbounds))
	for _, raw := range outbounds {
		got = append(got, outboundTag(raw))
	}
	// proxy-2 is claimed by the second fallback, so the outbound that literally
	// asks for "proxy-2" afterwards must be pushed further down the sequence.
	require.Equal(t, []string{"proxy-1", "proxy-2", "node-one", "proxy-4"}, got)

	unique := make(map[string]struct{}, len(got))
	for _, tag := range got {
		_, dup := unique[tag]
		require.False(t, dup, "tag %q assigned twice", tag)
		unique[tag] = struct{}{}
	}

	require.NotEqual(t, "direct", got[0], "reserved direct tag must not reach a proxy outbound")
	require.NotEqual(t, "block", got[1], "reserved block tag must not reach a proxy outbound")

	for _, reserved := range reservedOutboundTags {
		for i, tag := range got {
			require.NotEqual(t, reserved, tag, "outbound %d must not use reserved tag %q", i, reserved)
		}
	}
}

// TestBuildResponseRemarksCollidingWithReservedTags checks the rendered profiles
// end-to-end: nodes named "direct"/"block" get distinct fallback tags, while the
// direct/block outbounds and their routing rules stay intact.
func TestBuildResponseRemarksCollidingWithReservedTags(t *testing.T) {
	t.Parallel()

	result, err := buildResponse(newTestSubSvc(t), "reserved-tags", aggregatedSources{
		jsonConfigs: []json.RawMessage{
			json.RawMessage(`{"type":"vless","address":"direct.example","port":443,"uuid":"id-1","remark":"direct"}`),
			json.RawMessage(`{"type":"vless","address":"block.example","port":443,"uuid":"id-2","remark":"block"}`),
		},
	}, 0, "")
	require.NoError(t, err)

	configs := decodeSubscription(t, result.Body)
	require.Len(t, configs, 2)

	for i, conn := range configs {
		require.Len(t, conn.Outbounds, 3)
		proxyTag := conn.Outbounds[0].Tag
		require.NotEmpty(t, proxyTag)
		require.NotEqual(t, "direct", proxyTag, "profile %d: proxy tag collides with the direct outbound", i)
		require.NotEqual(t, "block", proxyTag, "profile %d: proxy tag collides with the block outbound", i)
		require.Equal(t, "direct", conn.Outbounds[1].Tag)
		require.Equal(t, "block", conn.Outbounds[2].Tag)
		require.Equal(t, conn.Remarks, proxyTag, "profile %d: profile name must match its proxy tag", i)
		require.Equal(t, proxyTag, conn.Routing.Rules[len(conn.Routing.Rules)-1].OutboundTag,
			"profile %d: catch-all rule is renamed after the collision", i)
		require.Equal(t, "block", conn.Routing.Rules[0].OutboundTag)
	}

	require.NotEqual(t, configs[0].Remarks, configs[1].Remarks, "fallback tags must stay unique")
	require.NotEqual(t, configs[0].Outbounds[0].Tag, configs[1].Outbounds[0].Tag)
}

// proxyAddress returns the address of the proxy outbound in profile index.
func proxyAddress(t *testing.T, body []byte, index int) string {
	t.Helper()

	var configs []struct {
		Outbounds []struct {
			Settings struct {
				VNext []struct {
					Address string `json:"address"`
				} `json:"vnext"`
				Servers []struct {
					Address string `json:"address"`
				} `json:"servers"`
			} `json:"settings"`
		} `json:"outbounds"`
	}
	require.NoError(t, json.Unmarshal(body, &configs))
	require.Greater(t, len(configs), index)

	outbound := configs[index].Outbounds[0]
	if len(outbound.Settings.VNext) > 0 {
		return outbound.Settings.VNext[0].Address
	}
	if len(outbound.Settings.Servers) > 0 {
		return outbound.Settings.Servers[0].Address
	}

	return ""
}

func TestBuildResponseRejectsUnsupportedOnlySources(t *testing.T) {
	t.Parallel()

	svc := newTestSubSvc(t)
	_, err := buildResponse(svc, "unsupported", aggregatedSources{
		jsonConfigs: []json.RawMessage{json.RawMessage(`{"type":"tuic","address":"vpn.example","port":443,"uuid":"id","password":"secret"}`)},
	}, 0, "")
	require.ErrorIs(t, err, ErrNoSubscriptionItems)
	_, _, cached := svc.GetCache("unsupported")
	require.False(t, cached)
}

func TestParseShareLink(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		link     string
		wantType string
		wantAddr string
		wantPort int
		wantUUID string
	}{
		{
			name:     "vless reality",
			link:     "vless://uuid-1@reality.example:443?security=reality&sni=front.example&pbk=public-key&sid=abcd&fp=chrome&type=tcp#Reality",
			wantType: "vless",
			wantAddr: "reality.example",
			wantPort: 443,
			wantUUID: "uuid-1",
		},
		{
			name:     "trojan tls",
			link:     "trojan://secret@trojan.example:443?security=tls&sni=front.example&type=tcp#Trojan",
			wantType: "trojan",
			wantAddr: "trojan.example",
			wantPort: 443,
		},
		{
			name:     "hysteria2",
			link:     "hysteria2://auth-token@hy.example:8443?sni=front.example&obfs=salamander&obfs-password=pass#HY2",
			wantType: "hysteria2",
			wantAddr: "hy.example",
			wantPort: 8443,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg, err := parseShareLink(tt.link)
			require.NoError(t, err)
			require.Equal(t, tt.wantType, cfg.Type)
			require.Equal(t, tt.wantAddr, cfg.Address)
			require.Equal(t, tt.wantPort, cfg.Port)
			if tt.wantUUID != "" {
				require.Equal(t, tt.wantUUID, cfg.UUID)
			}
		})
	}
}

func TestParseShareLinkRejectsMalformedOrUnsupported(t *testing.T) {
	t.Parallel()

	for _, link := range []string{
		"://not-a-url",
		"tuic://uuid@vpn.example:443?congestion_control=bbr#TUIC",
		"vless://@:0#Broken",
		"vless://uuid@vpn.example:65536#TooBig",
		"trojan://secret@trojan.example:99999999#TooBig",
	} {
		_, err := parseShareLink(link)
		require.Error(t, err, "link %q must be rejected", link)
	}
}

func TestBuildResponseConvertsVMessLink(t *testing.T) {
	t.Parallel()
	link := "vmess://" + base64.StdEncoding.EncodeToString([]byte(`{"add":"vmess.example","port":"443","id":"id","aid":"0","net":"tcp","ps":"VMess"}`))
	result, err := buildResponse(newTestSubSvc(t), "vmess", aggregatedSources{items: []string{link}}, 0, "")
	require.NoError(t, err)

	var configs []struct {
		Remarks   string `json:"remarks"`
		Outbounds []struct {
			Protocol string `json:"protocol"`
			Settings struct {
				VNext []struct {
					Address string `json:"address"`
					Port    int    `json:"port"`
				} `json:"vnext"`
			} `json:"settings"`
		} `json:"outbounds"`
	}
	require.NoError(t, json.Unmarshal(result.Body, &configs))
	require.Len(t, configs, 1)
	require.Equal(t, "VMess", configs[0].Remarks)
	require.Equal(t, "vmess", configs[0].Outbounds[0].Protocol)
	require.Equal(t, "vmess.example", configs[0].Outbounds[0].Settings.VNext[0].Address)
	require.Equal(t, 443, configs[0].Outbounds[0].Settings.VNext[0].Port)
}

func TestBuildResponseConvertsVMessNumericFields(t *testing.T) {
	t.Parallel()
	link := "vmess://" + base64.StdEncoding.EncodeToString([]byte(`{"add":"vmess.example","port":443,"id":"id","aid":2,"net":"tcp"}`))
	result, err := buildResponse(newTestSubSvc(t), "vmess-numeric", aggregatedSources{items: []string{link}}, 0, "")
	require.NoError(t, err)
	var configs []struct {
		Outbounds []struct {
			Protocol string `json:"protocol"`
			Settings struct {
				VNext []struct {
					Port  int `json:"port"`
					Users []struct {
						AlterID int `json:"alterId"`
					} `json:"users"`
				} `json:"vnext"`
			} `json:"settings"`
		} `json:"outbounds"`
	}
	require.NoError(t, json.Unmarshal(result.Body, &configs))
	require.Len(t, configs, 1)
	require.Equal(t, "vmess", configs[0].Outbounds[0].Protocol)
	require.Equal(t, 443, configs[0].Outbounds[0].Settings.VNext[0].Port)
	require.Equal(t, 2, configs[0].Outbounds[0].Settings.VNext[0].Users[0].AlterID)
}

func TestParseVMessItemDispatchesPayloadShape(t *testing.T) {
	t.Parallel()

	t.Run("url form", func(t *testing.T) {
		t.Parallel()
		cfg, err := parseVMessItem("vmess://uuid-1@vmess.example:8443?security=tls&type=ws&path=%2Fws#URL-Node")
		require.NoError(t, err)
		require.Equal(t, "vmess", cfg.Type)
		require.Equal(t, "vmess.example", cfg.Address)
		require.Equal(t, 8443, cfg.Port)
		require.Equal(t, "uuid-1", cfg.UUID)
		require.Equal(t, "URL-Node", cfg.Remark)
	})

	t.Run("base64 json form", func(t *testing.T) {
		t.Parallel()
		link := "vmess://" + base64.StdEncoding.EncodeToString([]byte(`{"add":"vmess.example","port":443,"id":"id","aid":0,"ps":"JSON-Node"}`))
		cfg, err := parseVMessItem(link)
		require.NoError(t, err)
		require.Equal(t, "vmess", cfg.Type)
		require.Equal(t, "vmess.example", cfg.Address)
		require.Equal(t, 443, cfg.Port)
		require.Equal(t, "JSON-Node", cfg.Remark)
	})

	t.Run("url form without port is rejected", func(t *testing.T) {
		t.Parallel()
		_, err := parseVMessItem("vmess://uuid-1@vmess.example#NoPort")
		require.Error(t, err)
	})
}

func TestBuildResponseConvertsURLStyleVMessLink(t *testing.T) {
	t.Parallel()
	link := "vmess://uuid-url@vmess.example:8443?security=tls&sni=front.example&type=ws&path=%2Fws#URLVMess"
	result, err := buildResponse(newTestSubSvc(t), "vmess-url", aggregatedSources{items: []string{link}}, 0, "")
	require.NoError(t, err)

	var configs []struct {
		Remarks   string `json:"remarks"`
		Outbounds []struct {
			Protocol string `json:"protocol"`
			Settings struct {
				VNext []struct {
					Address string `json:"address"`
					Port    int    `json:"port"`
					Users   []struct {
						ID string `json:"id"`
					} `json:"users"`
				} `json:"vnext"`
			} `json:"settings"`
		} `json:"outbounds"`
	}
	require.NoError(t, json.Unmarshal(result.Body, &configs))
	require.Len(t, configs, 1)
	require.Equal(t, "URLVMess", configs[0].Remarks)
	require.Equal(t, "vmess", configs[0].Outbounds[0].Protocol)
	require.Equal(t, "vmess.example", configs[0].Outbounds[0].Settings.VNext[0].Address)
	require.Equal(t, 8443, configs[0].Outbounds[0].Settings.VNext[0].Port)
	require.Equal(t, "uuid-url", configs[0].Outbounds[0].Settings.VNext[0].Users[0].ID)
}

func TestParseVMessLinkPreservesCase(t *testing.T) {
	t.Parallel()

	// The Base64 payload and its fields are case-sensitive; lower-casing them
	// corrupts the UUID, host and remark, and the connection silently breaks.
	payload := `{"add":"VMess.Example","port":443,"id":"AbCdEf12-3456","aid":0,"net":"ws","host":"MIXED.Example","ps":"Node-DE"}`
	link := "vmess://" + base64.StdEncoding.EncodeToString([]byte(payload))

	cfg, err := parseVMessLink(link)
	require.NoError(t, err)
	require.Equal(t, "VMess.Example", cfg.Address)
	require.Equal(t, "AbCdEf12-3456", cfg.UUID)
	require.Equal(t, "MIXED.Example", cfg.Host)
	require.Equal(t, "Node-DE", cfg.Remark)

	upperScheme, err := parseVMessLink("VMESS://" + base64.StdEncoding.EncodeToString([]byte(payload)))
	require.NoError(t, err, "the scheme must be matched case-insensitively")
	require.Equal(t, cfg.UUID, upperScheme.UUID)

	_, err = parseVMessLink("vless://" + base64.StdEncoding.EncodeToString([]byte(payload)))
	require.Error(t, err, "a non-vmess scheme must be rejected")

	_, err = parseVMessLink("vmess://" + base64.StdEncoding.EncodeToString([]byte(`{"add":"","port":443,"id":"id"}`)))
	require.Error(t, err, "an empty address must be rejected")
}

func TestXrayTemplateRoutingAndDNS(t *testing.T) {
	t.Parallel()
	result, err := buildResponse(newTestSubSvc(t), "template", aggregatedSources{jsonConfigs: []json.RawMessage{json.RawMessage(`{"type":"vless","address":"node.example","port":443,"uuid":"id","remark":"node-one"}`)}}, 0, "")
	require.NoError(t, err)

	configs := decodeSubscription(t, result.Body)
	require.Len(t, configs, 1)
	config := configs[0]

	require.Len(t, config.DNS.Servers, 2)
	require.Equal(t, "IPIfNonMatch", config.Routing.DomainStrategy)
	require.Empty(t, config.Routing.Balancers, "no balancer: the default route must stay on one predictable outbound")
	require.Len(t, config.Routing.Rules, 5)
	require.Equal(t, "block", config.Routing.Rules[0].OutboundTag)
	require.Contains(t, config.Routing.Rules[2].IP, "geoip:ru")
	require.Equal(t, "direct", config.Routing.Rules[2].OutboundTag)
	require.Equal(t, "node-one", config.Routing.Rules[4].OutboundTag)
	require.Empty(t, config.Routing.Rules[4].BalancerTag)

	require.Len(t, config.Inbounds, 2, "each profile must expose local socks/http inbounds")
	require.Equal(t, "socks", config.Inbounds[0].Protocol)
	require.Equal(t, "http", config.Inbounds[1].Protocol)
}

func TestBuildResponseReservesDirectAndBlockTags(t *testing.T) {
	t.Parallel()

	result, err := buildResponse(newTestSubSvc(t), "reserved-tags", aggregatedSources{
		jsonConfigs: []json.RawMessage{
			json.RawMessage(`{"type":"vless","address":"one.example","port":443,"uuid":"id-1","remark":"proxy-2"}`),
			json.RawMessage(`{"type":"vless","address":"two.example","port":443,"uuid":"id-2","remark":"direct"}`),
			json.RawMessage(`{"type":"vless","address":"three.example","port":443,"uuid":"id-3","remark":"block"}`),
		},
	}, 0, "")
	require.NoError(t, err)

	configs := decodeSubscription(t, result.Body)
	require.Len(t, configs, 3)
	used := map[string]bool{"direct": true, "block": true}
	for _, conn := range configs {
		require.Len(t, conn.Outbounds, 3)
		tag := conn.Outbounds[0].Tag
		require.NotEmpty(t, tag)
		require.False(t, used[tag], "proxy tag must be unique and unreserved: %s", tag)
		used[tag] = true
		require.Equal(t, tag, conn.Remarks)
		require.Equal(t, tag, conn.Routing.Rules[len(conn.Routing.Rules)-1].OutboundTag)
		require.Equal(t, "direct", conn.Outbounds[1].Tag)
		require.Equal(t, "block", conn.Outbounds[2].Tag)
	}
}

func TestParseShareLinkShadowsocksMethodAndPassword(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		link         string
		wantType     string
		wantMethod   string
		wantPassword string
	}{
		{
			name:         "SIP002 base64url userinfo",
			link:         "ss://" + base64.RawURLEncoding.EncodeToString([]byte("chacha20-ietf-poly1305:ss-pass")) + "@ss.example:8388#SS-Node",
			wantType:     "ss",
			wantMethod:   "chacha20-ietf-poly1305",
			wantPassword: "ss-pass",
		},
		{
			name:         "padded standard base64 userinfo",
			link:         "ss://" + base64.StdEncoding.EncodeToString([]byte("aes-256-gcm:padded-pass")) + "@ss.example:8388#SS-Node",
			wantType:     "ss",
			wantMethod:   "aes-256-gcm",
			wantPassword: "padded-pass",
		},
		{
			name:         "plain method:password userinfo",
			link:         "ss://aes-128-gcm:plain-pass@ss.example:8388#SS-Node",
			wantType:     "ss",
			wantMethod:   "aes-128-gcm",
			wantPassword: "plain-pass",
		},
		{
			name:         "shadowsocks scheme alias",
			link:         "shadowsocks://2022-blake3-aes-128-gcm:alias-pass@ss.example:8388#SS-Node",
			wantType:     "shadowsocks",
			wantMethod:   "2022-blake3-aes-128-gcm",
			wantPassword: "alias-pass",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg, err := parseShareLink(tt.link)
			require.NoError(t, err)
			require.Equal(t, tt.wantType, cfg.Type)
			require.Equal(t, tt.wantMethod, cfg.Method)
			require.Equal(t, tt.wantPassword, cfg.Password)
		})
	}
}

func TestBuildResponseShadowsocksOutboundHasMethod(t *testing.T) {
	t.Parallel()

	links := []string{
		"ss://" + base64.RawURLEncoding.EncodeToString([]byte("chacha20-ietf-poly1305:ss-pass")) + "@ss.example:8388#SS-B64",
		"ss://aes-256-gcm:plain-pass@plain.example:8389#SS-Plain",
	}
	result, err := buildResponse(newTestSubSvc(t), "ss-method", aggregatedSources{items: links}, 0, "")
	require.NoError(t, err)

	var configs []struct {
		Outbounds []struct {
			Protocol string `json:"protocol"`
			Settings struct {
				Servers []struct {
					Address  string `json:"address"`
					Port     int    `json:"port"`
					Method   string `json:"method"`
					Password string `json:"password"`
				} `json:"servers"`
			} `json:"settings"`
		} `json:"outbounds"`
	}
	require.NoError(t, json.Unmarshal(result.Body, &configs))
	require.Len(t, configs, 2)

	for i, want := range []struct {
		address  string
		method   string
		password string
	}{
		{"ss.example", "chacha20-ietf-poly1305", "ss-pass"},
		{"plain.example", "aes-256-gcm", "plain-pass"},
	} {
		outbound := configs[i].Outbounds[0]
		require.Equal(t, "shadowsocks", outbound.Protocol)
		server := outbound.Settings.Servers[0]
		require.Equal(t, want.address, server.Address)
		require.Equal(t, want.method, server.Method)
		require.Equal(t, want.password, server.Password)
	}
}

func TestBuildXrayOutboundRejectsShadowsocksWithoutMethod(t *testing.T) {
	t.Parallel()

	_, err := buildXrayOutbound(&serverConfig{Type: "ss", Address: "ss.example", Port: 8388, Password: "pass"})
	require.Error(t, err, "an outbound without a cipher is unusable and must be rejected")
}
