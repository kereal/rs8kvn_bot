# Test Coverage Analysis — rs8kvn_bot

**Последняя проверка:** 2026-11-06
**Метод:** `go test -coverprofile=/tmp/coverage.out ./...` и `go tool cover -func=/tmp/coverage.out`
**Общее покрытие:** 70.8% statements (2026-11-06)
**Структура набора (2026-11-06):** 139 test-файлов, 1367 `func Test*` (плюс 15 `TestMain`), 25 пакетов (`go list ./...`), 22 пакета с тестами (остальные — `internal/testutil`, `tools/adminserve`, `internal/subserver/benchmarks`).

## Чистка тестового набора (2026-11-02, dev, 8 коммитов d5d162c..754d052)

Аудит 110 тест-файлов / 1455 тест-функций. Итог: −5520 строк нетто, 1455→1212 Test-функций,
покрытие 70.2%, race/lint 0. Ключевые решения (НЕ откатывать без причины):
- Удалены межпакетные клоны: клон internal/bot/database_test.go (копия internal/database;
  канонический файл — internal/database/database_test.go), admin_test.go
  (integration-тег; split-message покрыт split_message_test).
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
  после продления), fetch-node URL, AllowInsecure render (позже заменён на
  NoAllowInsecure-гварды — см. AGENTS.md), expire sec/ms.
  (Guard broadcastSessionTTL удалён вместе с бот-флоу рассылок — управление
  рассылками теперь через admin-панель, воркер доставки остался в internal/bot.)
- Флаки устранены: ConcurrentMissesShareOneFetch (release-барьер вместо sleep-окна),
  SavesRedirectPaymentDetails (без wall-clock допуска).

**Последняя проверка (историческая):** 2026-08-21

## По пакетам (замер 2026-11-06, `go test ./... -count=1 -coverprofile`)

| Пакет | Покрытие |
|---|---|
| internal/web | 89.3% |
| internal/xui | 91.2% |
| internal/ratelimiter | 92.6% |
| internal/utils | 86.1% |
| internal/config | 89.8% |
| internal/flag | 84.5% |
| internal/heartbeat | 83.3% |
| internal/vpn | 83.5% |
| internal/subserver | 80.4% |
| internal/service | 80.1% |
| internal/backup | 79.9% |
| admin | 78.9% |
| internal/scheduler | 77.7% |
| internal/metrics | 77.1% |
| internal/service/payment/platega | 76.0% |
| internal/database | 74.2% |
| internal/bot | 69.4% |
| internal/logger | 60.5% |
| cmd/bot | 16.4% |
| internal/testutil | 0.0% (фейки, тестов нет) |

tests/e2e — интеграционные тесты без unit-покрытия statements.

## Файлы с наименьшим покрытием (замер 2026-11-06, файлы ≥ 50 statements)

| Файл | Покрытие | Замечание |
|---|---|---|
| `cmd/bot/lifecycle.go` | 0.0% | lifecycle-обёртки, косвенно прикрыты e2e |
| `internal/bot/broadcast_worker.go` | 0.3% | воркер доставки рассылок — крупнейший пробел (296 statements) |
| `cmd/bot/main.go` | 13.8% | сборка графа и запуск воркеров; проверяется e2e/смоуком |
| `internal/subserver/access_log.go` | 33.3% | опциональный access log |
| `internal/database/models.go` | 55.2% | GORM-модели |
| `internal/service/payment/platega/callback.go` | 58.6% | разбор колбэка провайдера |
| `internal/logger/logger.go` | 60.5% | zap setup |
| `internal/database/subscription_nodes.go` | 62.7% | state-machine биндингов, покрыта через sync-тесты |
| `internal/database/trials.go` | 65.9% | bind/cleanup trial |
| `internal/bot/callback.go` | 66.1% | роутер callback'ов |

`internal/testutil` и `tools/adminserve` собственных тестов не имеют (0%) — так и задумано.

## Приоритеты улучшения (2026-11-06)

