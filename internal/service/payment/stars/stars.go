package stars

import (
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// Currency — единственная валюта Stars-платежей (1 Star = 1 minor unit).
const Currency = "XTR"

// MakeRequester — единственный метод, нужный для refundStarPayment:
// в tgbotapi v5.5.1 нет typed-хелпера (MakeRequest без ctx).
type MakeRequester interface {
	MakeRequest(endpoint string, params tgbotapi.Params) (*tgbotapi.APIResponse, error)
}

// Client выполняет прямые вызовы Telegram API для Stars.
type Client struct {
	api MakeRequester
}

// New создаёт Stars-клиент поверх существующего BotAPI.
func New(api MakeRequester) *Client {
	return &Client{api: api}
}
