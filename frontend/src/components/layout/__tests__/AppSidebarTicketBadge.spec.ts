import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, h } from 'vue'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createI18n } from 'vue-i18n'
import { createMemoryHistory, createRouter } from 'vue-router'
import UApp from '@nuxt/ui/components/App.vue'
import AppLayout from '../AppLayout.vue'
import { useAppStore, useAuthStore, useSubscriptionStore } from '@/stores'
import { useAdminSettingsStore } from '@/stores/adminSettings'
import { useTicketNotificationStore } from '@/stores/ticketNotifications'

const stats = vi.hoisted(() => ({ user: vi.fn(), admin: vi.fn() }))
const apiFactory = vi.hoisted(() => vi.fn())

vi.mock('@/api/tickets', () => ({ ticketsAPI: apiFactory }))
vi.mock('@/composables/useOnboardingTour', () => ({
  useOnboardingTour: () => ({ replayTour: vi.fn() })
}))
vi.mock('@/composables/useBatchImageAccess', () => ({
  useBatchImageAccess: () => ({ canUseBatchImage: false, refreshBatchImageAccess: vi.fn() })
}))

const paths = [
  '/admin/dashboard', '/admin/tickets', '/tickets', '/keys', '/dashboard',
  '/admin/tickets/42', '/:pathMatch(.*)*'
]

let wrapper: VueWrapper | undefined

async function renderAdminSidebar(path = '/admin/tickets') {
  const pinia = createPinia()
  setActivePinia(pinia)

  const app = useAppStore()
  app.siteName = 'SIGNAL'
  app.publicSettingsLoaded = true
  app.cachedPublicSettings = {} as NonNullable<typeof app.cachedPublicSettings>

  const auth = useAuthStore()
  auth.user = {
    id: 1,
    role: 'admin',
    username: 'Admin',
    email: 'admin@example.test',
    balance: 0,
    frozen_balance: 0
  } as NonNullable<typeof auth.user>

  useAdminSettingsStore().fetch = vi.fn().mockResolvedValue(undefined)
  useSubscriptionStore().activeSubscriptions = [] as never

  const router = createRouter({
    history: createMemoryHistory(),
    routes: [...new Set(paths)].map((path) => ({ path, component: { template: '<div />' } }))
  })
  await router.push(path)
  await router.isReady()

  const Harness = defineComponent({
    components: { AppLayout },
    template: '<AppLayout><div data-test="page" /></AppLayout>'
  })
  wrapper = mount({ render: () => h(UApp, null, { default: () => h(Harness) }) }, {
    attachTo: document.body,
    global: {
      plugins: [
        pinia,
        router,
        createI18n({ legacy: false, locale: 'en', missingWarn: false, fallbackWarn: false, messages: { en: {} } })
      ],
      stubs: { AnnouncementBell: true, SubscriptionProgressMini: true, VersionBadge: true }
    }
  })
  await flushPromises()
  return useTicketNotificationStore()
}

const linkFor = (href: string) =>
  wrapper!.findAll('a').find((link) => link.attributes('href') === href)

beforeEach(() => {
  localStorage.clear()
  vi.resetAllMocks()
  vi.spyOn(console, 'error').mockImplementation(() => {})
  apiFactory.mockImplementation((admin: boolean) => ({ stats: admin ? stats.admin : stats.user }))
})

afterEach(() => {
  wrapper?.unmount()
  wrapper = undefined
  vi.restoreAllMocks()
})

describe('sidebar ticket badge', () => {
  it('shows the pending message count next to the ticket entries', async () => {
    stats.user.mockResolvedValue({ unread: 2, total: 6 })
    stats.admin.mockResolvedValue({ unread: 4, total: 9 })
    const store = await renderAdminSidebar()
    await store.fetchAll(true, true)
    await flushPromises()

    expect(linkFor('/admin/tickets')?.text()).toContain('4')
    expect(linkFor('/tickets')?.text()).toContain('2')
  })

  it('keeps the entry clean when nothing is pending', async () => {
    stats.user.mockResolvedValue({ unread: 0, total: 6 })
    stats.admin.mockResolvedValue({ unread: 0, total: 9 })
    const store = await renderAdminSidebar()
    await store.fetchAll(true, true)
    await flushPromises()

    const text = linkFor('/admin/tickets')?.text() ?? ''
    expect(text).not.toContain('!')
    expect(text).not.toMatch(/[0-9]/)
  })

  it('shows a warning instead of zero when the count cannot be loaded', async () => {
    stats.user.mockRejectedValue(new Error('offline'))
    stats.admin.mockRejectedValue(new Error('offline'))
    const store = await renderAdminSidebar()
    await store.fetchAll(true, true)
    await flushPromises()

    expect(linkFor('/admin/tickets')?.text()).toContain('!')
    expect(linkFor('/tickets')?.text()).toContain('!')
  })

  it('keeps a nested ticket page highlighted', async () => {
    stats.user.mockResolvedValue({ unread: 0, total: 0 })
    stats.admin.mockResolvedValue({ unread: 0, total: 0 })
    const store = await renderAdminSidebar('/admin/tickets/42')
    await store.fetchAll(true, true)
    await flushPromises()

    const active = wrapper!.findAll('a').filter((link) => link.attributes('aria-current') === 'page')
    expect(active.map((link) => link.attributes('href'))).toContain('/admin/tickets')
  })
})
