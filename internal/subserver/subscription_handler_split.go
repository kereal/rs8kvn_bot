package subserver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/interfaces"
	"github.com/kereal/rs8kvn_bot/internal/logger"
	"github.com/kereal/rs8kvn_bot/internal/metrics"

	"go.uber.org/zap"
	"gopkg.in/yaml.v3"
)

// serveFromCache attempts to serve a subscription response from cache.
// On a hit, it revalidates the subscription status in the DB and invalidates
// stale entries. Returns (result, hit, err): when hit is false, the caller
// should proceed to a cache-miss path.
func serveFromCache(ctx context.Context, db interfaces.SubscriptionRepository, subSvc *Service, subID, cacheKey string) (*SubscriptionResult, bool, error) {
	cachedBody, cachedHeaders, ok := subSvc.GetCache(cacheKey)
	if !ok {
		return nil, false, nil
	}

	// On cache hit, revalidate the subscription status in the DB.
	status, expiryTime, err := db.GetSubscriptionStatus(ctx, subID)
	if err != nil {
		if errors.Is(err, database.ErrSubscriptionNotFound) {
			subSvc.InvalidateCache(cacheKey)
			metrics.SubserverCacheInvalidationsTotal.WithLabelValues("not_found").Inc()

			return nil, true, ErrSubscriptionNotFound
		}
		// Fail closed when status cannot be revalidated. Serving stale access
		// after a revoke or chargeback is worse than a temporary 5xx.
		logger.Error("Cache status revalidation failed, refusing stale entry",
			zap.String("sub_id", subID),
			zap.Error(err))

		return nil, true, fmt.Errorf("revalidate subscription status: %w", err)
	}

	// If the subscription is no longer active or expired, invalidate the cache.
	if status != string(database.SubscriptionStatusActive) || (!expiryTime.IsZero() && time.Now().After(expiryTime)) {
		subSvc.InvalidateCache(cacheKey)

		invalidReason := string(database.SubscriptionStatusRevoked)
		if status == string(database.SubscriptionStatusActive) {
			invalidReason = "expired"
		}

		metrics.SubserverCacheInvalidationsTotal.WithLabelValues(invalidReason).Inc()
		logger.Warn("Cache invalidated: subscription no longer active",
			zap.String("sub_id", subID),
			zap.String("status", status),
			zap.Time("expires_at", expiryTime),
		)
		// A revoked/expired subscription must read as not found to clients
		// (matches the /sub/:id 404 contract), not as a server error.
		return nil, true, ErrSubscriptionNotFound
	}

	logger.Debug("Cache hit", zap.String("sub_id", subID))
	// best-effort: обновляем last_request, ошибки не блокируют выдачу.
	err = db.UpdateLastRequest(ctx, subID)
	if err != nil {
		logger.Warn("Failed to update last_request",
			zap.String("sub_id", subID),
			zap.Error(err))
	}

	return &SubscriptionResult{
		Body:    cachedBody,
		Headers: cachedHeaders,
	}, true, nil
}

