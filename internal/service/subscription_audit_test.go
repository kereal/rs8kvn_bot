package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/metrics"
	"github.com/kereal/rs8kvn_bot/internal/testutil"
	promtestutil "github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Тесты гарантируют, что каждая точка мутации подписки в SubscriptionService
// пишет событие в audit_log. Фейк DatabaseService накапливает события в
// m.AuditLogs; logAudit проглатывает ошибки, поэтому silent-loss ловится
// только проверкой факта записи.

// auditTestSvc собирает SubscriptionService с фейком, накапливающим
// audit-события в m.AuditLogs.
func auditTestSvc(m *testutil.DatabaseService) *SubscriptionService {
	return NewSubscriptionService(m, nil, nil, nil, &config.Config{})
}

func TestSubscriptionService_Create_WritesCreatedAudit(t *testing.T) {
	t.Parallel()

	m := &testutil.DatabaseService{
		GetByTelegramIDFunc: func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
			return nil, gorm.ErrRecordNotFound
		},
		GetAnyByTelegramIDFunc: func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
			return nil, gorm.ErrRecordNotFound
		},
		GetPlanByNameFunc: func(ctx context.Context, name string) (*database.Plan, error) {
			return &database.Plan{ID: 1, Name: database.FreePlanName}, nil
		},
		CreateSubscriptionFunc: func(ctx context.Context, sub *database.Subscription, inviteCode string) error {
			sub.ID = 1
			return nil
		},
		GetNodesByPlanIDFunc:    func(ctx context.Context, planID uint) ([]database.Node, error) { return nil, nil },
		GetBySubscriptionIDFunc: func(ctx context.Context, subscriptionID uint) ([]database.SubscriptionNode, error) { return nil, nil },
	}

	svc := auditTestSvc(m)
	_, err := svc.Create(context.Background(), 123456, "audit-user", "INVITE1")
	require.NoError(t, err)

	events := m.AuditLogsByEvent(database.AuditEventSubscriptionCreated)
	require.Len(t, events, 1)

	e := events[0]
	assert.Equal(t, database.EntitySubscription, e.EntityType)
	assert.Equal(t, database.ActorUser, e.ActorType)
	require.NotNil(t, e.ActorID)
	assert.Equal(t, "123456", *e.ActorID)
	require.NotNil(t, e.NewState, "created-событие несёт начальное состояние")
}

func TestSubscriptionService_Create_AuditFailureDoesNotBreakFlow(t *testing.T) {
	t.Parallel()

	m := &testutil.DatabaseService{
		GetByTelegramIDFunc: func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
			return nil, gorm.ErrRecordNotFound
		},
		GetAnyByTelegramIDFunc: func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
			return nil, gorm.ErrRecordNotFound
		},
		GetPlanByNameFunc: func(ctx context.Context, name string) (*database.Plan, error) {
			return &database.Plan{ID: 1, Name: database.FreePlanName}, nil
		},
		CreateSubscriptionFunc: func(ctx context.Context, sub *database.Subscription, inviteCode string) error {
			sub.ID = 1
			return nil
		},
		GetNodesByPlanIDFunc:    func(ctx context.Context, planID uint) ([]database.Node, error) { return nil, nil },
		GetBySubscriptionIDFunc: func(ctx context.Context, subscriptionID uint) ([]database.SubscriptionNode, error) { return nil, nil },
		InsertAuditLogFunc: func(ctx context.Context, e *database.AuditLog) error {
			return errors.New("audit storage down")
		},
	}

	svc := auditTestSvc(m)
	result, err := svc.Create(context.Background(), 123456, "audit-user", "")
	require.NoError(t, err, "best-effort аудит не должен ломать бизнес-флоу")
	assert.NotNil(t, result)
}

