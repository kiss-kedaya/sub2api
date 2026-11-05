import { describe, expect, it } from 'vitest'
import type { ApiKey } from '@/types'
import { rateLimitWindows, usageTone } from '../keyRateLimits'

const key = (overrides: Partial<ApiKey> = {}): ApiKey =>
  ({
    rate_limit_5h: 0,
    rate_limit_1d: 0,
    rate_limit_7d: 0,
    usage_5h: 0,
    usage_1d: 0,
    usage_7d: 0,
    reset_5h_at: null,
    reset_1d_at: null,
    reset_7d_at: null,
    ...overrides,
  }) as ApiKey

describe('usageTone', () => {
  it.each([
    [0, 'ok'],
    [7.99, 'ok'],
    [8, 'warn'],
    [9.99, 'warn'],
    [10, 'danger'],
    [12, 'danger'],
  ])('used %s of 10 is %s', (used, tone) => {
    expect(usageTone(used, 10)).toBe(tone)
  })
})

describe('rateLimitWindows', () => {
  it('lists only the windows that have a limit, in 5h, 1d, 7d order', () => {
    const windows = rateLimitWindows(key({ rate_limit_7d: 70, rate_limit_5h: 5 }))
    expect(windows.map((w) => w.label)).toEqual(['5h', '7d'])
  })

  it('has nothing to show for a key without limits', () => {
    expect(rateLimitWindows(key())).toEqual([])
  })

  it('reads the matching usage, reset time and tone for each window', () => {
    const [w] = rateLimitWindows(key({ rate_limit_1d: 10, usage_1d: 8.5, reset_1d_at: '2026-11-06T00:00:00Z' }))
    expect(w).toMatchObject({ label: '1d', limit: 10, used: 8.5, tone: 'warn', resetAt: '2026-11-06T00:00:00Z' })
    expect(w.percent).toBeCloseTo(85)
  })

  it('draws a full bar for a key that is over its limit', () => {
    const [w] = rateLimitWindows(key({ rate_limit_5h: 4, usage_5h: 10 }))
    expect(w.percent).toBe(100)
    expect(w.tone).toBe('danger')
  })

  it('treats missing usage as zero and a missing reset time as null', () => {
    const [w] = rateLimitWindows(key({ rate_limit_7d: 3, usage_7d: undefined as unknown as number, reset_7d_at: undefined as unknown as null }))
    expect(w).toMatchObject({ used: 0, percent: 0, tone: 'ok', resetAt: null })
  })
})
