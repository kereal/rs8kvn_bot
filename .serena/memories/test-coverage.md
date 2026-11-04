# Test Coverage Analysis — rs8kvn_bot

**Последняя проверка:** 2026-11-02
**Метод:** `go test -coverprofile=/tmp/coverage.out ./...` и `go tool cover -func=/tmp/coverage.out`
**Общее покрытие:** 70.2% (2026-11-02, после чистки набора — см. секцию ниже)

## Чистка тестового набора (2026-11-02, dev, 8 коммитов d5d162c..754d052)

Аудит 110 тест-файлов / 1455 тест-функций. Итог: −5520 строк нетто, 1455→1212 Test-функций,
покрытие 70.2%, race/lint 0. Ключевые решения (НЕ откатывать без причины):
- Удалены межпакетные клоны: internal/bot/database_test.go (копия internal/database), admin_test.go
  (integration-тег; TTL-тест перенесён в broadcast_admin_flow_test без тега, split-message покрыт
  split_message_test).
- subserver: мёртвый прод ConvertJSONToShareLinks жил в _test.go (не компилировался в бинарь) — удалён
  вместе с 9 обёртками и base64StdEncode; DetectFormat-золотые слиты в таблицу; hits/misses-атомик в
  cache.go мёртвый (метрики CacheHits/MissesTotal отдельные).
- xui: PanelContract_* — канон контрактов v3.11.0; старым Success/not-found тестам предпочтение не отдавать.
- web middleware: мёртвая ветка expectedToken=="" удалена; health map-echo (SetReady/RegisterChecker) удалены.
- e2e удалены как unit-дубли: cache_test.go, edge_cases_test.go, health_test.go целиком; AutoRelogin_On401
  (механизма auto-relogin в базе нет — только классификатор bot/errors.go).
- scheduler: fakeRepo удалён → testutil.NewSubscriptionRepositoryFake (ВАЖНО: структурный литерал фейка
  даёт nil-embedded ServiceStoreFake → SIGSEGV, строить только конструктором).
- config: constants_test схлопнут в TestConstants_UncoveredInvariants; Config.String маскирует токен —
  тест проверяет РЕАЛЬНОЕ значение.
- Новые guard'ы: admin_del_test.go (/del — единственный продуктовый путь удаления), deactivate
  prompt/exec колбэки, broadcastSessionTTL, IntervalGate (utils/gate_test.go), RequestPayment
  повторный линк (защита от двойного списания), ClaimReminder/ReleaseReminder (нет двойного письма
  после продления), fetch-node URL, AllowInsecure render, expire sec/ms.
- Флаки устранены: ConcurrentMissesShareOneFetch (release-барьер вместо sleep-окна),
  SavesRedirectPaymentDetails (без wall-clock допуска).

**Последняя проверка (историческая):** 2026-08-21

Пакетные значения ниже — исторический снимок от 2026-06-26, а не актуальный CI-отчёт. Для текущих значений всегда использовать команду выше.

## По пакетам (оценка)

| Пакет | Покрытие | Статус |
|---|---|---|
| internal/flag | ~97% | 🟢 |
| internal/heartbeat | ~92% | 🟢 |
| internal/ratelimiter | ~90% | 🟢 |
| internal/xui | ~85% | 🟢 |
| internal/bot | ~82% | 🟢 |
| internal/scheduler | ~80% | 🟢 |
| internal/web | ~78% | 🟡 |
| internal/config | ~78% | 🟡 |
| internal/utils | ~70% | 🟡 |
| internal/logger | ~68% | 🟡 |
| internal/service | ~70% | 🟢 |
| internal/database | ~55% | 🟡 |
| internal/vpn | ~65% | 🟢 |
| internal/metrics | ~35% | 🔴 |
| internal/subserver | ~30% | 🔴 |
| internal/testutil | 0% | ⚪ (ожидаемо) |

## Файлы с низким покрытием
- internal/database/subscription_nodes.go — частично покрыт sync.go тестами
- internal/service/sync.go — ~50% (нужны тесты на ReconcilePlanNodes, handleSyncError)
- internal/vpn/client.go — ~65% (ThreeXUIClient, classify errors)
- internal/utils/markdown.go — EscapeMarkdown

