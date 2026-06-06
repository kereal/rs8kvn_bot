package subserver

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
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
// Fields cover VLESS, VMess, Trojan, Shadowsocks, SOCKS, Hysteria, TUIC protocols.
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
	Fingerprint   string `json:"fp"`
	PublicKey     string `json:"pbk"`
	ShortID       string `json:"sid"`
	Network       string `json:"network"`
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

// ConvertJSONToShareLinks parses a JSON object or array of server configs
// from body and returns a slice of share-link URIs (vless://, vmess://, etc.).
// Unrecognised or invalid entries are silently skipped.
func ConvertJSONToShareLinks(body []byte) ([]string, error) {
	var raw interface{}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}

	var items []json.RawMessage
	switch v := raw.(type) {
	case []interface{}:
		for _, item := range v {
			rawItem, _ := json.Marshal(item)
			items = append(items, rawItem)
		}
	case map[string]interface{}:
		rawMarshalled, _ := json.Marshal(v)
		items = append(items, rawMarshalled)
	default:
		return nil, fmt.Errorf("unexpected JSON type: %T", raw)
	}

	var links []string
	for _, item := range items {
		link, err := ConvertSingleJSONToLink(item)
		if err != nil {
			continue
		}
		links = append(links, link)
	}
	return links, nil
}

// ExtractJSONConfigs parses a JSON object or array of server configs from body
// and returns the raw config objects as json.RawMessage slice.
// Unlike ConvertJSONToShareLinks it does NOT convert to share-link URIs.
func ExtractJSONConfigs(body []byte) ([]json.RawMessage, error) {
	var raw interface{}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}

	var items []json.RawMessage
	switch v := raw.(type) {
	case []interface{}:
		for _, item := range v {
			rawItem, _ := json.Marshal(item)
			items = append(items, rawItem)
		}
	case map[string]interface{}:
		rawMarshalled, _ := json.Marshal(v)
		items = append(items, rawMarshalled)
	default:
		return nil, fmt.Errorf("unexpected JSON type: %T", raw)
	}
	return items, nil
}

