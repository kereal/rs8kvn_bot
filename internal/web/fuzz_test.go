package web

import (
	"regexp"
	"testing"

	"github.com/kereal/rs8kvn_bot/internal/config"
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
		"ABCDEFGHIJKLMNOP",
		"test_code",
		"another-code",
		"verylonginvitecode12345",
	}

	for _, tc := range testcases {
		f.Add(tc)
	}

	validPattern := regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

	f.Fuzz(func(t *testing.T, code string) {
		if len(code) > 1000 {
			return
		}

		srv := NewServer(":0", nil, &config.Config{}, "testbot", nil, nil)
		result := srv.inviteCodeRegex.MatchString(code)
		expected := validPattern.MatchString(code)

		if result != expected {
			t.Errorf("inviteCodeRegex.MatchString(%q) = %v, want %v", code, result, expected)
		}

		if result {
			for _, ch := range code {
				if ch == '/' || ch == '\\' || ch == '.' || ch == ' ' || ch == '@' {
					t.Errorf("inviteCodeRegex.MatchString(%q) = true but contains dangerous character %q", code, ch)
				}
			}
		}
	})
}
