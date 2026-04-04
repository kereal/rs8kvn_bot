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
	"sync/atomic"
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
	host            string
	username        string
	password        string
	httpClient      *http.Client
	transport       *http.Transport
	mu              sync.RWMutex
	lastLogin       time.Time
	sessionValidity time.Duration
	breaker         *CircuitBreaker
	loginGroup      singleflight.Group // Deduplicates concurrent login attempts

	// Observability counters
	loginCount       int64
	sessionFailCount int64
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
// sessionMaxAge specifies how long the panel session remains valid (must match panel settings).
func NewClient(host, username, password string, sessionMaxAge time.Duration) (*Client, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create cookie jar: %w", err)
	}

	transport := &http.Transport{
		MaxIdleConns:        config.MaxIdleConns,
		MaxIdleConnsPerHost: config.MaxIdleConns,
		IdleConnTimeout:     config.DefaultIdleConnTimeout,
		DisableCompression:  false,
		ForceAttemptHTTP2:   false,
	}

	return &Client{
		host:            strings.TrimSuffix(host, "/"),
		username:        username,
		password:        password,
		sessionValidity: sessionMaxAge,
		httpClient: &http.Client{
			Timeout:   config.DefaultHTTPTimeout,
			Jar:       jar,
			Transport: transport,
		},
		transport: transport,
		breaker:   NewCircuitBreaker(config.CircuitBreakerMaxFailures, config.CircuitBreakerTimeout),
	}, nil
}

// Login authenticates with the 3x-ui panel.
func (c *Client) Login(ctx context.Context) error {
	return c.ensureLoggedIn(ctx, true)
}

// Ping checks if the 3x-ui panel is reachable by verifying the session with a real request.
func (c *Client) Ping(ctx context.Context) error {
	c.mu.RLock()
	hasRecentLogin := time.Since(c.lastLogin) < c.sessionValidity
	c.mu.RUnlock()

	if !hasRecentLogin {
		return c.ensureLoggedIn(ctx, false)
	}

	ctx, cancel := context.WithTimeout(ctx, config.XUISessionVerifyTimeout)
	defer cancel()
	if !c.verifySession(ctx) {
		return c.ensureLoggedIn(ctx, false)
	}
	return nil
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
	validSession := !force && time.Since(c.lastLogin) < c.sessionValidity
	c.mu.RUnlock()

	if validSession {
		return nil
	}

	// Slow path: need to re-authenticate
	c.mu.Lock()
	defer c.mu.Unlock()

	// Double-check after acquiring write lock
	if !force && time.Since(c.lastLogin) < c.sessionValidity {
		return nil
	}

	// Use singleflight to deduplicate concurrent login attempts
	_, err, _ := c.loginGroup.Do("login", func() (interface{}, error) {
		// Try to verify existing session first before forcing re-login
		verifyCtx, verifyCancel := context.WithTimeout(ctx, config.XUISessionVerifyTimeout)
		defer verifyCancel()

		if !force && c.verifySession(verifyCtx) {
			c.lastLogin = time.Now()
			logger.Debug("XUI session verified successfully, no re-login needed")
			return nil, nil
		}

		return nil, RetryWithBackoff(ctx, config.XUIMaxRetries, config.XUIInitialRetryDelay, func() error {
			return c.doLogin(ctx)
		})
	})
	return err
}

// verifySession checks if the current session is still valid by making a real API request.
// Returns true if the session is valid, false otherwise.
func (c *Client) verifySession(ctx context.Context) bool {
	statusURL := fmt.Sprintf("%s/panel/api/server/status", c.host)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, statusURL, nil)
	if err != nil {
		return false
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		atomic.AddInt64(&c.sessionFailCount, 1)
		return false
	}
	defer closeResponseBody(resp)

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		logger.Debug("XUI session verification failed",
			zap.Int("status", resp.StatusCode),
			zap.String("body", truncateString(string(body), 100)))
		atomic.AddInt64(&c.sessionFailCount, 1)
		return false
	}

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, config.MaxResponseSize))
	if err != nil {
		atomic.AddInt64(&c.sessionFailCount, 1)
		return false
	}

	var apiResp APIResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		atomic.AddInt64(&c.sessionFailCount, 1)
		return false
	}

	return apiResp.Success
}

// doLogin performs the actual login request.
// Must be called with c.mu held.
func (c *Client) doLogin(ctx context.Context) error {
	// Clear stale connections before re-authentication
	c.transport.CloseIdleConnections()

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

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("login returned HTTP %d: %s", resp.StatusCode, truncateString(string(respBody), 200))
	}

	var apiResp APIResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return fmt.Errorf("failed to parse login response: %w", err)
	}

	if !apiResp.Success {
		return fmt.Errorf("login failed: %s", apiResp.Msg)
	}

	c.lastLogin = time.Now()
	atomic.AddInt64(&c.loginCount, 1)
	logger.Info("3x-ui login successful",
		zap.String("session_valid_until", c.lastLogin.Add(c.sessionValidity).Format("15:04:05")))

	return nil
}

