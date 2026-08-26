import { beforeEach, describe, expect, it, vi } from 'vitest'

const { get } = vi.hoisted(() => ({
  get: vi.fn()
}))

vi.mock('@/api/client', () => ({
  apiClient: { get }
}))

import { getStats } from '@/api/admin/accounts'

describe('admin account stats API', () => {
  beforeEach(() => {
    get.mockReset()
    get.mockResolvedValue({ data: { summary: {}, history: [] } })
  })

  it('uses an endpoint-specific 180s timeout', async () => {
    await getStats(42, 30)

    expect(get).toHaveBeenCalledWith('/admin/accounts/42/stats', {
      params: { days: 30 },
      timeout: 180000
    })
  })
})
