package vpn

import (
	"context"
)

// FetchClient implements vpn.Client for fetch nodes.
//
// Fetch nodes are read-only sources: they do not manage VPN clients on a panel.
// The subscription_url stored on the node points to an HTTP endpoint that
// returns proxy configuration directly. All provisioning operations are no-ops
// because there is nothing to create, update, or delete on the upstream — the
// subserver simply fetches the URL when serving /sub/:id.
type FetchClient struct{}

// NewFetchClient creates a no-op VPN client for fetch-type nodes.
func NewFetchClient() *FetchClient {
	return &FetchClient{}
}

// CreateSubscription is a no-op: fetch nodes do not manage VPN clients.
func (c *FetchClient) CreateSubscription(_ context.Context, _ SubscriptionProvision) error {
	return nil
}

// UpdateSubscription is a no-op: fetch nodes do not manage VPN clients.
func (c *FetchClient) UpdateSubscription(_ context.Context, _ SubscriptionProvision) error {
	return nil
}

// DeleteSubscription is a no-op: fetch nodes do not manage VPN clients.
func (c *FetchClient) DeleteSubscription(_ context.Context, _ SubscriptionProvision) error {
	return nil
}

// Close is a no-op: fetch nodes hold no resources.
func (c *FetchClient) Close() error {
	return nil
}
