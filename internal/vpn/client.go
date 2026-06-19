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
	Host         string
	APIToken     string
	InboundIDs   []int
	XUIClient    interfaces.XUIClient
	Type         database.NodeType
}

// SubscriptionProvision describes the data needed to provision a client on a VPN node.
type SubscriptionProvision struct {
	ClientID     string
	Username     string
	SubID        string
	TrafficBytes int64
	ExpiryTime   time.Time
	ResetDays    int
}

// Client defines the interface for VPN node operations.
type Client interface {
	CreateSubscription(ctx context.Context, provision SubscriptionProvision) error
	DeleteSubscription(ctx context.Context, provision SubscriptionProvision) error
	Close() error
}

// NewClient creates a VPN client based on the node type.
func NewClient(cfg Config) (Client, error) {
	switch cfg.Type {
	case database.NodeType3xUI:
		if cfg.XUIClient == nil {
			return nil, errors.New("xui client is required for 3x-ui node")
		}
		return NewThreeXUIClient(cfg.XUIClient, cfg.InboundIDs), nil
	case database.NodeTypeProxman:
		return nil, errors.New("proxman nodes are not supported yet")
	default:
		return nil, fmt.Errorf("unsupported node type: %s", cfg.Type)
	}
}
