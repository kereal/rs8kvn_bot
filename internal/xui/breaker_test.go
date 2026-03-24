package xui

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestNewCircuitBreaker(t *testing.T) {
	cb := NewCircuitBreaker(3, 10*time.Second)

	if cb.state != CircuitStateClosed {
		t.Errorf("Initial state = %v, want closed", cb.state)
	}

	if cb.failures != 0 {
		t.Errorf("Initial failures = %d, want 0", cb.failures)
	}
}

func TestCircuitBreaker_AllowsRequestsWhenClosed(t *testing.T) {
	cb := NewCircuitBreaker(3, 10*time.Second)

	allowed := cb.allowRequest()
	if !allowed {
		t.Error("Expected request to be allowed when circuit is closed")
	}
}

func TestCircuitBreaker_OpensAfterMaxFailures(t *testing.T) {
	cb := NewCircuitBreaker(3, 10*time.Second)

	// Record 3 failures
	for i := 0; i < 3; i++ {
		cb.recordResult(errors.New("failure"))
	}

	if cb.state != CircuitStateOpen {
		t.Errorf("State after 3 failures = %v, want open", cb.state)
	}

	// Next request should be blocked
	allowed := cb.allowRequest()
	if allowed {
		t.Error("Expected request to be blocked when circuit is open")
	}
}

func TestCircuitBreaker_HalfOpenAfterTimeout(t *testing.T) {
	cb := NewCircuitBreaker(2, 50*time.Millisecond)

	// Record 2 failures to open the circuit
	cb.recordResult(errors.New("failure"))
	cb.recordResult(errors.New("failure"))

	if cb.state != CircuitStateOpen {
		t.Errorf("State = %v, want open", cb.state)
	}

	// Wait for timeout
	time.Sleep(60 * time.Millisecond)

	// Next request should be allowed (half-open)
	allowed := cb.allowRequest()
	if !allowed {
		t.Error("Expected request to be allowed when circuit is half-open after timeout")
	}

	if cb.state != CircuitStateHalfOpen {
		t.Errorf("State after timeout = %v, want half-open", cb.state)
	}
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

	if cb.state != CircuitStateClosed {
		t.Errorf("State after 3 successes = %v, want closed", cb.state)
	}
}

func TestCircuitBreaker_Execute_Success(t *testing.T) {
	cb := NewCircuitBreaker(3, 10*time.Second)

	err := cb.Execute(context.Background(), func() error {
		return nil
	})

	if err != nil {
		t.Errorf("Execute() error = %v, want nil", err)
	}

	if cb.failures != 0 {
		t.Errorf("Failures after success = %d, want 0", cb.failures)
	}
}

func TestCircuitBreaker_Execute_Failure(t *testing.T) {
	testErr := errors.New("test error")
	cb := NewCircuitBreaker(3, 10*time.Second)

	err := cb.Execute(context.Background(), func() error {
		return testErr
	})

	if err != testErr {
		t.Errorf("Execute() error = %v, want test error", err)
	}

	if cb.failures != 1 {
		t.Errorf("Failures after one failure = %d, want 1", cb.failures)
	}
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

	if !errors.Is(err, ErrCircuitOpen) {
		t.Errorf("Execute() error = %v, want ErrCircuitOpen", err)
	}
}

func TestCircuitBreaker_Reset(t *testing.T) {
	cb := NewCircuitBreaker(3, 10*time.Second)

	// Open the circuit
	cb.recordResult(errors.New("failure"))
	cb.recordResult(errors.New("failure"))
	cb.recordResult(errors.New("failure"))

	cb.Reset()

	if cb.state != CircuitStateClosed {
		t.Errorf("State after Reset() = %v, want closed", cb.state)
	}

	if cb.failures != 0 {
		t.Errorf("Failures after Reset() = %d, want 0", cb.failures)
	}
}

func TestCircuitBreaker_Getters(t *testing.T) {
	cb := NewCircuitBreaker(5, 10*time.Second)

	state := cb.State()
	if state != CircuitStateClosed {
		t.Errorf("State() = %v, want closed", state)
	}

	failures := cb.Failures()
	if failures != 0 {
		t.Errorf("Failures() = %d, want 0", failures)
	}
}
