import { render, screen, within } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { ProviderTable } from '../src/components/providers/ProviderTable';
import { I18nProvider } from '../src/i18n';
import type { ProviderItem } from '../src/types/providers';

const row = (id: string, name: string, baseURL: string, prefix: string, kind?: string): ProviderItem => ({
  id, family: 'openai-compatibility', name, upstream_name: name, protocol: 'OpenAI Chat Completions',
  base_url: baseURL, prefix, priority: 0, disabled: false, key_configured: true, models: [],
  ...(kind ? { provider_kind: kind } : {}),
} as unknown as ProviderItem);

beforeEach(() => {
  window.localStorage.setItem('omc-lang', 'en');
  // The table's responsive list and antd read breakpoints, which jsdom does not implement.
  vi.stubGlobal('matchMedia', (query: string) => ({
    matches: false, media: query, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {},
  }));
});
afterEach(() => { vi.unstubAllGlobals(); });

it('names NaN.Builders from the server kind and keeps the compatible protocol beside it', () => {
  const props = {
    providersLoading: false, emptyText: 'none', traffic: undefined, providerIcons: {},
    statusQueue: { targetFor: () => undefined, isBusy: () => false, request: vi.fn() },
    deleteProviderMutation: { mutate: vi.fn(), isPending: false, variables: undefined },
    handleOpenEdit: vi.fn(), setIconPickerOpen: vi.fn(), setTargetProviderForIcon: vi.fn(),
  } as unknown as Omit<Parameters<typeof ProviderTable>[0], 'providers'>;
  render(
    <I18nProvider>
      <ProviderTable
        {...props}
        providers={[
          row('official', 'My inference', 'https://api.nan.builders/v1', 'nan', 'nan'),
          row('lookalike', 'nan', 'https://relay.example.test/v1', 'other'),
        ]}
      />
    </I18nProvider>,
  );
  const official = screen.getAllByRole('row').find((tr) => within(tr).queryByText('My inference'))!;
  expect(within(official).getByText('NaN.Builders')).toBeTruthy();
  expect(within(official).getByText(/OpenAI Compatible/i)).toBeTruthy();
  const images = Array.from(official.querySelectorAll('img')).map((img) => img.getAttribute('src'));
  expect(images.some((src) => src?.endsWith('provider-marks/nan-builders.png'))).toBe(true);
  expect(images.some((src) => /lobe-icons\/(pi|openai)-/i.test(src ?? ''))).toBe(false);

  const lookalike = screen.getAllByRole('row').find((tr) => within(tr).queryByText('other', { exact: true }))!;
  expect(within(lookalike).queryByText('NaN.Builders')).toBeNull();
});
