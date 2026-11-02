<template>
  <AppLayout>
    <div class="signal-account-page mx-auto max-w-3xl space-y-6">
      <UserPageHeader flush :title="t('redeem.title')" />
      <div class="grid grid-cols-1 divide-y divide-default overflow-hidden rounded-lg bg-default shadow-xs ring ring-default sm:grid-cols-[1.4fr_1fr] sm:divide-x sm:divide-y-0 dark:shadow-none">
        <div class="px-5 py-4 sm:px-6">
          <p class="text-sm text-muted">{{ t('redeem.currentBalance') }}</p>
          <p class="mt-1 text-2xl font-semibold tabular-nums text-highlighted">
            ${{ user?.balance?.toFixed(2) || '0.00' }}
          </p>
        </div>
        <div class="px-5 py-4 sm:px-6">
          <p class="text-sm text-muted">{{ t('redeem.concurrency') }}</p>
          <p class="mt-1 text-2xl font-semibold tabular-nums text-highlighted">
            {{ user?.concurrency || 0 }} <span class="text-xs font-normal text-muted">{{ t('redeem.requests') }}</span>
          </p>
        </div>
      </div>

      <!-- Redeem Form -->
      <section class="rounded-lg bg-default p-5 shadow-xs ring ring-default sm:p-6 dark:shadow-none">
        <form @submit.prevent="handleRedeem" class="space-y-5">
          <div>
            <label for="code" class="mb-1.5 block text-sm font-medium text-default">
              {{ t('redeem.redeemCodeLabel') }}
            </label>
            <UInput
              id="code"
              v-model="redeemCode"
              type="text"
              required
              size="xl"
              icon="i-lucide-gift"
              class="w-full"
              :placeholder="t('redeem.redeemCodePlaceholder')"
              :disabled="submitting"
            />
            <p class="mt-1.5 text-xs text-muted">
              {{ t('redeem.redeemCodeHint') }}
            </p>
          </div>

          <UButton
            type="submit"
            size="xl"
            block
            :icon="submitting ? 'i-lucide-loader-circle' : 'i-lucide-circle-check'"
            :ui="{ leadingIcon: submitting ? 'animate-spin' : '' }"
            :disabled="!redeemCode || submitting"
          >
            {{ submitting ? t('redeem.redeeming') : t('redeem.redeemButton') }}
          </UButton>
        </form>
      </section>

      <!-- Success Message -->
      <transition name="fade">
        <UAlert
          v-if="redeemResult"
          color="success"
          variant="subtle"
          icon="i-lucide-circle-check"
          :title="t('redeem.redeemSuccess')"
        >
          <template #description>
            <div class="mt-1 space-y-1">
              <p v-if="redeemResult.type === 'balance'" class="font-medium">
                {{ t('redeem.added') }}: ${{ redeemResult.value.toFixed(2) }}
              </p>
              <p v-else-if="redeemResult.type === 'concurrency'" class="font-medium">
                {{ t('redeem.added') }}: {{ redeemResult.value }}
                {{ t('redeem.concurrentRequests') }}
              </p>
              <p v-else-if="redeemResult.type === 'subscription'" class="font-medium">
                {{ t('redeem.subscriptionAssigned') }}
                <span v-if="redeemResult.group?.name"> - {{ redeemResult.group.name }}</span>
                <span v-if="redeemResult.validity_days">
                  ({{
                    t('redeem.subscriptionDays', { days: redeemResult.validity_days })
                  }})</span
                >
              </p>
              <p v-if="redeemResult.type === 'balance' && userRefreshedAfterRedeem && user">
                {{ t('redeem.newBalance') }}:
                <span class="font-semibold">${{ user.balance.toFixed(2) }}</span>
              </p>
              <p v-if="redeemResult.type === 'concurrency' && userRefreshedAfterRedeem && user">
                {{ t('redeem.newConcurrency') }}:
                <span class="font-semibold"
                  >{{ user.concurrency }} {{ t('redeem.requests') }}</span
                >
              </p>
            </div>
          </template>
        </UAlert>
      </transition>

      <!-- Error Message -->
      <transition name="fade">
        <UAlert
          v-if="errorMessage"
          color="error"
          variant="subtle"
          icon="i-lucide-circle-alert"
          :title="t('redeem.redeemFailed')"
          :description="errorMessage"
        />
      </transition>

      <!-- Information Card -->
      <UAlert color="neutral" variant="subtle" icon="i-lucide-info" class="dark:bg-primary/10 dark:text-primary dark:ring-primary/25" :title="t('redeem.aboutCodes')">
        <template #description>
          <ul class="mt-1 list-inside list-disc space-y-1">
            <li>{{ t('redeem.codeRule1') }}</li>
            <li>{{ t('redeem.codeRule2') }}</li>
            <li>
              {{ t('redeem.codeRule3') }}
              <UBadge v-if="contactInfo" color="neutral" variant="outline" class="ml-1.5">
                {{ contactInfo }}
              </UBadge>
            </li>
            <li>{{ t('redeem.codeRule4') }}</li>
          </ul>
        </template>
      </UAlert>

      <!-- Recent Activity -->
      <section class="overflow-hidden rounded-lg bg-default shadow-xs ring ring-default dark:shadow-none">
        <header class="border-b border-default px-5 py-4 sm:px-6">
          <h2 class="text-base font-semibold text-highlighted">
            {{ t('redeem.recentActivity') }}
          </h2>
        </header>
        <div class="px-5 py-4 sm:px-6">
          <!-- Loading State -->
          <div v-if="loadingHistory" class="flex items-center justify-center py-8">
            <UIcon name="i-lucide-loader-circle" class="size-6 animate-spin text-muted" />
          </div>

          <!-- History List -->
          <ul v-else-if="history.length > 0" class="-my-1 divide-y divide-default">
            <li
              v-for="item in history"
              :key="item.id"
              class="flex items-center justify-between gap-4 py-3"
            >
              <div class="flex min-w-0 items-center gap-3">
                <div
                  :class="[
                    'flex size-9 shrink-0 items-center justify-center rounded-lg',
                    isBalanceType(item.type)
                      ? item.value >= 0
                        ? 'bg-emerald-100 dark:bg-emerald-900/30'
                        : 'bg-red-100 dark:bg-red-900/30'
                      : isSubscriptionType(item.type)
                        ? 'bg-purple-100 dark:bg-purple-900/30'
                        : item.value >= 0
                          ? 'bg-blue-100 dark:bg-blue-900/30'
                          : 'bg-orange-100 dark:bg-orange-900/30'
                  ]"
                >
                  <!-- 余额类型图标 -->
                  <Icon
                    v-if="isBalanceType(item.type)"
                    name="dollar"
                    size="md"
                    :class="
                      item.value >= 0
                        ? 'text-emerald-600 dark:text-emerald-400'
                        : 'text-red-600 dark:text-red-400'
                    "
                  />
                  <!-- 订阅类型图标 -->
                  <Icon
                    v-else-if="isSubscriptionType(item.type)"
                    name="badge"
                    size="md"
                    class="text-purple-600 dark:text-purple-400"
                  />
                  <!-- 并发类型图标 -->
                  <Icon
                    v-else
                    name="bolt"
                    size="md"
                    :class="
                      item.value >= 0
                        ? 'text-blue-600 dark:text-blue-400'
                        : 'text-orange-600 dark:text-orange-400'
                    "
                  />
                </div>
                <div class="min-w-0">
                  <p class="text-sm font-medium text-highlighted">
                    {{ getHistoryItemTitle(item) }}
                  </p>
                  <p class="text-xs text-muted">
                    {{ formatDateTime(item.used_at) }}
                  </p>
                </div>
              </div>
              <div class="shrink-0 text-right">
                <p
                  :class="[
                    'text-sm font-semibold tabular-nums',
                    isBalanceType(item.type)
                      ? item.value >= 0
                        ? 'text-emerald-600 dark:text-emerald-400'
                        : 'text-red-600 dark:text-red-400'
                      : isSubscriptionType(item.type)
                        ? 'text-purple-600 dark:text-purple-400'
                        : item.value >= 0
                          ? 'text-blue-600 dark:text-blue-400'
                          : 'text-orange-600 dark:text-orange-400'
                  ]"
                >
                  {{ formatHistoryValue(item) }}
                </p>
                <p
                  v-if="!isAdminAdjustment(item.type)"
                  class="font-mono text-xs text-dimmed"
                >
                  {{ item.code.slice(0, 8) }}...
                </p>
                <p v-else class="text-xs text-dimmed">
                  {{ t('redeem.adminAdjustment') }}
                </p>
                <!-- Display notes for admin adjustments -->
                <p
                  v-if="item.notes"
                  class="mt-1 max-w-[200px] truncate text-xs italic text-muted"
                  :title="item.notes"
                >
                  {{ item.notes }}
                </p>
              </div>
            </li>
          </ul>

          <!-- Empty State -->
          <div v-else class="flex flex-col items-center py-8 text-center">
            <div class="mb-3 flex size-12 items-center justify-center rounded-lg bg-elevated">
              <UIcon name="i-lucide-clock" class="size-6 text-dimmed" />
            </div>
            <p class="text-sm text-muted">
              {{ t('redeem.historyWillAppear') }}
            </p>
          </div>
        </div>
        <footer class="flex flex-wrap items-center justify-between gap-3 border-t border-default px-5 py-3 text-sm text-muted sm:px-6">
          <span>{{ t('common.total') }}: {{ historyTotal }} {{ t('pagination.results') }}</span>
          <div class="flex flex-wrap items-center gap-2">
            <label class="flex items-center gap-2">
              {{ t('pagination.perPage') }}
              <select
                v-model="historyPageSize"
                class="input w-20 py-1.5"
                :disabled="loadingHistory || submitting"
                @change="fetchHistory(1)"
              >
                <option v-for="size in [20, 50, 100]" :key="size" :value="size">{{ size }}</option>
              </select>
            </label>
            <UButton
              color="neutral"
              variant="outline"
              :disabled="loadingHistory || submitting || historyPage <= 1"
              @click="fetchHistory(historyPage - 1)"
            >{{ t('pagination.previous') }}</UButton>
            <UButton
              color="neutral"
              variant="outline"
              :disabled="loadingHistory || submitting || historyPage * historyPageSize >= historyTotal"
              @click="fetchHistory(historyPage + 1)"
            >{{ t('pagination.next') }}</UButton>
          </div>
        </footer>
      </section>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAuthStore } from '@/stores/auth'
