import { describe, expect, it, vi } from 'vitest'
import { fetchPaginatedItems } from '@/utils/paginatedItems'

describe('fetchPaginatedItems', () => {
  it('keeps fetching after the backend clamps 1000 to 50', async () => {
    const all = Array.from({ length: 120 }, (_, id) => ({ id }))
    const fetchPage = vi.fn(async (page: number, size: number) => {
      const actual = Math.min(size, 50)
      return { items: all.slice((page - 1) * actual, page * actual), total: all.length, page_size: actual }
    })
    expect(await fetchPaginatedItems(fetchPage, 1000, 1000)).toEqual(all)
    expect(fetchPage.mock.calls).toEqual([[1, 1000], [2, 50], [3, 50]])
  })

  it('preserves the original total item ceiling for selectors and batch operations', async () => {
    const all = Array.from({ length: 1500 }, (_, id) => id)
    const fetchPage = vi.fn(async (page: number) => ({
      items: all.slice((page - 1) * 50, page * 50), total: all.length, page_size: 50
    }))
    expect(await fetchPaginatedItems(fetchPage, 1000, 1000)).toEqual(all.slice(0, 1000))
    expect(fetchPage).toHaveBeenCalledTimes(20)
  })

  it('does not submit a partial collection when a later page is empty', async () => {
    const fetchPage = vi.fn()
      .mockResolvedValueOnce({ items: [1, 2], total: 4, page_size: 2 })
      .mockResolvedValueOnce({ items: [], total: 4, page_size: 2 })
    await expect(fetchPaginatedItems(fetchPage, 100)).rejects.toThrow('Incomplete')
  })

  it('rejects changed page sizes instead of silently skipping items', async () => {
    const fetchPage = vi.fn()
      .mockResolvedValueOnce({ items: [1, 2], total: 4, page_size: 2 })
      .mockResolvedValueOnce({ items: [2], total: 4, page_size: 1 })
    await expect(fetchPaginatedItems(fetchPage, 100)).rejects.toThrow('Incomplete')
  })
})
