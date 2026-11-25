import { describe, expect, it } from 'vitest'

import { isUsageStatsBusyError, USAGE_STATS_BUSY, usageStatsBusyRetryDelayMs } from '@/utils/usageStatsBusy'

describe('isUsageStatsBusyError', () => {
  it('recognises the 429 the API client rejects with when a usage aggregate is turned away', () => {
    expect(isUsageStatsBusyError({ status: 429, code: 429, reason: USAGE_STATS_BUSY, retryAfter: '2' })).toBe(true)
    expect(isUsageStatsBusyError({ status: 429, code: USAGE_STATS_BUSY })).toBe(true)
  })

  it('also recognises the 503 an older backend answers with, so a mixed rollout behaves the same', () => {
    expect(isUsageStatsBusyError({ status: 503, code: 503, reason: USAGE_STATS_BUSY })).toBe(true)
  })

  it('does not mistake any other failure for it', () => {
    // The per-user panel limit is also a 429, but not this page's busy state.
    expect(isUsageStatsBusyError({ status: 429, code: 429, reason: 'RATE_LIMITED', retryAfter: '30' })).toBe(false)
    expect(isUsageStatsBusyError({ status: 503, reason: 'STATS_TIMEOUT' })).toBe(false)
    expect(isUsageStatsBusyError({ status: 500, message: 'USAGE_STATS_BUSY' })).toBe(false)
    expect(isUsageStatsBusyError(new Error('boom'))).toBe(false)
    expect(isUsageStatsBusyError(USAGE_STATS_BUSY)).toBe(false)
    expect(isUsageStatsBusyError(null)).toBe(false)
    expect(isUsageStatsBusyError(undefined)).toBe(false)
  })
})

describe('usageStatsBusyRetryDelayMs', () => {
  it('waits as long as Retry-After says, in whole seconds', () => {
    expect(usageStatsBusyRetryDelayMs({ retryAfter: '2' })).toBe(2000)
    expect(usageStatsBusyRetryDelayMs({ retryAfter: ' 5 ' })).toBe(5000)
    expect(usageStatsBusyRetryDelayMs({ retryAfter: 3 })).toBe(3000)
  })

  it('keeps the wait between one and ten seconds', () => {
    expect(usageStatsBusyRetryDelayMs({ retryAfter: '0.2' })).toBe(1000)
    expect(usageStatsBusyRetryDelayMs({ retryAfter: '10' })).toBe(10_000)
    expect(usageStatsBusyRetryDelayMs({ retryAfter: '120' })).toBe(10_000)
  })

  it('falls back to two seconds when the header is missing, empty or not a number of seconds', () => {
    expect(usageStatsBusyRetryDelayMs({})).toBe(2000)
    expect(usageStatsBusyRetryDelayMs({ retryAfter: '' })).toBe(2000)
    expect(usageStatsBusyRetryDelayMs({ retryAfter: 'soon' })).toBe(2000)
    expect(usageStatsBusyRetryDelayMs({ retryAfter: 'Wed, 21 Oct 2026 07:28:00 GMT' })).toBe(2000)
    expect(usageStatsBusyRetryDelayMs({ retryAfter: '0' })).toBe(2000)
    expect(usageStatsBusyRetryDelayMs({ retryAfter: '-3' })).toBe(2000)
    expect(usageStatsBusyRetryDelayMs({ retryAfter: Number.NaN })).toBe(2000)
    expect(usageStatsBusyRetryDelayMs(undefined)).toBe(2000)
    expect(usageStatsBusyRetryDelayMs(null)).toBe(2000)
  })
})
