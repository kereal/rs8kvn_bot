package subserver

import "regexp"

func SubIDRegex() *regexp.Regexp {
	return regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
}

const MaxIPEntries = 100

// SubscriptionResult represents the outcome of a subscription request.
// It carries the final body, response headers, and an optional status code
// for the HTTP layer to replay.
type SubscriptionResult struct {
	Body       []byte
	Headers    map[string]string
	StatusCode int
}
