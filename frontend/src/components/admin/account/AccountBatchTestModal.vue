<template>
  <BaseDialog
    :show="show"
    :title="t('admin.accounts.batchTest.title')"
    width="wide"
    @close="handleClose"
  >
    <div class="batch-test">
      <div class="batch-test-summary">
        <span>{{ t('admin.accounts.batchTest.selectedAccounts', { count: accounts.length }) }}</span>
        <span class="batch-test-dot">·</span>
        <span>{{ jobCountLabel }}</span>
      </div>

      <div class="batch-test-scope" role="tablist" :aria-label="t('admin.accounts.batchTest.scope')">
        <button
          type="button"
          role="tab"
          :class="{ active: scope === 'all' }"
          :aria-selected="scope === 'all'"
          :disabled="running"
          @click="scope = 'all'"
        >
          {{ t('admin.accounts.batchTest.scopeAll') }}
        </button>
        <button
          type="button"
          role="tab"
          data-test="batch-test-scope-selected"
          :class="{ active: scope === 'selected' }"
          :aria-selected="scope === 'selected'"
          :disabled="running"
          @click="scope = 'selected'"
        >
          {{ t('admin.accounts.batchTest.scopeSelected') }}
        </button>
      </div>

      <div v-if="scope === 'selected'" class="batch-test-picker">
        <input
          v-model="modelQuery"
          type="search"
          class="batch-test-search"
          :placeholder="t('admin.accounts.batchTest.searchModels')"
          :disabled="running"
        />
        <div class="batch-test-picker-actions">
          <button type="button" class="batch-test-text-btn" :disabled="running" @click="selectVisibleModels">
            {{ t('admin.accounts.batchTest.selectVisible') }}
          </button>
          <button type="button" class="batch-test-text-btn" :disabled="running" @click="selectedModelIds = []">
            {{ t('admin.accounts.batchTest.clearModels') }}
          </button>
        </div>
        <div v-if="catalogLoading && catalogModels.length === 0" class="batch-test-empty">
          {{ t('admin.accounts.batchTest.loadingCatalog') }}
        </div>
        <div v-else-if="visibleCatalogModels.length === 0" class="batch-test-empty">
          {{ t('admin.accounts.batchTest.noModels') }}
        </div>
        <div v-else class="batch-test-model-list">
          <label
            v-for="model in visibleCatalogModels"
            :key="model.id"
            class="batch-test-model"
          >
            <input
              type="checkbox"
              :value="model.id"
              :checked="selectedModelIds.includes(model.id)"
              :disabled="running"
              :data-test="`batch-test-model-${model.id}`"
              @change="onModelCheck(model.id, $event)"
            />
            <span class="batch-test-model-id">{{ model.id }}</span>
            <span v-if="model.display_name && model.display_name !== model.id" class="batch-test-model-name">
              {{ model.display_name }}
            </span>
          </label>
        </div>
      </div>

      <div class="batch-test-options">
        <label class="batch-test-option">
          <span>{{ t('admin.accounts.batchTest.concurrency') }}</span>
          <input
            v-model.number="concurrency"
            type="number"
            min="1"
            max="5"
            :disabled="running"
            @change="clampConcurrency"
          />
        </label>
        <label class="batch-test-option">
          <input v-model="includeMedia" type="checkbox" :disabled="running" />
          <span>{{ t('admin.accounts.batchTest.includeMedia') }}</span>
        </label>
      </div>
      <p class="batch-test-hint">{{ t('admin.accounts.batchTest.includeMediaHint') }}</p>

      <div class="batch-test-progress" :aria-hidden="rows.length === 0">
        <div class="batch-test-progress-bar">
          <span class="is-success" :style="{ width: progressSuccessPct }" />
          <span class="is-failed" :style="{ width: progressFailedPct }" />
        </div>
        <div class="batch-test-progress-meta">
          <span>{{ summaryLabel }}</span>
          <button
            v-if="rows.length > 0"
            type="button"
            class="batch-test-text-btn"
            @click="onlyFailed = !onlyFailed"
          >
            {{ onlyFailed ? t('admin.accounts.batchTest.showAll') : t('admin.accounts.batchTest.onlyFailed') }}
          </button>
        </div>
      </div>

      <div class="batch-test-table-wrap">
        <table v-if="visibleRows.length > 0" class="batch-test-table">
          <thead>
            <tr>
              <th>{{ t('admin.accounts.batchTest.account') }}</th>
              <th>{{ t('admin.accounts.batchTest.model') }}</th>
              <th>{{ t('admin.accounts.batchTest.status') }}</th>
              <th>{{ t('admin.accounts.batchTest.duration') }}</th>
              <th>{{ t('admin.accounts.batchTest.detail') }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="row in visibleRows" :key="row.key" :class="`is-${row.status}`">
              <td>{{ row.accountName }}</td>
              <td class="mono">{{ row.modelId }}</td>
              <td>
                <span class="batch-test-status" :class="`is-${row.status}`">{{ statusLabel(row.status) }}</span>
              </td>
              <td class="mono">{{ formatDuration(row.durationMs) }}</td>
              <td>{{ row.message }}</td>
            </tr>
          </tbody>
        </table>
        <div v-else-if="running" class="batch-test-empty">{{ t('admin.accounts.batchTest.loadingCatalog') }}</div>
      </div>
    </div>

    <template #footer>
      <div class="batch-test-footer">
        <button type="button" class="btn btn-secondary" @click="handleClose">
          {{ t('common.close') }}
        </button>
        <div class="batch-test-footer-actions">
          <button
            v-if="running"
            type="button"
            class="btn btn-warning"
            @click="stopRun"
          >
            {{ t('admin.accounts.batchTest.stop') }}
          </button>
          <button
            type="button"
            class="btn btn-primary"
            data-test="batch-test-start"
            :disabled="!canStart"
            @click="startRun"
          >
            {{ startLabel }}
          </button>
        </div>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import { adminAPI } from '@/api/admin'
import type { ClaudeModel } from '@/types'
import {
  buildAccountModelTestBody,
  groupJobsByAccount,
  isMediaHeavyModel,
  runWithConcurrency,
  streamAccountModelTest,
  type BatchTestAccount
} from '@/utils/accountModelTest'

type TestScope = 'all' | 'selected'
type RowStatus = 'queued' | 'running' | 'success' | 'failed' | 'skipped'

interface BatchTestRow {
  key: string
  accountId: number
  accountName: string
  platform?: string
  modelId: string
  status: RowStatus
  durationMs?: number
  message: string
}

const props = defineProps<{
  show: boolean
  accounts: BatchTestAccount[]
}>()

const emit = defineEmits<{
  (e: 'close'): void
}>()

const { t } = useI18n()

const scope = ref<TestScope>('all')
const modelQuery = ref('')
const selectedModelIds = ref<string[]>([])
const includeMedia = ref(false)
const concurrency = ref(2)
const onlyFailed = ref(false)
const running = ref(false)
const catalogLoading = ref(false)
const rows = ref<BatchTestRow[]>([])
const catalogByAccount = ref<Record<number, ClaudeModel[]>>({})
const catalogErrors = ref<Record<number, string>>({})

let abortController: AbortController | null = null
let loadVersion = 0
const catalogLoads = new Map<number, Promise<ClaudeModel[]>>()

const catalogModels = computed(() => {
  const seen = new Map<string, ClaudeModel>()
  for (const models of Object.values(catalogByAccount.value)) {
    for (const model of models) {
      if (!seen.has(model.id)) seen.set(model.id, model)
    }
  }
  return [...seen.values()].sort((a, b) => a.id.localeCompare(b.id))
})

const visibleCatalogModels = computed(() => {
  const query = modelQuery.value.trim().toLowerCase()
  return catalogModels.value.filter((model) => {
    if (!includeMedia.value && isMediaHeavyModel(model.id)) return false
    if (!query) return true
    return model.id.toLowerCase().includes(query) || model.display_name?.toLowerCase().includes(query)
  })
})

const estimatedJobs = computed(() => {
  if (props.accounts.length === 0) return 0
  if (scope.value === 'selected') {
    return props.accounts.length * selectedModelIds.value.length
  }
  const catalogs = Object.values(catalogByAccount.value)
  if (catalogs.length === 0) return 0
  const total = catalogs.reduce((sum, models) => {
    return sum + models.filter((model) => includeMedia.value || !isMediaHeavyModel(model.id)).length
  }, 0)
  const avg = Math.max(1, Math.round(total / catalogs.length))
  return props.accounts.length * avg
})

const jobCountLabel = computed(() => {
  if (scope.value === 'selected') {
    return t('admin.accounts.batchTest.jobCount', {
      accounts: props.accounts.length,
      models: selectedModelIds.value.length,
      jobs: estimatedJobs.value
    })
  }
  return t('admin.accounts.batchTest.jobCountApprox', {
    accounts: props.accounts.length,
    jobs: estimatedJobs.value
  })
})

const counts = computed(() => {
  const next = { queued: 0, running: 0, success: 0, failed: 0, skipped: 0 }
  for (const row of rows.value) next[row.status] += 1
  return next
})

const summaryLabel = computed(() =>
  t('admin.accounts.batchTest.summary', {
    success: counts.value.success,
    failed: counts.value.failed,
    running: counts.value.running,
    queued: counts.value.queued
  })
)

const totalCount = computed(() => rows.value.length)
const progressSuccessPct = computed(() => percent(counts.value.success, totalCount.value))
const progressFailedPct = computed(() => percent(counts.value.failed + counts.value.skipped, totalCount.value))

const visibleRows = computed(() => {
  if (!onlyFailed.value) return rows.value
  return rows.value.filter((row) => row.status === 'failed')
})

const canStart = computed(() => {
  if (running.value || props.accounts.length === 0) return false
  if (scope.value === 'selected' && selectedModelIds.value.length === 0) return false
  return true
})

const startLabel = computed(() => {
  if (running.value) return t('admin.accounts.testing')
  if (scope.value === 'selected') {
    return t('admin.accounts.batchTest.start', {
      accounts: props.accounts.length,
      models: selectedModelIds.value.length,
      jobs: estimatedJobs.value
    })
  }
  return t('admin.accounts.batchTest.startAll', {
    accounts: props.accounts.length,
    jobs: estimatedJobs.value
  })
})



const percent = (value: number, total: number) => {
  if (!total) return '0%'
  return `${Math.round((value / total) * 1000) / 10}%`
}

const clampConcurrency = () => {
  const next = Number(concurrency.value)
  concurrency.value = Number.isFinite(next) ? Math.min(5, Math.max(1, Math.round(next))) : 2
}

const statusLabel = (status: RowStatus) => t(`admin.accounts.batchTest.${status}`)

const formatDuration = (ms?: number) => {
  if (ms === undefined) return ''
  if (ms < 1000) return `${ms}ms`
  return `${(ms / 1000).toFixed(1)}s`
}

const toggleModel = (id: string, checked: boolean) => {
  if (checked) {
    if (!selectedModelIds.value.includes(id)) selectedModelIds.value = [...selectedModelIds.value, id]
    return
  }
  selectedModelIds.value = selectedModelIds.value.filter((item) => item !== id)
}

const onModelCheck = (id: string, event: Event) => {
  const target = event.target as HTMLInputElement | null
  toggleModel(id, Boolean(target?.checked))
}

const selectVisibleModels = () => {
  const next = new Set(selectedModelIds.value)
  for (const model of visibleCatalogModels.value) next.add(model.id)
  selectedModelIds.value = [...next]
}

const resetForm = () => {
  scope.value = 'all'
  modelQuery.value = ''
  selectedModelIds.value = []
  includeMedia.value = false
  concurrency.value = 2
  onlyFailed.value = false
  rows.value = []
  catalogByAccount.value = {}
  catalogErrors.value = {}
  catalogLoads.clear()
}

const handleClose = () => {
  stopRun()
  emit('close')
}

const stopRun = () => {
  if (abortController) {
    abortController.abort()
    abortController = null
  }
  running.value = false
}

const loadAccountCatalog = async (accountId: number) => {
  if (catalogByAccount.value[accountId]) return catalogByAccount.value[accountId]
  const pending = catalogLoads.get(accountId)
  if (pending) return pending

  const request = (async () => {
    try {
      const models = await adminAPI.accounts.getAvailableModels(accountId)
      catalogByAccount.value = { ...catalogByAccount.value, [accountId]: models }
      return models
    } catch (error) {
      const message = error instanceof Error ? error.message : t('admin.accounts.batchTest.catalogFailed')
      catalogErrors.value = { ...catalogErrors.value, [accountId]: message }
      catalogByAccount.value = { ...catalogByAccount.value, [accountId]: [] }
      return []
    } finally {
      catalogLoads.delete(accountId)
    }
  })()
  catalogLoads.set(accountId, request)
  return request
}

const seedAccountIds = () => {
  const byPlatform = new Map<string, number>()
  const unknown: number[] = []
  for (const account of props.accounts) {
    if (account.platform) {
      if (!byPlatform.has(account.platform)) byPlatform.set(account.platform, account.id)
    } else {
      unknown.push(account.id)
    }
  }
  return [...byPlatform.values(), ...unknown.slice(0, 3)]
}

const loadSeedCatalogs = async () => {
  const version = ++loadVersion
  catalogLoading.value = true
  try {
    await Promise.all(seedAccountIds().map((id) => loadAccountCatalog(id)))
  } finally {
    if (version === loadVersion) catalogLoading.value = false
  }
}

const loadRemainingCatalogs = async () => {
  const missing = props.accounts.filter((account) => !catalogByAccount.value[account.id]).map((account) => account.id)
  if (missing.length === 0) return
  const controller = new AbortController()
  try {
    await runWithConcurrency(missing, 3, async (id) => {
      await loadAccountCatalog(id)
    }, controller.signal)
  } catch (error) {
    if (!(error instanceof DOMException && error.name === 'AbortError')) throw error
  }
}

watch(
  () => [props.show, props.accounts.map((account) => account.id).join(',')] as const,
  async ([show]) => {
    if (!show) {
      stopRun()
      return
    }
    resetForm()
    await loadSeedCatalogs()
    void loadRemainingCatalogs()
  },
  { immediate: true }
)

const modelsForAccount = (account: BatchTestAccount) => {
  const models = catalogByAccount.value[account.id] || []
  return models.filter((model) => includeMedia.value || !isMediaHeavyModel(model.id))
}

const buildRows = (): BatchTestRow[] => {
  const next: BatchTestRow[] = []
  for (const account of props.accounts) {
    const catalogError = catalogErrors.value[account.id]
    const available = new Set((catalogByAccount.value[account.id] || []).map((model) => model.id))
    const wanted =
      scope.value === 'selected'
        ? selectedModelIds.value
        : modelsForAccount(account).map((model) => model.id)

    if (catalogError && wanted.length === 0) {
      next.push({
        key: `${account.id}:catalog`,
        accountId: account.id,
        accountName: account.name,
        platform: account.platform,
        modelId: '-',
        status: 'failed',
        message: catalogError
      })
      continue
    }

    for (const modelId of wanted) {
      const skip = scope.value === 'selected' && !available.has(modelId)
      next.push({
        key: `${account.id}:${modelId}`,
        accountId: account.id,
        accountName: account.name,
        platform: account.platform,
        modelId,
        status: skip ? 'skipped' : 'queued',
        message: skip ? t('admin.accounts.batchTest.skippedMismatch') : ''
      })
    }
  }
  return next
}

const runRow = async (row: BatchTestRow, signal: AbortSignal) => {
  if (row.status === 'skipped') return
  row.status = 'running'
  const started = performance.now()
  let success = false
  let detail = ''
  try {
    await streamAccountModelTest({
      accountId: row.accountId,
      body: buildAccountModelTestBody({
        modelId: row.modelId,
        platform: row.platform,
        prompt: ''
      }),
      signal,
      onEvent: (event) => {
        if (event.type === 'test_complete') {
          success = Boolean(event.success)
          if (!success) detail = event.error || t('admin.accounts.testFailed')
        }
        if (event.type === 'error') {
          success = false
          detail = event.error || t('common.unknownError')
        }
      }
    })
  } catch (error) {
    if (error instanceof DOMException && error.name === 'AbortError') throw error
    success = false
    detail = error instanceof Error ? error.message : t('common.unknownError')
  }
  row.durationMs = Math.round(performance.now() - started)
  row.status = success ? 'success' : 'failed'
  row.message = success ? '' : detail
}

const startRun = async () => {
  if (!canStart.value) return
  clampConcurrency()
  running.value = true
  onlyFailed.value = false
  abortController = new AbortController()
  const signal = abortController.signal

  try {
    await loadRemainingCatalogs()
    rows.value = buildRows()
    const executable = rows.value.filter((row) => row.status === 'queued')
    if (executable.length === 0) {
      running.value = false
      return
    }
    const groups = groupJobsByAccount(executable)
    await runWithConcurrency(groups, concurrency.value, async (group) => {
      for (const row of group) {
        if (signal.aborted) throw new DOMException('Aborted', 'AbortError')
        await runRow(row, signal)
      }
    }, signal)
  } catch (error) {
    if (!(error instanceof DOMException && error.name === 'AbortError')) {
      throw error
    }
  } finally {
    running.value = false
    abortController = null
  }
}
</script>

<style scoped>
.batch-test { display: grid; gap: 12px; color: var(--signal-text); }
.batch-test-summary,
.batch-test-progress-meta,
.batch-test-footer,
.batch-test-options,
.batch-test-picker-actions {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  flex-wrap: wrap;
}
.batch-test-summary { font-size: 13px; color: var(--signal-muted); }
.batch-test-dot { opacity: 0.5; }
.batch-test-scope { display: flex; gap: 6px; }
.batch-test-scope button {
  min-height: 32px;
  padding: 0 12px;
  border: 1px solid var(--signal-line);
  background: var(--signal-surface);
  color: var(--signal-text);
  border-radius: 8px;
  font-size: 12px;
  transition: border-color 160ms ease, background 160ms ease, color 160ms ease;
}
.batch-test-scope button.active {
  background: var(--signal-accent-soft);
  border-color: var(--signal-accent);
  color: var(--signal-accent);
}
.batch-test-picker,
.batch-test-table-wrap {
  border: 1px solid var(--signal-line);
  background: var(--signal-surface);
  border-radius: 8px;
}
.batch-test-search {
  width: 100%;
  min-height: 36px;
  border: 0;
  border-bottom: 1px solid var(--signal-line);
  background: transparent;
  padding: 0 12px;
  color: var(--signal-text);
}
.batch-test-picker-actions { padding: 6px 10px; }
.batch-test-model-list { max-height: 180px; overflow: auto; }
.batch-test-model {
  display: flex;
  align-items: center;
  gap: 8px;
  min-height: 34px;
  padding: 0 12px;
  border-top: 1px solid var(--signal-line);
}
.batch-test-model:hover { background: var(--signal-hover); }
.batch-test-model-id { font-size: 12px; }
.batch-test-model-name { font-size: 11px; color: var(--signal-muted); }
.batch-test-options { font-size: 12px; }
.batch-test-option { display: flex; align-items: center; gap: 8px; }
.batch-test-option input[type='number'] {
  width: 64px;
  min-height: 32px;
  border: 1px solid var(--signal-control-line);
  border-radius: 8px;
  background: var(--signal-surface);
  color: var(--signal-text);
  padding: 0 8px;
}
.batch-test-hint { margin: 0; font-size: 12px; color: var(--signal-muted); }
.batch-test-progress-bar {
  display: flex;
  height: 6px;
  overflow: hidden;
  border-radius: 99px;
  background: var(--signal-line);
}
.batch-test-progress-bar span { display: block; height: 100%; transition: width 180ms ease; }
.batch-test-progress-bar .is-success { background: #1f9d55; }
.batch-test-progress-bar .is-failed { background: #c4473a; }
.batch-test-progress-meta { font-size: 12px; color: var(--signal-muted); }
.batch-test-text-btn {
  color: var(--signal-accent);
  font-size: 12px;
  min-height: 30px;
}
.batch-test-table { width: 100%; border-collapse: collapse; font-size: 12px; }
.batch-test-table th,
.batch-test-table td {
  padding: 8px 10px;
  border-bottom: 1px solid var(--signal-line);
  text-align: left;
  vertical-align: top;
}
.batch-test-table th { color: var(--signal-muted); font-weight: 600; }
.batch-test-table tr.is-running { background: var(--signal-accent-soft); }
.batch-test-table-wrap { max-height: 320px; overflow: auto; }
.batch-test-empty { padding: 18px 12px; text-align: center; color: var(--signal-muted); font-size: 12px; }
.batch-test-status { font-weight: 650; }
.batch-test-status.is-success { color: #1f9d55; }
.batch-test-status.is-failed { color: #c4473a; }
.batch-test-status.is-running { color: var(--signal-accent); }
.batch-test-status.is-skipped,
.batch-test-status.is-queued { color: var(--signal-muted); }
.mono { font-variant-numeric: tabular-nums; word-break: break-all; }
.batch-test-footer-actions { display: flex; gap: 8px; margin-left: auto; }
@media (max-width: 767px) {
  .batch-test-scope { display: grid; grid-template-columns: 1fr 1fr; }
  .batch-test-footer { display: grid; gap: 8px; }
  .batch-test-footer-actions { margin-left: 0; width: 100%; }
  .batch-test-footer-actions .btn,
  .batch-test-footer .btn { width: 100%; min-height: 40px; }
}
</style>
