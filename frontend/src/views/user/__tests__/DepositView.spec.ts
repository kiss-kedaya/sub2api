import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import QRCode from 'qrcode'
import DepositView from '../DepositView.vue'

const mocks = vi.hoisted(() => ({
  getDepositInfo: vi.fn(), getDepositHistory: vi.fn(), getDepositNetworkStatus: vi.fn(),
  apiGet: vi.fn(), copy: vi.fn(), refreshUser: vi.fn(), showSuccess: vi.fn()
}))
// Site-wide public settings the page reads (the only source of the support contact).
const appStoreState = vi.hoisted(() => ({ contactInfo: '' }))
vi.mock('@/api/client', () => ({ apiClient: { get: mocks.apiGet } }))
vi.mock('@/api/crypto', () => ({
  getDepositInfo: mocks.getDepositInfo, getDepositHistory: mocks.getDepositHistory,
  getDepositNetworkStatus: mocks.getDepositNetworkStatus
}))
vi.mock('@/composables/useClipboard', () => ({ useClipboard: () => ({ copyToClipboard: mocks.copy }) }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ refreshUser: mocks.refreshUser }) }))
vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showSuccess: mocks.showSuccess,
    get contactInfo() { return appStoreState.contactInfo }
  })
}))
vi.mock('qrcode', () => ({ default: { toCanvas: vi.fn().mockResolvedValue(undefined) } }))
// Params are echoed so assertions can see what each message was given.
vi.mock('vue-i18n', async (original) => ({
  ...(await original<typeof import('vue-i18n')>()),
  useI18n: () => ({ t: (key: string, params?: Record<string, unknown>) => (params ? key + ' ' + JSON.stringify(params) : key) })
}))
vi.mock('@/components/layout/AppLayout.vue', () => ({ default: { template: '<main><slot /></main>' } }))

// The key-rotation gate has its own spec; this page spec only needs a stable,
// non-required gate unless a test explicitly flips it on.
const keyRotationGateMock = vi.hoisted(() => ({ requiredRef: null as any, statusRef: null as any }))
vi.mock('@/composables/useKeyRotationGate', async () => {
  const { ref } = await import('vue')
  keyRotationGateMock.requiredRef = ref(false)
  keyRotationGateMock.statusRef = ref(null)
  return {
    useKeyRotationGate: () => ({
      required: keyRotationGateMock.requiredRef,
      status: keyRotationGateMock.statusRef,
      refresh: vi.fn(),
    }),
    resetKeyRotationGate: vi.fn(),
    markKeyRotationRequired: vi.fn(),
    ensureKeyRotationGate: vi.fn(),
  }
})
vi.mock('@/api/keyRotationGate', async () => {
  const codes = await vi.importActual<typeof import('@/api/keyRotationGateCodes')>('@/api/keyRotationGateCodes')
  return {
    ...codes,
    isKeyRotationRequiredError: codes.isKeyRotationRequiredPayload,
    keyRotationGateAPI: { getStatus: vi.fn() },
  }
})

enableAutoUnmount(afterEach)

const hash = '0x' + 'ab'.repeat(32)
const otherHash = '0x' + 'cd'.repeat(32)

function render() {
  return mount(DepositView, {
    global: {
      stubs: {
        AppLayout: { template: '<main><slot /></main>' },
        Icon: true,
        LoadingSpinner: true,
        // Rendered for real in KeyRotationGateNotice.spec; its UButton needs a router.
        KeyRotationGateNotice: { template: '<div data-testid="key-rotation-gate-notice" />' }
      }
    }
  })
}

function pendingRow(overrides: Record<string, unknown> = {}) {
  return { chain: 'bsc', tx_hash: otherHash, token: 'USDT', amount: '10', status: 'pending', credited: 0, created_at: '2026-01-02T00:00:00Z', ...overrides }
}

const fakeTimers = () => vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout', 'setInterval', 'clearInterval'] })

function setVisibility(state: 'visible' | 'hidden') {
  Object.defineProperty(document, 'visibilityState', { configurable: true, get: () => state })
  document.dispatchEvent(new Event('visibilitychange'))
}

afterEach(() => {
  vi.useRealTimers()
  Reflect.deleteProperty(document, 'visibilityState')
})

beforeEach(() => {
  vi.clearAllMocks()
  appStoreState.contactInfo = ''
  mocks.getDepositHistory.mockReset()
  mocks.getDepositNetworkStatus.mockReset().mockResolvedValue([])
  mocks.refreshUser.mockResolvedValue(undefined)
  mocks.getDepositInfo.mockResolvedValue({
    enabled: true, chains: [{ chain: 'bsc', address: '0x' + '12'.repeat(20), tokens: ['USDT'] }],
    rate: 6.7, min_amount: 1, max_amount: 0
  })
  mocks.getDepositHistory.mockResolvedValue([
    { chain: 'bsc', tx_hash: hash, token: 'USDT', amount: '10', status: 'credited', credited: 67, created_at: '2026-01-01T00:00:00Z' },
    { chain: 'base', tx_hash: otherHash, token: 'USDC', amount: '20', status: 'pending', credited: 0, created_at: '2026-01-02T00:00:00Z' }
  ])
})

describe('user deposit availability', () => {
  it.each([null, undefined])('shows history without offering a new deposit when disabled and chains is %s', async (chains) => {
    fakeTimers()
    const payload = { enabled: false, chains, min_amount: 0, max_amount: 0 }
    mocks.apiGet.mockResolvedValue({ data: { data: payload } })
    const { getDepositInfo } = await vi.importActual<typeof import('@/api/crypto')>('@/api/crypto')
    mocks.getDepositInfo.mockImplementationOnce(getDepositInfo)

    const wrapper = render()
    await flushPromises()

    expect(wrapper.text()).toContain('deposit.disabled')
    expect(wrapper.text()).toContain('deposit.disabledHint')
    expect(wrapper.text()).not.toContain('deposit.loadFailed')
    expect(wrapper.find('[role="alert"]').exists()).toBe(false)
    expect(wrapper.find('.deposit-view__address-block').exists()).toBe(false)
    expect(wrapper.get('.deposit-view__history').text()).toContain(hash)
    expect(wrapper.get('.deposit-view__history').text()).not.toContain('deposit.autoRefresh')
    expect(mocks.getDepositHistory).toHaveBeenCalledOnce()
    await expect(getDepositInfo()).resolves.toEqual({ ...payload, chains: [] })

    setVisibility('visible')
    await vi.advanceTimersByTimeAsync(15000)
    expect(mocks.getDepositHistory).toHaveBeenCalledOnce()

    mocks.getDepositHistory.mockResolvedValueOnce([pendingRow()])
    await wrapper.get('.deposit-view__history-head button').trigger('click')
    await flushPromises()
    expect(wrapper.get('.deposit-view__history').text()).toContain(otherHash)
    expect(wrapper.get('.deposit-view__history').text()).not.toContain(hash)
    expect(mocks.getDepositHistory).toHaveBeenCalledTimes(2)
    await vi.advanceTimersByTimeAsync(15000)
    expect(mocks.getDepositHistory).toHaveBeenCalledTimes(2)
  })

  it('shows and lets the user refresh their history when deposit info fails', async () => {
    fakeTimers()
    mocks.getDepositInfo.mockRejectedValueOnce({ status: 503 })
    const wrapper = render()
    await flushPromises()

    expect(wrapper.get('[role="alert"]').text()).toContain('deposit.loadFailed')
    expect(wrapper.find('.deposit-view__address-block').exists()).toBe(false)
    expect(wrapper.get('.deposit-view__history').text()).toContain(hash)
    expect(wrapper.get('.deposit-view__history').text()).not.toContain('deposit.autoRefresh')
    expect(mocks.getDepositHistory).toHaveBeenCalledOnce()

    setVisibility('visible')
    await vi.advanceTimersByTimeAsync(15000)
    expect(mocks.getDepositHistory).toHaveBeenCalledOnce()
    mocks.getDepositHistory.mockResolvedValueOnce([pendingRow({ status: 'ignored', reason: 'below_min' })])
    await wrapper.get('.deposit-view__history-head button').trigger('click')
    await flushPromises()
    expect(wrapper.get('.deposit-view__history').text()).not.toContain(hash)
    expect(wrapper.get('.deposit-view__reason').text()).not.toContain('deposit.reasonBelowMin {"min":0}')
    expect(mocks.getDepositInfo).toHaveBeenCalledOnce()
    expect(mocks.getDepositHistory).toHaveBeenCalledTimes(2)

    await wrapper.get('[role="alert"] button').trigger('click')
    await flushPromises()
    expect(wrapper.find('.deposit-view__address-block').exists()).toBe(true)
    expect(mocks.getDepositHistory).toHaveBeenCalledTimes(3)
    await vi.advanceTimersByTimeAsync(5000)
    expect(mocks.getDepositHistory).toHaveBeenCalledTimes(4)
  })
})

