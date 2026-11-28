import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { createMemoryHistory, createRouter } from 'vue-router'

import HomeView from '../HomeView.vue'
import zh from '@/i18n/locales/zh'
import en from '@/i18n/locales/en'
import { formatCurrency } from '@/utils/format'
import { INTERNAL_DOCS_URL } from '@/utils/docsUrl'
import type { ModelPlazaResponse } from '@/api/modelPlaza'

const { appStore, authStore, plazaApi } = vi.hoisted(() => ({
  appStore: {
    cachedPublicSettings: {} as Record<string, unknown>,
    siteName: 'Fallback site',
    siteLogo: '',
    docUrl: '',
    apiBaseUrl: '',
    publicSettingsLoaded: true,
    fetchPublicSettings: vi.fn(),
    showSuccess: vi.fn(),
    showError: vi.fn(),
  },
  authStore: {
    isAuthenticated: false,
    isAdmin: false,
    user: null as { email?: string; balance?: number } | null,
    checkAuth: vi.fn(),
  },
  plazaApi: {
    getModelPlaza: vi.fn(),
  },
}))

vi.mock('@/stores', () => ({
  useAppStore: () => appStore,
  useAuthStore: () => authStore,
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => appStore,
}))

vi.mock('@/api/modelPlaza', () => ({
  getModelPlaza: plazaApi.getModelPlaza,
}))

// The real i18n instance is only initialised by the app bootstrap; specs get a stable locale
// (English formatting) and the copy toast key instead of intlify "missing key" warnings.
vi.mock('@/i18n', () => ({
  getLocale: () => 'en',
  i18n: { global: { t: (key: string) => key } },
}))

vi.mock('vue-i18n', async (importOriginal) => {
  const actual = await importOriginal<typeof import('vue-i18n')>()
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key }),
  }
})

// The SFC source powers the "every referenced i18n key exists in both locales" check.
const homeViewSources = import.meta.glob('../HomeView.vue', {
  query: '?raw',
  import: 'default',
  eager: true,
}) as Record<string, string>
const homeViewSource = Object.values(homeViewSources)[0] ?? ''

enableAutoUnmount(afterEach)

function createTestRouter() {
  return createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/:pathMatch(.*)*', name: 'any', component: { template: '<div />' } }],
  })
}

async function mountHome(settings: Record<string, unknown> = {}) {
  appStore.cachedPublicSettings = {
    site_name: 'Test site',
    site_subtitle: 'Test subtitle',
    ...settings,
  }

  const router = createTestRouter()
  await router.push('/home')
  await router.isReady()

  const wrapper = mount(HomeView, {
    global: {
      plugins: [router],
      stubs: {
        LocaleSwitcher: { template: '<div data-testid="locale-switcher" />' },
        Icon: { template: '<span data-testid="icon" />' },
      },
    },
  })
  await flushPromises()
  return wrapper
}

type HomeWrapper = Awaited<ReturnType<typeof mountHome>>

const byTestId = (wrapper: HomeWrapper, id: string) => wrapper.get(`[data-testid="${id}"]`)

function hrefs(wrapper: HomeWrapper) {
  return wrapper.findAll('a').map((link) => link.attributes('href') ?? '')
}

function linkHref(wrapper: HomeWrapper, id: string) {
  return byTestId(wrapper, id).attributes('href')
}

function modelPlazaDestination(wrapper: HomeWrapper) {
  return wrapper
    .findAll('a')
    .some((link) => link.attributes('href') === '/model-plaza')
}

function statPairs(wrapper: HomeWrapper) {
  return wrapper
    .findAll('[data-testid="home-stats"] li')
    .map((card) => ({
      label: card.get('[data-stat-label]').text(),
      value: card.get('[data-stat-value]').text(),
    }))
}

function plazaResponse(): ModelPlazaResponse {
  const base = {
    description: '',
    subscription_type: 'standard',
    rate_multiplier: 1,
    peak_rate_enabled: false,
    peak_start: '',
    peak_end: '',
    peak_rate_multiplier: 1,
    is_exclusive: false,
    image_rate_independent: false,
    image_rate_multiplier: 1,
    long_context_pricing_enabled: false,
  }
  const model = (name: string, platform: string) => ({
    name,
    platform,
    pricing: null,
    official_pricing: null,
  })
  return {
    description: '',
    groups: [
      {
        ...base,
        id: 1,
        name: 'Anthropic',
        platform: 'anthropic',
        models: [model('claude-a', 'anthropic'), model('claude-b', 'anthropic')],
      },
      {
        ...base,
        id: 2,
        name: 'OpenAI',
        platform: 'openai',
        models: [model('gpt-a', 'openai')],
      },
    ],
  } as ModelPlazaResponse
}

