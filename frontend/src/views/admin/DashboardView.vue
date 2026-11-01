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
      <div v-if="loading" class="flex items-center justify-center py-12">
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
          <DashboardStat icon="i-lucide-clock" :title="t('admin.dashboard.avgResponse')" :value="formatDuration(stats.average_duration_ms)" :class="corner(7)">
            <template #sub>{{ stats.active_users }} {{ t('admin.dashboard.activeUsers') }}</template>
          </DashboardStat>
        </UPageGrid>

        <!-- Quick Actions -->
        <DashboardQuickLinks :items="quickLinks" :label="t('admin.dashboard.quickActions')" />

        <!-- Charts: trend first (HomeChart), then the model table, then the per-user trend -->
        <DashboardChartCard
          class="admin-trend-card"
          :title="t('admin.dashboard.tokenUsageTrend')"
          :value="chartsLoading ? '-' : formatTokens(trendTotalTokens)"
        >
          <div class="admin-chart-body px-2 pb-2 pt-4 sm:px-4">
            <TokenUsageTrend :trend-data="trendData" :loading="chartsLoading" />
          </div>
        </DashboardChartCard>

        <!-- Short lists; the full distribution, breakdowns and ranking live on /admin/usage -->
        <div class="grid gap-4 sm:gap-6 xl:grid-cols-2">
          <DashboardTopList
            class="admin-top-models"
            :title="`${t('admin.dashboard.modelDistribution')} Top ${topLimit}`"
            :rows="topModels"
            :columns="modelColumns"
            :to="usageLink"
            :view-all-label="t('admin.dashboard.viewAllUsage')"
            :empty-label="t('admin.dashboard.noDataAvailable')"
            :error-label="t('admin.dashboard.failedToLoad')"
            :loading="chartsLoading"
          />
          <DashboardTopList
            class="admin-top-spenders"
            :title="`${t('admin.dashboard.spendingRankingTitle')} Top ${topLimit}`"
            :rows="rankingItems"
            :columns="rankingColumns"
            :to="usageLink"
            :view-all-label="t('admin.dashboard.viewAllUsage')"
            :empty-label="t('admin.dashboard.noDataAvailable')"
            :error-label="t('admin.dashboard.failedToLoad')"
            :loading="rankingLoading"
            :error="rankingError"
            selectable
            @select="goToUserUsage"
          />
        </div>

        <!-- User Usage Trend (Top 12) -->
        <DashboardChartCard
          :title="`${t('admin.dashboard.recentUsage')} (Top 12)`"
          :value="userTrendLoading ? '-' : formatTokens(userTrendTotalTokens)"
        >
          <div class="h-64 px-2 pb-2 pt-4 sm:px-4">
            <div v-if="userTrendLoading" class="flex h-full items-center justify-center">
              <LoadingSpinner size="md" />
            </div>
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
import { ref, computed, h, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { useAppStore } from '@/stores/app'

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
import TokenUsageTrend from '@/components/charts/TokenUsageTrend.vue'
import DashboardToolbar from '@/components/dashboard/DashboardToolbar.vue'
import DashboardStat from '@/components/dashboard/DashboardStat.vue'
import DashboardChartCard from '@/components/dashboard/DashboardChartCard.vue'
import DashboardQuickLinks from '@/components/dashboard/DashboardQuickLinks.vue'
import DashboardTopList from '@/components/dashboard/DashboardTopList.vue'
import type { TableColumn } from '@nuxt/ui'
import { statCorners, statGridClass, type DashboardQuickLink } from '@/components/dashboard/dashboardUi'
import { useBatchImageAccess } from '@/composables/useBatchImageAccess'
import { useSiteColorMode } from '@/composables/useSiteTheme'

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

const appStore = useAppStore()
const router = useRouter()
const { canUseBatchImage, refreshBatchImageAccess } = useBatchImageAccess()
const stats = ref<DashboardStats | null>(null)
const loading = ref(false)
const dashboardError = ref(false)
const chartsLoading = ref(false)
const userTrendLoading = ref(false)
const rankingLoading = ref(false)
const rankingError = ref(false)

// Chart data
const trendData = ref<TrendDataPoint[]>([])
const modelStats = ref<ModelStat[]>([])
const userTrend = ref<UserUsageTrendPoint[]>([])
const rankingItems = ref<UserSpendingRankingItem[]>([])
let chartLoadSeq = 0
let usersTrendLoadSeq = 0
let rankingLoadSeq = 0
// The overview shows short lists; the full ones are on /admin/usage.
const topLimit = 5
const rankingLimit = topLimit

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

// Dark mode follows the html.dark class, so the chart colors switch with the theme toggle
const { isDark: isDarkMode } = useSiteColorMode()

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

const formatDuration = (ms: number): string => {
  if (ms >= 1000) {
    return `${(ms / 1000).toFixed(2)}s`
  }
  return `${Math.round(ms)}ms`
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

// Short lists for the overview. "View all" opens /admin/usage on the same date range, where
// the full model distribution (with per-user breakdown) and the user ranking live.
const usageLink = computed(() => ({
  path: '/admin/usage',
  query: { start_date: startDate.value, end_date: endDate.value }
}))

const topModels = computed(() =>
  [...modelStats.value]
    .sort((a, b) => toFiniteNumber(b.total_tokens) - toFiniteNumber(a.total_tokens))
    .slice(0, topLimit)
)

const rankingUserLabel = (item: UserSpendingRankingItem): string =>
  item.username?.trim() || item.email?.trim() || t('admin.redeem.userPrefix', { id: item.user_id })

const right = (text: string, strong = false) =>
  h('div', { class: ['text-right tabular-nums', strong && 'font-medium text-highlighted'] }, text)

const modelColumns = computed<TableColumn<ModelStat>[]>(() => [
  {
    accessorKey: 'model',
    header: t('admin.dashboard.model'),
    cell: ({ row }) => h('span', { class: 'block max-w-56 truncate font-medium text-highlighted', title: row.original.model }, row.original.model)
  },
  { id: 'requests', header: () => right(t('admin.dashboard.requests')), cell: ({ row }) => right(formatNumber(row.original.requests)) },
  { id: 'tokens', header: () => right(t('admin.dashboard.tokens')), cell: ({ row }) => right(formatTokens(row.original.total_tokens)) },
  { id: 'actual', header: () => right(t('admin.dashboard.actual')), cell: ({ row }) => right(`$${formatCost(row.original.actual_cost)}`, true) }
])

const rankingColumns = computed<TableColumn<UserSpendingRankingItem>[]>(() => [
  {
    id: 'user',
    header: t('admin.dashboard.spendingRankingUser'),
    cell: ({ row }) => h('span', { class: 'flex min-w-0 items-center gap-2' }, [
      h('span', { class: 'shrink-0 text-xs text-dimmed tabular-nums' }, `#${row.index + 1}`),
      h('span', { class: 'block max-w-48 truncate font-medium text-highlighted', title: rankingUserLabel(row.original) }, rankingUserLabel(row.original))
    ])
  },
  { id: 'requests', header: () => right(t('admin.dashboard.spendingRankingRequests')), cell: ({ row }) => right(formatNumber(row.original.requests)) },
  { id: 'tokens', header: () => right(t('admin.dashboard.spendingRankingTokens')), cell: ({ row }) => right(formatTokens(row.original.tokens)) },
  { id: 'spend', header: () => right(t('admin.dashboard.spendingRankingSpend')), cell: ({ row }) => right(`$${formatCost(row.original.actual_cost)}`, true) }
])

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
const loadDashboardSnapshot = async (includeStats: boolean) => {
  const currentSeq = ++chartLoadSeq
  if (includeStats && !stats.value) {
    loading.value = true
  }
  chartsLoading.value = true
  if (includeStats) dashboardError.value = false
  try {
    const response = await adminAPI.dashboard.getSnapshotV2({
      start_date: startDate.value,
      end_date: endDate.value,
      granularity: granularity.value,
      include_stats: includeStats,
      include_trend: true,
      include_model_stats: true,
      include_group_stats: false,
      include_users_trend: false
    })
    if (currentSeq !== chartLoadSeq) return
    if (includeStats && response.stats) {
      stats.value = response.stats
    }
    trendData.value = response.trend || []
    modelStats.value = response.models || []
  } catch (error) {
    if (currentSeq !== chartLoadSeq) return
    appStore.showError(t('admin.dashboard.failedToLoad'))
    if (includeStats) dashboardError.value = true
    console.error('Error loading dashboard snapshot:', error)
  } finally {
    if (currentSeq === chartLoadSeq) {
      loading.value = false
      chartsLoading.value = false
    }
  }
}

const loadUsersTrend = async () => {
  const currentSeq = ++usersTrendLoadSeq
  userTrendLoading.value = true
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
    rankingItems.value = (response.ranking || []).slice(0, topLimit)
  } catch (error) {
    if (currentSeq !== rankingLoadSeq) return
    console.error('Error loading user spending ranking:', error)
    rankingItems.value = []
    rankingError.value = true
  } finally {
    if (currentSeq === rankingLoadSeq) {
      rankingLoading.value = false
    }
  }
}

const loadDashboardStats = async () => {
  await Promise.all([
    loadDashboardSnapshot(true),
    loadUsersTrend(),
    loadUserSpendingRanking()
  ])
}

const loadChartData = async () => {
  await Promise.all([
    loadDashboardSnapshot(false),
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
/* The shared trend component brings its own `.card`; inside this Nuxt UI card it is only the
   chart. The doubled class outranks the admin skin's `.console-signal.signal-admin .card`. */
.admin-trend-card.admin-trend-card :deep(.card) {
  padding: 0;
  border: 0;
  border-radius: 0;
  background: transparent;
}

.admin-trend-card :deep(.card > h3) { display: none; }
.admin-trend-card :deep(.h-48) { height: 280px; }

</style>
