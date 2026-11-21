import { beforeAll, beforeEach, describe, expect, it, vi } from 'vitest'

type NavigationGuard = (
  to: Record<string, any>,
  from: Record<string, any>,
  next: ReturnType<typeof vi.fn>
) => Promise<void>

const routerHarness = vi.hoisted(() => ({
  guard: null as NavigationGuard | null,
}))

const authStore = vi.hoisted(() => ({
  checkAuth: vi.fn(),
  isAuthenticated: true,
  isAdmin: false,
  isAdminObserver: false,
  isSimpleMode: false,
  hasPendingAuthSession: false,
}))

const appStore = vi.hoisted(() => ({
  siteName: 'Sub2API',
  backendModeEnabled: false,
  publicSettingsLoaded: false,
  // True while the store still runs on the server-injected window.__APP_CONFIG__
  // snapshot and has not confirmed it against /api/v1/settings/public.
  publicSettingsNeedsRevalidate: false,
  cachedPublicSettings: null as null | {
    payment_enabled?: boolean
    risk_control_enabled?: boolean
    crypto_enabled?: boolean
    custom_menu_items?: []
  },
  fetchPublicSettings: vi.fn(),
}))

vi.mock('vue-router', () => ({
  createWebHistory: vi.fn(() => ({})),
  createRouter: vi.fn(() => ({
    beforeEach: vi.fn((guard: NavigationGuard) => {
      routerHarness.guard = guard
    }),
    afterEach: vi.fn(),
    onError: vi.fn(),
  })),
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => authStore,
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => appStore,
}))

vi.mock('@/stores/adminSettings', () => ({
  useAdminSettingsStore: () => ({ customMenuItems: [] }),
}))

vi.mock('@/stores/adminCompliance', () => ({
  useAdminComplianceStore: () => ({
    initialized: true,
    fetchStatus: vi.fn(),
    requireAcknowledgement: vi.fn(),
  }),
}))

vi.mock('@/composables/useNavigationLoading', () => ({
  useNavigationLoadingState: () => ({
    startNavigation: vi.fn(),
    endNavigation: vi.fn(),
    isLoading: { value: false },
  }),
}))

vi.mock('@/composables/useRoutePrefetch', () => ({
  useRoutePrefetch: () => ({
    triggerPrefetch: vi.fn(),
    cancelPendingPrefetch: vi.fn(),
    resetPrefetchState: vi.fn(),
  }),
}))

function createDeferred<T>() {
  let resolve!: (value: T | PromiseLike<T>) => void
  const promise = new Promise<T>((resolvePromise) => {
    resolve = resolvePromise
  })
  return { promise, resolve }
}

function runGuard(meta: Record<string, unknown>, path: string) {
  if (!routerHarness.guard) {
    throw new Error('router guard was not registered')
  }

  const next = vi.fn()
  const navigation = routerHarness.guard(
    {
      path,
      fullPath: path,
      name: 'FeatureRoute',
      params: {},
      meta: { requiresAuth: true, ...meta },
    },
    {},
    next
  )
  return { navigation, next }
}

