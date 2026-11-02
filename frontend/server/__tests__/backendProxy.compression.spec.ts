import { createServer, type Server, type RequestListener } from 'node:http'
import { createGzip, gzipSync, constants } from 'node:zlib'
import * as h3 from 'h3'
import { createSession, sealBffSession } from '../utils/session'
import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest'

// Real HTTP wire compression + native Node fetch + the actual Nitro route adapter.
// A Response with gzip headers and a plain body does not exercise this bug.
const servers: Server[] = []
const runtime = { backendOrigin: '', sessionPassword: 'compression-fixture-password', sessionCookieSecure: 'false' }
let route: ReturnType<typeof h3.defineEventHandler>
let publicOrigin = ''
let finishStream = () => {}
const access = 'fixture-access-not-a-real-token'
const refresh = 'fixture-refresh-not-a-real-token'
const tokenJSON = JSON.stringify({ code: 0, data: { access_token: access, refresh_token: refresh, expires_in: 3600, user: { id: 7 } } })

async function listen(handler: RequestListener): Promise<string> {
  const server = createServer(handler)
  servers.push(server)
  await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve))
  const address = server.address()
  if (!address || typeof address === 'string') throw new Error('missing fixture address')
  return `http://127.0.0.1:${address.port}`
}

beforeAll(async () => {
  for (const name of ['defineEventHandler', 'getRequestURL', 'getHeaders', 'readRawBody', 'setResponseHeader', 'setResponseStatus', 'sendStream', 'getCookie', 'setCookie', 'deleteCookie'] as const) {
    vi.stubGlobal(name, h3[name])
  }
  vi.stubGlobal('useRuntimeConfig', () => runtime)
  route = (await import('../routes/api/v1/[...path]')).default
})
afterAll(() => vi.unstubAllGlobals())

beforeEach(async () => {
  runtime.backendOrigin = await listen((req, res) => {
    if (req.url?.endsWith('/stream') || req.url === '/api/v1/admin/accounts/7/test') {
      res.writeHead(200, { 'content-type': 'text/event-stream', 'content-encoding': 'gzip' })
      const gzip = createGzip()
      gzip.pipe(res)
      gzip.write('data: first\n\n')
      gzip.flush(constants.Z_SYNC_FLUSH)
      finishStream = () => { if (!gzip.writableEnded) gzip.end('data: last\n\n') }
      return
    }
    const callback = req.url?.includes('/oauth/google/callback')
    const login = req.url?.includes('/auth/login') || req.url?.includes('/pending/exchange')
    const location = `${req.headers['x-forwarded-proto']}://${req.headers['x-forwarded-host']}/auth/oauth/callback#access_token=${access}&refresh_token=${refresh}&expires_in=3600`
    const text = callback ? `<a href="${location}">Redirect</a>` : login ? tokenJSON : JSON.stringify({ code: 0, data: { label: 'compressed payload', values: Array(100).fill('repeat') } })
    const wire = gzipSync(text)
    res.writeHead(callback ? 302 : 200, {
      'content-type': callback ? 'text/html' : 'application/json',
      'content-encoding': 'gzip',
      'content-length': String(wire.length),
      ...(callback ? { location } : {}),
    })
    res.end(req.method === 'HEAD' ? undefined : wire)
  })
  const app = h3.createApp()
  app.use(route)
  const listener = h3.toNodeListener(app)
  publicOrigin = await listen((req, res) => {
    // Model the trusted ingress explicitly. Native fetch may override a supplied
    // Host header with its loopback connection URL; that is not our public host.
    req.headers.host = 'site.example'
    req.headers['x-forwarded-proto'] = 'https'
    return listener(req, res)
  })
})

afterEach(async () => {
  finishStream()
  finishStream = () => {}
  await Promise.all(servers.splice(0).map((server) => new Promise<void>((resolve, reject) => {
    server.close((error) => error ? reject(error) : resolve())
    server.closeAllConnections()
  })))
})

function noWireHeaders(response: Response) {
  expect(response.headers.get('content-encoding')).toBeNull()
}

