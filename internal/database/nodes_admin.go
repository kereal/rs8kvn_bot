package database

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"unicode/utf8"

	"gorm.io/gorm"
)

// maxNodeNameLen — верхняя граница имени ноды (после trim).
const maxNodeNameLen = 100

// NodeOverview — нода со счётчиками зависимостей для списка и guard'ов
// удаления. Embedded, чтобы Scan одним SQL заполнил и поля ноды, и счётчики.
type NodeOverview struct {
	Node `gorm:"embedded"`

	PlansCount         int64
	SubscriptionsCount int64
}

// nodeOverviewSelect — один SQL на весь список: коррелированные подзапросы
// вместо N+1 (по образцу ListPlanOverviews).
const nodeOverviewSelect = `nodes.id, nodes.name, nodes.is_active, nodes.host, nodes.api_token,
	nodes.inbound_ids, nodes.subscription_url, nodes.type, nodes.created_at, nodes.updated_at,
	(SELECT COUNT(*) FROM plan_nodes WHERE plan_nodes.node_id = nodes.id) AS plans_count,
	(SELECT COUNT(*) FROM subscription_nodes WHERE subscription_nodes.node_id = nodes.id) AS subscriptions_count`

// NodeFieldError — ошибка валидации конкретного поля ноды. Поле попадает
// в details ответа 400, чтобы UI подсветил нужный input.
type NodeFieldError struct {
	Field  string
	Reason string
}

func (e *NodeFieldError) Error() string { return e.Field + ": " + e.Reason }

func (e *NodeFieldError) Unwrap() error { return ErrNodeInvalid }

// NodeInUseError — ноду нельзя удалить: есть зависимые строки. Details несёт
// счётчики зависимостей ("plans", "subscriptions") для ответа 409 in_use.
type NodeInUseError struct {
	Details map[string]int64
}

func (e *NodeInUseError) Error() string {
	return fmt.Sprintf("node is referenced by %d plan(s) and %d subscription(s)",
		e.Details["plans"], e.Details["subscriptions"])
}

func (e *NodeInUseError) Unwrap() error { return ErrNodeInUse }

// ParseInboundIDs разбирает nodes.inbound_ids (TEXT JSON-массив целых).
// Валидация живёт здесь, а не в хендлере: экран — не единственный вызывающий.
func ParseInboundIDs(raw string) ([]int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = "[]"
	}

	// []json.RawMessage: bare "null" и скаляры не должны проходить как валидные.
	var rawIDs []json.RawMessage

	err := json.Unmarshal([]byte(raw), &rawIDs)
	if err != nil {
		return nil, fmt.Errorf("must be a JSON array of integers: %w", err)
	}

	if rawIDs == nil {
		return nil, fmt.Errorf("must be a JSON array of integers")
	}

	ids := make([]int, 0, len(rawIDs))
	for _, rawID := range rawIDs {
		var id int

		err := json.Unmarshal(rawID, &id)
		if err != nil {
			return nil, fmt.Errorf("must be a JSON array of integers: %w", err)
		}

		if id < 0 {
			return nil, fmt.Errorf("must not contain negative values")
		}

		ids = append(ids, id)
	}

	return ids, nil
}

// EncodeInboundIDs сериализует список инбаундов в TEXT-JSON для nodes.inbound_ids.
func EncodeInboundIDs(ids []int) string {
	if len(ids) == 0 {
		return "[]"
	}

	data, err := json.Marshal(ids)
	if err != nil {
		// []int всегда сериализуем; ветка для симметрии с ParseInboundIDs.
		return "[]"
	}

	return string(data)
}

// ListNodeOverviews возвращает все ноды (активные и выключенные) с числом
// привязанных планов и подписок. Один SQL: список нод — единицы-десятки,
// пагинация не нужна, N+1 запрещён.
func (s *Service) ListNodeOverviews(ctx context.Context) ([]NodeOverview, error) {
	var rows []NodeOverview

	result := s.db.WithContext(ctx).
		Model(&Node{}).
		Select(nodeOverviewSelect).
		Order("nodes.id ASC").
		Scan(&rows)
	if result.Error != nil {
		return nil, fmt.Errorf("failed to list node overviews: %w", result.Error)
	}

	return rows, nil
}

// GetNodeOverview возвращает одну ноду со счётчиками зависимостей.
func (s *Service) GetNodeOverview(ctx context.Context, id uint) (NodeOverview, error) {
	var row NodeOverview

	result := s.db.WithContext(ctx).
		Model(&Node{}).
		Select(nodeOverviewSelect).
		Where("nodes.id = ?", id).
		Scan(&row)
	if result.Error != nil {
		return NodeOverview{}, fmt.Errorf("failed to get node overview %d: %w", id, result.Error)
	}

	if result.RowsAffected == 0 {
		return NodeOverview{}, fmt.Errorf("node %d: %w", id, ErrNodeNotFound)
	}

	return row, nil
}

