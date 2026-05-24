package utils

import (
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
)

func TestDaysUntilReset(t *testing.T) {
	t.Parallel()

	now := time.Now()

	tests := []struct {
		name       string
		expiryTime time.Time
		want       int
	}{
		{"zero expiry", time.Time{}, -1},
		{"future expiry", now.Add(24 * time.Hour), 1},
		{"past expiry", now.Add(-1 * time.Hour), 0},
		{"exactly now", now, 0},
		{"3 days", now.Add(3 * 24 * time.Hour), 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DaysUntilReset(now, tt.expiryTime)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestFormatDateRu(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		t    time.Time
		want string
	}{
		{"zero time", time.Time{}, "—"},
		{"specific date", time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC), "15 января 2025"},
		{"december", time.Date(2025, 12, 31, 0, 0, 0, 0, time.UTC), "31 декабря 2025"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatDateRu(tt.t)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestGenerateProgressBar(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		usedGB  float64
		limitGB float64
		wantLen int
	}{
		{"zero limit", 0, 0, 10},
		{"negative limit", 5, -1, 10},
		{"empty bar", 0, 10, 10},
		{"full bar", 10, 10, 10},
		{"half way", 5, 10, 10},
		{"over 100%", 15, 10, 10},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GenerateProgressBar(tt.usedGB, tt.limitGB)
			assert.Equal(t, tt.wantLen, utf8.RuneCountInString(got))
		})
	}
}

func TestIsRealUsername(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		username string
		want     bool
	}{
		{"empty string", "", false},
		{"simple ascii", "user", true},
		{"with underscore", "user_name", true},
		{"with digits", "user123", true},
		{"mixed", "User_Name_123", true},
		{"only digits", "11", true},
		{"with spaces", "user name", false},
		{"with dash", "user-name", false},
		{"with dot", "user.name", false},
		{"with emoji", "user🎉", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsRealUsername(tt.username)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestIsNumericUsername(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		username string
		want     bool
	}{
		{"empty string", "", false},
		{"single digit", "7", true},
		{"double digit", "11", true},
		{"large number", "1234567890", true},
		{"mixed alphanumeric", "user11", false},
		{"alphabetic", "user", false},
		{"with underscore", "user_11", false},
		{"with spaces", "11 22", false},
		{"with hyphen", "11-22", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsNumericUsername(tt.username)
			assert.Equal(t, tt.want, got)
		})
	}
}
