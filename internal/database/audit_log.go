package database

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// EntityType адресует сущность, к которой относится событие аудита.
//
// Это открытый список: журналу всё равно, что логировать, новый тип
// сущности = новая константа, миграция не нужна.
type EntityType string

const (
	EntitySubscription EntityType = "subscription"
	EntityOrder        EntityType = "order"
	EntityBroadcast    EntityType = "broadcast"
	EntityPlan         EntityType = "plan"
	EntityNode         EntityType = "node"
	EntityProduct      EntityType = "product"
	EntitySystem       EntityType = "system"
)

// ActorType — кто инициировал событие.
type ActorType string

const (
	ActorUser            ActorType = "user"
	ActorAdmin           ActorType = "admin"
	ActorWorker          ActorType = "worker"
	ActorPaymentProvider ActorType = "payment_provider"
	ActorSystem          ActorType = "system"
)

// AuditEvent — имя события. Открытый реестр: новое событие = одна константа.
// Имена в lower_snake_case, прошедшее время — past tense.
const (
	// Жизненный цикл подписки.

	AuditEventSubscriptionCreated    = "subscription_created"
	AuditEventTrialCreated           = "trial_created"
	AuditEventTrialBound             = "trial_bound"
	AuditEventSubscriptionReanimated = "subscription_reanimated"
	AuditEventSubscriptionRenewed    = "subscription_renewed"
	AuditEventPlanChanged            = "plan_changed"
	AuditEventSubscriptionExpired    = "subscription_expired"
	AuditEventDowngradedToFree       = "downgraded_to_free"
	AuditEventSubscriptionRevoked    = "subscription_revoked"
	AuditEventSubscriptionDeleted    = "subscription_deleted"
	AuditEventTrialDeleted           = "trial_deleted"

	// Уведомления.

	AuditEventReminderSent    = "reminder_sent"
	AuditEventTrafficNotified = "traffic_notified"

	// Оплата и заказы.

	AuditEventOrderCreated          = "order_created"
	AuditEventPaymentConfirmed      = "payment_confirmed"
	AuditEventOrderExpired          = "order_expired"
	AuditEventOrderCanceled         = "order_canceled"
	AuditEventChargebackReceived    = "chargeback_received"
	AuditEventPaymentCreationUncert = "payment_creation_uncertain"

	// Рассылки.

	AuditEventBroadcastStarted   = "broadcast_started"
	AuditEventBroadcastCompleted = "broadcast_completed"
	AuditEventBroadcastFailed    = "broadcast_failed"
	AuditEventBroadcastCanceled  = "broadcast_canceled"

	// Технические агрегаты (сводки за прогон воркеров, не per-item шум).

	AuditEventSyncRunDegraded        = "sync_run_degraded"
	AuditEventOrphanedClientsRevoked = "orphaned_clients_revoked"
)

// AuditLog — одна запись журнала аудита (append-only: только INSERT и SELECT).
//
// См. миграцию 039_create_audit_log: entity_type/event без CHECK-констрейнтов,
// FK нет — история переживает физическое удаление сущности.
type AuditLog struct {
	ID         uint       `gorm:"primaryKey"`
	EntityType EntityType `gorm:"type:text;not null;column:entity_type"`
	// EntityID — строковое представление ID сущности; nil для системных событий.
	EntityID  *string   `gorm:"column:entity_id"`
	Event     string    `gorm:"type:text;not null;column:event"`
	ActorType ActorType `gorm:"type:text;not null;column:actor_type"`
	ActorID   *string   `gorm:"column:actor_id"`
	// OldState/NewState — JSON-снапшот изменившихся полей до/после.
	OldState *string `gorm:"column:old_state"`
	// NewState — JSON; для created-событий содержит начальное состояние.
	NewState *string `gorm:"column:new_state"`
	// Details — JSON со свободным контекстом события.
	Details   string    `gorm:"type:text;not null;default:'{}';column:details"`
	CreatedAt time.Time `gorm:"autoCreateTime;column:created_at"`
}

// TableName фиксирует имя таблицы независимо от соглашений GORM о множественном числе.
func (AuditLog) TableName() string { return "audit_log" }

