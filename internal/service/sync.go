package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/interfaces"
	"github.com/kereal/rs8kvn_bot/internal/logger"
	"github.com/kereal/rs8kvn_bot/internal/metrics"
	"github.com/kereal/rs8kvn_bot/internal/utils"
	"github.com/kereal/rs8kvn_bot/internal/vpn"
	"github.com/kereal/rs8kvn_bot/internal/xui"

	"github.com/mattn/go-sqlite3"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// SyncService manages the synchronization of subscriptions with VPN nodes.
type SyncService struct {
	db      interfaces.SyncStore
	clients NodeClients
	nodes   []database.Node

	locksMu     sync.Mutex
	locks       map[uint]*subscriptionSyncLock
	lockTimeout time.Duration
}

type subscriptionSyncLock struct {
	ch      chan struct{} // capacity-1 token: the holder owns the token
	waiters int           // goroutines waiting or holding this lock
}

// syncIdentifier builds a unique VPN client identifier from username and telegram ID.
func syncIdentifier(sub *database.Subscription) string {
	return XUIEmail(sub.Username, sub.TelegramID)
}

// NewSyncService creates a new SyncService.
func NewSyncService(db interfaces.SyncStore, clients NodeClients, nodes []database.Node) *SyncService {
	return &SyncService{
		db:          db,
		clients:     clients,
		nodes:       nodes,
		locks:       make(map[uint]*subscriptionSyncLock),
		lockTimeout: 2 * time.Minute,
	}
}

// lockSubscription acquires a per-subscription lock and returns an unlock
// function. Acquisition is bounded by the shorter of the caller's context
// deadline and subscriptionLockTimeout, so a stuck holder cannot block other
// goroutines operating on the same subscription indefinitely. The map entry is
// removed once the last waiter releases the lock, preventing unbounded map
// growth over the lifetime of the process.
func (s *SyncService) lockSubscription(ctx context.Context, subscriptionID uint) (func(), error) {
	s.locksMu.Lock()

	l, ok := s.locks[subscriptionID]
	if !ok {
		l = &subscriptionSyncLock{ch: make(chan struct{}, 1)}
		l.ch <- struct{}{} // initial token available

		s.locks[subscriptionID] = l
	}

	l.waiters++
	s.locksMu.Unlock()

	timeout := s.lockTimeout
	if dl, ok := ctx.Deadline(); ok {
		if rem := time.Until(dl); rem < timeout {
			timeout = rem
		}
	}

	acquireCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	select {
	case <-l.ch:
		return func() {
			l.ch <- struct{}{} // return the token to the next waiter

			s.dropWaiter(l, subscriptionID)
		}, nil
	case <-acquireCtx.Done():
		// Never return the token here: the holder still owns it. Only undo our
		// waiter accounting so the map entry can be reclaimed correctly.
		s.dropWaiter(l, subscriptionID)
		return nil, fmt.Errorf("lock subscription %d: %w", subscriptionID, acquireCtx.Err())
	}
}

// dropWaiter decrements the waiter count for a subscription lock and removes
// the map entry once no goroutine holds or waits for it. It must NOT touch l.ch:
// only the actual holder returns the token on unlock.
func (s *SyncService) dropWaiter(l *subscriptionSyncLock, subscriptionID uint) {
	s.locksMu.Lock()

	l.waiters--
	if l.waiters == 0 {
		delete(s.locks, subscriptionID)
	}
	s.locksMu.Unlock()
}

// ReconcilePlanNodes reconciles the subscription_nodes table against the current plan.
// It handles four cases:
//   - ADD: a plan node has no subscription_nodes record → pending_add
//   - CHANGED_LIMITS: an active node's plan limits differ from last provisioning → pending_add (triggers UpdateSubscription)
//   - REMOVE: a subscription_nodes node is no longer in the plan → pending_remove
//   - STALE: a pending_add node was removed from the plan → pending_remove
//
// Acquires the per-subscription lock. Callers that already hold the lock
// (e.g. SyncPendingNodes) must call reconcilePlanNodesLocked directly.
func (s *SyncService) ReconcilePlanNodes(ctx context.Context, subscriptionID uint) error {
	unlock, err := s.lockSubscription(ctx, subscriptionID)
	if err != nil {
		return fmt.Errorf("reconcile plan nodes: acquire lock: %w", err)
	}
	defer unlock()

	return s.reconcilePlanNodesLocked(ctx, subscriptionID)
}

// reconcilePlanNodesLocked is the lock-free body of ReconcilePlanNodes.
// The caller must hold lockSubscription(subscriptionID). It delegates to the
// shared reconciliation core (plan_reconcile.go) with the background error
// policy: per-node failures are logged and joined via errors.Join, while
// cancellation or deadline errors abort immediately. Revoked subscriptions
// are left unchanged by the core's readSubscription option.
func (s *SyncService) reconcilePlanNodesLocked(ctx context.Context, subscriptionID uint) error {
	logger.Debug("reconcile plan nodes",
		zap.Uint("subscription_id", subscriptionID))

	var errs []error

	err := reconcilePlanNodesCore(ctx, dbServicePlanStore{db: s.db}, subscriptionID, planReconcileOptions{
		readSubscription: true,
		onNodeError: func(operation string, nodeID uint, err error) error {
			wrappedErr := fmt.Errorf("reconcile plan nodes: %s node %d: %w", operation, nodeID, err)
			logger.Warn("reconcile plan nodes: node operation failed",
				zap.Uint("subscription_id", subscriptionID),
				zap.Uint("node_id", nodeID),
				zap.String("operation", operation),
				zap.Error(wrappedErr))

			if utils.IsCtxErr(err) {
				return wrappedErr
			}

			errs = append(errs, wrappedErr)

			return nil
		},
	})
	if err != nil {
		return err
	}

	logger.Debug("reconcile plan nodes completed",
		zap.Uint("subscription_id", subscriptionID))

	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	return nil
}

