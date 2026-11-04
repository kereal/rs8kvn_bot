package bot

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"

	"github.com/kereal/rs8kvn_bot/internal/service"
	"github.com/kereal/rs8kvn_bot/internal/testutil"
	"github.com/kereal/rs8kvn_bot/internal/xui"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/stretchr/testify/assert"
	"gorm.io/gorm"
)

func TestHandleCallback_CallbackDataRouting(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		callbackData    string
		setupMock       func(*testutil.BotStoreFake, *testutil.XUIClient)
		setupSubService func(*testutil.BotStoreFake, *testutil.XUIClient, *config.Config) *service.SubscriptionService
		wantSend        bool
		wantText        string
		wantMinSendCnt  int // minimum bot.Send calls (loading + final message)
		wantRequest     bool
		wantAlertText   string
	}{
		{
			name:         "create_subscription",
			callbackData: "create_subscription",
			setupMock: func(mockDB *testutil.BotStoreFake, mockXUI *testutil.XUIClient) {
				mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
					return nil, gorm.ErrRecordNotFound
				}
				mockDB.GetPlanByNameFunc = func(ctx context.Context, name string) (*database.Plan, error) {
					return &database.Plan{ID: 1, Name: database.FreePlanName, TrafficLimit: 1073741824}, nil
				}
				mockDB.CreateSubscriptionFunc = func(ctx context.Context, sub *database.Subscription, inviteCode string) error {
					sub.ID = 1
					return nil
				}
				mockDB.GetNodesByPlanIDFunc = func(ctx context.Context, planID uint) ([]database.Node, error) {
					return nil, nil
				}
				mockDB.GetBySubscriptionIDFunc = func(ctx context.Context, subscriptionID uint) ([]database.SubscriptionNode, error) {
					return nil, nil
				}
			},
			setupSubService: func(mockDB *testutil.BotStoreFake, mockXUI *testutil.XUIClient, cfg *config.Config) *service.SubscriptionService {
				clients := service.NodeClients{1: {Reader: mockXUI}}
				nodes := []database.Node{{ID: 1, Name: "main", IsActive: true, Host: "http://example.com", APIToken: "token", InboundIDs: "[1]"}}

				return service.NewSubscriptionService(mockDB, clients, nodes, cfg)
			},
			wantSend:       true,
			wantText:       "Ваша подписка готова",
			wantMinSendCnt: 2, // loading message + final success message
		},
		{
			name:         "qr_code",
			callbackData: "qr_code",
			setupMock: func(mockDB *testutil.BotStoreFake, mockXUI *testutil.XUIClient) {
				mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
					return &database.Subscription{
						TelegramID: telegramID,
						Username:   "testuser",
						Status:     "active",
					}, nil
				}
			},
			wantSend: true,
			wantText: "",
		},
		{
			name:         "admin_stats",
			callbackData: "admin_stats",
			setupMock: func(mockDB *testutil.BotStoreFake, mockXUI *testutil.XUIClient) {
				mockDB.CountAllSubscriptionsFunc = func(ctx context.Context) (int64, error) {
					return 10, nil
				}
				mockDB.CountActiveSubscriptionsFunc = func(ctx context.Context) (int64, error) {
					return 8, nil
				}
			},
			wantSend: true,
			wantText: "10",
		},
		{
			name:         "admin_lastreg",
			callbackData: "admin_lastreg",
			setupMock: func(mockDB *testutil.BotStoreFake, mockXUI *testutil.XUIClient) {
				mockDB.GetLatestSubscriptionsFunc = func(ctx context.Context, limit int) ([]database.Subscription, error) {
					return []database.Subscription{
						{ID: 1, Username: "user1"},
						{ID: 2, Username: "user2"},
					}, nil
				}
			},
			wantSend: true,
			wantText: "user1",
		},
		{
			name:         "back_to_start",
			callbackData: "back_to_start",
			setupMock: func(mockDB *testutil.BotStoreFake, mockXUI *testutil.XUIClient) {
				mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
					return nil, gorm.ErrRecordNotFound
				}
			},
			wantSend: true,
			wantText: "Привет",
		},
		{
			name:         "menu_donate",
			callbackData: "menu_donate",
			setupMock: func(mockDB *testutil.BotStoreFake, mockXUI *testutil.XUIClient) {
				// No special setup needed
			},
			wantSend: true,
			wantText: "Поддержка",
		},
		{
			name:         "menu_subscription",
			callbackData: "menu_subscription",
			setupMock: func(mockDB *testutil.BotStoreFake, mockXUI *testutil.XUIClient) {
				mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
					return &database.Subscription{
						TelegramID: telegramID,
						Username:   "testuser",
						Status:     "active",
					}, nil
				}
				mockXUI.GetClientTrafficFunc = func(ctx context.Context, username string) (*xui.ClientTraffic, error) {
					return &xui.ClientTraffic{Up: 1000, Down: 2000}, nil
				}
			},
			setupSubService: func(mockDB *testutil.BotStoreFake, mockXUI *testutil.XUIClient, cfg *config.Config) *service.SubscriptionService {
				clients := service.NodeClients{1: {Reader: mockXUI}}
				nodes := []database.Node{{ID: 1, Name: "main", IsActive: true, Host: "http://example.com", APIToken: "token", InboundIDs: "[1]"}}

				return service.NewSubscriptionService(mockDB, clients, nodes, cfg)
			},
			wantSend: true,
			wantText: "Ваша подписка",
		},
		{
			name:         "back_to_subscription",
			callbackData: "back_to_subscription",
			setupMock: func(mockDB *testutil.BotStoreFake, mockXUI *testutil.XUIClient) {
				// No special setup needed - just deletes message via Request, not Send
			},
			wantSend: false,
			wantText: "",
		},
		{
			name:         "menu_help",
			callbackData: "menu_help",
			setupMock: func(mockDB *testutil.BotStoreFake, mockXUI *testutil.XUIClient) {
				mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
					return &database.Subscription{
						TelegramID: telegramID,
						Username:   "testuser",
						Status:     "active",
					}, nil
				}
			},
			wantSend: true,
			wantText: "Ваша подписка",
		},
		{
			name:         "share_invite",
			callbackData: "share_invite",
			setupMock: func(mockDB *testutil.BotStoreFake, mockXUI *testutil.XUIClient) {
				mockDB.GetOrCreateInviteFunc = func(ctx context.Context, referrerTGID int64, code string) (*database.Invite, error) {
					return &database.Invite{Code: code, ReferrerTGID: referrerTGID}, nil
				}
			},
			wantSend: true,
			wantText: "пригласительная",
		},
		{
			name:         "menu_documents",
			callbackData: "menu_documents",
			setupMock:    func(mockDB *testutil.BotStoreFake, mockXUI *testutil.XUIClient) {},
			wantSend:     true,
			wantText:     "Документы",
		},
		{
			name:         "menu_privacy",
			callbackData: "menu_privacy",
			setupMock:    func(mockDB *testutil.BotStoreFake, mockXUI *testutil.XUIClient) {},
			wantSend:     true,
			wantText:     "*Политика конфиденциальности*",
		},
		{
			name:         "menu_terms",
			callbackData: "menu_terms",
			setupMock:    func(mockDB *testutil.BotStoreFake, mockXUI *testutil.XUIClient) {},
			wantSend:     true,
			wantText:     "*Пользовательское соглашение*",
		},
		{
			name:         "menu_support",
			callbackData: "menu_support",
			setupMock:    func(mockDB *testutil.BotStoreFake, mockXUI *testutil.XUIClient) {},
			wantSend:     true,
			wantText:     "С любыми проблемами и вопросами обращайтесь сюда: @kereal",
		},
		{
			name:         "unknown callback",
			callbackData: "unknown_callback",
			setupMock: func(mockDB *testutil.BotStoreFake, mockXUI *testutil.XUIClient) {
				// No setup needed
			},
			wantSend: false,
			wantText: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := &config.Config{
				TelegramAdminID: 123456,
				SiteURL:         "https://example.com",
			}
			mockDB := testutil.NewBotStoreFake()
			mockXUI := testutil.NewXUIClient()
			mockBot := testutil.NewBotAPI()

			tt.setupMock(mockDB, mockXUI)

			var subService *service.SubscriptionService
			if tt.setupSubService != nil {
				subService = tt.setupSubService(mockDB, mockXUI, cfg)
			}

			handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), subService, "")

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

			handler.HandleCallback(ctx, update)

			if tt.wantSend {
				assert.True(t, mockBot.SendCalledSafe(), "Bot.Send should be called for %s", tt.name)

				if tt.wantMinSendCnt > 0 {
					assert.GreaterOrEqual(t, mockBot.SendCountSafe(), tt.wantMinSendCnt,
						"create flow must send at least the loading message and the final message (%s)", tt.name)
				}

				if tt.wantText != "" {
					switch tt.callbackData {
					case "menu_privacy", "menu_terms", "menu_support":
						all := mockBot.GetAllSentMessages()

						var combined strings.Builder
						for _, m := range all {
							combined.WriteString(m.Text)
						}

						assert.Contains(t, combined.String(), tt.wantText, "message should contain %q", tt.wantText)
					default:
						assert.Contains(t, mockBot.LastSentTextSafe(), tt.wantText, "message should contain %q", tt.wantText)
					}
				}
			} else {
				assert.False(t, mockBot.SendCalledSafe(), "Bot.Send should not be called for %s", tt.name)
			}
		})
	}
}

