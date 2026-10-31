package service

import (
	"fmt"

	"github.com/kereal/rs8kvn_bot/internal/config"
	"github.com/kereal/rs8kvn_bot/internal/utils"
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
		// Available=false: у подписки есть активные узлы, но ни один не умеет
		// читать трафик (proxman, fetch) — показываем честное «недоступен»,
		// а не вводящие в заблуждение 0 ГБ.
		if !traffic.Available {
			trafficInfo = "недоступен"
		} else {
			trafficInfo = fmt.Sprintf("%.2f из %d Гб (%.0f%%)", traffic.UsedGB, traffic.LimitGB, traffic.Percentage)
			progress = "\n" + traffic.ProgressBar
		}
	}

	resetInfo := traffic.ResetInfo
	if resetInfo == "" {
		resetInfo = "нет"
	}

	if status == "" {
		status = "активна"
	}

	return fmt.Sprintf(
		"%s\n\n✌️ Статус: *%s*\n💡 Тариф: *%s*\n📊 Трафик: %s%s\n\n📅 Создана: %s\n⏰ Истекает: %s\n🔄 Сброс: %s\n\n🔗 Ссылка (для Happ)\n`%s`",
		heading,
		status,
		utils.EscapeLegacyMarkdown(traffic.PlanName),
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
