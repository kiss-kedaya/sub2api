/**
 * Server-side request orchestration for the SSR BFF.
 *
 * Nothing in here imports H3 or Nitro: the route file adapts an event into a
 * `BffContext` and writes the `BffResult` back. That keeps the security rules
 * unit testable with a fake fetch.
 */

import {
  API_PREFIX,
  applySessionContract,
  checkCsrf,
  checkSameOrigin,
  filterOAuthRequestCookie,
  filterOAuthSetCookieHeaders,
  filterRequestHeaders,
  filterResponseHeaders,
  isOAuthCallbackRoute,
  isOAuthCookieRoute,
  isOAuthCompletionRoute,
  isOAuthLoginStartRoute,
  isLogoutRoute,
  isMachineTokenRequest,
  isMutatingMethod,
  isTokenRoute,
  readCsrfHeader,
  sanitizeLocation,
} from './proxyPolicy'
import { createSession, isSessionExpired, randomToken, type ServerSession } from './session'

export interface BffContext {
  method: string
  /** Already validated by resolveUpstreamPath. */
  path: string
  /** Raw query string without the leading '?'. */
  query: string
  headers: Record<string, string | string[] | undefined>
  body: Buffer | null
  session: ServerSession | null
  sessionPassword: string
  backendOrigin: string
  publicOrigin: string
  protocol: string
  clientIp: string
  fetchImpl?: typeof fetch
  now?: () => number
  /** Time to first byte for the upstream call; a streaming body is not cut. */
  timeoutMs?: number
}

const DEFAULT_TIMEOUT_MS = 180_000
const REFRESH_TIMEOUT_MS = 30_000

export type SessionEffect =
  | { action: 'keep' }
  | { action: 'set'; value: ServerSession }
  | { action: 'clear' }

export interface BffResult {
  status: number
  headers: Record<string, string | string[]>
  body: Buffer | ReadableStream<Uint8Array> | null
  session: SessionEffect
  setCookies?: string[]
}

const JSON_HEADERS = { 'content-type': 'application/json; charset=utf-8' }

function envelope(code: number, message: string): Buffer {
  return Buffer.from(JSON.stringify({ code, message, reason: '', metadata: null }), 'utf8')
}

function fail(status: number, message: string): BffResult {
  return {
    status,
    headers: { ...JSON_HEADERS, 'cache-control': 'no-store', vary: 'cookie' },
    body: envelope(status, message),
    session: { action: 'keep' },
  }
}

function headerValue(
  headers: Record<string, string | string[] | undefined>,
  name: string
): string | undefined {
  const raw = headers[name] ?? headers[name.toLowerCase()] ??
    Object.entries(headers).find(([key]) => key.toLowerCase() === name.toLowerCase())?.[1]
  if (raw === undefined) return undefined
  return Array.isArray(raw) ? raw[0] : raw
}

export function buildUpstreamUrl(origin: string, path: string, query: string): string {
  const base = new URL(origin)
  if (base.protocol !== 'http:' && base.protocol !== 'https:') throw new Error('unsupported backend protocol')
  if (base.username || base.password) throw new Error('backend origin must not carry credentials')
  if (!path.startsWith(`${API_PREFIX}/`) && path !== API_PREFIX) throw new Error('path outside api prefix')
  const url = new URL(`${path}${query ? `?${query}` : ''}`, base.origin)
  if (url.origin !== base.origin) throw new Error('origin escape')
  return url.toString()
}

function readBearer(headers: Record<string, string | string[] | undefined>): string | null {
  const raw = headerValue(headers, 'authorization')
  if (!raw) return null
  const match = /^Bearer\s+(.+)$/i.exec(raw.trim())
  return match ? match[1].trim() : null
}

/**
 * In-process single flight for token rotation.
 *
 * Two parallel 401s in the same Node process must not both spend the one-time
 * refresh token: the loser would fail and, before this guard, clear the cookie.
 * Cross-process races are handled by never clearing on a refresh failure.
 */
const inflightRefresh = new Map<string, Promise<ServerSession | null>>()

