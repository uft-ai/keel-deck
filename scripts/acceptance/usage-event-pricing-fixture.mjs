import contract from './contracts/usage-event-pricing.json' with { type: 'json' };

export const USAGE_EVENT_PRICING_CONTRACT = contract;

/**
 * A request-list fixture row carrying the pricing fields the real list handler
 * sends for the named contract case. Without them a row is not a shape the server
 * produces, and the console's price action has nothing honest to decide on.
 */
export function withCertifiedPricing(record, caseId) {
  const entry = USAGE_EVENT_PRICING_CONTRACT.cases.find((candidate) => candidate.id === caseId);
  if (!entry) throw new Error(`Unknown usage event pricing contract case: ${caseId}`);
  const { pricing_status, unpriced_reason, tokens_reported } = entry.row;
  return {
    ...record,
    pricing_status,
    unpriced_reason,
    tokens: { ...record.tokens, reported: tokens_reported },
  };
}
