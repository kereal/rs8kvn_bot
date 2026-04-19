package web

import (
	"context"
	"fmt"
	"sync"

	"go.uber.org/zap"

	"rs8kvn_bot/internal/logger"
)

// singleFlightCall represents an in-flight or completed single flight call.
type singleFlightCall struct {
	result interface{}
	err    error
	done   chan struct{}
}

// SingleFlight deduplicates concurrent calls with the same key.
// It ensures only one function execution for a given key at a time.
// Do waits for the first call to complete and returns its result.
// If ctx is cancelled before the function completes, Do returns ctx.Err()
// and the in-flight call continues in the background.
type SingleFlight struct {
	mu    sync.Mutex
	calls map[string]*singleFlightCall
}

// NewSingleFlight creates a new SingleFlight instance.
func NewSingleFlight() *SingleFlight {
	return &SingleFlight{
		calls: make(map[string]*singleFlightCall),
	}
}

// Do executes fn if no call with the same key is in-flight, otherwise waits
// for the ongoing call to complete. The provided context controls the wait:
// if cancelled, Do returns ctx.Err() immediately and does not wait for fn.
func (s *SingleFlight) Do(ctx context.Context, key string, fn func(ctx context.Context) (interface{}, error)) (interface{}, error) {
	s.mu.Lock()
	if existing, ok := s.calls[key]; ok {
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-existing.done:
			return existing.result, existing.err
		}
	}

	// Create a new in-flight call
	call := &singleFlightCall{
		done: make(chan struct{}),
	}
	s.calls[key] = call
	s.mu.Unlock()

	// Execute fn in a goroutine
	go func() {
		defer func() {
			if r := recover(); r != nil {
				// Convert panic to error
				call.err = fmt.Errorf("panic in singleflight function: %v", r)
				logger.Error("singleflight goroutine panic",
					zap.Any("panic", r),
					zap.String("key", key))
			}
			s.mu.Lock()
			delete(s.calls, key)
			s.mu.Unlock()
			close(call.done)
		}()
		call.result, call.err = fn(ctx)
	}()

	// Wait for completion or context cancellation
	select {
	case <-ctx.Done():
		// Do NOT delete s.calls[key] or close call.done here — the goroutine's
		// deferred cleanup is the sole owner of that mutation. Just bail out.
		return nil, ctx.Err()
	case <-call.done:
		return call.result, call.err
	}
}