// doRequestWithAuthRetry executes an HTTP request and automatically handles auth failures.
// If the response status code is 401/302/307, it performs a re-login and retries the request once.
// The response body is always read and closed before returning.
func (c *Client) doRequestWithAuthRetry(ctx context.Context, fn func() (statusCode int, body []byte, err error)) ([]byte, error) {
	statusCode, body, err := fn()
	if err != nil {
		return body, err
	}

	if statusCode == http.StatusUnauthorized ||
		statusCode == http.StatusFound ||
		statusCode == http.StatusTemporaryRedirect {
		logger.Info("XUI auto-relogin triggered",
			zap.Int("http_status", statusCode))

		// Invalidate session
		c.mu.Lock()
		c.lastLogin = time.Time{}
		c.mu.Unlock()

		// Clear stale connections
		c.transport.CloseIdleConnections()

		// Re-login
		if loginErr := c.doLogin(ctx); loginErr != nil {
			return body, fmt.Errorf("auto-relogin failed: %w", loginErr)
		}

		logger.Debug("XUI auto-relogin succeeded, request retried")

		// Retry the request once
		_, body, err = fn()
		return body, err
	}

	return body, nil
}

// AddClient creates a new client in the 3x-ui panel with auto-generated IDs.
func (c *Client) AddClient(ctx context.Context, inboundID int, email string, trafficBytes int64, expiryTime time.Time) (*ClientConfig, error) {
	clientID, err := utils.GenerateUUID()
	if err != nil {
		return nil, fmt.Errorf("generate client id: %w", err)
	}
	subID, err := utils.GenerateSubID()
	if err != nil {
		return nil, fmt.Errorf("generate sub id: %w", err)
	}

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

	var result *ClientConfig
	err := RetryWithBackoff(ctx, config.XUIMaxRetries, config.XUIInitialRetryDelay, func() error {
		var err error
		result, err = c.doAddClientWithID(ctx, inboundID, email, clientID, subID, trafficBytes, expiryTime, resetDays)
		return err
	})
	return result, err
}

