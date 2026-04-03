package web

import (
	"regexp"
	"testing"

	"rs8kvn_bot/internal/bot"
	"rs8kvn_bot/internal/config"
)

func FuzzInviteCodeRegex(f *testing.F) {
	testcases := []string{
		"abc123",
		"abc_123",
		"abc-123",
		"ABC123",
		"",
		"abc/123",
		"abc.123",
		"abc 123",
		"abc@123",
		"abc'; DROP TABLE--",
		"../etc/passwd",
		"123456",
		"abcdef",
		"a",
		"abcdefghij1234567890",
		"абв",
		"用户",
		"<script>alert(1)</script>",
		"../../etc/passwd",
		"code_with_many_underscores_and-dashes",
	}

	for _, tc := range testcases {
		f.Add(tc)
	}

	validPattern := regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

	f.Fuzz(func(t *testing.T, code string) {
		if len(code) > 1000 {
			return
		}

		srv := NewServer(":0", nil, nil, &config.Config{}, bot.NewTestBotConfig(), nil)
		result := srv.inviteCodeRegex.MatchString(code)
		expected := validPattern.MatchString(code)

		if result != expected {
			t.Errorf("inviteCodeRegex.MatchString(%q) = %v, want %v", code, result, expected)
		}

		// Security: result should never contain path separators or special chars
		if result {
			for _, ch := range code {
				if ch == '/' || ch == '\\' || ch == '.' || ch == ' ' || ch == '@' {
					t.Errorf("inviteCodeRegex.MatchString(%q) = true but contains dangerous character %q", code, ch)
				}
			}
		}
	})
}

func FuzzInviteCodeRegex_ValidOnly(f *testing.F) {
	validCodes := []string{
		"abc123",
		"ABCDEFGHIJKLMNOP",
		"test_code",
		"another-code",
		"1234567890",
		"a",
		"short",
		"verylonginvitecode12345",
	}

	for _, tc := range validCodes {
		f.Add(tc)
	}

	f.Fuzz(func(t *testing.T, code string) {
		if len(code) == 0 {
			return
		}
		// Only test codes that start with lowercase letter to avoid invalid codes
		if code[0] < 'a' || code[0] > 'z' {
			if code[0] < 'A' || code[0] > 'Z' {
				if code[0] < '0' || code[0] > '9' {
					t.Skip("Not a valid code format")
				}
			}
		}

		srv := NewServer(":0", nil, nil, &config.Config{}, bot.NewTestBotConfig(), nil)
		result := srv.inviteCodeRegex.MatchString(code)

		// Valid codes should always match
		isValidFormat := func(s string) bool {
			if len(s) == 0 {
				return false
			}
			for _, ch := range s {
				if (ch < 'a' || ch > 'z') && (ch < 'A' || ch > 'Z') && (ch < '0' || ch > '9') && ch != '_' && ch != '-' {
					return false
				}
			}
			return true
		}

		if isValidFormat(code) && len(code) <= 100 {
			if !result {
				t.Errorf("Valid code %q should match regex", code)
			}
		}
	})
}
