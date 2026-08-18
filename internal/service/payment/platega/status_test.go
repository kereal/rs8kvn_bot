package platega

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestGetTransactionStatusContract(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/transaction/550e8400-e29b-41d4-a716-446655440101", r.URL.Path)
		require.Equal(t, "merchant", r.Header.Get("X-Merchantid"))
		require.Equal(t, "secret", r.Header.Get("X-Secret"))

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id": "550e8400-e29b-41d4-a716-446655440101",
			"status": "CONFIRMED",
			"paymentDetails": {"amount": 5250, "currency": "RUB"},
			"merchantName": "Demo",
			"mechantId": "550e8400-e29b-41d4-a716-446655440101",
			"comission": 4.5,
			"paymentMethod": "SBPQR",
			"comissionUsdt": 1.64044944,
			"amountUsdt": 10.8988764,
			"externalId": "ext-1",
			"description": "Оплата заказа #1"
		}`))
	}))
	defer server.Close()

	got, err := New(Config{MerchantID: "merchant", Secret: "secret", BaseURL: server.URL}).GetTransactionStatus(context.Background(), uuid.MustParse("550e8400-e29b-41d4-a716-446655440101"))
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, "CONFIRMED", got.Status)
	require.Equal(t, "RUB", got.PaymentDetails.Currency)
	require.Equal(t, "4.5", got.Commission.String())
	require.Equal(t, "1.64044944", got.CommissionUsdt.String())

	fee, err := got.CommissionCents()
	require.NoError(t, err)
	require.Equal(t, int64(450), fee)
}

func TestGetTransactionStatusRejectsErrors(t *testing.T) {
	t.Parallel()

	for _, status := range []int{http.StatusUnauthorized, http.StatusNotFound, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "failure", status) }))
			defer server.Close()

			_, err := New(Config{BaseURL: server.URL}).GetTransactionStatus(context.Background(), uuid.MustParse("550e8400-e29b-41d4-a716-446655440102"))
			require.Error(t, err)
		})
	}
}

func TestGetTransactionStatusRejectsNilID(t *testing.T) {
	t.Parallel()

	_, err := New(Config{}).GetTransactionStatus(context.Background(), uuid.Nil)
	require.Error(t, err)
}

func TestCommissionCents(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		raw     string
		want    int64
		wantErr bool
	}{
		{raw: "0", want: 0},
		{raw: "4.5", want: 450},
		{raw: "5.00", want: 500},
		{raw: "1.64044944", want: 164},
		{raw: "52.5000000000000000", want: 5250},
		{raw: "", wantErr: true},
		{raw: "abc", wantErr: true},
	} {
		t.Run(tt.raw, func(t *testing.T) {
			resp := &TransactionStatusResponse{Commission: json.Number(tt.raw)}
			got, err := resp.CommissionCents()
			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}
