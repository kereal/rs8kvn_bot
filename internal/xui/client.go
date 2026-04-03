package xui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
	"golang.org/x/sync/singleflight"
	"rs8kvn_bot/internal/config"
	"rs8kvn_bot/internal/logger"
	"rs8kvn_bot/internal/utils"
)

// marshalJSON marshals data to JSON and returns a reader.
// The returned reader must be consumed before calling this function again.
func marshalJSON(v interface{}) (*bytes.Reader, error) {
	body, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return bytes.NewReader(body), nil
}

// closeResponseBody closes the response body and logs any error.
// This prevents resource leaks while still logging potential issues.
func closeResponseBody(resp *http.Response) {
	if resp == nil || resp.Body == nil {
		return
	}
	if err := resp.Body.Close(); err != nil {
		logger.Debug("Failed to close response body",
			zap.Error(err),
			zap.String("url", resp.Request.URL.String()))
	}
}

// Client manages communication with a 3x-ui panel.
// It handles authentication, session management, and API requests.
type Client struct {
	host       string
	username   string
	password   string
	httpClient *http.Client
	mu         sync.RWMutex
	lastLogin  time.Time
	breaker    *CircuitBreaker
	loginGroup singleflight.Group // Deduplicates concurrent login attempts
}

// APIResponse represents a generic response from the 3x-ui API.
type APIResponse struct {
	Success bool            `json:"success"`
	Msg     string          `json:"msg"`
	Obj     json.RawMessage `json:"obj,omitempty"`
}

// ClientConfig represents a client configuration in 3x-ui.
type ClientConfig struct {
	ID         string `json:"id"`
	Email      string `json:"email"`
	LimitIP    int    `json:"limitIp"`
	TotalGB    int64  `json:"totalGB"`
	ExpiryTime int64  `json:"expiryTime"`
	Enable     bool   `json:"enable"`
	TgID       int64  `json:"tgId"`
	SubID      string `json:"subId"`
	Flow       string `json:"flow,omitempty"`
	Reset      int    `json:"reset,omitempty"`
}

// ClientTraffic represents traffic statistics for a client in 3x-ui.
type ClientTraffic struct {
	ID         int    `json:"id"`
	InboundID  int    `json:"inboundId"`
	Enable     bool   `json:"enable"`
	Email      string `json:"email"`
	UUID       string `json:"uuid"`
	SubID      string `json:"subId"`
	Up         int64  `json:"up"`
	Down       int64  `json:"down"`
	AllTime    int64  `json:"allTime"`
	ExpiryTime int64  `json:"expiryTime"`
	Total      int64  `json:"total"`
	Reset      int    `json:"reset"`
	LastOnline int64  `json:"lastOnline"`
}

// NewClient creates a new 3x-ui API client.
// The client is optimized for low memory usage with minimal connection pooling.
func NewClient(host, username, password string) (*Client, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create cookie jar: %w", err)
	}

	// Optimized transport for minimal memory footprint
	transport := &http.Transport{
		MaxIdleConns:        config.MaxIdleConns,
		MaxIdleConnsPerHost: config.MaxIdleConns,
		IdleConnTimeout:     config.DefaultIdleConnTimeout,
		DisableCompression:  false,
		ForceAttemptHTTP2:   false,
	}

	return &Client{
		host:     strings.TrimSuffix(host, "/"),
		username: username,
		password: password,
		httpClient: &http.Client{
			Timeout:   config.DefaultHTTPTimeout,
			Jar:       jar,
			Transport: transport,
		},
		breaker: NewCircuitBreaker(config.CircuitBreakerMaxFailures, config.CircuitBreakerTimeout),
	}, nil
}

// Login authenticates with the 3x-ui panel.
// The session is valid for approximately 15 minutes.
func (c *Client) Login(ctx context.Context) error {
	return c.ensureLoggedIn(ctx, true)
}

// Ping checks if the 3x-ui panel is reachable without forcing re-authentication.
// Returns nil if session is valid or re-authentication succeeds.
func (c *Client) Ping(ctx context.Context) error {
	return c.ensureLoggedIn(ctx, false)
}

