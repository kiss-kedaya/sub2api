<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { useMediaQuery } from '@vueuse/core'
import { useI18n } from 'vue-i18n'
import type { TableColumn } from '@nuxt/ui'
import { compactTableUi, type SemanticColor } from '../utils/templateUi'
import { opsAPI, type OpsRuntimeLogConfig, type OpsSystemLog, type OpsSystemLogSinkHealth } from '@/api/admin/ops'
import Pagination from '@/components/common/Pagination.vue'
import Select from '@/components/common/Select.vue'
import { useAppStore } from '@/stores'
import { extractApiErrorMessage } from '@/utils/apiError'

const appStore = useAppStore()
const { t } = useI18n()

// 与 DataTable 一致：< 768px 切换为卡片视图，避免宽表在移动端被截断。
const isDesktopViewport = useMediaQuery('(min-width: 768px)')

const props = withDefaults(defineProps<{
  platformFilter?: string
  refreshToken?: number
}>(), {
  platformFilter: '',
  refreshToken: 0
})

const loading = ref(false)
const logs = ref<OpsSystemLog[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = ref(20)

const health = ref<OpsSystemLogSinkHealth>({
  queue_depth: 0,
  queue_capacity: 0,
  dropped_count: 0,
  write_failed_count: 0,
  written_count: 0,
  avg_write_delay_ms: 0
})

const runtimeLoading = ref(false)
const runtimeSaving = ref(false)
const runtimeConfig = reactive<OpsRuntimeLogConfig>({
  level: 'info',
  persist_access_logs: false,
  enable_sampling: false,
  sampling_initial: 100,
  sampling_thereafter: 100,
  caller: true,
  stacktrace_level: 'error',
  retention_days: 30
})

const filters = reactive({
  time_range: '1h' as '5m' | '30m' | '1h' | '6h' | '24h' | '7d' | '30d',
  start_time: '',
  end_time: '',
  host: '',
  level: '',
  component: '',
  request_id: '',
  client_request_id: '',
  user_id: '',
  api_key_id: '',
  account_id: '',
  platform: '',
  model: '',
  q: ''
})

const runtimeLevelOptions = [
  { value: 'debug', label: 'debug' },
  { value: 'info', label: 'info' },
  { value: 'warn', label: 'warn' },
  { value: 'error', label: 'error' }
]

const stacktraceLevelOptions = [
  { value: 'none', label: 'none' },
  { value: 'error', label: 'error' },
  { value: 'fatal', label: 'fatal' }
]

const timeRangeOptions = [
  { value: '5m', label: '5m' },
  { value: '30m', label: '30m' },
  { value: '1h', label: '1h' },
  { value: '6h', label: '6h' },
  { value: '24h', label: '24h' },
  { value: '7d', label: '7d' },
  { value: '30d', label: '30d' }
]

const filterLevelOptions = computed(() => [
  { value: '', label: t('admin.ops.systemLogs.all') },
  { value: 'debug', label: 'debug' },
  { value: 'info', label: 'info' },
  { value: 'warn', label: 'warn' },
  { value: 'error', label: 'error' }
])

const levelColor = (level: string): SemanticColor => {
  const v = String(level || '').toLowerCase()
  if (v === 'error' || v === 'fatal') return 'error'
  if (v === 'warn' || v === 'warning') return 'warning'
  if (v === 'debug') return 'neutral'
  return 'info'
}
const logTableUi = { ...compactTableUi, base: 'table-fixed border-separate border-spacing-0', thead: 'bg-elevated/50 [&>tr]:after:content-none', tr: 'align-top', th: 'px-3 py-2 text-[11px] font-semibold text-muted border-b border-default', td: 'px-3 py-2 border-b border-default' }
const logColumns = computed<TableColumn<OpsSystemLog>[]>(() => [
  { id: 'time', header: t('admin.ops.systemLogs.time'), meta: { class: { th: 'w-[170px]' } } },
  { id: 'host', header: t('admin.ops.systemLogs.host'), meta: { class: { th: 'w-[160px]' } } },
  { id: 'level', header: t('admin.ops.systemLogs.level'), meta: { class: { th: 'w-[80px]' } } },
  { id: 'detail', header: t('admin.ops.systemLogs.logDetails') },
])

const formatTime = (value: string) => {
  if (!value) return '-'
  const d = new Date(value)
  if (Number.isNaN(d.getTime())) return value
  return d.toLocaleString()
}

const getExtraString = (extra: Record<string, any> | undefined, key: string) => {
  if (!extra) return ''
  const v = extra[key]
  if (v == null) return ''
  if (typeof v === 'string') return v.trim()
  if (typeof v === 'number' || typeof v === 'boolean') return String(v)
  return ''
}

const formatSystemLogDetail = (row: OpsSystemLog) => {
  const parts: string[] = []
  const msg = String(row.message || '').trim()
  if (msg) parts.push(msg)

  const extra = row.extra || {}
  const statusCode = getExtraString(extra, 'status_code')
  const latencyMs = getExtraString(extra, 'latency_ms')
  const method = getExtraString(extra, 'method')
  const path = getExtraString(extra, 'path')
  const clientIP = getExtraString(extra, 'client_ip')
  const protocol = getExtraString(extra, 'protocol')

  const accessParts: string[] = []
  if (statusCode) accessParts.push(`status=${statusCode}`)
  if (latencyMs) accessParts.push(`latency_ms=${latencyMs}`)
  if (method) accessParts.push(`method=${method}`)
  if (path) accessParts.push(`path=${path}`)
  if (clientIP) accessParts.push(`ip=${clientIP}`)
  if (protocol) accessParts.push(`proto=${protocol}`)
  if (accessParts.length > 0) parts.push(accessParts.join(' '))

  const corrParts: string[] = []
  if (row.request_id) corrParts.push(`req=${row.request_id}`)
  if (row.client_request_id) corrParts.push(`client_req=${row.client_request_id}`)
  if (row.user_id != null) corrParts.push(`user=${row.user_id}`)
  if (row.api_key_id != null) corrParts.push(`key=${row.api_key_id}`)
  if (row.account_id != null) corrParts.push(`acc=${row.account_id}`)
  if (row.platform) corrParts.push(`platform=${row.platform}`)
  if (row.model) corrParts.push(`model=${row.model}`)
  if (corrParts.length > 0) parts.push(corrParts.join(' '))

  const errors = getExtraString(extra, 'errors')
  if (errors) parts.push(`errors=${errors}`)
  const err = getExtraString(extra, 'err') || getExtraString(extra, 'error')
  if (err) parts.push(`error=${err}`)

  // 用空格拼接，交给 CSS 自动换行，尽量“填满再换行”。
  return parts.join('  ')
}

const toRFC3339 = (value: string) => {
  if (!value) return undefined
  const d = new Date(value)
  if (Number.isNaN(d.getTime())) return undefined
  return d.toISOString()
}

const buildQuery = () => {
  const query: Record<string, any> = {
    page: page.value,
    page_size: pageSize.value,
    time_range: filters.time_range
  }

  if (filters.time_range === '30d') {
    query.time_range = '30d'
  }
  if (filters.start_time) query.start_time = toRFC3339(filters.start_time)
  if (filters.end_time) query.end_time = toRFC3339(filters.end_time)
  if (filters.host.trim()) query.host = filters.host.trim()
  if (filters.level.trim()) query.level = filters.level.trim()
  if (filters.component.trim()) query.component = filters.component.trim()
  if (filters.request_id.trim()) query.request_id = filters.request_id.trim()
  if (filters.client_request_id.trim()) query.client_request_id = filters.client_request_id.trim()
  if (filters.user_id.trim()) {
    const v = Number.parseInt(filters.user_id.trim(), 10)
    if (Number.isFinite(v) && v > 0) query.user_id = v
  }
  if (filters.api_key_id.trim()) {
    const v = Number.parseInt(filters.api_key_id.trim(), 10)
    if (Number.isFinite(v) && v > 0) query.api_key_id = v
  }
  if (filters.account_id.trim()) {
    const v = Number.parseInt(filters.account_id.trim(), 10)
    if (Number.isFinite(v) && v > 0) query.account_id = v
  }
  if (filters.platform.trim()) query.platform = filters.platform.trim()
  if (filters.model.trim()) query.model = filters.model.trim()
  if (filters.q.trim()) query.q = filters.q.trim()
  return query
}

const fetchLogs = async () => {
  loading.value = true
  try {
    const res = await opsAPI.listSystemLogs(buildQuery())
    logs.value = res.items || []
    total.value = res.total || 0
  } catch (err: any) {
    console.error('[OpsSystemLogTable] Failed to fetch logs', err)
    appStore.showError(err?.response?.data?.detail || t('admin.ops.systemLogs.loadFailed'))
  } finally {
    loading.value = false
  }
}

const fetchHealth = async () => {
  try {
    health.value = await opsAPI.getSystemLogSinkHealth()
  } catch {
    // 忽略健康数据读取失败，不影响主流程。
  }
}

const loadRuntimeConfig = async () => {
  runtimeLoading.value = true
  try {
    const cfg = await opsAPI.getRuntimeLogConfig()
    runtimeConfig.level = cfg.level
    runtimeConfig.persist_access_logs = cfg.persist_access_logs
    runtimeConfig.enable_sampling = cfg.enable_sampling
    runtimeConfig.sampling_initial = cfg.sampling_initial
    runtimeConfig.sampling_thereafter = cfg.sampling_thereafter
    runtimeConfig.caller = cfg.caller
    runtimeConfig.stacktrace_level = cfg.stacktrace_level
    runtimeConfig.retention_days = cfg.retention_days
  } catch (err: any) {
    console.error('[OpsSystemLogTable] Failed to load runtime log config', err)
  } finally {
    runtimeLoading.value = false
  }
}

const saveRuntimeConfig = async () => {
  runtimeSaving.value = true
  try {
    const saved = await opsAPI.updateRuntimeLogConfig({ ...runtimeConfig })
    runtimeConfig.level = saved.level
    runtimeConfig.persist_access_logs = saved.persist_access_logs
    runtimeConfig.enable_sampling = saved.enable_sampling
    runtimeConfig.sampling_initial = saved.sampling_initial
    runtimeConfig.sampling_thereafter = saved.sampling_thereafter
    runtimeConfig.caller = saved.caller
    runtimeConfig.stacktrace_level = saved.stacktrace_level
    runtimeConfig.retention_days = saved.retention_days
    appStore.showSuccess(t('admin.ops.systemLogs.runtimeConfigActive'))
  } catch (err: any) {
    console.error('[OpsSystemLogTable] Failed to save runtime log config', err)
    appStore.showError(err?.response?.data?.detail || t('admin.ops.systemLogs.runtimeConfigSaveFailed'))
  } finally {
    runtimeSaving.value = false
  }
}

const resetRuntimeConfig = async () => {
  const ok = window.confirm(t('admin.ops.systemLogs.resetRuntimeConfigConfirm'))
  if (!ok) return

  runtimeSaving.value = true
  try {
    const saved = await opsAPI.resetRuntimeLogConfig()
    runtimeConfig.level = saved.level
    runtimeConfig.persist_access_logs = saved.persist_access_logs
    runtimeConfig.enable_sampling = saved.enable_sampling
    runtimeConfig.sampling_initial = saved.sampling_initial
    runtimeConfig.sampling_thereafter = saved.sampling_thereafter
    runtimeConfig.caller = saved.caller
    runtimeConfig.stacktrace_level = saved.stacktrace_level
    runtimeConfig.retention_days = saved.retention_days
    appStore.showSuccess(t('admin.ops.systemLogs.runtimeConfigReset'))
    await fetchHealth()
  } catch (err: any) {
    console.error('[OpsSystemLogTable] Failed to reset runtime log config', err)
    appStore.showError(err?.response?.data?.detail || t('admin.ops.systemLogs.runtimeConfigResetFailed'))
  } finally {
    runtimeSaving.value = false
  }
}

const cleanupCurrentFilter = async () => {
  const ok = window.confirm(t('admin.ops.systemLogs.cleanupConfirm'))
  if (!ok) return
  try {
    const payload = {
      start_time: toRFC3339(filters.start_time),
      end_time: toRFC3339(filters.end_time),
      host: filters.host.trim() || undefined,
      level: filters.level.trim() || undefined,
      component: filters.component.trim() || undefined,
      request_id: filters.request_id.trim() || undefined,
      client_request_id: filters.client_request_id.trim() || undefined,
      user_id: filters.user_id.trim() ? Number.parseInt(filters.user_id.trim(), 10) : undefined,
      api_key_id: filters.api_key_id.trim() ? Number.parseInt(filters.api_key_id.trim(), 10) : undefined,
      account_id: filters.account_id.trim() ? Number.parseInt(filters.account_id.trim(), 10) : undefined,
      platform: filters.platform.trim() || undefined,
      model: filters.model.trim() || undefined,
      q: filters.q.trim() || undefined
    }
    const res = await opsAPI.cleanupSystemLogs(payload)
    appStore.showSuccess(t('admin.ops.systemLogs.cleanupSuccess', { count: res.deleted || 0 }))
    page.value = 1
    await Promise.all([fetchLogs(), fetchHealth()])
  } catch (err: any) {
    console.error('[OpsSystemLogTable] Failed to cleanup logs', err)
    appStore.showError(
      extractApiErrorMessage(err, t('admin.ops.systemLogs.cleanupFailed'), {
        OPS_SYSTEM_LOG_CLEANUP_FILTER_REQUIRED: t('admin.ops.systemLogs.cleanupFilterRequired')
      })
    )
  }
}

const resetFilters = () => {
  filters.time_range = '1h'
  filters.start_time = ''
  filters.end_time = ''
  filters.host = ''
  filters.level = ''
  filters.component = ''
  filters.request_id = ''
  filters.client_request_id = ''
  filters.user_id = ''
  filters.api_key_id = ''
  filters.account_id = ''
  filters.platform = props.platformFilter || ''
  filters.model = ''
  filters.q = ''
  page.value = 1
  fetchLogs()
}

watch(() => props.platformFilter, (v) => {
  if (v && !filters.platform) {
    filters.platform = v
    page.value = 1
    fetchLogs()
  }
})

watch(() => props.refreshToken, () => {
  fetchLogs()
  fetchHealth()
})

const onPageChange = (next: number) => {
  page.value = next
  fetchLogs()
}

const onPageSizeChange = (next: number) => {
  pageSize.value = next
  page.value = 1
  fetchLogs()
}

const applyFilters = () => {
  page.value = 1
  fetchLogs()
}

const hasData = computed(() => logs.value.length > 0)

onMounted(async () => {
  if (props.platformFilter) {
    filters.platform = props.platformFilter
  }
  await Promise.all([fetchLogs(), fetchHealth(), loadRuntimeConfig()])
})
</script>

<template>
  <section class="rounded-md border border-default bg-default p-4 shadow-xs">
    <div class="mb-4 flex flex-wrap items-center justify-between gap-3">
      <div>
        <h3 class="text-sm font-bold text-highlighted">{{ t('admin.ops.systemLogs.title') }}</h3>
        <p class="mt-1 text-xs text-muted">{{ t('admin.ops.systemLogs.description') }}</p>
      </div>
      <div class="flex flex-wrap items-center gap-2 text-xs">
        <span class="rounded-md bg-elevated px-2 py-1 text-default">{{ t('admin.ops.systemLogs.queue') }} {{ health.queue_depth }}/{{ health.queue_capacity }}</span>
        <span class="rounded-md bg-elevated px-2 py-1 text-default">{{ t('admin.ops.systemLogs.written') }} {{ health.written_count }}</span>
        <span class="rounded-md bg-amber-100 px-2 py-1 text-amber-700 dark:bg-amber-900/30 dark:text-amber-300">{{ t('admin.ops.systemLogs.dropped') }} {{ health.dropped_count }}</span>
        <span class="rounded-md bg-red-100 px-2 py-1 text-red-700 dark:bg-red-900/30 dark:text-red-300">{{ t('admin.ops.systemLogs.failed') }} {{ health.write_failed_count }}</span>
      </div>
    </div>

    <div class="mb-4 rounded-xl border border-default bg-elevated/50 p-3">
      <div class="mb-2 flex items-center justify-between">
        <div class="text-xs font-semibold text-default">{{ t('admin.ops.systemLogs.runtimeConfig') }}</div>
        <span v-if="runtimeLoading" class="text-xs text-muted">{{ t('common.loading') }}</span>
      </div>
      <div class="grid grid-cols-1 gap-3 md:grid-cols-2 xl:grid-cols-6">
        <label class="text-xs text-toned">
          {{ t('admin.ops.systemLogs.level') }}
          <Select v-model="runtimeConfig.level" class="mt-1" :options="runtimeLevelOptions" />
        </label>
        <label class="text-xs text-toned">
          {{ t('admin.ops.systemLogs.stacktraceThreshold') }}
          <Select v-model="runtimeConfig.stacktrace_level" class="mt-1" :options="stacktraceLevelOptions" />
        </label>
        <label class="text-xs text-toned">
          {{ t('admin.ops.systemLogs.samplingInitial') }}
          <UInput v-model.number="runtimeConfig.sampling_initial" type="number" min="1" class="w-full mt-1" />
        </label>
        <label class="text-xs text-toned">
          {{ t('admin.ops.systemLogs.samplingThereafter') }}
          <UInput v-model.number="runtimeConfig.sampling_thereafter" type="number" min="1" class="w-full mt-1" />
        </label>
        <label class="text-xs text-toned">
          {{ t('admin.ops.systemLogs.retentionDays') }}
          <UInput v-model.number="runtimeConfig.retention_days" type="number" min="1" max="3650" class="w-full mt-1" />
          <span class="mt-1 block text-[11px] text-muted">{{ t('admin.ops.systemLogs.retentionDaysHint') }}</span>
        </label>
        <div class="md:col-span-2 xl:col-span-6">
          <div class="grid gap-3 lg:grid-cols-[minmax(0,1fr)_auto] lg:items-end">
            <div class="flex flex-wrap items-center gap-x-4 gap-y-2">
              <UCheckbox v-model="runtimeConfig.caller" size="sm" :label="t('admin.ops.systemLogs.caller')" />
              <UCheckbox v-model="runtimeConfig.enable_sampling" size="sm" :label="t('admin.ops.systemLogs.sampling')" />
              <UCheckbox v-model="runtimeConfig.persist_access_logs" size="sm" :label="t('admin.ops.systemLogs.persistAccessLogs')" />
            </div>
            <div class="flex flex-wrap items-center gap-2 lg:justify-end">
              <UButton type="button" size="sm" :disabled="runtimeSaving" @click="saveRuntimeConfig">
                {{ runtimeSaving ? t('common.saving') : t('admin.ops.systemLogs.saveAndApply') }}
              </UButton>
              <UButton type="button" color="neutral" variant="outline" size="sm" :disabled="runtimeSaving" @click="resetRuntimeConfig">
                {{ t('admin.ops.systemLogs.resetDefaults') }}
              </UButton>
            </div>
          </div>
        </div>
      </div>
      <p class="mt-2 text-xs text-muted">{{ t('admin.ops.systemLogs.persistAccessLogsHint') }}</p>
      <p v-if="health.last_error" class="mt-2 text-xs text-red-600 dark:text-red-400">{{ t('admin.ops.systemLogs.latestWriteError') }} {{ health.last_error }}</p>
    </div>

    <div class="mb-4 grid grid-cols-1 gap-3 md:grid-cols-5">
      <label class="text-xs text-toned">
        {{ t('admin.ops.systemLogs.timeRange') }}
        <Select v-model="filters.time_range" class="mt-1" :options="timeRangeOptions" />
      </label>
      <label class="text-xs text-toned">
        {{ t('admin.ops.systemLogs.startTime') }}
        <UInput v-model="filters.start_time" type="datetime-local" class="w-full mt-1" />
      </label>
      <label class="text-xs text-toned">
        {{ t('admin.ops.systemLogs.endTime') }}
        <UInput v-model="filters.end_time" type="datetime-local" class="w-full mt-1" />
      </label>
      <label class="text-xs text-toned">
        {{ t('admin.ops.systemLogs.level') }}
        <Select v-model="filters.level" class="mt-1" :options="filterLevelOptions" />
      </label>
      <label class="text-xs text-toned">
        {{ t('admin.ops.systemLogs.component') }}
        <UInput v-model="filters.component" type="text" :placeholder="t('admin.ops.systemLogs.componentPlaceholder')" class="w-full mt-1" />
      </label>
      <label class="text-xs text-toned">
        {{ t('admin.ops.systemLogs.host') }}
        <UInput v-model="filters.host" type="text" class="w-full mt-1" />
      </label>
      <label class="text-xs text-toned">
        request_id
        <UInput v-model="filters.request_id" type="text" class="w-full mt-1" />
      </label>
      <label class="text-xs text-toned">
        client_request_id
        <UInput v-model="filters.client_request_id" type="text" class="w-full mt-1" />
      </label>
      <label class="text-xs text-toned">
        user_id
        <UInput v-model="filters.user_id" type="text" class="w-full mt-1" />
      </label>
      <label class="text-xs text-toned">
        {{ t('admin.ops.systemLogs.keyId') }}
        <UInput v-model="filters.api_key_id" type="text" class="w-full mt-1" />
      </label>
      <label class="text-xs text-toned">
        account_id
        <UInput v-model="filters.account_id" type="text" class="w-full mt-1" />
      </label>
      <label class="text-xs text-toned">
        {{ t('admin.ops.systemLogs.platform') }}
        <UInput v-model="filters.platform" type="text" class="w-full mt-1" />
      </label>
      <label class="text-xs text-toned">
        {{ t('admin.ops.systemLogs.model') }}
        <UInput v-model="filters.model" type="text" class="w-full mt-1" />
      </label>
      <label class="text-xs text-toned">
        {{ t('admin.ops.systemLogs.keyword') }}
        <UInput v-model="filters.q" type="text" :placeholder="t('admin.ops.systemLogs.keywordPlaceholder')" class="w-full mt-1" />
      </label>
    </div>

    <div class="mb-3 flex flex-wrap gap-2">
      <UButton type="button" size="sm" @click="applyFilters">{{ t('admin.ops.systemLogs.search') }}</UButton>
      <UButton type="button" color="neutral" variant="outline" size="sm" @click="resetFilters">{{ t('common.reset') }}</UButton>
      <UButton type="button" color="error" size="sm" @click="cleanupCurrentFilter">{{ t('admin.ops.systemLogs.cleanCurrentFilters') }}</UButton>
      <UButton type="button" color="neutral" variant="outline" size="sm" @click="fetchHealth">{{ t('admin.ops.systemLogs.refreshHealth') }}</UButton>
    </div>

    <div class="overflow-hidden rounded-xl border border-default">
      <div v-if="loading" class="px-4 py-8 text-center text-sm text-muted">{{ t('common.loading') }}</div>
      <div v-else-if="!hasData" class="px-4 py-8 text-center text-sm text-muted">{{ t('admin.ops.systemLogs.empty') }}</div>
      <div v-else-if="!isDesktopViewport" class="divide-y divide-default">
        <div v-for="row in logs" :key="row.id" class="space-y-1.5 p-3">
          <div class="flex items-center justify-between gap-2">
            <UBadge :color="levelColor(row.level)" variant="subtle" size="sm">{{ row.level }}</UBadge>
            <span class="text-xs text-muted">{{ formatTime(row.created_at) }}</span>
          </div>
          <div v-if="row.host" class="truncate text-xs text-muted" :title="row.host">
            {{ row.host }}
          </div>
          <div class="whitespace-normal break-all text-xs text-default">
            {{ formatSystemLogDetail(row) }}
          </div>
        </div>
      </div>
      <div v-else class="overflow-auto">
        <UTable :data="logs" :columns="logColumns" :ui="logTableUi">
          <template #time-cell="{ row }"><span class="text-xs text-default">{{ formatTime(row.original.created_at) }}</span></template>
          <template #host-cell="{ row }"><span class="block truncate text-xs text-default" :title="row.original.host || '-'">{{ row.original.host || '-' }}</span></template>
          <template #level-cell="{ row }"><UBadge :color="levelColor(row.original.level)" variant="subtle" size="sm">{{ row.original.level }}</UBadge></template>
          <template #detail-cell="{ row }"><span class="block whitespace-normal break-all text-xs text-default">{{ formatSystemLogDetail(row.original) }}</span></template>
        </UTable>
      </div>
      <Pagination
        :total="total"
        :page="page"
        :page-size="pageSize"
        @update:page="onPageChange"
        @update:page-size="onPageSizeChange"
      />
    </div>
  </section>
</template>
