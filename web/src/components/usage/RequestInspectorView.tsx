import React from 'react';
import { useQuery } from '@tanstack/react-query';
import { api } from '../../api/client';
import { useT } from '../../i18n';
import type { RequestInspector, RequestInspectorSection } from '../../types/usageEvents';
import { formatBytes } from '../../utils/format';
import { LoadFailure, Notice } from '../feedback';

type Translate = ReturnType<typeof useT>;

function captureNotice(inspector: RequestInspector, t: Translate): React.ReactNode {
  if (inspector.capture_state === 'retained') return null;
  if (inspector.capture_reason === 'no_request_id') return <Notice tone="info" description={t('events.inspector_no_request_id')} />;
  if (inspector.capture_state === 'expired_unknown') return <Notice tone="info" description={t('events.inspector_expired')} />;
  if (inspector.capture_state === 'pending') return <Notice tone="info" description={t('events.inspector_pending')} />;
  if (inspector.capture_state === 'missing') return <Notice tone="info" description={t('events.inspector_missing')} />;
  return <Notice tone="warning" description={t('events.inspector_unavailable')} />;
}

function sectionTitle(section: RequestInspectorSection, t: Translate): string {
  const name = t(`events.inspector_section.${section.kind}`);
  return section.attempt ? `${name} · ${t('events.inspector_attempt', { n: section.attempt })}` : name;
}

function InspectorSection({ section, limits }: { section: RequestInspectorSection; limits: RequestInspector['limits'] }) {
  const t = useT();
  const facts = [
    section.status ? t('events.inspector_status', { status: section.status }) : null,
    section.body_bytes > 0 ? t('events.inspector_body_bytes', { size: formatBytes(section.body_bytes) }) : null,
  ].filter(Boolean);
  return (
    <section className="request-inspector-section" data-kind={section.kind}>
      <header>
        <strong>{sectionTitle(section, t)}</strong>
        {facts.length > 0 && <span className="request-detail-note">{facts.join(' · ')}</span>}
      </header>
      {section.markers?.map((marker) => (
        <p key={marker} className="request-inspector-marker">{marker}</p>
      ))}
      {section.meta && <pre>{section.meta}</pre>}
      {section.body_omitted
        ? <p className="request-detail-note">{t('events.inspector_binary')}</p>
        : section.body && <pre>{section.body}</pre>}
      {section.truncated && (
        <p className="request-detail-note">{t('events.inspector_section_truncated', { size: formatBytes(limits.section_bytes) })}</p>
      )}
    </section>
  );
}

// A response missing the inspector's required objects is a contract fault (an old
// server, a proxy page or a broken fixture); it must be reported, not rendered.
function isRequestInspector(value: unknown): value is RequestInspector {
  if (!value || typeof value !== 'object') return false;
  const candidate = value as Partial<RequestInspector>;
  return typeof candidate.capture_state === 'string'
    && !!candidate.identity && typeof candidate.identity === 'object'
    && !!candidate.limits && typeof candidate.limits === 'object'
    && !!candidate.preview && typeof candidate.preview === 'object'
    && Array.isArray(candidate.sections);
}

/** RequestInspectorView shows the retained log sections without storing them. */
export function RequestInspectorView({ eventId }: { eventId: number }) {
  const t = useT();
  const result = useQuery({
    queryKey: ['usage-event-inspector', eventId],
    queryFn: () => api.getUsageEventInspector(eventId),
  });
  if (result.isLoading) return <p className="request-detail-note">{t('common.loading')}</p>;
  if (result.isError || !isRequestInspector(result.data)) {
    return (
      <LoadFailure
        title={t('events.inspector_load_failed')}
        error={result.isError ? result.error : undefined}
        detail={result.isError ? undefined : t('events.inspector_malformed')}
        onRetry={() => void result.refetch()}
      />
    );
  }
  const inspector = result.data;
  const retained = inspector.capture_state === 'retained';
  return (
    <div className="request-inspector">
      {captureNotice(inspector, t)}
      {retained && inspector.identity.match === 'mismatch' && (
        <Notice tone="warning" description={t('events.inspector_mismatch', { id: inspector.identity.logged_request_id ?? '' })} />
      )}
      {retained && inspector.identity.match === 'unknown' && <Notice tone="info" description={t('events.inspector_unknown')} />}
      {retained && inspector.parse_state === 'unrecognized' && <Notice tone="info" description={t('events.inspector_unrecognized')} />}
      {inspector.preview.read_error && <Notice tone="warning" description={t('events.inspector_read_error')} />}
      {retained && !inspector.preview.eof && !inspector.preview.read_error && (
        <Notice tone="info" description={t('events.inspector_scan_limit', { size: formatBytes(inspector.limits.scan_bytes) })} />
      )}
      {inspector.preview.sections_omitted > 0 && (
        <Notice tone="info" description={t('events.inspector_sections_omitted', { n: inspector.preview.sections_omitted })} />
      )}
      {inspector.sections.length > 0 && <p className="request-detail-note">{t('events.inspector_boundaries')}</p>}
      {inspector.sections.map((section, index) => (
        <InspectorSection key={`${section.kind}-${section.attempt ?? 0}-${index}`} section={section} limits={inspector.limits} />
      ))}
    </div>
  );
}
