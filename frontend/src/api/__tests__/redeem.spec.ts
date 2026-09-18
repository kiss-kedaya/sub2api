import { beforeEach, describe, expect, it, vi } from 'vitest'
import { getHistory } from '../redeem'

const { get } = vi.hoisted(() => ({ get: vi.fn() }))
vi.mock('../client', () => ({ apiClient: { get } }))

describe('redemption history pagination', () => {
  beforeEach(() => vi.clearAllMocks())

  it('keeps history readable when a retained old instance returns the legacy array', async () => {
    const records = Array.from({ length: 25 }, (_, index) => ({ id: index + 1, code: 'test' }))
    get.mockResolvedValue({ data: records })
    const result = await getHistory(2, 20)
    expect(result).toMatchObject({ page: 2, page_size: 20, total: 25, pages: 2 })
    expect(result.items.map(item => item.id)).toEqual([21, 22, 23, 24, 25])
  })

  it.each([[undefined, undefined, 1, 20], [3, 50, 3, 50], [2, 100, 2, 100]])(
    'sends page %s and size %s to the server', async (page, size, expectedPage, expectedSize) => {
      const response = { items: [], total: 105, page: expectedPage, page_size: expectedSize, pages: 6 }
      get.mockResolvedValue({ data: response })
      expect(await getHistory(page, size)).toEqual(response)
      expect(get).toHaveBeenCalledWith('/redeem/history', {
        params: { page: expectedPage, page_size: expectedSize }
      })
    }
  )
})
