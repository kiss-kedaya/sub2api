// Pure geometry for the ink-wash sakura backdrop. No DOM access, so it is unit-testable.
// Virtual space: VW x VH, y grows downward. The trunk enters from the bottom-right edge.
import { type Rng, mulberry32, range, gauss, clamp, lerp } from './random'

export const VW = 760
export const VH = 1000

export interface Pt { x: number; y: number }
export interface Stroke { pts: Pt[]; w0: number; w1: number; ink: number; dry: number; depth: number; seed: number }
export type BlossomKind = 'flower' | 'bud' | 'dab'
export interface Blossom {
  x: number; y: number // flower centre
  sx: number; sy: number // where the stalk leaves the twig
  axis: number // direction the closed bud points
  rot: number // rotation of the open flower
  size: number // petal length
  kind: BlossomKind
  tint: number // 0..1 colour variation
  alpha: number // tonal depth (far branches are paler)
  phase: number // 0..0.4, staggers the open/close wave from trunk to tips
  seed: number
}
export interface Petal { x: number; y: number; rot: number; size: number; tint: number; alpha: number }
export interface Mark { x: number; y: number; r: number; rot: number; len: number; alpha: number; kind: 'moss' | 'lenticel' }
export interface SakuraModel { strokes: Stroke[]; blossoms: Blossom[]; petals: Petal[]; marks: Mark[] }

interface Sample { x: number; y: number; angle: number }
interface BranchSpec { x: number; y: number; angle: number; length: number; width: number; depth: number; ink: number; dry: number; gravity: number }
interface Ctx { rng: Rng; model: SakuraModel }

const MAX_DEPTH = 4

export function measure(pts: Pt[]): number[] {
  const acc = [0]
  for (let i = 1; i < pts.length; i++) acc.push(acc[i - 1] + Math.hypot(pts[i].x - pts[i - 1].x, pts[i].y - pts[i - 1].y))
  return acc
}

export function sampleAt(pts: Pt[], acc: number[], t: number): Sample {
  const target = acc[acc.length - 1] * clamp(t, 0, 1)
  let i = 1
  while (i < acc.length - 1 && acc[i] < target) i++
  const a = pts[i - 1]
  const b = pts[i]
  const span = acc[i] - acc[i - 1] || 1
  const f = clamp((target - acc[i - 1]) / span, 0, 1)
  return { x: lerp(a.x, b.x, f), y: lerp(a.y, b.y, f), angle: Math.atan2(b.y - a.y, b.x - a.x) }
}

// A limb that keeps a slow curve, sags under its own weight and turns at the odd node,
// which gives cherry wood its angular, zig-zag character.
function growPath(rng: Rng, x: number, y: number, angle: number, length: number, wander: number, gravity: number, kink: number): Pt[] {
  const steps = Math.max(5, Math.round(length / 8))
  const seg = length / steps
  const pts: Pt[] = [{ x, y }]
  let a = angle
  let bend = gauss(rng, 0.02)
  for (let i = 0; i < steps; i++) {
    bend = bend * 0.86 + gauss(rng, wander)
    a += bend
    if (rng() < kink) a += gauss(rng, 0.24)
    let dx = Math.cos(a)
    let dy = Math.sin(a) + gravity
    const n = Math.hypot(dx, dy) || 1
    dx /= n
    dy /= n
    a = Math.atan2(dy, dx)
    x += dx * seg
    y += dy * seg
    pts.push({ x, y })
  }
  return pts
}

