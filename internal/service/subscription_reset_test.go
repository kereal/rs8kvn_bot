package service

import (
	"testing"
	"time"

	"github.com/kereal/rs8kvn_bot/internal/xui"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// referenceNow is a fixed Saturday (2026-10-31, 15:30 UTC) used as "now" so
// the countdown math is deterministic. The Weekday assertion guards against a
// silently wrong calendar assumption.
func referenceNow(t *testing.T) time.Time {
	t.Helper()

	now := time.Date(2026, 10, 31, 15, 30, 0, 0, time.UTC)
	assert.Equal(t, time.Saturday, now.Weekday(), "reference date must stay a Saturday")

	return now
}

func TestDescribeTrafficReset(t *testing.T) {
	t.Parallel()

	now := referenceNow(t)

	tests := []struct {
		name     string
		profile  xui.ClientConfig
		wantText string
		wantDays int
	}{
		{
			name:     "never",
			profile:  xui.ClientConfig{TrafficReset: "never"},
			wantText: "не сбрасывается",
		},
		{
			name:     "empty period is never",
			profile:  xui.ClientConfig{},
			wantText: "не сбрасывается",
		},
		{
			name:     "monthly day 1 lands on next month first",
			profile:  xui.ClientConfig{TrafficReset: "monthly", TrafficResetDay: 1},
			wantText: "через 1 дн.",
			wantDays: 1,
		},
		{
			name:     "monthly day passed this month rolls to next month",
			profile:  xui.ClientConfig{TrafficReset: "monthly", TrafficResetDay: 30},
			wantText: "через 30 дн.",
			wantDays: 30,
		},
		{
			name:     "monthly day today counts zero",
			profile:  xui.ClientConfig{TrafficReset: "monthly", TrafficResetDay: 31},
			wantText: "сегодня",
		},
		{
			name:     "monthly day 0 normalizes to 1 like the panel",
			profile:  xui.ClientConfig{TrafficReset: "monthly", TrafficResetDay: 0},
			wantText: "через 1 дн.",
			wantDays: 1,
		},
		{
			name:     "monthly day out of range is unknown",
			profile:  xui.ClientConfig{TrafficReset: "monthly", TrafficResetDay: 32},
			wantText: "неизвестно",
		},
		{
			name:     "monthly negative day is unknown",
			profile:  xui.ClientConfig{TrafficReset: "monthly", TrafficResetDay: -3},
			wantText: "неизвестно",
		},
		{
			name:     "daily is tomorrow",
			profile:  xui.ClientConfig{TrafficReset: "daily"},
			wantText: "через 1 дн.",
			wantDays: 1,
		},
		{
			name:     "weekly 1 is next Monday",
			profile:  xui.ClientConfig{TrafficReset: "weekly", ResetWeekday: 1},
			wantText: "через 2 дн.",
			wantDays: 2,
		},
		{
			name:     "weekly 7 is next Sunday",
			profile:  xui.ClientConfig{TrafficReset: "weekly", ResetWeekday: 7},
			wantText: "через 1 дн.",
			wantDays: 1,
		},
		{
			name:     "weekly on the current weekday is today",
			profile:  xui.ClientConfig{TrafficReset: "weekly", ResetWeekday: 6},
			wantText: "сегодня",
		},
		{
			name:     "weekly without weekday is unknown",
			profile:  xui.ClientConfig{TrafficReset: "weekly", ResetWeekday: 0},
			wantText: "неизвестно",
		},
		{
			name:     "weekly weekday out of range is unknown",
			profile:  xui.ClientConfig{TrafficReset: "weekly", ResetWeekday: 8},
			wantText: "неизвестно",
		},
		{
			name:     "hourly is not modeled",
			profile:  xui.ClientConfig{TrafficReset: "hourly"},
			wantText: "неизвестно",
		},
		{
			name:     "unrecognized period is unknown",
			profile:  xui.ClientConfig{TrafficReset: "yearly", TrafficResetDay: 1},
			wantText: "неизвестно",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := describeTrafficReset(now, tt.profile)

			assert.Equal(t, tt.wantText, got.Text)
			assert.Equal(t, tt.wantDays, got.Days)
		})
	}
}

func TestNextMonthlyReset(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		now  time.Time
		day  int
		want time.Time
	}{
		{
			name: "day later this month",
			now:  time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC),
			day:  20,
			want: time.Date(2026, 1, 20, 0, 0, 0, 0, time.UTC),
		},
		{
			name: "day earlier this month rolls to next month",
			now:  time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC),
			day:  1,
			want: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			name: "day missing in current month skips to a month that has it",
			now:  time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC),
			day:  31,
			want: time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC),
		},
		{
			name: "february rollover with day 31 lands on march 31",
			now:  time.Date(2026, 2, 10, 12, 0, 0, 0, time.UTC),
			day:  31,
			want: time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC),
		},
		{
			name: "day today returns today",
			now:  time.Date(2026, 3, 5, 23, 0, 0, 0, time.UTC),
			day:  5,
			want: time.Date(2026, 3, 5, 0, 0, 0, 0, time.UTC),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := nextMonthlyReset(tt.now, tt.day)
			require.False(t, got.IsZero())

			assert.Equal(t, tt.want, got)
		})
	}
}

func TestNextWeeklyReset(t *testing.T) {
	t.Parallel()

	saturday := referenceNow(t)

	tests := []struct {
		name    string
		now     time.Time
		weekday int
		want    time.Time
	}{
		{
			name:    "monday from saturday",
			now:     saturday,
			weekday: 1,
			want:    time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC),
		},
		{
			name:    "sunday from saturday",
			now:     saturday,
			weekday: 7,
			want:    time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			name:    "same weekday counts today",
			now:     saturday,
			weekday: 6,
			want:    time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC),
		},
		{
			name:    "saturday from sunday",
			now:     time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC),
			weekday: 6,
			want:    time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := nextWeeklyReset(tt.now, tt.weekday)

			assert.Equal(t, tt.want, got)
		})
	}
}