// ConvertSingleJSONToLink converts a single raw JSON server config into a share link
// by dispatching to the protocol-specific builder based on the "type" field.
func ConvertSingleJSONToLink(raw json.RawMessage) (string, error) {
	cfg, err := toServerConfig(raw)
	if err != nil {
		return "", fmt.Errorf("parse config: %w", err)
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
	case "socks", "socks5":
		return buildSOCKSServerLink(cfg)
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
	params.Set("encryption", cfg.Encryption)
	if cfg.Flow != "" {
		params.Set("flow", cfg.Flow)
	}
	if cfg.Security != "" {
		params.Set("security", cfg.Security)
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
		params.Set("type", cfg.Network)
	}
	if cfg.Host != "" {
		params.Set("host", cfg.Host)
	}
	if cfg.Path != "" {
		params.Set("path", cfg.Path)
	}
	if cfg.TLS != "" {
		params.Set("security", cfg.TLS)
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

	return fmt.Sprintf("vless://%s@%s:%d?%s#%s",
		cfg.UUID, cfg.Address, cfg.Port, params.Encode(), url.QueryEscape(remark)), nil
}

// buildVMessServerLink builds a vmess:// share URI (base64-encoded JSON object) from a parsed server config.
func buildVMessServerLink(cfg *serverConfig) (string, error) {
	vmessObj := map[string]interface{}{
		"v":    "2",
		"ps":   cfg.Remark,
		"add":  cfg.Address,
		"port": cfg.Port,
		"id":   cfg.UUID,
		"aid":  "0",
		"scy":  cfg.Scy,
		"net":  cfg.Network,
		"type": cfg.HeaderType,
		"host": cfg.Host,
		"path": cfg.Path,
		"tls":  cfg.TLS,
		"sni":  cfg.SNI,
		"alpn": cfg.Alpn,
		"fp":   cfg.Fingerprint,
	}

	if vmessObj["scy"].(string) == "" {
		vmessObj["scy"] = "auto"
	}
	if vmessObj["net"].(string) == "" {
		vmessObj["net"] = "tcp"
	}
	if vmessObj["tls"].(string) == "" {
		delete(vmessObj, "tls")
	}
	if vmessObj["sni"].(string) == "" {
		delete(vmessObj, "sni")
	}
	if vmessObj["alpn"].(string) == "" {
		delete(vmessObj, "alpn")
	}
	if vmessObj["fp"].(string) == "" {
		delete(vmessObj, "fp")
	}
	if vmessObj["host"].(string) == "" {
		delete(vmessObj, "host")
	}
	if vmessObj["path"].(string) == "" {
		delete(vmessObj, "path")
	}
	if vmessObj["type"].(string) == "" {
		delete(vmessObj, "type")
	}

	raw, _ := json.Marshal(vmessObj)
	return "vmess://" + base64StdEncode(raw), nil
}

// buildTrojanServerLink builds a trojan:// share URI from a parsed server config.
func buildTrojanServerLink(cfg *serverConfig) (string, error) {
	params := url.Values{}
	if cfg.SNI != "" {
		params.Set("sni", cfg.SNI)
	}
	if cfg.Network != "" {
		params.Set("type", cfg.Network)
	}
	if cfg.Host != "" {
		params.Set("host", cfg.Host)
	}
	if cfg.Path != "" {
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

	base := fmt.Sprintf("trojan://%s@%s:%d", password, cfg.Address, cfg.Port)
	if params.Encode() != "" {
		base += "?" + params.Encode()
	}
	base += "#" + url.QueryEscape(remark)
	return base, nil
}

// buildShadowsocksServerLink builds an ss:// share URI (base64-encoded method:password@host:port).
func buildShadowsocksServerLink(cfg *serverConfig) (string, error) {
	raw := cfg.Method + ":" + cfg.Password + "@" + cfg.Address + ":" + strconv.Itoa(cfg.Port)
	encoded := base64StdEncode([]byte(raw))

	remark := cfg.Remark
	if remark == "" {
		remark = cfg.Ps
	}

	link := "ss://" + encoded
	if remark != "" {
		link += "#" + url.QueryEscape(remark)
	}
	return link, nil
}

// buildSOCKSServerLink builds a socks:// share URI from a parsed server config.
func buildSOCKSServerLink(cfg *serverConfig) (string, error) {
	params := url.Values{}
	if cfg.Protocol != "" {
		params.Set("protocol", cfg.Protocol)
	}
	if cfg.Method != "" {
		params.Set("method", cfg.Method)
	}
	if cfg.Obfs != "" {
		params.Set("obfs", cfg.Obfs)
	}
	if cfg.ObfsPassword != "" {
		params.Set("obfs-password", cfg.ObfsPassword)
	}

	remark := cfg.Remark
	if remark == "" {
		remark = cfg.Ps
	}

	userInfo := cfg.ProtocolParam
	if userInfo == "" {
		userInfo = cfg.UUID
	}

	base := fmt.Sprintf("socks://%s@%s:%d", userInfo, cfg.Address, cfg.Port)
	if params.Encode() != "" {
		base += "?" + params.Encode()
	}
	base += "#" + url.QueryEscape(remark)
	return base, nil
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

// buildTUICServerLink builds a tuic:// share URI from a parsed server config.
func buildTUICServerLink(cfg *serverConfig) (string, error) {
	params := url.Values{}
	if cfg.UUID != "" {
		params.Set("uuid", cfg.UUID)
	}
	if cfg.Password != "" {
		params.Set("password", cfg.Password)
	}
	if cfg.Host != "" {
		params.Set("sni", cfg.Host)
	}

	remark := cfg.Remark
	if remark == "" {
		remark = cfg.Ps
	}

	base := fmt.Sprintf("tuic://%s:%d", cfg.Address, cfg.Port)
	if params.Encode() != "" {
		base += "?" + params.Encode()
	}
	base += "#" + url.QueryEscape(remark)
	return base, nil
}