func TestHandleCallback_AdminStats_NonAdmin(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 999999, // Different from chat ID
	}
	mockDB := testutil.NewBotStoreFake()

	mockBot := testutil.NewBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")

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

	handler.HandleCallback(ctx, update)

	// Current handlers answer callback outside the admin handler itself;
	// validate via absence of user-facing send, not hardcoded Request assertion.
	assert.False(t, mockBot.SendCalledSafe(), "Bot.Send should not be called for non-admin")
}

func TestHandleCallback_AdminLastReg_NonAdmin(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 999999, // Different from chat ID
	}
	mockDB := testutil.NewBotStoreFake()

	mockBot := testutil.NewBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")

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

	handler.HandleCallback(ctx, update)

	assert.False(t, mockBot.SendCalledSafe(), "Bot.Send should not be called for non-admin")
}

func TestHandleCallback_AdminStats_DatabaseError(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewBotStoreFake()

	mockBot := testutil.NewBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")

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

	handler.HandleCallback(ctx, update)

	assert.True(t, mockBot.SendCalledSafe(), "Bot.Send should be called with error message")
	assert.Contains(t, mockBot.LastSentTextSafe(), "Ошибка", "message should mention error")
}

func TestHandleCallback_AdminLastReg_EmptyList(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewBotStoreFake()

	mockBot := testutil.NewBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")

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

	handler.HandleCallback(ctx, update)

	assert.True(t, mockBot.SendCalledSafe(), "Bot.Send should be called")
	assert.Contains(t, mockBot.LastSentTextSafe(), "Нет", "message should indicate no registrations")
}

