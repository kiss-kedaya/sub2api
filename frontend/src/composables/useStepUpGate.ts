/**
 * App-wide step-up gate.
 *
 * `useStepUp` is per-view; this module owns the single application-level TOTP
 * gate that the shared Axios client falls back to. Every admin API request
 * (write endpoints and step-up protected reads) goes through the shared client,
 * so an uncovered page still gets the TOTP dialog instead of a raw English
 * "This operation requires recent two-factor verification" toast.
 *
 * Behaviour:
 *  - STEP_UP_REQUIRED  -> open the global gate, wait for TOTP verification, and
 *                         retry the original request exactly once.
 *  - user cancels      -> reject with StepUpCancelledError (nothing executed).
 *  - concurrent 403s   -> share one prompt (single-flight, see useStepUp.prompt).
 *
 * The dialog host is mounted once by `<StepUpGate />` in App.vue. When no host
 * is mounted (SSR, or an API call before the app shell exists) the request is
 * rejected with the normal localized step-up error instead of hanging forever.
 */
import { useStepUp } from './useStepUp'
import { StepUpCancelledError } from '@/utils/stepUpErrors'

/** One gate for the whole app; the mounted `<StepUpGate />` renders it. */
export const stepUpGate = useStepUp()

let hostCount = 0

/**
 * Register a mounted TOTP dialog host. Returns the unregister callback.
 * Hosts are counted so nested/multiple mounts never disable the gate while at
 * least one dialog can still render.
 */
export function registerStepUpHost(): () => void {
  hostCount += 1
  let released = false
  return () => {
    if (released) return
    released = true
    hostCount = Math.max(0, hostCount - 1)
  }
}

/** True when a TOTP dialog host is available to ask the operator for a code. */
export function isStepUpHostAvailable(): boolean {
  return hostCount > 0
}

/**
 * Prompt through the application-level gate and retry the original request.
 * Throws `StepUpCancelledError` when the operator dismisses the dialog.
 */
export async function retryWithGlobalStepUp<T>(retry: () => Promise<T>): Promise<T> {
  const verified = await stepUpGate.prompt()
  if (!verified) {
    throw new StepUpCancelledError()
  }
  return retry()
}

/** Reset host bookkeeping + pending prompt. Test-only helper. */
export function resetStepUpGateForTests(): void {
  hostCount = 0
  stepUpGate.onCancel()
}
