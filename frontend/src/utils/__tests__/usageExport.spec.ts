import { describe, expect, it, vi } from 'vitest'
import type { UsageLog } from '@/types'
import { collectUsageForExport } from '@/utils/usageExport'

const row = (id: number) => ({ id } as UsageLog)
const signal = () => new AbortController().signal
const limited = { status: 429, retryAfter: '2' }
const noop = () => {}

describe('collectUsageForExport', () => {
  it('waits for Retry-After and resumes the clamped page without losing earlier rows', async () => {
    vi.useFakeTimers()
    const fetchPage = vi.fn()
      .mockResolvedValueOnce({ items: [row(1), row(2)], total: 3, page: 1, page_size: 2 })
      .mockRejectedValueOnce(limited)
      .mockResolvedValueOnce({ items: [row(3)], total: 3, page: 2, page_size: 2 })
    const waiting = vi.fn()
    try {
      const pending = collectUsageForExport(fetchPage, signal(), waiting)
      await vi.advanceTimersByTimeAsync(1999)
      expect(fetchPage.mock.calls).toEqual([[1, 100], [2, 2]])
      expect(waiting.mock.calls).toEqual([[true]])
      await vi.advanceTimersByTimeAsync(1)
      await expect(pending).resolves.toEqual([row(1), row(2), row(3)])
      expect(fetchPage.mock.calls).toEqual([[1, 100], [2, 2], [2, 2]])
      expect(waiting.mock.calls).toEqual([[true], [false]])
    } finally {
      vi.useRealTimers()
    }
  })

  it('cancels the wait without sending another request', async () => {
    vi.useFakeTimers()
    const controller = new AbortController()
    const fetchPage = vi.fn().mockRejectedValue(limited)
    const waiting = vi.fn()
    try {
      const pending = collectUsageForExport(fetchPage, controller.signal, waiting)
      const cancelled = expect(pending).rejects.toMatchObject({ name: 'AbortError' })
      await vi.advanceTimersByTimeAsync(0)
      expect(waiting).toHaveBeenCalledWith(true)
      controller.abort()
      await cancelled
      await vi.runAllTimersAsync()
      expect(fetchPage).toHaveBeenCalledTimes(1)
      expect(waiting.mock.calls).toEqual([[true], [false]])
    } finally {
      vi.useRealTimers()
    }
  })

  it('stops after two rate-limit retries of the same page', async () => {
    vi.useFakeTimers()
    const fetchPage = vi.fn().mockRejectedValue(limited)
    try {
      const failure = expect(collectUsageForExport(fetchPage, signal(), noop)).rejects.toEqual(limited)
      await vi.runAllTimersAsync()
      await failure
      expect(fetchPage.mock.calls).toEqual([[1, 100], [1, 100], [1, 100]])
    } finally {
      vi.useRealTimers()
    }
  })

  it('does not retry earlier than a server delay beyond the export wait budget', async () => {
    const tooLong = { status: 429, retryAfter: '901' }
    const fetchPage = vi.fn().mockRejectedValue(tooLong)
    await expect(collectUsageForExport(fetchPage, signal(), noop)).rejects.toEqual(tooLong)
    expect(fetchPage).toHaveBeenCalledTimes(1)
  })

  it('restarts once after a deletion changes the total and empties the next page', async () => {
    const all = [row(1), row(2)]
    const fetchPage = vi.fn()
      .mockResolvedValueOnce({ items: all, total: 3, page: 1, page_size: 2 })
      .mockResolvedValueOnce({ items: [], total: 2, page: 2, page_size: 2 })
      .mockResolvedValueOnce({ items: all, total: 2, page: 1, page_size: 2 })
    await expect(collectUsageForExport(fetchPage, signal(), noop)).resolves.toEqual(all)
    expect(fetchPage.mock.calls).toEqual([[1, 100], [2, 2], [1, 100]])
  })

  it('restarts once when pages overlap even if the reported total stayed the same', async () => {
    const page = (items: UsageLog[], number: number) => ({ items, total: 4, page: number, page_size: 2 })
    const fetchPage = vi.fn()
      .mockResolvedValueOnce(page([row(1), row(2)], 1))
      .mockResolvedValueOnce(page([row(2), row(3)], 2))
      .mockResolvedValueOnce(page([row(3), row(4)], 1))
      .mockResolvedValueOnce(page([row(5), row(6)], 2))
    await expect(collectUsageForExport(fetchPage, signal(), noop)).resolves.toEqual([row(3), row(4), row(5), row(6)])
    expect(fetchPage.mock.calls).toEqual([[1, 100], [2, 2], [1, 100], [2, 2]])
  })

  it('refuses persistent drift rather than returning partial rows', async () => {
    const fetchPage = vi.fn(async (page: number) => ({ items: [row(page)], total: 5, page, page_size: 2 }))
    await expect(collectUsageForExport(fetchPage, signal(), noop)).rejects.toThrow('Usage records changed')
    expect(fetchPage.mock.calls).toEqual([[1, 100], [1, 100]])
  })

  it('excludes in-flight rows appended by a request-ID search without reducing the completed total', async () => {
    const items = [{ ...row(1), inflight: true }, row(1), row(2)]
    const fetchPage = vi.fn().mockResolvedValue({ items, total: 2, page: 1, page_size: 2 })
    await expect(collectUsageForExport(fetchPage, signal(), noop)).resolves.toEqual([row(1), row(2)])
  })
})
