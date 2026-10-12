/**
 * Certifies the model-pull bodies the drawer posts.
 *
 * The page calls pullProviderModels, which JSON.stringifies this payload and
 * POSTs it to /management/providers/pull-models. Importing the whole client
 * here would also load the uncommitted quota module, so the test checks the
 * payload and that exact request shape instead.
 */
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { modelPullPayload, type ModelPullDraft } from '../web/src/components/providers/managementProviders.ts';

const client = readFileSync(new URL('../web/src/api/client.ts', import.meta.url), 'utf8');
assert.match(
  client,
  /async pullProviderModels\(payload:[\s\S]*?return request<\{ models: string\[\]; total: number \}>\('\/management\/providers\/pull-models', \{\s*method: 'POST',\s*body: JSON\.stringify\(payload\),/,
);

const stored: ModelPullDraft = {
  providerId: 'openai-compat-0',
  family: 'openai-compatibility',
  baseURL: 'https://api.nan.builders/v1',
  storedBaseURL: 'https://api.nan.builders/v1',
  apiKey: 'sk-stored',
  storedAPIKey: 'sk-stored',
  proxyURL: 'http://stored.example',
  storedProxyURL: 'http://stored.example',
  authIndex: 'nan-2',
  headers: { 'X-Tenant': 'keel' },
  storedHeaders: { 'X-Tenant': 'keel' },
};

function posted(draft: ModelPullDraft) {
  return JSON.parse(JSON.stringify(modelPullPayload(draft))) as Record<string, unknown>;
}

const selected = posted(stored);
assert.deepEqual(selected, { provider_id: 'openai-compat-0', auth_index: 'nan-2' });

const typed = posted({ ...stored, apiKey: 'sk-typed' });
assert.equal('auth_index' in typed, false);
assert.equal(typed.provider_id, 'openai-compat-0');
assert.equal(typed.family, 'openai-compatibility');
assert.equal(typed.base_url, 'https://api.nan.builders/v1');
assert.equal(typed.api_key, 'sk-typed');
assert.deepEqual(typed.headers, { 'X-Tenant': 'keel' });

const changedURL = posted({ ...stored, baseURL: 'https://other.example/v1' });
assert.equal('auth_index' in changedURL, false);
assert.equal(changedURL.base_url, 'https://other.example/v1');

const changedProxy = posted({ ...stored, proxyURL: 'http://proxy.example' });
assert.equal('auth_index' in changedProxy, false);
assert.equal(changedProxy.proxy_url, 'http://proxy.example');
assert.notEqual(changedProxy.proxy_url, stored.storedProxyURL);

const changedHeaders = posted({ ...stored, headers: { 'X-Tenant': 'other' } });
assert.equal('auth_index' in changedHeaders, false);
assert.deepEqual(changedHeaders.headers, { 'X-Tenant': 'other' });

console.log('PASS model pull: stored key and typed connection are mutually exclusive');
