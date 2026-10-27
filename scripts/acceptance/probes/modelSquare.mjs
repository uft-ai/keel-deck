import { mkdir } from 'node:fs/promises';
import { until, settleLayout } from '../harness.mjs';

const LONG_UPSTREAM = 'experimental-model-with-a-very-long-unbroken-identity-for-mobile-layout';
const config = (operations, native = null) => ({ operations, native, input_modalities: null, output_modalities: null, max_context_length: null, max_output_tokens: null });
const route = (provider_id, upstream_model, call_point, reference_id = '', name_scope = 'global', configuration = null) => ({ provider_id, upstream_model, call_point, reference_id, name_scope, configuration });
const provider = (index, name, host) => ({ id: `openai-compat-${index}`, family: 'openai-compatibility', name, endpoint_host: host, is_oauth: false });

const DEEPSEEK = 'deepseek/deepseek-v4.1-flash';
const GEMMA = 'google/gemma-4-26b-a4b-it';
// Eight connections serve Gemma; two share a display name, host and upstream model, so only
// their own IDs can tell them apart.
const GEMMA_CONNECTIONS = ['Alpha', 'Bravo', 'Charlie', 'Delta', 'Echo', 'Echo', 'Golf', 'Hotel']
  .map((name, index) => provider(10 + index, name, `${name.toLowerCase()}.example.test`));
