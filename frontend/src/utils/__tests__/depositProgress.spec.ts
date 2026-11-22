import { describe, expect, it } from 'vitest'
import {
  confirmationProgress, depositProgressCopy, isConfirmingDeposit,
} from '../depositProgress'

function row(status: string, overrides: Record<string, unknown> = {}) {
  return {
    chain: 'bsc', tx_hash: '0x' + 'ab'.repeat(32), token: 'USDT', amount: '10',
    status, credited: 0, created_at: '2026-01-01T00:00:00Z', ...overrides,
  }
}

describe('isConfirmingDeposit', () => {
  it.each(['pending', 'provisional'])('treats %s as in flight', (status) => {
    expect(isConfirmingDeposit(status)).toBe(true)
  })

  it.each(['credited', 'revoked', 'ignored', 'reverted', '', undefined])('does not treat %s as in flight', (status) => {
    expect(isConfirmingDeposit(status as string | undefined)).toBe(false)
  })
})

describe('confirmation progress', () => {
  it('reports the depth of a pending transfer', () => {
    expect(confirmationProgress(row('pending', { confirmations: 2, required_confirmations: 3 })))
      .toEqual({ done: 2, required: 3, remaining: 1, percent: 67 })
  })

  it('reports the depth of a provisional credit too', () => {
    expect(confirmationProgress(row('provisional', { confirmations: 1, required_confirmations: 4 })))
      .toEqual({ done: 1, required: 4, remaining: 3, percent: 25 })
  })

  it('clamps a depth beyond the target instead of printing 5/3', () => {
    expect(confirmationProgress(row('pending', { confirmations: 9, required_confirmations: 3 })))
      .toEqual({ done: 3, required: 3, remaining: 0, percent: 100 })
  })

  it.each([
    ['no counters', {}],
    ['a zero target', { confirmations: 1, required_confirmations: 0 }],
    ['a garbage counter', { confirmations: 'NaN', required_confirmations: 3 }],
  ])('returns null for %s', (_label, overrides) => {
    expect(confirmationProgress(row('pending', overrides))).toBeNull()
  })

  it('never reports progress for a finalized row', () => {
    expect(confirmationProgress(row('credited', { confirmations: 3, required_confirmations: 3 }))).toBeNull()
    expect(confirmationProgress(row('revoked', { confirmations: 3, required_confirmations: 3 }))).toBeNull()
  })
})

describe('progress copy mapping', () => {
  const pending = row('pending')
  const provisional = row('provisional')

  it('says "detected, confirming n/m" while a pending transfer has depth left', () => {
    expect(depositProgressCopy(pending, { done: 2, required: 3, remaining: 1, percent: 67 })).toEqual({
      key: 'deposit.confirmProgress',
      params: { done: 2, required: 3, remaining: 1 },
    })
  })

  it('does not promise an instant credit once the depth is reached', () => {
    expect(depositProgressCopy(pending, { done: 3, required: 3, remaining: 0, percent: 100 })).toEqual({
      key: 'deposit.confirmProgressDone',
      params: { done: 3, required: 3 },
    })
  })

  it('marks a provisional credit as already credited while it confirms', () => {
    expect(depositProgressCopy(provisional, { done: 1, required: 3, remaining: 2, percent: 33 })).toEqual({
      key: 'deposit.provisionalProgress',
      params: { done: 1, required: 3, remaining: 2 },
    })
  })

  it('uses the final-verification wording when a provisional credit is deep enough', () => {
    expect(depositProgressCopy(provisional, { done: 3, required: 3, remaining: 0, percent: 100 })).toEqual({
      key: 'deposit.provisionalProgressDone',
      params: { done: 3, required: 3 },
    })
  })

  it('falls back to a counter-free "confirming" line when the backend sends no counters', () => {
    expect(depositProgressCopy(pending, null)).toEqual({ key: 'deposit.confirmPending' })
    expect(depositProgressCopy(provisional, null)).toEqual({ key: 'deposit.provisionalConfirming' })
  })
})
