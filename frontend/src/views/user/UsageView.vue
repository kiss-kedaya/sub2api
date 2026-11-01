<template>
  <AppLayout>
    <div class="signal-usage space-y-4">
      <UserPageHeader :title="t('usage.title')">
        <template #description>
          <span class="inline-flex items-center gap-1.5 tabular-nums"><UIcon name="i-lucide-calendar" class="size-4" />{{ startDate }} / {{ endDate }}</span>
        </template>
      </UserPageHeader>

      <div class="usage-stats-strip overflow-hidden rounded-lg shadow-xs ring ring-default dark:shadow-none">
        <UsageStatsCards class="signal-usage-metrics" :stats="usageStats" :show-account-cost="false" :strike-standard-cost="true" />
      </div>

      <UCard class="shadow-xs dark:shadow-none" :ui="{ root: 'overflow-visible', body: 'p-3 sm:px-4 sm:py-3' }">
        <div class="flex flex-wrap items-center gap-x-6 gap-y-3">
          <div class="flex items-center gap-2">
            <span class="text-sm font-medium text-muted">{{ t('admin.dashboard.timeRange') }}</span>
            <DateRangePicker
              ref="dateRangeRef"
              v-model:start-date="startDate"
              v-model:end-date="endDate"
              @change="onDateRangeChange"
            />
          </div>
          <div class="ml-auto flex items-center gap-2">
            <span class="text-sm font-medium text-muted">{{ t('admin.dashboard.granularity') }}</span>
            <div class="w-28">
              <Select v-model="granularity" :options="granularityOptions" @change="loadChartData" />
            </div>
          </div>
        </div>
      </UCard>

      <div class="usage-charts grid grid-cols-1 gap-4 lg:grid-cols-2">
        <ModelDistributionChart
          v-model:metric="modelDistributionMetric"
          :model-stats="requestedModelStats"
          :animation-duration="chartAnimationDuration"
          :loading="modelStatsLoading"
          :show-source-toggle="false"
          :show-metric-toggle="true"
          :enable-breakdown="false"
          :show-account-cost="false"
          :start-date="startDate"
          :end-date="endDate"
        />
        <GroupDistributionChart
          v-model:metric="groupDistributionMetric"
          :group-stats="groupStats"
          :animation-duration="chartAnimationDuration"
          :loading="chartsLoading"
          :show-metric-toggle="true"
          :enable-breakdown="false"
          :show-account-cost="false"
          :start-date="startDate"
          :end-date="endDate"
        />
        <EndpointDistributionChart
          v-model:source="endpointDistributionSource"
          v-model:metric="endpointDistributionMetric"
          :endpoint-stats="inboundEndpointStats"
          :animation-duration="chartAnimationDuration"
          :upstream-endpoint-stats="upstreamEndpointStats"
          :endpoint-path-stats="endpointPathStats"
          :loading="endpointStatsLoading"
          :show-source-toggle="false"
          :show-metric-toggle="true"
          :enable-breakdown="false"
          :title="t('usage.endpointDistribution')"
          :start-date="startDate"
          :end-date="endDate"
        />
        <TokenUsageTrend :trend-data="trendData" :loading="chartsLoading" :animation-duration="chartAnimationDuration" />
      </div>

      <UCard class="shadow-xs dark:shadow-none" :ui="{ root: 'overflow-visible', body: 'p-4 sm:p-5' }">
        <div v-if="activeTab === 'errors'" class="grid grid-cols-1 items-end gap-4 sm:grid-cols-2 xl:grid-cols-5">
          <UFormField :label="t('admin.usage.requestId')">
            <UInput v-model="errorFilter.request_id" type="text" size="lg" class="w-full" :ui="{ base: 'py-2.5' }" :placeholder="t('usage.requestIdPlaceholder')" @change="applyErrorFilters" @keyup.enter="applyErrorFilters" />
          </UFormField>
          <UFormField :label="t('usage.errors.keyName')">
            <Select v-model="errorFilter.api_key_id" :options="errorKeyOptions" @change="applyErrorFilters" />
          </UFormField>
          <UFormField :label="t('usage.errors.model')">
            <Select
              v-model="errorFilter.model"
              :options="errorModelOptions"
              searchable
              creatable
              clearable
              :placeholder="t('usage.errors.modelPlaceholder')"
              @change="applyErrorFilters"
            />
          </UFormField>
          <UFormField :label="t('usage.errors.category')">
            <Select v-model="errorFilter.category" :options="errorCategoryOptions" @change="applyErrorFilters" />
          </UFormField>
          <UFormField :label="t('usage.errors.status')">
            <Select v-model="errorFilter.status_code" :options="errorStatusOptions" @change="applyErrorFilters" />
          </UFormField>
        </div>
        <div v-else class="grid grid-cols-1 items-end gap-4 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4">
          <UFormField :label="t('admin.usage.requestId')">
            <UInput v-model="filters.request_id" type="text" size="lg" class="w-full" :ui="{ base: 'py-2.5' }" :placeholder="t('usage.requestIdPlaceholder')" @change="applyFilters" @keyup.enter="applyFilters" />
          </UFormField>
          <UFormField :label="t('usage.apiKeyFilter')">
            <Select v-model="filters.api_key_id" :options="apiKeyOptions" @change="applyFilters" />
          </UFormField>
          <UFormField :label="t('usage.model')">
            <Select v-model="filters.model" :options="modelOptions" searchable @change="applyFilters" />
          </UFormField>
          <UFormField :label="t('admin.usage.group')">
            <Select v-model="filters.group_id" :options="groupOptions" searchable @change="applyFilters" />
          </UFormField>
          <UFormField :label="t('usage.type')">
            <Select v-model="filters.request_type" :options="requestTypeOptions" @change="applyFilters" />
          </UFormField>
          <UFormField :label="t('usage.compactionFilter')">
            <Select v-model="filters.native_compaction_v2" :options="compactionOptions" @change="applyFilters" />
          </UFormField>
          <UFormField :label="t('admin.usage.billingType')">
            <Select v-model="filters.billing_type" :options="billingTypeOptions" @change="applyFilters" />
          </UFormField>
          <UFormField :label="t('admin.usage.billingMode')">
            <Select v-model="filters.billing_mode" :options="billingModeOptions" @change="applyFilters" />
          </UFormField>
        </div>
      </UCard>

      <div class="flex flex-wrap items-center justify-between gap-3">
        <div v-if="errorViewEnabled" class="usage-tabs" role="tablist">
          <button type="button" role="tab" class="usage-tab" :aria-selected="activeTab === 'usage'" @click="activeTab = 'usage'">
            {{ t('usage.tabs.usage') }}
          </button>
          <button type="button" role="tab" class="usage-tab" :aria-selected="activeTab === 'errors'" @click="switchToErrors">
            {{ t('usage.tabs.errors') }}
          </button>
        </div>
        <div class="ml-auto flex flex-wrap items-center justify-end gap-2">
          <UButton
            color="neutral"
            variant="outline"
            icon="i-lucide-refresh-cw"
            :ui="{ leadingIcon: (activeTab === 'errors' ? errorLoading : loading) ? 'animate-spin' : '' }"
            :disabled="activeTab === 'errors' ? errorLoading : loading"
            :title="t('common.refresh')"
            :aria-label="t('common.refresh')"
            @click="refreshData"
          />
          <UButton
            color="neutral"
            variant="outline"
            icon="i-lucide-filter-x"
            :title="t('common.reset')"
            :aria-label="t('common.reset')"
            @click="resetFilters"
          />
          <div class="relative" ref="columnDropdownRef">
            <UButton
              color="neutral"
              variant="outline"
              icon="i-lucide-columns-3"
              data-testid="usage-column-settings"
              :title="t('admin.users.columnSettings')"
              :aria-label="t('admin.users.columnSettings')"
              :aria-expanded="showColumnDropdown"
              @click="showColumnDropdown = !showColumnDropdown"
            />
            <div
              v-if="showColumnDropdown"
              class="absolute right-0 top-full z-50 mt-1 max-h-80 w-52 overflow-y-auto rounded-lg bg-default p-1 shadow-lg ring ring-default"
            >
              <button
                v-for="col in currentToggleableColumns"
                :key="col.key"
                type="button"
                :data-testid="`usage-column-toggle-${col.key}`"
                @click="toggleCurrentColumn(col.key)"
                class="flex w-full items-center justify-between rounded-md px-2.5 py-1.5 text-left text-sm text-default transition-colors hover:bg-elevated"
              >
                <span>{{ col.label }}</span>
                <UIcon v-if="isCurrentColumnVisible(col.key)" name="i-lucide-check" class="size-4 text-highlighted" />
              </button>
            </div>
          </div>
          <UButton v-if="activeTab !== 'errors'" icon="i-lucide-download" :disabled="exporting" @click="exportToCSV">{{ exporting ? t('usage.exporting') : t('usage.exportCsv') }}</UButton>
        </div>
      </div>

      <template v-if="activeTab === 'usage'">
        <InflightUsagePanel
          :open="inflightOpen"
          :total="inflightTotal"
          :shown="inflightRows.length"
          :loading="inflightLoading"
          :loaded="inflightLoaded"
          :has-more="inflightHasMore"
          :error="inflightError"
          @toggle="toggleInflight"
          @refresh="loadInflight(false)"
          @more="loadMoreInflight"
        >
          <UsageTable
            flat
            :data="inflightRows"
            :loading="inflightLoading"
            :columns="inflightColumns"
            :show-account-billing="false"
            :show-upstream-endpoint="false"
          />
        </InflightUsagePanel>
        <UsageTable
          :data="usageLogs"
          :loading="loading"
          :columns="visibleColumns"
          :server-side-sort="true"
          :show-account-billing="false"
          :show-upstream-endpoint="false"
          default-sort-key="created_at"
          default-sort-order="desc"
          @sort="handleSort"
          @ipGeoBatchFailed="handleIpGeoBatchFailed"
        />

        <Pagination
          class="signal-pagination"
          v-if="pagination.total > 0"
          :page="pagination.page"
          :total="pagination.total"
          :page-size="pagination.page_size"
          @update:page="handlePageChange"
          @update:pageSize="handlePageSizeChange"
        />
      </template>

      <UserErrorRequestsTable
        v-else-if="errorViewEnabled"
        :rows="errorRows"
        :total="errorTotal"
        :loading="errorLoading"
        :page="errorPage"
        :page-size="errorPageSize"
        :visible-column-keys="errVisibleColumnKeys"
        @sort="onErrorSort"
        @update:page="onErrorPage"
        @update:pageSize="onErrorPageSize"
        @ipGeoBatchFailed="handleIpGeoBatchFailed"
      />
    </div>
  </AppLayout>

