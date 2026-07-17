package utils

import (
	"regexp"
	"strconv"
	"strings"
)

func EscapeMarkdown(text string) string {
	var b strings.Builder
	b.Grow(len(text) * 2)
	for _, r := range text {
		if strings.ContainsRune(`\_*[]()~`+"`"+">-#+=|{}^", r) {
			b.WriteRune('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// mdv2Reserved are chars that must be backslash-escaped in Telegram MarkdownV2
// when they are not part of a formatting construct.
const mdv2Reserved = `\_*[]()~` + "`" + `+-=|{}.!`

// mdv2Protect matches MarkdownV2 constructs the admin may intend to keep
// (fenced code, inline code, links, *bold*, _italic_, ~strike~).
var mdv2Protect = []*regexp.Regexp{
	regexp.MustCompile("```[\\s\\S]*?```"),
	regexp.MustCompile("`[^`]*`"),
	regexp.MustCompile(`\[[^\]]*\]\([^)]*\)`),
	regexp.MustCompile(`\*(\S(?:[^*\n]*?\S)?)\*`),
	regexp.MustCompile(`_(\S(?:[^_\n]*?\S)?)_`),
	regexp.MustCompile(`~(\S(?:[^~\n]*?\S)?)~`),
}

// EscapeMarkdownV2 escapes reserved MarkdownV2 chars (so plain text with dots,
// exclamation marks, etc. parses safely) while preserving valid formatting the
// admin wrote. The admin no longer has to manually escape special characters.
func EscapeMarkdownV2(text string) string {
	protected := make([]string, 0, 4)
	scrubbed := text
	for _, re := range mdv2Protect {
		scrubbed = re.ReplaceAllStringFunc(scrubbed, func(m string) string {
			protected = append(protected, m)
			return mdv2Token(len(protected) - 1)
		})
	}

	var b strings.Builder
	b.Grow(len(scrubbed) + 8)
	for _, r := range scrubbed {
		if strings.ContainsRune(mdv2Reserved, r) || r == '>' || r == '#' {
			b.WriteRune('\\')
		}
		b.WriteRune(r)
	}

	out := b.String()
	for i, p := range protected {
		out = strings.ReplaceAll(out, mdv2Token(i), escapeProtected(p))
	}
	return out
}

func escapeProtected(m string) string {
	switch {
	case strings.HasPrefix(m, "```") && strings.HasSuffix(m, "```"):
		inner := m[3 : len(m)-3]
		return "```" + escapeMdv2(inner) + "```"
	case strings.HasPrefix(m, "`") && strings.HasSuffix(m, "`") && len(m) >= 2:
		inner := m[1 : len(m)-1]
		return "`" + escapeMdv2(inner) + "`"
	case strings.HasPrefix(m, "[") && strings.Contains(m, "](") && strings.HasSuffix(m, ")"):
		closeBracket := strings.Index(m, "](")
		if closeBracket > 0 {
			textPart := m[1:closeBracket]
			urlPart := m[closeBracket+2 : len(m)-1]
			return "[" + escapeMdv2(textPart) + "](" + escapeMdv2(urlPart) + ")"
		}
		return m
	case strings.HasPrefix(m, "*") && strings.HasSuffix(m, "*") && len(m) >= 2:
		inner := m[1 : len(m)-1]
		return "*" + escapeMdv2(inner) + "*"
	case strings.HasPrefix(m, "_") && strings.HasSuffix(m, "_") && len(m) >= 2:
		inner := m[1 : len(m)-1]
		return "_" + escapeMdv2(inner) + "_"
	case strings.HasPrefix(m, "~") && strings.HasSuffix(m, "~") && len(m) >= 2:
		inner := m[1 : len(m)-1]
		return "~" + escapeMdv2(inner) + "~"
	default:
		return m
	}
}

func escapeMdv2(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 8)
	for _, r := range s {
		if strings.ContainsRune(mdv2Reserved, r) || r == '>' || r == '#' {
			b.WriteRune('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

func mdv2Token(i int) string {
	return "\x01" + strconv.Itoa(i) + "\x01"
}
