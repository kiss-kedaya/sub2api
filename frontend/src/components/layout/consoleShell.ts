import type { RouteMeta } from 'vue-router'

/** The global app shell already owns the console frame. */
export const CONSOLE_SHELL_KEY = Symbol('sub2api.console-shell')

/** Console chrome belongs to authenticated app pages, not auth/public screens. */
export function isConsoleLayoutRoute(meta: RouteMeta | null | undefined): boolean {
  return meta?.requiresAuth === true
}

/**
 * Nuxt remounts a page when its key changes. Query strings and route params are
 * handled by the view itself, so key on the matched route pattern rather than
 * fullPath: only a different page (not a query/filter change) should remount.
 */
export function consolePageKey(route: { matched?: Array<{ path?: string }>; path?: string }): string {
  return route.matched?.[0]?.path || route.path || '/'
}