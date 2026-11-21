import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { AxiosInstance, InternalAxiosRequestConfig } from 'axios'

vi.mock('@/i18n', () => ({ getLocale: () => 'zh-CN' }))

let apiClient: AxiosInstance
let signer: typeof import('@/api/admin/cryptoSigner')
let cryptoApi: typeof import('@/api/admin/crypto')
let requests: InternalAxiosRequestConfig[]
let respond: (config: InternalAxiosRequestConfig) => unknown

beforeEach(async () => {
  localStorage.clear()
  vi.resetModules()
  requests = []
  respond = () => ({ data: { data: {} } })
  apiClient = (await import('@/api/client')).apiClient
  apiClient.defaults.adapter = async (config: InternalAxiosRequestConfig) => {
    requests.push(config)
    return { data: respond(config), status: 200, statusText: 'OK', headers: {}, config }
  }
  signer = await import('@/api/admin/cryptoSigner')
  cryptoApi = await import('@/api/admin/crypto')
})

afterEach(() => vi.restoreAllMocks())

const bodyOf = (config: InternalAxiosRequestConfig): Record<string, unknown> =>
  typeof config.data === 'string' ? JSON.parse(config.data) : config.data

describe('signer shard API client', () => {
  it('reads the status and risk notice from the signer routes', async () => {
    respond = () => ({ data: { configured: true, vault_version: 3, locked: true } })
    const status = await signer.getSignerShardStatus()
    expect(requests[0].url).toBe('/admin/crypto/signer/status')
    expect(requests[0].method).toBe('get')
    expect(status).toMatchObject({ configured: true, vault_version: 3, locked: true })

    respond = () => ({ data: { points: { zh: ['a'], en: ['a'] }, phrases: { zh: 'x', en: 'y' }, display_once: true, schemes: [] } })
    const notice = await signer.getSignerRiskNotice()
    expect(requests[1].url).toBe('/admin/crypto/signer/risk-notice')
    expect(notice.display_once).toBe(true)
  })

  it('posts the one-time setup body exactly as the backend decodes it', async () => {
    respond = () => ({ data: { data: { configured: true, vault_version: 3, locked: false } } })
    await signer.setupSignerShards({
      totp_code: '123456',
      risk_phrase: '我已离线保存分片，丢失无法找回',
      data_key: 'AAAA',
      share_a: 'S2V1-AAAA',
      confirm_share: 'S2V1-BBBB',
      dk_commitment: 'commit'
    })
    expect(requests[0].url).toBe('/admin/crypto/signer/setup')
    expect(requests[0].method).toBe('post')
    expect(bodyOf(requests[0])).toEqual({
      totp_code: '123456',
      risk_phrase: '我已离线保存分片，丢失无法找回',
      data_key: 'AAAA',
      share_a: 'S2V1-AAAA',
      confirm_share: 'S2V1-BBBB',
      dk_commitment: 'commit'
    })
  })

  it('verifies a share with the fresh-code header the admin middleware requires', async () => {
    respond = () => ({ data: { data: { valid: true } } })
    await signer.verifySignerShare('S2V1-AAAA', '654321')
    expect(requests[0].url).toBe('/admin/crypto/signer/verify-share')
    expect(bodyOf(requests[0])).toEqual({ share: 'S2V1-AAAA' })
    expect(requests[0].headers['X-Crypto-TOTP']).toBe('654321')
  })

  it('re-splits and toggles the automatic-sweep share', async () => {
    respond = () => ({ data: { data: { configured: true } } })
    await signer.resplitSignerShards({
      totp_code: '123456',
      risk_phrase: 'phrase',
      data_key: 'AAAA',
      share_a: 'S2V1-AAAA',
      confirm_share: 'S2V1-BBBB',
      dk_commitment: 'commit'
    })
    expect(requests[0].url).toBe('/admin/crypto/signer/resplit')
    await signer.setSignerAutoSweep({ enabled: false, totp_code: '123456' })
    expect(requests[1].url).toBe('/admin/crypto/signer/auto-sweep')
    expect(bodyOf(requests[1])).toEqual({ enabled: false, totp_code: '123456' })
  })
})

describe('sweep API carries the recovery share', () => {
  it('sends share in the async sweep body only when provided', async () => {
    respond = () => ({ data: { data: { id: 'run-1', dry_run: false, chains: null, started_at: '', finished: false } } })
    await cryptoApi.startSweep(false, { totpCode: '123456' }, undefined, 'S2V1-SHARE')
    expect(requests[0].url).toContain('/admin/crypto/sweep/run')
    expect(bodyOf(requests[0])).toEqual({ share: 'S2V1-SHARE' })
    expect(requests[0].headers['X-Crypto-TOTP']).toBe('123456')

    await cryptoApi.startSweep(false, { totpCode: '123456' })
    expect(String(requests[1].data)).not.toContain('share')
  })

  it('adds share to the mnemonic/private-key export bodies when the vault is locked', async () => {
    respond = () => ({ data: { deposit_mnemonic: 'a', hot_mnemonic: 'b' } })
    await cryptoApi.exportWalletMnemonics({ totpCode: '123456' }, 'S2V1-SHARE')
    expect(bodyOf(requests[0])).toEqual({ share: 'S2V1-SHARE' })

    respond = () => ({ data: { address: '0x1', family: 'evm', derivation_index: 0, private_key: '0x2', format: 'hex' } })
    await cryptoApi.exportAddressPrivateKey(7, { totpCode: '123456' }, undefined, 'S2V1-SHARE')
    expect(requests[1].url).toContain('/admin/crypto/addresses/7/export-private-key')
    expect(bodyOf(requests[1])).toEqual({ share: 'S2V1-SHARE' })
  })
})