// MarkAllForRemoval sets all subscription nodes (active, pending_add, pending_remove) to pending_remove status.
// Used before deleting a subscription to ensure VPN clients are removed via sync.
func (s *SyncService) MarkAllForRemoval(ctx context.Context, subscriptionID uint) error {
	unlock, err := s.lockSubscription(ctx, subscriptionID)
	if err != nil {
		return fmt.Errorf("mark all for removal: acquire lock: %w", err)
	}
	defer unlock()

	return s.markAllForRemovalLocked(ctx, subscriptionID)
}

// markAllForRemovalLocked is the lock-free body used by callers that already
// hold the subscription lock.
func (s *SyncService) markAllForRemovalLocked(ctx context.Context, subscriptionID uint) error {
	logger.Debug("mark all nodes for removal",
		zap.Uint("subscription_id", subscriptionID))

	nodes, err := s.db.GetBySubscriptionID(ctx, subscriptionID)
	if err != nil {
		return fmt.Errorf("mark all for removal: load nodes: %w", err)
	}

	for _, sn := range nodes {
		err := s.db.UpdateSubscriptionNodeStatus(ctx, sn.SubscriptionID, sn.NodeID, database.SyncStatusPendingRemove)
		if err != nil {
			return fmt.Errorf("mark all for removal: set pending_remove node %d: %w", sn.NodeID, err)
		}

		logger.Debug("marked node for removal",
			zap.Uint("subscription_id", subscriptionID),
			zap.Uint("node_id", sn.NodeID))
	}

	logger.Debug("mark all for removal completed",
		zap.Uint("subscription_id", subscriptionID),
		zap.Int("nodes_processed", len(nodes)))

	return nil
}

// SyncSubscription performs pending VPN operations for the given subscription.
// Background path: per-node failures are logged and processing continues; only returns error on DB/cancel failure.
func (s *SyncService) SyncSubscription(ctx context.Context, subscriptionID uint) error {
	unlock, err := s.lockSubscription(ctx, subscriptionID)
	if err != nil {
		return fmt.Errorf("sync subscription: acquire lock: %w", err)
	}
	defer unlock()

	logger.Debug("sync subscription",
		zap.Uint("subscription_id", subscriptionID))

	pending, err := s.db.GetPendingBySubscriptionID(ctx, subscriptionID)
	if err != nil {
		return fmt.Errorf("sync subscription: load nodes: %w", err)
	}

	if len(pending) == 0 {
		logger.Debug("sync subscription: no pending nodes",
			zap.Uint("subscription_id", subscriptionID))

		return nil
	}

	sub, err := s.db.GetByID(ctx, subscriptionID)
	if err != nil {
		return fmt.Errorf("sync subscription: load subscription: %w", err)
	}

	err = s.syncNodes(ctx, sub, pending)
	if err != nil {
		if utils.IsCtxErr(err) {
			return err
		}

		// External-sync phase is best-effort: per-node failures keep the
		// subscription active and are retried by the background worker, so
		// they must not surface to the caller.
		logger.Warn("sync subscription: per-node failure logged, background will retry",
			zap.Uint("subscription_id", subscriptionID),
			zap.Error(err))
	}

	return nil
}

// syncNodes iterates over pending subscription nodes and dispatches add/remove/update operations.
// Per-node failures are logged and aggregated via errors.Join so background workers can observe
// degraded runs; processing continues past a failed node. Only ctx cancellation/deadline aborts early.
// For revoked subscriptions, pending adds and updates are durably queued for
// removal before any VPN call; a failed queue write skips that node. A nil
// subscription returns an error.
func (s *SyncService) syncNodes(ctx context.Context, sub *database.Subscription, pending []database.SubscriptionNode) error {
	if sub == nil {
		return fmt.Errorf("sync subscription: nil subscription")
	}

	nodeTypes := make(map[uint]database.NodeType, len(s.nodes))
	for _, n := range s.nodes {
		nodeTypes[n.ID] = n.Type
	}

	logger.Debug("processing pending nodes",
		zap.Uint("subscription_id", sub.ID),
		zap.Int("pending_count", len(pending)))

	var nodeErrs []error

	for _, sn := range pending {
		// Revocation may have committed before queuing removal failed. Never
		// provision these stale add/update bindings; persist removal first so
		// a failed VPN call is retried as a deletion by the background worker.
		if sub.Status == string(database.SubscriptionStatusRevoked) &&
			(sn.Status == database.SyncStatusPendingAdd || sn.Status == database.SyncStatusPendingUpdate) {
			err := s.db.UpdateSubscriptionNodeStatus(ctx, sn.SubscriptionID, sn.NodeID, database.SyncStatusPendingRemove)
			if err != nil {
				err = fmt.Errorf("mark revoked subscription node %d pending_remove: %w", sn.NodeID, err)
				if utils.IsCtxErr(err) {
					return err
				}

				s.handleSyncError(ctx, &sn, err)
				nodeErrs = append(nodeErrs, err)

				continue
			}

			sn.Status = database.SyncStatusPendingRemove
			clearRetry(&sn)
		}

		// pending_remove is dispatched before the runtime node-type check: a
		// removal binding may reference a node that is now gone or inactive, in
		// which case the binding is stale and must be dropped instead of being
		// retried against the startup snapshot forever (which would wedge the
		// admin /del flow and the background worker).
		if sn.Status == database.SyncStatusPendingRemove {
			logger.Debug("processing pending_remove",
				zap.Uint("subscription_id", sub.ID),
				zap.Uint("node_id", sn.NodeID))

			err := s.processPendingRemove(ctx, &sn, sub)
			if err != nil {
				if utils.IsCtxErr(err) {
					return err
				}

				logger.Warn("pending_remove failed",
					zap.Uint("subscription_id", sub.ID),
					zap.Uint("node_id", sn.NodeID),
					zap.Error(err))
				nodeErrs = append(nodeErrs, err)
			}

			continue
		}

		nodeType, hasNode := nodeTypes[sn.NodeID]
		if !hasNode {
			logger.Warn("node not found in runtime clients, skipping",
				zap.Uint("subscription_id", sub.ID),
				zap.Uint("node_id", sn.NodeID))
			s.handleSyncError(ctx, &sn, fmt.Errorf("%w: node not found in runtime clients", errNodeUnavailable))

			continue
		}

		switch nodeType {
		case database.NodeType3xUI, database.NodeTypeProxman, database.NodeTypeFetch:
		default:
			logger.Warn("unsupported node type, skipping",
				zap.Uint("subscription_id", sub.ID),
				zap.Uint("node_id", sn.NodeID),
				zap.String("node_type", string(nodeType)))
			s.handleSyncError(ctx, &sn, fmt.Errorf("%w: unsupported node type %s", errNodeUnavailable, nodeType))

			continue
		}

		switch sn.Status {
		case database.SyncStatusPendingAdd:
			logger.Debug("processing pending_add",
				zap.Uint("subscription_id", sub.ID),
				zap.Uint("node_id", sn.NodeID))

			err := s.processPendingAdd(ctx, &sn, sub)
			if err != nil {
				if utils.IsCtxErr(err) {
					return err
				}

				logger.Warn("pending_add failed",
					zap.Uint("subscription_id", sub.ID),
					zap.Uint("node_id", sn.NodeID),
					zap.Error(err))
				nodeErrs = append(nodeErrs, err)
			}
		case database.SyncStatusPendingUpdate:
			logger.Debug("processing pending_update",
				zap.Uint("subscription_id", sub.ID),
				zap.Uint("node_id", sn.NodeID))

			err := s.processPendingUpdate(ctx, &sn, sub)
			if err != nil {
				if utils.IsCtxErr(err) {
					return err
				}

				logger.Warn("pending_update failed",
					zap.Uint("subscription_id", sub.ID),
					zap.Uint("node_id", sn.NodeID),
					zap.Error(err))
				nodeErrs = append(nodeErrs, err)
			}
		}
	}

	if len(nodeErrs) > 0 {
		return errors.Join(nodeErrs...)
	}

	return nil
}

