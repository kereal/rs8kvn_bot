package service

import (
	"context"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/interfaces"
	"github.com/kereal/rs8kvn_bot/internal/vpn"
)

// NodeAccess is the single runtime entry point to one node, built once in the
// composition root (cmd/bot). Provisioning and reading are separate slots: a
// node type without a readable panel (proxman, fetch) simply has no Reader,
// instead of pretending every adapter can read.
type NodeAccess struct {
	// Provision creates, updates and removes VPN subscriptions on the node.
	// Present for every node type with provisioning support.
	Provision vpn.Client
	// Reader reads live panel state (traffic counters, client card). Non-nil
	// only for node types with a readable panel (3x-ui).
	Reader interfaces.XUIClientReader
	// Type is the node type this access was built for.
	Type database.NodeType
}

// NodeClients is the runtime registry of per-node access, keyed by node ID.
// SubscriptionService and SyncService share one instance.
type NodeClients map[uint]NodeAccess

// Reader returns the panel reader for the node. ok is false both for unknown
// node IDs and for node types without read capability. This is the single
// place that decides whether a node can be read from.
func (nc NodeClients) Reader(nodeID uint) (interfaces.XUIClientReader, bool) {
	access, ok := nc[nodeID]
	if !ok || access.Reader == nil {
		return nil, false
	}

	return access.Reader, true
}

// Provision returns the VPN provisioning client for the node. ok is false for
// unknown node IDs and for nodes without provisioning support.
func (nc NodeClients) Provision(nodeID uint) (vpn.Client, bool) {
	access, ok := nc[nodeID]
	if !ok || access.Provision == nil {
		return nil, false
	}

	return access.Provision, true
}

// readerTarget is one node with a panel reader, scoped to a subscription's
// client email. It is the shared unit of work for traffic aggregation and
// traffic notifications.
type readerTarget struct {
	nodeID  uint
	inbound []int
	reader  interfaces.XUIClientReader
	email   string
}

// activeReaders returns the panel readers of active nodes, optionally
// restricted to the nodes the subscription is actively bound to
// (database.SyncStatusActive). Nodes without a reader (proxman, fetch) are
// skipped silently. A nil activeNodeIDs disables the binding filter — the
// caller could not load subscription_nodes and falls back to polling every
// readable active node, matching the legacy best-effort aggregation behaviour.
// The binding filter itself lives here and nowhere else, so aggregation and
// notifications can not drift apart.
func (s *SubscriptionService) activeReaders(activeNodeIDs map[uint]struct{}, email string) []readerTarget {
	var targets []readerTarget

	for _, node := range s.activeNodes() {
		if activeNodeIDs != nil {
			if _, ok := activeNodeIDs[node.ID]; !ok {
				continue
			}
		}

		reader, ok := s.clients.Reader(node.ID)
		if !ok {
			continue
		}

		targets = append(targets, readerTarget{
			nodeID:  node.ID,
			inbound: node.ResolveInboundIDs(),
			reader:  reader,
			email:   email,
		})
	}

	return targets
}

// activeBindingNodeIDs builds the set of node IDs a subscription is actively
// bound to. On load failure it returns nil so callers can fall back to no
// filtering; the caller decides whether that fallback is allowed.
func (s *SubscriptionService) activeBindingNodeIDs(ctx context.Context, subscriptionID uint) (map[uint]struct{}, error) {
	subNodes, err := s.db.GetBySubscriptionID(ctx, subscriptionID)
	if err != nil {
		return nil, err
	}

	active := make(map[uint]struct{}, len(subNodes))
	for _, sn := range subNodes {
		if sn.Status == database.SyncStatusActive {
			active[sn.NodeID] = struct{}{}
		}
	}

	return active, nil
}
