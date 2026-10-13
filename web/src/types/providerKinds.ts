/**
 * Known providers behind the generic OpenAI-compatible family.
 *
 * The server derives `provider_kind` from the endpoint on every read, and this module
 * applies the same host rule where no server value exists yet: the unsaved editor and
 * icon lookups. The host is the evidence; a provider named "nan" elsewhere is not NaN.
 */
export const NAN_PROVIDER = {
  kind: 'nan',
  preset: 'nan-builders',
  label: 'NaN.Builders',
  host: 'api.nan.builders',
  baseURL: 'https://api.nan.builders/v1',
  prefix: 'nan',
  website: 'https://nan.builders',
  iconId: 'NanBuilders',
} as const;

/** Whether an endpoint, or a bare host, is NaN.Builders' official API host. */
export function isNanBaseURL(value?: string): boolean {
  const text = (value || '').trim();
  if (!text) return false;
  try {
    const parsed = new URL(text.includes('://') ? text : `https://${text}`);
    return parsed.hostname.toLowerCase() === NAN_PROVIDER.host;
  } catch {
    return false;
  }
}