describe('BFF decoded representation and downstream framing', () => {
  it('serves real gzip JSON without relaying compressed encoding or length', async () => {
    const upstream = await fetch(`${runtime.backendOrigin}/api/v1/settings/public`)
    expect(upstream.headers.get('content-encoding')).toBe('gzip')
    const wireLength = upstream.headers.get('content-length')
    const expected = await upstream.text()
    const response = await fetch(`${publicOrigin}/api/v1/settings/public`)
    noWireHeaders(response)
    expect(response.headers.get('content-length')).not.toBe(wireLength)
    expect(await response.text()).toBe(expected)
  })

  it.each(['/auth/login', '/auth/oauth/pending/exchange'])('rewrites compressed %s tokens and computes the final buffer length', async (path) => {
    const response = await fetch(`${publicOrigin}/api/v1${path}`, { method: 'POST', body: '{}', headers: { 'content-type': 'application/json', origin: 'https://site.example', 'x-sub2api-auth-mode': 'cookie' } })
    const text = await response.text()
    noWireHeaders(response)
    expect(response.status).toBe(200)
    expect(Number(response.headers.get('content-length'))).toBe(Buffer.byteLength(text))
    expect(text).not.toContain(access)
    expect(text).not.toContain(refresh)
    expect(JSON.parse(text).data).toMatchObject({ access_token: '', refresh_token: '', session_managed: true })
    expect(response.headers.getSetCookie().some((cookie) => cookie.startsWith('sub2_session=') && cookie.includes('HttpOnly'))).toBe(true)
  })

  it('keeps a compressed machine login token response without issuing session cookies', async () => {
    const response = await fetch(`${publicOrigin}/api/v1/auth/login`, { method: 'POST', body: '{}', headers: { 'content-type': 'application/json' } })
    const text = await response.text()
    noWireHeaders(response)
    expect(response.status).toBe(200)
    expect(Number(response.headers.get('content-length'))).toBe(Buffer.byteLength(text))
    expect(JSON.parse(text).data).toMatchObject({ access_token: access, refresh_token: refresh })
    expect(response.headers.getSetCookie()).toEqual([])
    expect(response.headers.get('access-control-allow-origin')).toBeNull()
  })

  it('removes the compressed token-bearing callback body as well as the fragment tokens', async () => {
    const response = await fetch(`${publicOrigin}/api/v1/auth/oauth/google/callback`, { redirect: 'manual', headers: { host: 'site.example', 'x-forwarded-proto': 'https' } })
    noWireHeaders(response)
    expect(response.status).toBe(302)
    expect(response.headers.get('location')).toBe('/auth/oauth/callback#session_managed=1')
    expect(await response.text()).toBe('')
    expect(response.headers.getSetCookie().some((cookie) => cookie.startsWith('sub2_session='))).toBe(true)
  })

  it('streams a gzip-flushed first event before upstream completion without buffering', async () => {
    const response = await fetch(`${publicOrigin}/api/v1/test/stream`)
    noWireHeaders(response)
    expect(response.headers.get('content-length')).toBeNull()
    expect(response.headers.get('x-accel-buffering')).toBe('no')
    const reader = response.body!.getReader()
    let timer: ReturnType<typeof setTimeout> | undefined
    try {
      const first = await Promise.race([reader.read(), new Promise<never>((_resolve, reject) => { timer = setTimeout(() => reject(new Error('stream was buffered')), 2000) })])
      expect(new TextDecoder().decode(first.value)).toBe('data: first\n\n')
      expect(first.done).toBe(false)
      finishStream()
      let rest = ''
      for (;;) {
        const next = await reader.read()
        if (next.done) break
        rest += new TextDecoder().decode(next.value)
      }
      expect(rest).toBe('data: last\n\n')
    } finally {
      clearTimeout(timer)
      await reader.cancel()
    }
  })

  it('allows the real raw account-test POST with its Cookie and same-origin source, without an extra token', async () => {
    const session = createSession({ accessToken: access, refreshToken: refresh, expiresIn: 3600, uid: 7 })
    const cookie = `sub2_session=${sealBffSession(session, runtime.sessionPassword)}`
    const response = await fetch(`${publicOrigin}/api/v1/admin/accounts/7/test`, { method: 'POST', body: '{}', headers: { cookie, origin: 'https://site.example', 'content-type': 'application/json' } })
    expect(response.status).toBe(200)
    expect(response.headers.get('content-type')).toBe('text/event-stream')
    noWireHeaders(response)
    finishStream()
    expect(await response.text()).toBe('data: first\n\ndata: last\n\n')
  })

  it.each(['/settings/public', '/auth/login', '/auth/logout', '/auth/oauth/pending/exchange', '/auth/oauth/google/callback'])('handles HEAD %s without parsing a body or creating a login', async (path) => {
    const response = await fetch(`${publicOrigin}/api/v1${path}`, { method: 'HEAD', redirect: 'manual', headers: { host: 'site.example', 'x-forwarded-proto': 'https' } })
    noWireHeaders(response)
    expect(response.status).toBe(path.endsWith('/callback') ? 302 : 200)
    expect(response.headers.get('content-length')).toBeNull()
    expect(response.headers.getSetCookie()).toEqual([])
    expect(await response.text()).toBe('')
    expect(response.headers.get('location') || '').not.toContain(access)
  })
})
