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
  filterRequestHeaders,
  filterResponseHeaders,
  isLogoutRoute,
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
  headers: Record<string, string>
  body: Buffer | ReadableStream<Uint8Array> | null
  session: SessionEffect
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
  const raw = headers[name] ?? headers[name.toLowerCase()]
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

  // Without a session secret the BFF cannot hold a login, but public endpoints
  // must still render: proxy them anonymously. Only the endpoints that would
  // hand out tokens fail closed.
  const sessionConfigured = Boolean(ctx.sessionPassword)
  if (!sessionConfigured && isTokenRoute(ctx.path) && !isLogoutRoute(ctx.path)) {
    return fail(503, 'SSR session is not configured')
  }

  let session = ctx.session
  const usesCookieSession = !!session
  const origin = headerValue(ctx.headers, 'origin')
  const referer = headerValue(ctx.headers, 'referer')
  const host = headerValue(ctx.headers, 'host')

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
  if (!csrf.ok) return fail(403, 'Request blocked by CSRF policy')

  const headers = filterRequestHeaders(ctx.headers)
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
  if (session && isTokenRoute(ctx.path) && ctx.path.endsWith('/auth/refresh')) {
    // Refresh uses the session's rotating token, never a client-supplied one.
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
    if (safe) responseHeaders.location = safe
    else delete responseHeaders.location
  } else {
    // A Location header on a non-redirect response is noise and could leak internals.
    delete responseHeaders.location
  }

  let sessionAfter: SessionEffect = { action: 'keep' }
  if (session && session !== ctx.session) sessionAfter = { action: 'set', value: session }
  else if (!session && ctx.session) sessionAfter = { action: 'clear' }

  if (isTokenRoute(ctx.path)) {
    const text = await upstream.text()
    if (isLogoutRoute(ctx.path)) {
      return {
        status: upstream.status,
        headers: { ...responseHeaders, ...JSON_HEADERS },
        body: Buffer.from(text || JSON.stringify({ code: 0, message: 'success', data: { message: 'ok' } }), 'utf8'),
        session: { action: 'clear' },
      }
    }
    let payload: unknown
    try {
      payload = JSON.parse(text)
    } catch {
      return { status: upstream.status, headers: responseHeaders, body: Buffer.from(text, 'utf8'), session: sessionAfter }
    }
    const { payload: clean, tokens } = applySessionContract(payload)
    if (tokens.accessToken && upstream.ok) {
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
      }
    }
    return {
      status: upstream.status,
      headers: { ...responseHeaders, ...JSON_HEADERS },
      body: Buffer.from(JSON.stringify(clean), 'utf8'),
      session: sessionAfter,
    }
  }

  return {
    status: upstream.status,
    headers: responseHeaders,
    body: upstream.body ?? null,
    session: sessionAfter,
  }
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
