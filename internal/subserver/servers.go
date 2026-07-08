package subserver

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/kereal/rs8kvn_bot/internal/logger"
	"github.com/kereal/rs8kvn_bot/internal/utils"

	"go.uber.org/zap"
)

// validSchemes lists all proxy URI schemes recognised by isValidServer.
var validSchemes = []string{
	"vless://",
	"vmess://",
	"trojan://",
	"ss://",
	"ssr://",
	"hysteria://",
	"hysteria2://",
	"hy2://",
	"tuic://",
	"wg://",
	"wireguard://",
}

// isValidServer checks whether line starts with a recognised proxy URI scheme.
func isValidServer(line string) bool {
	lower := strings.ToLower(line)
	for _, scheme := range validSchemes {
		if strings.HasPrefix(lower, scheme) {
			return true
		}
	}
	return false
}

// serverConfig holds a parsed 3x-ui JSON server configuration.
// Fields cover VLESS, VMess, Trojan, Shadowsocks, Hysteria, TUIC protocols.
type serverConfig struct {
	Type          string `json:"type"`
	Address       string `json:"address"`
	Port          int    `json:"port"`
	UUID          string `json:"uuid"`
	UserID        string `json:"userId"`
	Password      string `json:"password"`
	Flow          string `json:"flow"`
	Encryption    string `json:"encryption"`
	Security      string `json:"security"`
	SNI           string `json:"sni"`
	Aid           string `json:"aid"`
	Fingerprint   string `json:"fp"`
	PublicKey     string `json:"pbk"`
	ShortID       string `json:"sid"`
	Network       string `json:"network"`
	Mode          string `json:"mode"`
	Tag           string `json:"tag"`
	Remark        string `json:"remark"`
	Ps            string `json:"ps"`
	Scy           string `json:"scy"`
	Host          string `json:"host"`
	Path          string `json:"path"`
	TLS           string `json:"tls"`
	AllowInsecure bool   `json:"allowInsecure"`
	Alpn          string `json:"alpn"`
	HeaderType    string `json:"headerType"`
	PortNumber    int    `json:"portNumber"`
	Method        string `json:"method"`
	Plugin        string `json:"plugin"`
	PluginOpts    string `json:"pluginOpts"`
	Key           string `json:"key"`
	Crypt         string `json:"crypt"`
	Obfs          string `json:"obfs"`
	ObfsPassword  string `json:"obfsPassword"`
	Protocol      string `json:"protocol"`
	ProtocolParam string `json:"protocolParam"`
	ObfsParam     string `json:"obfsParam"`
}

// toServerConfig unmarshals raw JSON into a serverConfig and normalises
// field aliases (address←host, port←portNumber, uuid←userId, remark←tag).
func toServerConfig(raw json.RawMessage) (*serverConfig, error) {
	var cfg serverConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		logger.Error("Failed to parse server config JSON",
			zap.Error(err),
			zap.String("raw_preview", utils.TruncateString(string(raw), 200)))
		return nil, err
	}
	if cfg.Address == "" {
		cfg.Address = cfg.Host
	}
	if cfg.Port == 0 {
		cfg.Port = cfg.PortNumber
	}
	if cfg.UserID != "" && cfg.UUID == "" {
		cfg.UUID = cfg.UserID
	}
	if cfg.Tag != "" && cfg.Remark == "" {
		cfg.Remark = cfg.Tag
	}
	return &cfg, nil
}

// ExtractJSONConfigs parses a JSON object or array of server configs from body.
// It returns the raw config objects as json.RawMessage slice, avoiding the
// reflect/unmarshal round-trip that the old implementation performed on every
// array element. Arrays are unmarshaled directly into []json.RawMessage, which
// preserves the original bytes and reuses the backing array, while single
// objects are wrapped in a one-element slice.
func ExtractJSONConfigs(body []byte) ([]json.RawMessage, error) {
	var items []json.RawMessage
	if err := json.Unmarshal(body, &items); err != nil {
		if len(body) > 0 && body[0] == '{' {
			items = make([]json.RawMessage, 0, 1)
			items = append(items, body)
			return items, nil
		}
		logger.Error("Failed to extract JSON configs",
			zap.Error(err),
			zap.String("body_preview", utils.TruncateString(string(body), 200)))
		return nil, fmt.Errorf("extract JSON configs: %w", err)
	}
	return items, nil
}

// ConvertSingleJSONToLink converts a single raw JSON server config into a share link
// by dispatching to the protocol-specific builder based on the "type" field.
func ConvertSingleJSONToLink(raw json.RawMessage) (string, error) {
	cfg, err := toServerConfig(raw)
	if err != nil {
		logger.Error("Failed to convert JSON config to share link",
			zap.Error(err),
			zap.String("raw_preview", utils.TruncateString(string(raw), 200)))
		return "", err
	}

	switch strings.ToLower(cfg.Type) {
	case "vless":
		return buildVLESSServerLink(cfg)
	case "vmess":
		return buildVMessServerLink(cfg)
	case "trojan":
		return buildTrojanServerLink(cfg)
	case "shadowsocks", "ss":
		return buildShadowsocksServerLink(cfg)
	case "hysteria", "hysteria2", "hy2":
		return buildHysteriaServerLink(cfg, cfg.Type)
	case "tuic":
		return buildTUICServerLink(cfg)
	default:
		return "", fmt.Errorf("unsupported protocol: %s", cfg.Type)
	}
}

