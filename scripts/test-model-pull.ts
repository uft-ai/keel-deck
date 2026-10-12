/**
 * Certifies the model-pull bodies the drawer posts.
 *
 * The rendered provider-model-defaults component test exercises the real client
 * transport; this suite owns the mutually exclusive credential decisions.
 */
import assert from 'node:assert/strict';
import { modelPullPayload, type ModelPullDraft } from '../web/src/components/providers/managementProviders.ts';

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
