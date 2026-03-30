package utils

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateQRCodePNG(t *testing.T) {
	t.Run("generates PNG for valid URL", func(t *testing.T) {
		data := "https://example.com/vmess://abc123"
		png, err := GenerateQRCodePNG(data)
		require.NoError(t, err, "GenerateQRCodePNG() error")
		assert.NotEmpty(t, png, "GenerateQRCodePNG() returned empty bytes")
	})

	t.Run("generates valid PNG signature", func(t *testing.T) {
		data := "https://example.com"
		png, err := GenerateQRCodePNG(data)
		require.NoError(t, err, "GenerateQRCodePNG() error")

		// PNG signature: 137 80 78 71 13 10 26 10
		expectedSignature := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
		require.GreaterOrEqual(t, len(png), 8, "PNG data should be at least 8 bytes")
		assert.Equal(t, expectedSignature, png[:8], "GenerateQRCodePNG() did not produce valid PNG signature")
	})

	t.Run("different inputs produce different outputs", func(t *testing.T) {
		png1, err := GenerateQRCodePNG("https://example.com/1")
		require.NoError(t, err, "GenerateQRCodePNG() error for first input")
		png2, err := GenerateQRCodePNG("https://example.com/2")
		require.NoError(t, err, "GenerateQRCodePNG() error for second input")
		assert.NotEqual(t, png1, png2, "GenerateQRCodePNG() produced identical outputs for different inputs")
	})

	t.Run("handles empty string", func(t *testing.T) {
		png, err := GenerateQRCodePNG("")
		require.NoError(t, err, "GenerateQRCodePNG() error for empty input")
		assert.NotEmpty(t, png, "GenerateQRCodePNG() returned empty bytes for empty input")
	})

	t.Run("handles special characters", func(t *testing.T) {
		data := "https://example.com/path?query=value&foo=bar#anchor"
		png, err := GenerateQRCodePNG(data)
		require.NoError(t, err, "GenerateQRCodePNG() error for special characters")
		assert.NotEmpty(t, png, "GenerateQRCodePNG() returned empty bytes for special characters")
	})

	t.Run("handles unicode characters", func(t *testing.T) {
		data := "https://example.com/привет/世界"
		png, err := GenerateQRCodePNG(data)
		require.NoError(t, err, "GenerateQRCodePNG() error for unicode")
		assert.NotEmpty(t, png, "GenerateQRCodePNG() returned empty bytes for unicode")
	})

	t.Run("handles VLESS URL", func(t *testing.T) {
		data := "vless://abc123-uuid@example.com:443?encryption=none&security=reality&type=tcp&headerType=none#My%20Server"
		png, err := GenerateQRCodePNG(data)
		require.NoError(t, err, "GenerateQRCodePNG() error for VLESS URL")
		assert.NotEmpty(t, png, "GenerateQRCodePNG() returned empty bytes for VLESS URL")
	})

	t.Run("handles long URL", func(t *testing.T) {
		data := "https://example.com/path?data=" + repeatString("a", 500)
		png, err := GenerateQRCodePNG(data)
		require.NoError(t, err, "GenerateQRCodePNG() error for long URL")
		assert.NotEmpty(t, png, "GenerateQRCodePNG() returned empty bytes for long URL")
	})

	t.Run("consistent output for same input", func(t *testing.T) {
		data := "https://example.com/consistent"
		png1, err := GenerateQRCodePNG(data)
		require.NoError(t, err, "GenerateQRCodePNG() error for first call")
		png2, err := GenerateQRCodePNG(data)
		require.NoError(t, err, "GenerateQRCodePNG() error for second call")
		assert.Equal(t, png1, png2, "GenerateQRCodePNG() should produce consistent output for same input")
	})

	t.Run("PNG contains IHDR chunk", func(t *testing.T) {
		png, err := GenerateQRCodePNG("test")
		require.NoError(t, err, "GenerateQRCodePNG() error")
		require.GreaterOrEqual(t, len(png), 16, "PNG data should contain IHDR chunk")

		// PNG chunks: length (4 bytes) + type (4 bytes)
		// IHDR starts at byte 8
		assert.Equal(t, []byte{'I', 'H', 'D', 'R'}, png[12:16], "PNG should have IHDR chunk")
	})

	t.Run("PNG contains IEND chunk", func(t *testing.T) {
		png, err := GenerateQRCodePNG("test")
		require.NoError(t, err, "GenerateQRCodePNG() error")
		require.GreaterOrEqual(t, len(png), 12, "PNG data should contain IEND chunk")

		// IEND is the last chunk
		lastBytes := png[len(png)-8:]
		assert.Equal(t, []byte{'I', 'E', 'N', 'D'}, lastBytes[4:8], "PNG should end with IEND chunk")
	})
}