// buildVLESSServerLink builds a vless:// share URI from a parsed server config.
func buildVLESSServerLink(cfg *serverConfig) (string, error) {
	params := url.Values{}
	encryption := cfg.Encryption
	if encryption == "" {
		encryption = "none"
	}
	params.Set("encryption", encryption)
	if cfg.Flow != "" {
		params.Set("flow", cfg.Flow)
	}
	if cfg.Security != "" {
		params.Set("security", cfg.Security)
	} else if cfg.TLS != "" {
		params.Set("security", cfg.TLS)
	} else {
		params.Set("security", "none")
	}
	if cfg.SNI != "" {
		params.Set("sni", cfg.SNI)
	}
	if cfg.Fingerprint != "" {
		params.Set("fp", cfg.Fingerprint)
	}
	if cfg.PublicKey != "" {
		params.Set("pbk", cfg.PublicKey)
	}
	if cfg.ShortID != "" {
		params.Set("sid", cfg.ShortID)
	}
	if cfg.Network != "" {
		netType := cfg.Network
		// Legacy "splithttp" was renamed to "xhttp" in v2rayN 7.x / Xray-core.
		if netType == "splithttp" {
			netType = "xhttp"
		}
		params.Set("type", netType)
		if netType == "grpc" && cfg.Path != "" {
			// gRPC transport uses serviceName, not path, in share links.
			params.Set("serviceName", cfg.Path)
		}
		if netType == "xhttp" && cfg.Mode != "" {
			params.Set("mode", cfg.Mode)
		}
	}
	if cfg.Host != "" {
		params.Set("host", cfg.Host)
	}
	if cfg.Path != "" && cfg.Network != "grpc" {
		params.Set("path", cfg.Path)
	}
	if cfg.Alpn != "" {
		params.Set("alpn", cfg.Alpn)
	}
	if cfg.HeaderType != "" {
		params.Set("headerType", cfg.HeaderType)
	}

	remark := cfg.Remark
	if remark == "" {
		remark = cfg.Ps
	}

	return fmt.Sprintf("vless://%s@%s?%s#%s", cfg.UUID, net.JoinHostPort(cfg.Address, strconv.Itoa(cfg.Port)), params.Encode(), url.QueryEscape(remark)), nil
}

// buildVMessServerLink builds a vmess:// share URI (base64-encoded JSON object) from a parsed server config.
func buildVMessServerLink(cfg *serverConfig) (string, error) {
	type vmessObj struct {
		V    string `json:"v"`
		PS   string `json:"ps"`
		Add  string `json:"add"`
		Port string `json:"port"`
		ID   string `json:"id"`
		Aid  string `json:"aid"`
		Scy  string `json:"scy"`
		Net  string `json:"net"`
		Type string `json:"type"`
		Host string `json:"host"`
		Path string `json:"path"`
		TLS  string `json:"tls"`
		SNI  string `json:"sni"`
		ALPN string `json:"alpn"`
		FP   string `json:"fp"`
	}

	obj := vmessObj{
		V:    "2",
		PS:   cfg.Remark,
		Add:  cfg.Address,
		Port: strconv.Itoa(cfg.Port),
		ID:   cfg.UUID,
		Aid:  cfg.Aid,
		Scy:  cfg.Scy,
		Net:  cfg.Network,
		Type: cfg.HeaderType,
		Host: cfg.Host,
		Path: cfg.Path,
		TLS:  cfg.TLS,
		SNI:  cfg.SNI,
		ALPN: cfg.Alpn,
		FP:   cfg.Fingerprint,
	}

	if obj.Scy == "" {
		obj.Scy = "auto"
	}
	if obj.Aid == "" {
		obj.Aid = "0"
	}
	if obj.Net == "" {
		obj.Net = "tcp"
	}
	drop := func(field *string) {
		if *field == "" {
			*field = "__OMIT__"
		}
	}
	drop(&obj.TLS)
	drop(&obj.SNI)
	drop(&obj.ALPN)
	drop(&obj.FP)
	drop(&obj.Host)
	drop(&obj.Path)
	drop(&obj.Type)

	data, err := json.Marshal(obj)
	if err != nil {
		return "", fmt.Errorf("marshal vmess object: %w", err)
	}
	encoded := base64.StdEncoding.EncodeToString(data)
	encoded = strings.ReplaceAll(encoded, "__OMIT__", "")
	return "vmess://" + encoded, nil
}

