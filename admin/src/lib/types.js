// Типы ответов admin API — только JSDoc-typedef'ы, рантайма у модуля нет
// (export {} делает файл ES-модулем, типы доступны как import('./types.js').Plan).
//
// Источник истины по набору ключей каждой строки — ожидания в
// admin/contract_test.go (TestAPI_Contract_KeySets): изменение ключа требует
// осознанной правки и здесь, и там. Типы значений — из json-тегов DTO в
// admin/*.go: time.Time -> string (RFC3339), *time.Time -> string|null,
// json.RawMessage -> any (произвольный JSON).
//
// Аннотирование контейнера строк в view (например
// `/** @type {import('../lib/types.js').Plan[]} */ let items = $state([])`)
// делает опечатку row.total_count ошибкой svelte-check: checkJs не проверяет
// имена полей у нетипизированных объектов, а класс двух реальных багов панели —
// именно тихие опечатки в имени поля.

/** Конверт любого списочного эндпоинта (кроме планов: у них total нет). @template T @typedef {{items: T[], total: number}} ListEnvelope */

/**
 * GET /admin/api/plans — строка списка планов (admin/plans.go planItem).
 * @typedef {object} Plan
 * @property {number} id
 * @property {string} name
 * @property {boolean} is_active
 * @property {number} devices_limit
 * @property {number} traffic_limit
 * @property {number} products_count
 * @property {number} subscriptions_count
 * @property {string} created_at
 * @property {string} updated_at
 */

/** Конверт GET /admin/api/plans: total не отдаётся (admin/plans.go). @typedef {{items: Plan[]}} PlanListEnvelope */

/**
 * GET /admin/api/trials — последние триал-запросы (admin/trials.go).
 * @typedef {{id: number, ip: string, created_at: string}} TrialRequestItem
 */
/**
 * @typedef {{items: TrialRequestItem[], total: number}} TrialRequestsResponse
 */
/**
 * @typedef {{requests_24h: number, unique_ips_24h: number, top_ips: Array<{ip: string, count: number}>}} TrialRequestsStats
 */

/**
 * GET /admin/api/nodes/{id}/live — живой опрос панели ноды (admin/nodes.go).
 * @typedef {{node_id: number, available: boolean, ping_latency_ms: number, ping_error?: string, clients: number, disabled: number, traffic_bytes: number, last_online: number}} NodeLiveResponse
 */

/**
 * GET /admin/api/broadcasts/{id} — разобранный delivery_report (admin/broadcasts.go).
 * @typedef {{telegram_id: number, error: string}} BroadcastSendError
 */

/**
 * GET /admin/api/revenue?days=N[&currency=RUB] — агрегат выручки (admin/revenue.go).
 * days — ровно N UTC-дат от старых к новым, последний = сегодня, пустые дни нулями.
 * Все суммы, число оплат и средний чек относятся строго к одной валюте
 * currency и валюты не смешивают. canceled/expired — общие операционные
 * счётчики за период. currency — запрошенная валюта, либо авто-выбор сервера
 * (RUB, если среди оплаченных есть RUB, иначе первая по алфавиту), либо ""
 * когда валюта не запрошена и оплаченных заказов нет.
 * currencies — все валюты оплат и возвратов периода независимо от выбора.
 * revenue_cents — прежнее имя gross (равен gross_revenue_cents); gross —
 * оплаты периода, refunded — возвраты/чарджбэки, случившиеся в периоде,
 * net = gross - refunded (может быть отрицательным). refund_count — все
 * возвраты, chargeback_count — сколько из них chargeback_received.
 * @typedef {object} RevenueResponse
 * @property {{date: string, orders: number, revenue_cents: number}[]} days
 * @property {{paid_orders: number, revenue_cents: number, gross_revenue_cents: number, avg_check_cents: number, canceled: number, expired: number, refunded_cents: number, net_revenue_cents: number, refund_count: number, chargeback_count: number}} totals
 * @property {string} currency — валюта выборки (см. выше)
 * @property {string[]} currencies — валюты оплат и возвратов периода
 */

