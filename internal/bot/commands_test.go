package bot

import (
	"context"
	"errors"
	"testing"
	"time"

	"rs8kvn_bot/internal/config"
	"rs8kvn_bot/internal/database"
	"rs8kvn_bot/internal/testutil"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/stretchr/testify/assert"
	"gorm.io/gorm"
)

// Note: TestHandleStart_NilMessage, TestHandleHelp_NilMessage, and TestGetUsername_EdgeCases
// are already defined in handlers_test.go and handlers_extended_test.go

func TestHandleStart_WithTrialCode(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123,
		TrafficLimitGB:  50,
		XUIInboundID:    1,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	handler := NewHandler(testutil.NewMockBotAPI(), cfg, mockDB, mockXUI)

	ctx := context.Background()

	tests := []struct {
		name      string
		args      string
		setupMock func()
	}{
		{
			name: "valid trial code",
			args: "trial_abc12345",
			setupMock: func() {
				mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
					return nil, gorm.ErrRecordNotFound
				}
				mockDB.BindTrialSubscriptionFunc = func(ctx context.Context, subscriptionID string, telegramID int64, username string) (*database.Subscription, error) {
					return &database.Subscription{
						TelegramID:     telegramID,
						Username:       username,
						SubscriptionID: subscriptionID,
						ClientID:       "client-123",
						InviteCode:     "invite-code",
						Status:         "active",
					}, nil
				}
				mockDB.GetInviteByCodeFunc = func(ctx context.Context, code string) (*database.Invite, error) {
					return &database.Invite{
						Code:         code,
						ReferrerTGID: 999888,
					}, nil
				}
			},
		},
		{
			name: "user already has subscription",
			args: "trial_xyz99999",
			setupMock: func() {
				mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
					return &database.Subscription{
						TelegramID: telegramID,
						Status:     "active",
					}, nil
				}
			},
		},
		{
			name: "bind fails",
			args: "trial_fail123",
			setupMock: func() {
				mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
					return nil, gorm.ErrRecordNotFound
				}
				mockDB.BindTrialSubscriptionFunc = func(ctx context.Context, subscriptionID string, telegramID int64, username string) (*database.Subscription, error) {
					return nil, errors.New("bind failed")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.setupMock()

			update := tgbotapi.Update{
				Message: &tgbotapi.Message{
					Chat: &tgbotapi.Chat{ID: 123456},
					From: &tgbotapi.User{ID: 123456, UserName: "testuser"},
					Text: "/start " + tt.args,
				},
			}

			// Should not panic
			handler.HandleStart(ctx, update)
		})
	}
}

func TestHandleStart_NormalFlow(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123,
		TrafficLimitGB:  50,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	handler := NewHandler(testutil.NewMockBotAPI(), cfg, mockDB, mockXUI)

	ctx := context.Background()

	tests := []struct {
		name      string
		setupMock func()
	}{
		{
			name: "user with active subscription",
			setupMock: func() {
				mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
					return &database.Subscription{
						TelegramID: telegramID,
						Status:     "active",
						Username:   "testuser",
					}, nil
				}
			},
		},
		{
			name: "user without subscription",
			setupMock: func() {
				mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
					return nil, gorm.ErrRecordNotFound
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.setupMock()

			update := tgbotapi.Update{
				Message: &tgbotapi.Message{
					Chat: &tgbotapi.Chat{ID: 123456},
					From: &tgbotapi.User{ID: 123456, UserName: "testuser"},
					Text: "/start",
				},
			}

			// Should not panic
			handler.HandleStart(ctx, update)
		})
	}
}

func TestHandleStart_NilFrom(t *testing.T) {
	cfg := &config.Config{TelegramAdminID: 123}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	handler := NewHandler(testutil.NewMockBotAPI(), cfg, mockDB, mockXUI)

	ctx := context.Background()
	update := tgbotapi.Update{
		Message: &tgbotapi.Message{
			Chat: &tgbotapi.Chat{ID: 123456},
		},
	}

	// Should not panic when From is nil
	handler.HandleStart(ctx, update)
}

func TestHandleInvite_NilMessage(t *testing.T) {
	handler := &Handler{}
	ctx := context.Background()
	update := tgbotapi.Update{}

	// Should not panic
	handler.HandleInvite(ctx, update)
}

func TestHandleInvite_NilFrom(t *testing.T) {
	cfg := &config.Config{}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	handler := NewHandler(testutil.NewMockBotAPI(), cfg, mockDB, mockXUI)

	ctx := context.Background()
	update := tgbotapi.Update{
		Message: &tgbotapi.Message{
			Chat: &tgbotapi.Chat{ID: 123456},
		},
	}

	// Should not panic
	handler.HandleInvite(ctx, update)
}

