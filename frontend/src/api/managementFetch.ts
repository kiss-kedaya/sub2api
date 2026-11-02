import { getAppRuntimeContext } from '@runtime/context'

/** Raw streaming fetches do not use the Axios adapter. Keep their auth contract
 * consistent: Nuxt uses its same-origin HttpOnly cookie, legacy SPA uses Bearer.
 * Never send a stale localStorage token (or "Bearer null") in cookie mode.
 */
export function managementFetchHeaders(extra: Record<string, string> = {}): Record<string, string> {
  const headers: Record<string, string> = { 'Content-Type': 'application/json', ...extra }
  if (!getAppRuntimeContext()?.cookieSession && typeof localStorage !== 'undefined') {
    const token = localStorage.getItem('auth_token')
    if (token) headers.Authorization = `Bearer ${token}`
  }
  return headers
}
