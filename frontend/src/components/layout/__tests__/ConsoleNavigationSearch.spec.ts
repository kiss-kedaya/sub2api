import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createI18n } from 'vue-i18n'
import { createMemoryHistory, createRouter } from 'vue-router'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { useAuthStore } from '@/stores/auth'
import { useAppStore } from '@/stores/app'
import { useAdminSettingsStore } from '@/stores/adminSettings'
import ConsoleNavigationSearch from '../ConsoleNavigationSearch.vue'

// UModal teleports its content; render it in place so the palette can be driven.
const ModalStub = {
  props: ['open'],
  emits: ['update:open'],
  template: '<div v-if="open" data-test="palette"><slot name="content" /></div>'
}

let wrapper: VueWrapper | undefined

async function render(role: 'user' | 'admin', simpleMode = false, userId = role === 'admin' ? 1 : 900001) {
  const pinia = createPinia()
  setActivePinia(pinia)
  const auth = useAuthStore()
  auth.user = { id: userId, role } as NonNullable<typeof auth.user>
  vi.spyOn(auth, 'isSimpleMode', 'get').mockReturnValue(simpleMode)
  const app = useAppStore()
  app.cachedPublicSettings = { payment_enabled: false, channel_monitor_enabled: false } as NonNullable<typeof app.cachedPublicSettings>
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:pathMatch(.*)*', component: { template: '<div />' } }] })
  await router.push('/dashboard')
  const push = vi.spyOn(router, 'push')
  wrapper = mount(ConsoleNavigationSearch, {
    // Reka only clicks a highlighted item that is connected to the document.
    attachTo: document.body,
    global: {
      plugins: [pinia, router, createI18n({ legacy: false, locale: 'en', missingWarn: false, fallbackWarn: false, messages: { en: {} } })],
      // Nuxt UI's UModal is the SFC Modal.vue; stubs match the component name.
      stubs: { Modal: ModalStub }
    }
  })
  // The trigger is the sidebar's UDashboardSearchButton; the palette also opens with Ctrl/⌘ K.
  await pressCtrlK()
  return { wrapper, push }
}

async function pressCtrlK() {
  const wasOpen = !!wrapper?.find('[data-test="palette"]').exists()
  document.dispatchEvent(new KeyboardEvent('keydown', { key: 'k', ctrlKey: true, bubbles: true }))
  // UDashboardSearch loads its modal and command palette lazily.
  await vi.waitFor(() => {
    expect(wrapper!.find('[data-test="palette"]').exists()).toBe(!wasOpen)
    if (!wasOpen) expect(wrapper!.find('input').exists()).toBe(true)
  })
  await flushPromises()
}

async function search(term: string) {
  await wrapper!.get('input').setValue(term)
  await flushPromises()
}

afterEach(() => {
  wrapper?.unmount()
  wrapper = undefined
  vi.restoreAllMocks()
  localStorage.clear()
  document.documentElement.classList.remove('dark')
})

describe('console navigation search', () => {
  // The palette lists the sidebar's pages with the sidebar's labels (nav.*).
  it('limits secondary administrators to anonymous usage and personal navigation', async () => {
    const { wrapper, push } = await render('admin', false, 2)
    await search('/admin/usage')
    await vi.waitFor(() => expect(wrapper.findAll('.console-search-result')).toHaveLength(1))
    expect(wrapper.text()).toContain('nav.usage')
    await search('')
    expect(wrapper.text()).not.toContain('nav.users')
    expect(wrapper.text()).not.toContain('nav.accounts')
    expect(wrapper.text()).not.toContain('nav.settings')
    await search('/admin/accounts')
    await vi.waitFor(() => expect(wrapper.findAll('.console-search-result')).toHaveLength(0))
    await wrapper.get('input').trigger('keydown.enter')
    expect(push).not.toHaveBeenCalled()
  })

  it('matches the sidebar by hiding users and retaining groups in simple admin mode', async () => {
    const { wrapper } = await render('admin', true)
    expect(wrapper.text()).not.toContain('nav.users')
    expect(wrapper.text()).toContain('nav.groups')
  })

  it('reaches every page of a group, also the sub-pages behind an area sub-nav', async () => {
    const { wrapper, push } = await render('admin')
    // Switch the gated areas on so their pages are listed.
    const app = useAppStore()
    app.cachedPublicSettings = { payment_enabled: true, channel_monitor_enabled: true, affiliate_enabled: true, risk_control_enabled: true } as NonNullable<typeof app.cachedPublicSettings>
    useAdminSettingsStore().paymentEnabled = true
    for (const path of ['/admin/orders/plans', '/admin/affiliates/transfers', '/admin/mihomo', '/admin/serverless', '/admin/security-penalties', '/keys']) {
      await search(path)
      await vi.waitFor(() => expect(wrapper.findAll('.console-search-result').length, path).toBeGreaterThan(0))
    }
    await search('/admin/orders/plans')
    await vi.waitFor(() => expect(wrapper.findAll('.console-search-result')).toHaveLength(1))
    await wrapper.get('input').trigger('keydown.enter')
    await flushPromises()
    expect(push).toHaveBeenCalledWith('/admin/orders/plans')
  })

  it('does not expose admin or disabled feature destinations to a user', async () => {
    const { wrapper } = await render('user')
    expect(wrapper.text()).not.toContain('nav.accounts')
    expect(wrapper.text()).not.toContain('nav.buySubscription')
    expect(wrapper.text()).not.toContain('nav.channelStatus')
    expect(wrapper.text()).toContain('nav.apiKeys')
  })

  it('filters admin destinations and navigates on Enter without triggering a business action', async () => {
    const { wrapper, push } = await render('admin')
    await search('/admin/accounts')
    expect(wrapper.findAll('.console-search-result')).toHaveLength(1)
    await wrapper.get('input').trigger('keydown.enter')
    await flushPromises()
    expect(push).toHaveBeenCalledWith('/admin/accounts')
    expect(wrapper.find('input').exists()).toBe(false)
  })

  it('keeps an empty query result open and ignores Enter', async () => {
    const { wrapper, push } = await render('user')
    await search('missing-destination')
    await vi.waitFor(() => expect(wrapper.findAll('[data-slot="item"]')).toHaveLength(0))
    await wrapper.get('input').trigger('keydown.enter')
    expect(wrapper.find('input').exists()).toBe(true)
    expect(push).not.toHaveBeenCalled()
  })

  it('opens and closes with Ctrl+K (⌘K on macOS), also while typing', async () => {
    const { wrapper } = await render('user')
    expect(wrapper.find('[data-test="palette"]').exists()).toBe(true)
    await pressCtrlK()
    expect(wrapper.find('[data-test="palette"]').exists()).toBe(false)
    await pressCtrlK()
    expect(wrapper.find('[data-test="palette"]').exists()).toBe(true)
    expect(wrapper.find('input').exists()).toBe(true)
  })

  it('switches the theme from the palette and closes it', async () => {
    const { wrapper, push } = await render('user')
    const dark = wrapper.findAll('[data-slot="item"]').find((item) => item.text().includes('admin.settings.siteTheme.modes.dark'))
    expect(dark).toBeTruthy()
    await dark!.trigger('click')
    await flushPromises()
    expect(document.documentElement.classList.contains('dark')).toBe(true)
    expect(localStorage.getItem('theme')).toBe('dark')
    expect(wrapper.find('[data-test="palette"]').exists()).toBe(false)
    expect(push).not.toHaveBeenCalled()
  })
})
