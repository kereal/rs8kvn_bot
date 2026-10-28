// Package subserver implements the subscription delivery endpoint (/sub/:id)
// that aggregates proxy configurations from multiple 3x-ui upstream sources,
// caches responses, and tracks requesting devices and IPs for analytics.
package subserver

import (
	"context"
	"maps"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/interfaces"
	"github.com/kereal/rs8kvn_bot/internal/logger"

	"go.uber.org/zap"
)

// HandleSubscription processes a subscription request from cache or upstream sources.
// It returns the body and headers in the format chosen by DetectOutputFormat
// using formatParam and requestHeaders["user-agent"], with a separate cache entry
// per format. The two counts are successful upstream fetches and nodes with a
// subscription URL; both are zero on a cache hit or a subscription lookup error.
//
// Missing, inactive, or expired subscriptions yield ErrSubscriptionNotFound;
// database lookup and status revalidation errors propagate. Failed sources and
// invalid items are skipped, with ErrNoSubscriptionItems when none can be
// rendered; other Xray rendering errors propagate. Device/IP analytics are
// recorded on cache misses, and last_request is updated on successful lookups
// and cache revalidation. Analytics and last_request errors do not block delivery.
func HandleSubscription(ctx context.Context, db interfaces.SubscriptionRepository, subSvc *Service, subID, clientIP string, requestHeaders map[string]string, formatParam string) (*SubscriptionResult, int, int, error) {
	outFormat := DetectOutputFormat(formatParam, requestHeaders["user-agent"])
	cacheKey := subID
	if outFormat != OutputXray {
		// Per-format cache entries keep a suffix so a sing-box client never
		// receives the default Xray body (and vice versa). The Xray key stays
		// bare for backward compatibility with existing cache entries.
		cacheKey = subID + ":" + outFormat.String()
	}

	// 1. Try to serve from cache first.
	if result, hit, err := serveFromCache(ctx, db, subSvc, subID, cacheKey); hit {
		return result, 0, 0, err
	}

	// 2. Cache miss: load the subscription with plan and active sources.

	subFull, err := loadSubscription(ctx, db, subSvc, subID, clientIP, requestHeaders)
	if err != nil {
		return nil, 0, 0, err
	}

	// 3-4. Fetch from all active sources and aggregate items + traffic.
	agg, success, total := fetchAndAggregateSources(ctx, subID, subFull.Nodes)

	// 5. Build the final response and cache it. Paid (premium) subscriptions get
	// a " Premium" suffix appended to the upstream profile-title header.
	profileTitleSuffix := ""
	if subFull.Subscription.IsPaid() {
		profileTitleSuffix = " Premium"
	}

	res, err := buildResponse(subSvc, cacheKey, agg, subFull.Plan.TrafficLimit, profileTitleSuffix, outFormat)

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
		maps.Copy(entry, headers)

		entry["timestamp"] = nowStr
		devices = append(devices, entry)

		if len(devices) > MaxDeviceEntries {
			devices = devices[len(devices)-MaxDeviceEntries:]
		}
	}

	err = subFull.Subscription.SetDevices(devices)
	if err != nil {
		logger.Error("Failed to set devices JSON",
			zap.Uint("sub_pk", subFull.Subscription.ID),
			zap.String("sub_id", subFull.Subscription.SubscriptionID),
			zap.Error(err))

		return
	}

	err = db.UpdateDevices(ctx, subFull.Subscription.ID, subFull.Subscription.Devices)
	if err != nil {
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

	err = subFull.Subscription.SetIPs(ips)
	if err != nil {
		logger.Error("Failed to set ips JSON",
			zap.Uint("sub_pk", subFull.Subscription.ID),
			zap.String("sub_id", subFull.Subscription.SubscriptionID),
			zap.Error(err))

		return
	}

	err = db.UpdateIPs(ctx, subFull.Subscription.ID, subFull.Subscription.Ips)
	if err != nil {
		logger.Error("Failed to save ips to database",
			zap.Uint("sub_pk", subFull.Subscription.ID),
			zap.String("sub_id", subFull.Subscription.SubscriptionID),
			zap.Error(err))
	}
}
