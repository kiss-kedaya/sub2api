import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { announcementsAPI, groupWatchAPI } from '@/api'
import type { AnnouncementListItem } from '@/api/announcements'
import type { GroupWatchNotification } from '@/api/groupWatch'
import type { UserAnnouncement } from '@/types'

// The summary is a couple of counts, so it can be refreshed often; the timeline
// carries titles and excerpts and stays lazy until the bell is opened.
const SUMMARY_THROTTLE_MS = 60 * 1000
const LIST_THROTTLE_MS = 60 * 1000
const POPUP_THROTTLE_MS = 5 * 60 * 1000

const isAbortError = (error: unknown) => {
  if (!error || typeof error !== 'object') return false
  const { name, code } = error as { name?: string; code?: string }
  return name === 'AbortError' || code === 'ERR_CANCELED'
}

export const useAnnouncementStore = defineStore('announcements', () => {
  // Badge state (first paint)
  const unreadCount = ref(0)
  const totalCount = ref(0)
  const summaryLoaded = ref(false)
  const summaryLoading = ref(false)
  const summaryError = ref<string | null>(null)

  // Group-watch subscription notifications (merged into top-right header bell)
  const groupWatchUnreadCount = ref(0)
  const groupWatchTotalCount = ref(0)
  const groupWatchNotifications = ref<GroupWatchNotification[]>([])
  const groupWatchLoaded = ref(false)
  const groupWatchLoading = ref(false)
  const groupWatchError = ref<string | null>(null)
  const totalUnreadCount = computed(() => unreadCount.value + groupWatchUnreadCount.value)

  // Timeline (only fetched when the bell opens)
  const announcements = ref<AnnouncementListItem[]>([])
  const listLoaded = ref(false)
  const loading = ref(false)
  const listError = ref<string | null>(null)

  // Detail bodies, fetched per announcement and cached for the session
  const detailCache = ref<Record<number, UserAnnouncement>>({})
  const detailLoading = ref<Record<number, boolean>>({})
  const detailError = ref<Record<number, string | null>>({})

  // Popups
  const popupQueue = ref<UserAnnouncement[]>([])
  const currentPopup = ref<UserAnnouncement | null>(null)

  // Session-scoped dedup set — not reactive, used as a plain lookup only
  let shownPopupIds = new Set<number>()

  let summaryGeneration = 0
  let listGeneration = 0
  let groupWatchListGeneration = 0
  let summaryLastFetch = 0
  let listLastFetch = 0
  let groupWatchLastFetch = 0
  let popupLastFetch = 0

  let summaryAbort: AbortController | null = null
  let listAbort: AbortController | null = null
  let groupWatchAbort: AbortController | null = null
  let summaryInflight: Promise<void> | null = null
  let listInflight: Promise<void> | null = null
  let groupWatchInflight: Promise<void> | null = null
  let popupsInflight: Promise<void> | null = null

  /**
   * Counts only. Cheap enough to run on login, route changes and tab focus.
   * A failure keeps the previous count and surfaces `summaryError`; it never
   * writes a fake 0.
   */
  async function fetchSummary(force = false): Promise<void> {
    if (!force) {
      if (summaryInflight) return summaryInflight
      if (summaryLastFetch > 0 && Date.now() - summaryLastFetch < SUMMARY_THROTTLE_MS) return
    }

    const generation = ++summaryGeneration
    summaryLastFetch = Date.now()
    summaryAbort?.abort()
    const controller = new AbortController()
    summaryAbort = controller
    summaryLoading.value = true

    const request = (async () => {
      try {
        const [annResult, gwResult] = await Promise.allSettled([
          announcementsAPI.summary({ signal: controller.signal }),
          groupWatchAPI?.getNotificationSummary
            ? groupWatchAPI.getNotificationSummary({ signal: controller.signal })
            : Promise.resolve({ total: 0, unread: 0 }),
        ])
        if (generation !== summaryGeneration) return
        if (gwResult.status === 'fulfilled') {
          groupWatchTotalCount.value = gwResult.value.total
          groupWatchUnreadCount.value = gwResult.value.unread
        }
        if (annResult.status === 'rejected') {
          throw annResult.reason
        }
        totalCount.value = annResult.value.total
        unreadCount.value = annResult.value.unread
        summaryLoaded.value = true
        summaryError.value = null
      } catch (err: unknown) {
        if (generation !== summaryGeneration || isAbortError(err)) return
        // Allow an immediate retry, keep the last good count, and report the failure.
        summaryLastFetch = 0
        summaryError.value = err instanceof Error ? err.message : String(err)
        console.error('Failed to fetch announcement summary:', err)
      } finally {
        if (generation === summaryGeneration) summaryLoading.value = false
      }
    })()

    summaryInflight = request
    try {
      await request
    } finally {
      if (summaryInflight === request) summaryInflight = null
    }
  }

  /** Timeline for the bell. Called on open only (and throttled afterwards). */
  async function ensureList(force = false): Promise<void> {
    if (!force) {
      if (listInflight) return listInflight
      if (listLastFetch > 0 && Date.now() - listLastFetch < LIST_THROTTLE_MS) return
    }

    const generation = ++listGeneration
    listLastFetch = Date.now()
    listAbort?.abort()
    const controller = new AbortController()
    listAbort = controller
    loading.value = true

    const request = (async () => {
      try {
        const items = await announcementsAPI.list(false, { signal: controller.signal })
        if (generation !== listGeneration) return
        announcements.value = items.slice(0, 20)
        listLoaded.value = true
        listError.value = null
      } catch (err: unknown) {
        if (generation !== listGeneration || isAbortError(err)) return
        listLastFetch = 0
        listError.value = err instanceof Error ? err.message : String(err)
        console.error('Failed to fetch announcements:', err)
      } finally {
        if (generation === listGeneration) loading.value = false
      }
    })()

    listInflight = request
    try {
      await request
    } finally {
      if (listInflight === request) listInflight = null
    }
  }

  /** Kept for callers that predate the split; behaves like ensureList. */
  const fetchAnnouncements = (force = false) => ensureList(force)

  /** Body for one announcement. Cached per session; errors surface, never blank. */
  async function fetchDetail(id: number, force = false): Promise<UserAnnouncement | null> {
    if (!id) return null
    const cached = detailCache.value[id]
    if (cached?.content && !force) return cached

    detailLoading.value = { ...detailLoading.value, [id]: true }
    try {
      const data = await announcementsAPI.getById(id)
      detailCache.value = { ...detailCache.value, [id]: data }
      detailError.value = { ...detailError.value, [id]: null }
      const row = announcements.value.find((item) => item.id === id)
      if (row) row.read_at = data.read_at
      return data
    } catch (err: unknown) {
      detailError.value = {
        ...detailError.value,
        [id]: err instanceof Error ? err.message : String(err)
      }
      console.error('Failed to fetch announcement detail:', err)
      return null
    } finally {
      detailLoading.value = { ...detailLoading.value, [id]: false }
    }
  }

  function detailFor(id: number | null | undefined): UserAnnouncement | null {
    if (!id) return null
    return detailCache.value[id] ?? null
  }

  function isDetailLoading(id: number): boolean {
    return !!detailLoading.value[id]
  }

  function detailErrorFor(id: number | null | undefined): string | null {
    if (!id) return null
    return detailError.value[id] ?? null
  }

  /** Unread popup bootstrap. Bounded on the server and deduped per session. */
  async function fetchPopups(force = false): Promise<void> {
    if (!force) {
      if (popupsInflight) return popupsInflight
      if (popupLastFetch > 0 && Date.now() - popupLastFetch < POPUP_THROTTLE_MS) return
    }
    popupLastFetch = Date.now()

    const request = (async () => {
      try {
        const items = await announcementsAPI.popups()
        enqueueNewPopups(items)
      } catch (err: unknown) {
        if (isAbortError(err)) return
        popupLastFetch = 0
        console.error('Failed to fetch announcement popups:', err)
      }
    })()

    popupsInflight = request
    try {
      await request
    } finally {
      if (popupsInflight === request) popupsInflight = null
    }
  }

  function enqueueNewPopups(items: UserAnnouncement[]) {
    const next = items.filter((a) => !a.read_at && !shownPopupIds.has(a.id))
    if (next.length === 0) return

    for (const popup of next) {
      if (!popupQueue.value.some((queued) => queued.id === popup.id)) {
        popupQueue.value.push(popup)
      }
    }

    if (!currentPopup.value) {
      showNextPopup()
    }
  }

  function showNextPopup() {
    if (popupQueue.value.length === 0) {
      currentPopup.value = null
      return
    }
    currentPopup.value = popupQueue.value.shift()!
    shownPopupIds.add(currentPopup.value.id)
  }

  async function dismissPopup() {
    if (!currentPopup.value) return
    const id = currentPopup.value.id
    currentPopup.value = null

    // Mark as read (fire-and-forget, UI already updated)
    void markAsRead(id)

    if (popupQueue.value.length > 0) {
      setTimeout(() => showNextPopup(), 300)
    }
  }

  function applyReadLocally(id: number, readAt: string) {
    const row = announcements.value.find((item) => item.id === id)
    if (row && !row.read_at) {
      row.read_at = readAt
      unreadCount.value = Math.max(0, unreadCount.value - 1)
    }
    const cached = detailCache.value[id]
    if (cached && !cached.read_at) {
      detailCache.value = { ...detailCache.value, [id]: { ...cached, read_at: readAt } }
    }
  }

  async function markAsRead(id: number) {
    try {
      await announcementsAPI.markRead(id)
      applyReadLocally(id, new Date().toISOString())
    } catch (err: unknown) {
      console.error('Failed to mark announcement as read:', err)
      throw err
    }
  }

  async function markAllAsRead() {
    const unread = announcements.value.filter((a) => !a.read_at)
    if (unread.length === 0) {
      unreadCount.value = 0
      return
    }

    try {
      loading.value = true
      const results = await Promise.allSettled(
        unread.map(async (a) => {
          await announcementsAPI.markRead(a.id)
          applyReadLocally(a.id, new Date().toISOString())
        })
      )
      const failure = results.find((result) => result.status === 'rejected')
      if (failure) throw (failure as PromiseRejectedResult).reason
      unreadCount.value = 0
    } catch (err: unknown) {
      console.error('Failed to mark all as read:', err)
      throw err
    } finally {
      loading.value = false
    }
  }

  async function ensureGroupWatchList(force = false): Promise<void> {
    if (!force) {
      if (groupWatchInflight) return groupWatchInflight
      if (groupWatchLastFetch > 0 && Date.now() - groupWatchLastFetch < LIST_THROTTLE_MS) return
    }

    const generation = ++groupWatchListGeneration
    groupWatchLastFetch = Date.now()
    groupWatchAbort?.abort()
    const controller = new AbortController()
    groupWatchAbort = controller
    groupWatchLoading.value = true

    const request = (async () => {
      try {
        const items = groupWatchAPI?.listNotifications
          ? await groupWatchAPI.listNotifications(false, 30, { signal: controller.signal })
          : []
        if (generation !== groupWatchListGeneration) return
        groupWatchNotifications.value = items
        groupWatchTotalCount.value = items.length
        groupWatchUnreadCount.value = items.filter((item) => !item.read_at).length
        groupWatchLoaded.value = true
        groupWatchError.value = null
      } catch (err: unknown) {
        if (generation !== groupWatchListGeneration || isAbortError(err)) return
        groupWatchLastFetch = 0
        groupWatchError.value = err instanceof Error ? err.message : String(err)
        console.error('Failed to fetch group watch notifications:', err)
      } finally {
        if (generation === groupWatchListGeneration) groupWatchLoading.value = false
      }
    })()

    groupWatchInflight = request
    try {
      await request
    } finally {
      if (groupWatchInflight === request) groupWatchInflight = null
    }
  }

  async function markGroupWatchRead(id: number): Promise<void> {
    await groupWatchAPI.markNotificationRead(id)
    const row = groupWatchNotifications.value.find((item) => item.id === id)
    if (row && !row.read_at) {
      row.read_at = new Date().toISOString()
      groupWatchUnreadCount.value = Math.max(0, groupWatchUnreadCount.value - 1)
    }
  }

  async function markAllGroupWatchRead(): Promise<void> {
    if (groupWatchUnreadCount.value === 0 && !groupWatchNotifications.value.some((n) => !n.read_at)) {
      return
    }
    try {
      groupWatchLoading.value = true
      await groupWatchAPI.markAllNotificationsRead()
      const nowISO = new Date().toISOString()
      for (const item of groupWatchNotifications.value) {
        if (!item.read_at) item.read_at = nowISO
      }
      groupWatchUnreadCount.value = 0
    } finally {
      groupWatchLoading.value = false
    }
  }

  function reset() {
    summaryGeneration++
    listGeneration++
    groupWatchListGeneration++
    summaryAbort?.abort()
    listAbort?.abort()
    groupWatchAbort?.abort()
    summaryAbort = null
    listAbort = null
    groupWatchAbort = null
    summaryInflight = null
    listInflight = null
    groupWatchInflight = null
    popupsInflight = null

    unreadCount.value = 0
    totalCount.value = 0
    summaryLoaded.value = false
    summaryLoading.value = false
    summaryError.value = null
    summaryLastFetch = 0

    groupWatchUnreadCount.value = 0
    groupWatchTotalCount.value = 0
    groupWatchNotifications.value = []
    groupWatchLoaded.value = false
    groupWatchLoading.value = false
    groupWatchError.value = null
    groupWatchLastFetch = 0

    announcements.value = []
    listLoaded.value = false
    loading.value = false
    listError.value = null
    listLastFetch = 0

    detailCache.value = {}
    detailLoading.value = {}
    detailError.value = {}

    shownPopupIds = new Set()
    popupQueue.value = []
    currentPopup.value = null
    popupLastFetch = 0
  }

  return {
    // Badge state
    unreadCount,
    totalCount,
    totalUnreadCount,
    summaryLoaded,
    summaryLoading,
    summaryError,
    // Group watch notifications
    groupWatchUnreadCount,
    groupWatchTotalCount,
    groupWatchNotifications,
    groupWatchLoaded,
    groupWatchLoading,
    groupWatchError,
    // Timeline
    announcements,
    listLoaded,
    loading,
    listError,
    // Detail
    detailCache,
    detailFor,
    isDetailLoading,
    detailErrorFor,
    // Popups
    currentPopup,
    // Actions
    fetchSummary,
    ensureList,
    ensureGroupWatchList,
    fetchAnnouncements,
    fetchDetail,
    fetchPopups,
    dismissPopup,
    markAsRead,
    markAllAsRead,
    markGroupWatchRead,
    markAllGroupWatchRead,
    reset,
  }
})
