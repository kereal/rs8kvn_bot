package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFormatSubscriptionMessage_UsesCanonicalFields(t *testing.T) {
	traffic := &TrafficInfo{
		PlanName:           "Premium",
		LimitGB:            100,
		UsedGB:             12.5,
		Percentage:         12.5,
		ProgressBar:        "████",
		CreatedAtFormatted: "01.01.2026 12:00",
		ExpiresAtFormatted: "31.01.2026 12:00",
		ResetInfo:          "через 5 дн.",
	}

	got := FormatSubscriptionMessage("📋 *Ваша подписка*", "активна", traffic, "https://example.test/sub/abc")

	assert.Contains(t, got, "Статус: *активна*")
	assert.Contains(t, got, "Тариф: *Premium*")
	assert.Contains(t, got, "12.50 из 100 Гб (12%)")
	assert.Contains(t, got, "████")
	assert.Contains(t, got, "01.01.2026 12:00")
	assert.Contains(t, got, "31.01.2026 12:00")
	assert.Contains(t, got, "через 5 дн.")
	assert.Contains(t, got, "`https://example.test/sub/abc`")
}

func TestFormatSubscriptionMessage_DefaultsUnlimitedAndStatus(t *testing.T) {
	got := FormatSubscriptionMessage("Heading", "", &TrafficInfo{PlanName: "Free"}, "")

	assert.Contains(t, got, "Статус: *активна*")
	assert.Contains(t, got, "Тариф: *Free*")
	assert.Contains(t, got, "Трафик: неограничен")
	assert.Contains(t, got, "Сброс: нет")
}
