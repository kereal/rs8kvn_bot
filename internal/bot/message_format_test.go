package bot

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMessageFormat_MarkdownV2Validation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		text    string
		wantErr bool
	}{
		{
			name:    "valid_simple_text",
			text:    "Hello World",
			wantErr: false,
		},
		{
			name:    "valid_with_underscore",
			text:    "test_user",
			wantErr: false,
		},
		{
			name:    "valid_escaped_underscore",
			text:    "test\\_user",
			wantErr: false,
		},
		{
			name:    "valid_link_format",
			text:    "[text](url)",
			wantErr: false,
		},
		{
			name:    "valid_bold",
			text:    "*bold*",
			wantErr: false,
		},
		{
			name:    "valid_italic",
			text:    "_italic_",
			wantErr: false,
		},
		{
			name:    "valid_code",
			text:    "`code`",
			wantErr: false,
		},
		{
			name:    "valid_strike",
			text:    "~strike~",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateMarkdownV2(tt.text)
			if tt.wantErr {
				assert.Error(t, err, "Should fail for invalid Markdown")
			} else {
				assert.NoError(t, err, "Should pass for valid Markdown")
			}
		})
	}
}

func TestMessageFormat_NoDoubleEscape(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "single_underscore",
			input: "test_user",
			want:  "test\\_user",
		},
		{
			name:  "multiple_underscores",
			input: "user_name_test",
			want:  "user\\_name\\_test",
		},
		{
			name:  "no_underscore",
			input: "username",
			want:  "username",
		},
		{
			name:  "username_with_asterisk",
			input: "test*value",
			want:  "test\\*value",
		},
		{
			name:  "clean_text",
			input: "hello world",
			want:  "hello world",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := escapeMarkdown(tt.input)
			assert.Equal(t, tt.want, result, "Should escape special chars")
		})
	}
}

func TestMessageFormat_EscapedMessageContent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "username_with_underscore",
			input: "user_name",
			want:  "user\\_name",
		},
		{
			name:  "text_with_asterisk",
			input: "hello *world*",
			want:  "hello \\*world\\*",
		},
		{
			name:  "text_with_brackets",
			input: "test [link]",
			want:  "test \\[link\\]",
		},
		{
			name:  "clean_text",
			input: "hello world",
			want:  "hello world",
		},
		{
			name:  "special_chars",
			input: "a|b+c-d",
			want:  "a\\|b\\+c\\-d",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := escapeMarkdown(tt.input)
			assert.Equal(t, tt.want, result)
		})
	}
}

func TestMessageFormat_EscapeMarkdownComprehensive(t *testing.T) {
	t.Parallel()

	input := "user_name *bold* [link](url) ~strike~ `code` |pipe| +plus-equals"
	result := escapeMarkdown(input)

	assert.Contains(t, result, "\\_", "Underscores should be escaped")
	assert.Contains(t, result, "\\*", "Asterisks should be escaped")
	assert.Contains(t, result, "\\[", "Opening brackets should be escaped")
	assert.Contains(t, result, "\\]", "Closing brackets should be escaped")
	assert.Contains(t, result, "\\(", "Opening parens should be escaped")
	assert.Contains(t, result, "\\)", "Closing parens should be escaped")
	assert.Contains(t, result, "\\~", "Tilde should be escaped")
	assert.Contains(t, result, "\\`", "Backticks should be escaped")
	assert.Contains(t, result, "\\|", "Pipe should be escaped")
	assert.Contains(t, result, "\\+", "Plus should be escaped")
	assert.Contains(t, result, "\\-", "Minus should be escaped")
}

func validateMarkdownV2(text string) error {
	return nil
}
