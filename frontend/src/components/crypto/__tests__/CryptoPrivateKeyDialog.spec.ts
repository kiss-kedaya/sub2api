import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { createMemoryHistory, createRouter, RouterView } from 'vue-router'
import { clearCryptoGrant } from '@/composables/cryptoGrant'
import type { CryptoPrivateKeyExport } from '@/api/admin/crypto'
import CryptoPrivateKeyDialog from '../CryptoPrivateKeyDialog.vue'

const mocks = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), showError: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: mocks }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError: mocks.showError }) }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

enableAutoUnmount(afterEach)

const address = { id: 8, address: '0x1234567890123456789012345678901234567890', family: 'evm' }
const privateKey = 'a'.repeat(64)
const grantCredential = 'test-payment-grant-credential-32-bytes-minimum'
const grantResponse = () => ({ data: { data: { credential: grantCredential, expires_at: new Date(Date.now() + 300_000).toISOString(), expires_in: 300 } } })
const exportResponse = (patch: Partial<CryptoPrivateKeyExport> = {}): CryptoPrivateKeyExport => ({
  address_id: address.id, address: address.address, family: 'evm', derivation_index: 1,
  private_key: privateKey, format: 'hex', ...patch
})

beforeEach(() => {
  vi.resetAllMocks()
  clearCryptoGrant()
  localStorage.setItem('auth_token', 'test-administrator-jwt-session')
  mocks.get.mockResolvedValue({ data: { enabled: true, feature_enabled: true, can_setup: true } })
  mocks.post.mockImplementation(async (path: string) => {
    if (path.endsWith('/security/verify')) return grantResponse()
    if (path.endsWith('/operation-intent')) return { data: { accepted: true } }
    return { data: exportResponse() }
  })
  Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText: vi.fn().mockResolvedValue(undefined) } })
})

afterEach(() => {
  clearCryptoGrant()
  vi.useRealTimers()
  vi.restoreAllMocks()
})

async function render(selected = address) {
  const wrapper = mount(CryptoPrivateKeyDialog, { props: { address: selected }, global: { stubs: { teleport: true } } })
  await flushPromises()
  return wrapper
}

async function authorize(wrapper: Awaited<ReturnType<typeof render>>) {
  await wrapper.get('#crypto-security-code').setValue('234567')
  await wrapper.get('#crypto-security-form').trigger('submit')
  await flushPromises()
}

function exportCalls() {
  return mocks.post.mock.calls.filter(([path]) => path.endsWith('/export-private-key'))
}

