import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import CryptoView from '../CryptoView.vue'
import zh from '@/i18n/locales/zh/admin/crypto'
import en from '@/i18n/locales/en/admin/crypto'

const mocks = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn(), showSuccess: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: mocks }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showSuccess: mocks.showSuccess }) }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string, params?: Record<string, unknown>) => key + (params ? JSON.stringify(params) : '') }) }))
vi.mock('@/components/layout/AppLayout.vue', () => ({ default: { template: '<div><slot /></div>' } }))
enableAutoUnmount(afterEach)

const config = () => ({ enabled: true, chains: ['bsc'], rpc: { bsc: 'https://one.example,https://two.example' }, sweep_global_threshold: 100,
  sweep_min_per_address: {}, gas_topup: {}, hot_wallet_evm: '0x1234567890123456789012345678901234567890', hot_wallet_solana: '', hot_index: 0,
  sweep_auto: false, confirmations: {}, scan_range: {}, credit_min_amount: 1, credit_max_amount: 10000, credit_multiplier: 1 })
let serverConfig = config()
let walletStatus = { ok: true, unlocked: false, signing_enabled: false }

beforeEach(() => {
  vi.resetAllMocks()
  serverConfig = config()
  walletStatus = { ok: true, unlocked: false, signing_enabled: false }
  mocks.get.mockImplementation(async (path: string) => {
    if (path.endsWith('/security/status')) return { data: { enabled: true, feature_enabled: true, can_setup: true } }
    let data: unknown = []
    if (path.endsWith('/config')) data = serverConfig
    if (path.endsWith('/addresses')) data = { items: [{ id: 8, user_id: 2, address: '0xDEPOSIT', family: 'evm', status: 'active' }], total: 1, summary: { address_count: 1, deposit_count: 0, credited_total: 0, swept_total: 0 } }
    if (path.endsWith('/wallet/status')) data = { ...walletStatus }
    if (path.endsWith('/pool')) data = { free: { evm: 10 }, low_watermark: 2 }
    if (path.endsWith('/hot-wallet/address')) data = { index: 0, evm: '0xGAS', solana: '' }
    if (path.endsWith('/sweep/plan')) data = { Entries: [{ Chain: 'bsc', Address: '0xDEPOSIT', Token: 'USDT', AmountDecimal: 10 }], TotalUSD: 10, DryRun: true }
    if (path.endsWith('/deposits')) data = { items: [], summary: { total: 0, credited: 0, pending: 0, ignored: 0, amount: 0 } }
    return { data: { data }, status: 200 }
  })
  mocks.put.mockImplementation(async (_path: string, patch: Partial<ReturnType<typeof config>>) => {
    Object.assign(serverConfig, patch)
    walletStatus.signing_enabled = walletStatus.unlocked && serverConfig.enabled && Boolean(serverConfig.hot_wallet_evm)
    return { data: {}, status: 200 }
  })
  mocks.post.mockImplementation(async (path: string) => {
    if (path.endsWith('/export-mnemonics')) return { data: { deposit_mnemonic: 'test deposit words', hot_mnemonic: 'test gas words' }, status: 200 }
    if (path.endsWith('/backup')) return { data: new Blob(['encrypted'], { type: 'application/json' }), status: 200 }
    if (path.endsWith('/hot-mnemonic')) {
      walletStatus = { ok: true, unlocked: false, signing_enabled: false }
      return { data: { saved: true }, status: 200 }
    }
    if (path.endsWith('/wallet/unlock')) walletStatus = { ok: true, unlocked: true, signing_enabled: true }
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
async function render(verifyConfig = true) {
  const wrapper = mount(CryptoView, { global: { stubs: { teleport: true } } })
  await flushPromises()
  if (verifyConfig) await verify(wrapper, '111111')
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
  it('requires a fresh code before loading RPC credentials into the page', async () => {
    const wrapper = await render(false)
    expect(wrapper.find('#crypto-security-code').exists()).toBe(true)
    expect(mocks.get.mock.calls.some(([path]) => path.endsWith('/config'))).toBe(false)
    await verify(wrapper, '111111')
    expect(mocks.get).toHaveBeenCalledWith('/admin/crypto/config', expect.objectContaining({
      headers: { 'X-Crypto-TOTP': '111111' }, _retry: true
    }))
  })

  it('does not reopen an enabled 2FA prompt after config verification is cancelled', async () => {
    const wrapper = await render(false)
    expect(wrapper.find('#crypto-security-code').exists()).toBe(true)
    await wrapper.get('.crypto-security__actions .btn-secondary').trigger('click')
    await flushPromises()
    expect(wrapper.find('#crypto-security-code').exists()).toBe(false)
    expect(mocks.get.mock.calls.filter(([path]) => path.endsWith('/security/status'))).toHaveLength(2)
  })

  it('explains the new vault passphrase minimum while preserving existing vault and seed scopes', () => {
    expect(zh.crypto.unlockPassphraseHint).toContain('至少 12 个字符')
    expect(zh.crypto.unlockPassphraseHint).toContain('旧口令仍兼容')
    expect(zh.crypto.walletInitWarning).toContain('只校验非空')
    expect(en.crypto.unlockPassphraseHint).toContain('at least 12 characters')
    expect(en.crypto.walletInitWarning).toContain('original non-empty passphrase')
    for (const locale of [zh, en]) {
      expect(locale.crypto.mnemonicsDepositWarning).toMatch(/所有用户|every user/)
      expect(locale.crypto.mnemonicsHotWarning).toContain('Gas')
      expect(locale.crypto.mnemonicsHotWarning).toContain('deposit')
      expect(locale.crypto.depositMnemonicImpact).toMatch(/所有用户|every user/)
      expect(locale.crypto.hotMnemonicScope).toMatch(/只决定 Gas|only the Gas/)
      expect(JSON.stringify(locale)).not.toMatch(/signer|签名服务|独立签名策略/i)
    }
  })

  it('leaves polling and normal GET queries free of security prompts', async () => {
    const wrapper = await render()
    expect(wrapper.find('#crypto-security-form').exists()).toBe(false)
    const statusChecks = mocks.get.mock.calls.filter(([path]) => path.endsWith('/security/status')).length
    await configTab(wrapper)
    await wrapper.get('.crypto-view__lockstate button').trigger('click')
    await flushPromises()
    expect(mocks.get.mock.calls.some(([path]) => path === '/admin/crypto/wallet/status')).toBe(true)
    expect(mocks.get.mock.calls.filter(([path]) => path.endsWith('/security/status'))).toHaveLength(statusChecks)
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
    expect(mocks.post).toHaveBeenCalledWith('/admin/crypto/wallet/export-mnemonics', { passphrase: 'vault-passphrase' }, expect.objectContaining({
      headers: { 'X-Crypto-TOTP': '123456', 'Cache-Control': 'no-store', Pragma: 'no-cache' }
    }))
    await new Promise((resolve) => setTimeout(resolve, 200))
    expect(wrapper.findAll('.crypto-mnemonics__seed')).toHaveLength(2)
    expect(wrapper.text()).not.toContain('test deposit words')
    expect(wrapper.text()).not.toContain('test gas words')
    expect(HTMLAnchorElement.prototype.click).not.toHaveBeenCalled()
  })

  it('imports only hot mnemonic, explains risks, clears it and requires wallet unlock', async () => {
    walletStatus = { ok: true, unlocked: true, signing_enabled: true }
    const wrapper = await render()
    await configTab(wrapper)
    await button(wrapper, 'admin.crypto.hotImportAction').trigger('click')
    expect(wrapper.text()).toContain('admin.crypto.hotImportWarning')
    await wrapper.get('#crypto-wallet-passphrase').setValue('vault-passphrase')
    expect(wrapper.get('#crypto-hot-mnemonic').attributes('type')).toBe('password')
    await wrapper.get('#crypto-hot-mnemonic').setValue('hot words only')
    await wrapper.get('#crypto-wallet-form').trigger('submit')
    await verify(wrapper)
    expect(mocks.post).toHaveBeenCalledWith('/admin/crypto/wallet/hot-mnemonic', { passphrase: 'vault-passphrase', hot_mnemonic: 'hot words only' }, expect.objectContaining({ headers: { 'X-Crypto-TOTP': '123456' } }))
    expect(mocks.showSuccess).toHaveBeenCalledWith('admin.crypto.hotImportDone')
    expect(wrapper.find('#crypto-wallet-unlock-passphrase').exists()).toBe(true)
    await button(wrapper, 'admin.crypto.hotImportAction').trigger('click')
    expect((wrapper.get('#crypto-hot-mnemonic').element as HTMLInputElement).value).toBe('')
  })

  it('closes pending verification on tab navigation before a wallet write is authorized', async () => {
    const wrapper = await render()
    await configTab(wrapper)
    await button(wrapper, 'admin.crypto.backupAction').trigger('click')
    await wrapper.get('#crypto-wallet-passphrase').setValue('vault-passphrase')
    await wrapper.get('#crypto-wallet-form').trigger('submit')
    await flushPromises()

    expect(wrapper.find('#crypto-security-form').exists()).toBe(true)
    await wrapper.get('#crypto-tab-addresses').trigger('click')
    await flushPromises()
    expect(wrapper.find('#crypto-security-form').exists()).toBe(false)
    expect(mocks.post.mock.calls.some(([path]) => path.endsWith('/backup'))).toBe(false)
    expect(HTMLAnchorElement.prototype.click).not.toHaveBeenCalled()
  })

  it.each([
    { ok: true, unlocked: false, signing_enabled: false, label: 'walletLocked', disabled: true },
    { ok: true, unlocked: true, signing_enabled: true, label: 'walletReady', disabled: false },
    { ok: true, unlocked: true, signing_enabled: false, label: 'walletNeedsConfig', disabled: false },
    { ok: false, unlocked: true, signing_enabled: true, label: 'walletUnavailable', disabled: true }
  ])('shows $label and gates address derivation for wallet state $ok/$unlocked/$signing_enabled', async (state) => {
    walletStatus = state
    const wrapper = await render()
    await configTab(wrapper)
    expect(wrapper.get('.crypto-view__lockstate').text()).toContain(`admin.crypto.${state.label}`)
    expect(button(wrapper, 'admin.crypto.hotAddressFill').attributes('disabled') !== undefined).toBe(state.disabled)
    expect(mocks.get.mock.calls.some(([path]) => path === '/admin/crypto/hot-wallet')).toBe(!state.disabled)
    expect(wrapper.text()).not.toMatch(/signer|Approval|groupSigner|signerSocket/i)
    expect(wrapper.find('input[type="number"][name="hot_index"]').exists()).toBe(false)
    if (!state.disabled) {
      await button(wrapper, 'admin.crypto.hotAddressFill').trigger('click')
      await flushPromises()
      expect(mocks.get).toHaveBeenCalledWith('/admin/crypto/hot-wallet/address')
      expect(wrapper.get('input[placeholder="0x…"]').element).toHaveProperty('value', '0xGAS')
    }
    await wrapper.get('#crypto-tab-sweep').trigger('click')
    expect(button(wrapper, 'admin.crypto.sweepRun').attributes('disabled') !== undefined).toBe(!state.ok || !state.unlocked || !state.signing_enabled)
  })

  it('can derive and save a destination on first setup before signing is enabled', async () => {
    serverConfig.hot_wallet_evm = ''
    walletStatus = { ok: true, unlocked: true, signing_enabled: false }
    const wrapper = await render()
    await configTab(wrapper)
    expect(wrapper.get('.crypto-view__lockstate').text()).toContain('admin.crypto.walletNeedsConfig')
    expect(wrapper.text()).toContain('admin.crypto.walletNotReady')
    await button(wrapper, 'admin.crypto.hotAddressFill').trigger('click')
    await flushPromises()
    expect(wrapper.get('input[placeholder="0x…"]').element).toHaveProperty('value', '0xGAS')
    await wrapper.get('.crypto-view__save-bar button').trigger('click')
    await verify(wrapper)
    expect(mocks.put).toHaveBeenLastCalledWith('/admin/crypto/config', { hot_wallet_evm: '0xGAS' }, expect.anything())
    expect(wrapper.get('.crypto-view__lockstate').text()).toContain('admin.crypto.walletReady')
    await wrapper.get('#crypto-tab-sweep').trigger('click')
    expect(button(wrapper, 'admin.crypto.sweepRun').attributes('disabled')).toBeUndefined()
  })

  it('allows an existing non-empty vault passphrase shorter than 12 characters', async () => {
    const wrapper = await render()
    await configTab(wrapper)
    const input = wrapper.get('#crypto-wallet-unlock-passphrase')
    expect(input.attributes('minlength')).toBeUndefined()
    await input.setValue('old')
    await button(wrapper, 'admin.crypto.walletUnlockAction').trigger('click')
    await verify(wrapper)
    expect(mocks.post).toHaveBeenCalledWith('/admin/crypto/wallet/unlock', {
      passphrase: 'old', deposit_mnemonic: '', hot_mnemonic: ''
    }, expect.objectContaining({ headers: { 'X-Crypto-TOTP': '123456' } }))
  })

  it('shows unavailable status and prevents derivation when wallet status cannot be fetched', async () => {
    const normalGet = mocks.get.getMockImplementation()!
    mocks.get.mockImplementation((path: string) => path.endsWith('/wallet/status')
      ? Promise.reject(new Error('Unavailable')) : normalGet(path))
    const wrapper = await render()
    await configTab(wrapper)
    expect(wrapper.get('.crypto-view__lockstate').text()).toContain('admin.crypto.walletUnavailable')
    expect(button(wrapper, 'admin.crypto.hotAddressFill').attributes('disabled')).toBeDefined()
  })

  it('keeps the Gas derivation index out of config writes, even if an older response contains another index', async () => {
    serverConfig.hot_index = 7
    const wrapper = await render()
    await configTab(wrapper)
    expect(wrapper.text()).toContain('admin.crypto.hotIndexHint')
    const rpc = wrapper.findAll('input').find(input => (input.element as HTMLInputElement).value === 'https://one.example')!
    await rpc.setValue('https://changed.example')
    await rpc.trigger('change')
    await wrapper.get('.crypto-view__save-bar button').trigger('click')
    await verify(wrapper)
    expect(mocks.put).toHaveBeenLastCalledWith('/admin/crypto/config', {
      rpc: { bsc: 'https://changed.example,https://two.example' }
    }, expect.anything())
  })

  it('downloads the encrypted wallet backup only after fresh 2FA and clears the passphrase', async () => {
    const wrapper = await render()
    await configTab(wrapper)
    await button(wrapper, 'admin.crypto.backupAction').trigger('click')
    expect(wrapper.find('#crypto-hot-mnemonic').exists()).toBe(false)
    await wrapper.get('#crypto-wallet-passphrase').setValue('vault-passphrase')
    await wrapper.get('#crypto-wallet-form').trigger('submit')
    await flushPromises()
    expect(mocks.post).not.toHaveBeenCalled()
    await verify(wrapper)
    expect(mocks.post).toHaveBeenCalledWith('/admin/crypto/wallet/backup', { passphrase: 'vault-passphrase' }, expect.objectContaining({
      headers: { 'X-Crypto-TOTP': '123456' }, responseType: 'blob', _retry: true
    }))
    expect(HTMLAnchorElement.prototype.click).toHaveBeenCalledTimes(1)
    expect(mocks.showSuccess).toHaveBeenCalledWith('admin.crypto.backupDone')
    await button(wrapper, 'admin.crypto.backupAction').trigger('click')
    expect(wrapper.get('#crypto-wallet-passphrase').element).toHaveProperty('value', '')
    await wrapper.findAll('.btn-secondary').find(btn => btn.text() === 'common.cancel')!.trigger('click')
    await flushPromises()
    expect(mocks.post).toHaveBeenCalledTimes(1)
  })

  it('sends initialization seeds only with unlock and clears fields on cancellation', async () => {
    const wrapper = await render()
    await configTab(wrapper)
    await wrapper.get('#crypto-wallet-unlock-passphrase').setValue('vault-passphrase')
    await wrapper.get('#crypto-deposit-mnemonic').setValue('deposit words')
    await wrapper.get('#crypto-initial-hot-mnemonic').setValue('hot words')
    await button(wrapper, 'admin.crypto.walletUnlockAction').trigger('click')
    await flushPromises()
    await wrapper.findAll('.btn-secondary').find(btn => btn.text() === 'common.cancel')!.trigger('click')
    await flushPromises()
    expect(mocks.post).not.toHaveBeenCalled()
    expect((wrapper.get('#crypto-wallet-unlock-passphrase').element as HTMLInputElement).value).toBe('')
    expect((wrapper.get('#crypto-deposit-mnemonic').element as HTMLInputElement).value).toBe('')
    await wrapper.get('#crypto-wallet-unlock-passphrase').setValue('vault-passphrase')
    await button(wrapper, 'admin.crypto.walletUnlockAction').trigger('click')
    await verify(wrapper)
    expect(mocks.post).toHaveBeenCalledWith('/admin/crypto/wallet/unlock', { passphrase: 'vault-passphrase', deposit_mnemonic: '', hot_mnemonic: '' }, expect.objectContaining({ headers: { 'X-Crypto-TOTP': '123456' } }))
    expect(wrapper.get('.crypto-view__lockstate').text()).toContain('admin.crypto.walletReady')
    expect(wrapper.find('#crypto-wallet-unlock-passphrase').exists()).toBe(false)
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