function cluster(c: Ctx, at: Sample, s: BranchSpec, spur: boolean) {
  const { rng, model } = c
  const n = Math.max(1, Math.round(range(rng, spur ? 0.6 : 0.9, spur ? 1.8 : 2.9)))
  for (let i = 0; i < n; i++) {
    const roll = rng()
    const kind: BlossomKind = roll < 0.17 ? 'bud' : roll < 0.27 ? 'dab' : 'flower'
    // Stalks fan out from one point and hang a little, like a real sakura corymb.
    const fan = at.angle + gauss(rng, 0.95)
    let dx = Math.cos(fan)
    let dy = Math.sin(fan) + 0.32
    const len = Math.hypot(dx, dy) || 1
    dx /= len
    dy /= len
    const stalk = range(rng, 6, 14)
    model.blossoms.push({
      x: at.x + dx * stalk,
      y: at.y + dy * stalk,
      sx: at.x,
      sy: at.y,
      axis: Math.atan2(dy, dx),
      rot: range(rng, 0, Math.PI * 2),
      size: kind === 'bud' ? range(rng, 4.6, 6.4) : range(rng, 7, 10.4),
      kind,
      tint: rng(),
      alpha: clamp(0.82 + (s.ink - 0.85) * 0.9 + gauss(rng, 0.05), 0.5, 1),
      phase: 0,
      seed: Math.floor(rng() * 1e9),
    })
  }
}

function branch(c: Ctx, s: BranchSpec) {
  const { rng, model } = c
  // Cherry wood turns at nodes rather than curving smoothly; twigs turn more often.
  const wander = 0.004 + s.depth * 0.0026
  const pts = growPath(rng, s.x, s.y, s.angle, s.length, wander, s.gravity, s.depth <= 1 ? 0.08 : 0.13)
  const tipW = Math.max(0.45, s.width * (s.depth <= 1 ? 0.4 : 0.28))
  model.strokes.push({ pts, w0: s.width, w1: tipW, ink: s.ink, dry: s.dry, depth: s.depth, seed: Math.floor(rng() * 1e9) })
  const acc = measure(pts)
  const tip = sampleAt(pts, acc, 1)

  if (s.depth >= MAX_DEPTH || s.width < 1.4) {
    cluster(c, tip, s, false)
    return
  }

  const count = s.depth === 1 ? Math.round(range(rng, 1.8, 3.2)) : s.depth === 2 ? Math.round(range(rng, 0.8, 2.2)) : Math.round(range(rng, 0.2, 1.6))
  let side = rng() < 0.5 ? -1 : 1
  const ts = Array.from({ length: count }, () => range(rng, 0.28, 0.94)).sort((a, b) => a - b)
  for (const t of ts) {
    const at = sampleAt(pts, acc, t)
    const wAt = lerp(s.width, tipW, t)
    const spread = range(rng, 0.4, 0.88)
    let a = at.angle + side * spread
    if (Math.sin(a) > 0.42) a = at.angle - side * spread * 0.55 // never let a child dive downward
    branch(c, {
      x: at.x,
      y: at.y,
      angle: a,
      length: s.length * (s.depth >= 2 ? range(rng, 0.34, 0.52) : range(rng, 0.42, 0.64)) * (1 - t * 0.3),
      width: wAt * range(rng, 0.52, 0.7),
      depth: s.depth + 1,
      ink: clamp(s.ink - range(rng, 0.02, 0.09), 0.45, 1),
      dry: s.dry * 0.55,
      gravity: s.gravity * 1.25,
    })
    side = -side
  }

  if (s.depth >= 1) cluster(c, tip, s, false)

  // Short flowering spurs along the wood (cherry also flowers on older limbs).
  if (s.depth >= 1) {
    const spurs = Math.round(range(rng, 0, s.depth === 1 ? 1.3 : 1.1))
    for (let i = 0; i < spurs; i++) {
      const at = sampleAt(pts, acc, range(rng, 0.25, 0.85))
      const a = at.angle + (rng() < 0.5 ? -1 : 1) * range(rng, 0.7, 1.3)
      const len = range(rng, 5, 11)
      const end = { x: at.x + Math.cos(a) * len, y: at.y + Math.sin(a) * len, angle: a }
      model.strokes.push({ pts: [{ x: at.x, y: at.y }, { x: end.x, y: end.y }], w0: Math.max(0.6, lerp(s.width, tipW, 0.5) * 0.5), w1: 0.45, ink: s.ink, dry: 0, depth: s.depth + 1, seed: Math.floor(rng() * 1e9) })
      cluster(c, end, s, true)
    }
  }
}

