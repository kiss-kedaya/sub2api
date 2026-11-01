import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import ProfileBarkCard from '../ProfileBarkCard.vue'

const mocks = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn(), getTotpStatus: vi.fn(), stepUp: vi.fn(), showSuccess: vi.fn(), showError: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: mocks }))
vi.mock('@/stores/app', () => ({ useAppStore: () => mocks }))
vi.mock('@/stores', () => ({ useAppStore: () => mocks }))
vi.mock('@/api', () => ({ totpAPI: { getStatus: mocks.getTotpStatus, stepUp: mocks.stepUp } }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
enableAutoUnmount(afterEach)

function render(props: { title?: string; description?: string } = {}) {
  return mount(ProfileBarkCard, { props, global: { stubs: { teleport: true } } })
}

beforeEach(() => {
  vi.resetAllMocks()
  mocks.get.mockResolvedValue({ data: { data: { configured: true, enabled: true } } })
  mocks.put.mockResolvedValue({ data: { saved: true } })
  mocks.getTotpStatus.mockResolvedValue({ enabled: true, feature_enabled: true })
})

async function authorize(wrapper: ReturnType<typeof mount>, code = '123456') {
  await flushPromises()
  expect(mocks.put).not.toHaveBeenCalled()
  await wrapper.get('input[autocomplete="one-time-code"]').setValue(code)
  await flushPromises()
}

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
    await wrapper.get('[role="switch"]').trigger('click')
    await wrapper.get('[data-testid="bark-save"]').trigger('click')
    await authorize(wrapper)
    await flushPromises()
    expect(mocks.put).toHaveBeenCalledWith('/user/bark', { enabled: false }, {
      _retry: true, headers: { 'X-Bark-TOTP': '123456', 'Cache-Control': 'no-store', Pragma: 'no-cache' }
    })
    expect(mocks.put).toHaveBeenCalledOnce()
    expect(mocks.stepUp).not.toHaveBeenCalled()
    expect(mocks.put.mock.calls[0][1]).not.toHaveProperty('device_key')
    expect(mocks.showSuccess).toHaveBeenCalledWith('profile.bark.saved')
  })

  it('replaces a key only when entered and clears the input after saving', async () => {
    const wrapper = render()
    await flushPromises()
    await wrapper.get('#bark-device-key').setValue('fixture-device-key')
    await wrapper.get('[data-testid="bark-save"]').trigger('click')
    await authorize(wrapper)
    await flushPromises()
    expect(mocks.put).toHaveBeenCalledWith('/user/bark', { device_key: 'fixture-device-key', enabled: true }, expect.objectContaining({ headers: expect.objectContaining({ 'X-Bark-TOTP': '123456' }) }))
    expect((wrapper.get('#bark-device-key').element as HTMLInputElement).value).toBe('')
  })

  it('clears the saved key only through the explicit clear action', async () => {
    const wrapper = render()
    await flushPromises()
    const clear = wrapper.findAll('button').find(button => button.text() === 'profile.bark.clear')!
    await clear.trigger('click')
    await authorize(wrapper)
    await flushPromises()
    expect(mocks.put).toHaveBeenCalledWith('/user/bark', { device_key: '', enabled: false }, expect.objectContaining({ headers: expect.objectContaining({ 'X-Bark-TOTP': '123456' }) }))
  })

  it('blocks editing after a failed read and allows it after a successful refresh', async () => {
    mocks.get.mockRejectedValueOnce(new Error('Unavailable'))
    const wrapper = render()
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toContain('profile.bark.loadFailed')
    expect(wrapper.get('[data-testid="bark-save"]').attributes('disabled')).toBeDefined()
    expect(wrapper.get('#bark-device-key').attributes('disabled')).toBeDefined()
    await wrapper.get('[data-testid="bark-save"]').trigger('click')
    expect(mocks.put).not.toHaveBeenCalled()
    await wrapper.get('[role="alert"] button').trigger('click')
    await flushPromises()
    expect(wrapper.find('[role="alert"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="bark-save"]').attributes('disabled')).toBeUndefined()
  })

  it('requires bound account 2FA even for the first device key and links to Profile', async () => {
    mocks.get.mockResolvedValue({ data: { data: { configured: false, enabled: false } } })
    mocks.getTotpStatus.mockResolvedValue({ enabled: false, feature_enabled: false })
    const wrapper = render()
    await flushPromises()
    await wrapper.get('#bark-device-key').setValue('fixture-first-device')
    await wrapper.get('[data-testid="bark-save"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[role="alert"] a').attributes('href')).toBe('/profile')
    expect(wrapper.text()).toContain('profile.bark.totpRequired')
    expect(wrapper.find('input[autocomplete="one-time-code"]').exists()).toBe(false)
    expect(mocks.put).not.toHaveBeenCalled()
  })

  it('cancels a configuration change without issuing a write', async () => {
    const wrapper = render()
    await flushPromises()
    await wrapper.get('[data-testid="bark-save"]').trigger('click')
    await flushPromises()
    const cancel = wrapper.findAll('button').find(button => button.text() === 'common.cancel')!
    await cancel.trigger('click')
    await flushPromises()
    expect(mocks.put).not.toHaveBeenCalled()
    expect(wrapper.find('input[autocomplete="one-time-code"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="bark-save"]').attributes('disabled')).toBeUndefined()
  })

  it('clears a rejected code and retries the same pending change exactly once with a fresh code', async () => {
    mocks.put.mockRejectedValueOnce({ message: 'Old device could not be notified' })
      .mockResolvedValueOnce({ data: { saved: true } })
    const wrapper = render()
    await flushPromises()
    await wrapper.get('[role="switch"]').trigger('click')
    await wrapper.get('[data-testid="bark-save"]').trigger('click')
    await authorize(wrapper, '111111')
    expect(mocks.showError).toHaveBeenCalledWith('Old device could not be notified')
    expect(wrapper.get('input[autocomplete="one-time-code"]').element).toHaveProperty('value', '')
    expect(mocks.showSuccess).not.toHaveBeenCalled()
    await wrapper.get('input[autocomplete="one-time-code"]').setValue('222222')
    await flushPromises()
    expect(mocks.put).toHaveBeenCalledTimes(2)
    expect(mocks.put.mock.calls.map(call => call[1])).toEqual([{ enabled: false }, { enabled: false }])
    expect(mocks.put.mock.calls[1][2].headers['X-Bark-TOTP']).toBe('222222')
    expect(mocks.stepUp).not.toHaveBeenCalled()
    expect(wrapper.find('input[autocomplete="one-time-code"]').exists()).toBe(false)
    expect(mocks.showSuccess).toHaveBeenCalledOnce()
  })

  it('does not open a late verification after the card is unmounted', async () => {
    let resolveStatus!: (value: { enabled: boolean }) => void
    mocks.getTotpStatus.mockImplementationOnce(() => new Promise(resolve => { resolveStatus = resolve }))
    const wrapper = render()
    await flushPromises()
    await wrapper.get('[data-testid="bark-save"]').trigger('click')
    wrapper.unmount()
    resolveStatus({ enabled: true })
    await flushPromises()
    expect(mocks.put).not.toHaveBeenCalled()
  })
})
