import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, h, nextTick } from 'vue'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createI18n } from 'vue-i18n'
import { createMemoryHistory, createRouter } from 'vue-router'
import UApp from '@nuxt/ui/components/App.vue'
import { authAPI } from '@/api'
import { FLASH_CYCLE_MS } from '@/composables/useBalanceSyncIndicator'
import { useAppStore, useAuthStore, useSubscriptionStore } from '@/stores'
import { useAdminSettingsStore } from '@/stores/adminSettings'
import AppLayout from '../AppLayout.vue'

vi.mock('@/composables/useOnboardingTour', () => ({
  useOnboardingTour: () => ({ replayTour: vi.fn() })
}))

vi.mock('@/composables/useBatchImageAccess', () => ({
  useBatchImageAccess: () => ({ canUseBatchImage: false, refreshBatchImageAccess: vi.fn() })
}))

let wrapper: VueWrapper | undefined

const USER = {
  id: 1,
  role: 'user' as const,
  username: 'Local Test',
  email: 'test@example.test',
  balance: 20,
  frozen_balance: 3
}

async function renderShell() {
  const pinia = createPinia()
  setActivePinia(pinia)
  const app = useAppStore()
  app.siteName = 'SIGNAL'
  app.publicSettingsLoaded = true
  app.cachedPublicSettings = {} as NonNullable<typeof app.cachedPublicSettings>
  const auth = useAuthStore()
  auth.token = 'test-token'
  auth.user = { ...USER } as NonNullable<typeof auth.user>
  useAdminSettingsStore().fetch = vi.fn().mockResolvedValue(undefined)
  useSubscriptionStore().activeSubscriptions = []

  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/dashboard', component: { template: '<div />' }, meta: { title: 'Dashboard' } },
      // The sidebar links to many pages; they only need to resolve.
      { path: '/:pathMatch(.*)*', component: { template: '<div />' } }
    ]
  })
  await router.push('/dashboard')
  await router.isReady()

  const Harness = defineComponent({ components: { AppLayout }, template: '<AppLayout><div /></AppLayout>' })
  wrapper = mount({ render: () => h(UApp, null, { default: () => h(Harness) }) }, {
    attachTo: document.body,
    global: {
      plugins: [pinia, router, createI18n({ legacy: false, locale: 'en', missingWarn: false, fallbackWarn: false, messages: { en: {} } })],
      stubs: { AnnouncementBell: true, SubscriptionProgressMini: true, VersionBadge: true }
    }
  })
  await flushPromises()
  return auth
}

const chip = () => wrapper!.get('.header-balance')
const value = () => wrapper!.get('.header-balance-value').text()

/** A /auth/me call the test settles by hand. */
function deferredMe() {
  let resolve!: (data: unknown) => void
  let reject!: (error: unknown) => void
  const promise = new Promise((res, rej) => {
    resolve = res
    reject = rej
  })
  vi.spyOn(authAPI, 'getCurrentUser').mockReturnValue(promise as never)
  return {
    answer: (balance: number) => resolve({ data: { ...USER, balance } }),
    fail: (error: unknown) => reject(error)
  }
}

describe('balance chip in the header: refresh light', () => {
  beforeEach(() => {
    localStorage.clear()
    // Only the timers the chip uses are faked; flushPromises keeps working.
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout', 'Date'] })
  })

  afterEach(() => {
    wrapper?.unmount()
    wrapper = undefined
    useAuthStore().$dispose()
    vi.useRealTimers()
    vi.restoreAllMocks()
  })

  it('is lit green (breathing) while the number is current', async () => {
    await renderShell()

    expect(chip().attributes('data-sync')).toBe('fresh')
    expect(chip().attributes('aria-busy')).toBe('false')
    // The stylesheet reads the flash length from here, so it always matches the hold in the script.
    expect(chip().attributes('style')).toContain(`--balance-flash: ${FLASH_CYCLE_MS}ms`)
  })

  it('flashes from the moment the request is sent, and the new balance shows without reloading the page', async () => {
    const auth = await renderShell()
    expect(value()).toBe('$20.00')
    const me = deferredMe()

    const pending = auth.refreshUser({ force: true })
    await nextTick()
    expect(chip().attributes('data-sync')).toBe('loading')
    expect(chip().attributes('aria-busy')).toBe('true')

    me.answer(42.5)
    await pending
    await flushPromises()
    // The number has already changed; the ring finishes its flash cycle before turning green.
    expect(value()).toBe('$42.50')
    expect(chip().attributes('data-sync')).toBe('loading')

    await vi.advanceTimersByTimeAsync(FLASH_CYCLE_MS)
    await flushPromises()
    expect(chip().attributes('data-sync')).toBe('fresh')
    expect(chip().attributes('aria-busy')).toBe('false')
  })

  it('drops the ring when the refresh fails, so nothing claims the old number is live', async () => {
    const auth = await renderShell()
    const me = deferredMe()

    const pending = auth.refreshUser({ force: true }).catch(() => undefined)
    await nextTick()
    expect(chip().attributes('data-sync')).toBe('loading')

    me.fail(new Error('503'))
    await pending
    await vi.advanceTimersByTimeAsync(FLASH_CYCLE_MS)
    await flushPromises()

    expect(chip().attributes('data-sync')).toBe('failed')
    expect(value()).toBe('$20.00')
  })

  it('does not flash for an answer that is reused instead of fetched', async () => {
    const auth = await renderShell()
    const me = deferredMe()
    const first = auth.refreshUser({ force: true })
    me.answer(30)
    await first
    await vi.advanceTimersByTimeAsync(FLASH_CYCLE_MS)
    await flushPromises()
    expect(chip().attributes('data-sync')).toBe('fresh')

    // Inside the store's short reuse window: no request, so no flash.
    await auth.refreshUser()
    await nextTick()
    expect(chip().attributes('data-sync')).toBe('fresh')
  })

  it('keeps the sync ring pseudo-element aligned with the chip continuous-curvature corner-shape', async () => {
    const [{ readFileSync }, { resolve }] = await Promise.all([
      import('node:fs'),
      import('node:path'),
    ])
    const headerSrc = readFileSync(resolve(__dirname, '../AppHeader.vue'), 'utf8')
    const styleSrc = readFileSync(resolve(__dirname, '../../../style.css'), 'utf8')
    const afterRule = headerSrc.match(/\.console-navbar\s+\.header-balance::after\s*\{[^}]*\}/)?.[0] ?? ''
    expect(afterRule).toContain('border-radius: inherit;')
    expect(afterRule).toContain('corner-shape: inherit;')
    expect(styleSrc).toContain('.header-balance::after')
    expect(styleSrc).toContain('.header-balance-detail')
  })
})
