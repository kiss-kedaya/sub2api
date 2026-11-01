<template>
  <AppLayout>
    <section class="user-dashboard flex min-w-0 flex-col gap-4 sm:gap-6" :aria-label="t('dashboard.overview')">
      <DashboardToolbar
        v-model:startDate="startDate"
        v-model:endDate="endDate"
        v-model:granularity="granularity"
        :granularity-items="granularityItems"
        :granularity-label="t('dashboard.granularity')"
        :refresh-label="t('common.refresh')"
        :refreshing="loading || loadingCharts"
        @dateRangeChange="changeChartRange"
        @granularityChange="loadCharts"
        @refresh="refreshAll"
      />
      <div v-if="loading && !stats" class="flex justify-center py-10"><LoadingSpinner /></div>
      <UAlert
        v-if="statsError"
        role="alert"
        color="error"
        variant="subtle"
        orientation="horizontal"
        icon="i-lucide-circle-alert"
        :title="t('dashboard.loadFailed')"
      >
        <template #actions>
          <UButton color="neutral" variant="outline" size="sm" @click="refreshAll">{{ t('common.refresh') }}</UButton>
        </template>
      </UAlert>
      <template v-if="stats">
        <UserDashboardStats section="summary" :stats="stats" :balance="user?.balance || 0" :is-simple="authStore.isSimpleMode" />
        <UserDashboardQuickActions />
        <UserDashboardCharts :loading="loadingCharts" :error="chartsError" :trend="trendData" @refresh="loadCharts" />
        <UserDashboardModels :models="modelStats" :start-date="startDate" :end-date="endDate" :loading="loadingCharts" :error="chartsError" />
        <!-- Two folds; each loads its data only when opened (UCollapsible unmounts closed content). -->
        <div class="divide-y divide-default overflow-hidden rounded-lg bg-default ring ring-default">
          <UCollapsible v-if="!authStore.isSimpleMode" :open="platformsOpen" data-detail="platforms" @update:open="setPlatformsOpen">
            <UButton v-bind="foldTrigger">
              <UIcon name="i-lucide-layout-grid" class="size-4 shrink-0 text-muted" />
              <span class="min-w-0 flex-1 truncate text-start">{{ t('dashboard.platformBreakdown') }}</span>
              <UIcon name="i-lucide-chevron-down" :class="foldChevron" />
            </UButton>
            <template #content>
            <div class="border-t border-default px-4 pb-4 sm:px-5">
              <div v-if="loadingQuotas" class="flex justify-center py-10"><LoadingSpinner /></div>
              <UAlert
                v-else-if="quotasError"
                role="alert"
                class="mt-4"
                color="error"
                variant="subtle"
                orientation="horizontal"
                icon="i-lucide-circle-alert"
                :title="t('dashboard.chartsFailed')"
              >
                <template #actions>
                  <UButton color="neutral" variant="outline" size="sm" @click="loadQuotas(true)">{{ t('common.refresh') }}</UButton>
                </template>
              </UAlert>
              <UserDashboardStats v-else section="platforms" :stats="stats" :balance="user?.balance || 0" :is-simple="false" :platform-quotas="platformQuotas" />
            </div>
            </template>
          </UCollapsible>
          <UCollapsible :open="recentOpen" data-detail="recent" @update:open="setRecentOpen">
            <UButton v-bind="foldTrigger">
              <UIcon name="i-lucide-clock" class="size-4 shrink-0 text-muted" />
              <span class="min-w-0 flex-1 truncate text-start">{{ t('dashboard.recentUsage') }}</span>
              <UIcon name="i-lucide-chevron-down" :class="foldChevron" />
            </UButton>
            <template #content>
            <div class="border-t border-default px-4 pb-4 sm:px-5">
              <UAlert
                v-if="recentError"
                role="alert"
                class="mt-4"
                color="error"
                variant="subtle"
                orientation="horizontal"
                icon="i-lucide-circle-alert"
                :title="t('dashboard.chartsFailed')"
              >
                <template #actions>
                  <UButton color="neutral" variant="outline" size="sm" @click="loadRecent()">{{ t('common.refresh') }}</UButton>
                </template>
              </UAlert>
              <UserDashboardRecentUsage v-else :data="recentUsage" :loading="loadingUsage" :start-date="startDate" :end-date="endDate" />
            </div>
            </template>
          </UCollapsible>
        </div>
      </template>
    </section>
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
import DashboardToolbar from '@/components/dashboard/DashboardToolbar.vue'
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
const granularityItems = computed(() => [
  { value: 'day', label: t('dashboard.day') },
  { value: 'hour', label: t('dashboard.hour') },
])

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

// Fold rows: full-width ghost UButton as the UCollapsible trigger. `group` lets the chevron
// follow the trigger's data-state.
const foldTrigger = {
  color: 'neutral',
  variant: 'ghost',
  block: true,
  class: 'group justify-start gap-2.5 rounded-none px-4 py-0 min-h-13 text-sm font-medium text-highlighted sm:px-5',
} as const
const foldChevron = 'size-4 shrink-0 text-muted transition-transform duration-200 group-data-[state=open]:rotate-180 motion-reduce:transition-none'

function setPlatformsOpen(open: boolean) {
  platformsOpen.value = open
  if (open) void loadQuotas()
}

function setRecentOpen(open: boolean) {
  recentOpen.value = open
  if (open && !recentLoaded) void loadRecent()
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
.user-dashboard { color: var(--ui-text); }
</style>
