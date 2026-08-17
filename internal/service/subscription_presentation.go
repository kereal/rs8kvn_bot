package service

import (
	"fmt"

	"github.com/kereal/rs8kvn_bot/internal/config"
)

// PremiumBenefitsText is the concise user-facing summary shared by purchase
// and payment-success messages.
const PremiumBenefitsText = "♾️ Безлимитный трафик\n🌍 Больше серверов и вариантов подключения\n🧪 Дополнительные и экспериментальные функции\n💬 Приоритетная поддержка"

// FormatSubscriptionMessage renders the canonical subscription presentation.
// Both the bot's "My subscription" screen and payment success notification use
// this helper so tariff, traffic, dates, reset information and URL stay aligned.
func FormatSubscriptionMessage(heading, status string, traffic *TrafficInfo, subURL string) string {
	if traffic == nil {
		traffic = &TrafficInfo{}
	}

	trafficInfo := "неограничен"
	progress := ""

	if traffic.LimitGB > 0 {
		trafficInfo = fmt.Sprintf("%.2f из %d Гб (%.0f%%)", traffic.UsedGB, traffic.LimitGB, traffic.Percentage)
		progress = "\n" + traffic.ProgressBar
	}

	resetInfo := traffic.ResetInfo
	if resetInfo == "" {
		resetInfo = "нет"
	}

	if status == "" {
		status = "активна"
	}

	return fmt.Sprintf(
		"%s\n\n✌️ Статус: *%s*\n💡 Тариф: *%s*\n📊 Трафик: %s%s\n\n📅 Создана: %s\n⏰ Истекает: %s\n🔄 Сброс: %s\n\n🔗 Ссылка\n`%s`",
		heading,
		status,
		traffic.PlanName,
		trafficInfo,
		progress,
		traffic.CreatedAtFormatted,
		traffic.ExpiresAtFormatted,
		resetInfo,
		subURL,
	)
}

// SubscriptionURL is kept as a tiny presentation seam for callers that already
// carry Config and should not duplicate URL construction.
func SubscriptionURL(cfg *config.Config, subID string) string {
	if cfg == nil {
		return ""
	}

	return cfg.SubURL(subID)
}