function refreshSession(ctx: BffContext, session: ServerSession): Promise<ServerSession | null> {
  const key = session.refreshToken
  if (!key) return Promise.resolve(null)
  const existing = inflightRefresh.get(key)
  if (existing) return existing
  const pending = doRefreshSession(ctx, session)
  inflightRefresh.set(key, pending)
  void pending.finally(() => {
    if (inflightRefresh.get(key) === pending) inflightRefresh.delete(key)
  })
  return pending
}

async function doRefreshSession(ctx: BffContext, session: ServerSession): Promise<ServerSession | null> {
  if (!session.refreshToken) return null
  const fetchImpl = ctx.fetchImpl ?? fetch
  const controller = new AbortController()
  const timer = setTimeout(() => controller.abort(), REFRESH_TIMEOUT_MS)
  try {
    const res = await fetchImpl(buildUpstreamUrl(ctx.backendOrigin, `${API_PREFIX}/auth/refresh`, ''), {
      method: 'POST',
      headers: {
        'content-type': 'application/json',
        ...(headerValue(ctx.headers, 'user-agent') ? { 'user-agent': headerValue(ctx.headers, 'user-agent')! } : {}),
        ...(ctx.clientIp ? { 'x-forwarded-for': ctx.clientIp, 'x-real-ip': ctx.clientIp } : {}),
      },
      body: JSON.stringify({ refresh_token: session.refreshToken }),
      redirect: 'manual',
      cache: 'no-store',
      signal: controller.signal,
    })
    if (!res.ok) return null
    const payload = (await res.json()) as { code?: number; data?: Record<string, unknown> }
    if (payload?.code !== 0 || !payload.data) return null
    const accessToken = typeof payload.data.access_token === 'string' ? payload.data.access_token : ''
    const refreshToken =
      typeof payload.data.refresh_token === 'string' && payload.data.refresh_token
        ? payload.data.refresh_token
        : session.refreshToken
    const expiresIn = typeof payload.data.expires_in === 'number' ? payload.data.expires_in : 0
    if (!accessToken) return null
    return {
      ...createSession({ accessToken, refreshToken, expiresIn, uid: session.uid, now: ctx.now?.() }),
      // Keep the CSRF token stable across a silent refresh so in-flight forms stay valid.
      csrf: session.csrf,
    }
  } catch {
    return null
  } finally {
    clearTimeout(timer)
  }
}

