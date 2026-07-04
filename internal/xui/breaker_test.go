package xui

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestCircuitBreaker_FailureThreshold(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		failuresBefore int
		wantState     CircuitState
		wantAllowed   bool
	}{
		{"just below threshold allows", 1, CircuitStateClosed, true},
		{"exactly at threshold opens", 2, CircuitStateOpen, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cb := NewCircuitBreaker(2, 10*time.Second)

			for i := 0; i < tt.failuresBefore; i++ {
				cb.recordResult(errors.New("failure"))
			}

			assert.Equal(t, tt.wantState, cb.state)
			assert.Equal(t, tt.wantAllowed, cb.allowRequest())
			if tt.wantState == CircuitStateOpen {
				assert.Equal(t, CircuitStateOpen, cb.state)
			}
		})
	}
}

func TestCircuitBreaker_AllowsRequestsWhenClosed(t *testing.T) {
	t.Parallel()

	cb := NewCircuitBreaker(3, 10*time.Second)

	allowed := cb.allowRequest()
	assert.True(t, allowed, "Expected request to be allowed when circuit is closed")
}

func TestCircuitBreaker_OpensAfterMaxFailures(t *testing.T) {
	t.Parallel()

	cb := NewCircuitBreaker(3, 10*time.Second)

	for i := 0; i < 3; i++ {
		cb.recordResult(errors.New("failure"))
	}

	assert.Equal(t, CircuitStateOpen, cb.state, "State after 3 failures should be open")

	allowed := cb.allowRequest()
	assert.False(t, allowed, "Expected request to be blocked when circuit is open")
}

