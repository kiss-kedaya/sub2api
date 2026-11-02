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
  'origin',
  // Same-origin return_url checks in the Go API read the browser's referer.
  'referer',
  'range',
  'user-agent',
  'x-admin-ui-request',
  'x-client-request-id',
  // Node's fetch cannot override Host, so the backend would see the loopback
  // address. These carry the real public origin for same-origin checks.
  'x-forwarded-host',
  'x-forwarded-proto',
  'x-request-id',
  'x-user-ui-request',
])

const OAUTH_COOKIE_NAMES = new Set([
  'dingtalk_oauth_state', 'dingtalk_oauth_redirect', 'dingtalk_oauth_intent', 'dingtalk_oauth_bind_user',
  'email_oauth_state', 'email_oauth_redirect', 'email_oauth_provider', 'email_oauth_affiliate',
  'linuxdo_oauth_state', 'linuxdo_oauth_verifier', 'linuxdo_oauth_redirect', 'linuxdo_oauth_intent', 'linuxdo_oauth_bind_user',
  'oauth_bind_access_token', 'oauth_pending_browser_session', 'oauth_pending_session', 'oauth_promo_code',
  'oidc_oauth_state', 'oidc_oauth_verifier', 'oidc_oauth_redirect', 'oidc_oauth_nonce', 'oidc_oauth_intent', 'oidc_oauth_bind_user',
  'wechat_oauth_state', 'wechat_oauth_redirect', 'wechat_oauth_intent', 'wechat_oauth_mode', 'wechat_oauth_bind_user',
  'wechat_payment_oauth_state', 'wechat_payment_oauth_redirect', 'wechat_payment_oauth_context', 'wechat_payment_oauth_scope',
])

const OAUTH_PROVIDER_COOKIE_NAMES: Record<string, ReadonlySet<string>> = {
  dingtalk: new Set(['dingtalk_oauth_state', 'dingtalk_oauth_redirect', 'dingtalk_oauth_intent', 'dingtalk_oauth_bind_user']),
  github: new Set(),
  google: new Set(),
  linuxdo: new Set(['linuxdo_oauth_state', 'linuxdo_oauth_verifier', 'linuxdo_oauth_redirect', 'linuxdo_oauth_intent', 'linuxdo_oauth_bind_user']),
  oidc: new Set(['oidc_oauth_state', 'oidc_oauth_verifier', 'oidc_oauth_redirect', 'oidc_oauth_nonce', 'oidc_oauth_intent', 'oidc_oauth_bind_user']),
  wechat: new Set(['wechat_oauth_state', 'wechat_oauth_redirect', 'wechat_oauth_intent', 'wechat_oauth_mode', 'wechat_oauth_bind_user']),
}

const OAUTH_SHARED_COOKIE_NAMES = new Set([
  'oauth_bind_access_token', 'oauth_pending_browser_session', 'oauth_pending_session', 'oauth_promo_code',
])
const OAUTH_SHARED_COOKIE_PATHS = new Set([
  ...OAUTH_SHARED_COOKIE_NAMES,
  'email_oauth_state', 'email_oauth_redirect', 'email_oauth_provider', 'email_oauth_affiliate',
])

const EMAIL_OAUTH_COOKIE_NAMES = new Set(['email_oauth_state', 'email_oauth_redirect', 'email_oauth_provider', 'email_oauth_affiliate'])
const WECHAT_PAYMENT_COOKIE_NAMES = new Set([
  'wechat_payment_oauth_state', 'wechat_payment_oauth_redirect', 'wechat_payment_oauth_context', 'wechat_payment_oauth_scope',
])