describe('user deposit network status', () => {
  const bscAddress = '0x' + '12'.repeat(20)
  const baseAddress = '0x' + '34'.repeat(20)
  const readStatus = (wrapper: ReturnType<typeof render>) =>
    wrapper.get('.deposit-view__chain[aria-pressed="true"] .deposit-view__network-status').text()

  function expectTransferActions(wrapper: ReturnType<typeof render>, address: string) {
    expect(wrapper.get('.deposit-view__address').text()).toBe(address)
    expect(wrapper.find('.deposit-view__copy').exists()).toBe(true)
    expect(wrapper.find('canvas').exists()).toBe(true)
  }

  const expiresSoon = () => new Date(Date.now() + 60_000).toISOString()

  it.each([
    ['an explicit unknown status', [{ chain: 'bsc', status: 'unknown' }]],
    ['a missing chain', [{ chain: 'base', status: 'paused' }]],
    ['a confirmed status without expiration', [{ chain: 'bsc', status: 'paused' }]],
    ['an already expired status', [{ chain: 'bsc', status: 'stalled', expires_at: '2025-01-01T00:00:00Z' }]],
    ['a malformed expiration time', [{ chain: 'bsc', status: 'paused', expires_at: 'not-a-time' }]],
  ])('falls back to unknown without hiding transfer actions for %s', async (_reason, networks) => {
    mocks.getDepositNetworkStatus.mockResolvedValueOnce(networks)
    const wrapper = render()
    await flushPromises()

    expect(readStatus(wrapper)).toBe('deposit.networkStatus.unknown')
    // Missing or expired evidence is not a delay: say nothing rather than alarm.
    expect(wrapper.find('.deposit-view__network-notice').exists()).toBe(false)
    expectTransferActions(wrapper, bscAddress)
  })

  it.each([404, 503])('keeps deposits and history usable when the status endpoint returns %s', async (status) => {
    mocks.getDepositNetworkStatus.mockRejectedValueOnce({ status })
    const wrapper = render()
    await flushPromises()

    expect(readStatus(wrapper)).toBe('deposit.networkStatus.unknown')
    expectTransferActions(wrapper, bscAddress)
    expect(wrapper.get('.deposit-view__history').text()).toContain(hash)
    expect(wrapper.text()).not.toContain('deposit.loadFailed')
  })

  it('loads status while instructions are pending and renders instructions while status is pending', async () => {
    const info = { enabled: true, chains: [{ chain: 'bsc', address: bscAddress, tokens: ['USDT'] }], min_amount: 1, max_amount: 0 }
    let resolveInfo!: (value: typeof info) => void
    let resolveStatus!: (value: { chain: string; status: 'available'; expires_at: string }[]) => void
    mocks.getDepositInfo.mockReturnValueOnce(new Promise(resolve => { resolveInfo = resolve }))
    mocks.getDepositNetworkStatus.mockReturnValueOnce(new Promise(resolve => { resolveStatus = resolve }))
    const wrapper = render()
    await flushPromises()

    expect(mocks.getDepositNetworkStatus).toHaveBeenCalledOnce()
    resolveInfo(info)
    await flushPromises()
    expect(readStatus(wrapper)).toBe('deposit.networkStatus.unknown')
    expectTransferActions(wrapper, bscAddress)
    expect(wrapper.get('.deposit-view__history').text()).toContain(hash)

    resolveStatus([{ chain: 'bsc', status: 'available', expires_at: expiresSoon() }])
    await flushPromises()
    expect(readStatus(wrapper)).toBe('deposit.networkStatus.available')
    expect(wrapper.find('.deposit-view__network-notice').exists()).toBe(false)
  })

  it('keeps network selection and history available, blocking only the paused or stalled network', async () => {
    mocks.getDepositInfo.mockResolvedValueOnce({
      enabled: true,
      chains: [
        { chain: 'bsc', address: bscAddress, tokens: ['USDT'] },
        { chain: 'base', address: baseAddress, tokens: ['USDT'] },
        { chain: 'ethereum', address: '0x' + '56'.repeat(20), tokens: ['USDT'] },
        { chain: 'polygon', address: '0x' + '78'.repeat(20), tokens: ['USDT'] },
        { chain: 'arbitrum', address: '0x' + '90'.repeat(20), tokens: ['USDT'] },
      ],
      min_amount: 1, max_amount: 0
    })
    mocks.getDepositNetworkStatus.mockResolvedValueOnce([
      { chain: 'polygon', status: 'stalled', expires_at: expiresSoon() },
      { chain: 'base', status: 'paused', expires_at: expiresSoon() },
      { chain: 'ethereum', status: 'delayed', expires_at: expiresSoon() },
      { chain: 'arbitrum', status: 'slow', expires_at: expiresSoon() },
      { chain: 'bsc', status: 'available', expires_at: expiresSoon() },
    ])
    const wrapper = render()
    await flushPromises()

    expect(wrapper.findAll('.deposit-view__network-status').map(badge => badge.text())).toEqual([
      'deposit.networkStatus.available', 'deposit.networkStatus.paused',
      'deposit.networkStatus.delayed', 'deposit.networkStatus.stalled',
      'deposit.networkStatus.slow',
    ])
    expectTransferActions(wrapper, bscAddress)
    const chains = wrapper.findAll('.deposit-view__chain')
    for (const [index, state] of [[1, 'paused'], [3, 'stalled']] as const) {
      await chains[index].trigger('click')
      expect(chains[index].attributes('aria-pressed')).toBe('true')
      expect(wrapper.get('.deposit-view__network-notice').text()).toContain('deposit.networkStatusHint.' + state)
      expect(wrapper.find('.deposit-view__address').exists()).toBe(false)
      expect(wrapper.find('.deposit-view__copy').exists()).toBe(false)
      expect(wrapper.find('canvas').exists()).toBe(false)
      expect(wrapper.get('.deposit-view__history').text()).toContain(hash)
    }
    mocks.getDepositHistory.mockResolvedValueOnce([pendingRow()])
    await wrapper.get('.deposit-view__history-head button').trigger('click')
    await flushPromises()
    expect(wrapper.get('.deposit-view__history').text()).not.toContain(hash)
    expect(chains[3].attributes('aria-pressed')).toBe('true')

    await chains[2].trigger('click')
    expect(wrapper.get('.deposit-view__network-notice').text()).toContain('deposit.networkStatusHint.delayed')
    expectTransferActions(wrapper, '0x' + '56'.repeat(20))

    // A slow network is a calm notice, not a blocked deposit.
    await chains[4].trigger('click')
    expect(wrapper.get('.deposit-view__network-notice').text()).toContain('deposit.networkStatusHint.slow')
    expectTransferActions(wrapper, '0x' + '90'.repeat(20))
  })

  it('downgrades stalled status at its expiration even without a fresh response', async () => {
    vi.useFakeTimers({ toFake: ['Date', 'setTimeout', 'clearTimeout'] })
    const expiration = new Date(Date.now() + 1000).toISOString()
    mocks.getDepositNetworkStatus.mockResolvedValueOnce([{ chain: 'bsc', status: 'stalled', expires_at: expiration }])
    const wrapper = render()
    await flushPromises()

    await vi.advanceTimersByTimeAsync(999)
    expect(readStatus(wrapper)).toBe('deposit.networkStatus.stalled')
    expect(wrapper.find('.deposit-view__address-block').exists()).toBe(false)

    await vi.advanceTimersByTimeAsync(1)
    expect(readStatus(wrapper)).toBe('deposit.networkStatus.unknown')
    expectTransferActions(wrapper, bscAddress)
    expect(QRCode.toCanvas).toHaveBeenLastCalledWith(wrapper.get('canvas').element, bscAddress, expect.any(Object))
    expect(mocks.getDepositNetworkStatus).toHaveBeenCalledOnce()
  })

  it('re-reads the switched network in the background and restores its actions', async () => {
    mocks.getDepositInfo.mockResolvedValueOnce({
      enabled: true,
      chains: [
        { chain: 'bsc', address: bscAddress, tokens: ['USDT'] },
        { chain: 'base', address: baseAddress, tokens: ['USDT'] },
      ],
      min_amount: 1, max_amount: 0
    })
    mocks.getDepositNetworkStatus
      .mockResolvedValueOnce([{ chain: 'base', status: 'paused', expires_at: expiresSoon() }])
      .mockResolvedValueOnce([{ chain: 'base', status: 'available', expires_at: expiresSoon() }])
    mocks.copy.mockResolvedValueOnce(true)
    const wrapper = render()
    await flushPromises()

    const base = wrapper.findAll('.deposit-view__chain')[1]
    await base.trigger('click')
    await flushPromises()
    // Switching network refreshes that network once, so the user does not have
    // to press refresh to get an up-to-date answer.
    expect(mocks.getDepositNetworkStatus).toHaveBeenCalledTimes(2)
    expect(base.attributes('aria-pressed')).toBe('true')
    expect(readStatus(wrapper)).toBe('deposit.networkStatus.available')
    expectTransferActions(wrapper, baseAddress)
    expect(QRCode.toCanvas).toHaveBeenLastCalledWith(wrapper.get('canvas').element, baseAddress, expect.any(Object))
    await wrapper.get('.deposit-view__copy').trigger('click')
    expect(mocks.copy).toHaveBeenCalledWith(baseAddress)
    expect(mocks.getDepositInfo).toHaveBeenCalledOnce()
  })

  it('restores transfer actions after a manual refresh without changing the selected network', async () => {
    mocks.getDepositInfo.mockResolvedValueOnce({
      enabled: true,
      chains: [
        { chain: 'bsc', address: bscAddress, tokens: ['USDT'] },
        { chain: 'base', address: baseAddress, tokens: ['USDT'] },
      ],
      min_amount: 1, max_amount: 0
    })
    mocks.getDepositNetworkStatus.mockResolvedValue([{ chain: 'base', status: 'paused', expires_at: expiresSoon() }])
    const wrapper = render()
    await flushPromises()

    const base = wrapper.findAll('.deposit-view__chain')[1]
    await base.trigger('click')
    await flushPromises()
    expect(readStatus(wrapper)).toBe('deposit.networkStatus.paused')
    expect(wrapper.find('.deposit-view__address-block').exists()).toBe(false)

    mocks.getDepositNetworkStatus.mockResolvedValue([{ chain: 'base', status: 'available', expires_at: expiresSoon() }])
    await wrapper.get('.deposit-view__network-refresh').trigger('click')
    await flushPromises()
    expect(base.attributes('aria-pressed')).toBe('true')
    expect(readStatus(wrapper)).toBe('deposit.networkStatus.available')
    expectTransferActions(wrapper, baseAddress)
    expect(mocks.getDepositInfo).toHaveBeenCalledOnce()
  })

  it.each([
    [{ networks: [{ chain: 'bsc', status: 'paused', expires_at: expiresSoon() }] }, 'paused'],
    [{ data: { networks: [{ chain: 'bsc', status: 'paused', expires_at: expiresSoon() }] } }, 'paused'],
    [{ networks: [null, { chain: 'bsc', status: 'unexpected' }] }, 'unknown'],
    ['<html>legacy app shell</html>', 'unknown'],
  ])('accepts supported response envelopes and treats legacy/malformed data as unknown', async (data, state) => {
    mocks.apiGet.mockResolvedValueOnce({ data })
    const { getDepositNetworkStatus } = await vi.importActual<typeof import('@/api/crypto')>('@/api/crypto')
    mocks.getDepositNetworkStatus.mockImplementationOnce(getDepositNetworkStatus)
    const wrapper = render()
    await flushPromises()

    expect(mocks.apiGet).toHaveBeenLastCalledWith('/crypto/deposit-network-status', { signal: expect.any(AbortSignal) })
    expect(readStatus(wrapper)).toBe('deposit.networkStatus.' + state)
    expect(wrapper.find('.deposit-view__address-block').exists()).toBe(state === 'unknown')
  })

  it('shows the arrival estimate on the badge, under each network card and in the top notice', async () => {
    mocks.getDepositInfo.mockResolvedValueOnce({
      enabled: true,
      chains: [
        { chain: 'bsc', address: bscAddress, tokens: ['USDT'] },
        { chain: 'base', address: baseAddress, tokens: ['USDT'] },
      ],
      min_amount: 1, max_amount: 0
    })
    mocks.getDepositNetworkStatus.mockResolvedValueOnce([
      { chain: 'bsc', status: 'available', eta_seconds: 35, expires_at: expiresSoon() },
      { chain: 'base', status: 'slow', eta_seconds: 200, expires_at: expiresSoon() },
    ])
    const wrapper = render()
    await flushPromises()

    const [bscCard, baseCard] = wrapper.findAll('.deposit-view__chain')
    // 正常网络：徽标给「约 35 秒到账」，绝不出现「延迟」。
    expect(bscCard.get('.deposit-view__network-status').text()).toContain('deposit.networkStatusDetail.availableEta')
    expect(bscCard.get('.deposit-view__network-status').text()).toContain('deposit.eta.seconds')
    expect(bscCard.get('.deposit-view__chain-eta').text()).toContain('deposit.eta.card')
    expect(baseCard.get('.deposit-view__network-status').text()).toContain('deposit.networkStatusDetail.slowEta')
    expect(baseCard.get('.deposit-view__chain-eta').text()).toContain('deposit.eta.card')
    expect(wrapper.get('.deposit-view__eta-notice').text()).toContain('deposit.eta.notice')
    expect(wrapper.find('.deposit-view__network-notice').exists()).toBe(false)
  })

  it('switches the copy to instant credit for an instant-credit network', async () => {
    mocks.getDepositInfo.mockResolvedValueOnce({
      enabled: true,
      chains: [{ chain: 'bsc', address: bscAddress, tokens: ['USDT'], instant_credit: true, eta_seconds: 35 }],
      min_amount: 1, max_amount: 0
    })
    mocks.getDepositNetworkStatus.mockResolvedValueOnce([
      { chain: 'bsc', status: 'available', eta_seconds: 35, expires_at: expiresSoon() },
    ])
    const wrapper = render()
    await flushPromises()

    const card = wrapper.get('.deposit-view__chain')
    expect(card.get('.deposit-view__network-status').text()).toContain('deposit.networkStatusDetail.availableInstant')
    expect(card.get('.deposit-view__chain-eta').text()).toContain('deposit.eta.cardInstant')
    // 即时入账必须说清「失败会回滚」。
    expect(wrapper.get('.deposit-view__eta-notice').text()).toContain('deposit.instantCreditNotice')
    expect(wrapper.get('[data-testid="deposit-instant-credit-rule"]').text()).toContain('deposit.instantCreditRule')
  })

  it('treats an expired status as unknown instead of keeping its delay alarm or estimate', async () => {
    mocks.getDepositInfo.mockResolvedValueOnce({
      enabled: true,
      chains: [{ chain: 'bsc', address: bscAddress, tokens: ['USDT'], instant_credit: true, eta_seconds: 480 }],
      min_amount: 1, max_amount: 0
    })
    mocks.getDepositNetworkStatus.mockResolvedValueOnce([
      { chain: 'bsc', status: 'delayed', eta_seconds: 480, expires_at: new Date(Date.now() - 1000).toISOString() },
    ])
    const wrapper = render()
    await flushPromises()

    expect(readStatus(wrapper)).toBe('deposit.networkStatus.unknown')
    // deposit-info 里还有 eta/instant_credit，但状态过期就不能拿来当结论。
    expect(wrapper.find('.deposit-view__chain-eta').exists()).toBe(false)
    expect(wrapper.find('.deposit-view__eta-notice').exists()).toBe(false)
    expect(wrapper.find('.deposit-view__network-notice').exists()).toBe(false)
    expectTransferActions(wrapper, bscAddress)
  })

  it('re-reads the status when the window regains focus and stays silent on failure', async () => {
    const wrapper = render()
    await flushPromises()
    expect(mocks.getDepositNetworkStatus).toHaveBeenCalledOnce()

    mocks.getDepositNetworkStatus.mockResolvedValueOnce([
      { chain: 'bsc', status: 'available', eta_seconds: 35, expires_at: expiresSoon() },
    ])
    window.dispatchEvent(new Event('focus'))
    await flushPromises()
    expect(mocks.getDepositNetworkStatus).toHaveBeenCalledTimes(2)
    expect(readStatus(wrapper)).toContain('deposit.networkStatusDetail.availableEta')

    // A failed refresh is silent and keeps the observations that are still
    // within their expiry: a transient outage must not flash "checking".
    mocks.getDepositNetworkStatus.mockRejectedValueOnce({ status: 503 })
    window.dispatchEvent(new Event('focus'))
    await flushPromises()
    expect(mocks.getDepositNetworkStatus).toHaveBeenCalledTimes(3)
    expect(readStatus(wrapper)).toContain('deposit.networkStatusDetail.availableEta')
    expect(wrapper.find('[role="alert"]').exists()).toBe(false)
    expectTransferActions(wrapper, bscAddress)

    // An empty read is not evidence that every network went unknown either.
    mocks.getDepositNetworkStatus.mockResolvedValueOnce([])
    window.dispatchEvent(new Event('focus'))
    await flushPromises()
    expect(readStatus(wrapper)).toContain('deposit.networkStatusDetail.availableEta')
  })
})

