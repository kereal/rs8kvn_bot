# RFC: Proxy Router Manager — Архитектура и План Реализации

**Статус:** Утверждено
**Версия:** 5.0 (Final)
**Дата:** 2026-04-06
**Язык реализации:** Go
**Тип:** Отдельный standalone-сервис (демон)
**Развёртывание:** Выделенный сервер (роутер) — отдельно от сервера rs8kvn_bot
**Xray версия:** v26.3.23 (последняя стабильная)

---

## 1. Резюме

Proxy Router Manager — автономная Go-система управления доступом к внешним прокси-серверам. Управляет Xray Core через gRPC API (Hot Reload), раздаёт конфигурации клиентам через HTTP API, собирает статистику трафика.

**Архитектура:** 2 сервера — rs8kvn_bot (backend) + роутер (Proxy Manager + Xray).

**Ключевые решения:**

1. **Reconciliation** — desired state из источников (rs8kvn_bot API, Remnawave), применяется идемпотентно к Xray
2. **Без 3X-UI** — Reality ключи генерирует менеджер, прокси из Remnawave, пользователи из бота
3. **OverrideRules** — полная замена routing rules, гарантирует порядок (пользователи → geoip:private → block)
4. **QueryStats(reset: true)** — сброс counters после чтения, нет риска дублирования
5. **Масштаб:** до 100 пользователей × ~12 прокси = ~1200 слотов
6. **Remnawave:** VLESS+Reality ноды
7. **Happ:** JSON формат подписки

---

## 2. Мотивация

### 2.1 Проблема

rs8kvn_bot управляет подписками, но не маршрутизирует трафик. Нужна система, которая:

1. **Маршрутизирует** — каждый пользователь → свой outbound по UUID
2. **Не разрывает** — Hot Reload через gRPC, без перезапусков
3. **Самовосстанавливается** — при рестарте Xray автоматически восстанавливает всё
4. **Считает трафик** — агрегация по каждому пользователю

### 2.2 Переиспользование из rs8kvn_bot

| Компонент | Файл | Что берём |
|-----------|------|-----------|
| Circuit Breaker | `internal/xui/breaker.go` | Closed→Open→Half-Open |
| Retry | `internal/xui/client.go:747-787` | `RetryWithBackoff()` |
| Ошибки | `internal/xui/client.go:724-745` | `isRetryable()` |
| Rate Limiter | `internal/ratelimiter/ratelimiter.go` | Token Bucket |
| SQLite | `internal/database/database.go` | GORM + WAL mode |
| Config | `internal/config/config.go` | Env + валидация |
| Logger | `internal/logger/logger.go` | Structured zap |
| Heartbeat | `internal/heartbeat/heartbeat.go` | Ticker + graceful shutdown |
| Subparser | `internal/subproxy/proxy.go` | DetectFormat, MergeSubscriptions |

---

## 3. Архитектура

### 3.1 Схема развёртывания (2 сервера)

```
┌──────────────────────────────────────────────────────────────┐
│               Server A — Backend (rs8kvn_bot)                │
│  ┌──────────────────┐                                        │
│  │  rs8kvn_bot      │  HTTP API: /api/v1/users, /api/v1/stats│
│  │  (Telegram Bot)  │  SQLite: пользователи, платежи         │
│  └──────────────────┘                                        │
└───────────────────────────┬──────────────────────────────────┘
                            │ HTTPS (REST API, Bearer token)
                            ▼
┌──────────────────────────────────────────────────────────────┐
│               Server B — Router (Proxy Manager)              │
│  ┌──────────────────┐    ┌──────────────────┐               │
│  │  Proxy Manager   │───▶│  Xray Core       │               │
│  │  (Go сервис)     │gRPC│  VLESS+Reality   │               │
│  │  :8080 HTTP      │    │  :443 inbound    │               │
│  └────────┬─────────┘    └──────────────────┘               │
│           │                                                  │
│           │ HTTPS (подписка)                                 │
│           ▼                                                  │
│  ┌──────────────────┐                                        │
│  │  Nginx/Caddy     │  TLS termination                       │
│  └──────────────────┘                                        │
└──────────────────────────────────────────────────────────────┘
                            ▲
                            │ HTTPS
                  ┌─────────┴──────────────┐
                  │  Remnawave Sub URL     │
                  │  VLESS+Reality ноды    │
                  └────────────────────────┘
```

### 3.2 Data Flow