// loadSubscription fetches the subscription with plan and nodes from the DB,
// records device/IP analytics, and updates last_request. Returns the full
// subscription or an error.
func loadSubscription(ctx context.Context, db interfaces.SubscriptionRepository, subSvc *Service, subID, clientIP string, requestHeaders map[string]string) (*database.SubscriptionFull, error) {
	// The analytics update is a read-modify-write on the subscription row
	// (devices/IPs JSON). The freshest row must be loaded AFTER taking the lock
	// so concurrent cache misses for the SAME subID serialize the whole
	// read-modify-write and cannot overwrite each other's entries; requests for
	// different subIDs proceed in parallel. The lock is released via defer, so
	// it is guaranteed to be freed on error or panic.
	unlock := subSvc.analyticsLocks.Lock(subID)
	defer unlock()
	subFull, err := db.GetWithPlanAndNodes(ctx, subID)
	if err != nil {
		if errors.Is(err, database.ErrSubscriptionNotFound) {
			logger.Debug("Subscription not found in database",
				zap.String("sub_id", subID))

			return nil, ErrSubscriptionNotFound
		}

		logger.Error("Failed to get subscription with plan and sources",
			zap.String("sub_id", subID),
			zap.Error(err))

		return nil, fmt.Errorf("database error: %w", err)
	}

	logger.Debug("Subscription loaded from database",
		zap.Uint("sub_pk", subFull.Subscription.ID),
		zap.String("status", subFull.Subscription.Status),
		zap.Timep("expires_at", subFull.Subscription.ExpiresAt),
		zap.Int64("plan_traffic_limit", subFull.Plan.TrafficLimit),
		zap.Int("nodes_count", len(subFull.Nodes)),
	)

	UpdateDevices(ctx, db, subFull, requestHeaders)
	UpdateIPs(ctx, db, subFull, clientIP)

	// best-effort: обновляем last_request, ошибки не блокируют выдачу.
	err = db.UpdateLastRequest(ctx, subID)
	if err != nil {
		logger.Warn("Failed to update last_request",
			zap.String("sub_id", subID),
			zap.Error(err))
	}

	return subFull, nil
}

// aggregatedSources holds the collected items and traffic data from all sources.
type aggregatedSources struct {
	items              []string
	jsonConfigs        []json.RawMessage
	firstExpire        string
	firstExpireVal     int64
	totalUpload        int64
	totalDownload      int64
	firstSourceHeaders map[string]string
}

// maxSourceConcurrency bounds how many upstream sources are fetched in parallel
// so a single slow/down node cannot be amplified into unbounded connection usage.
const maxSourceConcurrency = 8

// sourceResult holds the outcome of fetching a single upstream node.
type sourceResult struct {
	source  database.Node
	body    []byte
	headers map[string]string
	format  Format
}

// fetchAndAggregateSources fetches all active source nodes concurrently (bounded
// by maxSourceConcurrency) and aggregates their items and traffic counters. The
// per-source fetch is isolated: a failure logs and is skipped, never aborting the
// others. Results are merged in source order to keep header/expire selection
// deterministic.
func fetchAndAggregateSources(ctx context.Context, subID string, nodes []database.Node) (aggregatedSources, int, int) {
	agg := aggregatedSources{}

	results := make([]sourceResult, len(nodes))
	sem := make(chan struct{}, maxSourceConcurrency)

	var wg sync.WaitGroup

	for i := range nodes {
		src := nodes[i]
		if src.SubscriptionURL == "" {
			logger.Warn("Skipping node without subscription_url",
				zap.String("sub_id", subID),
				zap.String("source", src.Name),
			)

			continue
		}

		if ctx.Err() != nil {
			break
		}

		wg.Add(1)

		sem <- struct{}{}

		go func(idx int, src database.Node) {
			defer wg.Done()
			defer func() { <-sem }()

			results[idx] = fetchSource(ctx, subID, src)
		}(i, src)
	}

	wg.Wait()

	successCount := 0
	totalCount := 0

	for i := range nodes {
		if nodes[i].SubscriptionURL == "" {
			continue
		}

		totalCount++

		if results[i].body != nil {
			successCount++
		}
	}

	for i := range nodes {
		res := results[i]
		if res.body == nil {
			continue
		}
		// Capture headers from the first successful source for client replay.
		if agg.firstSourceHeaders == nil {
			agg.firstSourceHeaders = res.headers
		}

		updateMinExpire(&agg, res.headers)

		// Aggregate usage counters across all sources.
		agg.totalUpload += ParseUserInfoValue(res.headers, "upload")
		agg.totalDownload += ParseUserInfoValue(res.headers, "download")

		aggregateFormat(&agg, res.format, res.body, res.source, subID)
	}

	return agg, successCount, totalCount
}

