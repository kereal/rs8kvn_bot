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
	"strings"
	"sync"
	"time"

	"rs8kvn_bot/internal/logger"
)

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

func (c *Client) ensureLoggedIn(ctx context.Context, force bool) error {
	c.mu.RLock()
	if !force && time.Since(c.lastLogin) < 15*time.Minute {
		c.mu.RUnlock()
		return nil
	}
	c.mu.RUnlock()

	c.mu.Lock()
	defer c.mu.Unlock()

	if !force && time.Since(c.lastLogin) < 15*time.Minute {
		return nil
	}

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

func (c *Client) AddClient(ctx context.Context, inboundID int, email string, trafficGB int64, expiryTime time.Time) (*ClientConfig, error) {
	if err := c.ensureLoggedIn(ctx, false); err != nil {
		return nil, fmt.Errorf("authentication required: %w", err)
	}

	clientID := generateUUID()
	subID := generateSubID()

	return c.AddClientWithID(ctx, inboundID, email, clientID, subID, trafficGB, expiryTime)
}

// AddClientWithID добавляет клиента с заранее определёнными ID (для атомарной операции БД)
func (c *Client) AddClientWithID(ctx context.Context, inboundID int, email string, clientID, subID string, trafficGB int64, expiryTime time.Time) (*ClientConfig, error) {
	if err := c.ensureLoggedIn(ctx, false); err != nil {
		return nil, fmt.Errorf("authentication required: %w", err)
	}

	// Данные нового клиента в формате settings
	clientSettings := map[string]interface{}{
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

	settingsJSON, err := json.Marshal(clientSettings)
	if err != nil {
		return nil, err
	}

	// Формируем запрос: id + settings (как строка JSON)
	requestData := map[string]interface{}{
		"id":       inboundID, // 3x-ui ожидает int
		"settings": string(settingsJSON),
	}

	body, err := json.Marshal(requestData)
	if err != nil {
		return nil, err
	}

	// POST /panel/api/inbounds/addClient — добавляет нового клиента к существующим
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

	// Пробуем распарсить как простой ответ с success/msg
	type SimpleResponse struct {
		Success bool        `json:"success"`
		Msg     string      `json:"msg"`
		Obj     interface{} `json:"obj,omitempty"`
	}

	var simpleResp SimpleResponse
	if err := json.Unmarshal(respBody, &simpleResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	// 3x-ui иногда возвращает success=false но с сообщением об успехе
	// Проверяем msg на наличие "successfully" или "added"
	if !simpleResp.Success && simpleResp.Msg != "" {
		if containsSuccess(simpleResp.Msg) {
			logger.Infof("3x-ui вернул success=false, но операция успешна: %s", simpleResp.Msg)
		} else {
			return nil, fmt.Errorf("failed to add client: %s", simpleResp.Msg)
		}
	}

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

func containsSuccess(msg string) bool {
	msg = strings.ToLower(msg)
	return strings.Contains(msg, "successfully") ||
		strings.Contains(msg, "added") ||
		strings.Contains(msg, "success")
}

func (c *Client) GetSubscriptionLink(baseURL string, subID string, subPath string) string {
	return fmt.Sprintf("%s/%s/%s", baseURL, subPath, subID)
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

			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return fmt.Errorf("context cancelled: %w", ctx.Err())
			}

			delay *= 2
		}
	}

	return fmt.Errorf("after %d retries: %w", maxRetries, lastErr)
}
