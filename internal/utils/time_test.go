package utils

import (
	"testing"
	"time"
)

func TestFirstSecondOfNextMonth(t *testing.T) {
	tests := []struct {
		name     string
		input    time.Time
		expected time.Time
	}{
		{
			name:     "January",
			input:    time.Date(2024, 1, 15, 12, 30, 0, 0, time.UTC),
			expected: time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			name:     "December",
			input:    time.Date(2024, 12, 25, 23, 59, 59, 0, time.UTC),
			expected: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			name:     "First day of month",
			input:    time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC),
			expected: time.Date(2024, 7, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			name:     "Last day of month",
			input:    time.Date(2024, 6, 30, 23, 59, 59, 999999999, time.UTC),
			expected: time.Date(2024, 7, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			name:     "Leap year February",
			input:    time.Date(2024, 2, 29, 12, 0, 0, 0, time.UTC),
			expected: time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			name:     "Non-leap year February",
			input:    time.Date(2023, 2, 28, 12, 0, 0, 0, time.UTC),
			expected: time.Date(2023, 3, 1, 0, 0, 0, 0, time.UTC),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FirstSecondOfNextMonth(tt.input)
			if !result.Equal(tt.expected) {
				t.Errorf("FirstSecondOfNextMonth(%v) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

func TestFirstSecondOfNextMonth_LocalTimezone(t *testing.T) {
	loc := time.FixedZone("UTC+3", 3*3600)
	input := time.Date(2024, 1, 15, 12, 0, 0, 0, loc)
	expected := time.Date(2024, 2, 1, 0, 0, 0, 0, loc)

	result := FirstSecondOfNextMonth(input)
	if !result.Equal(expected) {
		t.Errorf("FirstSecondOfNextMonth() with local timezone = %v, want %v", result, expected)
	}

	if result.Location() != loc {
		t.Errorf("FirstSecondOfNextMonth() should preserve timezone, got %v, want %v", result.Location(), loc)
	}
}

func TestFirstSecondOfNextMonth_Now(t *testing.T) {
	now := time.Now()
	result := FirstSecondOfNextMonth(now)

	if result.Before(now) {
		t.Errorf("FirstSecondOfNextMonth(now) = %v, should be after now", result)
	}

	year, month, _ := now.Date()
	nextMonth := month + 1
	nextYear := year
	if month == 12 {
		nextMonth = 1
		nextYear = year + 1
	}
	expected := time.Date(nextYear, nextMonth, 1, 0, 0, 0, 0, now.Location())

	if !result.Equal(expected) {
		t.Errorf("FirstSecondOfNextMonth(now) = %v, want %v", result, expected)
	}
}

func TestFirstSecondOfNextMonth_AllMonths(t *testing.T) {
	for month := time.January; month <= time.December; month++ {
		input := time.Date(2024, month, 15, 12, 0, 0, 0, time.UTC)
		result := FirstSecondOfNextMonth(input)

		expectedMonth := month + 1
		expectedYear := 2024
		if month == time.December {
			expectedMonth = time.January
			expectedYear = 2025
		}
		expected := time.Date(expectedYear, expectedMonth, 1, 0, 0, 0, 0, time.UTC)

		if !result.Equal(expected) {
			t.Errorf("FirstSecondOfNextMonth(%s) = %v, want %v", month, result, expected)
		}
	}
}
