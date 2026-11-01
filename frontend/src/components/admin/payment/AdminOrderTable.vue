<template>
  <div class="space-y-4">
    <!-- Toolbar row (template customers page) -->
    <div class="flex flex-wrap items-center justify-between gap-1.5">
      <div class="flex min-w-0 flex-1 flex-wrap items-center gap-1.5">
        <UInput
          v-model="searchQuery"
          icon="i-lucide-search"
          class="w-full sm:max-w-xs"
          :placeholder="t('payment.admin.searchOrders')"
          @update:model-value="handleSearch"
        />
        <Select v-model="filters.status" :options="statusFilterOptions" class="w-full sm:w-36" @change="emitFiltersChanged" />
        <Select v-model="filters.payment_type" :options="paymentTypeFilterOptions" class="w-full sm:w-40" @change="emitFiltersChanged" />
        <Select v-model="filters.order_type" :options="orderTypeFilterOptions" class="w-full sm:w-36" @change="emitFiltersChanged" />
      </div>
      <UButton
        color="neutral"
        variant="outline"
        icon="i-lucide-refresh-cw"
        square
        :disabled="loading"
        :ui="{ leadingIcon: loading ? 'animate-spin' : '' }"
        :title="t('common.refresh')"
        :aria-label="t('common.refresh')"
        @click="emit('refresh')"
      />
    </div>

    <DataTable :columns="columns" :data="orders" :loading="loading">
      <template #cell-id="{ value }">
        <span class="font-mono text-highlighted">#{{ value }}</span>
      </template>
      <template #cell-user_id="{ value }">
        <span class="text-muted">#{{ value }}</span>
      </template>
      <template #cell-pay_amount="{ value, row }">
        <div>
          <span class="font-medium tabular-nums text-highlighted">{{ paymentAmountSymbol(row) }}{{ value.toFixed(2) }}</span>
          <span v-if="row.fee_rate > 0" class="ml-1 text-xs text-muted" :title="t('payment.orders.fee') + ': ' + row.fee_rate + '%'">({{ row.fee_rate }}%)</span>
          <div v-if="row.amount !== row.pay_amount" class="text-xs text-muted">
            {{ t('payment.orders.creditedAmount') }}: {{ creditedAmountSymbol }}{{ row.amount.toFixed(2) }}
          </div>
        </div>
      </template>
      <template #cell-payment_type="{ value }">
        <span class="text-default">{{ t('payment.methods.' + value, value) }}</span>
      </template>
      <template #cell-status="{ value }">
        <UBadge :color="statusColor(value)" variant="subtle" :label="t('payment.status.' + value.toLowerCase(), value)" />
      </template>
      <template #cell-order_type="{ value }">
        <span class="text-default">{{ t('payment.admin.' + value + 'Order', value) }}</span>
      </template>
      <template #cell-created_at="{ value }">
        <span class="text-xs text-muted">{{ formatDateTime(value) }}</span>
      </template>
      <!-- View + the one action the order state allows -->
      <template #cell-actions="{ row }">
        <div class="flex items-center gap-0.5">
          <AdminRowAction :label="t('common.view')" icon="eye" @click="emit('detail', row)" />
          <UButton v-if="row.status === 'PENDING'" color="warning" variant="ghost" size="xs" icon="i-lucide-x" :label="t('payment.orders.cancel')" @click="emit('cancel', row)" />
          <UButton v-if="row.status === 'FAILED'" color="info" variant="ghost" size="xs" icon="i-lucide-rotate-cw" :label="t('payment.admin.retry')" @click="emit('retry', row)" />
          <UButton v-if="canRefundRow(row)" color="error" variant="ghost" size="xs" icon="i-lucide-circle-dollar-sign" :label="t('payment.admin.refund')" @click="emit('refund', row)" />
        </div>
      </template>
    </DataTable>

    <Pagination
      v-if="total > 0"
      :page="page"
      :total="total"
      :page-size="pageSize"
      @update:page="emit('update:page', $event)"
      @update:pageSize="emit('update:pageSize', $event)"
    />
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { PaymentOrder } from '@/types/payment'
import type { Column } from '@/components/common/types'
import DataTable from '@/components/common/DataTable.vue'
import Pagination from '@/components/common/Pagination.vue'
import Select from '@/components/common/Select.vue'
import AdminRowAction from '@/components/admin/page/AdminRowAction.vue'
import { statusBadgeClass, canRefund, formatOrderDateTime } from '@/components/payment/orderUtils'
import { currencySymbol } from '@/components/payment/currency'

