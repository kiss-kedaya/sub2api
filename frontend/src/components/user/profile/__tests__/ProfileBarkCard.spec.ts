import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import ProfileBarkCard from '../ProfileBarkCard.vue'

const mocks = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn(), getTotpStatus: vi.fn(), showSuccess: vi.fn(), showError: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: mocks }))
vi.mock('@/stores/app', () => ({ useAppStore: () => mocks }))
vi.mock('@/stores', () => ({ useAppStore: () => mocks }))
vi.mock('@/api', () => ({ totpAPI: { getStatus: mocks.getTotpStatus } }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
enableAutoUnmount(afterEach)

function render(props: { title?: string; description?: string } = {}) {
  return mount(ProfileBarkCard, { props, global: { stubs: { teleport: true } } })
}

beforeEach(() => {
  vi.resetAllMocks()
  mocks.get.mockResolvedValue({ data: { data: { configured: true, enabled: true } } })
  mocks.put.mockResolvedValue({ data: { saved: true } })
})

describe('shared Bark configuration card', () => {
  it('shows the wallet title while keeping the saved key private and blank', async () => {
    const wrapper = render({ title: 'Wallet security', description: 'Primary operator only' })
    await flushPromises()
    expect(wrapper.get('h2').text()).toBe('Wallet security')
    expect(wrapper.text()).toContain('Primary operator only')
    expect(wrapper.get('#bark-device-key').attributes()).toMatchObject({ type: 'password', autocomplete: 'off', spellcheck: 'false' })
    expect((wrapper.get('#bark-device-key').element as HTMLInputElement).value).toBe('')
    expect(wrapper.text()).toContain('profile.bark.keepKeyHint')
    expect(mocks.get).toHaveBeenCalledWith('/user/bark')
  })

  it('saves a toggle with no key field so the existing encrypted key is retained', async () => {
    const wrapper = render()
    await flushPromises()
    await wrapper.get('input[type="checkbox"]').setValue(false)
    await wrapper.get('.btn-primary').trigger('click')
    await flushPromises()
    expect(mocks.put).toHaveBeenCalledWith('/user/bark', { enabled: false }, {
      _retry: true, headers: { 'Cache-Control': 'no-store', Pragma: 'no-cache' }
    })
    expect(mocks.put).toHaveBeenCalledOnce()
    expect(mocks.put.mock.calls[0][1]).not.toHaveProperty('device_key')
    expect(mocks.showSuccess).toHaveBeenCalledWith('profile.bark.saved')
  })

  it('does not ask for a 2FA code when saving', async () => {
    const wrapper = render()
    await flushPromises()
    await wrapper.get('input[type="checkbox"]').setValue(false)
    await wrapper.get('.btn-primary').trigger('click')
    await flushPromises()
    // No one-time-code input and no TOTP status lookup: saving must be direct.
    expect(wrapper.find('input[autocomplete="one-time-code"]').exists()).toBe(false)
    expect(mocks.getTotpStatus).not.toHaveBeenCalled()
    expect(mocks.put).toHaveBeenCalledOnce()
  })

  it('replaces a key only when entered and clears the input after saving', async () => {
    const wrapper = render()
    await flushPromises()
    await wrapper.get('#bark-device-key').setValue('fixture-device-key')
    await wrapper.get('.btn-primary').trigger('click')
    await flushPromises()
    expect(mocks.put).toHaveBeenCalledWith('/user/bark', { device_key: 'fixture-device-key', enabled: true }, expect.objectContaining({ headers: expect.objectContaining({ 'Cache-Control': 'no-store' }) }))
    expect((wrapper.get('#bark-device-key').element as HTMLInputElement).value).toBe('')
  })

  it('clears the saved key only through the explicit clear action', async () => {
    const wrapper = render()
    await flushPromises()
    const clear = wrapper.findAll('button').find(button => button.text() === 'profile.bark.clear')!
    await clear.trigger('click')
    await flushPromises()
    expect(mocks.put).toHaveBeenCalledWith('/user/bark', { device_key: '', enabled: false }, expect.objectContaining({ headers: expect.objectContaining({ 'Cache-Control': 'no-store' }) }))
  })

  it('blocks editing after a failed read and allows it after a successful refresh', async () => {
    mocks.get.mockRejectedValueOnce(new Error('boom'))
    const wrapper = render()
    await flushPromises()
    expect(wrapper.text()).toContain('profile.bark.loadFailed')
    expect(wrapper.get('#bark-device-key').attributes('disabled')).toBeDefined()
    mocks.get.mockResolvedValue({ data: { data: { configured: true, enabled: true } } })
    await wrapper.get('.btn-secondary').trigger('click')
    await flushPromises()
    expect(wrapper.get('#bark-device-key').attributes('disabled')).toBeUndefined()
  })

  it('surfaces a save failure and keeps the form usable', async () => {
    mocks.put.mockRejectedValueOnce(new Error('BARK_ORIGINAL_NOTIFICATION_FAILED'))
    const wrapper = render()
    await flushPromises()
    await wrapper.get('input[type="checkbox"]').setValue(false)
    await wrapper.get('.btn-primary').trigger('click')
    await flushPromises()
    expect(mocks.showError).toHaveBeenCalled()
    expect(wrapper.get('input[type="checkbox"]').attributes('disabled')).toBeUndefined()
  })
})