```mermaid
flowchart TD
    subgraph "Server A — rs8kvn_bot"
        A1[HTTP API /api/v1/users]
    end

    subgraph "Внешние источники"
        B1[Remnawave Sub URL]
    end

    subgraph "Server B — Proxy Manager"
        RE[Reconciliation Engine\n(каждые 5 мин)]
        SE[State Engine\n(In-Memory + SQLite)]
        XC[Xray gRPC Client]
        HS[HTTP Server\n(подписка + metrics)]
        SC[Stats Collector\n(каждую 1 мин)]

        RE -- "fetch users" --> A1
        RE -- "fetch proxies" --> B1
        RE -- "desired state" --> SE
        RE -- "apply diff" --> XC
        HS -- "read state" --> SE
        SC -- "query stats" --> XC
        SC -- "persist" --> SE
    end

    subgraph "Xray Core"
        XI[Main Inbound\nVLESS+Reality :443]
        XO[Outbounds\nDynamic]
        XR[Routing Rules]
        XS[Stats Counter]
    end

    XC -- "AlterInbound (Add/Remove User)" --> XI
    XC -- "Add/Remove Outbound" --> XO
    XC -- "OverrideRules" --> XR
    XC -- "QueryStats(reset: true)" --> XS

    subgraph "Клиенты"
        K[Happ App]
    end

    K -- "GET /sub/:token" --> HS
    K -- "VLESS+Vision :443" --> XI
    XI -- "route by UUID" --> XO
```

### 3.3 Принцип работы

1. **Идентификация:** Пользователи из rs8kvn_bot API → `base_uuid`
2. **Виртуализация:** Для каждого пользователя → набор слотов (по одному на прокси). `SlotUUID = UUIDv5(base_uuid + proxy_tag)`
3. **Reconciliation (каждые 5 мин):** Desired state → diff → идемпотентный apply → persist
4. **Stats (каждую 1 мин):** QueryStats(reset: true) → агрегация → persist
5. **Health (каждые 10с):** ListInbounds → если ошибка → exponential backoff → Full Sync при восстановлении

---

## 4. Модель данных

### 4.1 In-Memory State

```go
type State struct {
    mu      sync.RWMutex
    users   map[string]*User      // base_uuid -> User
    proxies map[string]*Proxy     // tag -> Proxy
    slots   map[string]*Slot      // slot_uuid -> Slot
    system  SystemConfig          // Reality keys, port, server IP
    stats   map[string]*UserStats // base_uuid -> aggregated stats
}

type User struct {
    BaseUUID  string
    Email     string
    Enabled   bool
    SubToken  string    // токен для /sub/:token
    TotalUp   int64
    TotalDown int64
    LastSeen  time.Time
}

type Proxy struct {
    Tag            string          // "de-node-1"
    CountryCode    string          // "DE", "US"
    CountryName    string          // "Germany"
    Address        string
    Port           int
    StreamSettings json.RawMessage  // VLESS+Reality конфиг
    IsActive       bool
    Priority       int
}

type Slot struct {
    SlotUUID string  // UUIDv5(base_uuid + proxy_tag)
    UserUUID string  // FK -> User.BaseUUID
    ProxyTag string  // FK -> Proxy.Tag
}

type SystemConfig struct {
    RealityPublicKey  string
    RealityPrivateKey string
    ShortIDs          []string
    ListenPort        int
    ServerIP          string
    ServerNames       []string
    Dest              string
}

type UserStats struct {
    BaseUUID   string
    TotalUp    int64
    TotalDown  int64
    PeriodUp   int64  // за последний период (сбрасывается)
    PeriodDown int64
    LastReset  time.Time
}
```

### 4.2 SQLite Schema

```sql
CREATE TABLE IF NOT EXISTS users (
    base_uuid  TEXT PRIMARY KEY,
    email      TEXT NOT NULL DEFAULT '',
    sub_token  TEXT NOT NULL DEFAULT '',
    enabled    INTEGER NOT NULL DEFAULT 1,
    total_up   INTEGER NOT NULL DEFAULT 0,
    total_down INTEGER NOT NULL DEFAULT 0,
    last_seen  TEXT NOT NULL DEFAULT (datetime('now')),
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX idx_users_sub_token ON users(sub_token);

CREATE TABLE IF NOT EXISTS proxies (
    tag             TEXT PRIMARY KEY,
    country_code    TEXT NOT NULL DEFAULT '',
    country_name    TEXT NOT NULL DEFAULT '',
    address         TEXT NOT NULL DEFAULT '',
    port            INTEGER NOT NULL DEFAULT 443,
    stream_settings TEXT NOT NULL DEFAULT '{}',
    is_active       INTEGER NOT NULL DEFAULT 1,
    priority        INTEGER NOT NULL DEFAULT 0,
    created_at      TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at      TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS slots (
    slot_uuid TEXT PRIMARY KEY,
    user_uuid TEXT NOT NULL REFERENCES users(base_uuid) ON DELETE CASCADE,
    proxy_tag TEXT NOT NULL REFERENCES proxies(tag) ON DELETE CASCADE,
    UNIQUE(user_uuid, proxy_tag)
);
CREATE INDEX idx_slots_user ON slots(user_uuid);

CREATE TABLE IF NOT EXISTS user_stats (
    base_uuid   TEXT PRIMARY KEY REFERENCES users(base_uuid) ON DELETE CASCADE,
    total_up    INTEGER NOT NULL DEFAULT 0,
    total_down  INTEGER NOT NULL DEFAULT 0,
    period_up   INTEGER NOT NULL DEFAULT 0,
    period_down INTEGER NOT NULL DEFAULT 0,
    last_reset  TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at  TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS stats_history (
    id        INTEGER PRIMARY KEY AUTOINCREMENT,
    base_uuid TEXT NOT NULL REFERENCES users(base_uuid) ON DELETE CASCADE,
    ts        TEXT NOT NULL DEFAULT (datetime('now')),
    up        INTEGER NOT NULL DEFAULT 0,
    down      INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX idx_stats_history_user_ts ON stats_history(base_uuid, ts);

CREATE TABLE IF NOT EXISTS system_config (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS sync_log (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    ts              TEXT NOT NULL DEFAULT (datetime('now')),
    status          TEXT NOT NULL,
    users_added     INTEGER NOT NULL DEFAULT 0,
    users_removed   INTEGER NOT NULL DEFAULT 0,
    proxies_added   INTEGER NOT NULL DEFAULT 0,
    proxies_removed INTEGER NOT NULL DEFAULT 0,
    duration_ms     INTEGER NOT NULL DEFAULT 0,
    error           TEXT DEFAULT ''
);
```

