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

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/logger"
	"github.com/kereal/rs8kvn_bot/internal/utils"

	"go.uber.org/zap"
)

// Client — HTTP-клиент для взаимодействия с панелью 3x-ui.
type Client struct {
	host       string
	apiToken   string
	httpClient *http.Client
	transport  *http.Transport
}

// APIResponse — универсальный контейнер ответа от панели 3x-ui.
type APIResponse struct {
	Success bool            `json:"success"`
	Msg     string          `json:"msg"`
	Obj     json.RawMessage `json:"obj,omitempty"`
}

// ClientConfig — DTO клиента, используемое при создании/обновлении в панели.
type ClientConfig struct {
	ID        string `json:"id"`
	Email     string `json:"email"`
	LimitIP   int    `json:"limitIp"`
	TotalGB   int64  `json:"totalGB"`
	ExpiresAt int64  `json:"expiryTime"`
	Enable    bool   `json:"enable"`
	TgID      int64  `json:"tgId"`
	SubID     string `json:"subId"`
	Flow      string `json:"flow,omitempty"`
	Reset     int    `json:"reset,omitempty"`

	UUID string `json:"uuid"`
	Group string `json:"group"`

	CreatedAt int64 `json:"-"`
	UpdatedAt int64 `json:"-"`
}

// ClientTraffic — DTO трафика клиента, возвращаемого панелью.
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
	ExpiresAt  int64  `json:"expiryTime"`
	Total      int64  `json:"total"`
	Reset      int    `json:"reset"`
	LastOnline int64  `json:"lastOnline"`
}

// Inbound — DTO inbound-записи из панели 3x-ui.
type Inbound struct {
	ID             int             `json:"id"`
	Up             int             `json:"up"`
	Down           int             `json:"down"`
	Total          int             `json:"total"`
	Remark         string          `json:"remark"`
	Enable         bool            `json:"enable"`
	ExpiresAt      int64           `json:"expiryTime"`
	Listen         string          `json:"listen"`
	Port           int             `json:"port"`
	Protocol       string          `json:"protocol"`
	Settings       json.RawMessage `json:"settings"`
	StreamSettings json.RawMessage `json:"streamSettings"`
	Tag            string          `json:"tag"`
	Sniffing       json.RawMessage `json:"sniffing"`
}

// marshalJSON сериализует значение в bytes.Reader для HTTP-запроса.
func marshalJSON[T any](v T) (*bytes.Reader, error) {
	body, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return bytes.NewReader(body), nil
}

// buildClientBody формирует замыкание, сериализующее тело запроса
// с полем client и массивом inboundIds.
func (c *Client) buildClientBody(clientObj map[string]any, inboundIDs []int) func() (io.Reader, error) {
	return func() (io.Reader, error) {
		requestData := map[string]any{
			"client":     clientObj,
			"inboundIds": inboundIDs,
		}
		return marshalJSON(requestData)
	}
}

// closeResponseBody безопасно закрывает тело ответа, избегая nil-панике.
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

// GetTransport извлекает тип транспортного слоя (network) из StreamSettings.
// Поддерживает современный формат (JSON-объект) и legacy (двойная кодировка).
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

// GetRequiredFlow возвращает значение flow, необходимое для данного inbound.
// Для xhttp/h2/ws/grpc/grpcs flow не требуется ("");
// для всех остальных транспортов используется xtls-rprx-vision.
func (in *Inbound) GetRequiredFlow() string {
	transport := in.GetTransport()
	switch transport {
	case "xhttp", "h2", "ws", "grpc", "grpcs":
		return ""
	default:
		return "xtls-rprx-vision"
	}
}

// NewClient создаёт и инициализирует HTTP-клиент для работы с панелью.
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

// Ping проверяет доступность панели через /panel/api/server/status.
func (c *Client) Ping(ctx context.Context) error {
	statusURL := fmt.Sprintf("%s/panel/api/server/status", c.host)
	_, err := c.doHTTPRequest(ctx, http.MethodGet, statusURL, nil)
	return err
}

// doHTTPRequest выполняет HTTP-запрос к панели и возвращает сырой ответ.
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
		return respBody, fmt.Errorf("upstream returned non-200")
	}

	return respBody, nil
}

