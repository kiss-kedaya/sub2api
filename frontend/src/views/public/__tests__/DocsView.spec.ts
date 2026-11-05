import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { nextTick } from 'vue'
import { createMemoryHistory, createRouter } from 'vue-router'
import DocsView from '../DocsView.vue'
import english from '@/i18n/locales/en/docs'
import type { DocsPage } from '@/data/apiDocs'

const mocks = vi.hoisted(() => ({
  copy: vi.fn().mockResolvedValue(true),
  settings: { publicSettingsLoaded: true, siteName: 'Example gateway', siteLogo: '', cachedPublicSettings: null },
  docs: null as null | (() => unknown),
}))

vi.mock('@/stores/app', () => ({ useAppStore: () => mocks.settings }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ isAuthenticated: false, isAdmin: false }) }))
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
    t: (key: string) => key === 'nav.docs' ? 'Docs' : key.split('.').reduce<unknown>((value, part) => (value as Record<string, unknown>)?.[part], english) || key,
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
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/docs', component: DocsView, meta: { requiresAuth: false } },
      ...['/home', '/login', '/model-plaza'].map(path => ({ path, component: { template: '<div />' } })),
    ],
  })
  await router.push(`/docs?page=${query}`)
  await router.isReady()
  const wrapper = mount(DocsView, {
    attachTo: options.attachTo ? document.body : undefined,
    global: { plugins: [router], stubs: { LocaleSwitcher: true } },
  })
  await flushPromises()
  return { wrapper, router }
}

describe('public API documentation page', () => {
  beforeEach(() => {
    mocks.copy.mockClear()
    mocks.docs = null
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

  it('places the image and video category between guides and client setup', async () => {
    const { wrapper } = await openDocs()
    expect(wrapper.findAll('.docs-category').map(link => link.text())).toEqual(['Get started', 'API guides', 'Image & video', 'Client setup', 'API reference'])
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
})
