import { render, screen } from '@testing-library/react';
import { expect, it } from 'vitest';
import { I18nProvider } from '../src/i18n';
import { CostBreakdownView } from '../src/components/pricing/CostBreakdownView';
import { PricingEditorProvider } from '../src/components/pricing/PricingEditorContext';
import type { RequestCostBreakdown } from '../src/types/pricing';

function renderBreakdown(breakdown: RequestCostBreakdown) {
  window.localStorage.setItem('omc-lang', 'en');
  return render(
    <I18nProvider>
      <PricingEditorProvider>
        <CostBreakdownView breakdown={breakdown} model="nan/qwen3.8-flash" />
      </PricingEditorProvider>
    </I18nProvider>,
  );
}

it('offers a price only when the request ran without one', () => {
  renderBreakdown({ status: 'unpriced', recomputed_matches: false, unpriced_reason: 'no_price' });
  expect(screen.getByText('No price existed when this request ran, so it is left out of costs.')).toBeTruthy();
  expect(screen.getByTestId('cost-set-price')).toBeTruthy();
});

it.each([
  ['usage_missing', /reported no usage/],
  ['not_generated', /utility or history call/],
  ['usage_unknown', /older record does not say/],
  ['usage_incomplete', /only a billed total/],
] as const)('explains %s without offering a price that would not change it', (reason, text) => {
  renderBreakdown({ status: 'unpriced', recomputed_matches: false, unpriced_reason: reason });
  expect(screen.getByTestId('cost-unpriced-reason').textContent).toMatch(text);
  expect(screen.queryByTestId('cost-set-price')).toBeNull();
});

it('keeps the price action for an unpriced response from an older server', () => {
  renderBreakdown({ status: 'unpriced', recomputed_matches: false });
  expect(screen.getByTestId('cost-set-price')).toBeTruthy();
});
