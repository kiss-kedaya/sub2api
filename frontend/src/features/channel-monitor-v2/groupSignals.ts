/**
 * Freshness / rate-change signals rendered on the user-facing channel status board.
 *
 * Contracts (see `docs` hand-off note in the PR description):
 * - `created_at` is already part of the group payload returned by `/groups/available`.
 * - `rate_multiplier_changed_at` + `previous_rate_multiplier` are optional additive
 *   fields the backend still has to return. Until they exist the rate-change arrow
 *   stays hidden: `updated_at` is deliberately NOT used because it also moves for
 *   unrelated group edits (name, description, limits, ...).
 * - `previous_rate_multiplier` and the current rate must both be the *effective*
 *   multiplier for the requesting user (group rate or the user-specific override).
 */

export const GROUP_SIGNAL_WINDOW_MS = 24 * 60 * 60 * 1000

/** Tolerated clock skew so a just-created row is not hidden by client/server drift. */
const FUTURE_SKEW_MS = 5 * 60 * 1000

export interface GroupSignalSource {
  createdAt?: string | null
  rateMultiplier?: number | null
  rateMultiplierChangedAt?: string | null
  previousRateMultiplier?: number | null
}

export interface GroupRateChange {
  direction: 'up' | 'down'
  previous: number
  current: number
  delta: number
  changedAt: string
}

function parseTimestamp(value: string | null | undefined): number | null {
  if (!value) return null
  const parsed = Date.parse(value)
  return Number.isFinite(parsed) ? parsed : null
}

/** True when `value` is inside the trailing `windowMs` (future values up to the skew allowance count too). */
export function isRecentTimestamp(
  value: string | null | undefined,
  now: number,
  windowMs: number = GROUP_SIGNAL_WINDOW_MS,
): boolean {
  const timestamp = parseTimestamp(value)
  if (timestamp == null) return false
  return timestamp >= now - windowMs && timestamp <= now + FUTURE_SKEW_MS
}

/** A group counts as new when it was created inside the trailing 24h window. */
export function isNewGroup(createdAt: string | null | undefined, now: number): boolean {
  return isRecentTimestamp(createdAt, now)
}

/**
 * Resolve a rate change worth surfacing.
 * Returns null when the change is missing, stale, or effectively unchanged.
 */
export function resolveRateChange(source: GroupSignalSource | null | undefined, now: number): GroupRateChange | null {
  if (!source) return null
  const changedAt = source.rateMultiplierChangedAt
  if (!isRecentTimestamp(changedAt, now)) return null
  const current = source.rateMultiplier
  const previous = source.previousRateMultiplier
  if (typeof current !== 'number' || !Number.isFinite(current)) return null
  if (typeof previous !== 'number' || !Number.isFinite(previous)) return null
  const delta = current - previous
  // Two-decimal display granularity: a difference that rounds away is noise, not a change.
  if (Math.abs(delta) < 0.005) return null
  return {
    direction: delta > 0 ? 'up' : 'down',
    previous,
    current,
    delta,
    changedAt: changedAt as string,
  }
}

/** Signed multiplier delta without a trailing `x`, e.g. `+0.03` / `-0.12`. */
export function formatRateDelta(delta: number): string {
  const abs = Math.abs(delta)
  const digits = abs >= 0.01 ? 2 : 3
  const sign = delta > 0 ? '+' : '-'
  return `${sign}${abs.toFixed(digits)}`
}
