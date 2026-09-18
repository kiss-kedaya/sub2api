import { createServer as createHttpServer, request } from 'node:http'
import type { AddressInfo } from 'node:net'
import { fileURLToPath } from 'node:url'
import { runInNewContext } from 'node:vm'
import { afterAll, beforeAll, describe, expect, it, vi } from 'vitest'
import { createServer, loadConfigFromFile, type ViteDevServer } from 'vite'
import { consolePreviewConfig, consolePreviewPlugin, PREVIEW_MODE } from './console-preview'
import { PREVIEW_LABEL } from './fixtures'

let server: ViteDevServer
let port: number
const outbound = vi.fn(() => { throw new Error('Unexpected outbound fetch') })

async function getFreePort(): Promise<number> {
  return new Promise((resolve, reject) => {
    const probe = createHttpServer()
    probe.once('error', reject)
    probe.listen(0, '127.0.0.1', () => {
      const address = probe.address() as AddressInfo
      probe.close(error => error ? reject(error) : resolve(address.port))
    })
  })
}

function http(path: string, method = 'GET', headers: Record<string, string> = {}, body = '') {
  return new Promise<{ status: number; headers: Record<string, unknown>; text: string }>((resolve, reject) => {
    const req = request({ hostname: '127.0.0.1', port, path, method,
      headers: { Host: `127.0.0.1:${port}`, ...headers } }, res => {
      let text = ''
      res.setEncoding('utf8')
      res.on('data', chunk => { text += chunk })
      res.on('end', () => resolve({ status: res.statusCode!, headers: res.headers, text }))
    })
    req.on('error', reject)
    req.end(body)
  })
}

beforeAll(async () => {
  vi.stubGlobal('fetch', outbound)
  vi.stubEnv('VITE_API_BASE_URL', 'https://should-never-connect.example.test')
  vi.stubEnv('VITE_DEV_PROXY_TARGET', 'https://should-never-connect.example.test')
  const config = consolePreviewConfig()
  const freePort = await getFreePort()
  server = await createServer({ ...config, configFile: false, mode: PREVIEW_MODE,
    optimizeDeps: { noDiscovery: true, include: [] },
    server: { ...config.server, port: freePort }, logLevel: 'silent' })
  await server.listen()
  port = (server.httpServer!.address() as AddressInfo).port
})

afterAll(async () => {
  await server?.close()
  vi.unstubAllGlobals()
  vi.unstubAllEnvs()
})