describe('user deposit instant-credit history', () => {
  it('labels a provisional credit and a rollback with their own badge and reason', async () => {
    mocks.getDepositHistory.mockResolvedValue([
      { chain: 'bsc', tx_hash: hash, token: 'USDT', amount: '10', status: 'provisional', credited: 67, created_at: '2026-01-01T00:00:00Z' },
      {
        chain: 'bsc', tx_hash: otherHash, token: 'USDT', amount: '12', status: 'revoked', credited: 0,
        created_at: '2026-01-02T00:00:00Z', reason: 'reorg_rollback'
      },
    ])
    const wrapper = render()
    await flushPromises()

    // 行色语义：临时入账算收入(绿)，回滚算撤销(玫红)，徽标各自保留状态文案。
    const rows = wrapper.findAll('[data-testid="deposit-history-row"]')
    expect(rows.map(row => row.attributes('data-tone'))).toEqual(['positive', 'reversal'])
    expect(rows[0].get('[data-testid="deposit-history-status"]').text()).toBe('deposit.statusProvisional')
    expect(rows[1].get('[data-testid="deposit-history-status"]').text()).toBe('deposit.statusRevoked')
    // 回滚原因单独一套文案：不能说成「未入账」，那笔钱曾经入过账。
    expect(wrapper.get('.deposit-view__reason').text()).toContain('deposit.reasonRevoked')

    // 临时入账的余额已经加上，所以显示金额；已回滚且金额未知的那笔不算入账。
    expect(rows[0].get('[data-testid="transaction-amount"]').text()).toBe('+$67')
    expect(rows[1].get('[data-testid="transaction-amount"]').text()).toBe('—')
    expect(wrapper.text()).not.toContain('deposit.creditUnknown')
  })

  it('keeps older records without the provisional/revoked states working', async () => {
    const wrapper = render()
    await flushPromises()

    const rows = wrapper.findAll('[data-testid="deposit-history-row"]')
    expect(rows.map(row => row.attributes('data-tone'))).toEqual(['positive', 'pending'])
    // 已入账的绿字本身就说明状态，只有待确认/临时/回滚才挂徽标。
    expect(wrapper.findAll('[data-testid="deposit-history-status"]').map(badge => badge.text()))
      .toEqual(['deposit.statusConfirming'])
    expect(wrapper.find('.deposit-view__reason').exists()).toBe(false)
  })
})

