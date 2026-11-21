import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'

import SignerShardsPanel from '../SignerShardsPanel.vue'

const api = vi.hoisted(() => ({ status: vi.fn(), notice: vi.fn() }))
vi.mock('@/api/admin/cryptoSigner', () => ({
  getSignerShardStatus: api.status,
  getSignerRiskNotice: api.notice,
  setupSignerShards: vi.fn(),
  resplitSignerShards: vi.fn(),
  verifySignerShare: vi.fn(),
  setSignerAutoSweep: vi.fn()
}))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showSuccess: vi.fn() }) }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
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
        ShardUnlockDialog: { template: '<div data-testid="unlock-stub" />' }
      }
    }
  })
  await flushPromises()
  return wrapper
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
    delete (window as { isSecureContext?: unknown }).isSecureContext
  })

  it('shows the migration entry point when shards are not configured', async () => {
    const wrapper = await render()
    expect(wrapper.text()).toContain('admin.crypto.signer.notConfiguredTitle')
    expect(wrapper.get('[data-testid="signer-generate"]').attributes('disabled')).toBeUndefined()
    expect(wrapper.find('[data-testid="signer-verify"]').exists()).toBe(false)
  })

  it('shows configured state, vault version and the locked guidance with an unlock action', async () => {
    api.status.mockResolvedValue({
      configured: true,
      scheme: 'shamir-gf256',
      threshold: 2,
      total: 3,
      vault_version: 3,
      locked: true,
      auto_enabled: false,
      created_at: '2026-11-22T02:00:00Z',
      dk_commitment: 'Y29tbWl0'
    })
    const wrapper = await render()
    expect(wrapper.text()).toContain('admin.crypto.signer.configuredTitle')
    expect(wrapper.text()).toContain('admin.crypto.signer.lockedHint')
    expect(wrapper.text()).toContain('Y29tbWl0')
    const unlock = wrapper.findAll('button').find(button => button.text().includes('admin.crypto.signer.goUnlock'))
    expect(unlock).toBeTruthy()
    await unlock!.trigger('click')
    expect(wrapper.emitted('open-sweep')).toHaveLength(1)
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
