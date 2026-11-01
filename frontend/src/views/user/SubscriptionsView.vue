<template>
  <AppLayout>
    <div class="flex flex-col gap-4 sm:gap-6">
      <UserPageHeader flush :title="t('userSubscriptions.title')" :description="t('userSubscriptions.description')" />

      <div v-if="loading" class="flex justify-center py-12">
        <UIcon name="i-lucide-loader-circle" class="size-8 animate-spin text-muted" />
      </div>

      <div v-else-if="subscriptions.length === 0" class="flex flex-col items-center gap-2 py-16 text-center">
        <UIcon name="i-lucide-credit-card" class="size-10 text-dimmed" />
        <h3 class="text-base font-semibold text-highlighted">{{ t('userSubscriptions.noActiveSubscriptions') }}</h3>
        <p class="text-sm text-muted">{{ t('userSubscriptions.noActiveSubscriptionsDesc') }}</p>
      </div>

      <div v-else class="grid grid-cols-1 gap-4 lg:grid-cols-2">
        <UCard v-for="subscription in subscriptions" :key="subscription.id" :ui="{ header: 'p-4 sm:px-5', body: 'p-4 sm:p-5' }">
          <template #header>
            <div class="flex items-start justify-between gap-3">
              <div class="flex min-w-0 items-start gap-3">
                <PlatformIcon :platform="subscription.group?.platform" size="md" class="mt-0.5 shrink-0" />
                <div class="min-w-0">
                  <div class="flex flex-wrap items-center gap-2">
                    <h3 class="font-semibold text-highlighted">{{ subscription.group?.name || `Group #${subscription.group_id}` }}</h3>
                    <UBadge color="neutral" variant="subtle" size="sm">{{ platformLabel(subscription.group?.platform || '') }}</UBadge>
                  </div>
                  <p v-if="subscription.group?.description" class="mt-0.5 text-xs text-muted">{{ subscription.group.description }}</p>
                  <div class="mt-1 flex flex-wrap gap-x-3 gap-y-1 text-xs text-muted">
                    <span>{{ t('payment.planCard.rate') }}: ×{{ subscription.group?.rate_multiplier ?? 1 }}</span>
                    <span v-if="subscriptionHasPeakRate(subscription)" class="text-warning">
                      {{ t('payment.planCard.peakRate') }}: {{ subscriptionPeakRateLabel(subscription) }}
                    </span>
                  </div>
                </div>
              </div>
              <div class="flex shrink-0 items-center gap-1.5">
                <UBadge :color="subscription.status === 'active' ? 'success' : subscription.status === 'expired' ? 'neutral' : 'error'" variant="subtle">
                  {{ t(`userSubscriptions.status.${subscription.status}`) }}
                </UBadge>
                <UButton
                  v-if="subscription.status === 'active'"
                  color="neutral"
                  variant="outline"
                  size="sm"
                  icon="i-lucide-refresh-cw"
                  @click="router.push({ path: '/purchase', query: { tab: 'subscription', group: String(subscription.group_id) } })"
                >
                  {{ t('payment.renewNow') }}
                </UButton>
              </div>
            </div>
          </template>

          <div class="space-y-4">
            <div class="flex items-center justify-between gap-3 text-sm">
              <span class="text-muted">{{ t('userSubscriptions.expires') }}</span>
              <span v-if="subscription.expires_at" class="tabular-nums" :class="getExpirationClass(subscription.expires_at)">{{ formatExpirationDate(subscription.expires_at) }}</span>
              <span v-else class="text-highlighted">{{ t('userSubscriptions.noExpiration') }}</span>
            </div>

            <div v-for="win in usageWindows(subscription)" :key="win.key" class="space-y-1.5">
              <div class="flex items-center justify-between gap-3 text-sm">
                <span class="font-medium text-highlighted">{{ win.label }}</span>
                <span class="tabular-nums text-muted">${{ win.used.toFixed(2) }} / ${{ win.limit.toFixed(2) }}</span>
              </div>
              <UProgress :model-value="usagePercent(win.used, win.limit)" :color="usageColor(win.used, win.limit)" size="sm" />
              <p v-if="win.resetText" class="text-xs text-muted">{{ win.resetText }}</p>
            </div>

            <div
              v-if="!subscription.group?.daily_limit_usd && !subscription.group?.weekly_limit_usd && !subscription.group?.monthly_limit_usd"
              class="flex items-center gap-3"
            >
              <UIcon name="i-lucide-infinity" class="size-8 text-success" />
              <div>
                <p class="text-sm font-medium text-highlighted">{{ t('userSubscriptions.unlimited') }}</p>
                <p class="text-xs text-muted">{{ t('userSubscriptions.unlimitedDesc') }}</p>
              </div>
            </div>
          </div>
        </UCard>
      </div>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { useAppStore } from '@/stores/app'
import subscriptionsAPI from '@/api/subscriptions'
import type { UserSubscription } from '@/types'
import AppLayout from '@/components/layout/AppLayout.vue'
import UserPageHeader from '@/components/user/UserPageHeader.vue'
import PlatformIcon from '@/components/common/PlatformIcon.vue'
import { formatDateTimeToMinute } from '@/utils/format'
import { hasPeakRate, formatPeakRateWindow, serverTimezoneLabel } from '@/utils/peak-rate'
import { platformLabel } from '@/utils/platformColors'
import {
  getExpirationDateRelation,
  getRemainingDurationParts,
  isOneTimeDailyQuota,
  type RemainingDurationParts
} from '@/utils/subscriptionQuota'

