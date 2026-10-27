package api

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/oh-my-cpa/oh-my-cpa/internal/cpa/management"
	"github.com/oh-my-cpa/oh-my-cpa/internal/quota"
	"github.com/oh-my-cpa/oh-my-cpa/internal/repository"
)

// Automatic quota refresh cadence. The loop wakes every minute, which is the resolution a
// reset-driven read needs. A wake with nothing due makes no provider request: it costs the
// settings read, CPA's credential list and the latest stored observations, the same reads
// the dashboard's Credential Quota panel already makes each minute.
const (
	QUOTA_AUTO_REFRESH_TICK = time.Minute
	// A round reads at most this many credentials, and the rest are due again on the next
	// tick. A fleet whose windows all reset at one instant would otherwise turn a single
	// minute into a burst of credential-bearing requests through CPA.
	QUOTA_AUTO_REFRESH_ROUND_LIMIT = 16
	// The same concurrency bound the manual batch refresh uses.
	QUOTA_AUTO_REFRESH_CONCURRENCY = 4
)

// quotaAutoRefreshState remembers when this process last tried each credential. It lives
// in memory because it only exists to stop a failing read from repeating every tick: a
// failed read is never persisted, so the stored observation alone would keep it due.
type quotaAutoRefreshState struct {
	mu             sync.Mutex
	lastAttemptsMS map[string]int64
	// lastRoundError keeps a gateway that stays unreachable from logging the same warning
	// every minute: a failure is logged when it starts or changes, and recovery once.
	lastRoundError string
	// lastDiagnostics is the ambiguous index set last warned about, in the same once-per-change way.
	lastDiagnostics string
}

// quotaAutoRefreshRound summarizes one round for the service log.
type quotaAutoRefreshRound struct {
	Due              int
	Refreshed        int
	Failed           int
	ByReset          int
	CooldownsCleared int
	// Deferred reads were skipped because the provider asked to wait; they stay due.
	Deferred int
}

// RunQuotaAutoRefresh reads due credentials' quotas in the background until ctx ends.
//
// It is best effort in the way the pricing sync is: a round that cannot reach CPA leaves
// the stored readings as they were, and the next tick tries again. The policy is read on
// every tick, so a change made in the console takes effect within a minute.
func (h *Handler) RunQuotaAutoRefresh(ctx context.Context) error {
	ticker := time.NewTicker(QUOTA_AUTO_REFRESH_TICK)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			round, err := h.runQuotaAutoRefreshRound(ctx)
			if ctx.Err() != nil {
				return ctx.Err()
			}
			h.logQuotaAutoRefreshFailure(err)
			if round.Due > 0 && h.logger != nil {
				h.logger.Info("automatic quota refresh round",
					"due", round.Due, "refreshed", round.Refreshed, "failed", round.Failed,
					"after_reset", round.ByReset, "cooldowns_cleared", round.CooldownsCleared, "deferred", round.Deferred)
			}
		}
	}
}

