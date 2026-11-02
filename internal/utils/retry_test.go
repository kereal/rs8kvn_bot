package utils

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// IsRetryable: ретраятся только сетевые ошибки. Ошибки «сервер ответил» —
// в том числе не-200 от панели 3x-ui (sentinel ErrNon200Response, владелец —
// именно этот пакет, так как internal/xui импортирует utils; обратной
// зависимости нет) — НЕ ретраятся. Это текущее решение о ретраях: если
// панель ответила не-200, повтор запроса бессмысленен.
func TestIsRetryable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		err       error
		retryable bool
	}{
		{"nil", nil, false},
		{"non-200 sentinel", ErrNon200Response, false},
		{"wrapped non-200", fmt.Errorf("upstream returned non-200: %w", ErrNon200Response), false},
		{"dns error", &net.DNSError{Err: "dns", Name: "example.com"}, false},
		{"net timeout", &net.OpError{Op: "read", Err: fmt.Errorf("i/o timeout")}, true},
		{"connection refused", &net.OpError{Op: "dial", Net: "tcp", Err: fmt.Errorf("connection refused")}, true},
		{"no such host", fmt.Errorf("no such host"), false},
		{"temporary failure", fmt.Errorf("temporary failure in name resolution"), false},
		{"plain error", errors.New("boom"), false},
		{"context canceled", fmt.Errorf("post: %w", context.Canceled), false},
		{"context deadline", &url.Error{Op: "Get", URL: "http://x", Err: context.DeadlineExceeded}, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := IsRetryable(tc.err); got != tc.retryable {
				t.Fatalf("IsRetryable(%v) = %v, want %v", tc.err, got, tc.retryable)
			}
		})
	}
}

// testNetError — сетевая ошибка (Timeout=false, Temporary=true), которую
// IsRetryable признаёт ретраебельной.
type testNetError struct{}

func (testNetError) Error() string   { return "connection reset" }
func (testNetError) Timeout() bool   { return false }
func (testNetError) Temporary() bool { return true }

func TestRetryWithBackoff(t *testing.T) {
	t.Parallel()

	var (
		successRetryCalls, exhaustedCalls, non200Calls atomic.Int32
		cancelledCalls, nonRetryableCalls              atomic.Int32
	)

	tests := []struct {
		name         string
		setupCtx     func() context.Context
		fn           func() error
		wantErr      bool
		wantContains string
	}{
		{
			name: "success immediate",
			setupCtx: func() context.Context {
				return context.Background()
			},
			fn: func() error {
				return nil
			},
			wantErr: false,
		},
		{
			name: "retries then succeeds",
			setupCtx: func() context.Context {
				return context.Background()
			},
			fn: func() error {
				calls := successRetryCalls.Load()
				successRetryCalls.Add(1)

				if calls < 2 {
					return testNetError{}
				}

				return nil
			},
			wantErr: false,
		},
		{
			name: "exhausted retries",
			setupCtx: func() context.Context {
				return context.Background()
			},
			fn: func() error {
				exhaustedCalls.Add(1)
				return testNetError{}
			},
			wantErr:      true,
			wantContains: "connection reset",
		},
		{
			name: "context cancelled",
			setupCtx: func() context.Context {
				ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
				cancel()

				return ctx
			},
			fn: func() error {
				cancelledCalls.Add(1)
				return testNetError{}
			},
			wantErr:      true,
			wantContains: "context cancelled",
		},
		{
			name: "non-retryable error",
			setupCtx: func() context.Context {
				return context.Background()
			},
			fn: func() error {
				nonRetryableCalls.Add(1)
				return &net.DNSError{Err: "dns", Name: "example.com"}
			},
			wantErr: true,
		},
		{
			name: "non-200 not retried",
			setupCtx: func() context.Context {
				return context.Background()
			},
			fn: func() error {
				non200Calls.Add(1)
				return fmt.Errorf("upstream returned non-200: %w", ErrNon200Response)
			},
			wantErr:      true,
			wantContains: "upstream returned non-200",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := RetryWithBackoff(tt.setupCtx(), 3, time.Millisecond, tt.fn)

			if tt.wantErr {
				assert.Error(t, err)

				if tt.wantContains != "" {
					assert.Contains(t, err.Error(), tt.wantContains)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}

	assert.Equal(t, int32(3), successRetryCalls.Load(), "retries-then-succeeds call count")
	assert.Equal(t, int32(3), exhaustedCalls.Load(), "exhausted call count")
	assert.Equal(t, int32(1), cancelledCalls.Load(), "cancelled call count")
	assert.Equal(t, int32(1), nonRetryableCalls.Load(), "non-retryable call count")
	assert.Equal(t, int32(1), non200Calls.Load(), "non-200 call count")
}
