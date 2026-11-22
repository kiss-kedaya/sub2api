/**
 * Instant-credit revocation copy for chain deposits.
 *
 * The crypto scanner writes machine reason codes into
 * `deposit_transactions.revoke_reason` (see backend/internal/cryptopay/scanner/reconcile.go).
 * They must never surface to users as raw strings: this module maps the known
 * codes to i18n keys and keeps an explicit fallback for codes introduced by a
 * newer backend, so an unknown code renders as a readable sentence instead of
 * `reorg_something_new`.
 */

export interface DepositRevokeReasonCopy {
  /** i18n key under the `deposit` namespace. */
  key: string
  /** Named params for the i18n message (absent when the copy has no placeholders). */
  params?: Record<string, string>
}

const KNOWN_REVOKE_REASON_KEYS: Record<string, string> = {
  // Block that contained the credit was reorganized away.
  reorg_noncanonical_block: 'deposit.revokeReasonReorgNoncanonical',
  // The receipt the credit relied on disappeared.
  reorg_receipt_missing: 'deposit.revokeReasonReceiptMissing',
  // The transaction reverted on-chain after being credited provisionally.
  reorg_receipt_reverted: 'deposit.revokeReasonReceiptReverted'
}

export function depositRevokeReasonCopy(code: string | null | undefined): DepositRevokeReasonCopy {
  const normalized = (code ?? '').trim().toLowerCase()
  const key = KNOWN_REVOKE_REASON_KEYS[normalized]
  if (key) return { key }
  return {
    key: 'deposit.revokeReasonGeneric',
    params: { code: normalized || 'unknown' }
  }
}