// fetchSource performs a single upstream fetch, records metrics, and returns the
// parsed result. On error it returns a zero sourceResult (nil body), which the
// caller treats as "skip this source".
func fetchSource(ctx context.Context, subID string, src database.Node) sourceResult {
	sourceURL := buildSourceURL(src, subID)

	fetchStart := time.Now()
	srcResp, err := FetchFromNode(ctx, sourceURL)
	fetchDuration := time.Since(fetchStart).Seconds()

	if err != nil {
		metrics.SubserverSourceFetchTotal.WithLabelValues("error", "unknown").Inc()
		metrics.SubserverSourceFetchDuration.WithLabelValues("error").Observe(fetchDuration)
		logger.Error("Failed to fetch from node",
			zap.String("sub_id", subID),
			zap.String("source", src.Name),
			zap.String("node_url", sourceURL),
			zap.Error(err))

		return sourceResult{}
	}

	body := srcResp.Body

	srcHeaders := srcResp.Headers
	if srcHeaders == nil {
		srcHeaders = make(map[string]string)
	}

	format := DetectFormat(body)
	metrics.SubserverSourceFetchTotal.WithLabelValues("success", format.String()).Inc()
	metrics.SubserverSourceFetchDuration.WithLabelValues("success").Observe(fetchDuration)
	logger.Debug("Node response received",
		zap.String("sub_id", subID),
		zap.String("source", src.Name),
		zap.String("format", format.String()),
		zap.Int("body_size", len(body)),
		zap.Int("headers_count", len(srcHeaders)),
	)

	return sourceResult{
		source:  src,
		body:    body,
		headers: srcHeaders,
		format:  format,
	}
}

// buildSourceURL constructs the upstream fetch URL for a node, handling
// fetch-type nodes (use URL as-is) versus regular nodes (append subID).
func buildSourceURL(src database.Node, subID string) string {
	if src.Type == database.NodeTypeFetch {
		return src.SubscriptionURL
	}

	srcSubURL := src.SubscriptionURL
	if !strings.HasSuffix(srcSubURL, "/") {
		srcSubURL += "/"
	}

	return srcSubURL + subID
}

// aggregateFormat normalizes each supported upstream response into server configs
// or share links appended to agg. JSON/Clash parse failures, invalid Base64, and
// unrecognized formats contribute no items; errors are not returned.
func aggregateFormat(agg *aggregatedSources, format Format, body []byte, src database.Node, subID string) {
	switch format {
	case FormatJSON:
		configs, err := ExtractJSONConfigs(body)
		if err != nil {
			logger.Error("Failed to parse JSON configs from node", zap.String("sub_id", subID), zap.String("source", src.Name), zap.Error(err))
			return
		}
		agg.jsonConfigs = append(agg.jsonConfigs, configs...)
	case FormatClash:
		configs, err := ExtractClashConfigs(body)
		if err != nil {
			logger.Error("Failed to parse Clash configs from node", zap.String("sub_id", subID), zap.String("source", src.Name), zap.Error(err))
			return
		}
		agg.jsonConfigs = append(agg.jsonConfigs, configs...)
	case FormatBase64:
		encoded := strings.TrimSpace(string(body))
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			decoded, err = base64.RawStdEncoding.DecodeString(encoded)
		}
		if err != nil {
			return
		}
		appendShareLinks(agg, string(decoded))
	case FormatPlain:
		appendShareLinks(agg, string(body))
	}
}

// appendShareLinks appends trimmed, nonempty lines to agg without validating links.
func appendShareLinks(agg *aggregatedSources, body string) {
	for line := range strings.SplitSeq(body, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			agg.items = append(agg.items, line)
		}
	}
}

// updateMinExpire records the earliest expiry seen across sources. Expiry
// values are normalized to seconds before comparison so that seconds and
// milliseconds widths are ordered correctly.
func updateMinExpire(agg *aggregatedSources, srcHeaders map[string]string) {
	userInfo, ok := srcHeaders["subscription-userinfo"]
	if !ok {
		return
	}

	exp, ok := parseExpireToInt(userInfo)
	if !ok {
		return
	}

	if agg.firstExpireVal == 0 || exp < agg.firstExpireVal {
		agg.firstExpireVal = exp
		agg.firstExpire = strconv.FormatInt(exp, 10)
	}
}

