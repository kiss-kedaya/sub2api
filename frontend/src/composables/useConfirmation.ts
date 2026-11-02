import { reactive, readonly } from 'vue'

/**
 * Options for a single confirmation request. Every field except `message` is
 * optional; the dialog falls back to the shared confirm/cancel labels.
 */
export interface ConfirmationOptions {
  title?: string
  message: string
  confirmText?: string
  cancelText?: string
  danger?: boolean
}

interface ConfirmationState {
  open: boolean
  title: string
  message: string
  confirmText: string
  cancelText: string
  danger: boolean
}

interface PendingConfirmation {
  options: ConfirmationOptions
  resolve: (confirmed: boolean) => void
}

const state = reactive<ConfirmationState>({
  open: false,
  title: '',
  message: '',
  confirmText: '',
  cancelText: '',
  danger: true
})

const queue: PendingConfirmation[] = []
let active: PendingConfirmation | null = null
let mountedHosts = 0

function showNext() {
  const next = queue.shift()
  if (!next) {
    active = null
    state.open = false
    return
  }
  active = next
  state.title = next.options.title ?? ''
  state.message = next.options.message
  state.confirmText = next.options.confirmText ?? ''
  state.cancelText = next.options.cancelText ?? ''
  state.danger = next.options.danger ?? true
  state.open = true
}

/**
 * Ask the admin to confirm an action.
 *
 * Replaces `window.confirm` with a Nuxt UI dialog. The promise resolves with
 * `false` — the safe default — when the request is cancelled, when the route
 * changes, when the host unmounts, or when no host is mounted at all. It never
 * patches `window.confirm`, so nothing outside these call sites changes.
 */
export function confirm(options: ConfirmationOptions | string): Promise<boolean> {
  const normalized: ConfirmationOptions = typeof options === 'string' ? { message: options } : options
  if (!normalized?.message?.trim()) return Promise.resolve(false)
  if (mountedHosts === 0) return Promise.resolve(false)

  return new Promise<boolean>((resolve) => {
    queue.push({ options: normalized, resolve })
    if (!active) showNext()
  })
}

/** Resolve the dialog that is currently on screen. */
export function settleConfirmation(confirmed: boolean) {
  const current = active
  if (!current) return
  active = null
  state.open = false
  current.resolve(confirmed)
  showNext()
}

/** Resolve every outstanding request as cancelled (route change, unmount). */
export function cancelConfirmation() {
  const pending = queue.splice(0, queue.length)
  for (const entry of pending) entry.resolve(false)
  if (active) {
    const current = active
    active = null
    state.open = false
    current.resolve(false)
  }
}

/** Called by ConfirmHost on mount; returns the unregister callback. */
export function registerConfirmationHost(): () => void {
  mountedHosts += 1
  return () => {
    mountedHosts = Math.max(0, mountedHosts - 1)
    if (mountedHosts === 0) cancelConfirmation()
  }
}

export function useConfirmation() {
  return {
    state: readonly(state),
    confirm,
    settleConfirmation,
    cancelConfirmation
  }
}
