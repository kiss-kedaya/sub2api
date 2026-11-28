<template>
  <div class="model-test">
    <div class="model-test-toolbar">
      <div class="model-test-toolbar-copy">
        <p class="model-test-kicker">{{ headline }}</p>
      </div>
      <div class="model-test-toolbar-controls">
        <label class="model-test-search-wrap">
          <Icon name="search" size="sm" aria-hidden="true" />
          <UInput
            v-model="modelQuery"
            type="search"
            icon="i-lucide-search"
            class="model-test-search"
            :placeholder="t('admin.accounts.batchTest.filterModels')"
            :aria-label="t('admin.accounts.batchTest.filterModels')"
            :disabled="catalogLoading && rows.length === 0"
          />
        </label>
        <UCheckbox v-model="includeMedia" class="model-test-option" :disabled="anyRunning" :label="t('admin.accounts.batchTest.includeMedia')" />
        <div v-if="variant !== 'single'" class="model-test-concurrency">
          <label for="test-concurrency">{{ t('admin.accounts.batchTest.concurrencyLimit') }}</label>
          <Select
            id="test-concurrency"
            v-model="concurrency"
            :options="concurrencyItems"
            :searchable="false"
            size="sm"
            class="w-20 tabular-nums"
            :disabled="anyRunning"
            :aria-label="t('admin.accounts.batchTest.concurrencyLimit')"
            :title="t('admin.accounts.batchTest.concurrencyHint')"
            data-test="test-concurrency"
          />
        </div>
      </div>
    </div>

    <div class="model-test-actions">
      <UButton v-if="anyRunning" color="error" variant="subtle" icon="i-lucide-square" data-test="test-stop" :label="t('admin.accounts.batchTest.stop')" @click="stopRun" />
      <template v-else>
        <UButton icon="i-lucide-play" data-test="test-all-models" data-batch-test-start :disabled="!canTestAll" :label="testAllLabel" @click="startRun('all')" />
        <UButton color="neutral" variant="outline" data-test="test-selected-models" :disabled="!canTestSelected" :label="testSelectedLabel" @click="startRun('selected')" />
      </template>
      <div class="model-test-selection">
        <UButton color="neutral" variant="ghost" size="xs" :disabled="running || visibleRows.length === 0" :label="t('admin.accounts.batchTest.selectAll')" @click="selectVisible(true)" />
        <UButton color="neutral" variant="ghost" size="xs" :disabled="running || visibleRows.length === 0" :label="t('admin.accounts.batchTest.invert')" @click="invertVisible" />
      </div>
      <UButton v-if="rows.length > 0" color="neutral" :variant="onlyFailed ? 'soft' : 'ghost'" size="xs" :aria-pressed="onlyFailed" class="model-test-filter" @click="onlyFailed = !onlyFailed">{{ onlyFailed ? t('admin.accounts.batchTest.showAll') : t('admin.accounts.batchTest.onlyFailed') }}</UButton>
    </div>

    <div v-if="counts.success + counts.failed + counts.running + counts.skipped > 0" class="model-test-progress">
      <div
        class="model-test-progress-bar"
        role="progressbar"
        :aria-valuemin="0"
        :aria-valuemax="rows.length"
        :aria-valuenow="completedCount"
        :aria-label="summaryLabel"
        data-test="test-progress"
      >
        <span class="is-success" :style="{ transform: `scaleX(${progressSuccessRatio})` }" />
        <span class="is-failed" :style="{ transform: `translateX(${progressSuccessRatio * 100}%) scaleX(${progressFailedRatio})` }" />
      </div>
      <div class="model-test-progress-meta" role="status" :aria-label="summaryLabel">
        <span class="model-test-counter is-success"><i />{{ counts.success }} {{ t('admin.accounts.batchTest.success') }}</span>
        <span class="model-test-counter is-failed"><i />{{ counts.failed }} {{ t('admin.accounts.batchTest.failed') }}</span>
        <span v-if="counts.running" class="model-test-counter is-running"><i />{{ counts.running }} {{ t('admin.accounts.batchTest.running') }}</span>
        <span v-if="counts.skipped" class="model-test-counter"><i />{{ counts.skipped }} {{ t('admin.accounts.batchTest.skipped') }}</span>
        <span class="model-test-completed" data-test="test-progress-count">{{ completedCount }} / {{ rows.length }} · {{ progressPct }}%</span>
      </div>
    </div>

    <div class="model-test-table-wrap">
      <UTable
        v-if="visibleRows.length > 0"
        :data="visibleRows"
        :columns="tableColumns"
        :meta="tableMeta"
        class="model-test-table"
        :ui="tableUi"
      >
        <template #select-header>
          <UCheckbox
            :model-value="someVisibleSelected ? 'indeterminate' : allVisibleSelected"
            :disabled="running || visibleRows.length === 0"
            :aria-label="t('admin.accounts.batchTest.selectAll')"
            @update:model-value="toggleAllVisible($event === true)"
          />
        </template>
        <template #select-cell="{ row }">
          <UCheckbox
            :model-value="row.original.selected"
            :disabled="running"
            :data-test="selectTestId(row.original)"
            @update:model-value="setSelected(row.original, $event === true)"
          />
        </template>
        <template #account-cell="{ row }">{{ row.original.accountName }}</template>
        <template #model-cell="{ row }"><span class="font-mono text-xs">{{ row.original.modelId }}</span></template>
        <template #status-cell="{ row }">
          <span class="model-test-status" :class="`is-${row.original.status}`">{{ statusLabel(row.original.status) }}</span>
        </template>
        <template #duration-cell="{ row }"><span class="font-mono text-xs tabular-nums">{{ formatAccountModelTestDuration(row.original.durationMs) }}</span></template>
        <template #result-cell="{ row }">
          <div class="model-test-result">
              <p
                class="model-test-result-text"
                :class="`is-${row.original.status}`"
                :data-test="`test-output-${row.original.accountId}-${row.original.modelId}`"
              >{{ resultPreview(row.original) }}</p>
              <div
                v-if="failureKind(row.original)"
                class="model-test-failure"
                :class="`is-${failureKind(row.original)}`"
                :data-test="`test-failure-${row.original.accountId}-${row.original.modelId}`"
              >
                <span class="model-test-failure-badge">{{ failureLabel(row.original) }}</span>
                <p class="model-test-failure-hint">{{ failureHint(row.original) }}</p>
                <UButton
                  color="neutral"
                  variant="link"
                  size="xs"
                  class="p-0 model-test-failure-action"
                  :data-test="`test-failure-fix-${row.original.accountId}-${row.original.modelId}`"
                  :label="t('admin.accounts.batchTest.failure.fixAccount')"
                  @click="goToAccount(row.original)"
                />
              </div>
              <div v-if="row.original.images.length > 0" class="model-test-media">
                <img
                  v-for="(image, index) in row.original.images"
                  :key="`${image.url}-${index}`"
                  :src="image.url"
                  :alt="t('admin.accounts.imagePreviewAlt', { index: index + 1 })"
                  @click="previewImageUrl = image.url"
                />
              </div>
              <audio
                v-for="(audio, index) in row.original.audios"
                :key="`audio-${row.original.key}-${index}`"
                :src="audio.url"
                :type="audio.mimeType"
                controls
              />
              <video
                v-for="(video, index) in row.original.videos"
                :key="`video-${row.original.key}-${index}`"
                :src="video.url"
                :type="video.mimeType"
                controls
              />
              <UButton v-if="canExpand(row.original)" color="neutral" variant="link" size="xs" class="p-0" :aria-expanded="row.original.expanded" @click="row.original.expanded = !row.original.expanded">{{ row.original.expanded ? t('admin.accounts.batchTest.collapse') : t('admin.accounts.batchTest.expand') }}</UButton>
              <div class="model-test-disclosure" :class="{ 'is-open': row.original.expanded }" :inert="row.original.expanded ? undefined : true">
                <div><pre class="model-test-result-full">{{ row.original.expanded ? fullResult(row.original) : '' }}</pre></div>
              </div>
            </div>
        </template>
        <template #actions-cell="{ row }">
          <UButton
            v-if="row.original.status === 'running'"
            color="error"
            variant="subtle"
            size="xs"
            :data-test="cancelRowTestId(row.original)"
            :label="t('admin.accounts.batchTest.cancelRow')"
            @click="cancelRow(row.original)"
          />
          <UButton
            v-else
            color="neutral"
            variant="outline"
            size="xs"
            :data-test="rowTestId(row.original)"
            :disabled="running || row.original.catalogFailed"
            :label="t('admin.accounts.batchTest.testThis')"
            @click="testOne(row.original)"
          />
        </template>
      </UTable>
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
import { useRouter } from 'vue-router'
import type { TableColumn } from '@nuxt/ui'
import { adminAPI } from '@/api/admin'
import Select from '@/components/common/Select.vue'
import Icon from '@/components/icons/Icon.vue'
import type { ClaudeModel } from '@/types'
import {
  ACCOUNT_MODEL_TEST_CONCURRENCY_OPTIONS,
  AccountModelTestHTTPError,
  DEFAULT_ACCOUNT_MODEL_TEST_CONCURRENCY,
  applyAccountModelTestEvent,
  buildAccountModelTestBody,
  buildAccountEditLocation,
  classifyAccountModelTestFailure,
  createAccountModelTestOutput,
  formatAccountModelTestDuration,
  isMediaHeavyModel,
  runWithConcurrency,
  streamAccountModelTest,
  summarizeAccountModelTestOutput,
  type AccountModelTestFailureKind,
  type AccountModelTestOutput,
  type BatchTestAccount
} from '@/utils/accountModelTest'

