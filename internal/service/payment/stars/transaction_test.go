package stars

import (
	"context"
	"encoding/json"
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func TestGetTransactionsParsesIncomingInvoicePayment(t *testing.T) {
	api := &fakeAPI{resp: &tgbotapi.APIResponse{
		Ok: true,
		Result: json.RawMessage(`{
			"transactions": [
				{
					"id": "chg_123",
					"amount": 199,
					"date": 1763600000,
					"source": {
						"type": "user",
						"transaction_type": "invoice_payment",
						"invoice_payload": "42",
						"user": {"id": 555000111, "is_bot": false, "first_name": "Buyer"}
					}
				}
			]
		}`),
	}}
	client := New(api)

	transactions, err := client.GetTransactions(context.Background(), 5, 100)
	if err != nil {
		t.Fatalf("GetTransactions() error = %v", err)
	}
	if api.endpoint != "getStarTransactions" {
		t.Fatalf("endpoint = %q", api.endpoint)
	}
	if api.params["offset"] != "5" || api.params["limit"] != "100" {
		t.Fatalf("params = %#v", api.params)
	}
	if len(transactions.Transactions) != 1 {
		t.Fatalf("transactions count = %d", len(transactions.Transactions))
	}

	transaction := transactions.Transactions[0]
	if transaction.ID != "chg_123" || transaction.Amount != 199 {
		t.Fatalf("transaction = %#v", transaction)
	}
	if !transaction.IsIncomingInvoicePayment() {
		t.Fatal("invoice payment transaction must be recognized")
	}
	if transaction.Source.InvoicePayload != "42" || transaction.Source.User.ID != 555000111 {
		t.Fatalf("source = %#v", transaction.Source)
	}
}

func TestGetTransactionsIgnoresOutgoingRefund(t *testing.T) {
	api := &fakeAPI{resp: &tgbotapi.APIResponse{
		Ok: true,
		Result: json.RawMessage(`{
			"transactions": [{
				"id": "chg_refund",
				"amount": 199,
				"receiver": {
					"type": "user",
					"transaction_type": "refund",
					"user": {"id": 555000111}
				}
			}]
		}`),
	}}
	client := New(api)

	transactions, err := client.GetTransactions(context.Background(), 0, 100)
	if err != nil {
		t.Fatalf("GetTransactions() error = %v", err)
	}
	if len(transactions.Transactions) != 1 {
		t.Fatalf("transactions count = %d", len(transactions.Transactions))
	}
	if transactions.Transactions[0].IsIncomingInvoicePayment() {
		t.Fatal("outgoing refund must not be treated as an incoming payment")
	}
}

func TestGetTransactionsHonorsCancelledContext(t *testing.T) {
	api := &fakeAPI{}
	client := New(api)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := client.GetTransactions(ctx, 0, 100)
	if err == nil {
		t.Fatal("GetTransactions() error = nil")
	}
	if api.calls != 0 {
		t.Fatalf("calls = %d, want 0", api.calls)
	}
}
