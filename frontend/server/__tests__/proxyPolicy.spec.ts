import { describe, expect, it } from 'vitest'
import {
  API_PREFIX,
  applySessionContract,
  checkCsrf,
  filterOAuthRequestCookie,
  filterOAuthSetCookieHeaders,
  filterRequestHeaders,
  filterResponseHeaders,
  isOAuthCallbackRoute,
  isOAuthCompletionRoute,
  isAllowedMethod,
  isBffOnlyRoute,
  isLogoutRoute,
  isMutatingMethod,
  isTokenRoute,
  resolveClientIp,
  resolveUpstreamPath,
  sanitizeLocation,
  readCsrfHeader,
} from '../utils/proxyPolicy'

const HOST = 'kedaya.example'
const PROTO = 'https'

const acceptedPaths = [
  '/api/v1/keys',
  '/api/v1/keys/12',
  '/api/v1/admin/tickets/stats',
  '/api/v1/usage/dashboard/api-keys-usage',
  '/api/v1/auth/me',
  '/api/v1/announcements/item/9',
]

const rejectedPaths = [
  '/v1/messages',
  '/api/v2/keys',
  '/api/v1',
  '/api/v1/../v1/messages',
  '/api/v1/keys/../../v1/chat',
  '/api/v1//keys',
  '/api/v1/keys%2f..%2fadmin',
  '/api/v1/keys?x=1',
  '/api/v1/keys#x',
  'api/v1/keys',
  'http://evil.example/api/v1/keys',
  '/api/v1\\keys',
  '',
]

describe('upstream path allowlist', () => {
  it.each(acceptedPaths)('accepts %s', (path) => {
    const decision = resolveUpstreamPath(path)
    expect(decision.ok).toBe(true)
    expect(decision.path?.startsWith(API_PREFIX)).toBe(true)
  })

  it.each(rejectedPaths)('rejects %s', (path) => {
    expect(resolveUpstreamPath(path).ok).toBe(false)
  })

  it('normalises a trailing slash and keeps the segment order', () => {
    expect(resolveUpstreamPath('/api/v1/keys/')).toEqual({ ok: true, path: '/api/v1/keys' })
  })

  it('rejects overlong paths and control characters', () => {
    expect(resolveUpstreamPath(`/api/v1/${'a'.repeat(600)}`).ok).toBe(false)
    expect(resolveUpstreamPath('/api/v1/ke\u0000ys').ok).toBe(false)
  })
})

describe('method and route classification', () => {
  it('accepts the management verbs only', () => {
    for (const method of ['GET', 'HEAD', 'POST', 'PUT', 'PATCH', 'DELETE', 'get']) {
      expect(isAllowedMethod(method)).toBe(true)
    }
    for (const method of ['TRACE', 'CONNECT', 'OPTIONS', undefined]) {
      expect(isAllowedMethod(method)).toBe(false)
    }
  })

  it('knows which methods need CSRF', () => {
    expect(isMutatingMethod('POST')).toBe(true)
    expect(isMutatingMethod('DELETE')).toBe(true)
    expect(isMutatingMethod('GET')).toBe(false)
  })

  it('separates token routes, logout and the BFF-only endpoint', () => {
    expect(isTokenRoute('/api/v1/auth/login')).toBe(true)
    expect(isTokenRoute('/api/v1/auth/login/2fa')).toBe(true)
    expect(isTokenRoute('/api/v1/auth/refresh')).toBe(true)
    expect(isTokenRoute('/api/v1/auth/me')).toBe(false)
    expect(isLogoutRoute('/api/v1/auth/logout')).toBe(true)
    expect(isBffOnlyRoute('/api/v1/auth/session')).toBe(true)
  })
})