/** OAuth cookies are forwarded only on known OAuth endpoints and by exact name. */
export function oauthCookieNamesForPath(path: string): ReadonlySet<string> | null {
  const providerMatch = /^\/api\/v1\/auth\/oauth\/(dingtalk|github|google|linuxdo|oidc|wechat)\/(start|callback|bind\/start|complete-registration|bind-login|create-account)$/.exec(path)
  if (providerMatch) {
    const [, provider, action] = providerMatch
    if ((provider === 'github' || provider === 'google') &&
      !['start', 'callback', 'complete-registration'].includes(action)) return null
    return new Set([
      ...OAUTH_SHARED_COOKIE_NAMES,
      ...(OAUTH_PROVIDER_COOKIE_NAMES[provider] ?? []),
      ...(provider === 'github' || provider === 'google' ? EMAIL_OAUTH_COOKIE_NAMES : []),
    ])
  }
  if (path === `${API_PREFIX}/auth/oauth/bind-token`) return new Set(['oauth_bind_access_token'])
  if (/^\/api\/v1\/auth\/oauth\/wechat\/payment\/(?:start|callback)$/.test(path)) {
    return WECHAT_PAYMENT_COOKIE_NAMES
  }
  if (/^\/api\/v1\/auth\/oauth\/pending\/(?:exchange|send-verify-code|create-account|bind-login)$/.test(path)) {
    return OAUTH_SHARED_COOKIE_NAMES
  }
  return null
}

const OAUTH_COMPLETION_ROUTES = new Set([
  `${API_PREFIX}/auth/oauth/pending/exchange`,
  `${API_PREFIX}/auth/oauth/pending/create-account`,
  `${API_PREFIX}/auth/oauth/pending/bind-login`,
  ...['github', 'google'].map((provider) => `${API_PREFIX}/auth/oauth/${provider}/complete-registration`),
  ...['dingtalk', 'linuxdo', 'oidc', 'wechat'].flatMap((provider) =>
    ['complete-registration', 'bind-login', 'create-account'].map((action) => `${API_PREFIX}/auth/oauth/${provider}/${action}`)
  ),
])

export function isOAuthCompletionRoute(path: string): boolean {
  return OAUTH_COMPLETION_ROUTES.has(path)
}

export function isOAuthCallbackRoute(path: string): boolean {
  return /^\/api\/v1\/auth\/oauth\/(?:dingtalk|github|google|linuxdo|oidc|wechat)\/callback$/.test(path)
}

export function isOAuthCookieRoute(path: string): boolean {
  return oauthCookieNamesForPath(path) !== null
}

export function isOAuthLoginStartRoute(path: string): boolean {
  return /^\/api\/v1\/auth\/oauth\/(?:github|google|linuxdo|oidc|dingtalk|wechat)\/start$/.test(path)
}

export function filterOAuthRequestCookie(path: string, rawCookie: string | undefined): string | undefined {
  const allowed = oauthCookieNamesForPath(path)
  if (!allowed || !rawCookie) return undefined
  const kept: string[] = []
  for (const part of rawCookie.split(';')) {
    const separator = part.indexOf('=')
    if (separator < 1) continue
    const name = part.slice(0, separator).trim()
    if (allowed.has(name) && OAUTH_COOKIE_NAMES.has(name)) kept.push(`${name}=${part.slice(separator + 1).trim()}`)
  }
  return kept.length ? kept.join('; ') : undefined
}

/** Returns only Go-issued OAuth Set-Cookie lines whose exact names belong to this endpoint. */
export function filterOAuthSetCookieHeaders(path: string, values: readonly string[]): string[] {
  const allowed = oauthCookieNamesForPath(path)
  if (!allowed) return []
  return values.filter((line) => {
    const separator = line.indexOf('=')
    if (separator < 1) return false
    const name = line.slice(0, separator).trim().replace(/^Set-Cookie:\s*/i, '')
    if (!allowed.has(name) || !OAUTH_COOKIE_NAMES.has(name) || /[\r\n]/.test(line)) return false
    const attributes = line.slice(separator + 1).split(';').slice(1).map((part) => part.trim())
    if (attributes.some((attribute) => /^domain\s*=/i.test(attribute))) return false
    const cookiePath = attributes.find((attribute) => /^path\s*=/i.test(attribute))?.split('=', 2)[1]?.trim()
    const expectedPath = OAUTH_SHARED_COOKIE_PATHS.has(name)
      ? `${API_PREFIX}/auth/oauth`
      : name.startsWith('wechat_payment_oauth_')
        ? `${API_PREFIX}/auth/oauth/wechat/payment`
        : name.startsWith('wechat_oauth_')
          ? `${API_PREFIX}/auth/oauth/wechat`
          : name.startsWith('oidc_oauth_')
            ? `${API_PREFIX}/auth/oauth/oidc`
            : name.startsWith('linuxdo_oauth_')
              ? `${API_PREFIX}/auth/oauth/linuxdo`
              : name.startsWith('dingtalk_oauth_')
                ? `${API_PREFIX}/auth/oauth/dingtalk`
                : ''
    return Boolean(
      expectedPath &&
      cookiePath === expectedPath &&
      attributes.some((attribute) => /^httponly$/i.test(attribute)) &&
      attributes.some((attribute) => /^samesite=lax$/i.test(attribute))
    )
  })
}

