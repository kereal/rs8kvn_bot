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