type RowStatus = 'idle' | 'running' | 'success' | 'failed' | 'skipped'
type TestScope = 'all' | 'selected'

const CONCURRENCY_STORAGE_KEY = 'sub2api.accountModelTest.concurrency'

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
  /** HTTP 层失败的上游状态码，仅用于失败原因归类，不写进测试协议。 */
  failureStatus?: number
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
const router = useRouter()

const loadConcurrencyPreference = (): number => {
  try {
    const parsed = Number(localStorage.getItem(CONCURRENCY_STORAGE_KEY))
    if ((ACCOUNT_MODEL_TEST_CONCURRENCY_OPTIONS as readonly number[]).includes(parsed)) return parsed
  } catch {
    // localStorage 不可用（隐私模式/测试桩）时退回默认并发。
  }
  return DEFAULT_ACCOUNT_MODEL_TEST_CONCURRENCY
}

const modelQuery = ref('')
const includeMedia = ref(props.variant === 'single')
const onlyFailed = ref(false)
const running = ref(false)
const catalogLoading = ref(false)
const concurrency = ref(loadConcurrencyPreference())
const rows = ref<ModelTestRow[]>([])
const catalogByAccount = ref<Record<number, ClaudeModel[]>>({})
const catalogErrors = ref<Record<number, string>>({})
const previewImageUrl = ref('')

