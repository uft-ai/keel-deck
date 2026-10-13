import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, renderHook } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { useProviderManagement } from '../src/components/providers/useProviderManagement';
import { EMPTY_MODEL_OPTIONS_DRAFT } from '../src/components/providers/modelOptions';
import type { FormModelItem } from '../src/components/providers/managementProviders';

afterEach(() => vi.unstubAllGlobals());

type Answer = string[] | 'fail';

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
}

/** Answers each key's pull from `byKey`, and records every save body. */
function stubGateway(byKey: Record<string, Answer>) {
  const saves: Array<Record<string, unknown>> = [];
  const pulls: Array<Record<string, unknown>> = [];
  vi.stubGlobal('fetch', vi.fn(async (input: string, options: RequestInit) => {
    const body = options.body ? JSON.parse(String(options.body)) : {};
    if (input.endsWith('/management/providers/pull-models')) {
      pulls.push(body);
      const answer = byKey[String(body.api_key ?? body.auth_index)];
      if (answer === undefined || answer === 'fail') return json({ error: 'upstream refused' }, 502);
      return json({ models: answer, total: answer.length });
    }
    if (input.endsWith('/management/providers') && options.method === 'POST') {
      saves.push(body);
      return json({ status: 'ok', id: 'openai-compat-0' });
    }
    return json({ providers: [], total: 0 });
  }));
  return { saves, pulls };
}

function renderEditor() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const toast = { success: vi.fn(), error: vi.fn(), warning: vi.fn(), info: vi.fn(), pending: () => vi.fn(), dismiss: vi.fn() };
  const view = renderHook(() => useProviderManagement({
    toast, t: (key: string) => key, providerIcons: {}, writeProviderIcon: async () => ({ ok: true }),
    shiftCachedProviderIcons: vi.fn(), settleProviderRow: vi.fn(),
  }), { wrapper: ({ children }) => <QueryClientProvider client={client}>{children}</QueryClientProvider> });
  return { ...view, toast, client };
}

function row(name: string, disabled?: boolean): FormModelItem {
  return { id: `row-${name}`, name, alias: '', thinking: { levels: [] }, options: EMPTY_MODEL_OPTIONS_DRAFT, disabled };
}

function keys(...values: string[]) {
  return values.map((apiKey, index) => ({ id: `key-${index}`, apiKey, weight: 1 }));
}

it('adds an enabled row for each new model and keeps every saved row', async () => {
  stubGateway({ 'sk-a': ['kept-off', 'served', 'new-model'], 'sk-b': ['new-model', 'served', 'kept-off'] });
  const { result, unmount, client } = renderEditor();
  try {
    act(() => {
      result.current.setFormBaseURL('https://relay.example/v1');
      result.current.setFormKeys(keys('sk-a', 'sk-b'));
      result.current.setFormModels([row('kept-off', true), row('served', false), row('gone', false)]);
    });
    await act(async () => result.current.handleDiscoverAllModels());
    const byName = Object.fromEntries(result.current.formModels.map((model) => [model.name, model]));
    expect(Object.keys(byName)).toEqual(['kept-off', 'served', 'gone', 'new-model']);
    expect(byName['kept-off'].disabled).toBe(true);
    expect(byName.gone.disabled).toBe(false);
    expect(byName['new-model'].disabled).toBe(false);
  } finally {
    unmount();
    client.clear();
  }
});

it.each([
  ['a key lists different models', { 'sk-a': ['one', 'two'], 'sk-b': ['one'] }, 'pro.discover_mismatch'],
  ['a key fails', { 'sk-a': ['one'], 'sk-b': 'fail' as const }, 'pro.discover_failed'],
  ['a key lists nothing', { 'sk-a': [], 'sk-b': [] }, 'pro.discover_empty'],
])('keeps the form when %s', async (_, byKey, message) => {
  stubGateway(byKey);
  const { result, unmount, toast, client } = renderEditor();
  try {
    const before = [row('kept-off', true), row('served', false)];
    act(() => {
      result.current.setFormBaseURL('https://relay.example/v1');
      result.current.setFormKeys(keys('sk-a', 'sk-b'));
      result.current.setFormModels(before);
    });
    await act(async () => result.current.handleDiscoverAllModels());
    expect(result.current.formModels).toEqual(before);
    expect(toast.error).toHaveBeenCalledWith(message);
  } finally {
    unmount();
    client.clear();
  }
});

