package utils

import (
	"fmt"
	"time"
)

// GenerateUUID generates a unique identifier in UUID-like format
// based on current timestamp and nanoseconds.
func GenerateUUID() string {
	now := time.Now()
	unix := now.Unix()
	nano := now.UnixNano()

	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		unix,
		nano&0xFFFF,
		(nano>>16)&0xFFFF,
		(nano>>32)&0xFFFF,
		nano&0xFFFFFFFFFFFF,
	)
}

// GenerateSubID generates a unique subscription identifier
// based on current nanosecond timestamp.
func GenerateSubID() string {
	return fmt.Sprintf("%x", time.Now().UnixNano()&0xFFFFFFFFFFFFFF)
}
