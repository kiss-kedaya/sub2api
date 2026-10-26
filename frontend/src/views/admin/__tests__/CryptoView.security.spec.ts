import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { clearCryptoGrant } from '@/composables/cryptoGrant'
import { enableAutoUnmount, flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import CryptoView from '../CryptoView.vue'
import zh from '@/i18n/locales/zh/admin/crypto'
import en from '@/i18n/locales/en/admin/crypto'

const mocks = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn(), showSuccess: vi.fn(), showError: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: mocks }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showSuccess: mocks.showSuccess, showError: mocks.showError }) }))
vi.mock('@/stores', () => ({ useAppStore: () => ({ showSuccess: mocks.showSuccess, showError: mocks.showError }) }))
vi.mock('@/composables/useClipboard', () => ({ useClipboard: () => ({ copyToClipboard: vi.fn().mockResolvedValue(true) }) }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string, params?: Record<string, unknown>) => key + (params ? JSON.stringify(params) : '') }) }))
vi.mock('@/components/layout/AppLayout.vue', () => ({ default: { template: '<div><slot /></div>' } }))
vi.mock('@/components/crypto/CryptoWalletSecurityPanel.vue', () => ({ default: { name: 'CryptoWalletSecurityPanel', emits: ['change', 'recovered'], template: '<div />' } }))
enableAutoUnmount(afterEach)

const grantCredential = 'test-payment-grant-credential-32-bytes-minimum'
const grantResponse = () => ({ data: { data: { credential: grantCredential, expires_at: new Date(Date.now() + 300000).toISOString(), expires_in: 300 } } })

const config = () => ({ enabled: true, chains: ['bsc'], rpc: { bsc: 'https://one.example,https://two.example' }, sweep_global_threshold: 100,
  sweep_min_per_address: {}, gas_topup: {}, hot_wallet_evm: '0x1234567890123456789012345678901234567890', hot_wallet_solana: '', hot_index: 0,
  sweep_auto: false, confirmations: {}, scan_range: {}, credit_min_amount: 1, credit_max_amount: 10000, credit_multiplier: 1 })
let serverConfig = config()
let walletStatus = { ok: true, configured: false, ready: false, enabled: false }

beforeEach(() => {
  vi.resetAllMocks()
  clearCryptoGrant()
  localStorage.setItem('auth_token', 'test-administrator-jwt-session')
  serverConfig = config()
  walletStatus = { ok: true, configured: false, ready: false, enabled: false }
  mocks.get.mockImplementation(async (path: string) => {
    if (path === '/user/bark') return { data: { data: { configured: true, enabled: true } } }
    if (path.endsWith('/security/status')) return { data: { enabled: true, feature_enabled: true, can_setup: true } }
    let data: unknown = []
    if (path.endsWith('/config')) data = JSON.parse(JSON.stringify({ ...serverConfig, rpc: {}, rpc_configured: Object.keys(serverConfig.rpc) }))
    if (path.endsWith('/config/private')) data = JSON.parse(JSON.stringify(serverConfig))
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
    walletStatus.enabled = walletStatus.ready && serverConfig.enabled && Boolean(serverConfig.hot_wallet_evm)
    return { data: {}, status: 200 }
  })
  mocks.post.mockImplementation(async (path: string) => {
    if (path.endsWith('/security/verify')) return grantResponse()
    if (path.endsWith('/operation-intent')) return { data: { accepted: true }, status: 200 }
    if (path.endsWith('/export-mnemonics')) return { data: { deposit_mnemonic: 'test deposit words', hot_mnemonic: 'test gas words' }, status: 200 }
    if (path.endsWith('/backup')) return { data: new Blob(['encrypted'], { type: 'application/json' }), status: 200 }
    if (path.endsWith('/hot-mnemonic')) {
      walletStatus = { ok: true, configured: true, ready: true, enabled: true }
      return { data: { saved: true }, status: 200 }
    }
    if (path.endsWith('/wallet/initialize')) walletStatus = { ok: true, configured: true, ready: true, enabled: true }
    return { data: { data: path.endsWith('/auto-pick') ? { picked: [], probed: 2 } : {} }, status: 200 }
  })
  vi.stubGlobal('URL', Object.assign(URL, { createObjectURL: vi.fn().mockReturnValue('blob:encrypted'), revokeObjectURL: vi.fn() }))
  vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {})
})
afterEach(() => { clearCryptoGrant(); vi.restoreAllMocks(); vi.unstubAllGlobals() })

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
  if (!wrapper.find('#crypto-security-code').exists()) return
  await wrapper.get('#crypto-security-code').setValue(code)
  await wrapper.get('#crypto-security-form').trigger('submit')
  await flushPromises()
}
async function manageRPC(wrapper: VueWrapper) {
  await button(wrapper, 'admin.crypto.rpcManage').trigger('click')
  await verify(wrapper, '111111')
}