</template>

<script setup lang="ts">
import '@/styles/console-workspace.css'
import { computed, onMounted, onUnmounted, reactive, ref, watch } from 'vue'
import { usePreferredReducedMotion } from '@vueuse/core'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { keysAPI, usageAPI, userGroupsAPI } from '@/api'
import AppLayout from '@/components/layout/AppLayout.vue'
import Pagination from '@/components/common/Pagination.vue'
import Select, { type SelectOption } from '@/components/common/Select.vue'
import DateRangePicker from '@/components/common/DateRangePicker.vue'
import UsageStatsCards from '@/components/admin/usage/UsageStatsCards.vue'
import UsageTable from '@/components/admin/usage/UsageTable.vue'
import InflightUsagePanel from '@/components/admin/usage/InflightUsagePanel.vue'
import ModelDistributionChart from '@/components/charts/ModelDistributionChart.vue'
import GroupDistributionChart from '@/components/charts/GroupDistributionChart.vue'
import EndpointDistributionChart from '@/components/charts/EndpointDistributionChart.vue'
import TokenUsageTrend from '@/components/charts/TokenUsageTrend.vue'
import UserErrorRequestsTable from '@/components/user/UserErrorRequestsTable.vue'
import UserPageHeader from '@/components/user/UserPageHeader.vue'
import { getPersistedPageSize } from '@/composables/usePersistedPageSize'
import { formatReasoningEffort } from '@/utils/format'
import { fetchPaginatedItems } from '@/utils/paginatedItems'
import { getBillingModeLabel, getDisplayBillingMode as resolveDisplayBillingMode } from '@/utils/billingMode'
import { resolveUsageRequestType, requestTypeToLegacyStream } from '@/utils/usageRequestType'
import type {
  ApiKey,
  EndpointStat,
  Group,
  GroupStat,
  ModelStat,
  TrendDataPoint,
  UsageLog,
  UsageQueryParams,
  UsageStatsResponse,
  UserErrorRequest,
} from '@/types'
import type { Column } from '@/components/common/types'
import { COMMON_ERROR_STATUS_CODES } from '@/utils/errorBadges'

