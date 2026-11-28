import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createMemoryHistory, createRouter } from 'vue-router'
import AgentSetupGuide from '../AgentSetupGuide.vue'
import CcsImportModal from '@/components/keys/CcsImportModal.vue'
import Select from '@/components/common/Select.vue'
import { useEphemeralKeyStore } from '@/stores/ephemeralKeys'
import english from '@/i18n/locales/en/docs'

const mocks = vi.hoisted(() => ({
  copy: vi.fn().mockResolvedValue(true),
  listKeys: vi.fn().mockResolvedValue({ items: [], total: 0, page: 1, page_size: 20, pages: 0 }),
  createKey: vi.fn(),
  getVerificationFactors: vi.fn().mockResolvedValue({
    password_available: true,
    totp_enabled: false,
    passkey_count: 0,
    preferred: 'password',
    available_methods: ['password'],
    seal_available: true,
    create_verification_required: false,
  }),
  getAvailableGroups: vi.fn().mockResolvedValue([
    { id: 1, name: 'Claude Group', platform: 'anthropic', rate_multiplier: 1, subscription_type: 'standard', peak_rate_enabled: false, peak_start: '', peak_end: '', peak_rate_multiplier: 1, description: '' },
    { id: 2, name: 'OpenAI Group', platform: 'openai', rate_multiplier: 1, subscription_type: 'standard', peak_rate_enabled: false, peak_start: '', peak_end: '', peak_rate_multiplier: 1, description: '' },
  ]),
  getUserGroupRates: vi.fn().mockResolvedValue({}),
  showSuccess: vi.fn(),
  showError: vi.fn(),
}))

vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({ copyToClipboard: mocks.copy }),
}))

vi.mock('@/api', () => ({
  keysAPI: {
    list: mocks.listKeys,
    create: mocks.createKey,
    getVerificationFactors: mocks.getVerificationFactors,
    putSecretSeal: vi.fn().mockResolvedValue({ message: 'ok' }),
    authorizeReveal: vi.fn(),
    getSecretSeal: vi.fn(),
  },
  userGroupsAPI: {
    getAvailable: mocks.getAvailableGroups,
    getUserGroupRates: mocks.getUserGroupRates,
  },
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showSuccess: mocks.showSuccess,
    showError: mocks.showError,
    showWarning: vi.fn(),
  }),
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({
    user: { id: 1, email: 'user@example.test', role: 'user' },
    isAuthenticated: true,
  }),
}))

function resolveEn(key: string, params?: Record<string, unknown>): string {
  const raw = key
    .split('.')
    .reduce<unknown>((value, part) => (value as Record<string, unknown>)?.[part], english)
  if (typeof raw !== 'string') return key
  if (!params) return raw
  return raw.replace(/\{(\w+)\}/g, (_, name: string) => String(params[name] ?? ''))
}

vi.mock('vue-i18n', async (importOriginal) => ({
  ...(await importOriginal<typeof import('vue-i18n')>()),
  useI18n: () => ({
    locale: { value: 'en' },
    t: (key: string, params?: Record<string, unknown>) => resolveEn(key, params),
  }),
}))

async function mountGuide(props: {
  origin?: string
  siteName?: string
  isAuthenticated?: boolean
  selectedAgentId?: string
} = {}) {
  const pinia = createPinia()
  setActivePinia(pinia)
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/docs', component: { template: '<div />' } },
      { path: '/keys', component: { template: '<div />' } },
      { path: '/login', component: { template: '<div />' } },
      { path: '/model-plaza', component: { template: '<div />' } },
      { path: '/usage', component: { template: '<div />' } },
      { path: '/monitor', component: { template: '<div />' } },
    ],
  })
  await router.push('/docs?page=agent-setup')
  await router.isReady()

  const wrapper = mount(AgentSetupGuide, {
    attachTo: document.body,
    props: {
      origin: props.origin ?? 'https://gateway.example.test',
      siteName: props.siteName ?? 'Example Gateway',
      isAuthenticated: props.isAuthenticated ?? false,
      selectedAgentId: props.selectedAgentId ?? '',
    },
    global: {
      plugins: [pinia, router],
      stubs: {
        Transition: { template: '<slot />' },
        teleport: true,
      },
    },
  })
  await flushPromises()
  return { wrapper, router, pinia }
}