// CreateNodeAdmin валидирует и вставляет ноду, затем пишет аудит.
// Существующий CreateNode (nodes.go) остаётся методом бота без валидации:
// админский путь оборачивает его, чтобы проверка полей жила в репозитории.
func (s *Service) CreateNodeAdmin(ctx context.Context, node *Node) error {
	if node == nil {
		return fmt.Errorf("create node: %w", ErrNodeInvalid)
	}

	node.Name = strings.TrimSpace(node.Name)

	err := validateNodeFields(node)
	if err != nil {
		return fmt.Errorf("create node: %w", err)
	}

	err = s.CreateNode(ctx, node)
	if err != nil {
		return fmt.Errorf("create node: %w", err)
	}

	_ = s.insertAuditLogBestEffort(ctx, AuditNodeCreated(node))

	return nil
}

// UpdateNodeAdmin переписывает изменяемые поля ноды. Тип ноды менять нельзя:
// он определяет клиента провижининга, а существующие subscription_nodes этой
// ноды провижинены старым типом. Пустой api_token означает «не менять» —
// секрет write-only, задать пустой токен через панель нельзя.
func (s *Service) UpdateNodeAdmin(ctx context.Context, node *Node) error {
	if node == nil || node.ID == 0 {
		return fmt.Errorf("update node: %w", ErrNodeNotFound)
	}

	var before Node

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		err := tx.Where("id = ?", node.ID).First(&before).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("node %d: %w", node.ID, ErrNodeNotFound)
			}

			return fmt.Errorf("load node for update: %w", err)
		}

		if before.Type != node.Type {
			return fmt.Errorf("node %d: type %q is immutable: %w", node.ID, before.Type, ErrNodeTypeProtected)
		}

		node.Name = strings.TrimSpace(node.Name)

		err = validateNodeFields(node)
		if err != nil {
			return fmt.Errorf("update node: %w", err)
		}

		if node.APIToken == "" {
			node.APIToken = before.APIToken
		}

		err = tx.Save(node).Error
		if err != nil {
			return fmt.Errorf("save node %d: %w", node.ID, err)
		}

		return nil
	})
	if err != nil {
		return err
	}

	_ = s.insertAuditLogBestEffort(ctx, AuditNodeUpdated(&before, node))

	return nil
}

// DeleteNode удаляет ноду, на которую ничего не ссылается. plan_nodes каскадно
// удалится вместе с нодой (тихая отвязка), а FK subscription_nodes без
// ON DELETE уронит DELETE — оба случая перехватываются guard'ом со счётчиками.
func (s *Service) DeleteNode(ctx context.Context, id uint) (Node, error) {
	var deleted Node

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		err := tx.Where("id = ?", id).First(&deleted).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("node %d: %w", id, ErrNodeNotFound)
			}

			return fmt.Errorf("load node for delete: %w", err)
		}

		var plans, subscriptions int64

		err = tx.Model(&PlanNode{}).Where("node_id = ?", id).Count(&plans).Error
		if err != nil {
			return fmt.Errorf("count node plans: %w", err)
		}

		err = tx.Model(&SubscriptionNode{}).Where("node_id = ?", id).Count(&subscriptions).Error
		if err != nil {
			return fmt.Errorf("count node subscriptions: %w", err)
		}

		if plans > 0 || subscriptions > 0 {
			return &NodeInUseError{Details: map[string]int64{"plans": plans, "subscriptions": subscriptions}}
		}

		err = tx.Delete(&Node{}, id).Error
		if err != nil {
			// Гонка с появившейся зависимостью упирается во внешний ключ SQLite.
			if strings.Contains(err.Error(), "FOREIGN KEY constraint failed") {
				return fmt.Errorf("delete node %d: %w", id, ErrNodeInUse)
			}

			return fmt.Errorf("delete node %d: %w", id, err)
		}

		return nil
	})
	if err != nil {
		return Node{}, err
	}

	_ = s.insertAuditLogBestEffort(ctx, AuditNodeDeleted(&deleted))

	return deleted, nil
}

// ReplacePlanNodes заменяет набор нод плана целиком (идемпотентно): удаляются
// только отвязанные ноды, добавляются только новые — повтор того же набора не
// трогает строки. Отвязать ноду с subscription_nodes можно: панель меняет
// конфигурацию, а не состояния (ADR-0002 §2), фактическое удаление клиентов
// выполнит SyncService.ReconcilePlanNodes.
func (s *Service) ReplacePlanNodes(ctx context.Context, planID uint, nodeIDs []uint) ([]uint, error) {
	target := sortedUniqueIDs(nodeIDs)

	var (
		current []uint
		added   []uint
		removed []uint
	)

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var plan Plan

		err := tx.Where("id = ?", planID).First(&plan).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("plan %d: %w", planID, ErrPlanNotFound)
			}

			return fmt.Errorf("load plan %d: %w", planID, err)
		}

		if len(target) > 0 {
			var found []Node

			err = tx.Where("id IN ?", target).Find(&found).Error
			if err != nil {
				return fmt.Errorf("load nodes for plan %d: %w", planID, err)
			}

			if len(found) != len(target) {
				return &NodeFieldError{
					Field:  "node_ids",
					Reason: fmt.Sprintf("nodes not found: %s", missingIDs(target, found)),
				}
			}
		}

		err = tx.Model(&PlanNode{}).
			Where("plan_id = ?", planID).
			Order("node_id ASC").
			Pluck("node_id", &current).Error
		if err != nil {
			return fmt.Errorf("load plan nodes for plan %d: %w", planID, err)
		}

		removed = excludeIDs(current, target)
		added = excludeIDs(target, current)

		if len(removed) > 0 {
			err = tx.Where("plan_id = ? AND node_id IN ?", planID, removed).Delete(&PlanNode{}).Error
			if err != nil {
				return fmt.Errorf("unlink nodes from plan %d: %w", planID, err)
			}
		}

		for _, id := range added {
			err = tx.Create(&PlanNode{PlanID: planID, NodeID: id}).Error
			if err != nil {
				return fmt.Errorf("link node %d to plan %d: %w", id, planID, err)
			}
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	if len(added) > 0 || len(removed) > 0 {
		_ = s.insertAuditLogBestEffort(ctx, AuditPlanNodesChanged(planID, current, target, added, removed))
	}

	return target, nil
}

