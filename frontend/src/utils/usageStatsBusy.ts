/**
 * The backend caps how many usage aggregates it runs on the database at once. An aggregate that cannot get a slot
 * in time is answered `429` + `Retry-After` with this reason: nothing is broken, the page just has to ask again in
 * a moment (and must keep showing what it already has meanwhile).
 */
export const USAGE_STATS_BUSY = 'USAGE_STATS_BUSY'

const DEFAULT_RETRY_MS = 2_000
const MIN_RETRY_MS = 1_000
const MAX_RETRY_MS = 10_000

type RejectedRequest = { status?: unknown; reason?: unknown; code?: unknown; retryAfter?: unknown }

/**
 * True for the rejection the API client produces for a usage aggregate turned away by that cap. The reason is
 * what identifies it (older backends answered 503 with the same reason, so a mixed rollout behaves the same);
 * the per-user panel limit is also a 429 but carries another reason.
 */
export function isUsageStatsBusyError(error: unknown): boolean {
  if (typeof error !== 'object' || error === null) return false
  const { reason, code } = error as RejectedRequest
  return reason === USAGE_STATS_BUSY || code === USAGE_STATS_BUSY
}

/**
 * How long to wait before the one automatic retry: the server's Retry-After (whole seconds), kept within
 * 1–10 s so a missing or odd header neither hammers the API nor leaves the page waiting for minutes.
 */
export function usageStatsBusyRetryDelayMs(error: unknown): number {
  const raw = typeof error === 'object' && error !== null ? (error as RejectedRequest).retryAfter : undefined
  if (typeof raw !== 'string' && typeof raw !== 'number') return DEFAULT_RETRY_MS
  const text = String(raw).trim()
  const seconds = text === '' ? NaN : Number(text)
  if (!Number.isFinite(seconds) || seconds <= 0) return DEFAULT_RETRY_MS
  return Math.min(MAX_RETRY_MS, Math.max(MIN_RETRY_MS, Math.round(seconds * 1000)))
}
