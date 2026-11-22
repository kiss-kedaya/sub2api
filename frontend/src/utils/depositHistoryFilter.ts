import type { DepositRecord } from '@/api/crypto'

/**
 * Deposit history filtering.
 *
 * The history endpoint returns the user's recent deposits in one page (it is not
 * paginated), so filtering happens entirely in the browser: switching a filter
 * must not re-read the API or reset the running 5s refresh. Keeping the rules
 * pure makes every combination testable without mounting the page.
 */

export const DEPOSIT_STATUS_FILTERS = ['all', 'credited', 'provisional', 'revoked', 'ignored'] as const
export type DepositStatusFilter = (typeof DEPOSIT_STATUS_FILTERS)[number]

export const DEPOSIT_TIME_RANGES = ['all', '7d', '30d', '90d'] as const
export type DepositTimeRange = (typeof DEPOSIT_TIME_RANGES)[number]

const DAY_MS = 24 * 60 * 60 * 1000

const RANGE_MS: Record<Exclude<DepositTimeRange, 'all'>, number> = {
  '7d': 7 * DAY_MS,
  '30d': 30 * DAY_MS,
  '90d': 90 * DAY_MS
}

export function isDepositStatusFilter(value: unknown): value is DepositStatusFilter {
  return DEPOSIT_STATUS_FILTERS.some(option => option === value)
}

export function isDepositTimeRange(value: unknown): value is DepositTimeRange {
  return DEPOSIT_TIME_RANGES.some(option => option === value)
}

/**
 * Status buckets are the states a user can act on. `pending` rows are still
 * visible under "all"; the list has no dedicated "confirming" bucket on purpose
 * (the live progress line above the table already covers an in-flight transfer).
 */
export function matchesDepositStatus(row: Pick<DepositRecord, 'status'>, status: DepositStatusFilter): boolean {
  return status === 'all' || row.status === status
}

/**
 * Rolling window on `created_at`. A row with an unparsable timestamp is kept:
 * hiding a deposit the user may have paid for is worse than showing one extra
 * row under a time filter, and the table still prints its raw status.
 */
export function matchesDepositTimeRange(
  row: Pick<DepositRecord, 'created_at'>,
  range: DepositTimeRange,
  now: number = Date.now()
): boolean {
  if (range === 'all') return true
  const created = Date.parse(row.created_at)
  if (!Number.isFinite(created)) return true
  return created >= now - RANGE_MS[range]
}

export function filterDepositHistory(
  rows: readonly DepositRecord[],
  status: DepositStatusFilter,
  range: DepositTimeRange,
  now: number = Date.now()
): DepositRecord[] {
  return rows.filter(row => matchesDepositStatus(row, status) && matchesDepositTimeRange(row, range, now))
}

/** True when the user has narrowed the list, so the page can show the count. */
export function isDepositFilterActive(status: DepositStatusFilter, range: DepositTimeRange): boolean {
  return status !== 'all' || range !== 'all'
}
