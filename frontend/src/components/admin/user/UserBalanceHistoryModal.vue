<template>
  <BaseDialog :show="show" :title="t('admin.users.balanceHistoryTitle')" width="wide" :close-on-click-outside="true" :z-index="40" @close="$emit('close')">
    <div v-if="user" class="space-y-4">
      <!-- User header: identity left, current balance right -->
      <div class="space-y-2">
        <div class="flex items-center gap-3">
          <UAvatar :alt="user.email" :text="(user.email).charAt(0).toUpperCase()" size="md" />
          <div class="min-w-0 flex-1">
            <div class="flex items-center gap-2">
              <p class="truncate font-medium text-highlighted">{{ user.email }}</p>
              <UBadge v-if="user.deleted_at" color="error" variant="subtle" size="sm" :label="t('admin.usage.userDeletedBadge')" />
              <UBadge v-if="user.username" color="neutral" variant="subtle" size="sm" :label="user.username" />
            </div>
            <p class="text-xs text-muted">{{ t('admin.users.createdAt') }}: {{ formatDateTime(user.created_at) }}</p>
          </div>
          <div class="shrink-0 text-right">
            <p class="text-xs text-muted">{{ t('admin.users.currentBalance') }}</p>
            <p class="text-xl font-semibold tabular-nums text-highlighted">${{ user.balance?.toFixed(2) || '0.00' }}</p>
          </div>
        </div>
        <div class="flex items-center justify-between gap-4 text-xs text-muted">
          <p class="min-w-0 flex-1 truncate" :title="user.notes || ''">
            <template v-if="user.notes">{{ t('admin.users.notes') }}: {{ user.notes }}</template>
            <template v-else>&nbsp;</template>
          </p>
          <p class="shrink-0">
            {{ t('admin.users.totalRecharged') }}: <span class="font-semibold tabular-nums text-success">${{ totalRecharged.toFixed(2) }}</span>
          </p>
        </div>
      </div>

      <!-- Type filter + actions -->
      <div class="flex flex-wrap items-center gap-1.5">
        <Select v-model="typeFilter" :options="typeOptions" class="w-full sm:w-56" @change="loadHistory(1)" />
        <UButton v-if="!hideActions" color="neutral" variant="outline" icon="i-lucide-circle-plus" :label="t('admin.users.deposit')" @click="emit('deposit')" />
        <UButton v-if="!hideActions" color="neutral" variant="outline" icon="i-lucide-circle-minus" :label="t('admin.users.withdraw')" @click="emit('withdraw')" />
      </div>

      <div v-if="loading" class="flex justify-center py-8">
        <UIcon name="i-lucide-loader-circle" class="size-8 animate-spin text-dimmed" />
      </div>
      <p v-else-if="history.length === 0" class="py-8 text-center text-sm text-muted">{{ t('admin.users.noBalanceHistory') }}</p>

      <ul v-else class="max-h-112 divide-y divide-default overflow-y-auto border-y border-default">
        <li v-for="item in history" :key="item.id" class="flex items-start justify-between gap-3 py-3">
          <div class="flex min-w-0 items-start gap-3">
            <span :class="['flex size-8 shrink-0 items-center justify-center rounded-full', getIconBg(item)]">
              <UIcon :name="getIconName(item)" :class="['size-4', getIconColor(item)]" />
            </span>
            <div class="min-w-0">
              <p class="text-sm font-medium text-highlighted">{{ getItemTitle(item) }}</p>
              <p v-if="item.notes" class="mt-0.5 text-xs text-muted" :title="item.notes">
                {{ item.notes.length > 60 ? item.notes.substring(0, 55) + '...' : item.notes }}
              </p>
              <p class="mt-0.5 text-xs text-dimmed">{{ formatDateTime(item.used_at || item.created_at) }}</p>
            </div>
          </div>
          <div class="shrink-0 text-right">
            <p :class="['text-sm font-semibold tabular-nums', getValueColor(item)]">{{ formatValue(item) }}</p>
            <p v-if="isAdminType(item.type)" class="text-xs text-dimmed">{{ t('redeem.adminAdjustment') }}</p>
            <p v-else class="font-mono text-xs text-dimmed">{{ item.code.slice(0, 8) }}...</p>
          </div>
        </li>
      </ul>

      <div v-if="totalPages > 1" class="flex items-center justify-center gap-2 pt-2">
        <UButton color="neutral" variant="outline" size="sm" :disabled="currentPage <= 1" :label="t('pagination.previous')" @click="loadHistory(currentPage - 1)" />
        <span class="text-sm tabular-nums text-muted">{{ currentPage }} / {{ totalPages }}</span>
        <UButton color="neutral" variant="outline" size="sm" :disabled="currentPage >= totalPages" :label="t('pagination.next')" @click="loadHistory(currentPage + 1)" />
      </div>
    </div>
  </BaseDialog>
</template>

<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI, type BalanceHistoryItem } from '@/api/admin'
import { formatDateTime } from '@/utils/format'
import type { AdminUser } from '@/types'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Select from '@/components/common/Select.vue'