// retryUnavailableNode reports a node that cannot serve the given sync
// operation (missing config, inactive, or no VPN client) and keeps the
// subscription_node record pending for the background worker to retry.
//
// Unavailability is a transient condition: the runtime registry is a startup
// snapshot, the node may re-enter the fleet or gain a client later. The error
// is wrapped in errNodeUnavailable so the retry policy treats it as retryable
// (it ladders up to maxSyncRetries instead of parking on the first hit),
// while still parking eventually if the node never recovers.
func (s *SyncService) retryUnavailableNode(ctx context.Context, sn *database.SubscriptionNode, sub *database.Subscription, operation string) error {
	node, nodeErr := s.db.GetNodeByID(ctx, sn.NodeID)
	if nodeErr != nil {
		err := fmt.Errorf("%w: %s node %d unavailable: load node: %w", errNodeUnavailable, operation, sn.NodeID, nodeErr)
		logger.Warn("node unavailable for sync operation, keeping pending record",
			zap.String("operation", operation),
			zap.Uint("subscription_id", sub.ID),
			zap.Uint("node_id", sn.NodeID),
			zap.Error(err))
		s.handleSyncError(ctx, sn, err)

		return err
	}

	if !node.IsActive {
		err := fmt.Errorf("%w: %s node %d unavailable: inactive=%t type=%s", errNodeUnavailable, operation, sn.NodeID, node.IsActive, node.Type)
		logger.Warn("node unsuitable for sync operation, keeping pending record",
			zap.String("operation", operation),
			zap.Uint("subscription_id", sub.ID),
			zap.Uint("node_id", sn.NodeID),
			zap.Bool("node_active", node.IsActive),
			zap.String("node_type", string(node.Type)),
			zap.Error(err))
		s.handleSyncError(ctx, sn, err)

		return err
	}

	err := fmt.Errorf("%w: %s node %d unavailable: no VPN client", errNodeUnavailable, operation, sn.NodeID)
	s.handleSyncError(ctx, sn, err)

	return err
}

// errNodeUnavailable marks node-unavailability errors as retryable: the
// condition is transient (startup registry snapshot, node may come back), so
// the retry policy ladders instead of parking on the first hit. It is wrapped
// with %w by every retryUnavailableNode error and checked by errors.Is in the
// policy.
var errNodeUnavailable = errors.New("node unavailable")

// fallbackUpdateToCreate re-queues an update whose target client is gone from
// the panel (vpn.ErrSubscriptionNotFound): an update cannot succeed, the
// create path can. The node moves to pending_add — a legitimate status
// transition, so UpdateSubscriptionNodeStatus resets retry metadata and the
// next worker pass provisions the client fresh. The re-queue is not a scan
// failure: nil is returned and the outcome is visible in the node status and
// the Warn log.
func (s *SyncService) fallbackUpdateToCreate(ctx context.Context, sn *database.SubscriptionNode, cause error) error {
	err := s.db.UpdateSubscriptionNodeStatus(ctx, sn.SubscriptionID, sn.NodeID, database.SyncStatusPendingAdd)
	if err != nil {
		err = fmt.Errorf("fallback pending_update to pending_add node %d: %w", sn.NodeID, err)
		logger.Warn("update fallback to create failed",
			zap.Uint("subscription_id", sn.SubscriptionID),
			zap.Uint("node_id", sn.NodeID),
			zap.Error(err))

		return err
	}

	logger.Warn("panel client is gone, update re-queued as create",
		zap.Uint("subscription_id", sn.SubscriptionID),
		zap.Uint("node_id", sn.NodeID),
		zap.Error(cause))

	return nil
}

