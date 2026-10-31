// Seeded PRNG so the painting is identical on every load and every machine.
export type Rng = () => number

export function mulberry32(seed: number): Rng {
  let a = seed >>> 0
  return () => {
    a = (a + 0x6d2b79f5) >>> 0
    let t = a
    t = Math.imul(t ^ (t >>> 15), t | 1)
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61)
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296
  }
}

export const range = (rng: Rng, min: number, max: number) => min + (max - min) * rng()

// Rough gaussian (sum of uniforms), clamped to +-3 sigma.
export function gauss(rng: Rng, sigma = 1): number {
  const v = (rng() + rng() + rng() + rng() - 2) * 1.2247
  return Math.max(-3, Math.min(3, v)) * sigma
}

export const pick = <T>(rng: Rng, items: readonly T[]): T => items[Math.floor(rng() * items.length) % items.length]

export const clamp = (v: number, lo: number, hi: number) => Math.min(hi, Math.max(lo, v))

export const lerp = (a: number, b: number, t: number) => a + (b - a) * t

export const smoothstep = (t: number) => {
  const x = clamp(t, 0, 1)
  return x * x * (3 - 2 * x)
}
