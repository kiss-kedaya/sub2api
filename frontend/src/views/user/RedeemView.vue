<template>
  <AppLayout>
    <div class="mx-auto flex w-full flex-col gap-4 sm:gap-6 lg:max-w-2xl lg:gap-10">
      <UserPageHeader flush :title="t('redeem.title')" />

      <UPageGrid class="grid-cols-1 gap-4 sm:grid-cols-2 sm:gap-px">
        <UPageCard
          icon="i-lucide-wallet"
          :title="t('redeem.currentBalance')"
          variant="subtle"
          :ui="statUi"
          class="sm:rounded-none sm:rounded-l-lg"
        >
          <span class="text-2xl font-semibold tabular-nums text-highlighted">${{ user?.balance?.toFixed(2) || '0.00' }}</span>
        </UPageCard>
        <UPageCard
          icon="i-lucide-gauge"
          :title="t('redeem.concurrency')"
          variant="subtle"
          :ui="statUi"
          class="sm:rounded-none sm:rounded-r-lg"
        >
          <span class="text-2xl font-semibold tabular-nums text-highlighted">{{ user?.concurrency || 0 }} <span class="text-xs font-normal text-muted">{{ t('redeem.requests') }}</span></span>
        </UPageCard>
      </UPageGrid>

      <section>
        <UPageCard :title="t('redeem.redeemCodeLabel')" :description="t('redeem.redeemCodeHint')" variant="naked" orientation="horizontal" class="mb-4">
          <UPopover :content="{ align: 'end' }">
            <UButton color="neutral" variant="ghost" icon="i-lucide-info" class="w-fit lg:ms-auto">{{ t('redeem.aboutCodes') }}</UButton>
            <template #content>
              <ul class="max-w-xs list-inside list-disc space-y-1 p-4 text-sm text-toned">
                <li>{{ t('redeem.codeRule1') }}</li>
                <li>{{ t('redeem.codeRule2') }}</li>
                <li>
                  {{ t('redeem.codeRule3') }}
                  <UBadge v-if="contactInfo" color="neutral" variant="outline" class="ml-1.5">{{ contactInfo }}</UBadge>
                </li>
                <li>{{ t('redeem.codeRule4') }}</li>
              </ul>
            </template>
          </UPopover>
        </UPageCard>
        <UPageCard variant="subtle">
          <form class="flex flex-col gap-3 sm:flex-row" @submit.prevent="handleRedeem">
            <UInput
              id="code"
              v-model="redeemCode"
              type="text"
              required
              size="lg"
              icon="i-lucide-gift"
              class="w-full"
              :aria-label="t('redeem.redeemCodeLabel')"
              :placeholder="t('redeem.redeemCodePlaceholder')"
              :disabled="submitting"
            />
            <UButton
              type="submit"
              size="lg"
              class="shrink-0 justify-center"
              :loading="submitting"
              :icon="submitting ? undefined : 'i-lucide-circle-check'"
              :disabled="!redeemCode || submitting"
            >
              {{ submitting ? t('redeem.redeeming') : t('redeem.redeemButton') }}
            </UButton>
          </form>
        </UPageCard>
      </section>

      <transition name="fade">
        <UAlert v-if="redeemResult" color="success" variant="subtle" icon="i-lucide-circle-check" :title="t('redeem.redeemSuccess')">
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
                  ({{ t('redeem.subscriptionDays', { days: redeemResult.validity_days }) }})</span>
              </p>
              <p v-if="redeemResult.type === 'balance' && userRefreshedAfterRedeem && user">
                {{ t('redeem.newBalance') }}:
                <span class="font-semibold">${{ user.balance.toFixed(2) }}</span>
              </p>
              <p v-if="redeemResult.type === 'concurrency' && userRefreshedAfterRedeem && user">
                {{ t('redeem.newConcurrency') }}:
                <span class="font-semibold">{{ user.concurrency }} {{ t('redeem.requests') }}</span>
              </p>
            </div>
          </template>
        </UAlert>
      </transition>

      <transition name="fade">
        <UAlert v-if="errorMessage" color="error" variant="subtle" icon="i-lucide-circle-alert" :title="t('redeem.redeemFailed')" :description="errorMessage" />
      </transition>

      <section>
        <UPageCard :title="t('redeem.recentActivity')" variant="naked" orientation="horizontal" class="mb-4" />
        <UPageCard variant="subtle" :ui="{ container: 'p-0 sm:p-0 gap-y-0' }">
          <div v-if="loadingHistory" class="flex items-center justify-center py-10">
            <UIcon name="i-lucide-loader-circle" class="size-6 animate-spin text-muted" />
          </div>

          <ul v-else-if="history.length > 0" class="divide-y divide-default">
            <li v-for="item in history" :key="item.id" class="flex items-center justify-between gap-4 px-4 py-3 sm:px-6">
              <div class="flex min-w-0 items-center gap-3">
                <UAvatar
                  :icon="isBalanceType(item.type) ? 'i-lucide-circle-dollar-sign' : isSubscriptionType(item.type) ? 'i-lucide-badge-check' : 'i-lucide-zap'"
                  size="md"
                  :class="toneBg(item)"
                  :ui="{ icon: toneText(item) }"
                />
                <div class="min-w-0">
                  <p class="text-sm font-medium text-highlighted">{{ getHistoryItemTitle(item) }}</p>
                  <p class="text-xs tabular-nums text-muted">{{ formatDateTime(item.used_at) }}</p>
                </div>
              </div>
              <div class="shrink-0 text-right">
                <p class="text-sm font-semibold tabular-nums" :class="toneText(item)">{{ formatHistoryValue(item) }}</p>
                <p v-if="!isAdminAdjustment(item.type)" class="font-mono text-xs text-dimmed">{{ item.code.slice(0, 8) }}...</p>
                <p v-else class="text-xs text-dimmed">{{ t('redeem.adminAdjustment') }}</p>
                <p v-if="item.notes" class="mt-1 max-w-[200px] truncate text-xs italic text-muted" :title="item.notes">{{ item.notes }}</p>
              </div>
            </li>
          </ul>

          <div v-else class="flex flex-col items-center gap-2 py-10 text-center">
            <UIcon name="i-lucide-clock" class="size-8 text-dimmed" />
            <p class="text-sm text-muted">{{ t('redeem.historyWillAppear') }}</p>
          </div>
        </UPageCard>
        <div class="mt-4 flex flex-wrap items-center justify-between gap-3 text-sm text-muted">
          <span class="tabular-nums">{{ t('common.total') }}: {{ historyTotal }} {{ t('pagination.results') }}</span>
          <div class="flex flex-wrap items-center gap-1.5">
            <span>{{ t('pagination.perPage') }}</span>
            <Select
              v-model="historyPageSize"
              data-testid="history-page-size"
              class="w-20"
              :options="pageSizeOptions"
              :aria-label="t('pagination.perPage')"
              :disabled="loadingHistory || submitting"
              @change="fetchHistory(1)"
            />
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
        </div>
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
import Select from '@/components/common/Select.vue'
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

