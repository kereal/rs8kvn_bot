package xui

import (
	"strings"
	"testing"
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
		result := truncateString(s, maxLen)
		if maxLen == 0 {
			if result != "..." && result != "" {
				t.Errorf("truncateString(%q, 0) = %q, want '...' or ''", s, result)
			}
			return
		}
		if len(s) <= maxLen {
			if result != s {
				t.Errorf("truncateString(%q, %d) = %q, want %q", s, maxLen, result, s)
			}
		} else {
			if len(result) != maxLen+3 {
				t.Errorf("truncateString(%q, %d) len = %d, want %d", s, maxLen, len(result), maxLen+3)
			}
			if !strings.HasSuffix(result, "...") {
				t.Errorf("truncateString(%q, %d) = %q, should end with '...'", s, maxLen, result)
			}
		}
	})
}
