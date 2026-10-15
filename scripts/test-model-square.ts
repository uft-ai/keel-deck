import assert from 'node:assert/strict';
import { test } from 'node:test';
import { buildModelReferenceLinks, buildModelSquareCards, classifyAliasScope, classifyModelOpenness, countModelManufacturers, filterModelSquareCards, isSafeReferenceURL, resolveModelManufacturer, summarizeModelCards, UNRESOLVED_CONNECTION, type ModelNameScope, type ModelReference, type ModelSquareDirectory, type ModelSquareRoute } from '../web/src/types/modelSquare.ts';
import { LOBE_ICON_CATALOG } from '../web/src/types/lobeIconCatalog.ts';

const GPT: ModelReference = { id: 'openai/gpt-5', name: 'GPT-5', open_weights: false, limit: { context: 400_000, output: 128_000 }, modalities: { input: ['text', 'image'], output: ['text'] } };
const DEEPSEEK: ModelReference = { id: 'deepseek/deepseek-v4.1-flash', name: 'DeepSeek V4.1 Flash', limit: { context: 1_000_000 }, modalities: { input: ['text'], output: ['text'] } };
const route = (provider_id: string, upstream_model: string, call_point: string, reference_id = '', name_scope: ModelNameScope = 'global'): ModelSquareRoute => ({ provider_id, upstream_model, call_point, reference_id, name_scope, configuration: null });
const directoryOf = (routes: ModelSquareRoute[], { models = [], ...extra }: Partial<ModelSquareDirectory> = {}): ModelSquareDirectory => ({
  providers: [], partial: [], metadata_updated_at: '2026-10-05', model_info: { [GPT.id]: GPT, [DEEPSEEK.id]: DEEPSEEK }, routes, ...extra,
  models: [...new Set([...routes.map(item => item.call_point), ...models.map(model => model.id)])].map(id => ({ id, call_point: id, vision: 'unknown' })),
});
const names: Record<string, string> = { work: 'Work', other: 'Other Cloud', lab: 'Lab', 'oauth:codex': 'Codex OAuth' };
const cardsOf = (directory: ModelSquareDirectory) => buildModelSquareCards(directory, id => names[id] ?? id);

// The demonstrated case: one NaN connection renamed Work with prefix team, plus a second provider
// spelling the same model differently, and names nothing proves anything about.
const DIRECTORY = directoryOf([
  route('work', 'deepseek-v4-flash', 'deepseek-v4-flash', DEEPSEEK.id, 'global'),
  route('work', 'deepseek-v4-flash', 'team/deepseek-v4-flash', DEEPSEEK.id, 'provider'),
  route('other', 'deepseek-v4.1-flash', 'other/deepseek', DEEPSEEK.id, 'provider'),
  route('work', 'gpt-5', 'team/fast', GPT.id, 'provider'),
  route('other', 'claude-sonnet-4-5', 'team/fast', '', 'provider'),
  route('lab', 'experimental-v2', 'experimental-v2', '', 'global'),
  route('lab', 'experimental-v2', 'lab/experimental-v2', '', 'provider'),
  route('other', 'experimental-v2', 'other/experimental-v2', '', 'provider'),
  route('oauth:codex', 'gpt-5', 'gpt-5', GPT.id, 'unknown'),
], { models: [{ id: 'runtime-only', call_point: 'runtime-only', vision: 'unknown' }] });

test('names join a model card only through one verified reference', () => {
  const cards = cardsOf(DIRECTORY);
  const deepseek = cards.find(card => card.key === JSON.stringify(['model', DEEPSEEK.id]))!;
  assert.equal(deepseek.kind, 'model');
  assert.equal(deepseek.title, 'DeepSeek V4.1 Flash');
  assert.equal(deepseek.reference?.id, DEEPSEEK.id);
  assert.deepEqual(deepseek.aliases, ['deepseek-v4-flash', 'other/deepseek', 'team/deepseek-v4-flash']);
  // Two upstream spellings share the card only through the reference; each stays its own row.
  assert.deepEqual(deepseek.connections.map(connection => [connection.name, connection.upstreamModel]), [['Other Cloud', 'deepseek-v4.1-flash'], ['Work', 'deepseek-v4-flash']]);
  assert.deepEqual(deepseek.globalAliases, ['deepseek-v4-flash']);
  assert.deepEqual(deepseek.connections.find(connection => connection.providerId === 'work')?.scopedAliases, ['team/deepseek-v4-flash']);
  assert.deepEqual(deepseek.connections.find(connection => connection.providerId === 'other')?.scopedAliases, ['other/deepseek']);
});