func TestCircuitBreaker_Execute(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		setup        func() *CircuitBreaker
		fn           func() error
		wantErr      bool
		wantErrIs    error
	}{
		{
			name: "success",
			setup: func() *CircuitBreaker {
				return NewCircuitBreaker(3, 10*time.Second)
			},
			fn:      func() error { return nil },
			wantErr: false,
		},
		{
			name: "failure",
			setup: func() *CircuitBreaker {
				return NewCircuitBreaker(3, 10*time.Second)
			},
			fn:      func() error { return errors.New("test error") },
			wantErr: true,
		},
		{
			name: "open circuit",
			setup: func() *CircuitBreaker {
				cb := NewCircuitBreaker(2, 10*time.Second)
				cb.recordResult(errors.New("failure"))
				cb.recordResult(errors.New("failure"))
				return cb
			},
			fn:       func() error { return nil },
			wantErr:  true,
			wantErrIs: ErrCircuitOpen,
		},
		{
			name: "context cancellation",
			setup: func() *CircuitBreaker {
				return NewCircuitBreaker(3, 10*time.Second)
			},
			fn: func() error { return nil },
			wantErr: true,
			wantErrIs: func() error {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx.Err()
			}(),
		},
		{
			name: "context timeout",
			setup: func() *CircuitBreaker {
				return NewCircuitBreaker(3, 100*time.Millisecond)
			},
			fn: func() error { return nil },
			wantErr: true,
			wantErrIs: func() error {
				ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
				defer cancel()
				time.Sleep(2 * time.Millisecond)
				return ctx.Err()
			}(),
		},
		{
			name: "panicking callback",
			setup: func() *CircuitBreaker {
				return NewCircuitBreaker(3, 10*time.Second)
			},
			fn: func() error {
				panic("test panic")
			},
			wantErr: false, // panic propagates, not an error return
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cb := tt.setup()

			if tt.name == "panicking callback" {
				defer func() {
					assert.True(t, recover() != nil, "panic should propagate")
				}()
				cb.Execute(context.Background(), tt.fn)
				return
			}

			ctx := context.Background()
			if tt.name == "context cancellation" {
				cancelCtx, cancel := context.WithCancel(ctx)
				cancel()
				ctx = cancelCtx
			}
			if tt.name == "context timeout" {
				timeoutCtx, cancel := context.WithTimeout(ctx, 1*time.Nanosecond)
				time.Sleep(2 * time.Millisecond)
				ctx = timeoutCtx
				defer cancel()
			}

			err := cb.Execute(ctx, tt.fn)
			if tt.wantErr {
				assert.Error(t, err)
				if tt.wantErrIs != nil {
					assert.ErrorIs(t, err, tt.wantErrIs)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestCircuitBreaker_HalfOpenTransitions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		setup    func() (*CircuitBreaker, func())
		actions  func(t *testing.T, cb *CircuitBreaker)
		wantState CircuitState
	}{
		{
			name: "half-open after timeout",
			setup: func() (*CircuitBreaker, func()) {
				cb := NewCircuitBreaker(2, 5*time.Millisecond)
				cb.recordResult(errors.New("failure"))
				cb.recordResult(errors.New("failure"))
				return cb, func() { time.Sleep(10 * time.Millisecond) }
			},
			actions: func(t *testing.T, cb *CircuitBreaker) {
				allowed := cb.allowRequest()
				assert.True(t, allowed, "Should allow request when circuit is half-open after timeout")
			},
			wantState: CircuitStateHalfOpen,
		},
		{
			name: "closes after successes",
			setup: func() (*CircuitBreaker, func()) {
				cb := NewCircuitBreaker(2, 5*time.Millisecond)
				cb.recordResult(errors.New("failure"))
				cb.recordResult(errors.New("failure"))
				return cb, func() { time.Sleep(10 * time.Millisecond) }
			},
			actions: func(t *testing.T, cb *CircuitBreaker) {
				cb.allowRequest() // enter half-open
				for i := 0; i < 3; i++ {
					cb.recordResult(nil)
				}
			},
			wantState: CircuitStateClosed,
		},
		{
			name: "max half-open attempts",
			setup: func() (*CircuitBreaker, func()) {
				cb := NewCircuitBreaker(2, 5*time.Millisecond)
				cb.recordResult(errors.New("failure"))
				cb.recordResult(errors.New("failure"))
				return cb, func() { time.Sleep(10 * time.Millisecond) }
			},
			actions: func(t *testing.T, cb *CircuitBreaker) {
				cb.allowRequest() // 1st attempt
				cb.allowRequest() // 2nd
				cb.allowRequest() // 3rd (halfOpenMax=3)
				allowed := cb.allowRequest() // 4th should be blocked
				assert.False(t, allowed, "Fourth request should be blocked after max half-open attempts")
			},
			wantState: CircuitStateHalfOpen,
		},
		{
			name: "failure in half-open reopens circuit",
			setup: func() (*CircuitBreaker, func()) {
				cb := NewCircuitBreaker(2, 5*time.Millisecond)
				cb.recordResult(errors.New("failure"))
				cb.recordResult(errors.New("failure"))
				return cb, func() { time.Sleep(10 * time.Millisecond) }
			},
			actions: func(t *testing.T, cb *CircuitBreaker) {
				cb.allowRequest() // enter half-open
				cb.recordResult(errors.New("failure"))
			},
			wantState: CircuitStateOpen,
		},
		{
			name: "multiple transitions",
			setup: func() (*CircuitBreaker, func()) {
				cb := NewCircuitBreaker(2, 5*time.Millisecond)
				return cb, func() {}
			},
			actions: func(t *testing.T, cb *CircuitBreaker) {
				// closed -> open
				cb.recordResult(errors.New("failure"))
				cb.recordResult(errors.New("failure"))
				assert.Equal(t, CircuitStateOpen, cb.state)

				time.Sleep(10 * time.Millisecond)
				cb.allowRequest() // open -> half-open
				assert.Equal(t, CircuitStateHalfOpen, cb.state)

				for i := 0; i < 3; i++ {
					cb.recordResult(nil)
				} // half-open -> closed
				assert.Equal(t, CircuitStateClosed, cb.state)

				cb.recordResult(errors.New("failure"))
				cb.recordResult(errors.New("failure")) // closed -> open again
				assert.Equal(t, CircuitStateOpen, cb.state)
			},
			wantState: CircuitStateOpen,
		},
		{
			name: "timeout not reached stays open",
			setup: func() (*CircuitBreaker, func()) {
				cb := NewCircuitBreaker(2, 1*time.Second)
				cb.recordResult(errors.New("failure"))
				cb.recordResult(errors.New("failure"))
				return cb, func() {}
			},
			actions: func(t *testing.T, cb *CircuitBreaker) {
				allowed := cb.allowRequest()
				assert.False(t, allowed, "Request should be blocked before timeout")
			},
			wantState: CircuitStateOpen,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cb, prep := tt.setup()
			prep()
			tt.actions(t, cb)
			assert.Equal(t, tt.wantState, cb.state)
		})
	}
}

func TestCircuitBreaker_Reset(t *testing.T) {
	t.Parallel()

	cb := NewCircuitBreaker(3, 10*time.Second)

	cb.recordResult(errors.New("failure"))
	cb.recordResult(errors.New("failure"))
	cb.recordResult(errors.New("failure"))

	cb.Reset()

	assert.Equal(t, CircuitStateClosed, cb.state, "State after Reset() should be closed")
	assert.Equal(t, 0, cb.failures, "Failures after Reset() should be 0")
}

func TestCircuitBreaker_Getters(t *testing.T) {
	t.Parallel()

	cb := NewCircuitBreaker(5, 10*time.Second)

	assert.Equal(t, CircuitStateClosed, cb.State())
	assert.Equal(t, 0, cb.Failures())
}

func TestCircuitBreaker_ConcurrentAccess(t *testing.T) {
	t.Parallel()

	cb := NewCircuitBreaker(100, 10*time.Second)

	const goroutines = 50
	const operationsPerGoroutine = 100

	var wg sync.WaitGroup
	errCh := make(chan error, goroutines*operationsPerGoroutine)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < operationsPerGoroutine; j++ {
				err := cb.Execute(context.Background(), func() error {
					return nil
				})
				if err != nil {
					errCh <- err
				}
			}
		}()
	}

	wg.Wait()
	close(errCh)

	errorCount := 0
	for err := range errCh {
		if errors.Is(err, ErrCircuitOpen) {
			errorCount++
		}
	}

	assert.Equal(t, 0, errorCount, "No requests should be blocked in closed state")
	assert.Equal(t, CircuitStateClosed, cb.state, "State should remain closed")
}