const props = defineProps<{ show: boolean; user: AdminUser | null; hideActions?: boolean }>()
const emit = defineEmits(['close', 'deposit', 'withdraw'])
const { t } = useI18n()

const history = ref<BalanceHistoryItem[]>([])
const loading = ref(false)
const currentPage = ref(1)
const total = ref(0)
const totalRecharged = ref(0)
const pageSize = 15
const typeFilter = ref('')
let requestVersion = 0

const totalPages = computed(() => Math.ceil(total.value / pageSize) || 1)

// Type filter options
const typeOptions = computed(() => [
  { value: '', label: t('admin.users.allTypes') },
  { value: 'balance', label: t('admin.users.typeBalance') },
  { value: 'payment_balance', label: t('admin.users.typeCryptoBalance') },
  { value: 'affiliate_balance', label: t('admin.users.typeAffiliateBalance') },
  { value: 'admin_balance', label: t('admin.users.typeAdminBalance') },
  { value: 'concurrency', label: t('admin.users.typeConcurrency') },
  { value: 'admin_concurrency', label: t('admin.users.typeAdminConcurrency') },
  { value: 'subscription', label: t('admin.users.typeSubscription') }
])

// Watch modal open
watch(() => [props.show, props.user?.id] as const, ([show], _, onCleanup) => {
  onCleanup(() => { requestVersion++ })
  history.value = []
  total.value = 0
  totalRecharged.value = 0
  if (show && props.user) {
    typeFilter.value = ''
    loadHistory(1)
  }
})

const loadHistory = async (page: number) => {
  if (!props.user) return
  const version = ++requestVersion
  loading.value = true
  currentPage.value = page
  try {
    const res = await adminAPI.users.getUserBalanceHistory(
      props.user.id,
      page,
      pageSize,
      typeFilter.value || undefined
    )
    if (version !== requestVersion) return
    history.value = res.items || []
    total.value = res.total || 0
    totalRecharged.value = res.total_recharged || 0
  } catch (error) {
    if (version !== requestVersion) return
    console.error('Failed to load balance history:', error)
  } finally {
    if (version === requestVersion) loading.value = false
  }
}

// Helper: check if admin type
const isAdminType = (type: string) => type === 'admin_balance' || type === 'admin_concurrency'

// Helper: check if balance type (includes admin_balance)
const isBalanceType = (type: string) => type === 'balance' || type === 'payment_balance' || type === 'admin_balance' || type === 'affiliate_balance'

// Helper: check if subscription type
const isSubscriptionType = (type: string) => type === 'subscription'

// Icon name based on type
const getIconName = (item: BalanceHistoryItem) => {
  if (isBalanceType(item.type)) return 'i-lucide-circle-dollar-sign'
  if (isSubscriptionType(item.type)) return 'i-lucide-badge-check'
  return 'i-lucide-zap' // concurrency
}

// Icon background color
const getIconBg = (item: BalanceHistoryItem) => {
  if (isBalanceType(item.type)) return item.value >= 0 ? 'bg-success/10' : 'bg-error/10'
  if (isSubscriptionType(item.type)) return 'bg-primary/10'
  return item.value >= 0 ? 'bg-info/10' : 'bg-warning/10'
}

// Icon text color
const getIconColor = (item: BalanceHistoryItem) => getValueColor(item)

// Value text color
const getValueColor = (item: BalanceHistoryItem) => {
  if (isBalanceType(item.type)) return item.value >= 0 ? 'text-success' : 'text-error'
  if (isSubscriptionType(item.type)) return 'text-primary'
  return item.value >= 0 ? 'text-info' : 'text-warning'
}

// Item title
const getItemTitle = (item: BalanceHistoryItem) => {
  switch (item.type) {
    case 'balance':
      return t('redeem.balanceAddedRedeem')
    case 'payment_balance':
      return t('redeem.balanceAddedCrypto')
    case 'affiliate_balance':
      return t('redeem.balanceAddedAffiliate')
    case 'admin_balance':
      return item.value >= 0 ? t('redeem.balanceAddedAdmin') : t('redeem.balanceDeductedAdmin')
    case 'concurrency':
      return t('redeem.concurrencyAddedRedeem')
    case 'admin_concurrency':
      return item.value >= 0 ? t('redeem.concurrencyAddedAdmin') : t('redeem.concurrencyReducedAdmin')
    case 'subscription':
      return t('redeem.subscriptionAssigned')
    default:
      return t('common.unknown')
  }
}

// Format display value
const formatValue = (item: BalanceHistoryItem) => {
  if (isBalanceType(item.type)) {
    const sign = item.value >= 0 ? '+' : ''
    return `${sign}$${item.value.toFixed(2)}`
  }
  if (isSubscriptionType(item.type)) {
    const days = item.validity_days || Math.round(item.value)
    const groupName = item.group?.name || ''
    return groupName ? `${days}d - ${groupName}` : `${days}d`
  }
  // concurrency types
  const sign = item.value >= 0 ? '+' : ''
  return `${sign}${item.value}`
}
</script>
