package bot

import (
	"fmt"
	"strconv"
	"strings"
)

// parseBroadcastCallbackID validates callback payloads before they reach the
// repository, keeping malformed or oversized IDs out of admin actions.
func parseBroadcastCallbackID(data, prefix string) (uint, error) {
	raw := strings.TrimPrefix(data, prefix)
	value, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || value == 0 || value > uint64(^uint(0)) {
		if err == nil {
			err = fmt.Errorf("invalid broadcast id")
		}
		return 0, fmt.Errorf("parse broadcast callback %q: %w", data, err)
	}
	return uint(value), nil
}