describe('AgentSetupGuide', () => {
  beforeEach(() => {
    mocks.copy.mockClear()
    mocks.showSuccess.mockClear()
    mocks.showError.mockClear()
    mocks.listKeys.mockReset()
    mocks.listKeys.mockResolvedValue({ items: [], total: 0, page: 1, page_size: 20, pages: 0 })
    mocks.getVerificationFactors.mockReset()
    mocks.getVerificationFactors.mockResolvedValue({
      password_available: true,
      totp_enabled: false,
      passkey_count: 0,
      preferred: 'password',
      available_methods: ['password'],
      seal_available: true,
      create_verification_required: false,
    })
    mocks.getAvailableGroups.mockReset()
    mocks.getAvailableGroups.mockResolvedValue([
      { id: 1, name: 'Claude Group', platform: 'anthropic', rate_multiplier: 1, subscription_type: 'standard', peak_rate_enabled: false, peak_start: '', peak_end: '', peak_rate_multiplier: 1, description: '' },
      { id: 2, name: 'OpenAI Group', platform: 'openai', rate_multiplier: 1, subscription_type: 'standard', peak_rate_enabled: false, peak_start: '', peak_end: '', peak_rate_multiplier: 1, description: '' },
    ])
    mocks.getUserGroupRates.mockReset()
    mocks.getUserGroupRates.mockResolvedValue({})
    mocks.createKey.mockReset()
    mocks.createKey.mockResolvedValue({
      id: 77,
      user_id: 1,
      key_prefix: 'sk-demo-',
      key_masked: 'sk-demo-****-7777',
      name: 'Agent Key',
      group_id: 1,
      group_ids: [1, 2],
      status: 'active',
      ip_whitelist: [],
      ip_blacklist: [],
      quota: 0,
      quota_used: 0,
      expires_at: null,
      created_at: '2026-11-27T00:00:00Z',
      updated_at: '2026-11-27T00:00:00Z',
      key: 'test-key-must-not-leak-000077',
    })
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue({
        ok: true,
        status: 200,
        json: async () => ({ data: [{ id: 'claude-sonnet-4-6' }, { id: 'gpt-5.6-sol' }] }),
      }),
    )
  })

  afterEach(() => {
    vi.useRealTimers()
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
    document.body.innerHTML = ''
  })

  it('renders the interactive CC Switch simulator, preserves guest redirect query, and filters agents by workflow', async () => {
    const { wrapper, router } = await mountGuide()

    expect(wrapper.find('[data-testid="site-ccswitch-workflow"]').exists()).toBe(true)
    expect(wrapper.findAll('[data-testid="site-ccswitch-workflow"] ol > li')).toHaveLength(4)
    expect(wrapper.find('[data-testid="ccswitch-interactive-demo"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="ccs-demo-apps-pane"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="overview-active-shot"]').exists()).toBe(false)
    expect(wrapper.findAll('[data-testid="agent-card-grid"] > a')).toHaveLength(10)

    const loginHref = wrapper.get('[data-testid="agent-overview-keys-cta"]').attributes('href')!
    expect(router.resolve(loginHref).path).toBe('/login')
    expect(router.resolve(loginHref).query.redirect).toBe('/docs?page=agent-setup')

    const usageHref = wrapper.get('[data-testid="agent-quick-usage"]').attributes('href')!
    expect(router.resolve(usageHref).path).toBe('/login')
    expect(router.resolve(usageHref).query.redirect).toBe('/docs?page=agent-setup')

    await wrapper.get('[data-testid="agent-filter-ide"]').trigger('click')
    const ideCards = wrapper.findAll('[data-testid="agent-card-grid"] > a')
    expect(ideCards.length).toBeGreaterThanOrEqual(2)
    expect(ideCards.some((card) => card.text().includes('Cursor'))).toBe(true)

    await wrapper.get('[data-testid="agent-filter-ccswitch"]').trigger('click')
    const ccsCards = wrapper.findAll('[data-testid="agent-card-grid"] > a')
    expect(ccsCards.some((card) => card.text().includes('Claude Code'))).toBe(true)
    expect(ccsCards.some((card) => card.text().includes('Cursor'))).toBe(false)

    await wrapper.get('[data-testid="agent-copy-gateway"]').trigger('click')
    await flushPromises()
    expect(mocks.copy).toHaveBeenCalledWith('https://gateway.example.test', 'Copied')
    wrapper.unmount()
  })

  it('completes real in-page key creation -> ApiKeySecretDialog -> real ccswitch:// import from Overview', async () => {
    const { wrapper } = await mountGuide({ isAuthenticated: true })

    // Primary hero CTA opens in-page CreateApiKeyModal; secondary link goes to /keys
    expect(wrapper.get('[data-testid="agent-overview-manage-keys"]').attributes('href')).toBe('/keys')
    expect(wrapper.get('[data-testid="agent-quick-plaza"]').attributes('href')).toBe('/model-plaza')
    expect(wrapper.get('[data-testid="agent-quick-usage"]').attributes('href')).toBe('/usage')

    // Persistent 导入到 CCS button is visible even before creating a key, and STEP 03 bottom CTA says Import to CC Switch
    expect(wrapper.find('[data-testid="docs-apikey-import-ccs-btn"]').exists()).toBe(true)
    await wrapper.get('[data-testid="workflow-step-3"]').trigger('click')
    expect(wrapper.get('[data-testid="overview-step-import-ccs"]').text()).toContain('Import to CC Switch')
    await wrapper.get('[data-testid="workflow-step-1"]').trigger('click')

    // Step 1: Apps view — install OpenClaw
    expect(wrapper.find('[data-testid="ccs-demo-apps-pane"]').exists()).toBe(true)
    await wrapper.get('[data-testid="demo-install-app-openclaw"]').trigger('click')
    await new Promise((r) => setTimeout(r, 450))
    await flushPromises()
    expect(wrapper.find('[data-testid="demo-sidebar-app-openclaw"]').exists()).toBe(true)

    // Step 2: Click workflow step 2 -> click guided Create Key button -> opens real CreateApiKeyModal
    await wrapper.get('[data-testid="workflow-step-2"]').trigger('click')
    await wrapper.get('[data-testid="ccs-guided-create-key-btn"]').trigger('click')
    await flushPromises()

    expect(wrapper.find('[data-testid="create-api-key-modal-form"]').exists()).toBe(true)
    await wrapper.get('[data-testid="create-key-submit-btn"]').trigger('click')
    await flushPromises()

    expect(mocks.createKey).toHaveBeenCalled()
    expect(wrapper.find('[data-test="api-key-secret-dialog"]').exists()).toBe(true)
    expect(wrapper.get('[data-test="api-key-secret-value"]').text()).toBe('test-key-must-not-leak-000077')

    // Click Import to CCS inside ApiKeySecretDialog -> opens real CcsImportModal -> triggers ccswitch:// deeplink
    await wrapper.get('[data-test="api-key-secret-import-ccs"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-test="ccs-import-modal"]').exists()).toBe(true)

    const confirmLink = wrapper.get('[data-test="ccs-import-confirm"]')
    expect(confirmLink.element.tagName).toBe('A')
    expect(confirmLink.attributes('href')).toMatch(/^ccswitch:\/\/v1\/import\?/)
    document.addEventListener('click', (e) => e.preventDefault(), { once: true })
    await confirmLink.trigger('click')
    window.dispatchEvent(new Event('blur'))
    await flushPromises()

    // Simulator advances to Step 4 showing Example Gateway provider card first with zero competitor brands
    expect(wrapper.find('[data-testid="ccs-demo-provider-pane"]').exists()).toBe(true)
    const providerText = wrapper.get('[data-testid="ccs-demo-provider-pane"]').text()
    expect(providerText).toContain('Example Gateway')
    expect(providerText).not.toMatch(/AICodeMirror|PackyCode|APINebula|PatewayAI/i)

    // Sidebar Usage view works inside the simulator without competitor brands
    await wrapper.get('[data-testid="demo-sidebar-usage"]').trigger('click')
    const usagePane = wrapper.get('[data-testid="ccs-demo-usage-pane"]')
    expect(usagePane.exists()).toBe(true)
    expect(usagePane.text()).not.toMatch(/PackyCode/i)

    wrapper.unmount()
  })

  it('defaults agent detail pages (opencode & claude-code) to manual setup, preserves live key across agent switch, and supports 7-app ccswitch-ecosystem import', async () => {
    const { wrapper, router } = await mountGuide({
      selectedAgentId: 'opencode',
      isAuthenticated: false,
    })

    expect(wrapper.get('[data-testid="agent-detail-header"]').text()).toContain('OpenCode + Example Gateway')
    const detailLoginHref = wrapper.get('[data-testid="agent-detail-keys-cta"]').attributes('href')!
    expect(router.resolve(detailLoginHref).path).toBe('/login')
    expect(router.resolve(detailLoginHref).query.redirect).toBe('/docs?page=agent-setup&agent=opencode')

    // Must default to Manual setup!
    expect(wrapper.find('[data-testid="agent-manual-panel"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="agent-ccswitch-panel"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="setup-mode-manual"]').attributes('aria-selected')).toBe('true')

    // Apply a live key in DocsApiKeyBar
    const demoKey = 'demo-user-key-9876543210'
    await wrapper.get('[data-testid="docs-apikey-paste-input"]').setValue(demoKey)
    await wrapper.get('[data-testid="docs-apikey-apply-btn"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="docs-apikey-display"]').text()).toBe(demoKey)

    // Switching to another agent resets mode to manual AND preserves liveApiKey!
    await wrapper.get('[data-testid="setup-mode-ccswitch"]').trigger('click')
    expect(wrapper.find('[data-testid="agent-ccswitch-panel"]').exists()).toBe(true)

    await wrapper.setProps({ selectedAgentId: 'claude-code' })
    await flushPromises()
    expect(wrapper.find('[data-testid="agent-manual-panel"]').exists()).toBe(true)
    expect(wrapper.get('[data-testid="docs-apikey-display"]').text()).toBe(demoKey)

    await wrapper.get('[data-testid="agent-os-powershell"]').trigger('click')
    expect(wrapper.get('[data-testid="agent-manual-panel"]').text()).toContain('$env:ANTHROPIC_BASE_URL="https://gateway.example.test"')
    expect(wrapper.get('[data-testid="agent-manual-panel"]').text()).toContain('%USERPROFILE%\\.claude\\settings.json')

    await wrapper.get('[data-testid="agent-model-input"]').setValue('claude-opus-5-5')
    expect(wrapper.get('[data-testid="agent-manual-panel"]').text()).toContain('$env:ANTHROPIC_MODEL="claude-opus-5-5"')

    // Switch to ccswitch-ecosystem and verify CcsImportModal offers all 7 ecosystem apps
    await wrapper.setProps({ selectedAgentId: 'ccswitch-ecosystem' })
    await flushPromises()
    await wrapper.get('[data-testid="setup-mode-ccswitch"]').trigger('click')
    await wrapper.get('[data-testid="agent-direct-ccs-import"]').trigger('click')
    await flushPromises()
    const ccsModal = wrapper.getComponent(CcsImportModal)
    const platformSelect = ccsModal.findAllComponents(Select)[0]
    const optionValues = (platformSelect.props('options') as Array<{ value: string }>).map((o) => o.value)
    expect(optionValues).toEqual(['hermes', 'pi', 'mcode', 'kimi', 'dsh', 'agy', 'omp'])

    wrapper.unmount()
  })

  it('auto-fills remembered keys from useEphemeralKeyStore on authenticated mount without password prompt', async () => {
    mocks.listKeys.mockResolvedValue({
      items: [
        {
          id: 501,
          user_id: 1,
          key_prefix: 'sk-demo-',
          key_masked: 'sk-demo-****-0501',
          name: 'Remembered Key',
          group_id: 1,
          group: { id: 1, name: 'Claude', platform: 'anthropic' },
          status: 'active',
          ip_whitelist: [],
          ip_blacklist: [],
          quota: 0,
          quota_used: 0,
          expires_at: null,
          created_at: '2026-11-27T00:00:00Z',
          updated_at: '2026-11-27T00:00:00Z',
        },
      ],
      total: 1,
      page: 1,
      page_size: 20,
      pages: 1,
    })

    const pinia = createPinia()
    setActivePinia(pinia)
    const ephemeralKeys = useEphemeralKeyStore()
    ephemeralKeys.remember(501, 'demo-remembered-0501')

    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        { path: '/docs', component: { template: '<div />' } },
        { path: '/keys', component: { template: '<div />' } },
        { path: '/login', component: { template: '<div />' } },
        { path: '/model-plaza', component: { template: '<div />' } },
        { path: '/usage', component: { template: '<div />' } },
      ],
    })
    await router.push('/docs?page=agent-setup&agent=opencode')
    await router.isReady()

    const wrapper = mount(AgentSetupGuide, {
      attachTo: document.body,
      props: {
        origin: 'https://gateway.example.test',
        siteName: 'Example Gateway',
        isAuthenticated: true,
        selectedAgentId: 'opencode',
      },
      global: {
        plugins: [pinia, router],
        stubs: { Transition: { template: '<slot />' }, teleport: true },
      },
    })
    await flushPromises()

    expect(wrapper.get('[data-testid="docs-apikey-display"]').text()).toBe('demo-remembered-0501')
    expect(wrapper.text()).not.toContain('YOUR_API_KEY')
    wrapper.unmount()
  })

  it('renders per-field copyable param cards for VS Code (Cline / Roo Code) and copies unmasked raw snippets even when masked on screen', async () => {
    const { wrapper } = await mountGuide({ selectedAgentId: 'vscode' })

    const card0 = wrapper.get('[data-testid="agent-param-card-0-0"]')
    const card1 = wrapper.get('[data-testid="agent-param-card-0-1"]')
    expect(card0.text()).toContain('OpenAI Compatible')
    expect(card1.text()).toContain('Anthropic')
    expect(wrapper.find('[data-testid="agent-copy-step-0-0"]').exists()).toBe(false)

    await wrapper.get('[data-testid="agent-copy-param-0-0-1"]').trigger('click')
    await flushPromises()
    expect(mocks.copy).toHaveBeenCalledWith('https://gateway.example.test/v1', 'Copied')

    expect(wrapper.find('[data-testid="param-fill-key-btn-0-0-2"]').exists()).toBe(true)

    const demoKey = 'demo-user-key-9876543210'
    await wrapper.get('[data-testid="docs-apikey-paste-input"]').setValue(demoKey)
    await wrapper.get('[data-testid="docs-apikey-apply-btn"]').trigger('click')
    await flushPromises()

    expect(wrapper.get('[data-testid="param-apikey-value-0-0-2"]').text()).toBe(demoKey)
    expect(wrapper.get('[data-testid="param-apikey-value-0-1-2"]').text()).toBe(demoKey)
    expect(wrapper.text()).not.toContain('YOUR_API_KEY')

    // Masking display still copies the real raw key via rawCatalog without string reverse-replacement
    await wrapper.get('[data-testid="docs-apikey-toggle-mask"]').trigger('click')
    expect(wrapper.get('[data-testid="param-apikey-value-0-0-2"]').text()).toContain('••••••••')
    mocks.copy.mockClear()
    await wrapper.get('[data-testid="agent-copy-param-0-0-2"]').trigger('click')
    await flushPromises()
    expect(mocks.copy).toHaveBeenCalledWith(demoKey, 'Copied')

    mocks.copy.mockClear()
    await wrapper.get('[data-testid="agent-copy-verify"]').trigger('click')
    await flushPromises()
    expect(mocks.copy.mock.calls[0][0]).toContain(demoKey)
    expect(mocks.copy.mock.calls[0][0]).not.toContain('••••••••')

    wrapper.unmount()
  })

  it('supports 1-click in-browser connectivity verification and reports success & 401 errors clearly', async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce({
        ok: true,
        status: 200,
        json: async () => ({ data: [{ id: 'claude-sonnet-4-6' }] }),
      })
      .mockResolvedValueOnce({
        ok: true,
        status: 200,
        json: async () => ({ data: [{ id: 'claude-sonnet-4-6' }, { id: 'gpt-5.6-sol' }] }),
      })
      .mockResolvedValueOnce({
        ok: false,
        status: 401,
        json: async () => ({}),
      })
    vi.stubGlobal('fetch', fetchMock)

    const { wrapper } = await mountGuide({ selectedAgentId: 'vscode' })
    const demoKey = 'demo-verify-key-11223344'
    await wrapper.get('[data-testid="docs-apikey-paste-input"]').setValue(demoKey)
    await wrapper.get('[data-testid="docs-apikey-apply-btn"]').trigger('click')
    await flushPromises()

    await wrapper.get('[data-testid="agent-browser-verify-btn"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="agent-browser-verify-result"]').text()).toContain('2')

    await wrapper.get('[data-testid="agent-browser-verify-btn"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="agent-browser-verify-result"]').text()).toContain('401')

    wrapper.unmount()
  })

  it('opens IDE-only agents (Cursor) directly in in-app setup mode with structured param cards and no redundant CC Switch tabs', async () => {
    const { wrapper } = await mountGuide({ selectedAgentId: 'cursor' })

    expect(wrapper.get('[data-testid="agent-detail-header"]').text()).toContain('Cursor + Example Gateway')
    expect(wrapper.find('[data-testid="setup-mode-ccswitch"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="agent-manual-panel"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="agent-param-card-0-0"]').exists()).toBe(true)
    expect(wrapper.get('[data-testid="agent-manual-panel"]').text()).toContain('https://gateway.example.test/v1')
    wrapper.unmount()
  })

  it('emits updated TOC items when switching between overview and agent detail', async () => {
    const { wrapper } = await mountGuide()
    const emitted = wrapper.emitted('toc-change')
    expect(emitted).toBeTruthy()
    expect(emitted![0][0]).toEqual(
      expect.arrayContaining([
        expect.objectContaining({ id: 'site-ccswitch-workflow' }),
        expect.objectContaining({ id: 'pick-your-agent' }),
      ]),
    )

    await wrapper.setProps({ selectedAgentId: 'codex' })
    const latest = wrapper.emitted('toc-change')!.at(-1)![0]
    expect(latest).toEqual(
      expect.arrayContaining([
        expect.objectContaining({ id: 'quick-start' }),
        expect.objectContaining({ id: 'verify-connection' }),
        expect.objectContaining({ id: 'other-agents' }),
      ]),
    )
    wrapper.unmount()
  })

  it('uses Nuxt UI Select (no native <select>), prevents clearKey refill across Overview/Detail, and preserves activeKeyName on Overview->Detail navigation', async () => {
    mocks.listKeys.mockResolvedValue({
      items: [
        {
          id: 601,
          user_id: 1,
          key_prefix: 'sk-demo-',
          key_masked: 'sk-demo-****-0601',
          name: 'First Key',
          group_id: 1,
          has_sealed_secret: true,
          group: { id: 1, name: 'Claude', platform: 'anthropic' },
          status: 'active',
          ip_whitelist: [],
          ip_blacklist: [],
          quota: 0,
          quota_used: 0,
          expires_at: null,
          created_at: '2026-11-27T00:00:00Z',
          updated_at: '2026-11-27T00:00:00Z',
        },
        {
          id: 602,
          user_id: 1,
          key_prefix: 'sk-demo-',
          key_masked: 'sk-demo-****-0602',
          name: 'Second Key',
          group_id: 2,
          has_sealed_secret: false,
          group: { id: 2, name: 'OpenAI', platform: 'openai' },
          status: 'active',
          ip_whitelist: [],
          ip_blacklist: [],
          quota: 0,
          quota_used: 0,
          expires_at: null,
          created_at: '2026-11-27T00:00:00Z',
          updated_at: '2026-11-27T00:00:00Z',
        },
      ],
      total: 2,
      page: 1,
      page_size: 20,
      pages: 1,
    })

    const pinia = createPinia()
    setActivePinia(pinia)
    const ephemeralKeys = useEphemeralKeyStore()
    ephemeralKeys.remember(601, 'demo-remembered-0601')
    ephemeralKeys.remember(602, 'demo-remembered-0602')

    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        { path: '/docs', component: { template: '<div />' } },
        { path: '/keys', component: { template: '<div />' } },
        { path: '/login', component: { template: '<div />' } },
        { path: '/model-plaza', component: { template: '<div />' } },
        { path: '/usage', component: { template: '<div />' } },
      ],
    })
    await router.push('/docs?page=agent-setup')
    await router.isReady()

    const wrapper = mount(AgentSetupGuide, {
      attachTo: document.body,
      props: {
        origin: 'https://gateway.example.test',
        siteName: 'Example Gateway',
        isAuthenticated: true,
        selectedAgentId: '',
      },
      global: {
        plugins: [pinia, router],
        stubs: { Transition: { template: '<slot />' }, teleport: true },
      },
    })
    await flushPromises()

    // Should auto-fill Second Key (activeKeyId = 602) and preserve activeKeyName when switching to Detail
    expect(wrapper.get('[data-testid="docs-apikey-display"]').text()).toBe('demo-remembered-0602')
    await wrapper.setProps({ selectedAgentId: 'opencode' })
    await flushPromises()
    expect(wrapper.get('[data-testid="docs-apikey-bar"]').text()).toContain('Second Key')

    // Clearing key sets autoFillSuppressed so navigating back to Overview does NOT silently refill
    await wrapper.get('[data-testid="docs-apikey-clear"]').trigger('click')
    await flushPromises()
    expect(ephemeralKeys.autoFillSuppressed).toBe(true)
    expect(wrapper.find('[data-testid="docs-apikey-display"]').exists()).toBe(false)
    expect(wrapper.find('select').exists()).toBe(false)
    expect(wrapper.find('[data-testid="docs-apikey-select"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="docs-apikey-import-ccs-btn"]').exists()).toBe(true)

    await wrapper.setProps({ selectedAgentId: '' })
    await flushPromises()
    expect(wrapper.find('[data-testid="docs-apikey-display"]').exists()).toBe(false)

    wrapper.unmount()
  })

  it('does not re-open CcsImportModal on ApiKeySecretDialog Done after cancelling an in-dialog CcsImportModal', async () => {
    vi.useFakeTimers()
    const { wrapper } = await mountGuide({ isAuthenticated: true })

    await wrapper.get('[data-testid="workflow-step-3"]').trigger('click')
    await wrapper.get('[data-testid="overview-step-import-ccs"]').trigger('click')
    await flushPromises()

    expect(wrapper.find('[data-testid="create-api-key-modal-form"]').exists()).toBe(true)
    await wrapper.get('[data-testid="create-key-submit-btn"]').trigger('click')
    await flushPromises()

    expect(wrapper.find('[data-test="api-key-secret-dialog"]').exists()).toBe(true)
    expect(wrapper.get('[data-test="api-key-secret-value"]').text()).toBe('test-key-must-not-leak-000077')

    // User clicks Import to CCS inside ApiKeySecretDialog -> CcsImportModal opens
    await wrapper.get('[data-test="api-key-secret-import-ccs"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-test="ccs-import-modal"]').exists()).toBe(true)

    // User cancels CcsImportModal
    const ccsModal = wrapper.getComponent(CcsImportModal)
    const cancelBtn = ccsModal
      .findAll('button')
      .find((btn) => btn.text().includes('common.cancel') || btn.text().includes('Cancel'))
    expect(cancelBtn).toBeDefined()
    await cancelBtn!.trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-test="ccs-import-modal"]').exists()).toBe(false)

    // Wait for ApiKeySecretDialog MIN_VISIBLE_MS (1000ms) so Done unlocks, then click Done
    vi.advanceTimersByTime(1100)
    await flushPromises()
    await wrapper.get('[data-test="api-key-secret-done"]').trigger('click')
    await flushPromises()

    expect(wrapper.find('[data-test="api-key-secret-dialog"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="ccs-import-modal"]').exists()).toBe(false)

    wrapper.unmount()
  })

  it('still opens CcsImportModal once when clicking ApiKeySecretDialog Done directly after Create Key without in-dialog import', async () => {
    vi.useFakeTimers()
    const { wrapper } = await mountGuide({ isAuthenticated: true })

    await wrapper.get('[data-testid="workflow-step-3"]').trigger('click')
    await wrapper.get('[data-testid="overview-step-import-ccs"]').trigger('click')
    await flushPromises()

    await wrapper.get('[data-testid="create-key-submit-btn"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-test="api-key-secret-dialog"]').exists()).toBe(true)

    vi.advanceTimersByTime(1100)
    await flushPromises()
    await wrapper.get('[data-test="api-key-secret-done"]').trigger('click')
    await flushPromises()

    expect(wrapper.find('[data-test="api-key-secret-dialog"]').exists()).toBe(false)
    expect(wrapper.findAll('[data-test="ccs-import-modal"]')).toHaveLength(1)

    wrapper.unmount()
  })

  it('opens CcsImportModal after guest pastes and applies a key following agent-direct-ccs-import (Defect A)', async () => {
    const { wrapper } = await mountGuide({
      isAuthenticated: false,
      selectedAgentId: 'opencode',
    })

    await wrapper.get('[data-testid="setup-mode-ccswitch"]').trigger('click')
    await wrapper.get('[data-testid="agent-direct-ccs-import"]').trigger('click')
    await flushPromises()

    expect(wrapper.find('[data-test="ccs-import-modal"]').exists()).toBe(false)

    await wrapper.get('[data-testid="docs-apikey-paste-input"]').setValue('test-key-must-not-leak-guest-01')
    await wrapper.get('[data-testid="docs-apikey-apply-btn"]').trigger('click')
    await flushPromises()

    expect(wrapper.find('[data-test="ccs-import-modal"]').exists()).toBe(true)
    const confirmLink = wrapper.get('[data-test="ccs-import-confirm"]')
    expect(confirmLink.element.tagName).toBe('A')
    expect(confirmLink.attributes('href')).toMatch(/^ccswitch:\/\/v1\/import\?/)

    wrapper.unmount()
  })

  it('does not re-open CcsImportModal when cancelling RevealApiKeyDialog without unlocking after prior sealed key clear (Defect B)', async () => {
    mocks.listKeys.mockResolvedValue({
      items: [
        {
          id: 601,
          user_id: 1,
          key_prefix: 'sk-demo-',
          key_masked: 'sk-demo-****-0601',
          name: 'Sealed Key',
          group_id: 1,
          has_sealed_secret: true,
          group: { id: 1, name: 'Claude', platform: 'anthropic' },
          status: 'active',
          ip_whitelist: [],
          ip_blacklist: [],
          quota: 0,
          quota_used: 0,
          expires_at: null,
          created_at: '2026-11-27T00:00:00Z',
          updated_at: '2026-11-27T00:00:00Z',
        },
      ],
      total: 1,
      page: 1,
      page_size: 20,
      pages: 1,
    })

    const pinia = createPinia()
    setActivePinia(pinia)
    const ephemeralKeys = useEphemeralKeyStore()
    ephemeralKeys.remember(601, 'test-key-must-not-leak-sealed-601')

    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        { path: '/docs', component: { template: '<div />' } },
        { path: '/keys', component: { template: '<div />' } },
        { path: '/login', component: { template: '<div />' } },
        { path: '/model-plaza', component: { template: '<div />' } },
        { path: '/usage', component: { template: '<div />' } },
      ],
    })
    await router.push('/docs?page=agent-setup')
    await router.isReady()

    const wrapper = mount(AgentSetupGuide, {
      attachTo: document.body,
      props: {
        origin: 'https://gateway.example.test',
        siteName: 'Example Gateway',
        isAuthenticated: true,
        selectedAgentId: '',
      },
      global: {
        plugins: [pinia, router],
        stubs: { Transition: { template: '<slot />' }, teleport: true },
      },
    })
    await flushPromises()

    // Clear the pre-remembered sealed key
    await wrapper.get('[data-testid="docs-apikey-clear"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-testid="docs-apikey-display"]').exists()).toBe(false)

    // Path 1: persistent docs-apikey-import-ccs-btn -> RevealApiKeyDialog -> Cancel without unlocking
    await wrapper.get('[data-testid="docs-apikey-import-ccs-btn"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-test="api-key-reveal-dialog"]').exists()).toBe(true)

    await wrapper.get('[data-test="api-key-reveal-cancel"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-test="api-key-reveal-dialog"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="ccs-import-modal"]').exists()).toBe(false)

    // Path 2: overview-step-import-ccs -> RevealApiKeyDialog -> Cancel without unlocking -> must clear pending state
    await wrapper.get('[data-testid="workflow-step-3"]').trigger('click')
    await wrapper.get('[data-testid="overview-step-import-ccs"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-test="api-key-reveal-dialog"]').exists()).toBe(true)

    await wrapper.get('[data-test="api-key-reveal-cancel"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-test="api-key-reveal-dialog"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="ccs-import-modal"]').exists()).toBe(false)

    // Subsequent key paste must NOT trigger the cancelled overview-step-import-ccs flow
    await wrapper.get('[data-testid="docs-apikey-paste-input"]').setValue('test-key-must-not-leak-after-cancel-02')
    await wrapper.get('[data-testid="docs-apikey-apply-btn"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-test="ccs-import-modal"]').exists()).toBe(false)

    wrapper.unmount()
  })
})
