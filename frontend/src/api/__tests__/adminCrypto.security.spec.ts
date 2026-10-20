import { beforeEach, describe, expect, it, vi } from 'vitest'
const client = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: client }))
import * as api from '@/api/admin/crypto'

beforeEach(() => {
  vi.resetAllMocks()
  client.get.mockResolvedValue({ data: { data: {} }, status: 200 })
  client.post.mockResolvedValue({ data: { data: {} }, status: 200 })
  client.put.mockResolvedValue({ data: {}, status: 200 })
})

describe('Crypto request contract (not backend authorization)', () => {
  it('adds the code to each mutation and CSV request, never to ordinary GETs', async () => {
    const verification = { totpCode: '123456' }
    await api.updateCryptoConfig({ rpc: { bsc: 'https://rpc.example' } }, verification)
    await api.setAddressStatus(8, 'disabled', verification)
    await api.runSweep(false, verification)
    await api.runSweep(true, verification)
    await api.unlockSigner('vault', 'deposit words', 'gas words', verification)
    await api.testRPCEndpoint('bsc', 'https://rpc.example', verification)
    await api.autoPickRPC('bsc', [], 3, verification)
    await api.backupSigner('vault', verification)
    await api.importHotMnemonic('vault', 'gas words', verification)
    await api.exportDepositsCSV('bsc', 'credited', verification)
    for (const [, , options] of [...client.post.mock.calls, ...client.put.mock.calls]) {
      expect(options.headers).toEqual({ 'X-Crypto-TOTP': '123456' })
      expect(options._retry).toBe(true)
    }
    expect(client.get.mock.calls[0][1].headers).toEqual({ 'X-Crypto-TOTP': '123456' })
    client.get.mockClear()
    await api.getHotWalletAddress()
    await api.getSignerStatus()
    await api.getCryptoSecurityStatus()
    for (const [, options] of client.get.mock.calls) expect(options?.headers?.['X-Crypto-TOTP']).toBeUndefined()
    await api.updateCryptoConfig({ enabled: true })
    expect(client.put.mock.lastCall?.[2].headers).toEqual({})
  })

  it('uses normal security responses, precise wallet bodies and raw encrypted Blob', async () => {
    const status = { enabled: true, feature_enabled: true, can_setup: true }
    client.get.mockResolvedValueOnce({ data: status })
    expect(await api.getCryptoSecurityStatus()).toEqual(status)
    const setup = { secret: 'KEY', qr_code_url: 'otpauth://totp/test', setup_token: 'TOKEN', countdown: 90 }
    client.post.mockResolvedValueOnce({ data: setup })
    expect(await api.setupCryptoSecurity('password')).toEqual(setup)
    client.post.mockResolvedValueOnce({ data: { saved: true } })
    expect(await api.importHotMnemonic('vault', 'gas words', { totpCode: '234567' })).toEqual({ saved: true })
    expect(client.post.mock.lastCall?.[1]).toEqual({ passphrase: 'vault', hot_mnemonic: 'gas words' })
    const encrypted = new Blob(['{"encrypted":"ciphertext"}'], { type: 'application/json' })
    client.post.mockResolvedValueOnce({ data: encrypted, status: 200 })
    expect(await api.backupSigner('vault', { totpCode: '345678' })).toBe(encrypted)
    expect(client.post.mock.lastCall?.[1]).toEqual({ passphrase: 'vault' })
    expect(client.post.mock.lastCall?.[2].responseType).toBe('blob')
  })

  it('retains JSON server rejections for Blob downloads rather than saving an error file', async () => {
    const errorBlob = { text: async () => JSON.stringify({ reason: 'CRYPTO_TOTP_REPLAY', message: 'Used code' }) }
    client.post.mockResolvedValueOnce({ data: errorBlob, status: 403 })
    await expect(api.backupSigner('vault', { totpCode: '123456' })).rejects.toMatchObject({ status: 403, reason: 'CRYPTO_TOTP_REPLAY' })
    client.get.mockResolvedValueOnce({ data: errorBlob, status: 429 })
    await expect(api.exportDepositsCSV('', '', { totpCode: '123456' })).rejects.toMatchObject({ status: 429 })
  })
})
