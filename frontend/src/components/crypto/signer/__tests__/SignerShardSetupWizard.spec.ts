import { webcrypto } from 'node:crypto'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'

import { detectSignerCrypto } from '@/utils/signer/cryptoProvider'
import { parseShare } from '@/utils/signer/shardFormat'
import SignerShardSetupWizard from '../SignerShardSetupWizard.vue'

const api = vi.hoisted(() => ({
  setupSignerShards: vi.fn(),
  resplitSignerShards: vi.fn()
}))
vi.mock('@/api/admin/cryptoSigner', () => ({
  setupSignerShards: api.setupSignerShards,
  resplitSignerShards: api.resplitSignerShards,
  getSignerRiskNotice: vi.fn(),
  getSignerShardStatus: vi.fn(),
  verifySignerShare: vi.fn(),
  setSignerAutoSweep: vi.fn()
}))
vi.mock('vue-i18n', () => ({
  useI18n: () => ({ t: (key: string) => key, locale: { value: 'zh-CN' } })
}))
enableAutoUnmount(afterEach)

const crypto = detectSignerCrypto({
  forceBackend: 'noble',
  env: {
    isSecureContext: true,
    getRandomValues: (array: ArrayBufferView) => webcrypto.getRandomValues(array as Uint8Array),
    subtle: null
  }
})
const PHRASE = '我已离线保存分片，丢失无法找回'
const notice = {
  points: { zh: ['p1', 'p2', 'p3'], en: ['p1', 'p2', 'p3'] },
  phrases: { zh: PHRASE, en: PHRASE },
  display_once: true,
  schemes: []
}
const doneStatus = {
  configured: true,
  scheme: 'shamir-gf256',
  threshold: 2,
  total: 3,
  vault_version: 3,
  locked: false,
  auto_enabled: false,
  created_at: '2026-11-22T02:00:00Z'
}

let storageSpy: ReturnType<typeof vi.spyOn> | null = null
let idbGetter: ReturnType<typeof vi.fn> | null = null

async function render(props: Record<string, unknown> = {}) {
  const wrapper = mount(SignerShardSetupWizard, {
    props: { show: true, crypto, notice, status: { ...doneStatus, dk_commitment: 'Y29tbWl0' }, ...props },
    global: { stubs: { teleport: true } }
  })
  await flushPromises()
  await new Promise(resolve => setTimeout(resolve, 0))
  return wrapper
}

async function passAuthAndRisk(wrapper: Awaited<ReturnType<typeof render>>): Promise<void> {
  await wrapper.get('#signer-wizard-totp').setValue('123456')
  await wrapper.get('[data-testid="signer-wizard-auth-next"]').trigger('click')
  await flushPromises()
  await new Promise(resolve => setTimeout(resolve, 0))
  await wrapper.get('#signer-risk-phrase-input').setValue(PHRASE)
  await wrapper.get('[data-testid="signer-risk-confirm"]').trigger('click')
  await flushPromises()
  // Key generation + SHA-256 checksums are async.
  await new Promise(resolve => setTimeout(resolve, 50))
  await flushPromises()
}

async function revealShares(wrapper: Awaited<ReturnType<typeof render>>): Promise<string[]> {
  const toggles = wrapper.findAll('[data-testid="shard-reveal-toggle"]')
  for (const toggle of toggles) await toggle.trigger('click')
  await flushPromises()
  return wrapper.findAll('.shard-reveal__text').map(node => node.text())
}

async function goToConfirm(wrapper: Awaited<ReturnType<typeof render>>): Promise<void> {
  await wrapper.get('[data-testid="signer-wizard-ack"]').trigger('click')
  await wrapper.get('[data-testid="signer-wizard-reveal-next"]').trigger('click')
  await flushPromises()
}

async function fillSubmit(wrapper: Awaited<ReturnType<typeof render>>, shareText: string, code = '654321'): Promise<void> {
  await wrapper.get('#signer-wizard-backfill').setValue(shareText)
  await flushPromises()
  await wrapper.get('#signer-wizard-submit-totp').setValue(code)
  await flushPromises()
}

