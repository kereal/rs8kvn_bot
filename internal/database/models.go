package database

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// Sentinel errors returned by Get* functions when a record is not found.
// Callers should use errors.Is to distinguish "not found" from infrastructure/DB errors.
var (
	ErrInviteNotFound       = errors.New("invite not found")
	ErrSubscriptionNotFound = fmt.Errorf("subscription not found: %w", gorm.ErrRecordNotFound)
	ErrPlanNotFound         = errors.New("plan not found")
	ErrOrderNotFound        = errors.New("order not found")
)

const (
	TrialPlanName = "trial"
	FreePlanName  = "free"
)

// Subscription represents a user's VPN subscription.
type Subscription struct {
	ID             uint      `gorm:"primaryKey"`
	// TelegramID — уникальный для каждой подписки. Trial подписки используют отрицательные ID.
	TelegramID     int64     `gorm:"uniqueIndex"`
	Username       string    `gorm:"size:255;index"`
	ClientID       string    `gorm:"size:255;uniqueIndex"`
	SubscriptionID string    `gorm:"size:255;index"`
	ExpiresAt      time.Time `gorm:"index:idx_expiry"`
	Status         string    `gorm:"default:active;size:50;index"`
	InviteCode     string    `gorm:"size:16;index"`
	PlanID         uint      `gorm:"index"`
	ReferredBy     int64     `gorm:"index"`
	ProductID      uint      `gorm:"index"`
	StartedAt      time.Time
	PricePaidCents int64     `gorm:"default:0"`
	Currency       string    `gorm:"size:3"`
	Devices        string    `gorm:"type:text;default:'[]'"` // JSON array of {header_key: value} device entries
	Ips            string    `gorm:"type:text;default:'[]'"` // JSON array of {ip: timestamp} entries
	CreatedAt      time.Time `gorm:"autoCreateTime"`
	UpdatedAt      time.Time `gorm:"autoUpdateTime"`

	Plan    *Plan              `gorm:"foreignKey:PlanID"`
	Product *Product           `gorm:"foreignKey:ProductID"`
	Orders  []Order            `gorm:"foreignKey:SubscriptionID"`
	Nodes   []SubscriptionNode `gorm:"foreignKey:SubscriptionID"`
}

type NodeType string

const (
	NodeType3xUI    NodeType = "3x-ui"
	NodeTypeProxman NodeType = "proxman"
)

// Node represents a configured 3x-ui panel source.
type Node struct {
	ID              uint      `gorm:"primaryKey;column:id"`
	Name            string    `gorm:"size:255;column:name"`
	IsActive        bool      `gorm:"default:true;column:is_active"`
	Host            string    `gorm:"size:255;column:host"`
	APIToken        string    `gorm:"size:255;column:api_token"`
	InboundIDs      string    `gorm:"type:text;not null;default:'[]';column:inbound_ids"`
	SubscriptionURL string    `gorm:"size:512;column:subscription_url"`
	Type            NodeType  `gorm:"type:varchar(10);not null;default:3x-ui;column:type" json:"type"`
	CreatedAt       time.Time `gorm:"autoCreateTime;column:created_at"`
	UpdatedAt       time.Time `gorm:"autoUpdateTime;column:updated_at"`

	PlanNodes []PlanNode `gorm:"foreignKey:NodeID"`
}

// Plan represents a subscription plan.
type Plan struct {
	ID           uint      `gorm:"primaryKey;column:id"`
	Name         string    `gorm:"size:50;uniqueIndex;column:name"`
	DevicesLimit int       `gorm:"default:1;column:devices_limit"`
	// TrafficLimit — лимит трафика в байтах. 0 = безлимит.
	TrafficLimit int64     `gorm:"default:0;column:traffic_limit"`
	CreatedAt    time.Time `gorm:"autoCreateTime;column:created_at"`
	UpdatedAt    time.Time `gorm:"autoUpdateTime;column:updated_at"`

	Products  []Product  `gorm:"foreignKey:PlanID"`
	PlanNodes []PlanNode `gorm:"foreignKey:PlanID"`
}

// PlanNode is the join model for M2M between Plan and Node.
type PlanNode struct {
	PlanID uint `gorm:"primaryKey;column:plan_id"`
	NodeID uint `gorm:"primaryKey;column:node_id"`

	Plan *Plan `gorm:"foreignKey:PlanID"`
	Node *Node `gorm:"foreignKey:NodeID"`
}

