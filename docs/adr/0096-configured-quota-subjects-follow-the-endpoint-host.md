# ADR 0096: Configured quota subjects follow the endpoint host

- Status: Accepted
- Date: 2026-10-13
- Supersedes ADR 0095's subject rule (`NanConfiguredQuotaSubjects`, which also
  accepted a provider named `nan`) and the first-claim choice of manual refresh;
  retains its loop, delivery retry, order and persistence.

## Context

A configured OpenAI-compatible key became a NaN quota subject when its provider was
named `nan` or its base URL host was `api.nan.builders`. A provider name is a label
the operator chooses. An entry named `nan` on another host still had its key sent to
NaN's quota endpoint on every scheduled and manual read.

An auth index claimed by more than one credential was skipped by the scheduler, but
the overview and manual refresh kept the first claim, so a login file silently won
over a configured key with the same index.

The period-ledger steps of a NaN read sat inside the NaN provider case, although the
ledger itself is not specific to NaN.

## Decision

**The host is the identity.** `ConfiguredQuotaSubjects` returns the configured keys
whose base URL host belongs to a provider with a configured-key quota reader. Today
only `api.nan.builders` (compared without case) qualifies. The subject's provider
comes from that match. The name never selects provider rules, so a renamed entry on
the official host stays a NaN subject and an entry named `nan` elsewhere is none.
`management.IsNanBaseURL` is Deck's one NaN host check.

**Ambiguous indexes are refused everywhere.** One subject list counts claims across
auth files and configured keys. An index with more than one claim has no subject.
The overview omits it and lists it under `diagnostics` (`reason`
`duplicate_auth_index`, `auth_index`, `count`). A batch refresh skips it and reports
the same diagnostic. A targeted refresh or detail read answers `409` with code
`duplicate_auth_index`; the agent operation answers `resource_conflict`. The
scheduler skips it and logs one warning when the set of diagnostics changes. No
provider read or ledger call happens for such an index.

**Ledger steps are shared.** `internal/quota/period_delivery.go` reads the ledger
record of each exact model before the provider read and delivers one observation
per model after it, based on that read's version and `observed_at`. The provider
case keeps only its endpoint, parsing and observation mapping.

**Rows follow their source.** Each overview item carries `source` (`auth_file` or
`configured_key`). The dashboard merges, inspects and links configured-key rows by
that source rather than by provider name.

## Consequences

- A NaN account behind another address is a generic compatible provider with no
  quota subject.
- A configured key of a provider without a reader gets no subject and no error.
- A provider with a configured-key quota reader is added at one host match and one
  provider case; the subject list, schedule, ledger steps and dashboard need no change.
