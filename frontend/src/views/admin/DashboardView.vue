<template>
  <AppLayout>
    <div class="space-y-6">
      <!-- Loading State -->
      <div v-if="loading" class="flex items-center justify-center py-12">
        <LoadingSpinner />
      </div>

      <template v-else-if="stats">
        <!-- Row 1: Core Stats -->
        <div class="grid grid-cols-2 gap-4 lg:grid-cols-4">
          <!-- Total API Keys -->
          <div class="card p-4">
            <div class="flex items-center gap-3">
              <div class="rounded-lg bg-blue-100 p-2 dark:bg-blue-900/30">
                <svg
                  class="h-5 w-5 text-blue-600 dark:text-blue-400"
                  fill="none"
                  stroke="currentColor"
                  viewBox="0 0 24 24"
                >
                  <path
                    stroke-linecap="round"
                    stroke-linejoin="round"
                    stroke-width="2"
                    d="M15.75 5.25a3 3 0 013 3m3 0a6 6 0 01-7.029 5.912c-.563-.097-1.159.026-1.563.43L10.5 17.25H8.25v2.25H6v2.25H2.25v-2.818c0-.597.237-1.17.659-1.591l6.499-6.499c.404-.404.527-1 .43-1.563A6 6 0 1121.75 8.25z"
                  />
                </svg>
              </div>
              <div>
                <p class="text-xs font-medium text-gray-500 dark:text-gray-400">
                  {{ t('admin.dashboard.apiKeys') }}
                </p>
                <p class="text-xl font-bold text-gray-900 dark:text-white">
                  {{ stats.total_api_keys }}
                </p>
                <p class="text-xs text-green-600 dark:text-green-400">
                  {{ stats.active_api_keys }} {{ t('common.active') }}
                </p>
              </div>
            </div>
          </div>

          <!-- Service Accounts -->
          <div class="card p-4">
            <div class="flex items-center gap-3">
              <div class="rounded-lg bg-purple-100 p-2 dark:bg-purple-900/30">
                <svg
                  class="h-5 w-5 text-purple-600 dark:text-purple-400"
                  fill="none"
                  stroke="currentColor"
                  viewBox="0 0 24 24"
                >
                  <path
                    stroke-linecap="round"
                    stroke-linejoin="round"
                    stroke-width="2"
                    d="M5.25 14.25h13.5m-13.5 0a3 3 0 01-3-3m3 3a3 3 0 100 6h13.5a3 3 0 100-6m-16.5-3a3 3 0 013-3h13.5a3 3 0 013 3m-19.5 0a4.5 4.5 0 01.9-2.7L5.737 5.1a3.375 3.375 0 012.7-1.35h7.126c1.062 0 2.062.5 2.7 1.35l2.587 3.45a4.5 4.5 0 01.9 2.7m0 0a3 3 0 01-3 3m0 3h.008v.008h-.008v-.008zm0-6h.008v.008h-.008v-.008zm-3 6h.008v.008h-.008v-.008zm0-6h.008v.008h-.008v-.008z"
                  />
                </svg>
              </div>
              <div>
                <p class="text-xs font-medium text-gray-500 dark:text-gray-400">
                  {{ t('admin.dashboard.accounts') }}
                </p>
                <p class="text-xl font-bold text-gray-900 dark:text-white">
                  {{ stats.total_accounts }}
                </p>
                <p class="text-xs">
                  <span class="text-green-600 dark:text-green-400"
                    >{{ stats.normal_accounts }} {{ t('common.active') }}</span
                  >
                  <span v-if="stats.error_accounts > 0" class="ml-1 text-red-500"
                    >{{ stats.error_accounts }} {{ t('common.error') }}</span
                  >
                </p>
              </div>
            </div>
          </div>

          <!-- Today Requests -->
          <div class="card p-4">
            <div class="flex items-center gap-3">
              <div class="rounded-lg bg-green-100 p-2 dark:bg-green-900/30">
                <svg
                  class="h-5 w-5 text-green-600 dark:text-green-400"
                  fill="none"
                  stroke="currentColor"
                  viewBox="0 0 24 24"
                >
                  <path
                    stroke-linecap="round"
                    stroke-linejoin="round"
                    stroke-width="2"
                    d="M3 13.125C3 12.504 3.504 12 4.125 12h2.25c.621 0 1.125.504 1.125 1.125v6.75C7.5 20.496 6.996 21 6.375 21h-2.25A1.125 1.125 0 013 19.875v-6.75zM9.75 8.625c0-.621.504-1.125 1.125-1.125h2.25c.621 0 1.125.504 1.125 1.125v11.25c0 .621-.504 1.125-1.125 1.125h-2.25a1.125 1.125 0 01-1.125-1.125V8.625zM16.5 4.125c0-.621.504-1.125 1.125-1.125h2.25C20.496 3 21 3.504 21 4.125v15.75c0 .621-.504 1.125-1.125 1.125h-2.25a1.125 1.125 0 01-1.125-1.125V4.125z"
                  />
                </svg>
              </div>
              <div>
                <p class="text-xs font-medium text-gray-500 dark:text-gray-400">
                  {{ t('admin.dashboard.todayRequests') }}
                </p>
                <p class="text-xl font-bold text-gray-900 dark:text-white">
                  {{ stats.today_requests }}
                </p>
                <p class="text-xs text-gray-500 dark:text-gray-400">
                  {{ t('common.total') }}: {{ formatNumber(stats.total_requests) }}
                </p>
              </div>
            </div>
          </div>

          <!-- New Users Today -->
          <div class="card p-4">
            <div class="flex items-center gap-3">
              <div class="rounded-lg bg-emerald-100 p-2 dark:bg-emerald-900/30">
                <svg
                  class="h-5 w-5 text-emerald-600 dark:text-emerald-400"
                  fill="none"
                  stroke="currentColor"
                  viewBox="0 0 24 24"
                >
                  <path
                    stroke-linecap="round"
                    stroke-linejoin="round"
                    stroke-width="2"
                    d="M18 9v3m0 0v3m0-3h3m-3 0h-3m-2-5a4 4 0 11-8 0 4 4 0 018 0zM3 20a6 6 0 0112 0v1H3v-1z"
                  />
                </svg>
              </div>
              <div>
                <p class="text-xs font-medium text-gray-500 dark:text-gray-400">
                  {{ t('admin.dashboard.users') }}
                </p>
                <p class="text-xl font-bold text-emerald-600 dark:text-emerald-400">
                  +{{ stats.today_new_users }}
                </p>
                <p class="text-xs text-gray-500 dark:text-gray-400">
                  {{ t('common.total') }}: {{ formatNumber(stats.total_users) }}
                </p>
              </div>
            </div>
          </div>
        </div>

        <!-- Row 2: Token Stats -->
        <div class="grid grid-cols-2 gap-4 lg:grid-cols-4">
          <!-- Today Tokens -->
          <div class="card p-4">
            <div class="flex items-center gap-3">
              <div class="rounded-lg bg-amber-100 p-2 dark:bg-amber-900/30">
                <svg
                  class="h-5 w-5 text-amber-600 dark:text-amber-400"
                  fill="none"
                  stroke="currentColor"
                  viewBox="0 0 24 24"
                >
                  <path
                    stroke-linecap="round"
                    stroke-linejoin="round"
                    stroke-width="2"
                    d="m21 7.5-9-5.25L3 7.5m18 0-9 5.25m9-5.25v9l-9 5.25M3 7.5l9 5.25M3 7.5v9l9 5.25m0-9v9"
                  />
                </svg>
              </div>
              <div>
                <p class="text-xs font-medium text-gray-500 dark:text-gray-400">
                  {{ t('admin.dashboard.todayTokens') }}
                </p>
                <p class="text-xl font-bold text-gray-900 dark:text-white">
                  {{ formatTokens(stats.today_tokens) }}
                </p>
                <p class="text-xs">
                  <span
                    class="text-amber-600 dark:text-amber-400"
                    :title="t('admin.dashboard.actual')"
                    >${{ formatCost(stats.today_actual_cost) }}</span
                  >
                  <span
                    class="text-gray-400 dark:text-gray-500"
                    :title="t('admin.dashboard.standard')"
                  >
                    / ${{ formatCost(stats.today_cost) }}</span
                  >
                </p>
              </div>
            </div>
          </div>

          <!-- Total Tokens -->
          <div class="card p-4">
            <div class="flex items-center gap-3">
              <div class="rounded-lg bg-indigo-100 p-2 dark:bg-indigo-900/30">
                <svg
                  class="h-5 w-5 text-indigo-600 dark:text-indigo-400"
                  fill="none"
                  stroke="currentColor"
                  viewBox="0 0 24 24"
                >
                  <path
                    stroke-linecap="round"
                    stroke-linejoin="round"
                    stroke-width="2"
                    d="M20.25 6.375c0 2.278-3.694 4.125-8.25 4.125S3.75 8.653 3.75 6.375m16.5 0c0-2.278-3.694-4.125-8.25-4.125S3.75 4.097 3.75 6.375m16.5 0v11.25c0 2.278-3.694 4.125-8.25 4.125s-8.25-1.847-8.25-4.125V6.375m16.5 0v3.75m-16.5-3.75v3.75m16.5 0v3.75C20.25 16.153 16.556 18 12 18s-8.25-1.847-8.25-4.125v-3.75m16.5 0c0 2.278-3.694 4.125-8.25 4.125s-8.25-1.847-8.25-4.125"
                  />
                </svg>
              </div>
              <div>
                <p class="text-xs font-medium text-gray-500 dark:text-gray-400">
                  {{ t('admin.dashboard.totalTokens') }}
                </p>
                <p class="text-xl font-bold text-gray-900 dark:text-white">
                  {{ formatTokens(stats.total_tokens) }}
                </p>
                <p class="text-xs">
                  <span
                    class="text-indigo-600 dark:text-indigo-400"
                    :title="t('admin.dashboard.actual')"
                    >${{ formatCost(stats.total_actual_cost) }}</span
                  >
                  <span
                    class="text-gray-400 dark:text-gray-500"
                    :title="t('admin.dashboard.standard')"
                  >
                    / ${{ formatCost(stats.total_cost) }}</span
                  >
                </p>
              </div>
            </div>
          </div>

          <!-- Performance (RPM/TPM) -->
          <div class="card p-4">
            <div class="flex items-center gap-3">
              <div class="rounded-lg bg-violet-100 p-2 dark:bg-violet-900/30">
                <svg
                  class="h-5 w-5 text-violet-600 dark:text-violet-400"
                  fill="none"
                  stroke="currentColor"
                  viewBox="0 0 24 24"
                >
                  <path
                    stroke-linecap="round"
                    stroke-linejoin="round"
                    stroke-width="2"
                    d="M13 10V3L4 14h7v7l9-11h-7z"
                  />
                </svg>
              </div>
              <div class="flex-1">
                <p class="text-xs font-medium text-gray-500 dark:text-gray-400">
                  {{ t('admin.dashboard.performance') }}
                </p>
                <div class="flex items-baseline gap-2">
                  <p class="text-xl font-bold text-gray-900 dark:text-white">
                    {{ formatTokens(stats.rpm) }}
                  </p>
                  <span class="text-xs text-gray-500 dark:text-gray-400">RPM</span>
                </div>
                <div class="flex items-baseline gap-2">
                  <p class="text-sm font-semibold text-violet-600 dark:text-violet-400">
                    {{ formatTokens(stats.tpm) }}
                  </p>
                  <span class="text-xs text-gray-500 dark:text-gray-400">TPM</span>
                </div>
              </div>
            </div>
          </div>

          <!-- Avg Response Time -->
          <div class="card p-4">
            <div class="flex items-center gap-3">
              <div class="rounded-lg bg-rose-100 p-2 dark:bg-rose-900/30">
                <svg
                  class="h-5 w-5 text-rose-600 dark:text-rose-400"
                  fill="none"
                  stroke="currentColor"
                  viewBox="0 0 24 24"
                >
                  <path
                    stroke-linecap="round"
                    stroke-linejoin="round"
                    stroke-width="2"
                    d="M12 6v6h4.5m4.5 0a9 9 0 11-18 0 9 9 0 0118 0z"
                  />
                </svg>
              </div>
              <div>
                <p class="text-xs font-medium text-gray-500 dark:text-gray-400">
                  {{ t('admin.dashboard.avgResponse') }}
                </p>
                <p class="text-xl font-bold text-gray-900 dark:text-white">
                  {{ formatDuration(stats.average_duration_ms) }}
                </p>
                <p class="text-xs text-gray-500 dark:text-gray-400">
                  {{ stats.active_users }} {{ t('admin.dashboard.activeUsers') }}
                </p>
              </div>
            </div>
          </div>
        </div>

        <!-- Charts Section -->
        <div class="space-y-6">
          <!-- Date Range Filter -->
          <div class="card p-4">
            <div class="flex flex-wrap items-center gap-4">
              <div class="flex items-center gap-2">
                <span class="text-sm font-medium text-gray-700 dark:text-gray-300"
                  >{{ t('admin.dashboard.timeRange') }}:</span
                >
                <DateRangePicker
                  v-model:start-date="startDate"
                  v-model:end-date="endDate"
                  @change="onDateRangeChange"
                />
              </div>
              <div class="ml-auto flex items-center gap-2">
                <span class="text-sm font-medium text-gray-700 dark:text-gray-300"
                  >{{ t('admin.dashboard.granularity') }}:</span
                >
                <div class="w-28">
                  <Select
                    v-model="granularity"
                    :options="granularityOptions"
                    @change="loadChartData"
                  />
                </div>
              </div>
            </div>
          </div>

          <!-- Charts Grid -->
          <div class="grid grid-cols-1 gap-6 lg:grid-cols-2">
            <ModelDistributionChart :model-stats="modelStats" :loading="chartsLoading" />
            <TokenUsageTrend :trend-data="trendData" :loading="chartsLoading" />
          </div>

          <!-- User Usage Trend (Full Width) -->
          <div class="card p-4">
            <h3 class="mb-4 text-sm font-semibold text-gray-900 dark:text-white">
              {{ t('admin.dashboard.recentUsage') }} (Top 12)
            </h3>
            <div class="h-64">
              <Line v-if="userTrendChartData" :data="userTrendChartData" :options="lineOptions" />
              <div
                v-else
                class="flex h-full items-center justify-center text-sm text-gray-500 dark:text-gray-400"
              >
                {{ t('admin.dashboard.noDataAvailable') }}
              </div>
            </div>
          </div>
        </div>
      </template>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'

