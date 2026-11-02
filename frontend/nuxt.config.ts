import { fileURLToPath } from 'node:url'

export default defineNuxtConfig({
  compatibilityDate: '2026-11-01',
  ssr: true,
  srcDir: 'app',
  serverDir: 'server',
  modules: ['@nuxt/ui'],
  devtools: { enabled: false },
  telemetry: false,
  typescript: { typeCheck: 'build' },
  components: false,
  alias: {
    '@': fileURLToPath(new URL('./src', import.meta.url)),
    '@runtime/context': fileURLToPath(new URL('./app/runtime/context.ts', import.meta.url)),
  },
  css: ['@/styles/fonts.css', '@/style.css'],
  ui: { colorMode: false, fonts: false },
  icon: { clientBundle: { scan: true }, provider: 'none' },
  runtimeConfig: {
    // Private: this value never enters the browser payload. Point at the Go API slot.
    backendOrigin: 'http://127.0.0.1:8080',
    sessionPassword: '',
    sessionCookieSecure: 'auto',
    backendTimeoutMs: 180000,
    ssrVersion: '',
    ssrCommit: '',
    ssrBuiltAt: '',
    expectedBackendVersion: '',
    public: { apiBase: '/api/v1' },
  },
  nitro: { preset: 'node-server' },
  app: {
    head: {
      htmlAttrs: { lang: 'zh' },
      meta: [{ name: 'viewport', content: 'width=device-width, initial-scale=1, viewport-fit=cover, interactive-widget=resizes-content' }],
      link: [{ rel: 'icon', href: '/logo.svg' }],
    },
  },
  experimental: { asyncContext: true },
  routeRules: {
    '/**': { headers: { 'Cache-Control': 'private, no-store' } },
    '/_nuxt/**': { headers: { 'Cache-Control': 'public, max-age=31536000, immutable' } },
  },
  vite: {
    define: {
      __INTLIFY_JIT_COMPILATION__: true,
      'import.meta.env.VITE_API_BASE_URL': JSON.stringify('/api/v1'),
      'import.meta.env.VITE_WS_BASE_URL': 'undefined',
      'import.meta.env.VITE_INFINITE_CANVAS_URL': 'undefined',
    },
    // Source locales contain messages, not precompiled functions.
    resolve: { alias: { 'vue-i18n': fileURLToPath(new URL('./node_modules/vue-i18n/dist/vue-i18n.esm-bundler.js', import.meta.url)) } },
    optimizeDeps: { include: ['axios', 'pinia', 'dompurify', 'marked', 'driver.js', 'qrcode', 'vue-i18n'] },
  },
})
