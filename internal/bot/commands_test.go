package bot

import (
	"context"
	"errors"
	"strings"
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
	ctx := context.Background()

	tests := []struct {
		name          string
		cfg           *config.Config
		args          string
		setupMock     func(db *testutil.MockDatabaseService)
		wantText      string
		wantSendCount int
	}{
		{
			name: "valid trial code",
			cfg: &config.Config{
				TelegramAdminID: 0, // No admin notification to keep LastSentText predictable
				TrafficLimitGB:  50,
				XUIInboundID:    1,
			},
			args: "trial_abc12345",
			setupMock: func(db *testutil.MockDatabaseService) {
				db.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
					return nil, gorm.ErrRecordNotFound
				}
				db.BindTrialSubscriptionFunc = func(ctx context.Context, subscriptionID string, telegramID int64, username string) (*database.Subscription, error) {
					return &database.Subscription{
						TelegramID:     telegramID,
						Username:       username,
						SubscriptionID: subscriptionID,
						ClientID:       "client-123",
						InviteCode:     "invite-code",
						Status:         "active",
					}, nil
				}
				db.GetInviteByCodeFunc = func(ctx context.Context, code string) (*database.Invite, error) {
					return &database.Invite{
						Code:         code,
						ReferrerTGID: 999888,
					}, nil
				}
			},
			wantText:      "Подписка активирована",
			wantSendCount: 1,
		},
		{
			name: "user already has subscription",
			cfg: &config.Config{
				TelegramAdminID: 0,
				TrafficLimitGB:  50,
			},
			args: "trial_xyz99999",
			setupMock: func(db *testutil.MockDatabaseService) {
				db.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
					return &database.Subscription{
						TelegramID: telegramID,
						Status:     "active",
					}, nil
				}
			},
			wantText:      "уже есть активная",
			wantSendCount: 1,
		},
		{
			name: "bind fails",
			cfg: &config.Config{
				TelegramAdminID: 0,
				TrafficLimitGB:  50,
			},
			args: "trial_fail123",
			setupMock: func(db *testutil.MockDatabaseService) {
				db.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
					return nil, gorm.ErrRecordNotFound
				}
				db.BindTrialSubscriptionFunc = func(ctx context.Context, subscriptionID string, telegramID int64, username string) (*database.Subscription, error) {
					return nil, errors.New("bind failed")
				}
			},
			wantText:      "❌",
			wantSendCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockDB := testutil.NewMockDatabaseService()
			mockXUI := testutil.NewMockXUIClient()
			mockBot := testutil.NewMockBotAPI()
			handler := NewHandler(mockBot, tt.cfg, mockDB, mockXUI, NewTestBotConfig())
			tt.setupMock(mockDB)

			fullText := "/start " + tt.args
			update := tgbotapi.Update{
				Message: &tgbotapi.Message{
					Chat: &tgbotapi.Chat{ID: 123456},
					From: &tgbotapi.User{ID: 123456, UserName: "testuser"},
					Text: fullText,
					Entities: []tgbotapi.MessageEntity{
						{Type: "bot_command", Offset: 0, Length: 6}, // "/start"
					},
				},
			}

			handler.HandleStart(ctx, update)

			assert.True(t, mockBot.SendCalledSafe(), "Should send a message")
			assert.Equal(t, tt.wantSendCount, mockBot.SendCountSafe(), "Should send expected number of messages")
			assert.Contains(t, mockBot.LastSentText, tt.wantText, "Should contain expected text")
		})
	}
}

