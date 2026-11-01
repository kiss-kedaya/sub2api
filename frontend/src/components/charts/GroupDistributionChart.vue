<template>
  <UCard :ui="{ header: 'flex flex-wrap items-center justify-between gap-3 px-4 py-3 sm:px-5', body: 'p-4 sm:p-5' }">
    <template #header>
      <h3 class="text-xs font-medium uppercase text-muted">
        {{ t('admin.dashboard.groupDistribution') }}
      </h3>
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
    </template>

    <div v-if="loading" class="flex h-48 items-center justify-center">
      <LoadingSpinner />
    </div>
    <DistributionPanel
      v-else-if="displayGroupStats.length > 0 && chartData"
      :chart-data="chartData"
      :chart-options="doughnutOptions"
      :rows="displayGroupStats"
      :columns="columns"
      :get-row-id="(group) => `group-${group.group_id}`"
      :expanded-id="expandedKey"
      :is-selectable="enableBreakdown ? (group) => group.group_id > 0 : undefined"
      @select="(group) => toggleBreakdown('group', group.group_id)"
    >
      <template #group-cell="{ row }">
        <span
          class="inline-flex max-w-[100px] items-center gap-1 truncate font-medium"
          :class="enableBreakdown && row.original.group_id > 0 ? 'text-primary' : 'text-highlighted'"
          :title="row.original.group_name || String(row.original.group_id)"
        >
          <UIcon
            v-if="enableBreakdown && row.original.group_id > 0"
            :name="expandedKey === `group-${row.original.group_id}` ? 'i-lucide-chevron-down' : 'i-lucide-chevron-right'"
            class="size-3 shrink-0"
          />
          <span class="truncate">{{ row.original.group_name || t('admin.dashboard.noGroup') }}</span>
        </span>
      </template>
      <!-- User breakdown sub-rows -->
      <template #expanded>
        <UserBreakdownSubTable
          :items="breakdownItems"
          :loading="breakdownLoading"
          :show-account-cost="showAccountCost"
        />
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
import type { GroupStat, UserBreakdownItem } from '@/types'
import { getUserBreakdown } from '@/api/admin/dashboard'

ChartJS.register(ArcElement, Tooltip, Legend)

const { t } = useI18n()

type DistributionMetric = 'tokens' | 'actual_cost'

const props = withDefaults(defineProps<{
  groupStats: GroupStat[]
  loading?: boolean
  animationDuration?: number
  metric?: DistributionMetric
  showMetricToggle?: boolean
  enableBreakdown?: boolean
  showAccountCost?: boolean
  startDate?: string
  endDate?: string
  filters?: Record<string, any>
}>(), {
  loading: false,
  metric: 'tokens',
  showMetricToggle: false,
  enableBreakdown: true,
  showAccountCost: true,
})

const emit = defineEmits<{
  'update:metric': [value: DistributionMetric]
}>()

const expandedKey = ref<string | null>(null)
const breakdownItems = ref<UserBreakdownItem[]>([])
const breakdownLoading = ref(false)
const showAccountCost = computed(() => props.showAccountCost)

const toggleBreakdown = async (type: string, id: number | string) => {
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
      group_id: Number(id),
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

const metricItems = computed(() => [
  { label: t('admin.dashboard.metricTokens'), value: 'tokens' },
  { label: t('admin.dashboard.metricActualCost'), value: 'actual_cost' }
])

const numberCell = (value: string, tone = 'text-muted') => h('span', { class: `block text-right tabular-nums ${tone}` }, value)
const rightHeader = (label: string) => () => h('span', { class: 'block text-right' }, label)

const columns = computed<TableColumn<GroupStat>[]>(() => [
  { id: 'group', header: t('admin.dashboard.group') },
  { id: 'requests', header: rightHeader(t('admin.dashboard.requests')), cell: ({ row }) => numberCell(formatNumber(row.original.requests)) },
  { id: 'tokens', header: rightHeader(t('admin.dashboard.tokens')), cell: ({ row }) => numberCell(formatTokens(row.original.total_tokens)) },
  { id: 'actual', header: rightHeader(t('admin.dashboard.actual')), cell: ({ row }) => numberCell(`$${formatCost(row.original.actual_cost)}`, 'text-success') },
  ...(showAccountCost.value
    ? [{ id: 'account', header: rightHeader(t('admin.dashboard.accountCost')), cell: ({ row }: { row: { original: GroupStat } }) => numberCell(`$${formatCost(row.original.account_cost)}`, 'text-warning') }]
    : []),
  { id: 'standard', header: rightHeader(t('admin.dashboard.standard')), cell: ({ row }) => numberCell(`$${formatCost(row.original.cost)}`, 'text-dimmed') }
])

const displayGroupStats = computed(() => {
  if (!props.groupStats?.length) return []

  const metricKey = props.metric === 'actual_cost' ? 'actual_cost' : 'total_tokens'
  return [...props.groupStats].sort((a, b) => toFiniteNumber(b[metricKey]) - toFiniteNumber(a[metricKey]))
})

const chartData = computed(() => {
  if (!props.groupStats?.length) return null

  return {
    labels: displayGroupStats.value.map((g) => g.group_name || String(g.group_id)),
    datasets: [
      doughnutDataset(
        theme.value,
        displayGroupStats.value.map((g) => toFiniteNumber(props.metric === 'actual_cost' ? g.actual_cost : g.total_tokens)),
        displayGroupStats.value.map((_, index) => theme.value.categorical[index % theme.value.categorical.length])
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
  return toFiniteNumber(value).toLocaleString()
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