import { useAppStore } from '@/stores/app'
import { useSubscriptionStore } from '@/stores/subscriptions'
import { redeemAPI, authAPI, type RedeemHistoryItem } from '@/api'
import type { RedeemCode } from '@/types'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import UserPageHeader from '@/components/user/UserPageHeader.vue'
import { formatDateTime } from '@/utils/format'

const { t } = useI18n()
const authStore = useAuthStore()
const appStore = useAppStore()
const subscriptionStore = useSubscriptionStore()

const user = computed(() => authStore.user)

const redeemCode = ref('')
const submitting = ref(false)
const redeemResult = ref<RedeemCode | null>(null)
const userRefreshedAfterRedeem = ref(false)
const errorMessage = ref('')

// History data
const history = ref<RedeemHistoryItem[]>([])
const loadingHistory = ref(false)
const historyPage = ref(1)
const historyPageSize = ref(20)
const historyTotal = ref(0)
let historyRequest = 0
let loadedHistoryPageSize = 20
const contactInfo = ref('')

// Helper functions for history display
const isBalanceType = (type: string) => {
  return type === 'balance' || type === 'admin_balance'
}

const isSubscriptionType = (type: string) => {
  return type === 'subscription'
}

const isAdminAdjustment = (type: string) => {
  return type === 'admin_balance' || type === 'admin_concurrency'
}

