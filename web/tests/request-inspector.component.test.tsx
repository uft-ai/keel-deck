import { App as AntdApp } from 'antd';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, waitFor } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import { I18nProvider } from '../src/i18n';
import { RequestInspectorView } from '../src/components/usage/RequestInspectorView';
import type { RequestInspector } from '../src/types/usageEvents';
import { REQUEST_INSPECTOR_CONTRACT, requestInspectorResponse } from '../../scripts/acceptance/request-inspector-fixture.mjs';

const LIMITS = { scan_bytes: 64 << 20, section_bytes: 64 << 10, max_sections: 64 };

function inspector(overrides: Partial<RequestInspector>): RequestInspector {
  return {
    capture_state: 'retained',
    parse_state: 'recognized',
    boundaries: 'heuristic',
    identity: { request_id: 'req-1', logged_request_id: 'req-1', match: 'matched' },
    limits: LIMITS,
    preview: { bytes_read: 1024, eof: true, truncated: false, sections_omitted: 0 },
    sections: [],
    ...overrides,
  };
}

function renderInspector(payload: unknown, status = 200) {
  window.localStorage.setItem('omc-lang', 'en');
  vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify(payload), { status, headers: { 'Content-Type': 'application/json' } })));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <I18nProvider>
        <AntdApp>
          <RequestInspectorView eventId={7} />
        </AntdApp>
      </I18nProvider>
    </QueryClientProvider>,
  );
}

it('renders every upstream attempt with status, headers, markers and omitted binary bodies', async () => {
  renderInspector(inspector({
    sections: [
      { kind: 'request_body', name: 'REQUEST BODY', body: '{"model":"nan/qwen3.8-flash"}', body_bytes: 30, truncated: true },
      { kind: 'api_request', name: 'API REQUEST 1', attempt: 1, meta: 'Upstream URL: https://api.nan.builders/v1', body: 'first', body_bytes: 5, truncated: false },
      { kind: 'api_request', name: 'API REQUEST 2', attempt: 2, body: 'second', body_bytes: 6, truncated: false, markers: ['[API REQUEST BODY TRUNCATED: captured first 6 bytes]'] },
      { kind: 'api_response', name: 'API RESPONSE 1', attempt: 1, status: 429, meta: 'Retry-After: 1', body: 'rate limited', body_bytes: 12, truncated: false },
      { kind: 'api_response', name: 'API RESPONSE 2', attempt: 2, status: 200, meta: 'Content-Type: audio/wav', body_bytes: 4096, truncated: false, body_omitted: true, omission_reason: 'binary' },
    ],
  }));
  expect(await screen.findByText('Upstream response · Attempt 2')).toBeTruthy();
  expect(screen.getByText('Upstream request · Attempt 1')).toBeTruthy();
  expect(screen.getByText('Upstream request · Attempt 2')).toBeTruthy();
  expect(screen.getByText(/Status 429/)).toBeTruthy();
  expect(screen.getByText('Retry-After: 1')).toBeTruthy();
  expect(screen.getByText('Content-Type: audio/wav')).toBeTruthy();
  expect(screen.getByText('Binary content omitted')).toBeTruthy();
  expect(screen.getByText('[API REQUEST BODY TRUNCATED: captured first 6 bytes]')).toBeTruthy();
  expect(screen.getByText(/Only the first 64(\.0)? KB of this section is shown/)).toBeTruthy();
});

it('explains missing logs, hidden mismatches and bounded reads instead of showing text', async () => {
  const { unmount } = renderInspector(inspector({ capture_state: 'expired_unknown', capture_reason: 'request_log_not_found', identity: { request_id: 'gone', match: 'unknown' } }));
  expect(await screen.findByText(/Relay holds no log for this request/)).toBeTruthy();
  unmount();

  const pending = renderInspector(inspector({ capture_state: 'pending', capture_reason: 'capture_pending', identity: { request_id: 'busy', match: 'unknown' } }));
  expect(await screen.findByText(/Relay is still writing this request's log/)).toBeTruthy();
  pending.unmount();

  renderInspector(inspector({
    identity: { request_id: 'req-1', logged_request_id: 'req-other', match: 'mismatch' },
    parse_state: 'partial',
    preview: { bytes_read: LIMITS.scan_bytes, eof: false, truncated: true, sections_omitted: 3 },
  }));
  expect(await screen.findByText(/another request \(req-other\)/)).toBeTruthy();
  expect(screen.getByText(/Only the first 64(\.0)? MB were read/)).toBeTruthy();
  expect(screen.getByText('3 more sections exceed the display limit')).toBeTruthy();
});

it('shows a load failure for a failed inspection request', async () => {
  renderInspector({ error: 'audit log failure' }, 500);
  expect(await screen.findByText('Request inspection could not be read')).toBeTruthy();
});

it('reports a malformed inspection response instead of crashing the drawer', async () => {
  renderInspector({});
  expect(await screen.findByText('Request inspection could not be read')).toBeTruthy();
  expect(screen.getByText('The server returned an incomplete inspection response.')).toBeTruthy();
});

it('renders every Go-certified wire case received through the typed client', async () => {
  for (const entry of REQUEST_INSPECTOR_CONTRACT.cases) {
    const { container, unmount } = renderInspector(requestInspectorResponse(entry.id).json);
    await waitFor(() => expect(container.querySelector('.request-inspector')).toBeTruthy());
    expect(screen.queryByText('Request inspection could not be read')).toBeNull();
    expect(container.querySelectorAll('.request-inspector-section')).toHaveLength(entry.response.sections.length);
    unmount();
  }
});
