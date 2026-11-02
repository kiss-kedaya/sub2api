import { computed } from 'vue'
import { en, zh_cn } from '@nuxt/ui/locale'
import { i18n } from '@/i18n'

/** Called inside setup so the computed belongs to this SSR application. */
export function useNuxtUiLocale() {
  return computed(() => i18n.global.locale.value === 'zh' ? zh_cn : en)
}
