import { mulberry32 } from './inkSakura/random'

export interface SakuraRect { left: number; top: number; right: number; bottom: number }
export interface SakuraPoint { x: number; y: number; size: number; rotation: number; duration: number; delay: number; drift: number }

const intersects = (a: SakuraRect, b: SakuraRect, gap = 0) =>
  a.left < b.right + gap && a.right + gap > b.left && a.top < b.bottom + gap && a.bottom + gap > b.top

/** Finds a few empty card areas; text, controls, and the existing tree stay clear. */
export function findSakuraPetalPlacements(
  surfaces: SakuraRect[],
  blockers: SakuraRect[],
  excluded: SakuraRect[],
  seed: number,
  maxCount: number,
): SakuraPoint[] {
  const random = mulberry32(seed)
  const points: SakuraPoint[] = []
  const shuffled = [...surfaces]
  for (let i = shuffled.length - 1; i > 0; i -= 1) {
    const j = Math.floor(random() * (i + 1))
    ;[shuffled[i], shuffled[j]] = [shuffled[j], shuffled[i]]
  }

  for (const surface of shuffled) {
    if (points.length >= maxCount) break
    if (surface.right - surface.left < 120 || surface.bottom - surface.top < 70) continue
    for (let attempt = 0; attempt < 8 && points.length < maxCount; attempt += 1) {
      const size = 10 + random() * 5
      const point = {
        x: surface.left + 14 + random() * Math.max(1, surface.right - surface.left - size - 28),
        y: surface.top + 12 + random() * Math.max(1, surface.bottom - surface.top - size - 24),
        size,
        rotation: random() * 120 - 60,
        duration: 24 + random() * 16,
        delay: -random() * 24,
        drift: random() * 24 - 12,
      }
      const candidate = { left: point.x, top: point.y, right: point.x + size, bottom: point.y + size }
      if (blockers.some(rect => intersects(candidate, rect, 7))) continue
      if (excluded.some(rect => intersects(candidate, rect, 3))) continue
      if (points.some(other => Math.hypot(other.x - point.x, other.y - point.y) < 100)) continue
      points.push(point)
      break
    }
  }
  return points
}
