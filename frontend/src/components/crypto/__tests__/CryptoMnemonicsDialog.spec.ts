import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import CryptoMnemonicsDialog from '../CryptoMnemonicsDialog.vue'

const mocks = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: mocks }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError: vi.fn(), showSuccess: vi.fn() }) }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
enableAutoUnmount(afterEach)

const deposit = 'deposit alpha bravo charlie delta echo foxtrot golf hotel india juliet kilo'
const hot = 'hot november oscar papa quebec romeo sierra tango uniform victor whiskey xray'

beforeEach(() => {
  vi.resetAllMocks()
  mocks.get.mockResolvedValue({ data: { enabled: true, feature_enabled: true, can_setup: true } })
  mocks.post.mockResolvedValue({ data: { deposit_mnemonic: deposit, hot_mnemonic: hot } })
  Object.defineProperty(navigator, 'clipboard', {
    configurable: true,
    value: { writeText: vi.fn().mockResolvedValue(undefined) }
  })
})
afterEach(() => { vi.restoreAllMocks() })

function render(show = true) {
  return mount(CryptoMnemonicsDialog, { props: { show }, global: { stubs: { teleport: true } } })
}

async function submitAndAuthorize(wrapper: ReturnType<typeof render>, waitForTransition = true) {
  await wrapper.get('#crypto-mnemonics-passphrase').setValue('vault-secret')
  await wrapper.get('input[type="checkbox"]').setValue(true)
  await wrapper.get('#crypto-mnemonics-form').trigger('submit')
  await flushPromises()
  expect(mocks.post).not.toHaveBeenCalled()
  await wrapper.get('#crypto-security-code').setValue('234567')
  await wrapper.get('#crypto-security-form').trigger('submit')
  await flushPromises()
  if (waitForTransition) await new Promise((resolve) => setTimeout(resolve, 200))
}

describe('Crypto mnemonic plaintext dialog UI (not a backend security test)', () => {
  it('requires the vault passphrase and production confirmation before asking for TOTP', async () => {
    const wrapper = render()
    await wrapper.get('#crypto-mnemonics-passphrase').setValue('vault-secret')
    await wrapper.get('#crypto-mnemonics-form').trigger('submit')
    expect(mocks.get).not.toHaveBeenCalled()
    expect(mocks.post).not.toHaveBeenCalled()
    expect(wrapper.get('button[form="crypto-mnemonics-form"]').attributes('disabled')).toBeDefined()
  })

  it('sends only the vault passphrase and fresh TOTP with no-store, then shows two independently masked sections', async () => {
    const localWrite = vi.spyOn(localStorage, 'setItem')
    const sessionWrite = vi.spyOn(sessionStorage, 'setItem')
    const wrapper = render()
    await submitAndAuthorize(wrapper)
    expect(mocks.post).toHaveBeenCalledWith('/admin/crypto/signer/export-mnemonics', { passphrase: 'vault-secret' }, expect.objectContaining({
      headers: { 'X-Crypto-TOTP': '234567', 'Cache-Control': 'no-store', Pragma: 'no-cache' }
    }))
    expect(wrapper.findAll('.crypto-mnemonics__seed')).toHaveLength(2)
    expect(wrapper.text()).not.toContain(deposit)
    expect(wrapper.text()).not.toContain(hot)
    expect(wrapper.findAll('.crypto-mnemonics__words--masked')).toHaveLength(2)
    expect(wrapper.find('#crypto-mnemonics-passphrase').exists()).toBe(false)
    expect(localWrite).not.toHaveBeenCalled()
    expect(sessionWrite).not.toHaveBeenCalled()
  })

  it('reveals and copies each seed independently only after an explicit button click', async () => {
    const wrapper = render()
    await submitAndAuthorize(wrapper)
    const sections = wrapper.findAll('.crypto-mnemonics__seed')
    const copy = vi.mocked(navigator.clipboard.writeText)
    expect(copy).not.toHaveBeenCalled()
    await sections[0].get('button[aria-pressed]').trigger('click')
    expect(sections[0].findAll('.crypto-mnemonics__words li strong').map((word) => word.text()).join(' ')).toBe(deposit)
    expect(sections[1].text()).not.toContain(hot)
    expect((sections[1].get('button[aria-pressed]').element as HTMLButtonElement).getAttribute('aria-pressed')).toBe('false')
    expect(copy).not.toHaveBeenCalled()
    const copyButtons = wrapper.findAll('.crypto-mnemonics__actions button:not([aria-pressed])')
    await copyButtons[0].trigger('click')
    expect(copy).toHaveBeenCalledTimes(1)
    expect(copy).toHaveBeenLastCalledWith(deposit)
    await copyButtons[1].trigger('click')
    expect(copy).toHaveBeenCalledTimes(2)
    expect(copy).toHaveBeenLastCalledWith(hot)
  })

  it('removes both plaintext seeds from the page after the short reveal window', async () => {
    vi.useFakeTimers()
    const wrapper = render()
    await submitAndAuthorize(wrapper, false)
    await vi.advanceTimersByTimeAsync(200)
    await flushPromises()
    expect(wrapper.text()).toContain('admin.crypto.mnemonicsAutoClear')
    await vi.advanceTimersByTimeAsync(60_000)
    await flushPromises()
    expect(wrapper.text()).toContain('admin.crypto.mnemonicsExpired')
    expect(wrapper.text()).not.toContain(deposit)
    expect(wrapper.text()).not.toContain(hot)
    expect(wrapper.findAll('.crypto-mnemonics__seed')).toHaveLength(0)
    vi.useRealTimers()
  })

  it('cancels without a request, clears secrets on close/reopen, and never writes to clipboard', async () => {
    const wrapper = render()
    await wrapper.get('#crypto-mnemonics-passphrase').setValue('do-not-retain')
    await wrapper.get('input[type="checkbox"]').setValue(true)
    await wrapper.get('.crypto-mnemonics__footer .btn-secondary').trigger('click')
    expect(mocks.get).not.toHaveBeenCalled()
    expect(mocks.post).not.toHaveBeenCalled()
    await wrapper.setProps({ show: false })
    await wrapper.setProps({ show: true })
    await submitAndAuthorize(wrapper)
    await wrapper.get('.crypto-mnemonics__footer .btn-primary').trigger('click')
    await wrapper.setProps({ show: false })
    expect(wrapper.text()).not.toContain(deposit)
    expect(wrapper.text()).not.toContain(hot)
    expect(wrapper.findAll('.crypto-mnemonics__seed')).toHaveLength(0)
    expect(navigator.clipboard.writeText).not.toHaveBeenCalled()
  })
})
