import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { cancelCollapseMotion, collapseHeight, COLLAPSE_EASE, COLLAPSE_MS, expandHeight } from '../collapseMotion'

// jsdom has no Web Animations, so the element gets a recording fake: what matters is which keyframes, duration and easing
// the helper asks for, that `done` only fires when the animation finishes, and that a reversal cancels the old one.
type Fake = { frames: Keyframe[]; options: KeyframeAnimationOptions; onfinish: null | (() => void); oncancel: null | (() => void); cancel: () => void; cancelled: boolean }

function makeElement(full = 300, rectHeight = full) {
  const el = document.createElement('div')
  Object.defineProperty(el, 'scrollHeight', { value: full, configurable: true })
  el.getBoundingClientRect = () => ({ height: rectHeight } as DOMRect)
  const animations: Fake[] = []
  ;(el as unknown as { animate: unknown }).animate = (frames: Keyframe[], options: KeyframeAnimationOptions) => {
    const fake: Fake = {
      frames, options, onfinish: null, oncancel: null, cancelled: false,
      // The real cancel event is queued, not synchronous: code that relies on oncancel having run by the time it
      // starts the next motion is broken in a browser even if it passes against a synchronous fake.
      cancel() { this.cancelled = true; queueMicrotask(() => this.oncancel?.()) },
    }
    animations.push(fake)
    return fake as unknown as Animation
  }
  return { el, animations }
}

const setReducedMotion = (reduce: boolean) => {
  vi.stubGlobal('matchMedia', (query: string) => ({ matches: reduce && query.includes('prefers-reduced-motion'), media: query, addEventListener() {}, removeEventListener() {} }))
}

describe('collapseMotion', () => {
  beforeEach(() => setReducedMotion(false))
  afterEach(() => vi.unstubAllGlobals())

  it('expands from 0 to the full height while fading in, 220 ms ease-out', () => {
    const { el, animations } = makeElement(300)
    const done = vi.fn()
    expandHeight(el, done)

    expect(animations).toHaveLength(1)
    expect(animations[0].frames).toEqual([{ height: '0px', opacity: 0 }, { height: '300px', opacity: 1 }])
    expect(animations[0].options).toEqual({ duration: COLLAPSE_MS, easing: COLLAPSE_EASE })
    expect(COLLAPSE_MS).toBeLessThanOrEqual(300)
    expect(done).not.toHaveBeenCalled()
  })

  it('collapses from the full height to 0 while fading out', () => {
    const { el, animations } = makeElement(300)
    collapseHeight(el, vi.fn())
    expect(animations[0].frames).toEqual([{ height: '300px', opacity: 1 }, { height: '0px', opacity: 0 }])
  })

  it('clips overflow only while moving, then restores whatever was there before', () => {
    const { el, animations } = makeElement()
    el.style.overflow = 'visible'
    expandHeight(el, vi.fn())
    expect(el.style.overflow).toBe('hidden')
    animations[0].onfinish?.()
    expect(el.style.overflow).toBe('visible')
  })

  it('calls done only when the animation finishes (the Transition then hides the block)', () => {
    const { el, animations } = makeElement()
    const done = vi.fn()
    collapseHeight(el, done)
    expect(done).not.toHaveBeenCalled()
    animations[0].onfinish?.()
    expect(done).toHaveBeenCalledTimes(1)
  })

  it('reversing mid-way (Vue: *-cancelled hook, then the opposite hook) starts from the height it has right now', () => {
    const { el, animations } = makeElement(300, 300)
    expandHeight(el, vi.fn())
    // the block has only grown to 120 px when the user clicks again
    el.getBoundingClientRect = () => ({ height: 120 } as DOMRect)
    cancelCollapseMotion(el) // what <Transition> calls first
    const doneClose = vi.fn()
    collapseHeight(el, doneClose)

    expect(animations[0].cancelled).toBe(true)
    expect(animations).toHaveLength(2)
    expect(animations[1].frames[0]).toEqual({ height: '120px', opacity: 1 })
    expect(animations[1].frames[1]).toEqual({ height: '0px', opacity: 0 })
    animations[1].onfinish?.()
    expect(doneClose).toHaveBeenCalledTimes(1)
  })

  it('reversing without the cancelled hook (direct call) also starts from the current height', () => {
    const { el, animations } = makeElement(300, 300)
    expandHeight(el, vi.fn())
    el.getBoundingClientRect = () => ({ height: 200 } as DOMRect)
    collapseHeight(el, vi.fn())
    expect(animations[1].frames[0]).toEqual({ height: '200px', opacity: 1 })
  })

  it('after a reversal the block is NOT left clipped: overflow returns to its original value once the last motion ends', async () => {
    const { el, animations } = makeElement(300, 300)
    el.style.overflow = 'visible'
    expandHeight(el, vi.fn())
    cancelCollapseMotion(el)
    collapseHeight(el, vi.fn())
    await Promise.resolve() // let the first animation's queued cancel event run, as a browser would
    await Promise.resolve()
    expect(el.style.overflow).toBe('hidden') // still moving
    animations[1].onfinish?.()
    expect(el.style.overflow).toBe('visible')
  })

  it('a stale mid-way height is not reused by a motion started much later', () => {
    const { el, animations } = makeElement(300, 300)
    const now = vi.spyOn(performance, 'now')
    now.mockReturnValue(1000)
    expandHeight(el, vi.fn())
    el.getBoundingClientRect = () => ({ height: 90 } as DOMRect)
    cancelCollapseMotion(el) // interrupted, and nothing follows (for example the dialog was closed)
    now.mockReturnValue(1500) // half a second later
    el.getBoundingClientRect = () => ({ height: 300 } as DOMRect)
    collapseHeight(el, vi.fn())
    expect(animations[1].frames[0]).toEqual({ height: '300px', opacity: 1 })
  })

  it('prefers-reduced-motion: no animation at all, done runs immediately', () => {
    setReducedMotion(true)
    const { el, animations } = makeElement()
    const done = vi.fn()
    expandHeight(el, done)
    expect(animations).toHaveLength(0)
    expect(done).toHaveBeenCalledTimes(1)
    expect(el.style.overflow).toBe('')
  })

  it('an engine without Web Animations finishes immediately instead of throwing', () => {
    const el = document.createElement('div')
    const done = vi.fn()
    expect(() => collapseHeight(el, done)).not.toThrow()
    expect(done).toHaveBeenCalledTimes(1)
  })
})
