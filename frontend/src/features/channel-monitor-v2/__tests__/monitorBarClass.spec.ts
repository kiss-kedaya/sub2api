import { describe, expect, it } from 'vitest'
import {
  CACHE_BLACK_BAR_THRESHOLD_PERCENT,
  availabilityBarClass,
  cacheTextClass,
  isLowCacheRate,
  monitorBarClass,
} from '../monitorFormat'

/**
 * Owner rule: a cache hit rate inside the 30% band turns the channel status bar
 * black on that single condition. Availability must never change the outcome,
 * and a missing cache rate stays on the existing grey "no data" style.
 */
describe('low cache rate black band', () => {
  const availability = 99.9

  it('locks the threshold at 30 percent', () => {
    expect(CACHE_BLACK_BAR_THRESHOLD_PERCENT).toBe(30)
  })

  it.each([
    ['0%', 0],
    ['5%', 0.05],
    ['29.9%', 0.299],
    ['0.01% rounding noise', 0.0001],
  ])('paints %s black', (_label, value) => {
    expect(isLowCacheRate(value)).toBe(true)
    expect(monitorBarClass(value, availability)).toBe('bg-black')
    expect(cacheTextClass(value)).toBe('text-black dark:text-white')
  })

  it.each([
    ['30%', 0.3],
    ['30.1%', 0.301],
    ['100%', 1],
  ])('keeps the availability colours at %s', (_label, value) => {
    expect(isLowCacheRate(value)).toBe(false)
    expect(monitorBarClass(value, availability)).toBe(availabilityBarClass(availability))
    expect(cacheTextClass(value)).toBe('text-gray-900 dark:text-gray-100')
  })

  it.each([
    ['null', null],
    ['undefined', undefined],
  ])('keeps the no-data grey for %s', (_label, value) => {
    expect(isLowCacheRate(value)).toBe(false)
    // The timeline never calls monitorBarClass for an unknown bucket; the
    // helper must still fall back to the availability greys, never black.
    expect(monitorBarClass(value, null)).toBe('bg-gray-300 dark:bg-dark-600')
    expect(monitorBarClass(value, null)).not.toBe('bg-black')
    expect(cacheTextClass(value)).toBe('text-gray-900 dark:text-gray-100')
  })

  it('ignores availability, even when availability is perfect', () => {
    for (const availabilityValue of [100, 50, 10, null]) {
      expect(monitorBarClass(0, availabilityValue)).toBe('bg-black')
      expect(monitorBarClass(0.29, availabilityValue)).toBe('bg-black')
    }
  })

  it('never sends a high cache rate to black, whatever the availability is', () => {
    expect(monitorBarClass(0.9, 20)).toBe(availabilityBarClass(20))
    expect(monitorBarClass(0.9, 95)).toBe(availabilityBarClass(95))
  })

  it('ignores non-finite values instead of painting them black', () => {
    expect(isLowCacheRate(Number.NaN)).toBe(false)
    expect(monitorBarClass(Number.NaN, null)).toBe('bg-gray-300 dark:bg-dark-600')
  })
})
