/**
 * Path, method, header and origin policy for the SSR BFF.
 *
 * Everything here is pure so the allowlist, the CSRF rules and the token
 * stripping can be unit tested without a running Nitro server.
 */

import { timingSafeEqual } from 'node:crypto'

export const API_PREFIX = '/api/v1'

/** Routes whose JSON body carries Go bearer tokens. */
export const TOKEN_ROUTES = new Set([
  `${API_PREFIX}/auth/login`,
  `${API_PREFIX}/auth/login/2fa`,
  `${API_PREFIX}/auth/register`,
  `${API_PREFIX}/auth/refresh`,
  `${API_PREFIX}/auth/logout`,
])

/** Routes served by the BFF itself; never forwarded upstream. */
export const BFF_ONLY_ROUTES = new Set([`${API_PREFIX}/auth/session`])

export const ALLOWED_METHODS = new Set(['GET', 'HEAD', 'POST', 'PUT', 'PATCH', 'DELETE'])

/**
 * CSRF header names. The client adapter sends `x-sub2api-csrf`; the older
 * `x-csrf-token` name is still accepted so a rolling deploy cannot lock users
 * out of their own mutations.
 */
export const CSRF_HEADERS = ['x-sub2api-csrf', 'x-csrf-token'] as const

export function readCsrfHeader(
  headers: Record<string, string | string[] | undefined>
): string | undefined {
  for (const name of CSRF_HEADERS) {
    const raw = headers[name] ?? headers[name.toUpperCase()]
    if (raw === undefined) continue
    const value = Array.isArray(raw) ? raw[0] : raw
    if (value) return value
  }
  return undefined
}

const MUTATING_METHODS = new Set(['POST', 'PUT', 'PATCH', 'DELETE'])

/** Request headers worth forwarding. Everything else is dropped. */
const REQUEST_HEADER_ALLOWLIST = new Set([
  'accept',
  'accept-language',
  'content-language',
  'content-type',
  'idempotency-key',
  'range',
  'user-agent',
  'x-admin-ui-request',
  'x-client-request-id',
  'x-request-id',
  'x-user-ui-request',
])

/** Response headers worth forwarding. Set-Cookie and CORS headers never are. */
const RESPONSE_HEADER_ALLOWLIST = new Set([
  'content-disposition',
  'content-length',
  'content-encoding',
  'content-type',
  'etag',
  'last-modified',
  'location',
  'retry-after',
  'x-request-id',
  'x-ratelimit-limit',
  'x-ratelimit-remaining',
  'x-ratelimit-reset',
])

const MAX_PATH_LENGTH = 512

export interface PathDecision {
  ok: boolean
  path?: string
  reason?: string
}

/**
 * Validates the upstream path.
 *
 * The origin is fixed in runtime config; only the path is client supplied, and
 * it must stay inside `/api/v1`. Dot segments, encoded separators, scheme-like
 * prefixes and control characters are rejected instead of normalised, so a
 * malformed request can never be turned into a different upstream target.
 */
export function resolveUpstreamPath(rawPath: string | undefined | null): PathDecision {
  if (!rawPath || typeof rawPath !== 'string') return { ok: false, reason: 'missing path' }
  if (!rawPath.startsWith('/')) return { ok: false, reason: 'relative path' }
  if (rawPath.length > MAX_PATH_LENGTH) return { ok: false, reason: 'path too long' }
  if (/[\u0000-\u001f\u007f]/.test(rawPath)) return { ok: false, reason: 'control character' }
  if (rawPath.includes('\\') || rawPath.includes('//')) return { ok: false, reason: 'malformed path' }
  if (rawPath.includes('?') || rawPath.includes('#')) return { ok: false, reason: 'query in path' }
  // Encoded separators could turn one segment into two after decoding.
  if (/%2f|%5c/i.test(rawPath)) return { ok: false, reason: 'encoded separator' }

  let decoded: string
  try {
    decoded = decodeURIComponent(rawPath)
  } catch {
    return { ok: false, reason: 'malformed escape' }
  }
  if (decoded.includes('\\') || decoded.includes('//')) return { ok: false, reason: 'malformed path' }
  if (/[\u0000-\u001f\u007f]/.test(decoded)) return { ok: false, reason: 'control character' }

  const segments = decoded.split('/')
  for (const segment of segments) {
    if (segment === '.' || segment === '..') return { ok: false, reason: 'dot segment' }
    if (segment.includes(':') && segment.startsWith(':')) return { ok: false, reason: 'scheme segment' }
  }

  const parts = segments.filter((segment) => segment !== '')
  if (parts.length === 0) return { ok: false, reason: 'empty path' }
  const normalized = `/${parts.join('/')}`
  if (!normalized.startsWith(`${API_PREFIX}/`)) return { ok: false, reason: 'outside api prefix' }
  return { ok: true, path: normalized.replace(/\/$/, '') }
}

