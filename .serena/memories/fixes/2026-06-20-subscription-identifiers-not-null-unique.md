# NOT NULL UNIQUE для идентификаторов подписки

Выполнено 2026-06-20.

## Решение
- `subscriptions.client_id` и `subscriptions.subscription_id` приведены к `NOT NULL UNIQUE`.
- Обновлена GORM-модель `Subscription` в `internal/database/models.go`:
  - `ClientID string gorm:"size:255;not null;uniqueIndex"`
  - `SubscriptionID string gorm:"size:255;not null;uniqueIndex"`
- Дополнена уже существующая миграция `023_add_unique_constraints_and_indexes`:
  - `internal/database/migrations/023_add_unique_constraints_and_indexes.up.sql`
  - добавлен `CREATE UNIQUE INDEX idx_subscriptions_subscription_id_unique ON subscriptions(subscription_id);`
  - `internal/database/migrations/023_add_unique_constraints_and_indexes.down.sql`
  - добавлен `DROP INDEX IF EXISTS idx_subscriptions_subscription_id_unique;`
- Обновлены тестовые фабричные данные, чтобы явные вставки `Subscription` содержали уникальные `SubscriptionID`.

## Верификация
- `rtk go test -short -count=1 -timeout=180s ./...` — 1664 passed.
- `rtk go test -short -race -count=1 -timeout=180s ./...` — 1664 passed после повторного прогона.
- `rtk go vet ./...` — без замечаний.
- `rtk git diff --check HEAD...dev && rtk git diff --check HEAD` — без замечаний.

## Коммит
- `fix: enforce subscription identifiers not null unique`

## Примечания
- Миграция `023` на момент работы еще не была применена, поэтому пересборка таблицы не потребовалась.
- `client_id` и `subscription_id` в исторической миграции `000_create_subscriptions.up.sql` оставлены nullable для старых инстансов; enforcement обеспечивается миграцией `023` и GORM-моделью.