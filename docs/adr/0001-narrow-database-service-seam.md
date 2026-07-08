# ADR-0001: Сужение шва `DatabaseService` до пер-срезовых интерфейсов

- **Статус:** Accepted
- **Дата:** 2026-07-09
- **Происхождение:** архитектурный обзор (Кандидат 1), петля grilling

## Контекст

`interfaces.DatabaseService` — «божественный» интерфейс, композиция 8 под-интерфейсов
(`SubscriptionRepository`, `SubscriptionNodeRepository`, `TrialRepository`, `NodeRepository`,
`InviteRepository`, `PlanRepository`, `ProductRepository`, `OrderRepository`) плюс корневые
методы (`Ping`, `Close`, `GetPoolStats`, `Transaction`, админ-методы планов). Суммарно ~60 методов.
Узкие под-интерфейсы были определены, но ни один потребитель их не использовал — каждый брал
весь `DatabaseService`. Следствие: гигантский hand-rolled фейк в `internal/testutil/testutil.go`
(1114 строк, по `XxxFunc`-полю на метод), который почти каждый тест инстанцирует целиком ради
стаба 1–2 методов.

Цель: сделать шов на стороне вызова узким (мелкий интерфейс, глубокая реализация), чтобы каждый
модуль явно объявлял только тот срез БД, который вызывает, и тесты стабили только нужное.

## Решение

1. Потребители переведены с `interfaces.DatabaseService` на узкие срезы:
   - `internal/subserver` → `interfaces.SubscriptionRepository`
   - `internal/web` → составной `interfaces.WebRepository` =
     `SubscriptionRepository` + `InviteRepository` + `TrialRepository` + `PlanRepository`
     (web отдаёт `s.db` в `subserver.HandleSubscription`, поэтому `SubscriptionRepository`
     обязателен; Node/Order/SubscriptionNode/Product выброшены)
   - `internal/scheduler/subscription_expire_worker.go` → `interfaces.SubscriptionRepository`
   - Широкие модули (`SubscriptionService`, bot-`Handler`, `SyncService`, `OrderService`) оставлены
     на составном `DatabaseService` (им реально нужно большинство срезов).
2. Сам `interfaces.DatabaseService` сохранён как composition root в `cmd/bot/main.go`; в бизнес-модулях
   как тип параметра он больше не используется. Структурная типизация Go гарантирует, что
   `*database.Service` и `*testutil.DatabaseService` удовлетворяют любому узкому срезу — изменение
   compile-checked, тесты остаются зелёными.
3. В `internal/testutil/db_slice_fakes.go` добавлены пер-срезовые фейки (`*...Fake`, по одному на
   под-интерфейс) + конструкторы `NewSubscriptionRepository()`, `NewTrialRepository()` и т.д.
   для узких тестов. Плоский `DatabaseService` в `testutil.go` оставлен нетронутым.

## Отвергнутая альтернатива: сборка `DatabaseService` через embedding пер-срезовых фейков

Рассмотрено (вариант 3A в grilling): сделать `DatabaseService` структурой, embedding-ящей
пер-срезовые фейки, чтобы он «собирался из срезов». **Отклонено**, потому что Go не позволяет
задавать promoted-поле внутри composite literal: существующие тесты строят фейк как
`&testutil.DatabaseService{ GetPlanByNameFunc: ... }` (~40 мест, в основном `subscription_test.go`).
Embedding сломал бы их компиляцию — нарушил бы обратную совместимость, ради которой выбран 3A,
а не 3B (полная перепись фейка). Поэтому композиция реализована параллельными opt-in фейками,
а не через embedding.

## Последствия

- **Локальность:** зависимость модуля от БД стала самоописательной (читаешь тип параметра — видишь
  точный срез).
- **Leverage:** новые узкие тесты берут `testutil.NewXRepository()` и стабят только нужный срез;
  гигантский фейк можно не инстанцировать.
- Плоский `DatabaseService` сохранён — это дублирует часть поверхности фейка, но цена приемлема
   ради нулевого риска для 1776 существующих тестов. Миграция старых composite-literal тестов на
  пер-срезовые фейки — отдельная, опциональная работа.
- `DatabaseService` **не раздуваем обратно** в единый параметр у уже суженных модулей.

## Не переоткрывать

Будущие архитектурные обзоры НЕ должны предлагать «вернуть `DatabaseService` как единый тип
параметра у subserver/web/scheduler» или «собрать фейк через embedding» — обе темы разобраны здесь.