export function isAllowedMethod(method: string | undefined | null): boolean {
  return !!method && ALLOWED_METHODS.has(method.toUpperCase())
}

export function isMutatingMethod(method: string | undefined | null): boolean {
  return !!method && MUTATING_METHODS.has(method.toUpperCase())
}

export function isTokenRoute(path: string): boolean {
  return TOKEN_ROUTES.has(path)
}

export function isBffOnlyRoute(path: string): boolean {
  return BFF_ONLY_ROUTES.has(path)
}

export function isLogoutRoute(path: string): boolean {
  return path === `${API_PREFIX}/auth/logout`
}

export function filterRequestHeaders(
  headers: Record<string, string | string[] | undefined>
): Record<string, string> {
  const out: Record<string, string> = {}
  for (const [name, value] of Object.entries(headers)) {
    const key = name.toLowerCase()
    if (!REQUEST_HEADER_ALLOWLIST.has(key)) continue
    if (value === undefined) continue
    out[key] = Array.isArray(value) ? value.join(', ') : String(value)
  }
  return out
}

export function filterResponseHeaders(headers: Headers): Record<string, string> {
  const out: Record<string, string> = {}
  headers.forEach((value, name) => {
    if (RESPONSE_HEADER_ALLOWLIST.has(name.toLowerCase())) out[name.toLowerCase()] = value
  })
  // Everything the BFF returns is per-session data.
  out['cache-control'] = 'no-store, no-cache, must-revalidate, private'
  out['pragma'] = 'no-cache'
  out['vary'] = 'cookie'
  return out
}

/**
 * Location handling for upstream redirects.
 *
 * - a redirect to the private backend origin is rewritten to the public origin;
 * - a redirect to any other loopback/private host is dropped, never leaked;
 * - same-origin and relative redirects pass through;
 * - third-party redirects (OAuth start) pass through unchanged, which is safe:
 *   the browser follows them without any cookie of ours and without a token,
 *   and the BFF itself never follows redirects.
 *
 * Returns null when the header must be removed.
 */
export function sanitizeLocation(
  location: string,
  backendOrigin: string,
  publicOrigin: string
): string | null {
  if (!location) return null
  let url: URL
  try {
    url = new URL(location, publicOrigin || backendOrigin)
  } catch {
    return null
  }
  const backend = new URL(backendOrigin)
  if (url.origin === backend.origin) {
    if (!publicOrigin) return null
    return `${publicOrigin}${url.pathname}${url.search}${url.hash}`
  }
  if (isPrivateHost(url.hostname)) return null
  return location
}

function isPrivateHost(hostname: string): boolean {
  const host = hostname.replace(/^\[|\]$/g, '').toLowerCase()
  if (host === 'localhost' || host === '::1' || host === '0.0.0.0') return true
  if (host.endsWith('.internal') || host.endsWith('.local')) return true
  return isTrustedPeer(host) || /^127\./.test(host) || /^169\.254\./.test(host)
}

/**
 * Delegates to the proxy chain only when the immediate peer is the local nginx
 * or the loopback address; otherwise a client could spoof its own IP and dodge
 * the Go rate limiter.
 */
export function resolveClientIp(
  forwardedFor: string | string[] | undefined,
  remoteAddress: string | undefined
): string {
  const chain = (Array.isArray(forwardedFor) ? forwardedFor.join(',') : forwardedFor || '')
    .split(',')
    .map((part) => part.trim())
    .filter(Boolean)
  const peerIsTrusted = isTrustedPeer(remoteAddress)
  if (peerIsTrusted && chain.length > 0) {
    const candidate = chain[0]
    if (isIpLiteral(candidate)) return candidate
  }
  return isIpLiteral(remoteAddress) ? remoteAddress! : ''
}

