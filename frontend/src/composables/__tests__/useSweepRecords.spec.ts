import { describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import type { SweepRecord } from '@/api/admin/crypto'
import { SWEEP_PAGE_SIZE, useSweepRecords } from '../useSweepRecords'

const rec = (id: number): SweepRecord => ({
  id, chain: 'bsc', from_address: '0x' + id.toString(16).padStart(40, '0'), to_address: '0x0', token: '0xtoken', amount: '1',
  gas_tx_hash: '', sweep_tx_hash: '', status: 'swept', error: '', created_at: '2026-11-05T00:00:00Z'
})
/** ids high..low, newest first, like the endpoint. */
const page = (high: number, count: number) => Array.from({ length: count }, (_, i) => rec(high - i))
const deferred = <T,>() => {
  let resolve!: (value: T) => void
  let reject!: (reason: unknown) => void
  const promise = new Promise<T>((res, rej) => { resolve = res; reject = rej })
  return { promise, resolve, reject }
}

describe('useSweepRecords', () => {
  it('loads the first page without a cursor and offers more when the page is full', async () => {
    const fetchPage = vi.fn().mockResolvedValue(page(500, 5))
    const sweeps = useSweepRecords(fetchPage, 5)
    await sweeps.reload()
    expect(fetchPage).toHaveBeenCalledWith({ limit: 5 })
    expect(sweeps.records.value.map(r => r.id)).toEqual([500, 499, 498, 497, 496])
    expect(sweeps.hasMore.value).toBe(true)
    expect(sweeps.loading.value).toBe(false)
  })

  it('defaults to a page of 100', () => {
    expect(SWEEP_PAGE_SIZE).toBe(100)
  })

  it('hides "load more" when the first page is short', async () => {
    const sweeps = useSweepRecords(vi.fn().mockResolvedValue(page(3, 3)), 5)
    await sweeps.reload()
    expect(sweeps.hasMore.value).toBe(false)
    await sweeps.loadMore()
    expect(sweeps.records.value).toHaveLength(3)
  })

  it('appends the next page using the lowest id as the cursor', async () => {
    const fetchPage = vi.fn()
      .mockResolvedValueOnce(page(10, 5))
      .mockResolvedValueOnce(page(5, 5))
      .mockResolvedValueOnce(page(0 + 0, 0))
    const sweeps = useSweepRecords(fetchPage, 5)
    await sweeps.reload()
    await sweeps.loadMore()
    expect(fetchPage).toHaveBeenLastCalledWith({ limit: 5, beforeId: 6 })
    expect(sweeps.records.value.map(r => r.id)).toEqual([10, 9, 8, 7, 6, 5, 4, 3, 2, 1])
    expect(sweeps.hasMore.value).toBe(true)
    await sweeps.loadMore()
    expect(fetchPage).toHaveBeenLastCalledWith({ limit: 5, beforeId: 1 })
    expect(sweeps.hasMore.value).toBe(false) // an empty page is the end
  })

  it('ends the list on a short page', async () => {
    const fetchPage = vi.fn().mockResolvedValueOnce(page(10, 5)).mockResolvedValueOnce(page(5, 2))
    const sweeps = useSweepRecords(fetchPage, 5)
    await sweeps.reload()
    await sweeps.loadMore()
    expect(sweeps.records.value).toHaveLength(7)
    expect(sweeps.hasMore.value).toBe(false)
  })

  it('drops rows it already holds and stops when a full page brings nothing new', async () => {
    // What a backend that ignores before_id keeps answering.
    const same = page(10, 5)
    const fetchPage = vi.fn().mockResolvedValue(same)
    const sweeps = useSweepRecords(fetchPage, 5)
    await sweeps.reload()
    await sweeps.loadMore()
    expect(sweeps.records.value.map(r => r.id)).toEqual([10, 9, 8, 7, 6])
    expect(sweeps.hasMore.value).toBe(false)
  })

  it('keeps only the new rows of a partly overlapping page', async () => {
    const fetchPage = vi.fn().mockResolvedValueOnce(page(10, 5)).mockResolvedValueOnce([rec(6), rec(5), rec(4), rec(3), rec(2)])
    const sweeps = useSweepRecords(fetchPage, 5)
    await sweeps.reload()
    await sweeps.loadMore()
    expect(sweeps.records.value.map(r => r.id)).toEqual([10, 9, 8, 7, 6, 5, 4, 3, 2])
    expect(sweeps.hasMore.value).toBe(true)
  })

  it('ignores "load more" while nothing is loaded, while loading, and while a request is out', async () => {
    const first = deferred<SweepRecord[]>()
    const fetchPage = vi.fn().mockReturnValue(first.promise)
    const sweeps = useSweepRecords(fetchPage, 5)
    await sweeps.loadMore()
    expect(fetchPage).not.toHaveBeenCalled()

    const reloading = sweeps.reload()
    await sweeps.loadMore()
    expect(fetchPage).toHaveBeenCalledTimes(1)
    expect(sweeps.loading.value).toBe(true)
    first.resolve(page(10, 5))
    await reloading

    const more = deferred<SweepRecord[]>()
    fetchPage.mockReturnValueOnce(more.promise)
    const loading = sweeps.loadMore()
    expect(sweeps.loadingMore.value).toBe(true)
    await sweeps.loadMore()
    expect(fetchPage).toHaveBeenCalledTimes(2)
    more.resolve(page(5, 5))
    await loading
    expect(sweeps.loadingMore.value).toBe(false)
  })

  it('a reload while "load more" is in flight discards the late answer', async () => {
    const stale = deferred<SweepRecord[]>()
    const fetchPage = vi.fn()
      .mockResolvedValueOnce(page(10, 5))
      .mockReturnValueOnce(stale.promise)
      .mockResolvedValueOnce(page(20, 5))
    const sweeps = useSweepRecords(fetchPage, 5)
    await sweeps.reload()
    const loading = sweeps.loadMore()
    await sweeps.reload()
    expect(sweeps.records.value.map(r => r.id)).toEqual([20, 19, 18, 17, 16])
    stale.resolve(page(5, 5))
    await loading
    await flushPromises()
    expect(sweeps.records.value.map(r => r.id)).toEqual([20, 19, 18, 17, 16])
    expect(sweeps.loadingMore.value).toBe(false)
    expect(sweeps.hasMore.value).toBe(true)
  })

  it('a failed reload keeps what is on screen and clears the loading flag', async () => {
    const fetchPage = vi.fn().mockResolvedValueOnce(page(10, 5)).mockRejectedValueOnce(new Error('down'))
    const sweeps = useSweepRecords(fetchPage, 5)
    await sweeps.reload()
    await expect(sweeps.reload()).rejects.toThrow('down')
    expect(sweeps.records.value).toHaveLength(5)
    expect(sweeps.loading.value).toBe(false)
  })

  it('a failed "load more" reports the error, keeps the rows and allows a retry', async () => {
    const fetchPage = vi.fn()
      .mockResolvedValueOnce(page(10, 5))
      .mockRejectedValueOnce(new Error('down'))
      .mockResolvedValueOnce(page(5, 5))
    const sweeps = useSweepRecords(fetchPage, 5)
    await sweeps.reload()
    await expect(sweeps.loadMore()).rejects.toThrow('down')
    expect(sweeps.records.value).toHaveLength(5)
    expect(sweeps.loadingMore.value).toBe(false)
    expect(sweeps.hasMore.value).toBe(true)
    await sweeps.loadMore()
    expect(sweeps.records.value).toHaveLength(10)
  })

  it('computes the cursor from the lowest id even if rows arrive unsorted', async () => {
    const fetchPage = vi.fn().mockResolvedValueOnce([rec(7), rec(9), rec(8)]).mockResolvedValueOnce([])
    const sweeps = useSweepRecords(fetchPage, 3)
    await sweeps.reload()
    await sweeps.loadMore()
    expect(fetchPage).toHaveBeenLastCalledWith({ limit: 3, beforeId: 7 })
  })
})
