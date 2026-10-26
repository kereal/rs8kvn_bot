# Диаграммы проекта rs8kvn_bot

Интерактивные standalone-диаграммы (archify, quality profile `showcase`). Каждая —
самодостаточный HTML: темы dark/light, pan/zoom, поиск по узлам, трассировка связей,
интерактивные главы (`meta.views`) для демонстрации по сценариям, экспорт PNG/SVG/WebM.
Открывается в любом браузере, без сервера.

Все диаграммы построены по коду репозитория (revision `849bd897`); JSON-спецификации
лежат рядом и могут быть перегенерированы.

## Карта проекта

| Файл | Тип | Что показывает |
|---|---|---|
| [project-map.html](project-map.html) | architecture | Общая карта: Telegram/HTTP вход, Service Layer, VPN-адаптеры, subserver, воркеры, SQLite, внешние системы |

## Sequence — цепочки вызовов

| Файл | Что показывает |
|---|---|
| [platega-payment-lifecycle.html](platega-payment-lifecycle.html) | Вебхук `/payment/callback`: guard-цепочка, per-order lock, `ConfirmOrderPaidCAS`, post-commit sync |
| [sub-serving.html](sub-serving.html) | `GET /sub/{subID}`: кэш 240s с singleflight, параллельная агрегация нод, детект формата |
| [subscription-intake.html](subscription-intake.html) | Создание подписки: DB-setup синхронно, внешний sync best-effort, выдача ссылки |
| [trial-invite.html](trial-invite.html) | `/i/{code}`: валидация, rate-limit по IP, лендинг, race-safe привязка триала |
| [broadcast.html](broadcast.html) | `/broadcast`: wizard, персистентная очередь, resume после рестарта, retry с классификацией |
| [subscription-teardown.html](subscription-teardown.html) | Двухфазное удаление `/del`: revoke → депровизия → физическое удаление после пустой очереди нод |

## Lifecycle — автоматы состояний

| Файл | Что показывает |
|---|---|
| [subscription-nodes-sync.html](subscription-nodes-sync.html) | `pending_add → active ⇄ pending_update → removed` + retry-очередь с backoff |
| [order-lifecycle.html](order-lifecycle.html) | `pending → paid / expired / canceled / chargeback`: grace 5 минут, атомарный downgrade |
| [subscription-lifecycle.html](subscription-lifecycle.html) | `created → active ⇄ expired`, отзыв `revoked` без физического удаления |

## Workflow — процессы

| Файл | Что показывает |
|---|---|
| [ci-pipeline.html](ci-pipeline.html) | CI: test → vet → golangci-lint → gosec → Docker → ghcr.io, red-CI стоп |
| [release-flow.html](release-flow.html) | Релиз: SemVer-тег → пайплайн → образ → GitHub Release → deploy → healthcheck |

## Dataflow — потоки данных

| Файл | Что показывает |
|---|---|
| [traffic-accounting.html](traffic-accounting.html) | Учёт трафика: 90%/exhausted предупреждения, bitmask `traffic_reminders_sent`, ре-включение после сброса |
| [config-aggregation.html](config-aggregation.html) | Агрегация конфигов: 3 типа нод → fetch → детект формата → merge → кэш → клиент |

## Регенерация

Спецификации (`*.json`) лежат рядом с HTML. Пересборка любой диаграммы:

```bash
node <path-to-archify>/bin/archify.mjs deliver <type> <spec.json> <output.html> --quality showcase
```

где `<type>` — `architecture` / `sequence` / `lifecycle` / `workflow` / `dataflow`
(тип виден в суффиксе файла спецификации или в поле `diagram_type`).

Для `architecture` с source-evidence добавляется `--repo-root <путь к репозиторию>` —
пути в `sources[]` проверяются на соответствие зафиксированному revision.
