<template>
  <AppLayout>
    <div class="signal-dashboard">
      <header class="signal-masthead">
        <h1>{{ t('dashboard.overview') }}</h1>
        <button class="btn btn-secondary signal-overview-refresh" :disabled="loading || loadingCharts" @click="refreshAll">
          <Icon name="refresh" size="sm" :class="{ 'animate-spin': loading || loadingCharts }" />
          {{ t('common.refresh') }}
        </button>
      </header>
      <div v-if="loading && !stats" class="signal-loading"><LoadingSpinner /></div>
      <div v-if="statsError" class="signal-error" role="alert">
        <Icon name="exclamationCircle" size="md" />
        <span>{{ t('dashboard.loadFailed') }}</span>
        <button class="btn btn-secondary" @click="refreshAll">{{ t('common.refresh') }}</button>
      </div>
      <template v-if="stats">
        <UserDashboardStats section="summary" :stats="stats" :balance="user?.balance || 0" :is-simple="authStore.isSimpleMode">
          <template #actions><UserDashboardQuickActions /></template>
          <template #models>
            <UserDashboardModels :models="modelStats" :start-date="startDate" :end-date="endDate" :loading="loadingCharts" :error="chartsError" />
          </template>
        </UserDashboardStats>
        <UserDashboardCharts
          v-model:startDate="startDate" v-model:endDate="endDate" v-model:granularity="granularity"
          :loading="loadingCharts" :error="chartsError" :trend="trendData"
          @dateRangeChange="changeChartRange" @granularityChange="loadCharts" @refresh="loadCharts"
        />
        <div class="signal-dashboard-details">
          <details v-if="!authStore.isSimpleMode" @toggle="togglePlatforms" class="signal-disclosure" data-detail="platforms">
            <summary><Icon name="grid" size="sm" /><span>{{ t('dashboard.platformBreakdown') }}</span><Icon name="chevronDown" size="sm" /></summary>
            <template v-if="platformsOpen">
              <div v-if="loadingQuotas" class="signal-loading"><LoadingSpinner /></div>
              <div v-else-if="quotasError" class="signal-error" role="alert">
                <span>{{ t('dashboard.chartsFailed') }}</span>
                <button class="btn btn-secondary" @click="loadQuotas(true)">{{ t('common.refresh') }}</button>
              </div>
              <UserDashboardStats v-else section="platforms" :stats="stats" :balance="user?.balance || 0" :is-simple="false" :platform-quotas="platformQuotas" />
            </template>
          </details>
          <details @toggle="toggleRecent" class="signal-disclosure" data-detail="recent">
            <summary><Icon name="clock" size="sm" /><span>{{ t('dashboard.recentUsage') }}</span><Icon name="chevronDown" size="sm" /></summary>
            <template v-if="recentOpen">
              <div v-if="recentError" class="signal-error" role="alert">
                <span>{{ t('dashboard.chartsFailed') }}</span>
                <button class="btn btn-secondary" @click="loadRecent()">{{ t('common.refresh') }}</button>
              </div>
              <UserDashboardRecentUsage v-else :data="recentUsage" :loading="loadingUsage" :start-date="startDate" :end-date="endDate" />
            </template>
          </details>
        </div>
      </template>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAuthStore } from '@/stores/auth'
import { usageAPI, type UserDashboardStats as UserStatsType } from '@/api/usage'
import { getMyPlatformQuotas } from '@/api/user'
import { formatDateLocalInput } from '@/utils/format'
import type { UsageLog, TrendDataPoint, ModelStat, PlatformQuotaItem } from '@/types'
import AppLayout from '@/components/layout/AppLayout.vue'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import Icon from '@/components/icons/Icon.vue'
import UserDashboardStats from '@/components/user/dashboard/UserDashboardStats.vue'
import UserDashboardCharts from '@/components/user/dashboard/UserDashboardCharts.vue'
import UserDashboardModels from '@/components/user/dashboard/UserDashboardModels.vue'
import UserDashboardRecentUsage from '@/components/user/dashboard/UserDashboardRecentUsage.vue'
import UserDashboardQuickActions from '@/components/user/dashboard/UserDashboardQuickActions.vue'

const { t } = useI18n()
const authStore = useAuthStore()
const user = computed(() => authStore.user)
const stats = ref<UserStatsType | null>(null)
const trendData = ref<TrendDataPoint[]>([])
const modelStats = ref<ModelStat[]>([])
const recentUsage = ref<UsageLog[]>([])
const platformQuotas = ref<PlatformQuotaItem[]>([])
const loading = ref(false)
const loadingCharts = ref(false)
const loadingUsage = ref(false)
const loadingQuotas = ref(false)
const statsError = ref(false)
const chartsError = ref(false)
const recentError = ref(false)
const quotasError = ref(false)
const recentOpen = ref(false)
const platformsOpen = ref(false)
let recentLoaded = false
let quotasLoaded = false
let disposed = false
let chartsRequest: AbortController | undefined
let recentRequest: AbortController | undefined

