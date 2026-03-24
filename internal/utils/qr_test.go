package utils

import (
	"bytes"
	"testing"
)

func TestGenerateQRCodePNG(t *testing.T) {
	t.Run("generates PNG for valid URL", func(t *testing.T) {
		data := "https://example.com/vmess://abc123"
		png, err := GenerateQRCodePNG(data)
		if err != nil {
			t.Fatalf("GenerateQRCodePNG() error = %v", err)
		}
		if len(png) == 0 {
			t.Error("GenerateQRCodePNG() returned empty bytes")
		}
	})

	t.Run("generates valid PNG signature", func(t *testing.T) {
		data := "https://example.com"
		png, err := GenerateQRCodePNG(data)
		if err != nil {
			t.Fatalf("GenerateQRCodePNG() error = %v", err)
		}

		// PNG signature: 137 80 78 71 13 10 26 10
		expectedSignature := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
		if len(png) < 8 || !bytes.Equal(png[:8], expectedSignature) {
			t.Error("GenerateQRCodePNG() did not produce valid PNG signature")
		}
	})

	t.Run("different inputs produce different outputs", func(t *testing.T) {
		png1, err := GenerateQRCodePNG("https://example.com/1")
		if err != nil {
			t.Fatalf("GenerateQRCodePNG() error = %v", err)
		}
		png2, err := GenerateQRCodePNG("https://example.com/2")
		if err != nil {
			t.Fatalf("GenerateQRCodePNG() error = %v", err)
		}
		if bytes.Equal(png1, png2) {
			t.Error("GenerateQRCodePNG() produced identical outputs for different inputs")
		}
	})

	t.Run("handles empty string", func(t *testing.T) {
		png, err := GenerateQRCodePNG("")
		if err != nil {
			t.Fatalf("GenerateQRCodePNG() error = %v", err)
		}
		if len(png) == 0 {
			t.Error("GenerateQRCodePNG() returned empty bytes for empty input")
		}
	})
}
