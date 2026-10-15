import React from 'react';
import clsx from 'clsx';
import { Button, Select } from 'antd';
import { useQuery } from '@tanstack/react-query';
import { Link, useSearchParams } from 'react-router-dom';
import { api, ApiError } from '../api/client';
import { useT } from '../i18n';
import { LobeIcon, ProviderBrandIcon } from '../components/LobeIcon';
import { ArrowLeftOutlined, BoxOutlined, CheckOutlined, CloseOutlined, CopyOutlined, DownOutlined, ExternalLinkOutlined, LayersOutlined, RightOutlined, UpOutlined } from '../components/icons';
import { PageHeader } from '../components/common/PageHeader';
import { PageLoading } from '../components/common/PageLoading';
import { RefreshButton } from '../components/common/RefreshButton';
import { credentialProviderIconId, getCredentialProviderMetadata } from '../components/common/providerMetadata';
import { LoadFailure, Notice, useToast } from '../components/feedback';
import { useOpenPriceEditor } from '../components/pricing/PricingEditorContext';
import { PRICING_QUERY_KEYS } from '../components/pricing/pricingQueries';
import { useOverlayHistory } from '../hooks/useOverlayHistory';
import { copyText } from '../utils/clipboard';
import { formatRatePer1M } from '../types/pricingDisplay';
import { isUsageFacetsResponse } from '../types/usageEvents';
import { MODEL_REQUEST_FACET_QUERY, buildModelLedger, modelRequestsLink, type ModelLedger } from '../types/modelSquareLedger';
import {
  UNRESOLVED_CONNECTION,
  buildModelReferenceLinks,
  buildModelSquareCards,
  classifyModelOpenness,
  countModelManufacturers,
  filterModelSquareCards,
  isSafeReferenceURL,
  resolveModelManufacturer,
  summarizeModelCards,
  type ModelCard,
  type ModelCardView,
  type ModelConnection,
  type ModelManufacturer,
  type ModelReference,
  type ModelReferenceLink,
  type ModelRouteConfiguration,
  type ModelSquareProvider,
} from '../types/modelSquare';
import styles from './ModelSquarePage.module.css';
import { SearchInput } from '../components/common/SearchInput';
import { EmptyState } from '../components/common/EmptyState';
import { SectionHeading } from '../components/common/SectionHeading';
import { AppDrawer } from '../components/common/AppDrawer';
import { Toolbar } from '../components/common/Toolbar';
import { Pager } from '../components/common/Pager';
import { Count } from '../components/common/Count';

const PAGE_SIZE = 120;
/** A card previews this many connections; its header opens the rest. */
const PREVIEW_CONNECTIONS = 3;
const KNOWN_MODALITIES = ['text', 'image', 'audio', 'video', 'pdf'];

type Translate = ReturnType<typeof useT>;
type OpenPriceEditor = (model: string) => void;

function manufacturerLabel(manufacturer: ModelManufacturer, t: Translate): string {
  return manufacturer.name || t(manufacturer.id === 'multiple' ? 'models.multiple_makers' : 'models.unknown_maker');
}

/** Below this size a framed glyph is a smudge, so the glyph stands alone. */
const FRAMED_MARK_MIN_SIZE = 20;

/**
 * A maker's mark: its brand artwork, or the console's own glyph where there is no brand to show.
 *
 * The two groups that are not a maker - several makers behind one name, and a maker nobody
 * identified - are drawn from the console's palette rather than at brand weight, so they sit
 * beside the artwork as quiet chrome instead of reading as a tenth, louder brand.
 */
const ManufacturerMark: React.FC<{ manufacturer: ModelManufacturer; size: number }> = ({ manufacturer, size }) => {
  const iconId = manufacturer.modelIconId || manufacturer.iconId;
  if (iconId) return <LobeIcon iconId={iconId} size={size} />;
  const Glyph = manufacturer.id === 'multiple' ? LayersOutlined : BoxOutlined;
  const isFramed = size >= FRAMED_MARK_MIN_SIZE;
  return <span className={clsx(styles['generic-mark'], isFramed && styles['generic-mark-framed'])} style={{ width: size, height: size }} aria-hidden="true">
    <Glyph size={isFramed ? Math.round(size * 0.6) : size} strokeWidth={1.5} />
  </span>;
};

// A connection named after its own adapter ("codex") carries no name of its own, so the adapter's
// display label stands in for it.
function describeProvider(provider: ModelSquareProvider): { name: string; iconId: string } {
  const hasOwnName = provider.name !== '' && provider.name.toLowerCase() !== provider.family.toLowerCase();
  return {
    name: hasOwnName ? provider.name : getCredentialProviderMetadata(provider.family).label,
    iconId: provider.icon_id || credentialProviderIconId(provider.family, provider.name),
  };
}

/** Where a name's requests go: the gateway, not this view, picks among its connections. */
type AliasScopeLabel = { kind: 'global' | 'shared' | 'unknown' } | { kind: 'provider'; connection: string };

