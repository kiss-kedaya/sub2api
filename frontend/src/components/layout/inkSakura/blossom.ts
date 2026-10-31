// Sakura blossoms in a freehand (xieyi) manner: five notched petals in a pale pink
// wash with a deeper heart, ink stamens, ink calyx on buds. The only colour in the scene.
import { blossomOpenness, type Blossom, type Petal, type SakuraModel } from './model'
import { type RGB, rgba } from './ink'
import { type Rng, mulberry32, range, gauss, lerp, smoothstep } from './random'

export interface BlossomPalette {
  petal: RGB; petalA: number
  bud: RGB
  heart: RGB; heartA: number
  edge: RGB; edgeA: number
  ink: RGB; stamenA: number; calyxA: number
  fallenA: number
}

export const DAY_BLOSSOM: BlossomPalette = {
  petal: [242, 182, 198], petalA: 0.55,
  bud: [222, 126, 156],
  heart: [226, 130, 158], heartA: 0.3,
  edge: [210, 118, 146], edgeA: 0.26,
  ink: [44, 36, 38], stamenA: 0.6, calyxA: 0.5,
  fallenA: 0.55,
}

export const NIGHT_BLOSSOM: BlossomPalette = {
  petal: [176, 128, 146], petalA: 0.42,
  bud: [170, 112, 136],
  heart: [150, 96, 118], heartA: 0.2,
  edge: [150, 100, 120], edgeA: 0.18,
  ink: [214, 200, 206], stamenA: 0.3, calyxA: 0.34,
  fallenA: 0.3,
}

const mixRgb = (a: RGB, b: RGB, t: number): RGB => [lerp(a[0], b[0], t), lerp(a[1], b[1], t), lerp(a[2], b[2], t)]

export function mixPalette(a: BlossomPalette, b: BlossomPalette, t: number): BlossomPalette {
  return {
    petal: mixRgb(a.petal, b.petal, t), petalA: lerp(a.petalA, b.petalA, t),
    bud: mixRgb(a.bud, b.bud, t),
    heart: mixRgb(a.heart, b.heart, t), heartA: lerp(a.heartA, b.heartA, t),
    edge: mixRgb(a.edge, b.edge, t), edgeA: lerp(a.edgeA, b.edgeA, t),
    ink: mixRgb(a.ink, b.ink, t), stamenA: lerp(a.stamenA, b.stamenA, t), calyxA: lerp(a.calyxA, b.calyxA, t),
    fallenA: lerp(a.fallenA, b.fallenA, t),
  }
}

const wrap = (a: number) => Math.atan2(Math.sin(a), Math.cos(a))
const lerpAngle = (a: number, b: number, t: number) => a + wrap(b - a) * t

// One petal pointing along +x from the flower centre: rounded lobes with a small notch at the tip.
// `point` (0..1) narrows the tip: 0 = rounded notched petal, 1 = pointed petal of a closed bud.
export function petalPath(ctx: CanvasRenderingContext2D, len: number, wid: number, notch: number, point = 0) {
  const tip = lerp(0.2, 0.05, point)
  const shoulder = lerp(1.03, 0.82, point)
  ctx.beginPath()
  ctx.moveTo(0, 0)
  ctx.bezierCurveTo(len * 0.28, -wid * 0.6, len * shoulder, -wid * 0.58, len, -wid * tip)
  ctx.quadraticCurveTo(len - notch, 0, len, wid * tip)
  ctx.bezierCurveTo(len * shoulder, wid * 0.58, len * 0.28, wid * 0.6, 0, 0)
  ctx.closePath()
}