// buildTrojanServerLink builds a trojan:// share URI from a parsed server config.
func buildTrojanServerLink(cfg *serverConfig) (string, error) {
	params := url.Values{}
	if cfg.SNI != "" {
		params.Set("sni", cfg.SNI)
	}
	if cfg.Network != "" {
		params.Set("type", cfg.Network)
		if cfg.Network == "grpc" && cfg.Path != "" {
			params.Set("serviceName", cfg.Path)
		}
	}
	if cfg.Host != "" {
		params.Set("host", cfg.Host)
	}
	if cfg.Path != "" && cfg.Network != "grpc" {
		params.Set("path", cfg.Path)
	}
	if cfg.TLS != "" {
		params.Set("security", cfg.TLS)
	}
	if cfg.Fingerprint != "" {
		params.Set("fp", cfg.Fingerprint)
	}
	if cfg.Alpn != "" {
		params.Set("alpn", cfg.Alpn)
	}
	if cfg.AllowInsecure {
		params.Set("allowInsecure", "1")
	}

	password := cfg.Password
	if password == "" {
		password = cfg.UUID
	}

	remark := cfg.Remark
	if remark == "" {
		remark = cfg.Ps
	}

	base := fmt.Sprintf("trojan://%s@%s", password, net.JoinHostPort(cfg.Address, strconv.Itoa(cfg.Port)))
	if params.Encode() != "" {
		base += "?" + params.Encode()
	}
	base += "#" + url.QueryEscape(remark)
	return base, nil
}

// buildShadowsocksServerLink builds an ss:// share URI in SIP002 form:
// ss://base64url(method:password)@host:port#tag (host:port in cleartext, no padding).
func buildShadowsocksServerLink(cfg *serverConfig) (string, error) {
	userInfo := base64.RawURLEncoding.EncodeToString([]byte(cfg.Method + ":" + cfg.Password))

	remark := cfg.Remark
	if remark == "" {
		remark = cfg.Ps
	}

	link := "ss://" + userInfo + "@" + net.JoinHostPort(cfg.Address, strconv.Itoa(cfg.Port))
	if cfg.PluginOpts != "" {
		link += "?plugin=" + url.QueryEscape(cfg.PluginOpts)
	}
	if remark != "" {
		link += "#" + url.QueryEscape(remark)
	}
	return link, nil
}

// buildHysteriaServerLink builds a hysteria:// or hysteria2:// share URI from a parsed server config.
func buildHysteriaServerLink(cfg *serverConfig, protocol string) (string, error) {
	params := url.Values{}
	if cfg.Host != "" {
		params.Set("sni", cfg.Host)
	}
	if cfg.SNI != "" {
		params.Set("sni", cfg.SNI)
	}
	if cfg.AllowInsecure {
		params.Set("insecure", "1")
	}
	if cfg.Fingerprint != "" {
		params.Set("fp", cfg.Fingerprint)
	}
	if cfg.Obfs != "" {
		params.Set("obfs", cfg.Obfs)
	}
	if cfg.ObfsPassword != "" {
		params.Set("obfs-password", cfg.ObfsPassword)
	}
	if cfg.Alpn != "" {
		params.Set("alpn", cfg.Alpn)
	}

	remark := cfg.Remark
	if remark == "" {
		remark = cfg.Ps
	}

	password := cfg.Password
	if password == "" {
		password = cfg.UUID
	}

	link := fmt.Sprintf("%s://%s@%s:%d", protocol, password, cfg.Address, cfg.Port)
	if params.Encode() != "" {
		link += "?" + params.Encode()
	}
	link += "#" + url.QueryEscape(remark)
	return link, nil
}

// buildTUICServerLink builds a tuic:// share URI. The uuid:password credential
// lives in the userinfo component (standard TUIC share-link form), not as query params.
func buildTUICServerLink(cfg *serverConfig) (string, error) {
	params := url.Values{}
	if cfg.Host != "" {
		params.Set("sni", cfg.Host)
	} else if cfg.SNI != "" {
		params.Set("sni", cfg.SNI)
	}
	if cfg.Alpn != "" {
		params.Set("alpn", cfg.Alpn)
	}
	if cfg.AllowInsecure {
		params.Set("allow_insecure", "1")
		params.Set("insecure", "1")
	}
	if cfg.Fingerprint != "" {
		params.Set("fp", cfg.Fingerprint)
	}

	remark := cfg.Remark
	if remark == "" {
		remark = cfg.Ps
	}

	userInfo := cfg.UUID
	if cfg.Password != "" {
		userInfo = userInfo + ":" + cfg.Password
	}

	base := fmt.Sprintf("tuic://%s@%s", userInfo, net.JoinHostPort(cfg.Address, strconv.Itoa(cfg.Port)))
	if params.Encode() != "" {
		base += "?" + params.Encode()
	}
	base += "#" + url.QueryEscape(remark)
	return base, nil
}
