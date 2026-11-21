import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import {
  formatStepUpSkipRemaining,
  recordStepUpSkip,
  resetStepUpSkipForTests,
  stepUpSkipRemainingSeconds,
  stepUpSkipVisible,
  useStepUpSkip,
} from '../useStepUpSkip'

describe('step-up skip notice store', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    resetStepUpSkipForTests()
  })

  afterEach(() => {
    resetStepUpSkipForTests()
    vi.useRealTimers()
  })

  it('shows the grace window reported by the backend and counts down', () => {
    recordStepUpSkip(272)

    expect(stepUpSkipVisible.value).toBe(true)
    expect(stepUpSkipRemainingSeconds.value).toBe(272)
    expect(formatStepUpSkipRemaining(stepUpSkipRemainingSeconds.value)).toBe('4:32')

    vi.advanceTimersByTime(1000)
    expect(stepUpSkipRemainingSeconds.value).toBe(271)
    expect(formatStepUpSkipRemaining(stepUpSkipRemainingSeconds.value)).toBe('4:31')
  })

  it('formats m:ss like the notice copy', () => {
    expect(formatStepUpSkipRemaining(300)).toBe('5:00')
    expect(formatStepUpSkipRemaining(272)).toBe('4:32')
    expect(formatStepUpSkipRemaining(59)).toBe('0:59')
    expect(formatStepUpSkipRemaining(9)).toBe('0:09')
    expect(formatStepUpSkipRemaining(0)).toBe('0:00')
  })

  it('hides automatically when the window reaches zero', () => {
    recordStepUpSkip(2)
    expect(stepUpSkipVisible.value).toBe(true)

    vi.advanceTimersByTime(2000)
    expect(stepUpSkipRemainingSeconds.value).toBe(0)
    expect(stepUpSkipVisible.value).toBe(false)
  })

  it('a later skip with more time left extends the window', () => {
    recordStepUpSkip(30)
    vi.advanceTimersByTime(10_000)
    expect(stepUpSkipRemainingSeconds.value).toBe(20)

    recordStepUpSkip(280)
    expect(stepUpSkipRemainingSeconds.value).toBe(280)
    expect(stepUpSkipVisible.value).toBe(true)
  })

  it('ignores invalid remaining values (never invents a window)', () => {
    recordStepUpSkip(0)
    recordStepUpSkip(-5)
    recordStepUpSkip(Number.NaN)
    recordStepUpSkip(Number.POSITIVE_INFINITY)

    expect(stepUpSkipVisible.value).toBe(false)
    expect(stepUpSkipRemainingSeconds.value).toBe(0)
  })

  it('exposes a component-friendly facade', () => {
    const skip = useStepUpSkip()
    recordStepUpSkip(65)

    expect(skip.visible.value).toBe(true)
    expect(skip.remainingSeconds.value).toBe(65)
    expect(skip.formattedRemaining.value).toBe('1:05')
  })
})
