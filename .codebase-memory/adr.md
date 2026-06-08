## PURPOSE
Разбить монолитный database.go на независимые файлы по доменам для улучшения навигации и поддерживаемости.

## STACK
Go 1.25, GORM, один пакет `database` (internal/database/), 9 файлов вместо 1.

## ARCHITECTURE
- `models.go` — все GORM-модели, TableName(), хелперы Subscription, константы
- `migrations.go` — go:embed + runMigrations()
- `service.go` — Service struct, конструктор, Close/Ping/PoolStats
- `subscriptions.go` — Subscription + TelegramID CRUD (19 функций)
- `nodes.go` — Node/Plan CRUD + LinkNodeToPlan
- `invites.go` — Invite/Referral CRUD
- `trials.go` — Trial CRUD + BindTrialSubscription
- `orders.go` — Order CRUD
- `products.go` — GetActiveByPlanID

## PATTERNS
- Файлы сгруппированы по доменам (не по слоям)
- Все файлы в одном package `database`, общий доступ к `Service` struct
- Публичный API (DatabaseService interface) не изменился

## TRADEOFFS
- + навигация по файлам вместо поиска по 1216 строкам
- + параллельное редактирование разных доменов без merge конфликтов
- - небольшое увеличение числа файлов (9 вместо 1)
- - нужно обновлять импорты при перемещении между доменами (не требуется — один package)

## PHILOSOPHY
Один файл на домен: если домен >100 строк — выносим в отдельный файл. Это компромисс между монолитом и микро-пакетами.