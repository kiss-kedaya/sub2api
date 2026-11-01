/**
 * Shared Nuxt UI `ui` overrides that reproduce the official dashboard-vue template look
 * (HomeStats stat cards, HomeSales / customers tables) across the ops, monitor and usage pages.
 * Only design tokens are used, so light/dark and the admin's primary color follow the theme.
 */

/** UPageCard `ui` for a stat tile (template HomeStats.vue). */
export const statCardUi = {
  container: 'gap-y-1.5',
  wrapper: 'items-start',
  leading: 'p-2.5 rounded-full bg-primary/10 ring ring-inset ring-primary/25',
  title: 'font-normal text-muted text-xs uppercase'
}

/** Class for stat tiles inside `UPageGrid class="lg:gap-px"`: joins them into one strip on desktop. */
export const statCardClass = 'lg:rounded-none first:rounded-l-lg last:rounded-r-lg hover:z-1'

/** UTable `ui` with the template's rounded header row and row separators. */
export const templateTableUi = {
  base: 'border-separate border-spacing-0',
  thead: '[&>tr]:bg-elevated/50 [&>tr]:after:content-none',
  tbody: '[&>tr]:last:[&>td]:border-b-0',
  th: 'py-2 first:rounded-l-lg last:rounded-r-lg border-y border-default first:border-l last:border-r',
  td: 'border-b border-default',
  separator: 'h-0'
}

/** UTable `ui` for compact tables inside cards and dialogs (no outer header border). */
export const compactTableUi = {
  base: 'border-separate border-spacing-0',
  thead: '[&>tr]:after:content-none',
  th: 'py-2 px-3 text-xs font-medium text-muted border-b border-default',
  td: 'py-2 px-3 border-b border-default text-sm',
  tbody: '[&>tr]:last:[&>td]:border-b-0',
  separator: 'h-0'
}

/** UCard `ui` for a chart card (template HomeChart.vue): header, edge-to-edge body. */
export const chartCardUi = {
  root: 'overflow-visible',
  header: 'px-4 py-3 sm:px-5',
  body: 'p-4 sm:p-5'
}

export type SemanticColor = 'primary' | 'secondary' | 'success' | 'info' | 'warning' | 'error' | 'neutral'

/** Channel monitor check status → UBadge color (operational / degraded / failed; error and unknown stay neutral). */
export function monitorStatusColor(status: string | null | undefined): SemanticColor {
  if (status === 'operational') return 'success'
  if (status === 'degraded') return 'warning'
  if (status === 'failed') return 'error'
  return 'neutral'
}
