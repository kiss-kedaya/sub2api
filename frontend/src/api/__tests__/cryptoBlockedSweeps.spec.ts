import { beforeEach, describe, expect, it, vi } from 'vitest'
const client = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: client }))
import { listBlockedSweeps, reconcileSweep } from '@/api/admin/crypto'

// Both handlers answer { data: ... } with no "code" key, so the axios interceptor
// leaves the body as it is and the wrapper has to take .data itself.
const blocked = [
  { job_id: 99, chain: 'bsc', address: '0xabc', status: 'gas_pending', error: 'awaiting gas payer transaction confirmation', age_minutes: 7200, can_reconcile: true },
  { job_id: 103, chain: 'bsc', address: '0xdef', status: 'failed', error: '', age_minutes: 1900, can_reconcile: true }
]

beforeEach(() => vi.resetAllMocks())

describe('blocked sweeps contract', () => {
  it('returns the list itself, not the { data } envelope around it', async () => {
    client.get.mockResolvedValue({ data: { data: blocked }, status: 200 })
    const list = await listBlockedSweeps()
    expect(client.get).toHaveBeenCalledWith('/admin/crypto/sweep/blocked')
    expect(Array.isArray(list)).toBe(true)
    expect(list.map(item => item.job_id)).toEqual([99, 103])
  })

  it('returns an empty list when the server sends none or something unexpected', async () => {
    for (const body of [{ data: [] }, { data: null }, {}, null, undefined]) {
      client.get.mockResolvedValue({ data: body, status: 200 })
      expect(await listBlockedSweeps()).toEqual([])
    }
  })

  it('returns the reconcile result so its message can be shown', async () => {
    const result = { job_id: 103, chain: 'bsc', status: 'swept', action: 'confirmed', message: 'token transfer confirmed on chain' }
    client.post.mockResolvedValue({ data: { data: result }, status: 200 })
    const got = await reconcileSweep(103, { credential: 'test-payment-grant-credential-32-bytes-minimum' })
    expect(client.post.mock.calls[0][0]).toBe('/admin/crypto/sweep/records/103/reconcile')
    expect(got.message).toBe('token transfer confirmed on chain')
    expect(got.action).toBe('confirmed')
  })
})
