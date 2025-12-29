import { createI18n } from 'vue-i18n'
import en from './locales/en'
import zh from './locales/zh'

const LOCALE_KEY = 'sub2api_locale'

function getDefaultLocale(): string {
  // Check localStorage first
  const saved = localStorage.getItem(LOCALE_KEY)
  if (saved && ['en', 'zh'].includes(saved)) {
    return saved
  }

  // Check browser language
  const browserLang = navigator.language.toLowerCase()
  if (browserLang.startsWith('zh')) {
    return 'zh'
  }

  return 'en'
}

export const i18n = createI18n({
  legacy: false,
  locale: getDefaultLocale(),
  fallbackLocale: 'en',
  messages: {
    en,
    zh
  },
  // 禁用 HTML 消息警告 - 引导步骤使用富文本内容（driver.js 支持 HTML）
  // 这些内容是内部定义的，不存在 XSS 风险
  warnHtmlMessage: false
})

export function setLocale(locale: string) {
  if (['en', 'zh'].includes(locale)) {
    i18n.global.locale.value = locale as 'en' | 'zh'
    localStorage.setItem(LOCALE_KEY, locale)
    document.documentElement.setAttribute('lang', locale)
  }
}

export function getLocale(): string {
  return i18n.global.locale.value
}

export const availableLocales = [
  { code: 'en', name: 'English', flag: '🇺🇸' },
  { code: 'zh', name: '中文', flag: '🇨🇳' }
]

export default i18n
