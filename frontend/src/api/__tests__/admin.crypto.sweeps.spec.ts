import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { AxiosInstance, InternalAxiosRequestConfig } from 'axios'

vi.mock('@/i18n', () => ({ getLocale: () => 'zh-CN' }))

// The real client, with only the transport replaced. These endpoints answer
// without a `code`, exactly like the backend, so the client does not unwrap them.
let apiClient: AxiosInstance
let api: typeof import('@/api/admin/crypto')
let requests: InternalAxiosRequestConfig[]
let respond: (config: InternalAxiosRequestConfig) => unknown

beforeEach(async () => {
  localStorage.clear()
  vi.resetModules()
  requests = []
  respond = () => ({ data: [] })
  apiClient = (await import('@/api/client')).apiClient
  apiClient.defaults.adapter = async (config: InternalAxiosRequestConfig) => {
    requests.push(config)
    return { data: respond(config), status: 200, statusText: 'OK', headers: {}, config }
  }
  api = await import('@/api/admin/crypto')
})
afterEach(() => vi.restoreAllMocks())

const queryOf = (config: InternalAxiosRequestConfig) => new URL(apiClient.getUri(config), 'http://localhost').searchParams

describe('getAddressOwners', () => {
  const a = '0x' + 'a1'.repeat(20)
  const b = '0x' + 'b2'.repeat(20)

  it('sends every address as a repeated address= parameter, with no brackets or indexes', async () => {
    respond = () => ({ data: { owners: {} } })
    await api.getAddressOwners([a, b])
    const url = apiClient.getUri(requests[0])
    expect(url).toContain('/admin/crypto/address-owners?')
    const query = queryOf(requests[0])
    expect(query.getAll('address')).toEqual([a, b])
    expect(url).not.toContain('address%5B')
    expect(url).not.toContain('address[')
  })

  it('unwraps data.owners from the code-less response', async () => {
    const owners = { [a]: { user_id: 1, username: 'fixture-name', email: 'fixture@example.test', deleted: false } }
    respond = () => ({ data: { owners } })
    await expect(api.getAddressOwners([a])).resolves.toEqual(owners)
  })

  it.each([
    ['no data', {}],
    ['owners is not an object', { data: { owners: 'nope' } }],
    ['owners missing', { data: {} }],
    ['an empty body', undefined],
    ['null', null]
  ])('falls back to an empty map when the answer has %s', async (_name, body) => {
    respond = () => body
    await expect(api.getAddressOwners([a])).resolves.toEqual({})
  })

  it('lets a 404 through as a rejection carrying the status, for the caller to treat as "unknown"', async () => {
    apiClient.defaults.adapter = async config => {
      const error = Object.assign(new Error('Request failed with status code 404'), {
        isAxiosError: true, config, response: { status: 404, data: { message: 'page not found' }, headers: {}, config, statusText: 'Not Found' }
      })
      throw error
    }
    await expect(api.getAddressOwners([a])).rejects.toMatchObject({ status: 404 })
  })
})

describe('listSweepRecords paging', () => {
  it('sends limit and before_id only when given, so the old call is unchanged', async () => {
    respond = () => ({ data: [] })
    await api.listSweepRecords()
    const plain = queryOf(requests[0])
    expect(plain.has('limit')).toBe(false)
    expect(plain.has('before_id')).toBe(false)
    expect(plain.has('chain')).toBe(false)

    await api.listSweepRecords('bsc', { limit: 100, beforeId: 345 })
    const paged = queryOf(requests[1])
    expect(paged.get('chain')).toBe('bsc')
    expect(paged.get('limit')).toBe('100')
    expect(paged.get('before_id')).toBe('345')
  })

  it('returns the array under data, and an empty array when there is none', async () => {
    respond = () => ({ data: [{ id: 3 }, { id: 2 }] })
    await expect(api.listSweepRecords()).resolves.toEqual([{ id: 3 }, { id: 2 }])
    respond = () => ({})
    await expect(api.listSweepRecords()).resolves.toEqual([])
  })
})

describe('listBlockedSweeps', () => {
  const job = { job_id: 9, chain: 'bsc', address: '0x' + 'c3'.repeat(20), status: 'sweep_pending', error: '', age_minutes: 12, can_reconcile: true }

  it('unwraps the {"data": [...]} answer the backend really sends', async () => {
    respond = () => ({ data: [job] })
    await expect(api.listBlockedSweeps()).resolves.toEqual([job])
  })

  it('still accepts a bare array', async () => {
    respond = () => [job]
    await expect(api.listBlockedSweeps()).resolves.toEqual([job])
  })

  it.each([[{ data: null }], [{}], [null], ['nope']])('is an empty list for %j', async body => {
    respond = () => body
    await expect(api.listBlockedSweeps()).resolves.toEqual([])
  })
})
