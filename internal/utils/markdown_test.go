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

func TestEscapeMarkdownV2_NestedEntities(t *testing.T) {
	// Код внутри жирного текста: внешняя конструкция захватывает внутреннюю,
	// поэтому разделители вложенной не должны экранироваться.
	got := EscapeMarkdownV2("*жирный `код`*")
	assert.Equal(t, "*жирный `код`*", got)
	assert.NotContains(t, got, "\x01")
}

func TestParseMarkdownV2(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []MarkdownRun
	}{
		{
			name: "plain text keeps reserved chars as text",
			in:   "Цена 100 руб. Акция!",
			want: []MarkdownRun{{Type: "text", Text: "Цена 100 руб. Акция!"}},
		},
		{
			name: "bold",
			in:   "*жирный*",
			want: []MarkdownRun{{Type: "text", Text: "жирный", Bold: true}},
		},
		{
			name: "italic",
			in:   "_курсив_",
			want: []MarkdownRun{{Type: "text", Text: "курсив", Italic: true}},
		},
		{
			name: "strike",
			in:   "~зачёркнутый~",
			want: []MarkdownRun{{Type: "text", Text: "зачёркнутый", Strike: true}},
		},
		{
			name: "inline code",
			in:   "`команда`",
			want: []MarkdownRun{{Type: "text", Text: "команда", Code: true}},
		},
		{
			name: "fenced code",
			in:   "```go\nfmt.Println()\n```",
			want: []MarkdownRun{{Type: "pre", Text: "go\nfmt.Println()\n", Code: true}},
		},
		{
			name: "link",
			in:   "[сайт](https://example.com)",
			want: []MarkdownRun{{Type: "text", Text: "сайт", URL: "https://example.com"}},
		},
		{
			name: "link with unsafe scheme is not clickable",
			in:   "[клик](javascript:alert)",
			want: []MarkdownRun{{Type: "text", Text: "клик"}},
		},
		{
			name: "unclosed bold stays literal",
			in:   "*не закрыто",
			want: []MarkdownRun{{Type: "text", Text: "*не закрыто"}},
		},
		{
			name: "headers are not supported by telegram markdownv2",
			in:   "# Заголовок",
			want: []MarkdownRun{{Type: "text", Text: "# Заголовок"}},
		},
		{
			name: "mixed text and entities",
			in:   "Цена *100* рублей",
			want: []MarkdownRun{
				{Type: "text", Text: "Цена "},
				{Type: "text", Text: "100", Bold: true},
				{Type: "text", Text: " рублей"},
			},
		},
		{
			name: "code inside bold keeps both styles",
			in:   "*жирный `код`*",
			want: []MarkdownRun{
				{Type: "text", Text: "жирный ", Bold: true},
				{Type: "text", Text: "код", Bold: true, Code: true},
			},
		},
		{
			name: "italic inside bold is literal: bold is escaped first",
			in:   "*жирный _и курсив_*",
			want: []MarkdownRun{{Type: "text", Text: "жирный _и курсив_", Bold: true}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseMarkdownV2(tc.in)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestParseMarkdownV2_EscapedTextBecomesLiteral(t *testing.T) {
	// Точка и решётка экранируются воркером: получатель видит обычный текст.
	got := ParseMarkdownV2("100 руб. #1")
	assert.Equal(t, []MarkdownRun{{Type: "text", Text: "100 руб. #1"}}, got)
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
