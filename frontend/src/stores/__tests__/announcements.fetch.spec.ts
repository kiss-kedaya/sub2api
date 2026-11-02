import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { useAnnouncementStore } from '../announcements'
import type { UserAnnouncement } from '@/types'

const api = vi.hoisted(() => ({
  list: vi.fn(),
  summary: vi.fn(),
  popups: vi.fn(),
  getById: vi.fn(),
  markRead: vi.fn(),
}))
vi.mock('@/api', () => ({ announcementsAPI: api }))

const row = (id: number) => ({
  id, title: `Notice ${id}`, excerpt: 'Excerpt', notify_mode: 'silent',
  created_at: '2026-09-18T00:00:00Z', updated_at: '2026-09-18T00:00:00Z'
})

function pending<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason: unknown) => void
  const promise = new Promise<T>((res, rej) => { resolve = res; reject = rej })
  return { promise, resolve, reject }
}

beforeEach(() => {
  setActivePinia(createPinia())
  vi.resetAllMocks()
  vi.spyOn(console, 'error').mockImplementation(() => {})
  api.popups.mockResolvedValue([])
  api.markRead.mockResolvedValue({ message: 'ok' })
})
afterEach(() => vi.restoreAllMocks())

describe('announcement summary', () => {
  it('does not restore counts after logout resets the store', async () => {
    const store = useAnnouncementStore()
    const old = pending<{ total: number; unread: number }>()
    api.summary.mockReturnValueOnce(old.promise)
    const request = store.fetchSummary()
    store.reset()
    old.resolve({ total: 5, unread: 3 })
    await request
    expect(store.unreadCount).toBe(0)
    expect(store.summaryLoading).toBe(false)
  })

  it('keeps the new session loading when an old request finishes', async () => {
    const store = useAnnouncementStore()
    const old = pending<{ total: number; unread: number }>()
    const current = pending<{ total: number; unread: number }>()
    api.summary.mockReturnValueOnce(old.promise).mockReturnValueOnce(current.promise)
    const oldRequest = store.fetchSummary()
    store.reset()
    const currentRequest = store.fetchSummary()
    old.resolve({ total: 1, unread: 1 })
    await oldRequest
    expect(store.summaryLoading).toBe(true)
    current.resolve({ total: 2, unread: 2 })
    await currentRequest
    expect(store.unreadCount).toBe(2)
    expect(store.summaryLoading).toBe(false)
  })

  it('keeps the latest forced refresh when responses arrive out of order', async () => {
    const store = useAnnouncementStore()
    const old = pending<{ total: number; unread: number }>()
    api.summary.mockReturnValueOnce(old.promise).mockResolvedValueOnce({ total: 4, unread: 4 })
    const oldRequest = store.fetchSummary()
    await store.fetchSummary(true)
    old.resolve({ total: 1, unread: 1 })
    await oldRequest
    expect(store.unreadCount).toBe(4)
  })

  it('keeps the last count and reports the failure instead of writing a fake zero', async () => {
    const store = useAnnouncementStore()
    api.summary.mockResolvedValueOnce({ total: 3, unread: 2 })
    await store.fetchSummary(true)
    expect(store.unreadCount).toBe(2)

    api.summary.mockRejectedValueOnce(new Error('offline'))
    await store.fetchSummary(true)
    expect(store.unreadCount).toBe(2)
    expect(store.summaryError).toBe('offline')

    api.summary.mockResolvedValueOnce({ total: 3, unread: 1 })
    await store.fetchSummary()
    expect(store.summaryError).toBeNull()
    expect(store.unreadCount).toBe(1)
  })

  it('dedupes concurrent fetches and throttles the next one', async () => {
    const store = useAnnouncementStore()
    const current = pending<{ total: number; unread: number }>()
    api.summary.mockReturnValueOnce(current.promise)
    const first = store.fetchSummary()
    const second = store.fetchSummary()
    expect(api.summary).toHaveBeenCalledTimes(1)
    current.resolve({ total: 1, unread: 1 })
    await Promise.all([first, second])
    await store.fetchSummary()
    expect(api.summary).toHaveBeenCalledTimes(1)
  })
})

describe('announcement timeline', () => {
  it('is not fetched until the list is asked for', async () => {
    const store = useAnnouncementStore()
    await store.fetchSummary(true)
    expect(api.list).not.toHaveBeenCalled()
    api.list.mockResolvedValueOnce([row(1)])
    await store.ensureList()
    expect(api.list).toHaveBeenCalledTimes(1)
    expect(store.announcements.map((item) => item.id)).toEqual([1])
  })

  it('keeps the failure visible and retries after a rejected list', async () => {
    const store = useAnnouncementStore()
    api.list.mockRejectedValueOnce(new Error('offline')).mockResolvedValueOnce([row(2)])
    await store.ensureList()
    expect(store.listError).toBe('offline')
    await store.ensureList()
    expect(api.list).toHaveBeenCalledTimes(2)
    expect(store.listError).toBeNull()
    expect(store.announcements.map((item) => item.id)).toEqual([2])
  })

  it('dedupes concurrent list requests', async () => {
    const store = useAnnouncementStore()
    const current = pending<ReturnType<typeof row>[]>()
    api.list.mockReturnValueOnce(current.promise)
    const first = store.ensureList()
    const second = store.ensureList()
    expect(api.list).toHaveBeenCalledTimes(1)
    current.resolve([row(3)])
    await Promise.all([first, second])
    expect(store.announcements.map((item) => item.id)).toEqual([3])
  })
})

describe('announcement detail', () => {
  it('fetches the body once and caches it', async () => {
    const store = useAnnouncementStore()
    store.announcements = [row(7) as unknown as UserAnnouncement]
    api.getById.mockResolvedValueOnce({ ...row(7), content: 'body', read_at: null })
    const detail = await store.fetchDetail(7)
    expect(detail?.content).toBe('body')
    await store.fetchDetail(7)
    expect(api.getById).toHaveBeenCalledTimes(1)
  })

  it('exposes the detail failure instead of an empty body', async () => {
    const store = useAnnouncementStore()
    api.getById.mockRejectedValueOnce(new Error('gone'))
    expect(await store.fetchDetail(8)).toBeNull()
    expect(store.detailErrorFor(8)).toBe('gone')
  })
})

describe('announcement popups', () => {
  it('dedupes popups the session already showed', async () => {
    const store = useAnnouncementStore()
    const popup = { ...row(11), content: 'body', notify_mode: 'popup' } as unknown as UserAnnouncement
    api.popups.mockResolvedValue([popup])
    await store.fetchPopups(true)
    expect(store.currentPopup?.id).toBe(11)
    store.currentPopup = null
    await store.fetchPopups(true)
    expect(store.currentPopup).toBeNull()
  })
})
