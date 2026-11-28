import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { nextTick } from 'vue'
import { createPinia, setActivePinia } from 'pinia'
import { createMemoryHistory, createRouter } from 'vue-router'
import DocsView from '../DocsView.vue'
import english from '@/i18n/locales/en/docs'
import type { DocsPage } from '@/data/apiDocs'

const mocks = vi.hoisted(() => ({
  copy: vi.fn().mockResolvedValue(true),
  settings: { publicSettingsLoaded: true, siteName: 'Example gateway', siteLogo: '', cachedPublicSettings: null },
  auth: { isAuthenticated: false, isAdmin: false, isAdminObserver: false },
  docs: null as null | (() => unknown),
}))

vi.mock('@/stores/app', () => ({ useAppStore: () => mocks.settings }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => mocks.auth }))
vi.mock('@/composables/useClipboard', () => ({ useClipboard: () => ({ copyToClipboard: mocks.copy }) }))
vi.mock('@/data/apiDocs', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/data/apiDocs')>()
  return {
    ...actual,
    getApiDocs: (locale: string, origin: string) => mocks.docs?.() ?? actual.getApiDocs(locale, origin),
  }
})
vi.mock('vue-i18n', async (importOriginal) => ({
  ...await importOriginal<typeof import('vue-i18n')>(),
  useI18n: () => ({
    locale: { value: 'en' },
    t: (key: string, params?: Record<string, unknown>) => {
      const raw = key === 'nav.docs'
        ? 'Docs'
        : key.split('.').reduce<unknown>((value, part) => (value as Record<string, unknown>)?.[part], english) || key
      if (typeof raw !== 'string' || !params) return String(raw)
      return raw.replace(/\{(\w+)\}/g, (_, name: string) => String(params[name] ?? ''))
    },
  }),
}))

class IntersectionObserverMock {
  static instances: IntersectionObserverMock[] = []
  observed: Element[] = []
  observe = vi.fn((target: Element) => { this.observed.push(target) })
  unobserve = vi.fn()
  disconnect = vi.fn()
  private callback: IntersectionObserverCallback

  constructor(callback: IntersectionObserverCallback) {
    this.callback = callback
    IntersectionObserverMock.instances.push(this)
  }

  emit(entries: { target: Element; isIntersecting: boolean }[]) {
    this.callback(entries as unknown as IntersectionObserverEntry[], this as unknown as IntersectionObserver)
  }
}

// Media pages with cross-links, mirroring the shape of the real image/video generation docs.
function mediaDocs(): DocsPage[] {
  return [
    {
      id: 'image-generation',
      category: 'media',
      title: 'Image generation',
      description: 'Generate images from text prompts.',
      sections: [{
        id: 'image-request',
        title: 'Request an image',
        paragraphs: ['Send a prompt to the image endpoint.'],
        links: [
          { label: 'Video generation', page: 'video-generation' },
          { label: 'Provider image guide', href: 'https://example.test/image-guide' },
        ],
      }],
    },
    {
      id: 'video-generation',
      category: 'media',
      title: 'Video generation',
      description: 'Generate videos from text prompts.',
      sections: [{ id: 'video-request', title: 'Request a video', paragraphs: ['Send a prompt to the video endpoint.'] }],
    },
  ]
}

async function openDocs(query = 'quick-start', options: { attachTo?: boolean } = {}) {
  const pinia = createPinia()
  setActivePinia(pinia)
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/docs', component: DocsView, meta: { requiresAuth: false } },
      ...['/home', '/login', '/dashboard', '/keys', '/model-plaza'].map(path => ({ path, component: { template: '<div />' } })),
    ],
  })
  await router.push(`/docs?page=${query}`)
  await router.isReady()
  const wrapper = mount(DocsView, {
    attachTo: options.attachTo ? document.body : undefined,
    global: { plugins: [pinia, router], stubs: { LocaleSwitcher: true } },
  })
  await flushPromises()
  return { wrapper, router }
}

