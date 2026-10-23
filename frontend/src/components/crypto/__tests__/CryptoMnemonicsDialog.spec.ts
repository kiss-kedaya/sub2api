import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import CryptoMnemonicsDialog from '../CryptoMnemonicsDialog.vue'

const mocks = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), showError: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: mocks }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError: mocks.showError, showSuccess: vi.fn() }) }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
enableAutoUnmount(afterEach)

const deposit = 'deposit alpha bravo charlie delta echo foxtrot golf hotel india juliet kilo'
const hot = 'hot november oscar papa quebec romeo sierra tango uniform victor whiskey xray'

beforeEach(() => {
  vi.resetAllMocks()
  mocks.get.mockResolvedValue({ data: { enabled: true, feature_enabled: true, can_setup: true } })
  mocks.post.mockImplementation(async (path: string) => path.endsWith('/operation-intent')
    ? { data: { accepted: true } } : { data: { deposit_mnemonic: deposit, hot_mnemonic: hot } })
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
  await wrapper.get('input[type="checkbox"]').setValue(true)
  await wrapper.get('#crypto-mnemonics-form').trigger('submit')
  await flushPromises()
  expect(mocks.post.mock.calls.filter(([path]) => path.endsWith('/export-mnemonics'))).toHaveLength(0)
  expect(mocks.post).toHaveBeenCalledWith('/admin/crypto/security/operation-intent', { action: 'export_mnemonics' }, {
    _retry: true, headers: { 'Cache-Control': 'no-store', Pragma: 'no-cache' }
  })
  await wrapper.get('#crypto-security-code').setValue('234567')
  await wrapper.get('#crypto-security-form').trigger('submit')
  await flushPromises()
  if (waitForTransition) await new Promise((resolve) => setTimeout(resolve, 200))
}

describe('Crypto mnemonic plaintext dialog UI (not a backend security test)', () => {
  it('requires production confirmation before asking for TOTP', async () => {
    const wrapper = render()
    await wrapper.get('#crypto-mnemonics-form').trigger('submit')
    expect(mocks.get).not.toHaveBeenCalled()
    expect(mocks.post).not.toHaveBeenCalled()
    expect(wrapper.get('button[form="crypto-mnemonics-form"]').attributes('disabled')).toBeDefined()
  })

  it('sends only fresh TOTP with no-store, then shows two independently masked sections', async () => {
    const localWrite = vi.spyOn(localStorage, 'setItem')
    const sessionWrite = vi.spyOn(sessionStorage, 'setItem')
    const wrapper = render()
    await submitAndAuthorize(wrapper)
    expect(mocks.post).toHaveBeenCalledWith('/admin/crypto/wallet/export-mnemonics', {}, expect.objectContaining({
      headers: { 'X-Crypto-TOTP': '234567', 'Cache-Control': 'no-store', Pragma: 'no-cache' }
    }))
    expect(wrapper.findAll('.crypto-mnemonics__seed')).toHaveLength(2)
    expect(wrapper.text()).not.toContain(deposit)
    expect(wrapper.text()).not.toContain(hot)
    expect(wrapper.findAll('.crypto-mnemonics__words--masked')).toHaveLength(4)
    expect(wrapper.find('#crypto-mnemonics-passphrase').exists()).toBe(false)
    expect(localWrite).not.toHaveBeenCalled()
    expect(sessionWrite).not.toHaveBeenCalled()
  })

  it('reveals each seed independently and copies only the selected half of each mnemonic', async () => {
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
    const copyButtons = wrapper.findAll('[data-copy-part]')
    expect(copyButtons.map((button) => button.attributes('data-copy-part'))).toEqual([
      'deposit-1', 'deposit-2', 'hot-1', 'hot-2'
    ])
    const depositParts = deposit.split(' ').reduce<string[][]>((parts, word, index, all) => {
      const midpoint = Math.ceil(all.length / 2)
      const partIndex = index < midpoint ? 0 : 1
      parts[partIndex].push(word)
      return parts
    }, [[], []])
    const hotParts = hot.split(' ').reduce<string[][]>((parts, word, index, all) => {
      const partIndex = index < Math.ceil(all.length / 2) ? 0 : 1
      parts[partIndex].push(word)
      return parts
    }, [[], []])
    for (const [index, button] of copyButtons.entries()) {
      await button.trigger('click')
      await flushPromises()
      expect(copy).toHaveBeenNthCalledWith(index + 1, [...depositParts, ...hotParts][index].join(' '))
      expect(copy.mock.calls[index][0]).not.toBe(index < 2 ? deposit : hot)
    }
    expect(wrapper.findAll('.crypto-mnemonics__part--copied')).toHaveLength(4)
  })

  it('splits 24-word seeds into ordered 12-word copy segments', async () => {
    const words24 = Array.from({ length: 24 }, (_, index) => `seed${index + 1}`)
    mocks.post.mockImplementation(async (path: string) => path.endsWith('/operation-intent')
      ? { data: { accepted: true } } : { data: { deposit_mnemonic: words24.join(' '), hot_mnemonic: hot } })
    const wrapper = render()
    await submitAndAuthorize(wrapper)
    const depositButtons = wrapper.findAll('[data-copy-part]').filter((button) => button.attributes('data-copy-part')?.startsWith('deposit-'))
    const copy = vi.mocked(navigator.clipboard.writeText)
    await depositButtons[0].trigger('click')
    await flushPromises()
    await depositButtons[1].trigger('click')
    await flushPromises()
    expect(copy).toHaveBeenNthCalledWith(1, words24.slice(0, 12).join(' '))
    expect(copy).toHaveBeenNthCalledWith(2, words24.slice(12).join(' '))
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

  it('wipes late plaintext export data when the dialog closes during the request', async () => {
    let resolveExport!: (value: { data: { deposit_mnemonic: string; hot_mnemonic: string } }) => void
    mocks.post.mockImplementation((path: string) => path.endsWith('/operation-intent')
      ? Promise.resolve({ data: { accepted: true } }) : new Promise((resolve) => { resolveExport = resolve }))
    const wrapper = render()
    await wrapper.get('input[type="checkbox"]').setValue(true)
    await wrapper.get('#crypto-mnemonics-form').trigger('submit')
    await flushPromises()
    await wrapper.get('#crypto-security-code').setValue('234567')
    await wrapper.get('#crypto-security-form').trigger('submit')
    await Promise.resolve()

    const lateResponse = { deposit_mnemonic: deposit, hot_mnemonic: hot }
    expect(mocks.post).toHaveBeenCalledTimes(2)
    await wrapper.setProps({ show: false })
    resolveExport({ data: lateResponse })
    await flushPromises()

    expect(lateResponse).toEqual({ deposit_mnemonic: '', hot_mnemonic: '' })
    expect(wrapper.text()).not.toContain(deposit)
    expect(wrapper.text()).not.toContain(hot)
    expect(wrapper.findAll('.crypto-mnemonics__seed')).toHaveLength(0)
  })

  it('cancels without a request, clears secrets on close/reopen, and never writes to clipboard', async () => {
    const wrapper = render()
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

  it.each([false, 'failure'] as const)('blocks export before TOTP when notification is %s', async (result) => {
    mocks.post.mockImplementation(async () => {
      if (result === 'failure') throw new Error('Bark unavailable')
      return { data: { accepted: false } }
    })
    const wrapper = render()
    await wrapper.get('input[type="checkbox"]').setValue(true)
    await wrapper.get('#crypto-mnemonics-form').trigger('submit')
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toBe('admin.crypto.walletOperationNotifyFailed')
    expect(wrapper.find('#crypto-security-code').exists()).toBe(false)
    expect(mocks.get).not.toHaveBeenCalled()
    expect(mocks.post.mock.calls.filter(([path]) => path.endsWith('/export-mnemonics'))).toHaveLength(0)
    expect(mocks.showError).toHaveBeenCalledWith('admin.crypto.walletOperationNotifyFailed')
  })

  it('ignores a late notification when the export dialog has closed', async () => {
    let completeIntent!: (value: unknown) => void
    mocks.post.mockImplementation(() => new Promise(resolve => { completeIntent = resolve }))
    const wrapper = render()
    await wrapper.get('input[type="checkbox"]').setValue(true)
    await wrapper.get('#crypto-mnemonics-form').trigger('submit')
    await flushPromises()
    await wrapper.setProps({ show: false })
    completeIntent({ data: { accepted: true } })
    await flushPromises()
    expect(mocks.get).not.toHaveBeenCalled()
    expect(wrapper.find('#crypto-security-code').exists()).toBe(false)
    expect(mocks.post.mock.calls.filter(([path]) => path.endsWith('/export-mnemonics'))).toHaveLength(0)
  })
})