// parseExpireToInt extracts the "expire=" value from a subscription-userinfo
// header and parses it as an int64 (unix seconds). Values in milliseconds are
// normalized to seconds so sources using different widths compare correctly.
// Non-numeric expires are ignored because they cannot be compared reliably.
func parseExpireToInt(userInfo string) (int64, bool) {
	raw := ParseExpireFromUserInfo(userInfo)
	if raw == "" {
		return 0, false
	}

	exp, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, false
	}

	if exp > 1e11 {
		exp /= 1000
	}

	return exp, true
}

// buildResponse returns and caches a JSON array of complete Xray client profiles,
// one per converted outbound, together with response headers. Invalid or
// unsupported items are skipped; no usable outbounds yields ErrNoSubscriptionItems.
// Tagging, rendering, and final JSON encoding errors reach the caller without
// caching a response. trafficLimit is the total quota in bytes used in the
// user-info header. A nonempty profileTitleSuffix is appended to the upstream
// profile-title header, preserving its Base64 encoding when present.
func buildResponse(subSvc *Service, cacheKey string, agg aggregatedSources, trafficLimit int64, profileTitleSuffix string, format OutputFormat) (*SubscriptionResult, error) {
	userInfo := BuildUserInfoHeader(agg.totalUpload, agg.totalDownload, trafficLimit, agg.firstExpire)

	configs := aggregateConfigs(agg)
	if len(configs) == 0 {
		metrics.SubserverNoItemsTotal.Inc()
		return nil, ErrNoSubscriptionItems
	}

	var responseBody []byte
	var err error

	switch format {
	case OutputSingbox:
		responseBody, err = renderSingboxSubscription(configs)
		if err != nil {
			metrics.SubserverNoItemsTotal.Inc()
			return nil, ErrNoSubscriptionItems
		}
	case OutputClash:
		responseBody, err = renderClashSubscription(configs)
		if err != nil {
			metrics.SubserverNoItemsTotal.Inc()
			return nil, ErrNoSubscriptionItems
		}
	default:
		responseBody, err = renderXraySubscription(configs)
		if err != nil {
			if errors.Is(err, errNoUsableOutbounds) {
				metrics.SubserverNoItemsTotal.Inc()
				return nil, ErrNoSubscriptionItems
			}
			return nil, err
		}
	}

	cacheHeaders := ResponseHeaders(agg.firstSourceHeaders, format.ContentType(), userInfo)
	ApplyProfileTitleSuffix(cacheHeaders, profileTitleSuffix)
	subSvc.SetCache(cacheKey, responseBody, cacheHeaders)
	return &SubscriptionResult{Body: responseBody, Headers: cacheHeaders}, nil
}

// errNoUsableOutbounds marks the Xray path's "no valid configs" outcome so the
// caller can surface it as ErrNoSubscriptionItems without conflating it with
// tagging or rendering failures.
var errNoUsableOutbounds = errors.New("no usable outbounds")

// renderXraySubscription converts configs into the XTLS-standard JSON array of
// self-contained client profiles (one per node) — the historical output shape.
func renderXraySubscription(configs []json.RawMessage) ([]byte, error) {
	outbounds, err := ConvertJSONConfigsToXray(configs)
	if err != nil {
		return nil, errNoUsableOutbounds
	}
	err = tagProxyOutbounds(outbounds)
	if err != nil {
		return nil, err
	}
	// XTLS Subscription Standards define a subscription as a JSON array of
	// complete connection objects. Each element carries exactly one proxy
	// outbound so clients (v2rayNG parses one profile per array element) list
	// every node as a selectable profile instead of collapsing them into one.
	connections := make([]json.RawMessage, 0, len(outbounds))
	for _, outbound := range outbounds {
		connection, renderErr := renderXrayConnection(outboundTag(outbound), outbound)
		if renderErr != nil {
			return nil, renderErr
		}
		connections = append(connections, connection)
	}
	responseBody, err := json.Marshal(connections)
	if err != nil {
		return nil, fmt.Errorf("marshal Xray subscription: %w", err)
	}
	return responseBody, nil
}

