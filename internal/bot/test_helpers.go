package bot

func NewTestBotConfig() *BotConfig {
	return &BotConfig{
		Username:                "testbot",
		ID:                      123456789,
		FirstName:               "TestBot",
		IsBot:                   true,
		CanJoinGroups:           false,
		CanReadAllGroupMessages: false,
		SupportsInlineQueries:   false,
	}
}