// tgIDContextKey — ключ контекста для передачи Telegram ID в методы панели.
//
// Важно: значение живёт ровно до конца текущего вызова. При копировании
// контекста через context.WithValue / WithTimeout / WithCancel новый
// контекст наследует значение, поэтому не оборачивайте ctx вокруг
// параллельных вызовов, если в них тоже читается TgIDFromContext —
// они увидят чужой tgID. Ставьте WithTgID максимально локально,
// сразу перед AddClientWithID / UpdateClient.
type tgIDContextKey struct{}

// WithTgID сохраняет Telegram ID в контексте для последующего использования
// при создании/обновлении клиента в панели.
//
// Риски:
//   - Контекст копируется по значению, но карта значений общая; при
//     параллельных вызовах из того же ctx возможна утечка tgID в
//     дочерние запросы.
//   - Если контекст прокидывается выше по стеку (например, через HTTP
//     middleware), tgID может «протечь» в логирование/метрики, если
//     они тоже читают контекст. Здесь ключ package-private, поэтому
//     внешний доступ исключён.
func WithTgID(ctx context.Context, tgID int64) context.Context {
	return context.WithValue(ctx, tgIDContextKey{}, tgID)
}

// TgIDFromContext извлекает Telegram ID из контекста.
// Возвращает 0, если TgID не был установлен.
//
// Вызывается только внутри XUI-методов add/update после WithTgID.
// Если значение не установлено, панель получит tgId=0, что для
// большинства инстансов означает «без привязки Telegram».
func TgIDFromContext(ctx context.Context) int64 {
	if v, ok := ctx.Value(tgIDContextKey{}).(int64); ok {
		return v
	}
	return 0
}

// AddClient создаёт клиента с автоматической генерацией clientID и subID.
func (c *Client) AddClient(ctx context.Context, inboundIDs []int, email string, trafficBytes int64, expiryTime time.Time) (*ClientConfig, error) {
	clientID, err := utils.GenerateUUID()
	if err != nil {
		return nil, fmt.Errorf("generate client id: %w", err)
	}
	subID, err := utils.GenerateSubID()
	if err != nil {
		return nil, fmt.Errorf("generate sub id: %w", err)
	}

	return c.AddClientWithID(ctx, inboundIDs, email, clientID, subID, trafficBytes, expiryTime, -1)
}

