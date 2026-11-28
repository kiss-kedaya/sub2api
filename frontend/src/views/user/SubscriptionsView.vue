<template>
  <AppLayout>
    <div class="signal-subscriptions">
      <UserPageHeader flush :title="t('userSubscriptions.title')" :description="t('userSubscriptions.description')" />
      <!-- Loading State -->
      <div v-if="loading" class="flex justify-center py-12">
        <div
          class="h-8 w-8 animate-spin rounded-full border-2 border-primary border-t-transparent motion-reduce:animate-none"
        ></div>
      </div>

      <!-- Empty State -->
      <div v-else-if="subscriptions.length === 0" class="signal-subscriptions-empty rounded-lg">
        <div
          class="signal-subscriptions-empty__icon rounded-lg"
        >
          <Icon name="creditCard" size="xl" class="text-dimmed" />
        </div>
        <h3 class="mb-2 text-lg font-semibold text-highlighted">
          {{ t('userSubscriptions.noActiveSubscriptions') }}
        </h3>
        <p class="text-muted">
          {{ t('userSubscriptions.noActiveSubscriptionsDesc') }}
        </p>
      </div>

      <!-- Subscriptions Grid -->
      <div v-else class="signal-subscriptions-grid">
        <div
          v-for="subscription in subscriptions"
          :key="subscription.id"
          class="signal-subscription-card rounded-lg"
          :class="platformBorderClass(subscription.group?.platform || '')"
        >
          <!-- Header -->
          <div
            class="signal-subscription-card__header"
          >
            <div class="flex items-start gap-3">
              <div :class="['mt-2 h-1.5 w-1.5 shrink-0 rounded-full', platformAccentDotClass(subscription.group?.platform || '')]" />
              <div class="min-w-0">
                <div class="flex flex-wrap items-center gap-2">
                  <h3 class="min-w-0 font-semibold text-highlighted">
                    {{ subscription.group?.name || `Group #${subscription.group_id}` }}
                  </h3>
                  <span :class="['shrink-0 rounded-md border px-2 py-0.5 text-[11px] font-medium', platformBadgeClass(subscription.group?.platform || '')]">
                    {{ platformLabel(subscription.group?.platform || '') }}
                  </span>
                </div>
                <p v-if="subscription.group?.description" class="mt-0.5 text-xs text-muted">
                  {{ subscription.group.description }}
                </p>
                <div class="mt-1 flex flex-wrap gap-x-3 gap-y-1 text-[11px] tabular-nums text-dimmed">
                  <span>{{ t('payment.planCard.rate') }}: ×{{ subscription.group?.rate_multiplier ?? 1 }}</span>
                  <span v-if="subscriptionHasPeakRate(subscription)" class="text-amber-700 dark:text-amber-300">
                    {{ t('payment.planCard.peakRate') }}: {{ subscriptionPeakRateLabel(subscription) }}
                  </span>
                </div>
              </div>
            </div>
            <div class="flex items-center gap-2">
              <span
                :class="[
                  'signal-subscription-status rounded-full px-2 py-0.5 text-xs font-medium',
                  subscription.status === 'active'
                    ? 'bg-emerald-100 text-emerald-700 dark:bg-emerald-900/40 dark:text-emerald-300'
                    : subscription.status === 'expired'
                      ? 'bg-elevated text-muted'
                      : 'bg-red-100 text-red-700 dark:bg-red-900/40 dark:text-red-300'
                ]"
              >
                {{ t(`userSubscriptions.status.${subscription.status}`) }}
              </span>
              <button
                v-if="subscription.status === 'active'"
                type="button"
                :class="['signal-subscription-renew', platformButtonClass(subscription.group?.platform || '')]"
                @click="router.push({ path: '/purchase', query: { tab: 'subscription', group: String(subscription.group_id) } })"
              >
                <Icon name="refresh" size="xs" />
                {{ t('payment.renewNow') }}
              </button>
            </div>
          </div>

          <!-- Usage Progress -->
          <div class="signal-subscription-card__body">
            <!-- Expiration Info -->
            <div v-if="subscription.expires_at" class="flex items-center justify-between text-sm">
              <span class="text-muted">{{
                t('userSubscriptions.expires')
              }}</span>
              <span :class="['tabular-nums', getExpirationClass(subscription.expires_at)]">
                {{ formatExpirationDate(subscription.expires_at) }}
              </span>
            </div>
            <div v-else class="flex items-center justify-between text-sm">
              <span class="text-muted">{{
                t('userSubscriptions.expires')
              }}</span>
              <span class="text-default">{{
                t('userSubscriptions.noExpiration')
              }}</span>
            </div>

            <!-- Daily Usage -->
            <div v-if="subscription.group?.daily_limit_usd" class="space-y-2">
              <div class="flex items-center justify-between">
                <span class="text-sm font-medium text-default">
                  {{ t('userSubscriptions.daily') }}
                </span>
                <span class="text-sm tabular-nums text-muted">
                  ${{ (subscription.daily_usage_usd || 0).toFixed(2) }} / ${{
                    subscription.group.daily_limit_usd.toFixed(2)
                  }}
                </span>
              </div>
              <div class="relative h-2 overflow-hidden rounded-full bg-accented">
                <div
                  class="absolute inset-0 origin-left rounded-full transition-[transform,background-color] duration-200 ease-[var(--ease-out)] motion-reduce:transition-none"
                  :class="
                    getProgressBarClass(
                      subscription.daily_usage_usd,
                      subscription.group.daily_limit_usd
                    )
                  "
                  :style="{
                    transform: getProgressTransform(
                      subscription.daily_usage_usd,
                      subscription.group.daily_limit_usd
                    )
                  }"
                ></div>
              </div>
              <p
                v-if="subscription.daily_window_start"
                class="text-xs tabular-nums text-muted"
              >
                {{ formatDailyUsageWindow(subscription) }}
              </p>
            </div>

            <!-- Weekly Usage -->
            <div v-if="subscription.group?.weekly_limit_usd" class="space-y-2">
              <div class="flex items-center justify-between">
                <span class="text-sm font-medium text-default">
                  {{ t('userSubscriptions.weekly') }}
                </span>
                <span class="text-sm tabular-nums text-muted">
                  ${{ (subscription.weekly_usage_usd || 0).toFixed(2) }} / ${{
                    subscription.group.weekly_limit_usd.toFixed(2)
                  }}
                </span>
              </div>
              <div class="relative h-2 overflow-hidden rounded-full bg-accented">
                <div
                  class="absolute inset-0 origin-left rounded-full transition-[transform,background-color] duration-200 ease-[var(--ease-out)] motion-reduce:transition-none"
                  :class="
                    getProgressBarClass(
                      subscription.weekly_usage_usd,
                      subscription.group.weekly_limit_usd
                    )
                  "
                  :style="{
                    transform: getProgressTransform(
                      subscription.weekly_usage_usd,
                      subscription.group.weekly_limit_usd
                    )
                  }"
                ></div>
              </div>
              <p
                v-if="subscription.weekly_window_start"
                class="text-xs tabular-nums text-muted"
              >
                {{
                  t('userSubscriptions.resetIn', {
                    time: formatResetTime(subscription.weekly_window_start, 168)
                  })
                }}
              </p>
            </div>

            <!-- Monthly Usage -->
            <div v-if="subscription.group?.monthly_limit_usd" class="space-y-2">
              <div class="flex items-center justify-between">
                <span class="text-sm font-medium text-default">
                  {{ t('userSubscriptions.monthly') }}
                </span>
                <span class="text-sm tabular-nums text-muted">
                  ${{ (subscription.monthly_usage_usd || 0).toFixed(2) }} / ${{
                    subscription.group.monthly_limit_usd.toFixed(2)
                  }}
                </span>
              </div>
              <div class="relative h-2 overflow-hidden rounded-full bg-accented">
                <div
                  class="absolute inset-0 origin-left rounded-full transition-[transform,background-color] duration-200 ease-[var(--ease-out)] motion-reduce:transition-none"
                  :class="
                    getProgressBarClass(
                      subscription.monthly_usage_usd,
                      subscription.group.monthly_limit_usd
                    )
                  "
                  :style="{
                    transform: getProgressTransform(
                      subscription.monthly_usage_usd,
                      subscription.group.monthly_limit_usd
                    )
                  }"
                ></div>
              </div>
              <p
                v-if="subscription.monthly_window_start"
                class="text-xs tabular-nums text-muted"
              >
                {{
                  t('userSubscriptions.resetIn', {
                    time: formatResetTime(subscription.monthly_window_start, 720)
                  })
                }}
              </p>
            </div>

            <!-- No limits configured - Unlimited badge -->
            <div
              v-if="
                !subscription.group?.daily_limit_usd &&
                !subscription.group?.weekly_limit_usd &&
                !subscription.group?.monthly_limit_usd
              "
              class="signal-unlimited-quota rounded-lg"
            >
              <div class="flex items-center gap-3">
                <span class="text-4xl text-emerald-600 dark:text-emerald-400">∞</span>
                <div>
                  <p class="text-sm font-medium text-emerald-700 dark:text-emerald-300">
                    {{ t('userSubscriptions.unlimited') }}
                  </p>
                  <p class="text-xs text-emerald-600/70 dark:text-emerald-400/70">
                    {{ t('userSubscriptions.unlimitedDesc') }}
                  </p>
                </div>
              </div>
            </div>
          </div>
        </div>
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
import Icon from '@/components/icons/Icon.vue'
import { formatDateTimeToMinute } from '@/utils/format'
import { hasPeakRate, formatPeakRateWindow, serverTimezoneLabel } from '@/utils/peak-rate'
import { platformBorderClass, platformBadgeClass, platformButtonClass, platformLabel } from '@/utils/platformColors'
import {
  getExpirationDateRelation,
  getRemainingDurationParts,
  resolveDailyQuotaWindowDisplay,
  type RemainingDurationParts
} from '@/utils/subscriptionQuota'

