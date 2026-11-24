/**
 * What the balance chip in the header shows while the number is being refreshed.
 *
 * The auth store reports the truth: a request is in flight (`loading`), the last one failed so the number may
 * be stale (`failed`), or it is current (`fresh`). A request to /auth/me usually finishes in well under a
 * tenth of a second, which would make the "flash" invisible. So the flash is kept on screen for whole cycles
 * (at least one): it ends exactly where the CSS animation of the flash is at its quietest, which is also where
 * the slow breathing of the `fresh` state starts, so the hand-over has no jump.
 *
 * FLASH_CYCLE_MS is the length of one flash cycle. The template passes the same number to the stylesheet
 * (`--balance-flash`), so the two cannot drift apart.
 */
import { onScopeDispose, ref, watch, type Ref } from 'vue'

export type BalanceSyncState = 'loading' | 'failed' | 'fresh'

export const FLASH_CYCLE_MS = 700

export function useBalanceSyncIndicator(source: () => BalanceSyncState): Readonly<Ref<BalanceSyncState>> {
  const shown = ref<BalanceSyncState>(source())
  // When the flash began; the CSS animation restarts together with the `loading` state.
  let loadingSince = shown.value === 'loading' ? Date.now() : 0
  let holdTimer: ReturnType<typeof setTimeout> | null = null

  const clearHold = () => {
    if (holdTimer) {
      clearTimeout(holdTimer)
      holdTimer = null
    }
  }

  const apply = (next: BalanceSyncState) => {
    clearHold()
    if (next === 'loading') {
      // A request that starts while the flash is still on continues the same animation.
      if (shown.value !== 'loading') loadingSince = Date.now()
      shown.value = 'loading'
      return
    }
    if (shown.value === 'loading') {
      const elapsed = Date.now() - loadingSince
      const remaining = Math.max(1, Math.ceil(elapsed / FLASH_CYCLE_MS)) * FLASH_CYCLE_MS - elapsed
      if (remaining > 0) {
        // Every change of the state comes through here and replaces this timer, so `next` is always current.
        holdTimer = setTimeout(() => {
          holdTimer = null
          shown.value = next
        }, remaining)
        return
      }
    }
    shown.value = next
  }

  watch(source, apply)
  onScopeDispose(clearHold)

  return shown
}