describe('user deposit history transaction hashes', () => {
  it.each([0, null, undefined, Number.NaN])('does not display an unknown credit as zero: %s', async (credited) => {
    mocks.getDepositHistory.mockResolvedValue([{ chain: 'bsc', tx_hash: hash, token: 'USDT', amount: '10', status: 'credited', credited, created_at: '2026-01-01T00:00:00Z' }])
    const wrapper = render()
    await flushPromises()
    expect(wrapper.get('[data-testid="deposit-credit-unknown"]').text()).toBe('deposit.creditUnknown')
    expect(wrapper.get('.deposit-view__status').text()).toContain('deposit.statusReconciliation')
    // 金额未知时右列是占位符，不会把 0 当成已入账金额。
    expect(wrapper.get('[data-testid="transaction-amount"]').text()).toBe('—')
    expect(wrapper.text()).not.toContain('deposit.statusCredited')
    expect(mocks.showSuccess).not.toHaveBeenCalled()
  })

  it('keeps the original credited amount even when the displayed rate changes', async () => {
    mocks.getDepositInfo.mockResolvedValue({ enabled: true, chains: [{ chain: 'bsc', address: '0x' + '12'.repeat(20), tokens: ['USDT'] }], rate: 99, min_amount: 1, max_amount: 0 })
    mocks.getDepositHistory.mockResolvedValue([{ chain: 'bsc', tx_hash: hash, token: 'USDT', amount: '10', status: 'credited', credited: 67, created_at: '2026-01-01T00:00:00Z' }])
    const wrapper = render()
    await flushPromises()
    expect(wrapper.get('[data-testid="transaction-amount"]').text()).toBe('+$67')
    expect(wrapper.find('[data-testid="deposit-credit-unknown"]').exists()).toBe(false)
  })

  it('shows complete hashes for credited and pending deposits with their own network explorer', async () => {
    const wrapper = render()
    await flushPromises()
    const hashes = wrapper.findAll('a.deposit-view__hash')
    expect(hashes.map(link => link.text())).toEqual([hash, otherHash])
    expect(hashes.map(link => link.attributes('href'))).toEqual([
      'https://bscscan.com/tx/' + hash,
      'https://basescan.org/tx/' + otherHash
    ])
    expect(hashes[0].attributes('rel')).toBe('noopener noreferrer')
    expect(hashes[0].attributes('target')).toBe('_blank')
    // 记录行内保留完整哈希，右列只放金额语义。
    expect(wrapper.findAll('[data-testid="deposit-history-row"]')).toHaveLength(2)
  })

  it('copies the full selected transaction hash rather than its shortened label or deposit address', async () => {
    const wrapper = render()
    await flushPromises()
    await wrapper.findAll('.deposit-view__hash-copy')[1].trigger('click')
    expect(mocks.copy).toHaveBeenCalledOnce()
    expect(mocks.copy).toHaveBeenCalledWith(otherHash)
  })

  it('keeps malformed hashes readable and copyable without creating an explorer link', async () => {
    mocks.getDepositHistory.mockResolvedValueOnce([
      { chain: 'bsc', tx_hash: 'legacy-reference', token: 'USDT', amount: '1', status: 'credited', credited: 6.7, created_at: '2026-01-01T00:00:00Z' }
    ])
    const wrapper = render()
    await flushPromises()
    expect(wrapper.find('a.deposit-view__hash').exists()).toBe(false)
    expect(wrapper.get('code.deposit-view__hash').text()).toBe('legacy-reference')
    await wrapper.get('.deposit-view__hash-copy').trigger('click')
    expect(mocks.copy).toHaveBeenCalledWith('legacy-reference')
  })
})

describe('user deposit confirmation progress', () => {
  it('shows the confirming status, confirmation count and progress bar for a pending row', async () => {
    mocks.getDepositHistory.mockResolvedValue([pendingRow({ confirmations: 2, required_confirmations: 3, block_number: 123 })])
    const wrapper = render()
    await flushPromises()
    expect(wrapper.get('[data-testid="deposit-history-status"]').text()).toBe('deposit.statusConfirming')
    expect(wrapper.get('.deposit-view__progress-text').text()).toBe('deposit.confirmProgress {"done":2,"required":3,"remaining":1}')
    const bar = wrapper.get('[role="progressbar"]')
    expect(bar.attributes('aria-valuenow')).toBe('2')
    expect(bar.attributes('aria-valuemax')).toBe('3')
    expect(wrapper.get('.deposit-view__progress-fill').attributes('style')).toContain('scaleX(0.67)')
    expect(wrapper.get('.deposit-view__pending strong').text()).toBe('deposit.pendingSummary {"n":1}')
    expect(wrapper.findAll('.deposit-view__pending-list li').map(li => li.text())).toEqual(['10 USDT · BSC (BEP20) · 2/3'])
  })

  it('keeps a counter-free "confirming" line when the backend sends no confirmation fields or a zero target', async () => {
    mocks.getDepositHistory.mockResolvedValue([
      pendingRow(),
      pendingRow({ tx_hash: hash, confirmations: 1, required_confirmations: 0 })
    ])
    const wrapper = render()
    await flushPromises()
    expect(wrapper.findAll('[data-testid="deposit-history-status"]').map(b => b.text())).toEqual(['deposit.statusConfirming', 'deposit.statusConfirming'])
    // 缺确认数只给「已扫描到，确认中」：不编 n/m，也不画进度条。
    expect(wrapper.findAll('.deposit-view__progress-text').map(text => text.text()))
      .toEqual(['deposit.confirmPending', 'deposit.confirmPending'])
    expect(wrapper.find('[role="progressbar"]').exists()).toBe(false)
    expect(wrapper.findAll('.deposit-view__pending-list li').map(li => li.text())).toEqual(['10 USDT · BSC (BEP20)', '10 USDT · BSC (BEP20)'])
  })

  it('lists at most three confirming deposits in the summary', async () => {
    mocks.getDepositHistory.mockResolvedValue(
      ['a', 'b', 'c', 'd'].map(c => pendingRow({ tx_hash: '0x' + c.repeat(64), confirmations: 1, required_confirmations: 3 }))
    )
    const wrapper = render()
    await flushPromises()
    expect(wrapper.findAll('.deposit-view__pending-list li')).toHaveLength(3)
    expect(wrapper.get('.deposit-view__pending-more').text()).toBe('deposit.pendingMore {"n":1}')
  })

  it('says the block confirmations are complete instead of promising an immediate credit', async () => {
    mocks.getDepositHistory.mockResolvedValue([pendingRow({ confirmations: 3, required_confirmations: 3 })])
    const wrapper = render()
    await flushPromises()
    expect(wrapper.get('.deposit-view__progress-text').text()).toBe('deposit.confirmProgressDone {"done":3,"required":3}')
    expect(wrapper.get('.deposit-view__progress-fill').attributes('style')).toContain('scaleX(1)')
  })
})

