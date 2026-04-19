package xui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
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
	"rs8kvn_bot/internal/metrics"
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
	// Double-check with write lock, then release before entering singleflight
	// to avoid blocking all other API calls during retry backoff.
	c.mu.Lock()
	if !force && time.Since(c.lastLogin) < c.sessionValidity {
		c.mu.Unlock()
		return nil
	}
	c.mu.Unlock()

	// Use singleflight to deduplicate concurrent login attempts.
	// Mutex is NOT held here — only lastLogin updates are protected.
	_, err, _ := c.loginGroup.Do("login", func() (interface{}, error) {
		// Try to verify existing session first before forcing re-login
		verifyCtx, verifyCancel := context.WithTimeout(ctx, config.XUISessionVerifyTimeout)
		defer verifyCancel()

		if !force && c.verifySession(verifyCtx) {
			c.mu.Lock()
			c.lastLogin = time.Now()
			c.mu.Unlock()
			logger.Debug("XUI session verified successfully, no re-login needed")
			return nil, nil
		}

		if err := RetryWithBackoff(ctx, config.XUIMaxRetries, config.XUIInitialRetryDelay, func() error {
			return c.doLogin(ctx)
		}); err != nil {
			return nil, err
		}

		return nil, nil
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

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, config.MaxResponseSize))
	if err != nil {
		atomic.AddInt64(&c.sessionFailCount, 1)
		return false
	}

	if resp.StatusCode != http.StatusOK {
		logger.Debug("XUI session verification failed",
			zap.Int("status", resp.StatusCode),
			zap.String("body", truncateString(string(respBody), 100)))
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

	c.mu.Lock()
	c.lastLogin = time.Now()
	c.mu.Unlock()
	atomic.AddInt64(&c.loginCount, 1)
	logger.Info("3x-ui login successful",
		zap.String("session_valid_until", c.lastLogin.Add(c.sessionValidity).Format("2006-01-02 15:04:05")))

	return nil
}

// doHTTPRequest creates and executes an HTTP request with standard headers and response handling.
// It is safe for use with doRequestWithAuthRetry — the request is recreated on each retry,
// avoiding issues with consumed request bodies (e.g., bytes.Reader at EOF).
func (c *Client) doHTTPRequest(ctx context.Context, method, url string, bodyFn func() (io.Reader, error)) ([]byte, error) {
	return c.doRequestWithAuthRetry(ctx, func() (int, []byte, error) {
		var body io.Reader
		if bodyFn != nil {
			var err error
			body, err = bodyFn()
			if err != nil {
				return 0, nil, err
			}
		}

		req, err := http.NewRequestWithContext(ctx, method, url, body)
		if err != nil {
			return 0, nil, fmt.Errorf("failed to create request: %w", err)
		}
		req.Header.Set("Accept", "application/json")
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return 0, nil, fmt.Errorf("request failed: %w", err)
		}
		defer closeResponseBody(resp)

		respBody, err := io.ReadAll(io.LimitReader(resp.Body, config.MaxResponseSize))
		if err != nil {
			return 0, nil, fmt.Errorf("failed to read response: %w", err)
		}

		if resp.StatusCode != http.StatusOK {
			var err error
			switch resp.StatusCode {
			case http.StatusUnauthorized:
				err = fmt.Errorf("%w: HTTP %d: %s", ErrUnauthorized, resp.StatusCode, truncateString(string(respBody), 200))
			case http.StatusForbidden:
				err = fmt.Errorf("%w: HTTP %d: %s", ErrForbidden, resp.StatusCode, truncateString(string(respBody), 200))
			case http.StatusNotFound:
				err = fmt.Errorf("%w: HTTP %d: %s", ErrNotFound, resp.StatusCode, truncateString(string(respBody), 200))
			case http.StatusBadRequest:
				err = fmt.Errorf("%w: HTTP %d: %s", ErrBadRequest, resp.StatusCode, truncateString(string(respBody), 200))
			case 500, 502, 503, 504:
				err = fmt.Errorf("%w: HTTP %d: %s", ErrServerError, resp.StatusCode, truncateString(string(respBody), 200))
			default:
				err = fmt.Errorf("server returned HTTP %d: %s", resp.StatusCode, truncateString(string(respBody), 200))
			}
			return resp.StatusCode, respBody, err
		}

		return resp.StatusCode, respBody, nil
	})
}

