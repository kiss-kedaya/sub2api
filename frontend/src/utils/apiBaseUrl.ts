/**
 * The API endpoint an admin configures is normally written the way OpenAI SDKs want it, with a trailing
 * `/v1` (https://example.com/v1), but a bare site address is just as common. Examples and snippets need one
 * fixed starting point so they append `/v1/...` exactly once; this returns that starting point: the address
 * without trailing slashes and without any trailing `/v1` (also when it was typed twice).
 *
 * Only a whole `/v1` segment at the very end is removed: `/v10`, `/v1beta` and `/gateway/v1x` stay untouched.
 */
export function apiSiteRoot(value: string | null | undefined): string {
  let root = (value ?? '').trim().replace(/\/+$/, '')
  while (/\/v1$/i.test(root)) {
    root = root.replace(/\/v1$/i, '').replace(/\/+$/, '')
  }
  return root
}

/** `<site root>/v1`, or an empty string when there is no address to build from. */
export function apiBaseUrlWithV1(value: string | null | undefined): string {
  const root = apiSiteRoot(value)
  return root ? `${root}/v1` : ''
}
