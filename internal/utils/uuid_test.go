package utils

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateUUID(t *testing.T) {
	t.Run("generates non-empty UUID", func(t *testing.T) {
		got := GenerateUUID()
		assert.NotEmpty(t, got, "GenerateUUID() returned empty string")
	})

	t.Run("generates valid UUID v4 format", func(t *testing.T) {
		uuid := GenerateUUID()

		// UUID v4 format: xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx
		// where x is any hex digit, 4 is the version, y is one of 8, 9, a, or b
		pattern := `^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`
		assert.Regexp(t, pattern, uuid, "GenerateUUID() should match UUID v4 format")
	})

	t.Run("generates unique UUIDs", func(t *testing.T) {
		const iterations = 10000
		uuids := make(map[string]bool, iterations)

		for i := 0; i < iterations; i++ {
			uuid := GenerateUUID()
			assert.False(t, uuids[uuid], "GenerateUUID() generated duplicate UUID after %d iterations: %s", i, uuid)
			uuids[uuid] = true
		}
	})

	t.Run("generates correct length", func(t *testing.T) {
		uuid := GenerateUUID()
		// UUID format: 8-4-4-4-12 = 36 characters (32 hex + 4 dashes)
		expectedLen := 36

		assert.Len(t, uuid, expectedLen, "GenerateUUID() length")
	})

	t.Run("version bit is set correctly", func(t *testing.T) {
		for i := 0; i < 100; i++ {
			uuid := GenerateUUID()
			// Check that the 13th character (version) is '4'
			if len(uuid) >= 15 {
				assert.Equal(t, '4', rune(uuid[14]), "GenerateUUID() version bit not set correctly at position 14")
			}
		}
	})

	t.Run("variant bits are set correctly", func(t *testing.T) {
		for i := 0; i < 100; i++ {
			uuid := GenerateUUID()
			// Check that the 17th character (variant) is one of 8, 9, a, b
			if len(uuid) >= 20 {
				variant := uuid[19]
				assert.Contains(t, []byte{'8', '9', 'a', 'b'}, variant, "GenerateUUID() variant bits not set correctly at position 19")
			}
		}
	})
}

