/**
 * Visible step-up skip notice (global, one instance per app).
 *
 * The backend shortens the step-up window to 5 minutes (same TTL as the
 * on-chain payment crypto grant) and, whenever a sensitive request skips the
 * TOTP prompt because that window is still valid, answers with:
 *
 *   X-StepUp-Skipped: 1
 *   X-StepUp-Remaining: <seconds>
 *
 * Silently skipping 2FA is not acceptable: the operator must see that a
 * sensitive operation just executed without a code and how much of the
 * grace window is left. The shared Axios client calls
 * `recordStepUpSkip(remainingSeconds)` from those response headers and the
 * bottom-right notice renders a live countdown that disappears at zero.
 *
 * Keep this module free of Vue app/i18n imports: the Axios client imports it
 * and tests mock the client graph.
 */
import { computed, readonly, ref } from 'vue'

const COUNTDOWN_INTERVAL_MS = 1000

/** Absolute epoch(ms) when the current step-up grace window expires. */
const expiresAtMs = ref(0)
const visible = ref(false)
/** Bumped on every tick so `remainingSeconds` recomputes. */
const nowMs = ref(Date.now())

let ticker: ReturnType<typeof setInterval> | null = null

function stopTicker(): void {
  if (ticker !== null) {
    clearInterval(ticker)
    ticker = null
  }
}

function ensureTicker(): void {
  if (ticker !== null) return
  ticker = setInterval(() => {
    nowMs.value = Date.now()
    if (expiresAtMs.value - nowMs.value <= 0) {
      visible.value = false
      stopTicker()
    }
  }, COUNTDOWN_INTERVAL_MS)
}

function syncVisibility(): void {
  nowMs.value = Date.now()
  const remaining = expiresAtMs.value - nowMs.value
  if (remaining <= 0) {
    visible.value = false
    stopTicker()
    return
  }
  visible.value = true
  ensureTicker()
}

/**
 * Record that a sensitive request just skipped 2FA and when the grace window
 * ends. Called from the Axios response interceptor with the value of the
 * `X-StepUp-Remaining` header. Non-positive / non-finite values are ignored:
 * the notice must never invent a window that the backend did not report.
 */
export function recordStepUpSkip(remainingSeconds: number): void {
  if (!Number.isFinite(remainingSeconds) || remainingSeconds <= 0) return
  expiresAtMs.value = Date.now() + Math.floor(remainingSeconds) * 1000
  syncVisibility()
}

/** Remaining whole seconds of the grace window (0 when none). */
export const stepUpSkipRemainingSeconds = computed(() =>
  Math.max(0, Math.ceil((expiresAtMs.value - nowMs.value) / 1000)),
)

/** True while the backend grace window is still active. */
export const stepUpSkipVisible = readonly(visible)

/** `m:ss` formatting used by the notice: 272 -> "4:32". */
export function formatStepUpSkipRemaining(totalSeconds: number): string {
  const safe = Math.max(0, Math.floor(totalSeconds))
  const minutes = Math.floor(safe / 60)
  const seconds = safe % 60
  return `${minutes}:${String(seconds).padStart(2, '0')}`
}

/** Readable reactive facade for components. */
export function useStepUpSkip() {
  return {
    visible: stepUpSkipVisible,
    remainingSeconds: stepUpSkipRemainingSeconds,
    formattedRemaining: computed(() => formatStepUpSkipRemaining(stepUpSkipRemainingSeconds.value)),
  }
}

/** Test-only reset (also used when the session changes). */
export function resetStepUpSkipForTests(): void {
  stopTicker()
  expiresAtMs.value = 0
  nowMs.value = Date.now()
  visible.value = false
}

/** Hide immediately (e.g. after logout). */
export function clearStepUpSkip(): void {
  resetStepUpSkipForTests()
}
