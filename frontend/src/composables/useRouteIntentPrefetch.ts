import { onBeforeUnmount, onMounted } from 'vue'
import type { RouteRecordNormalized, Router } from 'vue-router'
import { resolveInternalNavigation } from '@/components/layout/navigationLinks'

type LazyComponentLoader = () => Promise<unknown>

function isLazyLoader(value: unknown): value is LazyComponentLoader {
  return (
    typeof value === 'function' &&
    !('render' in value) &&
    !('setup' in value) &&
    !('__vccOpts' in value)
  )
}

function shouldSkipForDataSaver(): boolean {
  if (typeof navigator === 'undefined') return false
  const conn = (navigator as Navigator & { connection?: { saveData?: boolean } }).connection
  return conn?.saveData === true
}

/**
 * Warm lazy route chunks on explicit user intent (hover, keyboard focus, or touch start)
 * so SPA clicks resolve without async-chunk stalls or loading-bar flashes.
 * Loads JS modules only — never triggers API data fetches.
 */
export function useRouteIntentPrefetch(router: Router): void {
  if (typeof window === 'undefined' || typeof document === 'undefined') return

  const prefetchedLoaders = new WeakSet<LazyComponentLoader>()
  const prefetchedPaths = new Set<string>()

  const warmRecord = (record: RouteRecordNormalized) => {
    const components = record.components
    if (!components) return
    for (const component of Object.values(components)) {
      if (!isLazyLoader(component) || prefetchedLoaders.has(component)) continue
      prefetchedLoaders.add(component)
      try {
        void Promise.resolve(component()).catch(() => {
          prefetchedLoaders.delete(component)
        })
      } catch {
        prefetchedLoaders.delete(component)
      }
    }
  }

  const prefetchAnchor = (target: EventTarget | null) => {
    if (shouldSkipForDataSaver()) return
    const anchor = target instanceof Element ? target.closest<HTMLAnchorElement>('a[href]') : null
    if (!anchor) return

    const path = resolveInternalNavigation(anchor, window.location)
    if (!path) return

    const pathname = path.split(/[?#]/, 1)[0] || '/'
    if (prefetchedPaths.has(pathname)) return

    let resolved: ReturnType<Router['resolve']>
    try {
      resolved = router.resolve(path)
    } catch {
      return
    }
    if (!resolved.matched.length) return

    prefetchedPaths.add(pathname)
    for (const record of resolved.matched) {
      warmRecord(record)
    }
  }

  const onPointerOver = (event: PointerEvent) => {
    if (event.pointerType === 'touch') return
    prefetchAnchor(event.target)
  }

  const onFocusIn = (event: FocusEvent) => {
    prefetchAnchor(event.target)
  }

  const onTouchStart = (event: TouchEvent) => {
    prefetchAnchor(event.target)
  }

  onMounted(() => {
    document.addEventListener('pointerover', onPointerOver, { passive: true })
    document.addEventListener('focusin', onFocusIn, { passive: true })
    document.addEventListener('touchstart', onTouchStart, { passive: true })
  })

  onBeforeUnmount(() => {
    document.removeEventListener('pointerover', onPointerOver)
    document.removeEventListener('focusin', onFocusIn)
    document.removeEventListener('touchstart', onTouchStart)
  })
}
