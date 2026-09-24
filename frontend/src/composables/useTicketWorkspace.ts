import { computed, onBeforeUnmount, onMounted, reactive, ref, watch, type Ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { newClientID, ticketsAPI, type Ticket, type TicketDetail, type TicketFilters, type TicketStats, type TicketReply, type TicketUpdate, type CreateTicket } from '@/api/tickets'

export function useTicketWorkspace(admin: Ref<boolean>) {
  const route = useRoute()
  const router = useRouter()
  const { t } = useI18n()
  const app = useAppStore()
  const api = computed(() => ticketsAPI(admin.value))
  const base = computed(() => admin.value ? '/admin/tickets' : '/tickets')
  const selectedID = computed(() => {
    const id = Number(route.params.id)
    return Number.isSafeInteger(id) && id > 0 ? id : 0
  })
  const filters = reactive<TicketFilters>({ page: 1, page_size: 20, search: '', status: '', category: '', priority: '', assigned_to: 'all' })
  const items = ref<Ticket[]>([])
  const total = ref(0)
  const pages = computed(() => Math.max(1, Math.ceil(total.value / filters.page_size)))
  const stats = ref<TicketStats>({ total: 0, open: 0, in_progress: 0, waiting_user: 0, resolved: 0, closed: 0, unread: 0 })
  const detail = ref<TicketDetail | null>(null)
  const loading = ref(true)
  const detailLoading = ref(false)
  const loadingOlder = ref(false)
  const refreshing = ref(false)
  const mutating = ref(false)
  const listError = ref('')
  const detailError = ref('')
  const syncFailed = ref(false)
  const revision = ref(0)
  const drafts = reactive<Record<string, string>>({})
  const draftKey = computed(() => `${admin.value}:${selectedID.value}`)
  const draft = computed({ get: () => drafts[draftKey.value] || '', set: (value: string) => { drafts[draftKey.value] = value } })
  let active = true
  let listController: AbortController | null = null
  let detailController: AbortController | null = null
  let olderController: AbortController | null = null
  let listSequence = 0
  let detailSequence = 0
  let timelineVersion = 0
  let refreshSequence = 0
  let pollTimer: ReturnType<typeof setTimeout> | undefined
  let searchTimer: ReturnType<typeof setTimeout> | undefined
  const replyAttempts = new Map<string, { content: string; status: TicketReply['status']; client_id: string }>()
  let readBusy = false
  const readCursors = new Map<string, number>()

  const errorMessage = (error: unknown) => {
    if (error && typeof error === 'object' && 'message' in error && typeof error.message === 'string') return error.message
    return t('tickets.failed')
  }

  async function loadList(quiet = false) {
    const sequence = ++listSequence
    listController?.abort()
    const controller = new AbortController()
    listController = controller
    if (!quiet) loading.value = true
    try {
      const [list, counts] = await Promise.all([
        api.value.list({ ...filters, assigned_to: admin.value ? filters.assigned_to : undefined }, controller.signal),
        api.value.stats(controller.signal)
      ])
      if (!active || sequence !== listSequence) return
      items.value = list.items
      total.value = list.total
      stats.value = counts
      listError.value = ''
      if (filters.page > pages.value) { filters.page = pages.value; void loadList(quiet) }
    } catch (error) {
      if (active && !controller.signal.aborted && sequence === listSequence) { listError.value = errorMessage(error); throw error }
    } finally {
      if (sequence === listSequence) loading.value = false
    }
  }

  function acceptDetail(next: TicketDetail, merge: boolean) {
    const current = detail.value
    if (merge && current?.ticket.id === next.ticket.id &&
        (!next.has_more || next.messages.some(m => current.messages.some(old => old.id === m.id)))) {
      const messages = new Map(current.messages.map(m => [m.id, m]))
      next.messages.forEach(m => messages.set(m.id, m))
      detail.value = { ...next, messages: [...messages.values()].sort((a, b) => a.id - b.id), has_more: current.has_more && next.has_more }
    } else { detail.value = next; timelineVersion++ }
    revision.value++
  }

  async function loadDetail(quiet = false) {
    const id = selectedID.value
    const scope = admin.value
    const sequence = ++detailSequence
    detailController?.abort()
    if (!id) { detail.value = null; detailLoading.value = false; return }
    const controller = new AbortController()
    detailController = controller
    if (!quiet) detailLoading.value = true
    try {
      const next = await api.value.detail(id, controller.signal)
      if (!active || sequence !== detailSequence || id !== selectedID.value || scope !== admin.value) return
      acceptDetail(next, quiet)
      detailError.value = ''
    } catch (error) {
      if (active && !controller.signal.aborted && sequence === detailSequence) { detailError.value = errorMessage(error); throw error }
    } finally {
      if (sequence === detailSequence) detailLoading.value = false
    }
  }

  async function refresh(quiet = false) {
    const sequence = ++refreshSequence
    refreshing.value = true
    const results = await Promise.allSettled([loadList(quiet), mutating.value ? Promise.resolve() : loadDetail(quiet)])
    if (active && sequence === refreshSequence) {
      syncFailed.value = results.some(result => result.status === 'rejected')
      refreshing.value = false
    }
  }

  async function older() {
    if (!detail.value?.has_more || loadingOlder.value) return
    const id = selectedID.value
    const scope = admin.value
    const first = detail.value.messages[0]?.id
    const version = timelineVersion
    if (!first) return
    loadingOlder.value = true
    const controller = new AbortController()
    olderController = controller
    try {
      const olderPage = await api.value.detail(id, controller.signal, first)
      if (active && !controller.signal.aborted && selectedID.value === id && admin.value === scope &&
          version === timelineVersion && detail.value?.messages[0]?.id === first) {
        const messages = new Map(olderPage.messages.map(m => [m.id, m]))
        detail.value.messages.forEach(m => messages.set(m.id, m))
        detail.value = { ...detail.value, messages: [...messages.values()].sort((a, b) => a.id - b.id), has_more: olderPage.has_more }
      }
    } catch (error) { if (!controller.signal.aborted) app.showError(errorMessage(error)) }
    finally { if (olderController === controller) loadingOlder.value = false }
  }

  async function markRead() {
    const last = detail.value?.messages.at(-1)?.id
    const key = draftKey.value
    const id = selectedID.value
    if (!last || readBusy || document.hidden || last <= (readCursors.get(key) || 0)) return
    readBusy = true
    try {
      await api.value.read(id, last)
      readCursors.set(key, last)
      if (active && draftKey.value === key && detail.value?.ticket.last_message_id === last) {
        detail.value.ticket.unread_count = 0
        const item = items.value.find(item => item.id === id)
        if (item) item.unread_count = 0
      }
    } catch { /* The next visible refresh retries this cursor. */ }
    finally { readBusy = false }
  }

  async function mutate(action: () => Promise<TicketDetail>, success: string, key: string) {
    if (mutating.value) return false
    mutating.value = true
    detailController?.abort()
    ++detailSequence
    try {
      const result = await action()
      if (active && draftKey.value === key) { acceptDetail(result, true); detailError.value = '' }
      if (active) { app.showSuccess(success); void loadList(true).catch(() => {}) }
      return true
    } catch (error) { if (active) app.showError(errorMessage(error)); return false }
    finally { mutating.value = false }
  }

  async function send(status?: TicketReply['status']) {
    const content = draft.value.trim()
    if (!content) return false
    const key = draftKey.value
    const id = selectedID.value
    let attempt = replyAttempts.get(key)
    if (!attempt || attempt.content !== content || attempt.status !== status) {
      attempt = { content, status, client_id: newClientID() }
      replyAttempts.set(key, attempt)
    }
    const request = { content, status, client_id: attempt.client_id }
    const success = await mutate(() => api.value.reply(id, request), t('tickets.sent'), key)
    if (success) {
      if ((drafts[key] || '').trim() === content) drafts[key] = ''
      if (replyAttempts.get(key) === attempt) replyAttempts.delete(key)
    }
    return success
  }

  async function update(input: TicketUpdate) {
    const id = selectedID.value
    return mutate(() => api.value.update(id, input), t('tickets.updated'), draftKey.value)
  }

  async function create(input: CreateTicket) {
    if (mutating.value) return false
    mutating.value = true
    try {
      const result = await api.value.create(input)
      if (active) {
        stats.value.can_create = false
        resetFilters()
        app.showSuccess(t('tickets.created'))
        await router.push(`${base.value}/${result.ticket.id}`)
        void loadList(true).catch(() => {})
      }
      return true
    } catch (error) { if (active) app.showError(errorMessage(error)); return false }
    finally { mutating.value = false }
  }

  function resetFilters() {
    Object.assign(filters, { page: 1, search: '', status: '', category: '', priority: '', assigned_to: 'all' })
  }
  function select(id: number) { return router.push(`${base.value}/${id}`) }
  function back() { return router.push(base.value) }
  function changePage(page: number) {
    if (page < 1 || page > pages.value || loading.value) return
    filters.page = page
    void loadList().catch(() => {})
  }
  watch(() => [filters.status, filters.category, filters.priority, filters.assigned_to], () => {
    filters.page = 1
    void loadList().catch(() => {})
  })
  watch(() => filters.search, () => {
    clearTimeout(searchTimer)
    searchTimer = setTimeout(() => { filters.page = 1; void loadList().catch(() => {}) }, 300)
  })
  watch([selectedID, admin], () => {
    detail.value = null
    timelineVersion++
    detailError.value = ''
    olderController?.abort()
    loadingOlder.value = false
    void loadDetail().catch(() => {})
  })
  watch(admin, () => { resetFilters(); void refresh() })

  function schedulePoll() {
    pollTimer = setTimeout(async () => {
      if (!active) return
      if (!document.hidden && !mutating.value && !refreshing.value) await refresh(true)
      if (active) schedulePoll()
    }, 15000)
  }
  const onVisibility = () => { if (!document.hidden && !mutating.value && !refreshing.value) void refresh(true) }
  onMounted(() => { void refresh(); schedulePoll(); document.addEventListener('visibilitychange', onVisibility) })
  onBeforeUnmount(() => {
    active = false
    clearTimeout(pollTimer)
    clearTimeout(searchTimer)
    listController?.abort()
    detailController?.abort()
    olderController?.abort()
    document.removeEventListener('visibilitychange', onVisibility)
  })

  return { filters, items, total, pages, stats, detail, selectedID, loading, detailLoading, loadingOlder, refreshing, mutating,
    listError, detailError, syncFailed, revision, draft, refresh, older, markRead, send, update, create, resetFilters, select, back, changePage }
}
