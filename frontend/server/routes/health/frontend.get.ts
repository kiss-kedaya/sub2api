import {
  buildFrontendHealth,
  probeBackendCached,
  type HealthEnv,
} from '../../utils/health'
import { isTrustedPeer } from '../../utils/proxyPolicy'

/**
 * Readiness for the SSR service.
 *
 * - reports both artifact versions so a backend-only or SSR-only rollout is
 *   visible instead of silently skewing the API contract;
 * - never echoes the private backend origin or the session secret;
 * - `?live=1` answers a plain liveness probe without touching the backend.
 */
export default defineEventHandler(async (event) => {
  setResponseHeader(event, 'cache-control', 'no-store, no-cache, must-revalidate, private')

  const query = getQuery(event)
  if (query.live === '1' || query.live === 'true') {
    return { status: 'ok', renderer: 'nuxt-ssr', contract: 1 }
  }

  const runtime = useRuntimeConfig(event)
  const origin = String(runtime.backendOrigin || '')
  const env: HealthEnv = {
    ssrVersion: String(runtime.ssrVersion || process.env.NUXT_SSR_VERSION || '0.0.0-ssr'),
    commit: String(runtime.ssrCommit || process.env.NUXT_SSR_COMMIT || '') || null,
    builtAt: String(runtime.ssrBuiltAt || process.env.NUXT_SSR_BUILT_AT || '') || null,
    expectedBackendVersion:
      String(runtime.expectedBackendVersion || process.env.NUXT_EXPECTED_BACKEND_VERSION || '') || null,
    sessionConfigured: Boolean(String(runtime.sessionPassword || '')),
  }

  const probe = origin ? await probeBackendCached(origin) : null
  const payload = buildFrontendHealth(env, probe)
  setResponseStatus(event, payload.status === 'not_ready' ? 503 : 200)

  // Detailed commit/build diagnostics stay available to local operators only;
  // the public probe returns readiness and versions without exposing checkout
  // identity or configuration warnings.
  const peer = event.node.req.socket?.remoteAddress || ''
  if (!isTrustedPeer(peer)) {
    return {
      status: payload.status,
      renderer: payload.renderer,
      contract: payload.contract,
      version: payload.version,
      backend: payload.backend,
      checked_at: payload.checked_at,
    }
  }

  return payload
})
