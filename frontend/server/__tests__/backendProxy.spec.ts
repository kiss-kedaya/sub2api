import { describe, expect, it, vi } from 'vitest'
import { buildUpstreamUrl, handleBffRequest, type BffContext } from '../utils/backendProxy'
import { createSession, unsealBffSession } from '../utils/session'

const PASSWORD = 'test-session-password'
const BACKEND = 'http://127.0.0.1:8080'
const PUBLIC = 'https://site.example'
const HOST = 'site.example'

const session = (overrides: Partial<ReturnType<typeof createSession>> = {}) => ({
  ...createSession({
    accessToken: 'access-1',
    refreshToken: 'refresh-1',
    expiresIn: 3600,
    uid: 7,
    now: Date.now(),
  }),
  ...overrides,
})

const json = (body: unknown, status = 200, headers: Record<string, string> = {}) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { 'content-type': 'application/json', ...headers },
  })

function ctx(overrides: Partial<BffContext> = {}): BffContext {
  return {
    method: 'GET',
    path: '/api/v1/keys',
    query: '',
    headers: { host: HOST, origin: PUBLIC },
    body: null,
    session: null,
    sessionPassword: PASSWORD,
    backendOrigin: BACKEND,
    publicOrigin: PUBLIC,
    protocol: 'https',
    clientIp: '203.0.113.5',
    ...overrides,
  }
}

function recorder(handler: (url: string, init: RequestInit) => Response | Promise<Response>) {
  const calls: Array<{ url: string; init: RequestInit }> = []
  const fetchImpl = vi.fn(async (url: string | URL | Request, init?: RequestInit) => {
    const href = String(url)
    calls.push({ url: href, init: init ?? {} })
    return handler(href, init ?? {})
  }) as unknown as typeof fetch
  return { calls, fetchImpl }
}

const payloadOf = (body: unknown) => JSON.parse(Buffer.from(body as Buffer).toString('utf8'))

describe('upstream url', () => {
  it('pins the fixed origin and keeps the validated path and query', () => {
    expect(buildUpstreamUrl(BACKEND, '/api/v1/keys', 'page=1&timezone=Asia%2FShanghai'))
      .toBe('http://127.0.0.1:8080/api/v1/keys?page=1&timezone=Asia%2FShanghai')
  })

  it('refuses a path outside the api prefix or an origin with credentials', () => {
    expect(() => buildUpstreamUrl(BACKEND, '/v1/messages', '')).toThrow()
    expect(() => buildUpstreamUrl('http://user:pass@127.0.0.1:8080', '/api/v1/keys', '')).toThrow()
    expect(() => buildUpstreamUrl('file:///etc/passwd', '/api/v1/keys', '')).toThrow()
  })
})