describe('feature route guard', () => {
  beforeAll(async () => {
    await import('@/router')
  })

  beforeEach(() => {
    authStore.isAuthenticated = true
    authStore.isAdmin = false
    authStore.isAdminObserver = false
    authStore.isSimpleMode = false
    appStore.publicSettingsLoaded = false
    appStore.publicSettingsNeedsRevalidate = false
    appStore.cachedPublicSettings = null
    appStore.fetchPublicSettings.mockReset()
  })

  it.each(['/admin/users', '/admin/accounts', '/admin/settings', '/admin/orders', '/admin/dashboard'])('redirects observer away from %s to usage', async (path) => {
    authStore.isAdmin = true
    authStore.isAdminObserver = true
    const { navigation, next } = runGuard({ requiresAdmin: true }, path)
    await navigation
    expect(next).toHaveBeenCalledWith('/admin/usage')
  })

  it('keeps usage reachable for an observer without the owner compliance workflow', async () => {
    authStore.isAdmin = true
    authStore.isAdminObserver = true
    const { navigation, next } = runGuard({ requiresAdmin: true }, '/admin/usage')
    await navigation
    expect(next).toHaveBeenCalledWith()
  })

  it('waits for the first public-settings request before deciding payment access', async () => {
    const deferred = createDeferred<{ payment_enabled: boolean }>()
    appStore.fetchPublicSettings.mockImplementation(async () => {
      const settings = await deferred.promise
      appStore.cachedPublicSettings = settings
      appStore.publicSettingsLoaded = true
      return settings
    })

    const { navigation, next } = runGuard({ requiresPayment: true }, '/purchase')

    await vi.waitFor(() => expect(appStore.fetchPublicSettings).toHaveBeenCalledTimes(1))
    expect(next).not.toHaveBeenCalled()

    deferred.resolve({ payment_enabled: true })
    await navigation
    expect(next).toHaveBeenCalledOnce()
    expect(next).toHaveBeenCalledWith()
  })

  it.each([
    ['payment', { requiresPayment: true }, '/purchase'],
    ['risk control', { requiresRiskControl: true }, '/admin/risk-control'],
  ])('does not treat a failed %s settings load as explicitly disabled', async (_name, meta, path) => {
    authStore.isAdmin = meta.requiresRiskControl === true
    appStore.fetchPublicSettings.mockResolvedValue(null)

    const { navigation, next } = runGuard(meta, path)
    await navigation

    expect(appStore.publicSettingsLoaded).toBe(false)
    expect(next).toHaveBeenCalledOnce()
    expect(next).toHaveBeenCalledWith()
  })

  it.each([
    ['payment', { requiresPayment: true }, { payment_enabled: false }, '/dashboard'],
    [
      'risk control',
      { requiresRiskControl: true },
      { risk_control_enabled: false },
      '/admin/settings',
    ],
  ])('redirects when loaded settings explicitly disable %s', async (_name, meta, settings, target) => {
    authStore.isAdmin = meta.requiresRiskControl === true
    appStore.cachedPublicSettings = settings
    appStore.publicSettingsLoaded = true

    const { navigation, next } = runGuard(meta, '/feature')
    await navigation

    expect(appStore.fetchPublicSettings).not.toHaveBeenCalled()
    expect(next).toHaveBeenCalledOnce()
    expect(next).toHaveBeenCalledWith(target)
  })

  // Regression: an instance rendered crypto_enabled=false into
  // window.__APP_CONFIG__ while another instance had it enabled. The injected
  // snapshot was treated as authoritative, so /deposit was redirected away for
  // good. The guard must wait for the API before enforcing requiresCrypto.
  it('revalidates the injected snapshot instead of trusting a stale crypto_enabled', async () => {
    appStore.publicSettingsLoaded = true
    appStore.publicSettingsNeedsRevalidate = true
    appStore.cachedPublicSettings = { crypto_enabled: false }

    appStore.fetchPublicSettings.mockImplementation(async () => {
      appStore.publicSettingsNeedsRevalidate = false
      appStore.cachedPublicSettings = { crypto_enabled: true }
      return appStore.cachedPublicSettings
    })

    const { navigation, next } = runGuard({ requiresCrypto: true }, '/deposit')
    await navigation

    expect(appStore.fetchPublicSettings).toHaveBeenCalledTimes(1)
    expect(next).toHaveBeenCalledOnce()
    // next() with no argument = navigation allowed.
    expect(next).toHaveBeenCalledWith()
  })

  it('redirects /deposit once revalidation confirms crypto is disabled', async () => {
    appStore.publicSettingsLoaded = true
    appStore.publicSettingsNeedsRevalidate = true
    appStore.cachedPublicSettings = { crypto_enabled: false }

    appStore.fetchPublicSettings.mockImplementation(async () => {
      appStore.publicSettingsNeedsRevalidate = false
      appStore.cachedPublicSettings = { crypto_enabled: false }
      return appStore.cachedPublicSettings
    })

    const { navigation, next } = runGuard({ requiresCrypto: true }, '/deposit')
    await navigation

    expect(appStore.fetchPublicSettings).toHaveBeenCalledTimes(1)
    expect(next).toHaveBeenCalledWith('/dashboard')
  })

  it('falls back to the injected value when revalidation cannot complete', async () => {
    appStore.publicSettingsLoaded = true
    appStore.publicSettingsNeedsRevalidate = true
    appStore.cachedPublicSettings = { crypto_enabled: false }
    // Timeout degradation: fetchPublicSettings resolves with the injected value
    // without confirming it, so the guard keeps the conservative crypto rule.
    appStore.fetchPublicSettings.mockImplementation(async () => appStore.cachedPublicSettings)

    const { navigation, next } = runGuard({ requiresCrypto: true }, '/deposit')
    await navigation

    expect(appStore.fetchPublicSettings).toHaveBeenCalledTimes(1)
    expect(next).toHaveBeenCalledWith('/dashboard')
  })

  it('allows /deposit without refetching once settings were revalidated', async () => {
    appStore.publicSettingsLoaded = true
    appStore.publicSettingsNeedsRevalidate = false
    appStore.cachedPublicSettings = { crypto_enabled: true }

    const { navigation, next } = runGuard({ requiresCrypto: true }, '/deposit')
    await navigation

    expect(appStore.fetchPublicSettings).not.toHaveBeenCalled()
    expect(next).toHaveBeenCalledWith()
  })
})
