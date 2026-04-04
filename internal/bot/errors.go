package bot

import (
	"errors"
	"strings"
)

// Sentinel errors for XUI-related failures.
// These are used to classify raw errors from the XUI client and subscription service
// so that error handling does not depend on fragile string matching at the call site.
var (
	ErrXUIConnection      = errors.New("xui: connection error")
	ErrXUIAuth            = errors.New("xui: authentication error")
	ErrXUIContextCanceled = errors.New("xui: context canceled")
	ErrXUIDNS             = errors.New("xui: DNS/network error")
	ErrXUITLS             = errors.New("xui: TLS/SSL error")
	ErrXUIServer          = errors.New("xui: server error")
	ErrXUIRollbackFailed  = errors.New("xui: rollback failed")
	ErrXUICircuitOpen     = errors.New("xui: circuit breaker open")
)

// classifyXUIError wraps the given error with an appropriate sentinel error
// based on its message content. If no pattern matches, the original error is returned unchanged.
// This centralizes all string-based error classification in one place.
func classifyXUIError(err error) error {
	if err == nil {
		return nil
	}

	msg := strings.ToLower(err.Error())

	switch {
	case strings.Contains(msg, "rollback failed"):
		return ErrXUIRollbackFailed
	case strings.Contains(msg, "connection refused") || strings.Contains(msg, "timeout"):
		return ErrXUIConnection
	case strings.Contains(msg, "authentication") || strings.Contains(msg, "unauthorized") ||
		strings.Contains(msg, "login returned http") || strings.Contains(msg, "auto-relogin failed"):
		return ErrXUIAuth
	case strings.Contains(msg, "context canceled"):
		return ErrXUIContextCanceled
	case strings.Contains(msg, "no such host") || strings.Contains(msg, "dial tcp"):
		return ErrXUIDNS
	case strings.Contains(msg, "certificate") || strings.Contains(msg, "tls"):
		return ErrXUITLS
	case strings.Contains(msg, "inbound") || strings.Contains(msg, "client"):
		return ErrXUIServer
	case strings.Contains(msg, "circuit breaker is open"):
		return ErrXUICircuitOpen
	}

	return err
}