const getHistoryItemTitle = (item: RedeemHistoryItem) => {
  if (item.type === 'balance') {
    return t('redeem.balanceAddedRedeem')
  } else if (item.type === 'admin_balance') {
    return item.value >= 0 ? t('redeem.balanceAddedAdmin') : t('redeem.balanceDeductedAdmin')
  } else if (item.type === 'concurrency') {
    return t('redeem.concurrencyAddedRedeem')
  } else if (item.type === 'admin_concurrency') {
    return item.value >= 0 ? t('redeem.concurrencyAddedAdmin') : t('redeem.concurrencyReducedAdmin')
  } else if (item.type === 'subscription') {
    return t('redeem.subscriptionAssigned')
  }
  return t('common.unknown')
}

const formatHistoryValue = (item: RedeemHistoryItem) => {
  if (isBalanceType(item.type)) {
    const sign = item.value >= 0 ? '+' : ''
    return `${sign}$${item.value.toFixed(2)}`
  } else if (isSubscriptionType(item.type)) {
    // 订阅类型显示有效天数和分组名称
    const days = item.validity_days || Math.round(item.value)
    const groupName = item.group?.name || ''
    return groupName ? `${days}${t('redeem.days')} - ${groupName}` : `${days}${t('redeem.days')}`
  } else {
    const sign = item.value >= 0 ? '+' : ''
    return `${sign}${item.value} ${t('redeem.requests')}`
  }
}