func TestHandleStart_NormalFlow(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123,
		TrafficLimitGB:  50,
	}
	ctx := context.Background()

	tests := []struct {
		name      string
		setupMock func(db *testutil.MockDatabaseService)
		wantText  string
	}{
		{
			name: "user with active subscription",
			setupMock: func(db *testutil.MockDatabaseService) {
				db.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
					return &database.Subscription{
						TelegramID: telegramID,
						Status:     "active",
						Username:   "testuser",
					}, nil
				}
			},
			wantText: "кнопки",
		},
		{
			name: "user without subscription",
			setupMock: func(db *testutil.MockDatabaseService) {
				db.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
					return nil, gorm.ErrRecordNotFound
				}
			},
			wantText: "получить подписку",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockDB := testutil.NewMockDatabaseService()
			mockXUI := testutil.NewMockXUIClient()
			mockBot := testutil.NewMockBotAPI()
			handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig())
			tt.setupMock(mockDB)

			update := tgbotapi.Update{
				Message: &tgbotapi.Message{
					Chat: &tgbotapi.Chat{ID: 123456},
					From: &tgbotapi.User{ID: 123456, UserName: "testuser"},
					Text: "/start",
					Entities: []tgbotapi.MessageEntity{
						{Type: "bot_command", Offset: 0, Length: 6},
					},
				},
			}

			handler.HandleStart(ctx, update)

			assert.True(t, mockBot.SendCalledSafe(), "Should send a message")
			assert.Contains(t, mockBot.LastSentText, tt.wantText, "Should contain expected text")
		})
	}
}

func TestHandleStart_NilFrom(t *testing.T) {
	cfg := &config.Config{TelegramAdminID: 123}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	handler := NewHandler(testutil.NewMockBotAPI(), cfg, mockDB, mockXUI, NewTestBotConfig())

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
	handler := NewHandler(testutil.NewMockBotAPI(), cfg, mockDB, mockXUI, NewTestBotConfig())

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
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig())

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

	handler.HandleInvite(ctx, update)

	assert.True(t, mockBot.SendCalledSafe(), "Should send invite link")
	assert.Contains(t, mockBot.LastSentText, "пригласительная ссылка", "Should contain invite link text")
}

func TestHandleInvite_DatabaseError(t *testing.T) {
	cfg := &config.Config{
		SiteURL: "https://example.com",
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig())

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

	handler.HandleInvite(ctx, update)

	assert.True(t, mockBot.SendCalledSafe(), "Should send error message")
	assert.Contains(t, mockBot.LastSentText, "❌", "Should show error")
}

func TestHandleHelp_Success(t *testing.T) {
	cfg := &config.Config{
		TrafficLimitGB: 100,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig())

	ctx := context.Background()
	update := tgbotapi.Update{
		Message: &tgbotapi.Message{
			Chat: &tgbotapi.Chat{ID: 123456},
		},
	}

	handler.HandleHelp(ctx, update)

	assert.True(t, mockBot.SendCalledSafe(), "Should send help message")
	assert.Contains(t, mockBot.LastSentText, "Справка", "Help should contain title")
	assert.Contains(t, mockBot.LastSentText, "/start", "Help should list commands")
}

func TestHandleHelp_VerifyHelpText(t *testing.T) {
	cfg := &config.Config{
		TrafficLimitGB: 50,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	handler := NewHandler(testutil.NewMockBotAPI(), cfg, mockDB, mockXUI, NewTestBotConfig())

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
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig())

	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return &database.Subscription{
			TelegramID:     telegramID,
			SubscriptionID: "existing-sub-id",
			Status:         "active",
		}, nil
	}

	ctx := context.Background()
	handler.handleBindTrial(ctx, 123456, "testuser", "trial-code-123")

	assert.True(t, mockBot.SendCalledSafe(), "Should send a message")
	assert.Contains(t, mockBot.LastSentText, "уже есть", "Should inform about existing subscription")
}

func TestHandleBindTrial_DatabaseError(t *testing.T) {
	cfg := &config.Config{
		TrafficLimitGB: 50,
		XUIInboundID:   1,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig())

	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return nil, gorm.ErrRecordNotFound
	}
	mockDB.BindTrialSubscriptionFunc = func(ctx context.Context, subscriptionID string, telegramID int64, username string) (*database.Subscription, error) {
		return nil, errors.New("database error")
	}

	ctx := context.Background()
	handler.handleBindTrial(ctx, 123456, "testuser", "trial-code-123")

	assert.True(t, mockBot.SendCalledSafe(), "Should send error message")
	assert.Contains(t, mockBot.LastSentText, "❌", "Should show error")
}

