import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { createI18n } from 'vue-i18n'
import { createMemoryHistory, createRouter } from 'vue-router'
import { mount } from '@vue/test-utils'
import { baseCompile } from '@intlify/message-compiler'
import { useAuthStore } from '@/stores/auth'
import { useAppStore } from '@/stores/app'
import { useAdminSettingsStore } from '@/stores/adminSettings'
import { routes } from '@/router/records'
import en from '@/i18n/locales/en'
import zh from '@/i18n/locales/zh'
import { applyFeatureFlags, getOperationsPages, operationsAreas, useOperationsNavigation } from '@/components/admin/operations/opsNavigation'
import OperationsAreaNav from '../OperationsAreaNav.vue'

beforeEach(() => {
  localStorage.clear()
  setActivePinia(createPinia())
  const auth = useAuthStore()
  auth.user = { id: 1, role: 'admin' } as NonNullable<typeof auth.user>
})

describe('shared operations navigation', () => {
  it('keeps exactly five workspaces and all existing page URLs and route names', () => {
    expect(operationsAreas.map(area => area.id)).toEqual(['overview', 'accounts', 'channels', 'diagnostics', 'network'])
    for (const page of getOperationsPages(operationsAreas)) {
      const record = routes.find(route => route.name === page.name)
      expect(record?.path, page.name).toBe(page.path)
      expect(record?.meta?.titleKey).toBe(page.labelKey)
    }
    expect(getOperationsPages(operationsAreas)).toHaveLength(24)
  })

  it('keeps old bookmarked routes reachable with their query and hash intact', async () => {
    const router = createRouter({ history: createMemoryHistory(), routes: routes.map(record => ({ ...record, component: { template: '<div />' } })) })
    for (const page of getOperationsPages(operationsAreas)) {
      await router.push({ path: page.path, query: { account_id: '9', request_id: 'bookmark' }, hash: '#saved' })
      expect(router.currentRoute.value.name).toBe(page.name)
      expect(router.currentRoute.value.query).toEqual({ account_id: '9', request_id: 'bookmark' })
      expect(router.currentRoute.value.hash).toBe('#saved')
    }
  })

  it('resolves every navigation and area label in both languages', () => {
    const i18n = createI18n({ legacy: false, locale: 'en', messages: { en, zh }, messageCompiler: message => new Function(`return (${baseCompile(message as string, { mode: 'arrow' }).code})`)() })
    for (const locale of ['en', 'zh']) {
      for (const key of [...getOperationsPages(operationsAreas).map(page => page.labelKey), ...operationsAreas.flatMap(area => [area.labelKey, area.descriptionKey])]) {
        expect(i18n.global.te(key, locale), key).toBe(true)
      }
    }
  })

  it('retains the sidebar opt-out filter including nested children', () => {
    const input = [{ path: 'parent', children: [{ path: 'off', featureFlag: () => false }, { path: 'pending', featureFlag: () => undefined }] }]
    expect(applyFeatureFlags(input)[0].children?.map(item => item.path)).toEqual(['pending'])
    expect(input[0].children).toHaveLength(2)
  })

  it('reactively hides disabled features everywhere without hiding other diagnostic pages', () => {
    const navigation = useOperationsNavigation()
    const paths = () => getOperationsPages(navigation.value).map(page => page.path)
    expect(paths()).toContain('/admin/ops')
    useAppStore().cachedPublicSettings = { channel_monitor_enabled: false } as NonNullable<ReturnType<typeof useAppStore>['cachedPublicSettings']>
    useAdminSettingsStore().opsMonitoringEnabled = false
    expect(paths()).not.toContain('/admin/ops')
    expect(paths()).not.toContain('/admin/channels/monitor')
    expect(paths()).toContain('/admin/request-captures/settings')
    useAdminSettingsStore().opsMonitoringEnabled = true
    expect(paths()).toContain('/admin/ops')
  })

  it('preserves the existing simple-mode visibility of channel settings', () => {
    const auth = useAuthStore()
    vi.spyOn(auth, 'isSimpleMode', 'get').mockReturnValue(true)
    const paths = getOperationsPages(useOperationsNavigation().value).map(page => page.path)
    expect(paths).not.toContain('/admin/channels/pricing')
    expect(paths).not.toContain('/admin/channels/monitor')
    expect(paths).toContain('/admin/channel-operations')
    vi.restoreAllMocks()
  })

  it.each([{ id: 2, role: 'admin' }, { id: 3, role: 'user' }])('never exposes operations destinations to $role id=$id', user => {
    useAuthStore().user = user as NonNullable<ReturnType<typeof useAuthStore>['user']>
    expect(useOperationsNavigation().value).toEqual([])
  })
})

