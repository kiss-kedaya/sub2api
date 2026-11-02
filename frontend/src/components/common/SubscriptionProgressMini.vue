<template>
  <div v-if="hasActiveSubscriptions">
    <UPopover v-model:open="tooltipOpen" :content="{ align: 'end' }">
      <!-- Mini Progress Display -->
      <UButton
        color="neutral"
        variant="ghost"
        icon="i-lucide-credit-card"
        :title="t('subscriptionProgress.viewDetails')"
        class="gap-2"
      >
        <span class="flex items-center gap-0.5" aria-hidden="true">
          <span
            v-for="(sub, index) in displaySubscriptions.slice(0, 3)"
            :key="index"
            class="size-2 rounded-full"
            :class="getProgressDotClass(sub)"
          ></span>
        </span>
        <span class="text-xs font-medium tabular-nums">{{ activeSubscriptions.length }}</span>
      </UButton>

      <template #content>
        <div class="w-[340px] max-w-[calc(100vw-1rem)]">
          <div class="px-4 pb-2 pt-3">
            <h3 class="text-sm font-semibold text-highlighted">{{ t('subscriptionProgress.title') }}</h3>
            <p class="mt-0.5 text-xs text-muted">
              {{ t('subscriptionProgress.activeCount', { count: activeSubscriptions.length }) }}
            </p>
          </div>

          <div class="max-h-64 space-y-3 overflow-y-auto px-4 py-2">
            <div v-for="subscription in displaySubscriptions" :key="subscription.id">
              <div class="mb-1.5 flex items-center justify-between gap-2">
                <span class="truncate text-sm font-medium text-highlighted">
                  {{ subscription.group?.name || `Group #${subscription.group_id}` }}
                </span>
                <span
                  v-if="subscription.expires_at"
                  class="shrink-0 text-xs"
                  :class="getDaysRemainingClass(subscription.expires_at)"
                >
                  {{ formatDaysRemaining(subscription.expires_at) }}
                </span>
              </div>

              <!-- Unlimited subscription badge -->
              <UBadge
                v-if="isUnlimited(subscription)"
                color="success"
                variant="subtle"
                icon="i-lucide-infinity"
                :label="t('subscriptionProgress.unlimited')"
              />

              <!-- Progress bars for limited subscriptions -->
              <div v-else class="space-y-1.5">
                <div v-for="row in limitRows(subscription)" :key="row.key" class="flex items-center gap-2">
                  <span class="w-8 shrink-0 text-[10px] text-muted">{{ row.label }}</span>
                  <UProgress
                    :model-value="getProgressValue(row.used, row.limit)"
                    :color="getProgressColor(row.used, row.limit)"
                    size="xs"
                    class="min-w-0 flex-1"
                  />
                  <span class="w-24 shrink-0 text-right text-[10px] text-muted tabular-nums">
                    {{ formatUsage(row.used, row.limit) }}
                  </span>
                </div>
              </div>
            </div>
          </div>

          <div class="p-1.5">
            <UButton
              to="/subscriptions"
              color="neutral"
              variant="ghost"
              size="sm"
              block
              :label="t('subscriptionProgress.viewAll')"
              @click="closeTooltip"
            />
          </div>
        </div>
      </template>
    </UPopover>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useSubscriptionStore } from '@/stores'
import type { UserSubscription } from '@/types'

const { t } = useI18n()

const subscriptionStore = useSubscriptionStore()

// The popover closes itself on outside click / Escape.
const tooltipOpen = ref(false)

// Use store data instead of local state
const activeSubscriptions = computed(() => subscriptionStore.activeSubscriptions)
const hasActiveSubscriptions = computed(() => subscriptionStore.hasActiveSubscriptions)

const displaySubscriptions = computed(() => {
  // Sort by most usage (highest percentage first)
  return [...activeSubscriptions.value].sort((a, b) => {
    const aMax = getMaxUsagePercentage(a)
    const bMax = getMaxUsagePercentage(b)
    return bMax - aMax
  })
})

