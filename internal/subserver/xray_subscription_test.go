package subserver

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

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

			var config map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(result.Body, &config))
			var outbounds []map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(config["outbounds"], &outbounds))
			require.Len(t, outbounds, 3)
			require.Equal(t, tt.wantTag, rawString(t, outbounds[0]["tag"]))
			require.Equal(t, "direct", rawString(t, outbounds[1]["tag"]))
			require.Equal(t, "block", rawString(t, outbounds[2]["tag"]))

			var routing struct {
				Rules []struct {
					Network     string `json:"network"`
					OutboundTag string `json:"outboundTag"`
				} `json:"rules"`
			}
			require.NoError(t, json.Unmarshal(config["routing"], &routing))
			require.Equal(t, tt.wantTag, routing.Rules[len(routing.Rules)-1].OutboundTag,
				"default route must point at the first node's own tag")
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

func rawString(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var value string
	require.NoError(t, json.Unmarshal(raw, &value))
	return value
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
		name          string
		link          string
		wantType      string
		wantAddress   string
		wantPort      int
		wantPassword  string
		wantPublicKey string
	}{
		{name: "VLESS Reality WS", link: "vless://uuid@vpn.example:443?security=reality&sni=front.example&pbk=key&sid=abcd&type=ws&path=%2Fws#Node%20One", wantType: "vless", wantAddress: "vpn.example", wantPort: 443, wantPublicKey: "key"},
		{name: "Trojan TLS", link: "trojan://secret@vpn.example:8443?security=tls&sni=front.example#Trojan", wantType: "trojan", wantAddress: "vpn.example", wantPort: 8443, wantPassword: "secret"},
		{name: "Hysteria2 alias", link: "hy2://secret@vpn.example:443?sni=front.example&obfs=salamander&obfs-password=pass#Hy2", wantType: "hysteria2", wantAddress: "vpn.example", wantPort: 443, wantPassword: "secret"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := parseShareLink(tt.link)
			require.NoError(t, err)
			require.Equal(t, tt.wantType, cfg.Type)
			require.Equal(t, tt.wantAddress, cfg.Address)
			require.Equal(t, tt.wantPort, cfg.Port)
			if tt.wantPassword != "" {
				require.Equal(t, tt.wantPassword, cfg.Password)
			}
			if tt.wantPublicKey != "" {
				require.Equal(t, tt.wantPublicKey, cfg.PublicKey)
			}
		})
	}
}

func TestParseShareLinkRejectsMalformedOrUnsupported(t *testing.T) {
	t.Parallel()
	for _, link := range []string{"vless://uuid@vpn.example", "https://example.com:443", "vless://uuid@vpn.example:notaport"} {
		_, err := parseShareLink(link)
		require.Error(t, err, link)
	}
}

func TestBuildResponseConvertsVMessLink(t *testing.T) {
	t.Parallel()
	link := "vmess://" + base64.StdEncoding.EncodeToString([]byte(`{"v":"2","ps":"node","add":"vmess.example","port":"443","id":"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee","aid":"0","scy":"auto","net":"ws","host":"cdn.example","path":"/v2ray","tls":"tls","sni":"vmess.example"}`))
	result, err := buildResponse(newTestSubSvc(t), "vmess", aggregatedSources{items: []string{link}}, 0, "")
	require.NoError(t, err)
	var config struct {
		Outbounds []struct {
			Protocol string `json:"protocol"`
			Tag      string `json:"tag"`
			Settings struct {
				VNext []struct {
					Address string `json:"address"`
					Port    int    `json:"port"`
				} `json:"vnext"`
			} `json:"settings"`
		} `json:"outbounds"`
	}
	require.NoError(t, json.Unmarshal(result.Body, &config))
	require.Equal(t, "vmess", config.Outbounds[0].Protocol)
	require.Equal(t, "vmess.example", config.Outbounds[0].Settings.VNext[0].Address)
	require.Equal(t, 443, config.Outbounds[0].Settings.VNext[0].Port)
}

func TestBuildResponseConvertsVMessNumericFields(t *testing.T) {
	t.Parallel()
	link := "vmess://" + base64.StdEncoding.EncodeToString([]byte(`{"add":"vmess.example","port":443,"id":"id","aid":2,"net":"tcp"}`))
	result, err := buildResponse(newTestSubSvc(t), "vmess-numeric", aggregatedSources{items: []string{link}}, 0, "")
	require.NoError(t, err)
	var config struct {
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
	require.NoError(t, json.Unmarshal(result.Body, &config))
	require.Equal(t, "vmess", config.Outbounds[0].Protocol)
	require.Equal(t, 443, config.Outbounds[0].Settings.VNext[0].Port)
	require.Equal(t, 2, config.Outbounds[0].Settings.VNext[0].Users[0].AlterID)
}

func TestXrayTemplateRoutingAndDNS(t *testing.T) {
	t.Parallel()
	result, err := buildResponse(newTestSubSvc(t), "template", aggregatedSources{jsonConfigs: []json.RawMessage{json.RawMessage(`{"type":"vless","address":"node.example","port":443,"uuid":"id","remark":"node-one"}`)}}, 0, "")
	require.NoError(t, err)
	var config struct {
		DNS struct {
			Servers []json.RawMessage `json:"servers"`
		} `json:"dns"`
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
	require.NoError(t, json.Unmarshal(result.Body, &config))
	require.Len(t, config.DNS.Servers, 2)
	require.Equal(t, "IPIfNonMatch", config.Routing.DomainStrategy)
	require.Empty(t, config.Routing.Balancers, "no balancer: the default route must stay on one predictable outbound")
	require.Len(t, config.Routing.Rules, 5)
	require.Equal(t, "block", config.Routing.Rules[0].OutboundTag)
	require.Contains(t, config.Routing.Rules[2].IP, "geoip:ru")
	require.Equal(t, "direct", config.Routing.Rules[2].OutboundTag)
	require.Equal(t, "node-one", config.Routing.Rules[4].OutboundTag)
	require.Empty(t, config.Routing.Rules[4].BalancerTag)
}
