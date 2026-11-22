import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { AxiosInstance, InternalAxiosRequestConfig } from 'axios'

// The real client, with only the transport replaced, so the deposit-status
// adapter is exercised end to end: envelope handling, field validation and the
// new ETA evidence.
let apiClient: AxiosInstance
let api: typeof import('@/api/crypto')
let respond: (config: InternalAxiosRequestConfig) => unknown

beforeEach(async () => {
  localStorage.clear()
  vi.resetModules()
  respond = () => ({ networks: [] })
  apiClient = (await import('@/api/client')).apiClient
  apiClient.defaults.adapter = async (config: InternalAxiosRequestConfig) => {
    return { data: respond(config), status: 200, statusText: 'OK', headers: {}, config }
  }
  api = await import('@/api/crypto')
})
afterEach(() => vi.restoreAllMocks())

const expires = '2026-01-01T00:05:00Z'

describe('getDepositNetworkStatus ETA evidence', () => {
  it('keeps the status decision and reads the new ETA fields', async () => {
    respond = () => ({
      networks: [{
        chain: 'bsc', status: 'available', eta_seconds: 35, backlog_seconds: 12,
        required_confirmations: 0, observed_at: '2026-01-01T00:00:00Z', expires_at: expires,
      }],
    })

    await expect(api.getDepositNetworkStatus()).resolves.toEqual([{
      chain: 'bsc', status: 'available', observed_at: '2026-01-01T00:00:00Z', expires_at: expires,
      eta_seconds: 35, backlog_seconds: 12, required_confirmations: 0,
    }])
  })

  it('accepts the legacy {data:{networks}} envelope as well', async () => {
    respond = () => ({ data: { networks: [{ chain: 'base', status: 'slow', eta_seconds: 200, expires_at: expires }] } })

    const [row] = await api.getDepositNetworkStatus()
    expect(row.chain).toBe('base')
    expect(row.status).toBe('slow')
    expect(row.eta_seconds).toBe(200)
  })

  it('drops malformed ETA fields but keeps the status usable', async () => {
    respond = () => ({
      networks: [{
        chain: 'bsc', status: 'delayed', expires_at: expires,
        eta_seconds: 'soon', backlog_seconds: null, required_confirmations: {},
      }],
    })

    const [row] = await api.getDepositNetworkStatus()
    // A bad estimate must not cost the user the whole row: the delay decision
    // still renders, only the friendly minutes are missing.
    expect(row.status).toBe('delayed')
    expect(row.eta_seconds).toBeUndefined()
    expect(row.backlog_seconds).toBeUndefined()
    expect(row.required_confirmations).toBeUndefined()
  })

  it('maps a JSON null expiry to "no expiry" instead of discarding the row', async () => {
    respond = () => ({ networks: [{ chain: 'bsc', status: 'unknown', expires_at: null, eta_seconds: null }] })

    await expect(api.getDepositNetworkStatus()).resolves.toEqual([{
      chain: 'bsc', status: 'unknown', observed_at: undefined, expires_at: undefined,
      eta_seconds: undefined, backlog_seconds: undefined, required_confirmations: undefined,
    }])
  })

  it.each([
    ['an unsupported status', { chain: 'bsc', status: 'melted', expires_at: expires }],
    ['a non-string chain', { chain: 7, status: 'available', expires_at: expires }],
    ['a non-string expiry', { chain: 'bsc', status: 'available', expires_at: 12345 }],
  ])('ignores %s', async (_reason, network) => {
    respond = () => ({ networks: [network] })
    await expect(api.getDepositNetworkStatus()).resolves.toEqual([])
  })
})

describe('getDepositInfo ETA contract', () => {
  it('passes the per-chain ETA and instant-credit flag through untouched', async () => {
    const chain = {
      chain: 'bsc', address: '0x' + '12'.repeat(20), tokens: ['USDT'],
      assets: [{ symbol: 'USDT', native: false, market_priced: false, rate: 6.7 }],
      eta_seconds: 35, instant_credit: true,
    }
    respond = () => ({ data: { enabled: true, chains: [chain], min_amount: 1, max_amount: 0 } })

    const info = await api.getDepositInfo()
    expect(info.chains[0]).toEqual(chain)
    // Older backends omit both fields; the page must still render.
    expect(info.chains[0].eta_seconds).toBe(35)
    expect(info.chains[0].instant_credit).toBe(true)
  })
})
