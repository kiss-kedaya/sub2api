import { onBeforeUnmount, onMounted } from 'vue'
import type { Router } from 'vue-router'
import { resolveInternalNavigation } from '@/components/layout/navigationLinks'

/**
 * Safety net for plain same-origin <a href> links rendered by UI libraries.
 * NuxtLink/RouterLink already prevent the default; this only catches anchors
 * that would otherwise ask the browser for a new document.
 */
export function useClientAnchorNavigation(router: Router): void {
  if (typeof window === 'undefined') return

  const onClick = (event: MouseEvent) => {
    if (event.defaultPrevented || event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return
    const target = event.target
    const anchor = target instanceof Element ? target.closest<HTMLAnchorElement>('a[href]') : null
    if (!anchor) return
    const path = resolveInternalNavigation(anchor, window.location)
    if (!path) return
    event.preventDefault()
    void router.push(path)
  }

  onMounted(() => document.addEventListener('click', onClick))
  onBeforeUnmount(() => document.removeEventListener('click', onClick))
}