func TestHandleCallback_AdminLastReg_DatabaseError(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewBotStoreFake()

	mockBot := testutil.NewBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")

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

	handler.HandleCallback(ctx, update)

	assert.True(t, mockBot.SendCalledSafe(), "Bot.Send should be called with error message")
	assert.Contains(t, mockBot.LastSentTextSafe(), "Ошибка", "message should mention error")
}

func TestHandleCallback_MenuSubscription_NoSubscription(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}
	mockDB := testutil.NewBotStoreFake()

	mockBot := testutil.NewBotAPI()
	mockXUI := testutil.NewXUIClient()
	handler := newTestHandlerWithSubService(t, cfg, mockDB, mockXUI, mockBot)

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

	handler.HandleCallback(ctx, update)

	assert.True(t, mockBot.SendCalledSafe(), "Bot.Send should be called")
	assert.Contains(t, mockBot.LastSentTextSafe(), "подписк", "message should mention subscription")
}

func TestHandleShareInvite_Success(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		SiteURL: "https://example.com",
	}
	mockDB := testutil.NewBotStoreFake()

	mockBot := testutil.NewBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")

	mockDB.GetOrCreateInviteFunc = func(ctx context.Context, referrerTGID int64, code string) (*database.Invite, error) {
		return &database.Invite{
			Code:         "abc12345",
			ReferrerTGID: referrerTGID,
		}, nil
	}

	ctx := context.Background()
	handler.handleShareInvite(ctx, 123456, "testuser", 100)

	assert.True(t, mockBot.SendCalledSafe(), "Bot.Send should be called")
	assert.Contains(t, mockBot.LastSentTextSafe(), "пригласительная", "message should mention invite")
}

func TestHandleShareInvite_DatabaseError(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		SiteURL: "https://example.com",
	}
	mockDB := testutil.NewBotStoreFake()

	mockBot := testutil.NewBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")

	mockDB.GetOrCreateInviteFunc = func(ctx context.Context, referrerTGID int64, code string) (*database.Invite, error) {
		return nil, errors.New("database error")
	}

	ctx := context.Background()
	handler.handleShareInvite(ctx, 123456, "testuser", 100)

	assert.True(t, mockBot.SendCalledSafe(), "Bot.Send should be called with error message")
	assert.Contains(t, mockBot.LastSentTextSafe(), "Не удалось", "message should mention failure")
}

func TestHandleCallback_CreateSubscription_DatabaseError(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{}
	mockDB := testutil.NewBotStoreFake()

	mockBot := testutil.NewBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")

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

	handler.HandleCallback(ctx, update)

	assert.True(t, mockBot.SendCalledSafe(), "Bot.Send should be called with error message")
	assert.Contains(t, mockBot.LastSentTextSafe(), "ошибк", "message should mention error")
}

func TestGenerateInviteLink_DatabaseError(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewBotStoreFake()

	mockBot := testutil.NewBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")

	chatID := int64(123456)

	mockDB.GetOrCreateInviteFunc = func(ctx context.Context, referrerTGID int64, code string) (*database.Invite, error) {
		return nil, errors.New("database error")
	}

	ctx := context.Background()
	_, err := handler.generateInviteLink(ctx, chatID, linkTypeTelegram)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "database error")
}

