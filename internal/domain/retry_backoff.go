package domain

import "time"

var retryBackoffSchedule = []time.Duration{
	30 * time.Second,
	2 * time.Minute,
	10 * time.Minute,
	30 * time.Minute,
	time.Hour,
	2 * time.Hour,
	4 * time.Hour,
	8 * time.Hour,
	12 * time.Hour,
}

func RetryBackoff(failedAttempts int) time.Duration {
	if failedAttempts < 1 {
		failedAttempts = 1
	}
	index := failedAttempts - 1
	if index >= len(retryBackoffSchedule) {
		index = len(retryBackoffSchedule) - 1
	}
	return retryBackoffSchedule[index]
}
