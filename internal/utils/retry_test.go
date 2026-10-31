package utils

import (
	"errors"
	"fmt"
	"net"
	"testing"
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
		{"plain error", errors.New("boom"), false},
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
