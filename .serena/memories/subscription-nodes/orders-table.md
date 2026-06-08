# Orders Table — rs8kvn_bot

## Schema

Table `orders` (migration `017_create_orders`):

- `id` — PK, AUTOINCREMENT
- `subscription_id` — FK → `subscriptions(id)`
- `product_id` — FK → `products(id)`
- `status` — TEXT, CHECK (`pending` | `paid` | `expired` | `canceled`)
- `amount_cents` — сумма в копейках
- `currency` — CHAR(3), DEFAULT `RUB`
- `payment_provider` — платёжный провайдер
- `provider_payment_id` — внешний ID платежа у провайдера
- `created_at` — момент создания
- `paid_at` — подтверждение оплаты
- `activated_at` — активация подписки
- `expires_at` — срок действия счёта/инвойса

Indexes: `idx_orders_subscription_id`, `idx_orders_status`, `idx_orders_created_at`, `idx_orders_product_id`

## Statuses

- `pending` — создан, ожидает оплаты
- `paid` — оплата подтверждена
- `expired` — истёк срок ожидания оплаты
- `canceled` — отменён

## Model

`database.Order` в `internal/database/database.go`:
- GORM-модель с внешними ключами
- Связи: `Subscription` (`foreignKey:SubscriptionID`), `Product` (`foreignKey:ProductID`)
- ключевые поля: `provider_payment_id`, `paid_at`, `activated_at`, `expires_at`

## Repository

Interface `OrderRepository` в `internal/interfaces/interfaces.go`:
- `CreateOrder(ctx, *Order) error`
- `GetOrderByID(ctx, id uint) (*Order, error)`
- `GetOrdersBySubscriptionID(ctx, subscriptionID uint) ([]Order, error)`
- `UpdateOrderStatus(ctx, id uint, status string) error`

## Documentation

- `doc/architecture.md` — Data Model, ER-диаграмма включает `orders`
- `doc/handover.md` — Database: orders + payment lifecycle