// AuditLogger — точка записи для сервисов. Реализуется *database.Service и
// фейком в testutil. Все методы best-effort по контракту: сервисы не должны
// прерывать бизнес-операцию из-за ошибки журнала (кроме записи в платежной tx,
// где аудит атомарен с изменением).
type AuditLogger interface {
	InsertAuditLog(ctx context.Context, e *AuditLog) error
}

// --- Конструкторы событий -------------------------------------------------
//
// Каждый конструктор фиксирует семантику события в одном месте: entity,
// actor, состав old/new/details. Сервисы вызывают их и передают результат
// в logAudit (best-effort) или insertAuditLogInTx (внутри транзакции).

// formatSubID возвращает entity_id подписки: DB-идентификатор по умолчанию,
// для непривязанных trial (telegram_id < 0) — понятный строковый суффикс.
func formatSubID(sub *Subscription) string {
	if sub == nil {
		return ""
	}

	return fmt.Sprintf("%d", sub.ID)
}

// auditSubDetails собирает стандартный контекст подписки (telegram_id,
// username, subscription_id), чтобы каждое событие подписки было
// самодостаточным даже после физического удаления строки.
func auditSubDetails(sub *Subscription) map[string]any {
	d := map[string]any{}
	if sub == nil {
		return d
	}

	d["subscription_db_id"] = sub.ID
	if sub.TelegramID != 0 {
		d["telegram_id"] = sub.TelegramID
	}
	if sub.Username != "" {
		d["username"] = sub.Username
	}
	if sub.SubscriptionID != "" {
		d["subscription_id"] = sub.SubscriptionID
	}

	return d
}

// NewAuditLog — универсальный конструктор для событий, которым не нужен
// специализированный. state — nil-able; nil/пустые map превращаются в NULL.
func NewAuditLog(entityType EntityType, entityID string, event string, actorType ActorType, actorID string, oldState, newState, details map[string]any) *AuditLog {
	return &AuditLog{
		EntityType: entityType,
		EntityID:   marshalAuditJSONString(entityID),
		Event:      event,
		ActorType:  actorType,
		ActorID:    marshalAuditJSONString(actorID),
		OldState:   marshalAuditJSON(oldState),
		NewState:   marshalAuditJSON(newState),
		Details:    marshalAuditDetails(details),
	}
}

// marshalAuditJSON сериализует map в JSON-строку для колонок old_state/new_state.
// nil и пустые map дают NULL (не '{}'), чтобы «полей не менялось» отличалось
// от «поле сброшено в null».
func marshalAuditJSON(m map[string]any) *string {
	if len(m) == 0 {
		return nil
	}

	data, err := json.Marshal(m)
	if err != nil {
		return nil
	}

	// #nosec G115 -- строка без знаковых расширений
	s := string(data)

	return &s
}

// marshalAuditJSONString сериализует строковый идентификатор (entity_id/actor_id).
func marshalAuditJSONString(v string) *string {
	if v == "" {
		return nil
	}

	return &v
}

// marshalAuditDetails сериализует details; пустая map даёт '{}'.
func marshalAuditDetails(m map[string]any) string {
	if len(m) == 0 {
		return "{}"
	}

	data, err := json.Marshal(m)
	if err != nil {
		return "{}"
	}

	return string(data)
}

// subStateSnapshot — снапшот значимых полей подписки для old_state/new_state.
func subStateSnapshot(sub *Subscription) map[string]any {
	return SubscriptionAuditState(sub)
}

// SubscriptionAuditState возвращает снапшот значимых полей подписки для
// old_state/new_state. Публичен, чтобы сервисы могли снять снапшот «до»
// перед мутацией и передать его в AuditPlanChanged.
func SubscriptionAuditState(sub *Subscription) map[string]any {
	if sub == nil {
		return nil
	}

	s := map[string]any{
		"status":  sub.Status,
		"plan_id": sub.PlanID,
	}
	if sub.ExpiresAt != nil {
		s["expires_at"] = sub.ExpiresAt.UTC().Format(time.RFC3339)
	}
	if sub.ProductID != nil {
		s["product_id"] = *sub.ProductID
	}
	if sub.PricePaidCents != 0 {
		s["price_paid_cents"] = sub.PricePaidCents
	}

	return s
}