beforeEach(() => {
  authStore.isAuthenticated = false
  authStore.isAdmin = false
  authStore.user = null
  authStore.checkAuth.mockClear()
  appStore.apiBaseUrl = ''
  appStore.fetchPublicSettings.mockClear()
  appStore.showSuccess.mockClear()
  appStore.showError.mockClear()
  plazaApi.getModelPlaza.mockReset()
  plazaApi.getModelPlaza.mockRejectedValue(new Error('model plaza unavailable'))
  localStorage.clear()
  vi.spyOn(window, 'matchMedia').mockReturnValue({ matches: false } as MediaQueryList)
  Object.defineProperty(window, 'isSecureContext', { configurable: true, value: true })
  Object.defineProperty(navigator, 'clipboard', {
    configurable: true,
    value: { writeText: vi.fn().mockResolvedValue(undefined) },
  })
})

describe('HomeView custom and compact modes', () => {
  it('renders custom HTML ahead of compact mode', async () => {
    const wrapper = await mountHome({
      compact_home_enabled: true,
      home_content: '<section id="custom-home">Custom home</section>',
    })

    expect(wrapper.get('#custom-home').text()).toBe('Custom home')
    expect(wrapper.find('[data-testid="compact-home"]').exists()).toBe(false)
  })

  it('renders custom URL content ahead of compact mode', async () => {
    const wrapper = await mountHome({
      compact_home_enabled: true,
      home_content: ' https://example.com/home ',
    })

    expect(wrapper.get('iframe').attributes('src')).toBe('https://example.com/home')
    expect(wrapper.find('[data-testid="compact-home"]').exists()).toBe(false)
  })

  it('treats whitespace-only custom content as empty and selects compact mode', async () => {
    const wrapper = await mountHome({ compact_home_enabled: true, home_content: ' \n\t ' })

    expect(wrapper.get('[data-testid="compact-home"]').text()).toContain('Test site')
    expect(wrapper.get('[data-testid="compact-home-agent-setup-cta"]').attributes('href')).toBe('/docs?page=agent-setup')
  })

  it.each([undefined, false])('selects the default home when compact mode is %s', async (enabled) => {
    const wrapper = await mountHome(enabled === undefined ? {} : { compact_home_enabled: enabled })

    expect(wrapper.find('[data-testid="compact-home"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="home-hero"]').exists()).toBe(true)
    expect(wrapper.text()).toContain('Test subtitle')
  })
})

describe('HomeView default home structure', () => {
  it('no longer renders the removed feature cards or tag pills', async () => {
    const wrapper = await mountHome()

    expect(wrapper.text()).not.toContain('home.features')
    expect(wrapper.text()).not.toContain('home.tags')
    expect(wrapper.find('.home-feature-mark').exists()).toBe(false)
    expect(wrapper.find('.terminal-container').exists()).toBe(false)
  })

  it('drops the removed i18n keys from both locales', () => {
    for (const locale of [zh.home, en.home]) {
      expect(Object.prototype.hasOwnProperty.call(locale, 'features')).toBe(false)
      expect(Object.prototype.hasOwnProperty.call(locale, 'tags')).toBe(false)
    }
  })

  it('lays out hero, then figures, then the three-step guide, then the footer', async () => {
    const wrapper = await mountHome()
    const text = wrapper.text()

    expect(wrapper.find('[data-testid="home-hero"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="home-status"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="home-quickstart"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="home-agent-setup"]').exists()).toBe(true)
    expect(text).toContain('home.heroSubtitle')
    expect(text).toContain('home.providers.title')
    expect(text).toContain('home.quickStart.title')
    expect(text).toContain('home.quickStart.createKey.title')
    expect(text).toContain('home.quickStart.config.title')
    expect(text).toContain('home.quickStart.request.title')
    expect(text).toContain('home.agentSetup.title')
    expect(text).toContain('home.footer.riskStrip')
  })

  it('links directly from the home agent picker cards into the built-in Agent Setup Guide', async () => {
    const wrapper = await mountHome()

    expect(linkHref(wrapper, 'home-header-agent-setup')).toBe('/docs?page=agent-setup')
    expect(linkHref(wrapper, 'home-agent-setup-all')).toBe('/docs?page=agent-setup')
    expect(linkHref(wrapper, 'home-agent-card-claude-code')).toBe('/docs?page=agent-setup&agent=claude-code')
    expect(linkHref(wrapper, 'home-agent-card-codex')).toBe('/docs?page=agent-setup&agent=codex')
    expect(linkHref(wrapper, 'home-agent-setup-ccs')).toBe('/docs?page=agent-setup#site-ccswitch-workflow')
  })

  it('renders the three quick-start steps in order', async () => {
    const wrapper = await mountHome()
    const steps = wrapper.findAll('[data-testid="home-quickstart"] ol > li')

    expect(steps).toHaveLength(3)
    expect(steps[0].text()).toContain('home.quickStart.createKey.title')
    expect(steps[1].text()).toContain('home.quickStart.config.title')
    expect(steps[2].text()).toContain('home.quickStart.request.title')
  })

  it('falls back to the generic one-liner when the subtitle repeats the promise', async () => {
    // useI18n is mocked to echo keys, so the configured subtitle is literally the hero key.
    const wrapper = await mountHome({ site_subtitle: 'home.heroSubtitle' })

    expect(byTestId(wrapper, 'home-hero').text()).toContain('home.heroDescription')
    expect(byTestId(wrapper, 'home-hero').text()).not.toContain('home.heroSubtitlehome.heroSubtitle')
  })

  it('keeps the built-in docs link when the admin configured no external docs URL', async () => {
    const wrapper = await mountHome()

    expect(linkHref(wrapper, 'home-hero-docs')).toBe(INTERNAL_DOCS_URL)
    expect(linkHref(wrapper, 'home-docs-link')).toBe(INTERNAL_DOCS_URL)
  })

  it('prefers the configured docs URL for both docs links', async () => {
    const wrapper = await mountHome({ doc_url: 'https://docs.example.com' })

    expect(linkHref(wrapper, 'home-hero-docs')).toBe('https://docs.example.com/')
    expect(byTestId(wrapper, 'home-hero-docs').attributes('target')).toBe('_blank')
    expect(linkHref(wrapper, 'home-docs-link')).toBe('https://docs.example.com/')
  })
})