export async function handleBffRequest(ctx: BffContext): Promise<BffResult> {
  const fetchImpl = ctx.fetchImpl ?? fetch
  const now = ctx.now ?? Date.now
  const machineTokens = isMachineTokenRequest(ctx.path, ctx.headers, Boolean(ctx.session), ctx.method)

  // Without a session secret the BFF cannot hold a login, but public endpoints
  // must still render: proxy them anonymously. Only the endpoints that would
  // establish sessions fail closed, including machine auth. A missing cookie
  // secret must never silently enable a different externally visible mode.
  const sessionConfigured = Boolean(ctx.sessionPassword)
  if (!sessionConfigured && (isTokenRoute(ctx.path) || isOAuthCompletionRoute(ctx.path) || isOAuthCallbackRoute(ctx.path) || isOAuthLoginStartRoute(ctx.path)) && !isLogoutRoute(ctx.path)) {
    return fail(503, 'SSR session is not configured')
  }

  let session = ctx.session
  const usesCookieSession = !!session
  const origin = headerValue(ctx.headers, 'origin')
  const referer = headerValue(ctx.headers, 'referer')
  const host = headerValue(ctx.headers, 'host')

  // Do not create an attacker-selected browser login through an anonymous
  // cross-site POST. A forged auth-mode header never overrides browser markers.
  if (!machineTokens && isMutatingMethod(ctx.method) &&
    (isTokenRoute(ctx.path) || isOAuthCompletionRoute(ctx.path))) {
    if (headerValue(ctx.headers, 'sec-fetch-site') === 'cross-site' ||
      !checkSameOrigin({ origin, referer, host, proto: ctx.protocol }).ok) {
      return fail(403, 'Request blocked by origin policy')
    }
  }

  const csrf = checkCsrf({
    method: ctx.method,
    path: ctx.path,
    usesCookieSession,
    csrfHeader: readCsrfHeader(ctx.headers),
    csrfToken: session?.csrf,
    origin,
    referer,
    host,
    proto: ctx.protocol,
  })
  if (usesCookieSession && isMutatingMethod(ctx.method) && headerValue(ctx.headers, 'sec-fetch-site') === 'cross-site') {
    return fail(403, 'Request blocked by origin policy')
  }
  if (!csrf.ok) return fail(403, 'Request blocked by origin policy')
  if (!machineTokens && ctx.path === `${API_PREFIX}/auth/refresh` && !session && isMutatingMethod(ctx.method)) {
    return fail(401, 'A valid browser session is required')
  }

  const headers = filterRequestHeaders(ctx.headers)
  const oauthCookie = filterOAuthRequestCookie(ctx.path, headerValue(ctx.headers, 'cookie'))
  if (oauthCookie) headers.cookie = oauthCookie
  // Go callback handlers use the public request origin to generate redirects.
  // Derive forwarded values from the BFF request context, never from client X-Forwarded-*.
  const forwardPublicOrigin = isOAuthCookieRoute(ctx.path) ||
    (ctx.method.toUpperCase() === 'POST' && ctx.path === `${API_PREFIX}/payment/orders`)
  if (forwardPublicOrigin) {
    try {
      const publicURL = new URL(ctx.publicOrigin)
      headers['x-forwarded-host'] = publicURL.host
      headers['x-forwarded-proto'] = publicURL.protocol.slice(0, -1)
      headers.origin = publicURL.origin
      const referer = headerValue(ctx.headers, 'referer')
      if (referer) {
        try {
          if (new URL(referer).origin === publicURL.origin) headers.referer = referer
        } catch { /* Malformed referers are untrusted and omitted. */ }
      }
    } catch {
      return fail(400, 'Invalid public origin')
    }
  }
  const bearer = readBearer(ctx.headers)
  if (session) {
    // A cookie session always wins: the browser must not be able to pin another token.
    headers.authorization = `Bearer ${session.accessToken}`
  } else if (bearer) {
    headers.authorization = `Bearer ${bearer}`
  }
  if (ctx.clientIp) {
    headers['x-forwarded-for'] = ctx.clientIp
    headers['x-real-ip'] = ctx.clientIp
  }

  let requestBody = ctx.body
  if (session && (ctx.path === `${API_PREFIX}/auth/refresh` || isLogoutRoute(ctx.path))) {
    // Refresh and logout use this session's token. Logout must revoke its
    // refresh family even after the access token has expired.
    requestBody = Buffer.from(JSON.stringify({ refresh_token: session.refreshToken }), 'utf8')
    headers['content-type'] = 'application/json'
  }

  const timeoutMs = ctx.timeoutMs && ctx.timeoutMs > 0 ? ctx.timeoutMs : DEFAULT_TIMEOUT_MS

  // Bounds the wait for response headers only: once the upstream answers, the
  // body keeps streaming and is never cut by this timer.
  const send = async (): Promise<Response> => {
    const controller = new AbortController()
    const timer = setTimeout(() => controller.abort(), timeoutMs)
    try {
      return await fetchImpl(buildUpstreamUrl(ctx.backendOrigin, ctx.path, ctx.query), {
        method: ctx.method,
        headers,
        body: ctx.method === 'GET' || ctx.method === 'HEAD' ? undefined : (requestBody as BodyInit | null) ?? undefined,
        redirect: 'manual',
        cache: 'no-store',
        signal: controller.signal,
      })
    } finally {
      clearTimeout(timer)
    }
  }

  let upstream: Response
  let refreshAttempted = false
  try {
    if (session && isSessionExpired(session, now()) && !isTokenRoute(ctx.path)) {
      refreshAttempted = true
      const refreshed = await refreshSession(ctx, session)
      if (refreshed) {
        session = refreshed
        headers.authorization = `Bearer ${session.accessToken}`
      }
    }

    upstream = await send()

    // One silent refresh, then retry the original request. The client never sees the 401 chain.
    if (upstream.status === 401 && session?.refreshToken && !isTokenRoute(ctx.path) && !refreshAttempted) {
      const refreshed = await refreshSession(ctx, session)
      if (refreshed) {
        session = refreshed
        headers.authorization = `Bearer ${session.accessToken}`
        upstream = await send()
      }
      // A failed refresh keeps the cookie. Another instance or tab may have just
      // rotated it; clearing here would turn a lost race into a logout.
    }
  } catch (error) {
    const name = (error as { name?: string } | null)?.name
    if (name === 'AbortError' || name === 'TimeoutError') return fail(504, 'Backend timeout')
    return fail(502, 'Backend unavailable')
  }

  const responseHeaders = filterResponseHeaders(upstream.headers)
  const isRedirect = upstream.status >= 300 && upstream.status < 400
  const location = upstream.headers.get('location')
  if (isRedirect && location) {
    const safe = sanitizeLocation(location, ctx.backendOrigin, ctx.publicOrigin)
    if (safe) {
      responseHeaders.location = safe
    }
    else delete responseHeaders.location
  } else {
    // A Location header on a non-redirect response is noise and could leak internals.
    delete responseHeaders.location
  }

  let sessionAfter: SessionEffect = { action: 'keep' }
  if (session && session !== ctx.session) sessionAfter = { action: 'set', value: session }
  else if (!session && ctx.session) sessionAfter = { action: 'clear' }

  const setCookies = filterOAuthSetCookieHeaders(ctx.path, getSetCookieValues(upstream.headers))
  const cookieEffect = setCookies.length ? { setCookies } : {}
  const isHead = ctx.method.toUpperCase() === 'HEAD'

  if (isRedirect && isOAuthCallbackRoute(ctx.path)) {
    // Go's 302 HTML body can contain the original token-bearing Location too.
    // Never send that body, even on errors or rejected private redirects.
    await upstream.body?.cancel()
    if (responseHeaders.location) {
      const captured = captureOAuthRedirectSession(String(responseHeaders.location), ctx.publicOrigin, now(), !isHead)
      if (captured.error) return { ...fail(502, captured.error), ...(isHead ? { body: null } : {}), ...cookieEffect }
      responseHeaders.location = captured.location
      if (captured.session) sessionAfter = { action: 'set', value: captured.session }
    }
    return { status: upstream.status, headers: responseHeaders, body: null, session: sessionAfter, ...cookieEffect }
  }

  // HEAD has no representation to parse/rewrite and must not create a login
  // or clear a session by accidentally taking a JSON/logout branch.
  if (isHead || upstream.status === 204 || upstream.status === 304) {
    await upstream.body?.cancel()
    return { status: upstream.status, headers: responseHeaders, body: null, session: sessionAfter, ...cookieEffect }
  }

  if (isTokenRoute(ctx.path) || isOAuthCompletionRoute(ctx.path)) {
    if (isLogoutRoute(ctx.path)) {
      await upstream.body?.cancel()
      return {
        status: upstream.status,
        headers: { ...responseHeaders, ...JSON_HEADERS },
        body: envelope(upstream.ok ? 0 : upstream.status, upstream.ok ? 'success' : 'Logout failed'),
        session: { action: machineTokens ? 'keep' : 'clear' },
      }
    }
    let payload: unknown
    try {
      payload = await upstream.json()
    } catch {
      // Invalid auth responses may embed tokens or private backend errors.
      return { ...fail(502, 'Invalid authentication response'), ...cookieEffect }
    }
    if (machineTokens) {
      // No cookies, no session capture, no CORS relaxation. Bearer callers see
      // the original Go JSON, including its 2FA challenges and auth failures.
      return {
        status: upstream.status,
        headers: { ...responseHeaders, ...JSON_HEADERS },
        body: Buffer.from(JSON.stringify(payload), 'utf8'),
        session: { action: 'keep' },
      }
    }
    const { payload: clean, tokens } = applySessionContract(payload)
    const successful = upstream.ok && payload && typeof payload === 'object' &&
      (payload as Record<string, unknown>).code === 0
    if (tokens.accessToken && successful) {
      const created = createSession({
        accessToken: tokens.accessToken,
        refreshToken: tokens.refreshToken ?? '',
        expiresIn: tokens.expiresIn ?? 0,
        uid: tokens.uid ?? 0,
        now: now(),
      })
      return {
        status: upstream.status,
        headers: { ...responseHeaders, ...JSON_HEADERS },
        body: Buffer.from(JSON.stringify(withCsrfToken(clean, created.csrf)), 'utf8'),
        session: { action: 'set', value: created },
        ...cookieEffect,
      }
    }
    return {
      status: upstream.status,
      headers: { ...responseHeaders, ...JSON_HEADERS },
      body: Buffer.from(JSON.stringify(clean), 'utf8'),
      session: sessionAfter,
      ...cookieEffect,
    }
  }

  return {
    status: upstream.status,
    headers: responseHeaders,
    body: upstream.body ?? null,
    session: sessionAfter,
    ...(setCookies.length ? { setCookies } : {}),
  }
}

