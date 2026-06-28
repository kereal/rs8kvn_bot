# VPN Client Abstraction

**Status:** IMPLEMENTED (v3.0.0)

## Purpose
Абстрагировать создание/обновление/удаление VPN-клиентов от типа панели (3x-ui, proxman, будущие провайдеры).

## Interface (`internal/vpn/client.go`)
```go
type SubscriptionProvision struct {
    ClientID     string
    Username     string
    SubID        string
    TrafficBytes int64
    ExpiryTime   time.Time
    ResetDays    int
}

type Client interface {
    CreateSubscription(ctx context.Context, provision SubscriptionProvision) error
    UpdateSubscription(ctx context.Context, provision SubscriptionProvision) error
    DeleteSubscription(ctx context.Context, provision SubscriptionProvision) error
    Close() error
}
```

## Implementations
- **`ThreeXUIClient`** (`internal/vpn/threex_ui.go`): адаптер над `interfaces.XUIClient`
- `ProxmanClient` (`internal/vpn/proxman.go`): HTTP webhook client (`subscription.delete` event); response `"duplicate"` → `ErrSubscriptionAlreadyExists`; other errors classified via `classifyDeleteSubscriptionError`

## Usage
`SyncService` берёт `map[uint]vpn.Client` и выбирает клиент по node ID.
`NewClient(Config)` — фабрика, создающая нужную реализацию.

## Error Classification
- `ErrSubscriptionAlreadyExists` → fallback на UpdateSubscription
- `ErrSubscriptionNotFound` → при удалении, удаляем запись из БД