import React from 'react';
import { Button } from 'antd';
import { useQuery } from '@tanstack/react-query';
import { DownloadOutlined } from '../icons';
import dayjs from '../../utils/time';
import { api, apiErrorCode } from '../../api/client';
import { useT } from '../../i18n';
import { useOverlayHistory } from '../../hooks/useOverlayHistory';
import { useTimeZone } from '../../utils/TimeZoneProvider';
import { getAppConfig } from '../../types/config';
import { formatBytes } from '../../utils/format';
import {
  logViewerFileName,
  logViewerUrl,
  MAX_LOG_PREVIEW_BYTES,
  type LogViewerTarget,
} from '../../types/logs';
import { AppDrawer } from '../common/AppDrawer';
import { CopyButton } from '../common/CopyButton';
import { ParagraphPlaceholder } from '../common/ContentPlaceholder';
import { EmptyState } from '../common/EmptyState';
import { LoadFailure, Notice } from '../feedback';
import styles from './Logs.module.css';

export interface LogFileViewerProps {
  /** The file on display; null closes the viewer. */
  target: LogViewerTarget | null;
  onClose: () => void;
}

/**
 * One gateway log file read in place: the request, what was sent upstream and what came back.
 *
 * The viewer reads at most the first MAX_LOG_PREVIEW_BYTES of the redacted file and stops; Copy
 * copies only that. Download is a separate browser download of the whole redacted file from
 * the same route, so a large file is never held in the tab. Each is its own audited read.
 * Nothing is kept after the viewer closes: the text quotes request content.
 */
export const LogFileViewer: React.FC<LogFileViewerProps> = ({ target, onClose }) => {
  useTimeZone();
  const t = useT();
  const isOpen = target !== null;
  useOverlayHistory({ isOpen, onClose });
  // The panel keeps its title and body while it slides out.
  const shownRef = React.useRef(target);
  if (target) shownRef.current = target;
  const shown = shownRef.current;
  const url = shown ? logViewerUrl(getAppConfig().apiBaseUrl, shown) : '';

  const query = useQuery({
    queryKey: ['log-file', shown?.kind, url],
    queryFn: () => api.readLogPreview(url, MAX_LOG_PREVIEW_BYTES),
    enabled: isOpen,
    meta: { silent: true },
    retry: false,
    staleTime: Infinity,
    gcTime: 0,
  });

  const fileName = shown ? logViewerFileName(shown) : '';
  const loaded = query.data;
  const errorCode = query.isError ? apiErrorCode(query.error) : undefined;

  let body: React.ReactNode;
  if (query.isPending) {
    body = <ParagraphPlaceholder rows={10} />;
  } else if (errorCode === 'request_log_not_found') {
    // CPA keeps a file per request only while request logging is on, and otherwise its newest
    // few error logs: its absence is an answer about capture and retention, not a read to retry.
    body = <Notice tone="warning" title={t('logs.viewer_missing')} description={t('logs.viewer_missing_desc')} />;
  } else if (errorCode === 'capture_pending') {
    body = (
      <Notice
        tone="info"
        description={t('logs.viewer_pending')}
        action={<Button size="small" onClick={() => void query.refetch()}>{t('common.retry')}</Button>}
      />
    );
  } else if (query.isError) {
    body = <LoadFailure title={t('logs.viewer_failed')} error={query.error} onRetry={() => void query.refetch()} />;
  } else if (loaded && loaded.text.trim() === '' && loaded.complete) {
    body = <EmptyState size="compact" title={t('logs.viewer_empty')} />;
  } else {
    body = (
      <>
        {loaded && !loaded.complete && (
          <Notice tone="info" description={t('logs.viewer_preview_partial', { limit: formatBytes(MAX_LOG_PREVIEW_BYTES) })} />
        )}
        <pre className={styles['viewer-text']} tabIndex={0}>{loaded?.text}</pre>
      </>
    );
  }

  return (
    <AppDrawer
      width="wide"
      open={isOpen}
      onClose={onClose}
      destroyOnHidden
      title={<span className={styles['viewer-title']}>{fileName}</span>}
      footerStart={shown?.kind === 'file' ? (
        <span className={styles['viewer-meta']}>
          {formatBytes(shown.file.size)}
          {shown.file.modified ? ` · ${dayjs.unix(shown.file.modified).format('MM-DD HH:mm:ss')}` : ''}
        </span>
      ) : undefined}
      footer={(
        <>
          <CopyButton
            showLabel
            size="middle"
            label={loaded && !loaded.complete ? t('logs.copy_preview') : undefined}
            text={loaded?.text ?? ''}
            disabled={!loaded?.text}
          />
          <Button
            type="primary"
            icon={<DownloadOutlined />}
            disabled={!loaded}
            // A browser download streams the whole redacted file to disk instead of into the tab.
            href={loaded ? url : undefined}
            download={fileName}
          >
            {t('logs.download')}
          </Button>
        </>
      )}
    >
      {body}
    </AppDrawer>
  );
};
