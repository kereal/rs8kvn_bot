package xui

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/logger"
	"github.com/kereal/rs8kvn_bot/internal/metrics"
	"github.com/kereal/rs8kvn_bot/internal/utils"

	"go.uber.org/zap"
)

// operationRegex extracts the API operation from the URL path.
var operationRegex = regexp.MustCompile(`/panel/api/([^/]+)(?:/[^/]+)*`)

// Client — HTTP-клиент для взаимодействия с панелью 3x-ui.
type Client struct {
	host       string
	apiToken   string
	node       string
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
	ID              string `json:"id"`
	Email           string `json:"email"`
	LimitIP         int    `json:"limitIp"`
	TotalGB         int64  `json:"totalGB"`
	ExpiresAt       int64  `json:"expiryTime"`
	Enable          bool   `json:"enable"`
	TgID            int64  `json:"tgId"`
	SubID           string `json:"subId"`
	Flow            string `json:"flow,omitempty"`
	Reset           int    `json:"reset,omitempty"`
	ResetDay        int    `json:"resetDay,omitempty"`
	ResetMax        int    `json:"resetMax,omitempty"`
	TrafficReset    string `json:"trafficReset,omitempty"`
	TrafficResetDay int    `json:"trafficResetDay,omitempty"`

	UUID  string `json:"uuid"`
	Group string `json:"group"`

	CreatedAt int64 `json:"-"`
	UpdatedAt int64 `json:"-"`
}

// ClientRequest carries the parameters for creating or updating a 3x-ui client.
// Using a struct instead of positional parameters prevents identifier-swap bugs
// (currentEmail vs email vs clientID vs subID are all strings).
type ClientRequest struct {
	InboundIDs      []int
	Email           string    // desired email after the operation
	CurrentEmail    string    // existing email (update only); empty for create
	ClientID        string    // 3x-ui client UUID
	SubID           string    // subscription ID surfaced in /sub/{subID}
	TrafficBytes    int64     // totalGB limit in bytes
	ExpiryTime      time.Time // zero = no expiry
	ResetDays       int       // legacy auto-renew interval; negative = use default
	ResetDay        int       // calendar day for the monthly traffic reset
	ResetMax        int       // maximum renewals; 0 = unlimited
	TrafficReset    string    // v3.7.0 traffic reset mode, e.g. "monthly"
	TrafficResetDay int       // v3.7.0 monthly reset day
	TgID            int64     // Telegram user id for panel binding (0 = none)
	Comment         string    // free-form comment stored in the panel
	// Enable optionally overrides the client enabled flag on update.
	// nil keeps the backward-compatible default (true). Primarily used to
	// re-enable a client that the panel disabled after its traffic quota was
	// exhausted but whose counter has since been reset.
	Enable *bool
}