const { t } = useI18n()
import { adminAPI } from '@/api/admin'
import type { DashboardStats, TrendDataPoint, ModelStat, UserUsageTrendPoint } from '@/types'
import AppLayout from '@/components/layout/AppLayout.vue'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import DateRangePicker from '@/components/common/DateRangePicker.vue'
import Select from '@/components/common/Select.vue'
import ModelDistributionChart from '@/components/charts/ModelDistributionChart.vue'
import TokenUsageTrend from '@/components/charts/TokenUsageTrend.vue'

import {
  Chart as ChartJS,
  CategoryScale,
  LinearScale,
  PointElement,
  LineElement,
  Title,
  Tooltip,
  Legend,
  Filler
} from 'chart.js'
import { Line } from 'vue-chartjs'

// Register Chart.js components
ChartJS.register(
  CategoryScale,
  LinearScale,
  PointElement,
  LineElement,
  Title,
  Tooltip,
  Legend,
  Filler
)

const appStore = useAppStore()
const stats = ref<DashboardStats | null>(null)
const loading = ref(false)
const chartsLoading = ref(false)

// Chart data
const trendData = ref<TrendDataPoint[]>([])
const modelStats = ref<ModelStat[]>([])
const userTrend = ref<UserUsageTrendPoint[]>([])

