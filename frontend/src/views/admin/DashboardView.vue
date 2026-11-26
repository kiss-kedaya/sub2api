<template>
  <AppLayout>
    <div class="signal-admin-dashboard flex min-w-0 flex-col gap-4 sm:gap-6">
      <DashboardToolbar
        v-model:startDate="startDate"
        v-model:endDate="endDate"
        :granularity="granularity"
        :granularity-items="granularityOptions"
        :granularity-label="t('admin.dashboard.granularity')"
        :refresh-label="t('common.refresh')"
        :refreshing="loading || chartsLoading"
        @update:granularity="setGranularity"
        @dateRangeChange="onDateRangeChange"
        @granularityChange="loadChartData"
        @refresh="loadDashboardStats"
      />

      <!-- Loading State -->
      <div v-if="loading && !stats" class="flex items-center justify-center py-12">
        <LoadingSpinner />
      </div>

      <div v-else-if="dashboardError" class="flex flex-col items-center justify-center gap-4 py-12 text-sm text-muted">
        <p>{{ t('admin.dashboard.failedToLoad') }}</p>
        <UButton color="neutral" variant="outline" @click="loadDashboardStats">
          {{ t('common.refresh') }}
        </UButton>
      </div>

      <template v-else-if="stats">
        <!-- Stats: two rows of four, joined like the template's HomeStats -->
        <UPageGrid :class="statGridClass">
          <DashboardStat icon="i-lucide-key-round" :title="t('admin.dashboard.apiKeys')" :value="formatNumber(stats.total_api_keys)" :class="corner(0)">
            <template #badge>
              <UBadge color="success" variant="subtle">{{ stats.active_api_keys }} {{ t('common.active') }}</UBadge>
            </template>
          </DashboardStat>
          <DashboardStat icon="i-lucide-server" :title="t('admin.dashboard.accounts')" :value="formatNumber(stats.total_accounts)" :class="corner(1)">
            <template #badge>
              <UBadge color="success" variant="subtle">{{ stats.normal_accounts }} {{ t('common.active') }}</UBadge>
              <UBadge v-if="stats.error_accounts > 0" color="error" variant="subtle">{{ stats.error_accounts }} {{ t('common.error') }}</UBadge>
            </template>
          </DashboardStat>

          <DashboardStat icon="i-lucide-chart-column" :title="t('admin.dashboard.todayRequests')" :value="formatNumber(stats.today_requests)" :class="corner(2)">
            <template #sub>{{ t('common.total') }}: <span class="text-highlighted">{{ formatNumber(stats.total_requests) }}</span></template>
          </DashboardStat>
          <DashboardStat icon="i-lucide-users" :title="t('admin.dashboard.users')" :value="formatNumber(stats.total_users)" :class="corner(3)">
            <template #badge>
              <UBadge :color="stats.today_new_users > 0 ? 'success' : 'neutral'" variant="subtle" :title="t('admin.dashboard.newUsersToday')">+{{ stats.today_new_users }}</UBadge>
            </template>
          </DashboardStat>

          <DashboardStat icon="i-lucide-box" :title="t('admin.dashboard.todayTokens')" :value="formatTokens(stats.today_tokens)" :class="corner(4)">
            <template #sub>
              <div class="flex flex-wrap gap-x-3"><span>{{ t('admin.dashboard.actual') }} <span class="text-highlighted">${{ formatCost(stats.today_actual_cost) }}</span></span> <span>{{ t('admin.dashboard.accountCost') }} <span class="text-highlighted">${{ formatCost(stats.today_account_cost) }}</span></span> <span>{{ t('admin.dashboard.standard') }} ${{ formatCost(stats.today_cost) }}</span></div>
            </template>
          </DashboardStat>

          <DashboardStat icon="i-lucide-database" :title="t('admin.dashboard.totalTokens')" :value="formatTokens(stats.total_tokens)" :class="corner(5)">
            <template #sub>
              <div class="flex flex-wrap gap-x-3"><span>{{ t('admin.dashboard.actual') }} <span class="text-highlighted">${{ formatCost(stats.total_actual_cost) }}</span></span> <span>{{ t('admin.dashboard.accountCost') }} <span class="text-highlighted">${{ formatCost(stats.total_account_cost) }}</span></span> <span>{{ t('admin.dashboard.standard') }} ${{ formatCost(stats.total_cost) }}</span></div>
            </template>
          </DashboardStat>

          <DashboardStat icon="i-lucide-zap" :title="t('admin.dashboard.performance')" :class="corner(6)">
            <template #value>{{ formatTokens(stats.rpm) }} <span class="text-sm font-normal text-muted">RPM</span></template>
            <template #sub><span class="text-highlighted">{{ formatTokens(stats.tpm) }}</span> TPM</template>
          </DashboardStat>
          <!-- 余额卡：上面是有余额用户的总余额，下面是这些用户的数量。 -->
          <DashboardStat icon="i-lucide-wallet" :title="t('admin.dashboard.usersWithBalance')" :value="formatCost(stats.users_with_balance_total)" :class="corner(7)">
            <template #sub>{{ stats.users_with_balance }} {{ t('admin.dashboard.usersWithBalanceUnit') }}</template>
          </DashboardStat>
        </UPageGrid>

        <!-- Quick Actions -->
        <DashboardQuickLinks :items="quickLinks" :label="t('admin.dashboard.quickActions')" />

        <!-- Charts: trend first (HomeChart), then the model table, then the per-user trend -->
        <DashboardChartCard
          class="admin-trend-card"
          :title="t('admin.dashboard.tokenUsageTrend')"
          :value="chartsLoading || chartsError ? '-' : formatTokens(trendTotalTokens)"
        >
          <div class="admin-chart-body px-2 pb-2 pt-4 sm:px-4">
            <p v-if="chartsError" role="alert" class="py-12 text-center text-sm text-muted">
              {{ t('admin.dashboard.failedToLoad') }}
            </p>
            <TokenUsageTrend v-else bare :trend-data="trendData" :loading="chartsLoading" />
          </div>
        </DashboardChartCard>

        <ModelDistributionChart
          class="admin-model-card"
          :model-stats="modelStats"
          :enable-ranking-view="true"
          :ranking-items="rankingItems"
          :ranking-total-actual-cost="rankingTotalActualCost"
          :ranking-total-requests="rankingTotalRequests"
          :ranking-total-tokens="rankingTotalTokens"
          :loading="chartsLoading"
          :model-stats-error="chartsError"
          :ranking-loading="rankingLoading"
          :ranking-error="rankingError"
          :start-date="startDate"
          :end-date="endDate"
          @ranking-click="goToUserUsage"
        />

        <!-- User Usage Trend (Top 12) -->
        <DashboardChartCard
          :title="`${t('admin.dashboard.recentUsage')} (Top 12)`"
          :value="userTrendLoading || userTrendError ? '-' : formatTokens(userTrendTotalTokens)"
        >
          <div class="h-64 px-2 pb-2 pt-4 sm:px-4">
            <div v-if="userTrendLoading" class="flex h-full items-center justify-center">
              <LoadingSpinner size="md" />
            </div>
            <p v-else-if="userTrendError" role="alert" class="flex h-full items-center justify-center text-sm text-muted">
              {{ t('admin.dashboard.failedToLoad') }}
            </p>
            <Line v-else-if="userTrendChartData" :data="userTrendChartData" :options="lineOptions" />
            <div v-else class="flex h-full items-center justify-center text-sm text-muted">
              {{ t('admin.dashboard.noDataAvailable') }}
            </div>
          </div>
        </DashboardChartCard>
      </template>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'