// diffState возвращает JSON-пару «до/после» только по изменившимся полям.
// Передавайте snapshot ДО мутации и после неё.
func diffState(before, after map[string]any) (oldJSON, newJSON *string) {
	changed := map[string]any{}
	changedAfter := map[string]any{}

	for k, v := range after {
		if oldVal, ok := before[k]; !ok || fmt.Sprint(oldVal) != fmt.Sprint(v) {
			changed[k] = oldVal
			changedAfter[k] = v
		}
	}
	// Поля, удалённые из after (например expires_at стал nil).
	for k, v := range before {
		if _, ok := after[k]; !ok {
			changed[k] = v
			changedAfter[k] = nil
		}
	}

	if len(changed) == 0 {
		return nil, nil
	}

	return marshalAuditJSON(changed), marshalAuditJSON(changedAfter)
}

// --- События подписок ------------------------------------------------------

// AuditSubscriptionCreated фиксирует создание привязанной подписки.
func AuditSubscriptionCreated(sub *Subscription, inviteCode string) *AuditLog {
	details := auditSubDetails(sub)
	if inviteCode != "" {
		details["invite_code"] = inviteCode
	}

	return &AuditLog{
		EntityType: EntitySubscription,
		EntityID:   marshalAuditJSONString(formatSubID(sub)),
		Event:      AuditEventSubscriptionCreated,
		ActorType:  ActorUser,
		ActorID:    auditActorTelegram(sub),
		NewState:   marshalAuditJSON(subStateSnapshot(sub)),
		Details:    marshalAuditDetails(details),
	}
}

// AuditTrialCreated фиксирует создание анонимной trial-подписки.
func AuditTrialCreated(sub *Subscription) *AuditLog {
	return &AuditLog{
		EntityType: EntitySubscription,
		EntityID:   marshalAuditJSONString(formatSubID(sub)),
		Event:      AuditEventTrialCreated,
		ActorType:  ActorUser,
		NewState:   marshalAuditJSON(subStateSnapshot(sub)),
		Details:    marshalAuditDetails(auditSubDetails(sub)),
	}
}

// AuditTrialBound фиксирует привязку trial к Telegram-пользователю.
func AuditTrialBound(sub *Subscription) *AuditLog {
	return &AuditLog{
		EntityType: EntitySubscription,
		EntityID:   marshalAuditJSONString(formatSubID(sub)),
		Event:      AuditEventTrialBound,
		ActorType:  ActorUser,
		ActorID:    auditActorTelegram(sub),
		NewState:   marshalAuditJSON(subStateSnapshot(sub)),
		Details:    marshalAuditDetails(auditSubDetails(sub)),
	}
}

// AuditSubscriptionReanimated фиксирует оживление revoked-подписки.
func AuditSubscriptionReanimated(sub *Subscription) *AuditLog {
	return &AuditLog{
		EntityType: EntitySubscription,
		EntityID:   marshalAuditJSONString(formatSubID(sub)),
		Event:      AuditEventSubscriptionReanimated,
		ActorType:  ActorUser,
		ActorID:    auditActorTelegram(sub),
		NewState:   marshalAuditJSON(subStateSnapshot(sub)),
		Details:    marshalAuditDetails(auditSubDetails(sub)),
	}
}

// AuditSubscriptionRenewed фиксирует успешную оплату (продление/активацию).
// before — состояние подписки до платежа (nil для первой активации),
// after — состояние, сохранённое транзакцией подтверждения платежа.
// NewState строится из after, иначе событие показывало бы прежний тариф и срок.
func AuditSubscriptionRenewed(before, after *Subscription, product *Product) *AuditLog {
	details := auditSubDetails(after)
	if product != nil {
		details["product_id"] = product.ID
		details["product_name"] = product.Name
		details["duration_days"] = product.DurationDays
		details["price_cents"] = product.PriceCents
		details["currency"] = product.Currency
	}
	if before != nil && before.ExpiresAt != nil {
		details["old_expires_at"] = before.ExpiresAt.UTC().Format(time.RFC3339)
	}

	return &AuditLog{
		EntityType: EntitySubscription,
		EntityID:   marshalAuditJSONString(formatSubID(after)),
		Event:      AuditEventSubscriptionRenewed,
		ActorType:  ActorPaymentProvider,
		ActorID:    marshalAuditJSONString("platega"),
		OldState:   marshalAuditJSON(subStateSnapshot(before)),
		NewState:   marshalAuditJSON(subStateSnapshot(after)),
		Details:    marshalAuditDetails(details),
	}
}

