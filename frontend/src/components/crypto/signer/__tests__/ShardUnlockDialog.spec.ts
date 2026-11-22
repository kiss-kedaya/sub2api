import { webcrypto } from 'node:crypto'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'

import vectors from '@/utils/signer/__tests__/fixtures/goInteropVectors.json'
import { detectSignerCrypto } from '@/utils/signer/cryptoProvider'
import ShardUnlockDialog from '../ShardUnlockDialog.vue'

const api = vi.hoisted(() => ({ unlockSignerWithShare: vi.fn() }))
vi.mock('@/api/admin/cryptoSigner', () => ({ unlockSignerWithShare: api.unlockSignerWithShare }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
enableAutoUnmount(afterEach)

const crypto = detectSignerCrypto({
  forceBackend: 'noble',
  env: {
    isSecureContext: true,
    getRandomValues: (array: ArrayBufferView) => webcrypto.getRandomValues(array as Uint8Array),
    subtle: null
  }
})
const share = vectors.shard_vectors[2].shares[1]
const audit = {
  addresses: 3,
  planned: 3,
  swept: 2,
  failed: 0,
  amountUsd: 12.5,
  txHashes: ['0xaa', '0xbb'],
  status: 'completed',
  dryRun: false
}

async function render(props: Record<string, unknown> = {}) {
  const wrapper = mount(ShardUnlockDialog, {
    props: { show: true, crypto, ...props },
    global: { stubs: { QrCameraScanner: true, teleport: true } }
  })
  await flushPromises()
  await new Promise(resolve => setTimeout(resolve, 0))
  return wrapper
}

async function fill(wrapper: Awaited<ReturnType<typeof render>>): Promise<void> {
  await wrapper.get('#shard-unlock-input').setValue(share)
  await flushPromises()
  await wrapper.get('#shard-unlock-totp').setValue('123456')
  await flushPromises()
}

describe('signer unlock dialog (sweep + verify)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('shows the locked guidance when the signer is locked', async () => {
    const wrapper = await render({ locked: true })
    expect(wrapper.text()).toContain('admin.crypto.signer.unlock.lockedNotice')
  })

  it('keeps submit disabled until a valid share and a 6-digit code are present', async () => {
    const wrapper = await render({ runSweep: vi.fn() })
    const submit = wrapper.get('[data-testid="shard-unlock-submit"]')
    expect(submit.attributes('disabled')).toBeDefined()
    await wrapper.get('#shard-unlock-input').setValue(share)
    await flushPromises()
    expect(submit.attributes('disabled')).toBeDefined()
    await wrapper.get('#shard-unlock-totp').setValue('123456')
    await flushPromises()
    expect(submit.attributes('disabled')).toBeUndefined()
  })

  it('runs the sweep with the share + fresh code and shows the audit summary', async () => {
    const runSweep = vi.fn().mockResolvedValue(audit)
    const wrapper = await render({ runSweep })
    await fill(wrapper)
    await wrapper.get('[data-testid="shard-unlock-submit"]').trigger('click')
    await flushPromises()
    expect(runSweep).toHaveBeenCalledWith(share.replace(/-/g, ''), '123456')
    const panel = wrapper.get('[data-testid="shard-unlock-audit"]')
    expect(panel.text()).toContain('2 / 3')
    expect(panel.text()).toContain('12.50')
    expect(panel.text()).toContain('0xaa')
    expect(wrapper.emitted('completed')?.[0]?.[0]).toMatchObject({ swept: 2, amountUsd: 12.5 })
  })

  it('maps a rejected share to the stable error message and returns to input', async () => {
    const runSweep = vi.fn().mockRejectedValue({ reason: 'CRYPTO_SIGNER_SHARE_INVALID' })
    const wrapper = await render({ runSweep })
    await fill(wrapper)
    await wrapper.get('[data-testid="shard-unlock-submit"]').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('admin.crypto.signer.errors.shareInvalid')
  })

  it('shows the 15-minute lock message for rate-limited attempts', async () => {
    const runSweep = vi.fn().mockRejectedValue({ reason: 'CRYPTO_SIGNER_RATE_LIMITED' })
    const wrapper = await render({ runSweep })
    await fill(wrapper)
    await wrapper.get('[data-testid="shard-unlock-submit"]').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('admin.crypto.signer.unlock.rateLimited')
  })

  it('verifies and unlocks in verify mode, emitting the bounded window', async () => {
    const status = {
      configured: true,
      vault_version: 3,
      locked: false,
      unlocked_until: '2026-11-23T01:15:00Z',
      unlock_lease_seconds: 899,
      auto_enabled: false
    }
    api.unlockSignerWithShare.mockResolvedValue(status)
    const wrapper = await render({ mode: 'verify' })
    await fill(wrapper)
    await wrapper.get('[data-testid="shard-unlock-submit"]').trigger('click')
    await flushPromises()
    expect(api.unlockSignerWithShare).toHaveBeenCalledWith(share.replace(/-/g, ''), '123456')
    expect(wrapper.text()).toContain('admin.crypto.signer.unlock.verifySuccess')
    expect(wrapper.emitted('unlocked')?.[0]?.[0]).toMatchObject({ locked: false, unlocked_until: '2026-11-23T01:15:00Z' })
  })
})
