// Ink-wash brushwork for the sakura wood. Thick wood is painted with simulated bristles:
// each bristle carries its own ink load and runs dry on its own, which gives the fibrous
// dry-brush (feibai) texture and darker wet edges. Twigs are single crisp tapered strokes.
import type { Pt, SakuraModel, Stroke } from './model'
import { type Rng, mulberry32, range, gauss, clamp, lerp, smoothstep } from './random'

export type RGB = readonly [number, number, number]
export interface InkStyle { rgb: RGB; alpha: number; bleed: number }
export interface LayerSize { cssW: number; cssH: number; dpr: number; scale: number }

export const DAY_INK: InkStyle = { rgb: [30, 27, 26], alpha: 0.44, bleed: 0.15 }
export const NIGHT_INK: InkStyle = { rgb: [228, 216, 221], alpha: 0.2, bleed: 0.1 }

export const rgba = (c: RGB, a: number) => `rgba(${c[0]},${c[1]},${c[2]},${clamp(a, 0, 1).toFixed(3)})`

export function chaikin(pts: Pt[], iterations = 2): Pt[] {
  let out = pts
  for (let k = 0; k < iterations && out.length >= 3; k++) {
    const next: Pt[] = [out[0]]
    for (let i = 0; i < out.length - 1; i++) {
      const a = out[i]
      const b = out[i + 1]
      next.push({ x: a.x * 0.75 + b.x * 0.25, y: a.y * 0.75 + b.y * 0.25 })
      next.push({ x: a.x * 0.25 + b.x * 0.75, y: a.y * 0.25 + b.y * 0.75 })
    }
    next.push(out[out.length - 1])
    out = next
  }
  return out
}

// Slow brush-pressure variation in [-1, 1].
function valueNoise(rng: Rng, knots: number) {
  const v = Array.from({ length: knots + 1 }, () => rng() * 2 - 1)
  return (t: number) => {
    const x = clamp(t, 0, 1) * knots
    const i = Math.min(knots - 1, Math.floor(x))
    const f = x - i
    return lerp(v[i], v[i + 1], f * f * (3 - 2 * f))
  }
}

// eL / eR: per-point edge factors (around 1), so the outline wavers like ink on absorbent paper.
interface Ribbon { pts: Pt[]; nx: number[]; ny: number[]; w: number[]; t: number[]; acc: number[]; eL: number[]; eR: number[] }

function buildRibbon(s: Stroke, rng: Rng, widen = 1, extra = 0): Ribbon {
  const pts = s.pts.length > 2 ? chaikin(s.pts, 2) : s.pts
  const n = pts.length
  const acc = [0]
  for (let i = 1; i < n; i++) acc.push(acc[i - 1] + Math.hypot(pts[i].x - pts[i - 1].x, pts[i].y - pts[i - 1].y))
  const total = acc[n - 1] || 1
  const pressure = valueNoise(rng, 9)
  // Edge waver: a slow irregular swell plus fine paper grain (a single fast octave reads as a zip).
  const slow = Math.max(6, Math.round(total / 16))
  const fine = Math.max(12, Math.round(total / 5))
  const roughL = valueNoise(rng, slow)
  const roughR = valueNoise(rng, slow)
  const grainL = valueNoise(rng, fine)
  const grainR = valueNoise(rng, fine)
  const rough = [0.1, 0.07, 0.05, 0.035, 0.03, 0.03][Math.min(5, s.depth)]
  const grain = s.depth <= 1 ? 0.025 : 0.015
  const r: Ribbon = { pts, nx: [], ny: [], w: [], t: [], acc, eL: [], eR: [] }
  for (let i = 0; i < n; i++) {
    const a = pts[Math.max(0, i - 1)]
    const b = pts[Math.min(n - 1, i + 1)]
    const dx = b.x - a.x
    const dy = b.y - a.y
    const len = Math.hypot(dx, dy) || 1
    r.nx.push(-dy / len)
    r.ny.push(dx / len)
    const t = acc[i] / total
    r.t.push(t)
    let w = lerp(s.w0, s.w1, Math.pow(t, 0.85))
    if (s.depth > 0) w *= 0.88 + 0.12 * smoothstep(t / 0.07) // pressed, blunt start
    if (t > 0.88) w *= lerp(1, 0.28, (t - 0.88) / 0.12) // lifted, pointed finish
    w *= 1 + pressure(t) * (s.depth === 0 ? 0.1 : 0.16) // the brush presses and lifts
    r.w.push(Math.max(0.3, w * widen + extra))
    r.eL.push(1 + roughL(t) * rough + grainL(t) * grain)
    r.eR.push(1 + roughR(t) * rough + grainR(t) * grain)
  }
  return r
}

