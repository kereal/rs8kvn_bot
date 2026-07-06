package xui

import (
	"strings"
	"testing"

	"github.com/kereal/rs8kvn_bot/internal/utils"
)

func FuzzTruncateString(f *testing.F) {
	testcases := []struct {
		s      string
		maxLen int
	}{
		{"", 0},
		{"", 10},
		{"hello", 10},
		{"hello", 3},
		{"hello world", 5},
		{"привет мир", 6},
		{strings.Repeat("a", 1000), 10},
		{"a", 1},
		{"abc", 0},
	}

	for _, tc := range testcases {
		f.Add(tc.s, tc.maxLen)
	}

	f.Fuzz(func(t *testing.T, s string, maxLen int) {
		if maxLen < 0 || maxLen > 10000 {
			return
		}
		result := utils.TruncateString(s, maxLen)
		if maxLen == 0 {
			if result != "" {
				t.Errorf("utils.TruncateString(%q, 0) = %q, want ''", s, result)
			}
			return
		}
		r := []rune(s)
		if len(r) <= maxLen {
			if result != s {
				t.Errorf("utils.TruncateString(%q, %d) = %q, want %q", s, maxLen, result, s)
			}
		} else {
			rr := []rune(result)
			// result = r[:maxLen] + "..."
			if len(rr) != maxLen+3 {
				t.Errorf("utils.TruncateString(%q, %d) rune len = %d, want %d", s, maxLen, len(rr), maxLen+3)
			}
			if !strings.HasSuffix(result, "...") {
				t.Errorf("utils.TruncateString(%q, %d) = %q, should end with '...'", s, maxLen, result)
			}
		}
	})
}