function drawFlower(ctx: CanvasRenderingContext2D, b: Blossom, open: number, pal: BlossomPalette, rng: Rng) {
  const jitter = Array.from({ length: 5 }, () => gauss(rng, 0.1))
  // Closed: petals fold into a plump teardrop along the stalk axis.
  const fold = Array.from({ length: 5 }, (_, i) => (i - 2) * 0.1 + gauss(rng, 0.03))
  const len = b.size * lerp(0.8, 1, open)
  const wid = len * lerp(0.56, 0.8, open)
  const notch = len * 0.17 * open
  const colour = mixRgb(pal.petal, pal.bud, 1 - open)
  const a = lerp(0.24, pal.petalA, open) * b.alpha
  // Some flowers face away or sideways: foreshorten the open flower, never the bud.
  const tiltRoll = rng()
  const squash = lerp(1, tiltRoll < 0.35 ? 1 : range(rng, 0.52, 0.92), open)
  const tiltDir = range(rng, 0, Math.PI)
  ctx.save()
  ctx.translate(b.x, b.y)
  if (squash < 0.999) {
    ctx.rotate(tiltDir)
    ctx.scale(1, squash)
    ctx.rotate(-tiltDir)
  }
  for (let i = 0; i < 5; i++) {
    const angle = lerpAngle(b.axis + fold[i], b.rot + (i * Math.PI * 2) / 5 + jitter[i], open)
    ctx.save()
    ctx.rotate(angle)
    petalPath(ctx, len * (1 + jitter[i] * 0.4), wid, notch, 1 - open)
    ctx.fillStyle = rgba(colour, a)
    ctx.fill()
    if (open > 0.3) {
      ctx.strokeStyle = rgba(pal.edge, pal.edgeA * b.alpha * open)
      ctx.lineWidth = 0.35
      ctx.stroke()
    }
    ctx.restore()
  }
  if (open > 0.15) {
    ctx.fillStyle = rgba(pal.heart, pal.heartA * b.alpha * open)
    ctx.beginPath()
    ctx.arc(0, 0, len * 0.3, 0, Math.PI * 2)
    ctx.fill()
  }
  if (open > 0.55) {
    const s = smoothstep((open - 0.55) / 0.45) * pal.stamenA * b.alpha
    ctx.strokeStyle = rgba(pal.ink, s * 0.8)
    ctx.fillStyle = rgba(pal.ink, s)
    ctx.lineWidth = 0.32
    for (let j = 0; j < 7; j++) {
      const t = b.rot + (j * Math.PI * 2) / 7 + gauss(rng, 0.18)
      const r1 = len * range(rng, 0.34, 0.48)
      ctx.beginPath()
      ctx.moveTo(Math.cos(t) * len * 0.08, Math.sin(t) * len * 0.08)
      ctx.lineTo(Math.cos(t) * r1, Math.sin(t) * r1)
      ctx.stroke()
      ctx.beginPath()
      ctx.arc(Math.cos(t) * r1, Math.sin(t) * r1, 0.55, 0, Math.PI * 2)
      ctx.fill()
    }
  }
  if (open < 0.75) {
    // Calyx: two small ink strokes cupping the base of the bud.
    const c = pal.calyxA * b.alpha * (1 - open / 0.75)
    ctx.strokeStyle = rgba(pal.ink, c)
    ctx.lineCap = 'round'
    ctx.lineWidth = 0.9
    for (const side of [-1, 1]) {
      const t = b.axis + Math.PI + side * 0.5
      ctx.beginPath()
      ctx.moveTo(Math.cos(b.axis) * len * 0.12, Math.sin(b.axis) * len * 0.12)
      ctx.lineTo(Math.cos(t) * len * 0.26, Math.sin(t) * len * 0.26)
      ctx.stroke()
    }
  }
  ctx.restore()
}

// Boneless (mogu) dab: overlapping washes with a few ink dots, for the looser, farther flowers.
function drawDab(ctx: CanvasRenderingContext2D, b: Blossom, open: number, pal: BlossomPalette, rng: Rng) {
  const r = b.size * lerp(0.42, 0.92, open)
  const colour = mixRgb(pal.petal, pal.bud, 1 - open)
  const cx = b.x + Math.cos(b.axis) * r * 0.3 * (1 - open)
  const cy = b.y + Math.sin(b.axis) * r * 0.3 * (1 - open)
  for (let i = 0; i < 4; i++) {
    const t = b.rot + i * 1.65 + range(rng, -0.25, 0.25)
    const d = r * 0.44 * open
    ctx.fillStyle = rgba(colour, lerp(0.24, pal.petalA * 0.78, open) * b.alpha)
    ctx.beginPath()
    ctx.ellipse(cx + Math.cos(t) * d, cy + Math.sin(t) * d, r * 0.6, r * 0.48, t, 0, Math.PI * 2)
    ctx.fill()
  }
  if (open > 0.5) {
    ctx.fillStyle = rgba(pal.ink, pal.stamenA * b.alpha * smoothstep((open - 0.5) / 0.5))
    for (let i = 0; i < 3; i++) {
      ctx.beginPath()
      ctx.arc(cx + gauss(rng, r * 0.16), cy + gauss(rng, r * 0.16), 0.6, 0, Math.PI * 2)
      ctx.fill()
    }
  }
}

export function drawBlossoms(ctx: CanvasRenderingContext2D, model: SakuraModel, pal: BlossomPalette, progress: number) {
  for (const b of model.blossoms) {
    const rng = mulberry32(b.seed)
    const open = blossomOpenness(b, progress)
    if (b.kind === 'dab') drawDab(ctx, b, open, pal, rng)
    else drawFlower(ctx, b, open, pal, rng)
  }
}

export function drawFallenPetals(ctx: CanvasRenderingContext2D, petals: Petal[], pal: BlossomPalette) {
  for (const p of petals) {
    ctx.save()
    ctx.translate(p.x, p.y)
    ctx.rotate(p.rot)
    ctx.translate(-p.size / 2, 0)
    petalPath(ctx, p.size, p.size * 0.74, p.size * 0.15)
    ctx.fillStyle = rgba(mixRgb(pal.petal, pal.bud, p.tint * 0.3), pal.fallenA * p.alpha)
    ctx.fill()
    ctx.restore()
  }
}
