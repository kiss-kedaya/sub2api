<template>
  <AppLayout>
    <div class="space-y-4">
      <AdminPageHeader :title="t('nav.orderManagement')" />
      <!-- Toolbar row (template customers page) -->
      <PageToolbar>
        <template #left>
          <UInput
            v-model="orderSearch"
            icon="i-lucide-search"
            class="w-56 sm:w-64"
            :placeholder="t('payment.admin.searchOrders')"
            @update:model-value="debounceLoadOrders"
          />
          <Select v-model="orderFilters.status" :options="statusFilterOptions" class="w-36" @change="loadOrders" />
          <Select v-model="orderFilters.payment_type" :options="paymentTypeFilterOptions" class="w-40" @change="loadOrders" />
          <Select v-model="orderFilters.order_type" :options="orderTypeFilterOptions" class="w-36" @change="loadOrders" />
        </template>
        <template #right>
          <UButton
            color="neutral"
            variant="outline"
            icon="i-lucide-refresh-cw"
            square
            :disabled="ordersLoading"
            :ui="{ leadingIcon: ordersLoading ? 'animate-spin' : '' }"
            :title="t('common.refresh')"
            :aria-label="t('common.refresh')"
            @click="loadOrders"
          />
        </template>
      </PageToolbar>

      <!-- Table: view + the one action the order state allows -->
      <OrderTable :orders="orders" :loading="ordersLoading" show-user>
        <template #actions="{ row }">
          <div class="flex items-center gap-0.5">
            <AdminRowAction :label="t('common.view')" icon="eye" @click="showOrderDetail(row)" />
            <UButton v-if="row.status === 'PENDING'" color="warning" variant="ghost" size="xs" icon="i-lucide-x" :label="t('payment.orders.cancel')" @click="handleCancelOrder(row)" />
            <UButton v-if="row.status === 'FAILED'" color="info" variant="ghost" size="xs" icon="i-lucide-rotate-cw" :label="t('payment.admin.retry')" @click="handleRetryOrder(row)" />
            <template v-if="row.status === 'REFUND_REQUESTED'">
              <UBadge v-if="row.refund_amount" color="neutral" variant="subtle" class="tabular-nums" :label="`${creditedAmountSymbol}${row.refund_amount.toFixed(2)}`" />
              <UButton color="primary" variant="ghost" size="xs" icon="i-lucide-check" :label="t('payment.admin.approveRefund')" @click="openRefundDialog(row)" />
            </template>
            <UButton v-else-if="row.status === 'REFUND_FAILED'" color="primary" variant="ghost" size="xs" icon="i-lucide-rotate-cw" :label="t('payment.admin.retryRefund')" @click="openRefundDialog(row)" />
            <UButton
              v-else-if="row.status === 'REFUND_PENDING'"
              color="warning"
              variant="ghost"
              size="xs"
              icon="i-lucide-refresh-cw"
              :disabled="refundQueryingIds.has(row.id)"
              :ui="{ leadingIcon: refundQueryingIds.has(row.id) ? 'animate-spin' : '' }"
              :label="t('payment.admin.queryRefundStatus')"
              @click="handleQueryRefund(row)"
            />
            <UButton
              v-else-if="row.status === 'COMPLETED' || row.status === 'PARTIALLY_REFUNDED'"
              color="error"
              variant="ghost"
              size="xs"
              icon="i-lucide-circle-dollar-sign"
              :label="t('payment.admin.refund')"
              @click="openRefundDialog(row)"
            />
          </div>
        </template>
      </OrderTable>
      <Pagination v-if="orderPagination.total > 0" :page="orderPagination.page" :total="orderPagination.total" :page-size="orderPagination.page_size" @update:page="handleOrderPageChange" @update:pageSize="handleOrderPageSizeChange" />
    </div>

    <!-- Order Detail Dialog -->
    <BaseDialog :show="showDetailDialog" :title="t('payment.admin.orderDetail')" width="wide" @close="showDetailDialog = false">
      <div v-if="selectedOrder" class="space-y-4">
        <dl class="grid grid-cols-2 gap-x-6 gap-y-3 text-sm">
          <div><dt class="text-xs text-muted">{{ t('payment.orders.orderId') }}</dt><dd class="font-mono font-medium text-highlighted">#{{ selectedOrder.id }}</dd></div>
          <div><dt class="text-xs text-muted">{{ t('payment.orders.orderNo') }}</dt><dd class="font-medium text-highlighted">{{ selectedOrder.out_trade_no }}</dd></div>
          <div><dt class="text-xs text-muted">{{ t('payment.orders.status') }}</dt><dd><OrderStatusBadge :status="selectedOrder.status" /></dd></div>
          <div><dt class="text-xs text-muted">{{ t('payment.orders.amount') }}</dt><dd class="font-medium tabular-nums text-highlighted">{{ creditedAmountSymbol }}{{ selectedOrder.amount.toFixed(2) }}</dd></div>
          <div><dt class="text-xs text-muted">{{ t('payment.orders.payAmount') }}</dt><dd class="font-medium tabular-nums text-highlighted">{{ paymentAmountSymbol(selectedOrder) }}{{ selectedOrder.pay_amount.toFixed(2) }}</dd></div>
          <div><dt class="text-xs text-muted">{{ t('payment.orders.paymentMethod') }}</dt><dd class="text-default">{{ t('payment.methods.' + selectedOrder.payment_type, selectedOrder.payment_type) }}</dd></div>
          <div><dt class="text-xs text-muted">{{ t('payment.admin.feeRate') }}</dt><dd class="text-default">{{ selectedOrder.fee_rate }}%</dd></div>
          <div><dt class="text-xs text-muted">{{ t('payment.orders.createdAt') }}</dt><dd class="text-default">{{ formatDateTime(selectedOrder.created_at) }}</dd></div>
          <div><dt class="text-xs text-muted">{{ t('payment.admin.expiresAt') }}</dt><dd class="text-default">{{ formatDateTime(selectedOrder.expires_at) }}</dd></div>
          <div v-if="selectedOrder.paid_at"><dt class="text-xs text-muted">{{ t('payment.admin.paidAt') }}</dt><dd class="text-default">{{ formatDateTime(selectedOrder.paid_at) }}</dd></div>
          <div v-if="selectedOrder.refund_amount"><dt class="text-xs text-muted">{{ t('payment.admin.refundAmount') }}</dt><dd class="font-medium tabular-nums text-error">{{ creditedAmountSymbol }}{{ selectedOrder.refund_amount.toFixed(2) }}</dd></div>
          <div v-if="selectedOrder.refund_reason" class="col-span-2"><dt class="text-xs text-muted">{{ t('payment.admin.refundReason') }}</dt><dd class="text-default">{{ selectedOrder.refund_reason }}</dd></div>
        </dl>
        <!-- Refund request info -->
        <template v-if="selectedOrder.refund_requested_at">
          <USeparator :label="t('payment.admin.refundRequestInfo')" />
          <dl class="grid grid-cols-2 gap-x-6 gap-y-3 text-sm">
            <div><dt class="text-xs text-muted">{{ t('payment.admin.refundRequestedAt') }}</dt><dd class="text-default">{{ formatDateTime(selectedOrder.refund_requested_at) }}</dd></div>
            <div><dt class="text-xs text-muted">{{ t('payment.admin.refundRequestedBy') }}</dt><dd class="text-default">#{{ selectedOrder.refund_requested_by }}</dd></div>
            <div class="col-span-2"><dt class="text-xs text-muted">{{ t('payment.admin.refundRequestReason') }}</dt><dd class="text-default">{{ selectedOrder.refund_request_reason }}</dd></div>
          </dl>
        </template>
        <!-- Audit Logs -->
        <template v-if="orderAuditLogs.length > 0">
          <USeparator :label="t('payment.admin.auditLogs')" />
          <ul class="max-h-48 divide-y divide-default overflow-y-auto">
            <li v-for="log in orderAuditLogs" :key="log.id" class="py-2">
              <div class="flex items-center justify-between gap-2">
                <span class="text-xs font-medium text-default">{{ log.action }}</span>
                <span class="text-xs text-dimmed">{{ formatDateTime(log.created_at) }}</span>
              </div>
              <p v-if="log.detail" class="mt-1 break-all text-xs text-muted">{{ log.detail }}</p>
              <p v-if="log.operator" class="mt-1 text-xs text-dimmed">{{ t('payment.admin.operator') }}: {{ log.operator }}</p>
            </li>
          </ul>
        </template>
      </div>
    </BaseDialog>

    <AdminRefundDialog :show="showRefundDialog" :order="selectedOrder" :submitting="refundSubmitting" :require-force="refundRequireForce" :warning="refundWarning" @confirm="handleRefund" @cancel="closeRefundDialog" />
  </AppLayout>