const { t } = useI18n()
const router = useRouter()
const appStore = useAppStore()

const subscriptions = ref<UserSubscription[]>([])
const loading = ref(true)

function subscriptionHasPeakRate(subscription: UserSubscription): boolean {
  return hasPeakRate(subscription.group)
}

function subscriptionPeakRateLabel(subscription: UserSubscription): string {
  return formatPeakRateWindow(subscription.group, serverTimezoneLabel(appStore.cachedPublicSettings?.server_utc_offset))
}

async function loadSubscriptions() {
  try {
    loading.value = true
    subscriptions.value = await subscriptionsAPI.getMySubscriptions()
  } catch (error) {
    console.error('Failed to load subscriptions:', error)
    appStore.showError(t('userSubscriptions.failedToLoad'))
  } finally {
    loading.value = false
  }
}

function usagePercent(used: number | undefined, limit: number | null | undefined): number {
  if (!limit || limit === 0) return 0
  return Math.min(((used || 0) / limit) * 100, 100)
}

// Same thresholds as before: >= 90 % error, >= 70 % warning, otherwise the theme colour.
function usageColor(used: number | undefined, limit: number | null | undefined) {
  if (!limit || limit === 0) return 'neutral' as const
  const percentage = ((used || 0) / limit) * 100
  if (percentage >= 90) return 'error' as const
  if (percentage >= 70) return 'warning' as const
  return 'primary' as const
}

// Daily / weekly / monthly windows that have a limit, with their reset hint.
function usageWindows(subscription: UserSubscription) {
  const group = subscription.group
  const windows: Array<{ key: string; label: string; used: number; limit: number; resetText: string }> = []
  if (group?.daily_limit_usd) {
    windows.push({ key: 'daily', label: t('userSubscriptions.daily'), used: subscription.daily_usage_usd || 0, limit: group.daily_limit_usd,
      resetText: subscription.daily_window_start ? formatDailyUsageWindow(subscription) : '' })
  }
  if (group?.weekly_limit_usd) {
    windows.push({ key: 'weekly', label: t('userSubscriptions.weekly'), used: subscription.weekly_usage_usd || 0, limit: group.weekly_limit_usd,
      resetText: subscription.weekly_window_start ? t('userSubscriptions.resetIn', { time: formatResetTime(subscription.weekly_window_start, 168) }) : '' })
  }
  if (group?.monthly_limit_usd) {
    windows.push({ key: 'monthly', label: t('userSubscriptions.monthly'), used: subscription.monthly_usage_usd || 0, limit: group.monthly_limit_usd,
      resetText: subscription.monthly_window_start ? t('userSubscriptions.resetIn', { time: formatResetTime(subscription.monthly_window_start, 720) }) : '' })
  }
  return windows
}

function formatExpirationDate(expiresAt: string): string {
  const now = new Date()
  const expires = new Date(expiresAt)
  const diff = expires.getTime() - now.getTime()
  const days = Math.ceil(diff / (1000 * 60 * 60 * 24))
  const relation = getExpirationDateRelation(expires, now)

  if (relation === null) return ''

  if (relation === 'expired') {
    return t('userSubscriptions.status.expired')
  }

  const dateStr = formatDateTimeToMinute(expires)

  if (relation === 'today') {
    return `${dateStr} (${t('common.today')})`
  }
  if (relation === 'tomorrow') {
    return `${dateStr} (${t('common.tomorrow')})`
  }

  return t('userSubscriptions.daysRemaining', { days }) + ` (${dateStr})`
}

function getExpirationClass(expiresAt: string): string {
  const now = new Date()
  const expires = new Date(expiresAt)
  const diff = expires.getTime() - now.getTime()
  const days = Math.ceil(diff / (1000 * 60 * 60 * 24))

  if (diff <= 0) return 'text-error font-medium'
  if (days <= 3) return 'text-error'
  if (days <= 7) return 'text-warning'
  return 'text-highlighted'
}

function formatDurationParts(parts: RemainingDurationParts): string {
  if (parts.days > 0) {
    return `${parts.days}d ${parts.hours}h`
  }

  if (parts.hours > 0) {
    return `${parts.hours}h ${parts.minutes}m`
  }

  return `${parts.minutes}m`
}

function formatDailyUsageWindow(subscription: UserSubscription): string {
  if (isOneTimeDailyQuota(subscription) && subscription.expires_at) {
    const parts = getRemainingDurationParts(subscription.expires_at)
    if (!parts) return t('userSubscriptions.windowNotActive')
    return t('userSubscriptions.quotaEndsIn', { time: formatDurationParts(parts) })
  }

  return t('userSubscriptions.resetIn', {
    time: formatResetTime(subscription.daily_window_start, 24)
  })
}

function formatResetTime(windowStart: string | null, windowHours: number): string {
  if (!windowStart) return t('userSubscriptions.windowNotActive')

  const start = new Date(windowStart)
  const end = new Date(start.getTime() + windowHours * 60 * 60 * 1000)
  const parts = getRemainingDurationParts(end)

  return parts ? formatDurationParts(parts) : t('userSubscriptions.windowNotActive')
}

onMounted(() => {
  loadSubscriptions()
})
</script>