const { t } = useI18n()
const appStore = useAppStore()
const reducedMotion = usePreferredReducedMotion()
const chartAnimationDuration = computed(() => reducedMotion.value === 'reduce' ? 0 : 180)

type DistributionMetric = 'tokens' | 'actual_cost'
type EndpointSource = 'inbound' | 'upstream' | 'path'

const usageStats = ref<UsageStatsResponse | null>(null)
const usageLogs = ref<UsageLog[]>([])
const trendData = ref<TrendDataPoint[]>([])
const requestedModelStats = ref<ModelStat[]>([])
const groupStats = ref<GroupStat[]>([])
const inboundEndpointStats = ref<EndpointStat[]>([])
const upstreamEndpointStats = ref<EndpointStat[]>([])
const endpointPathStats = ref<EndpointStat[]>([])

const loading = ref(false)
const chartsLoading = ref(false)
const modelStatsLoading = ref(false)
const endpointStatsLoading = ref(false)
const exporting = ref(false)
const errorRows = ref<UserErrorRequest[]>([])
const errorLoading = ref(false)
const errorPage = ref(1)
const errorPageSize = ref(20)
const errorSortBy = ref('created_at')
const errorSortOrder = ref<'asc' | 'desc'>('desc')
const errorTotal = ref(0)
const errorFilter = ref<{ model: string | null; category: string; api_key_id: number | null; status_code: number | null; request_id: string }>({
  model: '',
  category: '',
  api_key_id: null,
  status_code: null,
  request_id: '',
})

const errorKeyOptions = computed<SelectOption[]>(() => [
  { value: null, label: t('usage.errors.allKeys') },
  ...apiKeys.value.map((k) => ({ value: k.id, label: k.name })),
])

// 模型候选取自当前已加载错误中出现过的模型；creatable 允许输入任意片段做后端模糊。
const errorModelOptions = computed<SelectOption[]>(() => {
  const seen = new Set<string>()
  const opts: SelectOption[] = []
  for (const r of errorRows.value) {
    if (r.model && !seen.has(r.model)) {
      seen.add(r.model)
      opts.push({ value: r.model, label: r.model })
    }
  }
  return opts
})

const errorCategoryCodes = ['auth', 'rate_limit', 'quota', 'invalid_request', 'model_not_found', 'service_unavailable', 'upstream', 'internal', 'cyber']

const errorCategoryOptions = computed<SelectOption[]>(() => [
  { value: '', label: t('usage.errors.allCategories') },
  ...errorCategoryCodes.map((c) => ({ value: c, label: t('usage.errors.categories.' + c) })),
])

