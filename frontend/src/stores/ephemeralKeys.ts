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
import { reactive } from 'vue'

export const useEphemeralKeyStore = defineStore('ephemeralApiKeys', () => {
  // Reactive Map: components reading `recall(id)` re-render when a key arrives.
  const plaintextById = reactive(new Map<number, string>())

  /** Plaintext for this key id, or undefined when only the mask is available. */
  const recall = (id: number): string | undefined => plaintextById.get(id)

  const has = (id: number): boolean => plaintextById.has(id)

  /** Store the plaintext right after creation / rotation (overwrites on rotate). */
  const remember = (id: number, plaintext: string): void => {
    if (!Number.isFinite(id) || !plaintext) return
    plaintextById.set(id, plaintext)
  }

  /** Drop one key (e.g. deleted) without touching the rest. */
  const forget = (id: number): void => {
    plaintextById.delete(id)
  }

  /** Refresh / logout leaves no plaintext behind. */
  const clear = (): void => {
    plaintextById.clear()
  }

  return { plaintextById, recall, has, remember, forget, clear }
})