import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createMemoryHistory, createRouter } from 'vue-router'
import AgentSetupGuide from '../AgentSetupGuide.vue'
import english from '@/i18n/locales/en/docs'

const mocks = vi.hoisted(() => ({
  copy: vi.fn().mockResolvedValue(true),
}))

vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({ copyToClipboard: mocks.copy }),
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
    global: { plugins: [router] },
  })
  await flushPromises()
  return { wrapper, router }
}

describe('AgentSetupGuide', () => {
  beforeEach(() => {
    mocks.copy.mockClear()
  })

  afterEach(() => {
    document.body.innerHTML = ''
  })

  it('renders the 4-step Site + CC Switch workflow and filters agents by workflow', async () => {
    const { wrapper } = await mountGuide()

    expect(wrapper.find('[data-testid="site-ccswitch-workflow"]').exists()).toBe(true)
    expect(wrapper.findAll('[data-testid="site-ccswitch-workflow"] ol > li')).toHaveLength(4)
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

  it('defaults CC Switch agents to the Site + CC Switch flow and supports switching to manual mode with live OS & model updates', async () => {
    const { wrapper } = await mountGuide({
      selectedAgentId: 'claude-code',
      isAuthenticated: true,
    })

    expect(wrapper.get('[data-testid="agent-detail-header"]').text()).toContain('Claude Code + Example Gateway')
    expect(wrapper.get('[data-testid="agent-detail-keys-cta"]').attributes('href')).toBe('/keys')
    expect(wrapper.find('[data-testid="agent-ccswitch-panel"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="agent-manual-panel"]').exists()).toBe(false)

    await wrapper.get('[data-testid="setup-mode-manual"]').trigger('click')
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

  it('opens IDE-only agents (Cursor) directly in in-app setup mode without redundant CC Switch tabs', async () => {
    const { wrapper } = await mountGuide({ selectedAgentId: 'cursor' })

    expect(wrapper.get('[data-testid="agent-detail-header"]').text()).toContain('Cursor + Example Gateway')
    expect(wrapper.find('[data-testid="setup-mode-ccswitch"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="agent-manual-panel"]').exists()).toBe(true)
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

  it('renders quick-action buttons, switches Step 3 screenshots, and opens/closes the fullscreen lightbox', async () => {
    const { wrapper } = await mountGuide({ isAuthenticated: true })

    expect(wrapper.get('[data-testid="agent-overview-keys-cta"]').attributes('href')).toBe('/keys')
    expect(wrapper.get('[data-testid="agent-quick-plaza"]').attributes('href')).toBe('/model-plaza')
    expect(wrapper.get('[data-testid="agent-quick-usage"]').attributes('href')).toBe('/usage')

    const step3ImgBefore = wrapper.get('[data-testid="overview-shot-step3"] img').attributes('alt')
    await wrapper.get('[data-testid="step3-shot-tab-modal"]').trigger('click')
    const step3ImgAfter = wrapper.get('[data-testid="overview-shot-step3"] img').attributes('alt')
    expect(step3ImgAfter).not.toBe(step3ImgBefore)

    await wrapper.get('[data-testid="overview-shot-step1"]').trigger('click')
    expect(document.body.querySelector('[data-testid="agent-screenshot-lightbox"]')).not.toBeNull()

    ;(document.body.querySelector('[data-testid="agent-lightbox-close"]') as HTMLButtonElement).click()
    await flushPromises()
    expect(document.body.querySelector('[data-testid="agent-screenshot-lightbox"]')).toBeNull()

    await wrapper.get('[data-testid="overview-shot-step2"]').trigger('click')
    expect(document.body.querySelector('[data-testid="agent-screenshot-lightbox"]')).not.toBeNull()
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
    await flushPromises()
    expect(document.body.querySelector('[data-testid="agent-screenshot-lightbox"]')).toBeNull()

    wrapper.unmount()
  })
})