// AuditPlanChanged фиксирует смену тарифа (админ /setplan). days — явная
// длительность из команды (0 = не задана).
func AuditPlanChanged(sub *Subscription, before map[string]any, days int, adminTelegramID int64) *AuditLog {
	oldJSON, newJSON := diffState(before, subStateSnapshot(sub))

	details := auditSubDetails(sub)
	details["new_plan_id"] = sub.PlanID
	if days > 0 {
		details["days"] = days
	}

	return &AuditLog{
		EntityType: EntitySubscription,
		EntityID:   marshalAuditJSONString(formatSubID(sub)),
		Event:      AuditEventPlanChanged,
		ActorType:  ActorAdmin,
		ActorID:    marshalAuditJSONString(fmt.Sprintf("%d", adminTelegramID)),
		OldState:   oldJSON,
		NewState:   newJSON,
		Details:    marshalAuditDetails(details),
	}
}

// AuditSubscriptionExpired фиксирует истечение срока (worker).
func AuditSubscriptionExpired(sub *Subscription) *AuditLog {
	return &AuditLog{
		EntityType: EntitySubscription,
		EntityID:   marshalAuditJSONString(formatSubID(sub)),
		Event:      AuditEventSubscriptionExpired,
		ActorType:  ActorWorker,
		ActorID:    marshalAuditJSONString("subscription_expire_worker"),
		OldState:   marshalAuditJSON(subStateSnapshot(sub)),
		Details:    marshalAuditDetails(auditSubDetails(sub)),
	}
}

// AuditDowngradedToFree фиксирует понижение до free (чарджбэк или истечение).
func AuditDowngradedToFree(sub *Subscription, reason string) *AuditLog {
	details := auditSubDetails(sub)
	if reason != "" {
		details["reason"] = reason
	}

	return &AuditLog{
		EntityType: EntitySubscription,
		EntityID:   marshalAuditJSONString(formatSubID(sub)),
		Event:      AuditEventDowngradedToFree,
		ActorType:  ActorWorker,
		OldState:   marshalAuditJSON(subStateSnapshot(sub)),
		Details:    marshalAuditDetails(details),
	}
}

// AuditSubscriptionRevoked фиксирует отзыв доступа (orphan reconciliation).
func AuditSubscriptionRevoked(sub *Subscription, reason string) *AuditLog {
	details := auditSubDetails(sub)
	if reason != "" {
		details["reason"] = reason
	}

	return &AuditLog{
		EntityType: EntitySubscription,
		EntityID:   marshalAuditJSONString(formatSubID(sub)),
		Event:      AuditEventSubscriptionRevoked,
		ActorType:  ActorWorker,
		OldState:   marshalAuditJSON(subStateSnapshot(sub)),
		Details:    marshalAuditDetails(details),
	}
}

// AuditSubscriptionDeleted фиксирует физическое удаление подписки (admin /del).
// snapshot — состояние строки до удаления; история остаётся в журнале после
// удаления строки из subscriptions (FK нет).
func AuditSubscriptionDeleted(sub *Subscription, adminTelegramID int64) *AuditLog {
	return &AuditLog{
		EntityType: EntitySubscription,
		EntityID:   marshalAuditJSONString(formatSubID(sub)),
		Event:      AuditEventSubscriptionDeleted,
		ActorType:  ActorAdmin,
		ActorID:    marshalAuditJSONString(fmt.Sprintf("%d", adminTelegramID)),
		OldState:   marshalAuditJSON(subStateSnapshot(sub)),
		Details:    marshalAuditDetails(auditSubDetails(sub)),
	}
}

