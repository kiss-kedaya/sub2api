import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import ProfileBarkCard from '../ProfileBarkCard.vue'

const mocks = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn(), showSuccess: vi.fn(), showError: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: mocks }))
vi.mock('@/stores/app', () => ({ useAppStore: () => mocks }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
enableAutoUnmount(afterEach)

beforeEach(() => {
  vi.resetAllMocks()
  mocks.get.mockResolvedValue({ data: { data: { configured: true, enabled: true } } })
  mocks.put.mockResolvedValue({ data: { saved: true } })
})

describe('shared Bark configuration card', () => {
  it('shows the wallet title while keeping the saved key private and blank', async () => {
    const wrapper = mount(ProfileBarkCard, { props: { title: 'Wallet security', description: 'Primary operator only' } })
    await flushPromises()
    expect(wrapper.get('h2').text()).toBe('Wallet security')
    expect(wrapper.text()).toContain('Primary operator only')
    expect(wrapper.get('#bark-device-key').attributes()).toMatchObject({ type: 'password', autocomplete: 'off', spellcheck: 'false' })
    expect((wrapper.get('#bark-device-key').element as HTMLInputElement).value).toBe('')
    expect(wrapper.text()).toContain('profile.bark.keepKeyHint')
    expect(mocks.get).toHaveBeenCalledWith('/user/bark')
  })

  it('saves a toggle with no key field so the existing encrypted key is retained', async () => {
    const wrapper = mount(ProfileBarkCard)
    await flushPromises()
    await wrapper.get('input[type="checkbox"]').setValue(false)
    await wrapper.get('.btn-primary').trigger('click')
    await flushPromises()
    expect(mocks.put).toHaveBeenCalledWith('/user/bark', { enabled: false })
    expect(mocks.put.mock.calls[0][1]).not.toHaveProperty('device_key')
    expect(mocks.showSuccess).toHaveBeenCalledWith('profile.bark.saved')
  })

  it('replaces a key only when entered and clears the input after saving', async () => {
    const wrapper = mount(ProfileBarkCard)
    await flushPromises()
    await wrapper.get('#bark-device-key').setValue('fixture-device-key')
    await wrapper.get('.btn-primary').trigger('click')
    await flushPromises()
    expect(mocks.put).toHaveBeenCalledWith('/user/bark', { device_key: 'fixture-device-key', enabled: true })
    expect((wrapper.get('#bark-device-key').element as HTMLInputElement).value).toBe('')
  })

  it('clears the saved key only through the explicit clear action', async () => {
    const wrapper = mount(ProfileBarkCard)
    await flushPromises()
    const clear = wrapper.findAll('button').find(button => button.text() === 'profile.bark.clear')!
    await clear.trigger('click')
    await flushPromises()
    expect(mocks.put).toHaveBeenCalledWith('/user/bark', { device_key: '', enabled: false })
  })

  it('blocks editing after a failed read and allows it after a successful refresh', async () => {
    mocks.get.mockRejectedValueOnce(new Error('Unavailable'))
    const wrapper = mount(ProfileBarkCard)
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toContain('profile.bark.loadFailed')
    expect(wrapper.get('.btn-primary').attributes('disabled')).toBeDefined()
    expect(wrapper.get('#bark-device-key').attributes('disabled')).toBeDefined()
    await wrapper.get('.btn-primary').trigger('click')
    expect(mocks.put).not.toHaveBeenCalled()
    await wrapper.get('[role="alert"] button').trigger('click')
    await flushPromises()
    expect(wrapper.find('[role="alert"]').exists()).toBe(false)
    expect(wrapper.get('.btn-primary').attributes('disabled')).toBeUndefined()
  })
})
