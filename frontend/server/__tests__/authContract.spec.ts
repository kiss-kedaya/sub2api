import { describe, expect, it, vi } from 'vitest'
import { handleBffRequest, type BffContext } from '../utils/backendProxy'
import { createSession } from '../utils/session'

const PUBLIC = 'https://site.example'
const auth = { code: 0, data: { access_token: 'fixture-access', refresh_token: 'fixture-refresh', expires_in: 3600, token_type: 'Bearer', user: { id: 7, balance: 12 } } }
const json = (payload: unknown, status = 200) => new Response(JSON.stringify(payload), { status, headers: { 'content-type': 'application/json', 'access-control-allow-origin': '*', 'set-cookie': 'unexpected=secret' } })
const bodyOf = (result: Awaited<ReturnType<typeof handleBffRequest>>) => JSON.parse(Buffer.from(result.body as Buffer).toString())
function context(overrides: Partial<BffContext> = {}): BffContext {
  return { method: 'POST', path: '/api/v1/auth/login', query: '', headers: { host: 'site.example', 'content-type': 'application/json' }, body: Buffer.from(JSON.stringify({ email: 'fixture@example.com', password: 'fixture-password' })), session: null, sessionPassword: 'fixture-session-password', backendOrigin: 'http://127.0.0.1:8080', publicOrigin: PUBLIC, protocol: 'https', clientIp: '203.0.113.5', ...overrides }
}

