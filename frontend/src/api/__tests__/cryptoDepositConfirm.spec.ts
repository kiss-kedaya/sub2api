import { beforeEach, describe, expect, it, vi } from 'vitest'
const client = vi.hoisted(() => ({ post: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: client }))
import { confirmDeposits } from '@/api/admin/crypto'

const grant = { credential: 'test-payment-grant-credential-32-bytes-minimum' }
const body = {
  results: [
    { id: 302, status: 'credited', order_id: 123, message: '' },
    { id: 321, status: 'rejected', message: 'receipt not found' }
  ],
  credited: 1,
  failed: 1
}

beforeEach(() => {
  vi.resetAllMocks()
  client.post.mockResolvedValue({ data: { data: body }, status: 200 })
})

describe('manual deposit confirmation contract', () => {
  it('posts the selected ids with the payment grant and blocks automatic replay', async () => {
    await confirmDeposits([302, 321], grant)
    expect(client.post).toHaveBeenCalledTimes(1)
    expect(client.post).toHaveBeenCalledWith('/admin/crypto/deposits/confirm', { ids: [302, 321] }, {
      headers: { 'X-Crypto-Grant': grant.credential },
      _retry: true,
      timeout: 120000
    })
  })

  it('sends a TOTP code in its own header when no grant is available', async () => {
    await confirmDeposits([302], { totpCode: '123456' })
    expect(client.post.mock.calls[0][2]).toEqual(expect.objectContaining({ headers: { 'X-Crypto-TOTP': '123456' }, _retry: true }))
  })

  it('trims the note and omits it when blank', async () => {
    await confirmDeposits([302], grant, '  ticket 88  ')
    expect(client.post.mock.calls[0][1]).toEqual({ ids: [302], note: 'ticket 88' })
    await confirmDeposits([302], grant, '   ')
    expect(client.post.mock.calls[1][1]).toEqual({ ids: [302] })
    await confirmDeposits([302], grant)
    expect(client.post.mock.calls[2][1]).toEqual({ ids: [302] })
  })

  it('returns the per-deposit results and counters from the data envelope', async () => {
    await expect(confirmDeposits([302, 321], grant)).resolves.toEqual(body)
  })

  it('also accepts an already unwrapped body', async () => {
    client.post.mockResolvedValue({ data: body, status: 200 })
    await expect(confirmDeposits([302, 321], grant)).resolves.toEqual(body)
  })

  it('derives the counters from the results when the server omits them', async () => {
    client.post.mockResolvedValue({ data: { data: { results: [
      { id: 1, status: 'credited' }, { id: 2, status: 'already_credited' }, { id: 3, status: 'rejected' }, { id: 4, status: 'error' }
    ] } }, status: 200 })
    const outcome = await confirmDeposits([1, 2, 3, 4], grant)
    expect(outcome.credited).toBe(1)
    expect(outcome.failed).toBe(2)
  })

  it('returns an empty outcome instead of throwing on an empty body', async () => {
    client.post.mockResolvedValue({ data: {}, status: 200 })
    await expect(confirmDeposits([1], grant)).resolves.toEqual({ results: [], credited: 0, failed: 0 })
  })

  it('lets request-level failures reach the caller untouched', async () => {
    const refused = { status: 403, reason: 'CRYPTO_GRANT_REQUIRED', message: 'verify again' }
    client.post.mockRejectedValue(refused)
    await expect(confirmDeposits([302], grant)).rejects.toBe(refused)
  })
})
