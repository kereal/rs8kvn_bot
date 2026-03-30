package utils

import (
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
}
