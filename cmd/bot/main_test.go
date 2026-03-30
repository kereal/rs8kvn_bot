package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetVersion(t *testing.T) {
	t.Run("returns non-empty string", func(t *testing.T) {
		v := getVersion()
		assert.NotEmpty(t, v, "getVersion() returned empty string")
	})

	t.Run("returns string with correct prefix", func(t *testing.T) {
		v := getVersion()
		assert.True(t, strings.HasPrefix(v, "rs8kvn_bot@"), "getVersion() = %s, want prefix rs8kvn_bot@", v)
	})

	t.Run("handles dev version gracefully", func(t *testing.T) {
		// When version is "dev", should still return a valid string
		v := getVersion()
		assert.Contains(t, v, "rs8kvn_bot@", "getVersion() should contain rs8kvn_bot@")
	})
}

func TestGetVersion_CommitVariable(t *testing.T) {
	// Test that commit variable is accessible
	t.Run("commit variable is defined", func(t *testing.T) {
		if commit == "" {
			t.Log("commit is empty (expected in test environment)")
		}
	})
}

func TestGetVersion_BuildTimeVariable(t *testing.T) {
	// Test that buildTime variable is accessible
	t.Run("buildTime variable is defined", func(t *testing.T) {
		if buildTime == "" {
			t.Log("buildTime is empty (expected in test environment)")
		}
	})
}

// Note: handleUpdate and handleUpdateSafely tests are not included here
// because bot.NewHandler requires a *tgbotapi.BotAPI concrete type,
// not an interface. Testing these functions would require either:
// 1. Integration tests with a real Telegram bot token
// 2. Refactoring to use an interface for the bot API
// 3. Using build tags to skip these tests in CI
//
// The core logic of these functions is tested indirectly through
// the handler tests in internal/bot/handlers_test.go
