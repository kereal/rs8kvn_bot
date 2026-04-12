# Дорожная карта — rs8kvn_bot

**Создано:** 2026-04-02  
**Обновлено:** 2026-04-11  
**Версия:** v2.2.0  
**Масштаб:** 10 клиентов → 100 клиентов

---

## Текущий статус

- **Активные пользователи:** ~10 клиентов
- **Покрытие тестами:** ~85%
- **Архитектура:** 3x-ui один сервер
- **Приоритет:** Монетизация и рост
- **Оптимизация памяти:** O(1) LRU cache ✅

## Рефакторинг v2.2.0 (2026-04-10) ✅ ВСЕ ВЫПОЛНЕНО

### Фаза 1: Багфиксы ✅

1. **ReferralCache.Save() — noop** ✅
   - Удалён broken dirty tracking (referral counts — derived из subscriptions)
   - Save() → no-op (DB — источник истины)
   - Sync() → просто Load() (refresh from DB)
   - Бонус: sync.Map type assertion → safe two-value form

2. **Nil pointer dereference при init fail** ✅
   - logger.Warn → logger.Fatal для критичных компонентов (DB, XUI, Bot API)
   - Раньше: продолжал с nil → гарантированный panic

3. **Неатомарное удаление подписки** ✅
   - Порядок изменён: DB-first, XUI-best-effort
   - Если DB delete падает → XUI не тронут (можно retry)
   - Если XUI delete падает → DB уже удалён, orphaned XUI client — меньшее зло
   - Обновлены тесты

4. **context.WithoutCancel для broadcast** ✅
   - Убран — broadcast уже обрабатывает ctx.Done() в loop

5. **handleBindTrial не инвалидирует кэш** ✅
   - Добавлен h.invalidateCache(chatID) после успешного bind

6. **formatDateRu zero-time** ✅
   - Добавлен if t.IsZero() { return "—" }

### Фаза 2: Quick wins ✅

- ✅ Dead code в verifySession() удалён
- ✅ Doc files уже lowercase

### Фаза 3: Security ✅

- ✅ 3.1 Timing-safe token comparison (crypto/subtle.ConstantTimeCompare)
- ✅ 3.2 isLocalAddress → loopback only (no IsPrivate)
- ✅ 3.3 Web server port binding check (net.Listen → Serve)
- ✅ 3.4 getClientIP malformed fallback
- ✅ 3.5 Health endpoint 503 при Down

### Фаза 3.6: Дедупликация ✅

- ✅ daysUntilReset, formatDateRu, generateProgressBar → internal/utils/format.go
- ✅ Оба пакета (bot, service) используют utils.DaysUntilReset и т.д.
- ✅ Тесты перенесены в internal/utils/format_test.go

### Фаза 4: Тесты ✅

- ✅ 4.1 Service layer tests: 24.8% → 95.2% (30 тестов)
- ✅ 4.2 ReferralCache tests: 15 тестов

---

## Краткосрочные приоритеты (1-2 месяца)

### Цель: 50-100 платящих клиентов

### Фаза 1: Монетизация (Неделя 1-2)
**Приоритет:** ВЫСШИЙ

1. **Интеграция платежей** (ЮKassa/Т-Банк Pay)
   - Автоматическое продление после оплаты
   - Несколько тарифных планов
   - Время: 4-6 часов

2. **Система промокодов**
   - Админ: `/promo КОД 30% 2026-12-31`
   - Пользователь: `/promo КОД`
   - Время: 2-3 часа

3. **Уведомления о лимите трафика**
   - Алерт при 80%, 90%, 100% использования
   - Время: 2-3 часа

### Фаза 2: Фичи для роста (Неделя 3-4)

4. **Система наград за рефералов**
5. **Еженедельный отчёт админу**
6. **Авто-напоминания неактивным**

### Фаза 3: Пользовательский опыт (Месяц 2)

7. **Генератор многосерверных подписок** 🔥
8. **Пауза/возобновление подписки**
9. **Сбор фидбека**

---

### Bugfixes (2026-04-11) ✅

1. ✅ **escapeMarkdown missing backslash** — `\` добавлен первым в список экранирования MarkdownV2
2. ✅ **HandleBroadcast 30s timeout** — заменён на 5 минут
3. ✅ **GetOrCreateInvite игнорирует INSERT ошибку** — добавлена проверка err
4. ✅ **pendingInvites утечка памяти** — добавлена периодическая очистка
5. ✅ **handleMySubscription дублирует GetWithTraffic** — заменено на вызов service слоя
6. ✅ **CleanupExpiredTrials wrong cutoff** — отдельный 1h cutoff для trial_requests

---

## Технический долг (оставшийся)

1. ~~Pending invite codes — in-memory only, lost on restart~~ → теперь есть периодическая очистка
2. ExpiryTime не сохраняется в БД при Create() — админ видит "—" вместо даты сброса
3. `/sub/{subID}` не проверяет статус подписки — отдаёт контент даже для отозванных
4. Circuit breaker cumulative failures → sliding window
5. Вынести тексты сообщений — централизованный конфиг
6. Типизированные ошибки — заменить сравнение строк
7. `.down.sql` миграции — поддержка отката

---

**Последнее обновление:** 2026-04-10  
**Следующий пересмотр:** Ежемесячно (первый понедельник)
