import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'

import SignerShardsPanel from '../SignerShardsPanel.vue'

const api = vi.hoisted(() => ({ status: vi.fn(), notice: vi.fn() }))
const toasts = vi.hoisted(() => ({ showSuccess: vi.fn(), showWarning: vi.fn() }))
vi.mock('@/api/admin/cryptoSigner', () => ({
  getSignerShardStatus: api.status,
  getSignerRiskNotice: api.notice,
  setupSignerShards: vi.fn(),
  resplitSignerShards: vi.fn(),
  verifySignerShare: vi.fn(),
  setSignerAutoSweep: vi.fn()
}))
vi.mock('@/stores/app', () => ({ useAppStore: () => toasts }))
vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string, params?: Record<string, unknown>) => (params ? `${key}${JSON.stringify(params)}` : key)
  })
}))
enableAutoUnmount(afterEach)

const notice = {
  points: { zh: ['p1', 'p2', 'p3'], en: ['p1', 'p2', 'p3'] },
  phrases: { zh: 'phrase', en: 'phrase' },
  display_once: true,
  schemes: []
}

async function render() {
  const wrapper = mount(SignerShardsPanel, {
    global: {
      stubs: {
        SignerShardSetupWizard: { template: '<div data-testid="wizard-stub" />' },
        ShardUnlockDialog: { name: 'ShardUnlockDialog', emits: ['close', 'unlocked', 'completed'], template: '<div data-testid="unlock-stub" />' }
      }
    }
  })
  await flushPromises()
  return wrapper
}

/** Only interval/Date are faked so promise scheduling of the mount keeps working. */
function useClock(iso: string) {
  vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval', 'Date'] })
  vi.setSystemTime(new Date(iso))
}

const configuredStatus = (unlockedUntil?: string, extra: Record<string, unknown> = {}) => ({
  configured: true,
  scheme: 'shamir-gf256',
  threshold: 2,
  total: 3,
  vault_version: 3,
  locked: unlockedUntil === undefined,
  auto_enabled: false,
  created_at: '2026-11-22T02:00:00Z',
  dk_commitment: 'Y29tbWl0',
  ...(unlockedUntil === undefined ? {} : { unlocked_until: unlockedUntil }),
  ...extra
})

function verifyButton(wrapper: Awaited<ReturnType<typeof render>>) {
  return wrapper.get('[data-testid="signer-verify"]')
}

