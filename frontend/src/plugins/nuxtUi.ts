import { computed } from 'vue'
import { _api } from '@iconify/vue'
import { en, zh_cn } from '@nuxt/ui/locale'
import ui from '@nuxt/ui/vue-plugin'
import { i18n } from '@/i18n'

/**
 * Icons are embedded at build time from the installed @iconify-json/* collections
 * (`icon.clientBundle` in vite.config.ts). Turn off Iconify's runtime API so an icon that
 * is missing from the bundle renders empty instead of being fetched from api.iconify.design.
 * To add an icon set, install `@iconify-json/<set>`; `scan: true` picks up the names used.
 */
_api.setFetch((async (input: RequestInfo | URL) => {
  if (import.meta.env.DEV) {
    console.warn(`[nuxt-ui] icon not bundled, not fetching from the Iconify API: ${String(input)}`)
  }
  throw new Error('Iconify API disabled')
}) as typeof fetch)

/** Nuxt UI's built-in strings (close buttons, pagination labels...) follow the app locale. */
export const nuxtUiLocale = computed(() => (i18n.global.locale.value === 'zh' ? zh_cn : en))

export default ui