// 状态码候选用固定常用列表(与管理端 UsageFilters 共用常量),不受当前页数据限制:
// 后端 status_code 过滤对全量生效,若只列当前页出现过的码,用户就选不到仅在后续页的码。
const errorStatusOptions = computed<SelectOption[]>(() => [
  { value: null, label: t('usage.errors.allStatuses') },
  ...COMMON_ERROR_STATUS_CODES.map((c) => ({ value: c, label: String(c) })),
])

const applyErrorFilters = () => {
  errorPage.value = 1
  void loadErrors()
}

let abortController: AbortController | null = null
let chartReqSeq = 0
let statsReqSeq = 0
let modelStatsReqSeq = 0

const formatLocalDate = (date: Date): string =>
  `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')}`

const getLast24HoursRangeDates = () => {
  const end = new Date()
  const start = new Date(end.getTime() - 24 * 60 * 60 * 1000)
  return { start: formatLocalDate(start), end: formatLocalDate(end) }
}

const getGranularityForRange = (start: string, end: string): 'day' | 'hour' => {
  const startTime = new Date(`${start}T00:00:00`).getTime()
  const endTime = new Date(`${end}T00:00:00`).getTime()
  return Math.ceil((endTime - startTime) / (1000 * 60 * 60 * 24)) <= 1 ? 'hour' : 'day'
}

const defaultRange = getLast24HoursRangeDates()
const startDate = ref(defaultRange.start)
const endDate = ref(defaultRange.end)
const granularity = ref<'day' | 'hour'>(getGranularityForRange(startDate.value, endDate.value))
const dateRangeRef = ref<InstanceType<typeof DateRangePicker> | null>(null)

const modelDistributionMetric = ref<DistributionMetric>('tokens')
const groupDistributionMetric = ref<DistributionMetric>('tokens')
const endpointDistributionMetric = ref<DistributionMetric>('tokens')
const endpointDistributionSource = ref<EndpointSource>('inbound')
const activeTab = ref<'usage' | 'errors'>('usage')
const errorViewEnabled = computed(() => appStore.cachedPublicSettings?.allow_user_view_error_requests ?? false)

const filters = ref<UsageQueryParams>({
  start_date: startDate.value,
  end_date: endDate.value,
  request_type: undefined,
  native_compaction_v2: null,
  billing_type: null,
  billing_mode: null,
})

const pagination = reactive({
  page: 1,
  page_size: getPersistedPageSize(),
  total: 0,
})
const sortState = reactive({
  sort_by: 'created_at',
  sort_order: 'desc' as 'asc' | 'desc',
})

const granularityOptions = computed<SelectOption[]>(() => [
  { value: 'day', label: t('admin.dashboard.day') },
  { value: 'hour', label: t('admin.dashboard.hour') },
])
const requestTypeOptions = computed<SelectOption[]>(() => [
  { value: null, label: t('admin.usage.allTypes') },
  { value: 'ws_v2', label: t('usage.ws') },
  { value: 'live', label: t('usage.live') },
  { value: 'stream', label: t('usage.stream') },
  { value: 'sync', label: t('usage.sync') },
])
const compactionOptions = computed<SelectOption[]>(() => [
  { value: null, label: t('usage.allCompactionTypes') },
  { value: true, label: t('usage.compactionOnly') },
])
const billingTypeOptions = computed<SelectOption[]>(() => [
  { value: null, label: t('admin.usage.allBillingTypes') },
  { value: 0, label: t('admin.usage.billingTypeBalance') },
  { value: 1, label: t('admin.usage.billingTypeSubscription') },
])
const billingModeOptions = computed<SelectOption[]>(() => [
  { value: null, label: t('admin.usage.allBillingModes') },
  { value: 'token', label: t('admin.usage.billingModeToken') },
  { value: 'per_request', label: t('admin.usage.billingModePerRequest') },
  { value: 'image', label: t('admin.usage.billingModeImage') },
  { value: 'video', label: t('admin.usage.billingModeVideo') },
])

const apiKeys = ref<ApiKey[]>([])
const groups = ref<Group[]>([])
const modelOptionValues = ref<string[]>([])

const apiKeyOptions = computed<SelectOption[]>(() => [
  { value: null, label: t('usage.allApiKeys') },
  ...apiKeys.value.map((key) => ({ value: key.id, label: key.name })),
])
const groupOptions = computed<SelectOption[]>(() => [
  { value: null, label: t('admin.usage.allGroups') },
  ...groups.value.map((group) => ({ value: group.id, label: group.name })),
])
const modelOptions = computed<SelectOption[]>(() => [
  { value: null, label: t('admin.usage.allModels') },
  ...modelOptionValues.value.map((model) => ({ value: model, label: model })),
])

const normalizedFilters = computed<UsageQueryParams>(() => {
  const requestType = filters.value.request_type
  const legacyStream = requestType ? requestTypeToLegacyStream(requestType) : filters.value.stream
  return {
    ...filters.value,
    start_date: startDate.value,
    end_date: endDate.value,
    stream: legacyStream === null ? undefined : legacyStream,
  }
})

const buildUsageListParams = (page: number, pageSize: number): UsageQueryParams => ({
  page,
  page_size: pageSize,
  ...normalizedFilters.value,
  request_id: filters.value.request_id?.trim() || undefined,
  include_inflight: false,
  sort_by: sortState.sort_by,
  sort_order: sortState.sort_order,
})

