package bot

import (
	"time"

	"rs8kvn_bot/internal/config"
)

func NewTestBotConfig() *BotConfig {
	return &BotConfig{
		Username:                "testbot",
		ID:                      123456789,
		FirstName:               "TestBot",
		IsBot:                   true,
		CanJoinGroups:           false,
		CanReadAllGroupMessages: false,
		SupportsInlineQueries:   false,
		loadedAt:                time.Now(),
	}
}

// NewTestHandler creates a Handler with all internal components initialized for testing.
// Pass nil for fields you want to mock separately (bot, db, xui).
func NewTestHandler(cfg *config.Config, botConfig *BotConfig) *Handler {
	if cfg == nil {
		cfg = &config.Config{}
	}
	if botConfig == nil {
		botConfig = NewTestBotConfig()
	}
	return &Handler{
		cfg:       cfg,
		botConfig: botConfig,
		keyboards: NewKeyboardBuilder(botConfig.Username, cfg.ContactUsername, config.DonateCardNumber, config.DonateURL, cfg.SiteURL),
	}
}
