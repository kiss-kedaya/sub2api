/**
 * Health and version contract for the SSR service.
 *
 * The SSR bundle and the Go API are two artifacts that can roll independently,
 * so the health payload reports both versions and the API contract the bundle
 * was built for. `status` is the readiness signal; nothing here carries a
 * secret or the private backend address.
 */

/** Bump when the BFF session/payload contract changes in a breaking way. */
export const SSR_API_CONTRACT = 1

const DEFAULT_PROBE_TIMEOUT_MS = 1500
const DEFAULT_CACHE_TTL_MS = 5000

export type ProbeError = 'timeout' | 'unreachable' | 'http_error' | null

export interface BackendProbe {
  reachable: boolean
  ready: boolean
  version: string | null
  latency_ms: number | null
  error: ProbeError
}

export interface HealthEnv {
  ssrVersion: string
  commit: string | null
  builtAt: string | null
  expectedBackendVersion: string | null
  sessionConfigured: boolean
}

export type HealthStatus = 'ok' | 'degraded' | 'not_ready'

export interface FrontendHealth {
  status: HealthStatus
  renderer: 'nuxt-ssr'
  contract: number
  version: string
  commit: string | null
  built_at: string | null
  session_configured: boolean
  expected_backend_version: string | null
  backend: BackendProbe
  warnings: string[]
  checked_at: string
}

const emptyProbe = (error: ProbeError): BackendProbe => ({
  reachable: false,
  ready: false,
  version: null,
  latency_ms: null,
  error,
})

/** Pure payload builder so the contract can be pinned by tests. */
export function buildFrontendHealth(
  env: HealthEnv,
  probe: BackendProbe | null,
  now: number = Date.now()
): FrontendHealth {
  const backend = probe ?? emptyProbe('unreachable')
  const warnings: string[] = []

  if (!env.sessionConfigured) warnings.push('session_password_missing')
  if (backend.reachable && !backend.version) warnings.push('backend_version_unknown')
  if (
    backend.version &&
    env.expectedBackendVersion &&
    backend.version !== env.expectedBackendVersion
  ) {
    warnings.push('backend_version_mismatch')
  }
  if (!env.expectedBackendVersion) warnings.push('expected_backend_version_unset')

  let status: HealthStatus = 'ok'
  if (!backend.reachable) status = 'not_ready'
  else if (!backend.ready || warnings.includes('backend_version_mismatch')) status = 'degraded'

  return {
    status,
    renderer: 'nuxt-ssr',
    contract: SSR_API_CONTRACT,
    version: env.ssrVersion,
    commit: env.commit,
    built_at: env.builtAt,
    session_configured: env.sessionConfigured,
    expected_backend_version: env.expectedBackendVersion,
    backend,
    warnings,
    checked_at: new Date(now).toISOString(),
  }
}

async function withTimeout<T>(
  run: (signal: AbortSignal) => Promise<T>,
  timeoutMs: number
): Promise<{ ok: true; value: T } | { ok: false; error: ProbeError }> {
  const controller = new AbortController()
  const timer = setTimeout(() => controller.abort(), timeoutMs)
  try {
    return { ok: true, value: await run(controller.signal) }
  } catch (error) {
    const name = (error as { name?: string } | null)?.name
    return { ok: false, error: name === 'AbortError' || name === 'TimeoutError' ? 'timeout' : 'unreachable' }
  } finally {
    clearTimeout(timer)
  }
}

/**
 * Probes the Go service: readiness first, then the public settings that carry
 * the backend version. The origin is never echoed back to the caller.
 */
export async function probeBackend(
  origin: string,
  fetchImpl: typeof fetch = fetch,
  timeoutMs: number = DEFAULT_PROBE_TIMEOUT_MS
): Promise<BackendProbe> {
  const started = Date.now()
  const readiness = await withTimeout(
    (signal) => fetchImpl(`${origin}/readyz`, { signal, redirect: 'manual', cache: 'no-store' }),
    timeoutMs
  )
  if (!readiness.ok) return { ...emptyProbe(readiness.error), latency_ms: Date.now() - started }

  const latency = Date.now() - started
  const ready = readiness.value.status === 200

  const versionProbe = await withTimeout(
    async (signal) => {
      const res = await fetchImpl(`${origin}/api/v1/settings/public`, {
        signal,
        redirect: 'manual',
        cache: 'no-store',
      })
      if (!res.ok) return null
      const payload = (await res.json()) as { data?: { version?: unknown } }
      const version = payload?.data?.version
      return typeof version === 'string' && version.trim() ? version.trim() : null
    },
    timeoutMs
  )

  return {
    reachable: true,
    ready,
    version: versionProbe.ok ? versionProbe.value : null,
    latency_ms: latency,
    error: ready ? null : 'http_error',
  }
}

interface CacheEntry {
  key: string
  at: number
  probe: BackendProbe
}

let cache: CacheEntry | null = null

/** Cached probe so a 1s health poller cannot hammer the Go service. */
export async function probeBackendCached(
  origin: string,
  fetchImpl: typeof fetch = fetch,
  ttlMs: number = DEFAULT_CACHE_TTL_MS,
  now: number = Date.now()
): Promise<BackendProbe> {
  if (cache && cache.key === origin && now - cache.at < ttlMs) return cache.probe
  const probe = await probeBackend(origin, fetchImpl)
  cache = { key: origin, at: now, probe }
  return probe
}

export function resetHealthCache(): void {
  cache = null
}
