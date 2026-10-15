import { App as AntdApp } from 'antd';
import { act, fireEvent, render, screen } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import { I18nProvider } from '../src/i18n';
import { ConnectionConfiguration, NameGroup } from '../src/pages/ModelSquarePage';
import type { ModelConnection, ModelRouteConfiguration, ModelSquareRoute } from '../src/types/modelSquare';

function wrap(node: React.ReactNode) {
  window.localStorage.setItem('omc-lang', 'en');
  return <I18nProvider><AntdApp>{node}</AntdApp></I18nProvider>;
}

const config = (operations: string[] | null, native: string[] | null): ModelRouteConfiguration => ({
  operations, native, input_modalities: null, output_modalities: null, max_context_length: null, max_output_tokens: null,
});
const route = (callPoint: string, configuration: ModelRouteConfiguration | null): ModelSquareRoute => ({
  provider_id: 'relay', upstream_model: 'gpt-5', call_point: callPoint, reference_id: '', name_scope: 'provider', configuration,
});
const connection = (routes: ModelSquareRoute[]): ModelConnection => ({
  key: 'relay', providerId: 'relay', upstreamModel: 'gpt-5', name: 'Relay', routes, scopedAliases: [...new Set(routes.map(item => item.call_point))],
});

it('keeps every distinct configured row of one exact name instead of the first', () => {
  const { container } = render(wrap(<ConnectionConfiguration connection={connection([
    route('team/fast', config(['chat'], null)),
    route('team/fast', config(['responses'], ['responses'])),
    route('team/slow', config(['chat'], null)),
  ])} />));
  const variants = [...container.querySelectorAll('[data-configuration-alias]')].map(element => element.getAttribute('data-configuration-alias'));
  expect(variants).toEqual(['team/fast', 'team/fast', 'team/slow']);
  expect(container.textContent).toContain('responses');
});

it('shows settings once when every row agrees', () => {
  const { container } = render(wrap(<ConnectionConfiguration connection={connection([
    route('team/fast', config(['chat'], null)),
    route('team/slow', config(['chat'], null)),
  ])} />));
  expect(container.querySelectorAll('[data-configuration-alias]')).toHaveLength(0);
  expect(container.textContent).toContain('chat');
});

function deferredClipboard() {
  const pending: { text: string; settle: (isCopied: boolean) => void }[] = [];
  vi.stubGlobal('navigator', {
    ...navigator,
    clipboard: { writeText: (text: string) => new Promise<void>((resolve, reject) => pending.push({ text, settle: isCopied => (isCopied ? resolve() : reject(new Error('denied'))) })) },
  });
  return pending;
}

it('marks a copy only after it succeeds and clears the mark when the group is entered again', async () => {
  const pending = deferredClipboard();
  render(wrap(<NameGroup alias="team/fast"><span>Relay</span></NameGroup>));
  fireEvent.click(screen.getByRole('button', { name: 'Copy call name team/fast' }));
  expect(screen.queryByRole('button', { name: 'Copied team/fast' })).toBeNull();
  await act(async () => pending[0].settle(true));
  expect(pending[0].text).toBe('team/fast');
  const copied = screen.getByRole('button', { name: 'Copied team/fast' });
  fireEvent.focus(copied, { relatedTarget: screen.getByText('Relay') });
  expect(screen.getByRole('button', { name: 'Copied team/fast' })).toBeTruthy();
  fireEvent.pointerEnter(copied.parentElement!);
  expect(screen.getByRole('button', { name: 'Copy call name team/fast' })).toBeTruthy();
});

it('never shows success for a failed copy', async () => {
  const pending = deferredClipboard();
  const original = document.execCommand;
  document.execCommand = () => false;
  try {
    render(wrap(<NameGroup alias="team/fast"><span>Relay</span></NameGroup>));
    fireEvent.click(screen.getByRole('button', { name: 'Copy call name team/fast' }));
    await act(async () => pending[0].settle(false));
    expect(screen.queryByRole('button', { name: 'Copied team/fast' })).toBeNull();
    expect(screen.getByRole('button', { name: 'Copy call name team/fast' })).toBeTruthy();
  } finally {
    document.execCommand = original;
  }
});

it('drops a pending copy result once the group names a different alias, even if it returns', async () => {
  const pending = deferredClipboard();
  const view = render(wrap(<NameGroup alias="team/fast"><span>Relay</span></NameGroup>));
  fireEvent.click(screen.getByRole('button', { name: 'Copy call name team/fast' }));
  view.rerender(wrap(<NameGroup alias="team/slow"><span>Relay</span></NameGroup>));
  view.rerender(wrap(<NameGroup alias="team/fast"><span>Relay</span></NameGroup>));
  await act(async () => pending[0].settle(true));
  expect(screen.getByRole('button', { name: 'Copy call name team/fast' })).toBeTruthy();
  expect(screen.queryByRole('button', { name: /^Copied/ })).toBeNull();
});