// Product represents a purchasable subscription product bound to a plan.
type Product struct {
	ID           uint      `gorm:"primaryKey;column:id"`
	PlanID       uint      `gorm:"not null;column:plan_id"`
	Name         string    `gorm:"size:255;not null;column:name"`
	DurationDays int       `gorm:"not null;column:duration_days"`
	PriceCents   int64     `gorm:"not null;column:price_cents"`
	Currency     string    `gorm:"size:3;not null;default:RUB;column:currency"`
	IsActive     bool      `gorm:"not null;default:true;column:is_active"`
	CreatedAt    time.Time `gorm:"autoCreateTime;column:created_at"`
	UpdatedAt    time.Time `gorm:"autoUpdateTime;column:updated_at"`

	Plan   *Plan   `gorm:"foreignKey:PlanID"`
	Orders []Order `gorm:"foreignKey:ProductID"`
}

// Order represents a recorded purchase event for a subscription.
// Statuses: pending | paid | expired | canceled.
//
// Fields:
//   - provider_payment_id — external payment ID from provider.
//   - paid_at — payment confirmation timestamp.
//   - activated_at — subscription activation timestamp.
//   - expires_at — payment invoice expiry (e.g. 30 minutes from creation).
type OrderStatus string

const (
	OrderStatusPending  OrderStatus = "pending"
	OrderStatusPaid     OrderStatus = "paid"
	OrderStatusExpired  OrderStatus = "expired"
	OrderStatusCanceled OrderStatus = "canceled"
)

type Order struct {
	ID                uint        `gorm:"primaryKey;column:id"`
	SubscriptionID    uint        `gorm:"not null;column:subscription_id"`
	ProductID         uint        `gorm:"not null;column:product_id"`
	Status            OrderStatus `gorm:"not null;size:16;column:status"`
	AmountCents       int64       `gorm:"not null;column:amount_cents"`
	Currency          string      `gorm:"size:3;not null;default:RUB;column:currency"`
	PaymentProvider   string      `gorm:"column:payment_provider"`
	ProviderPaymentID string      `gorm:"column:provider_payment_id"`
	CreatedAt         time.Time   `gorm:"not null;column:created_at"`
	PaidAt            *time.Time  `gorm:"column:paid_at"`
	ActivatedAt       *time.Time  `gorm:"column:activated_at"`
	ExpiresAt         *time.Time  `gorm:"column:expires_at"`

	Subscription *Subscription `gorm:"foreignKey:SubscriptionID"`
	Product      *Product      `gorm:"foreignKey:ProductID"`
}

// Invite represents a referral invite code.
type Invite struct {
	Code         string    `gorm:"primaryKey;size:16"`
	ReferrerTGID int64     `gorm:"index;not null"`
	CreatedAt    time.Time `gorm:"autoCreateTime"`

	Subscriptions []Subscription `gorm:"foreignKey:InviteCode"`
}

// SyncStatus represents the synchronization status of a subscription on a VPN node.
// Statuses: active | pending_add | pending_remove.
//
// Values:
//   - active — нода добавлена и последняя синхронизация прошла успешно.
//   - pending_add — запрошено добавление ноды, операция ещё не выполнена на панели.
//   - pending_remove — запрошено удаление ноды, операция ещё не выполнена на панели.
type SyncStatus string

const (
	SyncStatusActive        SyncStatus = "active"
	SyncStatusPendingAdd    SyncStatus = "pending_add"
	SyncStatusPendingRemove SyncStatus = "pending_remove"
)

// SubscriptionNode represents the actual synchronization state of a specific
// subscription with a specific VPN node (not plan-level, but concrete pair).
type SubscriptionNode struct {
	SubscriptionID uint       `gorm:"primaryKey;column:subscription_id"`
	NodeID         uint       `gorm:"primaryKey;column:node_id"`
	Status         SyncStatus `gorm:"not null;size:16;column:status"`
	RetryCount     int        `gorm:"not null;default:0;column:retry_count"`
	RetryAt        *time.Time `gorm:"column:retry_at"`
	LastError      *string    `gorm:"type:text;column:last_error"`
	UpdatedAt      time.Time  `gorm:"not null;autoUpdateTime;column:updated_at"`
}

// TrialRequest tracks trial requests for rate limiting.
type TrialRequest struct {
	ID        uint      `gorm:"primaryKey"`
	IP        string    `gorm:"size:45;index"`
	CreatedAt time.Time `gorm:"autoCreateTime"`
}