// 批量任务只控制「下一行能不能开跑」；每行另有自己的 AbortController，
// 这样单个模型可以独立取消，不会误伤同批其它请求。
let runAbortController: AbortController | null = null
const rowControllers = new Map<string, AbortController>()
let loadVersion = 0
const catalogLoads = new Map<number, Promise<ClaudeModel[]>>()

const showAccount = computed(() => props.variant !== 'single' && props.accounts.length > 1)
// Result table columns (the account column only when several accounts are tested together).
const tableColumns = computed<TableColumn<ModelTestRow>[]>(() => [
  { id: 'select', header: '', meta: { class: { th: 'w-10', td: 'w-10' } } },
  ...(showAccount.value ? [{ id: 'account', header: t('admin.accounts.batchTest.account') }] : []),
  { id: 'model', header: t('admin.accounts.batchTest.model') },
  { id: 'status', header: t('admin.accounts.batchTest.status') },
  { id: 'duration', header: t('admin.accounts.batchTest.duration') },
  { id: 'result', header: t('admin.accounts.batchTest.result') },
  { id: 'actions', header: t('admin.accounts.batchTest.action'), meta: { class: { th: 'text-right', td: 'text-right' } } }
])
// Rows keep an is-<status> class (styling and specs count rows by status).
const tableMeta = { class: { tr: (row: { original: ModelTestRow }) => `is-${row.original.status}` } }
const tableUi = {
  base: 'border-separate border-spacing-0',
  thead: '[&>tr]:bg-elevated [&>tr]:after:content-none',
  tbody: '[&>tr]:last:[&>td]:border-b-0',
  th: 'py-2 text-xs font-medium text-muted first:rounded-l-lg last:rounded-r-lg border-y border-default first:border-l last:border-r',
  td: 'py-2.5 align-top border-b border-default text-sm',
  separator: 'h-0'
}

