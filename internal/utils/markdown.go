package utils

import "strings"

func EscapeMarkdown(text string) string {
	var b strings.Builder
	b.Grow(len(text) * 2)
	for _, r := range text {
		if strings.ContainsRune(`\_*[]()~`+"`"+">#+-=|{}.!", r) {
			b.WriteRune('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}
