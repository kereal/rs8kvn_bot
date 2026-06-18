package vpn

import (
	"context"
	"errors"
	"fmt"

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

// Client defines the interface for VPN node operations.
type Client interface {
	CreateSubscription(ctx context.Context, uuid, username string) error
	DeleteSubscription(ctx context.Context, uuid, username string) error
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
		return NewProxmanClient(), nil
	default:
		return nil, fmt.Errorf("unsupported node type: %s", cfg.Type)
	}
}