const headline = computed(() => {
  if (props.variant === 'single' && props.accounts[0]) {
    return t('admin.accounts.modelCount', { count: rows.value.length })
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

const progressSuccessRatio = computed(() => ratio(counts.value.success, rows.value.length))
const progressFailedRatio = computed(() => ratio(counts.value.failed + counts.value.skipped, rows.value.length))
const completedCount = computed(
  () => counts.value.success + counts.value.failed + counts.value.skipped
)
const progressPct = computed(() => {
  if (!rows.value.length) return 0
  return Math.round((completedCount.value / rows.value.length) * 100)
})
const concurrencyItems = ACCOUNT_MODEL_TEST_CONCURRENCY_OPTIONS.map((value) => ({
  label: String(value),
  value
}))

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

const ratio = (value: number, total: number) => {
  if (!total) return 0
  return Math.min(1, Math.max(0, value / total))
}

const statusLabel = (status: RowStatus) => {
  if (status === 'idle') return t('admin.accounts.batchTest.notTested')
  return t(`admin.accounts.batchTest.${status}`)
}

// 失败原因归类：只影响展示与跳转，不改测试协议。
const failureKind = (row: ModelTestRow): AccountModelTestFailureKind | null => {
  if (row.status !== 'failed' || row.catalogFailed) return null
  return classifyAccountModelTestFailure({ error: row.error, status: row.failureStatus })
}

const failureLabel = (row: ModelTestRow) => {
  const kind = failureKind(row)
  return kind ? t(`admin.accounts.batchTest.failure.${kind}.label`) : ''
}

const failureHint = (row: ModelTestRow) => {
  const kind = failureKind(row)
  return kind ? t(`admin.accounts.batchTest.failure.${kind}.hint`) : ''
}

const goToAccount = (row: ModelTestRow) => {
  if (!router) return
  void router.push(buildAccountEditLocation(row.accountId))
}

const cancelRowTestId = (row: ModelTestRow) =>
  props.variant === 'single' ? `cancel-row-${row.modelId}` : `cancel-row-${row.accountId}-${row.modelId}`

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
  runAbortController?.abort()
  runAbortController = null
  for (const controller of rowControllers.values()) controller.abort()
  rowControllers.clear()
  running.value = false
  for (const row of rows.value) {
    if (row.status === 'running') {
      resetOutput(row)
      row.status = 'idle'
    }
  }
}

const cancelRow = (row: ModelTestRow) => {
  const controller = rowControllers.get(row.key)
  controller?.abort()
  rowControllers.delete(row.key)
  if (row.status === 'running') {
    resetOutput(row)
    row.status = 'idle'
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

watch(concurrency, (value) => {
  try {
    localStorage.setItem(CONCURRENCY_STORAGE_KEY, String(value))
  } catch {
    // 存不下就只对本次会话生效，不影响测试。
  }
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
  row.failureStatus = undefined
}

const runRow = async (row: ModelTestRow, parentSignal?: AbortSignal) => {
  if (row.status === 'skipped' || row.catalogFailed) return
  if (parentSignal?.aborted) return

  // 每行一个控制器：批量运行只负责调度，单行取消不牵连同批其它模型。
  const controller = new AbortController()
  const signal = controller.signal
  const abortFromParent = () => controller.abort()
  parentSignal?.addEventListener('abort', abortFromParent, { once: true })
  rowControllers.set(row.key, controller)

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
    row.failureStatus = error instanceof AccountModelTestHTTPError ? error.status : undefined
    row.error = error instanceof Error ? error.message : t('common.unknownError')
  } finally {
    parentSignal?.removeEventListener('abort', abortFromParent)
    if (rowControllers.get(row.key) === controller) rowControllers.delete(row.key)
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
  const controller = new AbortController()
  runAbortController = controller
  const signal = controller.signal
  try {
    await runWithConcurrency(
      jobs,
      concurrency.value,
      async (row) => {
        await runRow(row, signal)
      },
      signal
    )
  } catch (error) {
    if (!(error instanceof DOMException && error.name === 'AbortError')) throw error
  } finally {
    if (runAbortController === controller) {
      running.value = false
      runAbortController = null
    }
  }
}

const testOne = async (row: ModelTestRow) => {
  if (!props.show || running.value || row.status === 'running' || row.catalogFailed) return
  try {
    await runRow(row)
  } catch (error) {
    if (!(error instanceof DOMException && error.name === 'AbortError')) throw error
  }
}

watch(
  [() => props.show, () => props.accounts.map((account) => account.id + ':' + account.platform + ':' + account.type).join(',')],
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
  stopRun,
  cancelRow
})
</script>

<style scoped>
.model-test { --test-accent: #0f766e; --test-success: #15805d; --test-failed: #c2414c; display: grid; min-width: 0; gap: 16px; color: var(--signal-text); }
:global(.dark .model-test) { --test-accent: #5eead4; --test-success: #6ee7b7; --test-failed: #fda4af; }
.model-test-toolbar, .model-test-toolbar-controls, .model-test-actions { display: flex; align-items: center; gap: 12px; flex-wrap: wrap; }
.model-test-toolbar { justify-content: space-between; }
.model-test-kicker { margin: 0; font-size: 13px; font-weight: 650; }
.model-test-toolbar-controls { gap: 16px; }
.model-test-search-wrap { display: flex; align-items: center; gap: 8px; padding: 0 11px; border: 1px solid var(--signal-line); border-radius: 9px; background: var(--signal-surface); color: var(--signal-muted); transition: border-color 180ms var(--ease-out, cubic-bezier(0.23, 1, 0.32, 1)); }
.model-test-search-wrap:focus-within { border-color: var(--test-accent); box-shadow: 0 0 0 3px color-mix(in srgb, var(--test-accent) 12%, transparent); }
.model-test-search { min-height: 36px; width: 170px; min-width: 0; padding: 0; border: 0; outline: none; box-shadow: none; background: transparent; color: var(--signal-text); font-size: 12px; }
.model-test-option { display: flex; align-items: center; gap: 7px; color: var(--signal-muted); font-size: 12px; cursor: pointer; white-space: nowrap; }
.model-test-concurrency { display: flex; align-items: center; gap: 7px; color: var(--signal-muted); font-size: 12px; white-space: nowrap; }
.model-test input[type=checkbox] { width: 14px; height: 14px; accent-color: var(--test-accent); cursor: pointer; }
.model-test-actions { gap: 8px; }
.model-test-button { display: inline-flex; align-items: center; justify-content: center; gap: 7px; min-height: 36px; padding: 0 13px; border: 1px solid var(--signal-line); border-radius: 9px; background: var(--signal-surface); color: var(--signal-text); font-size: 12px; font-weight: 600; white-space: nowrap; transition: background-color 180ms var(--ease-out, cubic-bezier(0.23, 1, 0.32, 1)), border-color 180ms var(--ease-out, cubic-bezier(0.23, 1, 0.32, 1)), transform 140ms var(--ease-out, cubic-bezier(0.23, 1, 0.32, 1)); }
.model-test-button svg { width: 15px; height: 15px; flex-shrink: 0; }
.model-test-button.is-primary { background: #0f766e; border-color: #0f766e; color: #fff; box-shadow: 0 2px 4px #0f766e18; }
.model-test-button.is-stop { color: var(--test-failed); border-color: color-mix(in srgb, var(--test-failed) 24%, var(--signal-line)); background: color-mix(in srgb, var(--test-failed) 6%, var(--signal-surface)); }
.model-test-button:active:not(:disabled) { transform: scale(0.97); }
.model-test-button:disabled { opacity: .42; cursor: not-allowed; box-shadow: none; }
.model-test-selection { display: flex; align-items: center; gap: 2px; margin-left: 6px; padding-left: 12px; border-left: 1px solid var(--signal-line); }
.model-test-text-btn, .model-test-filter { min-height: 32px; padding: 0 9px; border-radius: 7px; color: var(--signal-muted); font-size: 12px; transition: color 160ms var(--ease-out, cubic-bezier(0.23, 1, 0.32, 1)), background-color 160ms var(--ease-out, cubic-bezier(0.23, 1, 0.32, 1)); }
.model-test-text-btn:disabled { opacity: .4; cursor: not-allowed; }
.model-test-filter { margin-left: auto; border: 1px solid transparent; }
.model-test-filter[aria-pressed=true] { color: var(--test-failed); background: color-mix(in srgb, var(--test-failed) 7%, transparent); border-color: color-mix(in srgb, var(--test-failed) 18%, transparent); }
.model-test :is(button, input):focus-visible { outline: 2px solid var(--test-accent); outline-offset: 3px; }
.model-test-search:focus-visible { outline: none; }
.model-test-progress { display: grid; gap: 9px; }
.model-test-progress-bar { position: relative; height: 3px; overflow: hidden; border-radius: 99px; background: var(--signal-line); }
.model-test-progress-bar span { position: absolute; inset: 0; transform-origin: left; transition: transform 240ms var(--ease-out, cubic-bezier(0.23, 1, 0.32, 1)); }
.model-test-progress-bar .is-success { background: var(--test-success); }
.model-test-progress-bar .is-failed { background: var(--test-failed); }
.model-test-progress-meta { display: flex; align-items: center; flex-wrap: wrap; gap: 14px; color: var(--signal-muted); font-size: 11px; font-variant-numeric: tabular-nums; }
.model-test-counter { display: inline-flex; align-items: center; gap: 6px; }
.model-test-counter i { width: 5px; height: 5px; border-radius: 50%; background: currentColor; }
.model-test-counter.is-success { color: var(--test-success); }
.model-test-counter.is-failed { color: var(--test-failed); }
.model-test-counter.is-running { color: var(--test-accent); }
.model-test-completed { margin-left: auto; }
.model-test-table-wrap { min-width: 0; border: 1px solid var(--signal-line); background: var(--signal-surface); border-radius: 12px; max-height: min(55dvh, 600px); overflow: auto; scrollbar-width: thin; }
.model-test-table { width: 100%; min-width: 660px; border-collapse: collapse; font-size: 12px; }
.model-test-table th, .model-test-table td { padding: 13px 14px; border-bottom: 1px solid var(--signal-line); text-align: left; vertical-align: top; }
.model-test-table th { padding-top: 11px; padding-bottom: 11px; color: var(--signal-muted); font-size: 11px; font-weight: 500; position: sticky; top: 0; background: var(--signal-bg); z-index: 1; }
.model-test-table tbody tr { transition: background-color 200ms var(--ease-out, cubic-bezier(0.23, 1, 0.32, 1)); }
.model-test-table tbody tr:last-child td { border-bottom: 0; }
@media (hover: hover) and (pointer: fine) {
  .model-test-button.is-primary:hover:not(:disabled) { background: #115e59; border-color: #115e59; }
  .model-test-button:hover:not(:disabled) { border-color: var(--test-accent); background: color-mix(in srgb, var(--test-accent) 5%, var(--signal-surface)); }
  .model-test-button.is-stop:hover:not(:disabled) { border-color: var(--test-failed); background: color-mix(in srgb, var(--test-failed) 12%, var(--signal-surface)); }
  .model-test-text-btn:hover:not(:disabled), .model-test-filter:hover { color: var(--test-accent); background: color-mix(in srgb, var(--test-accent) 7%, transparent); }
  .model-test-table tbody tr:hover { background: color-mix(in srgb, var(--signal-muted) 4%, transparent); }
}
.model-test-table tr.is-running { background: color-mix(in srgb, var(--test-accent) 4%, transparent); }
.model-test-check { width: 34px; padding-right: 2px !important; }
.model-test-actions-col { width: 76px; white-space: nowrap; }
.model-test-status { display: inline-flex; align-items: center; gap: 6px; padding: 3px 7px; border-radius: 6px; font-size: 11px; white-space: nowrap; color: var(--signal-muted); background: var(--signal-bg); }
.model-test-status::before { content: ''; width: 5px; height: 5px; border-radius: 50%; background: currentColor; }
.model-test-status.is-success { color: var(--test-success); background: color-mix(in srgb, var(--test-success) 8%, transparent); }
.model-test-status.is-failed { color: var(--test-failed); background: color-mix(in srgb, var(--test-failed) 8%, transparent); }
.model-test-status.is-running { color: var(--test-accent); background: color-mix(in srgb, var(--test-accent) 8%, transparent); }
.model-test-status.is-running::before { width: 9px; height: 9px; border: 1.5px solid currentColor; border-right-color: transparent; background: transparent; animation: model-test-spin 900ms linear infinite; }
.model-test-result { min-width: 220px; max-width: 420px; }
.model-test-result-text { margin: 0; line-height: 1.65; display: -webkit-box; -webkit-line-clamp: 3; -webkit-box-orient: vertical; overflow: hidden; overflow-wrap: anywhere; }
.model-test-result-text.is-failed { color: var(--test-failed); }
.model-test-result .model-test-text-btn { padding: 0; min-height: 26px; font-size: 11px; }
.model-test-failure { display: grid; gap: 4px; margin-top: 8px; padding: 7px 9px; border-radius: 8px; border: 1px solid var(--signal-line); background: var(--signal-bg); }
.model-test-failure-badge { justify-self: start; padding: 1px 6px; border-radius: 5px; font-size: 10px; font-weight: 600; letter-spacing: .01em; }
.model-test-failure-hint { margin: 0; font-size: 11px; line-height: 1.55; color: var(--signal-muted); }
.model-test-failure-action { justify-self: start; }
.model-test-failure.is-auth { border-color: color-mix(in srgb, var(--test-failed) 22%, var(--signal-line)); background: color-mix(in srgb, var(--test-failed) 6%, transparent); }
.model-test-failure.is-auth .model-test-failure-badge { color: var(--test-failed); background: color-mix(in srgb, var(--test-failed) 14%, transparent); }
.model-test-failure.is-rate-limit { border-color: color-mix(in srgb, #d97706 24%, var(--signal-line)); background: color-mix(in srgb, #d97706 6%, transparent); }
.model-test-failure.is-rate-limit .model-test-failure-badge { color: #b45309; background: color-mix(in srgb, #d97706 14%, transparent); }
.model-test-failure.is-network { border-color: color-mix(in srgb, #2563eb 22%, var(--signal-line)); background: color-mix(in srgb, #2563eb 6%, transparent); }
.model-test-failure.is-network .model-test-failure-badge { color: #1d4ed8; background: color-mix(in srgb, #2563eb 12%, transparent); }
.model-test-failure.is-other { border-color: var(--signal-line); }
.model-test-failure.is-other .model-test-failure-badge { color: var(--signal-muted); background: color-mix(in srgb, var(--signal-muted) 12%, transparent); }
:global(.dark .model-test-failure.is-rate-limit .model-test-failure-badge) { color: #fbbf24; }
:global(.dark .model-test-failure.is-network .model-test-failure-badge) { color: #93c5fd; }
.model-test-disclosure { display: grid; grid-template-rows: 0fr; opacity: 0; transition: grid-template-rows 220ms var(--ease-out, cubic-bezier(0.23, 1, 0.32, 1)), opacity 220ms var(--ease-out, cubic-bezier(0.23, 1, 0.32, 1)); }
.model-test-disclosure.is-open { grid-template-rows: 1fr; opacity: 1; }
.model-test-disclosure > div { overflow: hidden; min-height: 0; }
.model-test-result-full { margin: 8px 0 0; max-height: 180px; overflow: auto; white-space: pre-wrap; overflow-wrap: anywhere; background: var(--signal-bg); border: 1px solid var(--signal-line); border-radius: 8px; padding: 10px 12px; font-size: 11px; line-height: 1.65; }
.model-test-button.is-row { min-height: 28px; padding: 0 10px; border-radius: 7px; font-size: 11px; font-weight: 500; }
.model-test-media { display: flex; flex-wrap: wrap; gap: 8px; margin-top: 8px; }
.model-test-media img, .model-test-result video { max-height: 120px; max-width: 180px; object-fit: contain; border-radius: 8px; border: 1px solid var(--signal-line); background: var(--signal-bg); cursor: zoom-in; }
.model-test-result audio, .model-test-result video { width: 100%; margin-top: 8px; }
.model-test-empty { padding: 32px 16px; text-align: center; color: var(--signal-muted); font-size: 12px; }
.mono { font-variant-numeric: tabular-nums; overflow-wrap: anywhere; }
.model-test-lightbox { position: fixed; inset: 0; z-index: 80; display: flex; align-items: center; justify-content: center; background: rgba(0, 0, 0, .78); padding: 16px; }
.model-test-lightbox img { max-height: 90dvh; max-width: 90vw; border-radius: 8px; }
@keyframes model-test-spin { to { transform: rotate(360deg); } }
@media (max-width: 640px) {
  .model-test { gap: 13px; }
  .model-test-toolbar { gap: 10px; }
  .model-test-toolbar-controls { width: 100%; gap: 10px; }
  .model-test-search-wrap { flex: 1; min-width: 120px; }
  .model-test-search { width: 100%; }
  .model-test-button, .model-test-text-btn, .model-test-filter { min-height: 44px; }
  .model-test-actions { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); }
  .model-test-selection { grid-column: 1; margin-left: 0; padding-left: 0; border: 0; }
  .model-test-filter { justify-self: end; }
  .model-test-table-wrap { max-height: 50dvh; }
  .model-test-progress-meta { gap: 10px; }
}
@media (prefers-reduced-motion: reduce) {
  .model-test *, .model-test *::before { animation: none !important; transition: none !important; }
}
</style>
