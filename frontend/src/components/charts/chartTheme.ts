import { computed, onBeforeUnmount, onMounted, ref, type ComputedRef } from 'vue'

/**
 * Chart colours taken from the Nuxt UI tokens (`--ui-*`) and resolved to hex, so chart.js can
 * paint them and `${hex}20`-style alpha suffixes keep working. They follow dark mode (the `dark`
 * class on <html>) and the admin's Appearance settings (the `#site-theme-vars` style element).
 * Without a rendering engine (jsdom in specs) every token falls back to the previous fixed colour.
 */

type Token = { css: string; light: string; dark: string }

const token = (css: string, light: string, dark = light): Token => ({ css, light, dark })
/** `weight`% of `color`, the rest `other` — tints / shades that stay inside the theme. */
const mix = (color: string, other: string, weight: number) =>
  `color-mix(in oklab, var(${color}) ${weight}%, var(${other}))`

const SITE_THEME_STYLE_ID = 'site-theme-vars'

// Slots 0-11 fall back to the palette the charts used before, so stored expectations stay valid.
const CATEGORICAL: Token[] = [
  token('var(--ui-info)', '#3b82f6'),
  token('var(--ui-success)', '#10b981'),
  token('var(--ui-warning)', '#f59e0b'),
  token('var(--ui-error)', '#ef4444'),
  token('var(--ui-primary)', '#8b5cf6'),
  token(mix('--ui-info', '--ui-bg', 55), '#ec4899'),
  token(mix('--ui-success', '--ui-bg', 55), '#14b8a6'),
  token(mix('--ui-warning', '--ui-bg', 55), '#f97316'),
  token(mix('--ui-error', '--ui-bg', 55), '#6366f1'),
  token(mix('--ui-primary', '--ui-bg', 55), '#84cc16'),
  token(mix('--ui-info', '--ui-text-highlighted', 62), '#06b6d4'),
  token(mix('--ui-success', '--ui-text-highlighted', 62), '#a855f7'),
  token(mix('--ui-warning', '--ui-text-highlighted', 62), '#0ea5e9'),
  token(mix('--ui-error', '--ui-text-highlighted', 62), '#22c55e'),
  token(mix('--ui-primary', '--ui-text-highlighted', 62), '#eab308')
]

const TOKENS = {
  /** Axis ticks, legend labels and the default single-series line. */
  text: token('var(--ui-text-muted)', '#374151', '#e5e7eb'),
  grid: token('var(--ui-border-muted)', '#e5e7eb', '#374151'),
  surface: token('var(--ui-bg)', '#ffffff', '#18181b'),
  tooltipBg: token('var(--ui-bg)', '#ffffff', '#18181b'),
  tooltipTitle: token('var(--ui-text-highlighted)', '#111827', '#ffffff'),
  tooltipBody: token('var(--ui-text-muted)', '#4b5563', '#d1d5db'),
  tooltipBorder: token('var(--ui-border)', '#e5e7eb', '#3f3f46'),
  /** The "others" slice of a ranking. */
  other: token('var(--ui-text-dimmed)', '#94a3b8'),
  primary: token('var(--ui-primary)', '#171717', '#e5e5e5'),
  input: token('var(--ui-info)', '#3b82f6'),
  output: token('var(--ui-success)', '#10b981'),
  cacheCreation: token('var(--ui-warning)', '#f59e0b'),
  cacheRead: token(mix('--ui-info', '--ui-bg', 55), '#06b6d4'),
  cacheHitRate: token('var(--ui-primary)', '#8b5cf6')
} as const

export type ChartTheme = { [K in keyof typeof TOKENS]: string } & { categorical: string[] }

const isRenderingEngine = () =>
  typeof window !== 'undefined' && typeof document !== 'undefined' && !/jsdom/i.test(navigator.userAgent)

const toHex = (r: number, g: number, b: number, a = 1) => {
  const part = (value: number) => Math.round(Math.min(255, Math.max(0, value))).toString(16).padStart(2, '0')
  return `#${part(r)}${part(g)}${part(b)}${a >= 0.999 ? '' : part(a * 255)}`
}

