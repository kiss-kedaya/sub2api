import type { PaginatedResponse, UsageLog } from '@/types'
import { fetchPaginatedItems } from './paginatedItems'

const PAGE_SIZE = 100
const DEFAULT_RETRY_MS = 60_000
const RETRY_BUDGET_MS = 15 * 60_000
const MAX_RETRIES_PER_PAGE = 2

export class UsageExportChangedError extends Error {
  constructor() {
    super('Usage records changed during export')
    this.name = 'UsageExportChangedError'
  }
}

export function isUsageExportRateLimited(error: unknown): error is { status: 429; retryAfter?: string } {
  return typeof error === 'object' && error !== null && 'status' in error && error.status === 429
}

function getRetryDelay(retryAfter: unknown): number {
  if (typeof retryAfter !== 'string' || !retryAfter.trim()) return DEFAULT_RETRY_MS
  const raw = retryAfter.trim()
  const delay = /^\d+$/.test(raw) ? Number(raw) * 1000 : Date.parse(raw) - Date.now()
  // Never retry earlier than the advertised delay. A long delay exhausts the budget instead.
  return Number.isNaN(delay) ? DEFAULT_RETRY_MS : Math.max(1000, delay)
}

function waitForRetry(ms: number, signal: AbortSignal): Promise<void> {
  signal.throwIfAborted()
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => {
      signal.removeEventListener('abort', abort)
      resolve()
    }, ms)
    function abort() {
      clearTimeout(timer)
      reject(signal.reason)
    }
    signal.addEventListener('abort', abort, { once: true })
  })
}

export async function collectUsageForExport(
  fetchPage: (page: number, pageSize: number) => Promise<PaginatedResponse<UsageLog>>,
  signal: AbortSignal,
  onWaiting: (waiting: boolean) => void,
): Promise<UsageLog[]> {
  let waitBudget = RETRY_BUDGET_MS
  async function fetchWithRetry(page: number, pageSize: number) {
    for (let attempt = 0; ; attempt++) {
      signal.throwIfAborted()
      try {
        const result = await fetchPage(page, pageSize)
        signal.throwIfAborted()
        return result
      } catch (error) {
        signal.throwIfAborted()
        if (!isUsageExportRateLimited(error) || attempt >= MAX_RETRIES_PER_PAGE) throw error
        const delay = getRetryDelay(error.retryAfter)
        if (delay > waitBudget) throw error
        waitBudget -= delay
        onWaiting(true)
        try {
          await waitForRetry(delay, signal)
        } finally {
          onWaiting(false)
        }
      }
    }
  }

  // OFFSET pages aren't a snapshot. Restart once when the API exposes drift; don't download a partial CSV.
  for (let run = 0; ; run++) {
    let total = 0
    const ids = new Set<number>()
    try {
      return await fetchPaginatedItems(async (page, pageSize) => {
        const result = await fetchWithRetry(page, pageSize)
        if (!Array.isArray(result?.items) || !Number.isSafeInteger(result.total) || result.total < 0 ||
            (result.page != null && result.page !== page) ||
            (result.page_size != null && (!Number.isSafeInteger(result.page_size) || result.page_size < 1 || result.page_size > pageSize))) {
          throw new Error('Invalid usage export page')
        }
        if (page === 1) total = result.total
        const size = result.page_size ?? pageSize
        const expected = Math.max(0, Math.min(size, total - (page - 1) * size))
        // In-flight rows can be appended to page one for a request-ID search; CSV exports only completed records.
        const items = result.items.filter(item => !item?.inflight)
        if (result.total !== total || (page > 1 && size !== pageSize) || items.length !== expected) {
          throw new UsageExportChangedError()
        }
        for (const row of items) {
          if (!Number.isSafeInteger(row?.id) || row.id <= 0) throw new Error('Invalid usage export row')
          if (ids.has(row.id)) throw new UsageExportChangedError()
          ids.add(row.id)
        }
        return { ...result, items, page_size: size }
      }, PAGE_SIZE)
    } catch (error) {
      if (!(error instanceof UsageExportChangedError) || run !== 0) throw error
    }
  }
}
