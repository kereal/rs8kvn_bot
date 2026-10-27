# Аудит тестовой базы rs8kvn_bot (2026-10-27)

**Статус:** 2201 тест в 22 пакетах, все проходят  
**Команда аудита:** 4 read-only scout-агента (bot/database/service/infrastructure)  
**Дата:** 2026-10-27

---

## Резюме

Общее качество **хорошее**: много поведенческих тестов, транзакционные проверки, регрессионные тесты с доказательством через откат. Проблемы сосредоточены в:
- **internal/bot**: 30% тестов бесполезны (bare not-throw, wiring, mock-echo) или дублируют покрытие
- **internal/database**: периферийные тавтологии и reflection-тесты, но ядро (CAS, audit, миграции) сильное
- **internal/service**: зрелые тесты, пробелы только в краевых путях деактивации
- **internal/subserver + internal/xui**: покрытие протоколов широкое, но отсутствует e2e Hysteria2 и DNS/routing-проверка

---

## P1 — Блокеры (требуют исправления)

### 1. `internal/bot/subscription_test.go:145` — `TestCreateSubscription_XUIFailure` (Useless)
**Проблема:** Тест назван "XUIFailure", но ни одна ошибка XUI не инжектится. Мёртвые поля `errMsg` и `expectedError` не используются. Фактически дублирует успешный путь.

**Действие:** Переписать: реально заставить XUI-клиент вернуть ошибку, ассертить правильное сообщение пользователю, удалить мёртвые поля.

---

### 2. `internal/bot/handlers_test.go:419` — `TestHandleUpdate_CommandRouting` (Incomplete)
**Проблема:** Поле `wantCommand` в таблице нигде не ассертится — проверяется только `SendCalledSafe()`. Команда может диспатчиться не туда, и тест останется зелёным.

**Действие:** Задействовать `wantCommand` или удалить; добавить проверку, что не-админ не получает `/refstats`, `/send`, `/broadcast`.

---

### 3. `internal/subserver` — отсутствует e2e Hysteria2/hy2 в подписке (Incomplete)
**Проблема:** `parseShareLink` поддерживает `hysteria://`, `hysteria2://`, `hy2://`, а `buildHysteria2Xray` рендерит полный outbound с TLS/obfs (`finalmask`). Но:
- `TestParseShareLink` покрывает только `hysteria2://`; `hy2`-алиас и hysteria v1 не проверены
- Полный путь `hysteria2://` link → `buildResponse` → Xray-outbound не тестируется вовсе

**Действие:** Добавить сквозной тест:
```go
// hysteria2://auth@hy.example:8443?sni=front.example&fp=chrome&obfs=salamander&obfs-password=pass#HY2
// → buildResponse → распарсить outbound:
// protocol=="hysteria", settings.version==2, hysteriaSettings.auth=="auth",
// tlsSettings.serverName=="front.example",
// finalmask.udp[0].type=="salamander", finalmask.udp[0].settings.password=="pass"
```
Отдельный субтест: `hy2://`-алиас нормализуется в `hysteria2`.

---

### 4. `internal/subserver/concurrency_test.go` — `TestFetchAndAggregateSources_Parallel` (Fragile + Slow)
**Проблема:** Жёсткие тайм-окна по `time.Sleep`: `assert.Less(duration, 800ms)` и `assert.GreaterOrEqual(duration, 150ms)`. Классический флаки-риск на загруженном CI. Плюс 10 `httptest`-серверов со sleep 100ms (~секунды в CI-цикле).

**Действие:** Проверять параллельность структурно: атомарный счётчик «одновременно выполняющихся» запросов (увеличивать на входе, сравнить с окном контекста до завершения). Это докажет параллельность без зависимости от стенда и в разы быстрее.

---

## P2 — Важно

### 5. `internal/database/database_test.go:55-90` — reflection-тесты типов (Useless, 3 теста)
**Проблема:** `TestOrder_StatusUsesOrderStatusType`, `TestNode_TypeUsesNodeType`, `TestSubscriptionNode_StatusUsesSyncStatus` — reflection-проверка статического Go-типа поля структуры. Компилятор уже гарантирует это; тест ломается при переименовании типа без изменения поведения. Zero behavioral value.

**Действие:** Удалить.

---