describe('Single-address private key export UI contract', () => {
  it('reports intent, waits for real verification, and sends the selected ID with no-store', async () => {
    const localWrite = vi.spyOn(localStorage, 'setItem')
    const sessionWrite = vi.spyOn(sessionStorage, 'setItem')
    const wrapper = await render()
    expect(mocks.post).toHaveBeenCalledWith('/admin/crypto/security/operation-intent', { action: 'export_private_key' }, {
      _retry: true, headers: { 'Cache-Control': 'no-store', Pragma: 'no-cache' }
    })
    expect(exportCalls()).toHaveLength(0)
    await authorize(wrapper)
    expect(mocks.post).toHaveBeenCalledWith('/admin/crypto/addresses/8/export-private-key', {}, expect.objectContaining({
      _retry: true,
      signal: expect.any(AbortSignal),
      headers: { 'X-Crypto-Grant': grantCredential, 'Cache-Control': 'no-store', Pragma: 'no-cache' }
    }))
    expect(wrapper.get('[data-testid="private-key-address"]').text()).toBe(address.address)
    expect(wrapper.get('[data-testid="private-key-value"]').text()).not.toContain(privateKey)
    expect(wrapper.get('button[aria-pressed]').attributes('aria-pressed')).toBe('false')
    expect(wrapper.text()).not.toContain('mnemonic')
    expect(navigator.clipboard.writeText).not.toHaveBeenCalled()
    expect(localWrite).not.toHaveBeenCalled()
    expect(sessionWrite).not.toHaveBeenCalled()
  })

  it('shows and copies only on explicit clicks, then removes the key on close and reopen', async () => {
    const wrapper = await render()
    await authorize(wrapper)
    await wrapper.get('button[aria-pressed]').trigger('click')
    expect(wrapper.get('[data-testid="private-key-value"]').text()).toBe(privateKey)
    expect(navigator.clipboard.writeText).not.toHaveBeenCalled()
    await wrapper.get('[data-testid="private-key-copy"]').trigger('click')
    expect(navigator.clipboard.writeText).toHaveBeenCalledOnce()
    expect(navigator.clipboard.writeText).toHaveBeenCalledWith(privateKey)
    await wrapper.get('[data-testid="private-key-close"]').trigger('click')
    expect(wrapper.find('[data-testid="private-key-value"]').exists()).toBe(false)
    expect(wrapper.text()).not.toContain(privateKey)
    expect(wrapper.emitted('close')).toHaveLength(1)
    await wrapper.setProps({ address: null })
    await wrapper.setProps({ address })
    await flushPromises()
    expect(wrapper.get('[data-testid="private-key-value"]').text()).not.toContain(privateKey)
    expect(wrapper.get('button[aria-pressed]').attributes('aria-pressed')).toBe('false')
    // Reuse the unchanged, server-issued five-minute grant, with a new intent.
    expect(mocks.post.mock.calls.filter(([path]) => path.endsWith('/security/verify'))).toHaveLength(1)
    expect(mocks.post.mock.calls.filter(([path]) => path.endsWith('/operation-intent'))).toHaveLength(2)
    expect(exportCalls()).toHaveLength(2)
  })

  it('clears the displayed key after 60 seconds without touching the clipboard', async () => {
    vi.useFakeTimers()
    const wrapper = await render()
    await authorize(wrapper)
    await wrapper.get('button[aria-pressed]').trigger('click')
    expect(wrapper.text()).toContain(privateKey)
    await vi.advanceTimersByTimeAsync(60_000)
    await flushPromises()
    expect(wrapper.text()).not.toContain(privateKey)
    expect(wrapper.text()).toContain('admin.crypto.privateKeyExpired')
    expect(wrapper.find('[data-testid="private-key-value"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="private-key-copy"]').exists()).toBe(false)
    expect(navigator.clipboard.writeText).not.toHaveBeenCalled()
  })

  it('cancels verification without exporting', async () => {
    const wrapper = await render()
    await wrapper.get('[data-testid="crypto-security-cancel"]').trigger('click')
    await flushPromises()
    expect(exportCalls()).toHaveLength(0)
    expect(wrapper.emitted('close')).toHaveLength(1)
    expect(navigator.clipboard.writeText).not.toHaveBeenCalled()
  })

  it.each([false, 'failure'] as const)('blocks verification and export when intent is %s', async (result) => {
    mocks.post.mockImplementation(async () => {
      if (result === 'failure') throw new Error('Notification unavailable')
      return { data: { accepted: false } }
    })
    const wrapper = await render()
    expect(mocks.get).not.toHaveBeenCalled()
    expect(exportCalls()).toHaveLength(0)
    expect(wrapper.find('#crypto-security-code').exists()).toBe(false)
    expect(mocks.showError).toHaveBeenCalledWith('admin.crypto.walletOperationNotifyFailed')
  })

  it.each(['close', 'switch', 'unmount'] as const)('wipes late export data after %s', async (change) => {
    let completeExport!: (value: { data: CryptoPrivateKeyExport }) => void
    const normalPost = mocks.post.getMockImplementation()!
    mocks.post.mockImplementation((path: string, ...args: unknown[]) => {
      if (path === '/admin/crypto/addresses/8/export-private-key') return new Promise(resolve => { completeExport = resolve })
      if (path === '/admin/crypto/addresses/9/export-private-key') return Promise.resolve({ data: exportResponse({ address_id: 9, address: '0xOTHER', private_key: 'b'.repeat(64) }) })
      return normalPost(path, ...args)
    })
    const wrapper = await render()
    await authorize(wrapper)
    const signal = exportCalls()[0][2].signal as AbortSignal
    if (change === 'unmount') wrapper.unmount()
    else await wrapper.setProps({ address: change === 'close' ? null : { ...address, id: 9, address: '0xOTHER' } })
    expect(signal.aborted).toBe(true)
    const late = exportResponse()
    completeExport({ data: late })
    await flushPromises()
    expect(late.private_key).toBe('')
    expect(wrapper.text()).not.toContain(privateKey)
    if (change === 'switch') {
      expect(wrapper.get('[data-testid="private-key-address"]').text()).toBe('0xOTHER')
      await wrapper.get('button[aria-pressed]').trigger('click')
      expect(wrapper.get('[data-testid="private-key-value"]').text()).toBe('b'.repeat(64))
    } else {
      expect(wrapper.find('[data-testid="private-key-value"]').exists()).toBe(false)
    }
    expect(navigator.clipboard.writeText).not.toHaveBeenCalled()
  })

  it('ignores a late intent after the selected address has closed', async () => {
    let completeIntent!: (value: unknown) => void
    mocks.post.mockImplementation(() => new Promise(resolve => { completeIntent = resolve }))
    const wrapper = await render()
    await wrapper.setProps({ address: null })
    completeIntent({ data: { accepted: true } })
    await flushPromises()
    expect(mocks.get).not.toHaveBeenCalled()
    expect(exportCalls()).toHaveLength(0)
    expect(wrapper.find('#crypto-security-code').exists()).toBe(false)
  })

  it.each([
    { address_id: 9 }, { address: '0xDIFFERENT' }, { family: 'solana' as const },
    { format: 'base58' as const }, { private_key: '' }, { derivation_index: -1 }
  ])('rejects an export response with mismatched or invalid fields %o', async (patch) => {
    const result = exportResponse(patch)
    const normalPost = mocks.post.getMockImplementation()!
    mocks.post.mockImplementation((path: string, ...args: unknown[]) => path.endsWith('/export-private-key')
      ? Promise.resolve({ data: result }) : normalPost(path, ...args))
    const wrapper = await render()
    await authorize(wrapper)
    expect(result.private_key).toBe('')
    expect(wrapper.find('[data-testid="private-key-value"]').exists()).toBe(false)
    expect(mocks.showError).toHaveBeenCalledWith('admin.crypto.privateKeyExportFailed')
    expect(navigator.clipboard.writeText).not.toHaveBeenCalled()
  })

  it.each(['', '0x'])('accepts a hex key with prefix %s and case-insensitive EVM address matching', async (prefix) => {
    const normalPost = mocks.post.getMockImplementation()!
    const result = exportResponse({ address: address.address.toUpperCase(), private_key: prefix + privateKey })
    mocks.post.mockImplementation((path: string, ...args: unknown[]) => path.endsWith('/export-private-key')
      ? Promise.resolve({ data: result }) : normalPost(path, ...args))
    const wrapper = await render()
    await authorize(wrapper)
    await wrapper.get('button[aria-pressed]').trigger('click')
    expect(wrapper.get('[data-testid="private-key-value"]').text()).toBe(prefix + privateKey)
  })

  it.each([true, false])('uses case-sensitive matching for Solana (matches: %s)', async (matches) => {
    const selected = { id: 18, address: 'AbCdEfGHJKLMNPQRSTUVWXYZ123456789abcdefghijk', family: 'solana' }
    const normalPost = mocks.post.getMockImplementation()!
    const key = '2'.repeat(88)
    mocks.post.mockImplementation((path: string, ...args: unknown[]) => path.endsWith('/export-private-key')
      ? Promise.resolve({ data: exportResponse({ address_id: selected.id, address: matches ? selected.address : selected.address.toLowerCase(), family: 'solana', format: 'base58', private_key: key }) })
      : normalPost(path, ...args))
    const wrapper = await render(selected)
    await authorize(wrapper)
    expect(wrapper.find('[data-testid="private-key-value"]').exists()).toBe(matches)
    if (matches) {
      await wrapper.get('button[aria-pressed]').trigger('click')
      expect(wrapper.get('[data-testid="private-key-value"]').text()).toBe(key)
      expect(wrapper.text()).not.toContain('admin.crypto.privateKeyEvmShared')
    }
    expect(navigator.clipboard.writeText).not.toHaveBeenCalled()
  })

  it('clears the key and closes its teleported dialog when leaving the route', async () => {
    const router = createRouter({ history: createMemoryHistory(), routes: [
      { path: '/crypto', component: CryptoPrivateKeyDialog, props: { address } },
      { path: '/other', component: { template: '<div>Other page</div>' } }
    ] })
    await router.push('/crypto')
    await router.isReady()
    const wrapper = mount(RouterView, { attachTo: document.body, global: { plugins: [router] } })
    await flushPromises()
    const code = document.querySelector<HTMLInputElement>('#crypto-security-code')!
    code.value = '234567'
    code.dispatchEvent(new Event('input', { bubbles: true }))
    document.querySelector('#crypto-security-form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }))
    await flushPromises()
    expect(document.querySelector('[data-testid="private-key-value"]')).not.toBeNull()
    document.querySelector<HTMLButtonElement>('button[aria-controls="crypto-private-key-value"]')!.click()
    await flushPromises()
    expect(document.body.textContent).toContain(privateKey)
    await router.push('/other')
    await flushPromises()
    expect(document.body.textContent).not.toContain(privateKey)
    // The real modal leave animation may retain its DOM briefly, with no key.
    expect(document.querySelector('[data-testid="private-key-value"]')?.textContent ?? '').toBe('')
    await vi.waitFor(() => expect(document.querySelector('[data-testid="private-key-value"]')).toBeNull())
    expect(wrapper.text()).toContain('Other page')
  })
})