describe('machine Bearer and browser cookie contracts', () => {
  it.each(['/api/v1/auth/login', '/api/v1/auth/login/2fa', '/api/v1/auth/register', '/api/v1/auth/refresh'])('retains the Go JSON contract on machine %s without creating cookies', async (path) => {
    const fetchImpl = vi.fn(async () => json(auth)) as unknown as typeof fetch
    const result = await handleBffRequest(context({ path, fetchImpl }))
    expect(result.status).toBe(200)
    expect(bodyOf(result)).toEqual(auth)
    expect(result.session.action).toBe('keep')
    expect(result.setCookies).toBeUndefined()
    expect(result.headers['set-cookie']).toBeUndefined()
    expect(result.headers['access-control-allow-origin']).toBeUndefined()
    expect(result.headers['cache-control']).toContain('no-store')
    expect(fetchImpl).toHaveBeenCalledOnce()
  })

  it('does not depend on the monitoring program user agent or treat Node fetch mode alone as a browser', async () => {
    const result = await handleBffRequest(context({ headers: { host: 'site.example', 'Content-Type': 'application/json; charset=utf-8', 'user-agent': 'arbitrary-client', 'sec-fetch-mode': 'cors' }, fetchImpl: (async () => json(auth)) as typeof fetch }))
    expect(bodyOf(result)).toEqual(auth)
    expect(result.session.action).toBe('keep')
  })

  it('allows a machine login without a cookie secret but still delegates credentials to Go', async () => {
    const fetchImpl = vi.fn(async () => json(auth)) as unknown as typeof fetch
    const result = await handleBffRequest(context({ sessionPassword: '', fetchImpl }))
    expect(bodyOf(result)).toEqual(auth)
    expect(result.session.action).toBe('keep')
    expect(fetchImpl).toHaveBeenCalledOnce()
  })

  it('keeps machine login, balance query and rotating refresh on the same identity', async () => {
    const calls: Array<{ url: string; init?: RequestInit }> = []
    const fetchImpl = vi.fn(async (url: string | URL | Request, init?: RequestInit) => {
      calls.push({ url: String(url), init })
      if (String(url).endsWith('/auth/me')) {
        expect(new Headers(init?.headers).get('authorization')).toBe('Bearer fixture-access')
        return json({ code: 0, data: { id: 7, balance: 12 } })
      }
      if (String(url).endsWith('/auth/refresh')) {
        expect(JSON.parse(String(init?.body))).toEqual({ refresh_token: 'fixture-refresh' })
        return json({ code: 0, data: { access_token: 'fixture-access-2', refresh_token: 'fixture-refresh-2' } })
      }
      return json(auth)
    }) as unknown as typeof fetch
    const login = await handleBffRequest(context({ fetchImpl }))
    const token = bodyOf(login).data.access_token
    const me = await handleBffRequest(context({ method: 'GET', path: '/api/v1/auth/me', body: null, headers: { host: 'site.example', authorization: `Bearer ${token}` }, fetchImpl }))
    expect(await new Response(me.body as ReadableStream<Uint8Array>).json()).toEqual({ code: 0, data: { id: 7, balance: 12 } })
    const refreshed = await handleBffRequest(context({ path: '/api/v1/auth/refresh', body: Buffer.from(JSON.stringify({ refresh_token: bodyOf(login).data.refresh_token })), fetchImpl }))
    expect(bodyOf(refreshed).data.refresh_token).toBe('fixture-refresh-2')
    expect(refreshed.session.action).toBe('keep')
    expect(calls).toHaveLength(3)
  })

  it.each([401, 403, 429])('preserves Go rejection %s and never invents authentication', async (status) => {
    const rejected = { code: status, message: 'fixture-rejected' }
    const result = await handleBffRequest(context({ headers: { host: 'site.example', 'content-type': 'application/json', 'x-sub2api-auth-mode': 'bearer' }, fetchImpl: (async () => json(rejected, status)) as typeof fetch }))
    expect(result.status).toBe(status)
    expect(bodyOf(result)).toEqual(rejected)
    expect(result.session.action).toBe('keep')
  })

  it('keeps the second-factor challenge unmodified and without tokens', async () => {
    const challenge = { code: 0, data: { requires_2fa: true, temp_token: 'fixture-challenge' } }
    const result = await handleBffRequest(context({ fetchImpl: (async () => json(challenge)) as typeof fetch }))
    expect(bodyOf(result)).toEqual(challenge)
    expect(result.session.action).toBe('keep')
  })

  it.each([
    { origin: PUBLIC },
    { referer: `${PUBLIC}/login` },
    { cookie: 'unrelated=value', origin: PUBLIC },
    { 'sec-fetch-site': 'same-origin', origin: PUBLIC },
    { 'sec-fetch-dest': 'empty', origin: PUBLIC },
    { 'x-sub2api-auth-mode': 'cookie', origin: PUBLIC },
  ])('never exposes browser tokens even with an explicit bearer hint: %j', async (markers) => {
    const result = await handleBffRequest(context({ headers: { host: 'site.example', 'content-type': 'application/json', 'x-sub2api-auth-mode': 'bearer', ...markers }, fetchImpl: (async () => json(auth)) as typeof fetch }))
    expect(result.status).toBe(200)
    expect(bodyOf(result).data).toMatchObject({ access_token: '', refresh_token: '', session_managed: true })
    expect(result.session.action).toBe('set')
    expect(JSON.stringify(bodyOf(result))).not.toContain('fixture-access')
  })

  it.each([
    { origin: 'https://attacker.example' },
    { origin: 'null' },
    { referer: 'https://attacker.example/login' },
    { 'sec-fetch-site': 'cross-site' },
    { 'sec-fetch-mode': 'navigate' },
    { cookie: 'sub2_session=invalid' },
    { 'x-sub2api-auth-mode': 'cookie' },
    { 'content-type': 'text/plain' },
  ])('rejects unsafe anonymous browser login before Go: %j', async (markers) => {
    const fetchImpl = vi.fn() as unknown as typeof fetch
    const result = await handleBffRequest(context({ headers: { host: 'site.example', 'content-type': 'application/json', 'x-sub2api-auth-mode': 'bearer', ...markers }, fetchImpl }))
    expect(result.status).toBe(403)
    expect(fetchImpl).not.toHaveBeenCalled()
    expect(result.session.action).toBe('keep')
  })

  it('does not let the bearer hint bypass cookie refresh CSRF or replace the session token', async () => {
    const session = createSession({ accessToken: 'cookie-access', refreshToken: 'cookie-refresh', expiresIn: 3600, uid: 9 })
    const fetchImpl = vi.fn(async (_url: unknown, init?: RequestInit) => {
      expect(new Headers(init?.headers).get('authorization')).toBe('Bearer cookie-access')
      expect(new Headers(init?.headers).get('x-sub2api-auth-mode')).toBeNull()
      expect(JSON.parse(String(init?.body))).toEqual({ refresh_token: 'cookie-refresh' })
      return json(auth)
    }) as unknown as typeof fetch
    const input = context({ path: '/api/v1/auth/refresh', session, headers: { host: 'site.example', origin: PUBLIC, 'x-sub2api-auth-mode': 'bearer' }, body: Buffer.from('{"refresh_token":"attacker"}'), fetchImpl })
    const blocked = await handleBffRequest(input)
    expect(blocked.status).toBe(403)
    expect(fetchImpl).not.toHaveBeenCalled()
    const allowed = await handleBffRequest({ ...input, headers: { ...input.headers, 'x-sub2api-csrf': session.csrf } })
    expect(allowed.status).toBe(200)
    expect(bodyOf(allowed).data.access_token).toBe('')
    expect(allowed.session.action).toBe('set')
    expect(fetchImpl).toHaveBeenCalledOnce()
  })

  it('does not reinterpret OAuth completion as a machine token endpoint', async () => {
    const result = await handleBffRequest(context({ path: '/api/v1/auth/oauth/pending/exchange', headers: { host: 'site.example', origin: PUBLIC, 'content-type': 'application/json', 'x-sub2api-auth-mode': 'bearer' }, fetchImpl: (async () => json(auth)) as typeof fetch }))
    expect(bodyOf(result).data.access_token).toBe('')
    expect(result.session.action).toBe('set')
  })
})
