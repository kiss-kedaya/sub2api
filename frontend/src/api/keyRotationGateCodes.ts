/**
 * Key-rotation gate constants shared by the HTTP client and the API module.
 *
 * Kept in a dependency-free module so `api/client.ts` and
 * `api/keyRotationGate.ts` can both import them without a cycle.
 */

/** Machine-readable code returned by every key-rotation-gated endpoint. */
export const KEY_ROTATION_REQUIRED_CODE = 'KEY_ROTATION_REQUIRED'
/** Console route that lets the user rotate a key / add an IP allowlist. */
export const KEY_ROTATION_REMEDIATION_PATH = '/keys'
/** Window event broadcast by the HTTP client on a gated 403 response. */
export const KEY_ROTATION_REQUIRED_EVENT = 'key-rotation-required'

/**
 * True when a backend error payload belongs to the key-rotation gate.
 * Accepts both the `reason` field and the `metadata.error_code` copy.
 */
export function isKeyRotationRequiredPayload(payload: unknown): boolean {
  if (!payload || typeof payload !== 'object') return false
  const candidate = payload as { reason?: unknown; code?: unknown; metadata?: unknown }
  if (candidate.reason === KEY_ROTATION_REQUIRED_CODE) return true
  if (candidate.code === KEY_ROTATION_REQUIRED_CODE) return true
  const metadata = candidate.metadata
  if (metadata && typeof metadata === 'object') {
    return (metadata as Record<string, unknown>).error_code === KEY_ROTATION_REQUIRED_CODE
  }
  return false
}
