import { describe, expect, it } from 'vitest'
import {
  depositAmountLimits, estimatedDepositCredit, parseDepositAmount, validateDepositAmount,
  type DepositAmountLimits,
} from '../depositAmount'
import type { DepositInfo } from '@/api/crypto'

function info(overrides: Partial<DepositInfo> = {}): DepositInfo {
  return { enabled: true, chains: [], min_amount: 1, max_amount: 10000, ...overrides }
}

const limits = (min: number | null, max: number | null): DepositAmountLimits => ({ min, max })

describe('deposit amount thresholds', () => {
  it('prefers the explicit credit_min_amount / credit_max_amount fields', () => {
    expect(depositAmountLimits(info({ credit_min_amount: 2, credit_max_amount: 500 })))
      .toEqual({ min: 2, max: 500 })
  })

  it('falls back to min_amount / max_amount when the explicit fields are absent', () => {
    expect(depositAmountLimits(info())).toEqual({ min: 1, max: 10000 })
  })

  it('treats a non-positive cap as "not enforced" instead of rejecting everything', () => {
    // The current backend sends max_amount 0 when no per-deposit cap is set.
    expect(depositAmountLimits(info({ max_amount: 0 }))).toEqual({ min: 1, max: null })
  })

  it('does not fall back when an explicit field is present but disabled', () => {
    // An explicit 0 means "this deployment disabled the cap"; using the legacy
    // max_amount anyway would silently re-enable a limit the backend dropped.
    expect(depositAmountLimits(info({ credit_max_amount: 0, max_amount: 10000 })))
      .toEqual({ min: 1, max: null })
  })

  it('accepts numeric strings and ignores unparsable values', () => {
    expect(depositAmountLimits(info({ credit_min_amount: '0.5' as unknown as number })))
      .toEqual({ min: 0.5, max: 10000 })
    expect(depositAmountLimits(info({ credit_min_amount: 'oops' as unknown as number })))
      .toEqual({ min: 1, max: 10000 })
  })

  it('performs no validation when the deployment exposes no thresholds at all', () => {
    expect(depositAmountLimits(null)).toEqual({ min: null, max: null })
    expect(depositAmountLimits(info({ min_amount: undefined as unknown as number, max_amount: 0 })))
      .toEqual({ min: null, max: null })
    expect(validateDepositAmount('999999', limits(null, null)).blocked).toBe(false)
  })
})

describe('deposit amount parsing', () => {
  it.each([
    ['10', 10],
    [' 1,234.5 ', 1234.5],
    ['.5', 0.5],
    ['10.', 10],
    ['0.001', 0.001],
  ])('parses %s', (raw, expected) => {
    expect(parseDepositAmount(raw)).toBe(expected)
  })

  it.each(['', '   ', '0', '0.0', '-1', '1e5', '0x10', 'abc', '1.2.3', 'NaN'])('rejects %s', (raw) => {
    expect(parseDepositAmount(raw)).toBeNull()
  })
})

describe('deposit amount validation boundaries', () => {
  const range = limits(1, 100)

  it('does not block or error while the field is empty', () => {
    const check = validateDepositAmount('', range)
    expect(check.state).toBe('empty')
    expect(check.blocked).toBe(false)
  })

  it('rejects text and zero as invalid', () => {
    expect(validateDepositAmount('abc', range)).toMatchObject({ state: 'invalid', blocked: true })
    expect(validateDepositAmount('0', range)).toMatchObject({ state: 'invalid', blocked: true })
  })

  it('accepts exactly the minimum and exactly the maximum', () => {
    expect(validateDepositAmount('1', range)).toMatchObject({ state: 'ok', amount: 1, blocked: false })
    expect(validateDepositAmount('100', range)).toMatchObject({ state: 'ok', amount: 100, blocked: false })
  })

  it('blocks a value just below the minimum', () => {
    expect(validateDepositAmount('0.999', range)).toMatchObject({ state: 'below_min', blocked: true })
  })

  it('blocks a value just above the maximum', () => {
    expect(validateDepositAmount('100.001', range)).toMatchObject({ state: 'above_max', blocked: true })
  })

  it('ignores a missing minimum or cap independently', () => {
    expect(validateDepositAmount('0.0001', limits(null, 100)).state).toBe('ok')
    expect(validateDepositAmount('999999', limits(1, null)).state).toBe('ok')
  })

  it('compares a market-priced asset by USD notional, not coin amount', () => {
    const context = { marketPriced: true, priceUsd: 3210.12 }
    // 0.0000003 ETH ≈ $0.00096, below the 0.001 USD floor.
    expect(validateDepositAmount('0.0000003', limits(0.001, 10000), context))
      .toMatchObject({ state: 'below_min', blocked: true })
    // 1 ETH ≈ $3210.12, inside the range, and the notional is reported.
    const ok = validateDepositAmount('1', limits(0.001, 10000), context)
    expect(ok.state).toBe('ok')
    expect(ok.notional).toBeCloseTo(3210.12, 5)
    // 4 ETH ≈ $12840.48, above the 10000 USD cap.
    expect(validateDepositAmount('4', limits(0.001, 10000), context))
      .toMatchObject({ state: 'above_max', blocked: true })
  })

  it('degrades to unverifiable, not to a rejection, when no market quote exists', () => {
    const check = validateDepositAmount('0.5', limits(0.001, 10000), { marketPriced: true, priceUsd: null })
    expect(check.state).toBe('unverifiable')
    expect(check.blocked).toBe(false)
    expect(check.amount).toBe(0.5)
    expect(check.notional).toBeNull()
  })
})

describe('estimated deposit credit', () => {
  it('multiplies the token amount by the fixed rate', () => {
    expect(estimatedDepositCredit(10, 6.7)).toBe(67)
    expect(estimatedDepositCredit(0.1, 0.5)).toBe(0.05)
  })

  it('returns null when there is no usable rate or amount', () => {
    expect(estimatedDepositCredit(10, null)).toBeNull()
    expect(estimatedDepositCredit(10, 0)).toBeNull()
    expect(estimatedDepositCredit(null, 6.7)).toBeNull()
  })
})
