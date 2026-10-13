import type { ProviderModelItem } from '../../types/providers';
import { readModelOptionsDraft } from './modelOptions';
import type { FormKeyItem, FormModelItem } from './managementProviders';

/**
 * Account discovery reads the provider's model list through every key that can
 * serve. One shared entry routes each model to every key, so the keys must agree:
 * a failed, empty or different list leaves the form as it was.
 */

/** One key's discovery answer; `models` is absent when the read failed. */
export interface KeyCatalog {
  /** 1-based position in the key list. Never the key itself. */
  position: number;
  models?: string[];
}

export type CatalogAgreement =
  | { ok: true; models: string[] }
  | { ok: false; reason: 'failed' | 'empty'; positions: number[] }
  | { ok: false; reason: 'mismatch'; positions: number[]; differing: string[] };

/** Rows that can serve: a key the form holds or one stored under an auth index. */
export function servingKeys(keys: FormKeyItem[]): FormKeyItem[] {
  return keys.filter((key) => (key.apiKey || '').trim() !== '' || Boolean(key.authIndex));
}

function uniqueNames(names: string[]): string[] {
  return Array.from(new Set(names.map((name) => name.trim()).filter(Boolean))).sort((a, b) => a.localeCompare(b));
}

/** Checks that every key answered with the same nonempty list. Order is irrelevant. */
export function agreeOnCatalog(catalogs: KeyCatalog[]): CatalogAgreement {
  const failed = catalogs.filter((catalog) => catalog.models === undefined).map((catalog) => catalog.position);
  if (failed.length > 0) return { ok: false, reason: 'failed', positions: failed };
  const lists = catalogs.map((catalog) => ({ position: catalog.position, models: uniqueNames(catalog.models ?? []) }));
  const empty = lists.filter((list) => list.models.length === 0).map((list) => list.position);
  if (lists.length === 0 || empty.length > 0) return { ok: false, reason: 'empty', positions: empty };
  const first = lists[0].models;
  const different = lists.filter((list) => list.models.join('\n') !== first.join('\n'));
  if (different.length === 0) return { ok: true, models: first };
  const all = new Set(lists.flatMap((list) => list.models));
  const differing = [...all].filter((name) => !lists.every((list) => list.models.includes(name))).sort();
  return { ok: false, reason: 'mismatch', positions: lists.map((list) => list.position), differing };
}

export interface RefreshedRows {
  rows: FormModelItem[];
  added: string[];
  /** Stored upstream names the account no longer listed. Their rows stay. */
  omitted: string[];
}

/**
 * Adds one enabled row for each listed model that no row uses yet. Every
 * existing row stays as it is, including disabled ones and models the account
 * stopped listing, so a model that leaves and returns keeps its choice.
 */
export function refreshModelRows(
  rows: FormModelItem[],
  discovered: string[],
  defaults: Record<string, ProviderModelItem>,
  stamp: number,
): RefreshedRows {
  const kept = rows.filter((row) => row.name.trim() !== '');
  const stored = new Set(kept.map((row) => row.name.trim()));
  const listed = new Set(discovered);
  const added = discovered.filter((name) => !stored.has(name));
  const omitted = [...stored].filter((name) => !listed.has(name)).sort();
  return {
    rows: [
      ...kept,
      ...added.map((name, index) => ({
        id: `mdl-${stamp}-${index}`,
        name,
        alias: '',
        image: defaults[name]?.image || false,
        thinking: { levels: defaults[name]?.thinking?.levels || [] },
        options: readModelOptionsDraft(defaults[name]?.options),
        disabled: false,
      })),
    ],
    added,
    omitted,
  };
}
