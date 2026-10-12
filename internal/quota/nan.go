package quota

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	nanQuotaEndpoint  = "https://cloud-api.nan.builders/api/usage/quota"
	nanQuotaUserAgent = "Keel-quota/1"
	maxSafeInteger    = int64(1<<53 - 1)
)

// NanQuotaReport is one validated NaN dashboard quota response.
//
// AccountPeriodStart is display context only. It is not a model reset and does
// not establish that models or credentials share an allowance.
type NanQuotaReport struct {
	AccountPeriodStart *time.Time
	Models             []NanModelQuota
}

// NanModelQuota is one model's billing-period allowance.
//
// Remaining is retained separately because the provider publishes it directly.
// Rolling-window metadata is retained as metadata only; it is neither consumed
// usage nor the billing reset.
type NanModelQuota struct {
	Model            string
	TokensUsed       int64
	Cap              int64
	Remaining        int64
	PeriodEnd        *time.Time
	UpdatedAt        *time.Time
	WindowHours      *int64
	FullWindowTokens *int64
}

type nanQuotaPayload struct {
	PeriodStart string
	Models      []nanModelPayload
}

type nanModelPayload struct {
	Model            string
	TokensUsed       *int64
	Cap              *int64
	Remaining        *int64
	PeriodEnd        *time.Time
	UpdatedAt        *time.Time
	WindowHours      *int64
	FullWindowTokens *int64
	PeriodEndNull    bool
}

