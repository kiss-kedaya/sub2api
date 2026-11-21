/**
 * Step-up (sudo) 2FA composable.
 *
 * Wraps a sensitive admin action so that when the backend responds with a
 * STEP_UP_REQUIRED error, the caller can prompt for a TOTP code, obtain a
 * short-lived grant, and transparently retry the original action.
 *
 * Usage in a view:
 *   const stepUp = useStepUp()
 *   async function exportData() {
 *     await stepUp.run(() => adminAPI.accounts.exportData(...))
 *   }
 *   // template: <TotpStepUpDialog :controller="stepUp" />
 *
 * Coverage model: the shared Axios client also retries `STEP_UP_REQUIRED`
 * responses through the app-wide TOTP gate (see `useStepUpGate`), so every
 * admin API call is protected even when a page forgot to call `run()`. This
 * composable stays the right tool for page-local flows that also need custom
 * cancellation/blocked UI (and for tests that mock the API layer directly).
 */
import { ref } from 'vue'
import {
  StepUpCancelledError,
  isStepUpBlocked,
  isStepUpRequired,
  isStepUpUnavailable,
  stepUpMarkerOf,
} from '@/utils/stepUpErrors'

export {
  StepUpCancelledError,
  isStepUpCancelled,
  isStepUpRequired,
  isStepUpBlocked,
  isStepUpUnavailable,
  stepUpBlockReason,
  stepUpMarkerOf,
} from '@/utils/stepUpErrors'

export type StepUpController = ReturnType<typeof useStepUp>

export function useStepUp() {
  const visible = ref(false)
  const blockedReason = ref<string>('')
  let resolver: ((ok: boolean) => void) | null = null
  /**
   * Single-flight prompt: concurrent sensitive operations (e.g. a batch save
   * that fires several requests at once) must share one TOTP dialog instead of
   * stacking prompts or overwriting the pending resolver.
   */
  let pendingPrompt: Promise<boolean> | null = null

  function settle(verified: boolean) {
    visible.value = false
    const resolve = resolver
    resolver = null
    pendingPrompt = null
    resolve?.(verified)
  }

  /** Open the TOTP dialog and resolve true once a grant is obtained. */
  function prompt(): Promise<boolean> {
    if (pendingPrompt) return pendingPrompt
    visible.value = true
    pendingPrompt = new Promise<boolean>((resolve) => {
      resolver = resolve
    })
    return pendingPrompt
  }

  function onVerified() {
    blockedReason.value = ''
    settle(true)
  }

  function onCancel() {
    settle(false)
  }

  /**
   * Run a sensitive action. On STEP_UP_REQUIRED, prompt for a TOTP code and
   * retry once. STEP_UP_TOTP_NOT_ENABLED / admin-api-key errors are surfaced
   * to the caller (they cannot be resolved by entering a code). If the user
   * cancels the prompt, a StepUpCancelledError is thrown so callers can
   * distinguish "user changed their mind" from real failures.
   */
  async function run<T>(action: () => Promise<T>): Promise<T> {
    try {
      return await action()
    } catch (err) {
      if (isStepUpBlocked(err) || isStepUpUnavailable(err)) {
        blockedReason.value = stepUpMarkerOf(err)
        throw err
      }
      if (!isStepUpRequired(err)) {
        throw err
      }
      const ok = await prompt()
      if (!ok) {
        throw new StepUpCancelledError()
      }
      // Retry once now that the session holds a step-up grant.
      return await action()
    }
  }

  return {
    visible,
    blockedReason,
    prompt,
    onVerified,
    onCancel,
    run
  }
}
