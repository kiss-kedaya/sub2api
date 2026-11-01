<template>
  <!-- 用量页"用户排行"tab 内容：无卡片外观，依赖父级统一卡片；筛选/时间范围复用页面级筛选栏 -->
  <div class="flex flex-col gap-3">
    <!-- Toolbar -->
    <div class="flex flex-wrap items-center justify-between gap-3">
      <p class="text-xs text-muted">{{ t('admin.usage.tokenRanking.subtitle') }}</p>
      <div class="flex items-center gap-3">
        <span v-if="!loading && items.length > 0" class="text-xs text-muted">
          {{ t('admin.usage.tokenRanking.userCount', { count: items.length }) }}
        </span>
        <div class="w-28">
          <Select v-model="limit" :options="limitOptions" @change="load" />
        </div>
      </div>
    </div>

    <!-- Table -->
    <UTable
      :data="items"
      :columns="tableColumns"
      :loading="loading"
      :ui="{ ...tableUi, tr: 'cursor-pointer', td: 'border-b border-default whitespace-nowrap' }"
      @select="onRowSelect"
    >
      <template v-for="col in sortableHeaderSlots" :key="col.key" #[col.slot]>
        <UButton
          color="neutral"
          variant="ghost"
          size="xs"
          class="-mx-2 ms-auto"
          :class="sortBy === col.key ? 'text-highlighted' : 'text-muted'"
          :trailing-icon="sortBy === col.key ? 'i-lucide-arrow-down' : undefined"
          :label="t(col.label)"
          @click="setSort(col.key)"
        />
      </template>
      <template #rank-cell="{ row }">
        <UBadge v-if="row.index < 3" :color="row.index === 0 ? 'warning' : 'neutral'" :variant="row.index === 0 ? 'subtle' : 'outline'" size="sm" class="w-6 justify-center tabular-nums">{{ row.index + 1 }}</UBadge>
        <span v-else class="inline-block w-6 text-center text-sm tabular-nums text-dimmed">{{ row.index + 1 }}</span>
      </template>
      <template #user-cell="{ row }">
        <span class="block max-w-[260px] truncate text-sm font-medium text-highlighted" :title="row.original.email">
          {{ row.original.email || `User #${row.original.user_id}` }}
          <span class="ml-1 font-normal text-dimmed">#{{ row.original.user_id }}</span>
        </span>
      </template>
      <template #loading>
        <div class="py-12 text-center"><LoadingSpinner /></div>
      </template>
      <template #empty>
        <p class="py-12 text-center text-sm text-muted">{{ t('admin.dashboard.noDataAvailable') }}</p>
      </template>
    </UTable>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import type { TableColumn, TableRow } from '@nuxt/ui'
import { templateTableUi } from '@/views/admin/ops/utils/templateUi'
import { useI18n } from 'vue-i18n'
import { getUserBreakdown, type UserBreakdownParams } from '@/api/admin/dashboard'
import { formatCompactNumber, formatCostFixed } from '@/utils/format'
import type { UserBreakdownItem } from '@/types'
import Select from '@/components/common/Select.vue'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'

const props = defineProps<{
  startDate: string
  endDate: string
  filters: Record<string, unknown>
  model?: string
}>()

const emit = defineEmits<{ (e: 'select-user', userId: number, email: string): void }>()

const { t } = useI18n()
const tableUi = templateTableUi

type SortKey = NonNullable<UserBreakdownParams['sort_by']>
const sortableColumns: { key: SortKey; label: string }[] = [
  { key: 'requests', label: 'admin.usage.tokenRanking.columns.requests' },
  { key: 'input_tokens', label: 'admin.usage.tokenRanking.columns.inputTokens' },
  { key: 'output_tokens', label: 'admin.usage.tokenRanking.columns.outputTokens' },
  { key: 'cache_tokens', label: 'admin.usage.tokenRanking.columns.cacheTokens' },
  { key: 'total_tokens', label: 'admin.usage.tokenRanking.columns.totalTokens' },
  { key: 'actual_cost', label: 'admin.usage.tokenRanking.columns.cost' },
]

const tableColumns = computed<TableColumn<UserBreakdownItem>[]>(() => [
  { id: 'rank', header: '#', meta: { class: { th: 'w-16', td: 'w-16' } } },
  { id: 'user', header: t('admin.usage.tokenRanking.columns.user') },
  { id: 'requests', header: t('admin.usage.tokenRanking.columns.requests'), meta: { class: { th: 'text-right', td: 'text-right tabular-nums text-muted' } }, cell: ({ row }) => row.original.requests.toLocaleString() },
  { id: 'input_tokens', header: t('admin.usage.tokenRanking.columns.inputTokens'), meta: { class: { th: 'text-right', td: 'text-right tabular-nums text-muted' } }, cell: ({ row }) => fmtTokens(row.original.input_tokens) },
  { id: 'output_tokens', header: t('admin.usage.tokenRanking.columns.outputTokens'), meta: { class: { th: 'text-right', td: 'text-right tabular-nums text-muted' } }, cell: ({ row }) => fmtTokens(row.original.output_tokens) },
  { id: 'cache_tokens', header: t('admin.usage.tokenRanking.columns.cacheTokens'), meta: { class: { th: 'text-right', td: 'text-right tabular-nums text-muted' } }, cell: ({ row }) => fmtTokens(row.original.cache_tokens) },
  { id: 'total_tokens', header: t('admin.usage.tokenRanking.columns.totalTokens'), meta: { class: { th: 'text-right', td: 'text-right font-medium tabular-nums text-highlighted' } }, cell: ({ row }) => fmtTokens(row.original.total_tokens) },
  { id: 'actual_cost', header: t('admin.usage.tokenRanking.columns.cost'), meta: { class: { th: 'text-right', td: 'text-right font-medium tabular-nums text-highlighted' } }, cell: ({ row }) => '$' + fmtCost(row.original.actual_cost) },
])
const sortableHeaderSlots = sortableColumns.map(col => ({ ...col, slot: col.key + '-header' }))
function onRowSelect(_e: Event, row: TableRow<UserBreakdownItem>) {
  emit('select-user', row.original.user_id, row.original.email)
}

const limitOptions = [
  { value: 20, label: 'Top 20' },
  { value: 50, label: 'Top 50' },
  { value: 100, label: 'Top 100' },
  { value: 200, label: 'Top 200' },
]


const items = ref<UserBreakdownItem[]>([])
const loading = ref(false)
const sortBy = ref<SortKey>('total_tokens')
const limit = ref(50)
let reqSeq = 0

const fmtTokens = (v: number) => formatCompactNumber(v)
const fmtCost = (v: number) => formatCostFixed(v, 4)

const setSort = (key: SortKey) => {
  if (sortBy.value === key) return
  sortBy.value = key
  load()
}

const load = async () => {
  const seq = ++reqSeq
  loading.value = true
  try {
    const params: UserBreakdownParams = {
      ...props.filters,
      start_date: props.startDate,
      end_date: props.endDate,
      sort_by: sortBy.value,
      limit: limit.value,
    }
    if (props.model) params.model = props.model
    const res = await getUserBreakdown(params)
    if (seq !== reqSeq) return
    items.value = res.users || []
  } catch {
    if (seq !== reqSeq) return
    items.value = []
  } finally {
    if (seq === reqSeq) loading.value = false
  }
}

// Reload when the shared filters / date range / model change.
watch(
  () => [props.startDate, props.endDate, props.model, JSON.stringify(props.filters)],
  () => load(),
  { immediate: true }
)

defineExpose({ reload: load })
</script>
