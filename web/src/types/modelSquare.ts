import type { GatewayModelItem } from './gatewayModels';

export interface ModelSquareProvider {
  id: string;
  family: string;
  name: string;
  endpoint_host?: string;
  is_oauth: boolean;
  icon_id?: string;
}
/** How a route's client name was constructed: unprefixed, a configured provider prefix, or not established. */
export type ModelNameScope = 'global' | 'provider' | 'unknown';
/**
 * The explicit settings of the configured model row behind a route. `null` means the row does not
 * state the setting, which is not the same as an empty list it does state.
 */
export interface ModelRouteConfiguration {
  operations: string[] | null;
  native: string[] | null;
  input_modalities: string[] | null;
  output_modalities: string[] | null;
  max_context_length: number | null;
  max_output_tokens: number | null;
}
export interface ModelSquareRoute {
  provider_id: string;
  upstream_model: string;
  call_point: string;
  /** The canonical reference the server resolved for this route; empty when unresolved. */
  reference_id: string;
  name_scope: ModelNameScope;
  configuration: ModelRouteConfiguration | null;
}
export interface ModelReferenceLink { label: string; url: string; type?: string }
export interface ModelReference {
  id: string;
  name: string;
  description?: string;
  family?: string;
  open_weights?: boolean;
  reasoning?: boolean;
  tool_call?: boolean;
  structured_output?: boolean;
  attachment?: boolean;
  temperature?: boolean;
  knowledge?: string;
  release_date?: string;
  last_updated?: string;
  license?: string;
  limit: { context?: number; input?: number; output?: number };
  modalities: { input?: string[]; output?: string[] };
  weights?: ModelReferenceLink[];
  links?: ModelReferenceLink[];
}
export interface ModelSquareDirectory {
  models: GatewayModelItem[];
  providers: ModelSquareProvider[];
  routes: ModelSquareRoute[];
  partial: string[];
  model_info: Record<string, ModelReference>;
  metadata_updated_at: string;
}
export interface ModelManufacturer {
  id: string;
  name: string;
  iconId?: string;
  modelIconId?: string;
}