describe('area tabs', () => {
  async function render(path: string) {
    const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:pathMatch(.*)*', component: { template: '<div />' } }] })
    await router.push(path)
    const wrapper = mount(OperationsAreaNav, { global: { plugins: [router, createI18n({ legacy: false, locale: 'en', messages: { en, zh }, messageCompiler: message => new Function(`return (${baseCompile(message as string, { mode: 'arrow' }).code})`)() })] } })
    return { wrapper, router }
  }

  it('highlights the parent tab on a nested page without replacing its sub-navigation', async () => {
    const { wrapper, router } = await render('/admin/account-ops/channels?account_id=9')
    expect(wrapper.get('[aria-current="page"]').text()).toBe('Account notifications')
    expect(wrapper.findAll('.operations-area-nav a').map(node => node.text())).toContain('Upstream assets & sites')
    await wrapper.get('a[href="/admin/credential-guard"]').trigger('click')
    await vi.waitFor(() => expect(router.currentRoute.value.path).toBe('/admin/credential-guard'))
    expect(wrapper.get('[aria-current="page"]').text()).toBe('Credential inspection')
    wrapper.unmount()
  })

  it('hides the disabled live operations tab but keeps capture active', async () => {
    useAdminSettingsStore().opsMonitoringEnabled = false
    const { wrapper } = await render('/admin/request-captures/settings')
    expect(wrapper.find('a[href="/admin/ops"]').exists()).toBe(false)
    expect(wrapper.get('[aria-current="page"]').text()).toBe('Request capture')
    wrapper.unmount()
  })

  it.each([
    ['/admin/upstream-monitor', 'accounts', '/admin/upstream-monitor'],
    ['/admin/account-ops/channels', 'accounts', '/admin/account-ops'],
    ['/admin/channels/pricing', 'channels', '/admin/channels/pricing'],
    ['/admin/channels/monitor', 'channels', '/admin/channels/monitor'],
    ['/admin/model-comparison', 'channels', '/admin/model-comparison'],
    ['/admin/ops', 'diagnostics', '/admin/ops'],
    ['/admin/mihomo/nodes', 'network', '/admin/mihomo'],
    ['/admin/mihomo/dynamic', 'network', '/admin/mihomo'],
    ['/admin/mihomo/kernel', 'network', '/admin/mihomo'],
    ['/admin/serverless', 'network', '/admin/serverless'],
  ])('highlights %s in its expected workspace', async (path, area, active) => {
    const { wrapper } = await render(path)
    expect(wrapper.get('nav').attributes('aria-label')).toBe(en.opsCenter.areas[area as keyof typeof en.opsCenter.areas])
    expect(wrapper.get('[aria-current="page"]').attributes('href')).toBe(active)
    wrapper.unmount()
  })

  it('reveals the current tab when its area overflows on a phone', async () => {
    const scroll = vi.spyOn(Element.prototype, 'scrollIntoView')
    const { wrapper } = await render('/admin/excel-grants')
    await vi.waitFor(() => expect(scroll).toHaveBeenCalledWith({ block: 'nearest', inline: 'nearest' }))
    expect(wrapper.get('[aria-current="page"]').text()).toBe('Excel authorization')
    wrapper.unmount()
    scroll.mockRestore()
  })

  it('does not add tabs to overview or unrelated global pages', async () => {
    for (const path of ['/admin/ops-center', '/admin/accounts', '/traffic']) {
      const { wrapper } = await render(path)
      expect(wrapper.find('nav').exists()).toBe(false)
      wrapper.unmount()
    }
  })
})