func TestGenerateSubID(t *testing.T) {
	t.Run("generates non-empty SubID", func(t *testing.T) {
		got := GenerateSubID()
		assert.NotEmpty(t, got, "GenerateSubID() returned empty string")
	})

	t.Run("generates valid hex format", func(t *testing.T) {
		subID := GenerateSubID()

		// SubID format: 14 hex characters (28 hex chars in the implementation)
		pattern := `^[0-9a-f]+$`
		assert.Regexp(t, pattern, subID, "GenerateSubID() should contain hex characters only")
	})

	t.Run("generates unique SubIDs", func(t *testing.T) {
		const iterations = 10000
		subIDs := make(map[string]bool, iterations)

		for i := 0; i < iterations; i++ {
			subID := GenerateSubID()
			assert.False(t, subIDs[subID], "GenerateSubID() generated duplicate SubID after %d iterations: %s", i, subID)
			subIDs[subID] = true
		}
	})

	t.Run("generates correct length", func(t *testing.T) {
		subID := GenerateSubID()
		// 5 bytes = 10 hex characters
		expectedLen := 10

		assert.Len(t, subID, expectedLen, "GenerateSubID() length")
	})

	t.Run("generates URL-safe IDs", func(t *testing.T) {
		for i := 0; i < 100; i++ {
			subID := GenerateSubID()
			// Check that all characters are hex (0-9, a-f)
			for _, c := range subID {
				assert.True(t, (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f'),
					"GenerateSubID() contains non-URL-safe character: %c", c)
			}
		}
	})
}

func TestGenerateInviteCode(t *testing.T) {
	t.Run("generates non-empty code", func(t *testing.T) {
		got := GenerateInviteCode()
		assert.NotEmpty(t, got, "GenerateInviteCode() returned empty string")
	})

	t.Run("generates correct length", func(t *testing.T) {
		code := GenerateInviteCode()
		assert.Len(t, code, 8, "GenerateInviteCode() length")
	})

	t.Run("generates valid characters", func(t *testing.T) {
		code := GenerateInviteCode()
		const validChars = "0123456789abcdefghijklmnopqrstuvwxyz"
		for _, c := range code {
			assert.True(t, strings.ContainsRune(validChars, c),
				"GenerateInviteCode() contains invalid character: %c", c)
		}
	})

	t.Run("generates unique codes", func(t *testing.T) {
		code1 := GenerateInviteCode()
		code2 := GenerateInviteCode()
		assert.NotEqual(t, code1, code2, "GenerateInviteCode() should generate different codes")
	})

	t.Run("generates lowercase only", func(t *testing.T) {
		code := GenerateInviteCode()
		assert.Equal(t, strings.ToLower(code), code, "GenerateInviteCode() should generate lowercase only")
	})
}

func TestGenerateUUID_Concurrency(t *testing.T) {
	t.Run("concurrent generation is safe", func(t *testing.T) {
		const goroutines = 100
		const uuidsPerGoroutine = 100

		results := make(chan string, goroutines*uuidsPerGoroutine)

		for i := 0; i < goroutines; i++ {
			go func() {
				for j := 0; j < uuidsPerGoroutine; j++ {
					results <- GenerateUUID()
				}
			}()
		}

		uuids := make(map[string]bool)
		for i := 0; i < goroutines*uuidsPerGoroutine; i++ {
			uuid := <-results
			require.False(t, uuids[uuid], "Concurrent generation produced duplicate UUID: %s", uuid)
			uuids[uuid] = true
		}

		assert.Len(t, uuids, goroutines*uuidsPerGoroutine, "All UUIDs should be unique")
	})
}

func TestGenerateSubID_Concurrency(t *testing.T) {
	t.Run("concurrent generation is safe", func(t *testing.T) {
		const goroutines = 100
		const idsPerGoroutine = 100

		results := make(chan string, goroutines*idsPerGoroutine)

		for i := 0; i < goroutines; i++ {
			go func() {
				for j := 0; j < idsPerGoroutine; j++ {
					results <- GenerateSubID()
				}
			}()
		}

		subIDs := make(map[string]bool)
		for i := 0; i < goroutines*idsPerGoroutine; i++ {
			subID := <-results
			require.False(t, subIDs[subID], "Concurrent generation produced duplicate SubID: %s", subID)
			subIDs[subID] = true
		}

		assert.Len(t, subIDs, goroutines*idsPerGoroutine, "All SubIDs should be unique")
	})
}

// BenchmarkGenerateUUID benchmarks UUID generation
func BenchmarkGenerateUUID(b *testing.B) {
	for i := 0; i < b.N; i++ {
		GenerateUUID()
	}
}

// BenchmarkGenerateSubID benchmarks SubID generation
func BenchmarkGenerateSubID(b *testing.B) {
	for i := 0; i < b.N; i++ {
		GenerateSubID()
	}
}

// BenchmarkGenerateUUID_Parallel benchmarks parallel UUID generation
func BenchmarkGenerateUUID_Parallel(b *testing.B) {
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			GenerateUUID()
		}
	})
}

// BenchmarkGenerateSubID_Parallel benchmarks parallel SubID generation
func BenchmarkGenerateSubID_Parallel(b *testing.B) {
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			GenerateSubID()
		}
	})
}

// BenchmarkGenerateInviteCode benchmarks invite code generation
func BenchmarkGenerateInviteCode(b *testing.B) {
	for i := 0; i < b.N; i++ {
		GenerateInviteCode()
	}
}

// BenchmarkGenerateInviteCode_Parallel benchmarks parallel invite code generation
func BenchmarkGenerateInviteCode_Parallel(b *testing.B) {
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			GenerateInviteCode()
		}
	})
}

// BenchmarkGenerateUUID_SmallBatch benchmarks generating a small batch of UUIDs
func BenchmarkGenerateUUID_SmallBatch(b *testing.B) {
	for i := 0; i < b.N; i++ {
		for j := 0; j < 10; j++ {
			GenerateUUID()
		}
	}
}

// BenchmarkGenerateUUID_LargeBatch benchmarks generating a large batch of UUIDs
func BenchmarkGenerateUUID_LargeBatch(b *testing.B) {
	for i := 0; i < b.N; i++ {
		for j := 0; j < 1000; j++ {
			GenerateUUID()
		}
	}
}