// ensureLoggedIn checks if the session is valid and re-authenticates if necessary.
// If force is true, it will always re-authenticate.
func (c *Client) ensureLoggedIn(ctx context.Context, force bool) error {
	if err := c.breaker.Execute(ctx, func() error {
		return c.doEnsureLoggedIn(ctx, force)
	}); err != nil {
		return err
	}
	return nil
}

func (c *Client) doEnsureLoggedIn(ctx context.Context, force bool) error {
	// Fast path: check if we have a valid session without locking
	c.mu.RLock()
	validSession := !force && time.Since(c.lastLogin) < config.XUISessionValidity
	c.mu.RUnlock()

	if validSession {
		return nil
	}

	// Slow path: need to re-authenticate
	c.mu.Lock()
	defer c.mu.Unlock()

	// Double-check after acquiring write lock
	if !force && time.Since(c.lastLogin) < config.XUISessionValidity {
		return nil
	}

	// Use singleflight to deduplicate concurrent login attempts
	// Only one goroutine will perform the login, others will wait for the result
	_, err, _ := c.loginGroup.Do("login", func() (interface{}, error) {
		return nil, RetryWithBackoff(ctx, config.XUIMaxRetries, config.XUIInitialRetryDelay, func() error {
			return c.doLogin(ctx)
		})
	})
	return err
}

// doLogin performs the actual login request.
// Must be called with c.mu held.
func (c *Client) doLogin(ctx context.Context) error {
	loginURL := fmt.Sprintf("%s/login", c.host)

	reader, err := marshalJSON(map[string]string{
		"username": c.username,
		"password": c.password,
	})
	if err != nil {
		return fmt.Errorf("failed to marshal login request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, loginURL, reader)
	if err != nil {
		return fmt.Errorf("failed to create login request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("login request failed: %w", err)
	}
	defer closeResponseBody(resp)

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, config.MaxResponseSize))
	if err != nil {
		return fmt.Errorf("failed to read login response: %w", err)
	}

	var apiResp APIResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return fmt.Errorf("failed to parse login response: %w", err)
	}

	if !apiResp.Success {
		return fmt.Errorf("login failed: %s", apiResp.Msg)
	}

	c.lastLogin = time.Now()
	logger.Info("3x-ui login successful",
		zap.String("session_valid_until", c.lastLogin.Add(config.XUISessionValidity).Format("15:04:05")))

	return nil
}

// AddClient creates a new client in the 3x-ui panel with auto-generated IDs.
func (c *Client) AddClient(ctx context.Context, inboundID int, email string, trafficBytes int64, expiryTime time.Time) (*ClientConfig, error) {
	clientID := utils.GenerateUUID()
	subID := utils.GenerateSubID()

	return c.AddClientWithID(ctx, inboundID, email, clientID, subID, trafficBytes, expiryTime, -1)
}

