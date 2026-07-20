package bot

import "time"

// NewTestBotConfig returns a BotConfig populated with fixed test values.
// It lives in a non-test file so it is reachable from external test packages
// (e.g. cmd/bot) without pulling the test-only testutil fake into the build.
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
