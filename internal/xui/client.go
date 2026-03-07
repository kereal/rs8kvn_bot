package xui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"sync"
	"time"

	"tgvpn_go/internal/logger"
)

// maxResponseSize is the maximum allowed HTTP response size (1MB)
const maxResponseSize = 1 << 20

type Client struct {
	host       string
	username   string
	password   string
	httpClient *http.Client
	mu         sync.RWMutex
	lastLogin  time.Time
}

type APIResponse struct {
	Success bool            `json:"success"`
	Msg     string          `json:"msg"`
	Obj     json.RawMessage `json:"obj,omitempty"`
}

// Inbound represents a 3x-ui inbound
type Inbound struct {
	ID       int             `json:"id"`
	Port     int             `json:"port"`
	Protocol string          `json:"protocol"`
	Settings json.RawMessage `json:"settings"`
}

type InboundSettings struct {
	Clients []ClientConfig `json:"clients"`
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

func NewClient(host, username, password string) *Client {
	jar, _ := cookiejar.New(nil)
	return &Client{
		host:     host,
		username: username,
		password: password,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
			Jar:     jar,
		},
	}
}

func (c *Client) Login(ctx context.Context) error {
	return c.ensureLoggedIn(ctx, true)
}

// ensureLoggedIn checks if session is valid and logs in if needed
// force=true means always re-login regardless of session age
func (c *Client) ensureLoggedIn(ctx context.Context, force bool) error {
	c.mu.RLock()
	// Session valid for 15 minutes
	if !force && time.Since(c.lastLogin) < 15*time.Minute {
		c.mu.RUnlock()
		return nil
	}
	c.mu.RUnlock()

	c.mu.Lock()
	defer c.mu.Unlock()

	// Double-check after acquiring write lock
	if !force && time.Since(c.lastLogin) < 15*time.Minute {
		return nil
	}

	// Retry login with exponential backoff
	return retryWithBackoff(ctx, func() error {
		return c.doLogin(ctx)
	}, 3, 2*time.Second)
}

func (c *Client) doLogin(ctx context.Context) error {
	loginURL := fmt.Sprintf("%s/login", c.host)

	body, err := json.Marshal(map[string]string{
		"username": c.username,
		"password": c.password,
	})
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", loginURL, bytes.NewBuffer(body))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))
	if err != nil {
		return err
	}

	var apiResp APIResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return fmt.Errorf("failed to parse login response: %w", err)
	}

	if !apiResp.Success {
		return fmt.Errorf("login failed: %s", apiResp.Msg)
	}

	c.lastLogin = time.Now()
	logger.Infof("3x-ui login successful (session valid until %s)", c.lastLogin.Add(15*time.Minute).Format("15:04:05"))
	return nil
}

func (c *Client) GetInbound(ctx context.Context, inboundID int) (*Inbound, error) {
	if err := c.ensureLoggedIn(ctx, false); err != nil {
		return nil, fmt.Errorf("authentication required: %w", err)
	}

	url := fmt.Sprintf("%s/panel/api/inbounds/get/%d", c.host, inboundID)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))
	if err != nil {
		return nil, err
	}

	logger.Debugf("3x-ui getInbound response: %s", string(respBody))

	var apiResp APIResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return nil, err
	}

	if !apiResp.Success {
		return nil, fmt.Errorf("failed to get inbound: %s", apiResp.Msg)
	}

	var inbound Inbound
	if err := json.Unmarshal(apiResp.Obj, &inbound); err != nil {
		return nil, err
	}

	// Handle double-encoded settings (JSON string inside JSON)
	if len(inbound.Settings) > 0 && inbound.Settings[0] == '"' {
		var settingsStr string
		if err := json.Unmarshal(inbound.Settings, &settingsStr); err == nil {
			inbound.Settings = []byte(settingsStr)
		}
	}

	return &inbound, nil
}

// AddClient adds a new client to the specified inbound
func (c *Client) AddClient(ctx context.Context, inboundID int, email string, trafficGB int64, expiryTime time.Time) (*ClientConfig, error) {
	if err := c.ensureLoggedIn(ctx, false); err != nil {
		return nil, fmt.Errorf("authentication required: %w", err)
	}

	// First check if client already exists
	inbound, err := c.GetInbound(ctx, inboundID)
	if err != nil {
		return nil, err
	}

	var settings InboundSettings
	if err := json.Unmarshal(inbound.Settings, &settings); err != nil {
		return nil, fmt.Errorf("failed to parse inbound settings: %w", err)
	}

	for _, client := range settings.Clients {
		if client.Email == email {
			logger.Infof("Client with email %s already exists, returning existing subscription", email)
			return &client, nil
		}
	}

	// Generate new client data
	clientID := generateUUID()
	subID := generateSubID()

	// Create settings as JSON string (3x-ui expects string, not object)
	settingsJSON := map[string]interface{}{
		"clients": []map[string]interface{}{
			{
				"id":         clientID,
				"email":      email,
				"limitIp":    0,
				"totalGB":    trafficGB,
				"expiryTime": expiryTime.UnixMilli(),
				"enable":     true,
				"flow":       "xtls-rprx-vision",
				"subId":      subID,
				"reset":      31,
			},
		},
	}
	settingsStr, _ := json.Marshal(settingsJSON)

	// Create the full request data - settings must be a JSON string
	requestData := map[string]interface{}{
		"id":       inboundID,
		"settings": string(settingsStr),
	}

	body, err := json.Marshal(requestData)
	if err != nil {
		return nil, err
	}

	// Use addClient endpoint
	addURL := fmt.Sprintf("%s/panel/api/inbounds/addClient", c.host)
	req, err := http.NewRequestWithContext(ctx, "POST", addURL, bytes.NewBuffer(body))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))
	if err != nil {
		return nil, err
	}

	logger.Infof("3x-ui addClient response: %s", string(respBody))

	var apiResp APIResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if !apiResp.Success {
		return nil, fmt.Errorf("failed to add client: %s", apiResp.Msg)
	}

	logger.Infof("Client %s added successfully to inbound %d", email, inboundID)

	// Return client config
	return &ClientConfig{
		ID:         clientID,
		Email:      email,
		TotalGB:    trafficGB,
		ExpiryTime: expiryTime.UnixMilli(),
		Enable:     true,
		SubID:      subID,
		Reset:      31,
	}, nil
}