it('a model that leaves and returns keeps its off choice', async () => {
  const gateway = { 'sk-a': ['served'] as Answer };
  stubGateway(gateway);
  const { result, unmount, client } = renderEditor();
  try {
    act(() => {
      result.current.setFormBaseURL('https://relay.example/v1');
      result.current.setFormKeys(keys('sk-a'));
      result.current.setFormModels([row('kept-off', true), row('served', false)]);
    });
    await act(async () => result.current.handleDiscoverAllModels());
    gateway['sk-a'] = ['served', 'kept-off'];
    await act(async () => result.current.handleDiscoverAllModels());
    const keptOff = result.current.formModels.filter((model) => model.name === 'kept-off');
    expect(keptOff).toHaveLength(1);
    expect(keptOff[0].disabled).toBe(true);
  } finally {
    unmount();
    client.clear();
  }
});

it.each([
  ['the key changes', 'key'],
  ['the drawer is reopened on another draft', 'reopen'],
])('a read still pending when %s changes nothing', async (_, change) => {
  let release: () => void = () => {};
  const held = new Promise<void>((resolve) => { release = resolve; });
  const saves: unknown[] = [];
  vi.stubGlobal('fetch', vi.fn(async (input: string, options: RequestInit) => {
    if (input.endsWith('/pull-models')) {
      await held;
      return json({ models: ['late-model'], total: 1 });
    }
    if (options.method === 'POST') saves.push(options.body);
    return json({ status: 'ok', id: 'openai-compat-0' });
  }));
  const { result, unmount, client } = renderEditor();
  try {
    act(() => result.current.handleOpenCreate());
    act(() => {
      result.current.setFormBaseURL('https://relay.example/v1');
      result.current.setFormKeys(keys('sk-a'));
      result.current.setFormModels([row('kept')]);
    });
    let pending!: Promise<void>;
    act(() => { pending = result.current.handleDiscoverAllModels(); });
    if (change === 'key') {
      act(() => result.current.setFormKeys(keys('sk-b')));
    } else {
      act(() => result.current.handleOpenCreate());
      act(() => result.current.setFormModels([row('other-draft')]));
    }
    release();
    await act(async () => pending);
    const names = result.current.formModels.map((model) => model.name);
    expect(names).toEqual(change === 'key' ? ['kept'] : ['other-draft']);
    expect(result.current.isDiscovering).toBe(false);
    expect(saves).toHaveLength(0);
  } finally {
    unmount();
    client.clear();
  }
});

it('a new NaN.Builders entry saves only after the account list is read', async () => {
  const gateway: Record<string, Answer> = { 'sk-a': 'fail' };
  const { saves, pulls } = stubGateway(gateway);
  const { result, unmount, client } = renderEditor();
  try {
    act(() => {
      result.current.handleOpenCreate();
    });
    act(() => {
      result.current.setFormBaseURL('https://api.nan.builders/v1');
      result.current.setFormKeys(keys('sk-a'));
      // A hand-typed list does not stand in for the account's own list.
      result.current.setFormModels([row('typed-by-hand')]);
    });
    await act(async () => result.current.handleSaveProvider());
    expect(saves).toHaveLength(0);

    gateway['sk-a'] = ['qwen3.8-flash', 'glm5.3'];
    await act(async () => result.current.handleSaveProvider());
    expect(saves).toHaveLength(1);
    const names = (saves[0].model_entries as Array<{ name: string; disabled?: boolean }>).map((model) => model.name);
    expect(names).toEqual(['typed-by-hand', 'glm5.3', 'qwen3.8-flash']);
    expect(pulls).toHaveLength(2);

    // A changed key needs a new read before the next save.
    act(() => result.current.setFormKeys(keys('sk-b')));
    await act(async () => result.current.handleSaveProvider());
    expect(saves).toHaveLength(1);
    expect(pulls.at(-1)).toMatchObject({ api_key: 'sk-b' });
  } finally {
    unmount();
    client.clear();
  }
});