// renderSingboxSubscription converts configs into a single sing-box config whose
// outbounds array holds one server outbound per node plus a "proxy" selector group.
func renderSingboxSubscription(configs []json.RawMessage) ([]byte, error) {
	outbounds, err := ConvertJSONConfigsToSingbox(configs)
	if err != nil {
		return nil, ErrNoSubscriptionItems
	}
	tagSingboxOutbounds(outbounds)
	doc := BuildSingboxConfig(outbounds)
	responseBody, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("marshal sing-box subscription: %w", err)
	}
	return responseBody, nil
}

// renderClashSubscription converts configs into a complete Clash/Mihomo YAML profile.
func renderClashSubscription(configs []json.RawMessage) ([]byte, error) {
	proxies, err := ConvertJSONConfigsToClash(configs)
	if err != nil {
		return nil, ErrNoSubscriptionItems
	}
	tagClashProxies(proxies)
	doc := buildClashConfig(proxies)
	responseBody, err := yaml.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("marshal clash subscription: %w", err)
	}
	return responseBody, nil
}

// aggregateConfigs normalizes every aggregated source (raw JSON configs and share
// links) into the canonical []json.RawMessage list consumed by all renderers.
func aggregateConfigs(agg aggregatedSources) []json.RawMessage {
	configs := make([]json.RawMessage, 0, len(agg.jsonConfigs)+len(agg.items))
	configs = append(configs, agg.jsonConfigs...)
	for _, item := range agg.items {
		link := strings.TrimSpace(item)
		if link == "" {
			continue
		}
		if strings.HasPrefix(strings.ToLower(link), "vmess://") {
			cfg, err := parseVMessItem(link)
			if err != nil {
				logger.Warn("Skipping invalid vmess share link", zap.Error(err))
				continue
			}
			raw, err := json.Marshal(cfg) // #nosec G117 -- payload is the client credential this subscription must deliver
			if err == nil {
				configs = append(configs, raw)
			}
			continue
		}

		cfg, err := parseShareLink(link)
		if err != nil {
			logger.Warn("Skipping unsupported or invalid share link", zap.Error(err))
			continue
		}
		raw, err := json.Marshal(cfg) // #nosec G117 -- password is the client credential this subscription must deliver
		if err == nil {
			configs = append(configs, raw)
		}
	}
	return configs
}

// xrayTemplateData holds the values injected into templates/xray_client.json.
// All fields carry pre-encoded JSON so quoting (numbers vs strings) stays correct.
type xrayTemplateData struct {
	RemarksJSON   string
	OutboundsJSON string
	TagJSON       string
}

// renderXrayConnection renders one complete Xray-core client config for a single
// proxy outbound: the node, a direct and a block outbound, and routing whose
// catch-all rule targets that node. It is the array element clients import.
// remarks supplies both the profile name and the default-route tag and must
// match the proxy outbound's tag. JSON encoding and template execution errors
// are wrapped and returned; invalid rendered JSON also returns an error.
func renderXrayConnection(remarks string, outbound json.RawMessage) (json.RawMessage, error) {
	outboundsJSON, err := json.Marshal([]json.RawMessage{
		outbound,
		json.RawMessage(`{"protocol":"freedom","tag":"direct"}`),
		json.RawMessage(`{"protocol":"blackhole","tag":"block"}`),
	})
	if err != nil {
		return nil, fmt.Errorf("marshal Xray outbounds: %w", err)
	}
	remarksJSON, err := json.Marshal(remarks)
	if err != nil {
		return nil, fmt.Errorf("marshal Xray remarks: %w", err)
	}
	var rendered strings.Builder
	err = xrayTemplate.Execute(&rendered, xrayTemplateData{
		RemarksJSON:   string(remarksJSON),
		OutboundsJSON: string(outboundsJSON),
		TagJSON:       string(remarksJSON),
	})
	if err != nil {
		return nil, fmt.Errorf("render Xray template: %w", err)
	}
	body := []byte(rendered.String())
	if !json.Valid(body) {
		return nil, fmt.Errorf("rendered Xray config is invalid JSON")
	}
	return body, nil
}