test('unresolved names group only by one exact provider/model pair', () => {
  const cards = cardsOf(DIRECTORY);
  const lab = cards.find(card => card.key === JSON.stringify(['unresolved', 'lab', 'experimental-v2']))!;
  assert.deepEqual(lab.aliases, ['experimental-v2', 'lab/experimental-v2']);
  assert.equal(lab.reference, undefined);
  const other = cards.find(card => card.key === JSON.stringify(['unresolved', 'other', 'experimental-v2']))!;
  assert.deepEqual(other.aliases, ['other/experimental-v2']);
  assert.notEqual(lab.key, other.key);
});

test('a shared, mixed or unknown name is its own card and keeps every target', () => {
  const cards = cardsOf(DIRECTORY);
  const shared = cards.find(card => card.key === JSON.stringify(['alias', 'team/fast']))!;
  assert.equal(shared.isMixed, true);
  assert.equal(shared.manufacturer.id, 'multiple');
  assert.deepEqual(shared.connections.map(connection => connection.upstreamModel), ['claude-sonnet-4-5', 'gpt-5']);
  // Two targets under one name: neither connection may claim it as its own.
  assert.deepEqual(shared.globalAliases, ['team/fast']);
  for (const connection of shared.connections) assert.deepEqual(connection.scopedAliases, []);
  // The shared name is not also listed as an exclusive name on the GPT card.
  assert.equal(cards.some(card => card.kind === 'model' && card.aliases.includes('team/fast')), false);

  // OAuth provenance is known, its scope is not: the model card holds it as another advertised name.
  const oauth = cards.find(card => card.key === JSON.stringify(['model', GPT.id]))!;
  assert.deepEqual(oauth.aliases, ['gpt-5']);
  assert.deepEqual(oauth.otherAliases, ['gpt-5']);
  assert.deepEqual(oauth.globalAliases, []);
  assert.deepEqual(oauth.connections.map(connection => [connection.providerId, connection.scopedAliases]), [['oauth:codex', []]]);

  const runtime = cards.find(card => card.key === JSON.stringify(['alias', 'runtime-only']))!;
  assert.deepEqual(runtime.otherAliases, ['runtime-only']);
  assert.deepEqual(runtime.connections.map(connection => [connection.providerId, connection.routes[0].call_point]), [['', 'runtime-only']]);
  assert.equal(runtime.manufacturer.id, 'unknown');

  // Every advertised name is reachable exactly once.
  assert.deepEqual(cards.flatMap(card => card.aliases).sort(), DIRECTORY.models.map(model => model.id).sort());
});

test('a literal reference for a name without provenance stays a profile, not a resolved model', () => {
  const cards = cardsOf(directoryOf([route('', '', 'deepseek/deepseek-v4.1-flash', DEEPSEEK.id, 'unknown')]));
  assert.equal(cards[0].kind, 'alias');
  assert.equal(cards[0].profiles[0].metadata?.id, DEEPSEEK.id);
  assert.deepEqual(cards[0].otherAliases, ['deepseek/deepseek-v4.1-flash']);
  assert.deepEqual(summarizeModelCards(cards), { models: 0, otherCards: 1, names: 1 });
});

test('a partial directory or a missing upstream keeps names apart and their scope unknown', () => {
  const partial = cardsOf({ ...DIRECTORY, partial: ['routes'] });
  assert.equal(partial.some(card => card.kind !== 'alias'), false);
  assert.equal(partial.every(card => card.globalAliases.length === 0 && card.connections.every(connection => connection.scopedAliases.length === 0)), true);
  // Known route detail stays visible even though no name is shown as complete.
  assert.deepEqual(partial.find(card => card.identity === 'team/deepseek-v4-flash')?.connections.map(connection => connection.providerId), ['work']);

  const missingUpstream = cardsOf(directoryOf([route('work', '', 'team/odd', '', 'provider'), route('work', '', 'odd', '', 'global')]));
  assert.deepEqual(missingUpstream.map(card => card.kind), ['alias', 'alias']);
  assert.deepEqual(missingUpstream.flatMap(card => card.otherAliases).sort(), ['odd', 'team/odd']);
});

