package utils

import "time"

// FirstSecondOfNextMonth returns the first second of the next month.
// Handles December edge case by rolling over to January of the next year.
func FirstSecondOfNextMonth(t time.Time) time.Time {
	year, month, _ := t.Date()

	// Handle December edge case
	if month == time.December {
		return time.Date(year+1, time.January, 1, 0, 0, 0, 0, t.Location())
	}

	return time.Date(year, month+1, 1, 0, 0, 0, 0, t.Location())
}
