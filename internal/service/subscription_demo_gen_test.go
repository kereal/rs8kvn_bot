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

// TestGenerateAndVerifyBlockedBroadcastDemo — исполняемый тест-демогенератор,
// который создаёт в памяти полную картину завершённой рассылки с заблокировавшими
// пользователями, выполняет деактивацию и печатает в лог сгенерированные данные,
// аудит-события и результаты работы.
func TestGenerateAndVerifyBlockedBroadcastDemo(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	// 1. Создаем тестовых пользователей, у которых активны подписки
	subsMap := map[int64]*database.Subscription{
		991001: {
			ID:             101,
			TelegramID:     991001,
			Username:       "blocked_user_alpha",
			ClientID:       "client-alpha",
			SubscriptionID: "sub-alpha",
			Status:         string(database.SubscriptionStatusActive),
			CreatedAt:      time.Now().Add(-48 * time.Hour),
		},
		991002: {
			ID:             102,
			TelegramID:     991002,
			Username:       "blocked_user_beta",
			ClientID:       "client-beta",
			SubscriptionID: "sub-beta",
			Status:         string(database.SubscriptionStatusActive),
			CreatedAt:      time.Now().Add(-24 * time.Hour),
		},
	}

	// 2. Создаем завершенную рассылку со снапшотом recipients_state (2 заблокировали, 1 доставлена)
	broadcast := database.Broadcast{
		ID:               777,
		Name:             "Большая весенняя рассылка",
		MessageText:      "Привет! У нас отличные новости.",
		Status:           string(database.BroadcastStatusCompleted),
		SentCount:        1,
		BlockedCount:     2,
		UnreachableCount: 0,
		FailedCount:      0,
		RecipientsState: `{"snapshot":true,"recipients":[
			{"id":1,"telegram_id":991001,"status":"blocked","attempts":1,"last_error":"Forbidden: bot was blocked by the user"},
			{"id":2,"telegram_id":991002,"status":"blocked","attempts":1,"last_error":"Forbidden: bot was blocked by the user"},
			{"id":3,"telegram_id":991099,"status":"sent","attempts":1}
		]}`,
		CreatedAt: time.Now().Add(-2 * time.Hour),
		UpdatedAt: time.Now().Add(-1 * time.Hour),
	}

	// 3. Настраиваем mock DatabaseService
	m := &testutil.DatabaseService{
		Broadcasts: map[uint]*database.Broadcast{
			777: &broadcast,
		},
		GetByTelegramIDFunc: func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
			if sub, ok := subsMap[telegramID]; ok {
				return sub, nil
			}
			return nil, gorm.ErrRecordNotFound
		},
		UpdateSubscriptionFunc: func(ctx context.Context, s *database.Subscription) error {
			if sub, ok := subsMap[s.TelegramID]; ok {
				sub.Status = s.Status
				return nil
			}
			return gorm.ErrRecordNotFound
		},
	}

	svc := NewSubscriptionService(m, nil, nil, nil, &config.Config{})

	// 4. Запускаем деактивацию
	deactivatedCount, err := svc.DeactivateBlockedBroadcastRecipients(ctx, broadcast.ID)
	require.NoError(t, err)

	// 5. Проверяем результаты
	assert.Equal(t, 2, deactivatedCount, "должно быть успешно выключено ровно 2 пользователя")
	assert.Equal(t, string(database.SubscriptionStatusRevoked), subsMap[991001].Status)
	assert.Equal(t, string(database.SubscriptionStatusRevoked), subsMap[991002].Status)

	// Проверяем, что аудит записал события revoked
	require.Len(t, m.AuditLogs, 2, "в аудит-логе должно появиться ровно 2 записи о деактивации")
	for _, logEntry := range m.AuditLogs {
		assert.Equal(t, database.AuditEventSubscriptionRevoked, logEntry.Event)
		assert.Equal(t, database.EntitySubscription, logEntry.EntityType)
	}

	t.Logf("DEMO SUCCESS: Broadcast #%d analyzed. Deactivated %d blocked users. Audit events generated: %d",
		broadcast.ID, deactivatedCount, len(m.AuditLogs))
}