// reservedOutboundTags are the tags renderXrayConnection always assigns to the
// non-proxy outbounds of every rendered connection. A proxy outbound must never
// take one of them: routing resolves tags by name, so a node remarked "direct"
// would silently steal the direct route.
var reservedOutboundTags = []string{"direct", "block"}

// tagProxyOutbounds guarantees every proxy outbound has a unique, non-empty tag
// that does not collide with the reserved direct/block tags. The upstream remark
// (usually a node name with its country) is kept when it is unique, so clients
// show recognizable names; otherwise the outbound falls back to a free
// positional "proxy-N" tag. It replaces entries in the supplied slice in place.
// JSON errors are returned; entries processed before an error remain changed.
func tagProxyOutbounds(outbounds []json.RawMessage) error {
	used := make(map[string]struct{}, len(outbounds)+len(reservedOutboundTags))
	for _, reserved := range reservedOutboundTags {
		used[reserved] = struct{}{}
	}
	for i, raw := range outbounds {
		var outbound map[string]json.RawMessage
		err := json.Unmarshal(raw, &outbound)
		if err != nil {
			return fmt.Errorf("decode Xray outbound: %w", err)
		}
		tag := outboundTag(raw)
		if _, dup := used[tag]; tag == "" || dup {
			tag = freeProxyTag(i+1, used)
		}
		used[tag] = struct{}{}
		tagJSON, err := json.Marshal(tag)
		if err != nil {
			return fmt.Errorf("marshal Xray outbound tag: %w", err)
		}
		outbound["tag"] = tagJSON
		tagged, err := json.Marshal(outbound)
		if err != nil {
			return fmt.Errorf("marshal tagged Xray outbound: %w", err)
		}
		outbounds[i] = tagged
	}
	return nil
}

// freeProxyTag returns the first unused "proxy-N" tag at or after start, so a
// fallback never reuses a tag already claimed by another outbound or reserved
// by the rendered direct/block outbounds.
func freeProxyTag(start int, used map[string]struct{}) string {
	for n := start; ; n++ {
		candidate := "proxy-" + strconv.Itoa(n)
		if _, taken := used[candidate]; !taken {
			return candidate
		}
	}
}

// outboundTag returns the "tag" of an encoded outbound, or "" when absent or
// when the JSON cannot be decoded with a string tag.
func outboundTag(raw json.RawMessage) string {
	var probe struct {
		Tag string `json:"tag"`
	}
	err := json.Unmarshal(raw, &probe)
	if err != nil {
		return ""
	}
	return probe.Tag
}

// parseJSONNumber accepts a JSON number or string containing a base-10 integer.
// Invalid JSON, non-integer values, and values outside the int range return errors.
func parseJSONNumber(raw json.RawMessage) (int, error) {
	var number json.Number
	err := json.Unmarshal(raw, &number)
	if err == nil {
		return strconv.Atoi(number.String())
	}
	var text string
	err = json.Unmarshal(raw, &text)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(text)
}

// parseJSONScalar returns a JSON string's contents or a number's text.
// Missing or null values yield an empty string; other JSON kinds and malformed
// JSON return errors.
func parseJSONScalar(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil
	}
	var text string
	err := json.Unmarshal(raw, &text)
	if err == nil {
		return text, nil
	}
	var number json.Number
	err = json.Unmarshal(raw, &number)
	if err != nil {
		return "", err
	}
	return number.String(), nil
}

// parseVMessItem dispatches a vmess:// item on its payload shape. URL-style links
// (vmess://<id>@<host>:<port>?...<#remark>) are valid share URIs and go to
// parseShareLink; the Base64-encoded JSON form goes to parseVMessLink. The two
// shapes cannot be confused: the Base64 alphabets used by the JSON form never
// contain '@', so its presence marks the URL form.
func parseVMessItem(link string) (*serverConfig, error) {
	trimmed := strings.TrimSpace(link)
	if len(trimmed) > len("vmess://") && strings.EqualFold(trimmed[:len("vmess://")], "vmess://") &&
		strings.Contains(trimmed[len("vmess://"):], "@") {
		return parseShareLink(trimmed)
	}

	return parseVMessLink(trimmed)
}

