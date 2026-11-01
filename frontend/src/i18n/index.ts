import { createI18n } from 'vue-i18n'
import { getAppRuntimeContext } from '@runtime/context'

type LocaleCode = 'en' | 'zh'

type LocaleMessages = Record<string, any>

const LOCALE_KEY = 'sub2api_locale'
const DEFAULT_LOCALE: LocaleCode = 'en'

const localeLoaders: Record<LocaleCode, () => Promise<{ default: LocaleMessages }>> = {
  en: () => import('./locales/en'),
  zh: () => import('./locales/zh')
}

function isLocaleCode(value: string): value is LocaleCode {
  return value === 'en' || value === 'zh'
}

function getDefaultLocale(): LocaleCode {
  if (typeof window === 'undefined') return 'zh'
  const saved = localStorage.getItem(LOCALE_KEY)
  if (saved && isLocaleCode(saved)) {
    return saved
  }

  const browserLang = navigator.language.toLowerCase()
  if (browserLang.startsWith('zh')) {
    return 'zh'
  }

  return DEFAULT_LOCALE
}

export function createAppI18n(locale: LocaleCode = getDefaultLocale()) {
  return createI18n({
  legacy: false,
  locale,
  fallbackLocale: DEFAULT_LOCALE,
  messages: {},
  // 禁用 HTML 消息警告 - 引导步骤使用富文本内容（driver.js 支持 HTML）
  // 这些内容是内部定义的，不存在 XSS 风险
  warnHtmlMessage: false
  })
}

const standaloneI18n = createAppI18n()
// Existing utilities can keep importing i18n, without retaining one user's locale
// in another server request. Nuxt installs a fresh instance per application.
export const i18n = new Proxy(standaloneI18n, {
  get(_target, key) {
    const instance = getAppRuntimeContext()?.i18n ?? standaloneI18n
    return Reflect.get(instance, key, instance)
  }
})

const loadedLocales = new WeakMap<object, Set<LocaleCode>>()

export async function loadLocaleMessages(locale: LocaleCode): Promise<void> {
  const instance = getAppRuntimeContext()?.i18n ?? standaloneI18n
  let loaded = loadedLocales.get(instance)
  if (!loaded) { loaded = new Set(); loadedLocales.set(instance, loaded) }
  if (loaded.has(locale)) {
    return
  }

  const loader = localeLoaders[locale]
  const module = await loader()
  instance.global.setLocaleMessage(locale, module.default)
  loaded.add(locale)
}

export async function initI18n(): Promise<void> {
  const current = getLocale()
  await loadLocaleMessages(current)
  if (typeof document !== 'undefined') document.documentElement.setAttribute('lang', current)
}

export async function setLocale(locale: string): Promise<void> {
  if (!isLocaleCode(locale)) {
    return
  }

  await loadLocaleMessages(locale)
  i18n.global.locale.value = locale
  if (typeof document === 'undefined') return
  localStorage.setItem(LOCALE_KEY, locale)
  document.cookie = `sub2api_locale=${locale}; Path=/; SameSite=Lax; Max-Age=31536000`
  document.documentElement.setAttribute('lang', locale)

  // 同步更新浏览器页签标题，使其跟随语言切换
  const { resolveRouteDocumentTitle } = await import('@/router/title')
  const runtime = getAppRuntimeContext()
  const router = runtime?.router ?? (await import('@/router')).default
  const { useAppStore } = await import('@/stores/app')
  const { useAuthStore } = await import('@/stores/auth')
  const { useAdminSettingsStore } = await import('@/stores/adminSettings')
  const route = router.currentRoute.value
  const appStore = useAppStore()
  const authStore = useAuthStore()
  const adminSettingsStore = useAdminSettingsStore()
  const customMenuItems = [
    ...(appStore.cachedPublicSettings?.custom_menu_items ?? []),
    ...(authStore.isAdmin ? adminSettingsStore.customMenuItems : []),
  ]
  document.title = resolveRouteDocumentTitle(route, appStore.siteName, customMenuItems)
}

export function getLocale(): LocaleCode {
  const current = i18n.global.locale.value
  return isLocaleCode(current) ? current : DEFAULT_LOCALE
}

export const availableLocales = [
  { code: 'en', name: 'English', flag: '🇺🇸' },
  { code: 'zh', name: '中文', flag: '🇨🇳' }
] as const

export default i18n
