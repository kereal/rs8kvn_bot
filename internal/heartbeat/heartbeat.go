package heartbeat

import (
	"context"
	"net/http"
	"sync"
	"time"

	"rs8kvn_bot/internal/config"
	"rs8kvn_bot/internal/logger"
)

var (
	httpClient     *http.Client
	httpClientOnce sync.Once
)

// getHTTPClient returns a shared HTTP client with optimized transport for minimal memory.
// The client is created once and reused for all heartbeat requests.
func getHTTPClient() *http.Client {
	httpClientOnce.Do(func() {
		transport := &http.Transport{
			MaxIdleConns:        config.MaxIdleConns,
			MaxIdleConnsPerHost: config.MaxIdleConns,
			IdleConnTimeout:     config.DefaultIdleConnIdleTimeout,
			DisableCompression:  false,
			ForceAttemptHTTP2:   false,
		}
		httpClient = &http.Client{
			Timeout:   config.DefaultHTTPTimeout,
			Transport: transport,
		}
	})
	return httpClient
}

// Start begins sending periodic heartbeat POST requests to the specified URL.
// If url is empty, no heartbeat signals are sent.
// The function runs until the context is cancelled.
//
// Parameters:
//   - ctx: Context for cancellation
//   - url: The heartbeat endpoint URL (optional)
//   - intervalSeconds: Interval between heartbeats in seconds (minimum: config.MinHeartbeatInterval)
func Start(ctx context.Context, url string, intervalSeconds int) {
	if url == "" {
		logger.Info("Heartbeat URL not configured, skipping heartbeat scheduler")
		return
	}

	// Validate and normalize interval
	if intervalSeconds < config.MinHeartbeatInterval {
		logger.Warnf("Heartbeat interval %ds is too low, using minimum: %ds",
			intervalSeconds, config.MinHeartbeatInterval)
		intervalSeconds = config.MinHeartbeatInterval
	}

	interval := time.Duration(intervalSeconds) * time.Second
	logger.Infof("Heartbeat scheduler started: URL=%s, interval=%v", maskURL(url), interval)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Send initial heartbeat
	sendHeartbeat(url)

	for {
		select {
		case <-ticker.C:
			sendHeartbeat(url)
		case <-ctx.Done():
			logger.Info("Heartbeat scheduler stopped")
			return
		}
	}
}

// sendHeartbeat sends a POST request to the heartbeat URL.
// Errors are logged but do not cause the scheduler to stop.
func sendHeartbeat(url string) {
	client := getHTTPClient()

	resp, err := client.Post(url, "application/json", nil)
	if err != nil {
		logger.Errorf("Heartbeat failed: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		logger.Debug("Heartbeat sent successfully")
	} else {
		logger.Warnf("Heartbeat returned status: %d", resp.StatusCode)
	}
}

// maskURL masks a URL for logging purposes.
// Shows only scheme and host, hides the path.
func maskURL(urlStr string) string {
	if len(urlStr) == 0 {
		return "(empty)"
	}

	// Find the scheme separator
	schemeEnd := 0
	for i := 0; i < len(urlStr)-2; i++ {
		if urlStr[i] == ':' && urlStr[i+1] == '/' && urlStr[i+2] == '/' {
			schemeEnd = i
			break
		}
	}

	if schemeEnd == 0 {
		// No scheme found, just mask everything
		if len(urlStr) > 10 {
			return urlStr[:10] + "..."
		}
		return "***"
	}

	// Find the first slash after scheme://
	hostEnd := len(urlStr)
	for i := schemeEnd + 3; i < len(urlStr); i++ {
		if urlStr[i] == '/' {
			hostEnd = i
			break
		}
	}

	return urlStr[:hostEnd] + "/***"
}
