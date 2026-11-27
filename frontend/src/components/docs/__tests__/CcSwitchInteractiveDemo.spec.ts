import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { ref } from 'vue'
import CcSwitchInteractiveDemo from '../CcSwitchInteractiveDemo.vue'
import { ccswitchDemoZh } from '@/i18n/locales/zh/ccswitchDemo'
import { ccswitchDemoEn } from '@/i18n/locales/en/ccswitchDemo'

const mockLocale = ref<'zh' | 'en'>('zh')

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    locale: mockLocale,
  }),
}))

function flattenKeys(obj: unknown, prefix = ''): string[] {
  if (obj === null || typeof obj !== 'object' || Array.isArray(obj)) {
    return prefix ? [prefix] : []
  }
  return Object.entries(obj as Record<string, unknown>).flatMap(([k, v]) =>
    flattenKeys(v, prefix ? `${prefix}.${k}` : k),
  )
}

describe('CcSwitchInteractiveDemo (1:1 ccswitch.io workbench)', () => {
  beforeEach(() => {
    mockLocale.value = 'zh'
  })

  afterEach(() => {
    vi.useRealTimers()
    document.body.innerHTML = ''
  })

  it('keeps zh and en ccswitchDemo locale schemas 100% identical', () => {
    const zhKeys = flattenKeys(ccswitchDemoZh).sort()
    const enKeys = flattenKeys(ccswitchDemoEn).sort()
    expect(zhKeys).toEqual(enKeys)
    expect(zhKeys.length).toBeGreaterThan(250)
  })

  it('supports Step 0 Apps install, sidebar toggle, and guided spotlight', async () => {
    vi.useFakeTimers()
    const wrapper = mount(CcSwitchInteractiveDemo, {
      props: {
        origin: 'https://gateway.example.test',
        siteName: 'kedaya.ai',
        activeStep: 0,
      },
    })

    expect(wrapper.find('[data-testid="ccs-demo-apps-pane"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="ccs-guided-banner-apps"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="demo-sidebar-app-openclaw"]').exists()).toBe(false)

    await wrapper.get('[data-testid="demo-install-app-openclaw"]').trigger('click')
    vi.advanceTimersByTime(500)
    await flushPromises()
    expect(wrapper.find('[data-testid="demo-sidebar-app-openclaw"]').exists()).toBe(true)

    await wrapper.get('[data-testid="demo-toggle-app-claude"]').trigger('click')
    expect(wrapper.find('[data-testid="demo-sidebar-app-claude"]').exists()).toBe(false)

    wrapper.unmount()
  })

  it('emits request-create-key on Step 1 and request-import-ccs on Step 2, then activates site provider on Step 3', async () => {
    const wrapper = mount(CcSwitchInteractiveDemo, {
      props: {
        origin: 'https://gateway.example.test',
        siteName: 'kedaya.ai',
        activeStep: 1,
        focusAgentId: 'claude-code',
        hasLiveKey: false,
        importedToCcs: false,
      },
    })

    expect(wrapper.find('[data-testid="ccs-demo-provider-pane"]').exists()).toBe(true)
    await wrapper.get('[data-testid="ccs-guided-create-key-btn"]').trigger('click')
    expect(wrapper.emitted('request-create-key')).toHaveLength(1)

    await wrapper.setProps({ activeStep: 2, hasLiveKey: true, maskedKeyPreview: 'demo-key-••••1234' })
    await wrapper.get('[data-testid="ccs-guided-import-ccs-btn"]').trigger('click')
    expect(wrapper.emitted('request-import-ccs')).toEqual([['claude']])

    await wrapper.setProps({ activeStep: 3, importedToCcs: true })
    expect(wrapper.text()).toContain('kedaya.ai')
    expect(wrapper.text()).toContain('https://gateway.example.test')
    expect(wrapper.text()).toContain('demo-key-••••1234')

    await wrapper.get('[data-testid="demo-activate-site-provider"]').trigger('click')
    expect(wrapper.emitted('provider-activated')).toEqual([['claude']])
    expect(wrapper.find('[data-testid="ccs-demo-toast"]').exists()).toBe(true)

    wrapper.unmount()
  })

  it('navigates all 8 secondary panes and switches provider modes cleanly', async () => {
    const wrapper = mount(CcSwitchInteractiveDemo, {
      props: {
        origin: 'https://gateway.example.test',
        siteName: 'kedaya.ai',
        activeStep: 3,
      },
    })

    // Mode switching: route mode shows mismatch activation banner
    await wrapper.get('[data-testid="demo-mode-tab-route"]').trigger('click')
    expect(wrapper.text()).toContain('开始路由')

    // All secondary nav panes
    await wrapper.get('[data-testid="demo-sidebar-usage"]').trigger('click')
    expect(wrapper.find('[data-testid="ccs-demo-usage-pane"]').exists()).toBe(true)

    await wrapper.get('[data-testid="demo-sidebar-mcp"]').trigger('click')
    expect(wrapper.find('[data-testid="ccs-demo-mcp-pane"]').exists()).toBe(true)

    await wrapper.get('[data-testid="demo-sidebar-skills"]').trigger('click')
    expect(wrapper.find('[data-testid="ccs-demo-skills-pane"]').exists()).toBe(true)

    await wrapper.get('[data-testid="demo-sidebar-prompts"]').trigger('click')
    expect(wrapper.find('[data-testid="ccs-demo-prompts-pane"]').exists()).toBe(true)

    await wrapper.get('[data-testid="demo-sidebar-sessions"]').trigger('click')
    expect(wrapper.find('[data-testid="ccs-demo-sessions-pane"]').exists()).toBe(true)

    await wrapper.get('[data-testid="demo-sidebar-auth"]').trigger('click')
    expect(wrapper.find('[data-testid="ccs-demo-auth-pane"]').exists()).toBe(true)

    await wrapper.get('[data-testid="demo-sidebar-settings"]').trigger('click')
    expect(wrapper.find('[data-testid="ccs-demo-settings-pane"]').exists()).toBe(true)

    wrapper.unmount()
  })
})