// ClientTraffic — DTO трафика клиента, возвращаемого панелью.
type ClientTraffic struct {
	ID              int    `json:"id"`
	InboundID       int    `json:"inboundId"`
	Enable          bool   `json:"enable"`
	Email           string `json:"email"`
	UUID            string `json:"uuid"`
	SubID           string `json:"subId"`
	Up              int64  `json:"up"`
	Down            int64  `json:"down"`
	AllTime         int64  `json:"allTime"`
	ExpiresAt       int64  `json:"expiryTime"`
	Total           int64  `json:"total"`
	Reset           int    `json:"reset"`
	ResetDay        int    `json:"resetDay"`
	ResetMax        int    `json:"resetMax"`
	TrafficReset    string `json:"trafficReset"`
	TrafficResetDay int    `json:"trafficResetDay"`
	LastOnline      int64  `json:"lastOnline"`
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

	err := resp.Body.Close()
	if err != nil {
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

	err := json.Unmarshal(in.StreamSettings, &netSettings)
	if err == nil && netSettings.Network != "" {
		return netSettings.Network
	}

	// Fallback for old format: StreamSettings is a JSON-encoded string (legacy panel)
	var rawStr string

	err = json.Unmarshal(in.StreamSettings, &rawStr)
	if err != nil {
		logger.Debug("GetTransport: failed to unmarshal legacy streamSettings string",
			zap.Error(err))

		return ""
	}

	cleaned := strings.ReplaceAll(rawStr, "\\n", "\n")

	err = json.Unmarshal([]byte(cleaned), &netSettings)
	if err != nil {
		logger.Debug("GetTransport: failed to parse legacy streamSettings JSON",
			zap.Error(err))

		return ""
	}

	return netSettings.Network
}

// GetRequiredFlow возвращает значение flow, необходимое для данного inbound.
// Для транспортов, не использующих XTLS (xhttp, hysteria2, ws, grpc, и т.д.),
// flow не требуется (""). Ошибка получения inbound передаётся вызывающему коду.
func (in *Inbound) GetRequiredFlow() string {
	transport := in.GetTransport()
	switch transport {
	case "xhttp", "h2", "ws", "grpc", "grpcs",
		"hysteria", "hysteria2", "shadowsocks", "ss2022",
		"tuic", "wireguard", "reality", "darkertls":
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

// SetNode sets the low-cardinality node label used by XUI request metrics.
func (c *Client) SetNode(name string) {
	c.node = name
}

// Ping проверяет доступность панели через /panel/api/server/status.
func (c *Client) Ping(ctx context.Context) error {
	statusURL := fmt.Sprintf("%s/panel/api/server/status", c.host)
	_, err := c.doHTTPRequest(ctx, http.MethodGet, statusURL, nil)

	return err
}

// doHTTPRequest выполняет HTTP-запрос к панели и возвращает сырой ответ.
func (c *Client) doHTTPRequest(ctx context.Context, method, url string, bodyFn func() (io.Reader, error)) ([]byte, error) {
	operation := extractOperation(url)
	start := time.Now()

	// Single deferred handler records both metrics on every return path.
	result := "success"
	defer func() {
		metrics.XUIRequestsTotal.WithLabelValues(operation, result, c.node).Inc()
		metrics.XUIRequestDuration.WithLabelValues(operation, c.node).Observe(time.Since(start).Seconds())
	}()

	var body io.Reader

	if bodyFn != nil {
		var err error

		body, err = bodyFn()
		if err != nil {
			result = "error"
			return nil, err
		}
	}

	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		result = "error"
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiToken)

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		result = "error"
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer closeResponseBody(resp)

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, config.MaxResponseSize))
	if err != nil {
		result = "error"
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		result = "error"
		return respBody, fmt.Errorf("upstream returned non-200: %w", utils.ErrNon200Response)
	}

	return respBody, nil
}

// extractOperation извлекает имя операции из URL панели.
func extractOperation(urlStr string) string {
	matches := operationRegex.FindStringSubmatch(urlStr)
	if len(matches) >= 2 {
		return matches[1]
	}

	return "unknown"
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

	return c.AddClientWithID(ctx, ClientRequest{
		InboundIDs:   inboundIDs,
		Email:        email,
		ClientID:     clientID,
		SubID:        subID,
		TrafficBytes: trafficBytes,
		ExpiryTime:   expiryTime,
		ResetDays:    -1,
	})
}

