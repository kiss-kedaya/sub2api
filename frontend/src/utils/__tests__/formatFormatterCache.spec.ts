import { afterEach, describe, expect, it, vi } from 'vitest'

import { getLocale } from '@/i18n'
import { formatCurrency, formatDate, formatDateTime, formatNumber } from '../format'

// Tables format several cells per row on every render (and every scroll frame in a
// virtualized list), so each helper must reuse its Intl formatter instead of building
// a new one per call.
const RealNumberFormat = Intl.NumberFormat
const RealDateTimeFormat = Intl.DateTimeFormat

afterEach(() => {
  vi.restoreAllMocks()
})

describe('Intl formatter caching', () => {
  it('reuses one NumberFormat for repeated standard and compact numbers', () => {
    const numberFormatSpy = vi
      .spyOn(Intl, 'NumberFormat')
      .mockImplementation(((...args: ConstructorParameters<typeof Intl.NumberFormat>) => new RealNumberFormat(...args)) as unknown as typeof Intl.NumberFormat)

    const standard = formatNumber(4321)
    formatNumber(8765)
    formatNumber(1)
    formatNumber(2)
    expect(numberFormatSpy).toHaveBeenCalledTimes(1)
    expect(standard).toBe(new RealNumberFormat(getLocale(), { notation: 'standard', maximumFractionDigits: 1 }).format(4321))

    formatNumber(123456)
    formatNumber(987654)
    expect(numberFormatSpy).toHaveBeenCalledTimes(2)
  })

  it('reuses one NumberFormat per currency and decimal precision', () => {
    const numberFormatSpy = vi
      .spyOn(Intl, 'NumberFormat')
      .mockImplementation(((...args: ConstructorParameters<typeof Intl.NumberFormat>) => new RealNumberFormat(...args)) as unknown as typeof Intl.NumberFormat)

    const regular = formatCurrency(12.5)
    formatCurrency(99.25)
    expect(numberFormatSpy).toHaveBeenCalledTimes(1)
    expect(regular).toBe(new RealNumberFormat(getLocale(), {
      style: 'currency',
      currency: 'USD',
      minimumFractionDigits: 2,
      maximumFractionDigits: 2,
    }).format(12.5))

    // Sub-cent amounts use six decimals, which is a different cached formatter.
    formatCurrency(0.00042)
    expect(numberFormatSpy).toHaveBeenCalledTimes(2)
  })

  it('reuses one DateTimeFormat across identical option sets', () => {
    const dateTimeFormatSpy = vi
      .spyOn(Intl, 'DateTimeFormat')
      .mockImplementation(((...args: ConstructorParameters<typeof Intl.DateTimeFormat>) => new RealDateTimeFormat(...args)) as unknown as typeof Intl.DateTimeFormat)

    const options = { year: 'numeric', month: '2-digit', day: '2-digit' } as const
    const first = formatDateTime('2026-03-15T04:05:06Z', options)
    formatDate('2026-04-16T07:08:09Z', options)
    formatDateTime('2026-05-17T10:11:12Z', options)
    expect(dateTimeFormatSpy).toHaveBeenCalledTimes(1)
    expect(first).toBe(new RealDateTimeFormat(getLocale(), options).format(new Date('2026-03-15T04:05:06Z')))

    // A different option set is formatted by a second, separately cached formatter.
    formatDate('2026-03-15T04:05:06Z', { year: 'numeric' })
    expect(dateTimeFormatSpy).toHaveBeenCalledTimes(2)
  })
})