describe('Crypto page operation wiring (mocked UI contract only)', () => {
  it('refreshes an old sweep preview before asking for execution verification', async () => {
    walletStatus = { ok: true, configured: true, ready: true, enabled: true }
    let amount = 10
    const normalGet = mocks.get.getMockImplementation()!
    mocks.get.mockImplementation((path: string, ...args: unknown[]) => path.endsWith('/sweep/plan')
      ? Promise.resolve({ data: { data: { Entries: [{ Chain: 'bsc', Address: '0xDEPOSIT', Token: 'USDT', AmountDecimal: amount }], TotalUSD: amount, DryRun: true } } })
      : normalGet(path, ...args))
    const wrapper = await render()
    await wrapper.get('#crypto-tab-sweep').trigger('click')
    await flushPromises()
    await button(wrapper, 'admin.crypto.loadPlan').trigger('click')
    await flushPromises()
    amount = 12 // a deposit arrives after the operator's previous preview
    await button(wrapper, 'admin.crypto.sweepRun').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('"total":"12.00"')
    expect(mocks.post.mock.calls.filter(([path]) => path.endsWith('/sweep/run'))).toHaveLength(0)
    await verify(wrapper)
    expect(mocks.post).toHaveBeenCalledWith('/admin/crypto/sweep/run', null, expect.objectContaining({ params: { dry_run: '0' } }))
  })

  it('keeps a confirmation and Bark intent for every sweep even while verification is cached', async () => {
    walletStatus = { ok: true, configured: true, ready: true, enabled: true }
    const wrapper = await render()
    await wrapper.get('#crypto-tab-sweep').trigger('click')
    await flushPromises()
    await button(wrapper, 'admin.crypto.sweepRun').trigger('click')
    await verify(wrapper)
    await button(wrapper, 'admin.crypto.sweepRun').trigger('click')
    await flushPromises()
    expect(wrapper.find('#crypto-security-code').exists()).toBe(false)
    expect(mocks.post.mock.calls.filter(([path]) => path.endsWith('/security/verify'))).toHaveLength(1)
    expect(mocks.post.mock.calls.filter(([path]) => path.endsWith('/operation-intent'))).toHaveLength(2)
    expect(mocks.post.mock.calls.filter(([path]) => path.endsWith('/sweep/run'))).toHaveLength(2)
    expect(mocks.post.mock.calls.filter(([path]) => path.endsWith('/sweep/run'))[0][2]).toEqual(expect.objectContaining({
      timeout: 180000, params: { dry_run: '0' }
    }))
  })

  it('keeps the channel paused after security recovery without discarding edits or opening another TOTP prompt', async () => {
    walletStatus = { ok: true, configured: true, ready: true, enabled: true }
    const wrapper = await render()
    await configTab(wrapper)
    expect(wrapper.get('.crypto-view__header-status').text()).toContain('admin.crypto.channelActive')
    await manageRPC(wrapper)
    const rpc = wrapper.findAll('input').find(input => (input.element as HTMLInputElement).value === 'https://one.example')!
    await rpc.setValue('https://edited.example')
    await rpc.trigger('change')
    const configReads = mocks.get.mock.calls.filter(([path]) => path.endsWith('/config')).length
    const panel = wrapper.findComponent({ name: 'CryptoWalletSecurityPanel' })
    panel.vm.$emit('change', { enabled: true, initialized: true, locked: true, deposits_paused: true, alert_pending: true })
    await flushPromises()
    expect(wrapper.get('.crypto-view__header-status').text()).toContain('admin.crypto.channelPaused')
    serverConfig.enabled = false
    walletStatus.enabled = false
    panel.vm.$emit('change', { enabled: true, initialized: true, locked: false, deposits_paused: false, alert_pending: false })
    panel.vm.$emit('recovered')
    await flushPromises()
    expect(wrapper.get('.crypto-view__header-status').text()).toContain('admin.crypto.channelPaused')
    expect((rpc.element as HTMLInputElement).value).toBe('https://edited.example')
    expect(wrapper.find('#crypto-security-code').exists()).toBe(false)
    expect(mocks.get.mock.calls.filter(([path]) => path.endsWith('/config'))).toHaveLength(configReads)
    expect(mocks.put).not.toHaveBeenCalled()
    expect(mocks.post.mock.calls.filter(([path]) => !path.endsWith('/security/verify'))).toHaveLength(0)
  })

  it('selects supported networks and saves them only after fresh 2FA without changing pricing', async () => {
    const wrapper = await render()
    await configTab(wrapper)
    for (const chain of ['ethereum', 'polygon', 'plasma', 'base', 'xlayer']) {
      const option = wrapper.get(`[data-testid="crypto-chain-${chain}"]`)
      expect((option.element as HTMLInputElement).checked).toBe(false)
      await option.setValue(true)
    }
    expect(wrapper.find('[data-testid="crypto-chain-solana"]').exists()).toBe(false)
    expect(wrapper.findAll('[data-testid^="rpc-configured-"]')).toHaveLength(6)
    expect(wrapper.find('.crypto-view__rpc-input').exists()).toBe(false)
    await wrapper.get('.crypto-view__save-bar button').trigger('click')
    await flushPromises()
    expect(mocks.put).not.toHaveBeenCalled()
    await verify(wrapper, '234567')
    expect(mocks.put).toHaveBeenCalledOnce()
    const patch = mocks.put.mock.calls[0][1]
    expect(patch.chains).toEqual(['bsc', 'ethereum', 'polygon', 'plasma', 'base', 'xlayer'])
    expect(patch).not.toHaveProperty('credit_multiplier')
    expect(patch).not.toHaveProperty('enabled')
    expect(patch).not.toHaveProperty('rpc')
  })

  it('loads ordinary configuration without a prompt and reads private RPC only after explicit fresh verification', async () => {
    const wrapper = await render()
    expect(wrapper.find('#crypto-security-code').exists()).toBe(false)
    expect(mocks.get).toHaveBeenCalledWith('/admin/crypto/config', { headers: {}, _retry: true })
    expect(mocks.get.mock.calls.some(([path]) => path.endsWith('/security/status'))).toBe(false)
    await configTab(wrapper)
    expect(wrapper.find('.crypto-view__rpc-input').exists()).toBe(false)
    expect(wrapper.get('[data-testid="rpc-configured-bsc"]').text()).toContain('admin.crypto.rpcConfigured')
    await button(wrapper, 'admin.crypto.rpcManage').trigger('click')
    await flushPromises()
    expect(mocks.get.mock.calls.some(([path]) => path.endsWith('/config/private'))).toBe(false)
    await verify(wrapper, '111111')
    expect(mocks.get).toHaveBeenCalledWith('/admin/crypto/config/private', expect.objectContaining({
      headers: { 'X-Crypto-Grant': grantCredential }, _retry: true
    }))
    expect(wrapper.findAll('.crypto-view__rpc-input')).toHaveLength(2)
    await wrapper.get('.crypto-view__save-bar button').trigger('click')
    expect(mocks.put).not.toHaveBeenCalled()
  })

  it('does not reopen an enabled 2FA prompt after config verification is cancelled', async () => {
    const wrapper = await render()
    await configTab(wrapper)
    await button(wrapper, 'admin.crypto.rpcManage').trigger('click')
    await flushPromises()
    expect(wrapper.find('#crypto-security-code').exists()).toBe(true)
    await wrapper.get('.crypto-security__actions .btn-secondary').trigger('click')
    await flushPromises()
    expect(wrapper.find('#crypto-security-code').exists()).toBe(false)
    expect(mocks.get.mock.calls.filter(([path]) => path.endsWith('/security/status'))).toHaveLength(1)
    expect(wrapper.find('.crypto-view__rpc-input').exists()).toBe(false)
    await button(wrapper, 'admin.crypto.rpcManage').trigger('click')
    await flushPromises()
    expect(wrapper.find('#crypto-security-code').exists()).toBe(true)
    await verify(wrapper, '222222')
    expect(mocks.get).toHaveBeenCalledWith('/admin/crypto/config/private', expect.objectContaining({
      headers: { 'X-Crypto-Grant': grantCredential }, _retry: true
    }))
  })

  it.each([
    { id: 'crypto-sweep-threshold', value: 200, key: 'sweep_global_threshold', expected: 200 },
    { id: 'crypto-credit-min', value: 2, key: 'credit_min_amount', expected: 2 },
    { id: 'crypto-credit-max', value: 9000, key: 'credit_max_amount', expected: 9000 },
    { id: 'crypto-sweep-min-bsc', value: 5, key: 'sweep_min_per_address', expected: { bsc: 5 } },
    { id: 'crypto-scan-range-bsc', value: 100, key: 'scan_range', expected: { bsc: 100 } }
  ])('saves ordinary parameter $id without 2FA and preserves hidden RPC credentials', async ({ id, value, key, expected }) => {
    const wrapper = await render()
    await configTab(wrapper)
    await wrapper.get(`[data-testid="${id}"]`).setValue(value)
    await wrapper.get('.crypto-view__save-bar button').trigger('click')
    await flushPromises()
    expect(mocks.put).toHaveBeenCalledOnce()
    expect(mocks.put).toHaveBeenCalledWith('/admin/crypto/config', { [key]: expected }, { headers: {}, _retry: true })
    expect(mocks.put.mock.calls[0][1]).not.toHaveProperty('rpc')
    expect(mocks.put.mock.calls[0][1]).not.toHaveProperty('rpc_configured')
    expect(serverConfig.rpc).toEqual({ bsc: 'https://one.example,https://two.example' })
    expect(wrapper.find('#crypto-security-code').exists()).toBe(false)
    expect(mocks.get.mock.calls.some(([path]) => path.endsWith('/config/private') || path.endsWith('/security/status'))).toBe(false)
  })

  it('retains an unsaved ordinary edit when private RPC credentials are loaded', async () => {
    const wrapper = await render()
    await configTab(wrapper)
    await wrapper.get('[data-testid="crypto-sweep-threshold"]').setValue(250)
    await manageRPC(wrapper)
    expect(wrapper.get('[data-testid="crypto-sweep-threshold"]').element).toHaveProperty('value', '250')
    await wrapper.get('.crypto-view__save-bar button').trigger('click')
    await flushPromises()
    expect(mocks.put).toHaveBeenCalledWith('/admin/crypto/config', { sweep_global_threshold: 250 }, { headers: {}, _retry: true })
    expect(wrapper.find('#crypto-security-code').exists()).toBe(false)
  })

  it('requires fresh verification for mixed ordinary and sensitive edits while leaving hidden RPC untouched', async () => {
    const wrapper = await render()
    await configTab(wrapper)
    await wrapper.get('[data-testid="crypto-credit-min"]').setValue(2)
    await wrapper.get('[data-testid="crypto-chain-base"]').setValue(true)
    await wrapper.get('.crypto-view__save-bar button').trigger('click')
    await flushPromises()
    expect(mocks.put).not.toHaveBeenCalled()
    await verify(wrapper, '234567')
    expect(mocks.put).toHaveBeenCalledWith('/admin/crypto/config', { chains: ['bsc', 'base'], credit_min_amount: 2 }, expect.objectContaining({ headers: { 'X-Crypto-Grant': grantCredential } }))
    expect(serverConfig.rpc).toEqual({ bsc: 'https://one.example,https://two.example' })
  })

  it('shows a public read failure with a plain retry instead of opening a verification dialog', async () => {
    const normalGet = mocks.get.getMockImplementation()!
    let unavailable = true
    mocks.get.mockImplementation((path: string) => path.endsWith('/config') && unavailable
      ? Promise.reject(new Error('Read unavailable')) : normalGet(path))
    const wrapper = await render()
    await configTab(wrapper)
    expect(wrapper.text()).toContain('Read unavailable')
    expect(wrapper.find('#crypto-security-code').exists()).toBe(false)
    unavailable = false
    const refresh = wrapper.findAll('button').find(item => item.text() === 'common.refresh')!
    await refresh.trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-testid="crypto-sweep-threshold"]').exists()).toBe(true)
    expect(mocks.get.mock.calls.some(([path]) => path.endsWith('/security/status'))).toBe(false)
  })

  it('does not expose RPC returned accidentally by an older public endpoint', async () => {
    const normalGet = mocks.get.getMockImplementation()!
    mocks.get.mockImplementation((path: string) => path.endsWith('/config')
      ? Promise.resolve({ data: { data: JSON.parse(JSON.stringify(serverConfig)) } }) : normalGet(path))
    const wrapper = await render()
    await configTab(wrapper)
    expect(wrapper.find('.crypto-view__rpc-input').exists()).toBe(false)
    expect(wrapper.html()).not.toContain('https://one.example')
    await wrapper.get('[data-testid="crypto-sweep-threshold"]').setValue(250)
    await wrapper.get('.crypto-view__save-bar button').trigger('click')
    await flushPromises()
    expect(mocks.put.mock.calls[0][1]).not.toHaveProperty('rpc')
  })

  it('explains managed encryption and preserves the separate deposit and Gas seed risks', () => {
    expect(zh.crypto.walletManagedHint).toContain('无需额外口令或手动解锁')
    expect(en.crypto.walletManagedHint).toContain('No extra password or manual unlock')
    for (const locale of [zh,en]) {
      expect(locale.crypto.walletInitWarning).toMatch(/所有用户|every user/)
      expect(locale.crypto.hotImportWarning).toContain('Gas')
      expect(locale.crypto.walletInitWarning).not.toMatch(/12|passphrase|Vault/)
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

  it('reads public gas-wallet balance without a TOTP prompt', async () => {
    walletStatus = { ok: true, configured: true, ready: true, enabled: true }
    const wrapper = await render()
    await configTab(wrapper)
    const hotPath = '/admin/crypto/hot-wallet'
    // Funding information loads on entry instead of hiding behind Health.
    expect(mocks.get.mock.calls.some(([path]) => path === hotPath)).toBe(true)
    expect(wrapper.find('[data-testid="crypto-gas-wallet"]').exists()).toBe(true)
    const health = wrapper.findAll('details').find(item => item.text().includes('admin.crypto.healthTitle'))!
    const refresh = health.findAll('.btn-secondary').find(btn => btn.text() === 'common.refresh')!
    expect(refresh.attributes('disabled')).toBeUndefined()
    await refresh.trigger('click')
    await flushPromises()
    expect(mocks.get).toHaveBeenCalledWith(hotPath, expect.objectContaining({
      headers: {}, _retry: true
    }))
    expect(wrapper.find('#crypto-security-code').exists()).toBe(false)
  })

  it('reuses the verified payment credential for RPC edits while preserving the patch', async () => {
    const wrapper = await render()
    await configTab(wrapper)
    await manageRPC(wrapper)
    const rpc = wrapper.findAll('input').find(input => (input.element as HTMLInputElement).value === 'https://one.example')!
    await rpc.setValue('https://changed.example')
    await rpc.trigger('change')
    await wrapper.get('.crypto-view__save-bar button').trigger('click')
    await flushPromises()
    expect(wrapper.find('#crypto-security-code').exists()).toBe(false)
    expect(mocks.post.mock.calls.filter(([path]) => path.endsWith('/security/verify'))).toHaveLength(1)
    expect(mocks.put).toHaveBeenCalledTimes(1)
    expect(mocks.put).toHaveBeenLastCalledWith('/admin/crypto/config', { rpc: { bsc: 'https://changed.example,https://two.example' } }, expect.objectContaining({ headers: { 'X-Crypto-Grant': grantCredential } }))
  })

  it.each([
    { family: 'evm', status: 'active', toggle: 'disable', key: 'a'.repeat(64), format: 'hex' },
    { family: 'evm', status: 'disabled', toggle: 'enable', key: 'a'.repeat(64), format: 'hex' },
    { family: 'solana', status: 'disabled', toggle: 'enable', key: '2'.repeat(88), format: 'base58' }
  ])('exports the selected $status $family address with its button before $toggle', async ({ family, status, toggle, key, format }) => {
    const normalGet = mocks.get.getMockImplementation()!
    mocks.get.mockImplementation((path: string, ...args: unknown[]) => path.endsWith('/addresses')
      ? Promise.resolve({ data: { data: { items: [{ id: 28, user_id: 2, address: 'SELECTED_ADDRESS', family, status }], total: 1 } } })
      : normalGet(path, ...args))
    const normalPost = mocks.post.getMockImplementation()!
    mocks.post.mockImplementation((path: string, ...args: unknown[]) => path.endsWith('/export-private-key')
      ? Promise.resolve({ data: { address_id: 28, address: 'SELECTED_ADDRESS', family, derivation_index: 3, private_key: key, format } })
      : normalPost(path, ...args))
    const wrapper = await render()
    const actions = wrapper.get('#crypto-panel-addresses tbody tr').findAll('td').at(-1)!.findAll('button')
    expect(actions.map(action => action.text())).toEqual(['admin.crypto.privateKeyExportAction', `admin.crypto.${toggle}`])
    await actions[0].trigger('click')
    await flushPromises()
    expect(mocks.post.mock.calls.filter(([path]) => path.endsWith('/export-private-key'))).toHaveLength(0)
    await verify(wrapper)
    expect(mocks.post).toHaveBeenCalledWith('/admin/crypto/addresses/28/export-private-key', {}, expect.objectContaining({
      headers: { 'X-Crypto-Grant': grantCredential, 'Cache-Control': 'no-store', Pragma: 'no-cache' }
    }))
    expect(wrapper.get('[data-testid="private-key-address"]').text()).toBe('SELECTED_ADDRESS')
    expect(wrapper.get('[data-testid="private-key-value"]').text()).not.toContain(key)
    await wrapper.get('button[aria-pressed]').trigger('click')
    expect(wrapper.get('[data-testid="private-key-value"]').text()).toBe(key)
    await configTab(wrapper)
    expect(wrapper.find('[data-testid="private-key-value"]').exists()).toBe(false)
    expect(wrapper.text()).not.toContain(key)
  })

  it('protects disabling and enabling an address', async () => {
    const wrapper = await render()
    await button(wrapper, 'admin.crypto.disable').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('0xDEPOSIT')
    expect(mocks.post).not.toHaveBeenCalled()
    await verify(wrapper)
    expect(mocks.post).toHaveBeenCalledWith('/admin/crypto/addresses/8/status', { status: 'disabled' }, expect.objectContaining({ headers: { 'X-Crypto-Grant': grantCredential } }))
  })

  it('reuses the same fixed payment credential for a batch of RPC tests and discovery', async () => {
    const wrapper = await render()
    await configTab(wrapper)
    await manageRPC(wrapper)
    await button(wrapper, 'admin.crypto.rpcTestAll').trigger('click')
    await flushPromises()
    expect(mocks.post.mock.calls.filter(([path]) => path.endsWith('/rpc/test'))).toHaveLength(2)
    expect(wrapper.find('#crypto-security-code').exists()).toBe(false)
    await button(wrapper, 'admin.crypto.rpcFetch').trigger('click')
    await verify(wrapper, '234567')
    expect(mocks.post.mock.calls.filter(([path]) => path.endsWith('/security/verify'))).toHaveLength(1)
    expect(mocks.post).toHaveBeenLastCalledWith('/admin/crypto/rpc/auto-pick', expect.objectContaining({ chain: 'bsc' }), expect.objectContaining({ headers: { 'X-Crypto-Grant': grantCredential } }))
  })

  it('protects CSV GET downloads and only downloads after confirmation', async () => {
    const wrapper = await render()
    await wrapper.get('#crypto-tab-deposits').trigger('click')
    await flushPromises()
    await button(wrapper, 'admin.crypto.exportCsv').trigger('click')
    await flushPromises()
    expect(mocks.get.mock.calls.some(([path]) => path.endsWith('.csv'))).toBe(false)
    await verify(wrapper)
    expect(mocks.get).toHaveBeenCalledWith('/admin/crypto/deposits.csv', expect.objectContaining({ headers: { 'X-Crypto-Grant': grantCredential }, responseType: 'blob' }))
    expect(HTMLAnchorElement.prototype.click).toHaveBeenCalledTimes(1)
  })

  it('opens plaintext export and shows both seeds only after TOTP confirmation', async () => {
    walletStatus = { ok: true, configured: true, ready: true, enabled: true }
    const wrapper = await render()
    await configTab(wrapper)
    await button(wrapper, 'admin.crypto.mnemonicsExportAction').trigger('click')
    await wrapper.get('#crypto-mnemonics-form input[type="checkbox"]').setValue(true)
    await wrapper.get('#crypto-mnemonics-form').trigger('submit')
    await flushPromises()
    expect(mocks.post.mock.calls.filter(([path]) => path.endsWith('/export-mnemonics'))).toHaveLength(0)
    expect(mocks.post).toHaveBeenCalledWith('/admin/crypto/security/operation-intent', { action: 'export_mnemonics' }, expect.objectContaining({ _retry: true }))
    await verify(wrapper)
    expect(mocks.post).toHaveBeenCalledWith('/admin/crypto/wallet/export-mnemonics', {}, expect.objectContaining({
      headers: { 'X-Crypto-Grant': grantCredential, 'Cache-Control': 'no-store', Pragma: 'no-cache' }
    }))
    await new Promise((resolve) => setTimeout(resolve, 200))
    expect(wrapper.findAll('.crypto-mnemonics__seed')).toHaveLength(2)
    expect(wrapper.text()).not.toContain('test deposit words')
    expect(wrapper.text()).not.toContain('test gas words')
    expect(HTMLAnchorElement.prototype.click).not.toHaveBeenCalled()
  })

  it('imports only hot mnemonic, explains risks, clears it and requires wallet unlock', async () => {
    walletStatus = { ok: true, configured: true, ready: true, enabled: true }
    const wrapper = await render()
    await configTab(wrapper)
    await button(wrapper, 'admin.crypto.hotImportAction').trigger('click')
    expect(wrapper.text()).toContain('admin.crypto.hotImportWarning')
    expect(wrapper.get('#crypto-hot-mnemonic').attributes('type')).toBe('password')
    await wrapper.get('#crypto-hot-mnemonic').setValue('hot words only')
    await wrapper.get('#crypto-wallet-form').trigger('submit')
    await verify(wrapper)
    expect(mocks.post).toHaveBeenCalledWith('/admin/crypto/wallet/hot-mnemonic', { hot_mnemonic: 'hot words only' }, expect.objectContaining({ headers: { 'X-Crypto-Grant': grantCredential } }))
    expect(mocks.showSuccess).toHaveBeenCalledWith('admin.crypto.hotImportDone')
    expect(wrapper.find('#crypto-wallet-unlock-passphrase').exists()).toBe(false)
    await button(wrapper, 'admin.crypto.hotImportAction').trigger('click')
    expect((wrapper.get('#crypto-hot-mnemonic').element as HTMLInputElement).value).toBe('')
  })

  it('closes pending verification on tab navigation before a wallet write is authorized', async () => {
    walletStatus={ok:true,configured:true,ready:true,enabled:true}
    const wrapper=await render(); await configTab(wrapper)
    await button(wrapper,'admin.crypto.hotImportAction').trigger('click')
    await wrapper.get('#crypto-hot-mnemonic').setValue('private words')
    await wrapper.get('#crypto-wallet-form').trigger('submit'); await flushPromises()
    expect(wrapper.find('#crypto-security-form').exists()).toBe(true)
    await wrapper.get('#crypto-tab-addresses').trigger('click'); await flushPromises()
    expect(wrapper.find('#crypto-security-form').exists()).toBe(false)
    expect(mocks.post).not.toHaveBeenCalled()
  })

  it.each([
    { ok: true, configured: false, ready: false, enabled: false, label: 'walletNotConfigured', disabled: true },
    { ok: true, configured: true, ready: true, enabled: true, label: 'walletReady', disabled: false },
    { ok: true, configured: true, ready: true, enabled: false, label: 'walletNeedsConfig', disabled: false },
    { ok: false, configured: true, ready: true, enabled: true, label: 'walletUnavailable', disabled: true }
  ])('shows $label and gates address derivation for wallet state $ok/$ready/$enabled', async (state) => {
    walletStatus = state
    const wrapper = await render()
    await configTab(wrapper)
    expect(wrapper.get('.crypto-view__lockstate').text()).toContain(`admin.crypto.${state.label}`)
    expect(button(wrapper, 'admin.crypto.hotAddressFill').attributes('disabled') !== undefined).toBe(state.disabled)
    if (state.disabled) {
      expect(mocks.get.mock.calls.some(([path]) => path === '/admin/crypto/hot-wallet')).toBe(false)
    }
    expect(wrapper.text()).not.toMatch(/signer|Approval|groupSigner|signerSocket/i)
    expect(wrapper.find('input[type="number"][name="hot_index"]').exists()).toBe(false)
    if (!state.disabled) {
      await button(wrapper, 'admin.crypto.hotAddressFill').trigger('click')
      await flushPromises()
      expect(mocks.get).toHaveBeenCalledWith('/admin/crypto/hot-wallet/address', expect.objectContaining({ headers: {} }))
      expect(wrapper.find('#crypto-security-code').exists()).toBe(false)
      expect(wrapper.get('input[placeholder="0x…"]').element).toHaveProperty('value', '0xGAS')
    }
    await wrapper.get('#crypto-tab-sweep').trigger('click')
    expect(button(wrapper, 'admin.crypto.sweepRun').attributes('disabled') !== undefined).toBe(!state.ok || !state.ready || !state.enabled)
  })

  it('can derive and save a destination on first setup before signing is enabled', async () => {
    serverConfig.hot_wallet_evm = ''
    walletStatus = { ok: true, configured: true, ready: true, enabled: false }
    const wrapper = await render()
    await configTab(wrapper)
    expect(wrapper.get('.crypto-view__lockstate').text()).toContain('admin.crypto.walletNeedsConfig')
    expect(wrapper.text()).toContain('admin.crypto.walletNotReady')
    await button(wrapper, 'admin.crypto.hotAddressFill').trigger('click')
    await flushPromises()
    expect(wrapper.find('#crypto-security-code').exists()).toBe(false)
    expect(wrapper.get('input[placeholder="0x…"]').element).toHaveProperty('value', '0xGAS')
    await wrapper.get('.crypto-view__save-bar button').trigger('click')
    await verify(wrapper)
    expect(mocks.put).toHaveBeenLastCalledWith('/admin/crypto/config', { hot_wallet_evm: '0xGAS' }, expect.anything())
    expect(wrapper.get('.crypto-view__lockstate').text()).toContain('admin.crypto.walletReady')
    await wrapper.get('#crypto-tab-sweep').trigger('click')
    expect(button(wrapper, 'admin.crypto.sweepRun').attributes('disabled')).toBeUndefined()
  })

  it('has no manual wallet password, unlock or encrypted-backup controls', async () => {
    const wrapper=await render(); await configTab(wrapper)
    expect(wrapper.find('#crypto-wallet-unlock-passphrase').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('admin.crypto.backupAction')
    expect(wrapper.text()).not.toContain('admin.crypto.walletUnlockAction')
    expect(wrapper.find('#crypto-deposit-mnemonic').exists()).toBe(true)
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
    await manageRPC(wrapper)
    const rpc = wrapper.findAll('input').find(input => (input.element as HTMLInputElement).value === 'https://one.example')!
    await rpc.setValue('https://changed.example')
    await rpc.trigger('change')
    await wrapper.get('.crypto-view__save-bar button').trigger('click')
    await verify(wrapper)
    expect(mocks.put).toHaveBeenLastCalledWith('/admin/crypto/config', {
      rpc: { bsc: 'https://changed.example,https://two.example' }
    }, expect.anything())
  })

  it('clears a cancelled Gas seed without sending it or a password', async () => {
    walletStatus={ok:true,configured:true,ready:true,enabled:true}
    const wrapper=await render(); await configTab(wrapper)
    await button(wrapper,'admin.crypto.hotImportAction').trigger('click')
    await wrapper.get('#crypto-hot-mnemonic').setValue('private gas words')
    await wrapper.get('#crypto-wallet-form').trigger('submit'); await flushPromises()
    await wrapper.get('.crypto-security__actions .btn-secondary').trigger('click'); await flushPromises()
    expect(mocks.post).not.toHaveBeenCalled()
    await button(wrapper,'admin.crypto.hotImportAction').trigger('click')
    expect((wrapper.get('#crypto-hot-mnemonic').element as HTMLInputElement).value).toBe('')
  })

  it('imports initial seeds only after fresh 2FA and removes the form immediately', async () => {
    const wrapper=await render(); await configTab(wrapper)
    await wrapper.get('#crypto-deposit-mnemonic').setValue('deposit words')
    await wrapper.get('#crypto-initial-hot-mnemonic').setValue('gas words')
    await button(wrapper,'admin.crypto.walletInitializeAction').trigger('click'); await flushPromises()
    expect(mocks.post).not.toHaveBeenCalled()
    await verify(wrapper)
    expect(mocks.post).toHaveBeenCalledWith('/admin/crypto/wallet/initialize',{
      deposit_mnemonic:'deposit words',hot_mnemonic:'gas words'
    },expect.objectContaining({headers:{'X-Crypto-Grant':grantCredential}}))
    expect(wrapper.find('#crypto-deposit-mnemonic').exists()).toBe(false)
    expect(wrapper.find('#crypto-wallet-unlock-passphrase').exists()).toBe(false)
  })

  it('protects even a dry-run POST', async () => {
    const wrapper = await render()
    await wrapper.get('#crypto-tab-sweep').trigger('click')
    await flushPromises()
    await button(wrapper, 'admin.crypto.sweepDryRun').trigger('click')
    await verify(wrapper)
    expect(mocks.post).toHaveBeenCalledWith('/admin/crypto/sweep/run', null, expect.objectContaining({ params: { dry_run: '1' }, headers: { 'X-Crypto-Grant': grantCredential } }))
    expect(mocks.post.mock.calls.filter(([path]) => path.endsWith('/operation-intent'))).toHaveLength(0)
  })

  it('shows the shared private Bark configuration on the wallet page', async () => {
    const wrapper = await render()
    expect(wrapper.get('.crypto-view__notifications').attributes('open')).toBeUndefined()
    expect(wrapper.findComponent({ name: 'CryptoWalletSecurityPanel' }).element.closest('details')).toBeNull()
    expect(wrapper.get('.crypto-view__bark h2').text()).toBe('admin.crypto.walletBarkTitle')
    expect(wrapper.get('.crypto-view__bark').text()).toContain('admin.crypto.walletBarkDescription')
    expect((wrapper.get('#bark-device-key').element as HTMLInputElement).value).toBe('')
    expect(wrapper.get('#bark-device-key').attributes('type')).toBe('password')
  })

  it.each(['sweep', 'destination_change'] as const)('notifies %s intent before fresh verification without executing early', async (action) => {
    walletStatus = { ok: true, configured: true, ready: true, enabled: true }
    const wrapper = await render()
    if (action === 'sweep') {
      await wrapper.get('#crypto-tab-sweep').trigger('click')
      await flushPromises()
      await button(wrapper, 'admin.crypto.sweepRun').trigger('click')
    } else {
      await configTab(wrapper)
      await wrapper.get('input[placeholder="0x…"]').setValue('0x8888888888888888888888888888888888888888')
      await wrapper.get('.crypto-view__save-bar button').trigger('click')
    }
    await flushPromises()
    expect(mocks.post).toHaveBeenCalledWith('/admin/crypto/security/operation-intent', { action }, {
      _retry: true, headers: { 'Cache-Control': 'no-store', Pragma: 'no-cache' }
    })
    expect(mocks.put).not.toHaveBeenCalled()
    expect(mocks.post.mock.calls.filter(([path]) => path.endsWith('/sweep/run'))).toHaveLength(0)
    expect(wrapper.find('#crypto-security-code').exists()).toBe(true)
    await verify(wrapper, '345678')
    if (action === 'sweep') {
      expect(mocks.post).toHaveBeenCalledWith('/admin/crypto/sweep/run', null, expect.objectContaining({ headers: { 'X-Crypto-Grant': grantCredential } }))
    } else {
      expect(mocks.put).toHaveBeenCalledWith('/admin/crypto/config', { hot_wallet_evm: '0x8888888888888888888888888888888888888888' }, expect.objectContaining({ headers: { 'X-Crypto-Grant': grantCredential } }))
    }
  })

  it.each(['sweep', 'destination_change'] as const)('blocks %s when the intent notification fails', async (action) => {
    walletStatus = { ok: true, configured: true, ready: true, enabled: true }
    const normalPost = mocks.post.getMockImplementation()!
    mocks.post.mockImplementation((path: string, ...args: unknown[]) => path.endsWith('/operation-intent')
      ? Promise.reject(new Error('Notification unavailable')) : normalPost(path, ...args))
    const wrapper = await render()
    if (action === 'sweep') {
      await wrapper.get('#crypto-tab-sweep').trigger('click')
      await flushPromises()
      await button(wrapper, 'admin.crypto.sweepRun').trigger('click')
    } else {
      await configTab(wrapper)
      await wrapper.get('input[placeholder="0x…"]').setValue('0x8888888888888888888888888888888888888888')
      await wrapper.get('.crypto-view__save-bar button').trigger('click')
    }
    await flushPromises()
    expect(wrapper.get('.crypto-view__intent-error').text()).toBe('admin.crypto.walletOperationNotifyFailed')
    expect(mocks.showError).toHaveBeenCalledWith('admin.crypto.walletOperationNotifyFailed')
    expect(wrapper.find('#crypto-security-code').exists()).toBe(false)
    expect(mocks.put).not.toHaveBeenCalled()
    expect(mocks.post.mock.calls.filter(([path]) => path.endsWith('/sweep/run'))).toHaveLength(0)
  })

  it('does not open a delayed sweep verification after switching away', async () => {
    walletStatus = { ok: true, configured: true, ready: true, enabled: true }
    let completeIntent!: (value: unknown) => void
    const normalPost = mocks.post.getMockImplementation()!
    mocks.post.mockImplementation((path: string, ...args: unknown[]) => path.endsWith('/operation-intent')
      ? new Promise(resolve => { completeIntent = resolve }) : normalPost(path, ...args))
    const wrapper = await render()
    await wrapper.get('#crypto-tab-sweep').trigger('click')
    await flushPromises()
    await button(wrapper, 'admin.crypto.sweepRun').trigger('click')
    await flushPromises()
    await wrapper.get('#crypto-tab-addresses').trigger('click')
    completeIntent({ data: { accepted: true } })
    await flushPromises()
    expect(wrapper.find('#crypto-security-code').exists()).toBe(false)
    expect(mocks.post.mock.calls.filter(([path]) => path.endsWith('/sweep/run'))).toHaveLength(0)
  })
})