func TestHandleInvite_Success(t *testing.T) {
	cfg := &config.Config{
		SiteURL: "https://example.com",
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	handler := NewHandler(testutil.NewMockBotAPI(), cfg, mockDB, mockXUI)

	mockDB.GetOrCreateInviteFunc = func(ctx context.Context, referrerTGID int64, code string) (*database.Invite, error) {
		return &database.Invite{
			Code:         code,
			ReferrerTGID: referrerTGID,
		}, nil
	}

	ctx := context.Background()
	update := tgbotapi.Update{
		Message: &tgbotapi.Message{
			Chat: &tgbotapi.Chat{ID: 123456},
			From: &tgbotapi.User{ID: 123456, UserName: "testuser"},
		},
	}

	// Should not panic
	handler.HandleInvite(ctx, update)
}

func TestHandleInvite_DatabaseError(t *testing.T) {
	cfg := &config.Config{
		SiteURL: "https://example.com",
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	handler := NewHandler(testutil.NewMockBotAPI(), cfg, mockDB, mockXUI)

	mockDB.GetOrCreateInviteFunc = func(ctx context.Context, referrerTGID int64, code string) (*database.Invite, error) {
		return nil, errors.New("database error")
	}

	ctx := context.Background()
	update := tgbotapi.Update{
		Message: &tgbotapi.Message{
			Chat: &tgbotapi.Chat{ID: 123456},
			From: &tgbotapi.User{ID: 123456, UserName: "testuser"},
		},
	}

	// Should not panic
	handler.HandleInvite(ctx, update)
}

func TestHandleHelp_Success(t *testing.T) {
	cfg := &config.Config{
		TrafficLimitGB: 100,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	handler := NewHandler(testutil.NewMockBotAPI(), cfg, mockDB, mockXUI)

	ctx := context.Background()
	update := tgbotapi.Update{
		Message: &tgbotapi.Message{
			Chat: &tgbotapi.Chat{ID: 123456},
		},
	}

	// Should not panic
	handler.HandleHelp(ctx, update)
}

func TestHandleHelp_VerifyHelpText(t *testing.T) {
	cfg := &config.Config{
		TrafficLimitGB: 50,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	handler := NewHandler(testutil.NewMockBotAPI(), cfg, mockDB, mockXUI)

	// Test that help text contains expected content
	helpText := handler.getHelpText(50, "https://test.url/sub")

	assert.Contains(t, helpText, "50", "Help text should contain traffic limit")
	assert.Contains(t, helpText, "https://test.url/sub", "Help text should contain subscription URL")
	assert.Contains(t, helpText, "Happ", "Help text should mention the app")
	assert.Contains(t, helpText, "iOS", "Help text should contain iOS link")
	assert.Contains(t, helpText, "Android", "Help text should contain Android link")
}

func TestHandleBindTrial_AlreadyHasSubscription(t *testing.T) {
	cfg := &config.Config{
		TrafficLimitGB: 50,
		XUIInboundID:   1,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	handler := NewHandler(testutil.NewMockBotAPI(), cfg, mockDB, mockXUI)

	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return &database.Subscription{
			TelegramID:     telegramID,
			SubscriptionID: "existing-sub-id",
			Status:         "active",
		}, nil
	}

	ctx := context.Background()

	// Should not panic when user already has subscription
	handler.handleBindTrial(ctx, 123456, "testuser", "trial-code-123")
}

func TestHandleBindTrial_DatabaseError(t *testing.T) {
	cfg := &config.Config{
		TrafficLimitGB: 50,
		XUIInboundID:   1,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	handler := NewHandler(testutil.NewMockBotAPI(), cfg, mockDB, mockXUI)

	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return nil, gorm.ErrRecordNotFound
	}
	mockDB.BindTrialSubscriptionFunc = func(ctx context.Context, subscriptionID string, telegramID int64, username string) (*database.Subscription, error) {
		return nil, errors.New("database error")
	}

	ctx := context.Background()

	// Should not panic on database error
	handler.handleBindTrial(ctx, 123456, "testuser", "trial-code-123")
}

func TestHandleBindTrial_WithReferrerNotification(t *testing.T) {
	cfg := &config.Config{
		TrafficLimitGB:  50,
		XUIInboundID:    1,
		TelegramAdminID: 999999,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	handler := NewHandler(testutil.NewMockBotAPI(), cfg, mockDB, mockXUI)

	callCount := 0
	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		callCount++
		if callCount == 1 {
			return nil, gorm.ErrRecordNotFound
		}
		return nil, gorm.ErrRecordNotFound
	}
	mockDB.BindTrialSubscriptionFunc = func(ctx context.Context, subscriptionID string, telegramID int64, username string) (*database.Subscription, error) {
		return &database.Subscription{
			TelegramID:     telegramID,
			Username:       username,
			SubscriptionID: subscriptionID,
			ClientID:       "client-123",
			InviteCode:     "invite-code",
			Status:         "active",
		}, nil
	}
	mockDB.GetInviteByCodeFunc = func(ctx context.Context, code string) (*database.Invite, error) {
		return &database.Invite{
			Code:         code,
			ReferrerTGID: 888777,
		}, nil
	}

	ctx := context.Background()

	// Should not panic with referrer notification
	handler.handleBindTrial(ctx, 123456, "testuser", "trial-code-123")
}

func TestHandleStart_AdminUser(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  50,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	handler := NewHandler(testutil.NewMockBotAPI(), cfg, mockDB, mockXUI)

	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return nil, gorm.ErrRecordNotFound
	}

	ctx := context.Background()
	update := tgbotapi.Update{
		Message: &tgbotapi.Message{
			Chat: &tgbotapi.Chat{ID: 123456},
			From: &tgbotapi.User{ID: 123456, UserName: "adminuser"},
			Text: "/start",
		},
	}

	// Should not panic for admin user
	handler.HandleStart(ctx, update)

	// Verify admin check works
	assert.True(t, handler.isAdmin(123456))
}

func TestHandleBindTrial_Success(t *testing.T) {
	cfg := &config.Config{
		TrafficLimitGB:  50,
		XUIInboundID:    1,
		TelegramAdminID: 999999,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	handler := NewHandler(testutil.NewMockBotAPI(), cfg, mockDB, mockXUI)

	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return nil, gorm.ErrRecordNotFound
	}
	mockDB.BindTrialSubscriptionFunc = func(ctx context.Context, subscriptionID string, telegramID int64, username string) (*database.Subscription, error) {
		return &database.Subscription{
			TelegramID:     telegramID,
			Username:       username,
			SubscriptionID: subscriptionID,
			ClientID:       "client-123",
			InviteCode:     "invite-code",
			Status:         "active",
		}, nil
	}
	mockDB.GetInviteByCodeFunc = func(ctx context.Context, code string) (*database.Invite, error) {
		return nil, gorm.ErrRecordNotFound
	}

	ctx := context.Background()

	// Should not panic on successful bind
	handler.handleBindTrial(ctx, 123456, "testuser", "trial-code-123")
}

func TestHandleShareStart_UserWithExistingSubscription(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  100,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI)

	// User has active subscription
	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return &database.Subscription{
			TelegramID: telegramID,
			Status:     "active",
		}, nil
	}

	ctx := context.Background()
	handler.handleShareStart(ctx, 123456, "testuser", "ABC12345")

	assert.True(t, mockBot.SendCalled)
	// Should show main menu with subscription (not the invite message)
	assert.Contains(t, mockBot.LastSentText, "Используйте кнопки ниже")
}

func TestHandleShareStart_InvalidInviteCode(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  100,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI)

	// No subscription
	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return nil, nil
	}

	// Invalid invite code
	mockDB.GetInviteByCodeFunc = func(ctx context.Context, code string) (*database.Invite, error) {
		return nil, assert.AnError
	}

	ctx := context.Background()
	handler.handleShareStart(ctx, 123456, "testuser", "INVALID")

	assert.True(t, mockBot.SendCalled)
	// Should show menu for new user (not the invite message)
	assert.Contains(t, mockBot.LastSentText, "получить подписку")
}

func TestHandleShareStart_ValidInviteCode(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  100,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI)

	// No subscription
	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return nil, nil
	}

	// Valid invite code
	mockDB.GetInviteByCodeFunc = func(ctx context.Context, code string) (*database.Invite, error) {
		return &database.Invite{
			Code:         code,
			ReferrerTGID: 999999,
		}, nil
	}

	ctx := context.Background()
	handler.handleShareStart(ctx, 123456, "testuser", "ABC12345")

	assert.True(t, mockBot.SendCalled)
	assert.Contains(t, mockBot.LastSentText, "Вас пригласили!")

	// Check that invite was cached
	handler.pendingMu.RLock()
	pending, ok := handler.pendingInvites[123456]
	handler.pendingMu.RUnlock()

	assert.True(t, ok)
	assert.Equal(t, "ABC12345", pending.code)
	assert.True(t, pending.expiresAt.After(time.Now()))
}