func TestGenerateQRCodePNG_Concurrent(t *testing.T) {
	t.Run("concurrent generation is safe", func(t *testing.T) {
		const goroutines = 50
		const qrPerGoroutine = 20

		results := make(chan []byte, goroutines*qrPerGoroutine)
		var wg sync.WaitGroup

		for i := 0; i < goroutines; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				for j := 0; j < qrPerGoroutine; j++ {
					png, err := GenerateQRCodePNG("https://example.com/test")
					if err == nil && len(png) > 0 {
						results <- png
					}
				}
			}(i)
		}

		wg.Wait()
		close(results)

		count := 0
		for range results {
			count++
		}

		assert.Equal(t, goroutines*qrPerGoroutine, count, "All QR codes should be generated successfully")
	})

	t.Run("concurrent generation produces consistent output", func(t *testing.T) {
		const goroutines = 10
		data := "https://example.com/consistent"

		results := make(chan []byte, goroutines)
		var wg sync.WaitGroup

		for i := 0; i < goroutines; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				png, err := GenerateQRCodePNG(data)
				if err == nil {
					results <- png
				}
			}()
		}

		wg.Wait()
		close(results)

		var first []byte
		for png := range results {
			if first == nil {
				first = png
			} else {
				assert.Equal(t, first, png, "Concurrent generation should produce identical results")
			}
		}
	})
}

func TestGenerateQRCodePNG_EdgeCases(t *testing.T) {
	t.Run("single character", func(t *testing.T) {
		png, err := GenerateQRCodePNG("a")
		require.NoError(t, err, "GenerateQRCodePNG() error for single character")
		assert.NotEmpty(t, png, "GenerateQRCodePNG() returned empty bytes")
	})

	t.Run("numbers only", func(t *testing.T) {
		png, err := GenerateQRCodePNG("1234567890")
		require.NoError(t, err, "GenerateQRCodePNG() error for numbers")
		assert.NotEmpty(t, png, "GenerateQRCodePNG() returned empty bytes")
	})

	t.Run("binary-like string", func(t *testing.T) {
		data := string([]byte{0x00, 0x01, 0x02, 0x03, 0xFF, 0xFE, 0xFD})
		png, err := GenerateQRCodePNG(data)
		require.NoError(t, err, "GenerateQRCodePNG() error for binary-like string")
		assert.NotEmpty(t, png, "GenerateQRCodePNG() returned empty bytes")
	})

	t.Run("newline characters", func(t *testing.T) {
		data := "line1\nline2\nline3"
		png, err := GenerateQRCodePNG(data)
		require.NoError(t, err, "GenerateQRCodePNG() error for newlines")
		assert.NotEmpty(t, png, "GenerateQRCodePNG() returned empty bytes")
	})

	t.Run("whitespace only", func(t *testing.T) {
		png, err := GenerateQRCodePNG("   \t\n   ")
		require.NoError(t, err, "GenerateQRCodePNG() error for whitespace")
		assert.NotEmpty(t, png, "GenerateQRCodePNG() returned empty bytes")
	})

	t.Run("very short URL", func(t *testing.T) {
		png, err := GenerateQRCodePNG("a.b")
		require.NoError(t, err, "GenerateQRCodePNG() error for short URL")
		assert.NotEmpty(t, png, "GenerateQRCodePNG() returned empty bytes")
	})
}

// BenchmarkGenerateQRCodePNG benchmarks QR code generation
func BenchmarkGenerateQRCodePNG(b *testing.B) {
	data := "https://example.com/vless://abc123-uuid@example.com:443?encryption=none&security=reality"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = GenerateQRCodePNG(data)
	}
}

// BenchmarkGenerateQRCodePNG_Short benchmarks QR generation for short strings
func BenchmarkGenerateQRCodePNG_Short(b *testing.B) {
	data := "test"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = GenerateQRCodePNG(data)
	}
}

// BenchmarkGenerateQRCodePNG_Empty benchmarks QR generation for empty string
func BenchmarkGenerateQRCodePNG_Empty(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = GenerateQRCodePNG("")
	}
}

// BenchmarkGenerateQRCodePNG_Long benchmarks QR generation for long strings
func BenchmarkGenerateQRCodePNG_Long(b *testing.B) {
	data := repeatString("a", 1000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = GenerateQRCodePNG(data)
	}
}

// BenchmarkGenerateQRCodePNG_Parallel benchmarks parallel QR generation
func BenchmarkGenerateQRCodePNG_Parallel(b *testing.B) {
	data := "https://example.com/test"
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _ = GenerateQRCodePNG(data)
		}
	})
}

// repeatString creates a string by repeating a character
func repeatString(s string, n int) string {
	result := make([]byte, 0, len(s)*n)
	for i := 0; i < n; i++ {
		result = append(result, s...)
	}
	return string(result)
}