describe('public API documentation page', () => {
  beforeEach(() => {
    localStorage.clear()
    mocks.copy.mockClear()
    mocks.docs = null
    mocks.auth.isAuthenticated = false
    mocks.auth.isAdmin = false
    mocks.auth.isAdminObserver = false
    IntersectionObserverMock.instances = []
    vi.stubGlobal('IntersectionObserver', IntersectionObserverMock)
    Object.defineProperty(Element.prototype, 'scrollIntoView', { configurable: true, value: vi.fn() })
    vi.spyOn(window, 'matchMedia').mockReturnValue({ matches: false } as MediaQueryList)
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
    document.body.innerHTML = ''
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

  it('handles query navigation, unknown pages and mobile menu visibility', async () => {
    const { wrapper, router } = await openDocs('missing-page')
    expect(wrapper.get('h1').text()).toBe('Your first API request')
    await router.push('/docs?page=deepseek-fetch')
    await flushPromises()
    expect(wrapper.get('h1').text()).toBe('Web fetch')
    expect(wrapper.text()).toContain('8192')
    expect(wrapper.text()).toContain('POST /v1/tools/fetch')
    const menu = wrapper.find('button[aria-controls="docs-sidebar"]')
    expect(menu.attributes('aria-expanded')).toBe('false')
    await menu.trigger('click')
    expect(menu.attributes('aria-expanded')).toBe('true')
    wrapper.unmount()
  })

  it('navigates to and searches each new DeepSeek tool reference, including anchors and code tabs', async () => {
    const { wrapper, router } = await openDocs('deepseek-asr')
    for (const [id, title, path] of [
      ['deepseek-asr', 'Speech recognition ASR', '/v1/tools/asr'],
      ['deepseek-tts', 'Speech synthesis TTS', '/v1/tools/tts'],
      ['deepseek-ocr', 'Document recognition OCR', '/v1/tools/ocr'],
    ]) {
      await router.push(`/docs?page=${id}`)
      await flushPromises()
      expect(wrapper.get('h1').text()).toBe(title)
      expect(wrapper.text()).toContain(`POST ${path}`)
      const prefix = id.slice('deepseek-'.length)
      const request = wrapper.get(`section#${prefix}-parameters`)
      for (const language of ['Python', 'Node.js', 'cURL']) {
        const tab = request.findAll('button').find(button => button.text() === language)!
        expect(tab, `${id}:${language}`).toBeDefined()
        await tab.trigger('click')
        await request.get(`[data-testid="docs-copy-${prefix}-parameters"]`).trigger('click')
        await flushPromises()
        expect(mocks.copy.mock.lastCall?.[0]).toContain(path)
      }
      await wrapper.get('[data-testid="docs-search"]').setValue(title)
      expect(wrapper.get('#docs-sidebar').text()).toContain(title)
      await wrapper.get('[data-testid="docs-search"]').setValue('')
      await wrapper.get(`nav[aria-label="On this page"] a[href="#${prefix}-parameters"]`).trigger('click')
      await flushPromises()
      expect(router.currentRoute.value.hash).toBe(`#${prefix}-parameters`)
    }
    expect(wrapper.text()).toContain('Check error even after HTTP 200')
    wrapper.unmount()
  })

  it('places the image and video category between guides and client setup, and the key pages before the reference', async () => {
    const { wrapper } = await openDocs()
    expect(wrapper.findAll('.docs-category').map(link => link.text())).toEqual(['Get started', 'API guides', 'Image & video', 'Client setup', 'Keys & account', 'API reference'])
    wrapper.unmount()
  })

  it('links media pages internally with router-link and keeps external links in a new tab', async () => {
    mocks.docs = () => mediaDocs()
    const { wrapper, router } = await openDocs('image-generation')
    const sidebar = wrapper.get('#docs-sidebar')
    expect(sidebar.text()).toContain('Image & video')
    expect(sidebar.text()).toContain('Image generation')
    expect(sidebar.text()).toContain('Video generation')
    expect(wrapper.find('.docs-category[aria-current="page"]').text()).toBe('Image & video')

    const section = wrapper.get('section#image-request')
    const linkRow = section.get('div.flex-wrap')
    expect(linkRow.classes()).toEqual(expect.arrayContaining(['flex', 'flex-wrap', 'items-center', 'gap-x-5', 'gap-y-2']))
    const internal = linkRow.get('a[href="/docs?page=video-generation"]')
    expect(internal.text()).toContain('Video generation')
    expect(internal.attributes('target')).toBeUndefined()
    expect(internal.get('svg path').attributes('d')).toContain('M13.5 4.5L21 12')
    const external = linkRow.get('a[href="https://example.test/image-guide"]')
    expect(external.attributes('target')).toBe('_blank')
    expect(external.attributes('rel')).toBe('noopener noreferrer')
    expect(external.get('svg path').attributes('d')).toContain('M10 6H6')

    await internal.trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.query.page).toBe('video-generation')
    expect(wrapper.get('h1').text()).toBe('Video generation')
    wrapper.unmount()
  })

  it('highlights the section in view and re-arms the observer on page change', async () => {
    const { wrapper, router } = await openDocs('streaming', { attachTo: true })
    expect(IntersectionObserverMock.instances).toHaveLength(1)
    const [observer] = IntersectionObserverMock.instances
    expect(observer.observed.map(element => element.id)).toEqual(['enable-streaming', 'terminal-events', 'stream-errors'])

    const toc = wrapper.get('nav[aria-label="On this page"]')
    const tocLink = (id: string) => toc.get(`a[href="#${id}"]`)

    observer.emit([{ target: document.getElementById('terminal-events')!, isIntersecting: true }])
    await nextTick()
    expect(tocLink('terminal-events').attributes('aria-current')).toBe('location')
    expect(tocLink('terminal-events').classes()).toContain('text-blue-600')
    expect(tocLink('enable-streaming').attributes('aria-current')).toBeUndefined()

    observer.emit([
      { target: document.getElementById('terminal-events')!, isIntersecting: false },
      { target: document.getElementById('stream-errors')!, isIntersecting: true },
    ])
    await nextTick()
    expect(tocLink('stream-errors').attributes('aria-current')).toBe('location')
    expect(tocLink('terminal-events').attributes('aria-current')).toBeUndefined()

    await router.push('/docs?page=responses')
    await flushPromises()
    await nextTick()
    expect(observer.disconnect).toHaveBeenCalled()
    expect(IntersectionObserverMock.instances).toHaveLength(2)
    const nextToc = wrapper.get('nav[aria-label="On this page"]')
    expect(nextToc.text()).toContain('Request parameters')

    await nextToc.get('a[href="#responses-request"]').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.hash).toBe('#responses-request')
    wrapper.unmount()
  })

  it('searches notes, parameter names and link labels in addition to page text', async () => {
    const { wrapper } = await openDocs()
    const search = wrapper.get('[data-testid="docs-search"]')
    const sidebar = () => wrapper.get('#docs-sidebar').text()

    await search.setValue('account password')
    expect(sidebar()).toContain('Your first API request')
    expect(sidebar()).not.toContain('Web fetch')

    await search.setValue('8192')
    expect(sidebar()).toContain('Web fetch')
    expect(sidebar()).not.toContain('Your first API request')

    await search.setValue('previous_response_id')
    expect(sidebar()).toContain('Responses')
    expect(sidebar()).not.toContain('Web fetch')

    await search.setValue('Official DeepSeek Fetch API')
    expect(sidebar()).toContain('Web fetch')

    await search.setValue('')
    expect(sidebar()).toContain('Your first API request')
    wrapper.unmount()
  })

  it('shows the method, path and who may call an endpoint, and names the right credential', async () => {
    const { wrapper } = await openDocs('key-self-service')
    const key = wrapper.get('[data-testid="docs-endpoint-key-info-key"]')
    expect(key.text()).toContain('GET')
    expect(key.text()).toContain('/v1/sub2api/key')
    expect(key.text()).toContain('API key')
    expect(key.text()).not.toContain('Login token')
    const account = wrapper.get('[data-testid="docs-endpoint-key-info-account"]')
    expect(account.text()).toContain('GET')
    expect(account.text()).toContain('/v1/sub2api/account')
    expect(account.text()).toContain('API key')
    expect(account.text()).not.toContain('Login token')

    await wrapper.vm.$router.push('/docs?page=manage-keys')
    await flushPromises()
    const create = wrapper.get('[data-testid="docs-endpoint-console-create"]')
    expect(create.text()).toContain('POST')
    expect(create.text()).toContain('/api/v1/keys')
    expect(create.text()).toContain('Login token')
    expect(create.text()).not.toContain('API key')
    wrapper.unmount()
  })

  it('lists the key pages in the sidebar and finds them by what they document', async () => {
    const { wrapper } = await openDocs()
    const search = wrapper.get('[data-testid="docs-search"]')
    const sidebar = () => wrapper.get('#docs-sidebar').text()
    expect(sidebar()).toContain('Key self-service API')
    expect(sidebar()).toContain('Manage API keys (console API)')

    await search.setValue('batch-rotate')
    expect(sidebar()).toContain('Manage API keys (console API)')
    expect(sidebar()).not.toContain('Web fetch')

    await search.setValue('key_info_rate_limited')
    expect(sidebar()).toContain('Key self-service API')

    await search.setValue('balance.wallet')
    expect(sidebar()).toContain('Key self-service API')
    expect(sidebar()).not.toContain('Web fetch')
    wrapper.unmount()
  })

  it('does not repeat a marketing line under the navigation', async () => {
    const { wrapper } = await openDocs()
    expect(wrapper.get('#docs-sidebar').text()).not.toContain('From your first request to a complete tool workflow')
    wrapper.unmount()
  })

  it('brings the entry of the page being read into view when the page opens deep in the list', async () => {
    const { wrapper } = await openDocs('deepseek-ocr', { attachTo: true })
    const nav = wrapper.get('#docs-sidebar nav').element as HTMLElement
    // jsdom has no layout: give the list a geometry in which the active entry is far below the fold.
    Object.defineProperty(nav, 'clientHeight', { configurable: true, value: 200 })
    Object.defineProperty(nav, 'scrollHeight', { configurable: true, value: 900 })
    const active = nav.querySelector<HTMLElement>('[aria-current="page"]')!
    Object.defineProperty(active, 'offsetTop', { configurable: true, value: 800 })
    Object.defineProperty(active, 'offsetHeight', { configurable: true, value: 36 })
    Object.defineProperty(nav, 'offsetTop', { configurable: true, value: 0 })
    nav.scrollTop = 0

    await wrapper.vm.$router.push('/docs?page=deepseek-asr')
    await flushPromises()
    await wrapper.vm.$router.push('/docs?page=deepseek-ocr')
    await flushPromises()

    expect(nav.scrollTop).toBeGreaterThan(0)
    wrapper.unmount()
  })

  it('shows a cue that more entries are below while the list is longer than the panel, and removes it at the end', async () => {
    const { wrapper } = await openDocs('quick-start', { attachTo: true })
    const nav = wrapper.get('#docs-sidebar nav').element as HTMLElement
    const wrap = () => wrapper.get('.docs-nav-wrap')
    Object.defineProperty(nav, 'clientHeight', { configurable: true, value: 300 })
    Object.defineProperty(nav, 'scrollHeight', { configurable: true, value: 900 })

    nav.scrollTop = 0
    await wrapper.get('#docs-sidebar nav').trigger('scroll')
    expect(wrap().classes()).toContain('docs-nav-more')

    nav.scrollTop = 600
    await wrapper.get('#docs-sidebar nav').trigger('scroll')
    expect(wrap().classes()).not.toContain('docs-nav-more')
    wrapper.unmount()
  })

  it('renders the interactive Agent Setup Guide in the client setup category and navigates into per-agent guides without losing the active key', async () => {
    const { wrapper, router } = await openDocs('agent-setup')
    expect(wrapper.find('[data-testid="agent-setup-guide"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="site-ccswitch-workflow"]').exists()).toBe(true)
    expect(wrapper.find('.docs-category[aria-current="page"]').text()).toBe('Client setup')

    const demoKey = 'demo-persist-key-9988'
    await wrapper.get('[data-testid="docs-apikey-paste-input"]').setValue(demoKey)
    await wrapper.get('[data-testid="docs-apikey-apply-btn"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="docs-apikey-display"]').text()).toBe(demoKey)

    await router.push('/docs?page=agent-setup&agent=claude-code')
    await flushPromises()
    expect(wrapper.get('[data-testid="agent-detail-header"]').text()).toContain('Claude Code + Example gateway')
    expect(wrapper.get('[data-testid="docs-apikey-display"]').text()).toBe(demoKey)
    expect(wrapper.text()).not.toContain('YOUR_API_KEY')

    await wrapper.get('[data-testid="agent-back-all"]').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.query.agent).toBeUndefined()
    expect(wrapper.find('[data-testid="site-ccswitch-workflow"]').exists()).toBe(true)
    expect(wrapper.get('[data-testid="docs-apikey-display"]').text()).toBe(demoKey)
    wrapper.unmount()
  })

  it('collapses non-active sidebar categories by default and never fights manual expand/collapse actions', async () => {
    const { wrapper, router } = await openDocs('key-self-service')
    const toggle = (cat: string) => wrapper.get(`[data-testid="docs-group-toggle-${cat}"]`)
    const list = (cat: string) => wrapper.get(`[data-testid="docs-group-list-${cat}"]`)
    const expanded = (cat: string) => toggle(cat).attributes('aria-expanded') === 'true'

    // Initial state: only the current page's category (account) is expanded; all others are collapsed.
    expect(expanded('account')).toBe(true)
    expect(list('account').isVisible()).toBe(true)
    for (const cat of ['start', 'guides', 'media', 'integrations', 'reference']) {
      expect(expanded(cat), `initial ${cat}`).toBe(false)
      expect(list(cat).isVisible(), `initial visibility ${cat}`).toBe(false)
    }

    // Expanding another category keeps the current category open as well (no auto-fighting).
    await toggle('reference').trigger('click')
    await nextTick()
    expect(expanded('reference')).toBe(true)
    expect(expanded('account')).toBe(true)

    // User manually collapses the active category: it must stay collapsed even across sibling page navigation.
    await toggle('account').trigger('click')
    await nextTick()
    expect(expanded('account')).toBe(false)
    expect(expanded('reference')).toBe(true)

    await router.push('/docs?page=manage-keys')
    await flushPromises()
    expect(expanded('account')).toBe(false)
    expect(expanded('reference')).toBe(true)

    // Navigating to a different category expands the new category while keeping unrelated user-expanded categories open.
    await router.push('/docs?page=streaming')
    await flushPromises()
    expect(expanded('guides')).toBe(true)
    expect(expanded('reference')).toBe(true)
    expect(expanded('account')).toBe(false)

    // Navigating into a user-expanded category (reference) absorbs it as active, so moving away later collapses it cleanly.
    await router.push('/docs?page=responses')
    await flushPromises()
    expect(expanded('reference')).toBe(true)
    expect(expanded('guides')).toBe(false)

    await router.push('/docs?page=image-generation')
    await flushPromises()
    expect(expanded('media')).toBe(true)
    expect(expanded('reference')).toBe(false)

    // Clicking the active category tab in the top bar after collapsing it re-opens that category.
    await toggle('media').trigger('click')
    await nextTick()
    expect(expanded('media')).toBe(false)
    const mediaTab = wrapper.findAll('.docs-category').find(link => link.text() === 'Image & video')!
    await mediaTab.trigger('click')
    await nextTick()
    expect(expanded('media')).toBe(true)
    wrapper.unmount()
  })

  it('auto-expands matching sidebar categories during search and restores the pre-search collapse state afterward', async () => {
    const { wrapper } = await openDocs('key-self-service')
    const toggle = (cat: string) => wrapper.get(`[data-testid="docs-group-toggle-${cat}"]`)
    const search = wrapper.get('[data-testid="docs-search"]')

    expect(toggle('account').attributes('aria-expanded')).toBe('true')
    expect(toggle('reference').attributes('aria-expanded')).toBe('false')

    await search.setValue('Web fetch')
    expect(toggle('reference').attributes('aria-expanded')).toBe('true')
    expect(wrapper.get('[data-testid="docs-group-list-reference"]').isVisible()).toBe(true)

    // Even during search, manually collapsing a matching group is respected.
    await toggle('reference').trigger('click')
    await nextTick()
    expect(toggle('reference').attributes('aria-expanded')).toBe('false')

    // Clearing search restores the pre-search state (account expanded, reference collapsed).
    await search.setValue('')
    expect(toggle('account').attributes('aria-expanded')).toBe('true')
    expect(toggle('reference').attributes('aria-expanded')).toBe('false')

    // Clicking a top category tab while searching clears the stale query so the chosen category is visible.
    await search.setValue('definitely-no-match-xyz')
    expect(wrapper.find('[data-testid="docs-group-guides"]').exists()).toBe(false)
    const guidesTab = wrapper.findAll('.docs-category').find(link => link.text() === 'API guides')!
    await guidesTab.trigger('click')
    await flushPromises()
    expect((search.element as HTMLInputElement).value).toBe('')
    expect(wrapper.find('[data-testid="docs-group-guides"]').exists()).toBe(true)
    expect(toggle('guides').attributes('aria-expanded')).toBe('true')
    wrapper.unmount()
  })

  it('reveals a deep active sidebar entry after a previously collapsed category finishes expanding, without hijacking manual toggles', async () => {
    const { wrapper, router } = await openDocs('quick-start', { attachTo: true })
    const nav = wrapper.get('#docs-sidebar nav').element as HTMLElement
    Object.defineProperty(nav, 'clientHeight', { configurable: true, value: 240 })
    Object.defineProperty(nav, 'scrollHeight', { configurable: true, value: 960 })
    Object.defineProperty(nav, 'offsetTop', { configurable: true, value: 0 })

    // Give the deep target entry in the currently collapsed `reference` group a below-the-fold offset.
    const deepLink = wrapper.get('#docs-sidebar a[href="/docs?page=deepseek-ocr"]').element as HTMLElement
    Object.defineProperty(deepLink, 'offsetTop', { configurable: true, value: 840 })
    Object.defineProperty(deepLink, 'offsetHeight', { configurable: true, value: 36 })
    nav.scrollTop = 0

    // Navigating across categories into `deepseek-ocr` expands `reference` and reveals the active entry.
    await router.push('/docs?page=deepseek-ocr')
    await flushPromises()
    await nextTick()
    expect(wrapper.get('[data-testid="docs-group-toggle-reference"]').attributes('aria-expanded')).toBe('true')
    expect(nav.scrollTop).toBeGreaterThan(0)

    // Manually toggling another category must not jump the user's current scroll position.
    nav.scrollTop = 40
    await wrapper.get('[data-testid="docs-group-toggle-guides"]').trigger('click')
    await nextTick()
    expect(nav.scrollTop).toBe(40)
    wrapper.unmount()
  })

  it('renders quick-create key actions, summary jump cards, and endpoint copy on key-self-service, manage-keys, and authentication', async () => {
    // Logged-out visitor routes through /login with redirect=/keys?action=create.
    const { wrapper, router } = await openDocs('key-self-service')
    const createCta = wrapper.get('[data-testid="docs-quick-create-key"]')
    expect(createCta.text()).toContain('Sign in to create key')
    expect(router.resolve(createCta.attributes('href')!).path).toBe('/login')
    expect(router.resolve(createCta.attributes('href')!).query.redirect).toBe('/keys?action=create')
    const manageCta = wrapper.get('[data-testid="docs-quick-manage-keys"]')
    expect(router.resolve(manageCta.attributes('href')!).path).toBe('/login')
    expect(router.resolve(manageCta.attributes('href')!).query.redirect).toBe('/keys')

    // Summary jump card navigates to its anchor.
    await wrapper.get('[data-testid="docs-summary-card-key-info-account"]').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.hash).toBe('#key-info-account')

    // Endpoint copy button copies the full endpoint URL for the current origin.
    await wrapper.get('[data-testid="docs-copy-endpoint-key-info-account"]').trigger('click')
    await flushPromises()
    expect(mocks.copy).toHaveBeenCalledWith(expect.stringContaining('/v1/sub2api/account'), 'Copied')

    // Related `authentication` doc page also surfaces the quick-create action.
    await router.push('/docs?page=authentication')
    await flushPromises()
    expect(wrapper.get('[data-testid="docs-quick-create-key"]').text()).toContain('Sign in to create key')
    expect(router.resolve(wrapper.get('[data-testid="docs-quick-create-key"]').attributes('href')!).query.redirect).toBe('/keys?action=create')

    // Logged-in visitor routes directly to /keys?action=create on both key pages and inline section CTA.
    wrapper.unmount()
    mocks.auth.isAuthenticated = true
    const authed = await openDocs('manage-keys')
    expect(authed.wrapper.get('[data-testid="docs-quick-create-key"]').text()).toContain('Quick create API key')
    expect(authed.wrapper.get('[data-testid="docs-quick-create-key"]').attributes('href')).toBe('/keys?action=create')
    expect(authed.wrapper.get('[data-testid="docs-quick-manage-keys"]').attributes('href')).toBe('/keys')
    expect(authed.wrapper.get('[data-testid="docs-section-action-console-create"]').attributes('href')).toBe('/keys?action=create')
    authed.wrapper.unmount()
  })

  it('opens the Cmd/Ctrl+K full-text search modal, highlights matches, and navigates to matched sections via keyboard', async () => {
    const { wrapper, router } = await openDocs('quick-start', { attachTo: true })

    // Open with Ctrl+K shortcut
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'k', ctrlKey: true, bubbles: true }))
    await flushPromises()
    const modal = document.body.querySelector('[data-testid="docs-search-modal"]')
    expect(modal).not.toBeNull()

    const input = document.body.querySelector('[data-testid="docs-search-modal-input"]') as HTMLInputElement
    input.value = 'previous_response_id'
    input.dispatchEvent(new Event('input', { bubbles: true }))
    await flushPromises()

    const firstHit = document.body.querySelector('[data-testid="docs-search-result-0"]') as HTMLButtonElement
    expect(firstHit).not.toBeNull()
    expect(firstHit.textContent).toContain('Responses')

    // Press Enter to navigate straight to the matched page/section and close modal
    input.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }))
    await flushPromises()
    expect(router.currentRoute.value.query.page).toBe('responses')
    expect(document.body.querySelector('[data-testid="docs-search-modal"]')).toBeNull()
    wrapper.unmount()
  })

  it('syncs selected HTTP code tab preference across sections and pages (Starlight/Docusaurus syncKey pattern)', async () => {
    const { wrapper, router } = await openDocs('quick-start')
    const firstReq = wrapper.get('section#first-request')
    const pythonBtn = firstReq.findAll('button').find((b) => b.text() === 'Python')!
    await pythonBtn.trigger('click')
    await flushPromises()

    // Navigate to another multi-language API page and verify Python is already active
    await router.push('/docs?page=image-generation')
    await flushPromises()
    const imgReq = wrapper.get('section#image-request')
    const activeTab = imgReq.find('button[role="tab"][aria-selected="true"]')
    expect(activeTab.text()).toBe('Python')
    wrapper.unmount()
  })

  it('renders breadcrumbs, semantic callouts, stepped quick-start guide, and collapsible mobile TOC', async () => {
    const { wrapper, router } = await openDocs('quick-start')
    expect(wrapper.find('[data-testid="docs-breadcrumbs"]').exists()).toBe(true)
    expect(wrapper.get('[data-testid="docs-breadcrumb-category"]').text()).toBe('Get started')
    expect(wrapper.find('[data-testid="docs-callout-connection"]').exists()).toBe(true)
    expect(wrapper.find('.docs-steps').exists()).toBe(true)

    const mobileTocBtn = wrapper.get('[data-testid="docs-mobile-toc-toggle"]')
    expect(mobileTocBtn.attributes('aria-expanded')).toBe('false')
    await mobileTocBtn.trigger('click')
    expect(mobileTocBtn.attributes('aria-expanded')).toBe('true')
    await wrapper.get('#docs-mobile-toc-list a[href="#read-response"]').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.hash).toBe('#read-response')
    expect(mobileTocBtn.attributes('aria-expanded')).toBe('false')
    wrapper.unmount()
  })
})
