# Диаграммы проекта rs8kvn_bot

Интерактивные standalone-диаграммы [Archify](https://github.com/tt-a1i/archify). Каждая
диаграмма — самодостаточный HTML: темы dark/light, pan/zoom, поиск по узлам,
трассировка связей, интерактивные сценарии (`meta.views`), экспорт PNG/SVG/WebM.
Файлы открываются в браузере без сервера.

Набор пересобран по коду репозитория на revision
`300b5646189ef287ff268095da4183ff17858bbe`. JSON-спецификации лежат рядом с HTML;
ссылки на исходники в них проверяются против зафиксированного revision.

## Система и эксплуатация

| Файл | Тип | Что показывает |
|---|---|---|
| [project-map.html](project-map.html) | architecture | Компоненты и границы: Telegram/HTTP, service layer, workers, SQLite, VPN-адаптеры, внешние платёжные системы |
| [runtime-supervision.html](runtime-supervision.html) | architecture | Runtime-контур: fast-lane pre-checkout, financial mailbox, bounded handlers, отдельный payment BotAPI и supervisor воркеров |
| [ci-pipeline.html](ci-pipeline.html) | workflow | PR/tag pipeline: race-тесты, vet, build, golangci-lint, gosec, Docker и публикация в GHCR |
| [release-flow.html](release-flow.html) | workflow | Фактический релиз по SemVer-тегу: проверки → образ → GitHub Release |
| [audit-log.html](audit-log.html) | dataflow | Append-only журнал: entity/actor/event, old/new state, best-effort и транзакционные исключения, retention |

## Подписки и выдача доступа

| Файл | Тип | Что показывает |
|---|---|---|
| [subscription-intake.html](subscription-intake.html) | sequence | Создание подписки: обязательный DB-setup, best-effort sync нод, выдача ссылки |
| [sub-serving.html](sub-serving.html) | sequence | `GET /sub/{subID}`: кэш/singleflight, параллельный fetch, определение формата, клиентский ответ |
| [subscription-nodes-sync.html](subscription-nodes-sync.html) | lifecycle | `pending_add → active ⇄ pending_update → removed`, retry/backoff и восстановление очереди |
| [subscription-teardown.html](subscription-teardown.html) | sequence | `/del`: revoke → депровизия → физическое удаление после подтверждённой депровизии |
| [config-aggregation.html](config-aggregation.html) | dataflow | Источники конфигов, конвертация форматов, traffic/userinfo, merge и кэш |
| [traffic-accounting.html](traffic-accounting.html) | dataflow | Сбор трафика, 90%/100% пороги, idempotent bitmask и безопасное повторное включение |

## Продажи и платежи

| Файл | Тип | Что показывает |
|---|---|---|
| [order-lifecycle.html](order-lifecycle.html) | lifecycle | Состояния заказа, CAS-подтверждение, истечение, отмена, chargeback и повторная доставка callback |
| [subscription-lifecycle.html](subscription-lifecycle.html) | lifecycle | Реальная политика срока, downgrade, reanimation и revoke без обычного физического удаления |
| [stars-payment.html](stars-payment.html) | sequence | Telegram Stars: согласие, invoice, pre-checkout, successful payment, идемпотентность и авто-возврат |
| [platega-payment-lifecycle.html](platega-payment-lifecycle.html) | sequence | Legacy Platega callback: guard-цепочка, per-order lock, CAS и post-commit sync |
| [commerce-model.html](commerce-model.html) | erd | Физическая модель commerce-домена: subscriptions, orders, products, plans, nodes и audit log |
| [product-catalog.html](product-catalog.html) | tree | Иерархия возможностей и продаваемых продуктов, привязанная к планам и нодам |
| [payment-providers.html](payment-providers.html) | class | Provider-neutral контракт оплаты и реализации Platega/Telegram Stars вокруг `OrderService` |

## Маркетинг, продажи и коммуникации

| Файл | Тип | Что показывает |
|---|---|---|
| [marketing-funnel.html](marketing-funnel.html) | workflow | Код-подтверждённая воронка: invite/referral → trial → free → premium → renewal/upsell |
| [customer-journey.html](customer-journey.html) | lifecycle | Путь клиента от первого контакта до активации, использования, напоминаний и реактивации |
| [referral-lifecycle.html](referral-lifecycle.html) | lifecycle | Invite-код, переход по ссылке, trial request, привязка к Telegram и очистка |
| [broadcast.html](broadcast.html) | sequence | `/broadcast`: таргетинг, snapshot аудитории, claim/retry, resume, отчёт и отмена |
| [trial-invite.html](trial-invite.html) | sequence | `/i/{code}`: валидация, rate-limit, лендинг, deep link и race-safe привязка триала |

## Регенерация

Спецификации (`*.json`) лежат рядом с HTML. Основной проверяемый путь:

```bash
node /home/kereal/.agents/skills/archify/bin/archify.mjs finalize \
  <type> <spec.json> <output.html> \
  --repo-root /home/kereal/rs8kvn_bot \
  --quality showcase \
  --json
```

`<type>` — `architecture` / `workflow` / `sequence` / `dataflow` / `lifecycle` /
`erd` / `tree` / `class`. `finalize` проверяет схему, source-evidence, доставляет
HTML и прогоняет browser-check. При повторной доставке существующего HTML укажите
отдельный `--out-dir .archify/finalize/<slug>/review-N`, чтобы не перезаписать
привязанные к предыдущей версии evidence-файлы.
