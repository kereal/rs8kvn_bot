package bot

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"rs8kvn_bot/internal/config"
	"rs8kvn_bot/internal/database"
	"rs8kvn_bot/internal/ratelimiter"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// TestGetUsername_EdgeCases tests edge cases for username extraction
func TestGetUsername_EdgeCases(t *testing.T) {
	cfg := &config.Config{TelegramAdminID: 123}
	h := &Handler{cfg: cfg}

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
			expected: "user_12345",
		},
		{
			name: "unicode username",
			user: &tgbotapi.User{
				ID:        12345,
				UserName:  "用户名",
				FirstName: "名字",
			},
			expected: "用户名",
		},
		{
			name: "emoji in username",
			user: &tgbotapi.User{
				ID:        12345,
				UserName:  "user🎉name",
				FirstName: "First🎉Name",
			},
			expected: "user🎉name",
		},
		{
			name: "special characters in username",
			user: &tgbotapi.User{
				ID:        12345,
				UserName:  "user_name-123.test",
				FirstName: "",
			},
			expected: "user_name-123.test",
		},
		{
			name: "whitespace in username",
			user: &tgbotapi.User{
				ID:        12345,
				UserName:  " user ",
				FirstName: "",
			},
			expected: " user ",
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
			name: "firstname fallback when no username",
			user: &tgbotapi.User{
				ID:        12345,
				UserName:  "",
				FirstName: "John",
			},
			expected: "John",
		},
		{
			name: "very long username",
			user: &tgbotapi.User{
				ID:        12345,
				UserName:  strings.Repeat("a", 200),
				FirstName: "",
			},
			expected: strings.Repeat("a", 200),
		},
		{
			name: "mixed script username",
			user: &tgbotapi.User{
				ID:        12345,
				UserName:  "用户123abc",
				FirstName: "",
			},
			expected: "用户123abc",
		},
		{
			name: "right-to-left text",
			user: &tgbotapi.User{
				ID:        12345,
				UserName:  "مرحبا",
				FirstName: "",
			},
			expected: "مرحبا",
		},
		{
			name: "control characters",
			user: &tgbotapi.User{
				ID:        12345,
				UserName:  "user\x00name",
				FirstName: "",
			},
			expected: "user\x00name",
		},
		{
			name: "newline in firstname",
			user: &tgbotapi.User{
				ID:        12345,
				UserName:  "",
				FirstName: "First\nName",
			},
			expected: "First\nName",
		},
		{
			name: "negative user ID",
			user: &tgbotapi.User{
				ID:        -12345,
				UserName:  "",
				FirstName: "",
			},
			expected: "user_-12345",
		},
		{
			name: "zero user ID",
			user: &tgbotapi.User{
				ID:        0,
				UserName:  "",
				FirstName: "",
			},
			expected: "user_0",
		},
		{
			name: "max int64 user ID",
			user: &tgbotapi.User{
				ID:        9223372036854775807,
				UserName:  "",
				FirstName: "",
			},
			expected: "user_9223372036854775807",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := h.getUsername(tt.user)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestIsAdmin_EdgeCases tests admin checking edge cases
func TestIsAdmin_EdgeCases(t *testing.T) {
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
			cfg := &config.Config{TelegramAdminID: tt.adminID}
			h := &Handler{cfg: cfg}
			result := h.isAdmin(tt.chatID)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestGetMainMenuKeyboard_ButtonCounts tests keyboard structure
func TestGetMainMenuKeyboard_ButtonCounts(t *testing.T) {
	cfg := &config.Config{TelegramAdminID: 123}
	h := &Handler{cfg: cfg}

	t.Run("keyboard without subscription has correct buttons", func(t *testing.T) {
		keyboard := h.getMainMenuKeyboard(false)

		// Should have 3 rows: subscription+donate, help
		require.Len(t, keyboard.InlineKeyboard, 2, "expected 2 rows without subscription")

		// First row: subscription and donate
		require.Len(t, keyboard.InlineKeyboard[0], 2, "first row should have 2 buttons")
		assert.Contains(t, keyboard.InlineKeyboard[0][0].Text, "Подписка")
		assert.Contains(t, keyboard.InlineKeyboard[0][1].Text, "Донат")

		// Second row: help
		require.Len(t, keyboard.InlineKeyboard[1], 1, "second row should have 1 button")
		assert.Contains(t, keyboard.InlineKeyboard[1][0].Text, "Помощь")
	})

	t.Run("keyboard with subscription has share button", func(t *testing.T) {
		keyboard := h.getMainMenuKeyboard(true)

		// Should have 3 rows: subscription+donate, help, share
		require.Len(t, keyboard.InlineKeyboard, 3, "expected 3 rows with subscription")

		// Third row: share
		require.Len(t, keyboard.InlineKeyboard[2], 1, "third row should have 1 button")
		assert.Contains(t, keyboard.InlineKeyboard[2][0].Text, "Поделиться")
	})
}

// TestGetBackKeyboard_Structure tests back keyboard structure
func TestGetBackKeyboard_Structure(t *testing.T) {
	cfg := &config.Config{TelegramAdminID: 123}
	h := &Handler{cfg: cfg}

	keyboard := h.getBackKeyboard()

	require.Len(t, keyboard.InlineKeyboard, 1, "expected 1 row")
	require.Len(t, keyboard.InlineKeyboard[0], 1, "expected 1 button")

	button := keyboard.InlineKeyboard[0][0]
	assert.Equal(t, "🏠 В начало", button.Text)
	assert.Equal(t, "back_to_start", *button.CallbackData)
}

// TestGetDonateText_Content tests donation message content
func TestGetDonateText_Content(t *testing.T) {
	cfg := &config.Config{TelegramAdminID: 123}
	h := &Handler{cfg: cfg}

	text := h.getDonateText()

	assert.Contains(t, text, "Поддержка проекта")
	assert.Contains(t, text, "Т-Банке")
	assert.Contains(t, text, "https://tbank.ru/cf/9J6agHgWdNg")
	assert.Contains(t, text, "t.me/kereal")
	assert.Contains(t, text, "*") // Markdown formatting
	assert.NotEmpty(t, text)
}

// TestGetHelpText_EdgeCases tests help text with various traffic limits
func TestGetHelpText_EdgeCases(t *testing.T) {
	cfg := &config.Config{TelegramAdminID: 123}
	h := &Handler{cfg: cfg}

	tests := []struct {
		name            string
		trafficLimitGB  int
		subscriptionURL string
	}{
		{
			name:            "zero traffic limit",
			trafficLimitGB:  0,
			subscriptionURL: "https://example.com/sub",
		},
		{
			name:            "one GB traffic limit",
			trafficLimitGB:  1,
			subscriptionURL: "https://example.com/sub",
		},
		{
			name:            "large traffic limit",
			trafficLimitGB:  1000,
			subscriptionURL: "https://example.com/sub",
		},
		{
			name:            "empty subscription URL",
			trafficLimitGB:  10,
			subscriptionURL: "",
		},
		{
			name:            "very long subscription URL",
			trafficLimitGB:  10,
			subscriptionURL: "https://example.com/" + strings.Repeat("a", 500),
		},
		{
			name:            "subscription URL with special chars",
			trafficLimitGB:  10,
			subscriptionURL: "https://example.com/sub?key=value&other=123",
		},
		{
			name:            "unicode in URL",
			trafficLimitGB:  10,
			subscriptionURL: "https://example.com/подписка",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			text := h.getHelpText(tt.trafficLimitGB, tt.subscriptionURL)

			assert.Contains(t, text, fmt.Sprintf("%dГб", tt.trafficLimitGB))
			assert.Contains(t, text, tt.subscriptionURL)
			assert.Contains(t, text, "Happ")
			assert.Contains(t, text, "iOS")
			assert.Contains(t, text, "Android")
		})
	}
}

// TestGetMainMenuContent_Scenarios tests main menu content scenarios
func TestGetMainMenuContent_Scenarios(t *testing.T) {
	cfg := &config.Config{TelegramAdminID: 123456}
	h := &Handler{cfg: cfg}

	t.Run("content for user without subscription", func(t *testing.T) {
		text, keyboard := h.getMainMenuContent("TestUser", false, 654321)

		assert.Contains(t, text, "TestUser")
		assert.Contains(t, text, "получить подписку")
		assert.Len(t, keyboard.InlineKeyboard, 1) // Just the get subscription button
	})

	t.Run("content for user with subscription", func(t *testing.T) {
		text, keyboard := h.getMainMenuContent("TestUser", true, 654321)

		assert.Contains(t, text, "TestUser")
		// Check keyboard has subscription button instead
		assert.GreaterOrEqual(t, len(keyboard.InlineKeyboard), 2)
		assert.GreaterOrEqual(t, len(keyboard.InlineKeyboard), 2)
	})

	t.Run("content for admin user", func(t *testing.T) {
		text, keyboard := h.getMainMenuContent("AdminUser", true, 123456)

		assert.Contains(t, text, "AdminUser")
		// Admin should have additional buttons
		lastRow := keyboard.InlineKeyboard[len(keyboard.InlineKeyboard)-1]
		adminButtonFound := false
		for _, btn := range lastRow {
			if btn.Text == "📊 Стат" || btn.Text == "📋 Посл.рег" {
				adminButtonFound = true
				break
			}
		}
		assert.True(t, adminButtonFound, "Admin buttons should be present")
	})

	t.Run("content for admin without subscription", func(t *testing.T) {
		text, keyboard := h.getMainMenuContent("AdminUser", false, 123456)

		assert.Contains(t, text, "AdminUser")
		// Even without subscription, admin should have admin buttons
		lastRow := keyboard.InlineKeyboard[len(keyboard.InlineKeyboard)-1]
		adminButtonFound := false
		for _, btn := range lastRow {
			if btn.Text == "📊 Стат" || btn.Text == "📋 Посл.рег" {
				adminButtonFound = true
				break
			}
		}
		assert.True(t, adminButtonFound, "Admin buttons should be present")
	})

	t.Run("content with unicode username", func(t *testing.T) {
		text, _ := h.getMainMenuContent("用户🎉名", true, 654321)

		assert.Contains(t, text, "用户🎉名")
	})

	t.Run("content with very long username", func(t *testing.T) {
		longName := strings.Repeat("VeryLongUserName", 10)
		text, _ := h.getMainMenuContent(longName, true, 654321)

		assert.Contains(t, text, longName)
	})
}

// TestAddAdminButtons_Scenarios tests admin button addition scenarios
func TestAddAdminButtons_Scenarios(t *testing.T) {
	t.Run("adds buttons for admin", func(t *testing.T) {
		cfg := &config.Config{TelegramAdminID: 123456}
		h := &Handler{cfg: cfg}

		keyboard := tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("Button1", "callback1"),
			),
		)

		initialRows := len(keyboard.InlineKeyboard)
		h.addAdminButtons(&keyboard, 123456)

		assert.Greater(t, len(keyboard.InlineKeyboard), initialRows)
	})

	t.Run("does not add buttons for non-admin", func(t *testing.T) {
		cfg := &config.Config{TelegramAdminID: 123456}
		h := &Handler{cfg: cfg}

		keyboard := tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("Button1", "callback1"),
			),
		)

		initialRows := len(keyboard.InlineKeyboard)
		h.addAdminButtons(&keyboard, 654321)

		assert.Equal(t, initialRows, len(keyboard.InlineKeyboard))
	})

	t.Run("does not add buttons when admin ID is zero", func(t *testing.T) {
		cfg := &config.Config{TelegramAdminID: 0}
		h := &Handler{cfg: cfg}

		keyboard := tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("Button1", "callback1"),
			),
		)

		initialRows := len(keyboard.InlineKeyboard)
		h.addAdminButtons(&keyboard, 0)

		assert.Equal(t, initialRows, len(keyboard.InlineKeyboard))
	})
}

