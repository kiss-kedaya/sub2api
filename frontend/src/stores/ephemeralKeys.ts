/**
 * Ephemeral API key plaintexts.
 *
 * A key created or rotated in this tab stays readable here until the page is
 * refreshed, the tab is closed or the user signs out. This is deliberately a
 * plain in-memory Map:
 *  - never persisted to localStorage / sessionStorage / IndexedDB
 *  - never sent to the server
 *  - cleared on logout (see stores/auth.ts clearAuth)
 *
 * The list uses it to decide whether a row can show the full key + copy button
 * or must fall back to the server-side mask / password decryption.
 */

import { defineStore } from 'pinia'
import { reactive, ref } from 'vue'

export const useEphemeralKeyStore = defineStore('ephemeralApiKeys', () => {
  // Reactive Map: components reading `recall(id)` re-render when a key arrives.
  const plaintextById = reactive(new Map<number, string>())
  const activeKeyId = ref<number | null>(null)
  const pastedPlaintext = ref('')
  const autoFillSuppressed = ref(false)

  /** Plaintext for this key id, or undefined when only the mask is available. */
  const recall = (id: number): string | undefined => plaintextById.get(id)

  const has = (id: number): boolean => plaintextById.has(id)

  const suppressAutoFill = (suppressed: boolean): void => {
    autoFillSuppressed.value = suppressed
  }

  const setActiveKey = (id: number | null): void => {
    activeKeyId.value = id
    if (id != null) {
      autoFillSuppressed.value = false
    }
  }

  const setPastedPlaintext = (plaintext: string): void => {
    const trimmed = plaintext.trim()
    pastedPlaintext.value = trimmed
    if (trimmed) {
      autoFillSuppressed.value = false
    }
  }

  /** Return the preferred active plaintext key in memory, or the most recently remembered one. */
  const recallLatest = (): { id: number; secret: string } | null => {
    if (activeKeyId.value != null) {
      const secret = plaintextById.get(activeKeyId.value)
      if (secret) return { id: activeKeyId.value, secret }
    }
    const entries = Array.from(plaintextById.entries())
    const last = entries[entries.length - 1]
    return last ? { id: last[0], secret: last[1] } : null
  }

  /** Store the plaintext right after creation / rotation (overwrites on rotate). */
  const remember = (id: number, plaintext: string): void => {
    if (!Number.isFinite(id) || !plaintext) return
    plaintextById.set(id, plaintext)
    activeKeyId.value = id
    autoFillSuppressed.value = false
  }

  /** Drop one key (e.g. deleted) without touching the rest. */
  const forget = (id: number): void => {
    plaintextById.delete(id)
    if (activeKeyId.value === id) {
      activeKeyId.value = null
    }
  }

  /** Refresh / logout leaves no plaintext behind. */
  const clear = (): void => {
    plaintextById.clear()
    activeKeyId.value = null
    pastedPlaintext.value = ''
    autoFillSuppressed.value = false
  }

  return {
    plaintextById,
    activeKeyId,
    pastedPlaintext,
    autoFillSuppressed,
    recall,
    has,
    suppressAutoFill,
    setActiveKey,
    setPastedPlaintext,
    recallLatest,
    remember,
    forget,
    clear,
  }
})