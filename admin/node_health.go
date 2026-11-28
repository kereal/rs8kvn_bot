package admin

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/xui"
)

const (
	// nodeHealthCheckConcurrency ограничивает одновременные пинги: панели за
	// одним админом не должны получать залп запросов.
	nodeHealthCheckConcurrency = 4
	// nodeHealthCheckTimeout — общий бюджет ручного прогона. Ноды, не
	// успевшие ответить, остаются available:false и не держат запрос вечно.
	nodeHealthCheckTimeout = 20 * time.Second
	// nodeHealthCheckErrorLimit — длина текста ошибки в ответе.
	nodeHealthCheckErrorLimit = 200
)

// nodeHealthItem — результат проверки одной ноды. Ошибка сети — данные
// (available:false), а не ошибка протокола.
type nodeHealthItem struct {
	NodeID    uint   `json:"node_id"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	Available bool   `json:"available"`
	LatencyMS int64  `json:"latency_ms"`
	// Supported — у типа ноды есть health-клиент (3x-ui с токеном). Для
	// proxman/fetch проверка не выполняется и это не считается падением.
	Supported bool   `json:"supported"`
	Error     string `json:"error,omitempty"`
}

// nodeHealthCheckResponse — сводный результат ручного прогона. checked_at =
// null в GET, пока проверка ни разу не запускалась.
type nodeHealthCheckResponse struct {
	CheckedAt   *time.Time       `json:"checked_at"`
	Items       []nodeHealthItem `json:"items"`
	Total       int              `json:"total"`
	Unavailable int              `json:"unavailable"`
	Unsupported int              `json:"unsupported"`
}

// SetNodeHealthLast хранит последний результат только в памяти процесса:
// health — ручная диагностика, а не доменный факт, и в БД не пишется.
func (s *Server) setNodeHealthLast(result nodeHealthCheckResponse) {
	s.nodeHealthMu.Lock()
	defer s.nodeHealthMu.Unlock()

	s.nodeHealthLast = &result
}

func (s *Server) lastNodeHealth() nodeHealthCheckResponse {
	s.nodeHealthMu.Lock()
	defer s.nodeHealthMu.Unlock()

	if s.nodeHealthLast == nil {
		return nodeHealthCheckResponse{Items: []nodeHealthItem{}}
	}

	return *s.nodeHealthLast
}

// handleNodeHealthCheck проверяет все ноды вручную. POST, потому что это
// действие оператора (сетевые вызовы), и фонового polling нет.
func (s *Server) handleNodeHealthCheck(w http.ResponseWriter, r *http.Request) {
	rows, err := s.nodes.ListNodeOverviews(r.Context())
	if err != nil {
		s.writeStoreError(w, "list nodes for health check", err)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), nodeHealthCheckTimeout)
	defer cancel()

	items := make([]nodeHealthItem, len(rows))
	sem := make(chan struct{}, nodeHealthCheckConcurrency)

	var wg sync.WaitGroup

	for i := range rows {
		node := rows[i].Node
		items[i] = nodeHealthItem{NodeID: node.ID, Name: node.Name, Type: string(node.Type)}

		if !nodeHealthSupported(&node) {
			items[i].Error = "проверка не поддерживается для этого типа ноды"
			continue
		}

		items[i].Supported = true

		wg.Add(1)

		go func(idx int, target database.Node) {
			defer wg.Done()

			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				items[idx].Error = "превышен общий таймаут проверки"
				return
			}

			items[idx].Available, items[idx].LatencyMS, items[idx].Error = pingNode(ctx, &target)
		}(i, node)
	}

	wg.Wait()

	result := nodeHealthCheckResponse{Items: items, Total: len(items)}
	for _, item := range items {
		if item.Supported && !item.Available {
			result.Unavailable++
		}
		if !item.Supported {
			result.Unsupported++
		}
	}

	checkedAt := time.Now().UTC()
	result.CheckedAt = &checkedAt
	s.setNodeHealthLast(result)

	writeJSON(w, http.StatusOK, result)
}

// handleNodeHealthLast отдаёт последний результат этого процесса. После
// рестарта панели результата нет: health не персистится.
func (s *Server) handleNodeHealthLast(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.lastNodeHealth())
}

// nodeHealthSupported: пинг есть только у 3x-ui с токеном. proxman/fetch
// опрашиваются иначе — health-клиент для них не вызывается.
func nodeHealthSupported(node *database.Node) bool {
	return node.Type == database.NodeType3xUI && strings.TrimSpace(node.APIToken) != ""
}

// pingNode пингует панель готовым xui.Client.Ping. Возвращает данные, а не
// ошибку: недоступность одной ноды не ломает сводный прогон.
func pingNode(ctx context.Context, node *database.Node) (bool, int64, string) {
	client, err := xui.NewClient(node.Host, node.APIToken)
	if err != nil {
		return false, 0, sanitizeNodeHealthError(err.Error(), node)
	}

	pingCtx, cancel := context.WithTimeout(ctx, config.XUIRequestTimeout)
	defer cancel()

	start := time.Now()
	err = client.Ping(pingCtx)
	latency := time.Since(start).Milliseconds()

	if err != nil {
		return false, latency, sanitizeNodeHealthError(err.Error(), node)
	}

	return true, latency, ""
}

// userInfoRe матчит userinfo в URL: «http://user:pass@host» → «http://***@host».
var userInfoRe = regexp.MustCompile(`://[^/@\s]+@`)

// sanitizeNodeHealthError убирает из текста ошибки секреты: токен панели и
// credentials в URL хоста. Текст ошибки — свободная строка HTTP-клиента, её
// нельзя отдавать как есть.
func sanitizeNodeHealthError(message string, node *database.Node) string {
	if token := strings.TrimSpace(node.APIToken); token != "" {
		message = strings.ReplaceAll(message, token, "***")
	}

	message = userInfoRe.ReplaceAllString(message, "://***@")

	runes := []rune(message)
	if len(runes) > nodeHealthCheckErrorLimit {
		message = string(runes[:nodeHealthCheckErrorLimit]) + "…"
	}

	return message
}
