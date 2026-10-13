# ADR 0095: Background quota refresh covers configured NaN keys

- Status: Accepted
- Date: 2026-10-13
- Supersedes ADR 0094's credential list (auth files only), its use of
  `RefreshCredentialQuota` and its two-trigger order; retains its loop, policy,
  bounds, persistence rule and Meta exclusion.

## Context

ADR 0094 reads due credentials in the background, but it lists only CPA's auth files.
A NaN key is configured in CPA's OpenAI-compatibility list, not stored as an auth file,
so the loop never read it. NaN quota is also the only reading that delivers an
observation to Relay's period ledger: a key exhausted for its monthly period stays
blocked in Relay until a reading after the renewal reaches that ledger.

A NaN reading and its delivery can fail separately. The quota read can succeed and be
saved while Relay's ledger answers `503`, or a non-HTTP failure prevents the
delivery. The saved reading then carries a future renewal, so no reset trigger fires,
and the next scheduled read can be six hours away. Relay can also refuse a delivery
(`409`: a stale or conflicting observation, or a renewal not yet verified); reading
again at once would only be refused again.

An earlier fork loop scanned known resets every minute with exponential backoff. It
ran beside ADR 0094's loop, ignored its policy and kept retrying a steady failure.

## Decision

**One loop.** ADR 0094's `RunQuotaAutoRefresh` is the only background quota reader.
The earlier fork loop and its helpers are removed.

**Configured NaN keys are credentials.** A round lists CPA's auth files and the
configured NaN keys (`NanConfiguredQuotaSubjects`). An `auth_index` claimed more than
once across both lists is skipped entirely, as ADR 0094 does for auth files. Every
read uses `RefreshCredentialQuotaModels` with the key's exact configured models; an
auth file passes none. A NaN read keeps its order: a fresh ledger `GET` per model,
then the provider read, then one `PUT` per model.

**An unavailable delivery is retried after ten minutes.** Each stored window records
why its observation is still owed: `period_delivery_status` is `unavailable` or
`rejected`, next to the existing `period_delivery_pending`. A window with no model
takes `unavailable` when the models differ. A reading whose window says
`unavailable` is due again by a third trigger, `delivery`, once ten minutes
(`AUTO_REFRESH_RESET_RETRY_BACKOFF`) have passed since the newer of its observation
and the process's last attempt. The observation time is stamped when the read starts,
so the cadence counts from the start of the read that failed. A restarted process
uses the stored status alone. It is a target, not a recovery guarantee: rounds are
bounded and a ledger that keeps failing keeps waiting.

A rejected delivery, an observation that was never submitted, and a reading saved
before the status existed take no early retry; they wait for the next reset-driven or
scheduled read. A successful delivery stores no status, which ends the retries. The
quota reading is saved even when its delivery fails, because it is still the best
reading. The delivery trigger follows the policy: with both controls off, nothing is
read automatically and a manual refresh is the only delivery.

**Order.** Due credentials are ranked reset (0), delivery (1), interval (2), then by
oldest observation, then by `auth_index`, so a round's order does not depend on the
order CPA lists credentials. An undelivered observation keeps a renewed key blocked,
which matters more than keeping a reading young.

## Consequences

- A configured NaN key is read on the six-hour schedule and after its reported
  renewal like any credential, with its configured models.
- Relay learns of a renewal within about ten minutes of its ledger recovering, without
  an operator, while a refusal is not repeated every ten minutes.
- No table, migration, preference or setting is added: the status lives in the
  existing window JSON and the cadence reuses ADR 0094's retry constant.
