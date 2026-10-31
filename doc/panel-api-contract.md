# Контракт API панели 3x-ui

Проверенные факты о панели, с которой работает бот. Источники: живая панель
(`https://panel.example.net/pnl`, доступ через Bearer-токен ноды) и исходники
панели `MHSanaei/3x-ui` **тега `v3.11.0`** (`internal/web/service/client_crud.go`,
`internal/web/controller/api.go`, `internal/web/controller/client.go`,
`internal/web/service/inbound_traffic.go`, `internal/web/service/panel/api_token.go`).
Дата проверки: **2026-10-31**. Живая панель: `currentVersion: 3.11.0`
(`GET /panel/api/server/getPanelUpdateInfo`, Xray `26.10.21`) — проверка шла
прямо на целевой версии.

Документ существует, чтобы контракт не пришлось выяснять заново по живому
доступу к панели. Всё, что ниже помечено «проверено», воспроизводится
read-only запросами или из исходников панели.

## 1. Конверт ответа

Все клиентские эндпоинты отвечают **HTTP 200**, включая ошибки. Тела ответа:

```json
{ "success": true|false, "msg": "текст ошибки", "obj": {} | null }
```

Структурных кодов ошибок (`code`, `status`, тип ошибки) **нет** — только
`success` + `msg`. Поэтому классификация ошибок в боте опирается на текст `msg`;
это осознанный контракт, а не временная заглушка. Соответствующие строки
перечислены в разделе 3 и закреплены тестами в `internal/xui`.

## 2. Эндпоинты, которые использует бот

| Эндпоинт | Метод | Назначение в боте |
|---|---|---|
| `/panel/api/clients/add` | POST | создание клиента (`xui.Client.AddClientWithID`) |
| `/panel/api/clients/update/{email}` | POST | обновление клиента (`xui.Client.UpdateClient`) |
| `/panel/api/clients/del/{email}` | POST | удаление клиента (`xui.Client.DeleteClient`) |
| `/panel/api/clients/resetTraffic/{email}` | POST | сброс счётчика трафика (`xui.Client.ResetTraffic`) |
| `/panel/api/clients/traffic/{email}` | GET | чтение трафика (`xui.Client.GetClientTraffic`) |
| `/panel/api/clients/get/{email}` | GET | **полная карточка клиента**, включая профиль авто-renew (`xui.Client.GetClientByEmail`) |
| `/panel/api/inbounds/get/{id}` | GET | чтение инбаунда (`xui.Client.GetInbound`) |
| `/panel/api/server/status` | GET | проверка живости панели (`xui.Client.Ping`) |

### 2.1. Форма `GET /panel/api/clients/get/{email}` (проверено)

```json
{
  "success": true,
  "msg": "",
  "obj": {
    "client": { "email": "...", "reset": 30, "resetDay": 0, "resetMax": 0,
                "resetWeekday": 0, "trafficReset": "monthly", "trafficResetDay": 1,
                "enable": true, "totalGB": 128849018880, "expiryTime": 1795899600000,
                "subId": "...", "uuid": "...", "tgId": 0, "comment": "..." },
    "externalLinks": [],
    "inboundIds": [2, 4, 5],
    "tunnelAllowedIPs": null,
    "usedTraffic": null
  }
}
```

Клиент лежит **внутри `obj.client`**, не в `obj` — это обязательно к учёту при
разборе ответа. Эндпоинт отдаёт **полный профиль авто-renew**, поэтому
`GET /clients/get/{email}` — единственный правильный способ прочитать
`trafficReset`/`trafficResetDay`; знать `inboundId` для этого не требуется.

## 3. Точные тексты ошибок

Проверено на живой панели 2026-10-31 (для несуществующего email) и по
исходникам панели.

| Операция | Условие | `msg` | HTTP |
|---|---|---|---|
| `del/{email}` | клиента нет | `Something went wrong (client "<email>" not found in any inbound or client record\n)` | 200 |
| `update/{email}` | клиента нет | `Something went wrong (record not found)` | 200 |
| `resetTraffic/{email}` | клиента нет | `Something went wrong (record not found)` | 200 |
| `get/{email}` | клиента нет | `Obtain (record not found)` | 200 |
| `traffic/{email}` | клиента нет | `success=true`, `obj=null`, `msg=""` | 200 |
| `add` | email занят | `email already in use: <email>` (из исходников панели) | 200 |
| `add` | subId занят | `subId already in use: <subId>` (из исходников панели) | 200 |

**Особый случай `traffic/{email}`:** панель отвечает `success=true` и `obj=null`,
а не `success=false`. Отсутствие клиента выражается пустым `obj`, поэтому
`xui.doGetClientTraffic` обязан трактовать `obj == null` как «клиента нет»
(`xui.ErrClientNotFound`) — иначе вызывающий получит nil-указатель или нули
трафика вместо ошибки.

## 4. Особенности поведения панели

### 4.1. `add` идемпотентен при совпадающем subId

`ClientService.Create` (исходник панели) при уже существующем email:

- если `subId` **совпадает** — ошибки нет: панель переиспользует сохранённые
  `uuid`/`password`/`auth` и переписывает настройки клиента;
- если `subId` **отличается** — `email already in use: <email>`.

Бот всегда шлёт стабильный `subId` подписки, поэтому повторный `add` того же
клиента на той же ноде **не является ошибкой панели**. Ветка
`vpn.ErrSubscriptionAlreadyExists` в `SyncService.processPendingAdd` срабатывает
тогда, когда `subId` разошёлся (например, после ручной правки на панели) — и
это правильный сигнал «синхронизируй конфигурацию через update».

### 4.2. Профиль авто-renew и его нормализация