test('alias scope needs every route to agree', () => {
  assert.equal(classifyAliasScope([route('a', 'm', 'x/m', '', 'provider')]), 'provider');
  assert.equal(classifyAliasScope([route('a', 'm', 'x/m', '', 'provider'), route('b', 'm', 'x/m', '', 'provider')]), 'global');
  assert.equal(classifyAliasScope([route('a', 'm', 'm')]), 'global');
  assert.equal(classifyAliasScope([route('a', 'm', 'm', '', 'unknown')]), 'unknown');
  assert.equal(classifyAliasScope([route('a', 'm', 'x/m', '', 'provider')], true), 'unknown');
  assert.equal(classifyAliasScope([]), 'unknown');
});

test('filters choose display rows without changing card identity or global names', () => {
  const cards = cardsOf(DIRECTORY);
  const byProvider = filterModelSquareCards(cards, { providerId: 'other' });
  const deepseek = byProvider.find(view => view.card.identity === DEEPSEEK.id)!;
  assert.deepEqual(deepseek.connections.map(connection => connection.providerId), ['other']);
  assert.deepEqual(deepseek.card.globalAliases, ['deepseek-v4-flash']);
  assert.equal(deepseek.card.connections.length, 2);
  assert.equal(byProvider.some(view => view.card.key === JSON.stringify(['unresolved', 'lab', 'experimental-v2'])), false);

  assert.deepEqual(filterModelSquareCards(cards, { providerId: UNRESOLVED_CONNECTION }).map(view => view.card.identity), ['runtime-only']);
  // A model-level match keeps all rows; a name-level match keeps just the matching row.
  assert.equal(filterModelSquareCards(cards, { search: 'DeepSeek V4.1' }).find(view => view.card.identity === DEEPSEEK.id)?.connections.length, 2);
  assert.deepEqual(filterModelSquareCards(cards, { search: 'other/deepseek' }).find(view => view.card.identity === DEEPSEEK.id)?.connections.map(connection => connection.providerId), ['other']);
  assert.deepEqual(filterModelSquareCards(cards, { search: 'work' }).find(view => view.card.identity === DEEPSEEK.id)?.connections.map(connection => connection.providerId), ['work']);
  assert.deepEqual(filterModelSquareCards(cards, { manufacturerId: 'deepseek' }).map(view => view.card.identity), [DEEPSEEK.id]);
  assert.equal(filterModelSquareCards(cards, { search: 'not-advertised' }).length, 0);
});

test('a filtered-in provider is never hidden by the preview limit', () => {
  const routes = ['alpha', 'bravo', 'charlie', 'delta', 'echo', 'foxtrot', 'golf', 'zulu'].map(provider => route(provider, 'gemma4', `${provider}/gemma4`, GPT.id, 'provider'));
  const [card] = cardsOf(directoryOf(routes));
  assert.deepEqual(card.connections.slice(0, 3).map(connection => connection.providerId), ['alpha', 'bravo', 'charlie']);
  const [view] = filterModelSquareCards([card], { providerId: 'zulu' });
  assert.deepEqual(view.connections.map(connection => connection.providerId), ['zulu']);
  assert.equal(view.card.connections.length, 8);
});

test('connections with the same display name stay distinct and order deterministically', () => {
  const directory = directoryOf([route('b', 'm-2', 'b/m', GPT.id, 'provider'), route('a', 'm-1', 'a/m', GPT.id, 'provider'), route('a', 'm-0', 'a/m0', GPT.id, 'provider')]);
  const [card] = buildModelSquareCards(directory, () => 'Relay');
  assert.deepEqual(card.connections.map(connection => [connection.providerId, connection.upstreamModel]), [['a', 'm-0'], ['a', 'm-1'], ['b', 'm-2']]);
});

test('counts keep models, other cards and client names apart', () => {
  assert.deepEqual(summarizeModelCards(cardsOf(DIRECTORY)), { models: 2, otherCards: 4, names: 9 });
});

