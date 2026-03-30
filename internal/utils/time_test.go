package utils

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
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
			assert.Equal(t, tt.expected, result, "FirstSecondOfNextMonth(%v)", tt.input)
		})
	}
}

func TestFirstSecondOfNextMonth_LocalTimezone(t *testing.T) {
	loc := time.FixedZone("UTC+3", 3*3600)
	input := time.Date(2024, 1, 15, 12, 0, 0, 0, loc)
	expected := time.Date(2024, 2, 1, 0, 0, 0, 0, loc)

	result := FirstSecondOfNextMonth(input)
	assert.Equal(t, expected, result, "FirstSecondOfNextMonth() with local timezone")
	assert.Equal(t, loc, result.Location(), "FirstSecondOfNextMonth() should preserve timezone")
}

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

func TestFirstSecondOfNextMonth_Midnight(t *testing.T) {
	tests := []struct {
		name     string
		input    time.Time
		expected time.Time
	}{
		{
			name:     "exactly midnight January",
			input:    time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			expected: time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			name:     "one nanosecond before midnight",
			input:    time.Date(2024, 6, 15, 23, 59, 59, 999999999, time.UTC),
			expected: time.Date(2024, 7, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			name:     "exactly midnight December 31",
			input:    time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC),
			expected: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FirstSecondOfNextMonth(tt.input)
			assert.Equal(t, tt.expected, result, "FirstSecondOfNextMonth(%v)", tt.input)
		})
	}
}

func TestFirstSecondOfNextMonth_YearBoundaries(t *testing.T) {
	tests := []struct {
		name     string
		input    time.Time
		expected time.Time
	}{
		{
			name:     "year 2000 to 2001",
			input:    time.Date(2000, 12, 15, 12, 0, 0, 0, time.UTC),
			expected: time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			name:     "year 1999 to 2000",
			input:    time.Date(1999, 12, 15, 12, 0, 0, 0, time.UTC),
			expected: time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			name:     "far future year",
			input:    time.Date(2100, 6, 15, 12, 0, 0, 0, time.UTC),
			expected: time.Date(2100, 7, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			name:     "past year",
			input:    time.Date(1990, 3, 15, 12, 0, 0, 0, time.UTC),
			expected: time.Date(1990, 4, 1, 0, 0, 0, 0, time.UTC),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FirstSecondOfNextMonth(tt.input)
			assert.Equal(t, tt.expected, result, "FirstSecondOfNextMonth(%v)", tt.input)
		})
	}
}

func TestFirstSecondOfNextMonth_DifferentTimezones(t *testing.T) {
	tests := []struct {
		name     string
		location *time.Location
	}{
		{"UTC", time.UTC},
		{"America/New_York", mustLoadLocation("America/New_York")},
		{"Europe/London", mustLoadLocation("Europe/London")},
		{"Asia/Tokyo", mustLoadLocation("Asia/Tokyo")},
		{"Australia/Sydney", mustLoadLocation("Australia/Sydney")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := time.Date(2024, 6, 15, 12, 0, 0, 0, tt.location)
			result := FirstSecondOfNextMonth(input)

			expected := time.Date(2024, 7, 1, 0, 0, 0, 0, tt.location)

			assert.Equal(t, expected, result, "FirstSecondOfNextMonth with timezone %s", tt.name)
			assert.Equal(t, tt.location, result.Location(), "Location should be preserved")
		})
	}
}

func TestFirstSecondOfNextMonth_FixedZone(t *testing.T) {
	// Test with fixed offset zones
	tests := []struct {
		name   string
		offset int // hours from UTC
	}{
		{"UTC+0", 0},
		{"UTC+1", 1},
		{"UTC-5", -5},
		{"UTC+8", 8},
		{"UTC+10", 10},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			loc := time.FixedZone(tt.name, tt.offset*3600)
			input := time.Date(2024, 6, 15, 12, 0, 0, 0, loc)
			result := FirstSecondOfNextMonth(input)

			expected := time.Date(2024, 7, 1, 0, 0, 0, 0, loc)

			assert.Equal(t, expected, result, "FirstSecondOfNextMonth with %s", tt.name)
		})
	}
}

func TestFirstSecondOfNextMonth_Concurrent(t *testing.T) {
	t.Run("concurrent calls are safe", func(t *testing.T) {
		const goroutines = 100
		results := make(chan time.Time, goroutines)

		for i := 0; i < goroutines; i++ {
			go func(idx int) {
				input := time.Date(2024, time.January+time.Month(idx%12), 15, 12, 0, 0, 0, time.UTC)
				results <- FirstSecondOfNextMonth(input)
			}(i)
		}

		// Collect all results - should not panic or race
		for i := 0; i < goroutines; i++ {
			result := <-results
			assert.Equal(t, 1, result.Day(), "Result should be first day of month")
		}
	})
}

func TestFirstSecondOfNextMonth_CenturyTransition(t *testing.T) {
	tests := []struct {
		name     string
		input    time.Time
		expected time.Time
	}{
		{
			name:     "December 2099",
			input:    time.Date(2099, 12, 31, 23, 59, 59, 0, time.UTC),
			expected: time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			name:     "December 1899",
			input:    time.Date(1899, 12, 15, 12, 0, 0, 0, time.UTC),
			expected: time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FirstSecondOfNextMonth(tt.input)
			assert.Equal(t, tt.expected, result, "FirstSecondOfNextMonth(%v)", tt.input)
		})
	}
}

func TestFirstSecondOfNextMonth_VariousDaysOfMonth(t *testing.T) {
	// Test that the result is always the first of next month regardless of input day
	for day := 1; day <= 31; day++ {
		t.Run(fmt.Sprintf("day_%d", day), func(t *testing.T) {
			// Use a month with 31 days
			input := time.Date(2024, 1, day, 12, 0, 0, 0, time.UTC)
			result := FirstSecondOfNextMonth(input)

			expected := time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)
			assert.Equal(t, expected, result, "FirstSecondOfNextMonth for day %d", day)
		})
	}
}

// BenchmarkFirstSecondOfNextMonth benchmarks the function
func BenchmarkFirstSecondOfNextMonth(b *testing.B) {
	input := time.Now()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		FirstSecondOfNextMonth(input)
	}
}

// BenchmarkFirstSecondOfNextMonth_December benchmarks December transition
func BenchmarkFirstSecondOfNextMonth_December(b *testing.B) {
	input := time.Date(2024, 12, 15, 12, 0, 0, 0, time.UTC)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		FirstSecondOfNextMonth(input)
	}
}

// BenchmarkFirstSecondOfNextMonth_Parallel benchmarks parallel execution
func BenchmarkFirstSecondOfNextMonth_Parallel(b *testing.B) {
	input := time.Now()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			FirstSecondOfNextMonth(input)
		}
	})
}

// mustLoadLocation loads a timezone location, panicking on error (for test setup)
func mustLoadLocation(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic(fmt.Sprintf("failed to load location %s: %v", name, err))
	}
	return loc
}