func TestHandleBindTrial_WithReferrerNotification(t *testing.T) {
	cfg := &config.Config{
		TrafficLimitGB:  50,
		XUIInboundID:    1,
		TelegramAdminID: 999999,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig())

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
			ReferredBy:     888777, // Set referrer
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
	handler.handleBindTrial(ctx, 123456, "testuser", "trial-code-123")

	assert.True(t, mockBot.SendCalledSafe(), "Should send messages")

	// Verify all messages sent
	messages := mockBot.GetAllSentMessages()
	assert.GreaterOrEqual(t, len(messages), 2, "Should send at least 2 messages: user + referrer")

	// Verify referrer notification
	referrerNotified := false
	for _, msg := range messages {
		if msg.ChatID == 888777 && strings.Contains(msg.Text, "активировал подписку") {
			referrerNotified = true
			break
		}
	}
	assert.True(t, referrerNotified, "Referrer (888777) should receive notification about activation")
}

func TestHandleStart_AdminUser(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  50,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig())

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

	handler.HandleStart(ctx, update)

	assert.True(t, mockBot.SendCalledSafe(), "Should send welcome message")
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
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig())

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
	handler.handleBindTrial(ctx, 123456, "testuser", "trial-code-123")

	assert.True(t, mockBot.SendCalledSafe(), "Should send subscription info")
	assert.Contains(t, mockBot.LastSentText, "Подписка активирована", "Should contain activation message")
}

func TestHandleShareStart_UserWithExistingSubscription(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  100,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig())

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
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig())

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
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig())

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
}

func TestHandleStart_NilMessage(t *testing.T) {
	cfg := &config.Config{TelegramAdminID: 123456, TrafficLimitGB: 100}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig())

	ctx := context.Background()
	update := tgbotapi.Update{Message: nil}

	handler.HandleStart(ctx, update)

	assert.False(t, mockBot.SendCalledSafe(), "Should not send when message is nil")
}

func TestHandleStart_ExistingSubscription(t *testing.T) {
	cfg := &config.Config{TelegramAdminID: 123456, TrafficLimitGB: 100}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig())

	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return &database.Subscription{
			TelegramID:      123456,
			Username:        "testuser",
			Status:          "active",
			SubscriptionURL: "https://sub.url",
		}, nil
	}

	ctx := context.Background()
	update := tgbotapi.Update{
		Message: &tgbotapi.Message{
			Chat:     &tgbotapi.Chat{ID: 123456},
			From:     &tgbotapi.User{ID: 123456, UserName: "testuser"},
			Text:     "/start",
			Entities: []tgbotapi.MessageEntity{{Type: "bot_command", Offset: 0, Length: 6}},
		},
	}

	handler.HandleStart(ctx, update)

	assert.True(t, mockBot.SendCalledSafe())
	assert.Contains(t, mockBot.LastSentTextSafe(), "testuser")
}

func TestHandleHelp_NilMessage(t *testing.T) {
	cfg := &config.Config{TelegramAdminID: 123456, TrafficLimitGB: 100}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig())

	ctx := context.Background()
	update := tgbotapi.Update{Message: nil}

	handler.HandleHelp(ctx, update)

	assert.False(t, mockBot.SendCalledSafe(), "Should not send when message is nil")
}

func TestHandleShareStart_WithExistingSubscription(t *testing.T) {
	cfg := &config.Config{TelegramAdminID: 123456, TrafficLimitGB: 100}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig())

	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return &database.Subscription{
			TelegramID: telegramID,
			Username:   "testuser",
			Status:     "active",
		}, nil
	}

	ctx := context.Background()
	handler.handleShareStart(ctx, 123456, "testuser", "ABC123")

	assert.True(t, mockBot.SendCalledSafe())
	assert.Contains(t, mockBot.LastSentTextSafe(), "testuser")

	handler.pendingMu.RLock()
	_, exists := handler.pendingInvites[123456]
	handler.pendingMu.RUnlock()
	assert.False(t, exists, "Should not cache invite code when user has existing subscription")
}