function platformAccentDotClass(p: string): string {
  switch (p) {
    case 'anthropic': return 'bg-orange-500'
    case 'openai': return 'bg-emerald-500'
    case 'antigravity': return 'bg-purple-500'
    case 'gemini': return 'bg-blue-500'
    default: return 'bg-(--ui-text-dimmed)'
  }
}

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

function getProgressTransform(used: number | undefined, limit: number | null | undefined): string {
  if (!limit || limit <= 0) return 'scaleX(0)'
  const ratio = Math.max(0, Math.min((used || 0) / limit, 1))
  return `scaleX(${ratio})`
}

function getProgressBarClass(used: number | undefined, limit: number | null | undefined): string {
  if (!limit || limit === 0) return 'bg-(--ui-text-dimmed)'
  const percentage = ((used || 0) / limit) * 100
  if (percentage >= 90) return 'bg-red-500'
  if (percentage >= 70) return 'bg-orange-500'
  return 'bg-green-500'
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

  if (diff <= 0) return 'text-red-600 dark:text-red-400 font-medium'
  if (days <= 3) return 'text-red-600 dark:text-red-400'
  if (days <= 7) return 'text-orange-600 dark:text-orange-400'
  return 'text-default'
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
  const display = resolveDailyQuotaWindowDisplay(subscription)
  if (!display) return t('userSubscriptions.windowNotActive')

  const parts = getRemainingDurationParts(display.targetAt)
  if (!parts) return t('userSubscriptions.windowNotActive')

  if (display.kind === 'quotaEnds') {
    return t('userSubscriptions.quotaEndsIn', { time: formatDurationParts(parts) })
  }

  return t('userSubscriptions.resetIn', {
    time: formatDurationParts(parts)
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

<style scoped>
.signal-subscriptions {
  --subscriptions-bg: var(--signal-bg, var(--app-page-bg, #ffffff));
  --subscriptions-surface: var(--ui-bg);
  --subscriptions-line: var(--ui-border);
  --subscriptions-text: var(--ui-text-highlighted);
  --subscriptions-muted: var(--ui-text-muted);
  --subscriptions-accent: var(--signal-accent, var(--ui-primary, #155dfc));
  --subscriptions-accent-soft: var(--signal-accent-soft, color-mix(in oklab, var(--ui-primary, #155dfc) 10%, transparent));
  --subscriptions-amber: var(--signal-amber, var(--ui-warning, #b45309));
  display: grid;
  gap: 16px;
  min-width: 0;
  color: var(--subscriptions-text);
  letter-spacing: 0;
}

.signal-subscriptions-empty__icon {
  display: grid;
  width: 38px;
  height: 38px;
  flex: 0 0 auto;
  place-items: center;
  border: 1px solid var(--subscriptions-line);
  border-radius: 6px;
  color: var(--subscriptions-accent);
  background: var(--subscriptions-surface);
}

.signal-subscriptions-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 14px;
}

.signal-subscription-card {
  min-width: 0;
  overflow: hidden;
  border: 1px solid var(--subscriptions-line);
  border-radius: 8px;
  background: var(--subscriptions-surface);
  box-shadow: var(--app-shadow-card, 0 1px 2px rgba(10, 10, 10, 0.05));
}

.signal-subscription-card__header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 14px;
  padding: 14px 16px;
  border-bottom: 1px solid var(--subscriptions-line);
  background: var(--ui-bg-muted);
}

.signal-subscription-card__header > div:first-child {
  min-width: 0;
}

.signal-subscription-card__header h3,
.signal-subscription-card__header p {
  overflow-wrap: anywhere;
}

.signal-subscription-card__body {
  display: grid;
  gap: 16px;
  padding: 16px;
}

.signal-subscription-status {
  white-space: nowrap;
}

.signal-subscription-renew {
  display: inline-flex;
  min-height: 32px;
  align-items: center;
  justify-content: center;
  gap: 5px;
  padding: 5px 9px;
  border: 1px solid var(--subscriptions-accent);
  border-radius: 5px;
  color: var(--subscriptions-accent) !important;
  background: var(--subscriptions-surface) !important;
  font-size: 12px;
  font-weight: 650;
  transition:
    transform var(--cb-t-fast, 140ms) var(--cb-ease-press, cubic-bezier(0.3, 0, 0.2, 1)),
    background-color 140ms var(--ease-out, cubic-bezier(0.23, 1, 0.32, 1));
}

.signal-subscription-renew:focus-visible {
  outline: 2px solid var(--subscriptions-accent);
  outline-offset: 2px;
}

@media (hover: hover) and (pointer: fine) {
  .signal-subscription-renew:hover {
    background: var(--subscriptions-accent-soft) !important;
  }
}

.signal-unlimited-quota {
  display: flex;
  align-items: center;
  justify-content: center;
  min-height: 104px;
  padding: 20px;
  border: 1px dashed var(--subscriptions-line);
  border-radius: 6px;
  background: var(--subscriptions-accent-soft);
}

.signal-subscriptions-empty {
  padding: 56px 20px;
  border: 1px solid var(--subscriptions-line);
  border-radius: 8px;
  background: var(--subscriptions-surface);
  box-shadow: var(--app-shadow-card, 0 1px 2px rgba(10, 10, 10, 0.05));
  text-align: center;
}

.signal-subscriptions-empty__icon {
  width: 48px;
  height: 48px;
  margin: 0 auto 14px;
}

@media (max-width: 1023px) {
  .signal-subscriptions-grid {
    grid-template-columns: minmax(0, 1fr);
  }
}

@media (max-width: 639px) {
  .signal-subscription-renew {
    min-height: 44px;
    padding-inline: 12px;
  }
}

@media (max-width: 479px) {
  .signal-subscription-card__header {
    align-items: stretch;
    flex-direction: column;
  }

  .signal-subscription-card__header > div:last-child {
    justify-content: space-between;
  }

  .signal-subscription-card__header > div:first-child > div {
    align-items: flex-start;
  }
}

@media (max-width: 374px) {
  .signal-subscription-card__header,
  .signal-subscription-card__body {
    padding: 13px;
  }

  .signal-subscription-card__header h3 + span {
    flex-shrink: 0;
  }
}

@media (prefers-reduced-motion: reduce) {
  .signal-subscription-renew {
    transition: none;
  }
}
</style>

<style>
html.dark .signal-subscription-card,
html.dark .signal-subscriptions-empty {
  box-shadow: var(--app-shadow-card);
}

html.dark .signal-subscription-card__header {
  background: var(--ui-bg-muted);
}
</style>