// AuditTrialDeleted фиксирует удаление истёкшей анонимной trial-подписки.
func AuditTrialDeleted(sub *Subscription) *AuditLog {
	return &AuditLog{
		EntityType: EntitySubscription,
		EntityID:   marshalAuditJSONString(formatSubID(sub)),
		Event:      AuditEventTrialDeleted,
		ActorType:  ActorWorker,
		ActorID:    marshalAuditJSONString("trial_cleanup_worker"),
		OldState:   marshalAuditJSON(subStateSnapshot(sub)),
		Details:    marshalAuditDetails(auditSubDetails(sub)),
	}
}

// --- Уведомления -----------------------------------------------------------

// AuditReminderSent фиксирует отправленное напоминание об истечении срока.
func AuditReminderSent(sub *Subscription, windowName string) *AuditLog {
	details := auditSubDetails(sub)
	details["window"] = windowName

	return &AuditLog{
		EntityType: EntitySubscription,
		EntityID:   marshalAuditJSONString(formatSubID(sub)),
		Event:      AuditEventReminderSent,
		ActorType:  ActorWorker,
		ActorID:    marshalAuditJSONString("subscription_reminder_worker"),
		Details:    marshalAuditDetails(details),
	}
}

// AuditTrafficNotified фиксирует отправленное трафик-уведомление.
// kind: 90% | exhausted | reenabled.
func AuditTrafficNotified(sub *Subscription, kind string) *AuditLog {
	details := auditSubDetails(sub)
	details["kind"] = kind

	return &AuditLog{
		EntityType: EntitySubscription,
		EntityID:   marshalAuditJSONString(formatSubID(sub)),
		Event:      AuditEventTrafficNotified,
		ActorType:  ActorWorker,
		ActorID:    marshalAuditJSONString("subscription_traffic_worker"),
		Details:    marshalAuditDetails(details),
	}
}

// --- Оплата ----------------------------------------------------------------

// AuditOrderCreated фиксирует создание заказа (платёжная ссылка выдана).
func AuditOrderCreated(order *Order) *AuditLog {
	details := map[string]any{
		"subscription_id": order.SubscriptionID,
		"product_id":      order.ProductID,
		"amount_cents":    order.AmountCents,
		"currency":        order.Currency,
		"provider":        order.PaymentProvider,
	}

	return &AuditLog{
		EntityType: EntityOrder,
		EntityID:   marshalAuditJSONString(fmt.Sprintf("%d", order.ID)),
		Event:      AuditEventOrderCreated,
		ActorType:  ActorUser,
		NewState:   marshalAuditJSON(map[string]any{"status": string(order.Status)}),
		Details:    marshalAuditDetails(details),
	}
}

// AuditPaymentConfirmed фиксирует подтверждение оплаты. Вызывается внутри
// транзакции ConfirmOrderPaidCAS — атомарно с изменением заказа и подписки.
func AuditPaymentConfirmed(order *Order, sub *Subscription, product *Product, newExpiry time.Time) *AuditLog {
	details := map[string]any{
		"order_id":        order.ID,
		"subscription_id": order.SubscriptionID,
		"amount_cents":    order.AmountCents,
		"currency":        order.Currency,
		"provider":        order.PaymentProvider,
	}
	if product != nil {
		details["product_id"] = product.ID
		details["product_name"] = product.Name
	}

	newState := map[string]any{"order_status": string(OrderStatusPaid)}
	if sub != nil {
		newState["plan_id"] = sub.PlanID
		newState["subscription_status"] = sub.Status
		newState["expires_at"] = newExpiry.UTC().Format(time.RFC3339)
	}

	return &AuditLog{
		EntityType: EntityOrder,
		EntityID:   marshalAuditJSONString(fmt.Sprintf("%d", order.ID)),
		Event:      AuditEventPaymentConfirmed,
		ActorType:  ActorPaymentProvider,
		ActorID:    marshalAuditJSONString(order.PaymentProvider),
		NewState:   marshalAuditJSON(newState),
		Details:    marshalAuditDetails(details),
	}
}