// Ribbon outline with a rounded start, so a limb leaving its parent does not end in a hard edge.
function fillRibbon(ctx: CanvasRenderingContext2D, r: Ribbon) {
  ctx.beginPath()
  for (let i = 0; i < r.pts.length; i++) {
    const h = (r.w[i] / 2) * r.eL[i]
    const x = r.pts[i].x + r.nx[i] * h
    const y = r.pts[i].y + r.ny[i] * h
    if (i === 0) ctx.moveTo(x, y)
    else ctx.lineTo(x, y)
  }
  for (let i = r.pts.length - 1; i >= 0; i--) {
    const h = (r.w[i] / 2) * r.eR[i]
    ctx.lineTo(r.pts[i].x - r.nx[i] * h, r.pts[i].y - r.ny[i] * h)
  }
  const p = r.pts[0]
  // From the -n edge round the back of the start (the -tangent side) to the +n edge.
  // Canvas angles grow clockwise, so passing behind the start means sweeping anticlockwise.
  ctx.arc(p.x, p.y, r.w[0] / 2, Math.atan2(-r.ny[0], -r.nx[0]), Math.atan2(r.ny[0], r.nx[0]), true)
  ctx.closePath()
  ctx.fill()
}

// Ink tone fades along the stroke as the brush unloads.
function toneFill(ctx: CanvasRenderingContext2D, r: Ribbon, style: InkStyle, tone: number) {
  const a = r.pts[0]
  const b = r.pts[r.pts.length - 1]
  if (Math.hypot(b.x - a.x, b.y - a.y) < 2) return rgba(style.rgb, tone)
  const g = ctx.createLinearGradient(a.x, a.y, b.x, b.y)
  g.addColorStop(0, rgba(style.rgb, tone))
  g.addColorStop(1, rgba(style.rgb, tone * 0.66))
  return g
}

function drawBristles(ctx: CanvasRenderingContext2D, r: Ribbon, s: Stroke, style: InkStyle, tone: number, rng: Rng) {
  const count = Math.round(clamp(s.w0 * 0.85, 4, 28))
  const loaded = s.depth === 0 ? 1 : rng() < 0.5 ? -1 : 1 // side of the brush that holds more ink
  ctx.lineCap = 'butt'
  ctx.lineJoin = 'round'
  for (let j = 0; j < count; j++) {
    const lat = -0.5 + (j + 0.5) / count + gauss(rng, 0.22 / count)
    const side = lat * loaded + 0.5 // 0 = dry side, 1 = loaded side
    const load = clamp(0.7 + 0.26 * side + gauss(rng, 0.08), 0.35, 1.05)
    const edge = Math.abs(lat) > 0.38 && side > 0.5 ? 1.12 : 1 // ink pools along the loaded edge only
    const base = tone * 0.56 * load * edge * range(rng, 0.8, 1.2)
    const widthShare = range(rng, 1.3, 2.3) / count
    const unload = s.dry * range(rng, 0.6, 1.4) * (1.15 - side * 0.5)
    let on = true
    let open = false
    let chunk = 0
    // Follow the wavering outline: scale the offset by the edge factor on this bristle's side.
    const off = (i: number) => r.w[i] * lat * (lat >= 0 ? r.eL[i] : r.eR[i])
    const begin = (i: number, ink: number) => {
      ctx.beginPath()
      ctx.moveTo(r.pts[i].x + r.nx[i] * off(i), r.pts[i].y + r.ny[i] * off(i))
      ctx.lineWidth = Math.max(0.25, r.w[i] * widthShare)
      ctx.strokeStyle = rgba(style.rgb, base * clamp(ink + 0.3, 0.4, 1))
      open = true
      chunk = 0
    }
    for (let i = 0; i < r.pts.length; i++) {
      const ink = load - unload * r.t[i]
      const step = i ? r.acc[i] - r.acc[i - 1] : 0
      const dryness = clamp(0.62 - ink, 0, 0.6) // the drier the bristle, the more it skips
      if (on && rng() < dryness * step * 0.09) on = false
      else if (!on && rng() < (0.6 - dryness) * step * 0.12 + 0.01) on = true
      if (!on || ink <= 0.05) {
        if (open) ctx.stroke()
        open = false
        continue
      }
      if (!open) {
        begin(i, ink)
        continue
      }
      ctx.lineTo(r.pts[i].x + r.nx[i] * off(i), r.pts[i].y + r.ny[i] * off(i))
      // Split long runs so the line can narrow and lighten along the stroke.
      if (++chunk >= 6) {
        ctx.stroke()
        begin(i, ink)
      }
    }
    if (open) ctx.stroke()
  }
}