// Date range
const granularity = ref<'day' | 'hour'>('day')
const startDate = ref('')
const endDate = ref('')

// Granularity options for Select component
const granularityOptions = computed(() => [
  { value: 'day', label: t('admin.dashboard.day') },
  { value: 'hour', label: t('admin.dashboard.hour') }
])

// Dark mode detection
const isDarkMode = computed(() => {
  return document.documentElement.classList.contains('dark')
})

// Chart colors
const chartColors = computed(() => ({
  text: isDarkMode.value ? '#e5e7eb' : '#374151',
  grid: isDarkMode.value ? '#374151' : '#e5e7eb'
}))

// Line chart options (for user trend chart)
const lineOptions = computed(() => ({
  responsive: true,
  maintainAspectRatio: false,
  interaction: {
    intersect: false,
    mode: 'index' as const
  },
  plugins: {
    legend: {
      position: 'top' as const,
      labels: {
        color: chartColors.value.text,
        usePointStyle: true,
        pointStyle: 'circle',
        padding: 15,
        font: {
          size: 11
        }
      }
    },
    tooltip: {
      callbacks: {
        label: (context: any) => {
          return `${context.dataset.label}: ${formatTokens(context.raw)}`
        }
      }
    }
  },
  scales: {
    x: {
      grid: {
        color: chartColors.value.grid
      },
      ticks: {
        color: chartColors.value.text,
        font: {
          size: 10
        }
      }
    },
    y: {
      grid: {
        color: chartColors.value.grid
      },
      ticks: {
        color: chartColors.value.text,
        font: {
          size: 10
        },
        callback: (value: string | number) => formatTokens(Number(value))
      }
    }
  }
}))

