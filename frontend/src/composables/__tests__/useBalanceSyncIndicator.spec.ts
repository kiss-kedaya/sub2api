import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { effectScope, nextTick, ref } from 'vue'
import { FLASH_CYCLE_MS, useBalanceSyncIndicator, type BalanceSyncState } from '../useBalanceSyncIndicator'

describe('useBalanceSyncIndicator', () => {
  const source = ref<BalanceSyncState>('fresh')

  beforeEach(() => {
    vi.useFakeTimers()
    source.value = 'fresh'
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  const setup = () => {
    const scope = effectScope()
    const shown = scope.run(() => useBalanceSyncIndicator(() => source.value))!
    return { shown, scope }
  }

  /** Change the state the store reports and let the watcher react. */
  const report = async (state: BalanceSyncState) => {
    source.value = state
    await nextTick()
  }

  it('starts with whatever the store reports', () => {
    source.value = 'failed'
    const { shown, scope } = setup()
    expect(shown.value).toBe('failed')
    scope.stop()
  })

  it('shows the flash the moment a request starts', async () => {
    const { shown, scope } = setup()
    await report('loading')
    expect(shown.value).toBe('loading')
    scope.stop()
  })

  it('keeps a request that finishes in 80 ms on screen for one whole flash cycle', async () => {
    const { shown, scope } = setup()
    await report('loading')

    await vi.advanceTimersByTimeAsync(80)
    await report('fresh')
    expect(shown.value).toBe('loading')

    await vi.advanceTimersByTimeAsync(FLASH_CYCLE_MS - 80 - 1)
    expect(shown.value).toBe('loading')
    await vi.advanceTimersByTimeAsync(1)
    expect(shown.value).toBe('fresh')
    scope.stop()
  })

  it('still gives a request that finishes in the same instant one whole cycle', async () => {
    const { shown, scope } = setup()
    await report('loading')
    await report('fresh')
    expect(shown.value).toBe('loading')

    await vi.advanceTimersByTimeAsync(FLASH_CYCLE_MS - 1)
    expect(shown.value).toBe('loading')
    await vi.advanceTimersByTimeAsync(1)
    expect(shown.value).toBe('fresh')
    scope.stop()
  })

  it('ends the flash at the end of a cycle, never in the middle of one', async () => {
    const { shown, scope } = setup()
    await report('loading')

    // 1.1 cycles in: wait for the second cycle to finish.
    await vi.advanceTimersByTimeAsync(Math.round(FLASH_CYCLE_MS * 1.1))
    await report('fresh')
    expect(shown.value).toBe('loading')

    await vi.advanceTimersByTimeAsync(Math.round(FLASH_CYCLE_MS * 0.9) - 1)
    expect(shown.value).toBe('loading')
    await vi.advanceTimersByTimeAsync(1)
    expect(shown.value).toBe('fresh')
    scope.stop()
  })

  it('switches at once when the request finished exactly on a cycle boundary', async () => {
    const { shown, scope } = setup()
    await report('loading')

    await vi.advanceTimersByTimeAsync(FLASH_CYCLE_MS * 2)
    await report('fresh')
    expect(shown.value).toBe('fresh')
    scope.stop()
  })

  it('shows a failure after the flash, the same way', async () => {
    const { shown, scope } = setup()
    await report('loading')
    await vi.advanceTimersByTimeAsync(100)

    await report('failed')
    expect(shown.value).toBe('loading')
    await vi.advanceTimersByTimeAsync(FLASH_CYCLE_MS)
    expect(shown.value).toBe('failed')
    scope.stop()
  })

  it('shows what is true when the cycle ends, not what it was when the request ended', async () => {
    const { shown, scope } = setup()
    await report('loading')
    await vi.advanceTimersByTimeAsync(100)

    await report('fresh')
    await report('failed')
    await vi.advanceTimersByTimeAsync(FLASH_CYCLE_MS)
    expect(shown.value).toBe('failed')
    scope.stop()
  })

  it('carries on with the same flash when another request starts while it is still showing', async () => {
    const { shown, scope } = setup()
    await report('loading')
    await vi.advanceTimersByTimeAsync(300)
    await report('fresh')
    expect(shown.value).toBe('loading')

    // A second request begins before the first flash cycle is over: still one flash, no restart.
    await vi.advanceTimersByTimeAsync(100)
    await report('loading')
    expect(shown.value).toBe('loading')

    // That request ends in the following cycle; the clock is still the original one.
    await vi.advanceTimersByTimeAsync(FLASH_CYCLE_MS + 50 - 400)
    await report('fresh')
    expect(shown.value).toBe('loading')
    await vi.advanceTimersByTimeAsync(FLASH_CYCLE_MS - 50 - 1)
    expect(shown.value).toBe('loading')
    await vi.advanceTimersByTimeAsync(1)
    expect(shown.value).toBe('fresh')
    scope.stop()
  })

  it('starts the clock again for a request that begins after the ring has settled', async () => {
    const { shown, scope } = setup()
    await report('loading')
    await vi.advanceTimersByTimeAsync(10)
    await report('fresh')
    await vi.advanceTimersByTimeAsync(FLASH_CYCLE_MS)
    expect(shown.value).toBe('fresh')

    await vi.advanceTimersByTimeAsync(5_000)
    await report('loading')
    await vi.advanceTimersByTimeAsync(10)
    await report('fresh')
    expect(shown.value).toBe('loading')
    await vi.advanceTimersByTimeAsync(FLASH_CYCLE_MS - 10)
    expect(shown.value).toBe('fresh')
    scope.stop()
  })

  it('does not touch the state after its scope is gone', async () => {
    const { shown, scope } = setup()
    await report('loading')
    await vi.advanceTimersByTimeAsync(10)
    await report('fresh')

    scope.stop()
    await vi.advanceTimersByTimeAsync(FLASH_CYCLE_MS * 2)

    expect(shown.value).toBe('loading')
    expect(vi.getTimerCount()).toBe(0)
  })
})