</template>

<script setup lang="ts">
import { ref, reactive, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { adminPaymentAPI } from '@/api/admin/payment'
import { extractI18nErrorMessage } from '@/utils/apiError'
import { formatOrderDateTime } from '@/components/payment/orderUtils'
import type { PaymentOrder } from '@/types/payment'
import AppLayout from '@/components/layout/AppLayout.vue'
import AdminPageHeader from '@/components/admin/page/AdminPageHeader.vue'
import PageToolbar from '@/components/layout/PageToolbar.vue'
import Pagination from '@/components/common/Pagination.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Select from '@/components/common/Select.vue'
import AdminRowAction from '@/components/admin/page/AdminRowAction.vue'
import AdminRefundDialog from '@/components/admin/payment/AdminRefundDialog.vue'
import OrderStatusBadge from '@/components/payment/OrderStatusBadge.vue'
import OrderTable from '@/components/payment/OrderTable.vue'
import { currencySymbol } from '@/components/payment/currency'

interface AuditLog {
  id: number
  action: string
  detail: string | null
  operator: string | null
  created_at: string
}

const { t } = useI18n()
const appStore = useAppStore()

const ordersLoading = ref(false)
const orders = ref<PaymentOrder[]>([])
const orderSearch = ref('')
const orderFilters = reactive({ status: '', payment_type: '', order_type: '' })
const orderPagination = reactive({ page: 1, page_size: 20, total: 0 })
const selectedOrder = ref<PaymentOrder | null>(null)
const showDetailDialog = ref(false)
const showRefundDialog = ref(false)
const refundSubmitting = ref(false)
const refundRequireForce = ref(false)
const refundWarning = ref('')
const refundQueryingIds = ref(new Set<number>())
const orderAuditLogs = ref<AuditLog[]>([])
const creditedAmountSymbol = currencySymbol('USD')

function paymentAmountSymbol(order: PaymentOrder | null | undefined): string {
  return currencySymbol(order?.currency)
}

let debounceTimer: ReturnType<typeof setTimeout> | null = null
function debounceLoadOrders() {
  if (debounceTimer) clearTimeout(debounceTimer)
  debounceTimer = setTimeout(() => loadOrders(), 300)
}

async function loadOrders() {
  ordersLoading.value = true
  try {
    const res = await adminPaymentAPI.getOrders({
      page: orderPagination.page, page_size: orderPagination.page_size,
      keyword: orderSearch.value || undefined, status: orderFilters.status || undefined,
      payment_type: orderFilters.payment_type || undefined, order_type: orderFilters.order_type || undefined,
    })
    orders.value = res.data.items || []
    orderPagination.total = res.data.total || 0
    if (res.data.page_size > 0) orderPagination.page_size = res.data.page_size
  } catch (err: unknown) {
    appStore.showError(extractI18nErrorMessage(err, t, 'payment.errors', t('common.error')))
  } finally { ordersLoading.value = false }
}

function handleOrderPageChange(page: number) { orderPagination.page = page; loadOrders() }
function handleOrderPageSizeChange(size: number) { orderPagination.page_size = size; orderPagination.page = 1; loadOrders() }

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

async function showOrderDetail(order: PaymentOrder) {
  selectedOrder.value = order
  orderAuditLogs.value = []
  showDetailDialog.value = true
  try {
    const res = await adminPaymentAPI.getOrder(order.id)
    selectedOrder.value = res.data.order
    orderAuditLogs.value = res.data.auditLogs
  } catch (_err: unknown) { /* keep cached order data */ }
}

async function handleCancelOrder(order: PaymentOrder) {
  try { await adminPaymentAPI.cancelOrder(order.id); appStore.showSuccess(t('payment.admin.orderCancelled')); loadOrders() }
  catch (err: unknown) { appStore.showError(extractI18nErrorMessage(err, t, 'payment.errors', t('common.error'))) }
}

async function handleRetryOrder(order: PaymentOrder) {
  try { await adminPaymentAPI.retryRecharge(order.id); appStore.showSuccess(t('payment.admin.retrySuccess')); loadOrders() }
  catch (err: unknown) { appStore.showError(extractI18nErrorMessage(err, t, 'payment.errors', t('common.error'))) }
}

function openRefundDialog(order: PaymentOrder) {
  selectedOrder.value = order
  refundRequireForce.value = false
  refundWarning.value = ''
  showRefundDialog.value = true
}

function closeRefundDialog() {
  showRefundDialog.value = false
  refundRequireForce.value = false
  refundWarning.value = ''
}

function isRefundPendingWarning(warning: string | undefined): boolean {
  return /pending|处理中|待/.test(String(warning || '').toLowerCase())
}

async function handleRefund(data: { amount: number; reason: string; deduct_balance: boolean; force: boolean }) {
  if (!selectedOrder.value) return
  refundSubmitting.value = true
  try {
    const res = await adminPaymentAPI.refundOrder(selectedOrder.value.id, { amount: data.amount, reason: data.reason, deduct_balance: data.deduct_balance, force: data.force })
    if (res.data.success) {
      appStore.showSuccess(t('payment.admin.refundSuccess'))
      closeRefundDialog()
      loadOrders()
      return
    }
    if (isRefundPendingWarning(res.data.warning)) {
      appStore.showSuccess(t('payment.admin.refundPending'))
      closeRefundDialog()
      loadOrders()
      return
    }
    if (res.data.require_force) {
      // Backend needs an explicit force confirmation (e.g. the user spent their
      // balance after requesting the refund). Keep the dialog open and surface
      // the force checkbox instead of dropping the admin back to the list.
      refundRequireForce.value = true
      refundWarning.value = res.data.warning || ''
      return
    }
    appStore.showError(res.data.warning || t('common.error'))
  } catch (err: unknown) { appStore.showError(extractI18nErrorMessage(err, t, 'payment.errors', t('common.error'))) }
  finally { refundSubmitting.value = false }
}

async function handleQueryRefund(order: PaymentOrder) {
  refundQueryingIds.value = new Set(refundQueryingIds.value).add(order.id)
  try {
    const res = await adminPaymentAPI.queryRefund(order.id)
    if (res.data.success) {
      appStore.showSuccess(t('payment.admin.refundSuccess'))
    } else if (isRefundPendingWarning(res.data.warning)) {
      appStore.showSuccess(t('payment.admin.refundPending'))
    } else {
      appStore.showError(res.data.warning || t('common.error'))
    }
    loadOrders()
  } catch (err: unknown) {
    appStore.showError(extractI18nErrorMessage(err, t, 'payment.errors', t('common.error')))
  } finally {
    const next = new Set(refundQueryingIds.value)
    next.delete(order.id)
    refundQueryingIds.value = next
  }
}

function formatDateTime(dateStr: string): string { return formatOrderDateTime(dateStr) }

onMounted(() => loadOrders())
</script>
