package bot

import (
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSelf_ReturnsCorrectUser(t *testing.T) {
	botConfig := &BotConfig{
		Username:                "mybot",
		ID:                      987654321,
		FirstName:               "MyBot",
		IsBot:                   true,
		CanJoinGroups:           true,
		CanReadAllGroupMessages: false,
		SupportsInlineQueries:   true,
	}

	user := botConfig.Self()

	require.NotNil(t, user)
	assert.Equal(t, int64(987654321), user.ID)
	assert.Equal(t, "MyBot", user.FirstName)
	assert.Equal(t, "mybot", user.UserName)
	assert.True(t, user.IsBot)
	assert.True(t, user.CanJoinGroups)
	assert.False(t, user.CanReadAllGroupMessages)
	assert.True(t, user.SupportsInlineQueries)
}

func TestSelf_AllFieldsMapped(t *testing.T) {
	botConfig := &BotConfig{
		Username:                "fieldtest",
		ID:                      111222333,
		FirstName:               "FieldTest",
		IsBot:                   false,
		CanJoinGroups:           false,
		CanReadAllGroupMessages: true,
		SupportsInlineQueries:   false,
	}

	user := botConfig.Self()

	require.NotNil(t, user)
	assert.Equal(t, botConfig.ID, user.ID)
	assert.Equal(t, botConfig.FirstName, user.FirstName)
	assert.Equal(t, botConfig.Username, user.UserName)
	assert.Equal(t, botConfig.IsBot, user.IsBot)
	assert.Equal(t, botConfig.CanJoinGroups, user.CanJoinGroups)
	assert.Equal(t, botConfig.CanReadAllGroupMessages, user.CanReadAllGroupMessages)
	assert.Equal(t, botConfig.SupportsInlineQueries, user.SupportsInlineQueries)
}

func TestSelf_DefaultValues(t *testing.T) {
	botConfig := &BotConfig{}

	user := botConfig.Self()

	require.NotNil(t, user)
	assert.Equal(t, int64(0), user.ID)
	assert.Equal(t, "", user.FirstName)
	assert.Equal(t, "", user.UserName)
	assert.False(t, user.IsBot)
	assert.False(t, user.CanJoinGroups)
	assert.False(t, user.CanReadAllGroupMessages)
	assert.False(t, user.SupportsInlineQueries)
}

func TestSelf_ReturnsNewInstance(t *testing.T) {
	botConfig := &BotConfig{
		ID:        123,
		FirstName: "Test",
		Username:  "test",
	}

	user1 := botConfig.Self()
	user2 := botConfig.Self()

	assert.NotSame(t, user1, user2, "Self() should return new instances")
	assert.Equal(t, user1.ID, user2.ID)
}

func TestNewBotConfig_ReturnsValidConfig(t *testing.T) {
	botConfig := NewTestBotConfig()

	require.NotNil(t, botConfig)
	assert.Equal(t, "testbot", botConfig.Username)
	assert.Equal(t, int64(123456789), botConfig.ID)
	assert.Equal(t, "TestBot", botConfig.FirstName)
	assert.True(t, botConfig.IsBot)
	assert.False(t, botConfig.CanJoinGroups)
	assert.False(t, botConfig.CanReadAllGroupMessages)
	assert.False(t, botConfig.SupportsInlineQueries)
	assert.False(t, botConfig.loadedAt.IsZero(), "loadedAt should be set")
}

func TestNewBotConfig_SelfRoundTrip(t *testing.T) {
	original := NewTestBotConfig()
	user := original.Self()

	assert.Equal(t, original.ID, user.ID)
	assert.Equal(t, original.FirstName, user.FirstName)
	assert.Equal(t, original.Username, user.UserName)
	assert.Equal(t, original.IsBot, user.IsBot)
	assert.Equal(t, original.CanJoinGroups, user.CanJoinGroups)
	assert.Equal(t, original.CanReadAllGroupMessages, user.CanReadAllGroupMessages)
	assert.Equal(t, original.SupportsInlineQueries, user.SupportsInlineQueries)
}

func TestNewBotConfig_WithBotAPI(t *testing.T) {
	botAPI := &tgbotapi.BotAPI{
		Self: tgbotapi.User{
			ID:                      999888777,
			UserName:                "actualbot",
			FirstName:               "ActualBot",
			IsBot:                   true,
			CanJoinGroups:           true,
			CanReadAllGroupMessages: true,
			SupportsInlineQueries:   false,
		},
	}

	cfg, err := NewBotConfig(botAPI)

	require.NoError(t, err)
	require.NotNil(t, cfg)
	assert.Equal(t, "actualbot", cfg.Username)
	assert.Equal(t, int64(999888777), cfg.ID)
	assert.Equal(t, "ActualBot", cfg.FirstName)
	assert.True(t, cfg.IsBot)
	assert.True(t, cfg.CanJoinGroups)
	assert.True(t, cfg.CanReadAllGroupMessages)
	assert.False(t, cfg.SupportsInlineQueries)
	assert.False(t, cfg.loadedAt.IsZero())
}
