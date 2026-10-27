import React from 'react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { LogFileViewer } from '../src/components/logs/LogFileViewer';
import { I18nProvider } from '../src/i18n';
import { MAX_LOG_PREVIEW_BYTES, type LogViewerTarget } from '../src/types/logs';
import { installOmcApi } from './support/omcApi';

const REFUSED_LOG = '=== REQUEST INFO ===\nURL: /v1/messages\n\n=== RESPONSE ===\nStatus: 503\nauth_unavailable: no auth available\n';

function Surface({ target, onClose = () => {} }: { target: LogViewerTarget | null; onClose?: () => void }) {
  const [client] = React.useState(() => new QueryClient());
  return (
    <React.StrictMode>
      <QueryClientProvider client={client}>
        <I18nProvider><LogFileViewer target={target} onClose={onClose} /></I18nProvider>
      </QueryClientProvider>
    </React.StrictMode>
  );
}

/** Answers every read with one response and records the paths that were asked for. */
function stubLogRead(respond: () => Response): string[] {
  const paths: string[] = [];
  installOmcApi().on('GET', /^\/(management\/request(-error)?-logs\/|usage\/events\/\d+\/request-log$)/, request => {
    paths.push(`/omc/api/v1${request.path}`);
    return respond();
  });
  return paths;
}

// Download is a link to the whole file once loaded, and a disabled button before.
const downloadControl = () => (screen.queryByRole('link', { name: /Download/ }) ?? screen.getByRole('button', { name: /Download/ })) as HTMLElement;
const downloadEnabled = () => downloadControl().tagName === 'A' && downloadControl().getAttribute('aria-disabled') !== 'true';
const copyButton = () => screen.getByRole('button', { name: /Copy/ }) as HTMLButtonElement;

describe('LogFileViewer', () => {
  beforeEach(() => {
    window.localStorage.setItem('omc-lang', 'en');
    window.__OMCPA_CONFIG__ = { apiBaseUrl: '/omc/api/v1' } as typeof window.__OMCPA_CONFIG__;
  });

  it('reads a request log by the id on its gateway line and titles it as that file', async () => {
    const paths = stubLogRead(() => new Response(REFUSED_LOG, { status: 200, headers: { 'Content-Type': 'text/plain' } }));
    render(<Surface target={{ kind: 'request', requestId: '41dbb9ee' }} />);

    expect((await screen.findByText(/auth_unavailable: no auth available/)).tagName).toBe('PRE');
    expect(screen.getByText('request-41dbb9ee.log')).toBeTruthy();
    expect(new Set(paths)).toEqual(new Set(['/omc/api/v1/management/request-logs/41dbb9ee']));
    expect(downloadEnabled()).toBe(true);
    expect(downloadControl().getAttribute('href')).toBe('/omc/api/v1/management/request-logs/41dbb9ee');
    expect(copyButton().disabled).toBe(false);
  });

  it('reads a usage record through its own route, which checks the request id', async () => {
    const paths = stubLogRead(() => new Response(REFUSED_LOG, { status: 200 }));
    render(<Surface target={{ kind: 'event', eventId: 7, requestId: '41dbb9ee' }} />);

    await screen.findByText(/auth_unavailable/);
    expect(new Set(paths)).toEqual(new Set(['/omc/api/v1/usage/events/7/request-log']));
    expect(screen.getByText('request-41dbb9ee.log')).toBeTruthy();
    expect(downloadControl().getAttribute('href')).toBe('/omc/api/v1/usage/events/7/request-log');
  });

  it('asks again later while Relay is still writing the log', async () => {
    stubLogRead(() => new Response(
      JSON.stringify({ error: 'Relay is still writing', code: 'capture_pending' }),
      { status: 409, headers: { 'Content-Type': 'application/json' } },
    ));
    render(<Surface target={{ kind: 'event', eventId: 7, requestId: 'busy0001' }} />);

    expect((await screen.findByRole('alert')).textContent).toContain('still writing');
    expect(screen.getByRole('button', { name: /Retry/ })).toBeTruthy();
    expect(downloadEnabled()).toBe(false);
  });

  it('reads a named error log from the error-log route', async () => {
    const paths = stubLogRead(() => new Response(REFUSED_LOG, { status: 200 }));
    render(<Surface target={{ kind: 'file', file: { name: 'error-v1-messages-41dbb9ee.log', size: 1622, modified: 0 } }} />);

    await screen.findByText(/auth_unavailable/);
    expect(new Set(paths)).toEqual(new Set(['/omc/api/v1/management/request-error-logs/error-v1-messages-41dbb9ee.log']));
  });

  it('words a file CPA did not keep as retention, with nothing to retry, copy or save', async () => {
    stubLogRead(() => new Response(
      JSON.stringify({ error: 'CPA holds no log file for this request', code: 'request_log_not_found' }),
      { status: 404, headers: { 'Content-Type': 'application/json' } },
    ));
    render(<Surface target={{ kind: 'request', requestId: 'deadbeef' }} />);

    const notice = await screen.findByRole('alert');
    expect(notice.classList.contains('is-warning')).toBe(true);
    expect(notice.textContent).toContain('CPA holds no log file for this request');
    expect(screen.queryByRole('button', { name: /Retry/ })).toBeNull();
    expect(downloadEnabled()).toBe(false);
    expect(copyButton().disabled).toBe(true);
  });

  it('reports any other failed read as a failure that can be retried', async () => {
    stubLogRead(() => new Response(JSON.stringify({ error: 'CPA is unreachable' }), { status: 502, headers: { 'Content-Type': 'application/json' } }));
    render(<Surface target={{ kind: 'request', requestId: '41dbb9ee' }} />);

    const failure = await screen.findByRole('alert');
    expect(failure.classList.contains('is-error')).toBe(true);
    expect(screen.getByRole('button', { name: /Retry/ })).toBeTruthy();
  });

  it('draws only the preview of a longer file, says so, and copies only the preview', async () => {
    const paths = stubLogRead(() => new Response('x'.repeat(MAX_LOG_PREVIEW_BYTES + 1), { status: 200 }));
    render(<Surface target={{ kind: 'request', requestId: '41dbb9ee' }} />);

    expect((await screen.findByRole('alert')).textContent).toContain('Only the first');
    expect(document.querySelector('pre')?.textContent?.length).toBe(MAX_LOG_PREVIEW_BYTES);
    expect(paths).toEqual(['/omc/api/v1/management/request-logs/41dbb9ee']);
    expect(screen.getByRole('button', { name: /Copy preview/ })).toBeTruthy();
    expect(downloadEnabled()).toBe(true);
  });

  it('asks for nothing while closed and dismisses through its close control', async () => {
    const paths = stubLogRead(() => new Response(REFUSED_LOG, { status: 200 }));
    const onClose = vi.fn();
    const { rerender } = render(<Surface target={null} onClose={onClose} />);
    expect(paths).toEqual([]);

    rerender(<Surface target={{ kind: 'request', requestId: '41dbb9ee' }} onClose={onClose} />);
    await screen.findByText(/auth_unavailable/);
    fireEvent.click(screen.getByRole('button', { name: 'Close' }));

    expect(onClose).toHaveBeenCalledTimes(1);
  });
});
