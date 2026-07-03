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

// HandleSubscription processes a subscription request from cache or upstream sources.
// It returns the aggregated response body with headers, or an error if the subscription
// cannot be served.
//
// The flow is:
//  1. Check the cache for a fresh entry keyed by subID.
//  2. On cache hit, verify the subscription is still active; invalidate stale entries.
//  3. On cache miss, load the subscription with plan and sources from the database.
//  4. Track the requesting device and IP for analytics.
//  5. For each active source, fetch the upstream response and detect its format.
//  6. Aggregate subscription-userinfo headers (earliest expire, total upload/download).
//  7. Convert JSON configs to share links if mixed mode is detected.
//  8. Cache and return the final body with the appropriate Content-Type.
func HandleSubscription(ctx context.Context, db interfaces.DatabaseService, subSvc *Service, subID, clientIP string, requestHeaders map[string]string) (*SubscriptionResult, error) {
	cacheKey := subID

	// Try to serve from cache first.
	if cachedBody, cachedHeaders, ok := subSvc.GetCache(cacheKey); ok {
		// On cache hit, verify the subscription is still active.
		status, expiryTime, err := db.GetSubscriptionStatus(ctx, subID)
		if err != nil {
			subSvc.InvalidateCache(cacheKey)
			if errors.Is(err, database.ErrSubscriptionNotFound) {
				metrics.SubserverCacheInvalidationsTotal.WithLabelValues("not_found").Inc()
				return nil, ErrSubscriptionNotFound
			}
			metrics.SubserverCacheInvalidationsTotal.WithLabelValues("status_error").Inc()
			logger.Error("Cache status check failed, cache invalidated",
				zap.String("sub_id", subID),
				zap.Error(err))
			return nil, fmt.Errorf("cache status check failed: %w", err)
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
			return nil, fmt.Errorf("subscription not active")
		}
		logger.Info("Cache hit", zap.String("sub_id", subID))
		return &SubscriptionResult{
			Body:    cachedBody,
			Headers: cachedHeaders,
		}, nil
	}

	// Cache miss: load the subscription with plan and active sources.
	subFull, err := db.GetWithPlanAndNodes(ctx, subID)
	if err != nil {
		if errors.Is(err, database.ErrSubscriptionNotFound) {
			logger.Error("Subscription not found in database",
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

	var allItems []string
	var allJSONConfigs []json.RawMessage
	var firstExpire string
	var totalUpload, totalDownload int64
	allJSON := true
	var firstSourceHeaders map[string]string

	// Iterate over all active sources and collect subscription items.
	for _, src := range subFull.Nodes {
		if src.SubscriptionURL == "" {
			logger.Warn("Skipping node without subscription_url",
				zap.String("sub_id", subID),
				zap.String("source", src.Name),
			)
			continue
		}

		srcSubURL := src.SubscriptionURL
		if srcSubURL != "" && !strings.HasSuffix(srcSubURL, "/") {
			srcSubURL += "/"
		}
		sourceURL := srcSubURL + subID

		// Fetch the upstream subscription response from the source.
		fetchStart := time.Now()
		xuiResp, err := FetchFromXUI(ctx, sourceURL)
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

		body := xuiResp.Body
		xuiHeaders := xuiResp.Headers
		if xuiHeaders == nil {
			xuiHeaders = make(map[string]string)
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
			zap.Int("headers_count", len(xuiHeaders)),
		)

		// Capture headers from the first successful source for replay.
		if xuiHeaders != nil {
			if firstSourceHeaders == nil {
				firstSourceHeaders = xuiHeaders
			}
			if expireVal, ok := xuiHeaders["subscription-userinfo"]; ok {
				expire := ParseExpireFromUserInfo(expireVal)
				if expire != "" && (firstExpire == "" || expire < firstExpire) {
					firstExpire = expire
				}
			}
		}

		// Aggregate usage counters across all sources.
		totalUpload += ParseUserInfoValue(xuiHeaders, "upload")
		totalDownload += ParseUserInfoValue(xuiHeaders, "download")

		switch format {
		case FormatJSON:
			// JSON configs are kept as raw messages for pure-JSON output
			// or converted to share links in mixed mode.
			configs, parseErr := ExtractJSONConfigs(body)
			if parseErr != nil {
				logger.Error("Failed to parse JSON configs from node",
					zap.String("sub_id", subID),
					zap.String("source", src.Name),
					zap.Error(parseErr))
				allJSON = false
				continue
			}
			allJSONConfigs = append(allJSONConfigs, configs...)
		case FormatBase64:
			allJSON = false
			decoded, decErr := base64.StdEncoding.DecodeString(strings.TrimSpace(string(body)))
			if decErr != nil {
				allItems = append(allItems, strings.TrimSpace(string(body)))
			} else {
				allItems = append(allItems, strings.TrimSpace(string(decoded)))
			}
		case FormatPlain:
			allJSON = false
			lines := strings.Split(string(body), "\n")
			for _, line := range lines {
				line = strings.TrimSpace(line)
				if line != "" {
					allItems = append(allItems, line)
				}
			}
		}
	}

	// Build the aggregated Subscription-UserInfo header.
	userInfo := BuildUserInfoHeader(totalUpload, totalDownload, subFull.Plan.TrafficLimit, firstExpire)

	// If we are in mixed mode (some sources returned non-JSON),
	// convert any collected JSON configs to share links and merge into allItems.
	if !allJSON && len(allJSONConfigs) > 0 {
		for _, rawConfig := range allJSONConfigs {
			link, convErr := ConvertSingleJSONToLink(rawConfig)
			if convErr != nil {
				logger.Error("Failed to convert JSON config to share link",
					zap.String("sub_id", subID),
					zap.Error(convErr))
				continue
			}
			allItems = append(allItems, link)
		}
	}

	// Pure-JSON output: marshal all raw configs into a JSON array response.
	if allJSON && len(allJSONConfigs) > 0 {
		responseBody, marshalErr := json.Marshal(allJSONConfigs)
		if marshalErr != nil {
			logger.Error("Failed to marshal JSON response",
				zap.String("sub_id", subID),
				zap.Error(marshalErr))
			return nil, fmt.Errorf("failed to marshal response: %w", marshalErr)
		}
		cacheHeaders := ResponseHeaders(firstSourceHeaders, "application/json; charset=utf-8", userInfo)
		subSvc.SetCache(cacheKey, responseBody, cacheHeaders)
		return &SubscriptionResult{
			Body:    responseBody,
			Headers: cacheHeaders,
		}, nil
	}

	// No servers collected from any source.
	if len(allItems) == 0 {
		metrics.SubserverNoItemsTotal.Inc()
		return nil, ErrNoSubscriptionItems
	}

	// Mixed or plain-text output: join all share links and encode to base64.
	combined := strings.Join(allItems, "\n")
	responseBody := []byte(base64.StdEncoding.EncodeToString([]byte(combined)))
	ct := "text/plain; charset=utf-8; profile=base64"
	cacheHeaders := ResponseHeaders(firstSourceHeaders, ct, userInfo)
	subSvc.SetCache(cacheKey, responseBody, cacheHeaders)

	return &SubscriptionResult{
		Body:    responseBody,
		Headers: cacheHeaders,
	}, nil
}

// UpdateDevices records the current request headers as a device entry in the
// subscription's Devices JSON field. Each entry includes a "timestamp" key
// (UTC RFC3339) marking when the device was last seen. If an existing entry
// has the same x-hwid value it is replaced (rotated to the end). The updated
// list is persisted to DB.
func UpdateDevices(ctx context.Context, db interfaces.DatabaseService, subFull *database.SubscriptionFull, headers map[string]string) {
	devices, err := subFull.Subscription.ParseDevices()
	if err != nil {
		logger.Error("Failed to parse devices JSON",
			zap.Uint("sub_pk", subFull.Subscription.ID),
			zap.String("sub_id", subFull.Subscription.SubscriptionID),
			zap.Error(err))
		devices = []map[string]string{}
	}

	if headers != nil {
		currentHWID := headers["x-hwid"]
		nowStr := time.Now().UTC().Format(time.RFC3339)

		for i, dev := range devices {
			if dev["x-hwid"] == currentHWID {
				devices = append(devices[:i], devices[i+1:]...)
				break
			}
		}

		entry := make(map[string]string, len(headers)+1)
		for k, v := range headers {
			entry[k] = v
		}
		entry["timestamp"] = nowStr
		devices = append(devices, entry)

		if len(devices) > MaxDeviceEntries {
			devices = devices[len(devices)-MaxDeviceEntries:]
		}
	}

	if err := subFull.Subscription.SetDevices(devices); err != nil {
		logger.Error("Failed to set devices JSON",
			zap.Uint("sub_pk", subFull.Subscription.ID),
			zap.String("sub_id", subFull.Subscription.SubscriptionID),
			zap.Error(err))
		return
	}

	if err := db.UpdateDevices(ctx, subFull.Subscription.ID, subFull.Subscription.Devices); err != nil {
		logger.Error("Failed to save devices to database",
			zap.Uint("sub_pk", subFull.Subscription.ID),
			zap.String("sub_id", subFull.Subscription.SubscriptionID),
			zap.Error(err))
	}
}

// UpdateIPs records the current client IP with a UTC timestamp in the
// subscription's Ips JSON field. Duplicate IPs are rotated to the end.
// The list is capped at maxIPEntries (oldest entries are dropped).
func UpdateIPs(ctx context.Context, db interfaces.DatabaseService, subFull *database.SubscriptionFull, ip string) {
	ips, err := subFull.Subscription.ParseIPs()
	if err != nil {
		logger.Error("Failed to parse ips JSON",
			zap.Uint("sub_pk", subFull.Subscription.ID),
			zap.String("sub_id", subFull.Subscription.SubscriptionID),
			zap.Error(err))
		ips = []map[string]string{}
	}

	if ip != "" {
		nowStr := time.Now().UTC().Format(time.RFC3339)

		for i, entry := range ips {
			if _, exists := entry[ip]; exists {
				ips = append(ips[:i], ips[i+1:]...)
				break
			}
		}

		newEntry := map[string]string{ip: nowStr}
		ips = append(ips, newEntry)

		if len(ips) > MaxIPEntries {
			ips = ips[len(ips)-MaxIPEntries:]
		}
	}

	if err := subFull.Subscription.SetIPs(ips); err != nil {
		logger.Error("Failed to set ips JSON",
			zap.Uint("sub_pk", subFull.Subscription.ID),
			zap.String("sub_id", subFull.Subscription.SubscriptionID),
			zap.Error(err))
		return
	}

	if err := db.UpdateIPs(ctx, subFull.Subscription.ID, subFull.Subscription.Ips); err != nil {
		logger.Error("Failed to save ips to database",
			zap.Uint("sub_pk", subFull.Subscription.ID),
			zap.String("sub_id", subFull.Subscription.SubscriptionID),
			zap.Error(err))
	}
}
