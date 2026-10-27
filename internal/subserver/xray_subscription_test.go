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
	Remarks   string `json:"remarks"`
	Log       struct {
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
