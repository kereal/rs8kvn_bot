package bot

import "time"

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