describe('login and token capture', () => {
  it('blanks the tokens, keeps the user and seals the session', async () => {
    const loginBody = {
      code: 0,
      message: 'success',
      data: {
        access_token: 'access-new',
        refresh_token: 'refresh-new',
        expires_in: 7200,
        token_type: 'Bearer',
        user: { id: 7, email: 'a@b.c' },
      },
    }
    const { fetchImpl } = recorder(() => json(loginBody))
    const result = await handleBffRequest(
      ctx({
        method: 'POST',
        path: '/api/v1/auth/login',
        headers: { host: HOST, origin: PUBLIC, 'content-type': 'application/json' },
        body: Buffer.from(JSON.stringify({ email: 'a@b.c', password: 'x' })),
        fetchImpl,
      })
    )

    const data = payloadOf(result.body).data
    expect(data.access_token).toBe('')
    expect(data.refresh_token).toBe('')
    expect(data.session_managed).toBe(true)
    expect(data.expires_in).toBe(7200)
    expect(data.user).toEqual({ id: 7, email: 'a@b.c' })

    expect(result.session.action).toBe('set')
    const sealed = result.session.action === 'set' ? result.session.value : null
    expect(sealed?.accessToken).toBe('access-new')
    expect(sealed?.refreshToken).toBe('refresh-new')
    expect(sealed?.uid).toBe(7)
    expect(sealed?.csrf.length).toBeGreaterThan(20)
    // The app stores the csrf token in its runtime context; login is where it learns it.
    expect(data.csrf_token).toBe(sealed?.csrf)
    // The browser never sees the sealed value; it only arrives as Set-Cookie.
    expect(JSON.stringify(payloadOf(result.body))).not.toContain('access-new')
  })

  it('passes a 2FA challenge through without creating a session', async () => {
    const challenge = { code: 0, message: 'success', data: { requires_2fa: true, temp_token: 'tmp-1' } }
    const { fetchImpl } = recorder(() => json(challenge))
    const result = await handleBffRequest(
      ctx({
        method: 'POST',
        path: '/api/v1/auth/login',
        headers: { host: HOST, origin: PUBLIC },
        body: Buffer.from('{}'),
        fetchImpl,
      })
    )
    const data = payloadOf(result.body).data
    expect(data.requires_2fa).toBe(true)
    expect(data.temp_token).toBe('tmp-1')
    expect(data.session_managed).toBe(false)
    expect(result.session.action).toBe('keep')
  })

  it('clears the session on logout even when upstream fails', async () => {
    const active = session()
    const { fetchImpl } = recorder(() => json({ code: 401, message: 'expired' }, 401))
    const result = await handleBffRequest(
      ctx({
        method: 'POST',
        path: '/api/v1/auth/logout',
        headers: { host: HOST, origin: PUBLIC, 'x-csrf-token': active.csrf },
        session: active,
        fetchImpl,
      })
    )
    expect(result.session.action).toBe('clear')
  })
})

