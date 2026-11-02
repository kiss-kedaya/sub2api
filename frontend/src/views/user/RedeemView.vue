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
              aria-describedby="redeem-code-hint"
            />
            <p id="redeem-code-hint" class="mt-1.5 text-xs text-muted">
              {{ t('redeem.redeemCodeHint') }}
            </p>
          </div>

          <UButton
            type="submit"
            size="xl"
            block
            :loading="submitting"
            :icon="submitting ? 'i-lucide-loader-circle' : 'i-lucide-circle-check'"
            :ui="{ leadingIcon: submitting ? 'animate-spin' : '' }"
            :disabled="!redeemCode.trim() || submitting"
            data-testid="redeem-submit"
          >
            {{ submitting ? t('redeem.redeeming') : t('redeem.redeemButton') }}
          </UButton>
        </form>
      </section>

      <!-- Error Message -->
      <div class="min-h-12" aria-live="polite">
        <UAlert
          v-if="errorMessage"
          color="error"
          variant="subtle"
          icon="i-lucide-circle-alert"
          :title="t('redeem.redeemFailed')"
          :description="errorMessage"
        />
      </div>

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

    <UModal
      v-model:open="showRedeemResult"
      :title="t('redeem.redeemSuccess')"
      :description="t('redeem.resultDescription')"
      :dismissible="!submitting"
      data-testid="redeem-result-modal"
    >
      <template #content>
        <div v-if="redeemResult" data-testid="redeem-result-modal-content" class="space-y-4">
          <UAlert color="success" variant="subtle" icon="i-lucide-circle-check">
            <template #description>
              <div v-if="redeemResult.type === 'balance'" class="space-y-2">
                <p class="font-semibold tabular-nums">
                  {{ t(redeemResult.value >= 0 ? 'redeem.balanceChange' : 'redeem.balanceDeducted') }}:
                  {{ formatSignedCurrency(redeemResult.value) }}
                </p>
                <p v-if="redeemBalanceAfter !== null" class="text-sm">
                  {{ t('redeem.newBalance') }}:
                  <span class="font-semibold tabular-nums">${{ redeemBalanceAfter.toFixed(2) }}</span>
                </p>
              </div>
              <div v-else-if="redeemResult.type === 'concurrency'" class="space-y-2">
                <p class="font-semibold tabular-nums">
                  {{ t(redeemConcurrencyChange >= 0 ? 'redeem.concurrencyChange' : 'redeem.concurrencyReduced') }}:
                  {{ formatSignedNumber(redeemConcurrencyChange) }} {{ t('redeem.concurrentRequests') }}
                </p>
                <p v-if="redeemConcurrencyAfter !== null" class="text-sm">
                  {{ t('redeem.newConcurrency') }}:
                  <span class="font-semibold tabular-nums">{{ redeemConcurrencyAfter }} {{ t('redeem.requests') }}</span>
                </p>
              </div>
              <div v-else-if="redeemResult.type === 'subscription'" class="space-y-2">
                <p class="font-semibold">
                  {{ t(redeemResult.validity_days != null && redeemResult.validity_days < 0 ? 'redeem.subscriptionUpdated' : 'redeem.subscriptionAssigned') }}
                </p>
                <p v-if="redeemResult.group?.name" class="text-sm">{{ redeemResult.group.name }}</p>
                <p v-else-if="redeemResult.group_id != null" class="text-sm text-muted">
                  {{ t('redeem.subscriptionGroup') }} #{{ redeemResult.group_id }}
                </p>
                <p v-if="redeemSubscriptionDays" class="text-sm text-muted">
                  {{ t(redeemSubscriptionDays < 0 ? 'redeem.subscriptionDaysReduced' : 'redeem.subscriptionDays', { days: Math.abs(redeemSubscriptionDays) }) }}
                </p>
              </div>
              <p v-else class="font-medium">{{ t('redeem.resultRecorded') }}</p>
            </template>
          </UAlert>
          <UButton color="neutral" variant="outline" class="mt-4" @click="showRedeemResult = false">
            {{ t('common.close') }}
          </UButton>
        </div>
      </template>
    </UModal>
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
const showRedeemResult = ref(false)
const redeemBalanceAfter = ref<number | null>(null)
const redeemConcurrencyAfter = ref<number | null>(null)
const errorMessage = ref('')
const redeemConcurrencyChange = computed(() => Math.trunc(redeemResult.value?.value ?? 0))
// The service applies 30 days when an older/legacy subscription code carries zero days.
const redeemSubscriptionDays = computed(() => {
  if (redeemResult.value?.type !== 'subscription') return 0
  return redeemResult.value.validity_days === 0 ? 30 : redeemResult.value.validity_days ?? 0
})

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

const formatSignedCurrency = (value: number) => `${value >= 0 ? '+' : '-'}$${Math.abs(value).toFixed(2)}`
const formatSignedNumber = (value: number) => `${value >= 0 ? '+' : ''}${value}`

/** The redeem response confirms the applied delta; patch only the auth store, never reload the page. */
const applyRedeemDeltaLocally = (result: RedeemCode) => {
  const current = authStore.user
  if (!current) return

  if (result.type === 'balance' && Number.isFinite(result.value)) {
    redeemBalanceAfter.value = current.balance + result.value
    authStore.user = { ...current, balance: redeemBalanceAfter.value }
  } else if (result.type === 'concurrency' && Number.isFinite(result.value)) {
    const delta = Math.trunc(result.value)
    redeemConcurrencyAfter.value = current.concurrency + delta
    authStore.user = { ...current, concurrency: redeemConcurrencyAfter.value }
  }
}

const refreshAccountAfterRedeem = async (type: RedeemCode['type']) => {
  try {
    const refreshed = await authStore.refreshUser()
    if (type === 'balance' && Number.isFinite(refreshed.balance)) redeemBalanceAfter.value = refreshed.balance
    if (type === 'concurrency' && Number.isFinite(refreshed.concurrency)) redeemConcurrencyAfter.value = refreshed.concurrency
  } catch (error) {
    console.error('Failed to refresh user after redeem:', error)
    appStore.showWarning(t('redeem.userRefreshFailed'))
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
  // Set synchronously before the request so rapid/double form submits share one flight.
  if (submitting.value) return
  if (!redeemCode.value.trim()) {
    appStore.showError(t('redeem.pleaseEnterCode'))
    return
  }

  submitting.value = true
  errorMessage.value = ''
  redeemResult.value = null
  showRedeemResult.value = false
  redeemBalanceAfter.value = null
  redeemConcurrencyAfter.value = null

  let result: RedeemCode

  try {
    result = await redeemAPI.redeem(redeemCode.value.trim())
  } catch (error: any) {
    errorMessage.value = error.response?.data?.detail || t('redeem.failedToRedeem')
    appStore.showError(t('redeem.redeemFailed'))
    return
  } finally {
    submitting.value = false
  }

  // The code is consumed now: show the server-confirmed result immediately. Follow-up reads
  // update only the relevant stores and history; their failures must never turn success into error.
  redeemResult.value = result
  applyRedeemDeltaLocally(result)
  redeemCode.value = ''
  showRedeemResult.value = true
  appStore.showSuccess(t('redeem.codeRedeemSuccess'))

  if (result.type === 'balance' || result.type === 'concurrency') {
    void refreshAccountAfterRedeem(result.type)
  } else if (result.type === 'subscription') {
    void subscriptionStore.fetchActiveSubscriptions(true).catch((error) => {
      console.error('Failed to refresh subscriptions after redeem:', error)
      appStore.showWarning(t('redeem.subscriptionRefreshFailed'))
    })
  }
  void fetchHistory()
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
