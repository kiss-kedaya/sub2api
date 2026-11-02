import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { useTicketNotificationStore } from '../ticketNotifications'

const stats = vi.hoisted(() => ({ user: vi.fn(), admin: vi.fn() }))
const apiFactory = vi.hoisted(() => vi.fn())

vi.mock('@/api/tickets', () => ({ ticketsAPI: apiFactory }))

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
  // resetAllMocks clears implementations, so re-wire the scope routing each time.
  apiFactory.mockImplementation((admin: boolean) => ({ stats: admin ? stats.admin : stats.user }))
})
afterEach(() => vi.restoreAllMocks())

describe('ticket notification counts', () => {
  it('reads the existing stats endpoint instead of a ticket list', async () => {
    const store = useTicketNotificationStore()
    stats.user.mockResolvedValue({ unread: 3, total: 7, can_create: true })
    await store.fetchSummary('user')
    expect(apiFactory).toHaveBeenCalledWith(false)
    expect(stats.user).toHaveBeenCalledTimes(1)
    expect(store.unreadFor('user')).toBe(3)
    expect(store.totalFor('user')).toBe(7)
  })

  it('refreshes both scopes for an admin and only the user scope otherwise', async () => {
    const store = useTicketNotificationStore()
    stats.user.mockResolvedValue({ unread: 1, total: 1 })
    stats.admin.mockResolvedValue({ unread: 4, total: 9 })

    await store.fetchAll(false, true)
    expect(store.unreadFor('user')).toBe(1)
    expect(stats.admin).not.toHaveBeenCalled()

    await store.fetchAll(true, true)
    expect(apiFactory).toHaveBeenCalledWith(true)
    expect(store.unreadFor('admin')).toBe(4)
  })

  it('dedupes concurrent fetches for the same scope', async () => {
    const store = useTicketNotificationStore()
    const current = pending<{ unread: number; total: number }>()
    stats.user.mockReturnValueOnce(current.promise)
    const first = store.fetchSummary('user')
    const second = store.fetchSummary('user')
    expect(stats.user).toHaveBeenCalledTimes(1)
    current.resolve({ unread: 2, total: 2 })
    await Promise.all([first, second])
    expect(store.unreadFor('user')).toBe(2)
  })

  it('keeps the last count and surfaces the failure instead of reporting zero', async () => {
    const store = useTicketNotificationStore()
    stats.user.mockResolvedValueOnce({ unread: 5, total: 5 })
    await store.fetchSummary('user', true)
    expect(store.unreadFor('user')).toBe(5)

    stats.user.mockRejectedValueOnce(new Error('offline'))
    await store.fetchSummary('user', true)
    expect(store.unreadFor('user')).toBe(5)
    expect(store.errorFor('user')).toBe('offline')

    // A failed fetch must not close the throttle permanently.
    stats.user.mockResolvedValueOnce({ unread: 0, total: 5 })
    await store.fetchSummary('user')
    expect(store.errorFor('user')).toBeNull()
    expect(store.unreadFor('user')).toBe(0)
  })

  it('throttles repeated fetches and clears everything on reset', async () => {
    const store = useTicketNotificationStore()
    stats.user.mockResolvedValue({ unread: 2, total: 2 })
    await store.fetchSummary('user')
    await store.fetchSummary('user')
    expect(stats.user).toHaveBeenCalledTimes(1)

    store.reset()
    expect(store.unreadFor('user')).toBe(0)
    expect(store.isLoaded('user')).toBe(false)
    await store.fetchSummary('user')
    expect(stats.user).toHaveBeenCalledTimes(2)
  })
})
