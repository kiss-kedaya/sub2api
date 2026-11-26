import type { LocationQuery, RouteMeta } from 'vue-router'

/** The global app shell already owns the console frame. */
export const CONSOLE_SHELL_KEY = Symbol('sub2api.console-shell')

interface ConsoleShellRouteLike {
  path?: string
  query?: LocationQuery | Record<string, unknown>
}

/**
 * Console chrome belongs to authenticated app pages, plus the authenticated
 * embedded Model Plaza view so switching between Console ↔ Model Plaza keeps
 * the sidebar & header mounted without teardown flicker.
 */
export function isConsoleLayoutRoute(
  meta: RouteMeta | null | undefined,
  route?: ConsoleShellRouteLike | null,
  isAuthenticated = false,
): boolean {
  if (meta?.requiresAuth === true) return true
  return isAuthenticated && route?.path === '/model-plaza' && route.query?.embedded === '1'
}

/**
 * Nuxt remounts a page when its key changes. Query strings and route params are
 * handled by the view itself, so key on the matched route pattern rather than
 * fullPath: only a different page (not a query/filter change) should remount.
 */
export function consolePageKey(route: { matched?: Array<{ path?: string }>; path?: string }): string {
  return route.matched?.[0]?.path || route.path || '/'
}