// AddClientWithID создаёт клиента с указанными clientID и subID.
// При наличии inboundIDs с разными требованиями к flow запрос автоматически
// разбивается на отдельные вызовы панели, сгруппированные по совместимому flow.
func (c *Client) AddClientWithID(ctx context.Context, inboundIDs []int, email, clientID, subID string, trafficBytes int64, expiryTime time.Time, resetDays int) (*ClientConfig, error) {
	if len(inboundIDs) == 0 {
		return nil, fmt.Errorf("inbound IDs cannot be empty")
	}
	for _, id := range inboundIDs {
		if id <= 0 {
			return nil, fmt.Errorf("invalid inbound ID %d: must be positive", id)
		}
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

	groups, err := c.groupInboundIDsByFlow(ctx, inboundIDs)
	if err != nil {
		return nil, err
	}

	tgID := TgIDFromContext(ctx)

	var (
		result  *ClientConfig
		firstErr error
	)
	for flow, ids := range groups {
		errRetry := RetryWithBackoff(ctx, config.XUIMaxRetries, config.XUIInitialRetryDelay, func() error {
			var innerErr error
			result, innerErr = c.doAddClientWithID(ctx, ids, email, clientID, subID, trafficBytes, expiryTime, resetDays, flow, tgID)
			return innerErr
		})
		if errRetry != nil {
			firstErr = errRetry
			break
		}
	}
	return result, firstErr
}

// doAddClientWithID выполняет реальный POST /panel/api/clients/add
// с уже вычисленным flow для группы inboundIDs.
func (c *Client) doAddClientWithID(ctx context.Context, inboundIDs []int, email, clientID, subID string, trafficBytes int64, expiryTime time.Time, resetDays int, flow string, tgID int64) (*ClientConfig, error) {
	clientObj := map[string]any{
		"id":         clientID,
		"email":      email,
		"limitIp":    0,
		"totalGB":    trafficBytes,
		"expiryTime": getExpiresAtMillis(expiryTime),
		"enable":     true,
		"flow":       flow,
		"subId":      subID,
		"reset":      resetDays,
		"tgId":       tgID,
	}

	addURL := fmt.Sprintf("%s/panel/api/clients/add", c.host)

	respBody, err := c.doHTTPRequest(ctx, http.MethodPost, addURL, c.buildClientBody(clientObj, inboundIDs))
	if err != nil {
		return nil, err
	}

	logger.Debug("3x-ui clients/add response",
		zap.Int("body_length", len(respBody)),
		zap.String("response_preview", truncateString(string(respBody), 200)))

	var simpleResp struct {
		Success bool   `json:"success"`
		Msg     string `json:"msg"`
		Obj     any    `json:"obj,omitempty"`
	}

	if err := json.Unmarshal(respBody, &simpleResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if !simpleResp.Success {
		return nil, fmt.Errorf("add client failed")
	}

	return &ClientConfig{
		ID:        clientID,
		Email:     email,
		TotalGB:   trafficBytes,
		ExpiresAt: getExpiresAtMillis(expiryTime),
		Enable:    true,
		SubID:     subID,
		Reset:     resetDays,
	}, nil
}

// DeleteClient удаляет клиента из панели по email.
func (c *Client) DeleteClient(ctx context.Context, email string) error {
	if email == "" {
		return fmt.Errorf("email cannot be empty")
	}
	return RetryWithBackoff(ctx, config.XUIMaxRetries, config.XUIInitialRetryDelay, func() error {
		return c.doDeleteClient(ctx, email)
	})
}

// doDeleteClient выполняет реальный POST /panel/api/clients/del/{email}.
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

// UpdateClient обновляет данные клиента в панели.
// Как и AddClientWithID, автоматически разбивает запрос на группы по flow,
// если inboundIDs требуют разных значений flow.
func (c *Client) UpdateClient(ctx context.Context, inboundIDs []int, currentEmail, clientID, email, subID string, trafficBytes int64, expiryTime time.Time, tgID int64, comment string) error {
	if clientID == "" {
		return fmt.Errorf("client ID cannot be empty")
	}
	if currentEmail == "" {
		return fmt.Errorf("current email cannot be empty")
	}
	if len(inboundIDs) == 0 {
		return fmt.Errorf("inbound IDs cannot be empty")
	}
	for _, id := range inboundIDs {
		if id <= 0 {
			return fmt.Errorf("invalid inbound ID %d: must be positive", id)
		}
	}

	groups, err := c.groupInboundIDsByFlow(ctx, inboundIDs)
	if err != nil {
		return err
	}

	var firstErr error
	for flow, ids := range groups {
		errRetry := RetryWithBackoff(ctx, config.XUIMaxRetries, config.XUIInitialRetryDelay, func() error {
			return c.doUpdateClient(ctx, ids, currentEmail, clientID, email, subID, trafficBytes, expiryTime, tgID, comment, flow)
		})
		if errRetry != nil {
			firstErr = errRetry
			break
		}
	}
	return firstErr
}

// doUpdateClient выполняет реальный POST /panel/api/clients/update/{currentEmail}
// с уже вычисленным flow для группы inboundIDs.
func (c *Client) doUpdateClient(ctx context.Context, inboundIDs []int, currentEmail, clientID, email, subID string, trafficBytes int64, expiryTime time.Time, tgID int64, comment string, flow string) error {
	clientObj := map[string]any{
		"id":         clientID,
		"email":      email,
		"limitIp":    0,
		"totalGB":    trafficBytes,
		"expiryTime": getExpiresAtMillis(expiryTime),
		"enable":     true,
		"flow":       flow,
		"subId":      subID,
		"reset":      config.SubscriptionResetDay,
		"tgId":       tgID,
		"comment":    comment,
	}

	updateURL := fmt.Sprintf("%s/panel/api/clients/update/%s", c.host, url.PathEscape(currentEmail))

	respBody, err := c.doHTTPRequest(ctx, http.MethodPost, updateURL, c.buildClientBody(clientObj, inboundIDs))
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

// GetClientTraffic возвращает актуальный трафик клиента по email.
func (c *Client) GetClientTraffic(ctx context.Context, email string) (*ClientTraffic, error) {
	var result *ClientTraffic
	err := RetryWithBackoff(ctx, config.XUIMaxRetries, config.XUIInitialRetryDelay, func() error {
		var err error
		result, err = c.doGetClientTraffic(ctx, email)
		return err
	})
	return result, err
}

// ErrClientNotFound возвращается, когда панель не может найти клиента по email.
var ErrClientNotFound = errors.New("client not found")

// doGetClientTraffic выполняет GET /panel/api/clients/traffic/{email}.
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

// GetInbound запрашивает данные inbound по его ID у панели.
func (c *Client) GetInbound(ctx context.Context, inboundID int) (*Inbound, error) {
	return c.doGetInbound(ctx, inboundID)
}

// doGetInbound выполняет GET /panel/api/inbounds/get/{inboundID}.
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

// getRequiredFlow возвращает flow, требуемый для работы с указанным inbound.
// При ошибке получения inbound'а используется безопасный fallback xtls-rprx-vision.
func (c *Client) getRequiredFlow(ctx context.Context, inboundID int) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	inbound, err := c.doGetInbound(ctx, inboundID)
	if err != nil {
		logger.Debug("Failed to get inbound for flow, using default",
			zap.Error(err))
		return "xtls-rprx-vision", nil
	}

	return inbound.GetRequiredFlow(), nil
}

// groupInboundIDsByFlow группирует уникальные inboundIDs по совместимому значению flow.
// Если разные inbound'ы требуют разные flow, они попадут в разные группы,
// что позволяет корректно обработать их отдельными вызовами панели.
func (c *Client) groupInboundIDsByFlow(ctx context.Context, inboundIDs []int) (map[string][]int, error) {
	seen := make(map[int]struct{}, len(inboundIDs))
	uniqueIDs := make([]int, 0, len(inboundIDs))
	for _, id := range inboundIDs {
		if _, ok := seen[id]; !ok {
			seen[id] = struct{}{}
			uniqueIDs = append(uniqueIDs, id)
		}
	}

	groups := make(map[string][]int)
	for _, id := range uniqueIDs {
		flow, err := c.getRequiredFlow(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("failed to determine flow for inbound %d: %w", id, err)
		}
		groups[flow] = append(groups[flow], id)
	}
	return groups, nil
}

// Close закрывает прозрачное HTTP-соединение к панели.
func (c *Client) Close() error {
	if c.transport != nil {
		c.transport.CloseIdleConnections()
	}
	return nil
}

// getExpiresAtMillis конвертирует время истечения в миллисекунды Unix.
func getExpiresAtMillis(expiryTime time.Time) int64 {
	if expiryTime.IsZero() {
		return 0
	}
	return expiryTime.UnixMilli()
}

// truncateString обрезает строку до maxLen символов, добавляя "..." при усечении.
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// isRetryable определяет, можно ли повторить запрос при данной ошибке.
// DNS-ошибки и нерешаемые ошибки имён не ретраятся; таймауты — ретраятся.
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

// RetryWithBackoff выполняет fn с экспоненциальной задержкой до maxRetries раз.
// Прерывается при не-retryable ошибке или отменённом контексте.
func RetryWithBackoff(ctx context.Context, maxRetries int, initialDelay time.Duration, fn func() error) error {
	if maxRetries <= 0 {
		return errors.New("maxRetries must be positive")
	}
	if initialDelay <= 0 {
		return errors.New("initialDelay must be positive")
	}

	var lastErr error
	delay := initialDelay

	for attempt := range maxRetries {
		err := fn()
		if err == nil {
			return nil
		}

		if !isRetryable(err) {
			logger.Error("Non-retryable XUI error, failing immediately",
				zap.Error(err))
			return err
		}

		lastErr = err

		if attempt < maxRetries-1 {
			logger.Debug("Retry after error",
				zap.Int("attempt", attempt+1),
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

	logger.Error("XUI operation failed after retries",
		zap.Int("retries", maxRetries),
		zap.Error(lastErr))

	return fmt.Errorf("after %d retries: %w", maxRetries, lastErr)
}