describe('HomeView call to action by auth state', () => {
  it('sends anonymous visitors to login and keeps the login label', async () => {
    const wrapper = await mountHome()

    expect(linkHref(wrapper, 'home-hero-cta')).toBe('/login')
    expect(byTestId(wrapper, 'home-hero-cta').text()).toContain('home.login')
    expect(linkHref(wrapper, 'home-header-cta')).toBe('/login')
    expect(byTestId(wrapper, 'home-header-cta').text()).toContain('home.login')
  })

  it('offers registration when the site enables it', async () => {
    const wrapper = await mountHome({ registration_enabled: true })

    expect(linkHref(wrapper, 'home-hero-cta')).toBe('/register')
    expect(byTestId(wrapper, 'home-hero-cta').text()).toContain('home.getStarted')
    expect(linkHref(wrapper, 'home-key-action')).toBe('/register')
  })

  it('shows the console instead of login for authenticated users', async () => {
    authStore.isAuthenticated = true
    authStore.user = { email: 'user@example.com', balance: 0 }

    const wrapper = await mountHome()

    expect(hrefs(wrapper)).not.toContain('/login')
    expect(linkHref(wrapper, 'home-hero-cta')).toBe('/dashboard')
    expect(byTestId(wrapper, 'home-hero-cta').text()).toContain('home.goToDashboard')
    expect(linkHref(wrapper, 'home-header-cta')).toBe('/dashboard')
    expect(byTestId(wrapper, 'home-header-cta').text()).toContain('home.goToDashboard')
    expect(linkHref(wrapper, 'home-key-action')).toBe('/keys')
  })

  it('links administrators to the admin dashboard', async () => {
    authStore.isAuthenticated = true
    authStore.isAdmin = true

    const wrapper = await mountHome()

    expect(linkHref(wrapper, 'home-hero-cta')).toBe('/admin/dashboard')
    expect(authStore.checkAuth).toHaveBeenCalledOnce()
    expect(appStore.fetchPublicSettings).not.toHaveBeenCalled()
  })
})

