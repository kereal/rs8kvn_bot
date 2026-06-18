package vpn

import (
	"context"
	"fmt"
)

// ProxmanClient is a stub implementation for future proxman support.
type ProxmanClient struct{}

// NewProxmanClient creates a stub proxman client.
func NewProxmanClient() *ProxmanClient {
	return &ProxmanClient{}
}

// CreateSubscription is not implemented for proxman.
func (c *ProxmanClient) CreateSubscription(ctx context.Context, uuid, username string) error {
	return fmt.Errorf("proxman create subscription: not implemented")
}

// DeleteSubscription is not implemented for proxman.
func (c *ProxmanClient) DeleteSubscription(ctx context.Context, uuid, username string) error {
	return fmt.Errorf("proxman delete subscription: not implemented")
}

// Close is a no-op for the stub proxman client.
func (c *ProxmanClient) Close() error {
	return nil
}