// AddClientWithID создаёт клиента с указанными clientID и subID.
// Группирует inboundIDs по flow: для каждого уникального flow делается
// отдельный вызов панели со всеми inboundID этого flow (минимум вызовов).
func (c *Client) AddClientWithID(ctx context.Context, req ClientRequest) (*ClientConfig, error) {
	if len(req.InboundIDs) == 0 {
		return nil, fmt.Errorf("inbound IDs cannot be empty")
	}

	if req.ClientID == "" {
		return nil, fmt.Errorf("client ID cannot be empty")
	}

	if req.SubID == "" {
		return nil, fmt.Errorf("sub ID cannot be empty")
	}

	for _, id := range req.InboundIDs {
		if id <= 0 {
			return nil, fmt.Errorf("invalid inbound ID %d: must be positive", id)
		}
	}

	if req.ResetDays < 0 {
		req.ResetDays = config.SubscriptionResetDay
	}

	groups, err := c.groupInboundIDsByFlow(ctx, req.InboundIDs)
	if err != nil {
		return nil, err
	}

	tgID := req.TgID
	if tgID == 0 {
		tgID = TgIDFromContext(ctx)
	}

	var (
		result   *ClientConfig
		firstErr error
	)

	for flow, ids := range groups {
		errRetry := utils.RetryWithBackoff(ctx, config.XUIMaxRetries, config.XUIInitialRetryDelay, func() error {
			var innerErr error

			result, innerErr = c.doAddClientWithID(ctx, ids, req, flow, tgID)

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
func (c *Client) doAddClientWithID(ctx context.Context, inboundIDs []int, req ClientRequest, flow string, tgID int64) (*ClientConfig, error) {
	// Нормализация негативного ResetDays ровно та же, что в doUpdateClient:
	// контракт поля — «negative = use default». Без этого add и update дали бы
	// одному и тому же клиенту разный профиль (add отправлял бы reset=-1).
	// В проде дефект не проявлялся (панель нормализует значение при записи;
	// проверено 2026-10-31: reset=-1 среди клиентов не встречается).
	resetDays := req.ResetDays
	if resetDays < 0 {
		resetDays = config.SubscriptionResetDay
	}

	clientObj := map[string]any{
		"id":         req.ClientID,
		"email":      req.Email,
		"limitIp":    0,
		"totalGB":    req.TrafficBytes,
		"expiryTime": getExpiresAtMillis(req.ExpiryTime),
		"enable":     true,
		"flow":       flow,
		"subId":      req.SubID,
		"reset":      resetDays,
		"tgId":       tgID,
	}
	// 3x-ui v3.7.0 auto-renew profile (resetDay/resetMax/trafficReset/
	// trafficResetDay) is sent as a complete profile only when the caller
	// configured it (TrafficReset != ""). The panel does NOT merge profiles
	// on write: an omitted/empty trafficReset is normalized to "never"
	// (normalizeClientTrafficReset в internal/web/service/client_crud.go;
	// проверено на живой панели — клиенты с trafficReset=never существуют).
	// Поэтому профиль либо шлётся целиком, как настроен вызывающим, либо
	// панель запишет "never" — см. doc/panel-api-contract.md, §4.2.
	if req.TrafficReset != "" {
		clientObj["resetDay"] = req.ResetDay
		clientObj["resetMax"] = req.ResetMax
		clientObj["trafficReset"] = req.TrafficReset
		clientObj["trafficResetDay"] = req.TrafficResetDay
	}

	addURL := fmt.Sprintf("%s/panel/api/clients/add", c.host)

	respBody, err := c.doHTTPRequest(ctx, http.MethodPost, addURL, c.buildClientBody(clientObj, inboundIDs))
	if err != nil {
		return nil, err
	}

	logger.Debug("3x-ui clients/add response",
		zap.Int("body_length", len(respBody)),
		zap.String("response_preview", utils.TruncateString(string(respBody), 200)))

	_, err = parseEnvelope(opAdd, respBody)
	if err != nil {
		return nil, err
	}

	return &ClientConfig{
		ID:        req.ClientID,
		Email:     req.Email,
		TotalGB:   req.TrafficBytes,
		ExpiresAt: getExpiresAtMillis(req.ExpiryTime),
		Enable:    true,
		SubID:     req.SubID,
		Reset:     resetDays,
	}, nil
}

// DeleteClient удаляет клиента из панели по email.
func (c *Client) DeleteClient(ctx context.Context, email string) error {
	if email == "" {
		return fmt.Errorf("email cannot be empty")
	}

	return utils.RetryWithBackoff(ctx, config.XUIMaxRetries, config.XUIInitialRetryDelay, func() error {
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

	_, err = parseEnvelope(opDel, respBody)
	if err != nil {
		return err
	}

	logger.Info("Successfully deleted client",
		zap.String("email", email))

	return nil
}

// UpdateClient обновляет данные клиента в панели.
// Группирует inboundIDs по flow: для каждого уникального flow делается
// отдельный вызов панели со всеми inboundID этого flow (минимум вызовов).
func (c *Client) UpdateClient(ctx context.Context, req ClientRequest) error {
	if req.ClientID == "" {
		return fmt.Errorf("client ID cannot be empty")
	}

	if req.CurrentEmail == "" {
		return fmt.Errorf("current email cannot be empty")
	}

	if len(req.InboundIDs) == 0 {
		return fmt.Errorf("inbound IDs cannot be empty")
	}

	for _, id := range req.InboundIDs {
		if id <= 0 {
			return fmt.Errorf("invalid inbound ID %d: must be positive", id)
		}
	}

	groups, err := c.groupInboundIDsByFlow(ctx, req.InboundIDs)
	if err != nil {
		return err
	}

	var firstErr error

	for flow, ids := range groups {
		errRetry := utils.RetryWithBackoff(ctx, config.XUIMaxRetries, config.XUIInitialRetryDelay, func() error {
			return c.doUpdateClient(ctx, ids, req, flow)
		})
		if errRetry != nil {
			firstErr = errRetry
			break
		}
	}

	return firstErr
}

// doUpdateClient выполняет реальный POST /panel/api/clients/update/{currentEmail}.
// Панель ожидает плоский объект клиента (без обёртки "client"/"inboundIds"),
// поэтому тело формируется напрямую из clientObj.
func (c *Client) doUpdateClient(ctx context.Context, inboundIDs []int, req ClientRequest, flow string) error {
	resetDays := req.ResetDays
	if resetDays < 0 {
		resetDays = config.SubscriptionResetDay
	}

	// 3x-ui v3.7.0 no longer treats a regular client update as an auto-renew
	// operation. The panel's reset job is keyed by reset + a non-zero expiry;
	// send the renewal interval explicitly and move expiry to the next cycle.
	// This is the single recovery fallback for expired free clients: trials
	// (resetDays == 0) are intentionally left untouched.
	if resetDays > 0 && !req.ExpiryTime.IsZero() && req.ExpiryTime.Before(time.Now().UTC()) {
		req.ExpiryTime = time.Now().UTC().Truncate(time.Minute).AddDate(0, 0, resetDays)
	}

	clientObj := map[string]any{
		"id":         req.ClientID,
		"email":      req.Email,
		"limitIp":    0,
		"totalGB":    req.TrafficBytes,
		"expiryTime": getExpiresAtMillis(req.ExpiryTime),
		"enable":     true,
		"flow":       flow,
		"subId":      req.SubID,
		"reset":      resetDays,
		"tgId":       req.TgID,
		"comment":    req.Comment,
	}
	// Auto-renew profile fields are sent only when configured (see doAddClientWithID).
	if req.TrafficReset != "" {
		clientObj["resetDay"] = req.ResetDay
		clientObj["resetMax"] = req.ResetMax
		clientObj["trafficReset"] = req.TrafficReset
		clientObj["trafficResetDay"] = req.TrafficResetDay
	}

	if req.Enable != nil {
		clientObj["enable"] = *req.Enable
	}

	updateURL := fmt.Sprintf("%s/panel/api/clients/update/%s", c.host, url.PathEscape(req.CurrentEmail))

	respBody, err := c.doHTTPRequest(ctx, http.MethodPost, updateURL, func() (io.Reader, error) {
		return marshalJSON(clientObj)
	})
	if err != nil {
		return err
	}

	_, err = parseEnvelope(opUpdate, respBody)
	if err != nil {
		return err
	}

	logger.Info("Successfully updated client via new API",
		zap.String("current_email", req.CurrentEmail),
		zap.String("new_email", req.Email))

	return nil
}

// GetClientTraffic возвращает актуальный трафик клиента по email.
func (c *Client) GetClientTraffic(ctx context.Context, email string) (*ClientTraffic, error) {
	var result *ClientTraffic

	err := utils.RetryWithBackoff(ctx, config.XUIMaxRetries, config.XUIInitialRetryDelay, func() error {
		var err error

		result, err = c.doGetClientTraffic(ctx, email)

		return err
	})

	return result, err
}

// doGetClientTraffic выполняет GET /panel/api/clients/traffic/{email}.
func (c *Client) doGetClientTraffic(ctx context.Context, email string) (*ClientTraffic, error) {
	trafficURL := fmt.Sprintf("%s/panel/api/clients/traffic/%s", c.host, url.PathEscape(email))

	respBody, err := c.doHTTPRequest(ctx, http.MethodGet, trafficURL, nil)
	if err != nil {
		return nil, err
	}

	obj, err := parseEnvelope(opTraffic, respBody)
	if err != nil {
		return nil, err
	}

	// 3x-ui v3.10 answers an unknown email with success=true and obj=null
	// (verified against the live panel: {"success":true,"msg":"","obj":null}).
	// Parsing that as traffic data yields a misleading "unexpected end of JSON
	// input", which callers cannot tell apart from an infrastructure failure —
	// the traffic worker then logs a parse error instead of "client not found"
	// and silently skips the subscription. Map it to the typed sentinel.
	if isNullObj(obj) {
		return nil, ErrClientNotFound
	}

	var traffic ClientTraffic

	err = json.Unmarshal(obj, &traffic)
	if err != nil {
		return nil, fmt.Errorf("failed to parse traffic data: %w", err)
	}

	return &traffic, nil
}

// ClientRecord — ответ GET /panel/api/clients/get/{email}: карточка клиента
// (включая полный профиль авто-renew) плюс список inbound'ов, к которым
// клиент привязан. См. doc/panel-api-contract.md, раздел 2.1.
type ClientRecord struct {
	Client     ClientConfig `json:"client"`
	InboundIDs []int        `json:"inboundIds"`
}

// GetClientByEmail возвращает полную карточку клиента по email через
// выделенный эндпоинт GET /panel/api/clients/get/{email}. Эндпоинт отдаёт
// полный профиль авто-renew (reset/resetDay/resetMax/resetWeekday/
// trafficReset/trafficResetDay), который /clients/traffic не отдаёт.
// Возвращает ErrClientNotFound, если клиент не найден.
func (c *Client) GetClientByEmail(ctx context.Context, email string) (*ClientRecord, error) {
	var result *ClientRecord

	err := utils.RetryWithBackoff(ctx, config.XUIMaxRetries, config.XUIInitialRetryDelay, func() error {
		var innerErr error

		result, innerErr = c.doGetClientByEmail(ctx, email)

		return innerErr
	})

	return result, err
}

// doGetClientByEmail выполняет GET /panel/api/clients/get/{email} и разбирает
// obj.client и obj.inboundIds.
func (c *Client) doGetClientByEmail(ctx context.Context, email string) (*ClientRecord, error) {
	getURL := fmt.Sprintf("%s/panel/api/clients/get/%s", c.host, url.PathEscape(email))

	respBody, err := c.doHTTPRequest(ctx, http.MethodGet, getURL, nil)
	if err != nil {
		return nil, err
	}

	obj, err := parseEnvelope(opGet, respBody)
	if err != nil {
		return nil, err
	}

	if isNullObj(obj) {
		return nil, fmt.Errorf("get client %s: unexpected empty obj", email)
	}

	var record ClientRecord

	err = json.Unmarshal(obj, &record)
	if err != nil {
		return nil, fmt.Errorf("failed to parse client record for %s: %w", email, err)
	}

	return &record, nil
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

	err = json.Unmarshal(respBody, &apiResp)
	if err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if !apiResp.Success {
		return nil, fmt.Errorf("failed to get inbound: %s", apiResp.Msg)
	}

	var inbound Inbound

	err = json.Unmarshal(apiResp.Obj, &inbound)
	if err != nil {
		return nil, fmt.Errorf("failed to parse inbound data: %w", err)
	}

	return &inbound, nil
}

// getRequiredFlow возвращает flow, требуемый для работы с указанным inbound.
func (c *Client) getRequiredFlow(ctx context.Context, inboundID int) (string, error) {
	err := ctx.Err()
	if err != nil {
		return "", err
	}

	inbound, err := c.doGetInbound(ctx, inboundID)
	if err != nil {
		logger.Debug("Failed to get inbound for flow",
			zap.Error(err))

		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return "", err
		}

		return "", fmt.Errorf("get inbound for flow: %w", err)
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
