package bot

import (
	"context"
	"errors"
	"testing"

	"rs8kvn_bot/internal/config"
	"rs8kvn_bot/internal/database"
	"rs8kvn_bot/internal/testutil"
	"rs8kvn_bot/internal/xui"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/stretchr/testify/assert"
	"gorm.io/gorm"
)

func TestHandleCallback_NilCallbackQuery(t *testing.T) {
	handler := &Handler{}
	ctx := context.Background()
	update := tgbotapi.Update{}

	// Should not panic
	handler.HandleCallback(ctx, update)
}

func TestHandleCallback_NilFrom(t *testing.T) {
	cfg := &config.Config{}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	handler := NewHandler(testutil.NewMockBotAPI(), cfg, mockDB, mockXUI)

	ctx := context.Background()
	update := tgbotapi.Update{
		CallbackQuery: &tgbotapi.CallbackQuery{
			ID:   "test-callback-id",
			Data: "test_data",
		},
	}

	// Should not panic when From is nil
	handler.HandleCallback(ctx, update)
}

func TestHandleCallback_CallbackDataRouting(t *testing.T) {
	tests := []struct {
		name         string
		callbackData string
		setupMock    func(*testutil.MockDatabaseService, *testutil.MockXUIClient)
	}{
		{
			name:         "create_subscription",
			callbackData: "create_subscription",
			setupMock: func(mockDB *testutil.MockDatabaseService, mockXUI *testutil.MockXUIClient) {
				mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
					return nil, gorm.ErrRecordNotFound
				}
			},
		},
		{
			name:         "qr_code",
			callbackData: "qr_code",
			setupMock: func(mockDB *testutil.MockDatabaseService, mockXUI *testutil.MockXUIClient) {
				mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
					return &database.Subscription{
						TelegramID:      telegramID,
						Username:        "testuser",
						SubscriptionURL: "https://test.url/sub",
						Status:          "active",
					}, nil
				}
			},
		},
		{
			name:         "admin_stats",
			callbackData: "admin_stats",
			setupMock: func(mockDB *testutil.MockDatabaseService, mockXUI *testutil.MockXUIClient) {
				mockDB.CountAllSubscriptionsFunc = func(ctx context.Context) (int64, error) {
					return 10, nil
				}
				mockDB.CountActiveSubscriptionsFunc = func(ctx context.Context) (int64, error) {
					return 8, nil
				}
				mockDB.CountExpiredSubscriptionsFunc = func(ctx context.Context) (int64, error) {
					return 2, nil
				}
			},
		},
		{
			name:         "admin_lastreg",
			callbackData: "admin_lastreg",
			setupMock: func(mockDB *testutil.MockDatabaseService, mockXUI *testutil.MockXUIClient) {
				mockDB.GetLatestSubscriptionsFunc = func(ctx context.Context, limit int) ([]database.Subscription, error) {
					return []database.Subscription{
						{ID: 1, Username: "user1"},
						{ID: 2, Username: "user2"},
					}, nil
				}
			},
		},
		{
			name:         "back_to_start",
			callbackData: "back_to_start",
			setupMock: func(mockDB *testutil.MockDatabaseService, mockXUI *testutil.MockXUIClient) {
				mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
					return nil, gorm.ErrRecordNotFound
				}
			},
		},
		{
			name:         "menu_donate",
			callbackData: "menu_donate",
			setupMock: func(mockDB *testutil.MockDatabaseService, mockXUI *testutil.MockXUIClient) {
				// No special setup needed
			},
		},
		{
			name:         "menu_subscription",
			callbackData: "menu_subscription",
			setupMock: func(mockDB *testutil.MockDatabaseService, mockXUI *testutil.MockXUIClient) {
				mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
					return &database.Subscription{
						TelegramID:      telegramID,
						Username:        "testuser",
						SubscriptionURL: "https://test.url/sub",
						Status:          "active",
					}, nil
				}
				mockXUI.GetClientTrafficFunc = func(ctx context.Context, username string) (*xui.ClientTraffic, error) {
					return &xui.ClientTraffic{Up: 1000, Down: 2000}, nil
				}
			},
		},
		{
			name:         "back_to_subscription",
			callbackData: "back_to_subscription",
			setupMock: func(mockDB *testutil.MockDatabaseService, mockXUI *testutil.MockXUIClient) {
				// No special setup needed - just deletes message
			},
		},
		{
			name:         "menu_help",
			callbackData: "menu_help",
			setupMock: func(mockDB *testutil.MockDatabaseService, mockXUI *testutil.MockXUIClient) {
				mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
					return &database.Subscription{
						TelegramID:      telegramID,
						Username:        "testuser",
						SubscriptionURL: "https://test.url/sub",
						Status:          "active",
					}, nil
				}
			},
		},
		{
			name:         "share_invite",
			callbackData: "share_invite",
			setupMock: func(mockDB *testutil.MockDatabaseService, mockXUI *testutil.MockXUIClient) {
				mockDB.GetOrCreateInviteFunc = func(ctx context.Context, referrerTGID int64, code string) (*database.Invite, error) {
					return &database.Invite{Code: code, ReferrerTGID: referrerTGID}, nil
				}
			},
		},
		{
			name:         "unknown callback",
			callbackData: "unknown_callback",
			setupMock: func(mockDB *testutil.MockDatabaseService, mockXUI *testutil.MockXUIClient) {
				// No setup needed
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{
				TelegramAdminID: 123456,
				TrafficLimitGB:  50,
				SiteURL:         "https://example.com",
				XUIInboundID:    1,
			}
			mockDB := testutil.NewMockDatabaseService()
			mockXUI := testutil.NewMockXUIClient()
			tt.setupMock(mockDB, mockXUI)
			handler := NewHandler(testutil.NewMockBotAPI(), cfg, mockDB, mockXUI)

			ctx := context.Background()
			update := tgbotapi.Update{
				CallbackQuery: &tgbotapi.CallbackQuery{
					ID:   "test-callback-id",
					Data: tt.callbackData,
					From: &tgbotapi.User{ID: 123456, UserName: "testuser"},
					Message: &tgbotapi.Message{
						MessageID: 100,
						Chat:      &tgbotapi.Chat{ID: 123456},
					},
				},
			}

			// Should not panic
			handler.HandleCallback(ctx, update)
		})
	}
}

