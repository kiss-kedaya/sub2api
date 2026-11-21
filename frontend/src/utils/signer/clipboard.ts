/**
 * Sensitive clipboard handling for one-time shard material.
 *
 * A one-time share must not stay on the clipboard. Copying prefers
 * `navigator.clipboard` (secure contexts), falls back to the legacy
 * `document.execCommand('copy')` gesture path, and otherwise reports "manual"
 * so the UI can tell the operator to write it down by hand. After a successful
 * copy the clipboard is cleared best-effort; failure to clear is not an error
 * we can observe, hence "best effort".
 */

export type ClipboardCopyMode = 'clipboard' | 'execCommand' | 'manual'

export interface ClipboardCopyResult {
  ok: boolean
  mode: ClipboardCopyMode
}

export interface SensitiveCopyHandle {
  result: ClipboardCopyResult
  /** Schedule the best-effort clipboard wipe; returns a cancel function. */
  scheduleClear(delayMs?: number): () => void
  clearNow(): void
}

function clipboardApi(): Clipboard | null {
  if (typeof navigator === 'undefined') return null
  return navigator.clipboard && typeof navigator.clipboard.writeText === 'function' ? navigator.clipboard : null
}

export function execCommandCopy(text: string): boolean {
  if (typeof document === 'undefined' || typeof document.execCommand !== 'function') return false
  const textarea = document.createElement('textarea')
  textarea.value = text
  textarea.setAttribute('readonly', 'true')
  textarea.setAttribute('aria-hidden', 'true')
  textarea.style.cssText = 'position:fixed;left:0;top:0;width:1px;height:1px;opacity:0;pointer-events:none'
  document.body.appendChild(textarea)
  textarea.focus({ preventScroll: true })
  textarea.select()
  textarea.setSelectionRange(0, textarea.value.length)
  try {
    return document.execCommand('copy')
  } catch {
    return false
  } finally {
    document.body.removeChild(textarea)
  }
}

export async function copyText(text: string): Promise<ClipboardCopyResult> {
  if (!text) return { ok: false, mode: 'manual' }
  const api = clipboardApi()
  if (api) {
    try {
      await api.writeText(text)
      return { ok: true, mode: 'clipboard' }
    } catch {
      /* Permission denied or feature policy; try the gesture fallback. */
    }
  }
  return execCommandCopy(text) ? { ok: true, mode: 'execCommand' } : { ok: false, mode: 'manual' }
}

export function clearClipboardBestEffort(): void {
  const api = clipboardApi()
  if (api) {
    void api.writeText('').catch(() => undefined)
    return
  }
  // No Clipboard API: overwrite through the same textarea path, best effort.
  try {
    execCommandCopy('')
  } catch {
    /* ignore */
  }
}

let pendingClear: ReturnType<typeof setTimeout> | null = null

function cancelPendingClear(): void {
  if (pendingClear) {
    clearTimeout(pendingClear)
    pendingClear = null
  }
}

/** Copy and arm a clipboard wipe; the caller shows the "cleared" hint. */
export async function copySensitiveText(text: string): Promise<SensitiveCopyHandle> {
  const result = await copyText(text)
  return {
    result,
    scheduleClear(delayMs = 30_000): () => void {
      cancelPendingClear()
      pendingClear = setTimeout(() => {
        pendingClear = null
        clearClipboardBestEffort()
      }, delayMs)
      return cancelPendingClear
    },
    clearNow(): void {
      cancelPendingClear()
      clearClipboardBestEffort()
    }
  }
}

/** Test hook: drop the pending timer without touching the clipboard. */
export function __resetClipboardSchedulerForTests(): void {
  cancelPendingClear()
}
