package platega

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCreateTransactionContract(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/v2/transaction/process", r.URL.Path)
		require.Equal(t, "merchant", r.Header.Get("X-MerchantId"))
		require.Equal(t, "secret", r.Header.Get("X-Secret"))
		require.Equal(t, "application/json", r.Header.Get("Content-Type"))

		var body struct {
			PaymentDetails struct {
				Amount   json.RawMessage `json:"amount"`
				Currency string          `json:"currency"`
			} `json:"paymentDetails"`
			Payload  string `json:"payload"`
			Metadata struct {
				UserID   string `json:"userId"`
				UserName string `json:"userName"`
			} `json:"metadata"`
			PaymentMethod json.RawMessage `json:"paymentMethod"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		require.Nil(t, body.PaymentMethod)
		require.Equal(t, json.RawMessage(`230.05`), body.PaymentDetails.Amount)
		require.Equal(t, "RUB", body.PaymentDetails.Currency)
		require.Equal(t, "order-7", body.Payload)
		require.Equal(t, "123", body.Metadata.UserID)
		require.Equal(t, "@user", body.Metadata.UserName)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"transactionId":"550e8400-e29b-41d4-a716-446655440007","url":"https://pay.test/7","status":"PENDING","expiresIn":"00:15:00"}`))
	}))
	defer server.Close()

	got, err := New(Config{MerchantID: "merchant", Secret: "secret", BaseURL: server.URL}).CreateTransaction(context.Background(), CreateTransactionRequest{
		AmountCents: 23005, Currency: "RUB", Description: "Premium", Payload: "order-7", UserID: "123", UserName: "@user",
	})
	require.NoError(t, err)
	require.Equal(t, "550e8400-e29b-41d4-a716-446655440007", got.TransactionID)
	require.Equal(t, "https://pay.test/7", got.URL)
}

func TestCreateTransactionAcceptsRedirect(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"transactionId":"550e8400-e29b-41d4-a716-446655440008","redirect":"https://pay.test/8","expiresIn":"00:15:00"}`))
	}))
	defer server.Close()

	got, err := New(Config{BaseURL: server.URL}).CreateTransaction(context.Background(), CreateTransactionRequest{AmountCents: 1, Currency: "RUB"})
	require.NoError(t, err)
	require.Equal(t, "https://pay.test/8", got.Redirect)
}

func TestParseExpiresIn(t *testing.T) {
	for _, tt := range []struct {
		name    string
		raw     string
		want    time.Duration
		wantErr bool
	}{
		{name: "valid", raw: "00:15:00", want: 15 * time.Minute},
		{name: "missing", wantErr: true},
		{name: "zero", raw: "00:00:00", wantErr: true},
		{name: "invalid", raw: "15 minutes", wantErr: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseExpiresIn(tt.raw)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestCreateTransactionRejectsProviderErrors(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "failure", status) }))
			defer server.Close()
			_, err := New(Config{BaseURL: server.URL}).CreateTransaction(context.Background(), CreateTransactionRequest{AmountCents: 100, Currency: "RUB"})
			require.Error(t, err)
		})
	}
}
