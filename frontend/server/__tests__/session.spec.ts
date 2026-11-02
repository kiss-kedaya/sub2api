import { describe, expect, it } from 'vitest'
import {
  CSRF_HEADER,
  SESSION_COOKIE,
  createSession,
  csrfMatches,
  isSessionExpired,
  randomToken,
  sealBffSession,
  unsealBffSession,
  buildSessionProbe,
} from '../utils/session'
import { normalizeSecureMode } from '../utils/sessionCookie'

const PASSWORD = 'test-session-password'

const base = () =>
  createSession({
    accessToken: 'access-1',
    refreshToken: 'refresh-1',
    expiresIn: 3600,
    uid: 42,
    now: 1_700_000_000_000,
  })

describe('session sealing', () => {
  it('round-trips a sealed session', () => {
    const session = base()
    const sealed = sealBffSession(session, PASSWORD)
    expect(sealed.split('.')).toHaveLength(4)
    expect(sealed).not.toContain('access-1')
    expect(unsealBffSession(sealed, PASSWORD)).toEqual(session)
  })

  it('rejects a tampered, truncated or foreign cookie', () => {
    const sealed = sealBffSession(base(), PASSWORD)
    const parts = sealed.split('.')
    const tamperedBody = [parts[0], parts[1], `${parts[2].slice(0, -4)}AAAA`, parts[3]].join('.')
    expect(unsealBffSession(tamperedBody, PASSWORD)).toBeNull()
    expect(unsealBffSession(`${sealed}x`, PASSWORD)).toBeNull()
    expect(unsealBffSession('v1.a.b', PASSWORD)).toBeNull()
    expect(unsealBffSession('', PASSWORD)).toBeNull()
    expect(unsealBffSession(undefined, PASSWORD)).toBeNull()
  })

  it('rejects a cookie sealed with another password', () => {
    const sealed = sealBffSession(base(), PASSWORD)
    expect(unsealBffSession(sealed, 'another-password')).toBeNull()
  })

  it('fails closed when no password is configured', () => {
    expect(() => sealBffSession(base(), '')).toThrow(/not configured/)
    expect(unsealBffSession('v1.a.b.c', '')).toBeNull()
  })
})

describe('session lifetime and csrf', () => {
  it('treats an unknown expiry as live instead of expired', () => {
    const session = createSession({ accessToken: 'a', refreshToken: 'r', expiresIn: 0, uid: 1, now: 1 })
    expect(session.accessExp).toBe(0)
    expect(isSessionExpired(session, Date.now() + 10_000_000)).toBe(false)
  })

  it('expires once the access token deadline passes', () => {
    const session = base()
    expect(isSessionExpired(session, 1_700_000_000_000 + 3_599_000)).toBe(false)
    expect(isSessionExpired(session, 1_700_000_000_000 + 3_600_000)).toBe(true)
  })

  it('matches only the exact csrf token, in constant time', () => {
    const session = base()
    expect(csrfMatches(session, session.csrf)).toBe(true)
    expect(csrfMatches(session, `${session.csrf}x`)).toBe(false)
    expect(csrfMatches(session, session.csrf.toUpperCase())).toBe(false)
    expect(csrfMatches(session, '')).toBe(false)
    expect(csrfMatches(session, undefined)).toBe(false)
  })

  it('uses distinct csrf tokens per session and a stable cookie name', () => {
    expect(createSession({ accessToken: 'a', refreshToken: 'r', expiresIn: 1, uid: 1 }).csrf).not.toBe(
      createSession({ accessToken: 'a', refreshToken: 'r', expiresIn: 1, uid: 1 }).csrf
    )
    expect(randomToken(24).length).toBeGreaterThanOrEqual(30)
    expect(SESSION_COOKIE).toBe('sub2_session')
    expect(CSRF_HEADER).toBe('x-csrf-token')
  })
})

describe('session probe', () => {
  it('reports an anonymous, unconfigured instance instead of failing the page', () => {
    expect(buildSessionProbe(null, false)).toEqual({
      authenticated: false,
      session_managed: false,
      configured: false,
      csrf_token: null,
      expires_at: null,
    })
  })

  it('reports anonymous but managed when the cookie is absent', () => {
    expect(buildSessionProbe(null, true)).toEqual({
      authenticated: false,
      session_managed: true,
      configured: true,
      csrf_token: null,
      expires_at: null,
    })
  })

  it('exposes the csrf token only for a live session', () => {
    const session = base()
    const probe = buildSessionProbe(session, true)
    expect(probe.authenticated).toBe(true)
    expect(probe.csrf_token).toBe(session.csrf)
    expect(probe.expires_at).toBe(session.accessExp)
  })
})

describe('cookie secure mode', () => {
  it('falls back to auto for unknown values', () => {
    expect(normalizeSecureMode(undefined)).toBe('auto')
    expect(normalizeSecureMode('TRUE')).toBe('true')
    expect(normalizeSecureMode('false')).toBe('false')
    expect(normalizeSecureMode('yes')).toBe('auto')
  })
})