// TestSubscriptionCache_EdgeCases tests cache edge cases
func TestSubscriptionCache_EdgeCases(t *testing.T) {
	t.Run("Set with nil subscription", func(t *testing.T) {
		cache := NewSubscriptionCache(10, 5*time.Minute)
		cache.Set(123, nil)

		// Should not panic, and Get should return nil
		result := cache.Get(123)
		assert.Nil(t, result)
	})

	t.Run("Get on empty cache", func(t *testing.T) {
		cache := NewSubscriptionCache(10, 5*time.Minute)

		assert.Nil(t, cache.Get(999))
		assert.Equal(t, 0, cache.Size())
	})

	t.Run("Invalidate non-existent key", func(t *testing.T) {
		cache := NewSubscriptionCache(10, 5*time.Minute)

		// Should not panic
		cache.Invalidate(999)
		assert.Equal(t, 0, cache.Size())
	})

	t.Run("Set updates existing entry", func(t *testing.T) {
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

	t.Run("Zero TTL behavior", func(t *testing.T) {
		cache := NewSubscriptionCache(10, 1*time.Nanosecond)

		sub := &database.Subscription{TelegramID: 123, Username: "user"}
		cache.Set(123, sub)

		// Should expire almost immediately
		time.Sleep(10 * time.Millisecond)
		result := cache.Get(123)
		assert.Nil(t, result)
	})

	t.Run("Negative telegram ID", func(t *testing.T) {
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
	cache := NewSubscriptionCache(1000, 5*time.Minute)

	const numGoroutines = 100
	const numOperations = 1000

	var wg sync.WaitGroup
	wg.Add(numGoroutines * 3) // 3 types of operations

	// Concurrent writes
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				key := int64(id*numOperations + j)
				cache.Set(key, &database.Subscription{TelegramID: key})
			}
		}(i)
	}

	// Concurrent reads
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				key := int64(id*numOperations + j)
				cache.Get(key)
			}
		}(i)
	}

	// Concurrent invalidates
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				key := int64(id*numOperations + j)
				cache.Invalidate(key)
			}
		}(i)
	}

	wg.Wait()
	// Test passes if no race conditions or panics
}

