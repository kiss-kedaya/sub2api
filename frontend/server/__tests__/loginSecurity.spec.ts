import { describe, expect, it } from 'vitest'
import { handleBffRequest, type BffContext } from '../utils/backendProxy'
import { createSession } from '../utils/session'
import { csrfCookieOptions, sessionCookieOptions } from '../utils/sessionCookie'

const PASSWORD = 'login-security-password'
const BACKEND = 'http://127.0.0.1:8080'
const PUBLIC = 'https://site.example'
const HOST = 'site.example'
const ACCESS = 'eyJhbGciOiJIUzI1NiJ9.access.signature'
const REFRESH = 'refresh-token-value-123'

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { 'content-type': 'application/json' } })

function ctx(overrides: Partial<BffContext> = {}): BffContext {
  return {
    method: 'POST',
    path: '/api/v1/auth/login',
    query: '',
    headers: { host: HOST, origin: PUBLIC, 'content-type': 'application/json' },
    body: Buffer.from(JSON.stringify({ email: 'a@b.c', password: 'hunter2' })),
    session: null,
    sessionPassword: PASSWORD,
    backendOrigin: BACKEND,
    publicOrigin: PUBLIC,
    protocol: 'https',
    clientIp: '203.0.113.5',
    ...overrides,
  }
}

const loginOk = () =>
  json({
    code: 0,
    message: 'success',
    data: {
      access_token: ACCESS,
      refresh_token: REFRESH,
      expires_in: 7200,
      token_type: 'Bearer',
      user: { id: 11, email: 'a@b.c', role: 'user' },
    },
  })

const bodyText = (body: unknown) => Buffer.from(body as Buffer).toString('utf8')

describe('login response never exposes bearer tokens', () => {
  it('strips both tokens from the payload and flags the managed session', async () => {
    const fetchImpl = (async () => loginOk()) as unknown as typeof fetch
    const result = await handleBffRequest(ctx({ fetchImpl }))
    const raw = bodyText(result.body)

    expect(raw).not.toContain(ACCESS)
    expect(raw).not.toContain(REFRESH)
    const data = JSON.parse(raw).data
    expect(data.access_token).toBe('')
    expect(data.refresh_token).toBe('')
    expect(data.session_managed).toBe(true)
    expect(data.user).toEqual({ id: 11, email: 'a@b.c', role: 'user' })
    expect(result.session.action).toBe('set')
  })

  it('is the only place the csrf token is handed to the app', async () => {
    const fetchImpl = (async () => loginOk()) as unknown as typeof fetch
    const result = await handleBffRequest(ctx({ fetchImpl }))
    const data = JSON.parse(bodyText(result.body)).data
    const sealed = result.session.action === 'set' ? result.session.value : null
    expect(data.csrf_token).toBe(sealed?.csrf)
    expect(data.csrf_token).not.toBe(ACCESS)
    expect(data.csrf_token).not.toBe(REFRESH)
  })

  it('does not echo the password or the request body', async () => {
    const fetchImpl = (async () => loginOk()) as unknown as typeof fetch
    const result = await handleBffRequest(ctx({ fetchImpl }))
    expect(bodyText(result.body)).not.toContain('hunter2')
  })
})

describe('failed and replayed logins', () => {
  it('propagates a rejected login without creating a session', async () => {
    const fetchImpl = (async () =>
      json({ code: 401, message: 'INVALID_CREDENTIALS', reason: '', metadata: null }, 401)) as unknown as typeof fetch
    const result = await handleBffRequest(ctx({ fetchImpl }))
    expect(result.status).toBe(401)
    expect(result.session.action).toBe('keep')
    expect(bodyText(result.body)).not.toContain('csrf_token')
    expect(bodyText(result.body)).not.toContain('session_managed')
  })

  it('issues a different csrf token for every login (no fixation)', async () => {
    const fetchImpl = (async () => loginOk()) as unknown as typeof fetch
    const first = await handleBffRequest(ctx({ fetchImpl }))
    const second = await handleBffRequest(ctx({ fetchImpl }))
    const a = first.session.action === 'set' ? first.session.value : null
    const b = second.session.action === 'set' ? second.session.value : null
    expect(a?.csrf).toBeTruthy()
    expect(b?.csrf).toBeTruthy()
    expect(a?.csrf).not.toBe(b?.csrf)
  })

  it('rejects a csrf token carried over from a previous session', async () => {
    const oldSession = createSession({ accessToken: 'old', refreshToken: 'old-r', expiresIn: 60, uid: 1 })
    const newSession = createSession({ accessToken: 'new', refreshToken: 'new-r', expiresIn: 60, uid: 1 })
    const fetchImpl = (async () => json({ code: 0, message: 'success', data: {} })) as unknown as typeof fetch

    const result = await handleBffRequest(
      ctx({
        method: 'POST',
        path: '/api/v1/keys',
        body: Buffer.from('{}'),
        session: newSession,
        headers: { host: HOST, origin: PUBLIC, 'x-sub2api-csrf': oldSession.csrf },
        fetchImpl,
      })
    )
    expect(result.status).toBe(403)
  })
})

describe('cookie attributes', () => {
  it('pins the session cookie to HttpOnly, Lax and the site root', () => {
    expect(sessionCookieOptions(true)).toEqual({
      httpOnly: true,
      secure: true,
      sameSite: 'lax',
      path: '/',
      maxAge: 30 * 24 * 60 * 60,
    })
    expect(sessionCookieOptions(false).secure).toBe(false)
  })

  it('keeps the csrf cookie readable so the adapter can echo it', () => {
    expect(csrfCookieOptions(true)).toEqual({
      httpOnly: false,
      secure: true,
      sameSite: 'lax',
      path: '/',
      maxAge: 30 * 24 * 60 * 60,
    })
  })
})