### 6. `internal/database/database_test.go:380` — `TestService_GetByTelegramID_ReturnsActiveOnly` (Incomplete)
**Проблема:** Название обещает фильтрацию «только активные», но тело создаёт только активную подписку и проверяет её возврат. Исключение revoked/expired НЕ проверено.

**Действие:** Добавить revoked/expired-подписки и ассертить, что они НЕ возвращаются.

---

### 7. `internal/database/database_test.go` — `TestService_BindTrialSubscription_Concurrent` (Unclear)
**Проблема:** Назван «Concurrent», но НЕ конкурентный: последовательно первая привязка успешна, вторая падает. Настоящая гонка привязки trial не покрыта (нет goroutine/параллельной попытки bind).

**Действие:** Переименовать в `..._RejectsSecondBind` или реализовать настоящий concurrent bind.

---

### 8. `internal/database/audit_payment_test.go:40` — хардкод автоинкремент-ИД (Fragile)
**Проблема:** `assert.Equal(t, "1", *e.EntityID)` — хардкод автоинкремент-ИД (предполагает свежную БД, первый заказ). Хрупкое сцепление с последовательностью ИД.

**Действие:** Заменить на `assert.Equal(t, strconv.FormatUint(order.ID,...))` или сравнение с заранее известным ИД.

---

### 9. `internal/service/subscription_deactivate_test.go` — пробелы покрытия (Incomplete, 8 пунктов)
**Проблема:** Путь `DeactivateBlockedBroadcastRecipients` имеет ветки, не покрытые ни одним из 3 тестов:
1. Ошибка `GetBroadcastRecipientsStats` (ранний return)
2. Пустое множество целей (`len(targetTelegramIDs)==0`)
3. Telegram-без-подписки (GetByTelegramID NotFound → continue, не Failed)
4. Ошибка чтения подписки (не-NotFound error → Failed++)
5. Unreachable-адресаты (только `blocked` тестируется, `unreachable` нет)
6. Кап `maxImmediateSyncs = 25` и фоновый handoff
7. Side-effect `InvalidateBySubID` не ассертится
8. `RefreshActiveSubscriptionsMetric` не ассертится

**Действие:** Добавить табличный кейс с комбинацией `{stats-error, no-targets, no-subscription-skip, unreachable}` и тест капа 25 → фоновый deprovision, плюс hook на `InvalidateBySubID`.

---

### 10. `internal/service/subscription_test.go` — `Create_DBError_RollbackSuccess` vs `_RollbackFailed` (Duplicate)
**Проблема:** Почти идентичны (одинаковый mock, одинаковые ассерты). Оба имени обещают проверить rollback (успешный/неудачный), но rollback в этих сценариях вообще не задействован: нет узлов, нет XUI/VPN клиентов для отката.

**Действие:** Оставить один такой кейс (переименовав) и добавить настоящий rollback-тест с уже созданным панельным клиентом + последующим DB-сбоем.

---

### 11. `internal/subserver/xray_subscription_test.go` — `TestXrayTemplateRoutingAndDNS` (Incomplete)
**Проблема:** Проверены только `DNS.Servers` длиной 2 и правила `[0]`(block), `[2]`(geoip:ru→direct), `[4]`(catch-all). Не проверено:
- **что за 2 DNS-сервера** (реальные адреса? `localhost`? провайдерские IP?)
- правила `Rules[1]` и `Rules[3]` (пропущены индексы — дыра в проверке массива routing)
- отсутствие явного теста, что `domainStrategy` корректно сочетается с DNS

**Действие:** Ассертить содержимое `DNS.Servers` (2 конкретных адреса) и все 5 rules-индексов (`[1]`, `[3]` включительно), а не только 0/2/4.

---

### 12. `internal/subserver/xray_subscription_test.go` — VMess corrupt-base64 и строковый `aid` (Incomplete)
**Проблема:** `parseVMessItem`/`parseVMessLink` покрыты для URL-формы и base64-JSON-формы, но нет теста на **битый/невалидный base64** в `vmess://` пути (что вернёт ошибку), и на строковый `aid` (в `buildVMessXray` используется `strconv.Atoi(cfg.Aid)` — ветка ошибки при `aid:"abc"` не протестирована).

**Действие:** Добавить тесты:
- VMess corrupt-base64 (`vmess://` + не-дешифруемый payload → `Error`)
- Строковый `aid` (`"aid":"abc"` → `alterId==0`, без падения)

