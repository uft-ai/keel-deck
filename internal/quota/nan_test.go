package quota

import (
	"strings"
	"testing"
	"time"
)

func TestParseNanQuotaPreservesExactModelBoundaries(t *testing.T) {
	raw := []byte(`{
		"periodStart":"2026-10-01",
		"models":[{
			"model":"qwen3.8-flash",
			"tokensUsed":0,
			"cap":1000,
			"remaining":1000,
			"periodEnd":"2026-11-09T15:06:22Z",
			"updatedAt":"2026-10-12T00:00:00Z",
			"windowHours":4,
			"fullWindowTokens":250
		}]
	}`)
	report, err := ParseNanQuota(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(report.Models) != 1 {
		t.Fatalf("models = %d", len(report.Models))
	}
	model := report.Models[0]
	if model.TokensUsed != 0 || model.Cap != 1000 || model.Remaining != 1000 {
		t.Fatalf("counters = %+v", model)
	}
	if model.PeriodEnd == nil || model.PeriodEnd.Format(time.RFC3339) != "2026-11-09T15:06:22Z" {
		t.Fatalf("period end = %v", model.PeriodEnd)
	}
	windows := NormalizeNanQuota(report)
	if windows[0].ResetAccuracy != "exact" || *windows[0].Used != 0 || *windows[0].Limit != 1000 {
		t.Fatalf("window = %+v", windows[0])
	}
	if windows[0].PeriodHours != nil {
		t.Fatalf("rolling window stored as billing period: %+v", windows[0])
	}
}

func TestParseNanQuotaRejectsInvalidReports(t *testing.T) {
	cases := []string{
		``,
		`{}`,
		`{"models":[]}`,
		`{"models":[{"model":"","tokensUsed":0,"cap":1,"remaining":1}]}`,
		`{"models":[{"model":"m","tokensUsed":0,"cap":1,"remaining":1},{"model":"m","tokensUsed":1,"cap":1,"remaining":0}]}`,
		`{"models":[{"model":"m","tokensUsed":1.5,"cap":2,"remaining":1}]}`,
		`{"models":[{"model":"m","tokensUsed":-1,"cap":2,"remaining":1}]}`,
		`{"models":[{"model":"m","tokensUsed":1,"cap":0,"remaining":0}]}`,
		`{"models":[{"model":"m","tokensUsed":null,"cap":1,"remaining":1}]}`,
		`{"models":[{"model":"m","cap":1,"remaining":1}]}`,
		`{"models":[{"model":"m","tokensUsed":1,"cap":1,"remaining":1,"periodEnd":"next month"}]}`,
		`{"models":[{"model":"m","tokensUsed":9007199254740992,"cap":1,"remaining":1}]}`,
	}
	for _, raw := range cases {
		if _, err := ParseNanQuota([]byte(raw)); err == nil {
			t.Fatalf("accepted invalid report %s", raw)
		}
	}
}

func TestParseNanQuotaPreservesOverCapWithoutSynthesizingRollingUsage(t *testing.T) {
	raw := []byte(`{"models":[{"model":"glm5.3","tokensUsed":1200,"cap":1000,"remaining":0,"windowHours":4}]}`)
	report, err := ParseNanQuota(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	model := report.Models[0]
	if model.TokensUsed != 1200 || model.Remaining != 0 || model.PeriodEnd != nil {
		t.Fatalf("row = %+v", model)
	}
	window := NormalizeNanQuota(report)[0]
	if *window.Used != 1200 || *window.Limit != 1000 || *window.UsedPercent != 100 {
		t.Fatalf("display bounds changed exact counters: %+v", window)
	}
	if window.ResetAtMS != nil || window.PeriodHours != nil || window.Usage != nil || window.Capacity != nil {
		t.Fatalf("unknown reset or rolling consumption invented: %+v", window)
	}
}

func TestParseNanQuotaIgnoresUnknownFieldsAndPreservesIndependentRemaining(t *testing.T) {
	raw := []byte(`{
		"periodStart":"2026-10-01",
		"accountId":"not-membership-evidence",
		"models":[{
			"model":"qwen3.8-flash",
			"tokensUsed":700,
			"cap":1000,
			"remaining":100,
			"periodEnd":"2026-11-09T15:06:22Z",
			"windowHours":4,
			"providerExtension":{"ignored":true}
		}]
	}`)
	report, err := ParseNanQuota(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	model := report.Models[0]
	if model.TokensUsed+model.Remaining == model.Cap {
		t.Fatal("fixture failed to prove remaining is independent")
	}
	if model.Remaining != 100 || model.TokensUsed != 700 || model.Cap != 1000 {
		t.Fatalf("counters = %+v", model)
	}
	window := NormalizeNanQuota(report)[0]
	if window.ResetAtMS == nil || window.PeriodHours != nil || window.RemainingPercent == nil || *window.RemainingPercent != 10 {
		t.Fatalf("remaining or billing period wrong: %+v", window)
	}
}

func TestParseNanQuotaRejectsUnsafeIntegers(t *testing.T) {
	raw := []byte(`{"models":[{"model":"m","tokensUsed":9007199254740992,"cap":1,"remaining":0}]}`)
	if _, err := ParseNanQuota(raw); err == nil {
		t.Fatal("accepted an integer above the safe boundary")
	}
}

func TestParseNanQuotaTreatsNullPeriodEndAsUnknownReset(t *testing.T) {
	raw := []byte(`{"models":[{"model":"qwen3.8-flash","tokensUsed":1,"cap":2,"remaining":1,"periodEnd":null,"windowHours":4}]}`)
	report, err := ParseNanQuota(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	model := report.Models[0]
	if model.PeriodEnd != nil || model.WindowHours == nil {
		t.Fatalf("model = %+v", model)
	}
	window := NormalizeNanQuota(report)[0]
	if window.ResetAtMS != nil || window.ResetAccuracy != "unknown" || window.PeriodHours != nil {
		t.Fatalf("window = %+v", window)
	}
}

func TestNanQuotaRequestUsesFixedEndpoint(t *testing.T) {
	request, err := NanQuotaRequest()
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if request.URL.String() != nanQuotaEndpoint || !strings.HasPrefix(request.URL.String(), "https://") {
		t.Fatalf("url = %s", request.URL)
	}
}
