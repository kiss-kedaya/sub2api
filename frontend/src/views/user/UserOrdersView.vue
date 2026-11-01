<template>
  <AppLayout>
    <div class="signal-orders">
      <UserPageHeader flush :title="t('nav.myOrders')">
        <template #badge>
          <UBadge color="neutral" variant="subtle" class="tabular-nums">{{ pagination.total }}</UBadge>
        </template>
        <template #actions>
          <UButton icon="i-lucide-credit-card" @click="router.push('/purchase')">{{ t('payment.result.backToRecharge') }}</UButton>
        </template>
      </UserPageHeader>
      <!-- Filters -->
      <div class="signal-orders-toolbar">
        <div class="flex min-w-0 flex-1 flex-wrap items-center gap-3">
          <Select v-model="currentFilter" :options="statusFilters" class="w-36" @change="handlePageChange(1)" />
        </div>
        <UButton color="neutral" variant="outline" icon="i-lucide-refresh-cw" :ui="{ leadingIcon: loading ? 'animate-spin' : '' }" :disabled="loading" :title="t('common.refresh')" :aria-label="t('common.refresh')" @click="fetchOrders" />
      </div>

      <!-- Table -->
      <OrderTable :orders="orders" :loading="loading">
        <template #actions="{ row }">
          <div class="flex items-center gap-2">
            <button v-if="row.status === 'PENDING'" @click="handleCancel(row.id)" class="signal-order-action signal-order-action--warning">
              <Icon name="x" size="sm" />
              <span>{{ t('payment.orders.cancel') }}</span>
            </button>
            <button v-if="canRequestRefund(row)" @click="openRefundDialog(row)" class="signal-order-action">
              <Icon name="dollar" size="sm" />
              <span>{{ t('payment.orders.requestRefund') }}</span>
            </button>
          </div>
        </template>
      </OrderTable>

      <!-- Pagination -->
      <Pagination
        v-if="pagination.total > 0"
        :page="pagination.page"
        :total="pagination.total"
        :page-size="pagination.page_size"
        @update:page="handlePageChange"
        @update:pageSize="handlePageSizeChange"
      />
    </div>

    <!-- Cancel Confirm Dialog -->
    <BaseDialog :show="!!cancelTargetId" :title="t('payment.orders.cancel')" width="narrow" @close="cancelTargetId = null">
      <p class="text-sm text-gray-600 dark:text-gray-300">{{ t('payment.confirmCancel') }}</p>
      <template #footer>
        <div class="flex justify-end gap-3">
          <button class="btn btn-secondary" @click="cancelTargetId = null">{{ t('common.cancel') }}</button>
          <button class="btn btn-danger" :disabled="actionLoading" @click="confirmCancel">{{ actionLoading ? t('common.processing') : t('payment.orders.cancel') }}</button>
        </div>
      </template>
    </BaseDialog>

    <!-- Refund Dialog -->
    <BaseDialog :show="!!refundTarget" :title="t('payment.orders.requestRefund')" @close="refundTarget = null">
      <div v-if="refundTarget" class="space-y-4">
        <div class="rounded-xl bg-gray-50 p-4 dark:bg-dark-800">
          <div class="flex justify-between text-sm">
            <span class="text-gray-500 dark:text-gray-400">{{ t('payment.orders.orderId') }}</span>
            <span class="font-mono text-gray-900 dark:text-white">#{{ refundTarget.id }}</span>
          </div>
          <div class="mt-2 flex justify-between text-sm">
            <span class="text-gray-500 dark:text-gray-400">{{ t('payment.orders.amount') }}</span>
            <span class="text-gray-900 dark:text-white">${{ refundTarget.amount.toFixed(2) }}</span>
          </div>
        </div>
        <div>
          <label class="input-label">{{ t('payment.refundReason') }}</label>
          <textarea v-model="refundReason" rows="3" class="input mt-1 w-full" :placeholder="t('payment.refundReasonPlaceholder')" />
        </div>
      </div>
      <template #footer>
        <div class="flex justify-end gap-3">
          <button class="btn btn-secondary" @click="refundTarget = null">{{ t('common.cancel') }}</button>
          <button class="btn btn-primary" :disabled="actionLoading || !refundReason.trim()" @click="confirmRefund">{{ actionLoading ? t('common.processing') : t('payment.orders.requestRefund') }}</button>
        </div>
      </template>
    </BaseDialog>
  </AppLayout>
</template>

