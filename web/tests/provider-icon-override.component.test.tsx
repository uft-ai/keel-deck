import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, renderHook } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { useProviderManagement } from '../src/components/providers/useProviderManagement';
import type { ProviderItem } from '../src/types/providers';
import { getProviderDefaultIcon } from '../src/types/providerIconIds';

afterEach(() => vi.unstubAllGlobals());

function json(body: unknown) {
  return new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' } });
}

function renderEditor(providerIcons: Record<string, string> = {}) {
  vi.stubGlobal('fetch', vi.fn(async () => json({ status: 'ok', id: 'openai-compat-0' })));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const writes: Array<[string, string]> = [];
  const clears: Array<[string, string | undefined]> = [];
  const view = renderHook(() => useProviderManagement({
    toast: { success: vi.fn(), error: vi.fn(), warning: vi.fn(), info: vi.fn(), pending: () => vi.fn(), dismiss: vi.fn() },
    t: (key: string) => key,
    providerIcons,
    writeProviderIcon: async (id, icon) => { writes.push([id, icon]); return { ok: true }; },
    clearProviderIcon: async (id, name) => { clears.push([id, name]); return { ok: true }; },
    shiftCachedProviderIcons: vi.fn(),
    settleProviderRow: vi.fn(),
  }), { wrapper: ({ children }) => <QueryClientProvider client={client}>{children}</QueryClientProvider> });
  return { ...view, writes, clears, client };
}

const stored: ProviderItem = {
  id: 'openai-compat-0', family: 'openai-compatibility', name: 'Relay', base_url: 'https://relay.example/v1',
  key_entries: [{ index: 0, api_key: 'sk-a' }], models: [], model_entries: [],
} as never;

it('a create with the derived mark stores no override', async () => {
  const { result, unmount, writes, clears, client } = renderEditor();
  try {
    act(() => result.current.handleOpenCreate());
    act(() => {
      result.current.setFormBaseURL('https://relay.example/v1');
      result.current.setFormKeys([{ id: 'k', apiKey: 'sk-a', weight: 1 }]);
    });
    await act(async () => result.current.handleSaveProvider());
    await vi.waitFor(() => expect(result.current.providerDrawerOpen).toBe(false));
    expect(writes).toEqual([]);
    expect(clears).toEqual([]);
  } finally {
    unmount();
    client.clear();
  }
});

it('a chosen mark is stored and reopens as chosen', async () => {
  const { result, unmount, writes, client } = renderEditor();
  try {
    act(() => result.current.handleOpenEdit(stored));
    act(() => {
      result.current.setFormIcon('Anthropic');
      result.current.setIconManuallySelected(true);
    });
    await act(async () => result.current.handleSaveProvider());
    await vi.waitFor(() => expect(writes).toEqual([['openai-compat-0', 'Anthropic']]));
  } finally {
    unmount();
    client.clear();
  }
  const reopened = renderEditor({ 'openai-compat-0': 'Anthropic' });
  try {
    act(() => reopened.result.current.handleOpenEdit(stored));
    expect(reopened.result.current.iconManuallySelected).toBe(true);
    expect(reopened.result.current.formIcon).toBe('Anthropic');
  } finally {
    reopened.unmount();
    reopened.client.clear();
  }
});

it('a mark an older save froze follows the endpoint and is removed on save', async () => {
  // An older console stored the derived mark of every row, under id and name.
  const derived = getProviderDefaultIcon(stored.family, stored.name, stored.base_url);
  const { result, unmount, clears, writes, client } = renderEditor({ 'openai-compat-0': derived, Relay: derived });
  try {
    act(() => result.current.handleOpenEdit(stored));
    expect(result.current.iconManuallySelected).toBe(false);
    await act(async () => result.current.handleSaveProvider());
    await vi.waitFor(() => expect(clears).toEqual([['openai-compat-0', 'Relay']]));
    expect(writes).toEqual([]);
  } finally {
    unmount();
    client.clear();
  }
});

it('a stored mark the operator resets is removed on save', async () => {
  const { result, unmount, clears, writes, client } = renderEditor({ 'openai-compat-0': 'Pi' });
  try {
    act(() => result.current.handleOpenEdit(stored));
    expect(result.current.iconManuallySelected).toBe(true);
    // What the drawer's Reset does.
    act(() => result.current.setIconManuallySelected(false));
    await act(async () => result.current.handleSaveProvider());
    await vi.waitFor(() => expect(clears).toEqual([['openai-compat-0', 'Relay']]));
    expect(writes).toEqual([]);
  } finally {
    unmount();
    client.clear();
  }
});