// AddClientWithID creates a new client in the 3x-ui panel with specified IDs.
// This is useful for atomic operations where the IDs are generated before the API call.
//
// Parameters:
//   - inboundID: The inbound ID in 3x-ui panel
//   - email: User identifier (usually Telegram username)
//   - clientID: UUID for the client
//   - subID: Subscription ID for URL generation
//   - trafficBytes: Traffic limit in bytes
//   - expiryTime: Subscription expiry time
//   - resetDays: Day of month for traffic reset (0 = no auto-renewal)
func (c *Client) AddClientWithID(ctx context.Context, inboundID int, email, clientID, subID string, trafficBytes int64, expiryTime time.Time, resetDays int) (*ClientConfig, error) {
	if err := c.ensureLoggedIn(ctx, false); err != nil {
		return nil, fmt.Errorf("authentication required: %w", err)
	}

	// Validate inputs
	if inboundID < 1 {
		return nil, fmt.Errorf("invalid inbound ID: %d", inboundID)
	}
	if clientID == "" {
		return nil, fmt.Errorf("client ID cannot be empty")
	}
	if subID == "" {
		return nil, fmt.Errorf("subscription ID cannot be empty")
	}

	if resetDays < 0 {
		resetDays = config.SubscriptionResetDay
	}

	// Build client settings in 3x-ui format
	clientSettings := map[string]interface{}{
		"clients": []map[string]interface{}{
			{
				"id":         clientID,
				"email":      email,
				"limitIp":    0,
				"totalGB":    trafficBytes,
				"expiryTime": getExpiryTimeMillis(expiryTime),
				"enable":     true,
				"flow":       "xtls-rprx-vision",
				"subId":      subID,
				"reset":      resetDays,
			},
		},
	}

	settingsJSON, err := json.Marshal(clientSettings)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal client settings: %w", err)
	}

	// Build request: id + settings (as JSON string)
	requestData := map[string]interface{}{
		"id":       inboundID,
		"settings": string(settingsJSON),
	}

	reader, err := marshalJSON(requestData)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// POST to /panel/api/inbounds/addClient
	addURL := fmt.Sprintf("%s/panel/api/inbounds/addClient", c.host)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, addURL, reader)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("add client request failed: %w", err)
	}
	defer closeResponseBody(resp)

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, config.MaxResponseSize))
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	// Log response at DEBUG level only (may contain sensitive data)
	logger.Debug("3x-ui addClient response",
		zap.Int("body_length", len(respBody)),
		zap.String("response_preview", truncateString(string(respBody), 200)))

	// Parse response
	var simpleResp struct {
		Success bool        `json:"success"`
		Msg     string      `json:"msg"`
		Obj     interface{} `json:"obj,omitempty"`
	}

	if err := json.Unmarshal(respBody, &simpleResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if !simpleResp.Success {
		return nil, fmt.Errorf("failed to add client: %s", simpleResp.Msg)
	}

	return &ClientConfig{
		ID:         clientID,
		Email:      email,
		TotalGB:    trafficBytes,
		ExpiryTime: getExpiryTimeMillis(expiryTime),
		Enable:     true,
		SubID:      subID,
		Reset:      config.SubscriptionResetDay,
	}, nil
}

// DeleteClient removes a client from the 3x-ui panel.
func (c *Client) DeleteClient(ctx context.Context, inboundID int, clientID string) error {
	if err := c.ensureLoggedIn(ctx, false); err != nil {
		return fmt.Errorf("authentication required: %w", err)
	}

	deleteURL := fmt.Sprintf("%s/panel/api/inbounds/%d/delClient/%s", c.host, inboundID, clientID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, deleteURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create delete request: %w", err)
	}

	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("delete client request failed: %w", err)
	}
	defer closeResponseBody(resp)

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, config.MaxResponseSize))
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	var apiResp APIResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}

	if !apiResp.Success {
		return fmt.Errorf("failed to delete client: %s", apiResp.Msg)
	}

	logger.Info("Successfully deleted client",
		zap.String("client_id", clientID),
		zap.Int("inbound_id", inboundID))
	return nil
}

// UpdateClient updates an existing client in the 3x-ui panel.
func (c *Client) UpdateClient(ctx context.Context, inboundID int, clientID, email, subID string, trafficBytes int64, expiryTime time.Time, tgID int64, comment string) error {
	if err := c.ensureLoggedIn(ctx, false); err != nil {
		return fmt.Errorf("authentication required: %w", err)
	}

	if clientID == "" {
		return fmt.Errorf("client ID cannot be empty")
	}

	clientSettings := map[string]interface{}{
		"clients": []map[string]interface{}{
			{
				"id":         clientID,
				"email":      email,
				"limitIp":    0,
				"totalGB":    trafficBytes,
				"expiryTime": getExpiryTimeMillis(expiryTime),
				"enable":     true,
				"flow":       "xtls-rprx-vision",
				"subId":      subID,
				"reset":      config.SubscriptionResetDay,
				"tgId":       fmt.Sprintf("%d", tgID),
				"comment":    comment,
			},
		},
	}

	settingsJSON, err := json.Marshal(clientSettings)
	if err != nil {
		return fmt.Errorf("failed to marshal client settings: %w", err)
	}

	requestData := map[string]interface{}{
		"id":       inboundID,
		"settings": string(settingsJSON),
	}

	reader, err := marshalJSON(requestData)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	// POST to /panel/api/inbounds/updateClient/{clientID}
	updateURL := fmt.Sprintf("%s/panel/api/inbounds/updateClient/%s", c.host, clientID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, updateURL, reader)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("update client request failed: %w", err)
	}
	defer closeResponseBody(resp)

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, config.MaxResponseSize))
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	var apiResp APIResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}

	if !apiResp.Success {
		return fmt.Errorf("failed to update client: %s", apiResp.Msg)
	}

	return nil
}

