package utils

import (
	"regexp"
	"testing"
)

func TestGenerateUUID(t *testing.T) {
	t.Run("generates non-empty UUID", func(t *testing.T) {
		got := GenerateUUID()
		if got == "" {
			t.Error("GenerateUUID() returned empty string")
		}
	})

	t.Run("generates valid UUID v4 format", func(t *testing.T) {
		uuid := GenerateUUID()

		// UUID v4 format: xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx
		// where x is any hex digit, 4 is the version, y is one of 8, 9, a, or b
		pattern := `^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`
		matched, err := regexp.MatchString(pattern, uuid)
		if err != nil {
			t.Fatalf("Failed to compile regex: %v", err)
		}

		if !matched {
			t.Errorf("GenerateUUID() = %s, expected valid UUID v4 format", uuid)
		}
	})

	t.Run("generates unique UUIDs", func(t *testing.T) {
		const iterations = 10000
		uuids := make(map[string]bool, iterations)

		for i := 0; i < iterations; i++ {
			uuid := GenerateUUID()
			if uuids[uuid] {
				t.Errorf("GenerateUUID() generated duplicate UUID after %d iterations: %s", i, uuid)
			}
			uuids[uuid] = true
		}
	})

	t.Run("generates correct length", func(t *testing.T) {
		uuid := GenerateUUID()
		// UUID format: 8-4-4-4-12 = 36 characters (32 hex + 4 dashes)
		expectedLen := 36

		if len(uuid) != expectedLen {
			t.Errorf("GenerateUUID() length = %d, expected %d", len(uuid), expectedLen)
		}
	})

	t.Run("version bit is set correctly", func(t *testing.T) {
		for i := 0; i < 100; i++ {
			uuid := GenerateUUID()
			// Check that the 13th character (version) is '4'
			if len(uuid) >= 14 && uuid[14] != '4' {
				t.Errorf("GenerateUUID() version bit not set correctly, got %c at position 14", uuid[14])
			}
		}
	})

	t.Run("variant bits are set correctly", func(t *testing.T) {
		for i := 0; i < 100; i++ {
			uuid := GenerateUUID()
			// Check that the 17th character (variant) is one of 8, 9, a, b
			if len(uuid) >= 18 {
				variant := uuid[19]
				if variant != '8' && variant != '9' && variant != 'a' && variant != 'b' {
					t.Errorf("GenerateUUID() variant bits not set correctly, got %c at position 19", variant)
				}
			}
		}
	})
}

func TestGenerateSubID(t *testing.T) {
	t.Run("generates non-empty SubID", func(t *testing.T) {
		got := GenerateSubID()
		if got == "" {
			t.Error("GenerateSubID() returned empty string")
		}
	})

	t.Run("generates valid hex format", func(t *testing.T) {
		subID := GenerateSubID()

		// SubID format: 14 hex characters (28 hex chars in the implementation)
		pattern := `^[0-9a-f]+$`
		matched, err := regexp.MatchString(pattern, subID)
		if err != nil {
			t.Fatalf("Failed to compile regex: %v", err)
		}

		if !matched {
			t.Errorf("GenerateSubID() = %s, expected hex characters only", subID)
		}
	})

	t.Run("generates unique SubIDs", func(t *testing.T) {
		const iterations = 10000
		subIDs := make(map[string]bool, iterations)

		for i := 0; i < iterations; i++ {
			subID := GenerateSubID()
			if subIDs[subID] {
				t.Errorf("GenerateSubID() generated duplicate SubID after %d iterations: %s", i, subID)
			}
			subIDs[subID] = true
		}
	})

	t.Run("generates correct length", func(t *testing.T) {
		subID := GenerateSubID()
		// 14 bytes = 28 hex characters
		expectedLen := 28

		if len(subID) != expectedLen {
			t.Errorf("GenerateSubID() length = %d, expected %d", len(subID), expectedLen)
		}
	})

	t.Run("generates URL-safe IDs", func(t *testing.T) {
		for i := 0; i < 100; i++ {
			subID := GenerateSubID()
			// Check that all characters are hex (0-9, a-f)
			for _, c := range subID {
				if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
					t.Errorf("GenerateSubID() contains non-URL-safe character: %c", c)
				}
			}
		}
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
			if uuids[uuid] {
				t.Errorf("Concurrent generation produced duplicate UUID: %s", uuid)
			}
			uuids[uuid] = true
		}
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
			if subIDs[subID] {
				t.Errorf("Concurrent generation produced duplicate SubID: %s", subID)
			}
			subIDs[subID] = true
		}
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
