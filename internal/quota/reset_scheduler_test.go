package quota

import (
	"testing"
	"time"
)

func TestNextKnownResetIgnoresUnknownWindows(t *testing.T) {
	now := time.Date(2026, 10, 12, 1, 0, 0, 0, time.UTC)
	known := now.Add(time.Hour).UnixMilli()
	later := now.Add(2 * time.Hour).UnixMilli()
	got := NextKnownReset([]QuotaWindow{{ResetAtMS: nil}, {ResetAtMS: &later}, {ResetAtMS: &known}}, now)
	if got == nil || !got.Equal(time.UnixMilli(known).UTC()) {
		t.Fatalf("next = %v", got)
	}
	if NextKnownReset([]QuotaWindow{{}}, now) != nil {
		t.Fatal("unknown reset was scheduled")
	}
	past := now.Add(-time.Minute).UnixMilli()
	overdue := NextKnownReset([]QuotaWindow{{ResetAtMS: &past}}, now)
	if overdue == nil || !overdue.Equal(time.UnixMilli(past).UTC()) {
		t.Fatal("already due reset was skipped")
	}
	if !BackoffAfter(now, 1).Equal(now.Add(time.Minute)) || !BackoffAfter(now, 9).Equal(now.Add(32*time.Minute)) {
		t.Fatal("backoff was unbounded or immediate")
	}
}