export function buildSakura(seed = 20261031): SakuraModel {
  const rng = mulberry32(seed)
  const model: SakuraModel = { strokes: [], blossoms: [], petals: [], marks: [] }
  const c: Ctx = { rng, model }

  // Trunk: enters below the bottom edge leaning left, then turns back up (a dog-leg, not a pole).
  const trunkW = 40
  const lower = growPath(rng, 700, VH + 80, -1.98, 270, 0.004, 0, 0.03)
  const end = lower[lower.length - 1]
  const upper = growPath(rng, end.x, end.y, -1.46, 230, 0.005, 0, 0.04)
  const trunk = [...lower, ...upper.slice(1)]
  model.strokes.push({ pts: trunk, w0: trunkW, w1: trunkW * 0.46, ink: 1, dry: 0.85, depth: 0, seed: Math.floor(rng() * 1e9) })
  const acc = measure(trunk)

  // Limbs: [position on trunk, direction, length, share of trunk width, ink tone, gravity]
  const limbs: Array<[number, number, number, number, number, number]> = [
    [0.66, -2.48, 540, 0.56, 0.95, 0.0035], // long sweeping arm to the upper left
    [0.86, -2.0, 360, 0.5, 0.92, 0.003], // upper left
    [0.99, -1.5, 300, 0.58, 0.9, 0.002], // crown, rising
    [0.5, -0.5, 230, 0.4, 0.86, 0.004], // exits the right edge
    [0.36, -2.9, 260, 0.3, 0.58, 0.008], // low, pale, far branch
  ]
  for (const [t, angle, length, share, ink, gravity] of limbs) {
    const at = sampleAt(trunk, acc, t)
    const w = lerp(trunkW, trunkW * 0.52, t) * share
    branch(c, { x: at.x, y: at.y, angle, length, width: w, depth: 1, ink, dry: 0.5, gravity })
    // Moss dots where the limb leaves the trunk.
    const dots = Math.round(range(rng, 2, 4))
    for (let i = 0; i < dots; i++) {
      model.marks.push({ kind: 'moss', x: at.x + gauss(rng, w * 0.5), y: at.y + gauss(rng, w * 0.35), r: range(rng, 1, 2.2), rot: range(rng, 0, Math.PI), len: 0, alpha: range(rng, 0.45, 0.8) })
    }
  }

  // Horizontal lenticels on the trunk bark.
  for (let i = 0; i < 9; i++) {
    const t = range(rng, 0.06, 0.92)
    const at = sampleAt(trunk, acc, t)
    const w = lerp(trunkW, trunkW * 0.52, t)
    model.marks.push({ kind: 'lenticel', x: at.x + gauss(rng, w * 0.18), y: at.y, r: range(rng, 0.7, 1.3), rot: at.angle + Math.PI / 2 + gauss(rng, 0.12), len: w * range(rng, 0.22, 0.48), alpha: range(rng, 0.35, 0.7) })
  }

  // Open/close wave: blossoms near the trunk move first, twig tips last.
  const top = sampleAt(trunk, acc, 1)
  const far = Math.max(...model.blossoms.map((b) => Math.hypot(b.x - top.x, b.y - top.y)), 1)
  for (const b of model.blossoms) b.phase = clamp((Math.hypot(b.x - top.x, b.y - top.y) / far) * 0.32 + rng() * 0.08, 0, 0.4)

  // A few fallen petals on the ground and drifting.
  for (let i = 0; i < 9; i++) {
    const drifting = i < 3
    model.petals.push({
      x: drifting ? range(rng, 220, 520) : range(rng, 180, 700),
      y: drifting ? range(rng, 560, 820) : range(rng, 905, 985),
      rot: range(rng, 0, Math.PI * 2),
      size: range(rng, 4.4, 6.4),
      tint: rng(),
      alpha: drifting ? range(rng, 0.5, 0.75) : range(rng, 0.45, 0.85),
    })
  }
  return model
}

// Per-blossom openness for a global progress p (0 = night, closed; 1 = day, open).
export function blossomOpenness(b: Pick<Blossom, 'phase' | 'kind'>, p: number): number {
  const local = clamp((p - b.phase) / (1 - 0.4), 0, 1)
  const eased = local * local * (3 - 2 * local)
  return b.kind === 'bud' ? eased * 0.16 : eased
}
