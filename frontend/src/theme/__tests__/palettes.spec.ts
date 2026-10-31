import { describe, expect, it } from 'vitest'
import { CHROMATIC_PALETTE_NAMES, NEUTRAL_PALETTE_NAMES, PALETTES, PALETTE_SHADES, oklchToRgb, paletteHex, paletteRgbChannels } from '../palettes'

describe('site theme palettes', () => {
  it('has every allowlisted palette with 11 shades that get darker', () => {
    const names = [...CHROMATIC_PALETTE_NAMES, ...NEUTRAL_PALETTE_NAMES]
    expect(Object.keys(PALETTES).sort()).toEqual([...names].sort())
    for (const name of names) {
      const scale = PALETTES[name]
      expect(scale).toHaveLength(PALETTE_SHADES.length)
      const lightness = scale.map((color) => Number(/^oklch\(([\d.]+)%/.exec(color)?.[1]))
      lightness.slice(1).forEach((value, i) => expect(value, `${name}-${PALETTE_SHADES[i + 1]}`).toBeLessThan(lightness[i]))
      for (const color of scale) {
        expect(oklchToRgb(color).every((channel) => Number.isInteger(channel) && channel >= 0 && channel <= 255)).toBe(true)
      }
    }
  })

  it('converts to the sRGB values the legacy tokens use', () => {
    expect(paletteHex('neutral', 900)).toBe('#171717')
    expect(paletteHex('neutral', 200)).toBe('#e5e5e5')
    expect(paletteRgbChannels('neutral', 500)).toBe('115 115 115')
    expect(paletteHex('sakura', 400)).toBe('#f2b6c6')
    expect(oklchToRgb('oklch(100% 0 none)')).toEqual([255, 255, 255])
    expect(oklchToRgb('oklch(0% 0 none)')).toEqual([0, 0, 0])
  })

  it('refuses anything that is not a palette oklch() value', () => {
    expect(() => oklchToRgb('red')).toThrow()
    expect(() => oklchToRgb('oklch(50% 0.1 20) ; color: red')).toThrow()
  })
})