const { t } = useI18n()
import { adminAPI } from '@/api/admin'
import type {
  DashboardStats,
  TrendDataPoint,
  ModelStat,
  UserUsageTrendPoint,
  UserSpendingRankingItem
} from '@/types'
import AppLayout from '@/components/layout/AppLayout.vue'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import ModelDistributionChart from '@/components/charts/ModelDistributionChart.vue'
import TokenUsageTrend from '@/components/charts/TokenUsageTrend.vue'
import DashboardToolbar from '@/components/dashboard/DashboardToolbar.vue'
import DashboardStat from '@/components/dashboard/DashboardStat.vue'
import DashboardChartCard from '@/components/dashboard/DashboardChartCard.vue'
import DashboardQuickLinks from '@/components/dashboard/DashboardQuickLinks.vue'
import { statCorners, statGridClass, type DashboardQuickLink } from '@/components/dashboard/dashboardUi'
import { useBatchImageAccess } from '@/composables/useBatchImageAccess'
import { chartTooltipStyle, useChartTheme } from '@/components/charts/chartTheme'

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

// Register Chart.js components
ChartJS.register(
  CategoryScale,
  LinearScale,
  PointElement,
  LineElement,
  Tooltip,
  Legend,
  Filler
)

const router = useRouter()
const { canUseBatchImage, refreshBatchImageAccess } = useBatchImageAccess()
const stats = ref<DashboardStats | null>(null)
const loading = ref(false)
const dashboardError = ref(false)
const chartsLoading = ref(false)
const chartsError = ref(false)
const userTrendLoading = ref(false)
const userTrendError = ref(false)
const rankingLoading = ref(false)
const rankingError = ref(false)

