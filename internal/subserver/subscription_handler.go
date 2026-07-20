// Package subserver implements the subscription delivery endpoint (/sub/:id)
// that aggregates proxy configurations from multiple 3x-ui upstream sources,
// caches responses, and tracks requesting devices and IPs for analytics.
package subserver

import (
	"context"
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
//  1. Check the cache for a fresh entry keyed by subID (serveFromCache).
//  2. On cache miss, load the subscription with plan and sources from the database (loadSubscription).
//  3. Track the requesting device and IP for analytics.
//  4. For each active source, fetch the upstream response and detect its format (fetchAndAggregateSources).
//  5. Aggregate subscription-userinfo headers and build the final response (buildResponse).
func HandleSubscription(ctx context.Context, db interfaces.SubscriptionRepository, subSvc *Service, subID, clientIP string, requestHeaders map[string]string) (*SubscriptionResult, int, int, error) {
	// 1. Try to serve from cache first.
	hitStart := time.Now()
	if result, hit, err := serveFromCache(ctx, db, subSvc, subID); hit {
		metrics.SubserverCacheHitDuration.Observe(time.Since(hitStart).Seconds())
		return result, 0, 0, err
	}

	// 2. Cache miss: load the subscription with plan and active sources.
	missStart := time.Now()
	subFull, err := loadSubscription(ctx, db, subID, clientIP, requestHeaders)
	if err != nil {
		metrics.SubserverCacheMissDuration.Observe(time.Since(missStart).Seconds())
		return nil, 0, 0, err
	}

	// 3-4. Fetch from all active sources and aggregate items + traffic.
	agg, success, total := fetchAndAggregateSources(ctx, subID, subFull.Nodes)

	// 5. Build the final response and cache it.
	res, err := buildResponse(subSvc, subID, agg, subFull.Plan.TrafficLimit)
	metrics.SubserverCacheMissDuration.Observe(time.Since(missStart).Seconds())
	return res, success, total, err
}

// UpdateDevices records the current request headers as a device entry in the
// subscription's Devices JSON field. Each entry includes a "timestamp" key
// (UTC RFC3339) marking when the device was last seen. If an existing entry
// has the same x-hwid value it is replaced (rotated to the end). The updated
// list is persisted to DB.
func UpdateDevices(ctx context.Context, db interfaces.SubscriptionRepository, subFull *database.SubscriptionFull, headers map[string]string) {
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
func UpdateIPs(ctx context.Context, db interfaces.SubscriptionRepository, subFull *database.SubscriptionFull, ip string) {
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
