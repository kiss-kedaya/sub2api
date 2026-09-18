import { describe, expect, it, vi } from 'vitest'
import { getDashboardSnapshotV2 } from '../usage'

const get = vi.hoisted(() => vi.fn())
vi.mock('../client', () => ({ apiClient: { get } }))

describe('usage dashboard snapshot', () => {
  it('forwards cancellation and include flags without changing response data', async () => {
    const controller = new AbortController()
    const params = { start_date: '2026-09-12', end_date: '2026-09-18', include_trend: true, include_model_stats: true, include_group_stats: false }
    const data = { trend: [], models: [], generated_at: '2026-09-18T00:00:00Z' }
    get.mockResolvedValueOnce({ data })
    expect(await getDashboardSnapshotV2(params, { signal: controller.signal })).toBe(data)
    expect(get).toHaveBeenLastCalledWith('/usage/dashboard/snapshot-v2', { params, signal: controller.signal })
  })

  it('keeps the existing optional-argument contract', async () => {
    get.mockResolvedValueOnce({ data: { trend: [], models: [] } })
    await getDashboardSnapshotV2()
    expect(get).toHaveBeenLastCalledWith('/usage/dashboard/snapshot-v2', { params: undefined })
  })
})