### 4.3 ERD

```mermaid
erDiagram
    USER ||--o{ SLOT : generates
    PROXY ||--o{ SLOT : uses
    USER ||--|| USER_STATS : has
    USER ||--o{ STATS_HISTORY : records

    USER { string base_uuid PK; string email; string sub_token; bool enabled; int64 total_up; int64 total_down }
    PROXY { string tag PK; string country_code; string address; int port; bool is_active }
    SLOT { string slot_uuid PK; string user_uuid FK; string proxy_tag FK }
    USER_STATS { string base_uuid PK; int64 total_up; int64 total_down; int64 period_up; int64 period_down }
    STATS_HISTORY { int id PK; string base_uuid FK; datetime ts; int64 up; int64 down }
```

---

## 5. Конфигурация

```yaml
# config.yaml
xray:
  grpc_addr: "127.0.0.1:10085"
  inbound_tag: "main-in"
  grpc_timeout: "10s"
  required_version: "v26.3.23"
  reality:
    dest: "www.microsoft.com:443"
    server_names: ["www.microsoft.com", "google.com"]
    short_ids: ["", "0123"]

sources:
  rs8kvn_bot:
    base_url: "https://api.example.com"
    api_token: "${RS8KVN_API_TOKEN}"
    users_endpoint: "/api/v1/users"
    timeout: "15s"
    max_retries: 3

  remnawave:
    sub_url: "https://remnawave.example.com/sub/xxxxx"
    format: "base64"
    timeout: "15s"

reconciliation:
  interval: "5m"
  batch_size: 100
  batch_delay: "50ms"
  log_progress: true
  log_progress_interval: 10
  max_duration: "5m"
  single_flight: true

stats:
  interval: "1m"
  push_enabled: false
  push_endpoint: "/api/v1/stats/sync"

health:
  xray_interval: "10s"
  failure_threshold: 3
  backoff_initial: "1s"
  backoff_max: "30s"

http:
  listen: ":8080"
  sub_token: "${SUB_TOKEN}"
  admin_token: "${ADMIN_TOKEN}"
  rate_limit:
    enabled: true
    requests_per_second: 10
    burst: 20
  read_timeout: "10s"
  write_timeout: "10s"
  idle_timeout: "60s"

database:
  path: "./data/proxy_manager.db"
  wal_mode: true
  busy_timeout: "5s"

logging:
  level: "info"
  format: "json"
  output: "stdout"

metrics:
  enabled: true
  path: "/metrics"

backup:
  enabled: true
  interval: "1h"
  path: "./data/backups"
  max_backups: 24

shutdown:
  timeout: "30s"
```

### Переменные окружения

| Переменная | Описание | Обязательно |
|-----------|----------|-------------|
| `RS8KVN_API_TOKEN` | Токен для API rs8kvn_bot | Да |
| `SUB_TOKEN` | Токен для эндпоинта подписки | Да |
| `ADMIN_TOKEN` | Токен для admin эндпоинтов | Да |
| `PM_CONFIG` | Путь к config.yaml | Нет |

---

## 6. Интеграция с rs8kvn_bot API

**Роль:** Единственный источник данных о пользователях.

### GET /api/v1/users

```http
GET /api/v1/users HTTP/1.1
Authorization: Bearer <RS8KVN_API_TOKEN>
```

```json
{
  "users": [
    {
      "id": "550e8400-e29b-41d4-a716-446655440000",
      "email": "user@example.com",
      "enabled": true,
      "subscription_token": "abc123def456"
    }
  ]
}
```

| Поле | Тип | Описание |
|------|-----|----------|
| `id` | string (UUID) | `base_uuid` (ClientID из БД) |
| `email` | string | Username из БД |
| `enabled` | bool | `status = "active"` |
| `subscription_token` | string | SubscriptionID из БД |

### Phase 0 — API в rs8kvn_bot

Реализуется параллельно. Proxy Manager использует mock.

Использует `database.Service.GetAllSubscriptions()` → фильтрация `status = "active"` → маппинг полей.

---

## 7. Xray Core

### 7.1 config.json (создаётся один раз)

