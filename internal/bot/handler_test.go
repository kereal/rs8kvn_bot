package bot

import (
	"context"
	"testing"

	"rs8kvn_bot/internal/config"
	"rs8kvn_bot/internal/testutil"

	"github.com/stretchr/testify/assert"
)

func TestStoreConversation_DoesNotPanic(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  50,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig())

	ctx := context.Background()
	assert.NotPanics(t, func() {
		handler.StoreConversation(ctx, 123456, "Hello", "Hi there")
	})
}

func TestGetUserContext_ReturnsEmpty(t *testing.T) {
	cfg := &config.Config{
		TelegramAdminID: 123456,
		TrafficLimitGB:  50,
	}
	mockDB := testutil.NewMockDatabaseService()
	mockXUI := testutil.NewMockXUIClient()
	mockBot := testutil.NewMockBotAPI()
	handler := NewHandler(mockBot, cfg, mockDB, mockXUI, NewTestBotConfig())

	ctx := context.Background()
	result := handler.GetUserContext(ctx, 123456, "test query")

	assert.Equal(t, "", result, "GetUserContext should return empty string")
}
