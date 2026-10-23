import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import ProfileTotpCard from '../ProfileTotpCard.vue'

const mocks = vi.hoisted(() => ({ getStatus: vi.fn() }))
vi.mock('@/api', () => ({ totpAPI: mocks }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('../TotpDisableDialog.vue', () => ({ default: { template: '<div data-testid="disable-dialog" />' } }))
vi.mock('../TotpSetupModal.vue', () => ({ default: { template: '<div data-testid="setup-modal" />' } }))

enableAutoUnmount(afterEach)
beforeEach(() => vi.resetAllMocks())

function render() {
  return mount(ProfileTotpCard)
}

describe('Profile TOTP access independently of the global enrollment switch', () => {
  it.each([false, true])('keeps an already-bound authenticator manageable when global feature_enabled=%s', async featureEnabled => {
    mocks.getStatus.mockResolvedValue({ enabled: true, feature_enabled: featureEnabled, enabled_at: 1700000000 })
    const wrapper = render()
    await flushPromises()
    expect(wrapper.text()).toContain('profile.totp.enabled')
    expect(wrapper.text()).not.toContain('profile.totp.featureDisabled')
    const disable = wrapper.get('button.btn-outline-danger')
    await disable.trigger('click')
    expect(wrapper.find('[data-testid="disable-dialog"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="setup-modal"]').exists()).toBe(false)
  })

  it('shows the global-disabled notice only for accounts without an authenticator', async () => {
    mocks.getStatus.mockResolvedValue({ enabled: false, feature_enabled: false })
    const wrapper = render()
    await flushPromises()
    expect(wrapper.text()).toContain('profile.totp.featureDisabled')
    expect(wrapper.find('button').exists()).toBe(false)
    expect(wrapper.find('[data-testid="disable-dialog"]').exists()).toBe(false)
  })

  it('offers setup for unbound accounts while enrollment is enabled', async () => {
    mocks.getStatus.mockResolvedValue({ enabled: false, feature_enabled: true })
    const wrapper = render()
    await flushPromises()
    await wrapper.get('button.btn-primary').trigger('click')
    expect(wrapper.find('[data-testid="setup-modal"]').exists()).toBe(true)
  })
})