const PROVIDERS = [
  provider(0, 'Work', 'api.nan.builders'),
  provider(1, 'Other Cloud', 'other.example.test'),
  { ...provider(2, 'Team Relay', 'relay.example.test'), icon_id: 'DeepSeek' },
  { id: 'oauth:codex', family: 'codex', name: 'Codex OAuth', is_oauth: true },
  ...GEMMA_CONNECTIONS,
];
const ROUTES = [
  route('openai-compat-0', 'deepseek-v4-flash', 'deepseek-v4-flash', DEEPSEEK, 'global', config(['chat'])),
  route('openai-compat-0', 'deepseek-v4-flash', 'team/deepseek-v4-flash', DEEPSEEK, 'provider', config(['chat'])),
  // One connection, two exact names with different configured protocols.
  route('openai-compat-1', 'deepseek-v4.1-flash', 'other/deepseek', DEEPSEEK, 'provider', config(['chat'])),
  route('openai-compat-1', 'deepseek-v4.1-flash', 'other/deepseek-responses', DEEPSEEK, 'provider', config(['responses'], ['responses'])),
  route('oauth:codex', 'gpt-5', 'gpt-5', 'openai/gpt-5', 'unknown'),
  route('openai-compat-2', 'gpt-5', 'team/fast', 'openai/gpt-5', 'provider'),
  route('openai-compat-2', 'claude-sonnet-4.5', 'team/fast', 'anthropic/claude-sonnet-4-5', 'provider'),
  route('openai-compat-2', 'deepseek-r1', 'reasoner', 'deepseek/deepseek-r1'),
  route('openai-compat-2', 'gpt-5-mini', 'gpt-5-mini', 'openai/gpt-5-mini'),
  route('openai-compat-2', LONG_UPSTREAM, LONG_UPSTREAM),
  ...GEMMA_CONNECTIONS.map((item, index) => route(item.id, 'gemma4', `g${index}/gemma4`, GEMMA, 'provider')),
];
const DIRECTORY = {
  models: [...new Set([...ROUTES.map(item => item.call_point), 'runtime-only'])].map(id => ({ id, call_point: id, vision: 'unknown' })),
  metadata_updated_at: '2026-10-05',
  model_info: {
    [DEEPSEEK]: { id: DEEPSEEK, name: 'DeepSeek V4.1 Flash', open_weights: true, limit: { context: 1_000_000, output: 64_000 }, modalities: { input: ['text'], output: ['text'] } },
    'openai/gpt-5': { id: 'openai/gpt-5', name: 'GPT-5', open_weights: false, reasoning: true, tool_call: true, limit: { context: 400_000, output: 128_000 }, modalities: { input: ['text', 'image'], output: ['text'] } },
    'openai/gpt-5-mini': { id: 'openai/gpt-5-mini', name: 'GPT-5 mini', open_weights: false, limit: { context: 400_000, output: 128_000 }, modalities: { input: ['text', 'image'], output: ['text'] } },
    'anthropic/claude-sonnet-4-5': { id: 'anthropic/claude-sonnet-4-5', name: 'Claude Sonnet 4.5', open_weights: false, limit: { context: 200_000, output: 64_000 }, modalities: { input: ['text', 'image', 'pdf'], output: ['text'] } },
    'deepseek/deepseek-r1': { id: 'deepseek/deepseek-r1', name: 'DeepSeek R1', open_weights: true, license: 'MIT', limit: { context: 128_000, output: 32_768 }, modalities: { input: ['text'], output: ['text'] }, weights: [{ label: 'Hugging Face', url: 'https://huggingface.co/deepseek-ai/DeepSeek-R1' }] },
    [GEMMA]: { id: GEMMA, name: 'Gemma 4 26B', open_weights: true, limit: { context: 131_072 }, modalities: { input: ['text', 'image'], output: ['text'] } },
  },
  providers: PROVIDERS, routes: ROUTES, partial: [],
};
const CARD_COUNT = 8;
const usage = (requests, cost) => ({ requests, priced_requests: cost == null ? 0 : requests, cost_usd: cost });
const priced = (model, prompt, completion, usage30d) => ({
  model, prompt_price_per_1m: prompt, completion_price_per_1m: completion, cache_read_price_per_1m: prompt / 10, cache_write_price_per_1m: prompt * 1.25,
  price_multiplier: 1, tiers: [], source: 'manual', upstream_id: '', match_kind: '', mode: 'custom', synced_at_ms: 0, updated_at_ms: 0, usage_30d: usage30d,
});
// Prices belong to the routed upstream model, shared or per channel; request counts keep the
// advertised call names. Each connection has its own channel, so one Gemma connection's price
// is not its siblings'.
const channelOf = provider => (provider.is_oauth ? provider.family : `openai-compatible-${provider.id}`);
const SHARED_PRICES = [priced('gpt-5', 1.25, 10, usage(120, 14.2)), priced('deepseek-r1', 0.55, 2.19, usage(8, 0.4)), priced('deepseek-v4-flash', 0.075, 0.3, usage(3, 0.01))];
const PRICE_BOOK = {
  source: 'openrouter',
  providers: DIRECTORY.providers.map(provider => ({ ...provider,
    channel: channelOf(provider), priority: 0,
    models: [...new Set(ROUTES.filter(route => route.provider_id === provider.id).map(route => route.upstream_model))],
  })),
  channel_models: [{ ...priced('gemma4', 0.2, 0.8, usage(1, 0.01)), channel: channelOf(GEMMA_CONNECTIONS[0]) }],
  channels: [], upstream_count: 0, sync: { known: false, running: false, state: null },
  models: SHARED_PRICES,
  unpriced: [...new Set(ROUTES.map(route => route.upstream_model))].filter(model => model && !SHARED_PRICES.some(row => row.model === model)).map(model => ({ model, usage_30d: usage(0, null), suggestions: [] })),
};
const RECENT_REQUESTS = {
  window: { from: 0, to: 0, bucket_ms: 0 },
  facets: {
    models: [], providers: [], api_group_keys: [], auth_indexes: [], sources: [], executors: [], auth_types: [], reasoning_efforts: [], service_tiers: [], resolved_service_tiers: [],
    model_aliases: [{ value: 'gpt-5', requests: 1234 }, { value: 'team/fast', requests: 12 }],
  },
};

/**
 * The directory alone, for scenarios that bring their own price book and request records.
 * `state.directory`, when a scenario sets it, replaces the directory for later reads.
 */