// runQuotaAutoRefreshRound performs one tick's work and reports what it did.
func (h *Handler) runQuotaAutoRefreshRound(ctx context.Context) (quotaAutoRefreshRound, error) {
	if h.repo == nil {
		return quotaAutoRefreshRound{}, nil
	}
	raw, isStored, err := h.repo.GetPreference(ctx, repository.PreferenceQuotaAutoRefresh)
	if err != nil {
		return quotaAutoRefreshRound{}, err
	}
	settings := quota.ResolveAutoRefreshSettings(raw, isStored)
	if !settings.IsEnabled() {
		return quotaAutoRefreshRound{}, nil
	}

	instance, err := h.repo.GetInstance(ctx, defaultInstanceID())
	if errors.Is(err, sql.ErrNoRows) {
		// A deployment with no gateway configured yet has nothing to read.
		return quotaAutoRefreshRound{}, nil
	}
	if err != nil {
		return quotaAutoRefreshRound{}, err
	}
	client, err := h.clientForInstance(ctx, instance)
	if err != nil {
		return quotaAutoRefreshRound{}, err
	}
	// The same subject list as the quota pages: auth files and configured keys, with an
	// index more than one credential claims left out and reported once.
	subjects, err := quotaSubjects(ctx, client)
	if err != nil {
		return quotaAutoRefreshRound{}, err
	}
	h.logQuotaSubjectDiagnostics(subjects.Diagnostics)
	subjectsByIndex := make(map[string]quotaSubject, len(subjects.Subjects))
	filesByIndex := make(map[string]management.AuthFile, len(subjects.Subjects))
	authIndexes := make([]string, 0, len(subjects.Subjects))
	for _, subject := range subjects.Subjects {
		authIndex := strings.TrimSpace(subject.File.AuthIndex)
		subjectsByIndex[authIndex] = subject
		filesByIndex[authIndex] = subject.File
		authIndexes = append(authIndexes, authIndex)
	}
	if len(authIndexes) == 0 {
		return quotaAutoRefreshRound{}, nil
	}
	snapshots, err := h.repo.GetLatestQuotaSnapshots(ctx, authIndexes)
	if err != nil {
		return quotaAutoRefreshRound{}, err
	}

	// A cooldown whose recovery times have all passed still blocks the credential until its
	// evidence is cleared. BatchCorrelatedCooldowns cannot find it: its six-hour lookback
	// drops evidence that expired earlier, which is exactly the evidence nobody cleared.
	nowMS := h.now().UnixMilli()
	var expiredCooldowns map[string]int64
	if h.repo != nil {
		expired, err := h.repo.BatchExpiredCooldowns(ctx, authIndexes, nowMS)
		if err != nil && h.logger != nil {
			h.logger.Warn("expired cooldown lookup failed", "error", err.Error())
		}
		expiredCooldowns = expired
	}

	type dueCredential struct {
		subject             quotaSubject
		prior               *quota.NormalizedQuota
		trigger             string
		observedMS          int64
		cooldownRecoverAtMS int64
	}
	lastAttempts := h.quotaAutoRefresh.snapshotAttempts()
	due := make([]dueCredential, 0)
	for _, authIndex := range authIndexes {
		subject := subjectsByIndex[authIndex]
		file := subject.File
		candidate := quota.AutoRefreshCandidate{
			Provider:        quota.DetectProvider(file.Type, file.Provider),
			IsDisabled:      file.Disabled,
			IsSupported:     quota.CapabilitiesForFile(file).RefreshSupported,
			LastAttemptAtMS: lastAttempts[authIndex],
		}
		var prior *quota.NormalizedQuota
		if snapshot, ok := snapshots[authIndex]; ok {
			prior = normalizedQuotaFromSnapshot(authIndex, snapshot)
			candidate.LastObservedAtMS = snapshot.ObservedAtMS
			candidate.ResetAtMS = quota.WindowResetTimes(prior.Windows)
			candidate.HasUnavailableDelivery = quota.HasUnavailablePeriodDelivery(prior.Windows)
		}
		if recoverAtMS, ok := expiredCooldowns[authIndex]; ok {
			candidate.ActiveCooldownExpiredAtMS = recoverAtMS
		}
		if gate := h.quotaReadGates.forFile(file); gate != nil {
			candidate.ReadBackoffUntilMS = gate.scheduledReadAt(authIndex)
		}
		if trigger := quota.AutoRefreshTrigger(settings, candidate, nowMS); trigger != quota.AutoRefreshTriggerNone {
			due = append(due, dueCredential{subject: subject, prior: prior, trigger: trigger, observedMS: candidate.LastObservedAtMS, cooldownRecoverAtMS: expiredCooldowns[authIndex]})
		}
	}
	// Reset-driven reads go first, then undelivered observations, then scheduled reads;
	// within each, the oldest reading first.
	slices.SortFunc(due, func(a, b dueCredential) int {
		return cmp.Or(
			cmp.Compare(quota.AutoRefreshTriggerRank(a.trigger), quota.AutoRefreshTriggerRank(b.trigger)),
			cmp.Compare(a.observedMS, b.observedMS),
			strings.Compare(a.subject.File.AuthIndex, b.subject.File.AuthIndex),
		)
	})
	round := quotaAutoRefreshRound{Due: len(due)}
	if len(due) > QUOTA_AUTO_REFRESH_ROUND_LIMIT {
		due = due[:QUOTA_AUTO_REFRESH_ROUND_LIMIT]
	}

	svc := quota.NewService(client)
	var wg sync.WaitGroup
	var countMu sync.Mutex
	sem := make(chan struct{}, QUOTA_AUTO_REFRESH_CONCURRENCY)
	for _, credential := range due {
		authIndex := strings.TrimSpace(credential.subject.File.AuthIndex)
		previousAttemptMS := lastAttempts[authIndex]
		h.quotaAutoRefresh.recordAttempt(authIndex, nowMS)
		wg.Add(1)
		go func(subject quotaSubject, prior *quota.NormalizedQuota, trigger string, recoverAtMS int64) {
			file := subject.File
			defer wg.Done()
			if trigger == quota.AutoRefreshTriggerCooldownExpired || h.holdsCallerSlot(file) {
				sem <- struct{}{}
				defer func() { <-sem }()
			}

			// An expired cooldown needs a clear, not a read: the same CPA reset and evidence
			// wipe the operator's Clear Cooldown performs.
			if trigger == quota.AutoRefreshTriggerCooldownExpired {
				authIndex := strings.TrimSpace(file.AuthIndex)
				clearErr := svc.ClearCPACooldown(ctx, authIndex)
				var evidenceErr error
				if clearErr == nil && h.repo != nil {
					evidenceErr = h.repo.ClearCooldownEvidence(ctx, authIndex)
				}
				countMu.Lock()
				defer countMu.Unlock()
				if clearErr != nil || evidenceErr != nil {
					round.Failed++
				} else {
					round.CooldownsCleared++
				}
				h.recordAutomaticCooldownClear(authIndex, "recovery_time_passed", clearErr, evidenceErr)
				h.logExpiredCooldownClear(authIndex, recoverAtMS, clearErr, evidenceErr)
				return
			}

			refreshed, err := h.refreshAutomaticQuotaReading(ctx, svc, file, prior, subject.Models)
			if err == nil && refreshed != nil && refreshed.IsDeferred {
				// Nothing was sent, so the attempt this round recorded is taken back: the read
				// stays due as it was and goes out once the provider's pause has passed.
				h.quotaAutoRefresh.restoreAttempt(authIndex, previousAttemptMS)
				countMu.Lock()
				round.Deferred++
				countMu.Unlock()
				return
			}
			// The same persistence rule as a manual refresh: a failed or stale read
			// never replaces the last good observation.
			isRefreshed := err == nil && refreshed != nil
			if isRefreshed {
				if isUsable, _ := quota.ReadOutcome(refreshed, nil); !isUsable {
					isRefreshed = false
				} else if persistErr := h.persistNormalizedQuotaSnapshot(ctx, refreshed); persistErr != nil {
					isRefreshed = false
				}
			}
			countMu.Lock()
			defer countMu.Unlock()
			if !isRefreshed {
				round.Failed++
				return
			}
			round.Refreshed++
			if trigger == quota.AutoRefreshTriggerReset {
				round.ByReset++
			}
		}(credential.subject, credential.prior, credential.trigger, credential.cooldownRecoverAtMS)
	}
	wg.Wait()
	h.quotaAutoRefresh.forgetExcept(filesByIndex)
	h.quotaReadGates.forgetExcept(filesByIndex)
	return round, nil
}