function aliasScopeLabel(card: ModelCard, alias: string): AliasScopeLabel {
  const owner = card.connections.find(connection => connection.scopedAliases.includes(alias));
  if (owner) return { kind: 'provider', connection: owner.name };
  if (!card.globalAliases.includes(alias)) return { kind: 'unknown' };
  const targets = card.connections.filter(connection => connection.routes.some(route => route.call_point === alias));
  return { kind: targets.length > 1 ? 'shared' : 'global' };
}

/**
 * An exact client name's copy control, shown on hover or focus of its group.
 *
 * The check mark stays until the pointer or focus enters the group again, rather than on a timer:
 * an operator looking back at the row still sees which name went to the clipboard. A copy that
 * finishes after the group was re-entered or the name changed does not mark the new state.
 */
export const NameGroup: React.FC<{ alias?: string; isQuick?: boolean; className?: string; children: React.ReactNode }> = ({ alias, isQuick, className, children }) => {
  const t = useT();
  const toast = useToast();
  const [copied, setCopied] = React.useState<string>();
  const attempt = React.useRef(0);
  // The selection fallback (no `navigator.clipboard`, e.g. plain HTTP) moves focus to a scratch
  // field outside the group and back; that round trip is the copy itself, not a re-entry.
  const isCopying = React.useRef(false);
  const reset = () => { attempt.current += 1; setCopied(undefined); };
  // A different name, or none, makes any pending copy result stale.
  React.useEffect(() => () => { attempt.current += 1; setCopied(undefined); }, [alias]);
  const copy = async () => {
    if (!alias) return;
    const current = ++attempt.current;
    isCopying.current = true;
    const isCopied = await copyText(alias).finally(() => { isCopying.current = false; });
    if (current !== attempt.current) return;
    if (!isCopied) {
      toast.error(t('common.copy_failed'));
      return;
    }
    setCopied(alias);
  };
  const hasCopied = alias !== undefined && copied === alias;
  return <span
    className={clsx(styles['name-group'], className)}
    onPointerEnter={reset}
    onFocus={event => { if (!isCopying.current && !event.currentTarget.contains(event.relatedTarget)) reset(); }}
  >
    {children}
    {alias && <button
      type="button"
      className={clsx(styles['copy-button'], isQuick && styles['quick-copy'])}
      data-copy-model={alias}
      data-copied={hasCopied || undefined}
      onClick={() => void copy()}
      aria-label={t(hasCopied ? 'models.copied_named' : 'models.copy_named', { model: alias })}
      title={t(hasCopied ? 'models.copied_named' : 'models.copy_named', { model: alias })}
    >
      {hasCopied ? <CheckOutlined /> : <CopyOutlined />}
    </button>}
    <span className="sr-only" aria-live="polite">{hasCopied ? t('models.copied_named', { model: alias }) : ''}</span>
  </span>;
};

interface PriceControlProps {
  names: readonly string[];
  scope: 'global' | 'provider';
  ledger: ModelLedger;
  onSetPrice?: OpenPriceEditor;
  onOpenNames: () => void;
}

/**
 * The price-book rate for a scope's one exact name, or why there is no single rate to show.
 *
 * Several names never share a rate here: each has its own price row, so the control opens them.
 */
const PriceControl: React.FC<PriceControlProps> = ({ names, scope, ledger, onSetPrice, onOpenNames }) => {
  const t = useT();
  if (names.length === 0) return <span className={styles['price-state']}>{t(scope === 'global' ? 'models.no_global_name' : 'models.no_scoped_name')}</span>;
  if (names.length > 1) return <button type="button" className={styles['price-button']} onClick={onOpenNames}>{t('models.per_name_prices')}</button>;
  const [name] = names;
  const { price } = ledger.entryFor(name);
  if (price.status === 'unknown') return <span className={styles['price-state']}>{t('models.price_unavailable')}</span>;
  const label = price.status === 'priced' ? `${formatRatePer1M(price.price.prompt_price_per_1m)} / ${formatRatePer1M(price.price.completion_price_per_1m)}` : t('pricing.set_price');
  if (!onSetPrice) return <span className={styles['price-state']}>{price.status === 'priced' ? label : t('models.unpriced')}</span>;
  return <button
    type="button"
    className={clsx(styles['price-button'], price.status === 'unpriced' && styles['set-price'])}
    data-price-model={name}
    onClick={() => onSetPrice(name)}
    aria-label={t(price.status === 'unpriced' ? 'models.set_price_for' : 'models.edit_price_for', { model: name })}
    title={t('models.rate_legend')}
  >{label}</button>;
};

function connectionLabel(connection: ModelConnection, t: Translate): string {
  return connection.name || t('models.unresolved_connection');
}

/**
 * Same-named connections in one list need their host or upstream model to tell them apart; when
 * those match too, the connection's own ID does.
 */
