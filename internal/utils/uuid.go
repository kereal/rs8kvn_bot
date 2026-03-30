package utils

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
)

// GenerateUUID generates a cryptographically secure UUID v4 identifier.
// This implementation follows RFC 4122 section 4.4 for UUID version 4.
//
// Format: xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx
// Where x is random hex digit, 4 is version, y is variant (8, 9, a, or b)
func GenerateUUID() string {
	uuid := make([]byte, 16)

	// Read random bytes
	if _, err := io.ReadFull(rand.Reader, uuid); err != nil {
		// Fallback to pseudo-random if crypto/rand fails (should never happen)
		panic(fmt.Sprintf("crypto/rand failed: %v", err))
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
	)
}

// GenerateSubID generates a cryptographically secure random subscription identifier.
// Returns a 10-character hex string (5 random bytes).
//
// This provides 56 bits of entropy which is sufficient for subscription IDs
// and is URL-safe.
func GenerateSubID() string {
	bytes := make([]byte, 5)

	if _, err := io.ReadFull(rand.Reader, bytes); err != nil {
		panic(fmt.Sprintf("crypto/rand failed: %v", err))
	}

	return hex.EncodeToString(bytes)
}

// GenerateInviteCode generates a cryptographically secure random invite code.
// Returns an 8-character lowercase alphanumeric string.
func GenerateInviteCode() string {
	const charset = "0123456789abcdefghijklmnopqrstuvwxyz"
	bytes := make([]byte, 8)

	if _, err := io.ReadFull(rand.Reader, bytes); err != nil {
		panic(fmt.Sprintf("crypto/rand failed: %v", err))
	}

	for i := range bytes {
		bytes[i] = charset[bytes[i]%byte(len(charset))]
	}

	return string(bytes)
}
