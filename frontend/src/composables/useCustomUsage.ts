import { computed, onMounted, onUnmounted, ref, watch, type Ref } from 'vue'
import type { AccountListItem } from '@/types'
import { getCachedUsage, getConfig, queryUsage, type CustomUsageConfig, type CustomUsageResult } from '@/api/admin/customUsage'
import { supportsCustomUsage } from '@/utils/customUsage'

export interface CustomUsageRowState { result?: CustomUsageResult; loading?: boolean; error?: boolean }
type Schedule = Pick<CustomUsageConfig, 'enabled' | 'interval_minutes'>
type Job = { id: number; kind: 'config' | 'query'; force?: boolean; revision: number; version: number }
const MAX_CONCURRENT = 2

export function useCustomUsage(rows: Ref<AccountListItem[]>, active: Ref<boolean>, pageKey: Ref<string>) {
  const states = ref<Record<number, CustomUsageRowState>>({})
  const visible = new Set<number>()
  const cached = new Set<number>()
  const schedules = new Map<number, Schedule>()
  const attempts = new Map<number, number>()
  const versions = new Map<number, number>()
  const queue = new Map<number, Job>()
  const controllers = new Map<number, AbortController>()
  const eligible = computed(() => new Map(rows.value.filter(supportsCustomUsage).map(row => [row.id, row])))
  let revision = 0
  let inFlight = 0
  let disposed = false
  let cacheController: AbortController | undefined
  let cacheTimer: ReturnType<typeof setTimeout> | undefined
  let tickTimer: ReturnType<typeof setInterval> | undefined
  const canRun = (id: number) => !disposed && active.value && document.visibilityState !== 'hidden' && visible.has(id) && eligible.value.has(id)
  const patch = (id: number, value: CustomUsageRowState) => { states.value[id] = { ...states.value[id], ...value } }

  function stopRequests() {
    revision++
    cacheController?.abort()
    cacheController = undefined
    if (cacheTimer) clearTimeout(cacheTimer)
    cacheTimer = undefined
    queue.clear()
    controllers.forEach(controller => controller.abort())
    controllers.clear()
    for (const id of Object.keys(states.value)) patch(Number(id), { loading: false })
  }
  function enqueue(id: number, kind: Job['kind'], force = false) {
    if (!canRun(id) || controllers.has(id) || queue.has(id)) return
    queue.set(id, { id, kind, force, revision, version: versions.get(id) ?? 0 })
    if (kind === 'query') patch(id, { loading: true, error: false })
    pump()
  }
  function pump() {
    while (inFlight < MAX_CONCURRENT && queue.size) {
      const entry = queue.entries().next().value as [number, Job]
      const [id, job] = entry
      queue.delete(id)
      if (!canRun(id) || job.revision !== revision) continue
      const controller = new AbortController()
      controllers.set(id, controller)
      inFlight++
      void run(job, controller).finally(() => {
        inFlight--
        if (controllers.get(id) === controller) controllers.delete(id)
        pump()
      })
    }
  }
  async function run(job: Job, controller: AbortController) {
    const { id } = job
    const current = () => !controller.signal.aborted && job.revision === revision && job.version === (versions.get(id) ?? 0) && canRun(id)
    try {
      if (job.kind === 'config') {
        const config = await getConfig(id, controller.signal)
        if (!current()) return
        schedules.set(id, { enabled: config.enabled, interval_minutes: config.interval_minutes })
        // A cached item can be older than the persisted configuration.
        if (states.value[id]?.result) states.value[id].result!.enabled = config.enabled
      } else {
        attempts.set(id, Date.now())
        const result = await queryUsage(id, { force: job.force }, controller.signal)
        if (current()) patch(id, { result, error: false })
      }
    } catch {
      // Never forward Axios objects or upstream error bodies: they may contain credentials.
      if (current()) patch(id, { error: true })
    } finally {
      if (current()) patch(id, { loading: false })
    }
  }
  async function loadCache() {
    cacheTimer = undefined
    if (cacheController || disposed || !active.value || document.visibilityState === 'hidden') return
    const ids = [...visible].filter(id => canRun(id) && !cached.has(id)).slice(0, 50)
    if (!ids.length) return
    const generation = revision
    const cacheVersions = new Map(ids.map(id => [id, versions.get(id) ?? 0]))
    const controller = new AbortController()
    cacheController = controller
    ids.forEach(id => { cached.add(id); patch(id, { loading: true }) })
    try {
      const response = await getCachedUsage(ids, controller.signal)
      if (controller.signal.aborted || generation !== revision) return
      for (const id of ids) {
        if (!canRun(id) || cacheVersions.get(id) !== (versions.get(id) ?? 0)) continue
        const result = response.items[String(id)]
        patch(id, { result, loading: false, error: false })
        if (result?.enabled && result.configured && !schedules.has(id)) {
          if (Number.isSafeInteger(result.interval_minutes) && (result.interval_minutes === 0 || result.interval_minutes! >= 5)) {
            schedules.set(id, { enabled: true, interval_minutes: result.interval_minutes! })
          } else enqueue(id, 'config')
        }
      }
    } catch {
      if (!controller.signal.aborted && generation === revision) ids.forEach(id => { if (canRun(id) && cacheVersions.get(id) === (versions.get(id) ?? 0)) patch(id, { error: true, loading: false }) })
    } finally {
      if (cacheController === controller) cacheController = undefined
      if (!disposed && generation === revision) {
        ids.forEach(id => { if (!visible.has(id) && !states.value[id]?.result) cached.delete(id) })
        scheduleCache()
      }
    }
  }
  function scheduleCache() {
    if (cacheTimer || cacheController || disposed || !active.value || document.visibilityState === 'hidden') return
    if ([...visible].some(id => canRun(id) && !cached.has(id))) cacheTimer = setTimeout(() => { void loadCache() }, 60)
  }
  function loadMissingSchedules() {
    for (const id of visible) {
      const result = states.value[id]?.result
      if (result?.enabled && result.configured && !schedules.has(id) && !states.value[id]?.error) enqueue(id, 'config')
    }
  }
  function tick() {
    for (const id of visible) {
      const config = schedules.get(id)
      const result = states.value[id]?.result
      if (!canRun(id) || !config?.enabled || !result?.enabled || !result.configured || !Number.isSafeInteger(config.interval_minutes) || config.interval_minutes < 5) continue
      const lastUpdated = result.updated_at ? Date.parse(result.updated_at) : 0
      const lastAttempt = Math.max(attempts.get(id) ?? 0, Number.isFinite(lastUpdated) ? lastUpdated : 0)
      if (Date.now() - lastAttempt >= config.interval_minutes * 60000) enqueue(id, 'query')
    }
  }
  function setVisible(id: number, value: boolean) {
    if (value && eligible.value.has(id)) { visible.add(id); scheduleCache(); loadMissingSchedules() }
    else {
      visible.delete(id)
      queue.delete(id)
      controllers.get(id)?.abort()
      controllers.delete(id)
      patch(id, { loading: false })
    }
  }
  function refresh(id: number) {
    if (!canRun(id)) return
    const result = states.value[id]?.result
    if (!result) { cached.delete(id); scheduleCache(); return }
    if (result.enabled && result.configured) enqueue(id, 'query', true)
  }
  function configSaved(id: number, config: Schedule) {
    versions.set(id, (versions.get(id) ?? 0) + 1)
    controllers.get(id)?.abort()
    controllers.delete(id)
    queue.delete(id)
    schedules.set(id, { enabled: config.enabled, interval_minutes: config.interval_minutes })
    attempts.set(id, Date.now())
    // Saving is not testing: invalidate the old snapshot without querying the upstream.
    patch(id, { result: { enabled: config.enabled, configured: true, unit: '' }, error: false, loading: false })
    cached.add(id)
  }
  watch(pageKey, () => {
    stopRequests()
    states.value = {}
    cached.clear()
    schedules.clear()
    attempts.clear()
    versions.clear()
    // Cells re-report intersections after page rows change.
    visible.clear()
  })
  watch(() => [...eligible.value.keys()].join(','), () => {
    for (const id of visible) if (!eligible.value.has(id)) setVisible(id, false)
    for (const id of Object.keys(states.value).map(Number)) if (!eligible.value.has(id)) {
      delete states.value[id]; cached.delete(id); schedules.delete(id); attempts.delete(id); versions.delete(id)
    }
    scheduleCache()
  })
  watch(active, enabled => {
    if (!enabled) {
      stopRequests()
      for (const id of visible) if (!states.value[id]?.result) cached.delete(id)
    } else { scheduleCache(); loadMissingSchedules() }
  })
  function visibilityChanged() {
    if (document.visibilityState === 'hidden') {
      stopRequests()
      for (const id of visible) if (!states.value[id]?.result) cached.delete(id)
    } else { scheduleCache(); loadMissingSchedules(); tick() }
  }
  onMounted(() => {
    document.addEventListener('visibilitychange', visibilityChanged)
    tickTimer = setInterval(tick, 15000)
  })
  onUnmounted(() => {
    disposed = true
    stopRequests()
    if (tickTimer) clearInterval(tickTimer)
    document.removeEventListener('visibilitychange', visibilityChanged)
  })
  return { states, setVisible, refresh, configSaved }
}
