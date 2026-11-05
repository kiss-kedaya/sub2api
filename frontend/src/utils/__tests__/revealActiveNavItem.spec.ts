import { afterEach, describe, expect, it, vi } from 'vitest'
import { revealActiveNavItem } from '../revealActiveNavItem'

// jsdom has no layout, so the strip and its current entry are given fixed geometry.
// The strip is 300px wide on screen (left edge at x=0) and holds 800px of content.
function strip(opts: { scrollLeft?: number; scrollWidth?: number; clientWidth?: number; itemLeft?: number; itemWidth?: number; current?: boolean } = {}) {
  const el = document.createElement('nav')
  const scrollWidth = opts.scrollWidth ?? 800
  const clientWidth = opts.clientWidth ?? 300
  let scrollLeft = opts.scrollLeft ?? 0
  Object.defineProperty(el, 'scrollWidth', { configurable: true, get: () => scrollWidth })
  Object.defineProperty(el, 'clientWidth', { configurable: true, get: () => clientWidth })
  Object.defineProperty(el, 'scrollLeft', { configurable: true, get: () => scrollLeft, set: (v: number) => { scrollLeft = v } })
  el.getBoundingClientRect = () => ({ left: 0, right: clientWidth, top: 0, bottom: 40, width: clientWidth, height: 40, x: 0, y: 0, toJSON: () => ({}) })
  if (opts.current !== false) {
    const link = document.createElement('a')
    link.setAttribute('aria-current', 'page')
    // Where the entry sits on screen right now: its place in the content minus how far the strip is scrolled.
    const width = opts.itemWidth ?? 80
    const left = opts.itemLeft ?? 0
    link.getBoundingClientRect = () => ({ left: left - scrollLeft, right: left - scrollLeft + width, top: 0, bottom: 40, width, height: 40, x: left - scrollLeft, y: 0, toJSON: () => ({}) })
    el.appendChild(link)
  }
  return { el, scrollLeft: () => scrollLeft }
}

afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals() })

describe('revealActiveNavItem', () => {
  it('does nothing when there is no strip', () => {
    expect(revealActiveNavItem(null)).toBe(false)
    expect(revealActiveNavItem(undefined)).toBe(false)
  })

  it('does nothing when the whole strip fits', () => {
    const s = strip({ scrollWidth: 300, clientWidth: 300, itemLeft: 200 })
    expect(revealActiveNavItem(s.el)).toBe(false)
    expect(s.scrollLeft()).toBe(0)
  })

  it('does nothing when no entry is marked current', () => {
    const s = strip({ current: false })
    expect(revealActiveNavItem(s.el)).toBe(false)
    expect(s.scrollLeft()).toBe(0)
  })

  it('leaves the strip alone when the current entry is already in view', () => {
    const s = strip({ itemLeft: 100 })
    expect(revealActiveNavItem(s.el)).toBe(false)
    expect(s.scrollLeft()).toBe(0)
  })

  it('scrolls right until a current entry hidden past the right edge shows, with a margin', () => {
    // Entry spans 640..720 in the content; the strip shows 0..300.
    const s = strip({ itemLeft: 640, itemWidth: 80 })
    expect(revealActiveNavItem(s.el)).toBe(true)
    // right edge 720 + margin 16 - visible width 300 = 436
    expect(s.scrollLeft()).toBe(436)
  })

  it('never scrolls past the end of the content', () => {
    // The last entry touches the end of the content: the strip can only go to 800 - 300 = 500.
    const s = strip({ itemLeft: 720, itemWidth: 80 })
    expect(revealActiveNavItem(s.el)).toBe(true)
    expect(s.scrollLeft()).toBe(500)
  })

  it('scrolls back left for a current entry hidden before the left edge', () => {
    const s = strip({ scrollLeft: 500, itemLeft: 40, itemWidth: 80 })
    expect(revealActiveNavItem(s.el)).toBe(true)
    // entry now sits at -460; moving by -(460 + 16) from 500 lands on 24
    expect(s.scrollLeft()).toBe(24)
  })

  it('never scrolls to a negative position', () => {
    const s = strip({ scrollLeft: 10, itemLeft: 0, itemWidth: 80 })
    expect(revealActiveNavItem(s.el)).toBe(true)
    expect(s.scrollLeft()).toBe(0)
  })

  it('also recognises the selected tab of a tab strip', () => {
    const s = strip({ current: false, itemLeft: 0 })
    const tab = document.createElement('button')
    tab.setAttribute('aria-selected', 'true')
    tab.getBoundingClientRect = () => ({ left: 640 - s.scrollLeft(), right: 720 - s.scrollLeft(), top: 0, bottom: 40, width: 80, height: 40, x: 0, y: 0, toJSON: () => ({}) })
    s.el.appendChild(tab)
    expect(revealActiveNavItem(s.el)).toBe(true)
    expect(s.scrollLeft()).toBe(436)
  })

  it('glides with scrollTo when asked to, and jumps otherwise', () => {
    const s = strip({ itemLeft: 640 })
    const scrollTo = vi.fn()
    s.el.scrollTo = scrollTo as unknown as typeof s.el.scrollTo
    vi.stubGlobal('matchMedia', () => ({ matches: false }))
    expect(revealActiveNavItem(s.el, { smooth: true })).toBe(true)
    expect(scrollTo).toHaveBeenCalledWith({ left: 436, behavior: 'smooth' })
    expect(s.scrollLeft()).toBe(0)

    const jump = strip({ itemLeft: 640 })
    jump.el.scrollTo = vi.fn() as unknown as typeof jump.el.scrollTo
    expect(revealActiveNavItem(jump.el)).toBe(true)
    expect(jump.el.scrollTo).not.toHaveBeenCalled()
    expect(jump.scrollLeft()).toBe(436)
  })

  it('jumps instead of gliding when the visitor asked for reduced motion', () => {
    const s = strip({ itemLeft: 640 })
    s.el.scrollTo = vi.fn() as unknown as typeof s.el.scrollTo
    vi.stubGlobal('matchMedia', () => ({ matches: true }))
    expect(revealActiveNavItem(s.el, { smooth: true })).toBe(true)
    expect(s.el.scrollTo).not.toHaveBeenCalled()
    expect(s.scrollLeft()).toBe(436)
  })
})