/**
 * Финансовый возврат по заказу из журнала аудита (admin/orders.go orderRefund).
 * downgraded — подписка в той же транзакции понижена до free.
 * @typedef {object} OrderRefund
 * @property {string} event — payment_refunded | payment_auto_refunded | chargeback_received
 * @property {number} amount_cents
 * @property {string} currency
 * @property {boolean} downgraded
 * @property {string} actor_type
 * @property {string} created_at
 */

/**
 * GET /admin/api/nodes — строка списка нод (admin/nodes.go nodeItem).
 * @typedef {object} Node
 * @property {number} id
 * @property {string} name
 * @property {string} type
 * @property {string} host
 * @property {string} subscription_url
 * @property {number[]} inbound_ids
 * @property {boolean} is_active
 * @property {boolean} has_api_token
 * @property {number} plans_count
 * @property {number} subscriptions_count
 * @property {string} created_at
 * @property {string} updated_at
 */

/**
 * GET /admin/api/products — строка списка продуктов (admin/products.go productItem).
 * @typedef {object} Product
 * @property {number} id
 * @property {number} plan_id
 * @property {string} plan_name
 * @property {string} name
 * @property {number} duration_days
 * @property {number} price_cents
 * @property {string} currency
 * @property {boolean} is_active
 * @property {number} subscriptions_count
 * @property {number} orders_count
 * @property {string} created_at
 * @property {string} updated_at
 */

/**
 * GET /admin/api/invites — строка списка инвайтов (admin/invites.go inviteItem).
 * @typedef {object} Invite
 * @property {string} code
 * @property {number} referrer_tg_id
 * @property {number} referred_count
 * @property {number} subscriptions_count
 * @property {string} created_at
 */

/**
 * GET /admin/api/orders — строка списка заказов (admin/orders.go orderItem).
 * @typedef {object} Order
 * @property {number} id
 * @property {number} subscription_id
 * @property {number} telegram_id
 * @property {string} plan_name
 * @property {string} product_name
 * @property {number} duration_days
 * @property {string} status
 * @property {number} amount_cents
 * @property {string} currency
 * @property {string} payment_provider
 * @property {string} provider_payment_id
 * @property {string} created_at
 * @property {string|null} paid_at
 * @property {string|null} activated_at
 * @property {boolean} payment_creation_uncertain
 * @property {number|null} callback_amount_cents
 * @property {number|null} provider_fee_cents
 */

/**
 * GET /admin/api/orders/{id} — деталь заказа: список плюс тяжёлые поля
 * (admin/orders.go orderDetail, embedding orderItem).
 * @typedef {object} OrderDetail
 * @property {number} id
 * @property {number} subscription_id
 * @property {number} telegram_id
 * @property {string} plan_name
 * @property {string} product_name
 * @property {number} duration_days
 * @property {string} status
 * @property {number} amount_cents
 * @property {string} currency
 * @property {string} payment_provider
 * @property {string} provider_payment_id
 * @property {string} created_at
 * @property {string|null} paid_at
 * @property {string|null} activated_at
 * @property {boolean} payment_creation_uncertain
 * @property {number|null} callback_amount_cents
 * @property {number|null} provider_fee_cents
 * @property {number} product_id
 * @property {string|null} expires_at
 * @property {string|null} payment_expires_at
 * @property {string} payment_url
 * @property {OrderRefund[]} refunds
 */

/**
 * GET /admin/api/broadcasts — строка списка рассылок (admin/broadcasts.go broadcastItem).
 * @typedef {object} Broadcast
 * @property {number} id
 * @property {string} name
 * @property {string} status
 * @property {string|null} planned_at
 * @property {string|null} started_at
 * @property {string|null} finished_at
 * @property {number} recipients_total
 * @property {number} sent_count
 * @property {number} blocked_count
 * @property {number} unreachable_count
 * @property {number} failed_count
 * @property {string} last_error
 * @property {string|null} retry_at
 * @property {string} created_at
 */

