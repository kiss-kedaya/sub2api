import { describe, expect, it } from 'vitest'
import { formatMultiplier, formatMultiplierCompact } from '../formatters'

describe('formatMultiplier', () => {
  it('keeps significant decimals instead of rounding to 2 places', () => {
    expect(formatMultiplier(0.035)).toBe('0.035')
    expect(formatMultiplier(0.125)).toBe('0.125')
    expect(formatMultiplier(0.015)).toBe('0.015')
    expect(formatMultiplier(0.0625)).toBe('0.0625')
  })

  it('pads to at least 2 decimals for round values', () => {
    expect(formatMultiplier(0.3)).toBe('0.30')
    expect(formatMultiplier(1)).toBe('1.00')
    expect(formatMultiplier(1.5)).toBe('1.50')
    expect(formatMultiplier(2)).toBe('2.00')
  })

  it('handles small values down to 4 decimals', () => {
    expect(formatMultiplier(0.001)).toBe('0.001')
    expect(formatMultiplier(0.0001)).toBe('0.0001')
  })

  it('falls back to 2 significant digits below 0.0001', () => {
    expect(formatMultiplier(0.00005)).toBe('0.000050')
  })
})

describe('formatMultiplierCompact', () => {
  it('drops trailing zeros and does not pad round values', () => {
    expect(formatMultiplierCompact(1)).toBe('1')
    expect(formatMultiplierCompact(2)).toBe('2')
    expect(formatMultiplierCompact(2.5)).toBe('2.5')
    expect(formatMultiplierCompact(0.145)).toBe('0.145')
    expect(formatMultiplierCompact(0.16)).toBe('0.16')
    expect(formatMultiplierCompact(0.0001)).toBe('0.0001')
    expect(formatMultiplierCompact(0)).toBe('0')
  })

  it('keeps at most 4 decimals and wipes float noise', () => {
    expect(formatMultiplierCompact(0.16 * 2)).toBe('0.32')
    expect(formatMultiplierCompact(0.1 * 3)).toBe('0.3')
    expect(formatMultiplierCompact(1.2345 * 3)).toBe('3.7035')
    expect(formatMultiplierCompact(0.123456)).toBe('0.1235')
  })

  it('does not round a tiny positive value down to 0', () => {
    expect(formatMultiplierCompact(0.00004)).toBe('0.00004')
  })
})