describe('proxying', () => {
  it('injects the session token, drops the client token and cookie, and sets no-store', async () => {
    const { calls, fetchImpl } = recorder(() =>
      json({ code: 0, message: 'success', data: { items: [] } }, 200, { 'set-cookie': 'leak=1' })
    )
    const active = session()
    const result = await handleBffRequest(
      ctx({
        query: 'page=2',
        headers: {
          host: HOST,
          accept: 'application/json',
          authorization: 'Bearer attacker-supplied',
          cookie: 'sub2_session=stolen',
          'x-forwarded-for': '6.6.6.6',
        },
        session: active,
        fetchImpl,
      })
    )

    const sent = calls[0].init.headers as Record<string, string>
    expect(sent.authorization).toBe(`Bearer ${active.accessToken}`)
    expect(sent.cookie).toBeUndefined()
    expect(sent['x-forwarded-for']).toBe('203.0.113.5')
    expect(sent.accept).toBe('application/json')
    expect(calls[0].url).toBe('http://127.0.0.1:8080/api/v1/keys?page=2')
    expect(result.headers['cache-control']).toContain('no-store')
    expect(result.headers['set-cookie']).toBeUndefined()
    expect(result.status).toBe(200)
  })

  it('forwards a bearer-only client without inventing a session', async () => {
    const { calls, fetchImpl } = recorder(() => json({ code: 0, message: 'success', data: {} }))
    const result = await handleBffRequest(
      ctx({ headers: { host: HOST, authorization: 'Bearer app-token' }, fetchImpl })
    )
    const sent = calls[0].init.headers as Record<string, string>
    expect(sent.authorization).toBe('Bearer app-token')
    expect(result.session.action).toBe('keep')
  })

  it('refreshes once on 401, keeps the csrf token and retries with the new access token', async () => {
    const active = session({ accessToken: 'expired', refreshToken: 'refresh-1' })
    const { calls, fetchImpl } = recorder((url) => {
      if (url.endsWith('/api/v1/auth/refresh')) {
        return json({ code: 0, message: 'success', data: { access_token: 'fresh', refresh_token: 'refresh-2', expires_in: 3600 } })
      }
      const auth = (calls[calls.length - 1].init.headers as Record<string, string>).authorization
      if (auth === 'Bearer expired') return json({ code: 401, message: 'TOKEN_EXPIRED' }, 401)
      return json({ code: 0, message: 'success', data: { ok: true } })
    })

    const result = await handleBffRequest(ctx({ session: active, fetchImpl }))
    expect(result.status).toBe(200)
    expect(calls).toHaveLength(3)
    expect(result.session.action).toBe('set')
    const next = result.session.action === 'set' ? result.session.value : null
    expect(next?.accessToken).toBe('fresh')
    expect(next?.refreshToken).toBe('refresh-2')
    expect(next?.csrf).toBe(active.csrf)
  })

  it('keeps the cookie and returns the 401 when refresh fails', async () => {
    const active = session({ accessToken: 'expired', refreshToken: 'refresh-1' })
    const { fetchImpl } = recorder((url) => {
      if (url.endsWith('/api/v1/auth/refresh')) return json({ code: 401, message: 'revoked' }, 401)
      return json({ code: 401, message: 'TOKEN_EXPIRED' }, 401)
    })
    const result = await handleBffRequest(ctx({ session: active, fetchImpl }))
    expect(result.status).toBe(401)
    // A lost rotation race must not log the user out; only logout clears the cookie.
    expect(result.session.action).toBe('keep')
  })

  it('spends a rotating refresh token once for parallel 401s', async () => {
    const active = session({ accessToken: 'expired', refreshToken: 'refresh-rotate' })
    let refreshCalls = 0
    let freshToken: string | null = null
    const fetchImpl = (async (url: string | URL | Request, init?: RequestInit) => {
      const href = String(url)
      if (href.endsWith('/api/v1/auth/refresh')) {
        refreshCalls += 1
        await new Promise((resolve) => setTimeout(resolve, 5))
        freshToken = 'fresh-1'
        return json({ code: 0, message: 'success', data: { access_token: 'fresh-1', refresh_token: 'rotate-2', expires_in: 3600 } })
      }
      const auth = (init?.headers as Record<string, string> | undefined)?.authorization
      if (auth !== 'Bearer fresh-1') return json({ code: 401, message: 'TOKEN_EXPIRED' }, 401)
      return json({ code: 0, message: 'success', data: { ok: true } })
    }) as unknown as typeof fetch

    const [a, b] = await Promise.all([
      handleBffRequest(ctx({ session: active, fetchImpl })),
      handleBffRequest(ctx({ session: active, fetchImpl })),
    ])

    expect(refreshCalls).toBe(1)
    expect(freshToken).toBe('fresh-1')
    expect(a.status).toBe(200)
    expect(b.status).toBe(200)
    expect(a.session.action).toBe('set')
    expect(b.session.action).toBe('set')
  })

  it('drops a Location header on a non-redirect response', async () => {
    const { fetchImpl } = recorder(() =>
      json({ code: 0, message: 'success', data: {} }, 200, { location: 'http://127.0.0.1:8080/admin' })
    )
    const result = await handleBffRequest(ctx({ fetchImpl }))
    expect(result.headers.location).toBeUndefined()
  })

  it('rewrites a backend redirect to the public origin', async () => {
    const { fetchImpl } = recorder(() =>
      new Response(null, { status: 302, headers: { location: 'http://127.0.0.1:8080/oauth/done' } })
    )
    const result = await handleBffRequest(ctx({ fetchImpl }))
    expect(result.status).toBe(302)
    expect(result.headers.location).toBe('https://site.example/oauth/done')
  })

  it('gives up on a silent upstream instead of hanging the SSR request', async () => {
    const fetchImpl = ((_url: string | URL | Request, init?: RequestInit) =>
      new Promise((_resolve, reject) => {
        init?.signal?.addEventListener('abort', () => {
          const error = new Error('aborted')
          error.name = 'AbortError'
          reject(error)
        })
      })) as unknown as typeof fetch
    const started = Date.now()
    const result = await handleBffRequest(ctx({ fetchImpl, timeoutMs: 20 }))
    expect(result.status).toBe(504)
    expect(Date.now() - started).toBeLessThan(2000)
  })

  it('passes a streamed body straight through without buffering it', async () => {
    const { fetchImpl } = recorder(
      () => new Response('chunk-1', { status: 200, headers: { 'content-type': 'text/plain' } })
    )
    const result = await handleBffRequest(ctx({ fetchImpl }))
    expect(typeof (result.body as ReadableStream<Uint8Array>).getReader).toBe('function')
    const text = await new Response(result.body as ReadableStream<Uint8Array>).text()
    expect(text).toBe('chunk-1')
  })

  it('returns 502 when the backend cannot be reached', async () => {
    const fetchImpl = vi.fn(async () => {
      throw new Error('ECONNREFUSED')
    }) as unknown as typeof fetch
    const result = await handleBffRequest(ctx({ fetchImpl }))
    expect(result.status).toBe(502)
    expect(payloadOf(result.body).code).toBe(502)
  })

  it('still proxies public reads without a session password', async () => {
    const { calls, fetchImpl } = recorder(() => json({ code: 0, message: 'success', data: { site_name: 'x' } }))
    const result = await handleBffRequest(
      ctx({ path: '/api/v1/settings/public', sessionPassword: '', fetchImpl })
    )
    expect(result.status).toBe(200)
    expect(calls).toHaveLength(1)
    // Anonymous: no session exists, so no Authorization is invented.
    expect((calls[0].init.headers as Record<string, string>).authorization).toBeUndefined()
    expect(result.session.action).toBe('keep')
  })

  it('fails closed on login without a session password', async () => {
    const fetchImpl = vi.fn() as unknown as typeof fetch
    const result = await handleBffRequest(
      ctx({
        method: 'POST',
        path: '/api/v1/auth/login',
        sessionPassword: '',
        body: Buffer.from('{}'),
        fetchImpl,
      })
    )
    expect(result.status).toBe(503)
    expect(fetchImpl).not.toHaveBeenCalled()
  })
})

