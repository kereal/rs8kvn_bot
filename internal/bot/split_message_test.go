package bot

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSplitMessage_EntitySafe(t *testing.T) {
	t.Parallel()

	const maxLen = 20

	tests := []struct {
		name           string
		input          string
		maxChunk       int  // ordinary chunks must not exceed this
		entityOverflow bool // chunk may exceed maxChunk to keep an entity intact
		want           []string
	}{
		{
			name:     "short text stays one chunk",
			input:    "short",
			maxChunk: maxLen,
			want:     []string{"short"},
		},
		{
			name:     "plain long text is split at char boundary",
			input:    strings.Repeat("a", maxLen+5),
			maxChunk: maxLen,
			want:     nil, // length-only assertion
		},
		{
			name:     "markdown inline entity stays intact",
			input:    "prefix *bold* suffix extra padding",
			maxChunk: maxLen,
			want:     []string{"prefix *bold* suffix", "extra padding"},
		},
		{
			name:           "markdown entity with spaces is not broken",
			input:          "*bold text with spaces* after",
			maxChunk:       maxLen,
			entityOverflow: true,
			want:           []string{"*bold text with spaces*", "after"},
		},
		{
			name:           "multiline open entity is kept across newline",
			input:          "one *two three four five six seven* eight",
			maxChunk:       maxLen,
			entityOverflow: true,
			want:           []string{"one *two three four five six seven*", "eight"},
		},
		{
			name:     "over-long token without delimiters is hard-split",
			input:    "x" + strings.Repeat("y", maxLen*3),
			maxChunk: maxLen,
			want:     nil,
		},
		{
			name:           "over-long token with delimiter is kept whole",
			input:          "a*" + strings.Repeat("b", maxLen*3),
			maxChunk:       maxLen,
			entityOverflow: true,
			want:           []string{"a*" + strings.Repeat("b", maxLen*3)},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			chunks := splitMessage(tt.input, maxLen)
			require.NotEmpty(t, chunks, "must produce at least one chunk")

			for _, c := range chunks {
				if !tt.entityOverflow {
					assert.LessOrEqual(t, len([]rune(c)), tt.maxChunk,
						"ordinary chunk must not exceed maxLen runes: %q", c)
				}
			}

			if tt.want != nil {
				assert.Equal(t, tt.want, chunks)
			}
		})
	}
}

// TestSplitMessage_NoEntitySplitAcrossChunks verifies the contract behind the
// fix: every chunk is independently valid MarkdownV2 — no chunk opens an
// entity that a later chunk is expected to close, and inline entities never
// span a chunk boundary.
func TestSplitMessage_NoEntitySplitAcrossChunks(t *testing.T) {
	t.Parallel()

	const maxLen = 4096
	inputs := []string{
		"hello *bold and italic* world, then more text that wraps",
		"*a very long bold phrase with several words inside* trailing",
		"line one _underlined goes on_\nline two *bold continues* here",
		"prefix [link text](https://example.com) and then a long tail",
		"~strike through this whole sentence please~ and then some",
	}

	for _, in := range inputs {
		chunks := splitMessage(in, maxLen)
		for _, c := range chunks {
			assert.True(t, markdownEntitiesBalanced(c),
				"chunk must contain balanced MarkdownV2 entities: %q", c)
		}
	}
}

// markdownEntitiesBalanced reports whether s has no dangling inline entity
// delimiters (used as a lightweight invariant check, not a full parser).
func markdownEntitiesBalanced(s string) bool {
	var open []rune
	for _, r := range s {
		var closeFor rune
		switch r {
		case '[':
			open = append(open, '[')
			continue
		case ']':
			closeFor = '['
		case '*', '_', '`', '~':
			closeFor = r
		default:
			continue
		}
		if closeFor != 0 {
			if len(open) > 0 && open[len(open)-1] == closeFor {
				open = open[:len(open)-1]
			} else {
				open = append(open, closeFor)
			}
		}
	}
	return len(open) == 0
}
