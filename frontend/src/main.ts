import { createApp } from 'vue'
import { createPinia } from 'pinia'
import App from './App.vue'
import router from './router'
import i18n, { initI18n } from './i18n'
import nuxtUi from './plugins/nuxtUi'
import { useAppStore } from '@/stores/app'
import { applySiteTheme } from '@/composables/useSiteTheme'
import { updateFavicon } from '@/utils/branding'
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

  await initI18n()

  app.use(router)
  app.use(i18n)
  // Nuxt UI (Vue mode): head, router links, bundled icons and its color variables.
  app.use(nuxtUi)

  // 等待路由器完成初始导航后再挂载，避免竞态条件导致的空白渲染
  await router.isReady()
  app.mount('#app')
}

bootstrap()
