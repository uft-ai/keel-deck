import { render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { I18nProvider, useI18n } from '../src/i18n';

function Reading() {
  const { lang, t } = useI18n();
  return <p data-lang={lang}>{t('omc.title')}</p>;
}

function renderReading() {
  render(<I18nProvider><Reading /></I18nProvider>);
  return screen.getByText(/OMC/);
}

describe('first-run reading language', () => {
  it('reads English when nothing is stored', () => {
    const reading = renderReading();
    expect(reading.textContent).toBe('OMC Settings');
    expect(document.documentElement.lang).toBe('en');
  });

  it('reads English when the stored language is unknown', () => {
    window.localStorage.setItem('omc-lang', 'klingon');
    expect(renderReading().textContent).toBe('OMC Settings');
  });

  it('reads English when storage cannot be read', () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => { throw new Error('blocked'); });
    expect(renderReading().textContent).toBe('OMC Settings');
  });

  it('keeps a stored Chinese choice', () => {
    window.localStorage.setItem('omc-lang', 'zh');
    expect(renderReading().textContent).toBe('OMC 设置');
    expect(window.localStorage.getItem('omc-lang')).toBe('zh');
  });
});