function drawStroke(ctx: CanvasRenderingContext2D, s: Stroke, style: InkStyle, bleed: CanvasRenderingContext2D) {
  const rng = mulberry32(s.seed)
  const r = buildRibbon(s, rng)
  // Five tones of ink (jiao, nong, zhong, dan, qing): scorched-dark trunk, each order of branching lighter.
  const tone = style.alpha * Math.pow(s.ink, 1.4) * [1.6, 1.22, 0.9, 0.68, 0.56, 0.5][Math.min(5, s.depth)]
  if (s.w0 > 2.2) {
    bleed.fillStyle = rgba(style.rgb, style.bleed * s.ink)
    fillRibbon(bleed, buildRibbon(s, mulberry32(s.seed), 1.16, 0.8))
  }
  if (s.w0 < 3) {
    ctx.fillStyle = toneFill(ctx, r, style, tone)
    fillRibbon(ctx, r)
    return
  }
  // A light wash holds the stroke together; the bristles carry the tone and the texture.
  ctx.fillStyle = toneFill(ctx, r, style, tone * (s.depth === 0 ? 0.42 : 0.32))
  fillRibbon(ctx, r)
  drawBristles(ctx, r, s, style, tone, rng)
}

function makeCanvas(w: number, h: number): HTMLCanvasElement {
  const c = document.createElement('canvas')
  c.width = Math.max(1, w)
  c.height = Math.max(1, h)
  return c
}

// Everything that does not change between day and night: wood, flower stalks, moss, lenticels.
export function renderBranchLayer(model: SakuraModel, style: InkStyle, size: LayerSize): HTMLCanvasElement {
  const k = size.scale * size.dpr
  const W = Math.round(size.cssW * size.dpr)
  const H = Math.round(size.cssH * size.dpr)
  const out = makeCanvas(W, H)
  const crisp = makeCanvas(W, H)
  const wet = makeCanvas(W, H)
  const ctx = out.getContext('2d')
  const c = crisp.getContext('2d')
  const b = wet.getContext('2d')
  if (!ctx || !c || !b) return out
  c.setTransform(k, 0, 0, k, 0, 0)
  b.setTransform(k, 0, 0, k, 0, 0)

  // Pale, far wood first; the trunk last so it covers the roots of its limbs.
  for (const s of [...model.strokes].sort((a, z) => a.ink - z.ink || z.depth - a.depth)) drawStroke(c, s, style, b)

  c.lineCap = 'round'
  for (const bl of model.blossoms) {
    c.strokeStyle = rgba(style.rgb, style.alpha * 0.62 * bl.alpha)
    c.lineWidth = 0.5
    const mx = (bl.sx + bl.x) / 2 + (bl.y - bl.sy) * 0.12
    const my = (bl.sy + bl.y) / 2 - (bl.x - bl.sx) * 0.12
    c.beginPath()
    c.moveTo(bl.sx, bl.sy)
    c.quadraticCurveTo(mx, my, bl.x, bl.y)
    c.stroke()
  }

  for (const m of model.marks) {
    if (m.kind === 'moss') {
      c.fillStyle = rgba(style.rgb, style.alpha * m.alpha * 1.15)
      c.beginPath()
      c.ellipse(m.x, m.y, m.r, m.r * 0.72, m.rot, 0, Math.PI * 2)
      c.fill()
    } else {
      c.strokeStyle = rgba(style.rgb, style.alpha * m.alpha)
      c.lineWidth = m.r
      const dx = (Math.cos(m.rot) * m.len) / 2
      const dy = (Math.sin(m.rot) * m.len) / 2
      c.beginPath()
      c.moveTo(m.x - dx, m.y - dy)
      c.lineTo(m.x + dx, m.y + dy)
      c.stroke()
    }
  }

  // Bleed into the paper under the crisp ink. Older Safari has no ctx.filter: fake the blur with offsets.
  if (typeof (ctx as { filter?: unknown }).filter === 'string') {
    ctx.filter = `blur(${Math.max(1, 2.4 * k).toFixed(1)}px)`
    ctx.drawImage(wet, 0, 0)
    ctx.filter = 'none'
  } else {
    ctx.globalAlpha = 0.45
    for (const [dx, dy] of [[-1, 0], [1, 0], [0, -1], [0, 1]]) ctx.drawImage(wet, dx * k, dy * k)
    ctx.globalAlpha = 1
  }
  ctx.drawImage(crisp, 0, 0)
  return out
}