const startDate = ref(formatDateLocalInput(new Date(Date.now() - 6 * 86400000)))
const endDate = ref(formatDateLocalInput(new Date()))
const granularity = ref('day')

async function loadStats() {
  if (loading.value) return
  loading.value = true
  statsError.value = false
  try {
    const [, result] = await Promise.all([authStore.refreshUser(), usageAPI.getDashboardStats()])
    if (!disposed) stats.value = result
  } catch {
    if (!disposed) statsError.value = true
  } finally {
    if (!disposed) loading.value = false
  }
}

async function loadCharts() {
  chartsRequest?.abort()
  const request = new AbortController()
  chartsRequest = request
  loadingCharts.value = true
  chartsError.value = false
  trendData.value = []
  modelStats.value = []
  try {
    const result = await usageAPI.getDashboardSnapshotV2({
      start_date: startDate.value, end_date: endDate.value,
      granularity: granularity.value === 'hour' ? 'hour' : 'day',
      include_trend: true, include_model_stats: true, include_group_stats: false,
    }, { signal: request.signal })
    if (disposed || request !== chartsRequest) return
    trendData.value = result.trend ?? []
    modelStats.value = result.models ?? []
  } catch {
    if (!disposed && request === chartsRequest && !request.signal.aborted) chartsError.value = true
  } finally {
    if (!disposed && request === chartsRequest) loadingCharts.value = false
  }
}

async function loadRecent() {
  recentRequest?.abort()
  const request = new AbortController()
  recentRequest = request
  loadingUsage.value = true
  recentError.value = false
  try {
    const result = await usageAPI.query({
      start_date: startDate.value, end_date: endDate.value,
      page: 1, page_size: 5, sort_by: 'created_at', sort_order: 'desc',
    }, { signal: request.signal })
    if (disposed || request !== recentRequest) return
    recentUsage.value = result.items ?? []
    recentLoaded = true
  } catch {
    if (!disposed && request === recentRequest && !request.signal.aborted) recentError.value = true
  } finally {
    if (!disposed && request === recentRequest) loadingUsage.value = false
  }
}

async function loadQuotas(force = false) {
  if (loadingQuotas.value || (quotasLoaded && !force)) return
  loadingQuotas.value = true
  quotasError.value = false
  try {
    const result = await getMyPlatformQuotas()
    if (disposed) return
    platformQuotas.value = result.platform_quotas ?? []
    quotasLoaded = true
  } catch {
    if (!disposed) quotasError.value = true
  } finally {
    if (!disposed) loadingQuotas.value = false
  }
}

function togglePlatforms(event: Event) {
  platformsOpen.value = (event.currentTarget as HTMLDetailsElement).open
  if (platformsOpen.value) void loadQuotas()
}

function toggleRecent(event: Event) {
  recentOpen.value = (event.currentTarget as HTMLDetailsElement).open
  if (recentOpen.value && !recentLoaded) void loadRecent()
}

function invalidateRecent() {
  recentRequest?.abort()
  recentRequest = undefined
  recentLoaded = false
  loadingUsage.value = false
}

function changeChartRange() {
  void loadCharts()
  invalidateRecent()
  if (recentOpen.value) void loadRecent()
}

function refreshAll() {
  void loadStats()
  void loadCharts()
  invalidateRecent()
  quotasLoaded = false
  if (recentOpen.value) void loadRecent()
  if (platformsOpen.value) void loadQuotas(true)
}

onMounted(refreshAll)
onUnmounted(() => {
  disposed = true
  chartsRequest?.abort()
  recentRequest?.abort()
})
</script>

<style scoped>
.signal-dashboard { min-width: 0; color: var(--signal-text); }
.signal-masthead { display: flex; align-items: center; justify-content: space-between; gap: 16px; padding-bottom: 20px; }
.signal-masthead h1 { font-size: 20px; line-height: 28px; font-weight: 600; }
.signal-overview-refresh { gap: 8px; min-height: 34px; font-size: 12px; }
.signal-loading { display: flex; justify-content: center; padding: 40px 0; }
.signal-error { display: flex; flex-wrap: wrap; align-items: center; gap: 12px; padding: 24px 0; color: var(--signal-muted); font-size: 13px; }
.signal-dashboard-details { margin-top: 24px; border-top: 1px solid var(--signal-line); }
.signal-disclosure { border-bottom: 1px solid var(--signal-line); }
.signal-disclosure > summary { display: flex; align-items: center; gap: 10px; min-height: 52px; list-style: none; cursor: pointer; font-size: 13px; font-weight: 500; }
.signal-disclosure > summary::-webkit-details-marker { display: none; }
.signal-disclosure > summary > span { flex: 1; min-width: 0; }
.signal-disclosure > summary > svg { color: var(--signal-muted); flex-shrink: 0; }
.signal-disclosure[open] > summary > svg:last-child { transform: rotate(180deg); }
.signal-disclosure > summary:focus-visible { outline: 2px solid var(--signal-accent); outline-offset: -2px; }
</style>
