package subserver

import "strings"

// OutputFormat is the serialization format of the delivered subscription body.
// The same aggregated, normalized node configs are rendered into whichever
// format the requesting client understands. OutputXray is the historical
// default and keeps existing clients (v2rayNG, etc.) unchanged.
type OutputFormat int

const (
	// OutputXray renders a JSON array of self-contained Xray client profiles
	// (the XTLS subscription standard the server has always served).
	OutputXray OutputFormat = iota
	// OutputSingbox renders a single sing-box JSON config whose "outbounds"
	// array carries one server outbound per node plus a "selector" group.
	OutputSingbox
	// OutputClash renders a Clash/Mihomo YAML config with one "proxies" entry
	// per node.
	OutputClash
)

// String returns the canonical cache-key token for the format.
func (f OutputFormat) String() string {
	switch f {
	case OutputSingbox:
		return "singbox"
	case OutputClash:
		return "clash"
	default:
		return "xray"
	}
}

// ContentType returns the HTTP Content-Type for the format's rendered body.
func (f OutputFormat) ContentType() string {
	switch f {
	case OutputClash:
		return "text/yaml; charset=utf-8"
	default:
		return "application/json; charset=utf-8"
	}
}

// DetectOutputFormat picks the serialization format for a subscription request.
// A trimmed, case-insensitive sing-box or Clash alias in formatParam (from
// ?format=) overrides the User-Agent. Other values, including "xray", fall back
// to case-insensitive User-Agent detection. If neither input selects sing-box
// or Clash, it returns OutputXray.
func DetectOutputFormat(formatParam, ua string) OutputFormat {
	switch strings.ToLower(strings.TrimSpace(formatParam)) {
	case "singbox", "sing-box", "sing_box", "sb":
		return OutputSingbox
	case "clash", "mihomo", "meta", "clash-meta":
		return OutputClash
	}

	u := strings.ToLower(ua)
	switch {
	case strings.Contains(u, "sing-box"), strings.Contains(u, "singbox"):
		return OutputSingbox
	case strings.Contains(u, "clash"), strings.Contains(u, "mihomo"), strings.Contains(u, "stash"):
		return OutputClash
	}

	return OutputXray
}
