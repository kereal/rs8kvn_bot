package utils

import (
	"fmt"
	"time"
)

// GenerateUUID generates a unique identifier in UUID-like format
// based on current timestamp and nanoseconds.
func GenerateUUID() string {
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		time.Now().Unix(),
		time.Now().UnixNano()&0xFFFF,
		(time.Now().UnixNano()>>16)&0xFFFF,
		(time.Now().UnixNano()>>32)&0xFFFF,
		time.Now().UnixNano()&0xFFFFFFFFFFFF,
	)
}

// GenerateSubID generates a unique subscription identifier
// based on current nanosecond timestamp.
func GenerateSubID() string {
	return fmt.Sprintf("%x", time.Now().UnixNano()&0xFFFFFFFFFFFFFF)
}
