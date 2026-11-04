package bot

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/kereal/rs8kvn_bot/internal/utils"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGetUsername_EdgeCases tests edge cases for username extraction
func TestGetUsername_EdgeCases(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{TelegramAdminID: 123}
	h := newContentTestHandler(cfg)

	tests := []struct {
		name     string
		user     *tgbotapi.User
		expected string
	}{
		{
			name:     "nil user",
			user:     nil,
			expected: "unknown",
		},
		{
			name: "empty username and firstname",
			user: &tgbotapi.User{
				ID:        12345,
				UserName:  "",
				FirstName: "",
				LastName:  "",
			},
			expected: "",
		},
		{
			name: "username preferred over firstname",
			user: &tgbotapi.User{
				ID:        12345,
				UserName:  "preferred_username",
				FirstName: "NotUsed",
			},
			expected: "preferred_username",
		},
		{
			name: "empty username returns empty",
			user: &tgbotapi.User{
				ID:        12345,
				UserName:  "",
				FirstName: "John",
			},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := h.getUsername(tt.user)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestIsAdmin_EdgeCases tests admin checking edge cases
func TestIsAdmin_EdgeCases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		adminID  int64
		chatID   int64
		expected bool
	}{
		{
			name:     "matching positive IDs",
			adminID:  123456,
			chatID:   123456,
			expected: true,
		},
		{
			name:     "non-matching positive IDs",
			adminID:  123456,
			chatID:   654321,
			expected: false,
		},
		{
			name:     "zero admin ID",
			adminID:  0,
			chatID:   123456,
			expected: false,
		},
		{
			name:     "negative admin ID",
			adminID:  -1,
			chatID:   -1,
			expected: false,
		},
		{
			name:     "negative chat ID with positive admin",
			adminID:  123456,
			chatID:   -123456,
			expected: false,
		},
		{
			name:     "max int64 admin ID",
			adminID:  9223372036854775807,
			chatID:   9223372036854775807,
			expected: true,
		},
		{
			name:     "max int64 admin ID with different chat",
			adminID:  9223372036854775807,
			chatID:   1,
			expected: false,
		},
		{
			name:     "both zero",
			adminID:  0,
			chatID:   0,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := &config.Config{TelegramAdminID: tt.adminID}
			h := newContentTestHandler(cfg)
			result := h.isAdmin(tt.chatID)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGetHelpText_EdgeCases(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{TelegramAdminID: 123}
	h := newContentTestHandler(cfg)

	tests := []struct {
		name            string
		trafficLimit    int
		subscriptionURL string
	}{
		{
			name:            "zero traffic limit",
			trafficLimit:    0,
			subscriptionURL: "https://example.com/sub",
		},
		{
			name:            "one GB traffic limit",
			trafficLimit:    1,
			subscriptionURL: "https://example.com/sub",
		},
		{
			name:            "large traffic limit",
			trafficLimit:    1000,
			subscriptionURL: "https://example.com/sub",
		},
		{
			name:            "empty subscription URL",
			trafficLimit:    10,
			subscriptionURL: "",
		},
		{
			name:            "very long subscription URL",
			trafficLimit:    10,
			subscriptionURL: "https://example.com/" + strings.Repeat("a", 500),
		},
		{
			name:            "subscription URL with special chars",
			trafficLimit:    10,
			subscriptionURL: "https://example.com/sub?key=value&other=123",
		},
		{
			name:            "url injection attempt",
			trafficLimit:    10,
			subscriptionURL: "https://example.com/<script>alert(1)</script>",
		},
		{
			name:            "url sql injection attempt",
			trafficLimit:    10,
			subscriptionURL: "https://example.com/'; DROP TABLE users; --",
		},
		{
			name:            "url with query params",
			trafficLimit:    10,
			subscriptionURL: "https://example.com?hack=1&evil=2",
		},
		{
			name:            "unicode in URL",
			trafficLimit:    10,
			subscriptionURL: "https://example.com/подписка",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			text := h.getHelpText(tt.trafficLimit, tt.subscriptionURL)

			assert.Contains(t, text, fmt.Sprintf("%dГб", tt.trafficLimit))
			assert.Contains(t, text, tt.subscriptionURL)
			assert.Contains(t, text, "Happ")
			assert.Contains(t, text, "iOS")
			assert.Contains(t, text, "Android")
		})
	}
}

func TestSubscriptionCache_EdgeCases(t *testing.T) {
	t.Parallel()

	t.Run("Set with nil subscription", func(t *testing.T) {
		t.Parallel()

		cache := NewSubscriptionCache(10, 5*time.Minute)
		cache.Set(123, nil)

		// Should not panic, and Get should return nil
		result := cache.Get(123)
		assert.Nil(t, result)
	})

	t.Run("Invalidate non-existent key", func(t *testing.T) {
		t.Parallel()

		cache := NewSubscriptionCache(10, 5*time.Minute)

		// Should not panic
		cache.Invalidate(999)
		assert.Equal(t, 0, cache.Size())
	})

	t.Run("Set updates existing entry", func(t *testing.T) {
		t.Parallel()

		cache := NewSubscriptionCache(10, 5*time.Minute)

		sub1 := &database.Subscription{TelegramID: 123, Username: "user1"}
		sub2 := &database.Subscription{TelegramID: 123, Username: "user2"}

		cache.Set(123, sub1)
		result1 := cache.Get(123)
		require.NotNil(t, result1)
		assert.Equal(t, "user1", result1.Username)

		cache.Set(123, sub2)
		result2 := cache.Get(123)
		require.NotNil(t, result2)
		assert.Equal(t, "user2", result2.Username)

		// Size should still be 1
		assert.Equal(t, 1, cache.Size())
	})

	t.Run("Negative telegram ID", func(t *testing.T) {
		t.Parallel()

		cache := NewSubscriptionCache(10, 5*time.Minute)

		sub := &database.Subscription{TelegramID: -123, Username: "user"}
		cache.Set(-123, sub)

		result := cache.Get(-123)
		require.NotNil(t, result)
		assert.Equal(t, int64(-123), result.TelegramID)
	})
}

// TestSubscriptionCache_ConcurrentStress tests cache under heavy concurrent load
func TestSubscriptionCache_ConcurrentStress(t *testing.T) {
	t.Parallel()

	cache := NewSubscriptionCache(1000, 5*time.Minute)

	const (
		numGoroutines = 100
		numOperations = 1000
	)

	var wg sync.WaitGroup
	wg.Add(numGoroutines * 3) // 3 types of operations

	// Concurrent writes
	for i := range numGoroutines {
		go func(id int) {
			defer wg.Done()

			for j := range numOperations {
				key := int64(id*numOperations + j)
				cache.Set(key, &database.Subscription{TelegramID: key})
			}
		}(i)
	}

	// Concurrent reads
	for i := range numGoroutines {
		go func(id int) {
			defer wg.Done()

			for j := range numOperations {
				key := int64(id*numOperations + j)
				cache.Get(key)
			}
		}(i)
	}

	// Concurrent invalidates
	for i := range numGoroutines {
		go func(id int) {
			defer wg.Done()

			for j := range numOperations {
				key := int64(id*numOperations + j)
				cache.Invalidate(key)
			}
		}(i)
	}

	wg.Wait()
	// Test passes if no race conditions or panics
}

// TestGetMainMenuContent_SpecialUsernameChars tests usernames with special chars
func TestFormatUserLink_NumericUsername(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		username string
		id       int64
		want     string
	}{
		{"numeric username with id", "11", 11, "[11](tg://user?id=11)"},
		{"numeric username different id", "99", 11, "[99](tg://user?id=11)"},
		{"empty username with id", "", 11, "[unknown](tg://user?id=11)"},
		{"alphabetic username", "user11", 11, "[@user11](https://t.me/user11)"},
		{"tgId_ fallback with id", "tgId_123456", 123456, "[unknown](tg://user?id=123456)"},
		{"tgId_ fallback with different id", "tgId_111", 222, "[unknown](tg://user?id=222)"},
		{"zero id empty username", "", 0, "[unknown](#)"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := utils.FormatUserLink(tt.username, tt.id)
			assert.Equal(t, tt.want, got)
		})
	}
}