describe('signer shards settings panel', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    api.notice.mockResolvedValue(notice)
    api.status.mockResolvedValue({
      configured: false, vault_version: 2, locked: false, auto_enabled: false
    })
  })

  afterEach(() => {
    vi.useRealTimers()
    delete (window as { isSecureContext?: unknown }).isSecureContext
  })

  it('guides a fresh setup with steps instead of warning badges', async () => {
    const wrapper = await render()
    expect(wrapper.text()).toContain('admin.crypto.signer.notConfiguredTitle')
    expect(wrapper.get('[data-testid="signer-generate"]').attributes('disabled')).toBeUndefined()
    expect(wrapper.find('[data-testid="signer-verify"]').exists()).toBe(false)
    // Requirement 3/7: empty state is a guided path, and the secondary
    // "auto-sweep share disabled" badge must not live in the primary row.
    expect(wrapper.get('[data-testid="signer-setup-steps"]').text()).toContain('admin.crypto.signer.setupStep1')
    expect(wrapper.text()).not.toContain('admin.crypto.signer.autoOff')
  })

  it('shows the locked state in the lock colour with the unlock CTA', async () => {
    api.status.mockResolvedValue(configuredStatus())
    const wrapper = await render()
    const lock = wrapper.get('[data-testid="signer-lock-state"]')
    expect(lock.attributes('data-state')).toBe('locked')
    expect(lock.text()).toContain('admin.crypto.signer.locked')
    expect(wrapper.text()).toContain('admin.crypto.signer.lockedHint')
    const unlock = wrapper.findAll('button').find(button => button.text().includes('admin.crypto.signer.goUnlock'))
    expect(unlock).toBeTruthy()
    await unlock!.trigger('click')
    expect(wrapper.emitted('open-sweep')).toHaveLength(1)
    // Locked: unlocking is the primary action before a sweep.
    expect(verifyButton(wrapper).text()).toContain('admin.crypto.signer.verifyAction')
    expect(verifyButton(wrapper).classes()).toContain('signer-verify--primary')
  })

  it('counts down mm:ss while the window has more than a minute left', async () => {
    useClock('2026-11-23T00:00:00.000Z')
    api.status.mockResolvedValue(configuredStatus(new Date(Date.now() + 90_000).toISOString()))
    const wrapper = await render()
    expect(wrapper.get('[data-testid="signer-lock-state"]').attributes('data-state')).toBe('unlocked')
    const countdown = wrapper.get('[data-testid="signer-unlock-countdown"]')
    expect(countdown.text()).toContain('1:30')
    expect(countdown.attributes('data-tone')).toBe('success')
    expect(wrapper.find('[data-testid="signer-countdown-soon"]').exists()).toBe(false)
    // Unlocked: the same button becomes the secondary "re-unlock / extend".
    expect(verifyButton(wrapper).text()).toContain('admin.crypto.signer.reunlockAction')
    expect(verifyButton(wrapper).classes()).toContain('signer-verify--secondary')
  })

  it('turns the countdown warning in the last 60 seconds', async () => {
    useClock('2026-11-23T00:00:00.000Z')
    api.status.mockResolvedValue(configuredStatus(new Date(Date.now() + 30_000).toISOString()))
    const wrapper = await render()
    const countdown = wrapper.get('[data-testid="signer-unlock-countdown"]')
    expect(countdown.text()).toContain('0:30')
    expect(countdown.attributes('data-tone')).toBe('warning')
    expect(wrapper.find('[data-testid="signer-countdown-soon"]').exists()).toBe(true)
  })

  it('reports the expired window as locked instead of showing a live countdown', async () => {
    useClock('2026-11-23T00:00:00.000Z')
    // The backend can keep locked:false for a moment after the lease: the
    // deadline alone decides, so 0:00 must never read as "unlocked".
    api.status.mockResolvedValue(configuredStatus(new Date(Date.now() - 5_000).toISOString()))
    const wrapper = await render()
    const lock = wrapper.get('[data-testid="signer-lock-state"]')
    expect(lock.attributes('data-state')).toBe('expired')
    expect(lock.text()).toContain('admin.crypto.signer.lockedExpired')
    expect(wrapper.find('[data-testid="signer-unlock-countdown"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="signer-window-expired"]').text()).toContain('admin.crypto.signer.windowExpiredHint')
    expect(verifyButton(wrapper).text()).toContain('admin.crypto.signer.verifyAction')
    expect(verifyButton(wrapper).classes()).toContain('signer-verify--primary')
  })

  it('refreshes the signer exactly once and toasts once when the window lapses', async () => {
    useClock('2026-11-23T00:00:00.000Z')
    api.status.mockResolvedValue(configuredStatus(new Date(Date.now() + 90_000).toISOString()))
    const wrapper = await render()
    const baseline = api.status.mock.calls.length
    expect(toasts.showWarning).not.toHaveBeenCalled()

    vi.advanceTimersByTime(91_000)
    await flushPromises()
    expect(api.status.mock.calls.length).toBe(baseline + 1)
    expect(toasts.showWarning).toHaveBeenCalledTimes(1)
    expect(toasts.showWarning).toHaveBeenCalledWith('admin.crypto.signer.windowExpiredToast')
    expect(wrapper.emitted('expired')).toHaveLength(1)

    // No polling loop: well past the deadline nothing extra fires.
    vi.advanceTimersByTime(300_000)
    await flushPromises()
    expect(api.status.mock.calls.length).toBe(baseline + 1)
    expect(toasts.showWarning).toHaveBeenCalledTimes(1)
    expect(wrapper.emitted('expired')).toHaveLength(1)
  })

  it('hides the data-key commitment behind 高级信息 by default', async () => {
    api.status.mockResolvedValue(configuredStatus())
    const wrapper = await render()
    expect(wrapper.find('[data-testid="signer-commitment"]').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('Y29tbWl0')

    await wrapper.get('[data-testid="signer-advanced-toggle"]').trigger('click')
    expect(wrapper.get('[data-testid="signer-commitment"]').text()).toContain('Y29tbWl0')
    expect(wrapper.text()).toContain('admin.crypto.signer.commitmentHint')

    await wrapper.get('[data-testid="signer-advanced-toggle"]').trigger('click')
    expect(wrapper.find('[data-testid="signer-commitment"]').exists()).toBe(false)
  })

  it('toasts once and starts the countdown after a successful unlock', async () => {
    useClock('2026-11-23T00:00:00.000Z')
    api.status.mockResolvedValue(configuredStatus())
    const wrapper = await render()
    await wrapper.get('[data-testid="signer-verify"]').trigger('click')

    const unlockedStatus = configuredStatus(new Date(Date.now() + 900_000).toISOString())
    api.status.mockResolvedValue(unlockedStatus)
    wrapper.findComponent({ name: 'ShardUnlockDialog' }).vm.$emit('unlocked', unlockedStatus)
    await flushPromises()

    expect(toasts.showSuccess).toHaveBeenCalledTimes(1)
    expect(toasts.showSuccess).toHaveBeenCalledWith('admin.crypto.signer.unlockDone')
    expect(wrapper.get('[data-testid="signer-lock-state"]').attributes('data-state')).toBe('unlocked')
    expect(wrapper.get('[data-testid="signer-unlock-countdown"]').text()).toContain('15:00')
  })

  it('keeps the merged refresh seam instead of a card-level button', async () => {
    api.status.mockResolvedValue(configuredStatus())
    const wrapper = await render()
    expect(wrapper.find('[data-testid="signer-shards-refresh"]').exists()).toBe(false)
    const baseline = api.status.mock.calls.length
    await (wrapper.vm as unknown as { refresh: () => Promise<void> }).refresh()
    expect(api.status.mock.calls.length).toBe(baseline + 1)
  })

  it('opens the setup wizard from the generate button', async () => {
    const wrapper = await render()
    await wrapper.get('[data-testid="signer-generate"]').trigger('click')
    expect(wrapper.find('[data-testid="wizard-stub"]').exists()).toBe(true)
  })

  it('surfaces a load error without breaking the page', async () => {
    api.status.mockRejectedValue({ reason: 'CRYPTO_DISABLED', message: 'crypto is disabled' })
    const wrapper = await render()
    expect(wrapper.text()).toContain('crypto is disabled')
  })

  it('disables generation and explains itself on a non-secure context', async () => {
    Object.defineProperty(window, 'isSecureContext', { configurable: true, value: false })
    const wrapper = await render()
    expect(wrapper.text()).toContain('admin.crypto.signer.insecureContext')
    expect(wrapper.get('[data-testid="signer-generate"]').attributes('disabled')).toBeDefined()
  })
})