```json
{
  "log": { "loglevel": "warning" },
  "stats": {},
  "api": {
    "tag": "api",
    "services": ["HandlerService", "StatsService", "RouterService"]
  },
  "policy": {
    "system": { "statsInboundUplink": true, "statsInboundDownlink": true },
    "levels": { "0": { "statsUserUplink": true, "statsUserDownlink": true } }
  },
  "inbounds": [
    {
      "tag": "main-in",
      "port": 443,
      "protocol": "vless",
      "settings": { "clients": [], "decryption": "none" },
      "streamSettings": {
        "network": "tcp",
        "security": "reality",
        "realitySettings": {
          "show": false,
          "dest": "www.microsoft.com:443",
          "serverNames": ["www.microsoft.com", "google.com"],
          "privateKey": "<GENERATED_AT_FIRST_START>",
          "shortIds": ["", "0123"]
        }
      },
      "sniffing": { "enabled": true, "destOverride": ["http", "tls", "quic"] }
    },
    {
      "tag": "api",
      "listen": "127.0.0.1",
      "port": 10085,
      "protocol": "dokodemo-door",
      "settings": { "address": "127.0.0.1" }
    }
  ],
  "outbounds": [
    { "tag": "direct", "protocol": "freedom" },
    { "tag": "block", "protocol": "blackhole" }
  ],
  "routing": {
    "domainStrategy": "IPIfNonMatch",
    "rules": [
      { "type": "field", "inboundTag": ["api"], "outboundTag": "api" },
      { "type": "field", "ip": ["geoip:private"], "outboundTag": "block" }
    ]
  }
}
```

### 7.2 gRPC API

| Proto-файл | Сервис | Используемые методы |
|------------|--------|-------------------|
| `app/proxyman/command/command.proto` | HandlerService | `AlterInbound`, `AddOutbound`, `RemoveOutbound`, `ListInbounds` |
| `app/stats/command/command.proto` | StatsService | `QueryStats` |
| `app/router/command/router.proto` | RouterService | `OverrideRules` |

**Важно:** `AddUser`/`RemoveUser` — НЕ отдельные RPC. Передаются через `AlterInbound` с `AddUserOperation`/`RemoveUserOperation`.

### 7.3 Добавление пользователя (AlterInbound)

```go
func (c *XrayClient) AddUserSafe(ctx context.Context, inboundTag, email, slotUUID string) error {
    _, err := c.handlerClient.AlterInbound(ctx, &command.AlterInboundRequest{
        Tag: inboundTag,
        Operation: serial.ToTypedMessage(&command.AddUserOperation{
            User: &protocol.User{
                Level: 0, Email: email,
                Account: serial.ToTypedMessage(&vless.Account{
                    Id: slotUUID, Flow: "xtls-rprx-vision",
                }),
            },
        }),
    })
    if err != nil && !isAlreadyExistsError(err) {
        return fmt.Errorf("AlterInbound AddUser %s: %w", email, err)
    }
    return nil
}
```

### 7.4 Удаление пользователя (AlterInbound)

```go
func (c *XrayClient) RemoveUserSafe(ctx context.Context, inboundTag, email string) error {
    _, err := c.handlerClient.AlterInbound(ctx, &command.AlterInboundRequest{
        Tag: inboundTag,
        Operation: serial.ToTypedMessage(&command.RemoveUserOperation{Email: email}),
    })
    if err != nil && !isNotFoundError(err) {
        return fmt.Errorf("AlterInbound RemoveUser %s: %w", email, err)
    }
    return nil
}
```

### 7.5 OverrideRules (полная замена)

```go
func (c *XrayClient) OverrideRules(ctx context.Context, rules []*routerCommand.RoutingRule) error {
    _, err := c.routerClient.OverrideRules(ctx, &routerCommand.OverrideRulesRequest{
        Rule: rules,
    })
    return err
}

func BuildRoutingRules(slots []*Slot) []*routerCommand.RoutingRule {
    rules := make([]*routerCommand.RoutingRule, 0, len(slots)+2)
    // 1. API rule (всегда первый)
    rules = append(rules, &routerCommand.RoutingRule{
        Type: routerCommand.RoutingRule_Field,
        InboundTag: []string{"api"}, OutboundTag: "api",
    })
    // 2. User rules
    for _, s := range slots {
        rules = append(rules, &routerCommand.RoutingRule{
            Type: routerCommand.RoutingRule_Field,
            User: []string{s.SlotUUID}, OutboundTag: s.ProxyTag,
        })
    }
    // 3. Default deny (всегда последний)
    rules = append(rules, &routerCommand.RoutingRule{
        Type: routerCommand.RoutingRule_Field,
        Ip: []string{"geoip:private"}, OutboundTag: "block",
    })
    return rules
}
```

### 7.6 Helper-функции

```go
func isAlreadyExistsError(err error) bool {
    if st, ok := status.FromError(err); ok { return st.Code() == codes.AlreadyExists }
    return strings.Contains(err.Error(), "already exists")
}

func isNotFoundError(err error) bool {
    if st, ok := status.FromError(err); ok { return st.Code() == codes.NotFound }
    return strings.Contains(err.Error(), "not found")
}
```

### 7.7 gRPC инициализация