describe('user deposit auto refresh', () => {
  const creditedRow = { chain: 'bsc', tx_hash: hash, token: 'USDT', amount: '10', status: 'credited', credited: 67, created_at: '2026-01-01T00:00:00Z' }

  it('keeps manual refresh available when history cannot be read and resumes the timer after success', async () => {
    fakeTimers()
    mocks.getDepositHistory.mockRejectedValueOnce({ status: 503 }).mockResolvedValue([creditedRow])
    const wrapper = render()
    await flushPromises()
    expect(wrapper.get('.deposit-view__history-error').text()).toBe('deposit.historyFailed')
    expect(wrapper.get('.deposit-view__history').text()).not.toContain('deposit.autoRefresh')
    expect(wrapper.find('.deposit-view__address-block').exists()).toBe(true)
    setVisibility('visible')
    await vi.advanceTimersByTimeAsync(15000)
    expect(mocks.getDepositHistory).toHaveBeenCalledOnce()

    await wrapper.get('.deposit-view__history-head button').trigger('click')
    await flushPromises()
    expect(wrapper.get('.deposit-view__history').text()).toContain(hash)
    expect(wrapper.find('.deposit-view__history-error').exists()).toBe(false)
    expect(mocks.getDepositHistory).toHaveBeenCalledTimes(2)
    await vi.advanceTimersByTimeAsync(5000)
    expect(mocks.getDepositHistory).toHaveBeenCalledTimes(3)
  })

  it('discovers a new deposit within 5 seconds even when the history was empty', async () => {
    fakeTimers()
    mocks.getDepositHistory
      .mockResolvedValueOnce([])
      .mockResolvedValue([pendingRow({ confirmations: 1, required_confirmations: 3 })])
    const wrapper = render()
    await flushPromises()
    expect(mocks.getDepositHistory).toHaveBeenCalledTimes(1)
    expect(wrapper.find('.deposit-view__pending').exists()).toBe(false)

    await vi.advanceTimersByTimeAsync(4999)
    await flushPromises()
    expect(mocks.getDepositHistory).toHaveBeenCalledTimes(1)

    await vi.advanceTimersByTimeAsync(1)
    await flushPromises()
    expect(mocks.getDepositHistory).toHaveBeenCalledTimes(2)
    expect(wrapper.find('.deposit-view__pending').exists()).toBe(true)
  })

  it('keeps the 5 second cadence after the last pending deposit is credited', async () => {
    fakeTimers()
    mocks.getDepositHistory
      .mockResolvedValueOnce([pendingRow({ confirmations: 2, required_confirmations: 3 })])
      .mockResolvedValue([creditedRow])
    const wrapper = render()
    await flushPromises()
    await vi.advanceTimersByTimeAsync(5000)
    await flushPromises()
    expect(mocks.getDepositHistory).toHaveBeenCalledTimes(2)
    expect(wrapper.find('.deposit-view__pending').exists()).toBe(false)

    await vi.advanceTimersByTimeAsync(5000)
    await flushPromises()
    expect(mocks.getDepositHistory).toHaveBeenCalledTimes(3)

    await vi.advanceTimersByTimeAsync(5000)
    await flushPromises()
    expect(mocks.getDepositHistory).toHaveBeenCalledTimes(4)
  })

  it('pauses while the tab is hidden and refreshes as soon as it is visible again', async () => {
    fakeTimers()
    mocks.getDepositHistory.mockResolvedValue([pendingRow({ confirmations: 1, required_confirmations: 3 })])
    render()
    await flushPromises()
    expect(mocks.getDepositHistory).toHaveBeenCalledTimes(1)

    setVisibility('hidden')
    await vi.advanceTimersByTimeAsync(40000)
    await flushPromises()
    expect(mocks.getDepositHistory).toHaveBeenCalledTimes(1)

    setVisibility('visible')
    await flushPromises()
    expect(mocks.getDepositHistory).toHaveBeenCalledTimes(2)

    await vi.advanceTimersByTimeAsync(5000)
    await flushPromises()
    expect(mocks.getDepositHistory).toHaveBeenCalledTimes(3)
  })

  it('does not poll after the view is unmounted', async () => {
    fakeTimers()
    mocks.getDepositHistory.mockResolvedValue([pendingRow({ confirmations: 1, required_confirmations: 3 })])
    const wrapper = render()
    await flushPromises()
    expect(mocks.getDepositHistory).toHaveBeenCalledTimes(1)

    wrapper.unmount()
    await vi.advanceTimersByTimeAsync(30000)
    await flushPromises()
    expect(mocks.getDepositHistory).toHaveBeenCalledTimes(1)
  })

  it('does not overlap an in-flight read and keeps polling after a visibility refresh', async () => {
    fakeTimers()
    let release: (rows: unknown[]) => void = () => undefined
    mocks.getDepositHistory
      .mockResolvedValueOnce([creditedRow])
      .mockImplementationOnce(() => new Promise((resolve) => { release = resolve }))
      .mockResolvedValue([creditedRow])
    render()
    await flushPromises()
    expect(mocks.getDepositHistory).toHaveBeenCalledTimes(1)

    await vi.advanceTimersByTimeAsync(5000)
    await flushPromises()
    expect(mocks.getDepositHistory).toHaveBeenCalledTimes(2)

    // Becoming visible asks for an immediate read, but the previous one is
    // still in flight: no second request may be sent.
    setVisibility('visible')
    await flushPromises()
    expect(mocks.getDepositHistory).toHaveBeenCalledTimes(2)

    release([creditedRow])
    await flushPromises()
    await vi.advanceTimersByTimeAsync(5000)
    await flushPromises()
    expect(mocks.getDepositHistory).toHaveBeenCalledTimes(3)
  })

  it('refreshes the balance and shows a toast once when a confirming deposit is credited', async () => {
    fakeTimers()
    mocks.getDepositHistory
      .mockResolvedValueOnce([pendingRow({ tx_hash: hash, confirmations: 2, required_confirmations: 3 })])
      .mockResolvedValue([{ ...creditedRow, confirmations: 3, required_confirmations: 3 }])
    const wrapper = render()
    await flushPromises()
    expect(mocks.refreshUser).not.toHaveBeenCalled()

    await vi.advanceTimersByTimeAsync(5000)
    await flushPromises()
    expect(mocks.refreshUser).toHaveBeenCalledOnce()
    expect(mocks.showSuccess).toHaveBeenCalledWith('deposit.creditedToast {"amount":"67"}')
    expect(wrapper.find('.deposit-view__pending').exists()).toBe(false)

    // Credited rows keep the same 5 second cadence, but the toast must not repeat.
    await vi.advanceTimersByTimeAsync(10000)
    await flushPromises()
    expect(mocks.getDepositHistory).toHaveBeenCalledTimes(4)
    expect(mocks.showSuccess).toHaveBeenCalledOnce()
  })

  it('does not announce deposits that were already credited when the page opened', async () => {
    mocks.getDepositHistory.mockResolvedValue([creditedRow])
    render()
    await flushPromises()
    expect(mocks.refreshUser).not.toHaveBeenCalled()
    expect(mocks.showSuccess).not.toHaveBeenCalled()
  })
})

describe('user deposit currency selection', () => {
  const address = '0x' + '12'.repeat(20)

  function nativeChain() {
    return {
      enabled: true,
      chains: [{
        chain: 'ethereum', address, tokens: ['ETH', 'USDT', 'USDC'],
        assets: [
          { symbol: 'ETH', native: true, market_priced: true, rate: null, reference_price_usd: '3210.12' },
          { symbol: 'USDT', native: false, market_priced: false, rate: 6.7 },
          { symbol: 'USDC', native: false, market_priced: false, rate: 6.7 }
        ]
      }],
      min_amount: 0.001, max_amount: 0
    }
  }

  it('labels the mainnet coin with its network and keeps a stablecoin selected by default', async () => {
    mocks.getDepositInfo.mockResolvedValue(nativeChain())
    mocks.getDepositHistory.mockResolvedValue([])
    const wrapper = render()
    await flushPromises()
    expect(wrapper.findAll('.deposit-view__asset').map(a => a.text())).toEqual(['USDT', 'USDC', 'ETH (Ethereum)'])
    // An existing user must not land on a currency worth far more than a token.
    expect(wrapper.get('.deposit-view__notice').text()).toContain('deposit.networkNotice')
    expect(wrapper.get('.deposit-view__notice').text()).not.toContain('deposit.nativeNotice')
  })

  it('switches the transfer notice to the native wording when ETH is chosen', async () => {
    mocks.getDepositInfo.mockResolvedValue(nativeChain())
    mocks.getDepositHistory.mockResolvedValue([])
    const wrapper = render()
    await flushPromises()
    const eth = wrapper.findAll('.deposit-view__asset')[2]
    await eth.trigger('click')
    // The paragraph inside the notice, not the whole box: the box also renders
    // the decorative "!" mark.
    expect(wrapper.get('.deposit-view__notice p').text()).toBe(
      'deposit.nativeNotice {"network":"Ethereum","asset":"ETH"}'
    )
    // The address and QR code are shared; only the wording changes.
    expect(wrapper.get('code.deposit-view__address').text()).toBe(address)
    expect(wrapper.find('canvas').exists()).toBe(true)
  })

  it('derives assets from tokens when an older backend sends no asset list', async () => {
    mocks.getDepositInfo.mockResolvedValue({
      enabled: true, chains: [{ chain: 'polygon', address, tokens: ['USDT'] }],
      rate: 6.7, min_amount: 0.001, max_amount: 0
    })
    mocks.getDepositHistory.mockResolvedValue([])
    const wrapper = render()
    await flushPromises()
    // One asset needs no selector, and the notice keeps the legacy wording.
    expect(wrapper.find('.deposit-view__assets').exists()).toBe(false)
    expect(wrapper.get('.deposit-view__notice').text()).toContain('deposit.networkNotice')
    // An asset-independent rate from an old backend still renders for a
    // stablecoin, so a rolling deploy does not blank the sidebar.
    expect(wrapper.get('.deposit-view__terms').text()).toContain('1 USDT = 6.7 deposit.balanceUnits')
  })
})

describe('user deposit credit details', () => {
  const address = '0x' + '12'.repeat(20)

  // The shape the current backend returns: the rate lives on the asset, and a
  // market-priced native coin carries no rate at all.
  function marketChain(overrides: Record<string, unknown> = {}) {
    return {
      enabled: true,
      chains: [{
        chain: 'ethereum', address, tokens: ['ETH', 'USDT'],
        assets: [
          { symbol: 'ETH', native: true, market_priced: true, rate: null, reference_price_usd: '3210.12' },
          { symbol: 'USDT', native: false, market_priced: false, rate: 6.7 }
        ]
      }],
      min_amount: 0.001, max_amount: 10000,
      ...overrides
    }
  }

  async function selectAsset(wrapper: ReturnType<typeof render>, index: number) {
    await wrapper.findAll('.deposit-view__asset')[index].trigger('click')
  }

  it('replaces the fixed rate with the market settlement wording for a native coin', async () => {
    mocks.getDepositInfo.mockResolvedValue(marketChain())
    mocks.getDepositHistory.mockResolvedValue([])
    const wrapper = render()
    await flushPromises()
    await selectAsset(wrapper, 1)

    const terms = wrapper.get('.deposit-view__terms').text()
    expect(terms).toContain('deposit.settlementLabel')
    expect(terms).toContain('deposit.settlementMarket')
    // The bug report: "1 ETH = 6.7 balance units" must be impossible.
    expect(terms).not.toContain('deposit.rateLabel')
    expect(terms).not.toContain('= 6.7')
    expect(terms).not.toContain('balanceUnits')
    // Limits are compared as USD notional for a native coin, not as ETH.
    expect(terms).toContain('deposit.minLabelUsd')
    expect(terms).toContain('0.001 USD')
    expect(terms).toContain('deposit.maxLabelUsd')
    expect(terms).toContain('10000 USD')
  })

  it('shows the display-only market quote under the settlement line', async () => {
    mocks.getDepositInfo.mockResolvedValue(marketChain())
    mocks.getDepositHistory.mockResolvedValue([])
    const wrapper = render()
    await flushPromises()
    await selectAsset(wrapper, 1)
    expect(wrapper.get('.deposit-view__reference').text()).toBe('deposit.referencePrice {"price":"$3210.12"}')
  })

  it('omits the quote when the price feed was unavailable', async () => {
    mocks.getDepositInfo.mockResolvedValue(marketChain({
      chains: [{
        chain: 'ethereum', address, tokens: ['ETH', 'USDT'],
        assets: [
          { symbol: 'ETH', native: true, market_priced: true, rate: null },
          { symbol: 'USDT', native: false, market_priced: false, rate: 6.7 }
        ]
      }]
    }))
    mocks.getDepositHistory.mockResolvedValue([])
    const wrapper = render()
    await flushPromises()
    await selectAsset(wrapper, 1)
    expect(wrapper.get('.deposit-view__terms').text()).toContain('deposit.settlementMarket')
    expect(wrapper.find('.deposit-view__reference').exists()).toBe(false)
  })

  it('keeps the fixed rate line and token-unit limits for a stablecoin', async () => {
    mocks.getDepositInfo.mockResolvedValue(marketChain())
    mocks.getDepositHistory.mockResolvedValue([])
    const wrapper = render()
    await flushPromises()
    // USDT is selected by default so an existing user never lands on ETH.
    const terms = wrapper.get('.deposit-view__terms').text()
    expect(terms).toContain('1 USDT = 6.7 deposit.balanceUnits')
    expect(terms).toContain('deposit.minLabel')
    expect(terms).toContain('0.001 USDT')
    expect(terms).not.toContain('deposit.settlementMarket')
    expect(wrapper.find('.deposit-view__reference').exists()).toBe(false)
  })

  it('explains a rejected native deposit with the USD threshold, not USDT', async () => {
    mocks.getDepositInfo.mockResolvedValue(marketChain())
    mocks.getDepositHistory.mockResolvedValue([{
      chain: 'ethereum', tx_hash: hash, token: 'native', amount: '0.0000001',
      status: 'ignored', reason: 'below_min', credited: 0, created_at: '2026-01-01T00:00:00Z'
    }])
    const wrapper = render()
    await flushPromises()
    expect(wrapper.get('.deposit-view__reason').text()).toBe('deposit.reasonBelowMinUsd {"min":0.001}')
  })
})

