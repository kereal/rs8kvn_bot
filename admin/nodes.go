package admin

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/service"
	"github.com/kereal/rs8kvn_bot/internal/xui"
)

// NodeStore — узкий контракт экрана «Ноды». Конкретный *database.Service
// удовлетворяет ему. CreateNodeAdmin оборачивает ботовский CreateNode
// валидацией и аудитом; GetNodeByID возвращает ноду целиком (с токеном) для
// health-пинга — токен в ответ не идёт.
type NodeStore interface {
	ListNodeOverviews(ctx context.Context) ([]database.NodeOverview, error)
	GetNodeOverview(ctx context.Context, id uint) (database.NodeOverview, error)
	CreateNodeAdmin(ctx context.Context, node *database.Node) error
	UpdateNodeAdmin(ctx context.Context, node *database.Node) error
	DeleteNode(ctx context.Context, id uint) (database.Node, error)
	ReplacePlanNodes(ctx context.Context, planID uint, nodeIDs []uint) ([]uint, error)
	GetPlanNodeIDs(ctx context.Context, planID uint) ([]uint, error)
	GetNodeByID(ctx context.Context, id uint) (*database.Node, error)
}

// nodeItem — DTO ответа. api_token в DTO не попадает никогда (P16):
// вместо секрета — has_api_token.
type nodeItem struct {
	ID                 uint      `json:"id"`
	Name               string    `json:"name"`
	Type               string    `json:"type"`
	Host               string    `json:"host"`
	SubscriptionURL    string    `json:"subscription_url"`
	InboundIDs         []int     `json:"inbound_ids"`
	IsActive           bool      `json:"is_active"`
	HasAPIToken        bool      `json:"has_api_token"`
	PlansCount         int64     `json:"plans_count"`
	SubscriptionsCount int64     `json:"subscriptions_count"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

// nodePayload — тело create/PUT. Write-only: нигде не сериализуется.
// В PUT пустая api_token = «не менять».
type nodePayload struct {
	Name            string `json:"name"`
	Type            string `json:"type"`
	Host            string `json:"host"`
	SubscriptionURL string `json:"subscription_url"`
	InboundIDs      []int  `json:"inbound_ids"`
	APIToken        string `json:"api_token"`
	IsActive        bool   `json:"is_active"`
}

func (s *Server) handleListNodes(w http.ResponseWriter, r *http.Request) {
	rows, err := s.nodes.ListNodeOverviews(r.Context())
	if err != nil {
		s.writeStoreError(w, "list nodes", err)

		return
	}

	items := make([]nodeItem, 0, len(rows))
	for _, row := range rows {
		item, err := toNodeItem(row)
		if err != nil {
			s.writeStoreError(w, "list nodes", err)

			return
		}

		items = append(items, item)
	}

	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": len(items)})
}

func (s *Server) handleCreateNode(w http.ResponseWriter, r *http.Request) {
	payload, ok := decodeNodePayload(w, r)
	if !ok {
		return
	}

	node := payloadToNode(payload)

	err := s.nodes.CreateNodeAdmin(r.Context(), node)
	if err != nil {
		s.writeStoreError(w, "create node", err)

		return
	}

	writeNode(w, http.StatusCreated, s, r, node.ID)
}

func (s *Server) handleUpdateNode(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	payload, ok := decodeNodePayload(w, r)
	if !ok {
		return
	}

	node := payloadToNode(payload)
	node.ID = id

	err := s.nodes.UpdateNodeAdmin(r.Context(), node)
	if err != nil {
		s.writeStoreError(w, "update node", err)

		return
	}

	writeNode(w, http.StatusOK, s, r, id)
}

func (s *Server) handleDeleteNode(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	node, err := s.nodes.DeleteNode(r.Context(), id)
	if err != nil {
		s.writeStoreError(w, "delete node", err)

		return
	}

	// Удалённая строка без счётчиков (они обнулились вместе с зависимостями):
	// UI показывает, что именно удалил.
	item, err := toNodeItem(database.NodeOverview{Node: node})
	if err != nil {
		s.writeStoreError(w, "delete node", err)

		return
	}

	writeJSON(w, http.StatusOK, item)
}

func (s *Server) handleGetPlanNodes(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	ids, err := s.nodes.GetPlanNodeIDs(r.Context(), id)
	if err != nil {
		s.writeStoreError(w, "list plan nodes", err)

		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"node_ids": ids})
}

func (s *Server) handlePutPlanNodes(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	payload, ok := decodePlanNodesPayload(w, r)
	if !ok {
		return
	}

	ids, err := s.nodes.ReplacePlanNodes(r.Context(), id, payload.NodeIDs)
	if err != nil {
		s.writeStoreError(w, "replace plan nodes", err)

		return
	}

	if ids == nil {
		ids = []uint{}
	}

	writeJSON(w, http.StatusOK, map[string]any{"node_ids": ids})
}

// nodeHealthResult — результат ручной диагностики. Ошибка сети — данные, а не
// ошибка протокола: хендлер отвечает 200 c available:false.
type nodeHealthResult struct {
	Available bool   `json:"available"`
	LatencyMS int64  `json:"latency_ms"`
	Error     string `json:"error,omitempty"`
	CheckedAt string `json:"checked_at"`
}

func (s *Server) handleNodeHealth(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	node, err := s.nodes.GetNodeByID(r.Context(), id)
	if err != nil {
		s.writeStoreError(w, "load node for health", err)

		return
	}

	if node.Type != database.NodeType3xUI {
		writeErrorDetails(w, http.StatusBadRequest, "validation", "health check is not supported for this node type",
			map[string]any{"type": "health not supported for this node type"})

		return
	}

	if strings.TrimSpace(node.APIToken) == "" {
		writeErrorDetails(w, http.StatusBadRequest, "validation", "api_token is not set",
			map[string]any{"api_token": "not set"})

		return
	}

	writeJSON(w, http.StatusOK, s.checkNodeHealth(r, node))
}

// checkNodeHealth пингует панель через готовый xui.Client.Ping — сетевой код
// не дублируется. Новый HTTP-клиент не открывается: NewClient строит клиента
// с тем же транспортом, что и провижининг.
func (s *Server) checkNodeHealth(r *http.Request, node *database.Node) nodeHealthResult {
	result := nodeHealthResult{CheckedAt: time.Now().UTC().Format(time.RFC3339)}

	// Кривой host — результат диагностики (available:false), а не ошибка API.
	client, err := xui.NewClient(node.Host, node.APIToken)
	if err != nil {
		result.Error = err.Error()

		return result
	}

	ctx, cancel := context.WithTimeout(r.Context(), config.XUIRequestTimeout)
	defer cancel()

	start := time.Now()

	err = client.Ping(ctx)
	result.LatencyMS = time.Since(start).Milliseconds()

	if err != nil {
		result.Error = err.Error()

		return result
	}

	result.Available = true

	return result
}

// NodeLiveSource is the narrow service contract for the live read on the node
// detail page (SetNodeLiveSource). Как /traffic у подписок: единственный
// эндпоинт экрана «Ноды», который опрашивает панели, поэтому он отдельный и
// не входит в NodeStore.
type NodeLiveSource interface {
	GetNodeLive(ctx context.Context, nodeID uint) (*service.NodeLiveInfo, error)
}

// nodeLiveResult — DTO ответа GET /admin/api/nodes/{id}/live.
type nodeLiveResult struct {
	NodeID        uint   `json:"node_id"`
	Available     bool   `json:"available"`
	PingLatencyMS int64  `json:"ping_latency_ms"`
	PingError     string `json:"ping_error,omitempty"`
	Clients       int    `json:"clients"`
	Disabled      int    `json:"disabled"`
	TrafficBytes  int64  `json:"traffic_bytes"`
	// LastOnline — unix ms из панели, 0 = нет данных.
	LastOnline int64 `json:"last_online"`
}

// SetNodeLiveSource wires the live node read for the node detail page. Without
// it the endpoint answers 503: опрос панелей — тяжёлый вызов, остальной экран
// работает и без него.
func (s *Server) SetNodeLiveSource(src NodeLiveSource) {
	s.nodeLive = src
}

// handleNodeLive отвечает за живые данные ноды: пинг панели и суммарный
// трафик её активных клиентов. Best-effort: недоступность панели — данные
// (available:false), а не ошибка протокола.
func (s *Server) handleNodeLive(w http.ResponseWriter, r *http.Request) {
	if s.nodeLive == nil {
		writeError(w, http.StatusServiceUnavailable, "internal",
			"node live service is not wired into the panel")

		return
	}

	id, ok := pathID(w, r)
	if !ok {
		return
	}

	// 404 на неизвестную ноду — sentinel из store; живой опрос дальше.
	_, err := s.nodes.GetNodeByID(r.Context(), id)
	if err != nil {
		s.writeStoreError(w, "load node for live", err)

		return
	}

	info, err := s.nodeLive.GetNodeLive(r.Context(), id)
	if err != nil {
		s.writeStoreError(w, "node live", err)

		return
	}

	writeJSON(w, http.StatusOK, nodeLiveResult{
		NodeID:        info.NodeID,
		Available:     info.Available,
		PingLatencyMS: info.PingLatencyMS,
		PingError:     info.PingError,
		Clients:       info.Clients,
		Disabled:      info.Disabled,
		TrafficBytes:  info.TrafficBytes,
		LastOnline:    info.LastOnline,
	})
}

// payloadToNode переводит тело запроса в модель. inbound_ids сериализуется в
// TEXT-JSON; api_token передаётся как есть — пустая строка в PUT означает
// «не менять» и обрабатывается в репозитории.
func payloadToNode(payload *nodePayload) *database.Node {
	return &database.Node{
		Name:            payload.Name,
		Type:            database.NodeType(payload.Type),
		Host:            payload.Host,
		APIToken:        payload.APIToken,
		SubscriptionURL: payload.SubscriptionURL,
		InboundIDs:      database.EncodeInboundIDs(payload.InboundIDs),
		IsActive:        payload.IsActive,
	}
}

func toNodeItem(row database.NodeOverview) (nodeItem, error) {
	inbounds, err := database.ParseInboundIDs(row.InboundIDs)
	if err != nil {
		return nodeItem{}, err
	}

	return nodeItem{
		ID:                 row.ID,
		Name:               row.Name,
		Type:               string(row.Type),
		Host:               row.Host,
		SubscriptionURL:    row.SubscriptionURL,
		InboundIDs:         inbounds,
		IsActive:           row.IsActive,
		HasAPIToken:        row.APIToken != "",
		PlansCount:         row.PlansCount,
		SubscriptionsCount: row.SubscriptionsCount,
		CreatedAt:          row.CreatedAt,
		UpdatedAt:          row.UpdatedAt,
	}, nil
}

// writeNode строит DTO из свежей строки (счётчики, timestamps, актуальный
// признак токена) — один SELECT на мутацию, N+1 нет.
func writeNode(w http.ResponseWriter, status int, s *Server, r *http.Request, id uint) {
	row, err := s.nodes.GetNodeOverview(r.Context(), id)
	if err != nil {
		s.writeStoreError(w, "load node", err)

		return
	}

	item, err := toNodeItem(row)
	if err != nil {
		s.writeStoreError(w, "load node", err)

		return
	}

	writeJSON(w, status, item)
}

func decodeNodePayload(w http.ResponseWriter, r *http.Request) (*nodePayload, bool) {
	var payload nodePayload

	ok := decodeJSONBody(w, r, &payload)

	return &payload, ok
}

func decodePlanNodesPayload(w http.ResponseWriter, r *http.Request) (*planNodesPayload, bool) {
	var payload planNodesPayload

	ok := decodeJSONBody(w, r, &payload)

	return &payload, ok
}

// planNodesPayload — тело PUT /admin/api/plans/{id}/nodes: полная замена
// набора нод плана.
type planNodesPayload struct {
	NodeIDs []uint `json:"node_ids"`
}

// decodeJSONBody декодирует JSON-тело с лимитом размера и DisallowUnknownFields
// (P4). Общий код для payload'ов экрана «Ноды».
func decodeJSONBody(w http.ResponseWriter, r *http.Request, target any) bool {
	body := http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(body)
	dec.DisallowUnknownFields()

	err := dec.Decode(target)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")

		return false
	}

	// Reject trailing data so a malformed client cannot think it succeeded.
	_, err = dec.Token()
	if err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")

		return false
	}

	return true
}

// writeErrorDetails добавляет details к стандартному конверту ошибки (P9).
func writeErrorDetails(w http.ResponseWriter, status int, code, message string, details map[string]any) {
	writeJSON(w, status, map[string]any{"code": code, "message": message, "details": details})
}

// writeNodeInvalidError выводит ошибку валидации с details{field: причина}.
func writeNodeInvalidError(w http.ResponseWriter, err error) {
	var fieldErr *database.NodeFieldError
	if errors.As(err, &fieldErr) {
		writeErrorDetails(w, http.StatusBadRequest, "validation", err.Error(),
			map[string]any{fieldErr.Field: fieldErr.Reason})

		return
	}

	writeError(w, http.StatusBadRequest, "validation", err.Error())
}

// writeNodeInUseError выводит 409 in_use со счётчиками зависимостей в details.
func writeNodeInUseError(w http.ResponseWriter, err error) {
	var inUse *database.NodeInUseError
	if errors.As(err, &inUse) && len(inUse.Details) > 0 {
		details := make(map[string]any, len(inUse.Details))
		for key, value := range inUse.Details {
			details[key] = value
		}

		writeErrorDetails(w, http.StatusConflict, "in_use", err.Error(), details)

		return
	}

	writeError(w, http.StatusConflict, "in_use", err.Error())
}