// Chart data
const trendData = ref<TrendDataPoint[]>([])
const modelStats = ref<ModelStat[]>([])
const userTrend = ref<UserUsageTrendPoint[]>([])
const rankingItems = ref<UserSpendingRankingItem[]>([])
const rankingTotalActualCost = ref(0)
const rankingTotalRequests = ref(0)
const rankingTotalTokens = ref(0)
let overviewLoadSeq = 0
let chartLoadSeq = 0
let usersTrendLoadSeq = 0
let rankingLoadSeq = 0
const rankingLimit = 12

// Helper function to format date in local timezone
const formatLocalDate = (date: Date): string => {
  return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')}`
}

const getLast24HoursRangeDates = (): { start: string; end: string } => {
  const end = new Date()
  const start = new Date(end.getTime() - 24 * 60 * 60 * 1000)
  return {
    start: formatLocalDate(start),
    end: formatLocalDate(end)
  }
}

// Date range
const granularity = ref<'day' | 'hour'>('hour')
const defaultRange = getLast24HoursRangeDates()
const startDate = ref(defaultRange.start)
const endDate = ref(defaultRange.end)

// Granularity options for the toolbar select
const granularityOptions = computed(() => [
  { value: 'day', label: t('admin.dashboard.day') },
  { value: 'hour', label: t('admin.dashboard.hour') }
])

const setGranularity = (value: string) => {
  granularity.value = value === 'day' ? 'day' : 'hour'
}

// Eight stat tiles, two rows of four
const corner = (index: number) => statCorners(index, 8)

const quickLinks = computed<DashboardQuickLink[]>(() => [
  ...(canUseBatchImage.value ? [{ label: t('admin.dashboard.batchImage'), to: '/batch-image', icon: 'i-lucide-sparkles' }] : []),
  { label: t('admin.dashboard.groupPricing'), to: '/admin/groups', icon: 'i-lucide-layout-grid' }
])

// Chart header numbers: tokens over the chosen range
const trendTotalTokens = computed(() =>
  trendData.value.reduce((sum, point) => sum + toFiniteNumber(point.total_tokens), 0)
)
const userTrendTotalTokens = computed(() =>
  userTrend.value.reduce((sum, point) => sum + toFiniteNumber(point.tokens), 0)
)