describe('user deposit key rotation gate', () => {
  const gateError = {
    status: 403,
    reason: 'KEY_ROTATION_REQUIRED',
    metadata: { error_code: 'KEY_ROTATION_REQUIRED' },
  }

  afterEach(() => {
    keyRotationGateMock.requiredRef.value = false
    keyRotationGateMock.statusRef.value = null
  })

  it.each([true, false])('shows remediation without fetching history when info is denied (shared gate: %s)', async (sharedGate) => {
    keyRotationGateMock.requiredRef.value = sharedGate
    mocks.getDepositInfo.mockRejectedValueOnce(gateError)
    const wrapper = render()
    await flushPromises()

    expect(wrapper.find('[data-testid="key-rotation-gate-notice"]').exists()).toBe(true)
    expect(wrapper.text()).not.toContain('deposit.loadFailed')
    expect(wrapper.text()).not.toContain('deposit.disabled')
    expect(wrapper.find('.deposit-view__history').exists()).toBe(false)
    expect(mocks.getDepositHistory).not.toHaveBeenCalled()
    if (sharedGate) expect(mocks.getDepositNetworkStatus).not.toHaveBeenCalled()
  })

  it('treats an explicit denial from the network status read as a key rotation requirement', async () => {
    const wrapper = render()
    await flushPromises()
    expect(wrapper.get('.deposit-view__history').text()).toContain(hash)

    mocks.getDepositNetworkStatus.mockRejectedValueOnce(gateError)
    setVisibility('visible')
    await flushPromises()

    expect(wrapper.find('[data-testid="key-rotation-gate-notice"]').exists()).toBe(true)
    expect(wrapper.find('.deposit-view__history').exists()).toBe(false)
    expect(wrapper.find('.deposit-view__address-block').exists()).toBe(false)
    expect(wrapper.text()).not.toContain(hash)
  })

  it('hides the previous history and stops polling when a refresh is denied by the gate', async () => {
    fakeTimers()
    const wrapper = render()
    await flushPromises()
    expect(wrapper.get('.deposit-view__history').text()).toContain(hash)

    mocks.getDepositHistory.mockRejectedValueOnce(gateError)
    await wrapper.get('.deposit-view__history-head button').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-testid="key-rotation-gate-notice"]').exists()).toBe(true)
    expect(wrapper.find('.deposit-view__history').exists()).toBe(false)
    expect(wrapper.find('.deposit-view__address-block').exists()).toBe(false)
    expect(wrapper.text()).not.toContain(hash)
    setVisibility('visible')
    await vi.advanceTimersByTimeAsync(15000)
    expect(mocks.getDepositHistory).toHaveBeenCalledTimes(2)
  })
})

describe('user deposit history: instant credit state', () => {
  it('marks a provisional row and explains that the credit can still be rolled back', async () => {
    fakeTimers()
    mocks.getDepositHistory.mockResolvedValueOnce([
      {
        chain: 'bsc', tx_hash: hash, token: 'USDT', amount: '10', status: 'provisional',
        credited: 67, credited_known: true, provisional: true, credited_at: '2026-01-01T00:00:01Z',
        created_at: '2026-01-01T00:00:00Z'
      }
    ])
    const wrapper = render()
    await flushPromises()

    const history = wrapper.get('.deposit-view__history')
    expect(history.text()).toContain('deposit.statusProvisional')
    expect(wrapper.get('[data-testid="deposit-provisional-hint"]').text()).toBe('deposit.provisionalHint')
    // A provisional credit already moved the balance, so the amount is shown.
    expect(history.text()).toContain('67')
    expect(wrapper.find('[data-testid="deposit-revoked-hint"]').exists()).toBe(false)
  })

  it('marks a revoked row, translates the reason and tells the user to contact support', async () => {
    fakeTimers()
    mocks.getDepositHistory.mockResolvedValueOnce([
      {
        chain: 'bsc', tx_hash: hash, token: 'USDT', amount: '10', status: 'revoked',
        credited: 0, credited_known: false, provisional: true,
        revoked_at: '2026-01-01T00:00:30Z', revoke_reason: 'reorg_noncanonical_block',
        created_at: '2026-01-01T00:00:00Z'
      }
    ])
    const wrapper = render()
    await flushPromises()

    const history = wrapper.get('.deposit-view__history')
    expect(history.text()).toContain('deposit.statusRevoked')
    expect(wrapper.get('[data-testid="deposit-revoke-reason"]').text()).toBe('deposit.revokeReasonReorgNoncanonical')
    expect(wrapper.get('[data-testid="deposit-revoked-hint"]').text()).toBe('deposit.revokedHint')
    expect(history.text()).not.toContain('deposit.provisionalHint')
  })

  it('renders an unknown revoke reason as a readable fallback instead of a raw machine code', async () => {
    fakeTimers()
    mocks.getDepositHistory.mockResolvedValueOnce([
      {
        chain: 'bsc', tx_hash: hash, token: 'USDT', amount: '10', status: 'revoked',
        revoked_at: '2026-01-01T00:00:30Z', revoke_reason: 'reorg_new_reason', created_at: '2026-01-01T00:00:00Z'
      }
    ])
    const wrapper = render()
    await flushPromises()

    expect(wrapper.get('[data-testid="deposit-revoke-reason"]').text()).toBe(
      'deposit.revokeReasonGeneric {"code":"reorg_new_reason"}'
    )
  })
})

describe('user deposit confirmation progress: in-flight states', () => {
  it('shows the confirmation depth of a provisional credit without calling it pending', async () => {
    mocks.getDepositHistory.mockResolvedValue([
      {
        chain: 'bsc', tx_hash: hash, token: 'USDT', amount: '10', status: 'provisional',
        credited: 67, credited_known: true, confirmations: 1, required_confirmations: 3,
        created_at: '2026-01-01T00:00:00Z'
      }
    ])
    const wrapper = render()
    await flushPromises()

    expect(wrapper.get('[data-testid="deposit-progress-text-provisional"]').text())
      .toBe('deposit.provisionalProgress {"done":1,"required":3,"remaining":2}')
    const bar = wrapper.get('[role="progressbar"]')
    expect(bar.attributes('aria-valuenow')).toBe('1')
    expect(bar.attributes('aria-valuemax')).toBe('3')
    expect(wrapper.get('.deposit-view__progress-fill').attributes('style')).toContain('scaleX(0.33)')
    // 临时入账的说明还在：进度条不能把「可能被回滚」这件事挤掉。
    expect(wrapper.get('[data-testid="deposit-provisional-hint"]').text()).toBe('deposit.provisionalHint')
  })

  it('falls back to a counter-free provisional line when no counters are sent', async () => {
    mocks.getDepositHistory.mockResolvedValue([
      {
        chain: 'bsc', tx_hash: hash, token: 'USDT', amount: '10', status: 'provisional',
        credited: 67, credited_known: true, created_at: '2026-01-01T00:00:00Z'
      }
    ])
    const wrapper = render()
    await flushPromises()

    expect(wrapper.get('[data-testid="deposit-progress-text-provisional"]').text())
      .toBe('deposit.provisionalConfirming')
    expect(wrapper.find('[role="progressbar"]').exists()).toBe(false)
  })

  it('does not draw a progress line for finalized rows', async () => {
    mocks.getDepositHistory.mockResolvedValue([
      { chain: 'bsc', tx_hash: hash, token: 'USDT', amount: '10', status: 'credited', credited: 67, confirmations: 3, required_confirmations: 3, created_at: '2026-01-01T00:00:00Z' }
    ])
    const wrapper = render()
    await flushPromises()

    expect(wrapper.find('.deposit-view__progress').exists()).toBe(false)
  })
})

