package bot

import (
	"strings"
	"testing"
)

func FuzzEscapeMarkdown(f *testing.F) {
	testcases := []string{
		"",
		"hello world",
		"hello_world",
		"hello*world",
		"[test](url)",
		"`code`",
		"~~strike~~",
		"a|b",
		"file.txt",
		"wow!",
		"a+b",
		"a-b",
		"a=b",
		"#heading",
		"a>b",
		"{a}",
		"_*[test](url)`~>#+-=|{}.!",
		"Привет мир",
		"Привет_мир",
		"a_b_c",
		"1234567890",
		strings.Repeat("_", 100),
		strings.Repeat("*", 50),
	}

	for _, tc := range testcases {
		f.Add(tc)
	}

	f.Fuzz(func(t *testing.T, input string) {
		result := escapeMarkdown(input)
		if len(result) == 0 && len(input) == 0 {
			return
		}
		// Result should be at least as long as input
		if len(result) < len(input) {
			t.Errorf("escapeMarkdown(%q) result shorter than input", input)
		}
		// Result should not be unreasonably long (max 2x for all special chars)
		if len(result) > len(input)*2+100 {
			t.Errorf("escapeMarkdown(%q) result unreasonably long: %d vs %d", input, len(result), len(input))
		}
	})
}
