package vpn

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/interfaces"
)

// Config holds configuration for a VPN client.
type Config struct {
	Host       string
	APIToken   string
	InboundIDs []int
	XUIClient  interfaces.XUIClient
	Type       database.NodeType
}

// SubscriptionProvision describes the data needed to provision a client on a VPN node.
type SubscriptionProvision struct {
	ClientID        string
	Username        string
	CurrentEmail    string // existing email to update (rename); empty for create
	SubID           string
	TrafficBytes    int64
	ExpiryTime      time.Time
	ResetDays       int
	ResetDay        int
	ResetMax        int
	TrafficReset    string
	TrafficResetDay int
	TgID            int64  // Telegram user id for panel binding (0 = none)
	Comment         string // free-form comment stored in the panel
}

// Client is the abstraction for provisioning VPN subscriptions on different node types.
type Client interface {
	CreateSubscription(ctx context.Context, provision SubscriptionProvision) error
	UpdateSubscription(ctx context.Context, provision SubscriptionProvision) error
	DeleteSubscription(ctx context.Context, provision SubscriptionProvision) error
	// ResetTraffic обнуляет счётчик трафика (up/down) клиента на панели.
	// Используется при смене тарифа, чтобы трафик предыдущего периода
	// не влиял на лимит нового тарифа. Не все node-типы поддерживают
	// эту операцию — для неподдерживаемых это no-op.
	ResetTraffic(ctx context.Context, provision SubscriptionProvision) error
	Close() error
}

var (
	ErrSubscriptionAlreadyExists = errors.New("vpn subscription already exists")
	ErrSubscriptionNotFound      = errors.New("vpn subscription not found")
	ErrNotImplemented            = errors.New("vpn operation not implemented")
)

var _ Client = (*FetchClient)(nil)

// NewClient creates a VPN client based on the node type.
func NewClient(cfg Config) (Client, error) {
	switch cfg.Type {
	case database.NodeType3xUI:
		if cfg.XUIClient == nil {
			return nil, errors.New("xui client is required for 3x-ui node")
		}

		return NewThreeXUIClient(cfg.XUIClient, cfg.InboundIDs), nil
	case database.NodeTypeProxman:
		return NewProxmanClient(cfg.Host, cfg.APIToken), nil
	case database.NodeTypeFetch:
		return NewFetchClient(), nil
	default:
		return nil, fmt.Errorf("unsupported node type: %s", cfg.Type)
	}
}
