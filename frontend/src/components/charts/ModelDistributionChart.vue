<template>
  <UCard :ui="{ header: 'flex flex-wrap items-center justify-between gap-3 px-4 py-3 sm:px-5', body: 'p-4 sm:p-5' }">
    <template #header>
      <h3 class="text-xs font-medium uppercase text-muted">
        {{ !enableRankingView || activeView === 'model_distribution'
          ? t('admin.dashboard.modelDistribution')
          : t('admin.dashboard.spendingRankingTitle') }}
      </h3>
      <div class="flex flex-wrap items-center justify-end gap-2">
        <UTabs
          v-if="showSourceToggle"
          :model-value="source"
          :items="sourceItems"
          :content="false"
          color="neutral"
          size="xs"
          :ui="segmentedTabsUi"
          @update:model-value="(value) => emit('update:source', value as ModelSource)"
        />
        <UTabs
          v-if="showMetricToggle"
          :model-value="metric"
          :items="metricItems"
          :content="false"
          color="neutral"
          size="xs"
          :ui="segmentedTabsUi"
          @update:model-value="(value) => emit('update:metric', value as DistributionMetric)"
        />
        <UTabs
          v-if="enableRankingView"
          v-model="activeView"
          :items="viewItems"
          :content="false"
          color="neutral"
          size="xs"
          :ui="segmentedTabsUi"
        />
      </div>
    </template>

    <div v-if="activeView === 'model_distribution' && loading" class="flex h-48 items-center justify-center">
      <LoadingSpinner />
    </div>
    <DistributionPanel
      v-else-if="activeView === 'model_distribution' && displayModelStats.length > 0 && chartData"
      :chart-data="chartData"
      :chart-options="doughnutOptions"
      :rows="displayModelStats"
      :columns="modelColumns"
      :get-row-id="(model) => `model-${model.model}`"
      :expanded-id="expandedKey"
      :is-selectable="enableBreakdown ? () => true : undefined"
      @select="(model) => toggleBreakdown('model', model.model)"
    >
      <template #model-cell="{ row }">
        <span
          class="inline-flex max-w-[100px] items-center gap-1 truncate font-medium"
          :class="enableBreakdown ? 'text-primary' : 'text-highlighted'"
          :title="row.original.model"
        >
          <UIcon
            v-if="enableBreakdown"
            :name="expandedKey === `model-${row.original.model}` ? 'i-lucide-chevron-down' : 'i-lucide-chevron-right'"
            class="size-3 shrink-0"
          />
          <span class="truncate">{{ row.original.model }}</span>
        </span>
      </template>
      <template #expanded>
        <UserBreakdownSubTable
          :items="breakdownItems"
          :loading="breakdownLoading"
          :show-account-cost="showAccountCost"
        />
      </template>
    </DistributionPanel>
    <div v-else-if="activeView === 'model_distribution'" class="flex h-48 items-center justify-center text-sm text-muted">
      {{ t('admin.dashboard.noDataAvailable') }}
    </div>

    <div v-else-if="rankingLoading" class="flex h-48 items-center justify-center">
      <LoadingSpinner />
    </div>
    <div v-else-if="rankingError" class="flex h-48 items-center justify-center text-sm text-muted">
      {{ t('admin.dashboard.failedToLoad') }}
    </div>
    <DistributionPanel
      v-else-if="rankingDisplayItems.length > 0 && rankingChartData"
      :chart-data="rankingChartData"
      :chart-options="rankingDoughnutOptions"
      :rows="rankingDisplayItems"
      :columns="rankingColumns"
      :get-row-id="rankingRowId"
      :is-selectable="(item) => !item.isOther"
      @select="(item) => emit('ranking-click', item)"
    >
      <template #user-cell="{ row }">
        <span class="flex min-w-0 items-center gap-2">
          <span class="shrink-0 text-[11px] font-semibold text-dimmed tabular-nums">
            {{ row.original.isOther ? 'Σ' : `#${row.index + 1}` }}
          </span>
          <span class="block max-w-[140px] truncate font-medium text-highlighted" :title="getRankingRowLabel(row.original)">
            {{ getRankingRowLabel(row.original) }}
          </span>
        </span>
      </template>
    </DistributionPanel>
    <div v-else class="flex h-48 items-center justify-center text-sm text-muted">
      {{ t('admin.dashboard.noDataAvailable') }}
    </div>
  </UCard>