func (h *Handler) logQuotaAutoRefreshFailure(err error) {
	message := ""
	if err != nil {
		message = err.Error()
	}
	h.quotaAutoRefresh.mu.Lock()
	previous := h.quotaAutoRefresh.lastRoundError
	h.quotaAutoRefresh.lastRoundError = message
	h.quotaAutoRefresh.mu.Unlock()
	if h.logger == nil || message == previous {
		return
	}
	if message == "" {
		h.logger.Info("automatic quota refresh rounds recovered")
		return
	}
	h.logger.Warn("automatic quota refresh round failed", "error", message)
}

// logQuotaSubjectDiagnostics warns when the set of ambiguous auth indexes changes, so
// a lasting conflict is logged once rather than every minute.
func (h *Handler) logQuotaSubjectDiagnostics(diagnostics []quotaDiagnostic) {
	parts := make([]string, 0, len(diagnostics))
	for _, diagnostic := range diagnostics {
		parts = append(parts, fmt.Sprintf("%s:%d", diagnostic.AuthIndex, diagnostic.Count))
	}
	current := strings.Join(parts, ",")
	h.quotaAutoRefresh.mu.Lock()
	previous := h.quotaAutoRefresh.lastDiagnostics
	h.quotaAutoRefresh.lastDiagnostics = current
	h.quotaAutoRefresh.mu.Unlock()
	if h.logger == nil || current == previous || current == "" {
		return
	}
	h.logger.Warn("automatic quota refresh skips auth indexes claimed by more than one credential",
		"reason", duplicateAuthIndex, "auth_indexes", current)
}

