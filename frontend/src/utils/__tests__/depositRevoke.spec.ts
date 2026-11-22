import { describe, expect, it } from 'vitest'
import { depositRevokeReasonCopy } from '../depositRevoke'

describe('depositRevokeReasonCopy', () => {
  it.each([
    ['reorg_noncanonical_block', 'deposit.revokeReasonReorgNoncanonical'],
    ['reorg_receipt_missing', 'deposit.revokeReasonReceiptMissing'],
    ['reorg_receipt_reverted', 'deposit.revokeReasonReceiptReverted'],
    // The backend writes lowercase codes; be tolerant of casing/whitespace.
    [' Reorg_Receipt_Missing ', 'deposit.revokeReasonReceiptMissing']
  ])('maps %s to %s', (code, key) => {
    expect(depositRevokeReasonCopy(code)).toEqual({ key })
  })

  it('falls back to a readable sentence with the raw code for unknown reasons', () => {
    expect(depositRevokeReasonCopy('reorg_future_reason')).toEqual({
      key: 'deposit.revokeReasonGeneric',
      params: { code: 'reorg_future_reason' }
    })
  })

  it('never leaves the reason parameter empty', () => {
    expect(depositRevokeReasonCopy('')).toEqual({
      key: 'deposit.revokeReasonGeneric',
      params: { code: 'unknown' }
    })
    expect(depositRevokeReasonCopy(undefined)).toEqual({
      key: 'deposit.revokeReasonGeneric',
      params: { code: 'unknown' }
    })
  })
})