describe('user deposit streamlined funding steps', () => {
  const bscAddress = '0x' + '12'.repeat(20)

  it('omits the removed amount pre-check step and lets users copy the deposit address directly', async () => {
    mocks.getDepositHistory.mockResolvedValue([])
    mocks.copy.mockResolvedValueOnce(true)
    const wrapper = render()
    await flushPromises()

    expect(wrapper.find('[data-testid="deposit-amount-input"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="deposit-copy-blocked"]').exists()).toBe(false)
    expect(wrapper.findAll('.deposit-view__step').map(step => step.text())).toEqual(['01', '02'])

    const copyBtn = wrapper.get('.deposit-view__copy')
    expect((copyBtn.element as HTMLButtonElement).disabled).toBe(false)
    await copyBtn.trigger('click')
    expect(mocks.copy).toHaveBeenCalledWith(bscAddress)
  })
})

describe('user deposit history presentation and smooth updates', () => {
  const creditedRow = {
    chain: 'bsc', tx_hash: hash, token: 'USDT', amount: '10',
    status: 'credited', credited: 67, created_at: '2026-01-01T00:00:00Z'
  }
  const incomingHash = '0x' + 'ef'.repeat(32)
  const incomingRow = {
    chain: 'ethereum', tx_hash: incomingHash, token: 'ETH', amount: '0.25',
    status: 'pending', credited: 0, confirmations: 1, required_confirmations: 3,
    created_at: '2026-01-03T00:00:00Z'
  }

  it('does not render circular icon/avatar plates on deposit history rows', async () => {
    const wrapper = render()
    await flushPromises()

    expect(wrapper.findAll('[data-testid="deposit-history-row"]')).toHaveLength(2)
    expect(wrapper.find('[data-testid="transaction-icon"]').exists()).toBe(false)
  })

  it('keeps rows rendered without skeleton flicker during background 5s polls and skips unchanged snapshots', async () => {
    fakeTimers()
    let releasePoll!: (rows: unknown[]) => void
    mocks.getDepositHistory
      .mockResolvedValueOnce([creditedRow])
      .mockImplementationOnce(() => new Promise(resolve => { releasePoll = resolve }))
    const wrapper = render()
    await flushPromises()

    const rowBefore = wrapper.get('[data-testid="deposit-history-row"]').element
    expect(wrapper.find('[data-testid="transaction-list-loading"]').exists()).toBe(false)

    // Advance to the 5s background poll while the request is still in flight.
    await vi.advanceTimersByTimeAsync(5000)
    await flushPromises()
    expect(mocks.getDepositHistory).toHaveBeenCalledTimes(2)
    // Rows stay visible and steady — no skeleton replacement, no refresh button spinner.
    expect(wrapper.find('[data-testid="transaction-list-loading"]').exists()).toBe(false)
    expect(wrapper.findAll('[data-testid="deposit-history-row"]')).toHaveLength(1)
    expect((wrapper.get('.deposit-view__history-head button').element as HTMLButtonElement).disabled).toBe(false)

    // Resolve with unchanged ledger fields (even if preview fixture timestamp ticks).
    releasePoll([{ ...creditedRow, created_at: '2026-01-01T00:00:05Z' }])
    await flushPromises()

    const rowAfter = wrapper.get('[data-testid="deposit-history-row"]')
    expect(rowAfter.element).toBe(rowBefore)
    expect(rowAfter.attributes('data-fresh')).toBeUndefined()
  })

  it('animates only newly arriving records and clears their fresh state after the highlight window', async () => {
    fakeTimers()
    mocks.getDepositHistory
      .mockResolvedValueOnce([creditedRow])
      .mockResolvedValueOnce([incomingRow, creditedRow])
      .mockResolvedValue([incomingRow, creditedRow])
    const wrapper = render()
    await flushPromises()

    // Existing rows loaded initially are not marked as newly popped-in.
    expect(wrapper.get('[data-testid="deposit-history-row"]').attributes('data-fresh')).toBeUndefined()

    await vi.advanceTimersByTimeAsync(5000)
    await flushPromises()

    const rows = wrapper.findAll('[data-testid="deposit-history-row"]')
    expect(rows).toHaveLength(2)
    expect(rows[0].text()).toContain(incomingHash)
    expect(rows[0].attributes('data-fresh')).toBe('true')
    expect(rows[1].attributes('data-fresh')).toBeUndefined()

    // Highlight state clears automatically once the entrance animation finishes.
    await vi.advanceTimersByTimeAsync(2400)
    await flushPromises()
    expect(wrapper.findAll('[data-testid="deposit-history-row"]')[0].attributes('data-fresh')).toBeUndefined()
  })

  it('marks the first deposit discovered after an empty history as fresh', async () => {
    fakeTimers()
    mocks.getDepositHistory
      .mockResolvedValueOnce([])
      .mockResolvedValue([incomingRow])
    const wrapper = render()
    await flushPromises()
    expect(wrapper.find('[data-testid="deposit-no-history"]').exists()).toBe(true)

    await vi.advanceTimersByTimeAsync(5000)
    await flushPromises()
    const rows = wrapper.findAll('[data-testid="deposit-history-row"]')
    expect(rows).toHaveLength(1)
    expect(rows[0].attributes('data-fresh')).toBe('true')
  })

  it('updates confirmation progress in place without marking an existing row as new or flashing skeletons', async () => {
    fakeTimers()
    mocks.getDepositHistory
      .mockResolvedValueOnce([pendingRow({ confirmations: 1, required_confirmations: 3 })])
      .mockResolvedValue([pendingRow({ confirmations: 2, required_confirmations: 3 })])
    const wrapper = render()
    await flushPromises()

    expect(wrapper.get('.deposit-view__progress-fill').attributes('style')).toContain('scaleX(0.33)')

    await vi.advanceTimersByTimeAsync(5000)
    await flushPromises()
    expect(wrapper.find('[data-testid="transaction-list-loading"]').exists()).toBe(false)
    expect(wrapper.get('.deposit-view__progress-fill').attributes('style')).toContain('scaleX(0.67)')
    expect(wrapper.get('[data-testid="deposit-history-row"]').attributes('data-fresh')).toBeUndefined()
  })

  it('keeps existing rows visible while a manual refresh is in flight', async () => {
    let releaseManual!: (rows: unknown[]) => void
    mocks.getDepositHistory
      .mockResolvedValueOnce([creditedRow])
      .mockImplementationOnce(() => new Promise(resolve => { releaseManual = resolve }))
    const wrapper = render()
    await flushPromises()

    await wrapper.get('.deposit-view__history-head button').trigger('click')
    await flushPromises()

    expect(wrapper.find('[data-testid="transaction-list-loading"]').exists()).toBe(false)
    expect(wrapper.findAll('[data-testid="deposit-history-row"]')).toHaveLength(1)
    expect((wrapper.get('.deposit-view__history-head button').element as HTMLButtonElement).disabled).toBe(true)

    releaseManual([incomingRow, creditedRow])
    await flushPromises()
    expect((wrapper.get('.deposit-view__history-head button').element as HTMLButtonElement).disabled).toBe(false)
    expect(wrapper.findAll('[data-testid="deposit-history-row"]')[0].attributes('data-fresh')).toBe('true')
  })

  it('animates only the newly arrived transfer when a second deposit shares the same tx_hash', async () => {
    fakeTimers()
    const secondSameTx = {
      chain: 'bsc', tx_hash: hash, token: 'USDC', amount: '25',
      status: 'pending', credited: 0, confirmations: 1, required_confirmations: 3,
      created_at: '2026-01-01T00:00:05Z'
    }
    mocks.getDepositHistory
      .mockResolvedValueOnce([creditedRow])
      .mockResolvedValue([secondSameTx, creditedRow])
    const wrapper = render()
    await flushPromises()

    const originalRowEl = wrapper.get('[data-testid="deposit-history-row"]').element
    await vi.advanceTimersByTimeAsync(5000)
    await flushPromises()

    const rows = wrapper.findAll('[data-testid="deposit-history-row"]')
    expect(rows).toHaveLength(2)
    expect(rows[0].attributes('data-fresh')).toBe('true')
    expect(rows[1].attributes('data-fresh')).toBeUndefined()
    expect(rows[1].element).toBe(originalRowEl)
  })

  it('clears fresh animation markers when switching local filters so returning rows never replay pop-in', async () => {
    fakeTimers()
    mocks.getDepositHistory
      .mockResolvedValueOnce([creditedRow])
      .mockResolvedValue([incomingRow, creditedRow])
    const wrapper = render()
    await flushPromises()

    await vi.advanceTimersByTimeAsync(5000)
    await flushPromises()
    expect(wrapper.findAll('[data-testid="deposit-history-row"]')[0].attributes('data-fresh')).toBe('true')

    await wrapper.get('[data-testid="deposit-filter-status-credited"]').trigger('click')
    await wrapper.get('[data-testid="deposit-filter-status-all"]').trigger('click')
    const rows = wrapper.findAll('[data-testid="deposit-history-row"]')
    expect(rows).toHaveLength(2)
    expect(rows[0].attributes('data-fresh')).toBeUndefined()
    expect(rows[1].attributes('data-fresh')).toBeUndefined()
  })

  it('shows manual refresh loading feedback if clicked while a silent 5s background poll is already in flight', async () => {
    fakeTimers()
    let releasePoll!: (rows: unknown[]) => void
    mocks.getDepositHistory
      .mockResolvedValueOnce([creditedRow])
      .mockImplementationOnce(() => new Promise(resolve => { releasePoll = resolve }))
    const wrapper = render()
    await flushPromises()

    await vi.advanceTimersByTimeAsync(5000)
    await flushPromises()
    expect(mocks.getDepositHistory).toHaveBeenCalledTimes(2)
    expect((wrapper.get('.deposit-view__history-head button').element as HTMLButtonElement).disabled).toBe(false)

    // Clicking Refresh during the in-flight poll should show loading feedback without firing a duplicate request.
    await wrapper.get('.deposit-view__history-head button').trigger('click')
    expect(mocks.getDepositHistory).toHaveBeenCalledTimes(2)
    expect((wrapper.get('.deposit-view__history-head button').element as HTMLButtonElement).disabled).toBe(true)

    releasePoll([creditedRow])
    await flushPromises()
    expect((wrapper.get('.deposit-view__history-head button').element as HTMLButtonElement).disabled).toBe(false)
  })

  it('keeps loaded history visible without flashing skeletons when retrying a failed deposit-info load', async () => {
    let releaseRetryInfo!: (value: unknown) => void
    mocks.getDepositInfo
      .mockRejectedValueOnce({ status: 503 })
      .mockImplementationOnce(() => new Promise(resolve => { releaseRetryInfo = resolve }))
    mocks.getDepositHistory.mockResolvedValue([creditedRow])
    const wrapper = render()
    await flushPromises()

    expect(wrapper.findAll('[data-testid="deposit-history-row"]')).toHaveLength(1)
    await wrapper.get('[role="alert"] button').trigger('click')
    await flushPromises()

    // While deposit-info retries, existing history rows remain mounted instead of flashing skeletons.
    expect(wrapper.find('[data-testid="transaction-list-loading"]').exists()).toBe(false)
    expect(wrapper.findAll('[data-testid="deposit-history-row"]')).toHaveLength(1)

    releaseRetryInfo({
      enabled: true, chains: [{ chain: 'bsc', address: '0x' + '12'.repeat(20), tokens: ['USDT'] }],
      rate: 6.7, min_amount: 1, max_amount: 0
    })
    await flushPromises()
    expect(wrapper.find('.deposit-view__address-block').exists()).toBe(true)
  })
})

