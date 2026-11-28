import { describe, expect, it } from 'vitest'

import {
  getExpirationDateRelation,
  getRemainingExpiryDuration,
  resolveDailyQuotaWindowDisplay
} from '../subscriptionQuota'

describe('subscription expiry timing', () => {
  it('uses local calendar dates for today and tomorrow', () => {
    const now = new Date(2026, 2, 7, 23, 30)

    expect(getExpirationDateRelation(new Date(2026, 2, 7, 23, 45), now)).toBe('today')
    expect(getExpirationDateRelation(new Date(2026, 2, 8, 3, 30), now)).toBe('tomorrow')
  })

  it('treats the exact expiry instant and elapsed expiries as expired', () => {
    const now = new Date(2026, 6, 30, 9, 0)

    expect(getExpirationDateRelation(now, now)).toBe('expired')
    expect(getRemainingExpiryDuration(now, now)).toBeNull()
    expect(getExpirationDateRelation(new Date(2026, 6, 30, 8, 59), now)).toBe('expired')
    expect(getRemainingExpiryDuration(new Date(2026, 6, 30, 8, 59), now)).toBeNull()
  })

  it('rejects invalid target and current dates', () => {
    const invalid = new Date('invalid')
    const valid = new Date(2026, 6, 30, 9, 0)

    expect(getExpirationDateRelation(invalid, valid)).toBeNull()
    expect(getExpirationDateRelation(valid, invalid)).toBeNull()
    expect(getRemainingExpiryDuration(invalid, valid)).toBeNull()
    expect(getRemainingExpiryDuration(valid, invalid)).toBeNull()
  })

  it('returns rounded-up hours and minutes for an expiry under 24 hours away', () => {
    const now = new Date(2026, 6, 30, 9, 0)

    expect(getRemainingExpiryDuration(new Date(2026, 6, 31, 8, 30), now)).toEqual({
      unit: 'hoursMinutes',
      hours: 23,
      minutes: 30
    })
    expect(getRemainingExpiryDuration(new Date(now.getTime() + 1), now)).toEqual({
      unit: 'hoursMinutes',
      hours: 0,
      minutes: 1
    })
    expect(getRemainingExpiryDuration(new Date(now.getTime() + 23 * 60 * 60 * 1000 + 1), now)).toEqual({
      unit: 'hoursMinutes',
      hours: 23,
      minutes: 1
    })
  })

  it('preserves rounded-up day display from 24 hours onward', () => {
    const now = new Date(2026, 6, 30, 9, 0)

    expect(getRemainingExpiryDuration(new Date(now.getTime() + 24 * 60 * 60 * 1000), now)).toEqual({
      unit: 'days',
      days: 1
    })
    expect(getRemainingExpiryDuration(new Date(now.getTime() + 24 * 60 * 60 * 1000 + 1), now)).toEqual({
      unit: 'days',
      days: 2
    })
  })
})

describe('resolveDailyQuotaWindowDisplay', () => {
  it('returns null when daily window is not activated', () => {
    expect(
      resolveDailyQuotaWindowDisplay({
        starts_at: '2026-08-06T04:00:00Z',
        expires_at: '2026-08-08T04:00:00Z',
        daily_window_start: null,
        group: { daily_reset_mode: 'anchored_24h' } as any
      })
    ).toBeNull()
  })

  it('prefers backend next_daily_reset_at when before expires_at', () => {
    const result = resolveDailyQuotaWindowDisplay({
      starts_at: '2026-08-06T04:00:00Z',
      expires_at: '2026-08-08T04:00:00Z',
      daily_window_start: '2026-08-06T04:00:00Z',
      next_daily_reset_at: '2026-08-07T04:00:00Z',
      group: { daily_reset_mode: 'anchored_24h' } as any
    })

    expect(result).toEqual({
      kind: 'reset',
      targetAt: new Date('2026-08-07T04:00:00Z')
    })
  })

  it('shows quotaEnds at expires_at when anchored 24h next boundary equals expires_at', () => {
    const result = resolveDailyQuotaWindowDisplay({
      starts_at: '2026-08-06T04:00:00Z',
      expires_at: '2026-08-08T04:00:00Z',
      daily_window_start: '2026-08-07T04:00:00Z',
      next_daily_reset_at: null,
      group: { daily_reset_mode: 'anchored_24h' } as any
    })

    expect(result).toEqual({
      kind: 'quotaEnds',
      targetAt: new Date('2026-08-08T04:00:00Z')
    })
  })

  it('keeps starts_at cadence even when anchored_24h daily_window_start is legacy midnight before starts_at', () => {
    const result = resolveDailyQuotaWindowDisplay({
      starts_at: '2026-08-06T04:00:00Z',
      expires_at: '2026-08-08T04:00:00Z',
      daily_window_start: '2026-08-05T16:00:00Z',
      group: { daily_reset_mode: 'anchored_24h' } as any
    })

    expect(result).toEqual({
      kind: 'reset',
      targetAt: new Date('2026-08-07T04:00:00Z')
    })
  })

  it('shows quotaEnds for 1-day anchored_24h subscription', () => {
    const result = resolveDailyQuotaWindowDisplay({
      starts_at: '2026-08-06T04:00:00Z',
      expires_at: '2026-08-07T04:00:00Z',
      daily_window_start: '2026-08-06T04:00:00Z',
      group: { daily_reset_mode: 'anchored_24h' } as any
    })

    expect(result).toEqual({
      kind: 'quotaEnds',
      targetAt: new Date('2026-08-07T04:00:00Z')
    })
  })

  it('shows reset for calendar mode multi-day subscription and quotaEnds on final day', () => {
    const mid = resolveDailyQuotaWindowDisplay({
      starts_at: '2026-08-06T04:00:00Z',
      expires_at: '2026-08-08T04:00:00Z',
      daily_window_start: '2026-08-05T16:00:00Z',
      group: { daily_reset_mode: 'calendar' } as any
    })
    expect(mid).toEqual({
      kind: 'reset',
      targetAt: new Date('2026-08-06T16:00:00Z')
    })

    const lastDay = resolveDailyQuotaWindowDisplay({
      starts_at: '2026-08-06T04:00:00Z',
      expires_at: '2026-08-08T04:00:00Z',
      daily_window_start: '2026-08-07T16:00:00Z',
      group: { daily_reset_mode: 'calendar' } as any
    })
    expect(lastDay).toEqual({
      kind: 'quotaEnds',
      targetAt: new Date('2026-08-08T04:00:00Z')
    })
  })
})