describe('session-managed auth payload', () => {
  const login = {
    code: 0,
    message: 'success',
    data: {
      access_token: 'a.b.c',
      refresh_token: 'r.1',
      expires_in: 7200,
      token_type: 'Bearer',
      user: { id: 7, email: 'a@b.c' },
    },
  }

  it('blanks the tokens, keeps the user and flags the session as managed', () => {
    const { payload, tokens } = applySessionContract(login) as {
      payload: { data: Record<string, unknown>; code: number; message: string }
      tokens: Record<string, unknown>
    }
    expect(tokens).toEqual({ accessToken: 'a.b.c', refreshToken: 'r.1', expiresIn: 7200, uid: 7 })
    expect(payload.data.access_token).toBe('')
    expect(payload.data.refresh_token).toBe('')
    expect(payload.data.session_managed).toBe(true)
    expect(payload.data.expires_in).toBe(7200)
    expect(payload.data.token_type).toBe('Bearer')
    expect(payload.data.user).toEqual({ id: 7, email: 'a@b.c' })
    expect(payload.code).toBe(0)
    expect(payload.message).toBe('success')
  })

  it('leaves a 2FA challenge untouched and unmanaged', () => {
    const challenge = { code: 0, message: 'success', data: { requires_2fa: true, temp_token: 'tmp' } }
    const { payload, tokens } = applySessionContract(challenge) as {
      payload: { data: Record<string, unknown> }
      tokens: Record<string, unknown>
    }
    expect(tokens).toEqual({})
    expect(payload.data.requires_2fa).toBe(true)
    expect(payload.data.temp_token).toBe('tmp')
    expect(payload.data.session_managed).toBe(false)
  })

  it('ignores payloads without a data object', () => {
    expect(applySessionContract(null).tokens).toEqual({})
    expect(applySessionContract({ code: 0 }).tokens).toEqual({})
  })
})

