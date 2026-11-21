/**
 * Pure step-up error classification + localization.
 *
 * Kept dependency-free on purpose:
 *  - the Axios client (src/api/client.ts) imports it, so it must not pull the
 *    Vue i18n instance or Vue app state into module mocks in unit tests;
 *  - specs that `vi.mock('@/composables/useStepUp')` or `vi.mock('@/i18n')`
 *    must not break the client import graph.
 *
 * The i18n translator is registered by src/i18n/index.ts at app bootstrap
 * (`registerStepUpTranslator`). Until then — and in bare unit-test i18n
 * instances — localization returns null and callers keep their fallback.
 */

export const STEP_UP_REQUIRED = 'STEP_UP_REQUIRED'
export const STEP_UP_TOTP_NOT_ENABLED = 'STEP_UP_TOTP_NOT_ENABLED'
export const STEP_UP_ADMIN_API_KEY_FORBIDDEN = 'STEP_UP_ADMIN_API_KEY_FORBIDDEN'
export const STEP_UP_UNAVAILABLE = 'STEP_UP_UNAVAILABLE'
export const STEP_UP_CANCELLED = 'STEP_UP_CANCELLED'

/**
 * Thrown by `useStepUp.run()` / the global Axios gate when the operator
 * dismisses the TOTP dialog. Callers should treat it as "nothing was executed",
 * not as a generic failure; display copy is localized by the UI layer.
 */
export class StepUpCancelledError extends Error {
  readonly code = STEP_UP_CANCELLED
  constructor() {
    super('step-up verification cancelled by user')
    this.name = 'StepUpCancelledError'
  }
}

export function isStepUpCancelled(err: unknown): boolean {
  return err instanceof StepUpCancelledError
}

/** Extract the semantic error marker from either envelope shape (code or reason). */
export function stepUpMarkerOf(err: unknown): string {
  const e = (err ?? {}) as { code?: string | number; reason?: string }
  const candidates = [e.code, e.reason].map((v) => (typeof v === 'string' ? v : ''))
  return candidates.find((v) => v.startsWith('STEP_UP')) || ''
}

export function isStepUpRequired(err: unknown): boolean {
  return stepUpMarkerOf(err) === STEP_UP_REQUIRED
}

/** Non-retryable step-up rejections: entering a code cannot resolve them. */
export function isStepUpBlocked(err: unknown): boolean {
  const marker = stepUpMarkerOf(err)
  return marker === STEP_UP_TOTP_NOT_ENABLED || marker === STEP_UP_ADMIN_API_KEY_FORBIDDEN
}

/** Step-up service itself is unavailable (fail-closed backend) — retry later. */
export function isStepUpUnavailable(err: unknown): boolean {
  return stepUpMarkerOf(err) === STEP_UP_UNAVAILABLE
}

export function stepUpBlockReason(err: unknown): string {
  return stepUpMarkerOf(err)
}

const STEP_UP_MESSAGE_KEYS: Record<string, string> = {
  [STEP_UP_REQUIRED]: 'stepUp.required',
  [STEP_UP_TOTP_NOT_ENABLED]: 'stepUp.notEnabled',
  [STEP_UP_ADMIN_API_KEY_FORBIDDEN]: 'stepUp.adminApiKeyForbidden',
  [STEP_UP_UNAVAILABLE]: 'stepUp.unavailable',
  [STEP_UP_CANCELLED]: 'stepUp.cancelled',
}

export type StepUpTranslator = (key: string) => string | undefined

let stepUpTranslator: StepUpTranslator | null = null

/** Register the app i18n translator (called by src/i18n/index.ts at bootstrap). */
export function registerStepUpTranslator(translator: StepUpTranslator | null): void {
  stepUpTranslator = translator
}

export function stepUpMessageKey(code: string | number | undefined): string | null {
  if (code == null) return null
  return STEP_UP_MESSAGE_KEYS[String(code)] ?? null
}

/**
 * Localize a step-up error code. Returns null when the code is not step-up
 * related, when no translator is registered yet, or when the key is missing —
 * callers then keep their existing fallback text.
 */
export function localizeStepUpErrorCode(code: string | number | undefined): string | null {
  const key = stepUpMessageKey(code)
  if (!key || !stepUpTranslator) return null
  try {
    const text = stepUpTranslator(key)
    return typeof text === 'string' && text && text !== key ? text : null
  } catch {
    return null
  }
}
