<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { Chart as ChartJS, CategoryScale, Filler, Legend, LineElement, LinearScale, PointElement, Title, Tooltip } from 'chart.js'
import { Line } from 'vue-chartjs'
import type { ChartComponentRef } from 'vue-chartjs'
import type { OpsThroughputGroupBreakdownItem, OpsThroughputPlatformBreakdownItem, OpsThroughputTrendPoint } from '@/api/admin/ops'
import type { ChartState } from '../types'
import { formatHistoryLabel, sumNumbers } from '../utils/opsFormatters'
import HelpTooltip from '@/components/common/HelpTooltip.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import { chartTooltipStyle, useChartTheme } from '@/components/charts/chartTheme'
import { formatNumber } from '@/utils/format'

ChartJS.register(Title, Tooltip, Legend, LineElement, LinearScale, PointElement, CategoryScale, Filler)

interface Props {
  points: OpsThroughputTrendPoint[]
  loading: boolean
  timeRange: string
  byPlatform?: OpsThroughputPlatformBreakdownItem[]
  topGroups?: OpsThroughputGroupBreakdownItem[]
  fullscreen?: boolean
}

const props = defineProps<Props>()
const { t } = useI18n()
const emit = defineEmits<{
  (e: 'selectPlatform', platform: string): void
  (e: 'selectGroup', groupId: number): void
  (e: 'openDetails'): void
}>()

const throughputChartRef = ref<ChartComponentRef | null>(null)
watch(
  () => props.timeRange,
  () => {
    setTimeout(() => {
      const chart: any = throughputChartRef.value?.chart
      if (chart && typeof chart.resetZoom === 'function') {
        chart.resetZoom()
      }
    }, 100)
  }
)

const theme = useChartTheme()

const totalRequests = computed(() => sumNumbers(props.points.map((p) => p.request_count)))

const chartData = computed(() => {
  if (!props.points.length || totalRequests.value <= 0) return null
  const c = theme.value
  return {
    labels: props.points.map((p) => formatHistoryLabel(p.bucket_start, props.timeRange)),
    datasets: [
      {
        label: 'QPS',
        data: props.points.map((p) => p.qps ?? 0),
        borderColor: c.primary,
        backgroundColor: `${c.primary}20`,
        fill: true,
        tension: 0.4,
        pointRadius: 0,
        pointHitRadius: 10
      },
      {
        label: t('admin.ops.tpsK'),
        data: props.points.map((p) => (p.tps ?? 0) / 1000),
        borderColor: c.output,
        backgroundColor: `${c.output}20`,
        fill: true,
        tension: 0.4,
        pointRadius: 0,
        pointHitRadius: 10,
        yAxisID: 'y1'
      }
    ]
  }
})

const state = computed<ChartState>(() => {
  if (chartData.value) return 'ready'
  if (props.loading) return 'loading'
  return 'empty'
})

const options = computed(() => {
  const c = theme.value
  return {
    responsive: true,
    maintainAspectRatio: false,
    animation: false as const,
    transitions: { active: { animation: { duration: 0 } } },
    interaction: { intersect: false, mode: 'index' as const },
    plugins: {
      legend: {
        position: 'top' as const,
        align: 'end' as const,
        labels: { color: c.text, usePointStyle: true, boxWidth: 6, font: { size: 10 } }
      },
      tooltip: {
        ...chartTooltipStyle(c),
        displayColors: true,
        callbacks: {
          label: (context: any) => {
            let label = context.dataset.label || ''
            if (label) label += ': '
            if (context.raw !== null) label += context.parsed.y.toFixed(1)
            return label
          }
        }
      },
      // Optional: if chartjs-plugin-zoom is installed, these options will enable zoom/pan.
      zoom: {
        pan: { enabled: true, mode: 'x' as const, modifierKey: 'ctrl' as const },
        zoom: { wheel: { enabled: true }, pinch: { enabled: true }, mode: 'x' as const }
      }
    },
    scales: {
      x: {
        type: 'category' as const,
        grid: { display: false },
        ticks: {
          color: c.text,
          font: { size: 10 },
          maxTicksLimit: 8,
          autoSkip: true,
          autoSkipPadding: 10
        }
      },
      y: {
        type: 'linear' as const,
        display: true,
        position: 'left' as const,
        grid: { color: c.grid, borderDash: [4, 4] },
        ticks: { color: c.text, font: { size: 10 } }
      },
      y1: {
        type: 'linear' as const,
        display: true,
        position: 'right' as const,
        grid: { display: false },
        ticks: { color: c.output, font: { size: 10 } }
      }
    }
  }
})

function resetZoom() {
  const chart: any = throughputChartRef.value?.chart
  if (chart && typeof chart.resetZoom === 'function') chart.resetZoom()
}

function downloadChart() {
  const chart: any = throughputChartRef.value?.chart
  if (!chart || typeof chart.toBase64Image !== 'function') return
  const url = chart.toBase64Image('image/png', 1)
  const a = document.createElement('a')
  a.href = url
  a.download = `ops-throughput-${new Date().toISOString().slice(0, 19).replace(/[:T]/g, '-')}.png`
  a.click()
}
</script>

