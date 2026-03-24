package utils

import (
	"bytes"
	"fmt"

	go_qr "github.com/piglig/go-qr"
)

// GenerateQRCodePNG generates a QR code PNG image in memory.
// Returns the PNG bytes or an error.
func GenerateQRCodePNG(data string) ([]byte, error) {
	qr, err := go_qr.EncodeText(data, go_qr.Low)
	if err != nil {
		return nil, fmt.Errorf("failed to encode QR: %w", err)
	}

	// Scale=14, Border=4 gives approximately 512px for typical URLs
	config := go_qr.NewQrCodeImgConfig(14, 4)

	var buf bytes.Buffer
	if err := qr.WriteAsPNG(config, &buf); err != nil {
		return nil, fmt.Errorf("failed to write PNG: %w", err)
	}

	return buf.Bytes(), nil
}