// processPendingUpdate updates an existing VPN subscription client configuration.
// Used when a plan change requires updating traffic limits or expiry on an active node.
// The auto-renew profile is preserved from the panel (read-modify-write):
// manual admin edits must not be overwritten by product defaults.
func (s *SyncService) processPendingUpdate(ctx context.Context, sn *database.SubscriptionNode, sub *database.Subscription) error {
	client, ok := s.clients.Provision(sn.NodeID)
	if !ok {
		return s.retryUnavailableNode(ctx, sn, sub, "pending_update")
	}

	provision, err := s.buildNodeProvision(ctx, sub, sn.NodeID, profilePreserve)
	if err != nil {
		if errors.Is(err, vpn.ErrSubscriptionNotFound) {
			return s.fallbackUpdateToCreate(ctx, sn, err)
		}

		s.handleSyncError(ctx, sn, err)

		return fmt.Errorf("build provision for pending update node %d: %w", sn.NodeID, err)
	}

	logger.Debug("updating VPN subscription",
		zap.Uint("subscription_id", sub.ID),
		zap.Uint("node_id", sn.NodeID))

	err = client.UpdateSubscription(ctx, provision)
	if err != nil {
		if errors.Is(err, vpn.ErrSubscriptionNotFound) {
			return s.fallbackUpdateToCreate(ctx, sn, err)
		}

		s.handleSyncError(ctx, sn, err)
		return fmt.Errorf("update VPN subscription node %d: %w", sn.NodeID, err)
	}

	// Сбрасываем счётчик трафика, чтобы трафик предыдущего тарифа
	// не влиял на лимит нового. Best-effort: update уже прошёл успешно,
	// сброс трафика — улучшение, а не критическая операция.
	err = client.ResetTraffic(ctx, provision)
	if err != nil {
		logger.Warn("reset traffic after plan change failed (best-effort)",
			zap.Uint("subscription_id", sub.ID),
			zap.Uint("node_id", sn.NodeID),
			zap.Error(err))
	}

	err = s.db.UpdateSubscriptionNodeStatus(ctx, sn.SubscriptionID, sn.NodeID, database.SyncStatusActive)
	if err != nil {
		return fmt.Errorf("mark active: %w", err)
	}

	logger.Debug("subscription updated and marked active",
		zap.Uint("subscription_id", sub.ID),
		zap.Uint("node_id", sn.NodeID))

	return nil
}

// profilePolicy selects the auto-renew profile policy of the provision build.
type profilePolicy int

const (
	// profileDefault writes the product default profile: monthly traffic
	// reset on day 1, renewal interval config.SubscriptionResetDay,
	// unlimited renewals. Used by the add path.
	profileDefault profilePolicy = iota
	// profilePreserve copies the current panel profile into the provision
	// (read-modify-write), so manual admin edits survive a plan change.
	profilePreserve
)

// buildNodeProvision is the single assembly point of vpn.SubscriptionProvision
// for the sync add and update paths: common client fields plus an auto-renew
// profile chosen by policy. Previously both paths carried a copy of this
// build, which already produced a production drift (create wrote reset=-1).
//
// A trial always gets a profile without a reset cycle (reset=0,
// trafficReset=never): the trial quota never renews, regardless of the path
// policy or the client's current panel profile. The profile is sent explicitly
// because the panel does not merge profiles on write — an omitted trafficReset
// is normalized to "never" (doc/architecture.md, «Контракт API панели
// 3x-ui», §4.2).
//
// With profilePreserve the profile is read from the panel
// (GET /clients/get/{email}) — the only source of the full profile. A missing
// client yields the sentinel vpn.ErrSubscriptionNotFound (no fallback to
// create); any other read error fails the item, because writing the default
// profile would erase manual admin edits. Nodes without a readable panel
// (proxman, fetch) have no profile to preserve and get the product default.
func (s *SyncService) buildNodeProvision(ctx context.Context, sub *database.Subscription, nodeID uint, policy profilePolicy) (vpn.SubscriptionProvision, error) {
	plan, err := s.db.GetPlanByID(ctx, sub.PlanID)
	if err != nil {
		return vpn.SubscriptionProvision{}, fmt.Errorf("load plan for subscription sync: %w", err)
	}

	comment, err := referrerComment(ctx, s.db, sub)
	if err != nil {
		return vpn.SubscriptionProvision{}, fmt.Errorf("load referrer comment: %w", err)
	}

	provision := vpn.SubscriptionProvision{
		ClientID:     sub.ClientID,
		Username:     syncIdentifier(sub),
		CurrentEmail: syncIdentifier(sub),
		SubID:        sub.SubscriptionID,
		TrafficBytes: plan.TrafficLimit,
		TgID:         sub.TelegramID,
		Comment:      comment,
	}

	if plan.TrafficLimit > 0 {
		// Trials must never auto-renew even though the trial plan carries a
		// traffic limit (which would otherwise enable it via reset=-1 → 30).
		// The trial profile is sent explicitly (never) because the panel does
		// not merge profiles on write — see buildNodeProvision.
		if plan.Name == database.TrialPlanName {
			provision.ResetDays = 0
			provision.TrafficReset = "never"
		} else {
			switch policy {
			case profileDefault:
				defaultRenewProfile(&provision)
			case profilePreserve:
				reader, hasReader := s.clients.Reader(nodeID)
				if !hasReader {
					defaultRenewProfile(&provision)

					break
				}

				profile, profErr := panelRenewProfile(ctx, reader, syncIdentifier(sub))
				if profErr != nil {
					return vpn.SubscriptionProvision{}, fmt.Errorf("node %d: %w", nodeID, profErr)
				}

				provision.ResetDays = profile.Reset
				provision.ResetDay = profile.ResetDay
				provision.ResetMax = profile.ResetMax
				provision.TrafficReset = profile.TrafficReset
				provision.TrafficResetDay = profile.TrafficResetDay
			}
		}

		if sub.ExpiresAt != nil {
			provision.ExpiryTime = *sub.ExpiresAt
		} else {
			provision.ExpiryTime = time.Now().UTC().Truncate(time.Minute).AddDate(0, 0, config.SubscriptionResetDay)
		}
		// NB: recovery of an already-expired free client (move expiry to the
		// next cycle) happens in the xui layer (doUpdateClient), guarded by
		// resetDays > 0 so trials are never extended here.
	}

	return provision, nil
}