describe('preview server isolation', () => {
  it('provides local-only admin and user entry links', async () => {
    const admin = await http('/__preview/admin')
    expect(admin.status).toBe(302)
    expect(admin.headers.location).toBe('/admin/dashboard')
    expect(JSON.parse((await http('/api/v1/auth/me')).text).data.role).toBe('admin')
    const user = await http('/__preview/user')
    expect(user.status).toBe(302)
    expect(user.headers.location).toBe('/dashboard')
    expect(JSON.parse((await http('/api/v1/auth/me')).text).data.role).toBe('user')
    expect((await http('/__preview/admin', 'GET', { 'Sec-Fetch-Site': 'cross-site' })).status).toBe(403)
    expect(outbound).not.toHaveBeenCalled()
  })
  it('binds only loopback, disables environment loading, CORS and all proxies', () => {
    expect((server.httpServer!.address() as AddressInfo).address).toBe('127.0.0.1')
    expect(server.config.inlineConfig.envFile).toBe(false)
    expect(server.config.env.VITE_API_BASE_URL).toBeUndefined()
    expect(server.config.define?.['import.meta.env.VITE_API_BASE_URL']).toBe('"/api/v1"')
    expect(server.config.server.proxy).toBeUndefined()
    expect(server.config.server.cors).toBe(false)
    expect(consolePreviewPlugin().apply).toBe('serve')
    expect(server.config.plugins.some(plugin => plugin.name === 'inject-public-settings')).toBe(false)
  })

  it('rejects DNS rebinding, cross-origin, null-origin, proxy and cross-site requests', async () => {
    const cases: Record<string, string>[] = [
      { Host: 'public.example.test' }, { Host: `localhost:${port}` },
      { Origin: 'https://public.example.test' }, { Origin: 'null' },
      { Origin: `http://127.0.0.1:${port + 1}` },
      { 'X-Forwarded-Host': 'public.example.test' },
      { 'X-Forwarded-For': '192.0.2.1' }, { 'Sec-Fetch-Site': 'cross-site' },
    ]
    for (const headers of cases) {
      expect((await http('/api/v1/keys', 'GET', headers)).status).toBe(403)
      expect((await http('/dashboard', 'GET', headers)).status).toBe(403)
    }
    const response = await http('/api/v1/keys', 'GET', { Origin: `http://127.0.0.1:${port}` })
    expect(response.status).toBe(200)
    expect(response.headers['access-control-allow-origin']).toBeUndefined()
  })

  it('returns explicit local responses for all API namespaces and unknown writes', async () => {
    for (const path of ['/api/missing', '/v1/missing', '/setup/missing', '/api', '/v1', '/setup', '/%61pi/missing']) {
      const response = await http(path)
      expect(response.status).toBe(404)
      expect(response.headers['content-type']).toContain('application/json')
      expect(response.text).toContain('不会连接真实后端')
    }
    for (const path of ['/api/v1/user/password', '/v1/responses', '/setup/install']) {
      const response = await http(path, 'POST', { 'Content-Type': 'application/json' }, '{}')
      expect(response.status).toBe(501)
      expect(response.text).toContain('演示不支持')
    }
    expect(JSON.parse((await http('/setup/status')).text)).toMatchObject({ needs_setup: false })
    expect(JSON.parse((await http('/v1/models')).text).data).toHaveLength(6)
    expect(outbound).not.toHaveBeenCalled()
  })

  it('serves all added user-page contracts without outbound requests or gateway URLs', async () => {
    const getData = async (path: string) => JSON.parse((await http(path)).text).data
    const endpoints = [
      '/api/v1/channel-monitors',
      '/api/v1/channel-monitor-v2/snapshot?range=90m',
      '/api/v1/channel-monitor-v2/matrix?range=90m&group_by=platform_group',
      '/api/v1/channels/available',
      '/api/v1/payment/checkout-info',
      '/api/v1/payment/orders/my?page=1&page_size=20',
      '/api/v1/subscriptions',
      '/api/v1/redeem/history',
      '/api/v1/user/profile',
      '/api/v1/user/aff',
      '/api/v1/user/cf-allowlist',
    ]
    for (const endpoint of endpoints) {
      expect((await http(endpoint)).status, endpoint).toBe(200)
    }
    expect((await getData('/api/v1/channel-monitors')).items).toHaveLength(5)
    expect(await getData('/api/v1/payment/checkout-info')).toMatchObject({ methods: { alipay: { currency: 'CNY' }, epusdt: { currency: 'USDT' } } })

    const createResponse = await http('/api/v1/payment/orders', 'POST', { 'Content-Type': 'application/json' }, JSON.stringify({ amount: 20, payment_type: 'alipay', order_type: 'balance' }))
    expect(createResponse.status).toBe(200)
    const created = JSON.parse(createResponse.text).data
    expect(created.qr_code).toMatch(/^LOCAL-DEMO-PAYMENT:/)
    expect(created.pay_url).toBeUndefined()
    expect(created.client_secret).toBeUndefined()
    expect(outbound).not.toHaveBeenCalled()
  })

  it('switches admin preview identity and serves common admin routes without outbound requests', async () => {
    const switched = await http('/api/v1/__preview/role', 'POST', { 'Content-Type': 'application/json' }, '{"role":"admin"}')
    expect(switched.status).toBe(200)
    expect(JSON.parse(switched.text).data).toMatchObject({ role: 'admin', email: 'admin-preview@example.test' })
    expect(JSON.parse((await http('/api/v1/auth/me')).text).data).toMatchObject({ role: 'admin' })

    for (const endpoint of [
      '/api/v1/admin/dashboard/snapshot-v2?granularity=day',
      '/api/v1/admin/accounts?page=1&page_size=10',
      '/api/v1/admin/groups?page=1&page_size=10',
      '/api/v1/admin/users?page=1&page_size=10',
      '/api/v1/admin/usage?page=1&page_size=10',
      '/api/v1/admin/keys?page=1&page_size=10',
    ]) expect((await http(endpoint)).status, endpoint).toBe(200)

    expect((await http('/admin/keys')).headers.location).toBe('/keys')

    expect((await http('/api/v1/admin/users/910002/balance', 'POST', { 'Content-Type': 'application/json' }, '{"balance":999}')).status).toBe(501)
    const adminHtml = await http('/admin/dashboard')
    expect(adminHtml.text).toContain('admin-preview@example.test')
    expect(adminHtml.text).toContain('admin_guide_900001_admin_v4_interactive')
    expect(outbound).not.toHaveBeenCalled()

    await http('/api/v1/__preview/role', 'POST', { 'Content-Type': 'application/json' }, '{"role":"user"}')
  })

  it('rejects malformed/oversized bodies and supports CRUD through HTTP', async () => {
    expect((await http('/api/v1/keys', 'POST', { 'Content-Type': 'application/json' }, '{bad')).status).toBe(400)
    expect((await http('/api/v1/keys', 'POST', { 'Content-Type': 'application/json' }, '[]')).status).toBe(400)
    expect((await http('/api/v1/keys', 'POST', { 'Content-Type': 'text/plain' }, '{}')).status).toBe(415)
    expect((await http('/api/v1/keys', 'POST', { 'Content-Type': 'application/json', 'Content-Length': '70000' }, 'x'.repeat(70000))).status).toBe(413)
    const created = JSON.parse((await http('/api/v1/keys', 'POST', { 'Content-Type': 'application/json' }, JSON.stringify({ name: 'HTTP 演示', group_id: 2 }))).text).data
    expect(created.key).toMatch(/^sk-demo-not-valid-/)
    const path = `/api/v1/keys/${created.id}`
    expect((await http(path, 'PUT', { 'Content-Type': 'application/json' }, '{"status":"inactive"}')).status).toBe(200)
    expect(JSON.parse((await http(path)).text).data.status).toBe('inactive')
    expect((await http(path, 'DELETE')).status).toBe(200)
    expect((await http(path)).status).toBe(404)
  })

  it('injects only fake auth/settings before bootstrap and displays the preview label', async () => {
    expect((await http('/')).headers.location).toBe('/dashboard')
    const response = await http('/dashboard')
    expect(response.status).toBe(200)
    expect(response.text).toContain(PREVIEW_LABEL)
    expect(response.text).toContain('console-preview@example.test')
    expect(response.text).toContain('user_guide_900001_user_v4_interactive')
    expect(response.text).toContain('admin_guide_900001_admin_v4_interactive')
    expect(response.text.indexOf("localStorage.setItem('auth_token'")).toBeLessThan(response.text.indexOf('src="/src/main.ts"'))
    expect(response.text).not.toContain('should-never-connect')
    const bootstrap = response.text.match(/<script>\s*(window\.__APP_CONFIG__[\s\S]*?)<\/script>/)?.[1]
    expect(bootstrap).toBeDefined()
    const storage = new Map<string, string>()
    runInNewContext(bootstrap!, {
      window: {}, location: { origin: `http://127.0.0.1:${port}` },
      localStorage: {
        setItem: (key: string, value: unknown) => storage.set(key, String(value)),
        getItem: (key: string) => storage.get(key) ?? null,
        removeItem: (key: string) => storage.delete(key),
      },
      document: { documentElement: { classList: { toggle: vi.fn() } }, addEventListener: vi.fn() },
    })
    expect(JSON.parse(storage.get('auth_user')!)).toMatchObject({ id: 900001, role: 'user' })
    const publicSettings = JSON.parse((await http('/api/v1/settings/public')).text).data
    expect(publicSettings.site_name).toBe('kedaya.ai')
    expect(publicSettings.api_base_url).toBe(`http://127.0.0.1:${port}`)
    expect(publicSettings.version).toBe(PREVIEW_LABEL)
    expect(response.headers['content-security-policy']).toContain(`connect-src 'self' ws://127.0.0.1:${port}`)
    expect(response.headers['content-security-policy']).toContain("frame-ancestors 'none'")
    expect(outbound).not.toHaveBeenCalled()
  })

  it('rejects public binding even if a caller overrides the config', async () => {
    const config = consolePreviewConfig()
    await expect(createServer({ ...config, configFile: false, mode: PREVIEW_MODE,
      server: { ...config.server, host: '0.0.0.0' }, logLevel: 'silent' })).rejects.toThrow('127.0.0.1')
  })

  it('makes the plugin inert outside the dedicated mode', async () => {
    const config = consolePreviewConfig()
    const inert = await createServer({ ...config, configFile: false, mode: 'development',
      optimizeDeps: { noDiscovery: true, include: [] }, logLevel: 'silent' })
    try {
      const html = await inert.transformIndexHtml('/keys', '<html><head></head><body></body></html>')
      expect(html).not.toContain('auth_token')
      expect(html).not.toContain(PREVIEW_LABEL)
    } finally { await inert.close() }
  })

  it('refuses build and static preview before emitting backend assets', async () => {
    for (const config of ['../vite.config.ts', './vite.config.ts']) {
      const configFile = fileURLToPath(new URL(config, import.meta.url))
      for (const env of [
        { command: 'build' as const, mode: PREVIEW_MODE },
        { command: 'serve' as const, mode: PREVIEW_MODE, isPreview: true },
      ]) {
        await expect(loadConfigFromFile(env, configFile, undefined, 'silent')).rejects.toThrow('local dev only')
      }
    }
  })
})