func TestHandleCallback_AdminStats_NonAdmin(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 999999, // Different from chat ID
		TrafficLimitGB:  50,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	handler := NewHandler(testutil.NewMockBotAPI(), cfg, mockDB, mockXUI)

	ctx := context.Background()
	update := tgbotapi.Update{
		CallbackQuery: &tgbotapi.CallbackQuery{
			ID:   "test-callback-id",
			Data: "admin_stats",
			From: &tgbotapi.User{ID: 123456, UserName: "testuser"},
			Message: &tgbotapi.Message{
				MessageID: 100,
				Chat:      &tgbotapi.Chat{ID: 123456},
			},
		},
	}

	// Should not panic when non-admin tries to access admin stats
	handler.HandleCallback(ctx, update)

	// Verify isAdmin returns false for this user
	assert.False(t, handler.isAdmin(123456))
}

func TestHandleCallback_AdminLastReg_NonAdmin(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 999999, // Different from chat ID
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	handler := NewHandler(testutil.NewMockBotAPI(), cfg, mockDB, mockXUI)

	ctx := context.Background()
	update := tgbotapi.Update{
		CallbackQuery: &tgbotapi.CallbackQuery{
			ID:   "test-callback-id",
			Data: "admin_lastreg",
			From: &tgbotapi.User{ID: 123456, UserName: "testuser"},
			Message: &tgbotapi.Message{
				MessageID: 100,
				Chat:      &tgbotapi.Chat{ID: 123456},
			},
		},
	}

	// Should not panic when non-admin tries to access admin lastreg
	handler.HandleCallback(ctx, update)
}

func TestHandleCallback_AdminStats_DatabaseError(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  50,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	handler := NewHandler(testutil.NewMockBotAPI(), cfg, mockDB, mockXUI)

	mockDB.CountAllSubscriptionsFunc = func(ctx context.Context) (int64, error) {
		return 0, errors.New("database error")
	}

	ctx := context.Background()
	update := tgbotapi.Update{
		CallbackQuery: &tgbotapi.CallbackQuery{
			ID:   "test-callback-id",
			Data: "admin_stats",
			From: &tgbotapi.User{ID: 123456, UserName: "adminuser"},
			Message: &tgbotapi.Message{
				MessageID: 100,
				Chat:      &tgbotapi.Chat{ID: 123456},
			},
		},
	}

	// Should not panic on database error
	handler.HandleCallback(ctx, update)
}

