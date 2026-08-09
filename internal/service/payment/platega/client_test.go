package platega

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

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
		_, _ = w.Write([]byte(`{"transactionId":"tx-7","url":"https://pay.test/7","status":"PENDING"}`))
	}))
	defer server.Close()

	got, err := New(Config{MerchantID: "merchant", Secret: "secret", BaseURL: server.URL}).CreateTransaction(context.Background(), CreateTransactionRequest{
		AmountCents: 23005, Currency: "RUB", Description: "Premium", Payload: "order-7", UserID: "123", UserName: "@user",
	})
	require.NoError(t, err)
	require.Equal(t, "tx-7", got.TransactionID)
	require.Equal(t, "https://pay.test/7", got.URL)
}

func TestCreateTransactionAcceptsRedirect(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"transactionId":"tx-8","redirect":"https://pay.test/8"}`))
	}))
	defer server.Close()

	got, err := New(Config{BaseURL: server.URL}).CreateTransaction(context.Background(), CreateTransactionRequest{AmountCents: 1, Currency: "RUB"})
	require.NoError(t, err)
	require.Equal(t, "https://pay.test/8", got.Redirect)
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
