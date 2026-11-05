import { ref } from 'vue'
import type { SweepRecord } from '@/api/admin/crypto'

/** Rows per request. The backend caps a page at 200 and defaults to 100. */
export const SWEEP_PAGE_SIZE = 100

type PageFetcher = (page: { limit: number; beforeId?: number }) => Promise<SweepRecord[]>

/**
 * Paged sweep history, newest first. `reload` replaces the list with the first
 * page; `loadMore` appends the page after the oldest id already held.
 *
 * A reload bumps `epoch`, so a "load more" answer that arrives afterwards is
 * dropped instead of being appended to a list it no longer belongs to.
 */
export function useSweepRecords(fetchPage: PageFetcher, pageSize: number = SWEEP_PAGE_SIZE) {
  const records = ref<SweepRecord[]>([])
  const hasMore = ref(false)
  const loading = ref(false)
  const loadingMore = ref(false)
  let epoch = 0

  async function reload(): Promise<void> {
    const mine = ++epoch
    loading.value = true
    loadingMore.value = false
    try {
      const page = await fetchPage({ limit: pageSize })
      if (mine !== epoch) return
      records.value = page
      hasMore.value = page.length >= pageSize
    } finally {
      if (mine === epoch) loading.value = false
    }
  }

  async function loadMore(): Promise<void> {
    if (loading.value || loadingMore.value || !hasMore.value || records.value.length === 0) return
    const mine = epoch
    loadingMore.value = true
    try {
      const cursor = records.value.reduce((lowest, record) => Math.min(lowest, record.id), Number.POSITIVE_INFINITY)
      const page = await fetchPage({ limit: pageSize, beforeId: cursor })
      if (mine !== epoch) return
      const known = new Set(records.value.map(record => record.id))
      const fresh = page.filter(record => !known.has(record.id))
      records.value = [...records.value, ...fresh]
      // A short page is the end. So is a full page with nothing new in it, which
      // is what a backend that ignores `before_id` keeps answering.
      hasMore.value = page.length >= pageSize && fresh.length > 0
    } finally {
      if (mine === epoch) loadingMore.value = false
    }
  }

  return { records, hasMore, loading, loadingMore, reload, loadMore }
}
