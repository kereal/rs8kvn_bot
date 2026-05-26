## Исправления по ревью от 25.05.2026

- Критическая проблема: ReconcileOrphanedClients вызывала DeleteSubscription(ctx, 0) для trial-подписок (TelegramID=0). Теперь всегда используется DeleteSubscriptionByID(sub.ID) — корректно удаляет и trials, и обычные. Инвалидация кэша только для реальных TGID. Добавлена метрика `bot_orphaned_clients_removed_total`.
- Завершено обновление мока MockDatabaseService в testutil (добавлены отсутствующие *Func поля для trial/реферальных методов) — сборка и тесты теперь проходят.
- Логика legacy bootstrap в runMigrations и race в GetOrCreateInvite оставлена как есть (защищена unique index из миграции 004; early-return убран в предыдущей итерации).
- Массивный рефактор web.go (1349 строк) не изменялся (suggestion по разделению — для будущих PR).
- Все релевантные тесты (service+database, -short) прошли (116+), go build чистый.

Добавлены тесты (внутренний/service/subscription_test.go):
- TestSubscriptionService_ReconcileOrphanedClients_RemovesMissing: проверяет удаление через DeleteByID для normal + trial, conditional invalidate (только для реальных TGID), возврат count=2.
- TestSubscriptionService_ReconcileOrphanedClients_NoActive: edge-case 0 удалений.

Существующий TestService_GetInviteByReferrer в database_test.go уже покрывает GetInviteByReferrer + симуляцию legacy duplicates (pre-004/005) и возврат oldest code — рекомендация ревью выполнена без новых изменений.

Дата: 2026-05-25
Статус: warnings из ревью закрыты.