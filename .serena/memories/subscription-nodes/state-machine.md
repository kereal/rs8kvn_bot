> ⚠️ FUTURE TASK — schema/model already exist; worker/implementation still TODO.

# Subscription Nodes — фактическое состояние синхронизации подписки с VPN-нодами

Эта таблица хранит **не список серверов тарифа**, а **фактическое состояние синхронизации конкретной подписки с конкретными VPN-нодами**.

---

## Зачем нужна эта таблица

Допустим:

```text
Plan Premium
 ├─ Finland
 ├─ Germany
 └─ Netherlands
```

Пользователь активировал Premium.

Мы хотим добавить его на все три ноды.

Но:

```text
Finland     OK
Germany     OK
Netherlands DOWN
```

Тогда в БД будет:

| subscription_id | node_id     | status      |
| --------------- | ----------- | ----------- |
| 15              | Finland     | active      |
| 15              | Germany     | active      |
| 15              | Netherlands | pending_add |

То есть система знает:

```text
Пользователь уже есть на Finland
Пользователь уже есть на Germany
Пользователя нужно добавить на Netherlands
```

---

# Поля

## subscription_id

Подписка пользователя.

Например:

```text
15
```

---

## node_id

VPN-нода.

Например:

```text
3
```

---

## status

Самое важное поле.

### active

Означает:

```text
Пользователь успешно создан на ноде.
```

Пример:

| subscription_id | node_id | status |
| --------------- | ------- | ------ |
| 15              | 3       | active |

---

### pending_add

Означает:

```text
Пользователя нужно добавить на ноду.
```

Причины:

* новая подписка;
* смена плана;
* ошибка предыдущей попытки.

---

### pending_remove

Означает:

```text
Пользователя нужно удалить с ноды.
```

Причины:

* истек Premium;
* сменился план;
* нода больше не входит в план.

---

## retry_count

Сколько раз операция завершилась ошибкой.

Пример:

```text
0
```

операция еще не падала.

```text
3
```

три неудачных попытки.

---

## retry_at

Когда можно пробовать снова.

Пример:

```text
2026-06-08 15:30:00
```

До этого времени воркер запись не трогает.

---

## last_error

Последняя ошибка.

Например:

```text
connection refused
```

или

```text
timeout
```

или

```text
authentication failed
```

Очень помогает при отладке.

---

## updated_at

Когда запись последний раз менялась.

---

# Жизненный цикл записи

## Создание подписки

Пользователь получает Free.

Создаются записи:

| subscription_id | node_id | status      |
| --------------- | ------- | ----------- |
| 15              | Finland | pending_add |

---

После успешной синхронизации:

| subscription_id | node_id | status |
| --------------- | ------- | ------ |
| 15              | Finland | active |

---

## Переход на Premium

Premium содержит:

```text
Finland
Germany
Netherlands
```

Создаются:

| subscription_id | node_id     | status      |
| --------------- | ----------- | ----------- |
| 15              | Finland     | active      |
| 15              | Germany     | pending_add |
| 15              | Netherlands | pending_add |

---

После успешной синхронизации:

| subscription_id | node_id     | status |
| --------------- | ----------- | ------ |
| 15              | Finland     | active |
| 15              | Germany     | active |
| 15              | Netherlands | active |

---

## Истечение Premium

Пользователь возвращается на Free.

Нужно удалить его:

```text
Germany
Netherlands
```

Состояние:

| subscription_id | node_id     | status         |
| --------------- | ----------- | -------------- |
| 15              | Finland     | active         |
| 15              | Germany     | pending_remove |
| 15              | Netherlands | pending_remove |

---

После успешного удаления:

| subscription_id | node_id | status |
| --------------- | ------- | ------ |
| 15              | Finland | active |

Записи для Germany и Netherlands удаляются.

---

# Почему эта таблица важна

Она хранит не желаемое состояние тарифа.

Для этого уже есть:

```text
plans
plan_nodes
```

Она хранит:

```text
Что реально произошло на VPN-инфраструктуре.
```

Именно благодаря этой таблице система переживает:

* падение нод;
* рестарты приложения;
* ошибки сети;
* временную недоступность VPN API.

После запуска воркера всегда можно посмотреть:

```text
subscription_nodes
```

и понять:

```text
что уже синхронизировано,
что нужно добавить,
что нужно удалить.
```

По сути это очередь задач и источник истины для синхронизации VPN-доступов одновременно.

---
Schema: `subscription_nodes(subscription_id, node_id, status, retry_count, retry_at, last_error, updated_at)`
Go model: `database.SubscriptionNode` в `internal/database/database.go`
Migration: `internal/database/migrations/018_create_subscription_nodes.up.sql`