// Chart palette & tooltip follow the active site theme (light / dark + Appearance)
const chartTheme = useChartTheme()

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
        color: chartTheme.value.text,
        usePointStyle: true,
        pointStyle: 'circle',
        padding: 15,
        font: {
          size: 11
        }
      }
    },
    tooltip: {
      ...chartTooltipStyle(chartTheme.value),
      itemSort: (a: any, b: any) => {
        const aValue = typeof a?.raw === 'number' ? a.raw : Number(a?.parsed?.y ?? 0)
        const bValue = typeof b?.raw === 'number' ? b.raw : Number(b?.parsed?.y ?? 0)
        return bValue - aValue
      },
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
        color: chartTheme.value.grid
      },
      ticks: {
        color: chartTheme.value.text,
        font: {
          size: 10
        }
      }
    },
    y: {
      grid: {
        color: chartTheme.value.grid
      },
      ticks: {
        color: chartTheme.value.text,
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

  const getDisplayName = (point: UserUsageTrendPoint): string => {
    const username = point.username?.trim()
    if (username) {
      return username
    }

    const email = point.email?.trim()
    if (email) {
      return email
    }

    return t('admin.redeem.userPrefix', { id: point.user_id })
  }

  // Group by user_id to avoid merging different users with the same display name
  const userGroups = new Map<number, { name: string; data: Map<string, number> }>()
  const allDates = new Set<string>()

  userTrend.value.forEach((point) => {
    allDates.add(point.date)
    const key = point.user_id
    if (!userGroups.has(key)) {
      userGroups.set(key, { name: getDisplayName(point), data: new Map() })
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

const toFiniteNumber = (value: unknown): number => {
  const numberValue = Number(value)
  return Number.isFinite(numberValue) ? numberValue : 0
}

const formatNumber = (value: number | null | undefined): string => {
  return toFiniteNumber(value).toLocaleString()
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

const goToUserUsage = (item: UserSpendingRankingItem) => {
  void router.push({
    path: '/admin/usage',
    query: {
      user_id: String(item.user_id),
      start_date: startDate.value,
      end_date: endDate.value
    }
  })
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

// Load data
const loadDashboardOverview = async () => {
  const currentSeq = ++overviewLoadSeq
  loading.value = true
  dashboardError.value = false
  try {
    const response = await adminAPI.dashboard.getSnapshotV2({
      include_stats: true,
      include_trend: false,
      include_model_stats: false,
      include_group_stats: false,
      include_users_trend: false
    })
    if (currentSeq !== overviewLoadSeq) return false
    if (!response.stats) throw new Error('Missing dashboard overview')
    stats.value = response.stats
    return true
  } catch (error) {
    if (currentSeq === overviewLoadSeq) {
      dashboardError.value = true
      console.error('Error loading dashboard overview:', error)
    }
    return false
  } finally {
    if (currentSeq === overviewLoadSeq) loading.value = false
  }
}

const loadDashboardCharts = async () => {
  const currentSeq = ++chartLoadSeq
  chartsLoading.value = true
  chartsError.value = false
  try {
    const response = await adminAPI.dashboard.getSnapshotV2({
      start_date: startDate.value,
      end_date: endDate.value,
      granularity: granularity.value,
      include_stats: false,
      include_trend: true,
      include_model_stats: true,
      include_group_stats: false,
      include_users_trend: false
    })
    if (currentSeq !== chartLoadSeq) return
    trendData.value = response.trend || []
    modelStats.value = response.models || []
  } catch (error) {
    if (currentSeq !== chartLoadSeq) return
    chartsError.value = true
    trendData.value = []
    modelStats.value = []
    console.error('Error loading dashboard charts:', error)
  } finally {
    if (currentSeq === chartLoadSeq) chartsLoading.value = false
  }
}

const loadUsersTrend = async () => {
  const currentSeq = ++usersTrendLoadSeq
  userTrendLoading.value = true
  userTrendError.value = false
  try {
    const response = await adminAPI.dashboard.getUserUsageTrend({
      start_date: startDate.value,
      end_date: endDate.value,
      granularity: granularity.value,
      limit: 12
    })
    if (currentSeq !== usersTrendLoadSeq) return
    userTrend.value = response.trend || []
  } catch (error) {
    if (currentSeq !== usersTrendLoadSeq) return
    console.error('Error loading users trend:', error)
    userTrendError.value = true
    userTrend.value = []
  } finally {
    if (currentSeq === usersTrendLoadSeq) {
      userTrendLoading.value = false
    }
  }
}

const loadUserSpendingRanking = async () => {
  const currentSeq = ++rankingLoadSeq
  rankingLoading.value = true
  rankingError.value = false
  try {
    const response = await adminAPI.dashboard.getUserSpendingRanking({
      start_date: startDate.value,
      end_date: endDate.value,
      limit: rankingLimit
    })
    if (currentSeq !== rankingLoadSeq) return
    rankingItems.value = response.ranking || []
    rankingTotalActualCost.value = response.total_actual_cost || 0
    rankingTotalRequests.value = response.total_requests || 0
    rankingTotalTokens.value = response.total_tokens || 0
  } catch (error) {
    if (currentSeq !== rankingLoadSeq) return
    console.error('Error loading user spending ranking:', error)
    rankingItems.value = []
    rankingTotalActualCost.value = 0
    rankingTotalRequests.value = 0
    rankingTotalTokens.value = 0
    rankingError.value = true
  } finally {
    if (currentSeq === rankingLoadSeq) {
      rankingLoading.value = false
    }
  }
}

const loadDashboardStats = async () => {
  const initialChartSeq = chartLoadSeq
  if (await loadDashboardOverview() && initialChartSeq === chartLoadSeq) {
    await loadChartData()
  }
}

const loadChartData = async () => {
  await Promise.all([
    loadDashboardCharts(),
    loadUsersTrend(),
    loadUserSpendingRanking()
  ])
}

onMounted(() => {
  void refreshBatchImageAccess()
  loadDashboardStats()
})
</script>

<style scoped>
.admin-trend-card :deep(.h-48) {
  height: 280px;
}

.admin-model-card :deep(.max-h-56) {
  max-height: 300px;
}
</style>
