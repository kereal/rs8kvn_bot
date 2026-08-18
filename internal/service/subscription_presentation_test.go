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

func TestFormatSubscriptionMessage_EscapesMarkdownInPlanName(t *testing.T) {
	tests := []struct {
		name             string
		planName         string
		expectedInOutput string
	}{
		{
			name:             "underscore in plan name",
			planName:         "Premium_Pro",
			expectedInOutput: `Тариф: *Premium\_Pro*`,
		},
		{
			name:             "asterisk in plan name",
			planName:         "Plan*VIP",
			expectedInOutput: `Тариф: *Plan\*VIP*`,
		},
		{
			name:             "backtick in plan name",
			planName:         "Plan`Special",
			expectedInOutput: "Тариф: *Plan\\`Special*",
		},
		{
			name:             "opening bracket in plan name",
			planName:         "Plan[Elite]",
			expectedInOutput: `Тариф: *Plan\[Elite]*`,
		},
		{
			name:             "multiple special chars",
			planName:         "Pro_*Test*_Plan",
			expectedInOutput: `Тариф: *Pro\_\*Test\*\_Plan*`,
		},
		{
			name:             "all special chars",
			planName:         "_*`[VIP",
			expectedInOutput: "Тариф: *\\_\\*\\`\\[VIP*",
		},
		{
			name:             "normal plan name unchanged",
			planName:         "Premium Pro 2024",
			expectedInOutput: `Тариф: *Premium Pro 2024*`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			traffic := &TrafficInfo{
				PlanName:           tc.planName,
				CreatedAtFormatted: "01.01.2026",
				ExpiresAtFormatted: "31.01.2026",
			}

			got := FormatSubscriptionMessage("Test", "активна", traffic, "https://example.com/sub/test")

			assert.Contains(t, got, tc.expectedInOutput,
				"Plan name should be properly escaped in the output message")
		})
	}
}