```go
func NewXrayClient(addr string) (*grpc.ClientConn, error) {
    return grpc.Dial(addr,
        grpc.WithTransportCredentials(insecure.NewCredentials()),
        grpc.WithBlock(),
        grpc.WithTimeout(10*time.Second),
    )
}
```

### 7.8 VLESS+Reality Outbound

```json
{
  "tag": "de-node-1",
  "protocol": "vless",
  "settings": {
    "vnext": [{
      "address": "de-proxy.example.com", "port": 443,
      "users": [{ "id": "proxy-server-uuid", "flow": "xtls-rprx-vision", "encryption": "none" }]
    }]
  },
  "streamSettings": {
    "network": "tcp", "security": "reality",
    "realitySettings": {
      "serverName": "www.microsoft.com",
      "publicKey": "ProxyServerPublicKey",
      "shortId": "", "spiderX": "/"
    }
  }
}
```

### 7.9 Порядок apply

```
1. AddOutbound (новые прокси)
2. OverrideRules (полная замена: api → user rules → block)
3. AlterInbound AddUser (новые слоты)
4. AlterInbound RemoveUser (удалённые слоты)
5. RemoveOutbound (удалённые прокси)
```

---

## 8. Reconciliation Engine

### 8.1 Алгоритм

```
┌─────────────────────────────────────────────────────┐
│         Reconciliation Cycle (5 min)                │
├─────────────────────────────────────────────────────┤
│  1. BUILD DESIRED STATE                             │
│     ├── GET rs8kvn_bot → пользователи               │
│     ├── GET Remnawave → прокси                      │
│     └── UUIDv5 → слоты                              │
│                                                     │
│  2. LOAD LAST KNOWN (из SQLite)                     │
│                                                     │
│  3. COMPUTE DIFF (desired vs last known)            │
│                                                     │
│  4. APPLY (идемпотентно, батчами)                   │
│     ├── AddOutboundSafe                             │
│     ├── OverrideRules (полная замена)               │
│     ├── AddUserSafe                                 │
│     ├── RemoveUserSafe                              │
│     └── RemoveOutboundSafe                          │
│                                                     │
│  5. PERSIST (SQLite + sync_log)                     │
└─────────────────────────────────────────────────────┘
```

### 8.2 Реализация

```go
func (r *Reconciler) Reconcile(ctx context.Context) error {
    syncID := uuid.New().String()
    logger.Info("Reconciliation started", zap.String("sync_id", syncID))

    desired, err := r.buildDesiredState(ctx)
    if err != nil { return fmt.Errorf("build desired state: %w", err) }

    lastKnown, err := r.state.LoadLastKnown(ctx)
    if err != nil { lastKnown = NewEmptyState() }

    diff := computeDiff(desired, lastKnown)
    if diff.IsEmpty() {
        logger.Info("No changes needed", zap.String("sync_id", syncID))
        return nil
    }

    if err := r.applyDiff(ctx, diff, syncID); err != nil {
        return fmt.Errorf("apply diff: %w", err)
    }

    if err := r.state.Persist(ctx, desired); err != nil {
        logger.Warn("Failed to persist state", zap.Error(err))
    }

    logger.Info("Reconciliation completed", zap.String("sync_id", syncID))
    return nil
}
```

### 8.3 Батчинг

```go
func (r *Reconciler) applyDiff(ctx context.Context, diff *Diff, syncID string) error {
    total := diff.TotalOperations()
    completed := 0

    reportProgress := func() {
        if r.config.LogProgress && completed%r.config.LogProgressInterval == 0 {
            logger.Info("Sync progress",
                zap.String("sync_id", syncID),
                zap.Int("completed", completed), zap.Int("total", total))
        }
    }

    // Phase 1: Add outbounds
    for _, batch := range chunks(diff.AddOutbounds, r.config.BatchSize) {
        for _, ob := range batch {
            if err := r.xrayClient.AddOutboundSafe(ctx, ob); err != nil { return err }
            completed++
            reportProgress()
        }
        if err := sleepWithContext(ctx, r.config.BatchDelay); err != nil { return ctx.Err() }
    }

    // Phase 2: OverrideRules (полная замена всех правил)
    allRules := BuildRoutingRules(diff.AllSlots)
    if err := r.xrayClient.OverrideRules(ctx, allRules); err != nil {
        return fmt.Errorf("OverrideRules: %w", err)
    }
    completed += len(allRules)
    reportProgress()

    // Phase 3: Add users
    for _, batch := range chunks(diff.AddSlots, r.config.BatchSize) {
        for _, slot := range batch {
            email := fmt.Sprintf("%s@%s", slot.UserUUID, slot.ProxyTag)
            if err := r.xrayClient.AddUserSafe(ctx, "main-in", email, slot.SlotUUID); err != nil { return err }
            completed++
            reportProgress()
        }
        if err := sleepWithContext(ctx, r.config.BatchDelay); err != nil { return ctx.Err() }
    }

    // Phase 4: Remove users
    for _, batch := range chunks(diff.RemoveSlots, r.config.BatchSize) {
        for _, slot := range batch {
            email := fmt.Sprintf("%s@%s", slot.UserUUID, slot.ProxyTag)
            if err := r.xrayClient.RemoveUserSafe(ctx, "main-in", email); err != nil { return err }
            completed++
            reportProgress()
        }
        if err := sleepWithContext(ctx, r.config.BatchDelay); err != nil { return ctx.Err() }
    }

    // Phase 5: Remove outbounds
    for _, batch := range chunks(diff.RemoveOutbounds, r.config.BatchSize) {
        for _, ob := range batch {
            if err := r.xrayClient.RemoveOutboundSafe(ctx, ob.Tag); err != nil { return err }
            completed++
            reportProgress()
        }
        if err := sleepWithContext(ctx, r.config.BatchDelay); err != nil { return ctx.Err() }
    }

    return nil
}
```