// AuditOrderExpired фиксирует истечение неоплаченного заказа.
func AuditOrderExpired(order *Order) *AuditLog {
	return &AuditLog{
		EntityType: EntityOrder,
		EntityID:   marshalAuditJSONString(fmt.Sprintf("%d", order.ID)),
		Event:      AuditEventOrderExpired,
		ActorType:  ActorWorker,
		OldState:   marshalAuditJSON(map[string]any{"status": string(OrderStatusPending)}),
		NewState:   marshalAuditJSON(map[string]any{"status": string(OrderStatusExpired)}),
		Details: marshalAuditDetails(map[string]any{
			"subscription_id": order.SubscriptionID,
			"product_id":      order.ProductID,
			"amount_cents":    order.AmountCents,
			"currency":        order.Currency,
		}),
	}
}

// AuditOrderCanceled фиксирует отмену заказа (user cancel или provider callback).
func AuditOrderCanceled(order *Order, actorType ActorType) *AuditLog {
	return &AuditLog{
		EntityType: EntityOrder,
		EntityID:   marshalAuditJSONString(fmt.Sprintf("%d", order.ID)),
		Event:      AuditEventOrderCanceled,
		ActorType:  actorType,
		OldState:   marshalAuditJSON(map[string]any{"status": string(order.Status)}),
		NewState:   marshalAuditJSON(map[string]any{"status": string(OrderStatusCanceled)}),
		Details: marshalAuditDetails(map[string]any{
			"subscription_id": order.SubscriptionID,
			"product_id":      order.ProductID,
			"amount_cents":    order.AmountCents,
			"currency":        order.Currency,
		}),
	}
}

// AuditChargebackReceived фиксирует чарджбэк от платёжного провайдера.
// downgraded — была ли подписка понижена до free в той же транзакции.
func AuditChargebackReceived(order *Order, sub *Subscription, downgraded bool) *AuditLog {
	details := map[string]any{
		"order_id":        order.ID,
		"subscription_id": order.SubscriptionID,
		"amount_cents":    order.AmountCents,
		"currency":        order.Currency,
		"provider":        order.PaymentProvider,
		"downgraded":      downgraded,
	}

	return &AuditLog{
		EntityType: EntityOrder,
		EntityID:   marshalAuditJSONString(fmt.Sprintf("%d", order.ID)),
		Event:      AuditEventChargebackReceived,
		ActorType:  ActorPaymentProvider,
		ActorID:    marshalAuditJSONString(order.PaymentProvider),
		OldState:   marshalAuditJSON(map[string]any{"order_status": string(OrderStatusPaid)}),
		NewState:   marshalAuditJSON(map[string]any{"order_status": string(OrderStatusCanceled)}),
		Details:    marshalAuditDetails(details),
	}
}

// AuditPaymentCreationUncertain фиксирует неопределённый исход запроса к
// платёжному провайдеру (повторный автозапрос небезопасен — требуется
// ручная сверка).
func AuditPaymentCreationUncertain(order *Order) *AuditLog {
	return &AuditLog{
		EntityType: EntityOrder,
		EntityID:   marshalAuditJSONString(fmt.Sprintf("%d", order.ID)),
		Event:      AuditEventPaymentCreationUncert,
		ActorType:  ActorSystem,
		Details: marshalAuditDetails(map[string]any{
			"subscription_id": order.SubscriptionID,
			"product_id":      order.ProductID,
			"amount_cents":    order.AmountCents,
			"currency":        order.Currency,
			"action":          "requires manual reconciliation",
		}),
	}
}

// --- Рассылки --------------------------------------------------------------

// AuditBroadcastStarted фиксирует старт рассылки (claim worker-а).
func AuditBroadcastStarted(b *Broadcast) *AuditLog {
	return &AuditLog{
		EntityType: EntityBroadcast,
		EntityID:   marshalAuditJSONString(fmt.Sprintf("%d", b.ID)),
		Event:      AuditEventBroadcastStarted,
		ActorType:  ActorSystem,
		NewState:   marshalAuditJSON(map[string]any{"status": string(BroadcastStatusRunning)}),
		Details: marshalAuditDetails(map[string]any{
			"name":             b.Name,
			"recipients_total": b.RecipientsTotal,
		}),
	}
}