// defaultRenewProfile writes the product default auto-renew profile:
// monthly traffic reset on day 1, renewal interval
// config.SubscriptionResetDay, unlimited renewals.
func defaultRenewProfile(provision *vpn.SubscriptionProvision) {
	provision.ResetDays = config.SubscriptionResetDay
	provision.ResetMax = 0
	provision.TrafficReset = "monthly"
	provision.TrafficResetDay = 1
}

// panelRenewProfile reads the client's current auto-renew profile from the
// panel. The panel base is the source of truth: a read failure (including a
// missing client, mapped to vpn.ErrSubscriptionNotFound) aborts the build so
// an update never overwrites manual admin edits with the default profile.
func panelRenewProfile(ctx context.Context, reader interfaces.XUIClientReader, email string) (xui.ClientConfig, error) {
	record, err := reader.GetClientByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, xui.ErrClientNotFound) {
			err = fmt.Errorf("%w: %w", vpn.ErrSubscriptionNotFound, err)
		}

		return xui.ClientConfig{}, fmt.Errorf("read panel profile: %w", err)
	}

	return record.Client, nil
}

// processPendingAdd creates a VPN subscription on the target node.
// Handles idempotent "already exists" errors by synchronizing config and only then marking active.
// The auto-renew profile is the product default written explicitly (monthly/1,
// reset=config.SubscriptionResetDay, resetMax=0), not the panel-side profile.
func (s *SyncService) processPendingAdd(ctx context.Context, sn *database.SubscriptionNode, sub *database.Subscription) error {
	client, ok := s.clients.Provision(sn.NodeID)
	if !ok {
		return s.retryUnavailableNode(ctx, sn, sub, "pending_add")
	}

	provision, err := s.buildNodeProvision(ctx, sub, sn.NodeID, profileDefault)
	if err != nil {
		s.handleSyncError(ctx, sn, err)

		return fmt.Errorf("build provision for pending add node %d: %w", sn.NodeID, err)
	}

	logger.Debug("creating VPN subscription",
		zap.Uint("subscription_id", sub.ID),
		zap.Uint("node_id", sn.NodeID))

	err = client.CreateSubscription(ctx, provision)
	if err != nil {
		if errors.Is(err, vpn.ErrSubscriptionAlreadyExists) {
			updateErr := client.UpdateSubscription(ctx, provision)
			if updateErr != nil {
				s.handleSyncError(ctx, sn, updateErr)
				return fmt.Errorf("update existing VPN subscription node %d: %w", sn.NodeID, updateErr)
			}

			logger.Info("client already exists on node, configuration updated/synchronized",
				zap.Uint("subscription_id", sub.ID),
				zap.Uint("node_id", sn.NodeID))

			err := s.db.UpdateSubscriptionNodeStatus(ctx, sn.SubscriptionID, sn.NodeID, database.SyncStatusActive)
			if err != nil {
				return fmt.Errorf("mark active: %w", err)
			}

			return nil
		}

		s.handleSyncError(ctx, sn, err)

		return err
	}

	err = s.db.UpdateSubscriptionNodeStatus(ctx, sn.SubscriptionID, sn.NodeID, database.SyncStatusActive)
	if err != nil {
		return fmt.Errorf("mark active: %w", err)
	}

	logger.Debug("subscription created and marked active",
		zap.Uint("subscription_id", sub.ID),
		zap.Uint("node_id", sn.NodeID))

	return nil
}

// processPendingRemove deletes a VPN subscription from the target node.
// Handles idempotent "not found" errors by removing the DB record. A node that
// is gone or inactive has no reachable VPN client to remove, so its binding is
// stale DB state and is dropped instead of retried forever (which would wedge
// the admin /del flow and the background worker).
func (s *SyncService) processPendingRemove(ctx context.Context, sn *database.SubscriptionNode, sub *database.Subscription) error {
	// Resolve the node's current state first: a dead node must not be retried
	// forever, unlike pending_add/update where it may be reactivated later.
	node, nodeErr := s.db.GetNodeByID(ctx, sn.NodeID)
	if nodeErr != nil {
		if errors.Is(nodeErr, database.ErrNodeNotFound) || errors.Is(nodeErr, gorm.ErrRecordNotFound) {
			err := s.db.DeleteSubscriptionNode(ctx, sn.SubscriptionID, sn.NodeID)
			if err != nil {
				return fmt.Errorf("drop binding for missing node %d: %w", sn.NodeID, err)
			}

			return nil
		}

		return s.retryUnavailableNode(ctx, sn, sub, "pending_remove")
	}

	if !node.IsActive {
		err := s.db.DeleteSubscriptionNode(ctx, sn.SubscriptionID, sn.NodeID)
		if err != nil {
			return fmt.Errorf("drop binding for inactive node %d: %w", sn.NodeID, err)
		}

		return nil
	}

	client, ok := s.clients.Provision(sn.NodeID)
	if !ok {
		return s.retryUnavailableNode(ctx, sn, sub, "pending_remove")
	}

	provision := vpn.SubscriptionProvision{
		ClientID: sub.ClientID,
		Username: syncIdentifier(sub),
		SubID:    sub.SubscriptionID,
	}

	logger.Debug("deleting VPN subscription",
		zap.Uint("subscription_id", sub.ID),
		zap.Uint("node_id", sn.NodeID))

	err := client.DeleteSubscription(ctx, provision)
	if err != nil {
		if errors.Is(err, vpn.ErrSubscriptionNotFound) {
			logger.Info("client not found on node, treating as success",
				zap.Uint("subscription_id", sub.ID),
				zap.Uint("node_id", sn.NodeID))

			err := s.db.DeleteSubscriptionNode(ctx, sn.SubscriptionID, sn.NodeID)
			if err != nil {
				return fmt.Errorf("delete subscription node: %w", err)
			}

			return nil
		}

		s.handleSyncError(ctx, sn, err)

		return err
	}

	logger.Debug("subscription deleted from node",
		zap.Uint("subscription_id", sub.ID),
		zap.Uint("node_id", sn.NodeID))

	err = s.db.DeleteSubscriptionNode(ctx, sn.SubscriptionID, sn.NodeID)
	if err != nil {
		return fmt.Errorf("delete subscription node: %w", err)
	}

	return nil
}