export function isTrustedPeer(address: string | undefined): boolean {
  if (!address) return false
  const value = address.replace(/^::ffff:/, '')
  if (value === '::1' || value === '127.0.0.1') return true
  if (/^10\./.test(value)) return true
  if (/^192\.168\./.test(value)) return true
  if (/^172\.(1[6-9]|2\d|3[01])\./.test(value)) return true
  return false
}

function isIpLiteral(value: string | undefined): boolean {
  if (!value) return false
  const ipv4 = /^(\d{1,3}\.){3}\d{1,3}$/
  const ipv6 = /^[0-9a-f:]+$/i
  if (ipv4.test(value)) return value.split('.').every((part) => Number(part) <= 255)
  return ipv6.test(value) && value.includes(':')
}

export interface CsrfDecision {
  ok: boolean
  reason?: string
}

/**
 * Double submit plus origin pinning for cookie-authenticated mutations.
 * Bearer-authenticated requests are exempt: the credential is not ambient.
 */
export function checkCsrf(input: {
  method: string
  path: string
  usesCookieSession: boolean
  csrfHeader: string | undefined
  csrfToken: string | undefined
  origin: string | undefined
  referer: string | undefined
  host: string | undefined
  proto: string
}): CsrfDecision {
  if (!isMutatingMethod(input.method)) return { ok: true }
  if (!input.usesCookieSession) return { ok: true }

  // Login/2fa/register/refresh are authorised by credentials in the body, not by
  // the ambient cookie, so they only need the same-origin check below. Logout
  // and everything else must echo the session's CSRF token.
  const credentialRoute = isTokenRoute(input.path) && !isLogoutRoute(input.path)
  if (!credentialRoute) {
    if (!input.csrfHeader || !input.csrfToken || !safeTokenEquals(input.csrfHeader, input.csrfToken)) {
      return { ok: false, reason: 'csrf token missing or mismatched' }
    }
  }

  const expectedHost = (input.host || '').toLowerCase()
  if (!expectedHost) return { ok: false, reason: 'missing host' }
  const expectedOrigin = `${input.proto}://${expectedHost}`
  const source = input.origin || input.referer
  if (!source) return { ok: false, reason: 'missing origin' }
  let sourceOrigin: string
  try {
    sourceOrigin = new URL(source).origin.toLowerCase()
  } catch {
    return { ok: false, reason: 'malformed origin' }
  }
  if (sourceOrigin !== expectedOrigin) return { ok: false, reason: 'cross-origin request' }
  return { ok: true }
}

function safeTokenEquals(left: string, right: string): boolean {
  const a = Buffer.from(left)
  const b = Buffer.from(right)
  return a.length === b.length && timingSafeEqual(a, b)
}

export interface TokenPayload {
  accessToken?: string
  refreshToken?: string
  expiresIn?: number
  uid?: number
}

/**
 * Applies the BFF session contract to a Go auth response.
 *
 * The envelope stays byte identical (code, message, user, token_type,
 * requires_2fa, temp_token). Only the bearer tokens change:
 *   - `access_token` / `refresh_token` become empty strings, not missing keys,
 *     so an older client that writes them to localStorage writes '' (falsy),
 *     stops sending an Authorization header, and falls back to the cookie.
 *   - `session_managed: true` tells the new client the cookie owns the session.
 */
export function applySessionContract(payload: unknown): { payload: unknown; tokens: TokenPayload } {
  if (!payload || typeof payload !== 'object') return { payload, tokens: {} }
  const root = payload as Record<string, unknown>
  const data = root.data
  if (!data || typeof data !== 'object') return { payload, tokens: {} }
  const body = { ...(data as Record<string, unknown>) }
  const tokens: TokenPayload = {}
  if (typeof body.access_token === 'string' && body.access_token) tokens.accessToken = body.access_token
  if (typeof body.refresh_token === 'string' && body.refresh_token) tokens.refreshToken = body.refresh_token
  if (typeof body.expires_in === 'number') tokens.expiresIn = body.expires_in
  const user = body.user
  if (user && typeof user === 'object') {
    const id = Number((user as Record<string, unknown>).id)
    if (Number.isFinite(id) && id > 0) tokens.uid = id
  }
  body.access_token = ''
  body.refresh_token = ''
  body.session_managed = Boolean(tokens.accessToken)
  return { payload: { ...root, data: body }, tokens }
}