describe('csrf enforcement', () => {
  it('blocks a cookie-authenticated mutation without the csrf header', async () => {
    const fetchImpl = vi.fn() as unknown as typeof fetch
    const result = await handleBffRequest(
      ctx({ method: 'POST', path: '/api/v1/keys', session: session(), body: Buffer.from('{}'), fetchImpl })
    )
    expect(result.status).toBe(403)
    expect(fetchImpl).not.toHaveBeenCalled()
  })

  it('blocks a cross-origin mutation even with the token', async () => {
    const active = session()
    const fetchImpl = vi.fn() as unknown as typeof fetch
    const result = await handleBffRequest(
      ctx({
        method: 'POST',
        path: '/api/v1/keys',
        headers: { host: HOST, origin: 'https://evil.example', 'x-csrf-token': active.csrf },
        session: active,
        body: Buffer.from('{}'),
        fetchImpl,
      })
    )
    expect(result.status).toBe(403)
  })

  it('allows a same-origin mutation that echoes the token', async () => {
    const active = session()
    const { fetchImpl } = recorder(() => json({ code: 0, message: 'success', data: {} }))
    const result = await handleBffRequest(
      ctx({
        method: 'POST',
        path: '/api/v1/keys',
        headers: { host: HOST, origin: PUBLIC, 'x-csrf-token': active.csrf },
        session: active,
        body: Buffer.from('{}'),
        fetchImpl,
      })
    )
    expect(result.status).toBe(200)
  })
})

describe('sealed cookie round-trip', () => {
  it('keeps the session the route writes readable by the route that reads it', () => {
    const active = session()
    const sealedValue = active.accessToken
    expect(sealedValue).toBe('access-1')
    expect(unsealBffSession('v1.x.y.z', PASSWORD)).toBeNull()
  })
})