describe('HomeView figures and platform data', () => {
  it('renders model and platform counts from the model plaza', async () => {
    plazaApi.getModelPlaza.mockResolvedValue(plazaResponse())

    const wrapper = await mountHome({ model_plaza_enabled: true, model_plaza_require_auth: false })

    expect(plazaApi.getModelPlaza).toHaveBeenCalledOnce()
    expect(wrapper.text()).toContain('home.stats.title')
    expect(statPairs(wrapper)).toEqual([
      { label: 'home.stats.models', value: '3' },
      { label: 'home.stats.platforms', value: '2' },
    ])
  })

  it('adds group count and balance only for authenticated users', async () => {
    plazaApi.getModelPlaza.mockResolvedValue(plazaResponse())
    authStore.isAuthenticated = true
    authStore.user = { email: 'user@example.com', balance: 12.5 }

    const wrapper = await mountHome({ model_plaza_enabled: true, model_plaza_require_auth: false })

    expect(statPairs(wrapper)).toEqual([
      { label: 'home.stats.models', value: '3' },
      { label: 'home.stats.platforms', value: '2' },
      { label: 'home.stats.groups', value: '2' },
      { label: 'home.stats.balance', value: formatCurrency(12.5) },
    ])
  })

  it('shows no figures when the plaza is disabled', async () => {
    const wrapper = await mountHome({ model_plaza_enabled: false })

    expect(plazaApi.getModelPlaza).not.toHaveBeenCalled()
    expect(wrapper.find('[data-testid="home-stats"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="home-provider-list"]').exists()).toBe(true)
  })

  it('shows no figures when the plaza request fails instead of inventing numbers', async () => {
    plazaApi.getModelPlaza.mockRejectedValue(new Error('404'))
    authStore.isAuthenticated = true
    authStore.user = { email: 'user@example.com', balance: 0 }

    const wrapper = await mountHome({ model_plaza_enabled: true, model_plaza_require_auth: false })

    expect(statPairs(wrapper)).toEqual([{ label: 'home.stats.balance', value: formatCurrency(0) }])
  })

  it('does not call an auth-gated plaza for anonymous visitors', async () => {
    const wrapper = await mountHome({ model_plaza_enabled: true, model_plaza_require_auth: true })

    expect(plazaApi.getModelPlaza).not.toHaveBeenCalled()
    expect(wrapper.find('[data-testid="home-stats"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="home-provider-list"]').exists()).toBe(true)
  })

  it('shows the model plaza link to anonymous visitors when public access is enabled', async () => {
    const wrapper = await mountHome({
      compact_home_enabled: true,
      model_plaza_enabled: true,
      model_plaza_require_auth: false,
    })

    expect(modelPlazaDestination(wrapper)).toBe(true)
  })

  it('hides the model plaza link from anonymous visitors when sign-in is required', async () => {
    const wrapper = await mountHome({
      compact_home_enabled: true,
      model_plaza_enabled: true,
      model_plaza_require_auth: true,
    })

    expect(modelPlazaDestination(wrapper)).toBe(false)
  })

  it('shows the model plaza link to authenticated visitors when sign-in is required', async () => {
    authStore.isAuthenticated = true

    const wrapper = await mountHome({
      compact_home_enabled: true,
      model_plaza_enabled: true,
      model_plaza_require_auth: true,
    })

    expect(modelPlazaDestination(wrapper)).toBe(true)
  })

  it('shows the model plaza link in the default home header', async () => {
    const wrapper = await mountHome({
      model_plaza_enabled: true,
      model_plaza_require_auth: false,
    })

    expect(modelPlazaDestination(wrapper)).toBe(true)
  })

  it('hides the model plaza link when the feature is disabled', async () => {
    const wrapper = await mountHome({
      compact_home_enabled: true,
      model_plaza_enabled: false,
      model_plaza_require_auth: false,
    })

    expect(modelPlazaDestination(wrapper)).toBe(false)
  })
})