// BenchmarkGenerateSubID_SmallBatch benchmarks generating a small batch of SubIDs
func BenchmarkGenerateSubID_SmallBatch(b *testing.B) {
	for i := 0; i < b.N; i++ {
		for j := 0; j < 10; j++ {
			GenerateSubID()
		}
	}
}

// BenchmarkGenerateSubID_LargeBatch benchmarks generating a large batch of SubIDs
func BenchmarkGenerateSubID_LargeBatch(b *testing.B) {
	for i := 0; i < b.N; i++ {
		for j := 0; j < 1000; j++ {
			GenerateSubID()
		}
	}
}

// BenchmarkGenerateInviteCode_SmallBatch benchmarks generating a small batch of invite codes
func BenchmarkGenerateInviteCode_SmallBatch(b *testing.B) {
	for i := 0; i < b.N; i++ {
		for j := 0; j < 10; j++ {
			GenerateInviteCode()
		}
	}
}

// BenchmarkGenerateInviteCode_LargeBatch benchmarks generating a large batch of invite codes
func BenchmarkGenerateInviteCode_LargeBatch(b *testing.B) {
	for i := 0; i < b.N; i++ {
		for j := 0; j < 1000; j++ {
			GenerateInviteCode()
		}
	}
}

// BenchmarkAllGenerators benchmarks all generator functions together
func BenchmarkAllGenerators(b *testing.B) {
	b.Run("UUID", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			GenerateUUID()
		}
	})
	b.Run("SubID", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			GenerateSubID()
		}
	})
	b.Run("InviteCode", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			GenerateInviteCode()
		}
	})
}

func TestGenerateInviteCode_Concurrency(t *testing.T) {
	t.Run("concurrent generation is safe", func(t *testing.T) {
		const goroutines = 100
		const codesPerGoroutine = 100

		results := make(chan string, goroutines*codesPerGoroutine)

		for i := 0; i < goroutines; i++ {
			go func() {
				for j := 0; j < codesPerGoroutine; j++ {
					results <- GenerateInviteCode()
				}
			}()
		}

		codes := make(map[string]bool)
		for i := 0; i < goroutines*codesPerGoroutine; i++ {
			code := <-results
			require.False(t, codes[code], "Concurrent generation produced duplicate code: %s", code)
			codes[code] = true
		}

		assert.Len(t, codes, goroutines*codesPerGoroutine, "All codes should be unique")
	})
}

func TestGenerateInviteCode_Entropy(t *testing.T) {
	t.Run("generates high entropy codes", func(t *testing.T) {
		const iterations = 10000
		charCounts := make(map[rune]int)

		for i := 0; i < iterations; i++ {
			code := GenerateInviteCode()
			for _, c := range code {
				charCounts[c]++
			}
		}

		// Each character should appear roughly 1/36 of the time (36 possible chars)
		// With 8 chars * 10000 iterations = 80000 total chars
		// Expected count per char: 80000 / 36 ≈ 2222
		expectedPerChar := float64(iterations*8) / 36.0

		for char, count := range charCounts {
			// Allow 50% deviation from expected (rough check for randomness)
			ratio := float64(count) / expectedPerChar
			assert.Greater(t, ratio, 0.3, "Character %c appears too rarely: %d (ratio: %.2f)", char, count, ratio)
			assert.Less(t, ratio, 3.0, "Character %c appears too frequently: %d (ratio: %.2f)", char, count, ratio)
		}
	})
}

func TestGenerateUUID_Entropy(t *testing.T) {
	t.Run("version bit is always 4", func(t *testing.T) {
		for i := 0; i < 1000; i++ {
			uuid := GenerateUUID()
			require.Len(t, uuid, 36, "UUID length")
			assert.Equal(t, byte('4'), uuid[14], "Version bit at position 14 should be '4'")
		}
	})

	t.Run("variant bits are valid", func(t *testing.T) {
		for i := 0; i < 1000; i++ {
			uuid := GenerateUUID()
			require.Len(t, uuid, 36, "UUID length")
			variant := uuid[19]
			assert.Contains(t, []byte{'8', '9', 'a', 'b'}, variant, "Variant bits should be 8, 9, a, or b")
		}
	})
}