<template>
  <div class="flex h-full min-w-0 flex-col rounded-xl bg-default p-6 ring-1 ring-default">
    <div
      data-testid="throughput-chart-header"
      class="mb-4 flex shrink-0 flex-col gap-3 sm:flex-row sm:items-center sm:justify-between"
    >
      <h3 class="flex min-w-0 items-center gap-2 text-sm font-bold text-highlighted">
        <svg class="h-4 w-4 text-primary" fill="none" viewBox="0 0 24 24" stroke="currentColor">
          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M13 7h8m0 0v8m0-8l-8 8-4-4-6 6" />
        </svg>
        {{ t('admin.ops.throughputTrend') }}
        <HelpTooltip v-if="!props.fullscreen" trigger="auto" :content="t('admin.ops.tooltips.throughputTrend')" />
      </h3>
      <div
        data-testid="throughput-chart-toolbar"
        class="flex w-full min-w-0 flex-wrap items-center gap-2 text-xs text-muted sm:w-auto sm:justify-end"
      >
        <span class="flex shrink-0 items-center gap-1"><span class="h-2 w-2 rounded-full" :style="{ backgroundColor: theme.primary }"></span>QPS</span>
        <span class="flex shrink-0 items-center gap-1"><span class="h-2 w-2 rounded-full" :style="{ backgroundColor: theme.output }"></span>{{ t('admin.ops.tpsK') }}</span>
        <template v-if="!props.fullscreen">
          <button
            type="button"
            class="inline-flex min-h-11 sm:min-h-0 shrink-0 items-center rounded-lg border border-default bg-default px-2 py-1 text-[11px] font-semibold text-default transition-colors duration-150 active:scale-[0.97] [@media(hover:hover)_and_(pointer:fine)]:hover:bg-elevated [@media(hover:hover)_and_(pointer:fine)]:hover:text-highlighted disabled:opacity-50 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary"
            :disabled="state !== 'ready'"
            :title="t('admin.ops.requestDetails.title')"
            @click="emit('openDetails')"
          >
            {{ t('admin.ops.requestDetails.details') }}
          </button>
          <button
            type="button"
            class="inline-flex min-h-11 sm:min-h-0 shrink-0 items-center rounded-lg border border-default bg-default px-2 py-1 text-[11px] font-semibold text-default transition-colors duration-150 active:scale-[0.97] [@media(hover:hover)_and_(pointer:fine)]:hover:bg-elevated [@media(hover:hover)_and_(pointer:fine)]:hover:text-highlighted disabled:opacity-50 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary"
            :disabled="state !== 'ready'"
            :title="t('admin.ops.charts.resetZoomHint')"
            @click="resetZoom"
          >
            {{ t('admin.ops.charts.resetZoom') }}
          </button>
          <button
            type="button"
            class="inline-flex min-h-11 sm:min-h-0 shrink-0 items-center rounded-lg border border-default bg-default px-2 py-1 text-[11px] font-semibold text-default transition-colors duration-150 active:scale-[0.97] [@media(hover:hover)_and_(pointer:fine)]:hover:bg-elevated [@media(hover:hover)_and_(pointer:fine)]:hover:text-highlighted disabled:opacity-50 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary"
            :disabled="state !== 'ready'"
            :title="t('admin.ops.charts.downloadChartHint')"
            @click="downloadChart"
          >
            {{ t('admin.ops.charts.downloadChart') }}
          </button>
        </template>
      </div>
    </div>

    <!-- Drilldown chips (baseline interaction: click to set global filter) -->
    <div v-if="(props.topGroups?.length ?? 0) > 0" class="mb-3 flex flex-wrap gap-2">
      <button
        v-for="g in props.topGroups"
        :key="g.group_id"
        type="button"
        class="inline-flex min-h-11 sm:min-h-0 items-center gap-2 rounded-full border border-default bg-default px-3 py-1 text-[11px] font-semibold text-default transition-colors duration-150 active:scale-[0.97] [@media(hover:hover)_and_(pointer:fine)]:hover:bg-elevated focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary"
        @click="emit('selectGroup', g.group_id)"
      >
        <span class="max-w-[180px] truncate">{{ g.group_name || `#${g.group_id}` }}</span>
        <span class="tabular-nums text-dimmed">{{ formatNumber(g.request_count) }}</span>
      </button>
    </div>

    <div v-else-if="(props.byPlatform?.length ?? 0) > 0" class="mb-3 flex flex-wrap gap-2">
      <button
        v-for="p in props.byPlatform"
        :key="p.platform"
        type="button"
        class="inline-flex min-h-11 sm:min-h-0 items-center gap-2 rounded-full border border-default bg-default px-3 py-1 text-[11px] font-semibold text-default transition-colors duration-150 active:scale-[0.97] [@media(hover:hover)_and_(pointer:fine)]:hover:bg-elevated focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary"
        @click="emit('selectPlatform', p.platform)"
      >
        <span class="uppercase">{{ p.platform }}</span>
        <span class="tabular-nums text-dimmed">{{ formatNumber(p.request_count) }}</span>
      </button>
    </div>

    <div class="min-h-0 min-w-0 flex-1">
      <Line v-if="state === 'ready' && chartData" ref="throughputChartRef" :data="chartData" :options="options" />
      <div v-else class="flex h-full items-center justify-center">
        <div v-if="state === 'loading'" class="animate-pulse text-sm text-dimmed motion-reduce:animate-none">{{ t('common.loading') }}</div>
        <EmptyState v-else :title="t('common.noData')" :description="t('admin.ops.charts.emptyRequest')" />
      </div>
    </div>
  </div>
</template>
