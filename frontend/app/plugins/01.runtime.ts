import { defineNuxtPlugin, useCookie, useRequestHeaders, useState } from '#app'
import { createPinia } from 'pinia'
import { AxiosError, AxiosHeaders, type AxiosAdapter } from 'axios'
import { createAppI18n, loadLocaleMessages } from '@/i18n'
import { useAppStore } from '@/stores/app'
import { useAuthStore } from '@/stores/auth'
import { applySiteTheme } from '@/composables/useSiteTheme'
import type { AppRuntimeContext } from '../../src/runtime/context'

export default defineNuxtPlugin({
  name: 'legacy-runtime',
  enforce: 'pre',
  async setup(nuxtApp) {
    const pinia = createPinia()
    nuxtApp.vueApp.use(pinia)
    const incomingHeaders = useRequestHeaders(['cookie'])
    const languageCookie = useCookie<string>('sub2api_locale')
    const requestHeaders = useRequestHeaders(['accept-language'])
    const locale = useState<'zh' | 'en'>('app-locale', () => languageCookie.value === 'en' ? 'en'
      : languageCookie.value === 'zh' ? 'zh' : requestHeaders['accept-language']?.startsWith('en') ? 'en' : 'zh')
    const appI18n = createAppI18n(locale.value)
    let csrfToken: string | null = null
    const adapter: AxiosAdapter = async (config) => {
      const suffix = String(config.url ?? '').replace(/^\/api\/v1(?=\/|$)/, '')
      if (/^(?:[a-z]+:|\/\/)/i.test(suffix) || suffix.includes('..')) {
        throw new AxiosError('Only same-origin management API requests are allowed', 'ERR_BAD_REQUEST', config)
      }
      const headers: Record<string, string> = {}
      for (const [key, value] of Object.entries(AxiosHeaders.from(config.headers).toJSON())) {
        if (value != null && !['authorization', 'cookie', 'host'].includes(key.toLowerCase())) headers[key] = String(value)
      }
      if (csrfToken) headers['x-sub2api-csrf'] = csrfToken
      const isFormData = typeof FormData !== 'undefined' && config.data instanceof FormData
      if (isFormData) delete headers['content-type']
      const responseType = config.responseType
      let response
      try {
        response = await $fetch.raw(`/api/v1/${suffix.replace(/^\//, '')}`, {
        method: (config.method || 'get').toUpperCase() as 'GET',
          headers: { ...headers, ...(import.meta.server && incomingHeaders.cookie ? { cookie: incomingHeaders.cookie } : {}) },
          query: config.params,
          body: ['get', 'head'].includes(config.method || 'get') ? undefined : config.data,
          responseType: responseType === 'blob' ? 'blob' : responseType === 'arraybuffer' ? 'arrayBuffer' : responseType === 'stream' ? 'stream' : undefined,
        signal: config.signal as AbortSignal | undefined,
          timeout: config.timeout, ignoreResponseError: true, redirect: 'manual',
        })
      } catch (error) {
        if (import.meta.dev) console.error('[Nuxt API adapter]', suffix, error instanceof Error ? error.message : String(error))
        throw error
      }
      const result = { data: response._data, status: response.status, statusText: response.statusText,
        headers: Object.fromEntries(response.headers.entries()), config }
      const data = (result.data as { data?: { csrf_token?: string } })?.data
      if (typeof data?.csrf_token === 'string') csrfToken = data.csrf_token
      if (response.status < 200 || response.status >= 300) {
        throw new AxiosError(response.statusText || 'Request failed', 'ERR_BAD_RESPONSE', config, undefined, result)
      }
      return result
    }
    const context: AppRuntimeContext = { i18n: appI18n, cookieSession: true, apiAdapter: adapter }
    nuxtApp.provide('legacyContext', context)
    await nuxtApp.runWithContext(async () => {
      await loadLocaleMessages(locale.value)
      nuxtApp.vueApp.use(appI18n)
      if (import.meta.client && nuxtApp.payload.legacyState) pinia.state.value = nuxtApp.payload.legacyState as typeof pinia.state.value
      const app = useAppStore(pinia)
      const auth = useAuthStore(pinia)
      // Only public settings and an explicit session probe are initial requests.
      // No ticket lists, announcement bodies or secondary page data is prefetched.
      if (import.meta.server) {
        await app.fetchPublicSettings()
        try {
          const session = await $fetch<{ data?: { authenticated?: boolean; csrf_token?: string } }>('/api/v1/auth/session', {
            headers: incomingHeaders.cookie ? { cookie: incomingHeaders.cookie } : undefined,
          })
          csrfToken = session.data?.csrf_token ?? null
          if (session.data?.authenticated) {
            auth.cookieSession = true
            await auth.refreshUser()
          }
        } catch { /* An absent or expired session renders public pages anonymously. */ }
      } else {
        try {
          const session = await $fetch<{ data?: { csrf_token?: string } }>('/api/v1/auth/session')
          csrfToken = session.data?.csrf_token ?? null
        } catch { /* API errors remain visible when the user submits a form. */ }
        applySiteTheme(app.cachedPublicSettings, { colorMode: true })
      }
      nuxtApp.hook('app:rendered', () => {
        // Do not serialize arbitrary Pinia stores: API keys, forms and privileged
        // settings can hold credentials. This allowlist is intentionally tiny.
        nuxtApp.payload.legacyState = { app: pinia.state.value.app, auth: {
          user: auth.user, cookieSession: auth.isAuthenticated, token: null,
          runMode: auth.runMode, pendingAuthSession: null,
        } }
      })
    })
    return { provide: { pinia } }
  },
})
