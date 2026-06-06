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
	"net/url"
	"strings"
	"time"

	"go.uber.org/zap"
	"rs8kvn_bot/internal/config"
	"rs8kvn_bot/internal/logger"
	"rs8kvn_bot/internal/utils"
)

func marshalJSON(v interface{}) (*bytes.Reader, error) {
	body, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return bytes.NewReader(body), nil
}

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

type Client struct {
	host       string
	apiToken   string
	httpClient *http.Client
	transport  *http.Transport
}

type APIResponse struct {
	Success bool            `json:"success"`
	Msg     string          `json:"msg"`
	Obj     json.RawMessage `json:"obj,omitempty"`
}

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

type Inbound struct {
	ID             int             `json:"id"`
	Up             int             `json:"up"`
	Down           int             `json:"down"`
	Total          int             `json:"total"`
	Remark         string          `json:"remark"`
	Enable         bool            `json:"enable"`
	ExpiryTime     int64           `json:"expiryTime"`
	Listen         string          `json:"listen"`
	Port           int             `json:"port"`
	Protocol       string          `json:"protocol"`
	Settings       json.RawMessage `json:"settings"`
	StreamSettings json.RawMessage `json:"streamSettings"`
	Tag            string          `json:"tag"`
	Sniffing       json.RawMessage `json:"sniffing"`
}

func (in *Inbound) GetTransport() string {
	if len(in.StreamSettings) == 0 {
		return ""
	}

	// Try modern format: StreamSettings is already a JSON object
	var netSettings struct {
		Network string `json:"network"`
	}
	if err := json.Unmarshal(in.StreamSettings, &netSettings); err == nil && netSettings.Network != "" {
		return netSettings.Network
	}

	// Fallback for old format: StreamSettings is a JSON-encoded string (legacy panel)
	var rawStr string
	if err := json.Unmarshal(in.StreamSettings, &rawStr); err != nil {
		logger.Debug("GetTransport: failed to unmarshal legacy streamSettings string",
			zap.Error(err))
		return ""
	}
	cleaned := strings.ReplaceAll(rawStr, "\\n", "\n")
	if err := json.Unmarshal([]byte(cleaned), &netSettings); err != nil {
		logger.Debug("GetTransport: failed to parse legacy streamSettings JSON",
			zap.Error(err))
		return ""
	}
	return netSettings.Network
}

func (in *Inbound) GetRequiredFlow() string {
	transport := in.GetTransport()
	switch transport {
	case "xhttp", "h2", "ws", "grpc", "grpcs":
		return ""
	default:
		return "xtls-rprx-vision"
	}
}

func NewClient(host, apiToken string) (*Client, error) {
	transport := &http.Transport{
		MaxIdleConns:        config.MaxIdleConns,
		MaxIdleConnsPerHost: config.MaxIdleConns,
		IdleConnTimeout:     config.DefaultIdleConnTimeout,
		DisableCompression:  false,
		ForceAttemptHTTP2:   false,
	}

	return &Client{
		host:     strings.TrimRight(host, "/"),
		apiToken: apiToken,
		httpClient: &http.Client{
			Timeout:   config.DefaultHTTPTimeout,
			Transport: transport,
		},
		transport: transport,
	}, nil
}

func (c *Client) Ping(ctx context.Context) error {
	statusURL := fmt.Sprintf("%s/panel/api/server/status", c.host)
	_, err := c.doHTTPRequest(ctx, http.MethodGet, statusURL, nil)
	return err
}

func (c *Client) doHTTPRequest(ctx context.Context, method, url string, bodyFn func() (io.Reader, error)) ([]byte, error) {
	var body io.Reader
	if bodyFn != nil {
		var err error
		body, err = bodyFn()
		if err != nil {
			return nil, err
		}
	}

	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiToken)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer closeResponseBody(resp)

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, config.MaxResponseSize))
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return respBody, fmt.Errorf("server returned HTTP %d: %s (url: %s)", resp.StatusCode, truncateString(string(respBody), 200), url)
	}

	return respBody, nil
}

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

func (c *Client) AddClientWithID(ctx context.Context, inboundID int, email, clientID, subID string, trafficBytes int64, expiryTime time.Time, resetDays int) (*ClientConfig, error) {
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

	flow, flowErr := c.getRequiredFlow(ctx, inboundID)
	if flowErr != nil {
		return nil, fmt.Errorf("failed to determine flow: %w", flowErr)
	}

	var result *ClientConfig
	errRetry := RetryWithBackoff(ctx, config.XUIMaxRetries, config.XUIInitialRetryDelay, func() error {
		var innerErr error
		result, innerErr = c.doAddClientWithID(ctx, inboundID, email, clientID, subID, trafficBytes, expiryTime, resetDays, flow)
		return innerErr
	})
	return result, errRetry
}

