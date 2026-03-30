package xui

import (
	"context"
	"errors"
	"sync"
	"time"
)

var ErrCircuitOpen = errors.New("circuit breaker is open")

type CircuitState int

const (
	CircuitStateClosed CircuitState = iota
	CircuitStateOpen
	CircuitStateHalfOpen
)

type CircuitBreaker struct {
	mu               sync.RWMutex
	state            CircuitState
	failures         int
	successes        int
	halfOpenAttempts int
	lastFailure      time.Time

	maxFailures int
	timeout     time.Duration
	halfOpenMax int
}

func NewCircuitBreaker(maxFailures int, timeout time.Duration) *CircuitBreaker {
	return &CircuitBreaker{
		state:       CircuitStateClosed,
		maxFailures: maxFailures,
		timeout:     timeout,
		halfOpenMax: 3,
	}
}

func (cb *CircuitBreaker) Execute(ctx context.Context, fn func() error) error {
	// Check if context is cancelled before proceeding
	if err := ctx.Err(); err != nil {
		return err
	}

	if !cb.allowRequest() {
		return ErrCircuitOpen
	}

	err := fn()
	cb.recordResult(err)

	if err != nil {
		return err
	}
	return nil
}

func (cb *CircuitBreaker) allowRequest() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case CircuitStateClosed:
		return true

	case CircuitStateOpen:
		if time.Since(cb.lastFailure) >= cb.timeout {
			cb.state = CircuitStateHalfOpen
			cb.successes = 0
			cb.halfOpenAttempts = 1 // Count this transition as the first half-open attempt
			return true
		}
		return false

	case CircuitStateHalfOpen:
		if cb.halfOpenAttempts < cb.halfOpenMax {
			cb.halfOpenAttempts++
			return true
		}
		return false

	default:
		return true
	}
}

func (cb *CircuitBreaker) recordResult(err error) {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	if err != nil {
		cb.failures++
		cb.lastFailure = time.Now()

		if cb.failures >= cb.maxFailures {
			cb.state = CircuitStateOpen
		}
		return
	}

	switch cb.state {
	case CircuitStateClosed:
		cb.failures = 0

	case CircuitStateHalfOpen:
		cb.successes++
		if cb.successes >= cb.halfOpenMax {
			cb.state = CircuitStateClosed
			cb.failures = 0
			cb.successes = 0
			cb.halfOpenAttempts = 0
		}
	}
}

func (cb *CircuitBreaker) State() CircuitState {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.state
}

func (cb *CircuitBreaker) Failures() int {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.failures
}

func (cb *CircuitBreaker) Reset() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.state = CircuitStateClosed
	cb.failures = 0
	cb.successes = 0
	cb.halfOpenAttempts = 0
}