## Приоритеты улучшения

### P1 — Высокое
1. service/sync.go — непокрытые пути: ReconcilePlanNodes edge cases, handleSyncError
2. vpn/client.go — classify ошибок, retryUnavailableNode

### P2 — Среднее
3. database/subscription_nodes.go — UpsertSubscriptionNode, GetPendingSync edge cases
4. database/trials.go — BindTrialSubscription race-condition flows

### P3 — Низкое
5. utils/markdown.go — EscapeMarkdown

## Количество тестовых файлов

Количество тестовых файлов меняется вместе с кодом; не использовать историческое число ниже как текущую метрику.

## Добавленные тесты (2026-06-26, ветка test/add-coverage-p1-p2)

### P1 — Critical (0% → покрыто)
- `internal/database/orders_test.go` — 13 тестов: CRUD orders, status transitions, paid/activated updates
- `internal/database/products_test.go` — 6 тестов: GetActiveByPlanID (active/inactive product/plan), GetProductByID
- `internal/subserver/subscription_handler_test.go` — HandleSubscription (cache hit/miss/invalidated, Xray JSON response, multiple nodes, no URL, fetch error), UpdateDevices, UpdateIPs, helper functions (ParseUserInfoValue, ParseExpireFromUserInfo, BuildUserInfoHeader, FilterHeaders, SkipTransportHeader, ResponseHeaders, DetectFormat, isValidServer)
- `internal/subserver/xray_subscription_test.go` — контракт ответа `/sub`: JSON/Clash/share-link/base64 источники → JSON-массив полных Xray-конфигов, один профиль на ноду (тест `TestBuildResponseOneProfilePerNode` проверяет, что 3 ноды дают 3 профиля с разными address), `remarks` = тег ноды, catch-all на СОБСТВЕННЫЙ тег профиля, inbounds socks+http, DNS + routing правила, отказ при отсутствии конвертируемых источников, `parseShareLink` (VLESS Reality/Trojan/Hysteria2 + malformed), `vmess://` со строковыми и числовыми `port`/`aid`, `TestTagProxyOutboundsReservesDirectAndBlock` + `TestBuildResponseRemarksCollidingWithReservedTags` (ноды с remark `direct`/`block` получают запасной тег, а freedom/blackhole и правила маршрутизации остаются нетронутыми; падали до фикса `reservedOutboundTags`), `TestParseVMessLinkPreservesCase` (EqualFold по схеме `VMESS://`, payload не lower-cased), `TestBuildResponseReservesDirectAndBlockTags`

## Добавленные тесты (2026-08, Subscription Traffic Worker)
- `internal/xui/client_enable_test.go` — `TestUpdateClient_EnableTrue`/`TestUpdateClient_EnableFalse` (обновление с `Enable` флагом, mock inbound-flow lookup через `get/`), `TestGetClientTraffic_DecodesEnable` (декодирование `Enable` из ответа панели).
- `internal/database/subscription_traffic_test.go` — `TestTrafficReminder_ClaimAndRelease` (первый claim успешен, повторный no-op, разные биты сосуществуют, release позволяет повторный claim), `ClaimNotFound`/`ReleaseNotFound` (`ErrSubscriptionNotFound`), `TestGetActiveSubscriptionsWithTrafficLimit` (активная/revoked/unlimited-подписки — только активная на лимитном плане), `..._NoLimited` (пусто при отсутствии лимитных).
- `internal/service/subscription_traffic_notifications_test.go` — `NinetyPercent` (95% лимита, включён → отправлено + кнопка `buy_premium_list`), `ExhaustedDisabled` (≥100%, выключен → отправлено + кнопка, клиент НЕ ре-eнаблится), `ResetAndReenable` (сброс + выключен → `UpdateClient(Enable=true)` + сообщение без кнопки Premium), `SkippedWhenBelow90` (нет уведомления), `NilSqlNoop` (nil sub / telegram_id<=0 noop), `DedupClaimedBit` (claim=false → тихий no-op), `SendErrorReleasesClaim` (сбой Send → claim освобождён для повтора, ошибка наружу), `PlanWithoutLimitNoop` (traffic_limit=0 → не трогаем), `NoNodesRespondNoop` (все ноды недоступны → тихо), `ReenableUpdateErrorNoNotification` (сбой `UpdateClient` при reactivate → reset-сообщение НЕ отправляется). helper `hasPremiumButton` проверяет наличие колбэка `buy_premium_list` в ReplyMarkup; `trafficNotifySetup` возвращает mock БД/xui/бот для точечной подмены поведения.
- `internal/scheduler/subscription_traffic_worker_test.go` — `ProcessIteratesTargets` (обрабатываются только telegram_id>0; trial с отрицательным ID пропускается), `ProcessContinuesOnError` (не паникует, сканирование не прерывается), `ProcessStopOnRepoError` (не вызывается процесс при сбое выборки).
- Миграционные тесты `internal/database/migration_test.go` обновлены под 038: `TestMigration_032_NormalizesInvalidRetryState` и `TestMigration_033_NormalizesInvalidStatuses` переведены с позиционных `Steps(-N)` на абсолютные `Migrate(31)`/`Migrate(32)`/`Migrate(33)` (из-за отсутствия миграции 037 сместили позиционность).