func TestCircuitBreaker_ConcurrentFailures(t *testing.T) {
	t.Parallel()

	cb := NewCircuitBreaker(10, 10*time.Second)

	const goroutines = 20
	const failuresPerGoroutine = 2

	var wg sync.WaitGroup

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < failuresPerGoroutine; j++ {
				_ = cb.Execute(context.Background(), func() error {
					return errors.New("failure")
				})
			}
		}()
	}

	wg.Wait()

	assert.GreaterOrEqual(t, cb.failures, 10, "Failures should be at least maxFailures")
	assert.Equal(t, CircuitStateOpen, cb.state, "State should be open after concurrent failures")
}

func TestCircuitBreaker_ResetClearsAllState(t *testing.T) {
	t.Parallel()

	cb := NewCircuitBreaker(2, 100*time.Millisecond)

	cb.recordResult(errors.New("failure"))
	cb.recordResult(errors.New("failure"))
	cb.recordResult(errors.New("failure"))

	time.Sleep(10 * time.Millisecond)
	cb.allowRequest()

	cb.recordResult(nil)
	cb.recordResult(nil)

	cb.Reset()

	assert.Equal(t, CircuitStateClosed, cb.state, "State after Reset() should be closed")
	assert.Equal(t, 0, cb.failures, "Failures after Reset() should be 0")
	assert.Equal(t, 0, cb.successes, "Successes after Reset() should be 0")
	assert.Equal(t, 0, cb.halfOpenAttempts, "HalfOpenAttempts after Reset() should be 0")
}

func TestCircuitBreaker_SuccessInClosedResetsFailures(t *testing.T) {
	t.Parallel()

	cb := NewCircuitBreaker(5, 10*time.Second)

	cb.recordResult(errors.New("failure"))
	cb.recordResult(errors.New("failure"))
	assert.Equal(t, 2, cb.failures, "Failures should be 2")

	cb.recordResult(nil)
	assert.Equal(t, 0, cb.failures, "Failures should be reset to 0 after success in closed state")
}