describe('header hygiene', () => {
  it.each(['/api/v1/auth/oauth/start', '/api/v1/auth/oauth/email/start', '/api/v1/auth/oauth/google/bind/start', '/api/v1/auth/oauth/github/payment/start', '/api/v1/auth/oauth/oidc', '/api/v1/auth/oauth/google/create-account'])('never forwards cookies on nonexistent OAuth endpoint %s', (path) => {
    expect(filterOAuthRequestCookie(path, 'email_oauth_state=secret; oauth_pending_session=pending')).toBeUndefined()
    expect(filterOAuthSetCookieHeaders(path, ['email_oauth_state=secret; Path=/api/v1/auth/oauth; HttpOnly; SameSite=Lax'])).toEqual([])
  })
  it('forwards only provider OAuth cookies on exact OAuth routes and allowlisted response cookie names', () => {
    expect(filterOAuthRequestCookie('/api/v1/auth/oauth/linuxdo/callback', 'linuxdo_oauth_state=state; oauth_pending_browser_session=browser; sub2_session=secret; other=value'))
      .toBe('linuxdo_oauth_state=state; oauth_pending_browser_session=browser')
    expect(filterOAuthRequestCookie('/api/v1/auth/me', 'linuxdo_oauth_state=state; sub2_session=secret')).toBeUndefined()
    expect(filterOAuthRequestCookie('/api/v1/auth/oauth/oidc/callback', 'oidc_oauth_state=state; linuxdo_oauth_state=wrong'))
      .toBe('oidc_oauth_state=state')
    expect(filterOAuthSetCookieHeaders('/api/v1/auth/oauth/linuxdo/callback', [
      'linuxdo_oauth_state=; Path=/api/v1/auth/oauth/linuxdo; Max-Age=0; HttpOnly; SameSite=Lax',
      'sub2_session=attacker; Path=/; HttpOnly',
      'linuxdo_oauth_state=x; Path=/; Domain=evil.example',
      'evil=1; Path=/api/v1/auth/oauth/linuxdo',
    ])).toEqual(['linuxdo_oauth_state=; Path=/api/v1/auth/oauth/linuxdo; Max-Age=0; HttpOnly; SameSite=Lax'])
    expect(isOAuthCallbackRoute('/api/v1/auth/oauth/linuxdo/callback')).toBe(true)
    expect(isOAuthCompletionRoute('/api/v1/auth/oauth/oidc/complete-registration')).toBe(true)
    expect(isOAuthCallbackRoute('/api/v1/auth/oauth/oidc/callback/extra')).toBe(false)
  })

  it('drops credentials, cookies and hop-by-hop headers', () => {
    const headers = filterRequestHeaders({
      accept: 'application/json',
      'accept-language': 'zh-CN',
      authorization: 'Bearer leaked',
      cookie: 'sub2_session=x',
      host: 'evil.example',
      connection: 'keep-alive',
      'x-forwarded-for': '1.2.3.4',
      'x-admin-ui-request': '1',
      'user-agent': 'vitest',
    })
    expect(headers).toEqual({
      accept: 'application/json',
      'accept-language': 'zh-CN',
      'x-admin-ui-request': '1',
      'user-agent': 'vitest',
    })
  })

  it('forces no-store and never relays set-cookie', () => {
    const headers = new Headers()
    headers.set('content-type', 'application/json')
    headers.set('content-encoding', 'gzip')
    headers.set('content-length', '73')
    headers.set('set-cookie', 'a=b')
    headers.set('access-control-allow-origin', '*')
    headers.set('location', '/login')
    const out = filterResponseHeaders(headers)
    expect(out['cache-control']).toContain('no-store')
    expect(out.vary).toBe('cookie')
    expect(out['content-type']).toBe('application/json')
    expect(out['content-encoding']).toBeUndefined()
    expect(out['content-length']).toBeUndefined()
    expect(out.location).toBe('/login')
    expect(out['set-cookie']).toBeUndefined()
    expect(out['access-control-allow-origin']).toBeUndefined()
  })

  it('rewrites a backend redirect and never leaks a private host', () => {
    expect(sanitizeLocation('http://127.0.0.1:8080/oauth/done?x=1', 'http://127.0.0.1:8080', 'https://site.example'))
      .toBe('https://site.example/oauth/done?x=1')
    // OAuth start legitimately sends the browser to GitHub: pass it through.
    expect(sanitizeLocation('https://github.com/login', 'http://127.0.0.1:8080', 'https://site.example'))
      .toBe('https://github.com/login')
    expect(sanitizeLocation('/relative', 'http://127.0.0.1:8080', 'https://site.example')).toBe('/relative')
    // Anything else on a private address is dropped instead of exposed.
    expect(sanitizeLocation('http://10.0.0.5/admin', 'http://127.0.0.1:8080', 'https://site.example')).toBeNull()
    expect(sanitizeLocation('http://localhost:9000/x', 'http://127.0.0.1:8080', 'https://site.example')).toBeNull()
    expect(sanitizeLocation('http://127.0.0.1:8080/x', 'http://127.0.0.1:8080', '')).toBeNull()
    expect(sanitizeLocation('', 'http://127.0.0.1:8080', 'https://site.example')).toBeNull()
  })

  it('accepts the client adapter csrf header and the legacy name', () => {
    expect(readCsrfHeader({ 'x-sub2api-csrf': 'tok-1' })).toBe('tok-1')
    expect(readCsrfHeader({ 'x-csrf-token': 'tok-2' })).toBe('tok-2')
    expect(readCsrfHeader({ 'x-sub2api-csrf': ['tok-3'] })).toBe('tok-3')
    expect(readCsrfHeader({})).toBeUndefined()
  })

  it('only trusts a forwarded ip from a private peer', () => {
    expect(resolveClientIp('203.0.113.9', '127.0.0.1')).toBe('203.0.113.9')
    expect(resolveClientIp('203.0.113.9', '10.254.0.7')).toBe('203.0.113.9')
    expect(resolveClientIp('203.0.113.9', '198.51.100.4')).toBe('198.51.100.4')
    expect(resolveClientIp('not-an-ip', '127.0.0.1')).toBe('127.0.0.1')
    expect(resolveClientIp(undefined, undefined)).toBe('')
  })

  it('walks forwarded IPs from the trusted right edge, not the attacker-controlled left edge', () => {
    expect(resolveClientIp('198.51.100.66, 203.0.113.9', '127.0.0.1')).toBe('203.0.113.9')
    expect(resolveClientIp('198.51.100.66, 203.0.113.9, 10.0.0.2, 127.0.0.1', '127.0.0.1')).toBe('203.0.113.9')
    expect(resolveClientIp('198.51.100.66, 2001:db8::123, fd00::2, fe80::1', '::1')).toBe('2001:db8::123')
    expect(resolveClientIp('198.51.100.66, malformed', '127.0.0.1')).toBe('127.0.0.1')
    expect(resolveClientIp('198.51.100.66, :::', '127.0.0.1')).toBe('127.0.0.1')
    expect(resolveClientIp('198.51.100.66, 10.0.0.2', '203.0.113.9')).toBe('203.0.113.9')
  })
})

