<template>
  <div class="model-test">
    <div class="model-test-toolbar">
      <div class="model-test-toolbar-copy">
        <p class="model-test-kicker">{{ headline }}</p>
        <p class="model-test-hint">{{ t('admin.accounts.batchTest.unboundedConcurrency') }}</p>
      </div>
      <div class="model-test-toolbar-controls">
        <input
          v-model="modelQuery"
          type="search"
          class="model-test-search"
          :placeholder="t('admin.accounts.batchTest.filterModels')"
          :disabled="catalogLoading && rows.length === 0"
        />
        <label class="model-test-option">
          <input v-model="includeMedia" type="checkbox" :disabled="anyRunning" />
          <span>{{ t('admin.accounts.batchTest.includeMedia') }}</span>
        </label>
      </div>
    </div>

    <div class="model-test-actions">
      <button
        v-if="anyRunning"
        type="button"
        class="btn btn-warning"
        data-test="test-stop"
        @click="stopRun"
      >
        {{ t('admin.accounts.batchTest.stop') }}
      </button>
      <template v-else>
        <button
          type="button"
          class="btn btn-primary"
          data-test="test-all-models"
          data-batch-test-start
          :disabled="!canTestAll"
          @click="startRun('all')"
        >
          {{ testAllLabel }}
        </button>
        <button
          type="button"
          class="btn btn-secondary"
          data-test="test-selected-models"
          :disabled="!canTestSelected"
          @click="startRun('selected')"
        >
          {{ testSelectedLabel }}
        </button>
      </template>
      <button type="button" class="model-test-text-btn" :disabled="running || visibleRows.length === 0" @click="selectVisible(true)">
        {{ t('admin.accounts.batchTest.selectAll') }}
      </button>
      <button type="button" class="model-test-text-btn" :disabled="running || visibleRows.length === 0" @click="invertVisible">
        {{ t('admin.accounts.batchTest.invert') }}
      </button>
      <button
        v-if="rows.length > 0"
        type="button"
        class="model-test-text-btn"
        @click="onlyFailed = !onlyFailed"
      >
        {{ onlyFailed ? t('admin.accounts.batchTest.showAll') : t('admin.accounts.batchTest.onlyFailed') }}
      </button>
    </div>

    <div class="model-test-progress" :aria-hidden="rows.length === 0">
      <div class="model-test-progress-bar">
        <span class="is-success" :style="{ width: progressSuccessPct }" />
        <span class="is-failed" :style="{ width: progressFailedPct }" />
      </div>
      <div class="model-test-progress-meta">{{ summaryLabel }}</div>
    </div>

    <div class="model-test-table-wrap">
      <table v-if="visibleRows.length > 0" class="model-test-table">
        <thead>
          <tr>
            <th class="model-test-check">
              <input
                type="checkbox"
                :checked="allVisibleSelected"
                :indeterminate.prop="someVisibleSelected"
                :disabled="running || visibleRows.length === 0"
                :aria-label="t('admin.accounts.batchTest.selectAll')"
                @change="toggleAllVisible(($event.target as HTMLInputElement).checked)"
              />
            </th>
            <th v-if="showAccount">{{ t('admin.accounts.batchTest.account') }}</th>
            <th>{{ t('admin.accounts.batchTest.model') }}</th>
            <th>{{ t('admin.accounts.batchTest.status') }}</th>
            <th>{{ t('admin.accounts.batchTest.duration') }}</th>
            <th>{{ t('admin.accounts.batchTest.result') }}</th>
            <th class="model-test-actions-col">{{ t('admin.accounts.batchTest.action') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="row in visibleRows" :key="row.key" :class="`is-${row.status}`">
            <td class="model-test-check">
              <input
                type="checkbox"
                :checked="row.selected"
                :disabled="running"
                :data-test="selectTestId(row)"
                @change="setSelected(row, ($event.target as HTMLInputElement).checked)"
              />
            </td>
            <td v-if="showAccount">{{ row.accountName }}</td>
            <td class="mono">{{ row.modelId }}</td>
            <td>
              <span class="model-test-status" :class="`is-${row.status}`">
                {{ statusLabel(row.status) }}
              </span>
            </td>
            <td class="mono">{{ formatAccountModelTestDuration(row.durationMs) }}</td>
            <td class="model-test-result">
              <p
                class="model-test-result-text"
                :class="`is-${row.status}`"
                :data-test="`test-output-${row.accountId}-${row.modelId}`"
              >{{ resultPreview(row) }}</p>
              <div v-if="row.images.length > 0" class="model-test-media">
                <img
                  v-for="(image, index) in row.images"
                  :key="`${image.url}-${index}`"
                  :src="image.url"
                  :alt="t('admin.accounts.imagePreviewAlt', { index: index + 1 })"
                  @click="previewImageUrl = image.url"
                />
              </div>
              <audio
                v-for="(audio, index) in row.audios"
                :key="`audio-${row.key}-${index}`"
                :src="audio.url"
                :type="audio.mimeType"
                controls
              />
              <video
                v-for="(video, index) in row.videos"
                :key="`video-${row.key}-${index}`"
                :src="video.url"
                :type="video.mimeType"
                controls
              />
              <button
                v-if="canExpand(row)"
                type="button"
                class="model-test-text-btn"
                @click="row.expanded = !row.expanded"
              >
                {{ row.expanded ? t('admin.accounts.batchTest.collapse') : t('admin.accounts.batchTest.expand') }}
              </button>
              <pre v-if="row.expanded" class="model-test-result-full">{{ fullResult(row) }}</pre>
            </td>
            <td class="model-test-actions-col">
              <button
                type="button"
                class="btn btn-secondary btn-sm"
                :data-test="rowTestId(row)"
                :disabled="running || row.status === 'running' || row.catalogFailed"
                @click="testOne(row)"
              >
                {{ t('admin.accounts.batchTest.testThis') }}
              </button>
            </td>
          </tr>
        </tbody>
      </table>
      <div v-else class="model-test-empty">
        {{ catalogLoading ? t('admin.accounts.batchTest.loadingCatalog') : t('admin.accounts.batchTest.noModels') }}
      </div>
    </div>

    <Teleport to="body">
      <div
        v-if="previewImageUrl"
        class="model-test-lightbox"
        @click.self="previewImageUrl = ''"
      >
        <img :src="previewImageUrl" :alt="t('admin.accounts.imageLightboxAlt')" />
      </div>
    </Teleport>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import type { ClaudeModel } from '@/types'
import {
  applyAccountModelTestEvent,
  buildAccountModelTestBody,
  createAccountModelTestOutput,
  formatAccountModelTestDuration,
  isMediaHeavyModel,
  runWithConcurrency,
  streamAccountModelTest,
  summarizeAccountModelTestOutput,
  type AccountModelTestOutput,
  type BatchTestAccount
} from '@/utils/accountModelTest'

type RowStatus = 'idle' | 'running' | 'success' | 'failed' | 'skipped'
type TestScope = 'all' | 'selected'

interface ModelTestRow extends AccountModelTestOutput {
  key: string
  accountId: number
  accountName: string
  platform?: string
  modelId: string
  status: RowStatus
  durationMs?: number
  selected: boolean
  expanded: boolean
  catalogFailed?: boolean
}

const props = withDefaults(defineProps<{
  show: boolean
  accounts: BatchTestAccount[]
  variant?: 'single' | 'batch'
  buildBody?: (modelId: string, account: BatchTestAccount) => Record<string, unknown>
}>(), {
  variant: 'batch'
})

const { t } = useI18n()

const modelQuery = ref('')
const includeMedia = ref(props.variant === 'single')
const onlyFailed = ref(false)
const running = ref(false)
const catalogLoading = ref(false)
const rows = ref<ModelTestRow[]>([])
const catalogByAccount = ref<Record<number, ClaudeModel[]>>({})
const catalogErrors = ref<Record<number, string>>({})
const previewImageUrl = ref('')

let abortController: AbortController | null = null
let loadVersion = 0
const catalogLoads = new Map<number, Promise<ClaudeModel[]>>()

const showAccount = computed(() => props.variant !== 'single' && props.accounts.length > 1)

const headline = computed(() => {
  if (props.variant === 'single' && props.accounts[0]) {
    return t('admin.accounts.batchTest.singleHeadline', { name: props.accounts[0].name, count: rows.value.length })
  }
  return t('admin.accounts.batchTest.selectedAccounts', { count: props.accounts.length })
})

const visibleRows = computed(() => {
  const query = modelQuery.value.trim().toLowerCase()
  return rows.value.filter((row) => {
    if (onlyFailed.value && row.status !== 'failed') return false
    if (!query) return true
    return (
      row.modelId.toLowerCase().includes(query) ||
      row.accountName.toLowerCase().includes(query)
    )
  })
})

const counts = computed(() => {
  const next = { idle: 0, running: 0, success: 0, failed: 0, skipped: 0 }
  for (const row of rows.value) next[row.status] += 1
  return next
})

const summaryLabel = computed(() =>
  t('admin.accounts.batchTest.summaryBoard', {
    success: counts.value.success,
    failed: counts.value.failed,
    skipped: counts.value.skipped,
    running: counts.value.running,
    total: rows.value.length
  })
)

const progressSuccessPct = computed(() => percent(counts.value.success, rows.value.length))
const progressFailedPct = computed(() => percent(counts.value.failed + counts.value.skipped, rows.value.length))

const selectedVisible = computed(() => visibleRows.value.filter((row) => row.selected))
const allVisibleSelected = computed(
  () => visibleRows.value.length > 0 && visibleRows.value.every((row) => row.selected)
)
const someVisibleSelected = computed(
  () => selectedVisible.value.length > 0 && !allVisibleSelected.value
)

const canTestAll = computed(() => !anyRunning.value && !catalogLoading.value && jobsForScope('all').length > 0)
const canTestSelected = computed(() => !anyRunning.value && !catalogLoading.value && jobsForScope('selected').length > 0)

const anyRunning = computed(() => running.value || rows.value.some((row) => row.status === 'running'))

const testAllLabel = computed(() =>
  t('admin.accounts.batchTest.testAll', { count: jobsForScope('all').length })
)
const testSelectedLabel = computed(() =>
  t('admin.accounts.batchTest.testSelected', { count: jobsForScope('selected').length })
)

const percent = (value: number, total: number) => {
  if (!total) return '0%'
  return `${Math.round((value / total) * 1000) / 10}%`
}

const statusLabel = (status: RowStatus) => {
  if (status === 'idle') return t('admin.accounts.batchTest.notTested')
  return t(`admin.accounts.batchTest.${status}`)
}

const resultPreview = (row: ModelTestRow) => {
  if (row.status === 'idle') return '-'
  if (row.status === 'running') return summarizeAccountModelTestOutput(row) || t('admin.accounts.batchTest.running')
  if (row.status === 'skipped') return row.error || t('admin.accounts.batchTest.skipped')
  const summary = summarizeAccountModelTestOutput(row)
  if (summary) return summary
  if (row.status === 'success') return t('admin.accounts.batchTest.emptyOutput')
  return t('admin.accounts.testFailed')
}

const fullResult = (row: ModelTestRow) => [row.error, row.output.trim()].filter(Boolean).join('\n\n') || resultPreview(row)

const canExpand = (row: ModelTestRow) =>
  Boolean(row.output.trim() || row.error)

const selectTestId = (row: ModelTestRow) =>
  props.variant === 'single' ? `batch-test-model-${row.modelId}` : `test-row-select-${row.accountId}-${row.modelId}`

const rowTestId = (row: ModelTestRow) =>
  props.variant === 'single' ? `row-test-${row.modelId}` : `row-test-${row.accountId}-${row.modelId}`

const setSelected = (row: ModelTestRow, checked: boolean) => {
  row.selected = checked
}

const selectVisible = (checked: boolean) => {
  for (const row of visibleRows.value) row.selected = checked
}

const toggleAllVisible = (checked: boolean) => selectVisible(checked)

const invertVisible = () => {
  for (const row of visibleRows.value) row.selected = !row.selected
}

const resolveBody = (row: ModelTestRow) => {
  const account = props.accounts.find((item) => item.id === row.accountId) || {
    id: row.accountId,
    name: row.accountName,
    platform: row.platform
  }
  if (props.buildBody) return props.buildBody(row.modelId, account)
  return buildAccountModelTestBody({
    modelId: row.modelId,
    platform: row.platform,
    prompt: ''
  })
}

const resetForm = () => {
  modelQuery.value = ''
  includeMedia.value = props.variant === 'single'
  onlyFailed.value = false
  rows.value = []
  catalogByAccount.value = {}
  catalogErrors.value = {}
  catalogLoads.clear()
  previewImageUrl.value = ''
}

const stopRun = () => {
  if (abortController) {
    abortController.abort()
    abortController = null
  }
  running.value = false
  for (const row of rows.value) {
    if (row.status === 'running') {
      resetOutput(row)
      row.status = 'idle'
    }
  }
}

const loadAccountCatalog = async (accountId: number, version: number) => {
  if (catalogByAccount.value[accountId]) return catalogByAccount.value[accountId]
  const pending = catalogLoads.get(accountId)
  if (pending) return pending

  const request = (async () => {
    try {
      const models = await adminAPI.accounts.getAvailableModels(accountId)
      if (version !== loadVersion) return []
      catalogByAccount.value = { ...catalogByAccount.value, [accountId]: models }
      return models
    } catch (error) {
      if (version !== loadVersion) return []
      const message = error instanceof Error ? error.message : t('admin.accounts.batchTest.catalogFailed')
      catalogErrors.value = { ...catalogErrors.value, [accountId]: message }
      catalogByAccount.value = { ...catalogByAccount.value, [accountId]: [] }
      return []
    } finally {
      if (version === loadVersion) catalogLoads.delete(accountId)
    }
  })()
  catalogLoads.set(accountId, request)
  return request
}

const modelsForAccount = (account: BatchTestAccount) => {
  const models = catalogByAccount.value[account.id] || []
  return models.filter((model) => includeMedia.value || !isMediaHeavyModel(model.id))
}

const emptyRow = (account: BatchTestAccount, modelId: string, status: RowStatus, error = ''): ModelTestRow => ({
  key: `${account.id}::${modelId}`,
  accountId: account.id,
  accountName: account.name,
  platform: account.platform,
  modelId,
  status,
  selected: false,
  expanded: false,
  ...createAccountModelTestOutput(),
  error
})

const syncRowsFromCatalog = () => {
  const previous = new Map(rows.value.map((row) => [row.key, row]))
  const next: ModelTestRow[] = []
  for (const account of props.accounts) {
    const catalogError = catalogErrors.value[account.id]
    const models = modelsForAccount(account)
    if (catalogError && models.length === 0) {
      const key = `${account.id}::catalog`
      next.push(previous.get(key) || { ...emptyRow(account, '-', 'failed', catalogError), key, catalogFailed: true })
      continue
    }
    for (const model of models) {
      const key = `${account.id}::${model.id}`
      next.push(previous.get(key) || emptyRow(account, model.id, 'idle'))
    }
  }
  rows.value = next
}

const loadCatalogs = async (version: number) => {
  catalogLoading.value = true
  try {
    await Promise.all(props.accounts.map((account) => loadAccountCatalog(account.id, version)))
    if (version === loadVersion) syncRowsFromCatalog()
  } finally {
    if (version === loadVersion) catalogLoading.value = false
  }
}

watch(includeMedia, () => {
  if (props.show && !anyRunning.value) syncRowsFromCatalog()
})

const resetOutput = (row: ModelTestRow) => {
  const fresh = createAccountModelTestOutput()
  row.output = fresh.output
  row.success = undefined
  row.error = ''
  row.images = []
  row.audios = []
  row.videos = []
  row.durationMs = undefined
  row.expanded = false
}

const runRow = async (row: ModelTestRow, signal: AbortSignal) => {
  if (signal.aborted || row.status === 'skipped' || row.catalogFailed) return
  resetOutput(row)
  row.status = 'running'
  const started = performance.now()
  try {
    await streamAccountModelTest({
      accountId: row.accountId,
      body: resolveBody(row),
      signal,
      onEvent: (event) => {
        if (!signal.aborted) applyAccountModelTestEvent(row, event)
      }
    })
    if (signal.aborted) return
    row.durationMs = Math.round(performance.now() - started)
    row.status = row.success === true ? 'success' : 'failed'
    if (row.status === 'failed' && !row.error) {
      row.error = t('admin.accounts.testFailed')
    }
  } catch (error) {
    if (signal.aborted) return
    row.durationMs = Math.round(performance.now() - started)
    row.status = 'failed'
    row.success = false
    row.error = error instanceof Error ? error.message : t('common.unknownError')
  }
}

const jobsForScope = (scope: TestScope) => {
  return rows.value.filter((row) => !row.catalogFailed && row.status !== 'skipped' && (scope === 'all' || row.selected))
}

const startRun = async (scope: TestScope) => {
  if (!props.show || anyRunning.value || catalogLoading.value) return
  const jobs = jobsForScope(scope)
  if (jobs.length === 0) return
  running.value = true
  onlyFailed.value = false
  abortController = new AbortController()
  const signal = abortController.signal
  try {
    await runWithConcurrency(jobs, jobs.length, async (row) => {
      await runRow(row, signal)
    }, signal)
  } catch (error) {
    if (!(error instanceof DOMException && error.name === 'AbortError')) throw error
  } finally {
    if (abortController?.signal === signal) {
      running.value = false
      abortController = null
    }
  }
}

const testOne = async (row: ModelTestRow) => {
  if (!props.show || running.value || row.status === 'running' || row.catalogFailed) return
  if (!abortController) abortController = new AbortController()
  const signal = abortController.signal
  try {
    await runRow(row, signal)
  } catch (error) {
    if (!(error instanceof DOMException && error.name === 'AbortError')) throw error
  } finally {
    if (abortController?.signal === signal && !running.value && !rows.value.some((item) => item.status === 'running')) {
      abortController = null
    }
  }
}

watch(
  () => [props.show, props.accounts.map((account) => account.id + ':' + account.platform + ':' + account.type).join(',')] as const,
  async ([show]) => {
    const version = ++loadVersion
    stopRun()
    resetForm()
    catalogLoading.value = false
    if (show) await loadCatalogs(version)
  },
  { immediate: true }
)

onBeforeUnmount(() => {
  loadVersion += 1
  stopRun()
})

defineExpose({
  startRun,
  testOne,
  stopRun
})
</script>

<style scoped>
.model-test { display: grid; min-width: 0; gap: 12px; color: var(--signal-text); }
.model-test-toolbar,
.model-test-actions,
.model-test-progress-meta,
.model-test-toolbar-controls {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  flex-wrap: wrap;
}
.model-test-toolbar { align-items: flex-start; }
.model-test-kicker { margin: 0; font-size: 13px; font-weight: 650; }
.model-test-hint,
.model-test-progress-meta { margin: 0; font-size: 12px; color: var(--signal-muted); }
.model-test-search {
  min-height: 36px;
  min-width: 180px;
  border: 1px solid var(--signal-line);
  border-radius: 8px;
  background: var(--signal-surface);
  color: var(--signal-text);
  padding: 0 12px;
}
.model-test-option { display: flex; align-items: center; gap: 8px; font-size: 12px; }
.model-test-text-btn {
  color: var(--signal-accent);
  font-size: 12px;
  min-height: 30px;
}
.model-test-progress-bar {
  display: flex;
  height: 6px;
  overflow: hidden;
  border-radius: 99px;
  background: var(--signal-line);
}
.model-test-progress-bar span { display: block; height: 100%; transition: width 180ms ease; }
.model-test-progress-bar .is-success { background: #1f9d55; }
.model-test-progress-bar .is-failed { background: #c4473a; }
.model-test-table-wrap {
  min-width: 0;
  border: 1px solid var(--signal-line);
  background: var(--signal-surface);
  border-radius: 8px;
  max-height: min(52vh, 560px);
  overflow: auto;
}
.model-test-table { width: 100%; min-width: 680px; border-collapse: collapse; font-size: 12px; }
.model-test-table th,
.model-test-table td {
  padding: 8px 10px;
  border-bottom: 1px solid var(--signal-line);
  text-align: left;
  vertical-align: top;
}
.model-test-table th { color: var(--signal-muted); font-weight: 600; position: sticky; top: 0; background: var(--signal-surface); z-index: 1; }
.model-test-table tr.is-running { background: var(--signal-accent-soft); }
.model-test-table tr.is-success { background: color-mix(in srgb, #1f9d55 8%, transparent); }
.model-test-table tr.is-failed { background: color-mix(in srgb, #c4473a 8%, transparent); }
.model-test-check { width: 36px; }
.model-test-actions-col { width: 72px; white-space: nowrap; }
.model-test-status { font-weight: 650; }
.model-test-status.is-success { color: #1f9d55; }
.model-test-status.is-failed { color: #c4473a; }
.model-test-status.is-running { color: var(--signal-accent); }
.model-test-status.is-skipped,
.model-test-status.is-idle { color: var(--signal-muted); }
.model-test-result { min-width: 220px; max-width: 420px; }
.model-test-result-text {
  margin: 0;
  line-height: 1.45;
  display: -webkit-box;
  -webkit-line-clamp: 3;
  -webkit-box-orient: vertical;
  overflow: hidden;
  word-break: break-word;
}
.model-test-result-text.is-failed { color: #c4473a; }
.model-test-result-text.is-success { color: var(--signal-text); }
.model-test-result-full {
  margin: 8px 0 0;
  max-height: 180px;
  overflow: auto;
  white-space: pre-wrap;
  word-break: break-word;
  background: var(--signal-bg);
  border: 1px solid var(--signal-line);
  border-radius: 8px;
  padding: 8px;
  font-size: 11px;
}
.model-test-media { display: flex; flex-wrap: wrap; gap: 8px; margin-top: 8px; }
.model-test-media img,
.model-test-result video {
  max-height: 120px;
  max-width: 180px;
  object-fit: contain;
  border-radius: 8px;
  border: 1px solid var(--signal-line);
  background: var(--signal-bg);
  cursor: zoom-in;
}
.model-test-result audio,
.model-test-result video { width: 100%; margin-top: 8px; }
.model-test-empty { padding: 18px 12px; text-align: center; color: var(--signal-muted); font-size: 12px; }
.mono { font-variant-numeric: tabular-nums; word-break: break-all; }
.model-test-lightbox {
  position: fixed;
  inset: 0;
  z-index: 80;
  display: flex;
  align-items: center;
  justify-content: center;
  background: rgba(0, 0, 0, 0.78);
  padding: 16px;
}
.model-test-lightbox img { max-height: 90vh; max-width: 90vw; border-radius: 8px; }
@media (max-width: 767px) {
  .model-test-actions .btn { min-height: 40px; }
  .model-test-search { width: 100%; }
}
</style>
