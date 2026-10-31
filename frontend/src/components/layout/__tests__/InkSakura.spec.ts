import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import InkSakura from '../InkSakura.vue'
import { buildSakura, blossomOpenness, VH, VW } from '../inkSakura/model'
import { computeLayout, MIN_VIEWPORT_W } from '../inkSakura/scene'

describe('ink sakura model', () => {
  it('is deterministic for a seed, so every visitor sees the same painting', () => {
    expect(JSON.stringify(buildSakura(7))).toBe(JSON.stringify(buildSakura(7)))
    expect(JSON.stringify(buildSakura(7))).not.toBe(JSON.stringify(buildSakura(8)))
  })

  it('stays sparse: one trunk, a few dozen strokes, around a hundred blossoms', () => {
    const m = buildSakura()
    expect(m.strokes.filter((s) => s.depth === 0)).toHaveLength(1)
    expect(m.strokes.length).toBeGreaterThan(20)
    expect(m.strokes.length).toBeLessThan(140)
    expect(m.blossoms.length).toBeGreaterThan(50)
    expect(m.blossoms.length).toBeLessThan(180)
    expect(m.blossoms.some((b) => b.kind === 'bud')).toBe(true)
  })

  it('keeps most blossoms inside the painting area', () => {
    const m = buildSakura()
    const inside = m.blossoms.filter((b) => b.x > -20 && b.x < VW + 20 && b.y > -20 && b.y < VH + 20)
    expect(inside.length / m.blossoms.length).toBeGreaterThan(0.8)
  })

  it('opens flowers fully by day and closes them at night; buds stay mostly closed', () => {
    expect(blossomOpenness({ kind: 'flower', phase: 0.2 }, 1)).toBe(1)
    expect(blossomOpenness({ kind: 'flower', phase: 0.2 }, 0)).toBe(0)
    expect(blossomOpenness({ kind: 'bud', phase: 0 }, 1)).toBeLessThan(0.2)
    const a = blossomOpenness({ kind: 'flower', phase: 0.1 }, 0.4)
    const b = blossomOpenness({ kind: 'flower', phase: 0.1 }, 0.6)
    expect(b).toBeGreaterThan(a)
  })
})

describe('ink sakura layout', () => {
  it('is hidden on phones and small windows', () => {
    expect(computeLayout(MIN_VIEWPORT_W - 1, 900, 2)).toBeNull()
    expect(computeLayout(1440, 400, 1)).toBeNull()
  })

  it('fills the viewport height on desktop and never takes more than 62% of the width', () => {
    const l = computeLayout(1440, 900, 3)!
    expect(l.cssH).toBe(900)
    expect(l.cssW).toBeLessThanOrEqual(Math.round(1440 * 0.62))
    expect(l.dpr).toBe(1.5)
    const narrow = computeLayout(700, 1200, 1)!
    expect(narrow.cssW).toBeLessThanOrEqual(Math.round(700 * 0.62))
  })
})

describe('InkSakura component', () => {
  it('renders a decorative, non-interactive canvas', () => {
    const wrapper = mount(InkSakura)
    const canvas = wrapper.get('canvas')
    expect(canvas.attributes('aria-hidden')).toBe('true')
    expect(canvas.classes()).toContain('ink-sakura')
    wrapper.unmount()
  })
})