// handleSyncError updates retry metadata for a failed sync operation.
//
// Policy:
//   - context.Canceled / context.DeadlineExceeded from the caller's ctx mean
//     the scan is shutting down — no retry metadata is written, the background
//     worker will pick the record up on the next pass (R3).
//   - A retryable error (network timeout per utils.IsRetryable, transient node
//     unavailability per errNodeUnavailable, SQLite write-lock contention per
//     isTransientDBError) ladders with exponential backoff until maxSyncRetries.
//   - Anything else (the panel answered and refused, a non-200 front response,
//     a terminal DB error such as a constraint violation) is parked
//     immediately: retrying cannot succeed.
//   - An exhausted ladder (retry_count reached maxSyncRetries) parks too.
//
// A parked node keeps its pending status, holds the terminal cause in
// last_error, and carries a far-future retry_at so GetPendingSync stops
// selecting it. It is not a new state: any legitimate re-transition
// (UpdateSubscriptionNodeStatus, UpsertSubscriptionNode,
// MarkActiveNodesPendingUpdate) resets retry metadata and releases the node.
func (s *SyncService) handleSyncError(ctx context.Context, sn *database.SubscriptionNode, err error) {
	if utils.IsCtxErr(err) {
		logger.Debug("sync aborted by context, retry metadata untouched",
			zap.Uint("subscription_id", sn.SubscriptionID),
			zap.Uint("node_id", sn.NodeID),
			zap.Error(err))

		return
	}

	errMsg := err.Error()
	// Lock contention is only retried when the request never reached a
	// decision point: a non-200 front response may quote "database is locked"
	// in its body, and a refused request stays refused on retry.
	retryable := utils.IsRetryable(err) || errors.Is(err, errNodeUnavailable) ||
		(!errors.Is(err, utils.ErrNon200Response) && isTransientDBError(err))

	if !retryable || sn.RetryCount >= maxSyncRetries {
		switch {
		case !retryable:
			logger.Warn("sync error is not retryable, parking node",
				zap.Uint("subscription_id", sn.SubscriptionID),
				zap.Uint("node_id", sn.NodeID),
				zap.Int("retry_count", sn.RetryCount),
				zap.Error(err))
		case errors.Is(err, errNodeUnavailable):
			logger.Warn("node unavailability retries exhausted, parking node",
				zap.Uint("subscription_id", sn.SubscriptionID),
				zap.Uint("node_id", sn.NodeID),
				zap.Int("retry_count", sn.RetryCount),
				zap.Error(err))
		default:
			logger.Warn("sync retries exhausted, parking node",
				zap.Uint("subscription_id", sn.SubscriptionID),
				zap.Uint("node_id", sn.NodeID),
				zap.Int("retry_count", sn.RetryCount),
				zap.Error(err))
		}

		sn.RetryAt = &parkedRetryAt
		sn.LastError = &errMsg

		dbErr := s.db.UpdateRetry(ctx, sn.SubscriptionID, sn.NodeID, sn.RetryCount, sn.RetryAt, sn.LastError)
		if dbErr != nil {
			logger.Warn("failed to update retry metadata",
				zap.Uint("subscription_id", sn.SubscriptionID),
				zap.Uint("node_id", sn.NodeID),
				zap.Error(dbErr))
		}

		return
	}

	logger.Warn("sync error, scheduling retry",
		zap.Uint("subscription_id", sn.SubscriptionID),
		zap.Uint("node_id", sn.NodeID),
		zap.Int("retry_count", sn.RetryCount),
		zap.Error(err))

	retryAt := CalculateRetryAt(sn.RetryCount)
	sn.RetryAt = &retryAt
	sn.RetryCount++
	sn.LastError = &errMsg

	dbErr := s.db.UpdateRetry(ctx, sn.SubscriptionID, sn.NodeID, sn.RetryCount, sn.RetryAt, sn.LastError)
	if dbErr != nil {
		logger.Warn("failed to update retry metadata",
			zap.Uint("subscription_id", sn.SubscriptionID),
			zap.Uint("node_id", sn.NodeID),
			zap.Error(dbErr))
	}
}

