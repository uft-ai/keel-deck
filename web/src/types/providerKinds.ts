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

/**
 * Brand marks bundled beside the Lobe catalog, keyed by icon id and relative to the
 * app's base path. Each file is the provider's own published artwork; its source and
 * hash are recorded in docs/architecture.md.
 */
export const BUNDLED_PROVIDER_MARKS: Record<string, string> = {
  [NAN_PROVIDER.iconId]: 'provider-marks/nan-builders.png',
};

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
