package utils

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
)

// GenerateUUID generates a cryptographically secure UUID v4 identifier.
// This implementation follows RFC 4122 sections 4.4 for UUID version 4.
//
// Format: xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx
// Where x is random hex digit, 4 is version, y is variant (8, 9, a, or b)
func GenerateUUID() (string, error) {
	uuid := make([]byte, 16)

	if _, err := io.ReadFull(rand.Reader, uuid); err != nil {
		return "", fmt.Errorf("generate uuid: %w", err)
	}

	// Set version to 4 (random UUID) - bits 12-15 of time_hi_and_version
	uuid[6] = (uuid[6] & 0x0f) | 0x40

	// Set variant to RFC 4122 - bits 6-7 of clock_seq_hi_and_reserved
	uuid[8] = (uuid[8] & 0x3f) | 0x80

	// Format as UUID string: xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx
	return fmt.Sprintf("%x-%x-%x-%x-%x",
		uuid[0:4],
		uuid[4:6],
		uuid[6:8],
		uuid[8:10],
		uuid[10:16],
	), nil
}

// GenerateSubID generates a cryptographically secure random subscription identifier.
// Returns a 10-character hex string (5 random bytes).
//
// This provides 56 bits of entropy which is sufficient for subscription IDs
// and is URL-safe.
func GenerateSubID() (string, error) {
	bytes := make([]byte, 5)

	if _, err := io.ReadFull(rand.Reader, bytes); err != nil {
		return "", fmt.Errorf("generate sub id: %w", err)
	}

	return hex.EncodeToString(bytes), nil
}

// GenerateInviteCode generates a cryptographically secure random invite code.
// Returns an 8-character lowercase alphanumeric string.
// Uses rejection sampling to avoid modulo bias.
func GenerateInviteCode() (string, error) {
	const charset = "0123456789abcdefghijklmnopqrstuvwxyz"
	const charsetLen = len(charset) // 36
	const limit = 252               // 36 * 7, largest multiple of 36 < 256

	result := make([]byte, 8)
	buf := make([]byte, 8)

	for i := 0; i < 8; i++ {
		for {
			if _, err := io.ReadFull(rand.Reader, buf); err != nil {
				return "", fmt.Errorf("generate invite code: %w", err)
			}
			// Use bytes from buffer, rejecting values >= limit to avoid bias
			used := false
			for j := 0; j < len(buf); j++ {
				if buf[j] < limit {
					result[i] = charset[buf[j]%byte(charsetLen)]
					used = true
					break
				}
			}
			if used {
				break
			}
			// All bytes rejected (extremely unlikely), read more
		}
	}

	return string(result), nil
}