test('manufacturer artwork uses canonical maker identity, not relay branding', () => {
  assert.equal(resolveModelManufacturer('team/fast', GPT).id, 'openai');
  assert.equal(resolveModelManufacturer('experimental').id, 'unknown');
  assert.equal(resolveModelManufacturer('custom', { ...GPT, id: 'custom' }).id, 'unknown');
  assert.equal(resolveModelManufacturer('research/model', { ...GPT, id: 'research/model' }).id, 'research');
  assert.equal(resolveModelManufacturer('mygptish').id, 'unknown');
  assert.equal(resolveModelManufacturer('deepseek-r1-distill-qwen-32b').id, 'deepseek');
  assert.equal(resolveModelManufacturer('gpt-5-codex', { ...GPT, id: 'openai/gpt-5-codex' }).modelIconId, 'Codex');
  assert.equal(resolveModelManufacturer('google/gemma-3').modelIconId, 'Gemma');
});
test('external references preserve identity, use pi name search and never treat labels as URLs', () => {
  const links = buildModelReferenceLinks('team/fast', GPT);
  assert.equal(links[0].url, 'https://models.dev/models/openai/gpt-5');
  assert.equal(new URL(links[1].url).searchParams.get('q'), 'gpt-5');
  assert.equal(new URL(links[2].url).searchParams.get('name'), 'gpt-5');
  const unknown = buildModelReferenceLinks('strange/model & variant?x=1');
  assert.equal(unknown[0].url, 'https://models.dev/');
  assert.equal(new URL(unknown[2].url).searchParams.get('name'), 'model & variant?x=1');
  for (const link of unknown) assert.equal(isSafeReferenceURL(link.url), true);
  for (const value of ['javascript:alert(1)', 'http://example.test', 'https://secret@example.test', '/relative', 'not a url']) assert.equal(isSafeReferenceURL(value), false);
});
test('recognized manufacturer and family marks exist in the offline catalog', () => {
  for (const model of ['gpt-5', 'claude-sonnet', 'gemini-pro', 'grok-4', 'qwen3', 'deepseek-r1', 'kimi-k2', 'glm-4', 'minimax-m2', 'mistral-large', 'llama-3', 'command-r', 'seed-1', 'mimo-v2', 'mercury-2', 'index-1.9b', 'phi-4', 'nova-pro', 'ernie-4', 'hunyuan-large', 'sonar-pro', 'nemotron-70b', 'step-2']) {
    const maker = resolveModelManufacturer(model);
    for (const iconId of [maker.iconId, maker.modelIconId]) assert.ok(LOBE_ICON_CATALOG.some(item => item.id === iconId), `${model}: ${iconId}`);
  }
});
test('named makers sort alphabetically ahead of the multiple and unidentified groups', () => {
  const directory = directoryOf([
    route('a', 'runtime', 'runtime-only'), route('a', 'gpt-5', 'team/fast', GPT.id), route('b', 'claude', 'team/fast'),
    route('a', 'gpt-5', 'gpt-5', GPT.id), route('a', 'claude-opus', 'claude-opus'), route('a', 'zeta', 'zeta/model', 'zeta/model'),
  ], { model_info: { [GPT.id]: GPT, 'zeta/model': { ...GPT, id: 'zeta/model' } } });
  assert.deepEqual(countModelManufacturers(cardsOf(directory)).map(item => [item.manufacturer.id, item.count]), [['anthropic', 1], ['openai', 1], ['zeta', 1], ['multiple', 1], ['unknown', 1]]);
});
test('openness separates published weights from an open-source licence', () => {
  assert.equal(classifyModelOpenness({ open_weights: false }), 'closed');
  assert.equal(classifyModelOpenness({ open_weights: false, license: 'MIT' }), 'closed');
  assert.equal(classifyModelOpenness({}), undefined);
  assert.equal(classifyModelOpenness({ license: 'MIT' }), undefined);
  for (const license of ['MIT', 'MIT License', 'Apache 2.0', 'Apache-2.0', 'apache-2.0']) assert.equal(classifyModelOpenness({ open_weights: true, license }), 'open_source', license);
  for (const license of [undefined, '', 'CC-BY-NC-4.0', 'Llama 3.2 Community License', 'Mistral Research License', 'OpenMDW-1.1', 'Gemma', 'MIT-0-style']) assert.equal(classifyModelOpenness({ open_weights: true, license }), 'open_weights', String(license));
});
test('families the source does not describe still resolve to their maker', () => {
  for (const [model, maker] of [['mercury-2.5', 'inception'], ['index-mt-9b', 'bilibili'], ['phi-4', 'microsoft'], ['nova-pro', 'amazon'], ['ernie-4.5', 'baidu'], ['sonar-pro', 'perplexity'], ['gpt-oss-120b-medium', 'openai'], ['codex-auto-review', 'openai']]) assert.equal(resolveModelManufacturer(model).id, maker, model);
  for (const model of ['indexer', 'novation', 'stepwise', 'phil', 'openrouter/free']) assert.equal(resolveModelManufacturer(model).id, 'unknown', model);
  assert.equal(resolveModelManufacturer('cline-pass/glm-5.3-flash', { ...GPT, id: 'zhipuai/glm-5.3-flash' }).id, 'zai');
});
