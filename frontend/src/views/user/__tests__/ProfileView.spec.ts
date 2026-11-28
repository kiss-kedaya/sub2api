import { flushPromises, mount } from '@vue/test-utils'
import { createMemoryHistory, createRouter } from 'vue-router'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import ProfileView from '@/views/user/ProfileView.vue'

const {
  fetchPublicSettingsMock,
  refreshUserMock,
  authState
} = vi.hoisted(() => ({
  fetchPublicSettingsMock: vi.fn(),
  refreshUserMock: vi.fn(),
  authState: {
    user: null as Record<string, unknown> | null,
    refreshUser: vi.fn()
  }
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => authState
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    fetchPublicSettings: fetchPublicSettingsMock
  })
}))

vi.mock('@/utils/format', () => ({
  formatDate: () => 'April 2026'
}))

vi.mock('vue-i18n', async (importOriginal) => {
  const actual = await importOriginal<typeof import('vue-i18n')>()
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key
    })
  }
})

describe('ProfileView', () => {
  beforeEach(() => {
    refreshUserMock.mockReset()
    fetchPublicSettingsMock.mockReset()
    refreshUserMock.mockResolvedValue(undefined)
    authState.refreshUser = refreshUserMock
    authState.user = {
      id: 1,
      username: 'alice',
      email: 'alice@example.com',
      role: 'user',
      balance: 10,
      concurrency: 2,
      status: 'active',
      allowed_groups: null,
      balance_notify_enabled: true,
      balance_notify_threshold: null,
      balance_notify_extra_emails: [],
      created_at: '2026-04-20T00:00:00Z',
      updated_at: '2026-04-20T00:00:00Z'
    }
    fetchPublicSettingsMock.mockResolvedValue({
      contact_info: '',
      balance_low_notify_enabled: false,
      balance_low_notify_threshold: 0,
      linuxdo_oauth_enabled: true,
      wechat_oauth_enabled: true,
      wechat_oauth_open_enabled: true,
      wechat_oauth_mp_enabled: false,
      oidc_oauth_enabled: true,
      oidc_oauth_provider_name: 'OIDC'
    })
  })

  it('renders the simplified single-column profile shell without separate stat cards', async () => {
    const wrapper = mount(ProfileView, {
      global: {
        stubs: {
          AppLayout: { template: '<div><slot /></div>' },
          StatCard: { template: '<div class="stat-card" />' },
          ProfileInfoCard: { template: '<div data-testid="profile-info-card" />' },
          ProfileBalanceNotifyCard: { template: '<div data-testid="profile-balance-notify-card" />' },
          ProfilePasswordForm: { template: '<div data-testid="profile-password-form" />' },
          ProfileTotpCard: { template: '<div data-testid="profile-totp-card" />' },
          ProfileKeyVerificationCard: { template: '<div data-testid="profile-key-verification-card" />' },
          ProfilePersonalApiKeyCard: { template: '<div data-testid="profile-personal-api-key-card" />' },
          Icon: true
        }
      }
    })

    await flushPromises()

    expect(wrapper.findAll('.stat-card')).toHaveLength(0)
    expect(wrapper.get('[data-testid="profile-shell"]').exists()).toBe(true)
    expect(wrapper.get('[data-testid="profile-shell"]').html()).toContain('profile-info-card')
    expect(wrapper.get('[data-testid="profile-shell"]').html()).toContain('profile-personal-api-key-card')
    expect(wrapper.get('[data-testid="profile-shell"]').html()).toContain('profile-password-form')
    expect(wrapper.get('[data-testid="profile-shell"]').html()).toContain('profile-totp-card')
    // 「创建 API Key 时需要验证身份」开关放在安全区里，紧跟在两步验证之后。
    const shell = wrapper.get('[data-testid="profile-shell"]').html()
    expect(shell).toContain('profile-key-verification-card')
    expect(shell.indexOf('profile-key-verification-card')).toBeGreaterThan(shell.indexOf('profile-totp-card'))
  })

  it('groups profile sections into horizontal tabs and switches panels on click and deep link', async () => {
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        { path: '/profile', name: 'Profile', component: { template: '<div />' } },
      ],
    })
    await router.push('/profile?tab=security')
    await router.isReady()

    const wrapper = mount(ProfileView, {
      attachTo: document.body,
      global: {
        plugins: [router],
        stubs: {
          AppLayout: { template: '<div><slot /></div>' },
          ProfileInfoCard: { props: ['section'], template: '<div :data-testid="`profile-info-card-${section}`" />' },
          ProfileBalanceNotifyCard: { template: '<div data-testid="profile-balance-notify-card" />' },
          ProfilePasswordForm: { template: '<div data-testid="profile-password-form" />' },
          ProfileTotpCard: { template: '<div data-testid="profile-totp-card" />' },
          ProfilePasskeyCard: { template: '<div data-testid="profile-passkey-card" />' },
          ProfileKeyVerificationCard: { template: '<div data-testid="profile-key-verification-card" />' },
          ProfilePersonalApiKeyCard: { template: '<div data-testid="profile-personal-api-key-card" />' },
          ProfileBarkCard: { template: '<div data-testid="profile-bark-card" />' },
          TwoFactorSuggestionBanner: { template: '<div data-testid="two-factor-banner" />' },
          Icon: true,
        },
      },
    })

    try {
      await flushPromises()

      expect(wrapper.get('[data-testid="profile-tabs-toolbar"]').exists()).toBe(true)
      expect(wrapper.find('#profile-tab-profile').exists()).toBe(true)
      expect(wrapper.find('#profile-tab-bindings').exists()).toBe(true)
      expect(wrapper.find('#profile-tab-security').exists()).toBe(true)
      expect(wrapper.find('#profile-tab-notifications').exists()).toBe(true)

      // Deep link ?tab=security activates the Security panel and hides the others.
      expect(wrapper.get('[data-testid="profile-tab-panel-security"]').isVisible()).toBe(true)
      expect(wrapper.get('[data-testid="profile-tab-panel-profile"]').isVisible()).toBe(false)
      expect(wrapper.get('[data-testid="profile-tab-panel-bindings"]').isVisible()).toBe(false)
      expect(wrapper.get('[data-testid="profile-tab-panel-notifications"]').isVisible()).toBe(false)

      // Clicking the Sign-In Methods tab switches panel visibility and updates the query string.
      const tabButtonFor = (id: string) =>
        wrapper.findAll('button[role="tab"]').find((button) => button.find(`#${id}`).exists())
      await tabButtonFor('profile-tab-bindings')!.trigger('mousedown')
      await flushPromises()
      expect(wrapper.get('[data-testid="profile-tab-panel-bindings"]').isVisible()).toBe(true)
      expect(wrapper.get('[data-testid="profile-tab-panel-security"]').isVisible()).toBe(false)
      expect(router.currentRoute.value.query.tab).toBe('bindings')

      // Switching to Notifications reveals the notifications panel and updates the query string.
      await tabButtonFor('profile-tab-notifications')!.trigger('mousedown')
      await flushPromises()
      expect(wrapper.get('[data-testid="profile-tab-panel-notifications"]').isVisible()).toBe(true)
      expect(wrapper.get('[data-testid="profile-tab-panel-bindings"]').isVisible()).toBe(false)
      expect(router.currentRoute.value.query.tab).toBe('notifications')

      // Navigating directly back to /profile without ?tab= resets the active tab to profile.
      await router.push('/profile')
      await flushPromises()
      expect(wrapper.get('[data-testid="profile-tab-panel-profile"]').isVisible()).toBe(true)
      expect(wrapper.get('[data-testid="profile-tab-panel-notifications"]').isVisible()).toBe(false)

      // Switching to bindings and returning via the Profile tab clears the tab query parameter.
      await tabButtonFor('profile-tab-bindings')!.trigger('mousedown')
      await flushPromises()
      expect(router.currentRoute.value.query.tab).toBe('bindings')
      await tabButtonFor('profile-tab-profile')!.trigger('mousedown')
      await flushPromises()
      expect(wrapper.get('[data-testid="profile-tab-panel-profile"]').isVisible()).toBe(true)
      expect(router.currentRoute.value.query.tab).toBeUndefined()
    } finally {
      wrapper.unmount()
    }
  })

  it('scrolls the active tab trigger into view for narrow horizontal tab bars', async () => {
    const scrollIntoViewMock = vi.fn()
    const originalScrollIntoView = HTMLElement.prototype.scrollIntoView
    HTMLElement.prototype.scrollIntoView = scrollIntoViewMock

    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        { path: '/profile', name: 'Profile', component: { template: '<div />' } },
      ],
    })
    await router.push('/profile?tab=notifications')
    await router.isReady()

    const wrapper = mount(ProfileView, {
      attachTo: document.body,
      global: {
        plugins: [router],
        stubs: {
          AppLayout: { template: '<div><slot /></div>' },
          ProfileInfoCard: { template: '<div />' },
          ProfileBalanceNotifyCard: { template: '<div />' },
          ProfilePasswordForm: { template: '<div />' },
          ProfileTotpCard: { template: '<div />' },
          ProfilePasskeyCard: { template: '<div />' },
          ProfileKeyVerificationCard: { template: '<div />' },
          ProfileBarkCard: { template: '<div />' },
          TwoFactorSuggestionBanner: { template: '<div />' },
          Icon: true,
        },
      },
    })

    try {
      await flushPromises()
      expect(scrollIntoViewMock).toHaveBeenCalledWith({ block: 'nearest', inline: 'nearest' })
    } finally {
      wrapper.unmount()
      HTMLElement.prototype.scrollIntoView = originalScrollIntoView
    }
  })

  it('falls back to the profile tab when an unknown tab query is supplied', async () => {
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        { path: '/profile', name: 'Profile', component: { template: '<div />' } },
      ],
    })
    await router.push('/profile?tab=nonexistent')
    await router.isReady()

    const wrapper = mount(ProfileView, {
      global: {
        plugins: [router],
        stubs: {
          AppLayout: { template: '<div><slot /></div>' },
          ProfileInfoCard: { template: '<div />' },
          ProfileBalanceNotifyCard: { template: '<div />' },
          ProfilePasswordForm: { template: '<div />' },
          ProfileTotpCard: { template: '<div />' },
          ProfilePasskeyCard: { template: '<div />' },
          ProfileKeyVerificationCard: { template: '<div />' },
          ProfileBarkCard: { template: '<div />' },
          TwoFactorSuggestionBanner: { template: '<div />' },
          Icon: true,
        },
      },
    })

    await flushPromises()

    expect(wrapper.get('[data-testid="profile-tab-panel-profile"]').isVisible()).toBe(true)
    expect(wrapper.get('[data-testid="profile-tab-panel-security"]').isVisible()).toBe(false)
  })
})
