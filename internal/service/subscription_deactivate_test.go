package service

import (
	"context"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestDeactivateBlockedBroadcastRecipients(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	sub := &database.Subscription{
		ID:             10,
		TelegramID:     123456789,
		Username:       "blocked_user",
		ClientID:       "client-1",
		SubscriptionID: "sub-1",
		Status:         string(database.SubscriptionStatusActive),
		CreatedAt:      time.Now().UTC(),
	}

	broadcast := database.Broadcast{
		ID:               1,
		Name:             "Тестовая рассылка",
		MessageText:      "Привет",
		Status:           string(database.BroadcastStatusCompleted),
		BlockedCount:     1,
		UnreachableCount: 1,
		RecipientsState:  `{"snapshot":true,"recipients":[{"id":1,"telegram_id":123456789,"status":"blocked","attempts":1}]}`,
		CreatedAt:        time.Now().UTC(),
		UpdatedAt:        time.Now().UTC(),
	}

	m := &testutil.DatabaseService{
		Broadcasts: map[uint]*database.Broadcast{
			1: &broadcast,
		},
		GetByTelegramIDFunc: func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
			if telegramID == 123456789 {
				return sub, nil
			}
			return nil, gorm.ErrRecordNotFound
		},
		UpdateSubscriptionFunc: func(ctx context.Context, s *database.Subscription) error {
			sub.Status = s.Status
			return nil
		},
	}

	svc := NewSubscriptionService(m, nil, nil, nil, &config.Config{})

	count, err := svc.DeactivateBlockedBroadcastRecipients(ctx, 1)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
	assert.Equal(t, string(database.SubscriptionStatusRevoked), sub.Status)
	assert.Len(t, m.AuditLogs, 1)
}

