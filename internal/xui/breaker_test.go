package xui

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNewCircuitBreaker(t *testing.T) {
	cb := NewCircuitBreaker(3, 10*time.Second)

	assert.Equal(t, CircuitStateClosed, cb.state, "Initial state should be closed")
	assert.Equal(t, 0, cb.failures, "Initial failures should be 0")
}

func TestCircuitBreaker_AllowsRequestsWhenClosed(t *testing.T) {
	cb := NewCircuitBreaker(3, 10*time.Second)

	allowed := cb.allowRequest()
	assert.True(t, allowed, "Expected request to be allowed when circuit is closed")
}

func TestCircuitBreaker_OpensAfterMaxFailures(t *testing.T) {
	cb := NewCircuitBreaker(3, 10*time.Second)

	// Record 3 failures
	for i := 0; i < 3; i++ {
		cb.recordResult(errors.New("failure"))
	}

	assert.Equal(t, CircuitStateOpen, cb.state, "State after 3 failures should be open")

	// Next request should be blocked
	allowed := cb.allowRequest()
	assert.False(t, allowed, "Expected request to be blocked when circuit is open")
}

func TestCircuitBreaker_HalfOpenAfterTimeout(t *testing.T) {
	cb := NewCircuitBreaker(2, 50*time.Millisecond)

	// Record 2 failures to open the circuit
	cb.recordResult(errors.New("failure"))
	cb.recordResult(errors.New("failure"))

	assert.Equal(t, CircuitStateOpen, cb.state, "State should be open")

	// Wait for timeout
	time.Sleep(60 * time.Millisecond)

	// Next request should be allowed (half-open)
	allowed := cb.allowRequest()
	assert.True(t, allowed, "Expected request to be allowed when circuit is half-open after timeout")
	assert.Equal(t, CircuitStateHalfOpen, cb.state, "State after timeout should be half-open")
}

func TestCircuitBreaker_ClosesAfterSuccesses(t *testing.T) {
	cb := NewCircuitBreaker(2, 50*time.Millisecond)

	// Open the circuit
	cb.recordResult(errors.New("failure"))
	cb.recordResult(errors.New("failure"))

	// Wait and enter half-open
	time.Sleep(60 * time.Millisecond)
	cb.allowRequest()

	// Record 3 successes
	for i := 0; i < 3; i++ {
		cb.recordResult(nil)
	}

	assert.Equal(t, CircuitStateClosed, cb.state, "State after 3 successes should be closed")
}

func TestCircuitBreaker_Execute_Success(t *testing.T) {
	cb := NewCircuitBreaker(3, 10*time.Second)

	err := cb.Execute(context.Background(), func() error {
		return nil
	})

	assert.NoError(t, err, "Execute() error should be nil")
	assert.Equal(t, 0, cb.failures, "Failures after success should be 0")
}

func TestCircuitBreaker_Execute_Failure(t *testing.T) {
	testErr := errors.New("test error")
	cb := NewCircuitBreaker(3, 10*time.Second)

	err := cb.Execute(context.Background(), func() error {
		return testErr
	})

	assert.Equal(t, testErr, err, "Execute() should return test error")
	assert.Equal(t, 1, cb.failures, "Failures after one failure should be 1")
}

func TestCircuitBreaker_Execute_OpenCircuit(t *testing.T) {
	cb := NewCircuitBreaker(2, 10*time.Second)

	// Open the circuit
	cb.recordResult(errors.New("failure"))
	cb.recordResult(errors.New("failure"))

	// Execute should fail with circuit open error
	err := cb.Execute(context.Background(), func() error {
		return nil
	})

	assert.ErrorIs(t, err, ErrCircuitOpen, "Execute() should return ErrCircuitOpen")
}

func TestCircuitBreaker_Reset(t *testing.T) {
	cb := NewCircuitBreaker(3, 10*time.Second)

	// Open the circuit
	cb.recordResult(errors.New("failure"))
	cb.recordResult(errors.New("failure"))
	cb.recordResult(errors.New("failure"))

	cb.Reset()

	assert.Equal(t, CircuitStateClosed, cb.state, "State after Reset() should be closed")
	assert.Equal(t, 0, cb.failures, "Failures after Reset() should be 0")
}

func TestCircuitBreaker_Getters(t *testing.T) {
	cb := NewCircuitBreaker(5, 10*time.Second)

	state := cb.State()
	assert.Equal(t, CircuitStateClosed, state, "State() should return closed")

	failures := cb.Failures()
	assert.Equal(t, 0, failures, "Failures() should return 0")
}

func TestCircuitBreaker_Execute_ContextCancellation(t *testing.T) {
	cb := NewCircuitBreaker(3, 10*time.Second)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	err := cb.Execute(ctx, func() error {
		return nil
	})

	assert.ErrorIs(t, err, context.Canceled, "Execute() should return context.Canceled")
}