let logsGeneration = 0
const inflightOpen = ref(false)
const inflightRows = ref<UsageLog[]>([])
const inflightLoading = ref(false)
const inflightLoaded = ref(false)
const inflightError = ref('')
const inflightPage = ref(1)
const inflightTotal = ref(0)
const inflightHasMore = computed(() => inflightRows.value.length < inflightTotal.value)
let inflightGeneration = 0
const loadInflight = async (append = false) => {
  const generation = ++inflightGeneration
  const page = append ? inflightPage.value + 1 : 1
  inflightLoading.value = true
  inflightError.value = ''
  try {
    const res = await usageAPI.query({
      ...buildUsageListParams(page, 10),
      page,
      page_size: 10,
      include_inflight: true,
      inflight_only: true,
    })
    if (generation !== inflightGeneration) return
    const items = res.items || []
    inflightRows.value = append ? inflightRows.value.concat(items) : items
    inflightTotal.value = res.total || 0
    inflightPage.value = page
    inflightLoaded.value = true
  } catch (error) {
    if (generation !== inflightGeneration) return
    inflightError.value = t('usage.inflightLoadFailed')
    console.error('[UsageView] loadInflight failed:', error)
  } finally {
    if (generation === inflightGeneration) inflightLoading.value = false
  }
}
const loadMoreInflight = () => {
  if (!inflightLoading.value && inflightHasMore.value) void loadInflight(true)
}
const toggleInflight = () => {
  inflightOpen.value = !inflightOpen.value
  if (inflightOpen.value) void loadInflight(false)
}
const loadLogs = async (silent = false) => {
  if (silent && loading.value) return
  const generation = silent ? logsGeneration : ++logsGeneration
  if (!silent) {
    abortController?.abort()
    abortController = new AbortController()
    loading.value = true
  }
  const controller = silent ? new AbortController() : abortController!
  try {
    const res = await usageAPI.query(buildUsageListParams(pagination.page, pagination.page_size), {
      signal: controller.signal,
    })
    if (generation !== logsGeneration) return
    if (!controller.signal.aborted) {
      usageLogs.value = res.items
      pagination.total = res.total
      if (inflightOpen.value) void loadInflight(false)
    }
  } catch (error: any) {
    if (!silent && error?.name !== 'AbortError' && error?.code !== 'ERR_CANCELED') {
      appStore.showError(t('usage.failedToLoad'))
    }
  } finally {
    if (!silent && abortController === controller) loading.value = false
  }
}
const loadStats = async () => {
  const seq = ++statsReqSeq
  endpointStatsLoading.value = true
  try {
    const stats = await usageAPI.getStats(normalizedFilters.value)
    if (seq !== statsReqSeq) return
    usageStats.value = stats
    inboundEndpointStats.value = stats.endpoints || []
    upstreamEndpointStats.value = []
    endpointPathStats.value = []
  } catch (error) {
    if (seq !== statsReqSeq) return
    console.error('Failed to load usage stats:', error)
    inboundEndpointStats.value = []
    upstreamEndpointStats.value = []
    endpointPathStats.value = []
  } finally {
    if (seq === statsReqSeq) endpointStatsLoading.value = false
  }
}

const loadModelStats = async () => {
  const seq = ++modelStatsReqSeq
  modelStatsLoading.value = true
  try {
    const response = await usageAPI.getDashboardModels({
      ...normalizedFilters.value,
      model_source: 'requested',
    })
    if (seq !== modelStatsReqSeq) return
    requestedModelStats.value = response.models || []
    refreshModelOptions(response.models || [])
  } catch (error) {
    if (seq !== modelStatsReqSeq) return
    console.error('Failed to load model stats:', error)
    requestedModelStats.value = []
  } finally {
    if (seq === modelStatsReqSeq) modelStatsLoading.value = false
  }
}

const loadChartData = async () => {
  const seq = ++chartReqSeq
  chartsLoading.value = true
  try {
    const snapshot = await usageAPI.getDashboardSnapshotV2({
      ...normalizedFilters.value,
      granularity: granularity.value,
      include_trend: true,
      include_model_stats: false,
      include_group_stats: true,
    })
    if (seq !== chartReqSeq) return
    trendData.value = snapshot.trend || []
    groupStats.value = snapshot.groups || []
  } catch (error) {
    if (seq !== chartReqSeq) return
    console.error('Failed to load chart data:', error)
    trendData.value = []
    groupStats.value = []
  } finally {
    if (seq === chartReqSeq) chartsLoading.value = false
  }
}

const refreshModelOptions = (models: ModelStat[]) => {
  const current = filters.value.model
  const set = new Set(modelOptionValues.value)
  models.forEach((item) => {
    if (item.model) set.add(item.model)
  })
  if (current) set.add(current)
  modelOptionValues.value = Array.from(set).sort()
}

const applyFilters = () => {
  pagination.page = 1
  void loadLogs()
  void loadStats()
  void loadModelStats()
  void loadChartData()
  resetErrorRows()
}