func TestSubscriptionService_Reanimate_WritesReanimatedAudit(t *testing.T) {
	t.Parallel()

	m := &testutil.DatabaseService{
		GetByTelegramIDFunc: func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
			return nil, gorm.ErrRecordNotFound
		},
		GetAnyByTelegramIDFunc: func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
			return &database.Subscription{
				ID:         7,
				TelegramID: 123456,
				Status:     string(database.SubscriptionStatusRevoked),
			}, nil
		},
		GetPlanByNameFunc: func(ctx context.Context, name string) (*database.Plan, error) {
			return &database.Plan{ID: 1, Name: database.FreePlanName}, nil
		},
		DeleteSubscriptionNodesBySubscriptionIDFunc: func(ctx context.Context, subID uint) error { return nil },
		GetNodesByPlanIDFunc:                        func(ctx context.Context, planID uint) ([]database.Node, error) { return nil, nil },
		GetBySubscriptionIDFunc:                     func(ctx context.Context, subscriptionID uint) ([]database.SubscriptionNode, error) { return nil, nil },
	}

	svc := auditTestSvc(m)
	beforeReanimations := promtestutil.ToFloat64(metrics.SubscriptionReanimationsTotal)
	_, err := svc.reanimateRevokedSubscription(context.Background(), &database.Subscription{
		ID:         7,
		TelegramID: 123456,
		Status:     string(database.SubscriptionStatusRevoked),
	}, "")
	require.NoError(t, err)
	assert.Equal(t, float64(1), promtestutil.ToFloat64(metrics.SubscriptionReanimationsTotal)-beforeReanimations)

	events := m.AuditLogsByEvent(database.AuditEventSubscriptionReanimated)
	require.Len(t, events, 1)
	assert.Equal(t, database.EntitySubscription, events[0].EntityType)
}

func TestSubscriptionService_Downgrade_WritesDowngradedAudit(t *testing.T) {
	t.Parallel()

	m := &testutil.DatabaseService{
		GetPlanByNameFunc: func(ctx context.Context, name string) (*database.Plan, error) {
			return &database.Plan{ID: 1, Name: database.FreePlanName}, nil
		},
		GetNodesByPlanIDFunc:    func(ctx context.Context, planID uint) ([]database.Node, error) { return nil, nil },
		GetBySubscriptionIDFunc: func(ctx context.Context, subscriptionID uint) ([]database.SubscriptionNode, error) { return nil, nil },
	}

	svc := auditTestSvc(m)
	sub := &database.Subscription{
		ID:     9,
		Status: string(database.SubscriptionStatusActive),
		PlanID: 2,
	}
	_, err := svc.DowngradeToFreePlan(context.Background(), sub)
	require.NoError(t, err)

	events := m.AuditLogsByEvent(database.AuditEventDowngradedToFree)
	require.Len(t, events, 1)

	require.NotNil(t, events[0].Details)
	assert.Contains(t, events[0].Details, `"reason":"chargeback"`)
}

func TestSubscriptionService_AdminSetPlan_WritesPlanChangedWithAdminActor(t *testing.T) {
	t.Parallel()

	m := &testutil.DatabaseService{
		GetByIDFunc: func(ctx context.Context, id uint) (*database.Subscription, error) {
			expiry := time.Now().UTC().Add(24 * time.Hour)
			return &database.Subscription{ID: id, Status: string(database.SubscriptionStatusActive), PlanID: 2, ExpiresAt: &expiry}, nil
		},
		GetPlanByIDFunc: func(ctx context.Context, planID uint) (*database.Plan, error) {
			return &database.Plan{ID: planID, Name: "premium"}, nil
		},
		GetNodesByPlanIDFunc:    func(ctx context.Context, planID uint) ([]database.Node, error) { return nil, nil },
		GetBySubscriptionIDFunc: func(ctx context.Context, subscriptionID uint) ([]database.SubscriptionNode, error) { return nil, nil },
	}

	svc := auditTestSvc(m)
	adminID := int64(555000111)
	ctx := WithAuditAdmin(context.Background(), adminID)

	_, err := svc.AdminSetPlan(ctx, 9, 5, 30)
	require.NoError(t, err)

	events := m.AuditLogsByEvent(database.AuditEventPlanChanged)
	require.Len(t, events, 1)

	e := events[0]
	assert.Equal(t, database.ActorAdmin, e.ActorType)
	require.NotNil(t, e.ActorID)
	assert.Equal(t, "555000111", *e.ActorID)
	require.NotNil(t, e.OldState, "plan_changed обязан нести diff «до/после»")
	require.NotNil(t, e.NewState)
}