// A model's maker is distinct from the relay or protocol adapter carrying it.
const MODEL_MANUFACTURERS: Record<string, ModelManufacturer> = {
  openai: { id: 'openai', name: 'OpenAI', iconId: 'OpenAI', modelIconId: 'OpenAI' },
  anthropic: { id: 'anthropic', name: 'Anthropic', iconId: 'Anthropic', modelIconId: 'Claude' },
  google: { id: 'google', name: 'Google', iconId: 'Google', modelIconId: 'Gemini' },
  xai: { id: 'xai', name: 'xAI', iconId: 'XAI', modelIconId: 'Grok' },
  alibaba: { id: 'alibaba', name: 'Qwen', iconId: 'Qwen', modelIconId: 'Qwen' },
  deepseek: { id: 'deepseek', name: 'DeepSeek', iconId: 'DeepSeek', modelIconId: 'DeepSeek' },
  moonshotai: { id: 'moonshotai', name: 'Moonshot AI', iconId: 'Moonshot', modelIconId: 'Kimi' },
  zai: { id: 'zai', name: 'Z.ai', iconId: 'ZAI', modelIconId: 'Zhipu' },
  minimax: { id: 'minimax', name: 'MiniMax', iconId: 'Minimax', modelIconId: 'Minimax' },
  mistral: { id: 'mistral', name: 'Mistral AI', iconId: 'Mistral', modelIconId: 'Mistral' },
  meta: { id: 'meta', name: 'Meta', iconId: 'Meta', modelIconId: 'Meta' },
  cohere: { id: 'cohere', name: 'Cohere', iconId: 'Cohere', modelIconId: 'Cohere' },
  'bytedance-seed': { id: 'bytedance-seed', name: 'ByteDance', iconId: 'ByteDance', modelIconId: 'Doubao' },
  xiaomi: { id: 'xiaomi', name: 'Xiaomi', iconId: 'XiaomiMiMo', modelIconId: 'XiaomiMiMo' },
  inception: { id: 'inception', name: 'Inception', iconId: 'Inception', modelIconId: 'Inception' },
  bilibili: { id: 'bilibili', name: 'Bilibili', iconId: 'Bilibili', modelIconId: 'BilibiliIndex' },
  microsoft: { id: 'microsoft', name: 'Microsoft', iconId: 'Microsoft', modelIconId: 'Microsoft' },
  amazon: { id: 'amazon', name: 'Amazon', iconId: 'Aws', modelIconId: 'Nova' },
  baidu: { id: 'baidu', name: 'Baidu', iconId: 'Baidu', modelIconId: 'Wenxin' },
  tencent: { id: 'tencent', name: 'Tencent', iconId: 'Tencent', modelIconId: 'Hunyuan' },
  perplexity: { id: 'perplexity', name: 'Perplexity', iconId: 'Perplexity', modelIconId: 'Perplexity' },
  nvidia: { id: 'nvidia', name: 'NVIDIA', iconId: 'Nvidia', modelIconId: 'Nvidia' },
  stepfun: { id: 'stepfun', name: 'StepFun', iconId: 'Stepfun', modelIconId: 'Stepfun' },
};
const AUTHOR_ALIASES: Record<string, string> = { 'x-ai': 'xai', qwen: 'alibaba', 'z-ai': 'zai', zhipuai: 'zai', 'zai-org': 'zai', mistralai: 'mistral', 'meta-llama': 'meta', 'moonshotai-cn': 'moonshotai', 'deepseek-ai': 'deepseek', inceptionlabs: 'inception', 'amazon-nova': 'amazon', bytedance: 'bytedance-seed' };
const UNKNOWN_MANUFACTURER: ModelManufacturer = { id: 'unknown', name: '' };
const MODEL_FAMILY_RULES: readonly [RegExp, string][] = [
  [/^(?:gpt(?:-|\d)|chatgpt-|o[134](?:-|$)|codex(?:-|$))/, 'openai'],
  [/^claude(?:-|$)/, 'anthropic'], [/^(?:gemini|gemma)(?:-|$)/, 'google'],
  [/^grok(?:-|$)/, 'xai'], [/^(?:qwen|qwq)(?:\d|[-.]|$)/, 'alibaba'],
  [/^deepseek(?:-|$)/, 'deepseek'], [/^(?:kimi|moonshot)(?:[-\d]|$)/, 'moonshotai'],
  [/^glm(?:[-\d]|$)/, 'zai'], [/^minimax(?:[-\d]|$)/, 'minimax'],
  [/^(?:mistral|mixtral|codestral|devstral|magistral|ministral|pixtral|voxtral)(?:[-\d]|$)/, 'mistral'],
  [/^(?:llama(?:[-\d.]|$)|muse-)/, 'meta'], [/^command(?:-|$)/, 'cohere'],
  [/^(?:doubao|seed)(?:-|$)/, 'bytedance-seed'], [/^mimo(?:-|$)/, 'xiaomi'],
  [/^mercury(?:[-\d]|$)/, 'inception'], [/^index-/, 'bilibili'], [/^phi(?:[-\d]|$)/, 'microsoft'],
  [/^nova-/, 'amazon'], [/^ernie(?:[-\d]|$)/, 'baidu'], [/^hunyuan(?:-|$)/, 'tencent'],
  [/^sonar(?:-|$)/, 'perplexity'], [/^nemotron(?:-|$)/, 'nvidia'], [/^step-/, 'stepfun'],
];

/**
 * The catalog mark a model is drawn with: its maker's model mark, else the maker's own. A name
 * that resolves to no maker - a custom alias, say - is tried again as the upstream it routes to.
 */
export function modelMarkIconId(model: string, fallbackModel = '', metadata?: ModelReference): string | undefined {
  const primary = model.trim();
  const fallback = fallbackModel.trim();
  for (const [identity, reference] of [[primary, metadata], [fallback !== primary ? fallback : '', undefined]] as const) {
    if (!identity) continue;
    const maker = resolveModelManufacturer(identity, reference);
    const candidate = maker.modelIconId || maker.iconId;
    if (candidate) return candidate;
  }
  return undefined;
}

export function resolveModelManufacturer(identity: string, metadata?: ModelReference): ModelManufacturer {
  if (!identity) return UNKNOWN_MANUFACTURER;
  const author = (metadata?.id ?? identity).split('/').slice(0, -1).join('/').toLowerCase();
  const slug = identity.split('/').at(-1)?.toLowerCase() ?? '';
  const maker = MODEL_MANUFACTURERS[AUTHOR_ALIASES[author] ?? author] ?? MODEL_MANUFACTURERS[MODEL_FAMILY_RULES.find(([pattern]) => pattern.test(slug))?.[1] ?? ''];
  if (!maker) return metadata && author ? { id: author, name: author } : UNKNOWN_MANUFACTURER;
  if (maker.id === 'openai' && slug.includes('codex')) return { ...maker, modelIconId: 'Codex' };
  if (maker.id === 'google' && slug.startsWith('gemma')) return { ...maker, modelIconId: 'Gemma' };
  return maker;
}

