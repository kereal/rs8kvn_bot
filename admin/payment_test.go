package admin

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/kereal/rs8kvn_bot/internal/database"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAPI_PaymentConfig(t *testing.T) {
	t.Parallel()

	t.Run("stars mode reports XTR only", func(t *testing.T) {
		t.Parallel()

		srv, _ := newTestServer(t)
		srv.SetPaymentConfig(PaymentConfig{Provider: database.PaymentProviderTelegramStars, Enabled: true})

		rec := do(t, srv, http.MethodGet, "/admin/api/payment/config", testToken, "")
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

		var payload paymentConfigResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
		assert.Equal(t, database.PaymentProviderTelegramStars, payload.Provider)
		assert.True(t, payload.Enabled)
		assert.Equal(t, []string{"XTR"}, payload.SupportedCurrencies)
	})

	t.Run("platega mode rejects stars currency", func(t *testing.T) {
		t.Parallel()

		srv, _ := newTestServer(t)
		srv.SetPaymentConfig(PaymentConfig{Provider: database.PaymentProviderPlatega, Enabled: true})

		rec := do(t, srv, http.MethodGet, "/admin/api/payment/config", testToken, "")
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

		var payload paymentConfigResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
		assert.Equal(t, database.PaymentProviderPlatega, payload.Provider)
		assert.Contains(t, payload.SupportedCurrencies, "RUB")
		assert.NotContains(t, payload.SupportedCurrencies, "XTR")
	})

	t.Run("unset mode is safe and empty", func(t *testing.T) {
		t.Parallel()

		srv, _ := newTestServer(t)

		rec := do(t, srv, http.MethodGet, "/admin/api/payment/config", testToken, "")
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

		var payload paymentConfigResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
		assert.False(t, payload.Enabled)
		assert.Empty(t, payload.SupportedCurrencies)
		// Секретов в ответе нет ни в каком режиме.
		assert.NotContains(t, rec.Body.String(), "token")
		assert.NotContains(t, rec.Body.String(), "secret")
	})

	t.Run("requires token", func(t *testing.T) {
		t.Parallel()

		srv, _ := newTestServer(t)

		rec := do(t, srv, http.MethodGet, "/admin/api/payment/config", "", "")
		assert.Equal(t, http.StatusUnauthorized, rec.Code)
	})
}