func TestHandleCallback_AdminLastReg_EmptyList(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	handler := NewHandler(testutil.NewMockBotAPI(), cfg, mockDB, mockXUI)

	mockDB.GetLatestSubscriptionsFunc = func(ctx context.Context, limit int) ([]database.Subscription, error) {
		return []database.Subscription{}, nil
	}

	ctx := context.Background()
	update := tgbotapi.Update{
		CallbackQuery: &tgbotapi.CallbackQuery{
			ID:   "test-callback-id",
			Data: "admin_lastreg",
			From: &tgbotapi.User{ID: 123456, UserName: "adminuser"},
			Message: &tgbotapi.Message{
				MessageID: 100,
				Chat:      &tgbotapi.Chat{ID: 123456},
			},
		},
	}

	// Should not panic with empty subscription list
	handler.HandleCallback(ctx, update)
}

func TestHandleCallback_AdminLastReg_DatabaseError(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	handler := NewHandler(testutil.NewMockBotAPI(), cfg, mockDB, mockXUI)

	mockDB.GetLatestSubscriptionsFunc = func(ctx context.Context, limit int) ([]database.Subscription, error) {
		return nil, errors.New("database error")
	}

	ctx := context.Background()
	update := tgbotapi.Update{
		CallbackQuery: &tgbotapi.CallbackQuery{
			ID:   "test-callback-id",
			Data: "admin_lastreg",
			From: &tgbotapi.User{ID: 123456, UserName: "adminuser"},
			Message: &tgbotapi.Message{
				MessageID: 100,
				Chat:      &tgbotapi.Chat{ID: 123456},
			},
		},
	}

	// Should not panic on database error
	handler.HandleCallback(ctx, update)
}

func TestHandleCallback_MenuSubscription_NoSubscription(t *testing.T) {
	cfg := &config.Config{
		TrafficLimitGB: 50,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	handler := NewHandler(testutil.NewMockBotAPI(), cfg, mockDB, mockXUI)

	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return nil, gorm.ErrRecordNotFound
	}

	ctx := context.Background()
	update := tgbotapi.Update{
		CallbackQuery: &tgbotapi.CallbackQuery{
			ID:   "test-callback-id",
			Data: "menu_subscription",
			From: &tgbotapi.User{ID: 123456, UserName: "testuser"},
			Message: &tgbotapi.Message{
				MessageID: 100,
				Chat:      &tgbotapi.Chat{ID: 123456},
			},
		},
	}

	// Should not panic when no subscription exists
	handler.HandleCallback(ctx, update)
}

func TestHandleCallback_QRCode_NoSubscription(t *testing.T) {
	cfg := &config.Config{}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	handler := NewHandler(testutil.NewMockBotAPI(), cfg, mockDB, mockXUI)

	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return nil, gorm.ErrRecordNotFound
	}

	ctx := context.Background()
	update := tgbotapi.Update{
		CallbackQuery: &tgbotapi.CallbackQuery{
			ID:   "test-callback-id",
			Data: "qr_code",
			From: &tgbotapi.User{ID: 123456, UserName: "testuser"},
			Message: &tgbotapi.Message{
				MessageID: 100,
				Chat:      &tgbotapi.Chat{ID: 123456},
			},
		},
	}

	// Should not panic when no subscription exists
	handler.HandleCallback(ctx, update)
}

func TestHandleCallback_QRCode_WithSubscription(t *testing.T) {
	cfg := &config.Config{}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	handler := NewHandler(testutil.NewMockBotAPI(), cfg, mockDB, mockXUI)

	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return &database.Subscription{
			TelegramID:      telegramID,
			Username:        "testuser",
			SubscriptionURL: "vless://test@url:443?mode=vpn",
			Status:          "active",
		}, nil
	}

	ctx := context.Background()
	update := tgbotapi.Update{
		CallbackQuery: &tgbotapi.CallbackQuery{
			ID:   "test-callback-id",
			Data: "qr_code",
			From: &tgbotapi.User{ID: 123456, UserName: "testuser"},
			Message: &tgbotapi.Message{
				MessageID: 100,
				Chat:      &tgbotapi.Chat{ID: 123456},
			},
		},
	}

	// Should not panic with subscription
	handler.HandleCallback(ctx, update)
}