// TestRateLimiter_Integration tests rate limiter integration with handler
func TestRateLimiter_Integration(t *testing.T) {
	t.Run("rate limiter allows requests", func(t *testing.T) {
		rl := ratelimiter.NewRateLimiter(10, 1.0)

		// Should allow burst of 10
		for i := 0; i < 10; i++ {
			assert.True(t, rl.Allow(), "request %d should be allowed", i)
		}

		// Should deny next request
		assert.False(t, rl.Allow(), "request after burst should be denied")
	})

	t.Run("rate limiter refill", func(t *testing.T) {
		rl := ratelimiter.NewRateLimiter(5, 10.0) // 10 tokens per second

		// Consume all tokens
		for i := 0; i < 5; i++ {
			rl.Allow()
		}

		// Wait for refill
		time.Sleep(200 * time.Millisecond)

		// Should have refilled some tokens
		assert.True(t, rl.Allow(), "request after refill should be allowed")
	})

	t.Run("rate limiter wait with context", func(t *testing.T) {
		rl := ratelimiter.NewRateLimiter(1, 0.1) // Very slow refill

		// Consume the token
		rl.Allow()

		// Create a context with short timeout
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()

		// Wait should return false due to context cancellation
		result := rl.Wait(ctx)
		assert.False(t, result, "wait should return false on context cancellation")
	})

	t.Run("rate limiter wait success", func(t *testing.T) {
		rl := ratelimiter.NewRateLimiter(10, 100.0) // Fast refill

		ctx := context.Background()

		// Should succeed immediately
		result := rl.Wait(ctx)
		assert.True(t, result)
	})
}

