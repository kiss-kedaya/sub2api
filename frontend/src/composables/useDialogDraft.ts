import { getCurrentScope, onScopeDispose, ref, watch, type Ref } from 'vue'

/**
 * Draft autosave for dialogs that dismiss on an accidental outside click.
 *
 * The red lines come first, so the API is whitelist-only:
 * - Callers list the fields that may leave the tab. Anything else never reaches storage,
 *   which is how passwords, API keys, tokens, cookies, TOTP codes and pasted credential
 *   files stay out of drafts.
 * - Storage is `sessionStorage`, so a closed tab drops every draft.
 * - The key carries the current user id, so another account on the same browser cannot
 *   see these drafts.
 * - Edit dialogs pass a `signature` (usually a version or `updated_at`). A draft whose
 *   signature no longer matches the object on screen is thrown away instead of
 *   overwriting newer data.
 *
 * The store is only touched inside try/catch: private-mode and disabled storage make
 * every method a silent no-op rather than an error.
 *
 * This module deliberately imports nothing from the app: the auth store registers the
 * user-id resolver instead of being reached for from here, so mounting a dialog in a spec
 * never drags in the API client and the whole i18n bundle.
 */

const STORAGE_PREFIX = 'sub2api:dialog-draft:v1'
const THROTTLE_MS = 300

export type DialogDraftValue = string | number | boolean | null
type DraftBag = Record<string, unknown>

export interface DialogDraftOptions<T extends object> {
  /** Stable identity of this dialog; the current user id is prefixed automatically. */
  key: () => string
  /** The only state fields allowed to be persisted. */
  fields: readonly (keyof T & string)[]
  /** The live form object the dialog edits. */
  state: T
  /** Whether the dialog is open: drafts are read on open and written on close. */
  open: () => boolean
  /** Version marker of the edited object (for example a version counter or `updated_at`). */
  signature?: () => string | number | null | undefined
  /** Explicit user id, mainly for tests; defaults to the auth store. */
  userId?: () => string | number | null | undefined
  /** Explicit storage, mainly for tests; defaults to `window.sessionStorage`. */
  storage?: () => Storage | null
}

export interface DialogDraft {
  /** True while a draft has been applied, so the dialog can show its restored notice. */
  restored: Ref<boolean>
  /** Read the stored draft into the state object. Returns whether anything was applied. */
  restore: () => boolean
  /** Write the whitelisted fields now. */
  save: () => void
  /** Drop the stored draft and hide the notice. */
  discard: () => void
  /** Call before closing after a successful submit so the draft is cleared, not kept. */
  markSubmitted: () => void
}

/**
 * The signed-in user id, registered once by the auth store (see `stores/auth.ts`). Drafts
 * are not written without it: a shared key would let one account read another's form.
 */
let userIdResolver: (() => string | number | null | undefined) | null = null

export function setDialogDraftUserIdResolver(
  resolver: (() => string | number | null | undefined) | null
): void {
  userIdResolver = resolver
}

export function currentDialogDraftUserId(): string | number | null {
  if (!userIdResolver) return null
  try {
    return userIdResolver() ?? null
  } catch {
    return null
  }
}

function defaultStorage(): Storage | null {
  try {
    if (typeof window === 'undefined') return null
    return window.sessionStorage ?? null
  } catch {
    return null
  }
}

function isDraftValue(value: unknown): value is DialogDraftValue {
  return value === null || typeof value === 'string' || typeof value === 'number' || typeof value === 'boolean'
}

function hasIdentity(userId: unknown): userId is string | number {
  return userId !== null && userId !== undefined && userId !== ''
}

function storageKey(userId: string | number, key: string): string {
  return `${STORAGE_PREFIX}:${userId}:${key}`
}

interface StoredDraft {
  signature: string | null
  fields: Record<string, DialogDraftValue>
}

/** Remove every draft belonging to one user (used when signing out). */
export function clearUserDialogDrafts(
  userId: string | number | null | undefined,
  storage: Storage | null = defaultStorage()
): void {
  if (!hasIdentity(userId)) return
  if (!storage) return
  const prefix = `${STORAGE_PREFIX}:${userId}:`
  try {
    const doomed: string[] = []
    for (let index = 0; index < storage.length; index += 1) {
      const key = storage.key(index)
      if (key && key.startsWith(prefix)) doomed.push(key)
    }
    for (const key of doomed) storage.removeItem(key)
  } catch {
    // Storage disabled mid-session: nothing to clean up.
  }
}

