import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useTotpAttemptLimit } from '../useTotpAttemptLimit'

const BASE_TIME = new Date('2026-11-21T03:00:00Z').getTime()

describe('useTotpAttemptLimit', () => {
  beforeEach(() => {
    sessionStorage.clear()
    vi.useFakeTimers()
    vi.setSystemTime(BASE_TIME)
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('blocks the 6th submit inside the 60s window and recovers afterwards', () => {
    const limit = useTotpAttemptLimit({ accountKey: () => 'user@example.com' })

    for (let i = 1; i <= 5; i++) {
      expect(limit.recordAttempt(), `attempt ${i}`).toBe(true)
    }
    expect(limit.canSubmit.value).toBe(false)
    expect(limit.recordAttempt()).toBe(false)
    expect(limit.remainingSeconds.value).toBeGreaterThan(0)
    expect(limit.remainingSeconds.value).toBeLessThanOrEqual(60)

    // 60 秒滚动窗口过去后恢复。
    vi.advanceTimersByTime(61_000)
    expect(limit.canSubmit.value).toBe(true)
    expect(limit.remainingSeconds.value).toBe(0)
    expect(limit.recordAttempt()).toBe(true)
    limit.stopTimer()
  })

  it('keeps accounts independent', () => {
    const first = useTotpAttemptLimit({ accountKey: () => 'a@example.com' })
    const second = useTotpAttemptLimit({ accountKey: () => 'b@example.com' })

    for (let i = 0; i < 5; i++) {
      expect(first.recordAttempt()).toBe(true)
    }
    expect(first.recordAttempt()).toBe(false)
    expect(second.canSubmit.value).toBe(true)
    expect(second.recordAttempt()).toBe(true)
    first.stopTimer()
    second.stopTimer()
  })

  it('applies the server Retry-After on top of the local window', () => {
    const limit = useTotpAttemptLimit({ accountKey: () => 'user@example.com' })

    limit.applyServerRetryAfter(30)
    expect(limit.canSubmit.value).toBe(false)
    expect(limit.remainingSeconds.value).toBe(30)
    expect(limit.recordAttempt()).toBe(false)

    vi.advanceTimersByTime(31_000)
    expect(limit.canSubmit.value).toBe(true)
    expect(limit.recordAttempt()).toBe(true)
    limit.stopTimer()
  })

  it('ignores invalid Retry-After values', () => {
    const limit = useTotpAttemptLimit({ accountKey: () => 'user@example.com' })
    limit.applyServerRetryAfter(0)
    limit.applyServerRetryAfter(Number.NaN)
    expect(limit.canSubmit.value).toBe(true)
    limit.stopTimer()
  })

  it('resets after a successful verification', () => {
    const limit = useTotpAttemptLimit({ accountKey: () => 'user@example.com' })
    for (let i = 0; i < 5; i++) {
      expect(limit.recordAttempt()).toBe(true)
    }
    expect(limit.canSubmit.value).toBe(false)

    limit.reset()
    expect(limit.canSubmit.value).toBe(true)
    expect(limit.remainingSeconds.value).toBe(0)
    expect(limit.recordAttempt()).toBe(true)
    limit.stopTimer()
  })
})