// isTransientDBError reports SQLite write-lock contention (SQLITE_BUSY /
// SQLITE_LOCKED): the condition clears on its own, so such failures must climb
// the retry ladder instead of parking the node on a momentary writer
// collision. Terminal DB failures (constraint violations, schema problems)
// return false — retrying cannot fix them. GORM and the service wrappers keep
// the driver error with %w, so the typed check sees the concrete
// sqlite3.Error; the text fallback covers layers that flattened it (the
// driver's own message is exactly the busy text).
func isTransientDBError(err error) bool {
	if err == nil {
		return false
	}

	var serr sqlite3.Error
	if errors.As(err, &serr) {
		return serr.Code == sqlite3.ErrBusy || serr.Code == sqlite3.ErrLocked
	}

	msg := strings.ToLower(err.Error())

	return strings.Contains(msg, "database is locked") || strings.Contains(msg, "database table is locked")
}

// clearRetry сбрасывает retry-состояние биндинга: любой легитимный переход
// статуса освобождает ноду из бэкофф-лестницы и снимает парковку.
func clearRetry(sn *database.SubscriptionNode) {
	sn.RetryCount = 0
	sn.RetryAt = nil
	sn.LastError = nil
}

// maxSyncRetries caps the retry ladder of a pending node. With the backoff
// intervals below it spans ~5.6h of retrying (1m+2m+5m+15m+30m+45m+60m×4)
// before the node parks. Production showed unbounded growth (retry_count
// reached 1373 — ~57 days of hourly retries against a permanently refusing
// panel), so the cap is deliberate.
const maxSyncRetries = 10

// parkedRetryAt parks a node beyond every planning horizon once its retries
// are exhausted or its error is not retryable: GetPendingSync selects records
// with retry_at IS NULL OR retry_at <= now, so a far-future timestamp removes
// the node from every scan. The cause stays visible in last_error. Any
// legitimate status re-transition resets retry metadata and releases the node.
var parkedRetryAt = time.Date(9999, 12, 31, 23, 59, 0, 0, time.UTC)

// retryBackoffIntervals defines exponential-ish backoff for sync retries.
// Index = retryCount; beyond the last entry, the final interval is reused (capped).
var retryBackoffIntervals = []time.Duration{
	1 * time.Minute,
	2 * time.Minute,
	5 * time.Minute,
	15 * time.Minute,
	30 * time.Minute,
	45 * time.Minute,
	60 * time.Minute,
}

// CalculateRetryAt returns the next retry timestamp for the given retry count.
// Intervals: 1m → 2m → 5m → 15m → 30m → 45m → 60m (capped).
// After 6 failures further calls still get 60m and the record remains pending.
func CalculateRetryAt(retryCount int) time.Time {
	idx := retryCount
	if idx >= len(retryBackoffIntervals) {
		idx = len(retryBackoffIntervals) - 1
	}

	return time.Now().UTC().Truncate(time.Minute).Add(retryBackoffIntervals[idx])
}

