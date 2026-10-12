import { App as AntdApp } from 'antd';
import { render, screen } from '@testing-library/react';
import { expect, it } from 'vitest';
import { I18nProvider } from '../src/i18n';
import { RequestRow } from '../src/components/usage/RequestRow';
import { PricingEditorProvider } from '../src/components/pricing/PricingEditorContext';
import type { UsageEvent } from '../src/types/usageEvents';

function event(overrides: Partial<UsageEvent>): UsageEvent {
  return {
    id: 1,
    event_key: 'k',
    timestamp_ms: Date.UTC(2026, 9, 12),
    provider: 'nan',
    model: 'kokoro',
    failed: false,
    generate: true,
    latency_ms: 10,
    tokens: { input: 0, output: 0, reasoning: 0, cached: 0, cache_read: 0, cache_creation: 0, total: 0 },
    has_request_log: false,
    pricing_status: 'unpriced',
    ...overrides,
  };
}

function renderRow(row: UsageEvent) {
  window.localStorage.setItem('omc-lang', 'en');
  return render(
    <I18nProvider>
      <AntdApp>
        <PricingEditorProvider>
          <RequestRow event={row} credentials={new Map()} onOpen={() => {}} onToggleSelect={() => {}} />
        </PricingEditorProvider>
      </AntdApp>
    </I18nProvider>,
  );
}

it('offers a price for billable counters that ran without one', () => {
  renderRow(event({ unpriced_reason: 'no_price', tokens: { input: 10, output: 0, reasoning: 0, cached: 0, cache_read: 0, cache_creation: 0, total: 10 } }));
  expect(screen.getByTestId('request-set-price')).toBeTruthy();
});

it.each(['usage_missing', 'not_generated', 'usage_unknown', 'usage_incomplete'] as const)(
  'does not offer a price that would leave a %s request unpriced',
  (reason) => {
    renderRow(event({ unpriced_reason: reason, tokens: { input: 0, output: 0, reasoning: 0, cached: 0, cache_read: 0, cache_creation: 0, total: 0, reported: false } }));
    expect(screen.queryByTestId('request-set-price')).toBeNull();
  },
);

it('keeps the price action for an unpriced row from an older server', () => {
  renderRow(event({}));
  expect(screen.getByTestId('request-set-price')).toBeTruthy();
});

it('never offers a price for a row that predates request-time pricing', () => {
  renderRow(event({ pricing_status: 'legacy_unpriced' }));
  expect(screen.queryByTestId('request-set-price')).toBeNull();
});