function connectionHint(connection: ModelConnection, siblings: readonly ModelConnection[], providers: ReadonlyMap<string, ModelSquareProvider>): string {
  if (!connection.providerId) return '';
  const describe = (item: ModelConnection) => {
    const provider = providers.get(item.providerId);
    return [provider && !provider.is_oauth ? provider.endpoint_host : '', item.upstreamModel].filter(Boolean).join(' · ');
  };
  const namesakes = siblings.filter(sibling => sibling.name === connection.name);
  if (namesakes.length < 2) return '';
  const hint = describe(connection);
  return namesakes.some(sibling => sibling !== connection && describe(sibling) === hint) ? [hint, connection.providerId].filter(Boolean).join(' · ') : hint;
}

interface ConnectionRowProps {
  card: ModelCard;
  connection: ModelConnection;
  siblings: readonly ModelConnection[];
  providers: ReadonlyMap<string, ModelSquareProvider>;
  ledger: ModelLedger;
  onOpenConnection: (connectionKey: string) => void;
  onSetPrice?: OpenPriceEditor;
}

const ConnectionRow: React.FC<ConnectionRowProps> = ({ card, connection, siblings, providers, ledger, onOpenConnection, onSetPrice }) => {
  const t = useT();
  const provider = providers.get(connection.providerId);
  const label = connectionLabel(connection, t);
  const hint = connectionHint(connection, siblings, providers);
  return <li className={styles['connection-row']} data-connection={connection.providerId || UNRESOLVED_CONNECTION}>
    <NameGroup alias={connection.scopedAliases.length === 1 ? connection.scopedAliases[0] : undefined} isQuick className={styles['connection-group']}>
      <button type="button" className={styles['connection-name']} onClick={() => onOpenConnection(connection.key)} aria-label={t('models.through_named', { model: card.title, connection: label })}>
        {provider ? <ProviderBrandIcon iconId={describeProvider(provider).iconId} size={16} /> : <BoxOutlined />}
        <span className={styles['connection-label']}>{label}{hint && <span className={styles['connection-hint']}> · {hint}</span>}</span>
      </button>
    </NameGroup>
    <PriceControl names={connection.scopedAliases} scope="provider" ledger={ledger} onSetPrice={onSetPrice} onOpenNames={() => onOpenConnection(connection.key)} />
  </li>;
};

function connectionCountLabel(shown: number, total: number, t: Translate): string {
  if (shown < total) return t('models.connection_count_filtered', { shown, total });
  return t(total === 1 ? 'models.connection_count_one' : 'models.connection_count', { count: total });
}

function inputBadges(card: ModelCard, t: Translate): string[] {
  if (card.isMixed) return [t('models.mixed_models')];
  const inputs = card.reference?.modalities.input;
  if (!inputs?.length) return [t('models.reference_unknown')];
  return inputs.map(value => (KNOWN_MODALITIES.includes(value) ? t(`models.input_badge.${value}`) : value));
}

interface ModelCardItemProps {
  view: ModelCardView;
  providers: ReadonlyMap<string, ModelSquareProvider>;
  ledger: ModelLedger;
  onOpen: (cardKey: string, view: PanelView, connectionKey?: string) => void;
  onSetPrice?: OpenPriceEditor;
}

const ModelCardItem: React.FC<ModelCardItemProps> = React.memo(({ view: { card, connections }, providers, ledger, onOpen, onSetPrice }) => {
  const t = useT();
  return <li className={styles['card']} data-model-card={card.key}>
    <div className={styles['card-main']}>
      <span className={styles['eyebrow']}>{manufacturerLabel(card.manufacturer, t)}</span>
      <NameGroup alias={card.globalAliases.length === 1 ? card.globalAliases[0] : undefined} isQuick className={styles['title-group']}>
        <button type="button" className={styles['card-title']} data-model-identity={card.identity} onClick={() => onOpen(card.key, 'global')} aria-label={t('models.details_for', { model: card.title })}>
          <ManufacturerMark manufacturer={card.isMixed ? { id: 'multiple', name: '' } : card.manufacturer} size={28} />
          <h3>{card.title}</h3>
        </button>
      </NameGroup>
      <div className={styles['global-price']}>
        <PriceControl names={card.globalAliases} scope="global" ledger={ledger} onSetPrice={onSetPrice} onOpenNames={() => onOpen(card.key, 'global')} />
      </div>
      <ul className={styles['badges']} aria-label={t('models.reference_inputs')}>
        {inputBadges(card, t).map(badge => <li key={badge} className={styles['badge']}>{badge}</li>)}
      </ul>
    </div>
    <button type="button" className={styles['served-head']} onClick={() => onOpen(card.key, 'list')}>
      <span>{t(card.isMixed ? 'models.possible_targets' : 'models.served_by')}</span>
      <span>{connectionCountLabel(connections.length, card.connections.length, t)}<RightOutlined /></span>
    </button>
    <ul className={styles['connection-preview']}>
      {connections.slice(0, PREVIEW_CONNECTIONS).map(connection => <ConnectionRow key={connection.key} card={card} connection={connection} siblings={card.connections} providers={providers} ledger={ledger} onOpenConnection={key => onOpen(card.key, 'provider', key)} onSetPrice={onSetPrice} />)}
    </ul>
  </li>;
});
ModelCardItem.displayName = 'ModelCardItem';