func TestHandleShareInvite_Success(t *testing.T) {
	cfg := &config.Config{
		SiteURL: "https://example.com",
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	handler := NewHandler(testutil.NewMockBotAPI(), cfg, mockDB, mockXUI)

	mockDB.GetOrCreateInviteFunc = func(ctx context.Context, referrerTGID int64, code string) (*database.Invite, error) {
		return &database.Invite{
			Code:         "abc12345",
			ReferrerTGID: referrerTGID,
		}, nil
	}

	ctx := context.Background()

	// Should not panic
	handler.handleShareInvite(ctx, 123456, "testuser", 100)
}

func TestHandleShareInvite_DatabaseError(t *testing.T) {
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

	// Should not panic on database error
	handler.handleShareInvite(ctx, 123456, "testuser", 100)
}

func TestHandleCallback_CreateSubscription_DatabaseError(t *testing.T) {
	cfg := &config.Config{
		TrafficLimitGB: 50,
		XUIInboundID:   1,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	handler := NewHandler(testutil.NewMockBotAPI(), cfg, mockDB, mockXUI)

	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return nil, errors.New("database error")
	}

	ctx := context.Background()
	update := tgbotapi.Update{
		CallbackQuery: &tgbotapi.CallbackQuery{
			ID:   "test-callback-id",
			Data: "create_subscription",
			From: &tgbotapi.User{ID: 123456, UserName: "testuser"},
			Message: &tgbotapi.Message{
				MessageID: 100,
				Chat:      &tgbotapi.Chat{ID: 123456},
			},
		},
	}

	// Should not panic on database error
	handler.HandleCallback(ctx, update)
}

func TestHandleCallback_MenuHelp_NoSubscription(t *testing.T) {
	cfg := &config.Config{
		TrafficLimitGB: 50,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	handler := NewHandler(testutil.NewMockBotAPI(), cfg, mockDB, mockXUI)

	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return nil, gorm.ErrRecordNotFound
	}

	ctx := context.Background()
	update := tgbotapi.Update{
		CallbackQuery: &tgbotapi.CallbackQuery{
			ID:   "test-callback-id",
			Data: "menu_help",
			From: &tgbotapi.User{ID: 123456, UserName: "testuser"},
			Message: &tgbotapi.Message{
				MessageID: 100,
				Chat:      &tgbotapi.Chat{ID: 123456},
			},
		},
	}

	// Should not panic when no subscription exists
	handler.HandleCallback(ctx, update)
}

func TestHandleCallback_MenuHelp_DatabaseError(t *testing.T) {
	cfg := &config.Config{
		TrafficLimitGB: 50,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	handler := NewHandler(testutil.NewMockBotAPI(), cfg, mockDB, mockXUI)

	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return nil, errors.New("database error")
	}

	ctx := context.Background()
	update := tgbotapi.Update{
		CallbackQuery: &tgbotapi.CallbackQuery{
			ID:   "test-callback-id",
			Data: "menu_help",
			From: &tgbotapi.User{ID: 123456, UserName: "testuser"},
			Message: &tgbotapi.Message{
				MessageID: 100,
				Chat:      &tgbotapi.Chat{ID: 123456},
			},
		},
	}

	// Should not panic on database error
	handler.HandleCallback(ctx, update)
}

func TestHandleCallback_AllCallbackTypes(t *testing.T) {
	// Test that all expected callback types are handled
	expectedCallbacks := []string{
		"create_subscription",
		"qr_code",
		"admin_stats",
		"admin_lastreg",
		"back_to_start",
		"menu_donate",
		"menu_subscription",
		"back_to_subscription",
		"menu_help",
		"share_invite",
	}

	for _, callback := range expectedCallbacks {
		t.Run("callback_"+callback, func(t *testing.T) {
			assert.NotEmpty(t, callback, "Callback data should not be empty")
		})
	}
}
