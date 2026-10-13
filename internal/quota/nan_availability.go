package quota

import (
	"fmt"
	"strings"
	"time"

	"github.com/oh-my-cpa/oh-my-cpa/internal/cpa/management"
)

// PeriodObservationInput is the evidence one NaN model can submit.
//
// Relay decides whether it blocks or clears. This mapping only refuses a
// report that does not identify one model period.
type PeriodObservationInput struct {
	ObservationID  string
	BasedOnVersion uint64
	AuthIndex      string
	Model          string
	NotBefore      time.Time
	Report         NanQuotaReport
}

// NanPeriodObservation maps one validated model row to Relay's reduced schema.
// The selected model must exist once. Exhausted means remaining is zero or
// tokens used have reached the cap. Positive remaining with no periodEnd stays
// available; Relay decides whether that can renew a block.
func NanPeriodObservation(input PeriodObservationInput) (management.PeriodObservation, error) {
	var observation management.PeriodObservation
	authIndex := strings.TrimSpace(input.AuthIndex)
	model := strings.TrimSpace(input.Model)
	if input.ObservationID == "" || authIndex == "" || model == "" || input.NotBefore.IsZero() {
		return observation, fmt.Errorf("period observation is missing its identity")
	}
	var matched *NanModelQuota
	for index := range input.Report.Models {
		if input.Report.Models[index].Model == model {
			if matched != nil {
				return observation, fmt.Errorf("duplicate nan quota model %q", model)
			}
			matched = &input.Report.Models[index]
		}
	}
	if matched == nil {
		return observation, fmt.Errorf("nan quota model %q was not observed", model)
	}
	state := "available"
	if matched.Remaining == 0 || matched.TokensUsed >= matched.Cap {
		state = "exhausted"
	}
	remaining := matched.Remaining
	observation = management.PeriodObservation{
		ObservationID:      input.ObservationID,
		BasedOnVersion:     input.BasedOnVersion,
		State:              state,
		AuthIndex:          authIndex,
		Model:              model,
		PeriodEnd:          matched.PeriodEnd,
		AccountPeriodStart: input.Report.AccountPeriodStart,
		Remaining:          &remaining,
		NotBefore:          input.NotBefore.UTC(),
		PoolID:             "",
	}
	return observation, nil
}
