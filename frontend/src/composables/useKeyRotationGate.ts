import { computed, onMounted, reactive } from 'vue'

import {
  KEY_ROTATION_REQUIRED_EVENT,
  keyRotationGateAPI,
  type KeyRotationGateStatus,
} from '@/api/keyRotationGate'

/**
 * Shared key-rotation gate state.
 *
 * Several pages (redeem, recharge, crypto deposit) must show the same
 * remediation card. The state lives at module scope so a single status read
 * serves every page in the session, and the HTTP client's global
 * `key-rotation-required` event keeps it accurate even when a page only finds
 * out through a 403.
 */
export interface KeyRotationGateState {
  /** True once a status read (successful or not) has completed. */
  checked: boolean
  loading: boolean
  /** True while redeem / recharge / crypto deposit must stay blocked. */
  required: boolean
  status: KeyRotationGateStatus | null
}

const state = reactive<KeyRotationGateState>({
  checked: false,
  loading: false,
  required: false,
  status: null,
})

let inflight: Promise<void> | null = null
let listenerBound = false

function bindBlockedEvent(): void {
  if (listenerBound || typeof window === 'undefined') return
  listenerBound = true
  window.addEventListener(KEY_ROTATION_REQUIRED_EVENT, markKeyRotationRequired)
}

/** Mark the user as blocked without a network round-trip. */
export function markKeyRotationRequired(): void {
  state.required = true
}

/** Current gate state (read-only use; specs and diagnostics rely on it). */
export function keyRotationGateState(): Readonly<KeyRotationGateState> {
  return state
}

/**
 * Forget the cached status. Call on logout / account switch so a block never
 * follows a different user in the same browser tab.
 */
export function resetKeyRotationGate(): void {
  state.checked = false
  state.loading = false
  state.required = false
  state.status = null
  inflight = null
}

/**
 * Read the gate status once (or force a refresh). Failures are ignored: a
 * status read must never break the page, and a real blocked request still
 * arrives through the `key-rotation-required` event.
 */
export async function ensureKeyRotationGate(force = false): Promise<void> {
  if (typeof window === 'undefined') return
  if (!force && state.checked) return
  if (inflight) return inflight
  state.loading = true
  inflight = (async () => {
    try {
      const status = await keyRotationGateAPI.getStatus()
      state.status = status
      state.required = Boolean(status?.required)
    } catch {
      // Keep the previous decision; the blocked-request event will correct it.
    } finally {
      state.checked = true
      state.loading = false
      inflight = null
    }
  })()
  return inflight
}

/**
 * Page-level helper: binds the global blocked event and refreshes the status on
 * mount. Mounting is exactly when the user navigates to a gated page, so the
 * refresh picks up a remediation performed on the API keys page.
 */
export function useKeyRotationGate() {
  bindBlockedEvent()
  onMounted(() => {
    void ensureKeyRotationGate(true)
  })
  return {
    gateState: state,
    required: computed(() => state.required),
    status: computed(() => state.status),
    refresh: () => ensureKeyRotationGate(true),
  }
}
