import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createI18n } from 'vue-i18n'
import { createMemoryHistory, createRouter } from 'vue-router'
import { useAppStore, useAuthStore } from '@/stores'
import { useAdminSettingsStore } from '@/stores/adminSettings'
import AppLayout from '../AppLayout.vue'

vi.mock('@/composables/useOnboardingTour', () => ({
  useOnboardingTour: () => ({ replayTour: vi.fn() })
}))

vi.mock('@/composables/useBatchImageAccess', () => ({
  useBatchImageAccess: () => ({ canUseBatchImage: false, refreshBatchImageAccess: vi.fn() })
}))

const userPaths = ['/dashboard', '/keys', '/usage', '/monitor', '/available-channels', '/purchase',
  '/orders', '/subscriptions', '/profile', '/redeem', '/affiliate', '/ip-allowlist',
  '/payment/qrcode', '/payment/result', '/payment/stripe', '/payment/airwallex']
const adminPaths = ['/admin/dashboard', '/admin/accounts', '/admin/groups', '/admin/users', '/admin/keys', '/admin/usage']
const excludedPaths = ['/login', '/keys/other', '/usage-extra', '/infinite-canvas']
let wrapper: VueWrapper | undefined

async function renderShell(path: string, role: 'user' | 'admin' = 'user') {
  const pinia = createPinia()
  setActivePinia(pinia)
  const app = useAppStore()
  app.siteName = 'SIGNAL'
  app.publicSettingsLoaded = true
  app.docUrl = '/docs'
  const auth = useAuthStore()
  auth.user = {
    id: 1,
    role,
    username: 'Local Test',
    email: 'test@example.test',
    balance: 20,
    frozen_balance: 3
  } as NonNullable<typeof auth.user>
  useAdminSettingsStore().fetch = vi.fn().mockResolvedValue(undefined)

  const router = createRouter({
    history: createMemoryHistory(),
    routes: [...new Set([...userPaths, ...adminPaths, ...excludedPaths, '/:pathMatch(.*)*'])].map((routePath) => ({
      path: routePath,
      component: { template: '<div />' },
      meta: { title: routePath === '/keys' ? 'API Keys' : routePath }
    }))
  })
  await router.push(path)
  await router.isReady()

  wrapper = mount(AppLayout, {
    slots: { default: '<h1>Content title</h1><button id="page-action">Page action</button>' },
    global: {
      plugins: [pinia, router, createI18n({ legacy: false, locale: 'en', missingWarn: false, fallbackWarn: false, messages: { en: {} } })],
      stubs: { AnnouncementBell: true, SubscriptionProgressMini: true, LocaleSwitcher: true, VersionBadge: true }
    }
  })
  return { router, app }
}

beforeEach(() => {
  localStorage.clear()
  document.documentElement.classList.remove('dark')
})

afterEach(() => {
  wrapper?.unmount()
  wrapper = undefined
  vi.restoreAllMocks()
})

describe('SIGNAL shell route isolation', () => {
  it.each([...userPaths, '/keys?search=test#active', '/keys/'])('opts in %s and leaves one content heading', async (path) => {
    await renderShell(path)
    expect(wrapper!.classes()).toContain('console-signal')
    expect(wrapper!.find('.bg-mesh-gradient').exists()).toBe(false)
    expect(wrapper!.find('header h1').exists()).toBe(false)
    expect(wrapper!.findAll('h1')).toHaveLength(1)
    expect(wrapper!.get('.signal-breadcrumb a').attributes('href')).toBe('/dashboard')
    expect(wrapper!.get('#page-action').text()).toBe('Page action')
  })

  it.each(excludedPaths)('keeps the original shell on %s', async (path) => {
    await renderShell(path)
    expect(wrapper!.classes()).not.toContain('console-signal')
    expect(wrapper!.find('.signal-header').exists()).toBe(false)
    expect(wrapper!.find('.signal-sidebar').exists()).toBe(false)
    expect(wrapper!.find('.bg-mesh-gradient').exists()).toBe(true)
    expect(wrapper!.find('header h1').exists()).toBe(true)
  })

  it.each(adminPaths)('applies the admin workspace on %s with its existing header title', async (path) => {
    await renderShell(path, 'admin')
    expect(wrapper!.classes()).toContain('signal-admin')
    expect(wrapper!.get('header h1').text()).toBe(path)
    expect(wrapper!.get('.signal-breadcrumb a').attributes('href')).toBe('/admin/dashboard')
  })

  it('switches between admin and personal workspace without leaving theme state behind', async () => {
    const { router } = await renderShell('/keys', 'admin')
    expect(wrapper!.classes()).toContain('console-signal')
    expect(wrapper!.find('.sidebar a[href="/admin/dashboard"]').exists()).toBe(true)
    await router.push('/admin/dashboard')
    expect(wrapper!.classes()).toContain('console-signal')
    expect(wrapper!.classes()).toContain('signal-admin')
    expect(document.body.classList.contains('console-signal')).toBe(false)
    await router.push('/usage')
    expect(wrapper!.classes()).toContain('console-signal')
    expect(wrapper!.classes()).not.toContain('signal-admin')
  })

  it('keeps collapse, mobile navigation, theme and balance semantics', async () => {
    const { app } = await renderShell('/keys')
    expect(wrapper!.get('.header-balance-value').text()).toBe('$20.00')
    expect(wrapper!.get('.header-balance-frozen').text()).toContain('$3.00')
    expect(wrapper!.get('.header-balance-detail').text()).toContain('$23.00')
    expect(wrapper!.get('.header-balance').attributes('tabindex')).toBe('0')

    await wrapper!.get('.sidebar-footer button:last-child').trigger('click')
    expect(app.sidebarCollapsed).toBe(true)
    expect(wrapper!.get('.signal-frame').classes()).toContain('lg:ml-[72px]')
    await wrapper!.get('.header-location > button').trigger('click')
    expect(app.mobileOpen).toBe(true)
    const wasDark = document.documentElement.classList.contains('dark')
    await wrapper!.get('[data-test="theme-toggle"]').trigger('click')
    expect(document.documentElement.classList.contains('dark')).toBe(!wasDark)
    expect(wrapper!.find('.sidebar a[href="/keys"]').exists()).toBe(true)
    expect(wrapper!.find('.sidebar a[href="/usage"]').exists()).toBe(true)
  })
})
