package subserver

import "errors"

var (
	ErrSubscriptionNotFound = errors.New("subscription not found")
	ErrNoSubscriptionItems  = errors.New("no subscription items found")
)