</template>

<script setup lang="ts">
import { computed, h, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { Chart as ChartJS, ArcElement, Tooltip, Legend } from 'chart.js'
import type { TableColumn } from '@nuxt/ui'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import UserBreakdownSubTable from './UserBreakdownSubTable.vue'
import DistributionPanel from './DistributionPanel.vue'
import { chartTooltipStyle, doughnutDataset, segmentedTabsUi, useChartTheme } from './chartTheme'
import type { ModelStat, UserSpendingRankingItem, UserBreakdownItem } from '@/types'
import { getUserBreakdown } from '@/api/admin/dashboard'

ChartJS.register(ArcElement, Tooltip, Legend)

const { t } = useI18n()

type DistributionMetric = 'tokens' | 'actual_cost'
type ModelSource = 'requested' | 'upstream' | 'mapping'
type RankingDisplayItem = UserSpendingRankingItem & { isOther?: boolean }
const props = withDefaults(defineProps<{
  modelStats: ModelStat[]
  upstreamModelStats?: ModelStat[]
  mappingModelStats?: ModelStat[]
  source?: ModelSource
  enableRankingView?: boolean
  rankingItems?: UserSpendingRankingItem[]
  rankingTotalActualCost?: number
  rankingTotalRequests?: number
  rankingTotalTokens?: number
  loading?: boolean
  animationDuration?: number
  metric?: DistributionMetric
  showSourceToggle?: boolean
  showMetricToggle?: boolean
  enableBreakdown?: boolean
  showAccountCost?: boolean
  rankingLoading?: boolean
  rankingError?: boolean
  startDate?: string
  endDate?: string
  filters?: Record<string, any>
}>(), {
  upstreamModelStats: () => [],
  mappingModelStats: () => [],
  source: 'requested',
  enableRankingView: false,
  rankingItems: () => [],
  rankingTotalActualCost: 0,
  rankingTotalRequests: 0,
  rankingTotalTokens: 0,
  loading: false,
  metric: 'tokens',
  showSourceToggle: false,
  showMetricToggle: false,
  enableBreakdown: true,
  showAccountCost: true,
  rankingLoading: false,
  rankingError: false
})

const expandedKey = ref<string | null>(null)
const breakdownItems = ref<UserBreakdownItem[]>([])
const breakdownLoading = ref(false)

const toggleBreakdown = async (type: string, id: string) => {
  const key = `${type}-${id}`
  if (expandedKey.value === key) {
    expandedKey.value = null
    return
  }
  expandedKey.value = key
  breakdownLoading.value = true
  breakdownItems.value = []
  try {
    const res = await getUserBreakdown({
      ...props.filters,
      start_date: props.startDate,
      end_date: props.endDate,
      model: id,
      model_source: props.source,
    })
    breakdownItems.value = res.users || []
  } catch {
    breakdownItems.value = []
  } finally {
    breakdownLoading.value = false
  }
}

const emit = defineEmits<{
  'update:metric': [value: DistributionMetric]
  'update:source': [value: ModelSource]
  'ranking-click': [item: UserSpendingRankingItem]
}>()

const enableRankingView = computed(() => props.enableRankingView)
const showAccountCost = computed(() => props.showAccountCost)
const activeView = ref<'model_distribution' | 'spending_ranking'>('model_distribution')

const theme = useChartTheme()
const paletteColor = (index: number) => theme.value.categorical[index % theme.value.categorical.length]

const sourceItems = computed(() => [
  { label: t('usage.requestedModel'), value: 'requested' },
  { label: t('usage.upstreamModel'), value: 'upstream' },
  { label: t('usage.mapping'), value: 'mapping' }
])
const metricItems = computed(() => [
  { label: t('admin.dashboard.metricTokens'), value: 'tokens' },
  { label: t('admin.dashboard.metricActualCost'), value: 'actual_cost' }
])
const viewItems = computed(() => [
  { label: t('admin.dashboard.viewModelDistribution'), value: 'model_distribution' },
  { label: t('admin.dashboard.viewSpendingRanking'), value: 'spending_ranking' }
])

const numberCell = (value: string, tone = 'text-muted') => h('span', { class: `block text-right tabular-nums ${tone}` }, value)
const rightHeader = (label: string) => () => h('span', { class: 'block text-right' }, label)

const modelColumns = computed<TableColumn<ModelStat>[]>(() => [
  { id: 'model', header: t('admin.dashboard.model') },
  { id: 'requests', header: rightHeader(t('admin.dashboard.requests')), cell: ({ row }) => numberCell(formatNumber(row.original.requests)) },
  { id: 'tokens', header: rightHeader(t('admin.dashboard.tokens')), cell: ({ row }) => numberCell(formatTokens(row.original.total_tokens)) },
  { id: 'actual', header: rightHeader(t('admin.dashboard.actual')), cell: ({ row }) => numberCell(`$${formatCost(row.original.actual_cost)}`, 'text-success') },
  ...(showAccountCost.value
    ? [{ id: 'account', header: rightHeader(t('admin.dashboard.accountCost')), cell: ({ row }: { row: { original: ModelStat } }) => numberCell(`$${formatCost(row.original.account_cost)}`, 'text-warning') }]
    : []),
  { id: 'standard', header: rightHeader(t('admin.dashboard.standard')), cell: ({ row }) => numberCell(`$${formatCost(row.original.cost)}`, 'text-dimmed') }
])

const rankingColumns = computed<TableColumn<RankingDisplayItem>[]>(() => [
  { id: 'user', header: t('admin.dashboard.spendingRankingUser') },
  { id: 'requests', header: rightHeader(t('admin.dashboard.spendingRankingRequests')), cell: ({ row }) => numberCell(formatNumber(row.original.requests)) },
  { id: 'tokens', header: rightHeader(t('admin.dashboard.spendingRankingTokens')), cell: ({ row }) => numberCell(formatTokens(row.original.tokens)) },
  { id: 'spend', header: rightHeader(t('admin.dashboard.spendingRankingSpend')), cell: ({ row }) => numberCell(`$${formatCost(row.original.actual_cost)}`, 'text-success') }
])

const rankingRowId = (item: RankingDisplayItem, index: number) => (item.isOther ? 'others' : `${item.user_id}-${index}`)

const displayModelStats = computed(() => {
  const sourceStats = props.source === 'upstream'
    ? props.upstreamModelStats
    : props.source === 'mapping'
      ? props.mappingModelStats
      : props.modelStats
  if (!sourceStats?.length) return []

  const metricKey = props.metric === 'actual_cost' ? 'actual_cost' : 'total_tokens'
  return [...sourceStats].sort((a, b) => toFiniteNumber(b[metricKey]) - toFiniteNumber(a[metricKey]))
})

const chartData = computed(() => {
  if (!displayModelStats.value.length) return null

  return {
    labels: displayModelStats.value.map((m) => m.model),
    datasets: [
      doughnutDataset(
        theme.value,
        displayModelStats.value.map((m) => toFiniteNumber(props.metric === 'actual_cost' ? m.actual_cost : m.total_tokens)),
        displayModelStats.value.map((_, index) => paletteColor(index))
      )
    ]
  }
})

const rankingChartData = computed(() => {
  if (!props.rankingItems?.length) return null

  const labels = props.rankingItems.map((item, index) => `#${index + 1} ${getRankingUserLabel(item)}`)
  const data = props.rankingItems.map((item) => toFiniteNumber(item.actual_cost))
  const backgroundColor = props.rankingItems.map((_, index) => paletteColor(index))

  if (otherRankingItem.value) {
    labels.push(t('admin.dashboard.spendingRankingOther'))
    data.push(otherRankingItem.value.actual_cost)
    // "Others" always gets the dimmed neutral, never a palette colour.
    backgroundColor.push(theme.value.other)
  }

  return {
    labels,
    datasets: [doughnutDataset(theme.value, data, backgroundColor)]
  }
})

const otherRankingItem = computed<RankingDisplayItem | null>(() => {
  if (!props.rankingItems?.length) return null

  const rankedActualCost = props.rankingItems.reduce((sum, item) => sum + toFiniteNumber(item.actual_cost), 0)
  const rankedRequests = props.rankingItems.reduce((sum, item) => sum + toFiniteNumber(item.requests), 0)
  const rankedTokens = props.rankingItems.reduce((sum, item) => sum + toFiniteNumber(item.tokens), 0)

  const otherActualCost = Math.max((props.rankingTotalActualCost || 0) - rankedActualCost, 0)
  const otherRequests = Math.max((props.rankingTotalRequests || 0) - rankedRequests, 0)
  const otherTokens = Math.max((props.rankingTotalTokens || 0) - rankedTokens, 0)

  if (otherActualCost <= 0.000001 && otherRequests <= 0 && otherTokens <= 0) return null

  return {
    user_id: 0,
    email: '',
    username: '',
    actual_cost: otherActualCost,
    requests: otherRequests,
    tokens: otherTokens,
    isOther: true
  }
})

const rankingDisplayItems = computed<RankingDisplayItem[]>(() => {
  if (!props.rankingItems?.length) return []
  return otherRankingItem.value
    ? [...props.rankingItems, otherRankingItem.value]
    : [...props.rankingItems]
})

const doughnutOptions = computed(() => ({
  ...(props.animationDuration === undefined ? {} : { animation: { duration: props.animationDuration } }),
  responsive: true,
  maintainAspectRatio: false,
  cutout: '62%',
  plugins: {
    legend: {
      display: false
    },
    tooltip: {
      ...chartTooltipStyle(theme.value),
      callbacks: {
        label: (context: any) => {
          const value = context.raw as number
          const total = context.dataset.data.reduce((a: number, b: number) => a + b, 0)
          const percentage = total > 0 ? ((value / total) * 100).toFixed(1) : '0.0'
          const formattedValue = props.metric === 'actual_cost'
            ? `$${formatCost(value)}`
            : formatTokens(value)
          return `${context.label}: ${formattedValue} (${percentage}%)`
        }
      }
    }
  }
}))

const rankingDoughnutOptions = computed(() => ({
  ...(props.animationDuration === undefined ? {} : { animation: { duration: props.animationDuration } }),
  responsive: true,
  maintainAspectRatio: false,
  cutout: '62%',
  plugins: {
    legend: {
      display: false
    },
    tooltip: {
      ...chartTooltipStyle(theme.value),
      callbacks: {
        label: (context: any) => {
          const value = context.raw as number
          const total = context.dataset.data.reduce((a: number, b: number) => a + b, 0)
          const percentage = total > 0 ? ((value / total) * 100).toFixed(1) : '0.0'
          return `${context.label}: $${formatCost(value)} (${percentage}%)`
        }
      }
    }
  }
}))

const formatTokens = (value: number): string => {
  if (value >= 1_000_000_000) {
    return `${(value / 1_000_000_000).toFixed(2)}B`
  } else if (value >= 1_000_000) {
    return `${(value / 1_000_000).toFixed(2)}M`
  } else if (value >= 1_000) {
    return `${(value / 1_000).toFixed(2)}K`
  }
  return value.toLocaleString()
}

const formatNumber = (value: number): string => {
  return toFiniteNumber(value).toLocaleString()
}

const getRankingUserLabel = (item: UserSpendingRankingItem): string => {
  if (item.username?.trim()) return item.username.trim()
  if (item.email?.trim()) return item.email.trim()
  return t('admin.redeem.userPrefix', { id: item.user_id })
}

const getRankingRowLabel = (item: RankingDisplayItem): string => {
  if (item.isOther) return t('admin.dashboard.spendingRankingOther')
  return getRankingUserLabel(item)
}

const toFiniteNumber = (value: unknown): number => {
  const numberValue = Number(value)
  return Number.isFinite(numberValue) ? numberValue : 0
}

const formatCost = (value: number | null | undefined): string => {
  const safeValue = toFiniteNumber(value)
  if (safeValue >= 1000) {
    return (safeValue / 1000).toFixed(2) + 'K'
  } else if (safeValue >= 1) {
    return safeValue.toFixed(2)
  } else if (safeValue >= 0.01) {
    return safeValue.toFixed(3)
  }
  return safeValue.toFixed(4)
}
</script>
