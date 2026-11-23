import { describe, expect, it } from 'vitest'
import {
  formatTransactionAmount,
  resolveTransactionSign,
  resolveTransactionTone,
  transactionAmountClass,
  transactionIcon,
  transactionOrbClass,
  transactionToneAttribute,
  type TransactionItem
} from '../transactionList'

const base: TransactionItem = { id: 1, title: 'On-chain deposit' }

describe('transaction list row model', () => {
  it('maps record types to a lucide icon and lets an explicit icon win', () => {
    expect(transactionIcon({ ...base, type: 'deposit' })).toBe('i-lucide-arrow-down-left')
    expect(transactionIcon({ ...base, type: 'reversal' })).toBe('i-lucide-undo-2')
    expect(transactionIcon({ ...base, type: 'subscription' })).toBe('i-lucide-badge-check')
    expect(transactionIcon({ ...base, type: 'usage' })).toBe('i-lucide-zap')
    expect(transactionIcon({ ...base, type: 'exchange' })).toBe('i-lucide-repeat-2')
    expect(transactionIcon(base)).toBe('i-lucide-circle-dot')
    expect(transactionIcon({ ...base, type: 'deposit', icon: 'i-lucide-hand-coins' }))
      .toBe('i-lucide-hand-coins')
  })

  it('derives the tone from the amount sign unless the page pins one', () => {
    expect(resolveTransactionTone({ ...base, amount: 12.5 })).toBe('positive')
    expect(resolveTransactionTone({ ...base, amount: -4.2 })).toBe('negative')
    expect(resolveTransactionTone({ ...base, amount: 0 })).toBe('neutral')
    expect(resolveTransactionTone({ ...base, amount: '10 USDT' })).toBe('neutral')
    expect(resolveTransactionTone({ ...base, amount: 67, tone: 'reversal' })).toBe('reversal')
    expect(resolveTransactionTone({ ...base, amount: 67, tone: 'pending' })).toBe('pending')
  })

  it('composes the visible amount with the sign the tone implies', () => {
    expect(formatTransactionAmount({ ...base, amount: 12.3, amountPrefix: '$' })).toBe('+$12.3')
    expect(formatTransactionAmount({ ...base, amount: -4.2, amountPrefix: '$' })).toBe('-$4.2')
    // A reversal reads as money leaving the ledger, never as an unsigned credit.
    expect(formatTransactionAmount({ ...base, amount: 6.7, amountPrefix: '$', tone: 'reversal' })).toBe('-$6.7')
    // Pending rows keep the amount visible but carry no +/- claim.
    expect(formatTransactionAmount({ ...base, amount: 10, amountSuffix: ' USDT', tone: 'pending' })).toBe('10 USDT')
    // Explicit sign overrides (e.g. subscription day counts).
    expect(formatTransactionAmount({ ...base, amount: '30d', amountSign: '', tone: 'neutral' })).toBe('30d')
    // A missing amount is the caller's signal to render the em dash, not zero.
    expect(formatTransactionAmount(base)).toBeNull()
    expect(formatTransactionAmount({ ...base, amount: '' })).toBeNull()
  })

  it('resolves a sign independently of the formatter', () => {
    expect(resolveTransactionSign({ ...base, amount: 1 }, 'positive')).toBe('+')
    expect(resolveTransactionSign({ ...base, amount: 1 }, 'negative')).toBe('-')
    expect(resolveTransactionSign({ ...base, amount: 1 }, 'reversal')).toBe('-')
    expect(resolveTransactionSign({ ...base, amount: 1 }, 'pending')).toBe('')
    expect(resolveTransactionSign({ ...base, amount: 1, amountSign: '' }, 'positive')).toBe('')
  })

  it('maps tones to the shared semantic classes and data-tone value', () => {
    expect(transactionAmountClass({ ...base, amount: 1 })).toBe('amount-positive')
    expect(transactionAmountClass({ ...base, amount: -1 })).toBe('amount-negative')
    expect(transactionAmountClass({ ...base, amount: 1, tone: 'reversal' })).toBe('amount-reversal')
    expect(transactionAmountClass({ ...base, amount: 1, tone: 'pending' })).toBe('amount-pending')
    expect(transactionOrbClass({ ...base, amount: 1 })).toBe('icon-orb-positive')
    expect(transactionOrbClass({ ...base, amount: 1, tone: 'reversal' })).toBe('icon-orb-reversal')
    expect(transactionOrbClass({ ...base, amount: 1, tone: 'neutral' })).toBe('icon-orb-neutral')
    expect(transactionToneAttribute({ ...base, amount: -3 })).toBe('negative')
  })
})
