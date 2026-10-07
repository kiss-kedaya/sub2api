import { beforeEach, describe, expect, it, vi } from 'vitest'
import { getDashboardApiKeysUsage } from '@/api/usage'
import { apiClient } from '@/api/client'

vi.mock('@/api/client', () => ({ apiClient: { post: vi.fn() } }))

describe('API key statistics batch contract', () => {
  beforeEach(() => vi.resetAllMocks())

  it('fetches every ID on a large page without exceeding the server limit', async () => {
    vi.mocked(apiClient.post).mockImplementation(async (_url, body) => ({
      data: { stats: Object.fromEntries(body.api_key_ids.map((id: number) => [String(id), {
        api_key_id: id, today_actual_cost: id, total_actual_cost: id * 2
      }])) }
    }))
    const ids = Array.from({ length: 235 }, (_, index) => index + 1)
    const response = await getDashboardApiKeysUsage([...ids, 1])
    expect(Object.keys(response.stats)).toHaveLength(235)
    expect(response.stats['235'].total_actual_cost).toBe(470)
    expect(vi.mocked(apiClient.post).mock.calls.map((call) => call[1].api_key_ids.length)).toEqual([100, 100, 35])
  })

  it('does not submit an empty batch', async () => {
    expect(await getDashboardApiKeysUsage([])).toEqual({ stats: {} })
    expect(apiClient.post).not.toHaveBeenCalled()
  })

  it('stops after cancellation rather than starting later batches', async () => {
    const controller = new AbortController()
    vi.mocked(apiClient.post).mockImplementationOnce(async () => {
      controller.abort()
      return { data: { stats: {} } }
    })
    await expect(getDashboardApiKeysUsage(Array.from({ length: 101 }, (_, i) => i + 1), {
      signal: controller.signal
    })).rejects.toMatchObject({ name: 'AbortError' })
    expect(apiClient.post).toHaveBeenCalledTimes(1)
    expect(vi.mocked(apiClient.post).mock.calls[0][2]?.signal).toBe(controller.signal)
  })

  it('rejects on a failed chunk without returning incomplete totals', async () => {
    vi.mocked(apiClient.post).mockResolvedValueOnce({ data: { stats: {} } }).mockRejectedValueOnce(new Error('failed'))
    await expect(getDashboardApiKeysUsage(Array.from({ length: 201 }, (_, i) => i + 1))).rejects.toThrow('failed')
    expect(apiClient.post).toHaveBeenCalledTimes(2)
  })
})
