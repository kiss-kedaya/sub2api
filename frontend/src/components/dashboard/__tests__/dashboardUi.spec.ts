import { afterEach, describe, expect, it, vi } from 'vitest'
import { cssColorToHex } from '../dashboardUi'

// jsdom has no style engine for oklch()/color-mix(), so these tests stub the pieces the converter talks to and check
// the decisions it makes: which colors never leave the string parser, when it asks the style engine, and that the
// canvas readback (about 200 ms per call in a real browser) is only the last resort.
describe('cssColorToHex', () => {
  afterEach(() => {
    vi.restoreAllMocks()
    document.body.innerHTML = ''
  })

  const stubStyleAnswer = (answer: string) =>
    vi.spyOn(window, 'getComputedStyle').mockReturnValue({ color: answer } as CSSStyleDeclaration)

  const spyCanvas = (bytes: [number, number, number]) => {
    const getImageData = vi.fn(() => ({ data: new Uint8ClampedArray([...bytes, 255]) }))
    const context = { fillStyle: '', fillRect: vi.fn(), getImageData }
    const original = document.createElement.bind(document)
    vi.spyOn(document, 'createElement').mockImplementation(((tag: string) => {
      if (tag === 'canvas') return { getContext: () => context } as unknown as HTMLCanvasElement
      return original(tag)
    }) as typeof document.createElement)
    return getImageData
  }

  it('hex and rgb() are parsed from the string, with no DOM or canvas work', () => {
    const create = vi.spyOn(document, 'createElement')
    expect(cssColorToHex('#F2B6C6')).toBe('#f2b6c6')
    expect(cssColorToHex('rgb(23, 23, 23)')).toBe('#171717')
    expect(cssColorToHex('rgba(23 23 23 / 0.5)')).toBe('#171717')
    expect(create).not.toHaveBeenCalled()
  })

  it('color(srgb …) is parsed from the string and clamped to 0..255 like a canvas fill', () => {
    const create = vi.spyOn(document, 'createElement')
    expect(cssColorToHex('color(srgb 0.871871 0.564605 0.668481)')).toBe('#de90aa')
    // out-of-gamut components (oklch can produce them) clamp instead of overflowing into a 3-digit hex
    expect(cssColorToHex('color(srgb 1.0216 0.135707 -0.02)')).toBe('#ff2300')
    expect(create).not.toHaveBeenCalled()
  })

  it('oklch() is resolved by the style engine (color-mix in srgb), never by a canvas readback', () => {
    stubStyleAnswer('color(srgb 0.871871 0.564605 0.668481)')
    const getImageData = spyCanvas([1, 2, 3])
    expect(cssColorToHex('oklch(74% 0.1 358)')).toBe('#de90aa')
    expect(getImageData).not.toHaveBeenCalled()
  })

  it('a style engine that answers rgb() is understood too', () => {
    stubStyleAnswer('rgb(222, 144, 170)')
    const getImageData = spyCanvas([1, 2, 3])
    expect(cssColorToHex('oklch(74% 0.1 358)')).toBe('#de90aa')
    expect(getImageData).not.toHaveBeenCalled()
  })

  it('the probe element is removed again, so repeated reads do not leak nodes', () => {
    stubStyleAnswer('color(srgb 0.1 0.2 0.3)')
    cssColorToHex('oklch(20% 0.02 300)')
    cssColorToHex('oklch(20% 0.02 300)')
    expect(document.body.children).toHaveLength(0)
  })

  it('falls back to the canvas only when the style engine cannot resolve the color', () => {
    stubStyleAnswer('') // engine without color-mix: computed color comes back empty
    const getImageData = spyCanvas([222, 144, 170])
    expect(cssColorToHex('oklch(74% 0.1 358)')).toBe('#de90aa')
    expect(getImageData).toHaveBeenCalledTimes(1)
  })

  it('anything that is not a color function is rejected, not guessed', () => {
    expect(cssColorToHex('var(--ui-primary)')).toBeNull()
    expect(cssColorToHex('tomato')).toBeNull()
    expect(cssColorToHex('')).toBeNull()
  })
})
