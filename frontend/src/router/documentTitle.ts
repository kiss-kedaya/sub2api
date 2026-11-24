import type { RouteLocationNormalizedLoaded } from 'vue-router'
import { useAppStore } from '@/stores/app'
import { useAuthStore } from '@/stores/auth'
import { useAdminSettingsStore } from '@/stores/adminSettings'
import { resolveRouteDocumentTitle } from './title'

/**
 * The one place that writes `document.title` for a route: translated title key, custom-menu page labels and the site name.
 * The navigation guard, the locale switch, the App shell and the boot sequence all call this, so they cannot drift apart.
 * Safe to call again whenever its inputs change (messages arrived, settings arrived, locale switched).
 */
export function applyDocumentTitle(route: Pick<RouteLocationNormalizedLoaded, 'name' | 'params' | 'meta'>): void {
  if (typeof document === 'undefined') return
  const appStore = useAppStore()
  const authStore = useAuthStore()
  const adminSettingsStore = useAdminSettingsStore()
  const customMenuItems = [
    ...(appStore.cachedPublicSettings?.custom_menu_items ?? []),
    ...(authStore.isAdmin ? adminSettingsStore.customMenuItems : []),
  ]
  document.title = resolveRouteDocumentTitle(route, appStore.siteName, customMenuItems)
}