// History row tone: balance in/out = success/error, subscription = info,
// concurrency up/down = primary/warning (same buckets as before, on theme tokens).
const tone = (item: RedeemHistoryItem) =>
  isBalanceType(item.type) ? (item.value >= 0 ? 'success' : 'error')
    : isSubscriptionType(item.type) ? 'info'
      : item.value >= 0 ? 'primary' : 'warning'
const TONE_TEXT = { success: 'text-success', error: 'text-error', info: 'text-info', primary: 'text-primary', warning: 'text-warning' } as const
const TONE_BG = { success: 'bg-success/10', error: 'bg-error/10', info: 'bg-info/10', primary: 'bg-primary/10', warning: 'bg-warning/10' } as const
const toneText = (item: RedeemHistoryItem) => TONE_TEXT[tone(item)]
const toneBg = (item: RedeemHistoryItem) => TONE_BG[tone(item)]

const pageSizeOptions = [20, 50, 100].map((size) => ({ value: size, label: String(size) }))

// Stat cards like the template HomeStats.
const statUi = {
  container: 'gap-y-1.5',
  wrapper: 'items-start',
  leading: 'p-2.5 rounded-full bg-primary/10 ring ring-inset ring-primary/25',
  title: 'font-normal text-muted text-xs uppercase'
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
