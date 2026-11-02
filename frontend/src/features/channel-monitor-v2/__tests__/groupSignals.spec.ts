import { describe, expect, it } from 'vitest'
import {
  GROUP_SIGNAL_WINDOW_MS,
  formatRateDelta,
  isNewGroup,
  isRecentTimestamp,
  resolveRateChange,
} from '../groupSignals'

const NOW = Date.parse('2026-11-02T10:00:00Z')
const hoursAgo = (hours: number) => new Date(NOW - hours * 60 * 60 * 1000).toISOString()
const hoursAhead = (hours: number) => new Date(NOW + hours * 60 * 60 * 1000).toISOString()

describe('groupSignals', () => {
  it('exposes a 24h window', () => {
    expect(GROUP_SIGNAL_WINDOW_MS).toBe(24 * 60 * 60 * 1000)
  })

  describe('isRecentTimestamp', () => {
    it('accepts timestamps inside the trailing window', () => {
      expect(isRecentTimestamp(hoursAgo(0.5), NOW)).toBe(true)
      expect(isRecentTimestamp(hoursAgo(23.9), NOW)).toBe(true)
    })

    it('rejects timestamps older than the window', () => {
      expect(isRecentTimestamp(hoursAgo(24.1), NOW)).toBe(false)
      expect(isRecentTimestamp(hoursAgo(72), NOW)).toBe(false)
    })

    it('tolerates small clock skew but not a far-future value', () => {
      expect(isRecentTimestamp(hoursAhead(0.01), NOW)).toBe(true)
      expect(isRecentTimestamp(hoursAhead(3), NOW)).toBe(false)
    })

    it('returns false for missing or unparsable input', () => {
      expect(isRecentTimestamp(null, NOW)).toBe(false)
      expect(isRecentTimestamp(undefined, NOW)).toBe(false)
      expect(isRecentTimestamp('not-a-date', NOW)).toBe(false)
    })
  })

  describe('isNewGroup', () => {
    it('treats a group created inside 24h as new', () => {
      expect(isNewGroup(hoursAgo(2), NOW)).toBe(true)
    })

    it('treats an older group as not new', () => {
      expect(isNewGroup(hoursAgo(30), NOW)).toBe(false)
      expect(isNewGroup(null, NOW)).toBe(false)
    })
  })

  describe('resolveRateChange', () => {
    const base = {
      rateMultiplier: 0.09,
      rateMultiplierChangedAt: hoursAgo(1),
      previousRateMultiplier: 0.06,
    }

    it('reports an increase', () => {
      const change = resolveRateChange(base, NOW)
      expect(change).toMatchObject({ direction: 'up', previous: 0.06, current: 0.09 })
      expect(change?.delta).toBeCloseTo(0.03, 10)
    })

    it('reports a decrease', () => {
      const change = resolveRateChange({ ...base, previousRateMultiplier: 0.12 }, NOW)
      expect(change?.direction).toBe('down')
      expect(change?.delta).toBeCloseTo(-0.03, 10)
    })

    it('ignores changes older than the window', () => {
      expect(resolveRateChange({ ...base, rateMultiplierChangedAt: hoursAgo(30) }, NOW)).toBeNull()
    })

    it('ignores missing optional contract fields', () => {
      expect(resolveRateChange({ rateMultiplier: 0.09 }, NOW)).toBeNull()
      expect(resolveRateChange({ previousRateMultiplier: 0.06 }, NOW)).toBeNull()
      expect(resolveRateChange(null, NOW)).toBeNull()
    })

    it('ignores changes whose delta rounds away', () => {
      expect(resolveRateChange({
        rateMultiplier: 0.0901,
        rateMultiplierChangedAt: hoursAgo(1),
        previousRateMultiplier: 0.09,
      }, NOW)).toBeNull()
    })
  })

  describe('formatRateDelta', () => {
    it('keeps the sign and two decimals', () => {
      expect(formatRateDelta(0.03)).toBe('+0.03')
      expect(formatRateDelta(-0.02)).toBe('-0.02')
      expect(formatRateDelta(1)).toBe('+1.00')
    })

    it('falls back to three decimals for sub-cent changes', () => {
      expect(formatRateDelta(0.005)).toBe('+0.005')
      expect(formatRateDelta(-0.009)).toBe('-0.009')
    })
  })
})