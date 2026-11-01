<template>
  <UCard :ui="{ header: 'flex flex-wrap items-center justify-between gap-3 px-4 py-3 sm:px-5', body: 'p-4 sm:p-5' }">
    <template #header>
      <h3 class="text-xs font-medium uppercase text-muted">
        {{ title || t('usage.endpointDistribution') }}
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
          @update:model-value="onSourceChange"
        />
        <UTabs
          v-if="showMetricToggle"
          :model-value="metric"
          :items="metricItems"
          :content="false"
          color="neutral"
          size="xs"
          :ui="segmentedTabsUi"
          @update:model-value="onMetricChange"
        />
      </div>
    </template>

    <div v-if="loading" class="flex h-48 items-center justify-center">
      <LoadingSpinner />
    </div>
    <DistributionPanel
      v-else-if="displayEndpointStats.length > 0 && chartData"
      :chart-data="chartData"
      :chart-options="doughnutOptions"
      :rows="displayEndpointStats"
      :columns="columns"
      :get-row-id="(item) => item.endpoint"
      :expanded-id="expandedKey"
      :is-selectable="enableBreakdown ? () => true : undefined"
      @select="(item) => toggleBreakdown(item.endpoint)"
    >
      <template #endpoint-cell="{ row }">
        <span
          class="inline-flex max-w-[180px] items-center gap-1 truncate font-medium"
          :class="enableBreakdown ? 'text-primary' : 'text-highlighted'"
          :title="row.original.endpoint"
        >
          <UIcon
            v-if="enableBreakdown"
            :name="expandedKey === row.original.endpoint ? 'i-lucide-chevron-down' : 'i-lucide-chevron-right'"
            class="size-3 shrink-0"
          />
          <span class="truncate">{{ row.original.endpoint }}</span>
        </span>
      </template>
      <template #expanded>
        <UserBreakdownSubTable :items="breakdownItems" :loading="breakdownLoading" />
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
import type { EndpointStat, UserBreakdownItem } from '@/types'
import { getUserBreakdown } from '@/api/admin/dashboard'

ChartJS.register(ArcElement, Tooltip, Legend)

const { t } = useI18n()

type DistributionMetric = 'tokens' | 'actual_cost'
type EndpointSource = 'inbound' | 'upstream' | 'path'

const props = withDefaults(
  defineProps<{
    endpointStats: EndpointStat[]
    upstreamEndpointStats?: EndpointStat[]
    endpointPathStats?: EndpointStat[]
    loading?: boolean
    animationDuration?: number
    title?: string
    metric?: DistributionMetric
    source?: EndpointSource
    showMetricToggle?: boolean
    showSourceToggle?: boolean
    enableBreakdown?: boolean
    startDate?: string
    endDate?: string
    filters?: Record<string, any>
  }>(),
  {
    upstreamEndpointStats: () => [],
    endpointPathStats: () => [],
    loading: false,
    title: '',
    metric: 'tokens',
    source: 'inbound',
    showMetricToggle: false,
    showSourceToggle: false,
    enableBreakdown: true
  }
)

const emit = defineEmits<{
  'update:metric': [value: DistributionMetric]
  'update:source': [value: EndpointSource]
}>()

const expandedKey = ref<string | null>(null)
const breakdownItems = ref<UserBreakdownItem[]>([])
const breakdownLoading = ref(false)

const toggleBreakdown = async (endpoint: string) => {
  if (expandedKey.value === endpoint) {
    expandedKey.value = null
    return
  }
  expandedKey.value = endpoint
  breakdownLoading.value = true
  breakdownItems.value = []
  try {
    const res = await getUserBreakdown({
      ...props.filters,
      start_date: props.startDate,
      end_date: props.endDate,
      endpoint,
      endpoint_type: props.source,
    })
    breakdownItems.value = res.users || []
  } catch {
    breakdownItems.value = []
  } finally {
    breakdownLoading.value = false
  }
}

const theme = useChartTheme()
const onMetricChange = (value: string | number) => emit('update:metric', value as DistributionMetric)
const onSourceChange = (value: string | number) => emit('update:source', value as EndpointSource)

const sourceItems = computed(() => [
  { label: t('usage.inbound'), value: 'inbound' },
  { label: t('usage.upstream'), value: 'upstream' },
  { label: t('usage.path'), value: 'path' }
])
const metricItems = computed(() => [
  { label: t('admin.dashboard.metricTokens'), value: 'tokens' },
  { label: t('admin.dashboard.metricActualCost'), value: 'actual_cost' }
])

const numberCell = (value: string, tone = 'text-muted') => h('span', { class: `block text-right tabular-nums ${tone}` }, value)
const rightHeader = (label: string) => () => h('span', { class: 'block text-right' }, label)

const columns = computed<TableColumn<EndpointStat>[]>(() => [
  { id: 'endpoint', header: t('usage.endpoint') },
  { id: 'requests', header: rightHeader(t('admin.dashboard.requests')), cell: ({ row }) => numberCell(formatNumber(row.original.requests)) },
  { id: 'tokens', header: rightHeader(t('admin.dashboard.tokens')), cell: ({ row }) => numberCell(formatTokens(row.original.total_tokens)) },
  { id: 'actual', header: rightHeader(t('admin.dashboard.actual')), cell: ({ row }) => numberCell(`$${formatCost(row.original.actual_cost)}`, 'text-success') },
  { id: 'standard', header: rightHeader(t('admin.dashboard.standard')), cell: ({ row }) => numberCell(`$${formatCost(row.original.cost)}`, 'text-dimmed') }
])

const displayEndpointStats = computed(() => {
  const sourceStats = props.source === 'upstream'
    ? props.upstreamEndpointStats
    : props.source === 'path'
      ? props.endpointPathStats
      : props.endpointStats
  if (!sourceStats?.length) return []

  const metricKey = props.metric === 'actual_cost' ? 'actual_cost' : 'total_tokens'
  return [...sourceStats].sort((a, b) => b[metricKey] - a[metricKey])
})

const chartData = computed(() => {
  if (!displayEndpointStats.value?.length) return null

  return {
    labels: displayEndpointStats.value.map((item) => item.endpoint),
    datasets: [
      doughnutDataset(
        theme.value,
        displayEndpointStats.value.map((item) =>
          props.metric === 'actual_cost' ? item.actual_cost : item.total_tokens
        ),
        displayEndpointStats.value.map((_, index) => theme.value.categorical[index % theme.value.categorical.length])
      )
    ]
  }
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
  return value.toLocaleString()
}

const formatCost = (value: number): string => {
  if (value >= 1000) {
    return (value / 1000).toFixed(2) + 'K'
  } else if (value >= 1) {
    return value.toFixed(2)
  } else if (value >= 0.01) {
    return value.toFixed(3)
  }
  return value.toFixed(4)
}
</script>