### P2 — High (partial → improved)
- `internal/database/subscriptions_extra_test.go` — 14 тестов: GetSubscriptionStatus, GetWithPlanAndNodes, UpdateDevices, UpdateIPs, ExpireSubscription, GetExpiredPaidSubscriptions, GetSubscription
- `internal/subserver/servers_extra_test.go` — 16 тестов: ExtractJSONConfigs, ConvertSingleJSONToLink (VLESS/Trojan/SS/SOCKS/Hysteria2/TUIC/unsupported/invalid), toServerConfig alias normalisation (address/port/uuid/remark), truncateString

### Примечания
- GORM пропускает zero-value bool при Create — нужно `.Update("is_active", false)` после Create
- MockDatabaseService.UpdateIPs не проверяет UpdateIPsFunc — verify через side effect на subFull
- http.Header канонизирует ключи — "profile-title" → "Profile-Title"
- orders имеет partial UNIQUE index на (payment_provider, provider_payment_id) WHERE provider_payment_id IS NOT NULL
- (2026-08-11) Тесты `GetActiveByPlanID`, `CreateOrder`, `GetOrdersBySubscriptionID` удалены вместе с мёртвыми методами (см. orders-table.md)

## Добавленные тесты (2026-07-17, метрики)
- `internal/metrics/metrics_test.go` — проверка инициализации всех метрик, smoke-test endpoint `/metrics`
- `internal/metrics/db_test.go` — 4 теста: GORM callbacks для create/query/update/delete

## Добавленные тесты (2026-08-26, ветка feat/broadcast-campaigns)
- `internal/bot/broadcast_admin_flow_test.go` — parseBroadcastCallbackID (0/overflow/нечисловой), callbacks broadcast_cancel_/broadcast_retry_ (+не-админ), handleBroadcastFilter (plan/status/date/inactive/ever_paid + toggle off), handleBroadcastBackToFilters, форматтеры truncateRunes/formatIDList/formatErrorList
- `internal/bot/admin_broadcast_test.go` (+flood) — TestBroadcastFloodClassificationAndRetryDelay (распознавание 429, уважение/каппинг `retry_after`), TestBroadcast_FloodWaitsRetryAfterThenDelivers (429 не расходует обычные попытки → sent)
- `internal/xui/reset_traffic_test.go` — ResetTraffic: пустой email, успех (метод/путь/экранирование), Success:false с msg панели, невалидный JSON, не-2xx
- `internal/service/sync_test.go` — processPendingUpdate вызывает ResetTraffic при смене тарифа; сбой ResetTraffic — best-effort (нода всё равно active)
- `internal/service/subscription_test.go` — BindTrial rollback при DB-bind failure (rollback и conservative skip), CleanupExpiredTrials deprovision-failure оставляет строку, ReconcileOrphanedClients пересоздаёт node queue
