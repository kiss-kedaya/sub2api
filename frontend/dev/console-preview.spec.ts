import { request } from 'node:http'
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
  server = await createServer({ ...config, configFile: false, mode: PREVIEW_MODE,
    optimizeDeps: { noDiscovery: true, include: [] },
    server: { ...config.server, port: 0 }, logLevel: 'silent' })
  await server.listen()
  port = (server.httpServer!.address() as AddressInfo).port
})

afterAll(async () => {
  await server?.close()
  vi.unstubAllGlobals()
  vi.unstubAllEnvs()
})

describe('preview server isolation', () => {
  it('binds only loopback, disables environment loading, CORS and all proxies', () => {
    expect((server.httpServer!.address() as AddressInfo).address).toBe('127.0.0.1')
    expect(server.config.inlineConfig.envFile).toBe(false)
    expect(server.config.env.VITE_API_BASE_URL).toBeUndefined()
    expect(server.config.define['import.meta.env.VITE_API_BASE_URL']).toBe('"/api/v1"')
    expect(server.config.server.proxy).toBeUndefined()
    expect(server.config.server.cors).toBe(false)
    expect(consolePreviewPlugin().apply).toBe('serve')
    expect(server.config.plugins.some(plugin => plugin.name === 'inject-public-settings')).toBe(false)
  })

  it('rejects DNS rebinding, cross-origin, null-origin, proxy and cross-site requests', async () => {
    for (const headers of [
      { Host: 'public.example.test' }, { Host: `localhost:${port}` },
      { Origin: 'https://public.example.test' }, { Origin: 'null' },
      { Origin: `http://127.0.0.1:${port + 1}` },
      { 'X-Forwarded-Host': 'public.example.test' },
      { 'X-Forwarded-For': '192.0.2.1' }, { 'Sec-Fetch-Site': 'cross-site' },
    ]) {
      expect((await http('/api/v1/keys', 'GET', headers as Record<string, string>)).status).toBe(403)
      expect((await http('/dashboard', 'GET', headers as Record<string, string>)).status).toBe(403)
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
    const configFile = fileURLToPath(new URL('../vite.config.ts', import.meta.url))
    for (const env of [
      { command: 'build' as const, mode: PREVIEW_MODE },
      { command: 'serve' as const, mode: PREVIEW_MODE, isPreview: true },
    ]) {
      await expect(loadConfigFromFile(env, configFile, undefined, 'silent')).rejects.toThrow('local dev only')
    }
  })
})
