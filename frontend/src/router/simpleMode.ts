/**
 * Simple-mode route policy, shared by the router guard and the dashboard entries.
 *
 * In simple mode the console is trimmed down to a minimal set of pages. These
 * paths are not just hidden from navigation: the guard bounces them back to the
 * dashboard, so any button pointing at one looks like a dead click (#400).
 */
export const SIMPLE_MODE_RESTRICTED_PATHS = [
  '/admin/subscriptions',
  '/admin/redeem',
  '/subscriptions',
  '/redeem',
] as const

/** Would a simple-mode navigation to `path` be redirected back to the dashboard? */
export function isRestrictedInSimpleMode(path: string): boolean {
  return SIMPLE_MODE_RESTRICTED_PATHS.some(
    (restricted) => path === restricted || path.startsWith(`${restricted}/`),
  )
}