const ReferenceLinks: React.FC<{ links: readonly Pick<ModelReferenceLink, 'label' | 'url'>[] }> = ({ links }) => <>
  {links.filter(link => isSafeReferenceURL(link.url)).map(link => <a key={link.url} className={styles['reference-link']} href={link.url} target="_blank" rel="noopener noreferrer" referrerPolicy="no-referrer">
    {link.label}<ExternalLinkOutlined />
  </a>)}
</>;

const ModelProfile: React.FC<{ identity: string; metadata?: ModelReference }> = ({ identity, metadata }) => {
  const t = useT();
  const maker = resolveModelManufacturer(identity, metadata);
  const unknown = <span className={styles['is-absent']}>{t('models.unknown')}</span>;
  const text = (value?: string) => value || unknown;
  const limit = (value?: number) => value ? value.toLocaleString() : unknown;
  const modalities = (values?: string[]) => values?.length ? values.map(value => (KNOWN_MODALITIES.includes(value) ? t(`models.modality.${value}`) : value)).join(' · ') : unknown;
  const flag = (value?: boolean) => {
    if (value === undefined) return unknown;
    return value
      ? <span className={styles['flag']}><CheckOutlined className={styles['flag-yes']} />{t('models.supported')}</span>
      : <span className={clsx(styles['flag'], styles['is-absent'])}><CloseOutlined />{t('models.unsupported')}</span>;
  };
  const openness = metadata && classifyModelOpenness(metadata);
  const weightLinks = metadata?.weights?.filter(link => isSafeReferenceURL(link.url)) ?? [];
  const resourceLinks = metadata?.links?.filter(link => isSafeReferenceURL(link.url)) ?? [];

  return <section className={styles['model-profile']}>
    <div className={styles['profile-heading']}>
      <ManufacturerMark manufacturer={maker} size={32} />
      <div>
        <strong>{metadata?.name || identity}</strong>
        <code>{metadata?.id || identity}</code>
      </div>
    </div>
    {metadata ? <>
      {metadata.description && <p className={styles['model-description']}>{metadata.description}</p>}
      <dl className={clsx(styles['stat-tiles'], styles['limit-tiles'])}>
        <div><dt>{t('models.context_window')}</dt><dd>{limit(metadata.limit.context)}</dd></div>
        <div><dt>{t('models.output_limit')}</dt><dd>{limit(metadata.limit.output)}</dd></div>
        <div><dt>{t('models.input_limit')}</dt><dd>{limit(metadata.limit.input)}</dd></div>
      </dl>
      <SectionHeading level="label" className={styles['fact-heading']} title={t('models.section_capabilities')} />
      <dl className={styles['model-facts']}>
        <div><dt>{t('models.input_modalities')}</dt><dd>{modalities(metadata.modalities.input)}</dd></div>
        <div><dt>{t('models.output_modalities')}</dt><dd>{modalities(metadata.modalities.output)}</dd></div>
        <div><dt>{t('models.reasoning')}</dt><dd>{flag(metadata.reasoning)}</dd></div>
        <div><dt>{t('models.tool_call')}</dt><dd>{flag(metadata.tool_call)}</dd></div>
        <div><dt>{t('models.structured_output')}</dt><dd>{flag(metadata.structured_output)}</dd></div>
        <div><dt>{t('models.attachment')}</dt><dd>{flag(metadata.attachment)}</dd></div>
      </dl>
      <SectionHeading level="label" className={styles['fact-heading']} title={t('models.section_release')} />
      <dl className={styles['model-facts']}>
        <div><dt>{t('models.openness')}</dt><dd>{openness ? t(`models.openness.${openness}`) : unknown}</dd></div>
        <div><dt>{t('models.license')}</dt><dd>{text(metadata.license)}</dd></div>
        <div><dt>{t('models.release_date')}</dt><dd>{text(metadata.release_date)}</dd></div>
        <div><dt>{t('models.knowledge')}</dt><dd>{text(metadata.knowledge)}</dd></div>
        <div><dt>{t('models.last_updated')}</dt><dd>{text(metadata.last_updated)}</dd></div>
      </dl>
      {/* A model the source marks closed has no weights to link to, so only an open or unstated one reports a missing link. */}
      {(weightLinks.length > 0 || metadata.open_weights !== false) && <div className={styles['link-row']}>
        <h3>{t('models.weights')}</h3>
        {weightLinks.length > 0 ? <ReferenceLinks links={weightLinks} /> : <span className={styles['is-absent']}>{t('models.weights_unknown')}</span>}
      </div>}
      {resourceLinks.length > 0 && <div className={styles['link-row']}><h3>{t('models.resources')}</h3><ReferenceLinks links={resourceLinks} /></div>}
    </> : <div className={styles['profile-notice']}><Notice tone="info" title={t('models.metadata_unknown')} /></div>}
    <div className={styles['link-row']} aria-label={t('models.external_resources')}>
      <h3>{t('models.external_resources')}</h3>
      <ReferenceLinks links={buildModelReferenceLinks(identity, metadata)} />
    </div>
  </section>;
};