// User trend chart data
const userTrendChartData = computed(() => {
  if (!userTrend.value?.length) return null

  // Extract display name from email (part before @)
  const getDisplayName = (email: string, userId: number): string => {
    if (email && email.includes('@')) {
      return email.split('@')[0]
    }
    return `User #${userId}`
  }

  // Group by user
  const userGroups = new Map<string, { name: string; data: Map<string, number> }>()
  const allDates = new Set<string>()

  userTrend.value.forEach((point) => {
    allDates.add(point.date)
    const key = getDisplayName(point.email, point.user_id)
    if (!userGroups.has(key)) {
      userGroups.set(key, { name: key, data: new Map() })
    }
    userGroups.get(key)!.data.set(point.date, point.tokens)
  })

  const sortedDates = Array.from(allDates).sort()
  const colors = [
    '#3b82f6',
    '#10b981',
    '#f59e0b',
    '#ef4444',
    '#8b5cf6',
    '#ec4899',
    '#14b8a6',
    '#f97316',
    '#6366f1',
    '#84cc16',
    '#06b6d4',
    '#a855f7'
  ]

  const datasets = Array.from(userGroups.values()).map((group, idx) => ({
    label: group.name,
    data: sortedDates.map((date) => group.data.get(date) || 0),
    borderColor: colors[idx % colors.length],
    backgroundColor: `${colors[idx % colors.length]}20`,
    fill: false,
    tension: 0.3
  }))

  return {
    labels: sortedDates,
    datasets
  }
})

