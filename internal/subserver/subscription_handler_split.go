package subserver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
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
func serveFromCache(ctx context.Context, db interfaces.DatabaseService, subSvc *Service, subID string) (*SubscriptionResult, bool, error) {
	cacheKey := subID
	cachedBody, cachedHeaders, ok := subSvc.GetCache(cacheKey)
	if !ok {
		return nil, false, nil
	}

	// On cache hit, verify the subscription is still active.
	status, expiryTime, err := db.GetSubscriptionStatus(ctx, subID)
	if err != nil {
		subSvc.InvalidateCache(cacheKey)
		if errors.Is(err, database.ErrSubscriptionNotFound) {
			metrics.SubserverCacheInvalidationsTotal.WithLabelValues("not_found").Inc()
			return nil, true, ErrSubscriptionNotFound
		}
		metrics.SubserverCacheInvalidationsTotal.WithLabelValues("status_error").Inc()
		logger.Error("Cache status check failed, cache invalidated",
			zap.String("sub_id", subID),
			zap.Error(err))
		return nil, true, fmt.Errorf("cache status check failed: %w", err)
	}

	// If the subscription is no longer active or expired, invalidate the cache.
	if status != "active" || (!expiryTime.IsZero() && time.Now().After(expiryTime)) {
		subSvc.InvalidateCache(cacheKey)
		invalidReason := "revoked"
		if status == "active" {
			invalidReason = "expired"
		}
		metrics.SubserverCacheInvalidationsTotal.WithLabelValues(invalidReason).Inc()
		logger.Warn("Cache invalidated: subscription no longer active",
			zap.String("sub_id", subID),
			zap.String("status", status),
			zap.Time("expires_at", expiryTime),
		)
		return nil, true, fmt.Errorf("subscription not active")
	}

	logger.Debug("Cache hit", zap.String("sub_id", subID))
	// best-effort: обновляем last_request, ошибки не блокируют выдачу.
	if err := db.UpdateLastRequest(ctx, subID); err != nil {
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
func loadSubscription(ctx context.Context, db interfaces.DatabaseService, subID, clientIP string, requestHeaders map[string]string) (*database.SubscriptionFull, error) {
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

	// Track the requesting device and IP for analytics/audit.
	UpdateDevices(ctx, db, subFull, requestHeaders)
	UpdateIPs(ctx, db, subFull, clientIP)

	// best-effort: обновляем last_request, ошибки не блокируют выдачу.
	if err := db.UpdateLastRequest(ctx, subID); err != nil {
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
	totalUpload        int64
	totalDownload      int64
	allJSON            bool
	firstSourceHeaders map[string]string
}

// fetchAndAggregateSources iterates over all active source nodes, fetches
// upstream subscription responses, detects their format, and aggregates
// subscription items plus traffic counters.
func fetchAndAggregateSources(ctx context.Context, subID string, nodes []database.Node) aggregatedSources {
	agg := aggregatedSources{
		allJSON: true,
	}

	for _, src := range nodes {
		if src.SubscriptionURL == "" {
			logger.Warn("Skipping node without subscription_url",
				zap.String("sub_id", subID),
				zap.String("source", src.Name),
			)
			continue
		}

		sourceURL := buildSourceURL(src, subID)

		// Fetch the upstream subscription response from the source.
		fetchStart := time.Now()
		srcResp, err := FetchFromSource(ctx, sourceURL)
		fetchDuration := time.Since(fetchStart).Seconds()
		if err != nil {
			metrics.SubserverSourceFetchTotal.WithLabelValues("error", "unknown").Inc()
			metrics.SubserverSourceFetchDuration.WithLabelValues("error").Observe(fetchDuration)
			logger.Error("Failed to fetch from node",
				zap.String("sub_id", subID),
				zap.String("source", src.Name),
				zap.String("node_url", sourceURL),
				zap.Error(err))
			continue
		}

		body := srcResp.Body
		srcHeaders := srcResp.Headers
		if srcHeaders == nil {
			srcHeaders = make(map[string]string)
		}

		// Detect response format and log source details.
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

		// Capture headers from the first successful source for replay.
		if firstSourceHeaders := srcHeaders; firstSourceHeaders != nil {
			if agg.firstSourceHeaders == nil {
				agg.firstSourceHeaders = firstSourceHeaders
			}
			if expireVal, ok := firstSourceHeaders["subscription-userinfo"]; ok {
				expire := ParseExpireFromUserInfo(expireVal)
				if expire != "" && (agg.firstExpire == "" || expire < agg.firstExpire) {
					agg.firstExpire = expire
				}
			}
		}

		// Aggregate usage counters across all sources.
		agg.totalUpload += ParseUserInfoValue(srcHeaders, "upload")
		agg.totalDownload += ParseUserInfoValue(srcHeaders, "download")

		aggregateFormat(&agg, format, body, src, subID)
	}

	return agg
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
		decoded, decErr := base64.StdEncoding.DecodeString(strings.TrimSpace(string(body)))
		if decErr != nil {
			agg.items = append(agg.items, strings.TrimSpace(string(body)))
		} else {
			agg.items = append(agg.items, strings.TrimSpace(string(decoded)))
		}
	case FormatPlain:
		agg.allJSON = false
		for _, line := range strings.Split(string(body), "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				agg.items = append(agg.items, line)
			}
		}
	}
}

// buildResponse takes the aggregated source data and constructs the final
// subscription response body with headers, writing it to cache.
func buildResponse(subSvc *Service, cacheKey string, agg aggregatedSources, trafficLimit int64) (*SubscriptionResult, error) {
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
	subSvc.SetCache(cacheKey, responseBody, cacheHeaders)

	return &SubscriptionResult{
		Body:    responseBody,
		Headers: cacheHeaders,
	}, nil
}