---

## 9. Health Check

**Метод:** `ListInbounds` вместо gRPC health service (у Xray нет стандартного health check).

```
Каждые 10 секунд:
  1. ListInbounds() → если OK → reset backoff
  2. Если ошибка → exponential backoff (1s, 2s, 4s...)
  3. После 3 неудач → Xray "down"
  4. Если Xray был "down" и стал "up" → Full Sync
```

---

## 10. Stats Collector

```
Каждую 1 минуту:
  1. QueryStats(pattern: "", reset: true) → получаем counters И сбрасываем
  2. Маппинг slot_uuid → base_uuid
  3. Агрегация: userStats[base_uuid].PeriodUp += uplink
  4. Persist: UPDATE user_stats + INSERT INTO stats_history
  5. (Опционально) POST /api/v1/stats/sync → rs8kvn_bot
```

**reset: true** — counters сбрасываются после чтения. Проще, нет риска дублирования при крашах.

---

## 11. Генерация UUID-слотов

```go
func GenerateSlotUUID(baseUUID, proxyTag string) string {
    ns := uuid.MustParse("6ba7b810-9dad-11d1-80b4-00c04fd430c8")
    return uuid.NewSHA1(ns, []byte(baseUUID+":"+proxyTag)).String()
}
```

---

## 12. HTTP API

| Метод | Путь | Auth | Описание |
|-------|------|------|----------|
| GET | `/healthz` | Нет | Health check |
| GET | `/readyz` | Нет | Readiness check |
| GET | `/metrics` | Нет | Prometheus |
| GET | `/sub/:token` | Token | Подписка (JSON) |
| GET | `/admin/state` | Admin | Состояние |
| POST | `/admin/full-sync` | Admin | Full sync |
| GET | `/admin/stats` | Admin | Статистика |
| GET | `/admin/sync-log` | Admin | Журнал |

### GET /sub/:token

```json
[
  {
    "name": "🇩🇪 Germany 01",
    "type": "vless",
    "server": "1.2.3.4",
    "server_port": 443,
    "uuid": "slot-uuid-for-de-node-1",
    "flow": "xtls-rprx-vision",
    "security": "reality",
    "server_name": "www.microsoft.com",
    "reality_public_key": "RouterPublicKeyString",
    "short_id": ""
  }
]
```

404: `{"error": "subscription not found"}`
403: `{"error": "subscription disabled"}`

---

## 13. Безопасность

- `/sub/:token` — path token, rate limit 10 req/s
- `/admin/*` — Bearer token, rate limit 5 req/s
- Brute-force: 5 неудач/IP за 5 мин → бан 30 мин
- Reality private key — только в SQLite
- Все секреты — env variables

---

## 14. Мониторинг

### Prometheus метрики

```
proxy_manager_reconcile_duration_seconds    Histogram
proxy_manager_reconcile_total               Counter (status: success|partial|failed)
proxy_manager_reconcile_errors_total        Counter (error_type)
proxy_manager_users_count                   Gauge
proxy_manager_proxies_count                 Gauge
proxy_manager_slots_count                   Gauge
proxy_manager_xray_connected                Gauge (1/0)
proxy_manager_grpc_errors_total             Counter (method)
proxy_manager_http_requests_total           Counter (path, method, status)
proxy_manager_stats_collected_total         Counter
```

### Логирование

```json
{
  "level": "info", "ts": "2026-04-06T02:00:00Z",
  "caller": "reconciler.go:142", "msg": "reconciliation completed",
  "sync_id": "abc-123-def", "duration_ms": 1523,
  "users_added": 2, "users_removed": 0, "proxies_added": 1
}
```

### Alerting

- `xray_connected == 0` → Critical
- `reconcile_errors_total > 3 за 15 мин` → Warning
- `reconcile_duration_seconds > 120` → Warning

---

## 15. Обработка ошибок

| Тип | Пример | Действие |
|-----|--------|----------|
| Transient | Network timeout, gRPC Unavailable | Retry с backoff |
| Permanent | Invalid config, auth failure | Log + alert |
| Idempotent | AlreadyExists, NotFound | Считать успехом |
| State | Рассинхрон | Full Sync |

```go
func isRetryable(err error) bool {
    if err == nil { return true }
    if st, ok := status.FromError(err); ok {
        switch st.Code() {
        case codes.Unavailable, codes.DeadlineExceeded, codes.ResourceExhausted:
            return true
        case codes.AlreadyExists, codes.NotFound:
            return false
        }
    }
    var netErr net.Error
    if errors.As(err, &netErr) { return netErr.Timeout() }
    return false
}
```