function getMaxUsagePercentage(sub: UserSubscription): number {
  const percentages: number[] = []
  if (sub.group?.daily_limit_usd) {
    percentages.push(((sub.daily_usage_usd || 0) / sub.group.daily_limit_usd) * 100)
  }
  if (sub.group?.weekly_limit_usd) {
    percentages.push(((sub.weekly_usage_usd || 0) / sub.group.weekly_limit_usd) * 100)
  }
  if (sub.group?.monthly_limit_usd) {
    percentages.push(((sub.monthly_usage_usd || 0) / sub.group.monthly_limit_usd) * 100)
  }
  return percentages.length > 0 ? Math.max(...percentages) : 0
}

function isUnlimited(sub: UserSubscription): boolean {
  return (
    !sub.group?.daily_limit_usd &&
    !sub.group?.weekly_limit_usd &&
    !sub.group?.monthly_limit_usd
  )
}

type UsageColor = 'error' | 'warning' | 'success' | 'neutral'

function usageColor(percentage: number): UsageColor {
  if (percentage >= 90) return 'error'
  if (percentage >= 70) return 'warning'
  return 'success'
}

const DOT_CLASS: Record<UsageColor, string> = {
  error: 'bg-error',
  warning: 'bg-warning',
  success: 'bg-success',
  neutral: 'bg-inverted/40'
}

function getProgressDotClass(sub: UserSubscription): string {
  // Unlimited subscriptions read as healthy
  if (isUnlimited(sub)) return DOT_CLASS.success
  return DOT_CLASS[usageColor(getMaxUsagePercentage(sub))]
}

function getProgressColor(used: number | undefined, limit: number | null | undefined): UsageColor {
  if (!limit || limit === 0) return 'neutral'
  return usageColor(((used || 0) / limit) * 100)
}

function getProgressValue(used: number | undefined, limit: number | null | undefined): number {
  if (!limit || limit === 0) return 0
  return Math.min(((used || 0) / limit) * 100, 100)
}

type LimitRow = { key: string; label: string; used: number | undefined; limit: number | null | undefined }

function limitRows(sub: UserSubscription): LimitRow[] {
  const rows: LimitRow[] = []
  if (sub.group?.daily_limit_usd) rows.push({ key: 'daily', label: t('subscriptionProgress.daily'), used: sub.daily_usage_usd, limit: sub.group.daily_limit_usd })
  if (sub.group?.weekly_limit_usd) rows.push({ key: 'weekly', label: t('subscriptionProgress.weekly'), used: sub.weekly_usage_usd, limit: sub.group.weekly_limit_usd })
  if (sub.group?.monthly_limit_usd) rows.push({ key: 'monthly', label: t('subscriptionProgress.monthly'), used: sub.monthly_usage_usd, limit: sub.group.monthly_limit_usd })
  return rows
}

function formatUsage(used: number | undefined, limit: number | null | undefined): string {
  const usedValue = (used || 0).toFixed(2)
  const limitValue = limit?.toFixed(2) || '∞'
  return `$${usedValue}/$${limitValue}`
}

function formatDaysRemaining(expiresAt: string): string {
  const now = new Date()
  const expires = new Date(expiresAt)
  const diff = expires.getTime() - now.getTime()
  if (diff < 0) return t('subscriptionProgress.expired')
  const days = Math.ceil(diff / (1000 * 60 * 60 * 24))
  if (days === 0) return t('subscriptionProgress.expiresToday')
  if (days === 1) return t('subscriptionProgress.expiresTomorrow')
  return t('subscriptionProgress.daysRemaining', { days })
}

function getDaysRemainingClass(expiresAt: string): string {
  const now = new Date()
  const expires = new Date(expiresAt)
  const diff = expires.getTime() - now.getTime()
  const days = Math.ceil(diff / (1000 * 60 * 60 * 24))
  if (days <= 3) return 'text-error'
  if (days <= 7) return 'text-warning'
  return 'text-muted'
}

function closeTooltip() {
  tooltipOpen.value = false
}

onMounted(() => {
  // Trigger initial fetch if not already loaded
  // The actual data loading is handled by App.vue globally
  subscriptionStore.fetchActiveSubscriptions().catch((error) => {
    console.error('Failed to load subscriptions in SubscriptionProgressMini:', error)
  })
})

</script>
