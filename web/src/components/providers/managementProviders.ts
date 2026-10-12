import type { ProviderItem } from '../../types/providers';
import type { ModelOptionsDraft } from './modelOptions';

/**
 * The view models the provider console builds on top of `ProviderItem`.
 *
 * They live here rather than in the page because the drawer, the table and the
 * write paths all speak them: a form row is what the drawer edits, and the list's
 * cached shape is what a confirmed toggle writes back into.
 */

/** The cached shape of the providers list, named because a toggle writes into it. */
export interface ManagementProvidersData {
  providers: ProviderItem[];
  total: number;
}

/** One credential row in the editor's key list. */
export interface FormKeyItem {
  id: string;
  apiKey?: string;
  proxyUrl?: string;
  weight?: number;
  isChanging?: boolean;
  /** The stored key this row was loaded from. A pull may name it only while the secret is unchanged. */
  authIndex?: string;
  /** The secret loaded with authIndex. Compared with apiKey; never sent as a second copy. */
  storedAPIKey?: string;
  /** The proxy loaded with authIndex. Compared with proxyUrl; a later edit is not the stored value. */
  storedProxyURL?: string;
}

/** What a model pull may send. A stored key and a typed connection are alternatives, never both. */
export interface ModelPullDraft {
  providerId?: string;
  family?: string;
  baseURL: string;
  storedBaseURL?: string;
  apiKey: string;
  storedAPIKey?: string;
  proxyURL: string;
  storedProxyURL?: string;
  authIndex?: string;
  headers: Record<string, string>;
  storedHeaders?: Record<string, string>;
}

/**
 * modelPullPayload builds one of the two bodies the pull handler accepts.
 *
 * A selected stored key may name only the provider and that key. The handler
 * refuses a base URL, secret, proxy, or header beside auth_index, because those
 * would mix a typed connection with the stored one. Any typed secret, changed
 * URL, changed proxy, or edited header is therefore sent alone, and the stored
 * key is not named.
 */
export function modelPullPayload(draft: ModelPullDraft): {
  provider_id?: string;
  auth_index?: string;
  family?: string;
  base_url?: string;
  api_key?: string;
  proxy_url?: string;
  headers?: Record<string, string>;
} {
  const headerRecordsMatch = (left: Record<string, string>, right: Record<string, string> | undefined) => {
    const stored = right ?? {};
    const leftKeys = Object.keys(left);
    const rightKeys = Object.keys(stored);
    return leftKeys.length === rightKeys.length && leftKeys.every((key) => left[key] === stored[key]);
  };
  const usesStoredConnection = Boolean(draft.authIndex)
    && draft.apiKey.trim() === (draft.storedAPIKey ?? '').trim()
    && draft.baseURL.trim() === (draft.storedBaseURL ?? '').trim()
    && draft.proxyURL.trim() === (draft.storedProxyURL ?? '').trim()
    && headerRecordsMatch(draft.headers, draft.storedHeaders);
  if (usesStoredConnection) {
    return {
      provider_id: draft.providerId,
      auth_index: draft.authIndex,
    };
  }
  const headers = Object.keys(draft.headers).length > 0 ? draft.headers : undefined;
  return {
    provider_id: draft.providerId,
    family: draft.family,
    base_url: draft.baseURL.trim(),
    api_key: draft.apiKey.trim(),
    proxy_url: draft.proxyURL.trim(),
    ...(headers ? { headers } : {}),
  };
}

/** One request header in the editor's header list. */
export interface FormHeaderItem {
  id: string;
  key: string;
  value: string;
}

/** One model entry in the editor's model list. */
export interface FormModelItem {
  id: string;
  name: string;
  alias: string;
  originalName?: string;
  originalAlias?: string;
  image?: boolean;
  thinking?: {
    levels?: string[];
  };
  options: ModelOptionsDraft;
}
