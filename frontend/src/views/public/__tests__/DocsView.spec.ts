import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createMemoryHistory, createRouter } from 'vue-router'
import DocsView from '../DocsView.vue'
import { docsRedirect } from '../docsRoute'
import english from '@/i18n/locales/en/docs'

const mocks = vi.hoisted(() => ({
  copy: vi.fn().mockResolvedValue(true),
  settings: { publicSettingsLoaded: true, siteName: 'Example gateway', siteLogo: '', cachedPublicSettings: null },
}))
vi.mock('@/stores/app', () => ({ useAppStore: () => mocks.settings }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ isAuthenticated: false, isAdmin: false }) }))
vi.mock('@/composables/useClipboard', () => ({ useClipboard: () => ({ copyToClipboard: mocks.copy }) }))
vi.mock('vue-i18n', async (importOriginal) => ({
  ...await importOriginal<typeof import('vue-i18n')>(),
  useI18n: () => ({
    locale: { value: 'en' },
    t: (key: string) => key === 'nav.docs' ? 'Docs' : key.split('.').reduce<unknown>((value, part) => (value as Record<string, unknown>)?.[part], english) || key,
  }),
}))

// Same records as src/router/index.ts: one URL per page, old ?page= links redirect.
async function openDocs(query = 'quick-start') {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/docs', redirect: docsRedirect },
      { path: '/docs/:page', component: DocsView, meta: { requiresAuth: false } },
      ...['/home', '/login', '/model-plaza'].map(path => ({ path, component: { template: '<div />' } })),
    ],
  })
  await router.push(`/docs?page=${query}`)
  await router.isReady()
  const wrapper = mount(DocsView, { global: { plugins: [router], stubs: { LocaleSwitcher: true } } })
  await flushPromises()
  return { wrapper, router }
}

describe('public API documentation page', () => {
  beforeEach(() => {
    mocks.copy.mockClear()
    Object.defineProperty(Element.prototype, 'scrollIntoView', { configurable: true, value: vi.fn() })
    vi.spyOn(window, 'matchMedia').mockReturnValue({ matches: false } as MediaQueryList)
  })

  it('renders for a logged-out visitor, searches navigation and copies the selected language', async () => {
    const { wrapper, router } = await openDocs()
    expect(router.currentRoute.value.meta.requiresAuth).toBe(false)
    expect(wrapper.get('h1').text()).toBe('Your first API request')
    expect(wrapper.text()).toContain('Example gateway')
    await wrapper.get('[data-testid="docs-search"]').setValue('Web fetch')
    const sidebar = wrapper.get('#docs-sidebar')
    expect(sidebar.text()).toContain('Web fetch')
    expect(sidebar.text()).not.toContain('Connect Claude Code')
    const python = wrapper.findAll('button').find(button => button.text() === 'Python')!
    await python.trigger('click')
    await wrapper.get('[data-testid="docs-copy-first-request"]').trigger('click')
    await flushPromises()
    expect(mocks.copy.mock.calls[0][0]).toContain('import requests')
    expect(mocks.copy.mock.calls[0][0]).toContain('/v1/chat/completions')
    wrapper.unmount()
  })

  it('left table of contents lists categories, pages and the open page sections', async () => {
    const { wrapper, router } = await openDocs('quick-start')
    expect(router.currentRoute.value.fullPath).toBe('/docs/quick-start')
    const toc = wrapper.get('#docs-sidebar')
    expect(toc.text()).toContain(english.docs.category.start)
    expect(toc.text()).toContain('Your first API request')
    // the open page's sections sit under it; other pages' sections do not
    const sectionTitle = wrapper.findAll('.docs-section h2')[0].text().replace('#', '').trim()
    expect(toc.text()).toContain(sectionTitle)
    await router.push('/docs?page=deepseek-fetch#fetch-access')
    await flushPromises()
    expect(router.currentRoute.value.fullPath).toBe('/docs/deepseek-fetch#fetch-access')
    expect(wrapper.get('#docs-sidebar').text()).not.toContain(sectionTitle)
    wrapper.unmount()
  })

  it('theme button flips the mode in force, even after it changed outside the page', async () => {
    document.documentElement.classList.remove('dark')
    const { wrapper } = await openDocs()
    // e.g. the OS switched to dark while the shared "system" mode is in force
    document.documentElement.classList.add('dark')
    await wrapper.get(`button[aria-label="${english.docs.theme}"]`).trigger('click')
    expect(document.documentElement.classList.contains('dark')).toBe(false)
    expect(localStorage.getItem('theme')).toBe('light')
    localStorage.removeItem('theme')
    wrapper.unmount()
  })

  it('handles query navigation, unknown pages and mobile menu visibility', async () => {
    const { wrapper, router } = await openDocs('missing-page')
    expect(wrapper.get('h1').text()).toBe('Your first API request')
    await router.push('/docs?page=deepseek-fetch')
    await flushPromises()
    // Old ?page= links land on the page's own URL.
    expect(router.currentRoute.value.path).toBe('/docs/deepseek-fetch')
    expect(wrapper.get('h1').text()).toBe('Web fetch')
    expect(wrapper.text()).toContain('8192')
    expect(wrapper.text()).toContain('POST /v1/tools/fetch')
    const menu = wrapper.find('button[aria-controls="docs-sidebar"]')
    expect(menu.attributes('aria-expanded')).toBe('false')
    await menu.trigger('click')
    expect(menu.attributes('aria-expanded')).toBe('true')
    wrapper.unmount()
  })
})
