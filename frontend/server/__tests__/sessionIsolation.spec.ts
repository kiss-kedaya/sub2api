import { describe, expect, it } from 'vitest'
import { handleBffRequest, type BffContext } from '../utils/backendProxy'
import { createSession, sealBffSession, unsealBffSession } from '../utils/session'

const PASSWORD = 'isolation-password'
const BACKEND = 'http://127.0.0.1:8080'
const PUBLIC = 'https://site.example'
const HOST = 'site.example'

function sessionFor(uid: number, access: string, refresh: string) {
  return createSession({ accessToken: access, refreshToken: refresh, expiresIn: 3600, uid })
}

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

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { 'content-type': 'application/json' } })

function recorder() {
  const seen: Array<Record<string, string>> = []
  const fetchImpl = (async (_url: string | URL | Request, init?: RequestInit) => {
    seen.push({ ...((init?.headers as Record<string, string>) ?? {}) })
    return json({ code: 0, message: 'success', data: {} })
  }) as unknown as typeof fetch
  return { seen, fetchImpl }
}

describe('two sessions never share a token', () => {
  it('injects each session token into its own request', async () => {
    const a = sessionFor(1, 'token-a', 'refresh-a')
    const b = sessionFor(2, 'token-b', 'refresh-b')
    const { seen, fetchImpl } = recorder()

    await Promise.all([
      handleBffRequest(ctx({ session: a, fetchImpl })),
      handleBffRequest(ctx({ session: b, fetchImpl })),
    ])

    const auths = seen.map((headers) => headers.authorization).sort()
    expect(auths).toEqual(['Bearer token-a', 'Bearer token-b'])
    expect(a.accessToken).toBe('token-a')
    expect(b.accessToken).toBe('token-b')
  })

  it('rejects a cookie sealed with another password instead of injecting a token', async () => {
    const foreign = sessionFor(3, 'token-foreign', 'refresh-foreign')
    const sealedElsewhere = sealBffSession(foreign, 'another-instance-password')
    expect(unsealBffSession(sealedElsewhere, PASSWORD)).toBeNull()

    const { seen, fetchImpl } = recorder()
    await handleBffRequest(ctx({ session: unsealBffSession(sealedElsewhere, PASSWORD), fetchImpl }))
    expect(seen[0].authorization).toBeUndefined()
  })

  it('rejects a tampered cookie instead of throwing', async () => {
    const sealed = sealBffSession(sessionFor(4, 'token-a', 'refresh-a'), PASSWORD)
    const tampered = `${sealed.slice(0, -6)}AAAAAA`
    expect(unsealBffSession(tampered, PASSWORD)).toBeNull()
  })

  it('ignores a client-supplied bearer when a cookie session is present', async () => {
    const a = sessionFor(5, 'token-owner', 'refresh-owner')
    const { seen, fetchImpl } = recorder()
    await handleBffRequest(
      ctx({
        session: a,
        headers: { host: HOST, authorization: 'Bearer attacker', 'x-sub2api-csrf': a.csrf },
        fetchImpl,
      })
    )
    expect(seen[0].authorization).toBe('Bearer token-owner')
  })

  it('refreshes two sessions independently', async () => {
    const a = sessionFor(6, 'expired-a', 'refresh-a2')
    const b = sessionFor(7, 'expired-b', 'refresh-b2')
    const refreshedFor: string[] = []
    const fetchImpl = (async (url: string | URL | Request, init?: RequestInit) => {
      const href = String(url)
      if (href.endsWith('/api/v1/auth/refresh')) {
        const body = JSON.parse(String(init?.body)) as { refresh_token: string }
        refreshedFor.push(body.refresh_token)
        const suffix = body.refresh_token.endsWith('a2') ? 'a' : 'b'
        return json({ code: 0, message: 'success', data: { access_token: `fresh-${suffix}`, refresh_token: `rotated-${suffix}`, expires_in: 3600 } })
      }
      const auth = (init?.headers as Record<string, string>).authorization
      if (auth === 'Bearer expired-a' || auth === 'Bearer expired-b') return json({ code: 401, message: 'TOKEN_EXPIRED' }, 401)
      return json({ code: 0, message: 'success', data: {} })
    }) as unknown as typeof fetch

    const [ra, rb] = await Promise.all([
      handleBffRequest(ctx({ session: a, fetchImpl })),
      handleBffRequest(ctx({ session: b, fetchImpl })),
    ])

    expect(refreshedFor.sort()).toEqual(['refresh-a2', 'refresh-b2'])
    const tokenA = ra.session.action === 'set' ? ra.session.value.accessToken : null
    const tokenB = rb.session.action === 'set' ? rb.session.value.accessToken : null
    expect(tokenA).toBe('fresh-a')
    expect(tokenB).toBe('fresh-b')
    expect(tokenA).not.toBe(tokenB)
  })

  it('a failed refresh in one session leaves the other untouched', async () => {
    const good = sessionFor(8, 'expired-good', 'refresh-good')
    const bad = sessionFor(9, 'expired-bad', 'refresh-bad')
    const fetchImpl = (async (url: string | URL | Request, init?: RequestInit) => {
      const href = String(url)
      if (href.endsWith('/api/v1/auth/refresh')) {
        const body = JSON.parse(String(init?.body)) as { refresh_token: string }
        if (body.refresh_token === 'refresh-bad') return json({ code: 401, message: 'revoked' }, 401)
        return json({ code: 0, message: 'success', data: { access_token: 'fresh-good', refresh_token: 'rotated-good', expires_in: 3600 } })
      }
      const auth = (init?.headers as Record<string, string>).authorization
      if (auth === 'Bearer expired-good') return json({ code: 401, message: 'TOKEN_EXPIRED' }, 401)
      if (auth === 'Bearer expired-bad') return json({ code: 401, message: 'TOKEN_EXPIRED' }, 401)
      return json({ code: 0, message: 'success', data: {} })
    }) as unknown as typeof fetch

    const [rGood, rBad] = await Promise.all([
      handleBffRequest(ctx({ session: good, fetchImpl })),
      handleBffRequest(ctx({ session: bad, fetchImpl })),
    ])

    expect(rGood.status).toBe(200)
    expect(rGood.session.action).toBe('set')
    expect(rBad.status).toBe(401)
    // The failing session keeps its cookie; only logout clears.
    expect(rBad.session.action).toBe('keep')
  })
})
