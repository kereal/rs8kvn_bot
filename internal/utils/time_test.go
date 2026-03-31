package utils

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestFirstSecondOfNextMonth — консолидированный табличный тест
func TestFirstSecondOfNextMonth(t *testing.T) {
	tests := []struct {
		name     string
		input    time.Time
		expected time.Time
	}{
		// Базовые тесты по месяцам
		{"January", time.Date(2024, 1, 15, 12, 30, 0, 0, time.UTC), time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)},
		{"December", time.Date(2024, 12, 25, 23, 59, 59, 0, time.UTC), time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)},
		{"First day of month", time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC), time.Date(2024, 7, 1, 0, 0, 0, 0, time.UTC)},
		{"Last day of month", time.Date(2024, 6, 30, 23, 59, 59, 999999999, time.UTC), time.Date(2024, 7, 1, 0, 0, 0, 0, time.UTC)},
		{"Leap year February", time.Date(2024, 2, 29, 12, 0, 0, 0, time.UTC), time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)},
		{"Non-leap year February", time.Date(2023, 2, 28, 12, 0, 0, 0, time.UTC), time.Date(2023, 3, 1, 0, 0, 0, 0, time.UTC)},

		// Тесты временных зон
		{"UTC+3", time.Date(2024, 1, 15, 12, 0, 0, 0, time.FixedZone("UTC+3", 3*3600)), time.Date(2024, 2, 1, 0, 0, 0, 0, time.FixedZone("UTC+3", 3*3600))},
		{"America/New_York", time.Date(2024, 6, 15, 12, 0, 0, 0, mustLoadLocation("America/New_York")), time.Date(2024, 7, 1, 0, 0, 0, 0, mustLoadLocation("America/New_York"))},
		{"Europe/London", time.Date(2024, 6, 15, 12, 0, 0, 0, mustLoadLocation("Europe/London")), time.Date(2024, 7, 1, 0, 0, 0, 0, mustLoadLocation("Europe/London"))},
		{"Asia/Tokyo", time.Date(2024, 6, 15, 12, 0, 0, 0, mustLoadLocation("Asia/Tokyo")), time.Date(2024, 7, 1, 0, 0, 0, 0, mustLoadLocation("Asia/Tokyo"))},
		{"Australia/Sydney", time.Date(2024, 6, 15, 12, 0, 0, 0, mustLoadLocation("Australia/Sydney")), time.Date(2024, 7, 1, 0, 0, 0, 0, mustLoadLocation("Australia/Sydney"))},

		// Fixed zone тесты
		{"UTC+0", time.Date(2024, 6, 15, 12, 0, 0, 0, time.FixedZone("UTC+0", 0)), time.Date(2024, 7, 1, 0, 0, 0, 0, time.FixedZone("UTC+0", 0))},
		{"UTC-5", time.Date(2024, 6, 15, 12, 0, 0, 0, time.FixedZone("UTC-5", -5*3600)), time.Date(2024, 7, 1, 0, 0, 0, 0, time.FixedZone("UTC-5", -5*3600))},
		{"UTC+8", time.Date(2024, 6, 15, 12, 0, 0, 0, time.FixedZone("UTC+8", 8*3600)), time.Date(2024, 7, 1, 0, 0, 0, 0, time.FixedZone("UTC+8", 8*3600))},

		// Тесты перехода года
		{"Year 2000 to 2001", time.Date(2000, 12, 15, 12, 0, 0, 0, time.UTC), time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)},
		{"Year 1999 to 2000", time.Date(1999, 12, 15, 12, 0, 0, 0, time.UTC), time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)},
		{"Far future year", time.Date(2100, 6, 15, 12, 0, 0, 0, time.UTC), time.Date(2100, 7, 1, 0, 0, 0, 0, time.UTC)},
		{"Past year 1990", time.Date(1990, 3, 15, 12, 0, 0, 0, time.UTC), time.Date(1990, 4, 1, 0, 0, 0, 0, time.UTC)},

		// Тесты перехода века
		{"December 2099", time.Date(2099, 12, 31, 23, 59, 59, 0, time.UTC), time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)},
		{"December 1899", time.Date(1899, 12, 15, 12, 0, 0, 0, time.UTC), time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC)},

		// Тесты полуночи
		{"Midnight January 1", time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)},
		{"One nanosecond before midnight", time.Date(2024, 6, 15, 23, 59, 59, 999999999, time.UTC), time.Date(2024, 7, 1, 0, 0, 0, 0, time.UTC)},
		{"Midnight December 31", time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC), time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FirstSecondOfNextMonth(tt.input)
			assert.Equal(t, tt.expected, result, "FirstSecondOfNextMonth(%v)", tt.input)
		})
	}
}