func TestCircuitBreaker_Execute_ContextTimeout(t *testing.T) {
	cb := NewCircuitBreaker(3, 10*time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer cancel()

	// Wait for context to expire
	time.Sleep(10 * time.Millisecond)

	err := cb.Execute(ctx, func() error {
		return nil
	})

	assert.ErrorIs(t, err, context.DeadlineExceeded, "Execute() should return context.DeadlineExceeded")
}

func TestCircuitBreaker_HalfOpen_MaxAttempts(t *testing.T) {
	cb := NewCircuitBreaker(2, 50*time.Millisecond)

	// Open the circuit
	cb.recordResult(errors.New("failure"))
	cb.recordResult(errors.New("failure"))

	// Wait for timeout to enter half-open
	time.Sleep(60 * time.Millisecond)

	// First call transitions from Open to HalfOpen
	// In HalfOpen state, halfOpenAttempts is set to 1 and returns true
	allowed := cb.allowRequest()
	assert.True(t, allowed, "First request should transition from Open to HalfOpen")

	// Now we're in HalfOpen with halfOpenAttempts=1
	// halfOpenMax=3, so we can make 2 more attempts (1 < 3, 2 < 3, but 3 < 3 is false)
	allowed = cb.allowRequest()
	assert.True(t, allowed, "Second request should be allowed in half-open (attempt 2)")

	allowed = cb.allowRequest()
	assert.True(t, allowed, "Third request should be allowed in half-open (attempt 3)")

	// Fourth request should be blocked (halfOpenAttempts would be 4, which is >= halfOpenMax)
	allowed = cb.allowRequest()
	assert.False(t, allowed, "Fourth request should be blocked after max half-open attempts")
}

func TestCircuitBreaker_HalfOpen_FailureReopens(t *testing.T) {
	cb := NewCircuitBreaker(2, 50*time.Millisecond)

	// Open the circuit
	cb.recordResult(errors.New("failure"))
	cb.recordResult(errors.New("failure"))

	// Wait for timeout to enter half-open
	time.Sleep(60 * time.Millisecond)
	cb.allowRequest()

	assert.Equal(t, CircuitStateHalfOpen, cb.state, "State should be half-open")

	// Record a failure in half-open state
	cb.recordResult(errors.New("failure"))

	assert.Equal(t, CircuitStateOpen, cb.state, "State should be open after failure in half-open")
}

func TestCircuitBreaker_ConcurrentAccess(t *testing.T) {
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

	// All operations should succeed (no circuit open errors)
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

	// Should have recorded all failures
	assert.GreaterOrEqual(t, cb.failures, 10, "Failures should be at least maxFailures")
	assert.Equal(t, CircuitStateOpen, cb.state, "State should be open after concurrent failures")
}

func TestCircuitBreaker_ResetClearsAllState(t *testing.T) {
	cb := NewCircuitBreaker(2, 10*time.Second)

	// Open the circuit
	cb.recordResult(errors.New("failure"))
	cb.recordResult(errors.New("failure"))
	cb.recordResult(errors.New("failure"))

	// Wait and enter half-open
	time.Sleep(60 * time.Millisecond)
	cb.allowRequest()

	// Record some successes
	cb.recordResult(nil)
	cb.recordResult(nil)

	// Now reset
	cb.Reset()

	assert.Equal(t, CircuitStateClosed, cb.state, "State after Reset() should be closed")
	assert.Equal(t, 0, cb.failures, "Failures after Reset() should be 0")
	assert.Equal(t, 0, cb.successes, "Successes after Reset() should be 0")
	assert.Equal(t, 0, cb.halfOpenAttempts, "HalfOpenAttempts after Reset() should be 0")
}

func TestCircuitBreaker_SuccessInClosedResetsFailures(t *testing.T) {
	cb := NewCircuitBreaker(5, 10*time.Second)

	// Record some failures
	cb.recordResult(errors.New("failure"))
	cb.recordResult(errors.New("failure"))
	assert.Equal(t, 2, cb.failures, "Failures should be 2")

	// Record success
	cb.recordResult(nil)
	assert.Equal(t, 0, cb.failures, "Failures should be reset to 0 after success in closed state")
}

func TestCircuitBreaker_MultipleTransitions(t *testing.T) {
	cb := NewCircuitBreaker(2, 30*time.Millisecond)

	// First cycle: closed -> open
	cb.recordResult(errors.New("failure"))
	cb.recordResult(errors.New("failure"))
	assert.Equal(t, CircuitStateOpen, cb.state, "First transition: should be open")

	// Wait for timeout
	time.Sleep(40 * time.Millisecond)

	// Open -> half-open
	allowed := cb.allowRequest()
	assert.True(t, allowed, "Second transition: should allow in half-open")
	assert.Equal(t, CircuitStateHalfOpen, cb.state, "Second transition: should be half-open")

	// Half-open -> closed (3 successes)
	for i := 0; i < 3; i++ {
		cb.recordResult(nil)
	}
	assert.Equal(t, CircuitStateClosed, cb.state, "Third transition: should be closed")

	// Open again
	cb.recordResult(errors.New("failure"))
	cb.recordResult(errors.New("failure"))
	assert.Equal(t, CircuitStateOpen, cb.state, "Fourth transition: should be open again")
}

func TestCircuitBreaker_TimeoutNotReached(t *testing.T) {
	cb := NewCircuitBreaker(2, 1*time.Second)

	// Open the circuit
	cb.recordResult(errors.New("failure"))
	cb.recordResult(errors.New("failure"))

	// Don't wait - should still be open
	allowed := cb.allowRequest()
	assert.False(t, allowed, "Request should be blocked before timeout")
	assert.Equal(t, CircuitStateOpen, cb.state, "State should remain open before timeout")
}

func TestCircuitBreaker_StateString(t *testing.T) {
	tests := []struct {
		state    CircuitState
		expected int
	}{
		{CircuitStateClosed, 0},
		{CircuitStateOpen, 1},
		{CircuitStateHalfOpen, 2},
	}

	for _, tt := range tests {
		assert.Equal(t, tt.expected, int(tt.state), "CircuitState value")
	}
}
