import { beforeEach, describe, expect, it, vi } from 'vitest'
const client = vi.hoisted(() => ({ post: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: client }))
import { runCryptoScan } from '@/api/admin/crypto'

beforeEach(() => {
  vi.resetAllMocks()
  client.post.mockResolvedValue({ data: { data: { completed: true } }, status: 200 })
})

describe('manual deposit catch-up contract', () => {
  it('starts only a forward cursor scan and uses the authenticated admin session', async () => {
    await runCryptoScan()
    expect(client.post).toHaveBeenCalledTimes(1)
    expect(client.post).toHaveBeenCalledWith('/admin/crypto/scan/run', { from_block: 0 }, { timeout: 15 * 60 * 1000 })
  })

  it('supports a bounded operator-specified scanner start for internal callers', async () => {
    await runCryptoScan(12345)
    expect(client.post).toHaveBeenCalledWith('/admin/crypto/scan/run', { from_block: 12345 }, { timeout: 15 * 60 * 1000 })
  })
})