func (s *quotaAutoRefreshState) snapshotAttempts() map[string]int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return maps.Clone(s.lastAttemptsMS)
}

func (s *quotaAutoRefreshState) recordAttempt(authIndex string, nowMS int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lastAttemptsMS == nil {
		s.lastAttemptsMS = make(map[string]int64)
	}
	s.lastAttemptsMS[authIndex] = nowMS
}

// restoreAttempt puts back the attempt a round recorded for a read it did not send.
func (s *quotaAutoRefreshState) restoreAttempt(authIndex string, previousMS int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if previousMS == 0 {
		delete(s.lastAttemptsMS, authIndex)
		return
	}
	s.lastAttemptsMS[authIndex] = previousMS
}

// forgetExcept drops attempts for credentials CPA no longer lists, so the map is bounded
// by the fleet rather than by every index the process has ever seen.
func (s *quotaAutoRefreshState) forgetExcept(current map[string]management.AuthFile) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for authIndex := range s.lastAttemptsMS {
		if _, ok := current[authIndex]; !ok {
			delete(s.lastAttemptsMS, authIndex)
		}
	}
}

// logExpiredCooldownClear records every automatic release by credential, so an operator can see
// which credential was released and when its recovery time had passed.
func (h *Handler) logExpiredCooldownClear(authIndex string, recoverAtMS int64, clearErr, evidenceErr error) {
	if clearErr != nil || evidenceErr != nil {
		h.logCooldownClearFailure("expired CPA cooldown clear failed", authIndex, recoverAtMS, clearErr, evidenceErr)
		return
	}
	if h.logger != nil {
		h.logger.Info("expired CPA cooldown cleared", "auth_index", authIndex, "recover_at_ms", recoverAtMS)
	}
}

// logCooldownClearFailure names which step of an automatic release failed. CPA errors can carry
// upstream bodies, so only their status is kept, as the redemption path does.
func (h *Handler) logCooldownClearFailure(message, authIndex string, recoverAtMS int64, clearErr, evidenceErr error) {
	if h.logger == nil {
		return
	}
	attrs := []any{"auth_index", authIndex}
	if recoverAtMS > 0 {
		attrs = append(attrs, "recover_at_ms", recoverAtMS)
	}
	if clearErr != nil {
		attrs = append(attrs, "step", "cpa_reset", "status_code", cpaErrorStatus(clearErr))
	} else {
		attrs = append(attrs, "step", "evidence_wipe", "error", evidenceErr.Error())
	}
	h.logger.Warn(message, attrs...)
}

// recordAutomaticCooldownClear keeps an automatic release in the audit trail beside the
// operator's quota.clear_cooldown, because both change what CPA routes to.
func (h *Handler) recordAutomaticCooldownClear(authIndex, reason string, clearErr, evidenceErr error) {
	result, details := "success", map[string]any{"reason": reason}
	switch {
	case clearErr != nil:
		result, details["step"], details["status_code"] = "failure", "cpa_reset", cpaErrorStatus(clearErr)
	case evidenceErr != nil:
		result, details["step"] = "failure", "evidence_wipe"
	}
	h.recordAuditWithoutRequest("quota.clear_cooldown.automatic", "quota", authIndex, result, details)
}

// cpaErrorStatus is the HTTP status of a CPA management error, or zero when it never answered.
func cpaErrorStatus(err error) int {
	var httpError *management.HTTPError
	if errors.As(err, &httpError) {
		return httpError.StatusCode
	}
	return 0
}