const refreshData = () => {
  // 刷新时先把还在使用中的快捷时间窗（近24小时等）滑到当前时刻，
  // 不然加载时算出来的窗口把刷新后产生的记录挡在外面，看起来像没刷新。
  const presetRange = dateRangeRef.value?.refreshActivePreset?.() ?? null
  if (presetRange) {
    startDate.value = presetRange.startDate
    endDate.value = presetRange.endDate
    filters.value = { ...filters.value, start_date: presetRange.startDate, end_date: presetRange.endDate }
    pagination.page = 1
  }
  void loadLogs()
  if (inflightOpen.value) void loadInflight(false)
  void loadStats()
  void loadModelStats()
  void loadChartData()
  if (activeTab.value === 'errors') void loadErrors()
}

const resetFilters = () => {
  const range = getLast24HoursRangeDates()
  startDate.value = range.start
  endDate.value = range.end
  filters.value = {
    start_date: range.start,
    end_date: range.end,
    request_type: undefined,
    native_compaction_v2: null,
    billing_type: null,
    billing_mode: null,
  }
  granularity.value = getGranularityForRange(range.start, range.end)
  applyFilters()
  if (activeTab.value === 'errors') {
    errorFilter.value = { model: '', category: '', api_key_id: null, status_code: null, request_id: '' }
    applyErrorFilters()
  }
}

const onDateRangeChange = (range: { startDate: string; endDate: string; preset: string | null }) => {
  startDate.value = range.startDate
  endDate.value = range.endDate
  filters.value.start_date = range.startDate
  filters.value.end_date = range.endDate
  granularity.value = getGranularityForRange(range.startDate, range.endDate)
  applyFilters()
}

const handlePageChange = (page: number) => {
  pagination.page = page
  void loadLogs()
}

const handlePageSizeChange = (pageSize: number) => {
  pagination.page_size = pageSize
  pagination.page = 1
  void loadLogs()
}

const handleSort = (key: string, order: 'asc' | 'desc') => {
  sortState.sort_by = key
  sortState.sort_order = order
  pagination.page = 1
  void loadLogs()
}

const handleIpGeoBatchFailed = () => {
  appStore.showError(t('usage.ipGeo.batchFailed'))
}

const getRequestTypeExportText = (log: UsageLog): string => {
  const requestType = resolveUsageRequestType(log)
  if (requestType === 'cyber') return 'Cyber'
  if (requestType === 'live') return 'Live'
  if (requestType === 'ws_v2') return 'WS'
  if (requestType === 'stream') return 'Stream'
  if (requestType === 'sync') return 'Sync'
  return 'Unknown'
}

const getDisplayBillingMode = (
  row: Pick<UsageLog, 'billing_mode' | 'image_count'> | null | undefined
): string | null | undefined => resolveDisplayBillingMode(row)