// ParseNanQuota validates a NaN quota document without inventing counters or
// reset times. A zero counter remains an observed zero. Missing, malformed,
// duplicate, and empty reports are rejected. Call NormalizeNanQuota to map the
// result into existing model-scoped windows; this parser does not construct a
// full NormalizedQuota or choose a credential.
func ParseNanQuota(raw []byte) (NanQuotaReport, error) {
	var report NanQuotaReport
	if len(bytes.TrimSpace(raw)) == 0 {
		return report, fmt.Errorf("nan quota report is empty")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	var payload nanQuotaPayload
	if err := decoder.Decode(&payload); err != nil {
		return report, fmt.Errorf("invalid nan quota report: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return report, fmt.Errorf("invalid nan quota report: trailing data")
	}
	if strings.TrimSpace(payload.PeriodStart) != "" {
		parsed, err := time.Parse(time.RFC3339, payload.PeriodStart)
		if err != nil {
			parsed, err = time.Parse("2006-01-02", payload.PeriodStart)
		}
		if err != nil {
			return report, fmt.Errorf("invalid nan account period start")
		}
		utc := parsed.UTC()
		report.AccountPeriodStart = &utc
	}
	if len(payload.Models) == 0 {
		return report, fmt.Errorf("nan quota report has no models")
	}
	seen := make(map[string]struct{}, len(payload.Models))
	report.Models = make([]NanModelQuota, 0, len(payload.Models))
	for _, model := range payload.Models {
		id := strings.TrimSpace(model.Model)
		if id == "" {
			return report, fmt.Errorf("nan quota model is empty")
		}
		if _, duplicate := seen[id]; duplicate {
			return report, fmt.Errorf("duplicate nan quota model %q", id)
		}
		seen[id] = struct{}{}
		if model.TokensUsed == nil || model.Cap == nil || model.Remaining == nil {
			return report, fmt.Errorf("nan quota model %q is missing counters", id)
		}
		if *model.TokensUsed < 0 || *model.Remaining < 0 || *model.Cap <= 0 ||
			*model.TokensUsed > maxSafeInteger || *model.Remaining > maxSafeInteger || *model.Cap > maxSafeInteger {
			return report, fmt.Errorf("nan quota model %q has invalid counters", id)
		}
		if model.WindowHours != nil && (*model.WindowHours <= 0 || *model.WindowHours > maxSafeInteger) {
			return report, fmt.Errorf("nan quota model %q has invalid rolling window", id)
		}
		if model.FullWindowTokens != nil && (*model.FullWindowTokens <= 0 || *model.FullWindowTokens > maxSafeInteger) {
			return report, fmt.Errorf("nan quota model %q has invalid rolling cap", id)
		}
		report.Models = append(report.Models, NanModelQuota{
			Model:            id,
			TokensUsed:       *model.TokensUsed,
			Cap:              *model.Cap,
			Remaining:        *model.Remaining,
			PeriodEnd:        model.PeriodEnd,
			UpdatedAt:        model.UpdatedAt,
			WindowHours:      model.WindowHours,
			FullWindowTokens: model.FullWindowTokens,
		})
	}
	sort.Slice(report.Models, func(i, j int) bool {
		return report.Models[i].Model < report.Models[j].Model
	})
	return report, nil
}

// NormalizeNanQuota converts validated rows into model-scoped quota windows.
// Percentages are display bounds; used, cap, and Remaining retain their exact
// values. RemainingPercent uses the provider's remaining/cap, clamped only for
// display. ResetAtMS comes only from a present periodEnd. Null or absent
// periodEnd leaves it nil; a JSON null is explicit unknown and an omission is
// also unknown. Rolling windowHours and
// fullWindowTokens stay on NanModelQuota and never become PeriodHours or
// consumed rolling usage.
func NormalizeNanQuota(report NanQuotaReport) []QuotaWindow {
	windows := make([]QuotaWindow, 0, len(report.Models))
	for _, model := range report.Models {
		used := float64(model.TokensUsed)
		limit := float64(model.Cap)
		usedPercent := math.Min(used/limit*100, 100)
		remainingPercent := math.Min(math.Max(float64(model.Remaining)/limit*100, 0), 100)
		window := QuotaWindow{
			ID:               "nan:" + model.Model,
			Label:            model.Model,
			Kind:             "model_scoped",
			Scope:            "model",
			Model:            model.Model,
			Used:             &used,
			Limit:            &limit,
			UsedPercent:      &usedPercent,
			RemainingPercent: &remainingPercent,
		}
		if model.PeriodEnd != nil {
			reset := model.PeriodEnd.UTC().UnixMilli()
			window.ResetAtMS = &reset
			window.ResetAccuracy = "exact"
		} else if model.PeriodEnd == nil {
			window.ResetAccuracy = "unknown"
		}
		windows = append(windows, window)
	}
	return windows
}

func (payload *nanQuotaPayload) UnmarshalJSON(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	var fields struct {
		Models      json.RawMessage `json:"models"`
		PeriodStart string          `json:"periodStart"`
	}
	if err := decoder.Decode(&fields); err != nil {
		return err
	}
	if len(bytes.TrimSpace(fields.Models)) == 0 {
		return fmt.Errorf("models field is required")
	}
	modelDecoder := json.NewDecoder(bytes.NewReader(fields.Models))
	if err := modelDecoder.Decode(&payload.Models); err != nil {
		return fmt.Errorf("models: %w", err)
	}
	payload.PeriodStart = fields.PeriodStart
	return nil
}

func (payload *nanModelPayload) UnmarshalJSON(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	var fields map[string]json.RawMessage
	if err := decoder.Decode(&fields); err != nil {
		return err
	}
	var model string
	if err := strictString(fields, "model", &model); err != nil {
		return err
	}
	used, err := strictInt(fields, "tokensUsed", true)
	if err != nil {
		return err
	}
	cap, err := strictInt(fields, "cap", true)
	if err != nil {
		return err
	}
	remaining, err := strictInt(fields, "remaining", true)
	if err != nil {
		return err
	}
	periodEnd, periodEndNull, err := strictOptionalTime(fields, "periodEnd")
	if err != nil {
		return err
	}
	updatedAt, err := strictTime(fields, "updatedAt", false)
	if err != nil {
		return err
	}
	windowHours, err := strictInt(fields, "windowHours", false)
	if err != nil {
		return err
	}
	fullWindow, err := strictInt(fields, "fullWindowTokens", false)
	if err != nil {
		return err
	}
	payload.Model = model
	payload.TokensUsed = used
	payload.Cap = cap
	payload.Remaining = remaining
	payload.PeriodEnd = periodEnd
	payload.PeriodEndNull = periodEndNull
	payload.UpdatedAt = updatedAt
	payload.WindowHours = windowHours
	payload.FullWindowTokens = fullWindow
	return nil
}

func strictString(fields map[string]json.RawMessage, key string, target *string) error {
	raw, ok := fields[key]
	if !ok {
		return fmt.Errorf("%s is required", key)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("%s: %w", key, err)
	}
	return nil
}

func strictInt(fields map[string]json.RawMessage, key string, required bool) (*int64, error) {
	raw, ok := fields[key]
	if !ok {
		if required {
			return nil, fmt.Errorf("%s is required", key)
		}
		return nil, nil
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, fmt.Errorf("%s is null", key)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("%s: %w", key, err)
	}
	number, ok := value.(json.Number)
	if !ok {
		return nil, fmt.Errorf("%s is not an integer", key)
	}
	parsed, err := strconv.ParseInt(number.String(), 10, 64)
	if err != nil || parsed > maxSafeInteger {
		return nil, fmt.Errorf("%s is not a safe integer", key)
	}
	return &parsed, nil
}

func strictOptionalTime(fields map[string]json.RawMessage, key string) (*time.Time, bool, error) {
	raw, ok := fields[key]
	if !ok {
		return nil, false, nil
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, true, nil
	}
	parsed, err := strictTime(fields, key, false)
	return parsed, false, err
}

func strictTime(fields map[string]json.RawMessage, key string, required bool) (*time.Time, error) {
	raw, ok := fields[key]
	if !ok {
		if required {
			return nil, fmt.Errorf("%s is required", key)
		}
		return nil, nil
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	var text string
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(&text); err != nil {
		return nil, fmt.Errorf("%s: %w", key, err)
	}
	parsed, err := time.Parse(time.RFC3339, text)
	if err != nil {
		return nil, fmt.Errorf("%s is not RFC3339", key)
	}
	utc := parsed.UTC()
	return &utc, nil
}

// NanQuotaRequest returns the fixed dashboard quota request. The caller owns
// credential selection and server-side substitution; this adapter never accepts
// a caller-selected URL.
func NanQuotaRequest() (*http.Request, error) {
	request, err := http.NewRequest(http.MethodGet, nanQuotaEndpoint, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	return request, nil
}