// doRequestWithAuthRetry executes an HTTP request and automatically handles auth failures.
// If the response status code is 401, it performs a re-login (through circuit breaker)
// and retries the request once.
// Note: 302/307 redirects are NOT checked here because Go's http.Client follows them
// automatically — by the time we see the response, it's already the final destination.
// The response body is always read and closed before returning.
func (c *Client) doRequestWithAuthRetry(ctx context.Context, fn func() (statusCode int, body []byte, err error)) ([]byte, error) {
	statusCode, body, err := fn()

	// Check auth status code first — the closure returns both statusCode and error
	// for non-200 responses, so we must check statusCode before err to catch auth failures.
	if statusCode == http.StatusUnauthorized {
		logger.Info("XUI auto-relogin triggered",
			zap.Int("http_status", statusCode))

		// Clear stale connections
		c.transport.CloseIdleConnections()

		// Re-login through circuit breaker with proper mutex handling.
		// ensureLoggedIn manages its own locking and goes through the breaker.
		if loginErr := c.ensureLoggedIn(ctx, true); loginErr != nil {
			return body, fmt.Errorf("auto-relogin failed: %w", loginErr)
		}

		logger.Debug("XUI auto-relogin succeeded, request retried")

		// Retry the request once
		_, body, err = fn()
		return body, err
	}

	if err != nil {
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
	start := time.Now()
	defer func() {
		metrics.XUIRequestDuration.WithLabelValues("AddClientWithID").Observe(time.Since(start).Seconds())
	}()

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
	if err != nil {
		metrics.XUIRequestsTotal.WithLabelValues("AddClientWithID", "error").Inc()
	} else {
		metrics.XUIRequestsTotal.WithLabelValues("AddClientWithID", "success").Inc()
	}
	return result, err
}

// doAddClientWithID performs the actual add client request.
func (c *Client) doAddClientWithID(ctx context.Context, inboundID int, email, clientID, subID string, trafficBytes int64, expiryTime time.Time, resetDays int) (*ClientConfig, error) {
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

	requestData := map[string]interface{}{
		"id":       inboundID,
		"settings": string(settingsJSON),
	}

	addURL := fmt.Sprintf("%s/panel/api/inbounds/addClient", c.host)

	respBody, err := c.doHTTPRequest(ctx, http.MethodPost, addURL, func() (io.Reader, error) {
		return marshalJSON(requestData)
	})
	if err != nil {
		return nil, err
	}

	logger.Debug("3x-ui addClient response",
		zap.Int("body_length", len(respBody)),
		zap.String("response_preview", truncateString(string(respBody), 200)))

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
		Reset:      resetDays,
	}, nil
}

// DeleteClient removes a client from the 3x-ui panel.
func (c *Client) DeleteClient(ctx context.Context, inboundID int, clientID string) error {
	start := time.Now()
	defer func() {
		metrics.XUIRequestDuration.WithLabelValues("DeleteClient").Observe(time.Since(start).Seconds())
	}()

	if err := c.ensureLoggedIn(ctx, false); err != nil {
		return fmt.Errorf("authentication required: %w", err)
	}

	err := RetryWithBackoff(ctx, config.XUIMaxRetries, config.XUIInitialRetryDelay, func() error {
		return c.doDeleteClient(ctx, inboundID, clientID)
	})
	if err != nil {
		metrics.XUIRequestsTotal.WithLabelValues("DeleteClient", "error").Inc()
	} else {
		metrics.XUIRequestsTotal.WithLabelValues("DeleteClient", "success").Inc()
	}
	return err
}