/**
 * The models.dev context window for an exact client name, or undefined when it is not known.
 *
 * Every route the gateway may pick for the name must have known provenance and a resolved
 * reference with a window; the smallest is the one a conversation is sure to fit. One unknown
 * route, or a directory whose enrichment was partial, makes the whole estimate unknown: a number
 * from a sibling route would claim a fit nobody established. A literal match for an unresolved
 * name has no provenance and therefore never counts.
 */
export function referenceContextWindow(directory: ModelSquareDirectory | undefined, callPoint: string): number | undefined {
  if (!directory || !callPoint || directory.partial.length > 0) return undefined;
  const routes = directory.routes.filter(route => route.call_point === callPoint);
  if (!hasCompleteProvenance(routes)) return undefined;
  let smallest: number | undefined;
  for (const route of routes) {
    const window = route.reference_id ? directory.model_info[route.reference_id]?.limit.context : undefined;
    if (typeof window !== 'number' || !Number.isFinite(window) || window <= 0) return undefined;
    smallest = smallest === undefined ? window : Math.min(smallest, window);
  }
  return smallest;
}

export type ModelCardKind = 'model' | 'unresolved' | 'alias';
export type AliasScope = 'global' | 'provider' | 'unknown';

/** One provider/model pair serving a card, with every advertised name routed through it. */
export interface ModelConnection {
  key: string;
  /** Empty for a name whose provenance is not known. */
  providerId: string;
  upstreamModel: string;
  name: string;
  routes: ModelSquareRoute[];
  /** Names proven to belong to this connection alone, sorted. */
  scopedAliases: string[];
}

export interface ModelCard {
  key: string;
  kind: ModelCardKind;
  /** The canonical reference ID, the shared upstream model, or the client name the card is keyed by. */
  identity: string;
  title: string;
  manufacturer: ModelManufacturer;
  /** The one reference the whole card describes, if there is one. */
  reference?: ModelReference;
  /** Distinct models behind the card, each described once. */
  profiles: { identity: string; metadata?: ModelReference }[];
  /** Several different models may answer one of its names. */
  isMixed: boolean;
  aliases: string[];
  globalAliases: string[];
  otherAliases: string[];
  connections: ModelConnection[];
}

const tupleKey = (...parts: string[]) => JSON.stringify(parts);

/**
 * How an exact client name may be offered: as one connection's own name only when every route
 * for it is provider-scoped to that same connection; as a global or shared name when every route
 * is known; otherwise its scope is unknown and it is offered apart from both.
 */
export function classifyAliasScope(routes: readonly ModelSquareRoute[], isPartial = false): AliasScope {
  if (isPartial || !hasCompleteProvenance(routes) || routes.some(route => route.name_scope === 'unknown')) return 'unknown';
  const [first] = routes;
  const isExclusive = routes.every(route => route.name_scope === 'provider' && route.provider_id === first.provider_id && route.upstream_model === first.upstream_model);
  return isExclusive ? 'provider' : 'global';
}

function hasCompleteProvenance(routes: readonly ModelSquareRoute[]): boolean {
  return routes.length > 0 && routes.every(route => route.provider_id !== '' && route.upstream_model !== '');
}

function uniqueRoutes(routes: readonly ModelSquareRoute[]): ModelSquareRoute[] {
  const seen = new Set<string>();
  return routes.filter(route => {
    const key = JSON.stringify([route.provider_id, route.upstream_model, route.call_point, route.reference_id, route.name_scope, route.configuration]);
    if (seen.has(key)) return false;
    seen.add(key);
    return true;
  });
}

/**
 * Model cards built from the whole directory, before any display filter.
 *
 * A name joins a model card only when every route for it has known provenance and the same
 * resolved reference; unresolved names served by one provider/model pair share that pair's card;
 * anything else - several targets, a mix of known and unknown, or missing provenance - is its own
 * card keyed by the exact name, so it is never presented as one model.
 */