func TestHandleStart_SharePrefix(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  100,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig())

	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return nil, gorm.ErrRecordNotFound
	}

	ctx := context.Background()
	update := tgbotapi.Update{
		Message: &tgbotapi.Message{
			Chat:     &tgbotapi.Chat{ID: 123456},
			From:     &tgbotapi.User{ID: 123456, UserName: "testuser"},
			Text:     "/start share_ABC123",
			Entities: []tgbotapi.MessageEntity{{Type: "bot_command", Offset: 0, Length: 6}},
		},
	}

	handler.HandleStart(ctx, update)

	assert.True(t, mockBot.SendCalledSafe(), "Should send message for share prefix")
}

func TestHandleBindTrial_UpdateClientError(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 0,
		TrafficLimitGB:  100,
		XUIInboundID:    1,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig())

	sub := &database.Subscription{
		TelegramID:     123456,
		Username:       "testuser",
		SubscriptionID: "test-sub-id",
		ClientID:       "test-client-id",
		InviteCode:     "ABC123",
		Status:         "active",
	}
	callCount := 0
	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		callCount++
		if callCount == 1 {
			// First call: check if user already has subscription
			return nil, gorm.ErrRecordNotFound
		}
		// Subsequent calls: return referrer subscription
		return &database.Subscription{TelegramID: telegramID, Username: "referrer"}, nil
	}
	mockDB.BindTrialSubscriptionFunc = func(ctx context.Context, subscriptionID string, telegramID int64, username string) (*database.Subscription, error) {
		return sub, nil
	}
	mockDB.GetInviteByCodeFunc = func(ctx context.Context, code string) (*database.Invite, error) {
		return &database.Invite{Code: "ABC123", ReferrerTGID: 999999}, nil
	}
	mockXUI.UpdateClientFunc = func(ctx context.Context, inboundID int, clientID, email, subID string, trafficBytes int64, expiryTime time.Time, telegramID int64, comment string) error {
		return errors.New("update client failed")
	}

	ctx := context.Background()
	handler.handleBindTrial(ctx, 123456, "testuser", "ABC123")

	assert.True(t, mockBot.SendCalledSafe(), "Should send success message even if update client fails")
	assert.Contains(t, mockBot.LastSentTextSafe(), "Подписка активирована")
}

func TestHandleBindTrial_GetInviteError(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  100,
		XUIInboundID:    1,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig())

	sub := &database.Subscription{
		TelegramID:     123456,
		Username:       "testuser",
		SubscriptionID: "test-sub-id",
		ClientID:       "test-client-id",
		InviteCode:     "ABC123",
		Status:         "active",
	}
	mockDB.BindTrialSubscriptionFunc = func(ctx context.Context, subscriptionID string, telegramID int64, username string) (*database.Subscription, error) {
		return sub, nil
	}
	mockDB.GetInviteByCodeFunc = func(ctx context.Context, code string) (*database.Invite, error) {
		return nil, errors.New("get invite failed")
	}
	mockXUI.UpdateClientFunc = func(ctx context.Context, inboundID int, clientID, email, subID string, trafficBytes int64, expiryTime time.Time, telegramID int64, comment string) error {
		return nil
	}

	ctx := context.Background()
	handler.handleBindTrial(ctx, 123456, "testuser", "ABC123")

	assert.True(t, mockBot.SendCalledSafe(), "Should send success message even if get invite fails")
}

func TestHandleMySubscription_ShowLoadingFails(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  100,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig())

	mockBot.SendError = errors.New("send failed")

	dbCalled := false
	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		dbCalled = true
		return nil, gorm.ErrRecordNotFound
	}

	ctx := context.Background()
	handler.handleMySubscription(ctx, 123456, "testuser", 1)

	assert.False(t, dbCalled, "Database should not be called when loading fails")
}