const fetchHistory = async (page = 1) => {
  const request = ++historyRequest
  const pageSize = historyPageSize.value
  loadingHistory.value = true
  try {
    const result = await redeemAPI.getHistory(page, pageSize)
    if (request !== historyRequest) return
    history.value = result.items
    historyTotal.value = result.total
    historyPage.value = page
    historyPageSize.value = pageSize
    loadedHistoryPageSize = pageSize
  } catch (error) {
    if (request !== historyRequest) return
    historyPageSize.value = loadedHistoryPageSize
    appStore.showError(t('redeem.historyLoadFailed'))
    console.error('Failed to fetch history:', error)
  } finally {
    if (request === historyRequest) loadingHistory.value = false
  }
}

const handleRedeem = async () => {
  if (!redeemCode.value.trim()) {
    appStore.showError(t('redeem.pleaseEnterCode'))
    return
  }

  submitting.value = true
  errorMessage.value = ''
  redeemResult.value = null
  userRefreshedAfterRedeem.value = false

  try {
    const result = await redeemAPI.redeem(redeemCode.value.trim())

    redeemResult.value = result

    // Refresh user data to get updated balance/concurrency
    try {
      await authStore.refreshUser()
      userRefreshedAfterRedeem.value = true
    } catch (error) {
      console.error('Failed to refresh user after redeem:', error)
      appStore.showWarning(t('redeem.userRefreshFailed'))
    }

    // If subscription type, immediately refresh subscription status
    if (result.type === 'subscription') {
      try {
        await subscriptionStore.fetchActiveSubscriptions(true) // force refresh
      } catch (error) {
        console.error('Failed to refresh subscriptions after redeem:', error)
        appStore.showWarning(t('redeem.subscriptionRefreshFailed'))
      }
    }

    // Clear the input
    redeemCode.value = ''

    // Refresh history
    await fetchHistory()

    // Show success toast
    appStore.showSuccess(t('redeem.codeRedeemSuccess'))
  } catch (error: any) {
    errorMessage.value = error.response?.data?.detail || t('redeem.failedToRedeem')

    appStore.showError(t('redeem.redeemFailed'))
  } finally {
    submitting.value = false
  }
}

onMounted(async () => {
  fetchHistory()
  try {
    const settings = await authAPI.getPublicSettings()
    contactInfo.value = settings.contact_info || ''
  } catch (error) {
    console.error('Failed to load contact info:', error)
  }
})
</script>

<style scoped>
.fade-enter-active,
.fade-leave-active {
  transition: all 0.3s ease;
}

.fade-enter-from,
.fade-leave-to {
  opacity: 0;
  transform: translateY(-8px);
}
</style>