/** Wire framing, Set-Cookie and CORS headers must not be generically forwarded. */
const RESPONSE_HEADER_ALLOWLIST = new Set([
  'content-disposition',
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
  if (containsControlCharacter(rawPath)) return { ok: false, reason: 'control character' }
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
  if (containsControlCharacter(decoded)) return { ok: false, reason: 'control character' }

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

function containsControlCharacter(value: string): boolean {
  for (let index = 0; index < value.length; index += 1) {
    const code = value.charCodeAt(index)
    if (code <= 0x1f || code === 0x7f) return true
  }
  return false
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

/** This selects a response contract, not authentication or permission.
 * Go still verifies credentials, 2FA, refresh rotation and every Bearer request.
 * Browser markers always win over a caller's bearer hint. Node fetch adds only
 * sec-fetch-mode by default, so that header alone is not a browser credential.
 */
export function isMachineTokenRequest(
  path: string,
  headers: Record<string, string | string[] | undefined>,
  hasSession: boolean
): boolean {
  if (!isTokenRoute(path) || hasSession) return false
  const normalized = Object.fromEntries(Object.entries(headers).map(([key, value]) => [key.toLowerCase(), value]))
  if (String(normalized['x-sub2api-auth-mode'] || '').toLowerCase().split(',').some((mode) => mode.trim() === 'cookie')) return false
  for (const key of ['cookie', 'origin', 'referer', 'sec-fetch-site', 'sec-fetch-dest', 'sec-fetch-user']) {
    if (normalized[key] !== undefined) return false
  }
  const fetchMode = String(normalized['sec-fetch-mode'] || '').toLowerCase()
  if (fetchMode && fetchMode !== 'cors') return false
  const contentType = String(normalized['content-type'] || '').split(';')[0].trim().toLowerCase()
  return contentType === 'application/json'
}

/** Login CSRF matters before a cookie exists too. */
export function checkSameOrigin(input: {
  origin: string | undefined
  referer: string | undefined
  host: string | undefined
  proto: string
}): CsrfDecision {
  const expectedHost = (input.host || '').toLowerCase()
  if (!expectedHost) return { ok: false, reason: 'missing host' }
  const source = input.origin || input.referer
  if (!source) return { ok: false, reason: 'missing origin' }
  try {
    if (new URL(source).origin.toLowerCase() !== `${input.proto}://${expectedHost}`) {
      return { ok: false, reason: 'cross-origin request' }
    }
  } catch {
    return { ok: false, reason: 'malformed origin' }
  }
  return { ok: true }
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
  // Node fetch decompresses the body while retaining the upstream wire headers.
  // Neither the compressed byte length nor its encoding describes our body.
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

  // Login/2fa/register use body credentials. Cookie refresh instead substitutes
  // the sealed session's refresh token, so it MUST echo that session's CSRF.
  const credentialRoute = isTokenRoute(input.path) && !isLogoutRoute(input.path) &&
    input.path !== `${API_PREFIX}/auth/refresh`
  if (!credentialRoute) {
    if (!input.csrfHeader || !input.csrfToken || !safeTokenEquals(input.csrfHeader, input.csrfToken)) {
      return { ok: false, reason: 'csrf token missing or mismatched' }
    }
  }

  return checkSameOrigin(input)
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