func TestHandleCallback_UnknownCallback(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewBotStoreFake()

	mockBot := testutil.NewBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")

	ctx := context.Background()
	update := tgbotapi.Update{
		CallbackQuery: &tgbotapi.CallbackQuery{
			ID:   "test-callback-id",
			Data: "unknown_callback_data",
			From: &tgbotapi.User{ID: 123456, UserName: "testuser"},
			Message: &tgbotapi.Message{
				Chat:      &tgbotapi.Chat{ID: 123456},
				MessageID: 1,
			},
		},
	}

	handler.HandleCallback(ctx, update)

	assert.True(t, mockBot.RequestCalledSafe(), "Bot.Request should be called to answer callback")
	assert.False(t, mockBot.SendCalledSafe(), "Bot.Send should not be called for unknown callback")
}

func TestGenerateInviteLink_UnknownType(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewBotStoreFake()

	mockBot := testutil.NewBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")

	ctx := context.Background()
	_, err := handler.generateInviteLink(ctx, 123456, linkType("unknown"))

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unknown link type")
}

func TestHandleQRCode_QRError(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewBotStoreFake()

	mockBot := testutil.NewBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")

	mockDB.GetByTelegramIDFunc = func(ctx context.Context, telegramID int64) (*database.Subscription, error) {
		return &database.Subscription{
			TelegramID: telegramID,
			Username:   "testuser",
		}, nil
	}

	ctx := context.Background()
	handler.handleQRCode(ctx, 123456, "testuser", 100)

	assert.True(t, mockBot.SendCalledSafe())
}

func TestHandleCallback_NilMessage_RequestError(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewBotStoreFake()

	mockBot := testutil.NewBotAPI()
	mockBot.RequestError = errors.New("request failed")
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")

	ctx := context.Background()
	update := tgbotapi.Update{
		CallbackQuery: &tgbotapi.CallbackQuery{
			ID:      "test-callback-id",
			Data:    "some_data",
			From:    &tgbotapi.User{ID: 123456, UserName: "testuser"},
			Message: nil,
		},
	}

	handler.HandleCallback(ctx, update)

	assert.True(t, mockBot.RequestCalledSafe(), "Bot.Request should be called to answer callback")
}

func TestHandleCallback_QRTelegram(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TelegramAdminID: 123456,
	}
	mockDB := testutil.NewBotStoreFake()

	mockBot := testutil.NewBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")

	ctx := context.Background()
	update := tgbotapi.Update{
		CallbackQuery: &tgbotapi.CallbackQuery{
			ID:   "test-callback-id",
			Data: "qr_telegram",
			From: &tgbotapi.User{ID: 123456, UserName: "testuser"},
			Message: &tgbotapi.Message{
				MessageID: 100,
				Chat:      &tgbotapi.Chat{ID: 123456},
			},
		},
	}

	handler.HandleCallback(ctx, update)

	assert.True(t, mockBot.SendCalledSafe(), "Bot.Send should be called for QR telegram")
}

func TestHandleCallback_QRWeb(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewBotStoreFake()

	cfg := &config.Config{TelegramAdminID: 123456}
	mockBot := testutil.NewBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")

	ctx := context.Background()
	update := tgbotapi.Update{
		CallbackQuery: &tgbotapi.CallbackQuery{
			ID:   "test-callback-id",
			Data: "qr_web",
			From: &tgbotapi.User{ID: 123456, UserName: "testuser"},
			Message: &tgbotapi.Message{
				MessageID: 100,
				Chat:      &tgbotapi.Chat{ID: 123456},
			},
		},
	}

	handler.HandleCallback(ctx, update)

	assert.True(t, mockBot.SendCalledSafe(), "Bot.Send should be called for QR web")
}

func TestHandleCallback_BackToInvite(t *testing.T) {
	t.Parallel()

	mockDB := testutil.NewBotStoreFake()
	cfg := &config.Config{TelegramAdminID: 123456}

	mockBot := testutil.NewBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, NewTestBotConfig(), nil, "")

	ctx := context.Background()
	update := tgbotapi.Update{
		CallbackQuery: &tgbotapi.CallbackQuery{
			ID:   "test-callback-id",
			Data: "back_to_invite",
			From: &tgbotapi.User{ID: 123456, UserName: "testuser"},
			Message: &tgbotapi.Message{
				MessageID: 100,
				Chat:      &tgbotapi.Chat{ID: 123456},
			},
		},
	}

	handler.HandleCallback(ctx, update)

	assert.True(t, mockBot.RequestCalledSafe(), "Bot.Request should be called to delete message")
}