---

### 13. `internal/subserver/proxy_test.go` — `TestBase64StdEncode` (Useless)
**Проблема:** `expected := base64.StdEncoding.EncodeToString(input); assert.Equal(t, expected, base64StdEncode(input))`. В `fetch.go:222` функция — буквальный враппер: `return base64.StdEncoding.EncodeToString(data)`. Это тавтология: ожидаемое значение вычислено той же stdlib-функцией, которую оборачивает помощник.

**Действие:** Удалить или сравнить с фиксированной известной константой (e.g. `vg==` для `"ab"`).

---

### 14. `internal/subserver/xray_subscription_test.go` — дубликаты reserved-tag e2e (Duplicate)
**Проблема:** `TestBuildResponseRemarksCollidingWithReservedTags` vs `TestBuildResponseReservesDirectAndBlockTags` vs `TestTagProxyOutboundsReservesDirectAndBlock` — три теста проверяют одну и ту же логику (зарезервированные теги `direct`/`block`, фолбэк-теги при коллизии имени ноды).

**Действие:** Слить два e2e в один табличный тест, оставив unit-слой.

---

### 15. `internal/subserver/service_test.go` — дубликаты cache (Duplicate)
**Проблема:** `TestService_CacheThroughService`/`_CacheMiss`/`_InvalidateCache` полностью дублируют покрытие `cache_test.go` (`TestCache_SetAndGet`, `TestCache_GetMiss`, `TestCache_Delete`) через тривиальную обёртку `Service{Cache}`.

**Действие:** Убрать или свести к одному интеграционному кейсу.

---

### 16. `internal/bot` — 30% бесполезных/дублирующих тестов (Useless/Duplicate, массово)
**Проблема:** Bare not-throw / not-panic тесты (запрещены AGENTS.md):
- `callbacks_test.go:22-54` — `TestHandleCallback_NilCallbackQuery`, `TestHandleCallback_NilFrom`
- `commands_test.go:205-252` — `TestHandleStart_NilFrom`, `TestHandleInvite_NilMessage`, `TestHandleInvite_NilFrom`
- `menu_test.go:387-490` — `TestHandleMenuHelp_ContextCancellation`, `TestHandleBackToStart_SendError`, `TestHandleMenuDonate_SendError`, `TestHandleMenuHelp_SendError`
- `message_test.go:42-68, 124-147, 523-546` — `TestSend_SendError`, `TestSafeSend_SendError`, `TestSendMessage_NilHandler`
- `subscription_test.go:833-871` — `TestSendQRCode`, `TestHandleBackToInvite` через `assert.NotPanics`
- `graceful_shutdown_test.go:98-109` — `TestHeartbeat_StopOnContextCancel` вообще не вызывает heartbeat
- `graceful_shutdown_test.go:149-196` — `TestGracefulShutdown_WithActiveUpdates` и `_RateLimiterCleanup` (start+cancel, ноль ассертов)

Дубликаты:
- `database_test.go` — `TestHandleStart_WithDatabase` ≡ `TestHandleMySubscription_WithSubscription` ≡ `TestHandleSend_ByTelegramID`
- `subscription_test.go:712` `TestHandleQRCode_Success` ≡ `TestHandleQRCode_WithSubscription`
- `commands_test.go:883` `TestHandleMySub_Success` ≈ `subscription_test.go:561` `TestHandleMySubscription_ActiveSubscription`
- `callbacks_test.go:1018-1147` дублируют кейсы `TestHandleCallback_CallbackDataRouting`
- `handlers_test.go:246-283` дублируют `handlers_extended_test.go:186/21`

**Действие:** Удалить bare not-throw тесты или превратить в содержательные (ассерт на возвращаемую ошибку/состояние). Слить дубликаты.

---

### 17. `internal/bot/database_test.go` — Misleading-имена (Unclear)
**Проблема:** Тесты названы `TestHandle*` (`TestHandleStart_WithDatabase`, `TestHandleMySubscription_*`, etc), но **ни в одном не вызывается хендлер** — только методы `db.*`.

**Действие:** Переименовать в `TestDB_*` или перенести в `internal/database`.

---

## P3 — Можно (низкий приоритет)

