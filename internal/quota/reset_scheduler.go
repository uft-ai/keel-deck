package quota

import "time"

// NextKnownReset returns the earliest known reset, including one already due.
// A nil reset is unknown and is not scheduled.
func NextKnownReset(windows []QuotaWindow, now time.Time) *time.Time {
	var next *time.Time
	for _, window := range windows {
		if window.ResetAtMS == nil {
			continue
		}
		due := time.UnixMilli(*window.ResetAtMS).UTC()
		if next == nil || due.Before(*next) {
			copied := due
			next = &copied
		}
	}
	return next
}

// BackoffAfter returns the next attempt after a scheduled refresh.
// It is bounded and does not retry the same subject immediately.
func BackoffAfter(now time.Time, attempt int) time.Time {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 6 {
		attempt = 6
	}
	return now.Add(time.Minute * time.Duration(1<<(attempt-1)))
}
