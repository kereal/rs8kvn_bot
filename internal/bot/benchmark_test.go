package bot

import (
	"strings"
	"testing"

	"rs8kvn_bot/internal/utils"
)

func BenchmarkEscapeMarkdown(b *testing.B) {
	inputs := []string{
		"simple text",
		"user_name",
		"test*value*bold",
		"[link](url)",
		"a|b+c-d",
		strings.Repeat("x", 1000),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, input := range inputs {
			utils.EscapeMarkdown(input)
		}
	}
}

func BenchmarkEscapeMarkdown_Single(b *testing.B) {
	input := "test_user_name_with_special_chars"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		utils.EscapeMarkdown(input)
	}
}

func BenchmarkEscapeMarkdown_Parallel(b *testing.B) {
	input := "test_user_name_with_special_chars"
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			utils.EscapeMarkdown(input)
		}
	})
}
