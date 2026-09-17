<template>
  <AppLayout>
    <div class="signal-dashboard">
      <header class="signal-masthead">
        <div class="signal-identity">
          <span class="signal-mark" aria-hidden="true"><Icon name="chart" size="lg" /></span>
          <div class="signal-heading">
            <h1>{{ appStore.siteName }}</h1>
            <span class="signal-page-label">{{ t('dashboard.title') }}</span>
          </div>
        </div>
      </header>
      <div v-if="loading" class="signal-loading"><LoadingSpinner /></div>
      <template v-else-if="stats">
        <UserDashboardQuickActions />
        <UserDashboardStats :stats="stats" :balance="user?.balance || 0" :is-simple="authStore.isSimpleMode" :platform-quotas="platformQuotas" />
        <UserDashboardCharts v-model:startDate="startDate" v-model:endDate="endDate" v-model:granularity="granularity" :loading="loadingCharts" :trend="trendData" :models="modelStats" @dateRangeChange="loadCharts" @granularityChange="loadCharts" @refresh="refreshAll" />
        <UserDashboardRecentUsage :data="recentUsage" :loading="loadingUsage" />
      </template>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'; import { useAuthStore } from '@/stores/auth'; import { usageAPI, type UserDashboardStats as UserStatsType } from '@/api/usage'
import AppLayout from '@/components/layout/AppLayout.vue'; import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import UserDashboardStats from '@/components/user/dashboard/UserDashboardStats.vue'; import UserDashboardCharts from '@/components/user/dashboard/UserDashboardCharts.vue'
import UserDashboardRecentUsage from '@/components/user/dashboard/UserDashboardRecentUsage.vue'; import UserDashboardQuickActions from '@/components/user/dashboard/UserDashboardQuickActions.vue'
import type { UsageLog, TrendDataPoint, ModelStat, PlatformQuotaItem } from '@/types'
import { useAppStore } from '@/stores/app'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import { getMyPlatformQuotas } from '@/api/user'
import { formatDateLocalInput } from '@/utils/format'

const appStore = useAppStore()
const { t } = useI18n()
const authStore = useAuthStore(); const user = computed(() => authStore.user)
const stats = ref<UserStatsType | null>(null); const loading = ref(false); const loadingUsage = ref(false); const loadingCharts = ref(false)
const trendData = ref<TrendDataPoint[]>([]); const modelStats = ref<ModelStat[]>([]); const recentUsage = ref<UsageLog[]>([])
const platformQuotas = ref<PlatformQuotaItem[] | null>(null)

const startDate = ref(formatDateLocalInput(new Date(Date.now() - 6 * 86400000))); const endDate = ref(formatDateLocalInput(new Date())); const granularity = ref('day')

const loadStats = async () => { loading.value = true; try { await authStore.refreshUser(); stats.value = await usageAPI.getDashboardStats() } catch (error) { console.error('Failed to load dashboard stats:', error) } finally { loading.value = false } }
const loadCharts = async () => { loadingCharts.value = true; try { const res = await Promise.all([usageAPI.getDashboardTrend({ start_date: startDate.value, end_date: endDate.value, granularity: granularity.value as any }), usageAPI.getDashboardModels({ start_date: startDate.value, end_date: endDate.value })]); trendData.value = res[0].trend || []; modelStats.value = res[1].models || [] } catch (error) { console.error('Failed to load charts:', error) } finally { loadingCharts.value = false } }
const loadRecent = async () => { loadingUsage.value = true; try { const res = await usageAPI.getByDateRange(startDate.value, endDate.value); recentUsage.value = res.items.slice(0, 5) } catch (error) { console.error('Failed to load recent usage:', error) } finally { loadingUsage.value = false } }
const loadPlatformQuotas = async () => { try { const data = await getMyPlatformQuotas(); platformQuotas.value = data.platform_quotas ?? [] } catch (error) { console.warn('Failed to load platform quotas:', error); platformQuotas.value = [] } }
const refreshAll = () => { loadStats(); loadCharts(); loadRecent(); loadPlatformQuotas() }

onMounted(() => { refreshAll() })
</script>

<style scoped>
.signal-dashboard {
  min-width: 0;
  color: var(--signal-text, #202423);
  background: var(--signal-bg, #f5f6f5);
  letter-spacing: 0;
}
.signal-masthead {
  padding: 0 0 18px;
  border-bottom: 1px solid var(--signal-line, #dce2df);
}
.signal-identity, .signal-heading { display: flex; align-items: center; gap: 14px; min-width: 0; }
.signal-mark {
  display: grid;
  place-items: center;
  width: 38px;
  height: 38px;
  flex: 0 0 auto;
  color: var(--signal-accent, #087f68);
  border: 1px solid var(--signal-line, #dce2df);
  border-radius: 6px;
  background: var(--signal-surface, #fff);
  box-shadow: inset 0 0 12px color-mix(in srgb, var(--signal-accent, #087f68) 8%, transparent);
}
.signal-heading h1 {
  margin: 0;
  font-size: 28px;
  line-height: 1.2;
  font-weight: 700;
  overflow-wrap: anywhere;
}
.signal-page-label {
  padding-left: 14px;
  border-left: 1px solid var(--signal-line, #dce2df);
  color: var(--signal-muted, #65716a);
  font-size: 13px;
  flex-shrink: 0;
}
.signal-loading { display: flex; justify-content: center; padding: 64px 0; }
@media (max-width: 480px) {
  .signal-heading { flex-wrap: wrap; gap: 4px 12px; }
  .signal-heading h1 { font-size: 24px; }
  .signal-page-label { border: 0; padding: 0; }
}
</style>