Панель валидирует и нормализует профиль (`client_crud.go`):

- `trafficReset` ∈ {`""`, `never`, `hourly`, `daily`, `weekly`, `monthly`};
  пустое значение нормализуется в `never`;
- `trafficResetDay` ∈ [0, 31], `resetDay` ∈ [0, 31], `resetMax` ≥ 0;
- недельное продление (`resetWeekday > 0`) несовместимо с `reset > 0` или
  `resetDay > 0`.

`GET /clients/traffic/{email}` **не отдаёт** `trafficReset` и
`trafficResetDay` (в ответе есть `reset`, `resetDay`, `resetMax`,
`resetWeekday`, но не `trafficReset*`). Поэтому перед `UpdateClient` профиль
обязательно перечитывается через `GET /clients/get/{email}` — иначе панель
затрёт его значением `never`, и месячный сброс трафика перестанет срабатывать.

### 4.3. Валидация на стороне панели

Панель отклоняет некорректные значения (`common.NewError`), а не чинит их:
пустой email, запрещённые символы в email/subId (`/`, `\`, пробелы, управляющие),
`resetMax < 0`, `trafficResetDay` вне [0, 31]. Ошибки приходят в `msg` с
HTTP 200 — поэтому панельный адаптер обязан разбирать `msg`, а не полагаться на
код ответа.

## 4.4. Скоупы API-токенов (v3.11.0)

Bearer-токены панели имеют скоуп: `admin` (по умолчанию, `gorm default:admin`),
`monitor`, `node-sync`, `node-admin`. Скоупы кроме `admin` режут запросы по
allowlist'у (`nodeSyncScopeAllow`/`monitorScopeAllow` в
`internal/web/controller/api.go`); путь вне allowlist'а → **HTTP 403** с
`msg: "this API token is not permitted to access this endpoint"`.

**В allowlist `node-sync` НЕ входят** `/clients/get/{email}` и
`/clients/traffic/{email}` — чтение карточки и трафика клиента на этих скоупах
невозможно. Бот читает профиль и трафик (группа B), поэтому:

- токен ноды в базе бота должен иметь скоуп `admin` (дефолт `NormalizeScope`);
  проверено живой пробой на v3.11.0: `clients/get` с токеном ноды отвечает 200;
- при смене скоупа на `node-sync`/`monitor` бот получит 403 на все операции с
  клиентами; адаптер классифицирует не-200 как `utils.ErrNon200Response`
  (не ретрабельный — раздел 5), синхронизация узла деградирует с Warn в логе.

## 5. Как это реализовано в боте

1. `xui.Client.GetClientByEmail(ctx, email) (*xui.ClientRecord, error)` читает
   `GET /panel/api/clients/get/{email}` и разбирает `obj.client` и
   `obj.inboundIds` (тип `xui.ClientRecord{Client ClientConfig; InboundIDs []int}`).
   Сканирования `settings` инбаунда больше нет; параметр `inboundID` из
   сигнатуры (`interfaces.XUIClientReader`) удалён.
2. Классификация ошибок панели живёт в одном месте — `internal/xui/classify.go`
   (функция `classifyFailure` + разбор конверта `parseEnvelope`), применяется
   на всех операциях add/update/del/resetTraffic/traffic/get и возвращает
   sentinel-ошибки `xui.ErrClientNotFound` и `xui.ErrClientAlreadyExists`.
   `internal/vpn` маппит sentinel'ы в `vpn.ErrSubscriptionNotFound` /
   `vpn.ErrSubscriptionAlreadyExists` (`mapPanelError` в `threex_ui.go`);
   proxman маппит статусы webhook напрямую, без строковой классификации.
3. Мёртвые sentinel-ошибки (`ErrUnauthorized`, `ErrForbidden`, `ErrBadRequest`,
   `ErrNotFound`, `ErrServerError`) из `internal/xui/errors.go` удалены.
   Sentinel не-200 ответа один: `utils.ErrNon200Response` (владелец —
   `internal/utils`; `internal/xui` импортирует utils, обратной зависимости
   нет). `utils.IsRetryable` возвращает для него `false` — не-200 не
   ретраится (решение о ретраях закреплено тестом).

## 6. Как читать профиль авто-renew

Профиль авто-renew (`reset`, `resetDay`, `resetMax`, `resetWeekday`,
`trafficReset`, `trafficResetDay`) отдаёт **только** `GET /clients/get/{email}`
(в `obj.client`, см. раздел 2.1).

`GET /clients/traffic/{email}` профиль **не отдаёт**: в `ClientTraffic` есть
`reset`, `resetDay`, `resetMax`, `resetWeekday`, но нет `trafficReset*`.
Правило чтения:

- для обновления клиента (`UpdateClient`) профиль всегда перечитывается через
  `GET /clients/get/{email}` и передаётся в запрос целиком; список inbound'ов
  для обновления берётся из `obj.inboundIds` этого же ответа;
- пропуск профиля недопустим: панель не мержит профиль, отсутствующий
  `trafficReset` записывается как `never` (раздел 4.2), и периодический сброс
  трафика перестаёт срабатывать.

В коде это `xui.ClientRecord` и путь восстановления после сброса трафика
`SubscriptionService.reenableResetClients` (internal/service/
subscription_traffic_notifications.go): профиль — из `record.Client`,
inbound'ы для `UpdateClient` — из `record.InboundIDs` с фолбэком на
сконфигурированные inbound'ы узла, если панель вернула пустой список.

## 7. Как проверить контракт без доступа к панели

Контракт закреплён тестами `internal/xui` на фикстурах-ответах панели
(`httptest`), см. `internal/xui/panel_contract_test.go`. Живая панель нужна
только для перепроверки при обновлении панели.
