import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import CryptoView from '../CryptoView.vue'

const mocks = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn(), showSuccess: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: mocks }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showSuccess: mocks.showSuccess }) }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string, params?: Record<string, unknown>) => key + (params ? JSON.stringify(params) : '') }) }))
vi.mock('@/components/layout/AppLayout.vue', () => ({ default: { template: '<div><slot /></div>' } }))
enableAutoUnmount(afterEach)

const config = () => ({ enabled: true, chains: ['bsc'], rpc: { bsc: 'https://one.example,https://two.example' }, sweep_global_threshold: 100,
  sweep_min_per_address: {}, gas_topup: {}, hot_wallet_evm: '0x1234567890123456789012345678901234567890', hot_wallet_solana: '', hot_index: 0,
  sweep_auto: false, confirmations: {}, scan_range: {}, credit_min_amount: 1, credit_max_amount: 10000, credit_multiplier: 1, signer_socket: '/tmp/signer.sock' })

beforeEach(() => {
  vi.resetAllMocks()
  mocks.get.mockImplementation(async (path: string) => {
    if (path.endsWith('/security/status')) return { data: { enabled: true, feature_enabled: true, can_setup: true } }
    let data: unknown = []
    if (path.endsWith('/config')) data = config()
    if (path.endsWith('/addresses')) data = { items: [{ id: 8, user_id: 2, address: '0xDEPOSIT', family: 'evm', status: 'active' }], total: 1, summary: { address_count: 1, deposit_count: 0, credited_total: 0, swept_total: 0 } }
    if (path.endsWith('/signer/status')) data = { unlocked: false, signing_enabled: false }
    if (path.endsWith('/pool')) data = { free: { evm: 10 }, low_watermark: 2 }
    if (path.endsWith('/hot-wallet/address')) data = { index: 0, evm: '0xGAS', solana: '' }
    if (path.endsWith('/sweep/plan')) data = { Entries: [{ Chain: 'bsc', Address: '0xDEPOSIT', Token: 'USDT', AmountDecimal: 10 }], TotalUSD: 10, DryRun: true }
    if (path.endsWith('/deposits')) data = { items: [], summary: { total: 0, credited: 0, pending: 0, ignored: 0, amount: 0 } }
    return { data: { data }, status: 200 }
  })
  mocks.put.mockResolvedValue({ data: {}, status: 200 })
  mocks.post.mockImplementation(async (path: string) => {
    if (path.endsWith('/export-mnemonics')) return { data: { deposit_mnemonic: 'test deposit words', hot_mnemonic: 'test gas words' }, status: 200 }
    if (path.endsWith('/backup')) return { data: new Blob(['encrypted'], { type: 'application/json' }), status: 200 }
    if (path.endsWith('/hot-mnemonic')) return { data: { saved: true }, status: 200 }
    return { data: { data: path.endsWith('/auto-pick') ? { picked: [], probed: 2 } : {} }, status: 200 }
  })
  vi.stubGlobal('URL', Object.assign(URL, { createObjectURL: vi.fn().mockReturnValue('blob:encrypted'), revokeObjectURL: vi.fn() }))
  vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {})
})
afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals() })

function button(wrapper: VueWrapper, key: string) {
  const found = wrapper.findAll('button').find(item => item.text() === key)
  if (!found) throw new Error(`Missing button: ${key}`)
  return found
}
async function render() {
  const wrapper = mount(CryptoView, { global: { stubs: { teleport: true } } })
  await flushPromises()
  return wrapper
}
async function configTab(wrapper: VueWrapper) {
  await wrapper.get('#crypto-tab-config').trigger('click')
  await flushPromises()
}
async function verify(wrapper: VueWrapper, code = '123456') {
  await flushPromises()
  await wrapper.get('#crypto-security-code').setValue(code)
  await wrapper.get('#crypto-security-form').trigger('submit')
  await flushPromises()
}

