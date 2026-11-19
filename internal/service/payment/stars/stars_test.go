package stars

import (
	"context"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// fakeAPI — заглушка MakeRequester, фиксирует endpoint/params и возвращает ответ.
type fakeAPI struct {
	endpoint string
	params   tgbotapi.Params

	resp *tgbotapi.APIResponse
	err  error

	calls int
}

func (f *fakeAPI) MakeRequest(endpoint string, params tgbotapi.Params) (*tgbotapi.APIResponse, error) {
	f.calls++
	f.endpoint = endpoint
	f.params = params
	return f.resp, f.err
}

// tgError имитирует tgbotapi.Error (value receiver, как в v5.5.1).
type tgError tgbotapi.Error

func (e tgError) Error() string { return e.Message }

func TestBuildInvoiceValid(t *testing.T) {
	cfg, err := BuildInvoice(InvoiceParams{
		OrderID:  42,
		ChatID:   100200,
		Title:    "Подписка на месяц",
		Amount:   199,
		Currency: Currency,
	})
	if err != nil {
		t.Fatalf("BuildInvoice() err = %v, хотим nil", err)
	}

	if cfg.ChatID != 100200 {
		t.Errorf("ChatID = %d, хотим 100200", cfg.ChatID)
	}
	if cfg.Title != "Подписка на месяц" {
		t.Errorf("Title = %q", cfg.Title)
	}
	if cfg.Description != cfg.Title {
		t.Errorf("Description = %q, хотим Title %q", cfg.Description, cfg.Title)
	}
	if cfg.Payload != "42" {
		t.Errorf("Payload = %q, хотим \"42\"", cfg.Payload)
	}
	if cfg.StartParameter != "stars-42" {
		t.Errorf("StartParameter = %q, хотим \"stars-42\"", cfg.StartParameter)
	}
	if cfg.ProviderToken != "" {
		t.Errorf("ProviderToken = %q, хотим пустой", cfg.ProviderToken)
	}
	if cfg.Currency != Currency {
		t.Errorf("Currency = %q, хотим %q", cfg.Currency, Currency)
	}
	if len(cfg.Prices) != 1 {
		t.Fatalf("len(Prices) = %d, хотим 1", len(cfg.Prices))
	}
	if cfg.Prices[0].Label != cfg.Title {
		t.Errorf("Prices[0].Label = %q, хотим Title %q", cfg.Prices[0].Label, cfg.Title)
	}
	if cfg.Prices[0].Amount != 199 {
		t.Errorf("Prices[0].Amount = %d, хотим 199", cfg.Prices[0].Amount)
	}
	if cfg.IsFlexible {
		t.Error("IsFlexible = true, хотим false")
	}
	if cfg.MaxTipAmount != 0 || len(cfg.SuggestedTipAmounts) != 0 {
		t.Errorf("чаевые заданы: MaxTipAmount=%d, SuggestedTipAmounts=%v", cfg.MaxTipAmount, cfg.SuggestedTipAmounts)
	}
}

func TestBuildInvoiceWrongCurrency(t *testing.T) {
	_, err := BuildInvoice(InvoiceParams{OrderID: 1, Title: "T", Amount: 10, Currency: "RUB"})
	if err == nil {
		t.Fatal("BuildInvoice(RUB) err = nil, хотим ошибку")
	}
}

func TestBuildInvoiceNonPositiveAmount(t *testing.T) {
	for _, amount := range []int64{0, -5} {
		_, err := BuildInvoice(InvoiceParams{OrderID: 1, Title: "T", Amount: amount, Currency: Currency})
		if err == nil {
			t.Fatalf("BuildInvoice(Amount=%d) err = nil, хотим ошибку", amount)
		}
	}
}

func TestBuildInvoiceTitleExactly32Runes(t *testing.T) {
	title := strings.Repeat("ж", 32)
	cfg, err := BuildInvoice(InvoiceParams{OrderID: 1, Title: title, Amount: 1, Currency: Currency})
	if err != nil {
		t.Fatalf("BuildInvoice() err = %v", err)
	}
	if got := utf8.RuneCountInString(cfg.Title); got != 32 {
		t.Errorf("len(Title) = %d runes, хотим 32", got)
	}
	if cfg.Title != title {
		t.Errorf("Title = %q, хотим без изменений %q", cfg.Title, title)
	}
}

func TestBuildInvoiceTitleTruncatedWithoutEllipsis(t *testing.T) {
	title := strings.Repeat("aб", 40) // 80 runes
	cfg, err := BuildInvoice(InvoiceParams{OrderID: 1, Title: title, Amount: 1, Currency: Currency})
	if err != nil {
		t.Fatalf("BuildInvoice() err = %v", err)
	}
	if got := utf8.RuneCountInString(cfg.Title); got != 32 {
		t.Fatalf("len(Title) = %d runes, хотим 32", got)
	}
	if strings.Contains(cfg.Title, "...") {
		t.Errorf("Title = %q, содержит многоточие", cfg.Title)
	}
	if cfg.Title != string([]rune(title)[:32]) {
		t.Errorf("Title = %q, хотим первые 32 runes", cfg.Title)
	}
}

func TestRefundSuccess(t *testing.T) {
	api := &fakeAPI{resp: &tgbotapi.APIResponse{Ok: true}}
	c := New(api)

	err := c.Refund(context.Background(), 555000111, "chg_123")
	if err != nil {
		t.Fatalf("Refund() err = %v, хотим nil", err)
	}
	if api.calls != 1 {
		t.Fatalf("calls = %d, хотим 1", api.calls)
	}
	if api.endpoint != "refundStarPayment" {
		t.Errorf("endpoint = %q, хотим refundStarPayment", api.endpoint)
	}
	if got := api.params["user_id"]; got != "555000111" {
		t.Errorf("user_id = %q, хотим 555000111", got)
	}
	if got := api.params["telegram_payment_charge_id"]; got != "chg_123" {
		t.Errorf("telegram_payment_charge_id = %q, хотим chg_123", got)
	}
}

func TestRefundAlreadyRefunded(t *testing.T) {
	api := &fakeAPI{
		resp: &tgbotapi.APIResponse{Ok: false, ErrorCode: 400, Description: "Bad Request: CHARGE_ALREADY_REFUNDED"},
		err:  tgError{Code: 400, Message: "Bad Request: CHARGE_ALREADY_REFUNDED"},
	}
	c := New(api)

	err := c.Refund(context.Background(), 1, "chg_123")
	if !errors.Is(err, ErrAlreadyRefunded) {
		t.Fatalf("Refund() err = %v, хотим ErrAlreadyRefunded", err)
	}
}

func TestRefundOtherTelegramError(t *testing.T) {
	api := &fakeAPI{
		resp: &tgbotapi.APIResponse{Ok: false, ErrorCode: 400, Description: "Bad Request: CHARGE_NOT_FOUND"},
		err:  tgError{Code: 400, Message: "Bad Request: CHARGE_NOT_FOUND"},
	}
	c := New(api)

	err := c.Refund(context.Background(), 1, "chg_404")
	if err == nil {
		t.Fatal("Refund() err = nil, хотим ошибку")
	}
	if errors.Is(err, ErrAlreadyRefunded) {
		t.Fatalf("Refund() err = %v, не должен быть ErrAlreadyRefunded", err)
	}
}

func TestRefundContextCancelled(t *testing.T) {
	api := &fakeAPI{}
	c := New(api)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := c.Refund(ctx, 1, "chg_123")
	if err == nil {
		t.Fatal("Refund() err = nil, хотим ошибку отменённого ctx")
	}
	if api.calls != 0 {
		t.Fatalf("calls = %d, хотим 0 (HTTP не вызывается при отменённом ctx)", api.calls)
	}
}

func TestRefundAlreadyRefundedLowercaseDescription(t *testing.T) {
	api := &fakeAPI{
		resp: &tgbotapi.APIResponse{Ok: false, Description: "Bad Request: charge already refunded"},
		err:  tgError{Code: 400, Message: "Bad Request: charge already refunded"},
	}
	c := New(api)

	err := c.Refund(context.Background(), 1, "chg_123")
	if !errors.Is(err, ErrAlreadyRefunded) {
		t.Fatalf("Refund() err = %v, хотим ErrAlreadyRefunded", err)
	}
}

func TestRefundDescriptionOnlyDetection(t *testing.T) {
	// Ошибка без текста already refunded, но Description в ответе содержит его
	// (MakeRequest при ok=false возвращает и ответ, и ошибку).
	api := &fakeAPI{
		resp: &tgbotapi.APIResponse{Ok: false, Description: "Bad Request: CHARGE_ALREADY_REFUNDED"},
		err:  tgError{Code: 400, Message: "Bad Request: CHARGE_ALREADY_REFUNDED"},
	}
	c := New(api)

	err := c.Refund(context.Background(), 1, "chg_123")
	if !errors.Is(err, ErrAlreadyRefunded) {
		t.Fatalf("Refund() err = %v, хотим ErrAlreadyRefunded", err)
	}
}