// Format helpers
const formatTokens = (value: number | undefined): string => {
  if (value === undefined || value === null) return '0'
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

const formatDuration = (ms: number): string => {
  if (ms >= 1000) {
    return `${(ms / 1000).toFixed(2)}s`
  }
  return `${Math.round(ms)}ms`
}

// Date range change handler
const onDateRangeChange = (range: {
  startDate: string
  endDate: string
  preset: string | null
}) => {
  // Auto-select granularity based on date range
  const start = new Date(range.startDate)
  const end = new Date(range.endDate)
  const daysDiff = Math.ceil((end.getTime() - start.getTime()) / (1000 * 60 * 60 * 24))

  // If range is 1 day, use hourly granularity
  if (daysDiff <= 1) {
    granularity.value = 'hour'
  } else {
    granularity.value = 'day'
  }

  loadChartData()
}

// Initialize default date range
const initializeDateRange = () => {
  const now = new Date()
  const today = now.toISOString().split('T')[0]
  const weekAgo = new Date(now)
  weekAgo.setDate(weekAgo.getDate() - 6)

  startDate.value = weekAgo.toISOString().split('T')[0]
  endDate.value = today
  granularity.value = 'day'
}

// Load data
const loadDashboardStats = async () => {
  loading.value = true
  try {
    stats.value = await adminAPI.dashboard.getStats()
  } catch (error) {
    appStore.showError(t('admin.dashboard.failedToLoad'))
    console.error('Error loading dashboard stats:', error)
  } finally {
    loading.value = false
  }
}

const loadChartData = async () => {
  chartsLoading.value = true
  try {
    const params = {
      start_date: startDate.value,
      end_date: endDate.value,
      granularity: granularity.value
    }

    const [trendResponse, modelResponse, userResponse] = await Promise.all([
      adminAPI.dashboard.getUsageTrend(params),
      adminAPI.dashboard.getModelStats({ start_date: startDate.value, end_date: endDate.value }),
      adminAPI.dashboard.getUserUsageTrend({ ...params, limit: 12 })
    ])

    trendData.value = trendResponse.trend || []
    modelStats.value = modelResponse.models || []
    userTrend.value = userResponse.trend || []
  } catch (error) {
    console.error('Error loading chart data:', error)
  } finally {
    chartsLoading.value = false
  }
}

onMounted(() => {
  loadDashboardStats()
  initializeDateRange()
  loadChartData()
})
</script>

<style scoped>
/* Compact Select styling for dashboard */
:deep(.select-trigger) {
  @apply rounded-lg px-3 py-1.5 text-sm;
}

:deep(.select-dropdown) {
  @apply rounded-lg;
}

:deep(.select-option) {
  @apply px-3 py-2 text-sm;
}
</style>