// SyncPendingNodes fetches pending nodes across all subscriptions and processes them.
func (s *SyncService) SyncPendingNodes(ctx context.Context) error {
	logger.Debug("sync pending nodes started")

	// Парковка терминальна, но не вечна: нода, которая снова отвечает,
	// освобождает свои запаркованные биндинги до выборки pending, чтобы они
	// попали в этот же проход.
	s.releaseRecoveredNodes(ctx)
	s.refreshSyncGauges(ctx)

	pendingNodes, err := s.db.GetPendingSync(ctx)
	if err != nil {
		return fmt.Errorf("sync pending nodes: %w", err)
	}

	logger.Debug("pending nodes fetched",
		zap.Int("total_pending", len(pendingNodes)))

	if len(pendingNodes) == 0 {
		return nil
	}

	groups := make(map[uint][]database.SubscriptionNode, 0)
	for _, sn := range pendingNodes {
		groups[sn.SubscriptionID] = append(groups[sn.SubscriptionID], sn)
	}

	logger.Debug("pending nodes grouped",
		zap.Int("subscriptions_count", len(groups)))

	var errs []error

	for subID := range groups {
		unlock, err := s.lockSubscription(ctx, subID)
		if err != nil {
			errs = append(errs, fmt.Errorf("sync pending nodes: acquire lock for subscription %d: %w", subID, err))
			logger.Warn("sync pending nodes: failed to acquire lock",
				zap.Uint("subscription_id", subID),
				zap.Error(err))

			if utils.IsCtxErr(err) {
				break
			}

			continue
		}

		sub, subErr := s.db.GetByID(ctx, subID)
		if subErr != nil {
			if errors.Is(subErr, database.ErrSubscriptionNotFound) || errors.Is(subErr, gorm.ErrRecordNotFound) {
				clErr := s.db.DeleteSubscriptionNodesBySubscriptionID(ctx, subID)
				if clErr != nil {
					logger.Warn("purge orphan subscription nodes failed",
						zap.Uint("subscription_id", subID),
						zap.Error(clErr))
				} else {
					logger.Info("purged orphan subscription nodes",
						zap.Uint("subscription_id", subID))
				}
			}

			err := fmt.Errorf("sync pending nodes: load subscription %d: %w", subID, subErr)
			errs = append(errs, err)
			logger.Warn("sync subscription failed",
				zap.Uint("subscription_id", subID),
				zap.Error(err))
			unlock()

			continue
		}

		// Re-read pending nodes under the lock to fix TOCTOU: a concurrent
		// SyncSubscription may have already processed nodes between
		// GetPendingSync (above) and lockSubscription.
		freshNodes, fnErr := s.db.GetPendingBySubscriptionID(ctx, subID)
		if fnErr != nil {
			err := fmt.Errorf("sync pending nodes: re-read pending for subscription %d: %w", subID, fnErr)
			errs = append(errs, err)
			logger.Warn("sync subscription failed (re-read)",
				zap.Uint("subscription_id", subID),
				zap.Error(err))
			unlock()

			continue
		}

		if len(freshNodes) == 0 {
			logger.Debug("sync pending nodes: no pending after lock (already processed)",
				zap.Uint("subscription_id", subID))
			unlock()

			continue
		}

		logger.Debug("processing subscription pending nodes",
			zap.Uint("subscription_id", subID),
			zap.Int("pending_nodes", len(freshNodes)))

		err = s.syncNodes(ctx, sub, freshNodes)
		if err != nil {
			logger.Warn("sync subscription failed",
				zap.Uint("subscription_id", sub.ID),
				zap.Int("pending_nodes", len(freshNodes)),
				zap.Error(err))
			errs = append(errs, err)
		}

		// Use locked variant — we already hold the subscription lock.
		pruneErr := s.reconcilePlanNodesLocked(ctx, sub.ID)
		if pruneErr != nil {
			logger.Warn("reconcile plan nodes failed",
				zap.Uint("subscription_id", sub.ID),
				zap.Error(pruneErr))
			errs = append(errs, pruneErr)
		}

		unlock()
	}

	err = ctx.Err()
	if err != nil {
		return err
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	return nil
}

// parkedRetryThreshold отделяет парковку (retry_at в далёком будущем) от
// обычной ступени ладдера, которая не превышает 60 минут.
const parkedRetryThreshold = 61 * time.Minute

// syncStatusGauges перечисляет статусы, для которых gauge выставляется всегда:
// иначе исчезнувший статус оставлял бы в /metrics устаревшее значение.
var syncStatusGauges = []database.SyncStatus{
	database.SyncStatusActive,
	database.SyncStatusPendingAdd,
	database.SyncStatusPendingRemove,
	database.SyncStatusPendingUpdate,
}

// releaseRecoveredNodes снимает парковку с биндингов тех нод, которые снова
// отвечают на Ping. Пока панель отказывает, повторять бессмысленно — поэтому
// парковка ставится сразу; но после восстановления панели или ротации токена
// повод повторять уже есть. Без этого запаркованные подписки оставались без
// провизии до смены плана или оплаты, то есть навсегда.
func (s *SyncService) releaseRecoveredNodes(ctx context.Context) {
	parkedAfter := time.Now().UTC().Add(parkedRetryThreshold)

	nodeIDs, err := s.db.GetNodesWithParkedSync(ctx, parkedAfter)
	if err != nil {
		logger.Warn("sync: failed to list nodes with parked bindings", zap.Error(err))

		return
	}

	for _, nodeID := range nodeIDs {
		if ctx.Err() != nil {
			return
		}

		reader, ok := s.clients.Reader(nodeID)
		if !ok {
			continue
		}

		pingErr := reader.Ping(ctx)
		if pingErr != nil {
			logger.Debug("sync: node still unavailable, parked bindings kept",
				zap.Uint("node_id", nodeID),
				zap.Error(pingErr))

			continue
		}

		released, relErr := s.db.ReleaseParkedNodesOnNode(ctx, nodeID, parkedAfter)
		if relErr != nil {
			logger.Warn("sync: failed to release parked bindings",
				zap.Uint("node_id", nodeID),
				zap.Error(relErr))

			continue
		}

		if released > 0 {
			logger.Info("sync: released parked bindings after node recovery",
				zap.Uint("node_id", nodeID),
				zap.Int64("released", released))
		}
	}
}

// refreshSyncGauges обновляет метрики состояния биндингов: без них парковка и
// рост pending видны только по логам.
func (s *SyncService) refreshSyncGauges(ctx context.Context) {
	counts, err := s.db.CountSubscriptionNodesByStatus(ctx)
	if err != nil {
		logger.Warn("sync: failed to count subscription nodes by status", zap.Error(err))

		return
	}

	for _, status := range syncStatusGauges {
		metrics.SubscriptionNodes.WithLabelValues(string(status)).Set(float64(counts[string(status)]))
	}

	parkedAfter := time.Now().UTC().Add(parkedRetryThreshold)

	parked, err := s.db.CountParkedSubscriptionNodes(ctx, parkedAfter)
	if err != nil {
		logger.Warn("sync: failed to count parked bindings", zap.Error(err))

		return
	}

	metrics.SubscriptionNodesParked.Set(float64(parked))
}

// ApplyPlanToSubscription loads the subscription, marks its active nodes as
// pending_update for the current plan, and reconciles plan membership.
// The caller is responsible for invoking SyncSubscription afterwards.
func (s *SyncService) ApplyPlanToSubscription(ctx context.Context, subscriptionID uint) error {
	unlock, err := s.lockSubscription(ctx, subscriptionID)
	if err != nil {
		return fmt.Errorf("apply plan to subscription %d: acquire lock: %w", subscriptionID, err)
	}
	defer unlock()

	return s.applyPlanToSubscriptionLocked(ctx, subscriptionID)
}

func (s *SyncService) applyPlanToSubscriptionLocked(ctx context.Context, subscriptionID uint) error {
	sub, err := s.db.GetByID(ctx, subscriptionID)
	if err != nil {
		return fmt.Errorf("apply plan to subscription %d: load subscription: %w", subscriptionID, err)
	}

	newNodes, err := s.db.GetNodesByPlanID(ctx, sub.PlanID)
	if err != nil {
		return fmt.Errorf("apply plan to subscription %d: load plan nodes: %w", subscriptionID, err)
	}

	var newNodeIDs []uint

	for _, n := range newNodes {
		if n.IsActive {
			newNodeIDs = append(newNodeIDs, n.ID)
		}
	}

	err = s.db.MarkActiveNodesPendingUpdate(ctx, subscriptionID, newNodeIDs)
	if err != nil {
		return fmt.Errorf("apply plan to subscription %d: mark active nodes pending update: %w", subscriptionID, err)
	}

	err = s.reconcilePlanNodesLocked(ctx, subscriptionID)
	if err != nil {
		return fmt.Errorf("apply plan to subscription %d: reconcile plan nodes: %w", subscriptionID, err)
	}

	return nil
}

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
