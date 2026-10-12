package api

import (
	"testing"
	"time"

	"github.com/oh-my-cpa/oh-my-cpa/internal/cpa/management"
	"github.com/oh-my-cpa/oh-my-cpa/internal/quota"
)

func TestScheduledQuotaSelectionSkipsDisabledAndBacksOff(t *testing.T) {
	now := time.Date(2026, 10, 12, 3, 0, 0, 0, time.UTC)
	past := now.Add(-time.Minute).UnixMilli()
	handler := &Handler{nowFn: func() time.Time { return now }, quotaRetryAfter: map[string]time.Time{}}
	handler.quotaRetryAfter["nan-1"] = quota.BackoffAfter(now, 1)
	if !handler.now().Before(handler.quotaRetryAfter["nan-1"]) {
		t.Fatal("failed subject would refresh immediately")
	}
	subjects := []quotaSubject{{
		File:   management.AuthFile{AuthIndex: "nan-disabled", Disabled: true, Type: "nan", Provider: "nan"},
		Models: []string{"qwen3.8-flash"},
	}}
	subject, found := findQuotaSubject(subjects, "nan-disabled")
	if !found || !subject.File.Disabled {
		t.Fatal("current disabled subject was not retained")
	}
	if quota.NextKnownReset([]quota.QuotaWindow{{ResetAtMS: &past}}, time.Time{}) == nil {
		t.Fatal("restart did not select an already due reset")
	}
}