function getSetCookieValues(headers: Headers): string[] {
  const extended = headers as Headers & { getSetCookie?: () => string[] }
  if (typeof extended.getSetCookie === 'function') return extended.getSetCookie()
  const combined = headers.get('set-cookie')
  return combined ? combined.split(/,(?=\s*[^;,\s]+=)/) : []
}

function captureOAuthRedirectSession(
  location: string,
  publicOrigin: string,
  now: number,
  establishSession: boolean
): { location: string; session?: ServerSession; error?: string } {
  let target: URL
  let expectedOrigin: string
  try {
    expectedOrigin = new URL(publicOrigin).origin
    target = new URL(location, expectedOrigin)
  } catch {
    return { location, error: 'OAuth callback redirect was invalid' }
  }

  const fragment = new URLSearchParams(target.hash.slice(1))
  const tokenKeys = ['access_token', 'refresh_token', 'expires_in', 'token_type', 'id_token']
  if (target.origin !== expectedOrigin || target.username || target.password) {
    return { location: '', error: 'OAuth callback must return to this site' }
  }
  const accessToken = fragment.get('access_token')?.trim() || ''
  const refreshToken = fragment.get('refresh_token')?.trim() || ''
  const expiresInRaw = Number.parseInt(fragment.get('expires_in') || '', 10)
  for (const key of tokenKeys) {
    fragment.delete(key)
    target.searchParams.delete(key)
  }
  fragment.delete('session_managed')

  if (accessToken && establishSession) {
    if (accessToken.length > 8192 || refreshToken.length > 8192) {
      return { location, error: 'OAuth callback must return to this site' }
    }
    fragment.set('session_managed', '1')
    const expiresIn = Number.isFinite(expiresInRaw) && expiresInRaw > 0 ? expiresInRaw : 0
    target.hash = fragment.toString()
    return {
      location: `${target.pathname}${target.search}${target.hash}`,
      session: createSession({
        accessToken,
        refreshToken,
        expiresIn,
        uid: 0,
        now,
      }),
    }
  }

  target.hash = fragment.toString()
  return { location: `${target.pathname}${target.search}${target.hash}` }
}

/**
 * The app keeps the CSRF token in a runtime field; a login or refresh reply is
 * the first place it can learn one. The value is public, so returning it here
 * does not widen the token exposure that `applySessionContract` removed.
 */
function withCsrfToken(payload: unknown, csrf: string): unknown {
  if (!payload || typeof payload !== 'object') return payload
  const root = payload as Record<string, unknown>
  const data = root.data
  if (!data || typeof data !== 'object') return payload
  return { ...root, data: { ...(data as Record<string, unknown>), csrf_token: csrf } }
}

/** Convenience for tests and for the session endpoint. */
export function newCsrfToken(): string {
  return randomToken(24)
}