export function useDialogDraft<T extends object>(options: DialogDraftOptions<T>): DialogDraft {
  const restored = ref(false)
  let submitted = false
  /**
   * Field values that must not be persisted: the state right after a discard or a submit.
   * Comparing values instead of using a time window keeps this deterministic — a timer
   * would either swallow real typing or let the component's own reset slip through,
   * depending on how fast the user (or the test) gets back to the form.
   */
  let baseline: Record<string, DialogDraftValue> | null = null
  let throttleTimer: ReturnType<typeof setTimeout> | null = null

  const state = options.state as unknown as DraftBag
  const resolveUserId = () => (options.userId ? options.userId() : currentDialogDraftUserId())
  const resolveStorage = () => (options.storage ? options.storage() : defaultStorage())

  const signatureText = (): string | null => {
    const value = options.signature?.()
    return value === null || value === undefined ? null : String(value)
  }

  const cancelThrottle = () => {
    if (throttleTimer) {
      clearTimeout(throttleTimer)
      throttleTimer = null
    }
  }

  function readStored(): StoredDraft | null {
    const userId = resolveUserId()
    if (!hasIdentity(userId)) return null
    const storage = resolveStorage()
    if (!storage) return null
    try {
      const raw = storage.getItem(storageKey(userId, options.key()))
      if (!raw) return null
      const parsed = JSON.parse(raw) as Partial<StoredDraft> | null
      const fields = parsed?.fields
      if (!fields || typeof fields !== 'object') return null
      const clean: Record<string, DialogDraftValue> = {}
      for (const field of options.fields) {
        const value = (fields as DraftBag)[field]
        if (isDraftValue(value)) clean[field] = value
      }
      if (!Object.keys(clean).length) return null
      return {
        signature: typeof parsed?.signature === 'string' ? parsed.signature : null,
        fields: clean
      }
    } catch {
      return null
    }
  }

  function drop(): void {
    restored.value = false
    const userId = resolveUserId()
    if (!hasIdentity(userId)) return
    const storage = resolveStorage()
    if (!storage) return
    try {
      storage.removeItem(storageKey(userId, options.key()))
    } catch {
      // Storage disabled: the in-memory notice still clears.
    }
  }

  function save(): void {
    const userId = resolveUserId()
    // Without an identity the key cannot separate accounts, so nothing is written.
    if (!hasIdentity(userId)) return
    const storage = resolveStorage()
    if (!storage) return
    const fields: Record<string, DialogDraftValue> = {}
    for (const field of options.fields) {
      const value = state[field]
      if (isDraftValue(value)) fields[field] = value
    }
    if (!Object.keys(fields).length) {
      drop()
      return
    }
    try {
      storage.setItem(storageKey(userId, options.key()), JSON.stringify({ signature: signatureText(), fields }))
    } catch {
      // Quota or disabled storage: autosave is best effort.
    }
  }

  function restore(): boolean {
    const stored = readStored()
    if (!stored) {
      restored.value = false
      return false
    }
    // The object moved on since the draft was written: drop the stale values.
    if (stored.signature !== signatureText()) {
      drop()
      return false
    }
    let changed = false
    for (const field of options.fields) {
      if (!(field in stored.fields)) continue
      const value = stored.fields[field]
      if (state[field] !== value) changed = true
      state[field] = value
    }
    // The notice reports "your saved input is in here", not "we had to overwrite you": a
    // dialog whose component kept its values still restored a draft, and the user needs the
    // discard action either way.
    restored.value = true
    return changed
  }

  /** Snapshot of the whitelisted fields, used to tell "reset by us" from "typed by the user". */
  const snapshotFields = (): Record<string, DialogDraftValue> => {
    const snapshot: Record<string, DialogDraftValue> = {}
    for (const field of options.fields) {
      const value = state[field]
      if (isDraftValue(value)) snapshot[field] = value
    }
    return snapshot
  }

  /** True while the state still holds exactly the values we told it to forget. */
  const matchesBaseline = (): boolean => {
    if (!baseline) return false
    const current = snapshotFields()
    const keys = new Set([...Object.keys(current), ...Object.keys(baseline)])
    for (const key of keys) {
      if (current[key] !== baseline[key]) return false
    }
    return true
  }

  // Guards only the throttled write, never the close-time write: an explicit discard or a
  // submit has the component reset its form right after, and those cleared values must not
  // be written back.
  const suppressStateSaves = () => {
    baseline = snapshotFields()
  }

  function markSubmitted(): void {
    submitted = true
    cancelThrottle()
    baseline = null
  }

  const scheduleSave = () => {
    if (throttleTimer) return
    throttleTimer = setTimeout(() => {
      throttleTimer = null
      save()
    }, THROTTLE_MS)
  }

  watch(
    () => options.open(),
    (isOpen, wasOpen) => {
      if (isOpen && !wasOpen) {
        submitted = false
        baseline = null
        restore()
        return
      }
      if (!isOpen && wasOpen) {
        cancelThrottle()
        const forget = submitted || matchesBaseline()
        submitted = false
        baseline = null
        // Submitted, or left at exactly the state we were told to forget: nothing to keep.
        if (forget) {
          drop()
          return
        }
        // Closed by the veil, Escape or the close button: keep what was typed.
        save()
      }
    },
    { immediate: true }
  )

  watch(
    () => options.state,
    () => {
      // The component's own reset right after discard or submit lands here. Ignore it; the
      // baseline comparison keeps this honest without a timer.
      if (matchesBaseline()) return
      baseline = null
      if (!options.open() || submitted) return
      scheduleSave()
    },
    { deep: true }
  )

  if (getCurrentScope()) onScopeDispose(cancelThrottle)

  return {
    restored,
    restore,
    save,
    markSubmitted,
    discard: () => {
      submitted = false
      cancelThrottle()
      drop()
      // Record what the caller is about to reset to, so the reset is not saved back.
      baseline = null
      queueMicrotask(suppressStateSaves)
    }
  }
}