export function modelSquareFixtures(state = {}) {
  return [
    [(url) => url.pathname.endsWith('/management/model-square'), () => state.directory ?? DIRECTORY],
  ];
}

/** The two neighbours the directory joins by exact client name, and the editor a price opens. */
export function modelSquareLedgerFixtures() {
  return [
    [(url) => url.pathname.endsWith('/pricing'), () => PRICE_BOOK],
    [(url) => url.pathname.endsWith('/pricing/catalog'), () => ({ models: [] })],
    [(url) => /\/pricing\/models\/[^/]+$/.test(url.pathname), (url) => {
      const model = decodeURIComponent(url.pathname.split('/').at(-1));
      const channel = url.searchParams.get('channel');
      const rows = channel ? PRICE_BOOK.channel_models.filter(row => row.channel === channel) : PRICE_BOOK.models;
      return { model, price: rows.find(row => row.model === model) ?? null, automatic: null, suggestions: [], candidate: null, versions: [], profile: null };
    }],
    [(url) => url.pathname.endsWith('/usage/facets'), () => RECENT_REQUESTS],
    [(url) => url.pathname.includes('/usage/events'), () => ({ items: [], has_more: false, limit: 100 })],
  ];
}

export async function modelSquare({ base, page, context, check, state = {} }) {
  await context.grantPermissions(['clipboard-read', 'clipboard-write']);
  await page.goto(`${base}/model-square`, { waitUntil: 'domcontentloaded' });
  const cards = page.locator('.model-square-page [data-model-card]');
  const titleOf = title => page.getByRole('button', { name: `View information for ${title}`, exact: true });
  const cardOf = title => cards.filter({ has: titleOf(title) });
  await until(async () => await cards.count() === CARD_COUNT, { label: 'model cards' });
  await settleLayout(page);
  const pageText = await page.locator('.model-square-page').innerText();
  check('counts state resolved models, other entries and exact names separately', ['5 models', '3 other entries', '18 client names'].every(part => pageText.includes(part)), pageText.slice(0, 400));
  const sections = page.locator('.model-square-page [data-model-group]');
  const geometry = await sections.evaluateAll(elements => elements.map(element => {
    const bounds = element.getBoundingClientRect();
    return { left: bounds.left, top: bounds.top, right: bounds.right, bottom: bounds.bottom };
  }));
  check('maker sections form one full-width vertical sequence', geometry.length === 5 && geometry.every((bounds, index) => index === 0 || bounds.top >= geometry[index - 1].bottom && Math.abs(bounds.left - geometry[0].left) < 1 && Math.abs(bounds.right - geometry[0].right) < 1), JSON.stringify(geometry));
  check('named makers precede the multiple and unidentified groups', await page.locator('.model-square-page h2').allTextContents().then(names => names.join('|') === 'DeepSeek|Google|OpenAI|Multiple makers|Unidentified maker'), await page.locator('.model-square-page h2').allTextContents().then(names => names.join('|')));
  // Each grid row shares one height, and the connection header sits the same distance from every card's foot.
  const layout = await cards.evaluateAll(elements => elements.map(card => {
    const bounds = card.getBoundingClientRect();
    const head = card.querySelector('button[class*="served-head"]').getBoundingClientRect();
    return { top: Math.round(bounds.top), height: Math.round(bounds.height), tail: Math.round(bounds.bottom - head.top), head: Math.round(head.height) };
  }));
  const rows = Map.groupBy(layout, item => item.top);
  check('cards in a row share one height', [...rows.values()].every(row => row.every(item => item.height === row[0].height)), JSON.stringify(layout));
  // The header keeps one minimum height, so ordinary cards match across rows and sections too;
  // only the section holding the long unbroken title grows, and never shrinks below the others.
  const longSection = await titleOf(LONG_UPSTREAM).evaluate(title => title.closest('[data-model-group]').getAttribute('data-model-group'));
  const sectionHeights = await sections.evaluateAll(elements => elements.map(section => ({ id: section.getAttribute('data-model-group'), heights: [...section.querySelectorAll('[data-model-card]')].map(card => Math.round(card.getBoundingClientRect().height)) })));
  const ordinary = sectionHeights.filter(section => section.id !== longSection).flatMap(section => section.heights);
  check('ordinary cards share one height across rows and sections', ordinary.length > 1 && ordinary.every(height => height === ordinary[0]), JSON.stringify(sectionHeights));
  check('a long title grows its own section’s shared height instead of clipping', sectionHeights.find(section => section.id === longSection).heights.every((height, _, all) => height === all[0] && height >= ordinary[0]), JSON.stringify(sectionHeights));
  check('connection headers and three slots align across cards', layout.every(item => item.tail === layout[0].tail && item.head >= 44), JSON.stringify(layout));
  const columns = async () => new Set(await cards.evaluateAll(elements => elements.map(element => Math.round(element.getBoundingClientRect().left)))).size;
  check('cards lay out in several columns on a wide page', await columns() >= 2, `columns=${await columns()}`);

  const deepseek = cardOf('DeepSeek V4.1 Flash');
  await until(async () => (await deepseek.innerText()).includes('$0.075'), { label: 'price book joined' });
  const deepseekText = await deepseek.innerText();
  check('a card shows its global price as plain text with the exact rate precision', await deepseek.getByRole('button', { name: 'Edit price for deepseek-v4-flash' }).innerText() === '$0.075 / $0.30', deepseekText);
  check('a card shows reference input badges and its connection count', deepseekText.includes('Text') && /2 connections/i.test(deepseekText), deepseekText);
  check('connection previews are alphabetical', await deepseek.locator('[data-connection]').evaluateAll(rows => rows.map(row => row.getAttribute('data-connection')).join('|')) === 'openai-compat-1|openai-compat-0');
  check('a connection with several exact names asks for per-name prices', await deepseek.locator('[data-connection="openai-compat-1"]').getByRole('button', { name: 'Per-name prices' }).count() === 1);
  check('a card carries no reference specification', !/1,000,000|Reasoning|Tool calling/.test(deepseekText));
  const gemma = cardOf('Gemma 4 26B');
  check('eight connections preview the first three and count all eight', /8 connections/i.test(await gemma.innerText()) && await gemma.locator('[data-connection]').evaluateAll(rows => rows.map(row => row.innerText.split('\n')[0]).join('|')) === 'Alpha|Bravo|Charlie', await gemma.innerText());
  check('a scoped connection row shows its own channel price, not a sibling’s', (await gemma.locator('[data-connection="openai-compat-10"]').innerText()).includes('$0.20 / $0.80') && (await gemma.locator('[data-connection="openai-compat-11"]').innerText()).includes('Set price'));
  check('a shared name reaching several targets offers per-connection prices', await cardOf('team/fast').getByRole('button', { name: 'Per-connection prices' }).count() > 0);
  check('a model without a global name says so instead of borrowing a scoped price', (await gemma.innerText()).includes('No global name') && (await cardOf('GPT-5').innerText()).includes('No global name'));
  check('a shared name says it may reach several models', await cardOf('team/fast').innerText().then(text => text.includes('Mixed models') && /Possible targets/i.test(text)));
  check('a name without provenance stays visible with an unknown reference', await cardOf('runtime-only').innerText().then(text => text.includes('Reference unknown') && text.includes('Unresolved connection')));
  const localArtwork = await page.locator('.model-square-page').evaluate(element => [...element.querySelectorAll('img')].every(image => image.src.startsWith(location.origin) || image.src.startsWith('data:')));
  check('model and provider marks use local artwork', localArtwork);
  await mkdir('tmp/model-square-preview', { recursive: true });
  await page.screenshot({ path: 'tmp/model-square-preview/desktop.png', fullPage: true });

  // Copy: hover reveals it, success alone shows the check mark, and entering the group again resets it.
  const drawer = page.locator('.ant-drawer:visible');
  const workRow = deepseek.locator('[data-connection="openai-compat-0"]');
  const workCopy = workRow.locator('[data-copy-model="team/deepseek-v4-flash"]');
  const opacity = locator => locator.evaluate(element => getComputedStyle(element).opacity);
  await page.mouse.move(0, 0);
  check('quick copy stays out of sight until its name is hovered or focused', await opacity(workCopy) === '0');
  await workRow.getByRole('button', { name: 'DeepSeek V4.1 Flash through Work' }).hover();
  await until(async () => await opacity(workCopy) === '1', { label: 'copy revealed on hover' });
  await workCopy.click();
  await until(async () => await page.evaluate(() => navigator.clipboard.readText()) === 'team/deepseek-v4-flash', { label: 'scoped name on the clipboard' });
  check('copy writes the exact scoped name and opens nothing', await drawer.count() === 0 && await workCopy.getAttribute('aria-label') === 'Copied team/deepseek-v4-flash' && await workCopy.getAttribute('data-copied') !== null);
  await page.mouse.move(0, 0);
  check('the check mark stays after the pointer leaves', await workCopy.getAttribute('aria-label') === 'Copied team/deepseek-v4-flash');
  await workRow.getByRole('button', { name: 'DeepSeek V4.1 Flash through Work' }).hover();
  await until(async () => await workCopy.getAttribute('aria-label') === 'Copy call name team/deepseek-v4-flash', { label: 'copy reset on re-entry' });
  check('entering the name again restores the copy glyph', await workCopy.getAttribute('data-copied') === null);

  await cardOf('GPT-5 mini').getByRole('button', { name: 'Set price for gpt-5-mini' }).click();
  await until(async () => (await drawer.locator('.ant-drawer-title').allInnerTexts()).some(title => title.includes('gpt-5-mini')), { label: 'price editor' });
  check('Set price opens the shared editor for the routed upstream on its connection', await drawer.count() === 1 && !(await drawer.innerText()).includes('Served by') && (await drawer.innerText()).includes('Team Relay'));
  await drawer.locator('.ant-drawer-close').click();
  await drawer.waitFor({ state: 'hidden' });

  const status = page.locator('.model-square-page [role="status"]');
  check('an unfiltered directory states no result count', (await status.innerText()) === '' && new URL(page.url()).search === '');
  await page.getByRole('combobox', { name: 'Filter by connection' }).click();
  await page.locator('.ant-select-dropdown:visible .ant-select-item-option').filter({ hasText: 'Hotel' }).click();
  await until(async () => await cards.count() === 1, { label: 'connection filter' });
  check('a connection hidden behind the preview cut is shown when filtered', await gemma.locator('[data-connection]').evaluateAll(rows => rows.map(row => row.innerText.split('\n')[0]).join('|')) === 'Hotel' && /1 of 8 connections/i.test(await gemma.innerText()) && new URL(page.url()).searchParams.get('provider') === 'openai-compat-17', await gemma.innerText());
  check('filters leave the card’s global state unchanged', (await gemma.innerText()).includes('No global name') && (await status.innerText()) === `Showing 1 of ${CARD_COUNT}`);
  await page.getByRole('button', { name: 'Clear filters' }).first().click();
  await until(async () => await cards.count() === CARD_COUNT, { label: 'filters cleared' });
  check('clearing filters removes the connection filter', new URL(page.url()).search === '');

  await page.locator('[data-maker="openai"]').click();
  await until(async () => await cards.count() === 2, { label: 'maker filter' });
  check('a maker filter shows that maker alone, and says how many', await sections.count() === 1 && await page.locator('[data-maker="openai"]').getAttribute('aria-pressed') === 'true' && (await status.innerText()) === `Showing 2 of ${CARD_COUNT}`);
  const makerSelection = await until(() => page.locator('[data-maker="openai"]').evaluate((button) => {
    const probe = document.createElement('span');
    probe.style.color = getComputedStyle(document.documentElement).getPropertyValue('--selected-inset').trim();
    document.body.append(probe);
    const expected = getComputedStyle(probe).color;
    probe.remove();
    const actual = getComputedStyle(button).backgroundColor;
    return actual === expected ? { actual, expected } : undefined;
  }), { label: 'maker selection fill', timeoutMs: 2_000 }).catch(() => undefined);
  check('a selected maker chip takes the shared theme selection fill', makerSelection !== undefined, makerSelection ? makerSelection.actual : 'not selected-inset');
  await titleOf('GPT-5').click();
  await drawer.waitFor({ state: 'visible' });
  const panel = drawer.locator('[data-panel]');
  const gptName = drawer.locator('[data-client-name="gpt-5"]');
  check('an unknown-scope name is listed apart with its own price and request count', (await panel.innerText()).includes('Other advertised names') && (await panel.innerText()).includes('No global name') && await gptName.innerText().then(text => ['$1.25 / $10.00', '1,234', 'Scope unknown'].every(part => text.includes(part))), await panel.innerText());
  check('View requests opens the exact name’s 24-hour records', await gptName.getByRole('link', { name: 'View requests' }).getAttribute('href').then(href => href.endsWith('/usage/events?preset=24h&model_alias=gpt-5')));
  check('reference limits live in the details', (await panel.innerText()).includes('400,000') && (await panel.innerText()).includes('Closed-source model'));
  check('the first model has no previous neighbour', await drawer.getByRole('button', { name: 'Previous' }).isDisabled() && (await drawer.innerText()).includes('1 / 2'));
  await drawer.getByRole('button', { name: 'Next' }).click();
  await until(async () => (await drawer.locator('.ant-drawer-title').innerText()) === 'GPT-5 mini', { label: 'next model' });
  check('Next steps through the filtered cards', (await drawer.innerText()).includes('2 / 2') && await panel.getAttribute('data-panel') === 'global');
  check('the drawer heading carries the model mark beside its name', await drawer.locator('.ant-drawer-title [class*="drawer-title"]').evaluate(title => {
    const [mark, name] = title.children;
    return Boolean(mark && name) && mark.getBoundingClientRect().width >= 20 && name.textContent === 'GPT-5 mini';
  }));
  await drawer.locator('.ant-drawer-close').click();
  await drawer.waitFor({ state: 'hidden' });
  check('closing details after stepping keeps the maker filter', new URL(page.url()).searchParams.get('maker') === 'openai' && await cards.count() === 2);
  await page.locator('[data-maker="openai"]').click();
  await until(async () => await cards.count() === CARD_COUNT, { label: 'maker filter released' });

  // One drawer: global details, the connection list and a provider's details replace each other.
  await titleOf('DeepSeek V4.1 Flash').click();
  await drawer.waitFor({ state: 'visible' });
  check('global details list the global name, not a scoped one', await panel.getAttribute('data-panel') === 'global' && await drawer.locator('[data-client-name="deepseek-v4-flash"]').count() === 1 && await drawer.locator('[data-client-name="team/deepseek-v4-flash"]').count() === 0);
  await drawer.getByRole('button', { name: 'DeepSeek V4.1 Flash through Work' }).click();
  await until(async () => await panel.getAttribute('data-panel') === 'provider', { label: 'provider details' });
  const providerText = await panel.innerText();
  check('provider details name the host, upstream model, scoped name and configuration', ['api.nan.builders', 'deepseek-v4-flash', 'chat', 'do not verify provider support'].every(part => providerText.includes(part)) && await drawer.locator('[data-client-name="team/deepseek-v4-flash"]').count() === 1 && await drawer.locator('[data-client-name="deepseek-v4-flash"]').count() === 0, providerText);
  check('a scoped name’s request link encodes the exact name', await drawer.locator('[data-client-name="team/deepseek-v4-flash"]').getByRole('link', { name: 'View requests' }).getAttribute('href').then(href => href.endsWith('model_alias=team%2Fdeepseek-v4-flash')));
  await drawer.getByRole('button', { name: 'Back' }).click();
  await until(async () => await panel.getAttribute('data-panel') === 'global', { label: 'in-drawer back' });
  await drawer.getByRole('button', { name: 'DeepSeek V4.1 Flash through Other Cloud' }).click();
  await until(async () => await panel.getAttribute('data-panel') === 'provider', { label: 'second provider' });
  check('differing settings on one connection stay per exact name', await drawer.locator('[data-configuration-alias]').evaluateAll(items => items.map(item => item.getAttribute('data-configuration-alias')).join('|')) === 'other/deepseek|other/deepseek-responses');
  await drawer.getByRole('button', { name: 'Next' }).click();
  await until(async () => (await drawer.locator('.ant-drawer-title').innerText()) === 'Gemma 4 26B', { label: 'next card' });
  check('Previous/Next resets to the neighbour’s global details', await panel.getAttribute('data-panel') === 'global');
  await drawer.locator('.ant-drawer-close').click();
  await drawer.waitFor({ state: 'hidden' });
  check('one Close dismisses the drawer after in-drawer navigation', page.url().endsWith('/model-square'));

  await gemma.locator('button[class*="served-head"]').click();
  await drawer.waitFor({ state: 'visible' });
  check('the connection header opens the full list first', await panel.getAttribute('data-panel') === 'list' && await drawer.locator('[data-connection]').count() === 8);
  check('same-named connections are told apart', await drawer.locator('[data-connection]').filter({ hasText: 'Echo' }).evaluateAll(rows => new Set(rows.map(row => row.innerText)).size === 2));
  await page.keyboard.press('Escape');
  await drawer.waitFor({ state: 'hidden' });

  const search = page.getByRole('textbox', { name: 'Search models or makers' });
  await search.fill('team/fast');
  await until(async () => await cards.count() === 1, { label: 'search result' });
  await titleOf('team/fast').click();
  await drawer.waitFor({ state: 'visible' });
  check('a shared name exposes every target’s reference profile', await panel.innerText().then(text => ['GPT-5', 'Claude Sonnet 4.5', '400,000', '200,000', 'Shared name'].every(part => text.includes(part))), await panel.innerText());
  const externalLinks = drawer.locator('a[target="_blank"]');
  check('reference navigation uses new-tab links without opener access', await externalLinks.evaluateAll(links => links.length === 6 && links.every(link => link.rel.includes('noopener') && link.rel.includes('noreferrer'))));
  check('pi search uses the canonical model name', await drawer.locator('a[href="https://pi.dev/models?name=gpt-5"]').count() === 1);
  await drawer.locator('.ant-drawer-close').click();
  await drawer.waitFor({ state: 'hidden' });
  check('closing details preserves search', page.url().includes('/model-square') && new URL(page.url()).searchParams.get('q') === 'team/fast');
  await search.fill('');
  await until(async () => await cards.count() === CARD_COUNT, { label: 'full directory' });
  await titleOf('DeepSeek R1').click();
  await drawer.waitFor({ state: 'visible' });
  check('a model with downloadable weights under an open-source license is named an open-source model', (await drawer.innerText()).includes('Open-source model') && (await drawer.innerText()).includes('MIT') && await drawer.locator('a[href="https://huggingface.co/deepseek-ai/DeepSeek-R1"]').count() === 1);
  await drawer.locator('.ant-drawer-close').click();
  await drawer.waitFor({ state: 'hidden' });
  await titleOf('runtime-only').click();
  await drawer.waitFor({ state: 'visible' });
  check('an unmatched name keeps explicit unknown data and usable search links', (await drawer.innerText()).includes('No exact metadata match') && await drawer.locator('a[href="https://pi.dev/models?name=runtime-only"]').count() === 1);
  // A refresh that removes the open card closes its drawer.
  state.directory = { ...DIRECTORY, models: DIRECTORY.models.filter(model => model.id !== 'runtime-only') };
  // The modal drawer covers the page control, so its click is dispatched to it directly.
  await page.locator('.model-square-page').getByRole('button', { name: 'Refresh' }).dispatchEvent('click');
  await until(async () => await cards.count() === CARD_COUNT - 1, { label: 'refreshed directory' });
  await drawer.waitFor({ state: 'hidden' });
  check('a refresh that removes the open card closes its drawer', page.url().endsWith('/model-square'));
  state.directory = undefined;
  await page.locator('.model-square-page').getByRole('button', { name: 'Refresh' }).click();
  await until(async () => await cards.count() === CARD_COUNT, { label: 'directory restored' });

  for (const width of [390, 320]) {
    await page.setViewportSize({ width, height: 844 });
    await settleLayout(page);
    const overflow = await page.locator('.app-content').evaluate(element => element.scrollWidth - element.clientWidth);
    check(`cards fit ${width}px`, overflow <= 1, `overflow=${overflow}`);
    check(`cards form a single column at ${width}px`, await columns() === 1, `columns=${await columns()}`);
    const target = titleOf('DeepSeek V4.1 Flash');
    await target.scrollIntoViewIfNeeded();
    const controls = await deepseek.evaluate((card, limit) => [...card.querySelectorAll('button')].map(button => {
      const bounds = button.getBoundingClientRect();
      return { label: button.getAttribute('aria-label') ?? button.innerText, height: bounds.height, left: bounds.left, right: bounds.right, isFit: bounds.height >= 44 && bounds.left >= 0 && bounds.right <= limit + 1 };
    }), width);
    check(`card controls are full touch targets within ${width}px`, controls.every(control => control.isFit), JSON.stringify(controls.filter(control => !control.isFit)));
    const longTitle = await titleOf(LONG_UPSTREAM).boundingBox();
    check(`long model names wrap within ${width}px`, longTitle.x >= 0 && longTitle.x + longTitle.width <= width + 1, JSON.stringify(longTitle));
    // One column makes every card its own row: a wrapped long title grows its section's shared
    // row height instead of clipping, and its neighbours keep the same height and aligned tail.
    const stacked = await sections.evaluateAll(elements => elements.map(section => [...section.querySelectorAll('[data-model-card]')].map(card => {
      const bounds = card.getBoundingClientRect();
      const title = card.querySelector('h3').getBoundingClientRect();
      return { height: Math.round(bounds.height), tail: Math.round(bounds.bottom - card.querySelector('button[class*="served-head"]').getBoundingClientRect().top), isTitleInside: title.bottom <= card.querySelector('button[class*="served-head"]').getBoundingClientRect().top + 1 };
    })));
    check(`wrapped rows keep equal cards and unclipped titles at ${width}px`, stacked.every(section => section.every(card => card.height === section[0].height && card.tail === stacked[0][0].tail && card.isTitleInside)), JSON.stringify(stacked));
    if (width === 320) await page.screenshot({ path: 'tmp/model-square-preview/phone.png', fullPage: true });
    await target.click();
    await drawer.waitFor({ state: 'visible' });
    await settleLayout(page);
    await drawer.evaluate(async element => {
      await Promise.all(element.getAnimations({ subtree: true }).filter(animation => animation.effect?.getComputedTiming().iterations !== Infinity).map(animation => animation.finished.catch(() => {})));
    });
    const drawerGeometry = await drawer.locator('.ant-drawer-body').evaluate(element => ({ overflow: element.scrollWidth - element.clientWidth, right: element.getBoundingClientRect().right }));
    check(`model details fit the ${width}px drawer`, drawerGeometry.overflow <= 1 && drawerGeometry.right <= width + 1, JSON.stringify(drawerGeometry));
    if (width === 320) await page.screenshot({ path: 'tmp/model-square-preview/details-phone.png', fullPage: true });
    await drawer.locator('.ant-drawer-close').click();
    await drawer.waitFor({ state: 'hidden' });
  }
}
