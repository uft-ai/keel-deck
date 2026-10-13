import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, renderHook } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { useProviderManagement } from '../src/components/providers/useProviderManagement';
import { readModelOptionsDraft } from '../src/components/providers/modelOptions';

afterEach(() => vi.unstubAllGlobals());

it('applies discovered defaults only to newly selected models and preserves an existing native alias', async () => {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const fetch = vi.fn(async (input: string, options: RequestInit) => {
    expect(input).toBe('/omc/api/v1/management/providers/pull-models');
    expect(options.method).toBe('POST');
    expect(options.credentials).toBe('same-origin');
    expect(JSON.parse(String(options.body))).toMatchObject({ family: 'openai-compatibility', base_url: 'https://api.nan.builders/v1' });
    return new Response(JSON.stringify({
    models: ['qwen3.8-flash', 'qwen3.6', 'flux-2-klein', 'future-model'], total: 4,
    model_defaults: {
      'qwen3.8-flash': { name: 'qwen3.8-flash', thinking: { levels: ['none', 'low', 'high'] },
        options: { max_context_length: 1000000, operations: ['chat', 'responses'], input_modalities: ['text', 'image'] } },
      'flux-2-klein': { name: 'flux-2-klein', image: true, options: { operations: ['image-generation', 'image-edit'] } },
    },
    }), { status: 200, headers: { 'Content-Type': 'application/json' } });
  });
  vi.stubGlobal('fetch', fetch);
  const { result, unmount } = renderHook(() => useProviderManagement({
    toast: { success: vi.fn(), error: vi.fn(), warning: vi.fn(), info: vi.fn(), pending: () => vi.fn(), dismiss: vi.fn() },
    t: key => key, providerIcons: {}, writeProviderIcon: async () => ({ ok: true }), clearProviderIcon: async () => ({ ok: true }),
    shiftCachedProviderIcons: vi.fn(), settleProviderRow: vi.fn(),
  }), { wrapper: ({ children }) => <QueryClientProvider client={client}>{children}</QueryClientProvider> });
  const existing = { id: 'native', name: 'qwen3.6', alias: 'qwen3.6-native', image: false,
    thinking: { levels: ['none'] }, options: readModelOptionsDraft({ operations: ['responses'], native: ['responses'] }) };
  try {
    act(() => {
      result.current.setFormBaseURL('https://api.nan.builders/v1');
      result.current.setFormModels([existing]);
    });
    await act(async () => result.current.handlePullModels());
    act(() => result.current.handleAddPickedModels(['qwen3.6', 'qwen3.8-flash', 'flux-2-klein', 'future-model']));
    expect(result.current.formModels[0]).toEqual(existing);
    const qwen = result.current.formModels.find(model => model.name === 'qwen3.8-flash')!;
    expect(qwen.options.maxContextLength).toBe(1000000);
    expect(qwen.options.operations).toEqual(['chat', 'responses']);
    expect(qwen.thinking?.levels).toContain('none');
    expect(result.current.formModels.find(model => model.name === 'flux-2-klein')?.image).toBe(true);
    expect(result.current.formModels.find(model => model.name === 'future-model')?.options.maxContextLength).toBeNull();
    expect(fetch).toHaveBeenCalledOnce();
  } finally {
    unmount();
    client.clear();
  }
});
