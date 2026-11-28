package admin

import (
	"net/http"

	"github.com/kereal/rs8kvn_bot/internal/database"
)

// PaymentConfig — снимок активного платёжного режима. Источник истины тот же,
// что у бота: config.Config (PAYMENT_ENABLED/PAYMENT_PROVIDER). Панель его
// только читает: SetPaymentConfig вызывается интегратором при старте, второго
// хранилища настроек нет.
type PaymentConfig struct {
	Provider string
	Enabled  bool
}

// paymentConfigResponse — только безопасные поля: ни ключей, ни секретов
// webhook'а, ни credentials Stars.
type paymentConfigResponse struct {
	Provider            string   `json:"provider"`
	Enabled             bool     `json:"enabled"`
	SupportedCurrencies []string `json:"supported_currencies"`
}

// providerSupportedCurrencies — доменное правило провайдера, а не настройка:
// Telegram Stars принимает только XTR (order_stars.go), Platega — фиат
// (order_platega.go отклоняет XTR). Панель по этому списку предупреждает о
// несовместимой валюте продукта, но не блокирует создание.
var providerSupportedCurrencies = map[string][]string{
	database.PaymentProviderTelegramStars: {"XTR"},
	database.PaymentProviderPlatega:       {"RUB", "USD", "EUR"},
}

func (s *Server) handlePaymentConfig(w http.ResponseWriter, _ *http.Request) {
	currencies := providerSupportedCurrencies[s.payment.Provider]
	// Пустой список отдаём как [], а не null: панель итерируется по нему.
	if currencies == nil {
		currencies = []string{}
	}

	writeJSON(w, http.StatusOK, paymentConfigResponse{
		Provider:            s.payment.Provider,
		Enabled:             s.payment.Enabled,
		SupportedCurrencies: currencies,
	})
}