describe('signer shard setup wizard (end-to-end browser flow)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    api.setupSignerShards.mockResolvedValue(doneStatus)
    // localStorage and sessionStorage share the Storage prototype, so one spy
    // covers both; IndexedDB is captured through a throwing getter.
    storageSpy = vi.spyOn(Storage.prototype, 'setItem')
    idbGetter = vi.fn()
    Object.defineProperty(globalThis, 'indexedDB', { configurable: true, get: idbGetter })
  })

  afterEach(() => {
    storageSpy?.mockRestore()
    storageSpy = null
    delete (globalThis as { indexedDB?: unknown }).indexedDB
    idbGetter = null
  })

  it('requires the fresh 6-digit code before it will generate a key', async () => {
    const wrapper = await render()
    expect(wrapper.text()).toContain('admin.crypto.signer.wizard.authTitle')
    expect(wrapper.get('[data-testid="signer-wizard-auth-next"]').attributes('disabled')).toBeDefined()
    expect(wrapper.text()).not.toContain('admin.crypto.signer.wizard.revealTitle')
  })

  it('generates B/C in the browser, shows them once, and only submits A plus the typed-back share', async () => {
    const wrapper = await render()
    await passAuthAndRisk(wrapper)
    expect(wrapper.text()).toContain('admin.crypto.signer.wizard.revealTitle')

    const shares = await revealShares(wrapper)
    expect(shares).toHaveLength(2)
    for (const text of shares) expect(text).toMatch(/^S2V1-/)
    expect(shares[0]).not.toBe(shares[1])
    // Both text shares must parse with the server-compatible parser.
    for (const text of shares) await expect(parseShare(text, crypto)).resolves.toBeTruthy()

    await goToConfirm(wrapper)
    await fillSubmit(wrapper, shares[0])
    await wrapper.get('[data-testid="signer-wizard-submit"]').trigger('click')
    await flushPromises()

    expect(api.setupSignerShards).toHaveBeenCalledTimes(1)
    const payload = api.setupSignerShards.mock.calls[0][0]
    expect(payload.totp_code).toBe('654321')
    expect(payload.risk_phrase).toBe(PHRASE)
    expect(payload.share_a).toMatch(/^S2V1-/)
    expect(payload.share_a).not.toBe(shares[0])
    expect(payload.share_a).not.toBe(shares[1])
    expect(payload.confirm_share.replace(/-/g, '')).toBe(shares[0].replace(/-/g, ''))
    expect(atob(payload.data_key)).toHaveLength(32)
    expect(atob(payload.dk_commitment)).toHaveLength(32)
    // The API body contains A and the typed-back share, never the other one.
    expect(JSON.stringify(payload)).not.toContain(shares[1])

    // Shares are wiped immediately after the server accepted the setup.
    expect(wrapper.findAll('.shard-reveal__text')).toHaveLength(0)
    expect(wrapper.text()).toContain('admin.crypto.signer.wizard.doneTitle')
    expect(wrapper.emitted('completed')).toHaveLength(1)
  })

  it('refuses a back-filled share that was not shown', async () => {
    const wrapper = await render()
    await passAuthAndRisk(wrapper)
    const shares = await revealShares(wrapper)
    await goToConfirm(wrapper)
    // Corrupt one character; the checksum makes it invalid before submission. Change a character of the first data
    // group, not the last one: base32 without padding can leave unused padding bits in the final character (1, 3 or 4
    // of its 5 bits depending on the payload length), and then swapping it decodes to the very same bytes, so the
    // checksum still matches and the submit goes through (roughly 1 run in 15 with random shares).
    const bodyStart = shares[0].indexOf('-') + 1
    const swap = shares[0][bodyStart] === 'A' ? 'B' : 'A'
    const corrupted = shares[0].slice(0, bodyStart) + swap + shares[0].slice(bodyStart + 1)
    await fillSubmit(wrapper, corrupted)
    await wrapper.get('[data-testid="signer-wizard-submit"]').trigger('click')
    await flushPromises()
    expect(api.setupSignerShards).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('admin.crypto.signer.wizard.confirmMismatch')
  })

  it('never persists shards or keys to browser storage', async () => {
    const wrapper = await render()
    await passAuthAndRisk(wrapper)
    const shares = await revealShares(wrapper)
    await goToConfirm(wrapper)
    await fillSubmit(wrapper, shares[1])
    await wrapper.get('[data-testid="signer-wizard-submit"]').trigger('click')
    await flushPromises()

    for (const text of shares) {
      const stored = JSON.stringify(storageSpy!.mock.calls)
      expect(stored).not.toContain(text)
      expect(stored).not.toContain(text.slice(5, 15))
    }
    expect(idbGetter).not.toHaveBeenCalled()
  })

  it('keeps the shares on screen when submission fails so the operator can retry', async () => {
    api.setupSignerShards.mockRejectedValue({ reason: 'CRYPTO_SIGNER_RATE_LIMITED' })
    const wrapper = await render()
    await passAuthAndRisk(wrapper)
    const shares = await revealShares(wrapper)
    await goToConfirm(wrapper)
    await fillSubmit(wrapper, shares[0])
    await wrapper.get('[data-testid="signer-wizard-submit"]').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('admin.crypto.signer.errors.rateLimited')
    // Still in the confirm step with material available for a retry.
    expect(wrapper.find('[data-testid="signer-wizard-confirm"]').exists()).toBe(true)
  })

  it('warns before discarding un-submitted shares and clears them on close', async () => {
    const wrapper = await render()
    await passAuthAndRisk(wrapper)
    const shares = await revealShares(wrapper)
    ;(wrapper.vm as unknown as { requestClose: () => void }).requestClose()
    await flushPromises()
    expect(wrapper.text()).toContain('admin.crypto.signer.wizard.closeTitle')
    await wrapper.get('[data-testid="signer-wizard-discard"]').trigger('click')
    await flushPromises()
    expect(wrapper.emitted('close')).toHaveLength(1)
    expect(wrapper.findAll('.shard-reveal__text')).toHaveLength(0)
    expect(shares.length).toBe(2)
  })

  it('rejects a browser without a CSPRNG instead of falling back to weak randomness', async () => {
    const wrapper = await render({ crypto: null, cryptoUnavailable: 'no-csprng' })
    expect(wrapper.get('[data-testid="signer-wizard-unavailable"]').text()).toContain('admin.crypto.signer.noCsprng')
    expect(wrapper.find('[data-testid="signer-wizard-auth"]').exists()).toBe(false)
  })

  it('requires two existing shares before a re-split can recover the data key', async () => {
    const wrapper = await render({ mode: 'resplit' })
    expect(wrapper.text()).toContain('admin.crypto.signer.wizard.recoverTitle')
    await wrapper.get('[data-testid="signer-wizard-recover-action"]').trigger('click')
    // Empty inputs keep the action disabled; no recovery was attempted.
    expect(wrapper.text()).toContain('admin.crypto.signer.wizard.recoverTitle')
  })
})