// AuditBroadcastCompleted фиксирует успешное завершение рассылки со сводкой.
func AuditBroadcastCompleted(b *Broadcast) *AuditLog {
	return &AuditLog{
		EntityType: EntityBroadcast,
		EntityID:   marshalAuditJSONString(fmt.Sprintf("%d", b.ID)),
		Event:      AuditEventBroadcastCompleted,
		ActorType:  ActorSystem,
		NewState:   marshalAuditJSON(map[string]any{"status": string(BroadcastStatusCompleted)}),
		Details: marshalAuditDetails(map[string]any{
			"name":              b.Name,
			"recipients_total":  b.RecipientsTotal,
			"sent_count":        b.SentCount,
			"blocked_count":     b.BlockedCount,
			"unreachable_count": b.UnreachableCount,
			"failed_count":      b.FailedCount,
		}),
	}
}

// AuditBroadcastFailed фиксирует провал рассылки.
func AuditBroadcastFailed(b *Broadcast, cause string) *AuditLog {
	details := map[string]any{
		"name":         b.Name,
		"last_error":   b.LastError,
		"retry_count":  b.RetryCount,
		"sent_count":   b.SentCount,
		"failed_count": b.FailedCount,
	}
	if cause != "" {
		details["cause"] = cause
	}

	return &AuditLog{
		EntityType: EntityBroadcast,
		EntityID:   marshalAuditJSONString(fmt.Sprintf("%d", b.ID)),
		Event:      AuditEventBroadcastFailed,
		ActorType:  ActorSystem,
		NewState:   marshalAuditJSON(map[string]any{"status": string(BroadcastStatusFailed)}),
		Details:    marshalAuditDetails(details),
	}
}

// AuditBroadcastCanceled фиксирует отмену рассылки администратором.
func AuditBroadcastCanceled(b *Broadcast, adminTelegramID int64) *AuditLog {
	return &AuditLog{
		EntityType: EntityBroadcast,
		EntityID:   marshalAuditJSONString(fmt.Sprintf("%d", b.ID)),
		Event:      AuditEventBroadcastCanceled,
		ActorType:  ActorAdmin,
		ActorID:    marshalAuditJSONString(fmt.Sprintf("%d", adminTelegramID)),
		NewState:   marshalAuditJSON(map[string]any{"status": string(BroadcastStatusCanceled)}),
		Details:    marshalAuditDetails(map[string]any{"name": b.Name}),
	}
}

// --- Технические агрегаты ---------------------------------------------------

// AuditSyncRunDegraded фиксирует прогон SyncPendingNodes с частичными
// отказами (сводка за прогон, не per-item события).
func AuditSyncRunDegraded(failedSubs int, err error) *AuditLog {
	details := map[string]any{"failed_subscriptions": failedSubs}
	if err != nil {
		details["error"] = err.Error()
	}

	return &AuditLog{
		EntityType: EntitySystem,
		Event:      AuditEventSyncRunDegraded,
		ActorType:  ActorWorker,
		ActorID:    marshalAuditJSONString("subscription_sync_worker"),
		Details:    marshalAuditDetails(details),
	}
}

// AuditOrphanedClientsRevoked фиксирует сводку orphan-ревокий за прогон.
func AuditOrphanedClientsRevoked(revokedCount int) *AuditLog {
	return &AuditLog{
		EntityType: EntitySystem,
		Event:      AuditEventOrphanedClientsRevoked,
		ActorType:  ActorWorker,
		ActorID:    marshalAuditJSONString("orphan_reconciler"),
		Details:    marshalAuditDetails(map[string]any{"revoked_count": revokedCount}),
	}
}

// auditActorTelegram возвращает actor_id из подписки (nil для анонимных trial).
func auditActorTelegram(sub *Subscription) *string {
	if sub == nil || sub.TelegramID <= 0 {
		return nil
	}

	return marshalAuditJSONString(fmt.Sprintf("%d", sub.TelegramID))
}
