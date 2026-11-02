import { defineNuxtPlugin, useCookie, useHead } from '#app'
import { installNavigationGuards } from '@/router/guards'
import { resolveRouteDocumentTitle } from '@/router/title'
import { useAppStore } from '@/stores/app'
import { useSiteTheme, buildSiteThemeCss } from '@/composables/useSiteTheme'
import { getAppRuntimeContext } from '@runtime/context'

export default defineNuxtPlugin({
  name: 'legacy-navigation',
  dependsOn: ['legacy-runtime'],
  setup(nuxtApp) {
    const context = getAppRuntimeContext()
    if (context) context.router = nuxtApp.$router
    installNavigationGuards(nuxtApp.$router, false)
    const app = useAppStore()
    const theme = useSiteTheme()
    const mode = useCookie<'light' | 'dark' | 'system' | null>('theme')
    useHead(() => ({
      title: resolveRouteDocumentTitle(nuxtApp.$router.currentRoute.value, app.siteName),
      htmlAttrs: {
        class: mode.value === 'dark' ? 'dark' : undefined,
        style: mode.value === 'dark' ? 'color-scheme: dark' : undefined,
      },
      style: [{ id: 'site-theme-vars', textContent: buildSiteThemeCss(theme.theme.value) }],
    }))
  },
})