/**
 * GET /admin/api/broadcasts/{id} — деталь рассылки: список плюс текст, фильтры
 * и превью ошибок доставки (admin/broadcasts.go broadcastDetail). snapshot_taken
 * обязан присутствовать даже когда false — на нём бейдж (contract_test.go).
 * @typedef {object} BroadcastDetail
 * @property {number} id
 * @property {string} name
 * @property {string} status
 * @property {string|null} planned_at
 * @property {string|null} started_at
 * @property {string|null} finished_at
 * @property {number} recipients_total
 * @property {number} sent_count
 * @property {number} blocked_count
 * @property {number} unreachable_count
 * @property {number} failed_count
 * @property {string} last_error
 * @property {string|null} retry_at
 * @property {string} created_at
 * @property {any} filters
 * @property {string} filters_text
 * @property {string} message_text
 * @property {number} retry_count
 * @property {boolean} snapshot_taken
 * @property {{telegram_id: number, error: string}[]} errors_preview
 */

/**
 * POST /admin/api/broadcasts/preview — фрагмент сообщения, разобранный так же,
 * как его отрисует Telegram (internal/utils ParseMarkdownV2). type "pre" —
 * блок кода, url непустой — кликабельная ссылка.
 * @typedef {object} MarkdownRun
 * @property {'text'|'pre'} type
 * @property {string} text
 * @property {string} url
 * @property {boolean} bold
 * @property {boolean} italic
 * @property {boolean} strike
 * @property {boolean} code
 */

/**
 * POST /admin/api/broadcasts/preview — предпросмотр перед отправкой:
 * точный размер аудитории, описание фильтра, расписание и текст сообщения.
 * @typedef {object} BroadcastPreview
 * @property {number} audience_count
 * @property {string} filters_text
 * @property {string|null} planned_at
 * @property {MarkdownRun[]} message_runs
 */

/**
 * GET /admin/api/audit — строка журнала аудита (admin/audit.go auditItem).
 * @typedef {object} AuditEntry
 * @property {number} id
 * @property {string} entity_type
 * @property {string|null} entity_id
 * @property {string} event
 * @property {string} actor_type
 * @property {string|null} actor_id
 * @property {string} created_at
 * @property {any} details
 */

/**
 * GET /admin/api/audit/{id} — раскрытая строка: плюс старое/новое состояние
 * (admin/audit.go auditDetail).
 * @typedef {object} AuditDetail
 * @property {number} id
 * @property {string} entity_type
 * @property {string|null} entity_id
 * @property {string} event
 * @property {string} actor_type
 * @property {string|null} actor_id
 * @property {string} created_at
 * @property {any} details
 * @property {any} old_state
 * @property {any} new_state
 */

/** GET /admin/api/audit/events — реестр значений фильтров (admin/audit.go). @typedef {{events: string[], entity_types: string[], actor_types: string[]}} AuditRegistry */

/**
 * GET /admin/api/sync-queue — одна пара подписка↔нода в очереди
 * (admin/sync_queue.go syncQueueItem). last_error обрезан сервером (300
 * символов) и может быть null.
 * @typedef {object} SyncQueueRow
 * @property {number} subscription_id
 * @property {number} telegram_id
 * @property {string} subscription_id_public
 * @property {number} node_id
 * @property {string} node_name
 * @property {string} status — pending_add | pending_update | pending_remove
 * @property {number} retry_count
 * @property {string|null} retry_at
 * @property {string|null} last_error
 * @property {string} updated_at
 */

/**
 * GET /admin/api/subscriptions — строка списка подписок
 * (admin/subscriptions.go subscriptionItem).
 * @typedef {object} Subscription
 * @property {number} id
 * @property {number} telegram_id
 * @property {string} username
 * @property {string} subscription_id
 * @property {string} status
 * @property {number|null} plan_id
 * @property {string} plan_name
 * @property {string|null} expires_at
 * @property {string|null} last_request
 * @property {number} nodes_active
 * @property {number} nodes_total
 * @property {string} created_at
 * @property {string} updated_at
 */

/**
 * Привязка подписки к ноде — строка таблицы нод в детали подписки
 * (admin/subscriptions.go subscriptionNode).
 * @typedef {object} SubscriptionNode
 * @property {number} node_id
 * @property {string} node_name
 * @property {string} status
 * @property {string|null} last_error
 */