1. `internal/bot/broadcast_worker.go` — ретраи, flood-обработка, resume кампаний после краха (реальный прод-путь, покрыт почти нулём).
2. `cmd/bot/lifecycle.go` + `cmd/bot/main.go` — сборка графа и порядок запуска воркеров.
3. `internal/service/payment/platega/callback.go` — идемпотентность и разбор ответа провайдера.

## Добавленные тесты (2026-06-26, ветка test/add-coverage-p1-p2)

### P1 — Critical (0% → покрыто)
- `internal/database/orders_test.go` — 13 тестов: CRUD orders, status transitions, paid/activated updates
- `internal/database/products_test.go` — 6 тестов: GetActiveByPlanID (active/inactive product/plan), GetProductByID
- `internal/subserver/subscription_handler_test.go` — HandleSubscription (cache hit/miss/invalidated, Xray JSON response, multiple nodes, no URL, fetch error), UpdateDevices, UpdateIPs, helper functions (ParseUserInfoValue, ParseExpireFromUserInfo, BuildUserInfoHeader, FilterHeaders, SkipTransportHeader, ResponseHeaders, DetectFormat, isValidServer)
- `internal/subserver/xray_subscription_test.go` — контракт ответа `/sub`: JSON/Clash/share-link/base64 источники → JSON-массив полных Xray-конфигов, один профиль на ноду (тест `TestBuildResponseOneProfilePerNode` проверяет, что 3 ноды дают 3 профиля с разными address), `remarks` = тег ноды, catch-all на СОБСТВЕННЫЙ тег профиля, inbounds socks+http, DNS + routing правила, отказ при отсутствии конвертируемых источников, `parseShareLink` (VLESS Reality/Trojan/Hysteria2 + malformed), `vmess://` со строковыми и числовыми `port`/`aid`, `TestTagProxyOutboundsReservesDirectAndBlock` + `TestBuildResponseReservedTagsFallback` (ранее `TestBuildResponseRemarksCollidingWithReservedTags`; ноды с remark `direct`/`block` получают запасной тег, а freedom/blackhole и правила маршрутизации остаются нетронутыми; падали до фикса `reservedOutboundTags`), `TestParseVMessLinkPreservesCase` (EqualFold по схеме `VMESS://`, payload не lower-cased)

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
- (2026-08-11) Тесты `GetActiveByPlanID`, `CreateOrder`, `GetOrdersBySubscriptionID` удалены вместе с мёртвыми методами (см. orders-payment-platega.md)

## Добавленные тесты (2026-07-17, метрики)
- `internal/metrics/metrics_test.go` — проверка инициализации всех метрик, smoke-test endpoint `/metrics`
- `internal/metrics/db_test.go` — 4 теста: GORM callbacks для create/query/update/delete

## Добавленные тесты (2026-08-26, ветка feat/broadcast-campaigns)
<!-- Тест-файлы broadcast-флоу бота ниже удалены вместе с бот-wizard /broadcast (рассылки
     переехали в admin-панель: admin/broadcasts.go + admin API /admin/api/broadcasts*,
     в internal/bot остался только broadcast_worker.go). Историческая запись. -->
- ~~`internal/bot/broadcast_admin_flow_test.go`~~ — УДАЛЁН: parseBroadcastCallbackID, callbacks broadcast_cancel_/broadcast_retry_, handleBroadcastFilter, handleBroadcastBackToFilters, форматтеры
- ~~`internal/bot/admin_broadcast_test.go`~~ — УДАЛЁН (+flood-тесты 429/retry_after; flood-логика живёт в internal/bot/broadcast_worker.go: processRecipient/isFloodError)
- `internal/xui/reset_traffic_test.go` — ResetTraffic: пустой email, успех (метод/путь/экранирование), Success:false с msg панели, невалидный JSON, не-2xx
- `internal/service/sync_test.go` — processPendingUpdate вызывает ResetTraffic при смене тарифа; сбой ResetTraffic — best-effort (нода всё равно active)
- `internal/service/subscription_test.go` — BindTrial rollback при DB-bind failure (rollback и conservative skip), CleanupExpiredTrials deprovision-failure оставляет строку, ReconcileOrphanedClients пересоздаёт node queue