### 18. `internal/database/orders_test.go` — `TestOrderStatusConstants` (Useless)
**Проблема:** `assert.Equal(t, OrderStatus("pending"), OrderStatusPending)` — проверяет, что константа равна своему литералу. Прямая тавтология.

**Действие:** Удалить.

---

### 19. `internal/database/database_test.go:210` — `TestService_GetPoolStats` (Useless)
**Проблема:** `assert.GreaterOrEqual(t, stats.MaxOpen, 0)` — тривиально истинно (int никогда не < 0).

**Действие:** Удалить или заменить на осмысленную проверку.

---

### 20. `internal/database/migration_test.go` — тавтологии (Useless, 3 теста)
**Проблема:**
- `TestMigration_AddNewTable` — создаёт стороннюю таблицу через raw `Exec`, метод миграций вообще не вызывается
- `TestMigration_SchemaVersionTracking` — `assert.GreaterOrEqual(t, version, 0)` тривиально истинно
- `TestLatestEmbeddedMigrationVersion` — зашит маг. номер `40`; хрупко к каждому бампу миграции

**Действие:** Удалить первые два, третий переписать на динамический подсчёт миграций.

---

### 21. `internal/database/audit_log_test.go` — хрупкие / weak (Fragile/Useless, 2 теста)
**Проблема:**
- `TestAuditLog_TableSchema` — жёстко зашит `columnCount == 10` и `indexCount >= 3`. Добавление/удаление колонки ломает тест независимо от поведения.
- `TestAuditLog_UniversalConstructor` — проверяет, что тривиальный конструктор копирует переданные аргументы в поля (echo). Borderline wiring.

**Действие:** Первый переписать на проверку именованных колонок, второй удалить.

---

### 22. `internal/database/sqlite_wal_test.go` — config-wiring (Fragile)
**Проблема:** `TestSQLiteBusyTimeoutDSN`, `TestNewServiceUsesWALAndMultipleConnections` — пин-тест точной DSN-строки и `MaxOpenConnections == 4`. Ломается при изменении параметров пула без изменения поведения.

**Действие:** Удалить или заменить на проверку поведения (WAL включён, пул работает).

---

### 23. `internal/database` — дубликаты (Duplicate, 2 пары)
**Проблема:**
- `database_test.go` `TestSubscription_IsActive` + `nodes_test.go` `TestSubscription_IsActive_StatusCases` (5 из 6 совпадают)
- `migration_test.go` `TestMigration_Idempotency` + `TestMigration_PreserveDataOnUpgrade` (почти одно и то же)

**Действие:** Слить пары, оставить по одному тесту.

---

### 24. `internal/xui/client_test.go` — мелкие (Useless/Duplicate)
**Проблема:**
- `TestGetExpiresAtMillis` — дублированный assert `time.Time{}` дважды подряд
- `TestTruncateString` — дублирует `FuzzTruncateString`
- `compile_check_test.go` — compile-time check (wiring)

**Действие:** Убрать дубль, удалить `TestTruncateString`, оставить `compile_check` как страхующий.

---

### 25. `internal/subserver/cache_test.go` — TTL/Cleanup (Fragile)
**Проблема:** `TestCache_TTLExpiry`, `TestCache_Cleanup` — положились на `NewCache(10*time.Millisecond)` + `sleep`/`Eventually`-стиль реального времени. Дешёвый TTL-флейк на медленном CI.

**Действие:** Инжектируемые часы (clock) или увеличить допуск.

---

### 26. `internal/bot` — хрупкие / weak (Fragile/Low-value)
**Проблема:**
- `graceful_shutdown_test.go:17` — memory-leak через `runtime.ReadMemStats`+`GC()` с допуском 5 MB; `TestGoroutineLeak:111` — счётчик горутин + допуск +15. Шумные/флаки-prone и медленные.
- `keyboard_test.go:15-42` — жёстко `Len(...)==4`/`==2`: ломается при любом добавлении строки меню.
- `handlers_extended_test.go:257/484` — ассертят «сырой» `subscriptionURL` дословно внутри Markdown-текста. Хрупкие к экранированию.
- `menu_test.go` — `TestHandleBackToStart_VariousMessageIDs`/`VariousChatIDs` — в основном только `NotEmpty` и «проброс» входных значений. Низкая ценность, но не бесполезны.

