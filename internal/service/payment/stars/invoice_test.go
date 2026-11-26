package stars

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// TestBuildInvoiceTitleTrimmed — пробелы по краям title срезаются (Telegram
// требует 1..32 символа, пробелы не считаются содержательным названием).
func TestBuildInvoiceTitleTrimmed(t *testing.T) {
	cfg, err := BuildInvoice(InvoiceParams{OrderID: 1, Title: "  Месяц  ", Amount: 1, Currency: Currency})
	if err != nil {
		t.Fatalf("BuildInvoice() err = %v", err)
	}
	if cfg.Title != "Месяц" {
		t.Errorf("Title = %q, хотим %q", cfg.Title, "Месяц")
	}
	if cfg.Description != "Месяц" {
		t.Errorf("Description = %q, хотим %q", cfg.Description, "Месяц")
	}
	if len(cfg.Prices) != 1 || cfg.Prices[0].Label != "Месяц" {
		t.Errorf("Prices = %+v, хотим label %q", cfg.Prices, "Месяц")
	}
}

// TestBuildInvoiceTitleEmptyFallback — пустой (или из одних пробелов) title
// заменяется дефолтом, иначе Telegram отклонит invoice.
func TestBuildInvoiceTitleEmptyFallback(t *testing.T) {
	for _, title := range []string{"", "   ", "\t\n"} {
		cfg, err := BuildInvoice(InvoiceParams{OrderID: 1, Title: title, Amount: 1, Currency: Currency})
		if err != nil {
			t.Fatalf("BuildInvoice(title=%q) err = %v", title, err)
		}
		if cfg.Title != defaultTitle {
			t.Errorf("Title = %q, хотим %q", cfg.Title, defaultTitle)
		}
		if got := utf8.RuneCountInString(cfg.Title); got < 1 || got > maxTitleLen {
			t.Errorf("len(Title) = %d runes, вне диапазона 1..%d", got, maxTitleLen)
		}
	}
}

// TestBuildInvoiceTitleTooLongTrimmed — title длиннее 32 рун обрезается до 32.
func TestBuildInvoiceTitleTooLongTrimmed(t *testing.T) {
	title := "  " + strings.Repeat("я", 40) + "  "
	cfg, err := BuildInvoice(InvoiceParams{OrderID: 1, Title: title, Amount: 1, Currency: Currency})
	if err != nil {
		t.Fatalf("BuildInvoice() err = %v", err)
	}
	if got := utf8.RuneCountInString(cfg.Title); got != maxTitleLen {
		t.Fatalf("len(Title) = %d runes, хотим %d", got, maxTitleLen)
	}
	if cfg.Title != strings.Repeat("я", maxTitleLen) {
		t.Errorf("Title = %q, хотим %q", cfg.Title, strings.Repeat("я", maxTitleLen))
	}
}