// doDeleteClient performs the actual delete client request.
func (c *Client) doDeleteClient(ctx context.Context, inboundID int, clientID string) error {
	deleteURL := fmt.Sprintf("%s/panel/api/inbounds/%d/delClient/%s", c.host, inboundID, clientID)

	respBody, err := c.doHTTPRequest(ctx, http.MethodPost, deleteURL, nil)
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
	start := time.Now()
	defer func() {
		metrics.XUIRequestDuration.WithLabelValues("UpdateClient").Observe(time.Since(start).Seconds())
	}()

	if err := c.ensureLoggedIn(ctx, false); err != nil {
		return fmt.Errorf("authentication required: %w", err)
	}

	if clientID == "" {
		return fmt.Errorf("client ID cannot be empty")
	}

	err := RetryWithBackoff(ctx, config.XUIMaxRetries, config.XUIInitialRetryDelay, func() error {
		return c.doUpdateClient(ctx, inboundID, clientID, email, subID, trafficBytes, expiryTime, tgID, comment)
	})
	if err != nil {
		metrics.XUIRequestsTotal.WithLabelValues("UpdateClient", "error").Inc()
	} else {
		metrics.XUIRequestsTotal.WithLabelValues("UpdateClient", "success").Inc()
	}
	return err
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

	updateURL := fmt.Sprintf("%s/panel/api/inbounds/updateClient/%s", c.host, clientID)

	respBody, err := c.doHTTPRequest(ctx, http.MethodPost, updateURL, func() (io.Reader, error) {
		return marshalJSON(requestData)
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
	start := time.Now()
	defer func() {
		metrics.XUIRequestDuration.WithLabelValues("GetClientTraffic").Observe(time.Since(start).Seconds())
	}()

	if err := c.ensureLoggedIn(ctx, false); err != nil {
		return nil, fmt.Errorf("authentication required: %w", err)
	}

	var result *ClientTraffic
	err := RetryWithBackoff(ctx, config.XUIMaxRetries, config.XUIInitialRetryDelay, func() error {
		var err error
		result, err = c.doGetClientTraffic(ctx, email)
		return err
	})
	if err != nil {
		metrics.XUIRequestsTotal.WithLabelValues("GetClientTraffic", "error").Inc()
	} else {
		metrics.XUIRequestsTotal.WithLabelValues("GetClientTraffic", "success").Inc()
	}
	return result, err
}

// doGetClientTraffic performs the actual get client traffic request.
func (c *Client) doGetClientTraffic(ctx context.Context, email string) (*ClientTraffic, error) {
	trafficURL := fmt.Sprintf("%s/panel/api/inbounds/getClientTraffics/%s", c.host, url.PathEscape(email))

	respBody, err := c.doHTTPRequest(ctx, http.MethodGet, trafficURL, nil)
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
// DNS errors (no such host, name resolution failures) are not retryable
// as they indicate a configuration problem that won't resolve on its own.
// 4xx HTTP errors (client errors) are non-retryable; 5xx errors are retryable.
func isRetryable(err error) bool {
	if err == nil {
		return true
	}
	// Check typed XUI errors first
	switch {
	case errors.Is(err, ErrBadRequest),
		errors.Is(err, ErrForbidden),
		errors.Is(err, ErrNotFound),
		errors.Is(err, ErrUnauthorized):
		return false
	case errors.Is(err, ErrServerError):
		return true
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return false
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	msg := strings.ToLower(err.Error())
	// Common DNS error patterns across platforms and Go versions
	if strings.Contains(msg, "no such host") ||
		strings.Contains(msg, "temporary failure in name resolution") ||
		strings.Contains(msg, "name or service not known") ||
		strings.Contains(msg, "nodename nor servname provided") {
		return false
	}
	return true
}

// RetryWithBackoff executes a function with exponential backoff retry.
// RetryWithBackoff retries the provided function up to maxRetries using exponential backoff with jitter.
//
// It calls fn repeatedly until it succeeds or a non-retryable error is returned. Between attempts it waits,
// starting from initialDelay and doubling the delay after each wait (with added jitter). The function respects
// context cancellation and will return early if ctx is done. If all attempts fail, it returns an error wrapping
// the last encountered error.
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
			logger.Debug("Retry after error",
				zap.Int("attempt", i+1),
				zap.Int("max_retries", maxRetries),
				zap.Error(err))

			select {
			case <-time.After(delay + time.Duration(rand.Int63n(int64(delay/2)))): //nolint:gosec // G404: math/rand is sufficient for jitter, crypto/rand overhead unnecessary
				delay *= 2
			case <-ctx.Done():
				return fmt.Errorf("context cancelled: %w", ctx.Err())
			}
		}
	}

	logger.Warn("XUI operation failed after retries",
		zap.Int("retries", maxRetries),
		zap.Error(lastErr))

	return fmt.Errorf("after %d retries: %w", maxRetries, lastErr)
}

func (c *Client) Close() error {
	if c.transport != nil {
		c.transport.CloseIdleConnections()
	}
	return nil
}