func TestSubscriptionService_AdminSetPlan_WithoutAdminContextUsesZeroActor(t *testing.T) {
	t.Parallel()

	m := &testutil.DatabaseService{
		GetByIDFunc: func(ctx context.Context, id uint) (*database.Subscription, error) {
			return &database.Subscription{ID: id, Status: string(database.SubscriptionStatusActive), PlanID: 2}, nil
		},
		GetPlanByIDFunc: func(ctx context.Context, planID uint) (*database.Plan, error) {
			return &database.Plan{ID: planID, Name: "premium"}, nil
		},
		GetNodesByPlanIDFunc:    func(ctx context.Context, planID uint) ([]database.Node, error) { return nil, nil },
		GetBySubscriptionIDFunc: func(ctx context.Context, subscriptionID uint) ([]database.SubscriptionNode, error) { return nil, nil },
	}

	svc := auditTestSvc(m)
	_, err := svc.AdminSetPlan(context.Background(), 9, 5, 0)
	require.NoError(t, err)

	events := m.AuditLogsByEvent(database.AuditEventPlanChanged)
	require.Len(t, events, 1)
	// Без WithAuditAdmin хелпер возвращает 0 — actor_id = "0".
	// Событие всё равно пишется: журнал полнее, чем тише.
	require.NotNil(t, events[0].ActorID)
	assert.Equal(t, "0", *events[0].ActorID)
}

func TestSubscriptionService_DeleteByID_WritesDeletedAudit(t *testing.T) {
	t.Parallel()

	m := &testutil.DatabaseService{
		GetByIDFunc: func(ctx context.Context, id uint) (*database.Subscription, error) {
			return &database.Subscription{ID: id, TelegramID: 123456, SubscriptionID: "sub-del", Status: string(database.SubscriptionStatusActive)}, nil
		},
		DeleteSubscriptionNodesBySubscriptionIDFunc: func(ctx context.Context, subID uint) error { return nil },
		DeleteSubscriptionByIDFunc: func(ctx context.Context, id uint) (*database.Subscription, error) {
			return &database.Subscription{ID: id}, nil
		},
	}

	svc := auditTestSvc(m)
	adminID := int64(555000111)
	sub, err := svc.DeleteByID(WithAuditAdmin(context.Background(), adminID), 9)
	require.NoError(t, err)
	require.NotNil(t, sub)

	events := m.AuditLogsByEvent(database.AuditEventSubscriptionDeleted)
	require.Len(t, events, 1)
	assert.Equal(t, database.ActorAdmin, events[0].ActorType)
	require.NotNil(t, events[0].ActorID)
	assert.Equal(t, "555000111", *events[0].ActorID)
	require.NotNil(t, events[0].OldState, "deleted обязан нести снапшот удалённой строки")
}

func TestSubscriptionService_ExpireSubscription_WritesExpiredAndDowngradedAudits(t *testing.T) {
	t.Parallel()

	m := &testutil.DatabaseService{
		GetByIDFunc: func(ctx context.Context, id uint) (*database.Subscription, error) {
			return &database.Subscription{ID: id, Status: string(database.SubscriptionStatusActive), PlanID: 2, SubscriptionID: "sub-exp"}, nil
		},
		GetPlanByNameFunc: func(ctx context.Context, name string) (*database.Plan, error) {
			return &database.Plan{ID: 1, Name: database.FreePlanName}, nil
		},
		GetNodesByPlanIDFunc:    func(ctx context.Context, planID uint) ([]database.Node, error) { return nil, nil },
		GetBySubscriptionIDFunc: func(ctx context.Context, subscriptionID uint) ([]database.SubscriptionNode, error) { return nil, nil },
	}

	svc := auditTestSvc(m)
	err := svc.ExpireSubscription(context.Background(), 9)
	require.NoError(t, err)

	require.Len(t, m.AuditLogsByEvent(database.AuditEventSubscriptionExpired), 1)
	require.Len(t, m.AuditLogsByEvent(database.AuditEventDowngradedToFree), 1)
}