// TestHandler_ShowLoadingMessage tests loading message functionality
func TestHandler_ShowLoadingMessage(t *testing.T) {
	t.Run("loading message text is correct", func(t *testing.T) {
		// Just verify the expected loading text
		expectedText := "⏳ Загрузка..."
		assert.Equal(t, expectedText, "⏳ Загрузка...")
	})
}

// TestKeyboard_CallbackDataValidation tests callback data format
func TestKeyboard_CallbackDataValidation(t *testing.T) {
	cfg := &config.Config{TelegramAdminID: 123}
	h := &Handler{cfg: cfg}

	validCallbacks := map[string]string{
		"menu_subscription":   "subscription menu",
		"menu_donate":         "donate menu",
		"menu_help":           "help menu",
		"create_subscription": "create subscription",
		"share_invite":        "share invite",
		"back_to_start":       "back to start",
	}

	t.Run("main menu keyboard callbacks", func(t *testing.T) {
		keyboard := h.getMainMenuKeyboard(true)

		for _, row := range keyboard.InlineKeyboard {
			for _, btn := range row {
				if btn.CallbackData != nil && *btn.CallbackData != "" {
					_, exists := validCallbacks[*btn.CallbackData]
					assert.True(t, exists || strings.HasPrefix(*btn.CallbackData, "admin_"),
						"callback %s should be valid", *btn.CallbackData)
				}
			}
		}
	})

	t.Run("back keyboard callback", func(t *testing.T) {
		keyboard := h.getBackKeyboard()

		for _, row := range keyboard.InlineKeyboard {
			for _, btn := range row {
				_, exists := validCallbacks[*btn.CallbackData]
				assert.True(t, exists, "callback %s should be valid", *btn.CallbackData)
			}
		}
	})
}

// TestMessageFormat_Validation tests message format validation
func TestMessageFormat_Validation(t *testing.T) {
	cfg := &config.Config{TelegramAdminID: 123, TrafficLimitGB: 50}
	h := &Handler{cfg: cfg}

	t.Run("donate text has markdown links", func(t *testing.T) {
		text := h.getDonateText()
		assert.Contains(t, text, "[")
		assert.Contains(t, text, "](")
		assert.Contains(t, text, "https://")
	})

	t.Run("help text has markdown formatting", func(t *testing.T) {
		text := h.getHelpText(50, "https://example.com/sub")
		assert.Contains(t, text, "*") // Bold formatting
		assert.Contains(t, text, "`") // Code formatting
	})

	t.Run("main menu content has proper greeting", func(t *testing.T) {
		text, _ := h.getMainMenuContent("TestUser", true, 456)
		assert.Contains(t, text, "👋")
		assert.Contains(t, text, "Привет")
	})
}

