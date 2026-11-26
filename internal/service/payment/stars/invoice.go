package stars

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// maxTitleLen — лимит Telegram на title/description invoice (в runes).
const maxTitleLen = 32

// defaultTitle — fallback-title, когда название товара пустое (Telegram требует
// title длиной 1..32 символа).
const defaultTitle = "Subscription"

// InvoiceParams — параметры для построения Stars-invoice.
type InvoiceParams struct {
	OrderID  uint
	ChatID   int64
	Title    string
	Amount   int64 // Stars, minor units (1 Star = 1 minor unit)
	Currency string
}

// BuildInvoice строит fail-closed invoice для Telegram Stars, без БД.
// Ренты в Stars не отправляются: валюта обязана быть XTR, Amount > 0.
func BuildInvoice(p InvoiceParams) (tgbotapi.InvoiceConfig, error) {
	if p.Currency != Currency {
		return tgbotapi.InvoiceConfig{}, fmt.Errorf("stars: валюта %q не поддерживается, ожидалась %q", p.Currency, Currency)
	}
	if p.Amount <= 0 {
		return tgbotapi.InvoiceConfig{}, fmt.Errorf("stars: некорректная сумма %d, требуется > 0", p.Amount)
	}
	if p.OrderID == 0 {
		return tgbotapi.InvoiceConfig{}, errors.New("stars: пустой OrderID")
	}

	title := normalizeTitle(p.Title)

	payload := strconv.FormatUint(uint64(p.OrderID), 10)

	return tgbotapi.InvoiceConfig{
		BaseChat: tgbotapi.BaseChat{
			ChatID: p.ChatID,
		},
		Title:          title,
		Description:    title,
		Payload:        payload,
		ProviderToken:  "",
		Currency:       Currency,
		Prices:         []tgbotapi.LabeledPrice{{Label: title, Amount: int(p.Amount)}},
		StartParameter: "stars-" + payload,
		IsFlexible:     false,
	}, nil
}

// normalizeTitle приводит title к требованиям Telegram (1..32 рун):
// пробелы по краям срезаются, пустой title заменяется дефолтом, слишком длинный
// обрезается до 32 рун (без ellipsis — Telegram сам отклоняет title > 32 символов).
func normalizeTitle(title string) string {
	title = strings.TrimSpace(title)
	if title == "" {
		return defaultTitle
	}

	if utf8.RuneCountInString(title) > maxTitleLen {
		runes := []rune(title)
		title = string(runes[:maxTitleLen])
	}

	return title
}
