//go:build integration

package bot

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSplitMessage_DoesNotBreakMarkdownV2Entities(t *testing.T) {
	t.Parallel()

	const maxLen = 20

	tests := []struct {
		name     string
		input    string
		wantAny  []string
		maxChunk int
	}{
		{
			name:     "short text",
			input:    "short",
			wantAny:  []string{"short"},
			maxChunk: maxLen,
		},
		{
			name:     "plain long text",
			input:    strings.Repeat("a", maxLen+5),
			wantAny:  nil,
			maxChunk: maxLen,
		},
		{
			name:     "markdown entities stay intact",
			input:    "prefix *bold* suffix extra padding",
			wantAny:  []string{"prefix *bold* suffix", "extra padding"},
			maxChunk: maxLen,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			chunks := splitMessage(tt.input, maxLen)
			for _, c := range chunks {
				assert.LessOrEqual(t, len(c), tt.maxChunk, "chunk must not exceed maxLen")
			}
			if tt.wantAny != nil {
				var matched bool
				for _, c := range chunks {
					for _, w := range tt.wantAny {
						if c == w {
							matched = true
							break
						}
					}
				}
				assert.True(t, matched, "expected one of %v in chunks %v", tt.wantAny, chunks)
			}
		})
	}
}

func TestBroadcastSession_TTLExpiry(t *testing.T) {
	t.Parallel()

	f := NewTestFixture(t)
	defer f.Close()

	chatID := f.Cfg.TelegramAdminID

	f.Handler.startBroadcastSession(chatID)
	require.NotNil(t, f.Handler.getBroadcastSession(chatID))

	f.Handler.broadcastMu.Lock()
	s := f.Handler.broadcastSessions[chatID]
	s.createdAt = time.Now().Add(-16 * time.Minute)
	f.Handler.broadcastMu.Unlock()

	require.Nil(t, f.Handler.getBroadcastSession(chatID))
	require.False(t, f.Handler.broadcastSessionActive(chatID))
}
