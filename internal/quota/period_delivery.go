package quota

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/oh-my-cpa/oh-my-cpa/internal/cpa/management"
	"github.com/oh-my-cpa/oh-my-cpa/internal/security"
)

// periodReads is the ledger state a provider read is based on, read before that
// provider read so each observation names the version and time it follows.
type periodReads struct {
	ledger PeriodLedger
	models []string
	reads  map[string]management.PeriodRead
	failed bool
}

// readPeriodLedger reads the ledger record of each exact configured model before
// the provider read. A failed read is recorded on result as a delivery status;
// that model then gets no observation from this refresh.
func readPeriodLedger(ctx context.Context, client CPAClient, result *NormalizedQuota, models []string) (*periodReads, error) {
	ledger, hasLedger := client.(PeriodLedger)
	if !hasLedger {
		return nil, errors.New("period ledger is not configured")
	}
	models = exactModels(models)
	if len(models) == 0 {
		return nil, errors.New("quota refresh has no configured model")
	}
	state := &periodReads{ledger: ledger, models: models, reads: make(map[string]management.PeriodRead, len(models))}
	for _, model := range models {
		read, err := ledger.GetPeriodExhaustion(ctx, result.AuthIndex, model)
		if err != nil {
			state.failed = true
			result.PeriodDeliveries = append(result.PeriodDeliveries, PeriodDelivery{Model: model, Status: periodDeliveryStatus(err), Error: security.RedactText(err.Error())})
			continue
		}
		state.reads[model] = read
	}
	return state, nil
}

// markBlocked shows the ledger's current block on each window of a read model.
func (state *periodReads) markBlocked(result *NormalizedQuota) {
	for i := range result.Windows {
		if read, ok := state.reads[result.Windows[i].Model]; ok {
			blocked := read.Record != nil && read.Record.Blocked
			result.Windows[i].InferenceBlocked = &blocked
		}
	}
}

// deliver writes one observation per model whose ledger read succeeded. observe
// maps the provider's reading for one model; it receives the version and
// observed_at of that model's earlier read. No ledger read happens here.
func (state *periodReads) deliver(ctx context.Context, result *NormalizedQuota, observe func(id string, model string, version uint64, read management.PeriodRead) (management.PeriodObservation, error)) {
	fetchID := uuid.NewString()
	for _, model := range state.models {
		read, ok := state.reads[model]
		if !ok {
			continue
		}
		version := uint64(0)
		if read.Record != nil {
			version = read.Record.Version
		}
		observation, err := observe(fmt.Sprintf("deck-%s-%s-%d-%s", result.AuthIndex, model, version, fetchID), model, version, read)
		if err != nil {
			result.PeriodDeliveries = append(result.PeriodDeliveries, PeriodDelivery{Model: model, Status: "not_submitted", Error: err.Error()})
			continue
		}
		record, err := state.ledger.PutPeriodExhaustion(ctx, observation)
		if err != nil {
			result.PeriodDeliveries = append(result.PeriodDeliveries, PeriodDelivery{Model: model, State: observation.State, Status: periodDeliveryStatus(err), Error: security.RedactText(err.Error())})
			continue
		}
		blocked := record.Blocked
		result.PeriodDeliveries = append(result.PeriodDeliveries, PeriodDelivery{Model: model, State: observation.State, Status: "accepted", Version: record.Version, Blocked: &blocked})
		for i := range result.Windows {
			if result.Windows[i].Model == model {
				result.Windows[i].InferenceBlocked = &blocked
			}
		}
	}
}

func exactModels(models []string) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(models))
	for _, model := range models {
		name := strings.TrimSpace(model)
		if name == "" {
			continue
		}
		if _, duplicate := seen[name]; duplicate {
			continue
		}
		seen[name] = struct{}{}
		result = append(result, name)
	}
	return result
}

func periodDeliveryStatus(err error) string {
	var httpErr *management.HTTPError
	if errors.As(err, &httpErr) {
		switch httpErr.StatusCode {
		case 409:
			return PeriodDeliveryRejected
		case 503:
			return PeriodDeliveryUnavailable
		default:
			return PeriodDeliveryRejected
		}
	}
	return PeriodDeliveryUnavailable
}