**Действие:** Переписать memory/goroutine leak тесты на детерминированные (каналы/errgroup) или убрать допуски. Keyboard/URL-тесты свести к проверке маркеров вместо полных строк.

---

## Missing coverage (важные пробелы, не покрытые P1-P2)

27. **Rate limiting не проверяется** — нет теста, что повторный `/send` внутри окна блокируется (всегда `Wait==true` в `TestHandler_RateLimiter`)
28. **Broadcast**: нет покрытия flood-ожидания на границе таймаута и канселация worker при shutdown mid-broadcast
29. **Referral cache Sync при ошибке БД** (`GetAllReferralCounts` Error) не покрыт
30. **Audit log query helpers** (`GetAuditLogByEntity/ByEvent/GetRecentAuditLog`): не покрыты limit-семантика и запрос по никогда-не-существовавшей сущности
31. **Chargeback-атомарность**: откат chargeback-аудита при `applyPlan`-ошибке косвенно есть, но там НЕ ассертится отсутствие audit-событий
32. **Shadowsocks без method через подписку** — `ss://host:443` без method → успехом в `parseShareLink` → отказом на `buildXrayOutbound` — не проверен e2e

---

## ✅ Что хорошо (сохранить как есть)

- **Transactional integrity**: `TestConfirmOrderPaidCAS_RollsBackAuditWithTransaction`, `TestFindOrCreatePendingPaymentOrder_DoesNotAuditExpiryOnRollback`, `TestCancelPaidOrderAndDowngradeCAS_RollsBackOnPlanApplyFailure`, `TestBroadcastRecipientConcurrentFinalizationsAllPersist` (регрессия `SQLITE_BUSY_SNAPSHOT`)
- **Error paths**: not-found, FK/CHECK-инварианты миграций 032/033, nil-гард
- **Concurrent writes**: broadcast-claim + finalization гонки, отказ вторая привязка trial
- **Migration invariant/normalization**: 032 retry, 033 status, 005 dedup — полноценные откат-вертикальные тесты
- **Regression tests**: `admin_broadcast_test.go` и `broadcast_admin_flow_test.go` (state-машины рассылки), `payment_handler_test.go` (edge-кейсы тарифов), `split_message_test.go` (инвариант сбалансированных сущностей), `referral_cache_test.go`, `referral_handler_test.go`, `repro_qr_test.go` (контракт «Back удаляет только своё сообщение»), `fuzz_test.go`, `admin_setplan_test.go`
- **Service tests**: retries синка (retry-at backoff, `handleSyncError`), идемпотентность панели через `ErrSubscriptionAlreadyExists/NotFound`, отсутствие VPN-клиента → остаётся pending, продолжение после сбоя узла (`errors.Join`), конкурентные CAS в платежах

---

## Приоритизированный план действий

1. **P1 (блокеры, 4 пункта):** Исправить `TestCreateSubscription_XUIFailure` (#1), `TestHandleUpdate_CommandRouting` (#2), добавить e2e Hysteria2 (#3), переписать `TestFetchAndAggregateSources_Parallel` (#4)
2. **P2 (важно, 13 пунктов):** Удалить reflection-тесты (#5), исправить incomplete (#6, #7, #9, #11, #12), убрать хардкод ИД (#8), переписать rollback (#10), удалить тавтологии (#13), слить дубликаты (#14, #15, #16), переименовать (#17)
3. **P3 (можно, 10 пунктов):** Убрать оставшиеся тавтологии (#18-21), слить дубликаты (#23, #24), исправить хрупкие (#22, #25, #26)
4. **Missing coverage (8 пунктов):** Рассмотреть добавление (#27-32) по мере необходимости

---

## Метрики

| Категория | Найдено | P1 | P2 | P3 |
|-----------|---------|----|----|-----|
| Useless (тавтология, wiring, mock-echo) | 18 | 1 | 4 | 13 |
| Fragile (хрупкие к рефакторингу) | 12 | 1 | 2 | 9 |
| Incomplete (недопокрытые edge cases) | 15 | 2 | 6 | 7 |
| Duplicate (дублирующие покрытие) | 10 | 0 | 5 | 5 |
| Unclear (плохие имена/структура) | 5 | 1 | 2 | 2 |
| **Всего проблем** | **60** | **5** | **19** | **36** |

**Процент бесполезных/хрупких тестов:** ~13% (30 из 2201) требуют удаления или переработки
