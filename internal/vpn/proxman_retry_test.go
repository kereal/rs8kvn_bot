package vpn

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Поведение: сетевой сбой proxman (обрыв соединения) ретраится с бэкоффом,
// клиент в итоге получает успешный результат. События идемпотентны (EventID),
// поэтому повтор безопасен.
func TestProxmanClient_RetriesNetworkFailures(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) < 3 {
			// обрываем соединение без ответа — сетевая ошибка на клиенте
			hj, ok := w.(http.Hijacker)
			require.True(t, ok)
			conn, _, err := hj.Hijack()
			require.NoError(t, err)
			_ = conn.Close()
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	client := NewProxmanClient(srv.URL, "token")
	client.retryMax = 3
	client.retryDelay = time.Millisecond

	err := client.CreateSubscription(context.Background(), SubscriptionProvision{
		ClientID: "uuid", Username: "user", SubID: "sub",
	})
	require.NoError(t, err)
	assert.Equal(t, int32(3), attempts.Load())
}

// Не-сетевые ошибки (не-200 ответ) не ретраятся: один запрос — один отказ.
func TestProxmanClient_DoesNotRetryHTTPStatusErrors(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("bad"))
	}))
	defer srv.Close()

	client := NewProxmanClient(srv.URL, "token")
	client.retryMax = 3
	client.retryDelay = time.Millisecond

	err := client.CreateSubscription(context.Background(), SubscriptionProvision{
		ClientID: "uuid", Username: "user", SubID: "sub",
	})
	require.Error(t, err)
	assert.Equal(t, int32(1), attempts.Load())
}