<script setup lang="ts">
import { ref, reactive, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { useAppStore } from '@/stores'
import { paymentAPI } from '@/api/payment'
import { extractI18nErrorMessage } from '@/utils/apiError'
import type { PaymentOrder } from '@/types/payment'
import AppLayout from '@/components/layout/AppLayout.vue'
import UserPageHeader from '@/components/user/UserPageHeader.vue'
import Pagination from '@/components/common/Pagination.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Select from '@/components/common/Select.vue'
import Icon from '@/components/icons/Icon.vue'
import OrderTable from '@/components/payment/OrderTable.vue'

const { t } = useI18n()
const router = useRouter()
const appStore = useAppStore()

const loading = ref(false)
const actionLoading = ref(false)
const orders = ref<PaymentOrder[]>([])
const refundEligibleProviders = ref<Set<string>>(new Set())
const currentFilter = ref('')
const cancelTargetId = ref<number | null>(null)
const refundTarget = ref<PaymentOrder | null>(null)
const refundReason = ref('')
const pagination = reactive({ page: 1, page_size: 20, total: 0 })

const statusFilters = computed(() => [
  { value: '', label: t('common.all') },
  { value: 'PENDING', label: t('payment.status.pending') },
  { value: 'COMPLETED', label: t('payment.status.completed') },
  { value: 'FAILED', label: t('payment.status.failed') },
  { value: 'REFUNDED', label: t('payment.status.refunded') },
])

async function fetchOrders() {
  loading.value = true
  try {
    const res = await paymentAPI.getMyOrders({
      page: pagination.page,
      page_size: pagination.page_size,
      status: currentFilter.value || undefined,
    })
    orders.value = res.data.items || []
    pagination.total = res.data.total || 0
    if (res.data.page_size > 0) pagination.page_size = res.data.page_size
  } catch (err: unknown) {
    appStore.showError(extractI18nErrorMessage(err, t, 'payment.errors', t('common.error')))
  } finally {
    loading.value = false
  }
}

function handlePageChange(page: number) { pagination.page = page; fetchOrders() }
function handlePageSizeChange(size: number) { pagination.page_size = size; pagination.page = 1; fetchOrders() }

function handleCancel(orderId: number) { cancelTargetId.value = orderId }

async function confirmCancel() {
  if (!cancelTargetId.value) return
  actionLoading.value = true
  try {
    await paymentAPI.cancelOrder(cancelTargetId.value)
    appStore.showSuccess(t('common.success'))
    cancelTargetId.value = null
    await fetchOrders()
  } catch (err: unknown) {
    appStore.showError(extractI18nErrorMessage(err, t, 'payment.errors', t('common.error')))
  } finally {
    actionLoading.value = false
  }
}

function openRefundDialog(order: PaymentOrder) { refundTarget.value = order; refundReason.value = '' }

async function confirmRefund() {
  if (!refundTarget.value || !refundReason.value.trim()) return
  actionLoading.value = true
  try {
    await paymentAPI.requestRefund(refundTarget.value.id, { reason: refundReason.value.trim() })
    appStore.showSuccess(t('common.success'))
    refundTarget.value = null
    refundReason.value = ''
    await fetchOrders()
  } catch (err: unknown) {
    appStore.showError(extractI18nErrorMessage(err, t, 'payment.errors', t('common.error')))
  } finally {
    actionLoading.value = false
  }
}

function canRequestRefund(order: PaymentOrder): boolean {
  if (order.status !== 'COMPLETED') return false
  if (!order.provider_instance_id) return false
  return refundEligibleProviders.value.has(order.provider_instance_id)
}

async function loadRefundEligibility() {
  try {
    const res = await paymentAPI.getRefundEligibleProviders()
    refundEligibleProviders.value = new Set(res.data.provider_instance_ids || [])
  } catch { /* ignore — default to hiding refund button */ }
}

onMounted(() => { fetchOrders(); loadRefundEligibility() })
</script>

<style scoped>
.signal-orders {
  --orders-bg: var(--signal-bg, #f5f6f5);
  --orders-surface: var(--ui-bg);
  --orders-line: var(--ui-border);
  --orders-text: var(--ui-text-highlighted);
  --orders-muted: var(--ui-text-muted);
  --orders-accent: var(--signal-accent, #087f68);
  --orders-accent-soft: var(--signal-accent-soft, #e5f3ee);
  --orders-amber: var(--signal-amber, #a86610);
  display: grid;
  gap: 14px;
  min-width: 0;
  color: var(--orders-text);
  letter-spacing: 0;
}

.signal-orders-toolbar {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 10px 12px;
  border: 1px solid var(--orders-line);
  border-radius: 8px;
  background: var(--orders-surface);
  box-shadow: 0 1px 2px rgba(10, 10, 10, 0.05);
}

.dark .signal-orders-toolbar { box-shadow: none; }

.signal-order-action {
  display: inline-flex;
  min-height: 32px;
  align-items: center;
  gap: 5px;
  padding: 5px 8px;
  border: 1px solid var(--orders-line);
  border-radius: 6px;
  color: var(--orders-accent);
  background: transparent;
  font-size: 12px;
  font-weight: 600;
}

.signal-order-action:hover {
  border-color: var(--orders-accent);
  background: var(--orders-accent-soft);
}

.signal-order-action--warning {
  color: var(--orders-amber);
}

.signal-order-action--warning:hover {
  border-color: var(--orders-amber);
  background: color-mix(in srgb, var(--orders-amber) 10%, transparent);
}

.signal-orders :deep(.payment-order-table) {
  overflow: hidden;
  border: 1px solid var(--orders-line);
  border-radius: 8px;
  background: var(--orders-surface);
  box-shadow: 0 1px 2px rgba(10, 10, 10, 0.05);
}

.dark .signal-orders :deep(.payment-order-table) { box-shadow: none; }

.signal-orders :deep(.payment-order-table .table-wrapper) {
  min-height: 240px;
}

.signal-orders :deep(.payment-order-number) {
  display: inline-block;
  max-width: 220px;
  overflow: hidden;
  color: var(--orders-text);
  font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
  font-size: 12px;
  text-overflow: ellipsis;
  vertical-align: bottom;
}

.signal-orders :deep(.payment-order-status) {
  border: 1px solid currentColor;
  border-radius: 4px;
  background: transparent;
}

@media (max-width: 639px) {
  .signal-orders-toolbar :deep(.select-trigger) {
    min-height: 42px;
  }

  .signal-orders :deep(.payment-order-table) {
    overflow: visible;
    border: 0;
    background: transparent;
  }
}

@media (max-width: 374px) {
  .signal-orders-toolbar {
    align-items: stretch;
  }

  .signal-orders-toolbar > div,
  .signal-orders-toolbar :deep(.select-trigger) {
    width: 100%;
  }
}
</style>
