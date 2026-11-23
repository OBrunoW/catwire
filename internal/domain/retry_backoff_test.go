package domain

import (
	"testing"
	"time"
)

func TestRetryBackoffFollowsScheduleAndCapsAtTwelveHours(t *testing.T) {
	cases := map[int]time.Duration{
		0:  30 * time.Second,
		1:  30 * time.Second,
		2:  2 * time.Minute,
		3:  10 * time.Minute,
		4:  30 * time.Minute,
		5:  time.Hour,
		6:  2 * time.Hour,
		7:  4 * time.Hour,
		8:  8 * time.Hour,
		9:  12 * time.Hour,
		10: 12 * time.Hour,
		50: 12 * time.Hour,
	}
	for failedAttempts, expected := range cases {
		if got := RetryBackoff(failedAttempts); got != expected {
			t.Fatalf("attempt %d: expected %s, got %s", failedAttempts, expected, got)
		}
	}
}