describe('csrf policy', () => {
  const base = {
    method: 'POST',
    path: '/api/v1/keys',
    usesCookieSession: true,
    csrfHeader: 'tok-abcdefghijklmnop',
    csrfToken: 'tok-abcdefghijklmnop',
    origin: `https://${HOST}`,
    referer: undefined as string | undefined,
    host: HOST,
    proto: PROTO,
  }

  it('accepts a same-origin mutation carrying the session token', () => {
    expect(checkCsrf(base)).toEqual({ ok: true })
  })

  it('rejects a same-length CSRF token with only the final byte changed', () => {
    expect(checkCsrf({ ...base, csrfHeader: `${base.csrfHeader.slice(0, -1)}x` }).ok).toBe(false)
  })

  it('rejects a missing or mismatched token', () => {
    expect(checkCsrf({ ...base, csrfHeader: undefined }).ok).toBe(false)
    expect(checkCsrf({ ...base, csrfHeader: 'other-token-1234567890' }).ok).toBe(false)
  })

  it('rejects a same-length token that differs only at the end', () => {
    expect(checkCsrf({ ...base, csrfHeader: `${base.csrfHeader.slice(0, -1)}x` }).ok).toBe(false)
  })

  it('rejects cross-origin and origin-less mutations', () => {
    expect(checkCsrf({ ...base, origin: 'https://evil.example' }).ok).toBe(false)
    expect(checkCsrf({ ...base, origin: undefined, referer: undefined }).ok).toBe(false)
    expect(checkCsrf({ ...base, origin: undefined, referer: 'https://evil.example/x' }).ok).toBe(false)
    expect(checkCsrf({ ...base, origin: undefined, referer: `https://${HOST}/keys` }).ok).toBe(true)
  })

  it('leaves reads and bearer clients alone', () => {
    expect(checkCsrf({ ...base, method: 'GET', csrfHeader: undefined }).ok).toBe(true)
    expect(checkCsrf({ ...base, usesCookieSession: false, csrfHeader: undefined, origin: undefined }).ok).toBe(true)
  })

  it('allows the credential routes to skip the csrf token but not the origin check', () => {
    const login = { ...base, path: '/api/v1/auth/login', csrfHeader: undefined }
    expect(checkCsrf({ ...login, origin: `https://${HOST}` }).ok).toBe(true)
    expect(checkCsrf({ ...login, origin: 'https://evil.example' }).ok).toBe(false)
    // logout is not a credential route: it must echo the token.
    expect(checkCsrf({ ...base, path: '/api/v1/auth/logout', csrfHeader: undefined }).ok).toBe(false)
    // Cookie refresh spends an ambient credential, not the caller's body token.
    expect(checkCsrf({ ...base, path: '/api/v1/auth/refresh', csrfHeader: undefined }).ok).toBe(false)
    expect(checkCsrf({ ...base, path: '/api/v1/auth/refresh' }).ok).toBe(true)
  })
})