// parseVMessLink decodes a vmess:// share link into a serverConfig. It runs the
// Base64 decode, JSON parse, port validation and address check; any failure is
// returned as an error so the caller decides how to surface it. Links with an
// empty vmess.Address are rejected. Ports must be integers from 1 through 65535;
// port and aid accept JSON numbers or strings, and missing/null aid is empty.
// The scheme is case-insensitive; payload fields retain their case.
func parseVMessLink(link string) (*serverConfig, error) {
	trimmed := strings.TrimSpace(link)
	// Lower-case only the scheme comparison: the Base64 payload is
	// case-sensitive, and lower-casing it corrupts the credentials and host.
	if len(trimmed) < len("vmess://") || !strings.EqualFold(trimmed[:len("vmess://")], "vmess://") {
		return nil, fmt.Errorf("vmess link has no vmess:// scheme")
	}
	payload := strings.TrimSpace(trimmed[len("vmess://"):])
	decoded, decodeErr := base64.StdEncoding.DecodeString(payload)
	if decodeErr != nil {
		decoded, decodeErr = base64.RawStdEncoding.DecodeString(payload)
	}
	if decodeErr != nil {
		decoded, decodeErr = base64.RawURLEncoding.DecodeString(payload)
	}
	if decodeErr != nil {
		return nil, fmt.Errorf("vmess payload is not valid base64: %w", decodeErr)
	}
	var vmess struct {
		Address     string          `json:"add"`
		Port        json.RawMessage `json:"port"`
		UUID        string          `json:"id"`
		Aid         json.RawMessage `json:"aid"`
		Scy         string          `json:"scy"`
		Network     string          `json:"net"`
		HeaderType  string          `json:"type"`
		Host        string          `json:"host"`
		Path        string          `json:"path"`
		TLS         string          `json:"tls"`
		SNI         string          `json:"sni"`
		Alpn        string          `json:"alpn"`
		Fingerprint string          `json:"fp"`
		Remark      string          `json:"ps"`
	}
	err := json.Unmarshal(decoded, &vmess)
	if err != nil {
		return nil, fmt.Errorf("vmess payload is not valid JSON: %w", err)
	}
	if vmess.Address == "" {
		return nil, fmt.Errorf("vmess link has empty address")
	}
	port, err := parseJSONNumber(vmess.Port)
	if err != nil || port < 1 || port > 65535 {
		return nil, fmt.Errorf("vmess link has invalid port")
	}
	aid, err := parseJSONScalar(vmess.Aid)
	if err != nil {
		return nil, fmt.Errorf("vmess link has invalid aid: %w", err)
	}
	return &serverConfig{
		Type:        "vmess",
		Address:     vmess.Address,
		Port:        port,
		UUID:        vmess.UUID,
		Aid:         aid,
		Scy:         vmess.Scy,
		Network:     vmess.Network,
		HeaderType:  vmess.HeaderType,
		Host:        vmess.Host,
		Path:        vmess.Path,
		TLS:         vmess.TLS,
		SNI:         vmess.SNI,
		Alpn:        vmess.Alpn,
		Fingerprint: vmess.Fingerprint,
		Remark:      vmess.Remark,
	}, nil
}

