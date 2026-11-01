import type { RouteLocationNormalizedLoaded, RouteLocationResolved, RouteMeta, Router } from 'vue-router'

/**
 * Resolves `meta.parent` of a route: a route name (its params are taken from the current
 * route) or a path. Returns null when the parent does not exist.
 */
export function resolveRouteParent(
  router: Router,
  current: RouteLocationNormalizedLoaded,
  parent: NonNullable<RouteMeta['parent']>
): RouteLocationResolved | null {
  const target = typeof parent === 'string' ? (parent.startsWith('/') ? { path: parent } : { name: parent }) : parent
  try {
    if ('name' in target) {
      if (!router.hasRoute(target.name)) return null
      const record = router.getRoutes().find((entry) => entry.name === target.name)
      const keys = [...(record?.path.matchAll(/:(\w+)/g) ?? [])].map((match) => match[1])
      const params = Object.fromEntries(keys.filter((key) => key in current.params).map((key) => [key, current.params[key]]))
      return router.resolve({ name: target.name, params })
    }
    return router.resolve(target.path)
  } catch {
    return null
  }
}

/** The parents of the current route, outermost first (at most `limit`). */
export function routeParentTrail(router: Router, current: RouteLocationNormalizedLoaded, limit = 4): RouteLocationResolved[] {
  const trail: RouteLocationResolved[] = []
  const seen = new Set<string>([current.path])
  let parent = current.meta.parent
  while (parent && trail.length < limit) {
    const target = resolveRouteParent(router, current, parent)
    if (!target || seen.has(target.path)) break
    seen.add(target.path)
    trail.unshift(target)
    parent = target.meta.parent
  }
  return trail
}