function createReader(dark: boolean) {
  if (!isRenderingEngine()) {
    return { read: (t: Token) => (dark ? t.dark : t.light), dispose: () => {} }
  }
  // A hidden probe resolves var() / color-mix() chains; a 1px canvas turns any colour space into sRGB.
  const probe = document.createElement('span')
  probe.style.display = 'none'
  document.body.appendChild(probe)
  const canvas = document.createElement('canvas')
  canvas.width = canvas.height = 1
  const ctx = canvas.getContext('2d', { willReadFrequently: true })

  const normalize = (color: string, fallback: string) => {
    const rgb = /^rgba?\(\s*([\d.]+)[,\s]+([\d.]+)[,\s]+([\d.]+)(?:\s*[,/]\s*([\d.]+)(%?))?\s*\)$/i.exec(color)
    if (rgb) {
      const alpha = rgb[4] === undefined ? 1 : Number(rgb[4]) / (rgb[5] ? 100 : 1)
      return toHex(Number(rgb[1]), Number(rgb[2]), Number(rgb[3]), alpha)
    }
    if (!ctx) return fallback
    ctx.clearRect(0, 0, 1, 1)
    ctx.fillStyle = fallback
    ctx.fillStyle = color
    ctx.fillRect(0, 0, 1, 1)
    const [r, g, b, a] = ctx.getImageData(0, 0, 1, 1).data
    return toHex(r, g, b, a / 255)
  }

  return {
    read: (t: Token) => {
      const fallback = dark ? t.dark : t.light
      probe.style.color = ''
      probe.style.color = t.css
      const computed = getComputedStyle(probe).color
      return computed ? normalize(computed, fallback) : fallback
    },
    dispose: () => probe.remove()
  }
}

function resolveTheme(): ChartTheme {
  const dark = typeof document !== 'undefined' && document.documentElement.classList.contains('dark')
  const { read, dispose } = createReader(dark)
  try {
    const entries = Object.entries(TOKENS).map(([key, t]) => [key, read(t)])
    return { ...(Object.fromEntries(entries) as { [K in keyof typeof TOKENS]: string }), categorical: CATEGORICAL.map(read) }
  } finally {
    dispose()
  }
}

// One observer for all mounted charts: dark-mode toggles and Appearance changes bump the version.
const themeVersion = ref(0)
let users = 0
let observer: MutationObserver | null = null

const isThemeNode = (node: Node | null) =>
  node instanceof Element
    ? node.id === SITE_THEME_STYLE_ID
    : !!node?.parentElement && node.parentElement.id === SITE_THEME_STYLE_ID

function startObserving() {
  if (typeof MutationObserver === 'undefined' || typeof document === 'undefined') return
  observer = new MutationObserver((records) => {
    const changed = records.some((record) =>
      record.type === 'attributes' ||
      isThemeNode(record.target) ||
      [...record.addedNodes, ...record.removedNodes].some(isThemeNode)
    )
    if (changed) themeVersion.value++
  })
  observer.observe(document.documentElement, { attributes: true, attributeFilter: ['class'] })
  observer.observe(document.head, { childList: true, subtree: true, characterData: true })
}

/** Reactive chart colours for the calling component. */
export function useChartTheme(): ComputedRef<ChartTheme> {
  onMounted(() => {
    if (users++ === 0) startObserving()
  })
  onBeforeUnmount(() => {
    if (--users === 0) {
      observer?.disconnect()
      observer = null
    }
  })
  return computed(() => {
    void themeVersion.value
    return resolveTheme()
  })
}

/** Quiet segmented toggle for chart headers: neutral track, raised segment for the active item. */
export const segmentedTabsUi = {
  root: 'gap-0',
  list: 'w-auto bg-elevated p-0.5',
  indicator: 'bg-default shadow-xs ring ring-default',
  trigger: 'px-2.5 py-1 text-xs data-[state=active]:text-highlighted'
}

/** Doughnut slices separated by a hairline in the page colour. */
export function doughnutDataset(theme: ChartTheme, data: number[], backgroundColor: string[]) {
  return { data, backgroundColor, borderColor: theme.surface, borderWidth: 2, hoverOffset: 4 }
}

/** Tooltip chrome like Nuxt UI popovers: page background, hairline border, muted body text. */
export function chartTooltipStyle(theme: ChartTheme) {
  return {
    backgroundColor: theme.tooltipBg,
    titleColor: theme.tooltipTitle,
    bodyColor: theme.tooltipBody,
    footerColor: theme.tooltipBody,
    borderColor: theme.tooltipBorder,
    borderWidth: 1,
    padding: 10,
    cornerRadius: 6,
    boxPadding: 4,
    usePointStyle: true
  }
}