### Graceful Shutdown

```
SIGTERM → остановить reconciliation → остановить stats → drain HTTP → закрыть gRPC → закрыть SQLite → exit 0
Timeout: 30s
```

---

## 16. Структура проекта

```
proxy-router-manager/
├── cmd/proxy-manager/main.go
├── internal/
│   ├── config/config.go
│   ├── state/state.go          # In-memory + SQLite
│   ├── reconcile/
│   │   ├── reconciler.go       # Engine
│   │   ├── diff.go             # Diff computation
│   │   └── rules.go            # BuildRoutingRules + OverrideRules
│   ├── xray/
│   │   ├── client.go           # gRPC client
│   │   ├── health.go           # ListInbounds health check
│   │   ├── idempotent.go       # AddUserSafe, RemoveUserSafe
│   │   ├── errors.go           # isAlreadyExistsError, isNotFoundError
│   │   └── config.go           # config.json generator
│   ├── stats/collector.go      # QueryStats(reset: true)
│   ├── sources/
│   │   ├── rs8kvn_bot.go       # API client
│   │   └── remnawave.go        # VLESS+Reality parser
│   ├── http/
│   │   ├── server.go
│   │   ├── subscription.go     # /sub/:token
│   │   ├── admin.go
│   │   └── middleware.go       # Auth, rate limit
│   ├── models/                 # User, Proxy, Slot, SystemConfig
│   ├── breaker/breaker.go      # Circuit breaker
│   └── metrics/metrics.go      # Prometheus
├── pkg/uuid5/uuid5.go
├── testdata/
│   ├── remnawave/sub_base64.txt
│   └── subscription/response_multi.json
├── config.yaml.example
├── Dockerfile
├── docker-compose.yml
├── Makefile
├── go.mod
└── README.md
```

---

## 17. Зависимости

```go
require (
    github.com/xtls/xray-core v26.3.23
    github.com/google/uuid v1.6.0
    modernc.org/sqlite v1.30.x
    github.com/spf13/viper v1.19.x
    go.uber.org/zap v1.27.x
    github.com/go-chi/chi/v5 v5.0.x
    golang.org/x/time v0.5.x
    github.com/prometheus/client_golang v1.19.x
    google.golang.org/grpc v1.62.x
    google.golang.org/protobuf v1.33.x
    gopkg.in/yaml.v3 v3.0.1
    golang.org/x/sync v0.7.x
)
```

---

## 18. Контейнеризация

### Dockerfile

```dockerfile
FROM golang:1.23-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /proxy-manager ./cmd/proxy-manager

FROM alpine:3.20
RUN apk --no-cache add ca-certificates
WORKDIR /app
COPY --from=builder /proxy-manager .
COPY config.yaml.example ./config.yaml
EXPOSE 8080
ENTRYPOINT ["./proxy-manager"]
```

### docker-compose.yml

```yaml
version: "3.8"
services:
  proxy-manager:
    build: .
    ports:
      - "8080:8080"
    volumes:
      - ./data:/app/data
      - ./config.yaml:/app/config.yaml:ro
    environment:
      - RS8KVN_API_TOKEN=${RS8KVN_API_TOKEN}
      - SUB_TOKEN=${SUB_TOKEN}
      - ADMIN_TOKEN=${ADMIN_TOKEN}
    restart: unless-stopped
    healthcheck:
      test: ["CMD", "wget", "--spider", "-q", "http://localhost:8080/healthz"]
      interval: 30s
      timeout: 5s
      retries: 3
```

---

## 19. Тестирование

| Пакет | Что тестируем |
|-------|--------------|
| `internal/reconcile/diff` | Корректность diff |
| `internal/state` | CRUD, `-race` |
| `internal/xray/idempotent` | AlreadyExists → nil, NotFound → nil |
| `internal/xray/rules` | OverrideRules порядок |
| `pkg/uuid5` | Детерминированность |
| `internal/sources/remnawave` | Парсинг VLESS+Reality |
| `internal/http/subscription` | JSON формат, 404/403 |

Integration: mock gRPC (`bufconn`), `httptest.Server`
Golden files: `testdata/`
Coverage: 80%+, `go test -race ./...`

---

## 20. Развёртывание

### Сервер-роутер

CPU: 2 cores | RAM: 512 MB | Disk: 5 GB | OS: Ubuntu 22.04+
Ports: 443 (Xray), 8080 (HTTP), 10085 (gRPC, localhost)

### systemd

```ini
[Unit]
Description=Proxy Router Manager
After=network.target xray.service
Wants=xray.service

[Service]
Type=simple
User=proxy-manager
WorkingDirectory=/opt/proxy-manager
ExecStart=/opt/proxy-manager/proxy-manager -config /etc/proxy-manager/config.yaml
Restart=on-failure
RestartSec=5
EnvironmentFile=/etc/proxy-manager/.env
NoNewPrivileges=true
ProtectSystem=strict
ReadWritePaths=/opt/proxy-manager/data

[Install]
WantedBy=multi-user.target
```

