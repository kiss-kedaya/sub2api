import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useSiteTheme } from '@/composables/useSiteTheme'

/**
 * Shared look for the admin and user dashboards, after the Nuxt UI dashboard template
 * (nuxt-ui-templates/dashboard-vue: HomeStats, HomeChart, HomeSales).
 */

export interface DashboardQuickLink {
  label: string
  to: string
  icon: string
}

/** Stat grid (UPageGrid): two columns on phones, joined four-column block from lg. */
export const statGridClass = 'grid-cols-2 gap-3 sm:gap-6 lg:grid-cols-4 lg:gap-px'

/** Stat tile (UPageCard variant="subtle"): tinted round icon, small uppercase title. */
export const statCardUi = {
  root: 'bg-muted',
  // Tiles in a row share one height. content-start (lg grid) and a non-growing wrapper
  // (flex column below lg) keep everything packed at the top, so the numbers line up
  // whether or not a tile has a sub line.
  container: 'gap-y-1.5 content-start',
  wrapper: 'flex-none items-start',
  leading: 'p-2.5 rounded-full bg-primary/10 ring ring-inset ring-primary/25',
  title: 'font-normal text-muted text-xs uppercase',
}

/**
 * Corner classes for one tile of a joined stat grid (lg: four columns, 1px gaps, the
 * rings meet as hairlines). Only convex outer corners keep their radius, so a short last
 * row still reads as one block. Below lg the tiles stay separate rounded cards.
 */
export function statCorners(index: number, count: number, columns = 4): string {
  const column = index % columns
  const hasLeft = column > 0
  const hasRight = column < columns - 1 && index + 1 < count
  const hasAbove = index >= columns
  const hasBelow = index + columns < count
  return [
    'lg:rounded-none',
    !hasLeft && !hasAbove && 'lg:rounded-tl-lg',
    !hasRight && !hasAbove && 'lg:rounded-tr-lg',
    !hasLeft && !hasBelow && 'lg:rounded-bl-lg',
    !hasRight && !hasBelow && 'lg:rounded-br-lg',
  ].filter(Boolean).join(' ')
}

/** Dashboard tables (UTable), as HomeSales: tinted rounded header row, hairline rows. */
export const dashboardTableUi = {
  base: 'border-separate border-spacing-0',
  thead: '[&>tr]:bg-elevated/50 [&>tr]:after:content-none',
  tbody: '[&>tr]:last:[&>td]:border-b-0',
  th: 'first:rounded-l-lg last:rounded-r-lg border-y border-default first:border-l last:border-r',
  td: 'border-b border-default',
}

/** Same as dashboardTableUi with shorter rows, for lists that sit next to a chart. */
export const compactTableUi = {
  ...dashboardTableUi,
  th: `${dashboardTableUi.th} py-2.5`,
  td: `${dashboardTableUi.td} py-2.5`,
}

const MODERN_COLOR = /^(oklch|oklab|lab|lch|color)\(/i

function toHex(red: number, green: number, blue: number): string {
  return `#${[red, green, blue].map((value) => Math.round(value).toString(16).padStart(2, '0')).join('')}`
}

/** Reads a CSS color as #rrggbb; Chart.js series need hex to add alpha suffixes. */
function cssColorToHex(color: string): string | null {
  const value = color.trim()
  if (/^#[0-9a-f]{6}$/i.test(value)) return value.toLowerCase()
  const rgb = value.match(/^rgba?\(\s*([\d.]+)[\s,]+([\d.]+)[\s,]+([\d.]+)/i)
  if (rgb) return toHex(Number(rgb[1]), Number(rgb[2]), Number(rgb[3]))
  if (!MODERN_COLOR.test(value)) return null
  // The palettes are oklch: let a 1x1 canvas convert to sRGB bytes.
  const context = document.createElement('canvas').getContext('2d')
  if (!context) return null
  context.fillStyle = value
  context.fillRect(0, 0, 1, 1)
  const [red, green, blue] = context.getImageData(0, 0, 1, 1).data
  return toHex(red, green, blue)
}

function readCssColor(variable: string): string | null {
  if (typeof document === 'undefined' || !document.body) return null
  const probe = document.createElement('span')
  probe.style.cssText = `position:absolute;visibility:hidden;pointer-events:none;color:var(${variable})`
  document.body.appendChild(probe)
  const color = getComputedStyle(probe).color
  probe.remove()
  return color ? cssColorToHex(color) : null
}

/**
 * A theme token (e.g. --ui-primary) as a hex color for Chart.js. Follows light/dark
 * switches and the admin's Appearance settings; `fallback` when it cannot be read.
 */
export function useThemeColor(variable: string, fallback: string) {
  const color = ref(fallback)
  const { theme } = useSiteTheme()
  let observer: MutationObserver | undefined
  const refresh = () => {
    color.value = readCssColor(variable) ?? fallback
  }
  onMounted(() => {
    refresh()
    if (typeof MutationObserver === 'function') {
      observer = new MutationObserver(refresh)
      observer.observe(document.documentElement, { attributes: true, attributeFilter: ['class'] })
    }
  })
  watch(theme, refresh)
  onBeforeUnmount(() => observer?.disconnect())
  return color
}