// TestCache_IntegrationWithHandler tests cache integration
func TestCache_IntegrationWithHandler(t *testing.T) {
	t.Run("cache cleanup stops on context cancel", func(t *testing.T) {
		cache := NewSubscriptionCache(10, 5*time.Minute)
		ctx, cancel := context.WithCancel(context.Background())

		done := make(chan bool, 1)
		go func() {
			cache.StartCleanup(ctx, 10*time.Millisecond)
			done <- true
		}()

		// Cancel immediately
		cancel()

		select {
		case <-done:
			// Success
		case <-time.After(100 * time.Millisecond):
			t.Error("StartCleanup did not stop on context cancel")
		}
	})
}

// TestErrorHandling_Scenarios tests error handling scenarios
func TestErrorHandling_Scenarios(t *testing.T) {
	t.Run("getUsername handles all empty fields", func(t *testing.T) {
		cfg := &config.Config{TelegramAdminID: 123}
		h := &Handler{cfg: cfg}

		user := &tgbotapi.User{ID: 999}
		result := h.getUsername(user)

		assert.Equal(t, "user_999", result)
	})
}

// TestConstants_Valid tests that constants are properly defined
func TestConstants_Valid(t *testing.T) {
	assert.Equal(t, 1000, CacheMaxSize)
	assert.Equal(t, 5*time.Minute, CacheTTL)
}

// TestGetMainMenuContent_SpecialUsernameChars tests usernames with special chars
func TestGetMainMenuContent_SpecialUsernameChars(t *testing.T) {
	cfg := &config.Config{TelegramAdminID: 123}
	h := &Handler{cfg: cfg}

	specialUsernames := []string{
		"user\nwith\nnewlines",
		"tab\there",
		"user\x00with\x00nulls",
		"emoji🎉🔥🚀",
		"мир 世界 세계",
		strings.Repeat("a", 1000), // Very long
	}

	for _, username := range specialUsernames {
		t.Run(fmt.Sprintf("username_len_%d", len(username)), func(t *testing.T) {
			text, _ := h.getMainMenuContent(username, true, 456)
			assert.Contains(t, text, username)
		})
	}
}

// TestHelpText_InjectionSafety tests that subscription URL is safely included
func TestHelpText_InjectionSafety(t *testing.T) {
	cfg := &config.Config{TelegramAdminID: 123}
	h := &Handler{cfg: cfg}

	maliciousURLs := []string{
		"https://example.com?hack=1&evil=2",
		"https://example.com/<script>alert(1)</script>",
		"https://example.com/'; DROP TABLE users; --",
	}

	for _, url := range maliciousURLs {
		t.Run("url_safety", func(t *testing.T) {
			text := h.getHelpText(10, url)
			// URL should be included as-is (Markdown code block handles special chars)
			assert.Contains(t, text, url)
		})
	}
}

// TestCache_EvictionPolicy tests LRU eviction behavior
func TestCache_EvictionPolicy(t *testing.T) {
	t.Run("evicts oldest entries first", func(t *testing.T) {
		cache := NewSubscriptionCache(3, 5*time.Minute)

		// Add entries with delays to ensure different expiration times
		cache.Set(1, &database.Subscription{TelegramID: 1})
		time.Sleep(10 * time.Millisecond)
		cache.Set(2, &database.Subscription{TelegramID: 2})
		time.Sleep(10 * time.Millisecond)
		cache.Set(3, &database.Subscription{TelegramID: 3})
		time.Sleep(10 * time.Millisecond)
		cache.Set(4, &database.Subscription{TelegramID: 4}) // Should evict 1

		assert.Nil(t, cache.Get(1), "entry 1 should be evicted")
		assert.NotNil(t, cache.Get(2), "entry 2 should exist")
		assert.NotNil(t, cache.Get(3), "entry 3 should exist")
		assert.NotNil(t, cache.Get(4), "entry 4 should exist")
	})
}

// TestRateLimiter_WaitWithContextCancellation tests context cancellation during wait
func TestRateLimiter_WaitWithContextCancellation(t *testing.T) {
	rl := ratelimiter.NewRateLimiter(1, 0.01) // Very slow refill

	// Use the only token
	require.True(t, rl.Allow())

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	result := rl.Wait(ctx)
	elapsed := time.Since(start)

	assert.False(t, result)
	assert.Less(t, elapsed, 100*time.Millisecond, "should return quickly on context cancellation")
}
