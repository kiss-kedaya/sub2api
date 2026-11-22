import type { DepositRecord } from '@/api/crypto'

/**
 * Per-deposit confirmation progress.
 *
 * The scanner records how deep a transfer is (`confirmations`) and how deep it
 * must be (`required_confirmations`). Both states that are still in flight —
 * `pending` (not credited yet) and `provisional` (credited early, can still be
 * rolled back) — show that progress so the user can tell "the scanner saw my
 * transfer and it is at block 2 of 3" from "the scanner has not seen it yet".
 *
 * When the backend omits the counters (older deployment, or a chain that
 * credits on detection) the copy degrades to a bare "confirming" line instead
 * of inventing n/m.
 */

export interface ConfirmationProgress {
  done: number
  required: number
  remaining: number
  percent: number
}

export interface DepositProgressCopy {
  /** i18n key under the `deposit` namespace. */
  key: string
  /** Named params for the i18n message (absent when the copy has no placeholders). */
  params?: Record<string, number>
}

const CONFIRMING_STATUSES = ['pending', 'provisional'] as const

/** True while the transfer is still on its way to a final credit. */
export function isConfirmingDeposit(status: string | null | undefined): boolean {
  return CONFIRMING_STATUSES.some(value => value === status)
}

/**
 * Confirmation depth for a row that is still in flight. Null when the backend
 * sends no usable counter pair: the page then shows a generic line and no bar.
 */
export function confirmationProgress(row: DepositRecord | null | undefined): ConfirmationProgress | null {
  if (!row || !isConfirmingDeposit(row.status)) return null
  const required = Math.floor(Number(row.required_confirmations))
  const confirmations = Number(row.confirmations)
  if (!Number.isFinite(required) || required <= 0 || !Number.isFinite(confirmations)) return null
  const done = Math.min(Math.max(Math.floor(confirmations), 0), required)
  return { done, required, remaining: required - done, percent: Math.round((done / required) * 100) }
}

/**
 * Copy for the progress line. `progress` must be the value returned for the
 * same row, so the wording and the numbers cannot disagree.
 */
export function depositProgressCopy(
  row: Pick<DepositRecord, 'status'> | null | undefined,
  progress: ConfirmationProgress | null
): DepositProgressCopy {
  const provisional = row?.status === 'provisional'
  if (!progress) {
    return { key: provisional ? 'deposit.provisionalConfirming' : 'deposit.confirmPending' }
  }
  if (progress.remaining > 0) {
    return {
      key: provisional ? 'deposit.provisionalProgress' : 'deposit.confirmProgress',
      params: { done: progress.done, required: progress.required, remaining: progress.remaining }
    }
  }
  return {
    key: provisional ? 'deposit.provisionalProgressDone' : 'deposit.confirmProgressDone',
    params: { done: progress.done, required: progress.required }
  }
}