// parseShareLink normalizes URL-style VLESS, VMess, Trojan, Shadowsocks, and
// Hysteria links into a server config; hy2 becomes hysteria2. Base64-encoded
// VMess JSON payloads are handled separately by parseVMessLink; parseVMessItem
// routes each incoming vmess:// item to the matching parser. A host and explicit port
// from 1 through 65535 are required; URL, host, port, and unsupported-scheme
// errors reach the caller. Shadowsocks userinfo is accepted both Base64-encoded
// (SIP002) and as a plain "method:password" pair; when neither form yields a
// method the link is kept without one and the Xray outbound builder rejects it,
// so credentials are not validated here.
func parseShareLink(link string) (*serverConfig, error) {
	u, err := url.Parse(link)
	if err != nil {
		return nil, fmt.Errorf("parse share link: %w", err)
	}
	if u.Hostname() == "" {
		return nil, fmt.Errorf("share link has no host")
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil || port < 1 || port > 65535 {
		return nil, fmt.Errorf("share link has invalid port")
	}
	q := u.Query()
	cfg := &serverConfig{Type: strings.ToLower(u.Scheme), Address: u.Hostname(), Port: port, Remark: u.Fragment}
	switch cfg.Type {
	case "vless", "vmess", "trojan", "ss", "shadowsocks", "hysteria", "hysteria2", "hy2":
	default:
		return nil, fmt.Errorf("unsupported share-link scheme %q", u.Scheme)
	}
	if u.User != nil {
		cfg.UUID = u.User.Username()
		cfg.Password, _ = u.User.Password()
	}
	if cfg.Type == "trojan" || cfg.Type == "hysteria" || cfg.Type == "hysteria2" || cfg.Type == "hy2" {
		if cfg.Password == "" {
			cfg.Password = cfg.UUID
		}
	}
	cfg.Security, cfg.TLS = q.Get("security"), q.Get("tls")
	if cfg.Security == "" {
		cfg.Security = cfg.TLS
	}
	cfg.SNI, cfg.Fingerprint = q.Get("sni"), q.Get("fp")
	cfg.PublicKey, cfg.ShortID = q.Get("pbk"), q.Get("sid")
	cfg.Network, cfg.Path = q.Get("type"), q.Get("path")
	cfg.Host, cfg.Flow = q.Get("host"), q.Get("flow")
	cfg.Alpn, cfg.Method = q.Get("alpn"), q.Get("method")
	cfg.Plugin, cfg.PluginOpts = q.Get("plugin"), q.Get("plugin-opts")
	cfg.Obfs, cfg.ObfsPassword = q.Get("obfs"), q.Get("obfs-password")
	if cfg.Type == "ss" || cfg.Type == "shadowsocks" {
		if method, password, ok := splitShadowsocksUserInfo(cfg.UUID, cfg.Password); ok {
			cfg.Method, cfg.Password = method, password
		}
	}
	if cfg.Type == "hy2" {
		cfg.Type = "hysteria2"
	}
	return cfg, nil
}

// splitShadowsocksUserInfo extracts the SIP002 method/password pair from a
// Shadowsocks link's userinfo. Two forms occur in the wild:
//
//	ss://base64(method:password)@host:port  (the Base64 form; url.Parse leaves
//	                                         the whole payload as the username)
//	ss://method:password@host:port          (the plain form; url.Parse already
//	                                         split it into username and password)
//
// ok is false when neither form yields a method, so the caller keeps whatever it
// already parsed. A malformed password part is not an error: the URL only has to
// be structurally valid for the subscription to keep serving the node.
func splitShadowsocksUserInfo(user, password string) (method, pass string, ok bool) {
	if password != "" {
		// Plain, unencoded userinfo: the username is the cipher method.
		if user == "" {
			return "", "", false
		}
		return user, password, true
	}
	if user == "" {
		return "", "", false
	}
	decoded := decodeShadowsocksUserInfo(user)
	if decoded == "" {
		return "", "", false
	}
	method, pass, found := strings.Cut(decoded, ":")
	if !found || method == "" {
		return "", "", false
	}
	return method, pass, true
}

// decodeShadowsocksUserInfo decodes the Base64 Shadowsocks userinfo payload.
// RawURL is tried first (SIP002, unpadded), then the padded standard and raw
// standard alphabets. Undecodable payloads yield an empty string.
func decodeShadowsocksUserInfo(encoded string) string {
	for _, enc := range []*base64.Encoding{
		base64.RawURLEncoding,
		base64.StdEncoding,
		base64.RawStdEncoding,
	} {
		decoded, err := enc.DecodeString(encoded)
		if err == nil {
			return strings.TrimSpace(string(decoded))
		}
	}
	return ""
}