const { t } = useI18n()

defineProps<{
  orders: PaymentOrder[]
  loading: boolean
  page: number
  pageSize: number
  total: number
}>()

const emit = defineEmits<{
  (e: 'detail', order: PaymentOrder): void
  (e: 'cancel', order: PaymentOrder): void
  (e: 'retry', order: PaymentOrder): void
  (e: 'refund', order: PaymentOrder): void
  (e: 'refresh'): void
  (e: 'update:page', page: number): void
  (e: 'update:pageSize', size: number): void
  (e: 'filter', filters: { keyword?: string; status?: string; payment_type?: string; order_type?: string }): void
}>()

const searchQuery = ref('')
const filters = reactive({ status: '', payment_type: '', order_type: '' })
const creditedAmountSymbol = currencySymbol('USD')

function paymentAmountSymbol(order: PaymentOrder): string {
  return currencySymbol(order.currency)
}

let debounceTimer: ReturnType<typeof setTimeout> | null = null
function handleSearch() {
  if (debounceTimer) clearTimeout(debounceTimer)
  debounceTimer = setTimeout(() => emitFiltersChanged(), 300)
}

function emitFiltersChanged() {
  emit('filter', {
    keyword: searchQuery.value || undefined,
    status: filters.status || undefined,
    payment_type: filters.payment_type || undefined,
    order_type: filters.order_type || undefined,
  })
}

const columns = computed<Column[]>(() => [
  { key: 'id', label: t('payment.orders.orderId') },
  { key: 'user_id', label: t('payment.orders.userId') },
  { key: 'pay_amount', label: t('payment.orders.payAmount') },
  { key: 'payment_type', label: t('payment.orders.paymentMethod') },
  { key: 'status', label: t('payment.orders.status') },
  { key: 'order_type', label: t('payment.orders.orderType') },
  { key: 'created_at', label: t('payment.orders.createdAt') },
  { key: 'actions', label: t('payment.orders.actions') },
])

const statusFilterOptions = computed(() => [
  { value: '', label: t('payment.admin.allStatuses') },
  { value: 'PENDING', label: t('payment.status.pending') },
  { value: 'PAID', label: t('payment.status.paid') },
  { value: 'COMPLETED', label: t('payment.status.completed') },
  { value: 'EXPIRED', label: t('payment.status.expired') },
  { value: 'CANCELLED', label: t('payment.status.cancelled') },
  { value: 'FAILED', label: t('payment.status.failed') },
  { value: 'REFUNDED', label: t('payment.status.refunded') },
  { value: 'REFUND_REQUESTED', label: t('payment.status.refund_requested') },
  { value: 'REFUND_PENDING', label: t('payment.status.refund_pending') },
  { value: 'REFUND_FAILED', label: t('payment.status.refund_failed') },
])

const paymentTypeFilterOptions = computed(() => [
  { value: '', label: t('payment.admin.allPaymentTypes') },
  { value: 'alipay', label: t('payment.methods.alipay') },
  { value: 'wxpay', label: t('payment.methods.wxpay') },
  { value: 'stripe', label: t('payment.methods.stripe') },
  { value: 'airwallex', label: t('payment.methods.airwallex') },
  { value: 'epusdt', label: t('payment.methods.epusdt') },
])

const orderTypeFilterOptions = computed(() => [
  { value: '', label: t('payment.admin.allOrderTypes') },
  { value: 'balance', label: t('payment.admin.balanceOrder') },
  { value: 'subscription', label: t('payment.admin.subscriptionOrder') },
])

function canRefundRow(order: PaymentOrder): boolean {
  return canRefund(order.status)
}

function formatDateTime(dateStr: string): string {
  return formatOrderDateTime(dateStr)
}

// Status chip color from the shared status → badge class map
type StatusColor = 'success' | 'warning' | 'error' | 'primary' | 'info' | 'neutral'
function statusColor(status: string): StatusColor {
  const cls = statusBadgeClass(status)
  if (cls.includes('success')) return 'success'
  if (cls.includes('warning')) return 'warning'
  if (cls.includes('danger') || cls.includes('error')) return 'error'
  if (cls.includes('primary') || cls.includes('purple')) return 'primary'
  if (cls.includes('info') || cls.includes('blue')) return 'info'
  return 'neutral'
}
</script>
