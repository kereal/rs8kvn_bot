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

	// Конструкция, захваченная позже (например, жирный текст с кодом
	// внутри), содержит токен более ранней конструкции. Токены подставляются
	// после экранирования внутреннего текста: иначе разделители вложенной
	// конструкции тоже экранировались бы и ломали разметку.
	resolved := make([]string, len(protected))

	for i, p := range protected {
		text := escapeProtected(p)
		for j := range i {
			text = strings.ReplaceAll(text, mdv2Token(j), resolved[j])
		}

		resolved[i] = text
		out = strings.ReplaceAll(out, mdv2Token(i), text)
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

// EscapeLegacyMarkdown escapes special characters for Telegram's legacy Markdown
// (ParseMode "Markdown"). It escapes only the four reserved characters:
// _ (italic), * (bold), ` (code), [ (link start).
// Use this when embedding user-controlled text into messages sent with ModeMarkdown.
func EscapeLegacyMarkdown(text string) string {
	var b strings.Builder
	b.Grow(len(text) + len(text)/4) // pre-allocate for ~25% escaped chars

	for _, r := range text {
		switch r {
		case '_', '*', '`', '[':
			b.WriteRune('\\')
		}
		b.WriteRune(r)
	}

	return b.String()
}

// MarkdownRun — фрагмент разобранного сообщения: текст с набором активных
// стилей. Ссылка задаётся URL, блок кода — Type "pre".
type MarkdownRun struct {
	Type   string `json:"type"` // "text" | "pre"
	Text   string `json:"text"`
	URL    string `json:"url"`
	Bold   bool   `json:"bold"`
	Italic bool   `json:"italic"`
	Strike bool   `json:"strike"`
	Code   bool   `json:"code"`
}

// ParseMarkdownV2 разбирает текст так, как его увидит получатель: сначала
// применяется EscapeMarkdownV2 — ровно тот вызов, который делает воркер
// рассылки, — затем результат парсится как MarkdownV2.
//
// Так превью не может разойтись с доставкой: и экранирование, и набор
// поддерживаемых конструкций (жирный, курсив, зачёркнутый, код, блок кода,
// ссылка) берутся из одного места. Всё, что в MarkdownV2 не поддерживается
// Telegram (заголовки, подчёркивание, спойлеры), воркер экранирует, поэтому
// в разборе такие конструкции остаются обычным текстом.
func ParseMarkdownV2(text string) []MarkdownRun {
	return parseMarkdownRuns(EscapeMarkdownV2(text), false, false, false)
}

// parseMarkdownRuns разбирает уже экранированный MarkdownV2. Стили bold,
// italic и strike наследуются вложенными конструкциями.
func parseMarkdownRuns(s string, bold, italic, strike bool) []MarkdownRun {
	var runs []MarkdownRun

	var buf strings.Builder

	flush := func() {
		if buf.Len() == 0 {
			return
		}

		runs = append(runs, MarkdownRun{
			Type:   "text",
			Text:   buf.String(),
			Bold:   bold,
			Italic: italic,
			Strike: strike,
		})
		buf.Reset()
	}

	for i := 0; i < len(s); {
		switch {
		case s[i] == '\\' && i+1 < len(s):
			// Экранированный зарезервированный символ — обычный текст.
			buf.WriteByte(s[i+1])
			i += 2
		case strings.HasPrefix(s[i:], "```"):
			end := strings.Index(s[i+3:], "```")
			if end < 0 {
				flush()

				runs = append(runs, MarkdownRun{Type: "pre", Text: unescapeMarkdown(s[i+3:]), Code: true})

				i = len(s)

				continue
			}

			flush()

			runs = append(runs, MarkdownRun{Type: "pre", Text: unescapeMarkdown(s[i+3 : i+3+end]), Code: true})

			i += 3 + end + 3
		case s[i] == '`':
			end := strings.IndexByte(s[i+1:], '`')
			if end < 0 {
				buf.WriteByte('`')
				i++

				continue
			}

			flush()

			runs = append(runs, MarkdownRun{
				Type:   "text",
				Text:   unescapeMarkdown(s[i+1 : i+1+end]),
				Bold:   bold,
				Italic: italic,
				Strike: strike,
				Code:   true,
			})

			i += 1 + end + 1
		case s[i] == '*':
			end := strings.IndexByte(s[i+1:], '*')
			if end < 0 {
				buf.WriteByte('*')
				i++

				continue
			}

			flush()

			runs = append(runs, parseMarkdownRuns(s[i+1:i+1+end], true, italic, strike)...)

			i += 1 + end + 1
		case s[i] == '_':
			end := strings.IndexByte(s[i+1:], '_')
			if end < 0 {
				buf.WriteByte('_')
				i++

				continue
			}

			flush()

			runs = append(runs, parseMarkdownRuns(s[i+1:i+1+end], bold, true, strike)...)

			i += 1 + end + 1
		case s[i] == '~':
			end := strings.IndexByte(s[i+1:], '~')
			if end < 0 {
				buf.WriteByte('~')
				i++

				continue
			}

			flush()

			runs = append(runs, parseMarkdownRuns(s[i+1:i+1+end], bold, italic, true)...)

			i += 1 + end + 1
		case s[i] == '[':
			textEnd, linkEnd, url, ok := parseMarkdownLink(s[i:])
			if !ok {
				buf.WriteByte('[')
				i++

				continue
			}

			flush()

			inner := parseMarkdownRuns(s[i+1:i+textEnd], bold, italic, strike)
			if len(inner) == 0 {
				inner = []MarkdownRun{{Type: "text", Text: ""}}
			}

			for idx := range inner {
				inner[idx].URL = url
			}

			runs = append(runs, inner...)

			i += linkEnd
		default:
			buf.WriteByte(s[i])
			i++
		}
	}

	flush()

	return runs
}

// parseMarkdownLink разбирает «[текст](url)» в начале строки. Возвращает
// границу текста, границу всей конструкции, URL и признак успеха. URL с
// неизвестной схемой отбрасывается: Telegram такие ссылки не делает
// кликабельными, а текст остаётся текстом.
func parseMarkdownLink(s string) (textEnd, linkEnd int, url string, ok bool) {
	closeBracket := strings.IndexByte(s[1:], ']')
	if closeBracket < 0 {
		return 0, 0, "", false
	}

	textEnd = 1 + closeBracket
	if !strings.HasPrefix(s[textEnd:], "](") {
		return 0, 0, "", false
	}

	rest := s[textEnd+2:]
	closeParen := strings.IndexByte(rest, ')')
	if closeParen < 0 {
		return 0, 0, "", false
	}

	linkEnd = textEnd + 2 + closeParen + 1

	raw := rest[:closeParen]
	if !safeMarkdownURL(raw) {
		return textEnd, linkEnd, "", true
	}

	return textEnd, linkEnd, unescapeMarkdown(raw), true
}

// unescapeMarkdown превращает экранированные символы обратно в обычные:
// Telegram показывает «\.» как «.», а не как обратный слэш.
func unescapeMarkdown(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}

	var b strings.Builder

	b.Grow(len(s))

	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			i++

			b.WriteByte(s[i])

			continue
		}

		b.WriteByte(s[i])
	}

	return b.String()
}

// safeMarkdownURL оставляет только схемы, которые Telegram превращает в
// кликабельную ссылку.
func safeMarkdownURL(raw string) bool {
	url := strings.ToLower(strings.TrimSpace(raw))

	for _, scheme := range []string{"http://", "https://", "tg://", "mailto:"} {
		if strings.HasPrefix(url, scheme) {
			return true
		}
	}

	return false
}