const escapeCSVValue = (value: unknown): string => {
  if (value == null) return ''
  const str = String(value)
  const escaped = str.replace(/"/g, '""')
  if (/^[=+\-@\t\r]/.test(str)) return `"\'${escaped}"`
  if (/[,"\n\r]/.test(str)) return `"${escaped}"`
  return str
}

const exportToCSV = async () => {
  if (pagination.total === 0) {
    appStore.showWarning(t('usage.noDataToExport'))
    return
  }
  exporting.value = true
  appStore.showInfo(t('usage.preparingExport'))
  try {
    const exportParams = { ...buildUsageListParams(1, 100), include_inflight: false }
    const allLogs = await fetchPaginatedItems(
      (page, pageSize) => usageAPI.query({ ...exportParams, page, page_size: pageSize }), 100
    )
    if (allLogs.length === 0) {
      appStore.showWarning(t('usage.noDataToExport'))
      return
    }
    const headers = [
      'Time',
      'API Key Name',
      'Model',
      'Reasoning Effort',
      'Inbound Endpoint',
      'IP Address',
      'Type',
      'Billing Mode',
      'Input Tokens',
      'Output Tokens',
      'Cache Read Tokens',
      'Cache Creation Tokens',
      'Rate Multiplier',
      'Billed Cost',
      'Original Cost',
      'First Token (ms)',
      'Duration (ms)',
    ]
    const rows = allLogs.map((log) => [
      log.created_at,
      log.api_key?.name || '',
      log.model,
      formatReasoningEffort(log.reasoning_effort),
      log.inbound_endpoint || '',
      log.ip_address || '',
      getRequestTypeExportText(log),
      getBillingModeLabel(getDisplayBillingMode(log), t),
      log.input_tokens,
      log.output_tokens,
      log.cache_read_tokens,
      log.cache_creation_tokens,
      log.rate_multiplier,
      log.actual_cost.toFixed(8),
      log.total_cost.toFixed(8),
      log.first_token_ms ?? '',
      log.duration_ms ?? '',
    ].map(escapeCSVValue))
    const csvContent = [
      headers.map(escapeCSVValue).join(','),
      ...rows.map((row) => row.join(',')),
    ].join('\n')
    const blob = new Blob(['\uFEFF' + csvContent], { type: 'text/csv;charset=utf-8;' })
    const url = window.URL.createObjectURL(blob)
    const link = document.createElement('a')
    link.href = url
    link.download = `usage_${exportParams.start_date}_to_${exportParams.end_date}.csv`
    link.click()
    window.URL.revokeObjectURL(url)
    appStore.showSuccess(t('usage.exportSuccess'))
  } catch (error) {
    console.error('CSV Export failed:', error)
    appStore.showError(t('usage.exportFailed'))
  } finally {
    exporting.value = false
  }
}

const ALWAYS_VISIBLE = ['created_at']
const DEFAULT_HIDDEN_COLUMNS = ['user_agent']
const HIDDEN_COLUMNS_KEY = 'user-usage-hidden-columns'

const allColumns = computed<Column[]>(() => [
  { key: 'api_key', label: t('usage.apiKeyFilter'), sortable: false },
  { key: 'model', label: t('usage.model'), sortable: true },
  { key: 'reasoning_effort', label: t('usage.reasoningEffort'), sortable: false },
  { key: 'endpoint', label: t('usage.endpoint'), sortable: false },
  { key: 'ip_address', label: 'IP', sortable: false },
  { key: 'group', label: t('admin.usage.group'), sortable: false },
  { key: 'stream', label: t('usage.type'), sortable: false },
  { key: 'billing_mode', label: t('admin.usage.billingMode'), sortable: false },
  { key: 'tokens', label: t('usage.tokens'), sortable: false },
  { key: 'cost', label: t('usage.cost'), sortable: false },
  { key: 'latency', label: t('usage.latency'), sortable: false },
  { key: 'created_at', label: t('usage.time'), sortable: true },
  { key: 'user_agent', label: t('usage.userAgent'), sortable: false },
])

const hiddenColumns = reactive<Set<string>>(new Set())
// 用户仅展示自己的分组与密钥名称，不增加上游账户信息。
const inflightColumnKeys = new Set(['api_key', 'model', 'group', 'stream', 'cost', 'latency', 'created_at'])
const inflightColumns = computed(() => allColumns.value
  .filter(column => inflightColumnKeys.has(column.key))
  .map(column => ({ ...column, sortable: false })))
const toggleableColumns = computed(() => allColumns.value.filter((col) => !ALWAYS_VISIBLE.includes(col.key)))
const visibleColumns = computed(() =>
  allColumns.value.filter((col) => ALWAYS_VISIBLE.includes(col.key) || !hiddenColumns.has(col.key))
)
const isColumnVisible = (key: string) => !hiddenColumns.has(key)
const toggleColumn = (key: string) => {
  if (hiddenColumns.has(key)) hiddenColumns.delete(key)
  else hiddenColumns.add(key)
  localStorage.setItem(HIDDEN_COLUMNS_KEY, JSON.stringify([...hiddenColumns]))
}
const loadSavedColumns = () => {
  try {
    const saved = localStorage.getItem(HIDDEN_COLUMNS_KEY)
    const values = saved ? JSON.parse(saved) as string[] : DEFAULT_HIDDEN_COLUMNS
    values.forEach((key) => hiddenColumns.add(key))
  } catch {
    DEFAULT_HIDDEN_COLUMNS.forEach((key) => hiddenColumns.add(key))
  }
}

// 错误请求 tab 独立列设置(机制同用量列设置,存储互不影响)
const ERR_ALWAYS_VISIBLE = ['status', 'created_at']
const ERR_DEFAULT_HIDDEN_COLUMNS = ['user_agent']
const ERR_HIDDEN_COLUMNS_KEY = 'user-usage-error-hidden-columns'

// key 须与 UserErrorRequestsTable 的 allColumns 一致
const errAllColumns = computed<Column[]>(() => [
  { key: 'key_name', label: t('usage.errors.keyName') },
  { key: 'model', label: t('usage.errors.model') },
  { key: 'endpoint', label: t('usage.errors.endpoint') },
  { key: 'client_ip', label: 'IP' },
  { key: 'group', label: t('admin.usage.group') },
  { key: 'type', label: t('usage.type') },
  { key: 'platform', label: t('usage.errors.platform') },
  { key: 'category', label: t('usage.errors.category') },
  { key: 'status', label: t('usage.errors.status') },
  { key: 'message', label: t('usage.errors.message') },
  { key: 'created_at', label: t('usage.errors.time') },
  { key: 'user_agent', label: t('usage.userAgent') },
])

const errHiddenColumns = reactive<Set<string>>(new Set())
const errToggleableColumns = computed(() =>
  errAllColumns.value.filter((col) => !ERR_ALWAYS_VISIBLE.includes(col.key))
)
const errVisibleColumnKeys = computed(() =>
  errAllColumns.value
    .filter((col) => ERR_ALWAYS_VISIBLE.includes(col.key) || !errHiddenColumns.has(col.key))
    .map((col) => col.key)
)
const isErrColumnVisible = (key: string) => !errHiddenColumns.has(key)
const toggleErrColumn = (key: string) => {
  if (errHiddenColumns.has(key)) errHiddenColumns.delete(key)
  else errHiddenColumns.add(key)
  localStorage.setItem(ERR_HIDDEN_COLUMNS_KEY, JSON.stringify([...errHiddenColumns]))
}
const loadSavedErrColumns = () => {
  try {
    const saved = localStorage.getItem(ERR_HIDDEN_COLUMNS_KEY)
    const values = saved ? (JSON.parse(saved) as string[]) : ERR_DEFAULT_HIDDEN_COLUMNS
    values.forEach((key) => errHiddenColumns.add(key))
  } catch {
    ERR_DEFAULT_HIDDEN_COLUMNS.forEach((key) => errHiddenColumns.add(key))
  }
}

// 列设置下拉按当前 tab 分发
const currentToggleableColumns = computed(() =>
  activeTab.value === 'errors' ? errToggleableColumns.value : toggleableColumns.value
)
const isCurrentColumnVisible = (key: string) =>
  activeTab.value === 'errors' ? isErrColumnVisible(key) : isColumnVisible(key)
const toggleCurrentColumn = (key: string) => {
  if (activeTab.value === 'errors') toggleErrColumn(key)
  else toggleColumn(key)
}

const showColumnDropdown = ref(false)
const columnDropdownRef = ref<HTMLElement | null>(null)
const handleColumnClickOutside = (event: MouseEvent) => {
  if (columnDropdownRef.value && !columnDropdownRef.value.contains(event.target as HTMLElement)) {
    showColumnDropdown.value = false
  }
}

const loadApiKeys = async () => {
  const firstPage = await keysAPI.list(1, 100)
  const keys = [...firstPage.items]
  for (let page = 2; page <= firstPage.pages && keys.length > 0; page++) {
    const response = await keysAPI.list(page, 100)
    if (response.items.length === 0) break
    keys.push(...response.items)
  }
  return keys
}

const loadFilterOptions = async () => {
  try {
    const [keys, availableGroups] = await Promise.all([
      loadApiKeys(),
      userGroupsAPI.getAvailable(),
    ])
    apiKeys.value = keys
    groups.value = availableGroups
  } catch (error) {
    console.error('Failed to load usage filter options:', error)
  }
}

const resetErrorRows = () => {
  errorPage.value = 1
  if (activeTab.value === 'errors') {
    void loadErrors()
  } else {
    errorRows.value = []
    errorTotal.value = 0
  }
}

const loadErrors = async () => {
  errorLoading.value = true
  try {
    const resp = await usageAPI.listMyErrorRequests({
      page: errorPage.value,
      page_size: errorPageSize.value,
      start_date: startDate.value,
      end_date: endDate.value,
      model: (errorFilter.value.model ?? '').trim() || undefined,
      category: errorFilter.value.category || undefined,
      api_key_id: errorFilter.value.api_key_id ?? undefined,
      status_code: errorFilter.value.status_code ?? undefined,
      request_id: errorFilter.value.request_id.trim() || undefined,
      sort_by: errorSortBy.value,
      sort_order: errorSortOrder.value,
    })
    errorRows.value = resp.items
    errorTotal.value = resp.total
  } catch (error) {
    console.error('[UsageView] loadErrors failed:', error)
    appStore.showError(t('usage.errors.failedToLoad'))
  } finally {
    errorLoading.value = false
  }
}

const onErrorSort = (sortBy: string, sortOrder: 'asc' | 'desc') => {
  errorSortBy.value = sortBy
  errorSortOrder.value = sortOrder
  errorPage.value = 1
  void loadErrors()
}

const onErrorPage = (page: number) => {
  errorPage.value = page
  void loadErrors()
}

const onErrorPageSize = (pageSize: number) => {
  errorPageSize.value = pageSize
  errorPage.value = 1
  void loadErrors()
}

const switchToErrors = () => {
  activeTab.value = 'errors'
  if (errorRows.value.length === 0) void loadErrors()
}

onMounted(() => {
  loadSavedColumns()
  loadSavedErrColumns()
  document.addEventListener('click', handleColumnClickOutside)
  void loadFilterOptions()
  refreshData()
})

onUnmounted(() => {
  abortController?.abort()
  document.removeEventListener('click', handleColumnClickOutside)
})

watch(endpointDistributionSource, () => {
  // Endpoint source switching is handled by the chart component using already loaded stats.
})
</script>

<style scoped>
/* Stat strip: one card with hairline dividers (the cards keep their own dividers). */
.usage-stats-strip { background: var(--ui-bg); }
.usage-stats-strip :deep(.signal-usage-metrics) { border: 0; }
.usage-stats-strip :deep(.signal-usage-metrics > .card) { border-radius: 0; box-shadow: none; background: transparent; }
/* Usage / error records switch: neutral track with a raised pill. */
.usage-tabs { display: inline-flex; gap: 2px; padding: 3px; border-radius: 8px; background: var(--ui-bg-elevated); }
.usage-tab { min-height: 32px; padding: 4px 14px; border-radius: 6px; font-size: 13px; font-weight: 500; color: var(--ui-text-muted); transition: background-color 150ms ease, color 150ms ease, box-shadow 150ms ease; }
.usage-tab:hover { color: var(--ui-text-highlighted); }
.usage-tab:focus-visible { outline: 2px solid var(--ui-border-inverted); outline-offset: 1px; }
.usage-tab[aria-selected='true'] { background: var(--ui-bg); color: var(--ui-text-highlighted); box-shadow: 0 1px 2px rgba(10, 10, 10, 0.08), 0 0 0 1px var(--ui-border); }
@media (prefers-reduced-motion: reduce) { .usage-tab { transition: none; } }
</style>
