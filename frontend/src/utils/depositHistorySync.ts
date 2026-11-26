import type { DepositRecord } from '@/api/crypto'

/** Canonical transaction identifier for an on-chain deposit record. */
export function depositRecordKey(row: Pick<DepositRecord, 'chain' | 'tx_hash'>): string {
  return `${row.chain}:${row.tx_hash}`
}

/**
 * Per-transfer base identity for list keying and diffing.
 *
 * A single on-chain transaction (`tx_hash`) can emit multiple transfer logs
 * (for example different tokens or amounts in a batch transfer). Including
 * `token` and `amount` keeps distinct transfers in the same transaction
 * independently identifiable without key collisions.
 */
export function depositTransferIdentity(
  row: Pick<DepositRecord, 'chain' | 'tx_hash' | 'token' | 'amount'>
): string {
  return `${row.chain}:${row.tx_hash}:${row.token}:${row.amount}`
}

/**
 * Compares two deposit rows across all user-visible ledger fields.
 *
 * `created_at` is treated as immutable once present: in production the database
 * timestamp never changes for an existing tx_hash, while local preview fixtures
 * synthesize relative timestamps on every poll. Checking presence (empty vs
 * populated) still surfaces newly backfilled timestamps without causing noisy
 * no-op re-renders on unchanged rows.
 */
export function isSameDepositRecord(a: DepositRecord, b: DepositRecord): boolean {
  return (
    a.chain === b.chain &&
    a.tx_hash === b.tx_hash &&
    a.token === b.token &&
    a.amount === b.amount &&
    a.status === b.status &&
    (a.reason ?? '') === (b.reason ?? '') &&
    (a.revoke_reason ?? '') === (b.revoke_reason ?? '') &&
    Object.is(a.credited, b.credited) &&
    a.credited_known === b.credited_known &&
    a.confirmations === b.confirmations &&
    a.required_confirmations === b.required_confirmations &&
    a.block_number === b.block_number &&
    a.provisional === b.provisional &&
    (a.credited_at ?? '') === (b.credited_at ?? '') &&
    (a.revoked_at ?? '') === (b.revoked_at ?? '') &&
    Boolean(a.created_at) === Boolean(b.created_at)
  )
}

/** Returns true when two deposit history snapshots contain identical rows in the same order. */
export function isSameDepositHistory(
  previous: readonly DepositRecord[],
  next: readonly DepositRecord[]
): boolean {
  if (previous.length !== next.length) return false
  for (let i = 0; i < previous.length; i++) {
    if (!isSameDepositRecord(previous[i], next[i])) return false
  }
  return true
}

/**
 * Assigns a stable, unique list key to each row so `<TransitionGroup>` never
 * receives duplicate keys even when a transaction contains multiple transfers.
 *
 * Because deposit history is ordered newest-first (`ORDER BY id DESC`),
 * duplicate occurrences are numbered from oldest to newest (right-to-left).
 * Prepending a new transfer with the same identity therefore keeps the older
 * row's key intact so its DOM node glides down cleanly instead of being reused.
 */
export function uniqueDepositRowKeys(
  rows: readonly Pick<DepositRecord, 'chain' | 'tx_hash' | 'token' | 'amount'>[]
): string[] {
  const counts = new Map<string, number>()
  const keys = new Array<string>(rows.length)
  for (let i = rows.length - 1; i >= 0; i--) {
    const base = depositTransferIdentity(rows[i])
    const count = (counts.get(base) ?? 0) + 1
    counts.set(base, count)
    keys[i] = count === 1 ? base : `${base}#${count}`
  }
  return keys
}

/**
 * Returns the stable row keys of records present in `next` that did not exist
 * in `previous`, preserving `next` order. Correctly detects additional transfers
 * sharing the same `(chain, tx_hash)`.
 */
export function findNewDepositKeys(
  previous: readonly DepositRecord[],
  next: readonly DepositRecord[]
): string[] {
  const seen = new Set(uniqueDepositRowKeys(previous))
  const nextKeys = uniqueDepositRowKeys(next)
  const added: string[] = []
  for (const key of nextKeys) {
    if (!seen.has(key)) added.push(key)
  }
  return added
}