interface ClientNamesProps {
  card: ModelCard;
  title: string;
  names: readonly string[];
  empty: string;
  ledger: ModelLedger;
  onSetPrice?: OpenPriceEditor;
}

/** Each exact name with its own copy, price and request-history actions; never a sibling's. */
const ClientNames: React.FC<ClientNamesProps> = ({ card, title, names, empty, ledger, onSetPrice }) => {
  const t = useT();
  const scopeText = (alias: string) => {
    const scope = aliasScopeLabel(card, alias);
    return scope.kind === 'provider' ? t('models.scope.provider', { connection: scope.connection }) : t(`models.scope.${scope.kind}`);
  };
  return <section className={styles['client-names']}>
    <h3>{title}</h3>
    {names.length === 0 ? <p className={styles['is-absent']}>{empty}</p> : <ul>
      {names.map(alias => {
        const { recentRequests } = ledger.entryFor(alias);
        return <li key={alias} data-client-name={alias}>
          <NameGroup alias={alias} className={styles['client-name']}><code>{alias}</code></NameGroup>
          <span className={styles['client-scope']}>{scopeText(alias)}</span>
          <span className={styles['client-actions']}>
            <PriceControl names={[alias]} scope="global" ledger={ledger} onSetPrice={onSetPrice} onOpenNames={() => undefined} />
            <span className={styles['client-requests']}>{t('models.requests_24h')}: {recentRequests === undefined ? <span className={styles['is-absent']}>{t('models.unknown')}</span> : recentRequests.toLocaleString()}</span>
            <Link className={styles['requests-link']} to={modelRequestsLink(alias)}>{t('models.view_requests')}<RightOutlined /></Link>
          </span>
        </li>;
      })}
    </ul>}
  </section>;
};

function listText(values: string[] | null, absent: string, empty: string): string {
  if (values === null) return absent;
  return values.length === 0 ? empty : values.join(' · ');
}

const ConfigurationFacts: React.FC<{ configuration: ModelRouteConfiguration | null }> = ({ configuration }) => {
  const t = useT();
  if (!configuration) return <p className={styles['is-absent']}>{t('models.config_unavailable')}</p>;
  const notConfigured = t('models.config_not_configured');
  const none = t('models.config_none');
  const limit = (value: number | null) => value === null ? notConfigured : value.toLocaleString();
  return <dl className={styles['model-facts']}>
    <div><dt>{t('models.config_operations')}</dt><dd>{listText(configuration.operations, t('models.config_inherited'), none)}</dd></div>
    <div><dt>{t('models.config_native')}</dt><dd>{listText(configuration.native, t('models.config_no_native'), none)}</dd></div>
    <div><dt>{t('models.config_input')}</dt><dd>{listText(configuration.input_modalities, notConfigured, none)}</dd></div>
    <div><dt>{t('models.config_output')}</dt><dd>{listText(configuration.output_modalities, notConfigured, none)}</dd></div>
    <div><dt>{t('models.config_context')}</dt><dd>{limit(configuration.max_context_length)}</dd></div>
    <div><dt>{t('models.config_output_limit')}</dt><dd>{limit(configuration.max_output_tokens)}</dd></div>
  </dl>;
};

/**
 * Settings shared by every row on a connection are shown once; otherwise each exact name lists
 * every distinct setting it carries, since one name can keep several configured rows.
 */
export const ConnectionConfiguration: React.FC<{ connection: ModelConnection }> = ({ connection }) => {
  const t = useT();
  const variants = new Map<string, { alias: string; configuration: ModelRouteConfiguration | null }>();
  for (const route of connection.routes) variants.set(JSON.stringify([route.call_point, route.configuration]), { alias: route.call_point, configuration: route.configuration });
  const distinct = new Set([...variants.values()].map(variant => JSON.stringify(variant.configuration)));
  return <section className={styles['configuration']}>
    <h3>{t('models.configuration')}</h3>
    {distinct.size <= 1
      ? <ConfigurationFacts configuration={connection.routes[0]?.configuration ?? null} />
      : [...variants.entries()].sort(([leftKey, left], [rightKey, right]) => left.alias.localeCompare(right.alias) || leftKey.localeCompare(rightKey)).map(([key, variant]) => <div key={key} className={styles['configuration-alias']} data-configuration-alias={variant.alias}>
        <code>{variant.alias}</code>
        <ConfigurationFacts configuration={variant.configuration} />
      </div>)}
    <p className={styles['scope-note']}>{t('models.config_note')}</p>
  </section>;
};

type PanelView = 'global' | 'list' | 'provider';
interface Panel { cardKey: string; view: PanelView; connectionKey?: string; back?: Panel }