// GetClientTraffic retrieves traffic statistics for a client by email.
func (c *Client) GetClientTraffic(ctx context.Context, email string) (*ClientTraffic, error) {
	if err := c.ensureLoggedIn(ctx, false); err != nil {
		return nil, fmt.Errorf("authentication required: %w", err)
	}

	trafficURL := fmt.Sprintf("%s/panel/api/inbounds/getClientTraffics/%s", c.host, url.PathEscape(email))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, trafficURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("get client traffic request failed: %w", err)
	}
	defer closeResponseBody(resp)

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, config.MaxResponseSize))
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	var apiResp APIResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if !apiResp.Success {
		return nil, fmt.Errorf("failed to get client traffic: %s", apiResp.Msg)
	}

	var traffic ClientTraffic
	if err := json.Unmarshal(apiResp.Obj, &traffic); err != nil {
		return nil, fmt.Errorf("failed to parse traffic data: %w", err)
	}

	return &traffic, nil
}

// GetSubscriptionLink generates a subscription URL for the given subID.
func (c *Client) GetSubscriptionLink(baseURL, subID, subPath string) string {
	return fmt.Sprintf("%s/%s/%s", strings.TrimSuffix(baseURL, "/"), subPath, subID)
}

// GetExternalURL extracts the base URL (scheme + host) from a full URL.
func (c *Client) GetExternalURL(host string) string {
	return GetExternalURL(host)
}

func (c *Client) CircuitBreakerState() CircuitState {
	return c.breaker.State()
}

// GetExternalURL extracts the base URL (scheme + host) from a full URL.
// Returns the original host if URL parsing fails.
// Example: "http://example.com:2053/sub/abc123" -> "http://example.com:2053"
func GetExternalURL(host string) string {
	u, err := url.Parse(host)
	if err != nil {
		return host
	}
	return fmt.Sprintf("%s://%s", u.Scheme, u.Host)
}

// getExpiryTimeMillis returns expiry time in milliseconds.
// Returns 0 for zero time values (no expiry).
func getExpiryTimeMillis(expiryTime time.Time) int64 {
	if expiryTime.IsZero() {
		return 0
	}
	return expiryTime.UnixMilli()
}

// truncateString returns a truncated version of s with ellipsis if it exceeds maxLen.
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// RetryWithBackoff executes a function with exponential backoff retry.
// Exported for use in other packages that need retry logic for XUI operations.
func RetryWithBackoff(ctx context.Context, maxRetries int, initialDelay time.Duration, fn func() error) error {
	var lastErr error
	delay := initialDelay

	for i := 0; i < maxRetries; i++ {
		err := fn()
		if err == nil {
			return nil
		}
		lastErr = err

		if i < maxRetries-1 {
			logger.Warn("Retry after error",
				zap.Int("attempt", i+1),
				zap.Int("max_retries", maxRetries),
				zap.Error(err))

			select {
			case <-time.After(delay + time.Duration(rand.Int63n(int64(delay/2)))):
				delay *= 2 // Exponential backoff
			case <-ctx.Done():
				return fmt.Errorf("context cancelled: %w", ctx.Err())
			}
		}
	}

	return fmt.Errorf("after %d retries: %w", maxRetries, lastErr)
}

func (c *Client) Close() error {
	if transport, ok := c.httpClient.Transport.(*http.Transport); ok {
		transport.CloseIdleConnections()
	}
	return nil
}