// GetPlanNodeIDs возвращает идентификаторы нод, привязанных к плану.
// Имя без «Count»: это список, а не счётчик.
func (s *Service) GetPlanNodeIDs(ctx context.Context, planID uint) ([]uint, error) {
	var plan Plan

	err := s.db.WithContext(ctx).Where("id = ?", planID).First(&plan).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("plan %d: %w", planID, ErrPlanNotFound)
		}

		return nil, fmt.Errorf("load plan %d: %w", planID, err)
	}

	var ids []uint

	err = s.db.WithContext(ctx).
		Model(&PlanNode{}).
		Where("plan_id = ?", planID).
		Order("node_id ASC").
		Pluck("node_id", &ids).Error
	if err != nil {
		return nil, fmt.Errorf("list plan nodes for plan %d: %w", planID, err)
	}

	if ids == nil {
		ids = []uint{}
	}

	return ids, nil
}

// validateNodeFields проверяет поля ноды для create/update. Общие для обеих
// операций правила: имя, subscription_url, допустимый тип, host для типов с
// панелью, корректный inbound_ids.
func validateNodeFields(node *Node) error {
	if node.Name == "" {
		return &NodeFieldError{Field: "name", Reason: "is required"}
	}

	if utf8.RuneCountInString(node.Name) > maxNodeNameLen {
		return &NodeFieldError{Field: "name", Reason: fmt.Sprintf("must be at most %d characters", maxNodeNameLen)}
	}

	if !isHTTPURL(node.SubscriptionURL) {
		return &NodeFieldError{Field: "subscription_url", Reason: "must be an http(s):// URL"}
	}

	switch node.Type {
	case NodeType3xUI, NodeTypeProxman:
		if !isHTTPURL(node.Host) {
			return &NodeFieldError{
				Field:  "host",
				Reason: fmt.Sprintf("must be an http(s):// URL for node type %q", node.Type),
			}
		}
	case NodeTypeFetch:
		// fetch-нода читает subscription_url как источник конфига; host не нужен.
	default:
		return &NodeFieldError{
			Field:  "type",
			Reason: fmt.Sprintf("must be one of %q, %q, %q", NodeType3xUI, NodeTypeProxman, NodeTypeFetch),
		}
	}

	_, err := ParseInboundIDs(node.InboundIDs)
	if err != nil {
		return &NodeFieldError{Field: "inbound_ids", Reason: err.Error()}
	}

	return nil
}

// isHTTPURL проверяет схему http/https и непустой host.
func isHTTPURL(raw string) bool {
	if raw == "" {
		return false
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		return false
	}

	return (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != ""
}

// sortedUniqueIDs убирает дубликаты и сортирует входной набор.
func sortedUniqueIDs(ids []uint) []uint {
	seen := make(map[uint]bool, len(ids))
	unique := make([]uint, 0, len(ids))

	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			unique = append(unique, id)
		}
	}

	slices.Sort(unique)

	return unique
}

// excludeIDs возвращает элементы from, отсутствующие в exclude (порядок from
// сохраняется; оба набора уже без дубликатов).
func excludeIDs(from, exclude []uint) []uint {
	if len(from) == 0 || len(exclude) == 0 {
		return from
	}

	excluded := make(map[uint]bool, len(exclude))
	for _, id := range exclude {
		excluded[id] = true
	}

	rest := make([]uint, 0, len(from))
	for _, id := range from {
		if !excluded[id] {
			rest = append(rest, id)
		}
	}

	return rest
}

// missingIDs перечисляет идентификаторы из want, которых нет среди found.
func missingIDs(want []uint, found []Node) string {
	present := make(map[uint]bool, len(found))
	for _, node := range found {
		present[node.ID] = true
	}

	missing := make([]uint, 0, len(want))
	for _, id := range want {
		if !present[id] {
			missing = append(missing, id)
		}
	}

	parts := make([]string, len(missing))
	for i, id := range missing {
		parts[i] = fmt.Sprintf("%d", id)
	}

	return strings.Join(parts, ", ")
}
