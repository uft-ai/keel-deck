package management

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const periodExhaustionEndpoint = "/availability/period-exhaustion"

// PeriodObservation is one subject Deck submits to Relay's period ledger.
//
// Names match Relay 43019742. based_on_version is always sent, including zero.
// pool_id is present and empty unless a verified mapping exists. There is no
// source, reset_at, or blocked_period_end field on this wire.
type PeriodObservation struct {
	ObservationID      string     `json:"observation_id"`
	BasedOnVersion     uint64     `json:"based_on_version"`
	State              string     `json:"state"`
	AuthIndex          string     `json:"auth_index"`
	Model              string     `json:"model"`
	PeriodEnd          *time.Time `json:"period_end"`
	AccountPeriodStart *time.Time `json:"account_period_start"`
	Remaining          *int64     `json:"remaining"`
	NotBefore          time.Time  `json:"not_before"`
	PoolID             string     `json:"pool_id"`
}

// PeriodRecord is the ledger row Relay returns. Deck displays it and does not
// treat it as its own enforcement decision.
type PeriodRecord struct {
	AuthIndex          string     `json:"auth_index"`
	Model              string     `json:"model"`
	Blocked            bool       `json:"blocked"`
	Version            uint64     `json:"version"`
	PeriodEnd          *time.Time `json:"period_end"`
	ExhaustedAt        *time.Time `json:"exhausted_at"`
	AccountPeriodStart *time.Time `json:"account_period_start"`
	UpdatedAt          time.Time  `json:"updated_at"`
	ResetUnknown       bool       `json:"reset_unknown"`
}

// PeriodRead is a GET result. ObservedAt is Relay's clock and becomes the
// next observation's not_before; it is not the row's update time.
type PeriodRead struct {
	Record     *PeriodRecord `json:"record"`
	ObservedAt time.Time     `json:"observed_at"`
}

type periodWriteResponse struct {
	Record PeriodRecord `json:"record"`
}

// GetPeriodExhaustion reads one subject before a quota fetch starts.
// A missing row is a nil Record with version 0 for the caller to echo.
func (c *Client) GetPeriodExhaustion(ctx context.Context, authIndex, model string) (PeriodRead, error) {
	var read PeriodRead
	query := url.Values{}
	query.Set("auth_index", strings.TrimSpace(authIndex))
	query.Set("model", model)
	endpoint := periodExhaustionEndpoint + "?" + query.Encode()
	if err := c.DoJSON(ctx, "GET", endpoint, &read); err != nil {
		return PeriodRead{}, err
	}
	return read, nil
}

// PutPeriodExhaustion submits one observation. The caller has already read
// the subject and fetched quota after that read.
func (c *Client) PutPeriodExhaustion(ctx context.Context, observation PeriodObservation) (PeriodRecord, error) {
	body, err := json.Marshal(observation)
	if err != nil {
		return PeriodRecord{}, fmt.Errorf("encode period observation: %w", err)
	}
	if len(body) > 64*1024 {
		return PeriodRecord{}, fmt.Errorf("period observation exceeds 64 KiB")
	}
	var response periodWriteResponse
	if err := c.doBody(ctx, "PUT", periodExhaustionEndpoint, body, "application/json", &response); err != nil {
		return PeriodRecord{}, err
	}
	return response.Record, nil
}
