package subserver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/interfaces"
	"github.com/kereal/rs8kvn_bot/internal/logger"
	"github.com/kereal/rs8kvn_bot/internal/metrics"

	"go.uber.org/zap"
)

// serveFromCache attempts to serve a subscription response from cache.
// On a hit, it revalidates the subscription status in the DB and invalidates
// stale entries. Returns (result, hit, err): when hit is false, the caller
// should proceed to a cache-miss path.
func serveFromCache(ctx context.Context, db interfaces.SubscriptionRepository, subSvc *Service, subID string) (*SubscriptionResult, bool, error) {
	cacheKey := subID

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
	allJSON            bool
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
	agg := aggregatedSources{
		allJSON: true,
	}

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

// aggregateFormat parses the upstream body according to its detected format
// and appends items or JSON configs to the aggregator.
func aggregateFormat(agg *aggregatedSources, format Format, body []byte, src database.Node, subID string) {
	switch format {
	case FormatJSON:
		configs, parseErr := ExtractJSONConfigs(body)
		if parseErr != nil {
			logger.Error("Failed to parse JSON configs from node",
				zap.String("sub_id", subID),
				zap.String("source", src.Name),
				zap.Error(parseErr))

			agg.allJSON = false

			return
		}

		agg.jsonConfigs = append(agg.jsonConfigs, configs...)
	case FormatClash:
		// Clash is not a pure-JSON source, so force the base64/link output path.
		agg.allJSON = false

		configs, parseErr := ExtractClashConfigs(body)
		if parseErr != nil {
			logger.Error("Failed to parse Clash configs from node",
				zap.String("sub_id", subID),
				zap.String("source", src.Name),
				zap.Error(parseErr))

			return
		}

		agg.jsonConfigs = append(agg.jsonConfigs, configs...)
	case FormatBase64:
		agg.allJSON = false

		encoded := strings.TrimSpace(string(body))
		decoded, decErr := base64.StdEncoding.DecodeString(encoded)
		if decErr != nil {
			decoded, decErr = base64.RawStdEncoding.DecodeString(encoded)
		}
		if decErr != nil {
			agg.items = append(agg.items, encoded)
		} else {
			for line := range strings.SplitSeq(string(decoded), "\n") {
				line = strings.TrimSpace(line)
				if line != "" {
					agg.items = append(agg.items, line)
				}
			}
		}
	case FormatPlain:
		agg.allJSON = false

		for line := range strings.SplitSeq(string(body), "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				agg.items = append(agg.items, line)
			}
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

// buildResponse takes the aggregated source data and constructs the final
// subscription response body with headers, writing it to cache. When
// profileTitleSuffix is non-empty it is appended (base64-aware) to the upstream
// profile-title header before the response is cached.
func buildResponse(subSvc *Service, cacheKey string, agg aggregatedSources, trafficLimit int64, profileTitleSuffix string) (*SubscriptionResult, error) {
	userInfo := BuildUserInfoHeader(agg.totalUpload, agg.totalDownload, trafficLimit, agg.firstExpire)

	// If we are in mixed mode (some sources returned non-JSON),
	// convert any collected JSON configs to share links and merge into items.
	if !agg.allJSON && len(agg.jsonConfigs) > 0 {
		for _, rawConfig := range agg.jsonConfigs {
			link, convErr := ConvertSingleJSONToLink(rawConfig)
			if convErr != nil {
				logger.Error("Failed to convert JSON config to share link",
					zap.Error(convErr))

				continue
			}

			agg.items = append(agg.items, link)
		}
	}

	// Pure-JSON output: marshal all raw serverConfig objects into a JSON
	// array response.
	if agg.allJSON && len(agg.jsonConfigs) > 0 {
		responseBody, marshalErr := json.Marshal(agg.jsonConfigs)
		if marshalErr != nil {
			logger.Error("Failed to marshal JSON response",
				zap.Error(marshalErr))

			return nil, fmt.Errorf("failed to marshal response: %w", marshalErr)
		}

		cacheHeaders := ResponseHeaders(agg.firstSourceHeaders, "application/json; charset=utf-8", userInfo)
		ApplyProfileTitleSuffix(cacheHeaders, profileTitleSuffix)
		subSvc.SetCache(cacheKey, responseBody, cacheHeaders)

		return &SubscriptionResult{
			Body:    responseBody,
			Headers: cacheHeaders,
		}, nil
	}

	// No servers collected from any source.
	if len(agg.items) == 0 {
		metrics.SubserverNoItemsTotal.Inc()
		return nil, ErrNoSubscriptionItems
	}

	// Mixed or plain-text output: join all share links and encode to base64.
	combined := strings.Join(agg.items, "\n")
	responseBody := []byte(base64.StdEncoding.EncodeToString([]byte(combined)))
	ct := "text/plain; charset=utf-8; profile=base64"
	cacheHeaders := ResponseHeaders(agg.firstSourceHeaders, ct, userInfo)
	ApplyProfileTitleSuffix(cacheHeaders, profileTitleSuffix)
	subSvc.SetCache(cacheKey, responseBody, cacheHeaders)

	return &SubscriptionResult{
		Body:    responseBody,
		Headers: cacheHeaders,
	}, nil
}