// SubscriptionFull holds a subscription together with its plan and active nodes.
type SubscriptionFull struct {
	Subscription Subscription
	Plan         Plan
	Nodes        []Node
}

// PoolStats contains database connection pool statistics.
type PoolStats struct {
	MaxOpen       int
	Open          int
	InUse         int
	Idle          int
	WaitCount     int64
	WaitDuration  time.Duration
	MaxIdleClosed int64
}

func (PlanNode) TableName() string {
	return "plan_nodes"
}

func (Product) TableName() string {
	return "products"
}

func (Order) TableName() string {
	return "orders"
}

func (Node) TableName() string {
	return "nodes"
}

func (Plan) TableName() string {
	return "plans"
}

func (Subscription) TableName() string {
	return "subscriptions"
}

func (Invite) TableName() string {
	return "invites"
}

func (TrialRequest) TableName() string {
	return "trial_requests"
}

func (SubscriptionNode) TableName() string {
	return "subscription_nodes"
}

// IsExpired returns true if the subscription has expired.
// A zero ExpiresAt means no expiry is set, so it is not considered expired.
func (s *Subscription) IsExpired() bool {
	if s.ExpiresAt.IsZero() {
		return false
	}
	return time.Now().After(s.ExpiresAt)
}

// IsActive returns true if the subscription is active and not expired.
func (s *Subscription) IsActive() bool {
	return s.Status == "active" && !s.IsExpired()
}

// GetDevices parses the Devices JSON string into a slice of header maps.
func (s *Subscription) GetDevices() ([]map[string]string, error) {
	if s.Devices == "" {
		return []map[string]string{}, nil
	}
	var devices []map[string]string
	if err := json.Unmarshal([]byte(s.Devices), &devices); err != nil {
		return nil, fmt.Errorf("failed to unmarshal devices: %w", err)
	}
	return devices, nil
}

// SetDevices serializes a slice of header maps into the Devices JSON string.
func (s *Subscription) SetDevices(devices []map[string]string) error {
	data, err := json.Marshal(devices)
	if err != nil {
		return fmt.Errorf("failed to marshal devices: %w", err)
	}
	s.Devices = string(data)
	return nil
}

// GetIPs parses the Ips JSON string into a slice of ip->timestamp maps.
func (s *Subscription) GetIPs() ([]map[string]string, error) {
	if s.Ips == "" {
		return []map[string]string{}, nil
	}
	var ips []map[string]string
	if err := json.Unmarshal([]byte(s.Ips), &ips); err != nil {
		return nil, fmt.Errorf("failed to unmarshal ips: %w", err)
	}
	return ips, nil
}

// SetIPs serializes a slice of ip->timestamp maps into the Ips JSON string.
func (s *Subscription) SetIPs(ips []map[string]string) error {
	data, err := json.Marshal(ips)
	if err != nil {
		return fmt.Errorf("failed to marshal ips: %w", err)
	}
	s.Ips = string(data)
	return nil
}

func (n *Node) GetInboundIDs() ([]int, error) {
	if n.InboundIDs == "" {
		return []int{}, nil
	}
	var ids []int
	if err := json.Unmarshal([]byte(n.InboundIDs), &ids); err != nil {
		return nil, fmt.Errorf("failed to unmarshal inbound_ids: %w", err)
	}
	return ids, nil
}

func (n *Node) SetInboundIDs(ids []int) error {
	if len(ids) == 0 {
		n.InboundIDs = "[]"
		return nil
	}
	data, err := json.Marshal(ids)
	if err != nil {
		return fmt.Errorf("failed to marshal inbound_ids: %w", err)
	}
	n.InboundIDs = string(data)
	return nil
}

// DefaultInboundIDs is used when a node has no inbound IDs configured.
// Changing this value in one place updates all fallback paths.
var DefaultInboundIDs = []int{1}

// ResolveInboundIDs returns the node's inbound IDs, falling back to DefaultInboundIDs
// when the stored list is empty or malformed. Callers must not repeat this fallback.
func (n *Node) ResolveInboundIDs() []int {
	ids, err := n.GetInboundIDs()
	if err != nil || len(ids) == 0 {
		out := make([]int, len(DefaultInboundIDs))
		copy(out, DefaultInboundIDs)
		return out
	}
	out := make([]int, len(ids))
	copy(out, ids)
	return out
}