export function buildModelSquareCards(directory: ModelSquareDirectory, connectionName: (providerId: string) => string = id => id): ModelCard[] {
  const advertised = [...new Set(directory.models.map(model => model.id))];
  const advertisedSet = new Set(advertised);
  const routesByAlias = new Map<string, ModelSquareRoute[]>();
  for (const route of directory.routes) {
    if (!advertisedSet.has(route.call_point)) continue;
    routesByAlias.set(route.call_point, [...(routesByAlias.get(route.call_point) ?? []), route]);
  }

  // A partial directory may be missing routes for any name, so none can be shown as complete.
  const isPartial = directory.partial.length > 0;
  const cards = new Map<string, { kind: ModelCardKind; identity: string; aliases: { alias: string; routes: ModelSquareRoute[] }[] }>();
  for (const alias of advertised) {
    const routes = uniqueRoutes(routesByAlias.get(alias) ?? []);
    const isComplete = !isPartial && hasCompleteProvenance(routes);
    const [first] = routes;
    let kind: ModelCardKind = 'alias';
    let identity = alias;
    if (isComplete && first.reference_id && routes.every(route => route.reference_id === first.reference_id)) {
      kind = 'model';
      identity = first.reference_id;
    } else if (isComplete && routes.every(route => !route.reference_id && route.provider_id === first.provider_id && route.upstream_model === first.upstream_model)) {
      kind = 'unresolved';
      identity = first.upstream_model;
    }
    const key = kind === 'unresolved' ? tupleKey(kind, first.provider_id, first.upstream_model) : tupleKey(kind, identity);
    const card = cards.get(key) ?? { kind, identity, aliases: [] };
    card.aliases.push({ alias, routes });
    cards.set(key, card);
  }

  return [...cards.entries()].map(([key, { kind, identity, aliases }]): ModelCard => {
    const connections = new Map<string, ModelConnection>();
    const globalAliases: string[] = [];
    const otherAliases: string[] = [];
    for (const { alias, routes } of aliases) {
      const scope = classifyAliasScope(routes, isPartial);
      if (scope === 'global') globalAliases.push(alias);
      if (scope === 'unknown') otherAliases.push(alias);
      // A name with no route at all still gets a row, so every advertised name stays reachable.
      const rows = routes.length > 0 ? routes : [{ provider_id: '', upstream_model: '', call_point: alias, reference_id: '', name_scope: 'unknown' as const, configuration: null }];
      for (const route of rows) {
        const connectionKey = route.provider_id ? tupleKey(route.provider_id, route.upstream_model) : tupleKey('', alias);
        const connection = connections.get(connectionKey) ?? { key: connectionKey, providerId: route.provider_id, upstreamModel: route.upstream_model, name: route.provider_id ? connectionName(route.provider_id) : '', routes: [], scopedAliases: [] };
        connection.routes.push(route);
        if (scope === 'provider' && !connection.scopedAliases.includes(alias)) connection.scopedAliases.push(alias);
        connections.set(connectionKey, connection);
      }
    }
    const profiles: ModelCard['profiles'] = [];
    for (const { alias, routes } of aliases) {
      for (const route of routes.length > 0 ? routes : [undefined]) {
        const metadata = route?.reference_id ? directory.model_info[route.reference_id] : undefined;
        const profileIdentity = metadata?.id ?? (route?.upstream_model || alias);
        if (profiles.some(profile => (profile.metadata?.id ?? profile.identity) === profileIdentity)) continue;
        profiles.push({ identity: route?.upstream_model || alias, metadata });
      }
    }
    const reference = profiles.length === 1 ? profiles[0].metadata : undefined;
    const makers = profiles.map(profile => resolveModelManufacturer(profile.identity, profile.metadata));
    const manufacturer = makers.length > 0 && makers.every(maker => maker.id === makers[0].id) ? makers[0] : { id: 'multiple', name: '' };
    const sortedConnections = [...connections.values()].map(connection => ({ ...connection, scopedAliases: connection.scopedAliases.sort() }))
      .sort((left, right) => (left.name || '\uffff').localeCompare(right.name || '\uffff') || left.providerId.localeCompare(right.providerId) || left.upstreamModel.localeCompare(right.upstreamModel) || left.key.localeCompare(right.key));
    return {
      key, kind, identity,
      title: kind === 'model' ? reference?.name || identity : identity,
      manufacturer, reference, profiles,
      isMixed: profiles.length > 1,
      aliases: aliases.map(item => item.alias).sort(),
      globalAliases: globalAliases.sort(),
      otherAliases: otherAliases.sort(),
      connections: sortedConnections,
    };
  }).sort((left, right) => manufacturerRank(left.manufacturer) - manufacturerRank(right.manufacturer)
    || manufacturerSortName(left.manufacturer).localeCompare(manufacturerSortName(right.manufacturer))
    || left.title.localeCompare(right.title) || left.key.localeCompare(right.key));
}

/** The connection filter value for rows whose provenance is not known. */
export const UNRESOLVED_CONNECTION = 'unresolved';

export interface ModelCardView {
  card: ModelCard;
  /** The rows the search and connection filters leave, in display order. */
  connections: ModelConnection[];
}

/**
 * What the search, maker and connection filters leave on display.
 *
 * Filters only choose what is shown: a card's identity, names and prices come from the whole
 * directory. A match on the model itself keeps all its matching connections; a match on a
 * connection or a name keeps just the rows it matched.
 */