describe('user deposit history filters', () => {
  const isoDaysAgo = (days: number) => new Date(Date.now() - days * 24 * 60 * 60 * 1000).toISOString()

  function historyRow(status: string, hashValue: string, daysAgo: number) {
    return {
      chain: 'bsc', tx_hash: hashValue, token: 'USDT', amount: '10',
      status, credited: status === 'credited' ? 67 : 0, created_at: isoDaysAgo(daysAgo)
    }
  }

  beforeEach(() => {
    mocks.getDepositHistory.mockResolvedValue([
      historyRow('credited', '0x' + 'a1'.repeat(32), 1),
      historyRow('provisional', '0x' + 'b2'.repeat(32), 2),
      historyRow('revoked', '0x' + 'c3'.repeat(32), 3),
      historyRow('ignored', '0x' + 'd4'.repeat(32), 40),
      historyRow('pending', '0x' + 'e5'.repeat(32), 1),
    ])
  })

  const tableRows = (wrapper: ReturnType<typeof render>) => wrapper.findAll('[data-testid="deposit-history-row"]')

  it('filters by status locally without re-reading the API', async () => {
    const wrapper = render()
    await flushPromises()
    expect(tableRows(wrapper)).toHaveLength(5)

    await wrapper.get('[data-testid="deposit-filter-status-revoked"]').trigger('click')
    expect(tableRows(wrapper)).toHaveLength(1)
    expect(tableRows(wrapper)[0].text()).toContain('0x' + 'c3'.repeat(32))
    expect(wrapper.get('[data-testid="deposit-filter-status-revoked"]').attributes('aria-pressed')).toBe('true')
    expect(wrapper.get('[data-testid="deposit-filter-note"]').text())
      .toBe('deposit.filterResult {"n":1,"total":5}')
    // 本地过滤：不重新请求、不打断 5 秒轮询节奏。
    expect(mocks.getDepositHistory).toHaveBeenCalledOnce()
  })

  it('filters by time range and combines it with the status', async () => {
    const wrapper = render()
    await flushPromises()

    await wrapper.get('[data-testid="deposit-filter-range-7d"]').trigger('click')
    // 40 天前那条被时间范围挡掉，其余 4 条保留。
    expect(tableRows(wrapper)).toHaveLength(4)
    expect(wrapper.get('.deposit-view__history').text()).not.toContain('0x' + 'd4'.repeat(32))

    await wrapper.get('[data-testid="deposit-filter-status-credited"]').trigger('click')
    expect(tableRows(wrapper)).toHaveLength(1)
    expect(tableRows(wrapper)[0].text()).toContain('0x' + 'a1'.repeat(32))

    await wrapper.get('[data-testid="deposit-filter-status-provisional"]').trigger('click')
    expect(tableRows(wrapper)).toHaveLength(1)
    expect(tableRows(wrapper)[0].text()).toContain('0x' + 'b2'.repeat(32))
  })

  it('shows an empty state with a reset action when nothing matches', async () => {
    const wrapper = render()
    await flushPromises()

    // 已忽略那笔在 40 天前，加上 7 天范围就一条都不剩。
    await wrapper.get('[data-testid="deposit-filter-status-ignored"]').trigger('click')
    await wrapper.get('[data-testid="deposit-filter-range-7d"]').trigger('click')
    expect(tableRows(wrapper)).toHaveLength(0)
    expect(wrapper.get('[data-testid="deposit-no-filtered-history"]').text()).toContain('deposit.noFilteredHistory')
    expect(wrapper.get('[data-testid="deposit-filter-note"]').text())
      .toBe('deposit.filterResult {"n":0,"total":5}')

    await wrapper.get('[data-testid="deposit-reset-filters"]').trigger('click')
    expect(tableRows(wrapper)).toHaveLength(5)
    expect(wrapper.find('[data-testid="deposit-filter-note"]').exists()).toBe(false)
  })

  it('says the list is capped at the most recent 50 rows when the page is full', async () => {
    mocks.getDepositHistory.mockResolvedValue(
      Array.from({ length: 50 }, (_, index) => historyRow('credited', '0x' + (index % 10).toString().repeat(64), 1))
    )
    const wrapper = render()
    await flushPromises()

    expect(wrapper.get('[data-testid="deposit-history-limit"]').text())
      .toBe('deposit.historyLimitHint {"n":50}')
    // 只有 49 条时不提示上限，免得以为记录被截断。
    mocks.getDepositHistory.mockResolvedValue(
      Array.from({ length: 49 }, (_, index) => historyRow('credited', '0x' + (index % 10).toString().repeat(64), 1))
    )
    await wrapper.get('.deposit-view__history-head button').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-testid="deposit-history-limit"]').exists()).toBe(false)
  })
})

describe('user deposit revoked support entry', () => {
  const revoked = {
    chain: 'bsc', tx_hash: hash, token: 'USDT', amount: '10', status: 'revoked',
    credited: 0, credited_known: false, revoked_at: '2026-01-01T00:00:30Z',
    revoke_reason: 'reorg_noncanonical_block', created_at: '2026-01-01T00:00:00Z'
  }

  it('links to the site-wide support contact when it is a URL', async () => {
    appStoreState.contactInfo = 'https://t.me/kedaya_help'
    mocks.getDepositHistory.mockResolvedValue([revoked])
    const wrapper = render()
    await flushPromises()

    const link = wrapper.get('[data-testid="deposit-support-link"]')
    expect(link.attributes('href')).toBe('https://t.me/kedaya_help')
    expect(link.attributes('target')).toBe('_blank')
    expect(link.attributes('rel')).toBe('noopener noreferrer')
    expect(link.text()).toBe('deposit.revokedSupportAction')
    expect(wrapper.find('[data-testid="deposit-support-copy"]').exists()).toBe(false)
  })

  it('offers a copy action for a free-text contact such as QQ', async () => {
    appStoreState.contactInfo = 'QQ: 123456789'
    mocks.copy.mockResolvedValue(true)
    mocks.getDepositHistory.mockResolvedValue([revoked])
    const wrapper = render()
    await flushPromises()

    expect(wrapper.find('[data-testid="deposit-support-link"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="deposit-support-copy"]').text()).toBe('deposit.revokedSupportAction')
    expect(wrapper.get('.deposit-view__support').text()).toContain('QQ: 123456789')

    await wrapper.get('[data-testid="deposit-support-copy"]').trigger('click')
    await flushPromises()
    expect(mocks.copy).toHaveBeenCalledWith('QQ: 123456789')
    expect(wrapper.get('[data-testid="deposit-support-copied"]').text()).toBe('deposit.supportCopied')
  })

  it('explains what to do when no support contact is configured', async () => {
    mocks.getDepositHistory.mockResolvedValue([revoked])
    const wrapper = render()
    await flushPromises()

    expect(wrapper.get('.deposit-view__support').text()).toBe('deposit.supportUnavailable')
    expect(wrapper.find('[data-testid="deposit-support-link"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="deposit-support-copy"]').exists()).toBe(false)
    // 已回滚的原因和提示不能被客服入口挤掉。
    expect(wrapper.get('[data-testid="deposit-revoke-reason"]').text()).toBe('deposit.revokeReasonReorgNoncanonical')
    expect(wrapper.get('[data-testid="deposit-revoked-hint"]').text()).toBe('deposit.revokedHint')
  })

  it('does not render the support entry for non-revoked rows', async () => {
    const wrapper = render()
    await flushPromises()

    expect(wrapper.find('.deposit-view__support').exists()).toBe(false)
  })
})