func (c *Client) GetSubscriptionLink(baseURL string, subID string, subPath string) string {
	return fmt.Sprintf("%s/%s/%s", baseURL, subPath, subID)
}

// RemoveClient removes a client from the specified inbound by client ID
func (c *Client) RemoveClient(ctx context.Context, inboundID int, clientID string) error {
	if err := c.ensureLoggedIn(ctx, false); err != nil {
		return fmt.Errorf("authentication required: %w", err)
	}

	// First get the current inbound
	inbound, err := c.GetInbound(ctx, inboundID)
	if err != nil {
		return err
	}

	var settings InboundSettings
	if err := json.Unmarshal(inbound.Settings, &settings); err != nil {
		return fmt.Errorf("failed to parse inbound settings: %w", err)
	}

	// Filter out the client to remove
	newClients := make([]ClientConfig, 0, len(settings.Clients))
	for _, client := range settings.Clients {
		if client.ID != clientID {
			newClients = append(newClients, client)
		}
	}

	// Create settings as JSON string
	settingsJSON := map[string]interface{}{
		"clients": make([]map[string]interface{}, 0, len(newClients)),
	}
	for _, client := range newClients {
		clientMap := map[string]interface{}{
			"id":         client.ID,
			"email":      client.Email,
			"limitIp":    client.LimitIP,
			"totalGB":    client.TotalGB,
			"expiryTime": client.ExpiryTime,
			"enable":     client.Enable,
			"subId":      client.SubID,
		}
		if client.Flow != "" {
			clientMap["flow"] = client.Flow
		}
		if client.Reset != 0 {
			clientMap["reset"] = client.Reset
		}
		settingsJSON["clients"] = append(settingsJSON["clients"].([]map[string]interface{}), clientMap)
	}
	settingsStr, _ := json.Marshal(settingsJSON)

	// Create the full request data
	requestData := map[string]interface{}{
		"id":       inboundID,
		"settings": string(settingsStr),
	}

	body, err := json.Marshal(requestData)
	if err != nil {
		return err
	}

	updateURL := fmt.Sprintf("%s/panel/api/inbounds/update/%d", c.host, inboundID)
	req, err := http.NewRequestWithContext(ctx, "POST", updateURL, bytes.NewBuffer(body))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))
	if err != nil {
		return err
	}

	var apiResp APIResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}

	if !apiResp.Success {
		return fmt.Errorf("failed to remove client: %s", apiResp.Msg)
	}

	logger.Infof("Client %s removed successfully from inbound %d", clientID, inboundID)
	return nil
}

func GetExternalURL(host string) string {
	u, err := url.Parse(host)
	if err != nil {
		return host
	}
	return fmt.Sprintf("%s://%s", u.Scheme, u.Host)
}

func generateUUID() string {
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		time.Now().Unix(),
		time.Now().UnixNano()&0xFFFF,
		(time.Now().UnixNano()>>16)&0xFFFF,
		(time.Now().UnixNano()>>32)&0xFFFF,
		time.Now().UnixNano()&0xFFFFFFFFFFFF,
	)
}

func generateSubID() string {
	return fmt.Sprintf("%x", time.Now().UnixNano()&0xFFFFFFFFFFFFFF)
}

// retryWithBackoff retries a function with exponential backoff
func retryWithBackoff(ctx context.Context, fn func() error, maxRetries int, initialDelay time.Duration) error {
	var lastErr error
	delay := initialDelay

	for i := 0; i < maxRetries; i++ {
		if err := fn(); err == nil {
			return nil
		} else {
			lastErr = err
		}

		if i < maxRetries-1 {
			logger.Warnf("Retry %d/%d after error: %v", i+1, maxRetries, lastErr)

			// Use context-aware sleep
			select {
			case <-time.After(delay):
				// Continue to next retry
			case <-ctx.Done():
				return fmt.Errorf("context cancelled: %w", ctx.Err())
			}

			delay *= 2 // Exponential backoff
		}
	}

	return fmt.Errorf("after %d retries: %w", maxRetries, lastErr)
}