export function filterModelSquareCards(cards: readonly ModelCard[], filters: { search?: string; manufacturerId?: string; providerId?: string }): ModelCardView[] {
  const needle = (filters.search ?? '').trim().toLowerCase();
  const matches = (values: (string | undefined)[]) => values.some(value => value?.toLowerCase().includes(needle));
  return cards.flatMap(card => {
    if (filters.manufacturerId && card.manufacturer.id !== filters.manufacturerId) return [];
    const byProvider = card.connections.filter(connection => !filters.providerId
      || (filters.providerId === UNRESOLVED_CONNECTION ? connection.providerId === '' : connection.providerId === filters.providerId));
    const isModelMatch = !needle || matches([card.title, card.identity, card.manufacturer.name, ...card.profiles.flatMap(profile => [profile.identity, profile.metadata?.name, profile.metadata?.id, profile.metadata?.family])]);
    const connections = isModelMatch ? byProvider : byProvider.filter(connection => matches([connection.name, connection.upstreamModel, ...connection.routes.map(route => route.call_point)]));
    return connections.length > 0 ? [{ card, connections }] : [];
  });
}

// Named makers read alphabetically by the name the heading shows; the two groups that are not a
// maker follow them, so "Multiple" and "Unidentified" never interrupt the alphabet.
function manufacturerRank(manufacturer: ModelManufacturer): number {
  return manufacturer.id === 'unknown' ? 2 : manufacturer.id === 'multiple' ? 1 : 0;
}
function manufacturerSortName(manufacturer: ModelManufacturer): string {
  return (manufacturer.name || manufacturer.id).toLowerCase();
}

export interface ModelManufacturerCount {
  manufacturer: ModelManufacturer;
  count: number;
}

/** Makers in the order their sections appear, each with the number of cards it holds. */
export function countModelManufacturers(cards: readonly Pick<ModelCard, 'manufacturer'>[]): ModelManufacturerCount[] {
  const counts = new Map<string, ModelManufacturerCount>();
  for (const card of cards) {
    const current = counts.get(card.manufacturer.id);
    if (current) current.count += 1;
    else counts.set(card.manufacturer.id, { manufacturer: card.manufacturer, count: 1 });
  }
  return [...counts.values()];
}

/** Resolved models, other cards and distinct client names: three different counts, never one. */
export function summarizeModelCards(cards: readonly ModelCard[]): { models: number; otherCards: number; names: number } {
  const models = cards.filter(card => card.kind === 'model').length;
  return { models, otherCards: cards.length - models, names: new Set(cards.flatMap(card => card.aliases)).size };
}

export type ModelOpenness = 'closed' | 'open_weights' | 'open_source';

// Licences that grant use, modification and redistribution without field-of-use limits. A
// community, research or non-commercial licence publishes weights without making the model open source.
const OPEN_SOURCE_LICENSES = new Set(['mit', 'apache-2.0', 'bsd-2-clause', 'bsd-3-clause', 'mpl-2.0', 'isc', 'unlicense', 'gpl-3.0', 'lgpl-3.0', 'agpl-3.0']);

/**
 * How open a model is, from the two facts the source states separately.
 *
 * Published weights alone make a model open-weight; it is open source only when its licence is
 * one too. A model whose weights the source does not describe has no classification.
 */
export function classifyModelOpenness(metadata: Pick<ModelReference, 'open_weights' | 'license'>): ModelOpenness | undefined {
  if (metadata.open_weights === undefined) return undefined;
  if (!metadata.open_weights) return 'closed';
  const license = (metadata.license ?? '').trim().toLowerCase().replace(/\s+license$/, '').replace(/\s+/g, '-');
  return OPEN_SOURCE_LICENSES.has(license) ? 'open_source' : 'open_weights';
}

export function buildModelReferenceLinks(identity: string, metadata?: ModelReference): { label: string; url: string }[] {
  const canonical = metadata?.id ?? identity;
  const name = canonical.split('/').at(-1) || identity;
  return [
    { label: 'models.dev', url: metadata ? `https://models.dev/models/${canonical.split('/').map(encodeURIComponent).join('/')}` : 'https://models.dev/' },
    { label: 'OpenRouter', url: `https://openrouter.ai/models?q=${encodeURIComponent(name)}` },
    { label: 'pi.dev', url: `https://pi.dev/models?name=${encodeURIComponent(name)}` },
  ];
}

export function isSafeReferenceURL(value: string): boolean {
  try { const url = new URL(value); return url.protocol === 'https:' && !url.username && !url.password; } catch { return false; }
}
