/**
 * Page-load / background read marker for the shared Axios client.
 *
 * The 2026-11 incident: AccountsView loaded, a page-load request hit
 * `403 STEP_UP_REQUIRED`, the app-wide gate opened a TOTP dialog over the page,
 * the operator dismissed it, and every pending load failed with
 * `StepUpCancelledError` — an entire admin page unusable because of a
 * background read.
 *
 * Contract:
 *  - A request carrying `silentStepUp: true` NEVER opens the TOTP dialog.
 *    403 STEP_UP_REQUIRED rejects immediately with `{ code: 'STEP_UP_REQUIRED',
 *    silentStepUp: true }` so the caller renders a degraded placeholder
 *    ("该数据需二次验证后查看").
 *  - Only user-initiated sensitive actions (reveal credential, export, delete,
 *    batch writes, ...) may open the gate.
 *
 * Mark every request that a page fires automatically (initial load, refresh,
 * polling, prefetch, dropdown option loading). Do NOT mark:
 *  - secret-revealing reads triggered by a click
 *    (`accounts/:id/credential`, `accounts/data`, proxy export, backups,
 *    debug dumps, user/group api-keys);
 *  - write / action endpoints.
 */
import type { AxiosRequestConfig } from 'axios'

/** Spread into an axios config object: `{ ...SILENT_STEP_UP, params }`. */
export const SILENT_STEP_UP = { silentStepUp: true } as const

export type SilentStepUpConfig = typeof SILENT_STEP_UP

/** Mark an existing axios config object as a silent background read. */
export function withSilentStepUp<T extends AxiosRequestConfig>(config: T): T & SilentStepUpConfig {
  return { ...config, silentStepUp: true }
}
