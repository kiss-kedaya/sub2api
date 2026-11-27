import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createMemoryHistory, createRouter } from 'vue-router'
import AgentSetupGuide from '../AgentSetupGuide.vue'
import english from '@/i18n/locales/en/docs'

const mocks = vi.hoisted(() => ({
  copy: vi.fn().mockResolvedValue(true),
  listKeys: vi.fn().mockResolvedValue({ items: [], total: 0, page: 1, page_size: 20, pages: 0 }),
}))

vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({ copyToClipboard: mocks.copy }),
}))

vi.mock('@/api', () => ({
  keysAPI: {
    list: mocks.listKeys,
  },
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
    props: {
      origin: props.origin ?? 'https://gateway.example.test',
      siteName: props.siteName ?? 'Example Gateway',
      isAuthenticated: props.isAuthenticated ?? false,
      selectedAgentId: props.selectedAgentId ?? '',
    },
    global: {
      plugins: [router],
      stubs: {
        Transition: { template: '<slot />' },
      },
    },
  })
  await flushPromises()
  return { wrapper, router }
}

describe('AgentSetupGuide', () => {
  beforeEach(() => {
    mocks.copy.mockClear()
    mocks.listKeys.mockReset()
    mocks.listKeys.mockResolvedValue({ items: [], total: 0, page: 1, page_size: 20, pages: 0 })
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
    document.body.innerHTML = ''
  })

  it('renders the interactive CC Switch simulator (no static screenshots) and filters agents by workflow', async () => {
    const { wrapper } = await mountGuide()

    expect(wrapper.find('[data-testid="site-ccswitch-workflow"]').exists()).toBe(true)
    expect(wrapper.findAll('[data-testid="site-ccswitch-workflow"] ol > li')).toHaveLength(4)
    expect(wrapper.find('[data-testid="ccswitch-interactive-demo"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="ccs-demo-apps-pane"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="overview-active-shot"]').exists()).toBe(false)
    expect(wrapper.findAll('[data-testid="agent-card-grid"] > a')).toHaveLength(10)
    expect(wrapper.get('[data-testid="agent-overview-keys-cta"]').attributes('href')).toBe('/login')

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

  it('walks through all 4 interactive CC Switch simulator stages without needing static screenshots', async () => {
    vi.useFakeTimers()
    const { wrapper } = await mountGuide({ isAuthenticated: true })

    expect(wrapper.get('[data-testid="agent-overview-keys-cta"]').attributes('href')).toBe('/keys')
    expect(wrapper.get('[data-testid="agent-quick-plaza"]').attributes('href')).toBe('/model-plaza')
    expect(wrapper.get('[data-testid="agent-quick-usage"]').attributes('href')).toBe('/usage')

    // Step 1: Apps view — install OpenClaw and toggle Claude Code visibility in sidebar
    expect(wrapper.find('[data-testid="ccs-demo-apps-pane"]').exists()).toBe(true)
    await wrapper.get('[data-testid="demo-install-app-openclaw"]').trigger('click')
    vi.advanceTimersByTime(500)
    await flushPromises()
    expect(wrapper.find('[data-testid="demo-sidebar-app-openclaw"]').exists()).toBe(true)

    await wrapper.get('[data-testid="demo-toggle-app-claude"]').trigger('click')
    expect(wrapper.find('[data-testid="demo-sidebar-app-claude"]').exists()).toBe(false)

    // Step 2: Create Key view — toggle Smart Routing & simulate creating a key -> advances to Step 3
    await wrapper.get('[data-testid="workflow-step-2"]').trigger('click')
    expect(wrapper.find('[data-testid="demo-smart-routing-toggle"]').exists()).toBe(true)
    await wrapper.get('[data-testid="demo-simulate-create-key"]').trigger('click')
    expect(wrapper.find('[data-testid="demo-click-import-ccs"]').exists()).toBe(true)

    // Step 3: Receipt -> Import modal -> Confirm import -> advances to Step 4 Provider view
    await wrapper.get('[data-testid="demo-click-import-ccs"]').trigger('click')
    await wrapper.get('[data-testid="demo-import-app-opencode"]').trigger('click')
    await wrapper.get('[data-testid="demo-confirm-import-ccs"]').trigger('click')
    expect(wrapper.find('[data-testid="ccs-demo-provider-pane"]').exists()).toBe(true)
    expect(wrapper.get('[data-testid="ccs-demo-provider-pane"]').text()).toContain('OpenCode')

    // Sidebar Usage view works inside the simulator
    await wrapper.get('[data-testid="demo-sidebar-usage"]').trigger('click')
    expect(wrapper.find('[data-testid="ccs-demo-usage-pane"]').exists()).toBe(true)

    wrapper.unmount()
  })

  it('defaults agent detail pages (opencode & claude-code) to manual setup instead of duplicating CC Switch, with compact CC Switch tab available', async () => {
    const { wrapper } = await mountGuide({
      selectedAgentId: 'opencode',
      isAuthenticated: true,
    })

    expect(wrapper.get('[data-testid="agent-detail-header"]').text()).toContain('OpenCode + Example Gateway')
    expect(wrapper.get('[data-testid="agent-detail-keys-cta"]').attributes('href')).toBe('/keys')
    // Must default to Manual setup!
    expect(wrapper.find('[data-testid="agent-manual-panel"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="agent-ccswitch-panel"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="setup-mode-manual"]').attributes('aria-selected')).toBe('true')

    // Switching to CC Switch shows the compact agent-specific panel with interactive simulator
    await wrapper.get('[data-testid="setup-mode-ccswitch"]').trigger('click')
    expect(wrapper.find('[data-testid="agent-ccswitch-panel"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="agent-direct-ccs-import"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="ccswitch-interactive-demo"]').exists()).toBe(true)

    // Switching to another agent resets back to manual mode
    await wrapper.setProps({ selectedAgentId: 'claude-code' })
    await flushPromises()
    expect(wrapper.find('[data-testid="agent-manual-panel"]').exists()).toBe(true)

    await wrapper.get('[data-testid="agent-os-powershell"]').trigger('click')
    expect(wrapper.get('[data-testid="agent-manual-panel"]').text()).toContain('$env:ANTHROPIC_BASE_URL="https://gateway.example.test"')
    expect(wrapper.get('[data-testid="agent-manual-panel"]').text()).toContain('%USERPROFILE%\\.claude\\settings.json')

    await wrapper.get('[data-testid="agent-model-input"]').setValue('claude-opus-5-5')
    expect(wrapper.get('[data-testid="agent-manual-panel"]').text()).toContain('$env:ANTHROPIC_MODEL="claude-opus-5-5"')

    await wrapper.get('[data-testid="agent-copy-baseurl"]').trigger('click')
    await flushPromises()
    expect(mocks.copy).toHaveBeenCalledWith('https://gateway.example.test', 'Copied')
    wrapper.unmount()
  })

  it('renders per-field copyable param cards for VS Code (Cline / Roo Code) and auto-fills API keys across cards & verify commands', async () => {
    const { wrapper } = await mountGuide({ selectedAgentId: 'vscode' })

    // Two structured parameter cards (OpenAI Compatible & Anthropic), zero useless whole-card Copy Code button
    const card0 = wrapper.get('[data-testid="agent-param-card-0-0"]')
    const card1 = wrapper.get('[data-testid="agent-param-card-0-1"]')
    expect(card0.text()).toContain('OpenAI Compatible')
    expect(card1.text()).toContain('Anthropic')
    expect(wrapper.find('[data-testid="agent-copy-step-0-0"]').exists()).toBe(false)

    // Individual Base URL row copies just the URL value
    await wrapper.get('[data-testid="agent-copy-param-0-0-1"]').trigger('click')
    await flushPromises()
    expect(mocks.copy).toHaveBeenCalledWith('https://gateway.example.test/v1', 'Copied')

    // Before filling key, API Key row offers an interactive fill button instead of dead YOUR_API_KEY copy
    expect(wrapper.find('[data-testid="param-fill-key-btn-0-0-2"]').exists()).toBe(true)

    // Paste & apply a key via the top DocsApiKeyBar
    const demoKey = 'demo-user-key-9876543210'
    await wrapper.get('[data-testid="docs-apikey-paste-input"]').setValue(demoKey)
    await wrapper.get('[data-testid="docs-apikey-apply-btn"]').trigger('click')
    await flushPromises()

    // YOUR_API_KEY is completely replaced in both param cards and verify command
    expect(wrapper.get('[data-testid="param-apikey-value-0-0-2"]').text()).toBe(demoKey)
    expect(wrapper.get('[data-testid="param-apikey-value-0-1-2"]').text()).toBe(demoKey)
    expect(wrapper.text()).not.toContain('YOUR_API_KEY')

    // Masking display still copies the real plaintext key
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
})
