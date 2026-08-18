package utils

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEscapeMarkdownV2_PreservesEntities(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "bold with reserved chars",
			in:   "*hello! world*",
			want: `*hello\! world*`,
		},
		{
			name: "italic with parens",
			in:   "_link(text)_",
			want: `_link\(text\)_`,
		},
		{
			name: "strike with hash",
			in:   "~price #1~",
			want: `~price \#1~`,
		},
		{
			name: "inline code with reserved",
			in:   "`use cmd --opt`",
			want: "`use cmd \\-\\-opt`",
		},
		{
			name: "link with reserved in text",
			in:   "[click! here](http://x.com)",
			want: `[click\! here](http://x\.com)`,
		},
		{
			name: "fenced code block",
			in:   "```\nhello!\n```",
			want: "```\nhello\\!\n```",
		},
		{
			name: "plain text with hash not at line start",
			in:   "tag #123",
			want: `tag \#123`,
		},
		{
			name: "plain text with angle bracket",
			in:   "value > 0",
			want: `value \> 0`,
		},
		{
			name: "plain text with backslash",
			in:   `path\to\file`,
			want: `path\\to\\file`,
		},
		{
			name: "mixed entity and plain",
			in:   "*bold* and plain! #tag",
			want: `*bold* and plain\! \#tag`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := EscapeMarkdownV2(tc.in)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestEscapeLegacyMarkdown(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "underscore for italic",
			in:   "plan_name",
			want: `plan\_name`,
		},
		{
			name: "asterisk for bold",
			in:   "plan*pro",
			want: `plan\*pro`,
		},
		{
			name: "backtick for code",
			in:   "plan`special",
			want: "plan\\`special",
		},
		{
			name: "opening bracket for links",
			in:   "plan[vip]",
			want: `plan\[vip]`,
		},
		{
			name: "multiple special chars",
			in:   "plan_*test*_name",
			want: `plan\_\*test\*\_name`,
		},
		{
			name: "all four special chars",
			in:   "_*`[test",
			want: "\\_\\*\\`\\[test",
		},
		{
			name: "no special chars",
			in:   "Premium Plan",
			want: "Premium Plan",
		},
		{
			name: "empty string",
			in:   "",
			want: "",
		},
		{
			name: "only special chars",
			in:   "_*`[",
			want: "\\_\\*\\`\\[",
		},
		{
			name: "mixed with safe chars",
			in:   "Premium_Pro (2024) #1",
			want: `Premium\_Pro (2024) #1`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := EscapeLegacyMarkdown(tc.in)
			assert.Equal(t, tc.want, got)
		})
	}
}
