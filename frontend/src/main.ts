import { createApp } from 'vue'
import { createPinia } from 'pinia'
import App from './App.vue'
import router from './router'
import i18n, { initI18n } from './i18n'
import nuxtUi from './plugins/nuxtUi'
import { useAppStore } from '@/stores/app'
import { applySiteTheme } from '@/composables/useSiteTheme'
import { updateFavicon } from '@/utils/branding'
import { applyDocumentTitle } from '@/router/documentTitle'
import './styles/fonts.css'
import './style.css'

async function bootstrap() {
  const app = createApp(App)
  const pinia = createPinia()
  app.use(pinia)

  // Initialize settings from injected config BEFORE mounting (prevents flash)
  // This must happen after pinia is installed but before router and i18n
  const appStore = useAppStore()
  appStore.initFromInjectedConfig()
  // Site theme (colors, radius, font) and the light/dark class, before the first paint.
  // The visitor's saved choice (localStorage "theme") wins over the site default mode.
  applySiteTheme(appStore.cachedPublicSettings, { colorMode: true })

  // Set document title immediately after config is loaded
  if (appStore.siteName && appStore.siteName !== 'Sub2API') {
    document.title = `${appStore.siteName} - AI API Gateway`
  }
  updateFavicon(appStore.siteLogo)

  // The locale messages and the first route's component chunk are two independent downloads. They used to run one after
  // the other (messages, THEN router), which put two full network round trips between the page and its first paint;
  // on a slow connection each of those is a second or more of blank screen. Start both now and wait for both.
  const localeReady = initI18n()

  app.use(router) // installing the router starts the initial navigation: guards plus the route's lazy component chunk
  app.use(i18n)
  // Nuxt UI (Vue mode): head, router links, bundled icons and its color variables.
  app.use(nuxtUi)

  // 等待路由器完成初始导航、且语言包就绪后再挂载，避免竞态条件导致的空白渲染 / 首屏出现未翻译的文案
  await Promise.all([localeReady, router.isReady()])

  // The navigation guard resolved the tab title while the messages were still downloading, so it used the route's
  // static fallback title. Resolve it again now that the translations exist, before the first paint.
  applyDocumentTitle(router.currentRoute.value)

  app.mount('#app')
}

bootstrap()
