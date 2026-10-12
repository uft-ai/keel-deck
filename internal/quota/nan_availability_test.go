package quota

import (
	"testing"
	"time"
)

func TestNanPeriodObservationUsesTheModelBoundary(t *testing.T) {
	end := time.Date(2026, 11, 9, 15, 6, 22, 0, time.UTC)
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	report := NanQuotaReport{
		AccountPeriodStart: &start,
		Models: []NanModelQuota{{
			Model: "qwen3.8-flash", TokensUsed: 10, Cap: 100, Remaining: 90, PeriodEnd: &end,
		}},
	}
	got, err := NanPeriodObservation(PeriodObservationInput{
		ObservationID: "deck-1", BasedOnVersion: 4, AuthIndex: "1111111111111111",
		Model: "qwen3.8-flash", NotBefore: time.Date(2026, 10, 12, 1, 0, 0, 0, time.UTC), Report: report,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.State != "available" || got.BasedOnVersion != 4 || got.PoolID != "" || got.PeriodEnd == nil || !got.PeriodEnd.Equal(end) {
		t.Fatalf("observation = %+v", got)
	}
	if got.Remaining == nil || *got.Remaining != 90 || got.AccountPeriodStart == nil {
		t.Fatalf("context = %+v", got)
	}
}

func TestNanPeriodObservationDoesNotInventRenewal(t *testing.T) {
	report := NanQuotaReport{Models: []NanModelQuota{{
		Model: "qwen3.8-flash", TokensUsed: 100, Cap: 100, Remaining: 0,
	}}}
	got, err := NanPeriodObservation(PeriodObservationInput{
		ObservationID: "deck-2", AuthIndex: "1111111111111111", Model: "qwen3.8-flash",
		NotBefore: time.Date(2026, 10, 12, 1, 0, 0, 0, time.UTC), Report: report,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.State != "exhausted" || got.PeriodEnd != nil || got.BasedOnVersion != 0 {
		t.Fatalf("exhausted observation = %+v", got)
	}

	availableWithoutEnd := report
	availableWithoutEnd.Models = []NanModelQuota{{
		Model: "qwen3.8-flash", TokensUsed: 1, Cap: 10, Remaining: 1,
	}}
	unknown, err := NanPeriodObservation(PeriodObservationInput{
		ObservationID: "deck-3", AuthIndex: "1111111111111111", Model: "qwen3.8-flash",
		NotBefore: time.Now().UTC(), Report: availableWithoutEnd,
	})
	if err != nil || unknown.State != "available" || unknown.PeriodEnd != nil || unknown.Remaining == nil || *unknown.Remaining != 1 {
		t.Fatalf("positive unknown boundary = %+v %v", unknown, err)
	}
	if _, err := NanPeriodObservation(PeriodObservationInput{
		ObservationID: "deck-4", AuthIndex: "1111111111111111", Model: "missing",
		NotBefore: time.Now().UTC(), Report: report,
	}); err == nil {
		t.Fatal("missing model was accepted")
	}
}