/**
 * GET /admin/api/subscriptions/{id} — деталь подписки: список плюс тяжёлые
 * поля и привязки к нодам (admin/subscriptions.go subscriptionDetail).
 * devices/ips — предвалидированный JSON из web-сервера, приходит как есть.
 * @typedef {object} SubscriptionDetail
 * @property {number} id
 * @property {number} telegram_id
 * @property {string} username
 * @property {string} subscription_id
 * @property {string} status
 * @property {number|null} plan_id
 * @property {string} plan_name
 * @property {string|null} expires_at
 * @property {string|null} last_request
 * @property {number} nodes_active
 * @property {number} nodes_total
 * @property {string} created_at
 * @property {string} updated_at
 * @property {any} devices
 * @property {any} ips
 * @property {number|null} product_id
 * @property {number|null} duration_days
 * @property {number} price_paid_cents
 * @property {string} currency
 * @property {string|null} started_at
 * @property {number} reminders_sent
 * @property {number} traffic_reminders_sent
 * @property {number|null} referred_by
 * @property {string} invite_code
 * @property {string} sub_url
 * @property {SubscriptionNode[]} nodes
 * @property {number} delivered_count
 * @property {number[]} blocked
 * @property {number[]} unreachable
 * @property {BroadcastSendError[]} errors
 */

/**
 * POST /admin/api/subscriptions/{id}/sync — detail подписки плюс warning,
 * который появляется только при частичном сбое (admin/subscriptions_sync.go).
 * @typedef {object} SyncResponse
 * @property {SubscriptionDetail|null} subscription
 * @property {string} [warning]
 */

/** POST /admin/api/subscriptions/{id}/reset-traffic (admin/subscriptions.go). @typedef {{reset_nodes: number, failed_nodes?: number, warning?: string}} ResetTrafficResponse */

/** DELETE /admin/api/subscriptions/{id} (admin/subscriptions.go). @typedef {{id: number, deleted: boolean}} DeleteResponse */

/**
 * GET /admin/api/subscriptions/{id}/traffic — живой трафик для карточки
 * деталей (admin/subscriptions.go subscriptionTraffic, service.TrafficInfo).
 * Отдельный ленивый запрос: список подписок его не вызывает.
 * @typedef {object} TrafficResponse
 * @property {number} used_gb
 * @property {number} limit_gb
 * @property {number} percentage
 * @property {boolean} unlimited — план без лимита, узлы не опрашивались
 * @property {boolean} available — false: ни одна нода не ответила
 * @property {string} reset_info
 * @property {string} plan_name
 */

/**
 * GET /admin/api/overview — снимок «Состояния». Наборы ключей блоков пинует
 * сабтест "overview blocks" в admin/contract_test.go.
 * @typedef {object} Overview
 * @property {{version: string, commit: string, build_time: string, uptime_seconds: number}} build
 * @property {{path: string, size_bytes: number}} database
 * @property {{total: number, by_status: {[status: string]: number}}} subscriptions
 * @property {{total: number}} nodes
 * @property {{total: number, by_status: {[status: string]: number}, pending_total: number, oldest_pending_at: string|null}} bindings
 * @property {{total: number, by_status: {[status: string]: number}, uncertain: number}} orders
 * @property {{total: number, by_status: {[status: string]: number}}} broadcasts
 * @property {{events_24h: number}} audit
 * @property {string|null} last_sub_request
 * @property {{total: number, days: Array<{date: string, count: number}>}} expiring
 */

/** POST /admin/api/nodes/{id}/health — ошибка сети это available:false, не протокол (admin/nodes.go nodeHealthResult). @typedef {{available: boolean, latency_ms: number, error?: string, checked_at: string}} NodeHealth */

/** GET/PUT /admin/api/plans/{id}/nodes — набор привязанных к плану нод (admin/nodes.go). @typedef {{node_ids: number[]}} PlanNodeIDs */

/** GET /admin/api/broadcasts/audience — предпросмотр размера аудитории (admin/broadcasts.go). @typedef {{count: number}} AudienceCount */

/** POST /admin/api/broadcasts/{id}/deactivate-blocked (admin/broadcasts.go, service.DeactivationResult). @typedef {{targets: number, revoked: number, failed: number, error?: string}} DeactivateResult */

export {}