describe('Crypto page operation wiring (mocked UI contract only)', () => {
  it('leaves polling and normal GET queries free of security prompts', async () => {
    const wrapper = await render()
    expect(wrapper.find('#crypto-security-form').exists()).toBe(false)
    await configTab(wrapper)
    await wrapper.get('.crypto-view__lockstate button').trigger('click')
    await flushPromises()
    expect(mocks.get.mock.calls.some(([path]) => path === '/admin/crypto/signer/status')).toBe(true)
    expect(mocks.get.mock.calls.some(([path]) => path.endsWith('/security/status'))).toBe(false)
    expect(mocks.post).not.toHaveBeenCalled()
  })

  it('protects config edits outside the old money-fields subset, preserving the patch', async () => {
    const wrapper = await render()
    await configTab(wrapper)
    const rpc = wrapper.findAll('input').find(input => (input.element as HTMLInputElement).value === 'https://one.example')!
    await rpc.setValue('https://changed.example')
    await rpc.trigger('change')
    await wrapper.get('.crypto-view__save-bar button').trigger('click')
    await flushPromises()
    expect(mocks.put).not.toHaveBeenCalled()
    await wrapper.get('#crypto-security-code').setValue('123456')
    await wrapper.findAll('.btn-secondary').find(btn => btn.text() === 'common.cancel')!.trigger('click')
    expect(mocks.put).not.toHaveBeenCalled()
    await flushPromises()
    await wrapper.get('.crypto-view__save-bar button').trigger('click')
    await verify(wrapper, '234567')
    expect(mocks.put).toHaveBeenCalledTimes(1)
    expect(mocks.put).toHaveBeenLastCalledWith('/admin/crypto/config', { rpc: { bsc: 'https://changed.example,https://two.example' } }, expect.objectContaining({ headers: { 'X-Crypto-TOTP': '234567' } }))
  })

  it('protects disabling and enabling an address', async () => {
    const wrapper = await render()
    await button(wrapper, 'admin.crypto.disable').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('0xDEPOSIT')
    expect(mocks.post).not.toHaveBeenCalled()
    await verify(wrapper)
    expect(mocks.post).toHaveBeenCalledWith('/admin/crypto/addresses/8/status', { status: 'disabled' }, expect.objectContaining({ headers: { 'X-Crypto-TOTP': '123456' } }))
  })

  it('requires a separate prompt for every RPC test, and cancellation stops Test all', async () => {
    const wrapper = await render()
    await configTab(wrapper)
    await button(wrapper, 'admin.crypto.rpcTestAll').trigger('click')
    await verify(wrapper)
    expect(mocks.post.mock.calls.filter(([path]) => path.endsWith('/rpc/test'))).toHaveLength(1)
    expect((wrapper.get('#crypto-security-code').element as HTMLInputElement).value).toBe('')
    await wrapper.findAll('.btn-secondary').find(btn => btn.text() === 'common.cancel')!.trigger('click')
    await flushPromises()
    expect(mocks.post.mock.calls.filter(([path]) => path.endsWith('/rpc/test'))).toHaveLength(1)
    await button(wrapper, 'admin.crypto.rpcFetch').trigger('click')
    await verify(wrapper, '234567')
    expect(mocks.post).toHaveBeenLastCalledWith('/admin/crypto/rpc/auto-pick', expect.objectContaining({ chain: 'bsc' }), expect.objectContaining({ headers: { 'X-Crypto-TOTP': '234567' } }))
  })

  it('protects CSV GET downloads and only downloads after confirmation', async () => {
    const wrapper = await render()
    await wrapper.get('#crypto-tab-deposits').trigger('click')
    await flushPromises()
    await button(wrapper, 'admin.crypto.exportCsv').trigger('click')
    await flushPromises()
    expect(mocks.get.mock.calls.some(([path]) => path.endsWith('.csv'))).toBe(false)
    await verify(wrapper)
    expect(mocks.get).toHaveBeenCalledWith('/admin/crypto/deposits.csv', expect.objectContaining({ headers: { 'X-Crypto-TOTP': '123456' }, responseType: 'blob' }))
    expect(HTMLAnchorElement.prototype.click).toHaveBeenCalledTimes(1)
  })

  it('opens plaintext export and shows both seeds only after TOTP confirmation', async () => {
    const wrapper = await render()
    await configTab(wrapper)
    await button(wrapper, 'admin.crypto.mnemonicsExportAction').trigger('click')
    await wrapper.get('#crypto-mnemonics-passphrase').setValue('vault-passphrase')
    await wrapper.get('input[type="checkbox"]').setValue(true)
    await wrapper.get('#crypto-mnemonics-form').trigger('submit')
    await flushPromises()
    expect(mocks.post).not.toHaveBeenCalled()
    await verify(wrapper)
    expect(mocks.post).toHaveBeenCalledWith('/admin/crypto/signer/export-mnemonics', { passphrase: 'vault-passphrase' }, expect.objectContaining({
      headers: { 'X-Crypto-TOTP': '123456', 'Cache-Control': 'no-store', Pragma: 'no-cache' }
    }))
    await new Promise((resolve) => setTimeout(resolve, 200))
    expect(wrapper.findAll('.crypto-mnemonics__seed')).toHaveLength(2)
    expect(wrapper.text()).not.toContain('test deposit words')
    expect(wrapper.text()).not.toContain('test gas words')
    expect(HTMLAnchorElement.prototype.click).not.toHaveBeenCalled()
  })

  it('imports only hot mnemonic, explains risks, clears it and requires signer unlock', async () => {
    const wrapper = await render()
    await configTab(wrapper)
    await button(wrapper, 'admin.crypto.hotImportAction').trigger('click')
    expect(wrapper.text()).toContain('admin.crypto.hotImportWarning')
    await wrapper.get('#crypto-wallet-passphrase').setValue('vault-passphrase')
    expect(wrapper.get('#crypto-hot-mnemonic').attributes('type')).toBe('password')
    await wrapper.get('#crypto-hot-mnemonic').setValue('hot words only')
    await wrapper.get('#crypto-wallet-form').trigger('submit')
    await verify(wrapper)
    expect(mocks.post).toHaveBeenCalledWith('/admin/crypto/signer/hot-mnemonic', { passphrase: 'vault-passphrase', hot_mnemonic: 'hot words only' }, expect.objectContaining({ headers: { 'X-Crypto-TOTP': '123456' } }))
    expect(mocks.showSuccess).toHaveBeenCalledWith('admin.crypto.hotImportDone')
    expect(wrapper.find('#crypto-signer-passphrase').exists()).toBe(true)
    await button(wrapper, 'admin.crypto.hotImportAction').trigger('click')
    expect((wrapper.get('#crypto-hot-mnemonic').element as HTMLInputElement).value).toBe('')
  })

  it('sends initialization seeds only with unlock and clears fields on cancellation', async () => {
    const wrapper = await render()
    await configTab(wrapper)
    await wrapper.get('#crypto-signer-passphrase').setValue('vault-passphrase')
    await wrapper.get('#crypto-deposit-mnemonic').setValue('deposit words')
    await wrapper.get('#crypto-initial-hot-mnemonic').setValue('hot words')
    await button(wrapper, 'admin.crypto.signerUnlockAction').trigger('click')
    await flushPromises()
    await wrapper.findAll('.btn-secondary').find(btn => btn.text() === 'common.cancel')!.trigger('click')
    await flushPromises()
    expect(mocks.post).not.toHaveBeenCalled()
    expect((wrapper.get('#crypto-signer-passphrase').element as HTMLInputElement).value).toBe('')
    expect((wrapper.get('#crypto-deposit-mnemonic').element as HTMLInputElement).value).toBe('')
    await wrapper.get('#crypto-signer-passphrase').setValue('vault-passphrase')
    await button(wrapper, 'admin.crypto.signerUnlockAction').trigger('click')
    await verify(wrapper)
    expect(mocks.post).toHaveBeenCalledWith('/admin/crypto/signer/unlock', { passphrase: 'vault-passphrase', deposit_mnemonic: '', hot_mnemonic: '' }, expect.objectContaining({ headers: { 'X-Crypto-TOTP': '123456' } }))
  })

  it('protects even a dry-run POST', async () => {
    const wrapper = await render()
    await wrapper.get('#crypto-tab-sweep').trigger('click')
    await flushPromises()
    await button(wrapper, 'admin.crypto.sweepDryRun').trigger('click')
    await verify(wrapper)
    expect(mocks.post).toHaveBeenCalledWith('/admin/crypto/sweep/run', null, expect.objectContaining({ params: { dry_run: '1' }, headers: { 'X-Crypto-TOTP': '123456' } }))
  })
})
