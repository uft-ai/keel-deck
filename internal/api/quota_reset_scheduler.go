package api

import (
	"context"
	"encoding/json"
	"time"

	"github.com/oh-my-cpa/oh-my-cpa/internal/quota"
)

// RefreshDueQuota refreshes each subject whose known reset is already due.
// It uses the same configured-subject lookup and model-aware quota refresh as
// an explicit console refresh. A subject with no known reset is not called.
func (h *Handler) RefreshDueQuota(ctx context.Context) error {
	if h == nil || h.repo == nil {
		return nil
	}
	now := h.now().UTC()
	h.quotaRetryMu.Lock()
	snapshots, err := h.repo.LatestQuotaWindows(ctx)
	if err != nil {
		h.quotaRetryMu.Unlock()
		return err
	}
	defer h.quotaRetryMu.Unlock()
	if h.quotaRetryAfter == nil {
		h.quotaRetryAfter = map[string]time.Time{}
	}
	client, err := h.capabilityClient(ctx)
	if err != nil {
		return err
	}
	subjects, err := quotaSubjects(ctx, client)
	if err != nil {
		return err
	}
	for authIndex, snapshot := range snapshots {
		var windows []quota.QuotaWindow
		if err := json.Unmarshal([]byte(snapshot.WindowsJSON), &windows); err != nil {
			continue
		}
		due := quota.NextKnownReset(windows, time.Time{})
		pending := pendingPeriodDelivery(windows)
		if !pending && (due == nil || due.After(now)) {
			continue
		}
		subject, found := findQuotaSubject(subjects, authIndex)
		if !found || subject.File.Disabled || now.Before(h.quotaRetryAfter[authIndex]) {
			continue
		}
		h.quotaRetryAfter[authIndex] = quota.BackoffAfter(now, 1)
		refreshed, refreshErr := quota.NewService(client).RefreshCredentialQuotaModels(ctx, subject.File, h.loadPriorNormalizedQuota(ctx, authIndex), subject.Models)
		if refreshErr != nil || refreshed == nil || refreshed.Status == "error" || refreshed.Status == "stale" {
			continue
		}
		if err := h.persistNormalizedQuotaSnapshot(ctx, refreshed); err != nil {
			continue
		}
	}
	return nil
}

func pendingPeriodDelivery(windows []quota.QuotaWindow) bool {
	for _, window := range windows {
		if window.PeriodDeliveryPending {
			return true
		}
	}
	return false
}

func markPendingPeriodDelivery(reading *quota.NormalizedQuota) {
	if reading == nil {
		return
	}
	pending := map[string]bool{}
	for _, delivery := range reading.PeriodDeliveries {
		if delivery.Status == "unavailable" || delivery.Status == "rejected" {
			pending[delivery.Model] = true
		}
	}
	if len(pending) == 0 {
		return
	}
	for index := range reading.Windows {
		model := reading.Windows[index].Model
		if model == "" || pending[model] {
			reading.Windows[index].PeriodDeliveryPending = true
		}
	}
}