### Backup

```yaml
backup:
  enabled: true
  interval: "1h"
  path: "./data/backups"
  max_backups: 24
```

```bash
# cron @hourly
sqlite3 ./data/proxy_manager.db ".backup './data/backups/backup-$(date +\%Y\%m\%d-\%H\%M).db'"
find ./data/backups -name '*.db' -mtime +1 -delete
```

### Nginx

```nginx
server {
    listen 443 ssl;
    server_name proxy-manager.example.com;
    location /sub/ { proxy_pass http://127.0.0.1:8080; }
    location /healthz { proxy_pass http://127.0.0.1:8080; }
    location /metrics { allow 10.0.0.0/8; deny all; proxy_pass http://127.0.0.1:8080; }
    location /admin/ { allow 10.0.0.0/8; deny all; proxy_pass http://127.0.0.1:8080; }
}
```

---

## 21. Риски

| Риск | Влияние | Митигация |
|------|---------|-----------|
| Xray gRPC API меняется | Высокое | Pinning v26.3.23, проверка при старте |
| rs8kvn_bot API недоступен | Среднее | Retry + last known state |
| SQLite corruption | Высокое | WAL mode, hourly backup |
| Рассинхрон | Высокое | Full Sync при health check recovery |
| Частичный сбой apply | Высокое | Идемпотентные операции, retry |

---

## 22. План реализации

### Phase 0: API rs8kvn_bot (параллельно)
- [ ] `GET /api/v1/users` в rs8kvn_bot
- [ ] Proxy Manager → mock

### Phase 1: Foundation
- [ ] go.mod, структура, config, SQLite, logging, circuit breaker

### Phase 2: Xray gRPC
- [ ] gRPC connection, AddUserSafe/RemoveUserSafe (AlterInbound)
- [ ] OverrideRules, AddOutboundSafe/RemoveOutboundSafe
- [ ] QueryStats(reset: true), ListInbounds health check

### Phase 3: Sources
- [ ] rs8kvn_bot API client, Remnawave parser

### Phase 4: Reconciliation
- [ ] Desired state builder, diff, apply с батчингом
- [ ] OverrideRules, single-flight, persist

### Phase 5: Stats
- [ ] QueryStats(reset: true), агрегация, persist

### Phase 6: HTTP API
- [ ] `/sub/:token` (JSON), `/healthz`, `/readyz`, admin, auth, rate limit

### Phase 7: Observability
- [ ] Prometheus, structured logging, graceful shutdown

### Phase 8: Testing & Docs
- [ ] Unit tests (80%+, `-race`), integration, golden files
- [ ] README, Dockerfile, systemd

---

## 23. Чек-лист production

- [ ] Unit-тесты с `-race` проходят
- [ ] Coverage >= 80%
- [ ] Graceful shutdown (SIGTERM)
- [ ] Health check + readiness
- [ ] Prometheus метрики
- [ ] Rate limiting
- [ ] Full Sync восстанавливает состояние
- [ ] Идемпотентные операции работают
- [ ] Backup настроен
- [ ] systemd unit работает
- [ ] Логи без секретов
- [ ] Docker-образ работает

---

## 24. Принятые решения

### Q1: 3X-UI → Убран

Reality ключи генерирует менеджер. Прокси из Remnawave. Пользователи из бота. 3X-UI не нужен.

### Q2: Версия Xray → v26.3.23

Последняя стабильная (23 марта 2026). Soft check при старте.

### Q3: Масштаб → до 100 пользователей

100 × 12 = 1200 слотов. `batch_size: 100`, `batch_delay: 50ms`. Sync ~12 сек.

### Q4: ListUsers → НЕ запрашиваем

In-memory desired state — источник истины. Health check → Full Sync при рестарте.

### Q5: Статистика → QueryStats(reset: true)

Сброс counters после чтения. Проще, нет риска дублирования.

### Q6: OverrideRules → полная замена

Гарантирует порядок: api → user rules → geoip:private → block.

### Q7: Remnawave → VLESS+Reality

Все прокси — VLESS+Reality ноды.

### Q8: Happ → JSON

JSON массив с полями name, type, server, uuid, flow, security, server_name, reality_public_key, short_id.

### Вне скоупа

- Webhook от rs8kvn_bot (v2)
- Мульти-роутер
- Per-user bandwidth limits
- Географический роутинг

---

## 25. Глоссарий

| Термин | Определение |
|--------|-------------|
| **Base UUID** | UUID пользователя из rs8kvn_bot |
| **Slot** | UUID для пары (user, proxy). UUIDv5 |
| **Hot Reload** | Изменение Xray без перезапуска |
| **Full Sync** | Полная пересинхронизация |
| **Reconciliation** | Сверка desired и last known state |
| **OverrideRules** | Полная замена routing rules |
| **AlterInbound** | gRPC метод для Add/Remove User |
| **Идемпотентность** | Повторный вызов не меняет результат |
| **Reality** | Протокол шифрования Xray |
| **VLESS+Vision** | Протокол без оверхеда шифрования |
| **Circuit Breaker** | Защита от каскадных сбоев |
