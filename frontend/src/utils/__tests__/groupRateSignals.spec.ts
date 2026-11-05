import { describe, expect, it } from 'vitest'
import { formatRateValue, formatSignalTime } from '../groupRateSignals'

describe('formatRateValue', () => {
  it('keeps two decimals and trims the rest', () => {
    expect(formatRateValue(0.5)).toBe('0.50')
    expect(formatRateValue(1)).toBe('1.00')
    expect(formatRateValue(0.08)).toBe('0.08')
    expect(formatRateValue(12.3)).toBe('12.30')
  })

  it('keeps precision the rate really has, up to four decimals', () => {
    expect(formatRateValue(0.0125)).toBe('0.0125')
    expect(formatRateValue(0.0001)).toBe('0.0001')
    expect(formatRateValue(0.105)).toBe('0.105')
  })

  it('removes floating point noise', () => {
    expect(formatRateValue(0.1 + 0.2)).toBe('0.30')
    expect(formatRateValue(0.08 - 0.06)).toBe('0.02')
    expect(formatRateValue(0.020000000000000004)).toBe('0.02')
  })

  it('does not touch integer digits that happen to end in zero', () => {
    expect(formatRateValue(10)).toBe('10.00')
    expect(formatRateValue(100)).toBe('100.00')
  })

  it('shows a dash for values that are not numbers', () => {
    expect(formatRateValue(Number.NaN)).toBe('-')
    expect(formatRateValue(Number.POSITIVE_INFINITY)).toBe('-')
  })
})

describe('formatSignalTime', () => {
  it('formats a local month, day and time', () => {
    const text = formatSignalTime('2026-11-05T01:00:00Z', 'en')
    expect(text).toMatch(/Nov/)
    expect(text).toMatch(/\d{1,2}:\d{2}/)
  })

  it('follows the requested locale', () => {
    expect(formatSignalTime('2026-11-05T01:00:00Z', 'zh')).toMatch(/11月/)
  })

  it('falls back to a dash for missing or unparsable input', () => {
    expect(formatSignalTime(null)).toBe('-')
    expect(formatSignalTime(undefined)).toBe('-')
    expect(formatSignalTime('')).toBe('-')
    expect(formatSignalTime('not a date')).toBe('-')
  })
})