// AddTrialClient adds a trial client to a single source and returns the created client.
func (c *Client) AddTrialClient(ctx context.Context, inboundID int, email, clientID, subID string, trafficBytes int64, expiryTime time.Time, resetDays int) (*ClientConfig, error) {
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

	flow, flowErr := c.getRequiredFlow(ctx, inboundID)
	if flowErr != nil {
		return nil, fmt.Errorf("failed to determine flow: %w", flowErr)
	}

	var result *ClientConfig
	errRetry := RetryWithBackoff(ctx, config.XUIMaxRetries, config.XUIInitialRetryDelay, func() error {
		var innerErr error
		result, innerErr = c.doAddClientWithID(ctx, inboundID, email, clientID, subID, trafficBytes, expiryTime, resetDays, flow)
		return innerErr
	})
	return result, errRetry
}

func (c *Client) doAddClientWithID(ctx context.Context, inboundID int, email, clientID, subID string, trafficBytes int64, expiryTime time.Time, resetDays int, flow string) (*ClientConfig, error) {
	clientObj := map[string]interface{}{
		"id":         clientID,
		"email":      email,
		"limitIp":    0,
		"totalGB":    trafficBytes,
		"expiryTime": getExpiryTimeMillis(expiryTime),
		"enable":     true,
		"flow":       flow,
		"subId":      subID,
		"reset":      resetDays,
	}

	requestData := map[string]interface{}{
		"client":     clientObj,
		"inboundIds": []int{inboundID},
	}

	addURL := fmt.Sprintf("%s/panel/api/clients/add", c.host)

	respBody, err := c.doHTTPRequest(ctx, http.MethodPost, addURL, func() (io.Reader, error) {
		return marshalJSON(requestData)
	})
	if err != nil {
		return nil, err
	}

	logger.Debug("3x-ui clients/add response",
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

func (c *Client) DeleteClient(ctx context.Context, email string) error {
	if email == "" {
		return fmt.Errorf("email cannot be empty")
	}
	return RetryWithBackoff(ctx, config.XUIMaxRetries, config.XUIInitialRetryDelay, func() error {
		return c.doDeleteClient(ctx, email)
	})
}

func (c *Client) doDeleteClient(ctx context.Context, email string) error {
	if email == "" {
		return fmt.Errorf("email cannot be empty")
	}

	delURL := fmt.Sprintf("%s/panel/api/clients/del/%s", c.host, url.PathEscape(email))

	respBody, err := c.doHTTPRequest(ctx, http.MethodPost, delURL, nil)
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
		zap.String("email", email))
	return nil
}

func (c *Client) UpdateClient(ctx context.Context, inboundID int, currentEmail, clientID, email, subID string, trafficBytes int64, expiryTime time.Time, tgID int64, comment string) error {
	if clientID == "" {
		return fmt.Errorf("client ID cannot be empty")
	}
	if currentEmail == "" {
		return fmt.Errorf("current email cannot be empty")
	}
	if inboundID < 1 {
		return fmt.Errorf("invalid inbound ID: %d", inboundID)
	}

	return RetryWithBackoff(ctx, config.XUIMaxRetries, config.XUIInitialRetryDelay, func() error {
		return c.doUpdateClient(ctx, inboundID, currentEmail, clientID, email, subID, trafficBytes, expiryTime, tgID, comment)
	})
}

func (c *Client) doUpdateClient(ctx context.Context, inboundID int, currentEmail, clientID, email, subID string, trafficBytes int64, expiryTime time.Time, tgID int64, comment string) error {
	// Determine flow based on the actual inbound transport (tcp vs xhttp etc.)
	// This prevents sending wrong flow value during full-row replace on /clients/update.
	flow, flowErr := c.getRequiredFlow(ctx, inboundID)
	if flowErr != nil {
		logger.Debug("Failed to get required flow for update, using default xtls-rprx-vision", zap.Error(flowErr))
		flow = "xtls-rprx-vision"
	}

	clientObj := map[string]interface{}{
		"id":         clientID,
		"email":      email,
		"limitIp":    0,
		"totalGB":    trafficBytes,
		"expiryTime": getExpiryTimeMillis(expiryTime),
		"enable":     true,
		"flow":       flow,
		"subId":      subID,
		"reset":      config.SubscriptionResetDay,
		"tgId":       fmt.Sprintf("%d", tgID),
		"comment":    comment,
	}

	updateURL := fmt.Sprintf("%s/panel/api/clients/update/%s", c.host, url.PathEscape(currentEmail))

	respBody, err := c.doHTTPRequest(ctx, http.MethodPost, updateURL, func() (io.Reader, error) {
		return marshalJSON(clientObj)
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

	logger.Info("Successfully updated client via new API",
		zap.String("current_email", currentEmail),
		zap.String("new_email", email))

	return nil
}

func (c *Client) GetClientTraffic(ctx context.Context, email string) (*ClientTraffic, error) {
	var result *ClientTraffic
	err := RetryWithBackoff(ctx, config.XUIMaxRetries, config.XUIInitialRetryDelay, func() error {
		var err error
		result, err = c.doGetClientTraffic(ctx, email)
		return err
	})
	return result, err
}

var ErrClientNotFound = errors.New("client not found")

func (c *Client) doGetClientTraffic(ctx context.Context, email string) (*ClientTraffic, error) {
	trafficURL := fmt.Sprintf("%s/panel/api/clients/traffic/%s", c.host, url.PathEscape(email))

	respBody, err := c.doHTTPRequest(ctx, http.MethodGet, trafficURL, nil)
	if err != nil {
		return nil, err
	}

	var apiResp APIResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if !apiResp.Success {
		if strings.Contains(strings.ToLower(apiResp.Msg), "client not found") {
			return nil, ErrClientNotFound
		}
		return nil, fmt.Errorf("failed to get client traffic: %s", apiResp.Msg)
	}

	var traffic ClientTraffic
	if err := json.Unmarshal(apiResp.Obj, &traffic); err != nil {
		return nil, fmt.Errorf("failed to parse traffic data: %w", err)
	}

	return &traffic, nil
}

func (c *Client) GetInbound(ctx context.Context, inboundID int) (*Inbound, error) {
	return c.doGetInbound(ctx, inboundID)
}

func (c *Client) doGetInbound(ctx context.Context, inboundID int) (*Inbound, error) {
	inboundURL := fmt.Sprintf("%s/panel/api/inbounds/get/%d", c.host, inboundID)

	respBody, err := c.doHTTPRequest(ctx, http.MethodGet, inboundURL, nil)
	if err != nil {
		return nil, err
	}

	var apiResp APIResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if !apiResp.Success {
		return nil, fmt.Errorf("failed to get inbound: %s", apiResp.Msg)
	}

	var inbound Inbound
	if err := json.Unmarshal(apiResp.Obj, &inbound); err != nil {
		return nil, fmt.Errorf("failed to parse inbound data: %w", err)
	}

	return &inbound, nil
}

func (c *Client) getRequiredFlow(ctx context.Context, inboundID int) (string, error) {
	inbound, err := c.doGetInbound(ctx, inboundID)
	if err != nil {
		logger.Debug("Failed to get inbound for flow, using default",
			zap.Error(err))
		return "xtls-rprx-vision", nil
	}

	return inbound.GetRequiredFlow(), nil
}

func (c *Client) Close() error {
	if c.transport != nil {
		c.transport.CloseIdleConnections()
	}
	return nil
}

func getExpiryTimeMillis(expiryTime time.Time) int64 {
	if expiryTime.IsZero() {
		return 0
	}
	return expiryTime.UnixMilli()
}

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func isRetryable(err error) bool {
	if err == nil {
		return false
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
	if strings.Contains(msg, "no such host") ||
		strings.Contains(msg, "temporary failure in name resolution") ||
		strings.Contains(msg, "name or service not known") ||
		strings.Contains(msg, "nodename nor servname provided") {
		return false
	}
	return true
}

func RetryWithBackoff(ctx context.Context, maxRetries int, initialDelay time.Duration, fn func() error) error {
	if maxRetries <= 0 {
		return errors.New("maxRetries must be positive")
	}
	if initialDelay <= 0 {
		return errors.New("initialDelay must be positive")
	}

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
			case <-time.After(delay + time.Duration(rand.Int63n(int64(delay/2)))): //nolint:gosec
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

// Login and TestForceSessionExpiry are no-op stubs kept temporarily
// for e2e test compatibility after the move to Bearer token auth.
// They can be removed once the e2e tests are updated to the new auth model.
func (c *Client) Login(ctx context.Context) error {
	return nil
}

func (c *Client) TestForceSessionExpiry() {
	// no-op in the new token-based client
}