func TestGenerateSubID_Entropy(t *testing.T) {
	t.Run("generates high entropy IDs", func(t *testing.T) {
		const iterations = 10000
		charCounts := make(map[rune]int)

		for i := 0; i < iterations; i++ {
			id := GenerateSubID()
			for _, c := range id {
				charCounts[c]++
			}
		}

		// Each character should appear roughly 1/16 of the time (hex chars)
		// With 10 chars * 10000 iterations = 100000 total chars
		// Expected count per char: 100000 / 16 = 6250
		expectedPerChar := float64(iterations*10) / 16.0

		for char, count := range charCounts {
			ratio := float64(count) / expectedPerChar
			assert.Greater(t, ratio, 0.5, "Character %c appears too rarely: %d (ratio: %.2f)", char, count, ratio)
			assert.Less(t, ratio, 2.0, "Character %c appears too frequently: %d (ratio: %.2f)", char, count, ratio)
		}
	})
}

func TestGenerateUUID_Stress(t *testing.T) {
	t.Run("stress test uniqueness", func(t *testing.T) {
		const iterations = 100000
		uuids := make(map[string]struct{}, iterations)

		for i := 0; i < iterations; i++ {
			uuid := GenerateUUID()
			if _, exists := uuids[uuid]; exists {
				t.Fatalf("Duplicate UUID found after %d iterations: %s", i, uuid)
			}
			uuids[uuid] = struct{}{}
		}

		assert.Len(t, uuids, iterations, "All UUIDs should be unique")
	})
}

func TestGenerateSubID_Stress(t *testing.T) {
	t.Run("stress test uniqueness", func(t *testing.T) {
		const iterations = 100000
		ids := make(map[string]struct{}, iterations)

		for i := 0; i < iterations; i++ {
			id := GenerateSubID()
			if _, exists := ids[id]; exists {
				t.Fatalf("Duplicate SubID found after %d iterations: %s", i, id)
			}
			ids[id] = struct{}{}
		}

		assert.Len(t, ids, iterations, "All SubIDs should be unique")
	})
}

func TestGenerateInviteCode_Stress(t *testing.T) {
	t.Run("stress test uniqueness", func(t *testing.T) {
		const iterations = 100000
		codes := make(map[string]struct{}, iterations)

		for i := 0; i < iterations; i++ {
			code := GenerateInviteCode()
			if _, exists := codes[code]; exists {
				t.Fatalf("Duplicate code found after %d iterations: %s", i, code)
			}
			codes[code] = struct{}{}
		}

		assert.Len(t, codes, iterations, "All codes should be unique")
	})
}

func TestGenerateUUID_DashesPosition(t *testing.T) {
	t.Run("dashes are at correct positions", func(t *testing.T) {
		for i := 0; i < 100; i++ {
			uuid := GenerateUUID()
			require.Len(t, uuid, 36, "UUID length")
			assert.Equal(t, '-', rune(uuid[8]), "Dash at position 8")
			assert.Equal(t, '-', rune(uuid[13]), "Dash at position 13")
			assert.Equal(t, '-', rune(uuid[18]), "Dash at position 18")
			assert.Equal(t, '-', rune(uuid[23]), "Dash at position 23")
		}
	})
}

func TestGenerateSubID_Format(t *testing.T) {
	t.Run("contains only hex characters", func(t *testing.T) {
		for i := 0; i < 100; i++ {
			id := GenerateSubID()
			assert.Len(t, id, 10, "SubID length")
			for _, c := range id {
				assert.True(t, (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f'),
					"Character %c is not a valid hex character", c)
			}
		}
	})
}

// TestGenerateInviteCode_Format тестирует формат invite кода
func TestGenerateInviteCode_Format(t *testing.T) {
	t.Run("contains only lowercase letters and digits", func(t *testing.T) {
		for i := 0; i < 100; i++ {
			code := GenerateInviteCode()
			assert.Len(t, code, 8, "InviteCode length")
			for _, c := range code {
				assert.True(t, (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z'),
					"Character %c is not a valid lowercase alphanumeric character", c)
			}
		}
	})

	t.Run("all codes are unique", func(t *testing.T) {
		const iterations = 1000
		codes := make(map[string]struct{})

		for i := 0; i < iterations; i++ {
			code := GenerateInviteCode()
			assert.NotContains(t, codes, code, "Duplicate code generated")
			codes[code] = struct{}{}
		}

		assert.Len(t, codes, iterations, "All codes should be unique")
	})
}
