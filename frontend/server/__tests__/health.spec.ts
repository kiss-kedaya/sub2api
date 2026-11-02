import { beforeEach, describe, expect, it } from 'vitest'
import {
  SSR_API_CONTRACT,
  buildFrontendHealth,
  probeBackend,
  probeBackendCached,
  resetHealthCache,
  type HealthEnv,
} from '../utils/health'

const ORIGIN = 'http://127.0.0.1:8080'

const env = (overrides: Partial<HealthEnv> = {}): HealthEnv => ({
  ssrVersion: '2.3.0',
  commit: 'abc1234',
  builtAt: '2026-11-02T00:00:00Z',
  expectedBackendVersion: '2.3.0',
  sessionConfigured: true,
  ...overrides,
})

const ready = (version: string | null = '2.3.0', status = 200) => ({
  reachable: true,
  ready: status === 200,
  version,
  latency_ms: 3,
  error: status === 200 ? null : 'http_error' as const,
})

describe('health payload contract', () => {
  it('is ok when both artifacts agree and the session is configured', () => {
    const payload = buildFrontendHealth(env(), ready(), 1_700_000_000_000)
    expect(payload.status).toBe('ok')
    expect(payload.renderer).toBe('nuxt-ssr')
    expect(payload.contract).toBe(SSR_API_CONTRACT)
    expect(payload.version).toBe('2.3.0')
    expect(payload.expected_backend_version).toBe('2.3.0')
    expect(payload.backend.version).toBe('2.3.0')
    expect(payload.warnings).toEqual([])
    expect(payload.checked_at).toBe('2023-11-14T22:13:20.000Z')
  })

  it('flags a backend-only update as degraded instead of silently skewing', () => {
    const payload = buildFrontendHealth(env(), ready('2.3.1'))
    expect(payload.status).toBe('degraded')
    expect(payload.warnings).toContain('backend_version_mismatch')
  })

  it('reports readiness failures', () => {
    const payload = buildFrontendHealth(env(), ready('2.3.0', 503))
    expect(payload.status).toBe('degraded')
    expect(payload.backend.ready).toBe(false)
  })

  it('is not_ready when the backend cannot be reached', () => {
    const payload = buildFrontendHealth(env(), null)
    expect(payload.status).toBe('not_ready')
    expect(payload.backend.reachable).toBe(false)
  })

  it('warns about a missing session secret and an unpinned expected version', () => {
    const payload = buildFrontendHealth(
      env({ sessionConfigured: false, expectedBackendVersion: null }),
      ready('2.3.0')
    )
    expect(payload.warnings).toContain('session_password_missing')
    expect(payload.warnings).toContain('expected_backend_version_unset')
    expect(payload.status).toBe('ok')
  })

  it('never carries the backend origin or a secret', () => {
    const payload = buildFrontendHealth(env(), ready())
    const raw = JSON.stringify(payload)
    expect(raw).not.toContain('127.0.0.1')
    expect(raw).not.toContain('8080')
    expect(raw).not.toContain('password')
  })
})

describe('backend probe', () => {
  beforeEach(() => resetHealthCache())

  const fakeFetch = (handlers: { readyz?: () => Response | Promise<Response>; settings?: () => Response | Promise<Response> }) => {
    const calls: string[] = []
    const fetchImpl = (async (url: string | URL | Request) => {
      const href = String(url)
      calls.push(href)
      if (href.endsWith('/readyz')) return handlers.readyz ? handlers.readyz() : new Response('{"status":"ready"}', { status: 200 })
      if (href.endsWith('/api/v1/settings/public')) {
        return handlers.settings
          ? handlers.settings()
          : new Response(JSON.stringify({ code: 0, data: { version: '2.3.0' } }), { status: 200 })
      }
      return new Response('not found', { status: 404 })
    }) as unknown as typeof fetch
    return { calls, fetchImpl }
  }

  it('collects readiness, latency and the backend version', async () => {
    const { fetchImpl } = fakeFetch({})
    const probe = await probeBackend(ORIGIN, fetchImpl)
    expect(probe.reachable).toBe(true)
    expect(probe.ready).toBe(true)
    expect(probe.version).toBe('2.3.0')
    expect(probe.latency_ms).toBeGreaterThanOrEqual(0)
    expect(probe.error).toBeNull()
  })

  it('keeps reachable but not ready when /readyz fails', async () => {
    const { fetchImpl } = fakeFetch({ readyz: () => new Response('{"status":"not_ready"}', { status: 503 }) })
    const probe = await probeBackend(ORIGIN, fetchImpl)
    expect(probe.reachable).toBe(true)
    expect(probe.ready).toBe(false)
    expect(probe.error).toBe('http_error')
  })

  it('reports unreachable when the socket fails', async () => {
    const fetchImpl = (async () => {
      throw new Error('ECONNREFUSED')
    }) as unknown as typeof fetch
    const probe = await probeBackend(ORIGIN, fetchImpl)
    expect(probe.reachable).toBe(false)
    expect(probe.error).toBe('unreachable')
  })

  it('bounds a silent backend with the probe timeout', async () => {
    const fetchImpl = ((_url: string | URL | Request, init?: RequestInit) =>
      new Promise((_resolve, reject) => {
        init?.signal?.addEventListener('abort', () => {
          const error = new Error('aborted')
          error.name = 'AbortError'
          reject(error)
        })
      })) as unknown as typeof fetch
    const started = Date.now()
    const probe = await probeBackend(ORIGIN, fetchImpl, 20)
    expect(probe.reachable).toBe(false)
    expect(probe.error).toBe('timeout')
    expect(Date.now() - started).toBeLessThan(2000)
  })

  it('still reports reachable when the version endpoint is not available', async () => {
    const { fetchImpl } = fakeFetch({ settings: () => new Response('nope', { status: 500 }) })
    const probe = await probeBackend(ORIGIN, fetchImpl)
    expect(probe.reachable).toBe(true)
    expect(probe.version).toBeNull()
  })

  it('caches the probe so a poller cannot hammer the backend', async () => {
    const { calls, fetchImpl } = fakeFetch({})
    await probeBackendCached(ORIGIN, fetchImpl, 5000, 1000)
    await probeBackendCached(ORIGIN, fetchImpl, 5000, 2000)
    expect(calls).toHaveLength(2) // readyz + settings, once
    await probeBackendCached(ORIGIN, fetchImpl, 5000, 9000)
    expect(calls).toHaveLength(4)
  })
})
