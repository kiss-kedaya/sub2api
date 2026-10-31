// Layout, compositing and the live controller for the sakura backdrop.
// The wood is rendered once per theme and size and cached; only the blossoms are
// redrawn, and only while the day/night transition runs.
import { buildSakura, VH, VW, type SakuraModel } from './model'
import { DAY_INK, NIGHT_INK, renderBranchLayer } from './ink'
import { DAY_BLOSSOM, NIGHT_BLOSSOM, drawBlossoms, drawFallenPetals, mixPalette } from './blossom'
import { lerp } from './random'

export interface SceneLayout { cssW: number; cssH: number; dpr: number; scale: number }
export interface SceneFrame { night: number; progress: number } // night: ink/palette mix, progress: 0 closed .. 1 open

export const MIN_VIEWPORT_W = 640
const TRANSITION_MS = 1600

export function computeLayout(vw: number, vh: number, devicePixelRatio = 1): SceneLayout | null {
  if (vw < MIN_VIEWPORT_W || vh < 420) return null
  const scale = Math.min(vh / VH, (vw * 0.62) / VW)
  return { cssW: Math.round(VW * scale), cssH: Math.round(VH * scale), dpr: Math.min(1.5, Math.max(1, devicePixelRatio || 1)), scale }
}

export function paintScene(
  ctx: CanvasRenderingContext2D,
  model: SakuraModel,
  layout: SceneLayout,
  dayLayer: CanvasImageSource | null,
  nightLayer: CanvasImageSource | null,
  frame: SceneFrame,
) {
  ctx.setTransform(1, 0, 0, 1, 0, 0)
  ctx.globalAlpha = 1
  ctx.clearRect(0, 0, ctx.canvas.width, ctx.canvas.height)
  if (dayLayer && frame.night < 0.999) {
    ctx.globalAlpha = 1 - frame.night
    ctx.drawImage(dayLayer, 0, 0)
  }
  if (nightLayer && frame.night > 0.001) {
    ctx.globalAlpha = frame.night
    ctx.drawImage(nightLayer, 0, 0)
  }
  ctx.globalAlpha = 1
  const k = layout.scale * layout.dpr
  ctx.setTransform(k, 0, 0, k, 0, 0)
  const pal = mixPalette(DAY_BLOSSOM, NIGHT_BLOSSOM, frame.night)
  drawFallenPetals(ctx, model.petals, pal)
  drawBlossoms(ctx, model, pal, frame.progress)
  ctx.setTransform(1, 0, 0, 1, 0, 0)
}

let sharedModel: SakuraModel | null = null
let paintedOnce = false
const layers = new Map<string, HTMLCanvasElement>()

const getModel = () => (sharedModel ??= buildSakura())
const layerKey = (night: boolean, l: SceneLayout) => `${night ? 'n' : 'd'}:${l.cssW}x${l.cssH}@${l.dpr}`

function layerFor(night: boolean, l: SceneLayout): HTMLCanvasElement {
  const key = layerKey(night, l)
  let layer = layers.get(key)
  if (!layer) {
    layer = renderBranchLayer(getModel(), night ? NIGHT_INK : DAY_INK, l)
    layers.set(key, layer)
    while (layers.size > 4) layers.delete(layers.keys().next().value as string)
  }
  return layer
}

export const hasPaintedOnce = () => paintedOnce
export const isDarkTheme = () => document.documentElement.classList.contains('dark')

type IdleHandle = { cancel: () => void }
function whenIdle(fn: () => void): IdleHandle {
  const w = window as Window & { requestIdleCallback?: (cb: () => void, o?: { timeout: number }) => number; cancelIdleCallback?: (id: number) => void }
  if (w.requestIdleCallback && w.cancelIdleCallback) {
    const id = w.requestIdleCallback(fn, { timeout: 900 })
    return { cancel: () => w.cancelIdleCallback?.(id) }
  }
  const id = window.setTimeout(fn, 120)
  return { cancel: () => window.clearTimeout(id) }
}

// Paints into `canvas`, follows the theme class on <html> and the viewport size.
export function mountInkSakura(canvas: HTMLCanvasElement, onShown: () => void): () => void {
  const ctx = canvas.getContext('2d')
  if (!ctx) return () => {}
  const reduceMotion = window.matchMedia?.('(prefers-reduced-motion: reduce)')
  let layout: SceneLayout | null = null
  let targetNight = isDarkTheme() ? 1 : 0
  let night = targetNight
  let progress = 1 - targetNight
  let raf = 0
  let resizeTimer: number | undefined
  let idle: IdleHandle | null = null
  let destroyed = false

  const draw = () => {
    if (!layout || destroyed) return
    const day = night < 0.999 ? layerFor(false, layout) : null
    const dark = night > 0.001 ? layerFor(true, layout) : null
    paintScene(ctx, getModel(), layout, day, dark, { night, progress })
  }

  // Render the other theme's wood while the browser is idle, so the first toggle does not stutter.
  const prefetch = () => {
    idle?.cancel()
    idle = whenIdle(() => {
      if (layout && !destroyed) layerFor(targetNight === 0, layout)
    })
  }

  const applyLayout = (): boolean => {
    layout = computeLayout(window.innerWidth, window.innerHeight, window.devicePixelRatio)
    if (!layout) {
      ctx.clearRect(0, 0, canvas.width, canvas.height)
      return false
    }
    canvas.width = Math.round(layout.cssW * layout.dpr)
    canvas.height = Math.round(layout.cssH * layout.dpr)
    canvas.style.width = `${layout.cssW}px`
    canvas.style.height = `${layout.cssH}px`
    return true
  }

  const show = () => {
    paintedOnce = true
    onShown()
    prefetch()
  }

  const start = () => {
    if (!applyLayout() || !layout) return
    if (layers.has(layerKey(targetNight === 1, layout))) {
      draw()
      show()
      return
    }
    idle = whenIdle(() => {
      draw()
      show()
    })
  }

  const animateTo = (toNight: number) => {
    cancelAnimationFrame(raf)
    if (reduceMotion?.matches || !layout) {
      night = toNight
      progress = 1 - toNight
      draw()
      return
    }
    const n0 = night
    const p0 = progress
    const t0 = performance.now()
    const step = (now: number) => {
      const t = Math.min(1, (now - t0) / TRANSITION_MS)
      const e = t < 0.5 ? 2 * t * t : 1 - Math.pow(-2 * t + 2, 2) / 2
      night = lerp(n0, toNight, e)
      progress = lerp(p0, 1 - toNight, e)
      draw()
      if (t < 1 && !destroyed) raf = requestAnimationFrame(step)
    }
    raf = requestAnimationFrame(step)
  }

  const observer = new MutationObserver(() => {
    const next = isDarkTheme() ? 1 : 0
    if (next === targetNight) return
    targetNight = next
    animateTo(next)
    prefetch()
  })
  observer.observe(document.documentElement, { attributes: true, attributeFilter: ['class'] })

  const onResize = () => {
    window.clearTimeout(resizeTimer)
    resizeTimer = window.setTimeout(() => {
      if (applyLayout()) draw()
    }, 180)
  }
  window.addEventListener('resize', onResize, { passive: true })

  start()

  return () => {
    destroyed = true
    cancelAnimationFrame(raf)
    idle?.cancel()
    window.clearTimeout(resizeTimer)
    observer.disconnect()
    window.removeEventListener('resize', onResize)
  }
}