// doAddClientWithID performs the actual add client request.
func (c *Client) doAddClientWithID(ctx context.Context, inboundID int, email, clientID, subID string, trafficBytes int64, expiryTime time.Time, resetDays int) (*ClientConfig, error) {
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

	var respBody []byte
	respBody, err = c.doRequestWithAuthRetry(ctx, func() (int, []byte, error) {
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodPost, addURL, reader)
		if reqErr != nil {
			return 0, nil, fmt.Errorf("failed to create request: %w", reqErr)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")

		resp, reqErr := c.httpClient.Do(req)
		if reqErr != nil {
			return 0, nil, fmt.Errorf("add client request failed: %w", reqErr)
		}
		defer closeResponseBody(resp)

		body, reqErr := io.ReadAll(io.LimitReader(resp.Body, config.MaxResponseSize))
		if reqErr != nil {
			return 0, nil, fmt.Errorf("failed to read response: %w", reqErr)
		}

		if resp.StatusCode != http.StatusOK {
			return resp.StatusCode, body, fmt.Errorf("server returned HTTP %d: %s", resp.StatusCode, truncateString(string(body), 200))
		}

		return resp.StatusCode, body, nil
	})
	if err != nil {
		return nil, err
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

	return RetryWithBackoff(ctx, config.XUIMaxRetries, config.XUIInitialRetryDelay, func() error {
		return c.doDeleteClient(ctx, inboundID, clientID)
	})
}

// doDeleteClient performs the actual delete client request.
func (c *Client) doDeleteClient(ctx context.Context, inboundID int, clientID string) error {
	deleteURL := fmt.Sprintf("%s/panel/api/inbounds/%d/delClient/%s", c.host, inboundID, clientID)

	var respBody []byte
	respBody, err := c.doRequestWithAuthRetry(ctx, func() (int, []byte, error) {
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodPost, deleteURL, nil)
		if reqErr != nil {
			return 0, nil, fmt.Errorf("failed to create delete request: %w", reqErr)
		}
		req.Header.Set("Accept", "application/json")

		resp, reqErr := c.httpClient.Do(req)
		if reqErr != nil {
			return 0, nil, fmt.Errorf("delete client request failed: %w", reqErr)
		}
		defer closeResponseBody(resp)

		body, reqErr := io.ReadAll(io.LimitReader(resp.Body, config.MaxResponseSize))
		if reqErr != nil {
			return 0, nil, fmt.Errorf("failed to read response: %w", reqErr)
		}

		if resp.StatusCode != http.StatusOK {
			return resp.StatusCode, body, fmt.Errorf("server returned HTTP %d: %s", resp.StatusCode, truncateString(string(body), 200))
		}

		return resp.StatusCode, body, nil
	})
	if err != nil {
		return err
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

	return RetryWithBackoff(ctx, config.XUIMaxRetries, config.XUIInitialRetryDelay, func() error {
		return c.doUpdateClient(ctx, inboundID, clientID, email, subID, trafficBytes, expiryTime, tgID, comment)
	})
}

// doUpdateClient performs the actual update client request.
func (c *Client) doUpdateClient(ctx context.Context, inboundID int, clientID, email, subID string, trafficBytes int64, expiryTime time.Time, tgID int64, comment string) error {
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

	var respBody []byte
	respBody, err = c.doRequestWithAuthRetry(ctx, func() (int, []byte, error) {
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodPost, updateURL, reader)
		if reqErr != nil {
			return 0, nil, fmt.Errorf("failed to create request: %w", reqErr)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")

		resp, reqErr := c.httpClient.Do(req)
		if reqErr != nil {
			return 0, nil, fmt.Errorf("update client request failed: %w", reqErr)
		}
		defer closeResponseBody(resp)

		body, reqErr := io.ReadAll(io.LimitReader(resp.Body, config.MaxResponseSize))
		if reqErr != nil {
			return 0, nil, fmt.Errorf("failed to read response: %w", reqErr)
		}

		if resp.StatusCode != http.StatusOK {
			return resp.StatusCode, body, fmt.Errorf("server returned HTTP %d: %s", resp.StatusCode, truncateString(string(body), 200))
		}

		return resp.StatusCode, body, nil
	})
	if err != nil {
		return err
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

	var result *ClientTraffic
	err := RetryWithBackoff(ctx, config.XUIMaxRetries, config.XUIInitialRetryDelay, func() error {
		var err error
		result, err = c.doGetClientTraffic(ctx, email)
		return err
	})
	return result, err
}

// doGetClientTraffic performs the actual get client traffic request.
func (c *Client) doGetClientTraffic(ctx context.Context, email string) (*ClientTraffic, error) {
	trafficURL := fmt.Sprintf("%s/panel/api/inbounds/getClientTraffics/%s", c.host, url.PathEscape(email))

	var respBody []byte
	respBody, err := c.doRequestWithAuthRetry(ctx, func() (int, []byte, error) {
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, trafficURL, nil)
		if reqErr != nil {
			return 0, nil, fmt.Errorf("failed to create request: %w", reqErr)
		}
		req.Header.Set("Accept", "application/json")

		resp, reqErr := c.httpClient.Do(req)
		if reqErr != nil {
			return 0, nil, fmt.Errorf("get client traffic request failed: %w", reqErr)
		}
		defer closeResponseBody(resp)

		body, reqErr := io.ReadAll(io.LimitReader(resp.Body, config.MaxResponseSize))
		if reqErr != nil {
			return 0, nil, fmt.Errorf("failed to read response: %w", reqErr)
		}

		if resp.StatusCode != http.StatusOK {
			return resp.StatusCode, body, fmt.Errorf("server returned HTTP %d: %s", resp.StatusCode, truncateString(string(body), 200))
		}

		return resp.StatusCode, body, nil
	})
	if err != nil {
		return nil, err
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

// LoginCount returns the number of successful logins performed.
func (c *Client) LoginCount() int64 {
	return atomic.LoadInt64(&c.loginCount)
}

// SessionFailCount returns the number of session verification failures.
func (c *Client) SessionFailCount() int64 {
	return atomic.LoadInt64(&c.sessionFailCount)
}

// TestForceSessionExpiry sets lastLogin to the past to force re-authentication.
// This is only for testing purposes.
func (c *Client) TestForceSessionExpiry() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastLogin = time.Time{}
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

// isRetryable checks if an error is worth retrying.
// DNS errors (no such host) are not retryable as they indicate a configuration problem.
func isRetryable(err error) bool {
	if err == nil {
		return true
	}
	msg := strings.ToLower(err.Error())
	return !strings.Contains(msg, "no such host")
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

		if !isRetryable(err) {
			logger.Warn("Non-retryable XUI error, failing immediately",
				zap.Error(err))
			return err
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
	if c.transport != nil {
		c.transport.CloseIdleConnections()
	}
	return nil
}
