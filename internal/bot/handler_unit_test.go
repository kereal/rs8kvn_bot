package bot

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// ==================== normalizeCommand Tests ====================

func TestNormalizeCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   string
		want string
	}{
		{"start", "start"},
		{"help", "help"},
		{"invite", "invite"},
		{"del", "del"},
		{"broadcast", "broadcast"},
		{"send", "send"},
		{"refstats", "refstats"},
		{"v", "v"},
		{"unknown", "unknown"},
		{"", "unknown"},
		{"START", "unknown"},
		{"foo", "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, normalizeCommand(tt.in))

		})
	}
}

// ==================== formatUserDisplay Tests ====================

func TestFormatUserDisplay(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		username string
		want     string
	}{
		{"empty -> unknown", "", "unknown"},
		{"real username -> @username", "alice", "@alice"},
		{"underscore username", "alice_bob", "@alice_bob"},
		{"special char -> raw", "user@host", "user@host"},
		{"cyrillic -> raw", "юзер", "юзер"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, formatUserDisplay(tt.username))

		})
	}
}

// ==================== displayUsername Tests ====================

func TestDisplayUsername(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		username string
		want     string
	}{
		{"empty", "", ""},
		{"regular", "alice", ", @alice"},
		{"underscore", "alice_bob", ", @alice_bob"},
		{"numeric", "12345", ", @12345"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, displayUsername(tt.username))

		})
	}
}
