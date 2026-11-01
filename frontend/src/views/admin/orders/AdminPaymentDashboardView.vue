<template>
  <AppLayout>
    <div class="space-y-6">
      <AdminPageHeader :title="t('nav.paymentDashboard')">
        <template #actions>
          <UTabs
            :model-value="String(days)"
            :items="daysTabs"
            :content="false"
            size="xs"
            @update:model-value="(value: string | number) => (days = Number(value))"
          />
          <UButton
            color="neutral"
            variant="outline"
            icon="i-lucide-refresh-cw"
            square
            :disabled="loading"
            :ui="{ leadingIcon: loading ? 'animate-spin' : '' }"
            :title="t('common.refresh')"
            :aria-label="t('common.refresh')"
            @click="loadDashboard"
          />
        </template>
      </AdminPageHeader>
      <OrdersSubnav />

      <div v-if="loading" class="flex items-center justify-center py-12">
        <LoadingSpinner />
      </div>
      <template v-else-if="stats">
        <OrderStatsCards :stats="stats" />
        <DailyRevenueChart :data="stats.daily_series || []" :loading="loading" />
        <div class="grid grid-cols-1 gap-6 lg:grid-cols-2">
          <UCard :ui="{ header: 'p-4 sm:px-6', body: 'p-4 sm:p-6' }">
            <template #header>
              <p class="text-sm font-semibold text-highlighted">{{ t('payment.admin.paymentDistribution') }}</p>
            </template>
            <p v-if="!stats.payment_methods?.length" class="flex h-32 items-center justify-center text-sm text-muted">{{ t('payment.admin.noData') }}</p>
            <ul v-else class="space-y-3">
              <li v-for="method in stats.payment_methods" :key="method.type" class="flex items-center justify-between gap-3">
                <span class="flex items-center gap-2 text-sm text-default">
                  <span :class="['inline-block size-2.5 rounded-full', methodColor(method.type)]"></span>
                  {{ t('payment.methods.' + method.type, method.type) }}
                </span>
                <span class="text-right">
                  <span v-for="[currency, amount] in sortedAmounts(method.amount)" :key="currency" class="block text-sm font-medium tabular-nums text-highlighted">{{ formatMoney(currency, amount) }}</span>
                  <span class="text-xs text-muted">({{ method.count }})</span>
                </span>
              </li>
            </ul>
          </UCard>
          <UCard :ui="{ header: 'p-4 sm:px-6', body: 'p-4 sm:p-6' }">
            <template #header>
              <p class="text-sm font-semibold text-highlighted">{{ t('payment.admin.topUsers') }}</p>
            </template>
            <p v-if="!hasTopUsers(stats.top_users)" class="flex h-32 items-center justify-center text-sm text-muted">{{ t('payment.admin.noData') }}</p>
            <div v-else class="space-y-3">
              <div v-for="[currency, users] in sortedTopUsers(stats.top_users)" :key="currency" class="space-y-1">
                <p class="text-xs font-medium text-muted">{{ currency }}</p>
                <div v-for="(user, idx) in users" :key="user.user_id" class="flex items-center justify-between gap-3 py-1.5">
                  <span class="flex min-w-0 items-center gap-3">
                    <UBadge :color="idx === 0 ? 'primary' : 'neutral'" :variant="idx < 3 ? 'subtle' : 'outline'" size="sm" class="w-6 justify-center tabular-nums" :label="String(idx + 1)" />
                    <span class="truncate text-sm text-default">{{ user.email }}</span>
                  </span>
                  <span class="text-sm font-medium tabular-nums text-highlighted">{{ formatMoney(currency, user.amount) }}</span>
                </div>
              </div>
            </div>
          </UCard>
        </div>
      </template>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, ref, watch, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { adminPaymentAPI } from '@/api/admin/payment'
import { extractI18nErrorMessage } from '@/utils/apiError'
import type { CurrencyAmounts, DashboardStats, TopUserPaymentStats } from '@/types/payment'
import AppLayout from '@/components/layout/AppLayout.vue'
import AdminPageHeader from '@/components/admin/page/AdminPageHeader.vue'
import OrdersSubnav from './OrdersSubnav.vue'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import OrderStatsCards from '@/components/admin/payment/OrderStatsCards.vue'
import DailyRevenueChart from '@/components/admin/payment/DailyRevenueChart.vue'

const { t } = useI18n()
const appStore = useAppStore()

const DAYS_OPTIONS = [7, 30, 90] as const
const days = ref<number>(30)
const daysTabs = computed(() => DAYS_OPTIONS.map((d) => ({ label: d + t('payment.admin.daySuffix'), value: String(d) })))
const loading = ref(false)
const stats = ref<DashboardStats | null>(null)

function methodColor(type: string): string {
  const c: Record<string, string> = {
    alipay: 'bg-blue-500', wxpay: 'bg-green-500',
    alipay_direct: 'bg-blue-400', wxpay_direct: 'bg-green-400',
    stripe: 'bg-purple-500',
  }
  return c[type] || 'bg-gray-400'
}

function sortedAmounts(amounts: CurrencyAmounts): [string, number][] {
  return Object.entries(amounts).sort(([left], [right]) => left.localeCompare(right))
}

function sortedTopUsers(usersByCurrency: Record<string, TopUserPaymentStats[]>): [string, TopUserPaymentStats[]][] {
  return Object.entries(usersByCurrency).sort(([left], [right]) => left.localeCompare(right))
}

function hasTopUsers(usersByCurrency: Record<string, TopUserPaymentStats[]>): boolean {
  return Object.values(usersByCurrency).some(users => users.length > 0)
}

function formatMoney(currency: string, amount: number): string {
  return new Intl.NumberFormat(undefined, { style: 'currency', currency }).format(amount)
}

async function loadDashboard() {
  loading.value = true
  try {
    const res = await adminPaymentAPI.getDashboard(days.value)
    stats.value = res.data
  } catch (err: unknown) {
    appStore.showError(extractI18nErrorMessage(err, t, 'payment.errors', t('common.error')))
  } finally {
    loading.value = false
  }
}

watch(days, () => loadDashboard())
onMounted(() => loadDashboard())
</script>
