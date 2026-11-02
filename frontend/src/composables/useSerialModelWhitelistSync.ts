/**
 * Serial "pull upstream models → overwrite the model whitelist" runner.
 *
 * Uses only endpoints that already exist:
 * - `POST /admin/accounts/:id/models/sync-upstream` (read-only catalog fetch)
 * - `PUT  /admin/accounts/:id` with `credentials.model_mapping` (merge semantics,
 *   so unrelated credentials such as the api key are preserved)
 *
 * Requests are deliberately serial: one account at a time keeps upstream rate
 * limits and admin-API load predictable no matter how many accounts are selected.
 * An empty upstream catalog never overwrites an existing whitelist.
 */
import { ref } from 'vue'
import { syncUpstreamModels, update } from '@/api/admin/accounts'

export interface SerialModelSyncState {
  running: boolean
  total: number
  completed: number
  succeeded: number
  failed: number
  currentId: number | null
  failedIds: number[]
}

export interface SerialModelSyncFailure {
  accountId: number
  reason: string
}

/** Whitelist persistence uses identity mappings (`model → model`). */
export function buildIdentityModelMapping(models: string[]): Record<string, string> {
  const mapping: Record<string, string> = {}
  for (const model of models) {
    const trimmed = model.trim()
    if (trimmed) mapping[trimmed] = trimmed
  }
  return mapping
}

function normalizeModels(models: unknown): string[] {
  if (!Array.isArray(models)) return []
  const seen = new Set<string>()
  const out: string[] = []
  for (const value of models) {
    if (typeof value !== 'string') continue
    const trimmed = value.trim()
    if (!trimmed || seen.has(trimmed)) continue
    seen.add(trimmed)
    out.push(trimmed)
  }
  return out
}

function emptyState(): SerialModelSyncState {
  return {
    running: false,
    total: 0,
    completed: 0,
    succeeded: 0,
    failed: 0,
    currentId: null,
    failedIds: [],
  }
}

export function useSerialModelWhitelistSync() {
  const state = ref<SerialModelSyncState>(emptyState())
  const failures = ref<SerialModelSyncFailure[]>([])
  let cancelled = false

  function cancel() {
    cancelled = true
  }

  /** Runs the whole list; resolves with the final state (never throws). */
  async function start(accountIds: number[]): Promise<SerialModelSyncState> {
    if (state.value.running) return state.value
    cancelled = false
    failures.value = []
    state.value = { ...emptyState(), running: true, total: accountIds.length }

    for (const accountId of accountIds) {
      if (cancelled) break
      state.value.currentId = accountId
      try {
        const catalog = await syncUpstreamModels(accountId)
        const models = normalizeModels(catalog?.models)
        if (models.length === 0) {
          throw new Error('empty upstream model list')
        }
        await update(accountId, { credentials: { model_mapping: buildIdentityModelMapping(models) } })
        state.value.succeeded += 1
      } catch (error) {
        const reason = error instanceof Error ? error.message : String(error ?? 'unknown error')
        state.value.failed += 1
        state.value.failedIds = [...state.value.failedIds, accountId]
        failures.value = [...failures.value, { accountId, reason }]
      } finally {
        state.value.completed += 1
      }
    }

    state.value.currentId = null
    state.value.running = false
    return state.value
  }

  function reset() {
    cancelled = false
    state.value = emptyState()
    failures.value = []
  }

  return { state, failures, start, cancel, reset }
}