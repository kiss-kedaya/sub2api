import { afterEach, describe, expect, it } from 'vitest'
import { Chart as ChartJS } from 'chart.js'
import { THEME_SWITCHING_ATTR } from '@/theme/switching'
import { themeSwitchNoReplay } from '../chartTheme'

type FakeChart = { config: { options: Record<string, unknown> }; options: Record<string, unknown> }

/** Chart.js hands plugins a resolver over config.options; for these hooks a plain object behaves the same. */
function fakeChart(options: Record<string, unknown>): FakeChart {
  return { config: { options }, options }
}

const update = (chart: FakeChart) => {
  themeSwitchNoReplay.beforeUpdate?.(chart as never, { mode: 'default', cancelable: true } as never, {})
  const during = chart.options.animation
  themeSwitchNoReplay.afterUpdate?.(chart as never, { mode: 'default' } as never, {})
  return during
}

describe('themeSwitchNoReplay', () => {
  afterEach(() => {
    document.documentElement.removeAttribute(THEME_SWITCHING_ATTR)
  })

  it('is registered on Chart.js, so charts built anywhere pick it up', () => {
    expect(ChartJS.registry.plugins.get('themeSwitchNoReplay')).toBe(themeSwitchNoReplay)
  })

  it('leaves the animation alone outside a day/night switch', () => {
    const chart = fakeChart({ animation: { duration: 400 } })
    expect(update(chart)).toEqual({ duration: 400 })
    expect(chart.options.animation).toEqual({ duration: 400 })
  })

  it('repaints without animating during a switch, then puts the chart back as it was', () => {
    document.documentElement.setAttribute(THEME_SWITCHING_ATTR, '')
    const withAnimation = fakeChart({ animation: { duration: 400 } })
    expect(update(withAnimation)).toBe(false)
    expect(withAnimation.options.animation).toEqual({ duration: 400 })

    const withoutAnimation = fakeChart({})
    expect(update(withoutAnimation)).toBe(false)
    expect(withoutAnimation.options.animation).toBeUndefined()
  })

  it('keeps animating the updates after the switch has ended', () => {
    document.documentElement.setAttribute(THEME_SWITCHING_ATTR, '')
    const chart = fakeChart({ animation: { duration: 250 } })
    expect(update(chart)).toBe(false)
    document.documentElement.removeAttribute(THEME_SWITCHING_ATTR)
    expect(update(chart)).toEqual({ duration: 250 })
  })
})