func TestSubscriptionService_GetOrCreateSubscription_WritesCreatedAudit(t *testing.T) {
	t.Parallel()

	m := &testutil.DatabaseService{
		GetByTelegramIDFunc: func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
			return nil, gorm.ErrRecordNotFound
		},
		GetAnyByTelegramIDFunc: func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
			return nil, gorm.ErrRecordNotFound
		},
		GetPlanByNameFunc: func(ctx context.Context, name string) (*database.Plan, error) {
			return &database.Plan{ID: 1, Name: database.FreePlanName}, nil
		},
		CreateSubscriptionFunc: func(ctx context.Context, sub *database.Subscription, inviteCode string) error {
			sub.ID = 3
			return nil
		},
		GetNodesByPlanIDFunc:    func(ctx context.Context, planID uint) ([]database.Node, error) { return nil, nil },
		GetBySubscriptionIDFunc: func(ctx context.Context, subscriptionID uint) ([]database.SubscriptionNode, error) { return nil, nil },
	}

	svc := auditTestSvc(m)
	_, err := svc.GetOrCreateSubscription(context.Background(), 777888, "second-path", "")
	require.NoError(t, err)

	// Второй путь создания тоже обязан писать subscription_created.
	require.Len(t, m.AuditLogsByEvent(database.AuditEventSubscriptionCreated), 1)
}

func TestSubscriptionService_ReconcileOrphanedClients_WritesRevokedAndSummaryAudits(t *testing.T) {
	t.Parallel()

	orphan := database.Subscription{
		ID:             11,
		TelegramID:     123456,
		SubscriptionID: "sub-orphan",
		Status:         string(database.SubscriptionStatusActive),
	}

	m := &testutil.DatabaseService{
		GetAllSubscriptionsFunc: func(ctx context.Context) ([]database.Subscription, error) {
			return []database.Subscription{orphan}, nil
		},
		GetBySubscriptionIDFunc: func(ctx context.Context, subscriptionID uint) ([]database.SubscriptionNode, error) {
			// Единственный биндинг pending_remove => подписка orphan.
			return []database.SubscriptionNode{{SubscriptionID: subscriptionID, NodeID: 1, Status: database.SyncStatusPendingRemove}}, nil
		},
		CountActiveSubscriptionsFunc:  func(ctx context.Context) (int64, error) { return 0, nil },
		CountPremiumSubscriptionsFunc: func(ctx context.Context) (int64, error) { return 0, nil },
	}

	svc := auditTestSvc(m)
	revoked, err := svc.ReconcileOrphanedClients(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, revoked)

	require.Len(t, m.AuditLogsByEvent(database.AuditEventSubscriptionRevoked), 1)

	summary := m.AuditLogsByEvent(database.AuditEventOrphanedClientsRevoked)
	require.Len(t, summary, 1, "сводка за прогон пишется при revoked > 0")
	assert.Equal(t, database.EntitySystem, summary[0].EntityType)
}

func TestSubscriptionService_ReconcileOrphanedClients_NoRevocations_NoSummaryEvent(t *testing.T) {
	t.Parallel()

	m := &testutil.DatabaseService{
		GetAllSubscriptionsFunc: func(ctx context.Context) ([]database.Subscription, error) {
			return nil, nil
		},
		CountActiveSubscriptionsFunc:  func(ctx context.Context) (int64, error) { return 0, nil },
		CountPremiumSubscriptionsFunc: func(ctx context.Context) (int64, error) { return 0, nil },
	}

	svc := auditTestSvc(m)
	revoked, err := svc.ReconcileOrphanedClients(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 0, revoked)

	assert.Empty(t, m.AuditLogsByEvent(database.AuditEventOrphanedClientsRevoked),
		"нулевой прогон не должен засорять журнал — сводка только при revoked > 0")
}
