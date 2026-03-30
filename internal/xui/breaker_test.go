package xui

import (
	"context"
	"errors"
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
