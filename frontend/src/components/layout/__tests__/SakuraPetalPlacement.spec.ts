import { describe, expect, it } from 'vitest'
import { findSakuraPetalPlacements } from '../sakuraPetalPlacement'

describe('sakura petal placement', () => {
  const surface = { left: 0, top: 0, right: 320, bottom: 180 }

  it('is deterministic per render and limits the number of marks', () => {
    const first = findSakuraPetalPlacements([surface], [], [], 7, 5)
    expect(findSakuraPetalPlacements([surface], [], [], 7, 5)).toEqual(first)
    expect(first.length).toBeLessThanOrEqual(5)
  })

  it('does not place petals over text/control blockers or the existing tree', () => {
    const blockers = [{ left: 0, top: 0, right: 320, bottom: 110 }]
    expect(findSakuraPetalPlacements([surface], blockers, [], 11, 5)).toHaveLength(0)
    expect(findSakuraPetalPlacements([surface], [], [surface], 11, 5)).toHaveLength(0)
  })

  it('keeps flowers sparse with spacing between accepted marks', () => {
    const petals = findSakuraPetalPlacements([surface, { left: 330, top: 200, right: 650, bottom: 380 }], [], [], 19, 7)
    expect(petals.length).toBeLessThanOrEqual(2)
    for (let i = 0; i < petals.length; i += 1) {
      for (let j = i + 1; j < petals.length; j += 1) {
        expect(Math.hypot(petals[i].x - petals[j].x, petals[i].y - petals[j].y)).toBeGreaterThanOrEqual(100)
      }
    }
  })
})
