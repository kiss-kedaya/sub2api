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
  it('explains deployment-level payment authorization separately from a bad code', () => {
    expect(api.cryptoSecurityErrorKey({ reason: 'CRYPTO_OPERATOR_REQUIRED', status: 403 })).toBe('securityOperatorRequired')
  })
  it('reads wallet status and sends initialization seeds only to the built-in wallet initialization endpoint', async () => {
    const status = { ok: true, configured: true, ready: true, enabled: true }
    client.get.mockResolvedValueOnce({ data: { data: status } })
    expect(await api.getWalletStatus()).toEqual(status)
    expect(client.get).toHaveBeenLastCalledWith('/admin/crypto/wallet/status')
    await api.initializeWallet('deposit words', 'gas words', { totpCode: '123456' })
    expect(client.post).toHaveBeenLastCalledWith('/admin/crypto/wallet/initialize', {
      deposit_mnemonic: 'deposit words', hot_mnemonic: 'gas words'
    }, { headers: { 'X-Crypto-TOTP': '123456' }, _retry: true })
  })

  it('exports plaintext only from the wallet endpoint with per-request verification and no cache', async () => {
    const mnemonics = { deposit_mnemonic: 'deposit words', hot_mnemonic: 'gas words' }
    client.post.mockResolvedValueOnce({ data: mnemonics })
    expect(await api.exportWalletMnemonics( { totpCode: '234567' })).toEqual(mnemonics)
    expect(client.post).toHaveBeenLastCalledWith('/admin/crypto/wallet/export-mnemonics', {}, {
      headers: { 'X-Crypto-TOTP': '234567', 'Cache-Control': 'no-store', Pragma: 'no-cache' }, _retry: true
    })
    await api.initializeWallet('deposit words', 'gas words', { totpCode: '345678' })
    expect(client.post.mock.lastCall?.[2].headers).toEqual({ 'X-Crypto-TOTP': '345678' })
  })

  it('adds the code to mutations, sensitive config reads and CSV requests, never to ordinary GETs', async () => {
    const verification = { totpCode: '123456' }
    await api.updateCryptoConfig({ rpc: { bsc: 'https://rpc.example' } }, verification)
    await api.setAddressStatus(8, 'disabled', verification)
    await api.runSweep(false, verification)
    await api.runSweep(true, verification)
    await api.initializeWallet('deposit words', 'gas words', verification)
    await api.testRPCEndpoint('bsc', 'https://rpc.example', verification)
    await api.autoPickRPC('bsc', [], 3, verification)
    await api.importHotMnemonic('gas words', verification)
    await api.exportDepositsCSV('bsc', 'credited', verification)
    for (const [, , options] of [...client.post.mock.calls, ...client.put.mock.calls]) {
      expect(options.headers).toEqual({ 'X-Crypto-TOTP': '123456' })
      expect(options._retry).toBe(true)
    }
    expect(client.get.mock.calls[0][1].headers).toEqual({ 'X-Crypto-TOTP': '123456' })
    client.get.mockClear()
    await api.getCryptoConfig('234567')
    expect(client.get).toHaveBeenLastCalledWith('/admin/crypto/config', {
      headers: { 'X-Crypto-TOTP': '234567' }, _retry: true
    })
    client.get.mockClear()
    await api.getHotWalletAddress()
    await api.getWalletStatus()
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
    expect(await api.setupCryptoSecurity()).toEqual(setup)
    expect(client.post).toHaveBeenLastCalledWith('/admin/crypto/security/setup', {}, { headers: {}, _retry: true })
    client.post.mockResolvedValueOnce({ data: { saved: true } })
    expect(await api.importHotMnemonic('gas words', { totpCode: '234567' })).toEqual({ saved: true })
    expect(client.post.mock.lastCall?.[0]).toBe('/admin/crypto/wallet/hot-mnemonic')
    expect(client.post.mock.lastCall?.[1]).toEqual({ hot_mnemonic: 'gas words' })
    expect('backupWallet' in api).toBe(false)
    expect('unlockWallet' in api).toBe(false)
  })

  it('retains JSON server rejections for Blob downloads rather than saving an error file', async () => {
    const errorBlob = { text: async () => JSON.stringify({ reason: 'CRYPTO_TOTP_REPLAY', message: 'Used code' }) }
    client.get.mockResolvedValueOnce({ data: errorBlob, status: 429 })
    await expect(api.exportDepositsCSV('', '', { totpCode: '123456' })).rejects.toMatchObject({ status: 429 })
  })
})
