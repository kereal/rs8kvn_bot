package heartbeat

import (
	"context"
	"net/http"
	"sync"
	"time"

	"rs8kvn_bot/internal/logger"
)

var (
	httpClient     *http.Client
	httpClientOnce sync.Once
)

// getHTTPClient returns a shared HTTP client with optimized transport for minimal memory
func getHTTPClient() *http.Client {
	httpClientOnce.Do(func() {
		transport := &http.Transport{
			MaxIdleConns:        1,
			MaxIdleConnsPerHost: 1,
			IdleConnTimeout:     30 * time.Second,
			DisableCompression:  false,
			ForceAttemptHTTP2:   false,
		}
		httpClient = &http.Client{
			Timeout:   10 * time.Second,
			Transport: transport,
		}
	})
	return httpClient
}

// Start begins sending periodic heartbeat POST requests to the specified URL.
// If url is empty, no heartbeat signals are sent.
// The function runs until the context is cancelled.
func Start(ctx context.Context, url string, intervalSeconds int) {
	if url == "" {
		logger.Info("Heartbeat URL not configured, skipping heartbeat scheduler")
		return
	}

	if intervalSeconds < 1 {
		intervalSeconds = 300 // Default to 5 minutes
	}

	interval := time.Duration(intervalSeconds) * time.Second
	logger.Infof("Heartbeat scheduler started: URL=%s, interval=%v", url, interval)

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

// sendHeartbeat sends a POST request to the heartbeat URL
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