describe('HomeView code examples', () => {
  it('derives the base URL from the configured API endpoint', async () => {
    appStore.apiBaseUrl = 'https://api.example.com'

    const wrapper = await mountHome()

    expect(byTestId(wrapper, 'home-base-url-example').text()).toBe('https://api.example.com/v1')
    expect(byTestId(wrapper, 'home-request-example').text()).toContain('https://api.example.com/v1/chat/completions')
  })

  it('falls back to the current origin and never repeats /v1', async () => {
    const wrapper = await mountHome()
    expect(byTestId(wrapper, 'home-base-url-example').text()).toBe(`${window.location.origin}/v1`)

    appStore.apiBaseUrl = 'https://api.example.com/v1'
    const configured = await mountHome()
    expect(byTestId(configured, 'home-base-url-example').text()).toBe('https://api.example.com/v1')
  })

  it('writes /v1 once in the curl example however the endpoint was configured', async () => {
    // The admin setting is normally saved with its /v1 suffix; the example used to append another one.
    for (const configured of ['https://api.example.com', 'https://api.example.com/', 'https://api.example.com/v1', 'https://api.example.com/v1/', 'https://api.example.com/v1/v1']) {
      appStore.apiBaseUrl = configured
      const wrapper = await mountHome()
      const curl = byTestId(wrapper, 'home-request-example').text()

      expect(curl, configured).toContain('curl https://api.example.com/v1/chat/completions')
      expect(curl, configured).not.toContain('/v1/v1')
      expect(byTestId(wrapper, 'home-base-url-example').text(), configured).toBe('https://api.example.com/v1')
      wrapper.unmount()
    }
  })

  it('reads the public setting when the store has no endpoint of its own', async () => {
    appStore.apiBaseUrl = ''
    const wrapper = await mountHome({ api_base_url: 'https://kedaya.example/v1' })

    expect(byTestId(wrapper, 'home-request-example').text()).toContain('curl https://kedaya.example/v1/chat/completions')
    expect(byTestId(wrapper, 'home-request-example').text()).not.toContain('/v1/v1')
  })

  it('builds the curl example with real field names and a listed model', async () => {
    plazaApi.getModelPlaza.mockResolvedValue(plazaResponse())

    const wrapper = await mountHome({ model_plaza_enabled: true, model_plaza_require_auth: false })
    const curl = byTestId(wrapper, 'home-request-example').text()

    expect(curl).toContain('/v1/chat/completions')
    expect(curl).toContain('Authorization: Bearer $API_KEY')
    expect(curl).toContain('"model":"claude-a"')
    expect(curl).toContain('"messages":[{"role":"user","content":"Hello!"}]')
  })

  it('keeps the model placeholder when the plaza gives no model list', async () => {
    const wrapper = await mountHome()

    expect(byTestId(wrapper, 'home-request-example').text()).toContain('"model":"YOUR_MODEL_ID"')
  })

  it('copies the base URL and flips the button to the copied state', async () => {
    const wrapper = await mountHome()
    const button = () => byTestId(wrapper, 'home-copy-base-url')

    expect(button().text()).toContain('common.copy')

    await button().trigger('click')
    await flushPromises()

    expect(navigator.clipboard.writeText).toHaveBeenCalledWith(`${window.location.origin}/v1`)
    expect(button().text()).toContain('common.copied')
    expect(appStore.showSuccess).toHaveBeenCalledOnce()
  })

  it('copies the curl example too', async () => {
    const wrapper = await mountHome()

    await byTestId(wrapper, 'home-copy-request').trigger('click')
    await flushPromises()

    const copied = vi.mocked(navigator.clipboard.writeText).mock.calls[0]?.[0] as string
    expect(copied).toContain('curl')
    expect(copied).toContain('/v1/chat/completions')
    expect(byTestId(wrapper, 'home-copy-request').text()).toContain('common.copied')
  })
})

describe('HomeView footer', () => {
  it('exposes legal policy links in compact and default footers', async () => {
    const compact = await mountHome({ compact_home_enabled: true })
    expect(hrefs(compact)).toContain('/legal/privacy')
    expect(compact.text()).toContain('home.footer.riskStrip')

    const home = await mountHome()
    expect(home.text()).toContain('home.footer.riskStrip')
    expect(hrefs(home)).toContain('/legal/disclaimer')
  })
})

describe('HomeView i18n keys', () => {
  function resolveKey(locale: unknown, key: string): unknown {
    return key.split('.').reduce<unknown>((node, part) => {
      if (node && typeof node === 'object') return (node as Record<string, unknown>)[part]
      return undefined
    }, locale)
  }

  it('references only home.* keys that exist in both locales', () => {
    const keys = [...homeViewSource.matchAll(/\bt\(\s*'([^']+)'/g)]
      .map((match) => match[1])
      .filter((key) => key.startsWith('home.'))
    expect(new Set(keys).size).toBeGreaterThan(5)

    for (const key of new Set(keys)) {
      expect(typeof resolveKey(zh, key), `zh is missing ${key}`).toBe('string')
      expect(typeof resolveKey(en, key), `en is missing ${key}`).toBe('string')
    }
  })
})

describe('HomeView craft invariants', () => {
  it('uses dvh/svh viewports, semantic tokens in compact mode, and GPU-only agent card transitions', () => {
    expect(homeViewSource).toContain('min-h-dvh')
    expect(homeViewSource).toContain('h-dvh')
    expect(homeViewSource).toContain('min-h-svh')
    expect(homeViewSource).not.toContain('min-h-screen')
    expect(homeViewSource).not.toContain('h-screen')
    expect(homeViewSource).not.toMatch(/\b(?:text|bg|border)-(?:gray|dark|slate)-/)
    expect(homeViewSource).toContain('motion-reduce:transition-none motion-reduce:group-hover:translate-x-0')

    const styleMatch = homeViewSource.match(/<style scoped>([\s\S]*)<\/style>/)
    expect(styleMatch?.[1]).toBeTruthy()
    expect(styleMatch![1]).not.toMatch(/transition\s*:[^;]*box-shadow/)
  })
})