// TestFirstSecondOfNextMonth_AllMonths тестирует все 12 месяцев
func TestFirstSecondOfNextMonth_AllMonths(t *testing.T) {
	for month := time.January; month <= time.December; month++ {
		t.Run(month.String(), func(t *testing.T) {
			input := time.Date(2024, month, 15, 12, 0, 0, 0, time.UTC)
			result := FirstSecondOfNextMonth(input)

			expectedMonth := month + 1
			expectedYear := 2024
			if month == time.December {
				expectedMonth = time.January
				expectedYear = 2025
			}
			expected := time.Date(expectedYear, expectedMonth, 1, 0, 0, 0, 0, time.UTC)

			assert.Equal(t, expected, result, "FirstSecondOfNextMonth(%s)", month)
		})
	}
}

// TestFirstSecondOfNextMonth_Now тестирует с текущим временем
func TestFirstSecondOfNextMonth_Now(t *testing.T) {
	now := time.Now()
	result := FirstSecondOfNextMonth(now)

	assert.False(t, result.Before(now), "FirstSecondOfNextMonth(now) should be after now")

	year, month, _ := now.Date()
	nextMonth := month + 1
	nextYear := year
	if month == 12 {
		nextMonth = 1
		nextYear = year + 1
	}
	expected := time.Date(nextYear, nextMonth, 1, 0, 0, 0, 0, now.Location())

	assert.Equal(t, expected, result, "FirstSecondOfNextMonth(now)")
}

// TestFirstSecondOfNextMonth_Concurrent тестирует потокобезопасность
func TestFirstSecondOfNextMonth_Concurrent(t *testing.T) {
	const goroutines = 100
	results := make(chan time.Time, goroutines)

	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			input := time.Date(2024, time.January+time.Month(idx%12), 15, 12, 0, 0, 0, time.UTC)
			results <- FirstSecondOfNextMonth(input)
		}(i)
	}

	for i := 0; i < goroutines; i++ {
		result := <-results
		assert.Equal(t, 1, result.Day(), "Result should be first day of month")
	}
}

// TestFirstSecondOfNextMonth_VariousDaysOfMonth тестирует разные дни месяца
func TestFirstSecondOfNextMonth_VariousDaysOfMonth(t *testing.T) {
	for day := 1; day <= 31; day++ {
		t.Run(dayName(day), func(t *testing.T) {
			input := time.Date(2024, 1, day, 12, 0, 0, 0, time.UTC)
			result := FirstSecondOfNextMonth(input)
			expected := time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)
			assert.Equal(t, expected, result, "FirstSecondOfNextMonth for day %d", day)
		})
	}
}

func dayName(day int) string {
	suffix := "th"
	switch day {
	case 1, 21, 31:
		suffix = "st"
	case 2, 22:
		suffix = "nd"
	case 3, 23:
		suffix = "rd"
	}
	return fmt.Sprintf("day_%d%s", day, suffix)
}

// mustLoadLocation loads a timezone location, panicking on error (for test setup)
func mustLoadLocation(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic(fmt.Sprintf("failed to load location %s: %v", name, err))
	}
	return loc
}
