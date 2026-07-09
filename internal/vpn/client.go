package vpn

import (
	"context"
	"errors"
	"fmt"
	"strings"
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
	ClientID     string
	Username     string
	SubID        string
	TrafficBytes int64
	ExpiryTime   time.Time
	ResetDays    int
}

// Client is the abstraction for provisioning VPN subscriptions on different node types.
type Client interface {
	CreateSubscription(ctx context.Context, provision SubscriptionProvision) error
	UpdateSubscription(ctx context.Context, provision SubscriptionProvision) error
	DeleteSubscription(ctx context.Context, provision SubscriptionProvision) error
	Close() error
}

var (
	ErrSubscriptionAlreadyExists = errors.New("vpn subscription already exists")
	ErrSubscriptionNotFound      = errors.New("vpn subscription not found")
	ErrNotImplemented            = errors.New("vpn operation not implemented")
)

// classifyCreateSubscriptionError wraps the underlying error with ErrSubscriptionAlreadyExists
// when the error message indicates the client is already present on the node.
// Idempotent: a value already carrying the sentinel is returned unchanged.
func classifyCreateSubscriptionError(err error) error {
	if err == nil || errors.Is(err, ErrSubscriptionAlreadyExists) {
		return err
	}

	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "already exists") ||
		strings.Contains(msg, "client already exists") ||
		strings.Contains(msg, "duplicate") ||
		strings.Contains(msg, "already added") {
		return fmt.Errorf("%w: %w", ErrSubscriptionAlreadyExists, err)
	}

	return err
}

// classifyDeleteSubscriptionError wraps the underlying error with ErrSubscriptionNotFound
// when the error message indicates the client does not exist on the node.
// Idempotent: a value already carrying the sentinel is returned unchanged.
func classifyDeleteSubscriptionError(err error) error {
	if err == nil || errors.Is(err, ErrSubscriptionNotFound) {
		return err
	}

	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "not found") ||
		strings.Contains(msg, "does not exist") ||
		strings.Contains(msg, "client not found") {
		return fmt.Errorf("%w: %w", ErrSubscriptionNotFound, err)
	}

	return err
}

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