export const ModelSquarePage: React.FC = () => {
  const t = useT();
  const [params, setParams] = useSearchParams();
  const search = params.get('q') ?? '';
  const manufacturerId = params.get('maker') ?? '';
  const providerId = params.get('provider') ?? '';
  const [page, setPage] = React.useState(1);
  const [panel, setPanel] = React.useState<Panel>();
  const closeDetails = React.useCallback(() => setPanel(undefined), []);
  const detailsRef = React.useRef<HTMLDivElement>(null);

  const directory = useQuery({ queryKey: ['model-square'], queryFn: () => api.getModelSquare(), staleTime: 30_000 });
  const providers = React.useMemo(() => new Map((directory.data?.providers ?? []).map(provider => [provider.id, provider])), [directory.data]);
  // Cards, names and scope come from the whole directory; filters below only choose what shows.
  const cards = React.useMemo(() => directory.data ? buildModelSquareCards(directory.data, id => {
    const provider = providers.get(id);
    return provider ? describeProvider(provider).name : id;
  }) : [], [directory.data, providers]);
  // The maker strip counts what search and connection leave, so the maker filter never zeroes its siblings.
  const unmade = React.useMemo(() => filterModelSquareCards(cards, { search, providerId }), [cards, search, providerId]);
  const makers = React.useMemo(() => countModelManufacturers(unmade.map(view => view.card)), [unmade]);
  const views = React.useMemo(() => manufacturerId ? unmade.filter(view => view.card.manufacturer.id === manufacturerId) : unmade, [unmade, manufacturerId]);

  // Both neighbours are optional: a price book or request count that cannot be read leaves its
  // cells unstated and the directory itself untouched. The price book shares the pricing page's
  // cache, so a price saved in the editor reaches these rows without a read of their own.
  const pricing = useQuery({ queryKey: PRICING_QUERY_KEYS.book, queryFn: () => api.getPricing(), staleTime: 30_000, retry: false });
  const requests = useQuery({
    queryKey: ['model-square', 'recent-requests'],
    queryFn: async () => {
      const response = await api.getUsageFacets(MODEL_REQUEST_FACET_QUERY);
      if (!isUsageFacetsResponse(response)) throw new Error('usage facets response is incomplete');
      return response;
    },
    staleTime: 30_000,
    retry: false,
  });
  const ledger = React.useMemo(() => buildModelLedger(cards.flatMap(card => card.aliases), pricing.data, requests.data), [cards, pricing.data, requests.data]);
  const openPriceEditor = useOpenPriceEditor() ?? undefined;
  const refresh = () => { void directory.refetch(); void pricing.refetch(); void requests.refetch(); };

  const pageCount = Math.max(1, Math.ceil(views.length / PAGE_SIZE));
  const currentPage = Math.min(page, pageCount);
  const visibleViews = views.slice((currentPage - 1) * PAGE_SIZE, currentPage * PAGE_SIZE);
  const groups = countModelManufacturers(visibleViews.map(view => view.card)).map(({ manufacturer }) => ({
    manufacturer,
    views: visibleViews.filter(view => view.card.manufacturer.id === manufacturer.id),
    total: views.filter(view => view.card.manufacturer.id === manufacturer.id).length,
  }));

  const connectionOptions = React.useMemo(() => {
    const options = [...providers.values()].map(provider => ({ value: provider.id, label: describeProvider(provider).name }))
      .sort((left, right) => left.label.localeCompare(right.label) || left.value.localeCompare(right.value));
    if (cards.some(card => card.connections.some(connection => connection.providerId === ''))) options.push({ value: UNRESOLVED_CONNECTION, label: t('models.unresolved_connection') });
    return [{ value: '', label: t('models.all_connections') }, ...options];
  }, [providers, cards, t]);

  const updateParams = (change: (next: URLSearchParams) => void) => {
    setPage(1);
    setParams(current => { const next = new URLSearchParams(current); change(next); return next; }, { replace: true });
  };
  const setParam = (name: string, value: string) => updateParams(next => { if (value) next.set(name, value); else next.delete(name); });
  const isFiltered = search !== '' || manufacturerId !== '' || providerId !== '';
  const clearFilters = () => updateParams(next => { next.delete('q'); next.delete('maker'); next.delete('provider'); });

  const openPanel = React.useCallback((cardKey: string, view: PanelView, connectionKey?: string) => {
    setPanel(current => ({ cardKey, view, connectionKey, back: current?.cardKey === cardKey ? current : undefined }));
  }, []);
  const selectedIndex = panel ? views.findIndex(view => view.card.key === panel.cardKey) : -1;
  const selectedCard = panel ? cards.find(card => card.key === panel.cardKey) : undefined;
  // A refresh can remove the open card. The drawer and its history entry then close together,
  // so Back is never spent on a hidden overlay, and the card cannot reappear on a later read.
  const isDetailsOpen = selectedCard !== undefined;
  useOverlayHistory({ isOpen: isDetailsOpen, onClose: closeDetails });
  React.useEffect(() => { if (panel && !isDetailsOpen) setPanel(undefined); }, [panel, isDetailsOpen]);
  // Rows the filters leave; a card the filters no longer show keeps all of its own.
  const selectedRows = selectedIndex >= 0 ? views[selectedIndex].connections : selectedCard?.connections ?? [];
  // A refresh can remove the connection a panel was showing; its model details remain.
  const selectedConnection = panel?.view === 'provider' ? selectedCard?.connections.find(connection => connection.key === panel.connectionKey) : undefined;
  const stepSelection = (offset: number) => {
    const target = views[selectedIndex + offset];
    if (!target) return;
    setPanel({ cardKey: target.card.key, view: 'global' });
    // The neighbour's page must be the visible one, so closing the details lands beside it.
    setPage(Math.floor((selectedIndex + offset) / PAGE_SIZE) + 1);
    detailsRef.current?.closest('.ant-drawer-body')?.scrollTo({ top: 0 });
  };

  const errorCode = directory.error instanceof ApiError ? (directory.error.data as { code?: string })?.code : undefined;
  const needsKey = errorCode === 'client_key_required' || errorCode === 'client_key_missing';
  const summary = summarizeModelCards(cards);
  const subtitle = directory.data && t('models.summary_line', {
    models: summary.models,
    others: summary.otherCards,
    names: summary.names,
    makers: countModelManufacturers(cards).length,
    date: directory.data.metadata_updated_at,
  });

  const connectionsSection = selectedCard && <section className={styles['served-by']}>
    <h3>{t(selectedCard.isMixed ? 'models.possible_targets' : 'models.served_by')} · {connectionCountLabel(selectedRows.length, selectedCard.connections.length, t)}</h3>
    <ul>
      {selectedRows.map(connection => <ConnectionRow key={connection.key} card={selectedCard} connection={connection} siblings={selectedCard.connections} providers={providers} ledger={ledger} onOpenConnection={key => openPanel(selectedCard.key, 'provider', key)} onSetPrice={openPriceEditor} />)}
    </ul>
  </section>;
  const overview = selectedCard && <>
    <ClientNames card={selectedCard} title={t('models.use_in_client')} names={selectedCard.globalAliases} empty={t('models.no_global_name')} ledger={ledger} onSetPrice={openPriceEditor} />
    {selectedCard.otherAliases.length > 0 && <ClientNames card={selectedCard} title={t('models.other_names')} names={selectedCard.otherAliases} empty="" ledger={ledger} onSetPrice={openPriceEditor} />}
  </>;
  const profiles = selectedCard?.profiles.map(profile => <ModelProfile key={profile.metadata?.id ?? profile.identity} identity={profile.identity} metadata={profile.metadata} />);
  const selectedProvider = selectedConnection && providers.get(selectedConnection.providerId);
  const connectionProfiles = selectedCard && selectedConnection && selectedCard.profiles.filter(profile => selectedConnection.routes.some(route => (route.reference_id ? profile.metadata?.id === route.reference_id : !profile.metadata && profile.identity === (route.upstream_model || route.call_point))));

  return <div className={clsx(styles['page'], 'terminal-page', 'model-square-page')}>
    <PageHeader title={t('nav.model_square')} subtitle={subtitle} actions={<RefreshButton isRefreshing={directory.isFetching || pricing.isFetching || requests.isFetching} onRefresh={refresh} />} />
    {directory.isError && <LoadFailure title={t(needsKey ? 'models.key_required' : 'models.load_failed')} error={directory.error} onRetry={() => void directory.refetch()} />}
    {needsKey && <Link to="/api-keys">{t('models.manage_keys')}</Link>}
    {directory.isPending ? <PageLoading variant="block" /> : directory.data && <>
      <Toolbar>
        <SearchInput className={styles['search']} label={t('models.search')} value={search} onChange={(text) => setParam('q', text)} />
        <Select className={styles['connection-filter']} value={providerId} onChange={(value: string) => setParam('provider', value)} aria-label={t('models.filter_connections')} options={connectionOptions} popupMatchSelectWidth={false} />
        <span className={styles['result-count']} role="status">
          {isFiltered ? t('models.showing', { shown: views.length, total: cards.length }) : ''}
        </span>
        {isFiltered && <Button type="link" size="small" className={styles['clear-filters']} onClick={clearFilters}>{t('models.clear_filters')}</Button>}
      </Toolbar>
      <div className={clsx(styles['chip-group'], styles['maker-strip'])} role="group" aria-label={t('models.filter_makers')}>
        <button type="button" className={styles['chip']} aria-pressed={manufacturerId === ''} onClick={() => setParam('maker', '')}>
          {t('common.all')}<Count size="small">{unmade.length}</Count>
        </button>
        {makers.map(({ manufacturer, count }) => <button key={manufacturer.id} type="button" className={styles['chip']} data-maker={manufacturer.id} aria-pressed={manufacturerId === manufacturer.id} onClick={() => setParam('maker', manufacturerId === manufacturer.id ? '' : manufacturer.id)}>
          <ManufacturerMark manufacturer={manufacturer} size={14} />
          {manufacturerLabel(manufacturer, t)}<Count size="small">{count}</Count>
        </button>)}
      </div>
      <p className={styles['rate-legend']}>{t('models.rate_legend')}</p>
      {directory.data.partial.length > 0 && <Notice tone="warning" title={t('models.provenance_partial')} />}
      {views.length === 0 ? <EmptyState className={styles['empty']} title={t(directory.data.models.length ? 'models.no_results' : 'models.empty')}>
        {directory.data.models.length ? <Button onClick={clearFilters}>{t('models.clear_filters')}</Button> : <Link to="/ai-providers">{t('models.manage_connections')}</Link>}
      </EmptyState> : <ul className={styles['group-list']}>
        {groups.map(group => <li className={styles['model-group']} key={group.manufacturer.id} data-model-group={group.manufacturer.id}>
          <div className={styles['group-heading']}>
            <div className={styles['group-title']}>
              <ManufacturerMark manufacturer={group.manufacturer} size={20} />
              <h2>{manufacturerLabel(group.manufacturer, t)}</h2>
              <span>{t('models.group_count', { count: group.total })}</span>
            </div>
          </div>
          <ul className={styles['cards']}>
            {group.views.map(view => <ModelCardItem key={view.card.key} view={view} providers={providers} ledger={ledger} onOpen={openPanel} onSetPrice={openPriceEditor} />)}
          </ul>
        </li>)}
      </ul>}
      <Pager className={styles['pagination']} page={currentPage} pageSize={PAGE_SIZE} total={views.length} onChange={setPage} />
    </>}
    <AppDrawer
      title={selectedCard ? <span className={styles['drawer-title']}>
        <ManufacturerMark manufacturer={selectedCard.isMixed ? { id: 'multiple', name: '' } : selectedCard.manufacturer} size={24} />
        <span>{selectedConnection ? `${selectedCard.title} · ${connectionLabel(selectedConnection, t)}` : selectedCard.title}</span>
      </span> : ''}
      open={isDetailsOpen}
      onClose={closeDetails}
      className={styles['details-drawer']}
      destroyOnHidden
      footerStart={selectedCard && selectedIndex >= 0 && views.length > 1 && <>
        <Button icon={<UpOutlined />} disabled={selectedIndex === 0} onClick={() => stepSelection(-1)}>{t('models.previous')}</Button>
        <Button icon={<DownOutlined />} disabled={selectedIndex === views.length - 1} onClick={() => stepSelection(1)}>{t('models.next')}</Button>
      </>}
      footer={selectedCard && selectedIndex >= 0 && views.length > 1 && <span className={styles['details-position']}>{selectedIndex + 1} / {views.length}</span>}
    >
      {selectedCard && <div ref={detailsRef} data-panel={selectedConnection ? 'provider' : panel?.view}>
        {panel?.back && <Button type="link" className={styles['panel-back']} icon={<ArrowLeftOutlined />} onClick={() => setPanel(panel.back)}>{t('models.back')}</Button>}
        {selectedConnection ? <>
          <section className={styles['connection-facts']}>
            <h3>{t('models.through', { connection: connectionLabel(selectedConnection, t) })}</h3>
            {selectedConnection.providerId ? <dl className={styles['model-facts']}>
              <div><dt>{t('models.endpoint')}</dt><dd>{selectedProvider?.is_oauth ? 'OAuth' : selectedProvider?.endpoint_host || <span className={styles['is-absent']}>{t('models.unknown')}</span>}</dd></div>
              <div><dt>{t('models.upstream_model')}</dt><dd><code>{selectedConnection.upstreamModel || t('models.unknown')}</code></dd></div>
            </dl> : <p className={styles['is-absent']}>{t('models.provenance_unknown')}</p>}
          </section>
          <ClientNames card={selectedCard} title={t('models.use_in_client')} names={selectedConnection.scopedAliases} empty={t('models.no_scoped_name')} ledger={ledger} onSetPrice={openPriceEditor} />
          {selectedConnection.providerId && <ConnectionConfiguration connection={selectedConnection} />}
          <details className={styles['canonical-reference']}>
            <summary>{t('models.canonical_reference')}</summary>
            {connectionProfiles?.map(profile => <ModelProfile key={profile.metadata?.id ?? profile.identity} identity={profile.identity} metadata={profile.metadata} />)}
          </details>
        </> : panel?.view === 'list' ? <>{connectionsSection}{overview}{profiles}</> : <>{overview}{connectionsSection}{profiles}</>}
        <p className={styles['scope-note']}>
          {t('models.reference_note')}
          {directory.data?.metadata_updated_at && <> {t('models.snapshot_date', { date: directory.data.metadata_updated_at })}</>}
        </p>
      </div>}
    </AppDrawer>
  </div>;
};
