import { defineStore } from 'pinia'
import { ref } from 'vue'
import { ticketsAPI } from '@/api/tickets'

/**
 * Sidebar badge counts for support tickets.
 *
 * Every read goes through the existing `/tickets/stats` and
 * `/admin/tickets/stats` endpoints: they already return counts only, are
 * authenticated, and never pull the ticket list. Requests are deduped per scope,
 * throttled, and cancelled when a newer one starts.
 */

export type TicketScope = 'user' | 'admin'

const THROTTLE_MS = 60 * 1000

const isAbortError = (error: unknown) => {
  if (!error || typeof error !== 'object') return false
  const { name, code } = error as { name?: string; code?: string }
  return name === 'AbortError' || code === 'ERR_CANCELED'
}

interface ScopeState {
  unread: ReturnType<typeof ref<number>>
  total: ReturnType<typeof ref<number>>
  loading: ReturnType<typeof ref<boolean>>
  loaded: ReturnType<typeof ref<boolean>>
  error: ReturnType<typeof ref<string | null>>
  lastFetch: number
}

function createScopeState(): ScopeState {
  return {
    unread: ref(0),
    total: ref(0),
    loading: ref(false),
    loaded: ref(false),
    error: ref<string | null>(null),
    lastFetch: 0,
  }
}

export const useTicketNotificationStore = defineStore('ticketNotifications', () => {
  const scopes: Record<TicketScope, ScopeState> = {
    user: createScopeState(),
    admin: createScopeState(),
  }

  const inflight = new Map<TicketScope, Promise<void>>()
  const controllers = new Map<TicketScope, AbortController>()
  const generations: Record<TicketScope, number> = { user: 0, admin: 0 }

  const unreadFor = (scope: TicketScope) => scopes[scope].unread.value
  const totalFor = (scope: TicketScope) => scopes[scope].total.value
  const isLoading = (scope: TicketScope) => scopes[scope].loading.value
  const isLoaded = (scope: TicketScope) => scopes[scope].loaded.value
  /** Last failure for this scope, or null. Never converted into a fake zero. */
  const errorFor = (scope: TicketScope) => scopes[scope].error.value

  async function fetchSummary(scope: TicketScope, force = false): Promise<void> {
    const state = scopes[scope]
    if (!force) {
      const pending = inflight.get(scope)
      if (pending) return pending
      if (state.lastFetch > 0 && Date.now() - state.lastFetch < THROTTLE_MS) return
    }

    const generation = ++generations[scope]
    state.lastFetch = Date.now()
    controllers.get(scope)?.abort()
    const controller = new AbortController()
    controllers.set(scope, controller)
    state.loading.value = true

    const request = (async () => {
      try {
        const stats = await ticketsAPI(scope === 'admin').stats(controller.signal)
        if (generation !== generations[scope]) return
        const unread = Number(stats?.unread ?? 0)
        state.unread.value = Number.isFinite(unread) && unread > 0 ? unread : 0
        state.total.value = Number(stats?.total ?? 0) || 0
        state.loaded.value = true
        state.error.value = null
      } catch (err: unknown) {
        if (generation !== generations[scope] || isAbortError(err)) return
        // Keep the last good number, allow an immediate retry, and expose the failure.
        state.lastFetch = 0
        state.error.value = err instanceof Error ? err.message : String(err)
        console.error(`Failed to fetch ${scope} ticket summary:`, err)
      } finally {
        if (generation === generations[scope]) state.loading.value = false
      }
    })()

    inflight.set(scope, request)
    try {
      await request
    } finally {
      if (inflight.get(scope) === request) inflight.delete(scope)
    }
  }

  /** Refreshes every scope the signed-in user can see. */
  async function fetchAll(isAdmin: boolean, force = false): Promise<void> {
    const tasks: Array<Promise<void>> = [fetchSummary('user', force)]
    if (isAdmin) tasks.push(fetchSummary('admin', force))
    await Promise.all(tasks)
  }

  function reset() {
    for (const scope of ['user', 'admin'] as TicketScope[]) {
      generations[scope]++
      controllers.get(scope)?.abort()
      controllers.delete(scope)
      inflight.delete(scope)
      const state = scopes[scope]
      state.unread.value = 0
      state.total.value = 0
      state.loading.value = false
      state.loaded.value = false
      state.error.value = null
      state.lastFetch = 0
    }
  }

  return {
    unreadFor,
    totalFor,
    isLoading,
    isLoaded,
    errorFor,
    fetchSummary,
    fetchAll,
    reset,
  }
})
