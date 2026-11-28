<template>
  <UCard :ui="{ header: 'p-4 sm:px-6', body: 'p-4 sm:p-6' }">
    <template #header>
      <p class="text-sm font-semibold text-highlighted">{{ t('payment.admin.dailyRevenue') }}</p>
    </template>
    <div class="h-64">
      <div v-if="loading" class="flex h-full items-center justify-center">
        <LoadingSpinner size="md" />
      </div>
      <Line v-else-if="chartData" :data="chartData" :options="chartOptions" />
      <div
        v-else
        class="flex h-full items-center justify-center text-sm text-muted"
      >
        {{ t('payment.admin.noData') }}
      </div>
    </div>
  </UCard>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  Chart as ChartJS,
  CategoryScale,
  LinearScale,
  PointElement,
  LineElement,
  Tooltip,
  Legend,
  Filler
} from 'chart.js'
import { Line } from 'vue-chartjs'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import { chartTooltipStyle, useChartTheme } from '@/components/charts/chartTheme'
import type { DailyPaymentStats } from '@/types/payment'

ChartJS.register(CategoryScale, LinearScale, PointElement, LineElement, Tooltip, Legend, Filler)

const { t } = useI18n()
const chartTheme = useChartTheme()

const props = defineProps<{
  data: DailyPaymentStats[]
  loading?: boolean
}>()

const chartData = computed(() => {
  if (!props.data || props.data.length === 0) return null
  const theme = chartTheme.value
  const colors = [
    [theme.primary, `${theme.primary}1a`],
    ['rgb(168, 85, 247)', 'rgba(168, 85, 247, 0.1)'],
    ['rgb(245, 158, 11)', 'rgba(245, 158, 11, 0.1)'],
    ['rgb(239, 68, 68)', 'rgba(239, 68, 68, 0.1)'],
  ]
  const currencies = [...new Set(props.data.flatMap(day => Object.keys(day.amount)))].sort()
  return {
    labels: props.data.map(d => d.date),
    datasets: [
      ...currencies.map((currency, index) => {
        const [borderColor, backgroundColor] = colors[index % colors.length]
        return {
          label: `${currency} ${t('payment.admin.revenue')}`,
          data: props.data.map(day => day.amount[currency] || 0),
          borderColor,
          backgroundColor,
          fill: true,
          tension: 0.3,
          pointRadius: 3,
          pointHoverRadius: 5,
        }
      }),
      {
        label: t('payment.admin.orderCount'),
        data: props.data.map(d => d.count),
        borderColor: theme.output,
        backgroundColor: `${theme.output}1a`,
        fill: false,
        tension: 0.3,
        pointRadius: 3,
        pointHoverRadius: 5,
        yAxisID: 'y1',
      }
    ]
  }
})

const chartOptions = computed(() => {
  const theme = chartTheme.value
  return {
    responsive: true,
    maintainAspectRatio: false,
    animation: false as const,
    interaction: { mode: 'index' as const, intersect: false },
    scales: {
      x: {
        grid: { color: theme.grid },
        ticks: { color: theme.text },
      },
      y: {
        type: 'linear' as const,
        display: true,
        position: 'left' as const,
        title: { display: true, text: t('payment.admin.revenue'), color: theme.text },
        grid: { color: theme.grid },
        ticks: { color: theme.text },
      },
      y1: {
        type: 'linear' as const,
        display: true,
        position: 'right' as const,
        title: { display: true, text: t('payment.admin.orderCount'), color: theme.text },
        ticks: { color: theme.text },
        grid: { drawOnChartArea: false },
      }
    },
    plugins: {
      legend: { position: 'top' as const, labels: { color: theme.text } },
      tooltip: chartTooltipStyle(theme),
    }
  }
})
</script>
