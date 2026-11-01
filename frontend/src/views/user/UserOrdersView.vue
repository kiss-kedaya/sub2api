<template>
  <AppLayout>
    <div class="flex flex-col gap-4 sm:gap-6">
      <UserPageHeader flush :title="t('nav.myOrders')">
        <template #badge>
          <UBadge color="neutral" variant="subtle" class="tabular-nums">{{ pagination.total }}</UBadge>
        </template>
      </UserPageHeader>
      <div class="flex flex-wrap items-center justify-between gap-1.5">
        <Select v-model="currentFilter" :options="statusFilters" class="w-36" @change="handlePageChange(1)" />
        <div class="flex items-center gap-1.5">
          <UButton color="neutral" variant="outline" icon="i-lucide-refresh-cw" :ui="{ leadingIcon: loading ? 'animate-spin' : '' }" :disabled="loading" :title="t('common.refresh')" :aria-label="t('common.refresh')" @click="fetchOrders" />
          <UButton icon="i-lucide-credit-card" @click="router.push('/purchase')">{{ t('payment.result.backToRecharge') }}</UButton>
        </div>
      </div>

      <!-- Table -->
      <OrderTable :orders="orders" :loading="loading">
        <template #actions="{ row }">
          <div class="flex items-center gap-1">
            <UButton v-if="row.status === 'PENDING'" color="error" variant="ghost" size="sm" icon="i-lucide-x" @click="handleCancel(row.id)">{{ t('payment.orders.cancel') }}</UButton>
            <UButton v-if="canRequestRefund(row)" color="neutral" variant="ghost" size="sm" icon="i-lucide-undo-2" @click="openRefundDialog(row)">{{ t('payment.orders.requestRefund') }}</UButton>
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
      <p class="text-sm text-muted">{{ t('payment.confirmCancel') }}</p>
      <template #footer>
        <div class="flex justify-end gap-3">
          <UButton color="neutral" variant="subtle" @click="cancelTargetId = null">{{ t('common.cancel') }}</UButton>
          <UButton color="error" :disabled="actionLoading" @click="confirmCancel">{{ actionLoading ? t('common.processing') : t('payment.orders.cancel') }}</UButton>
        </div>
      </template>
    </BaseDialog>

    <!-- Refund Dialog -->
    <BaseDialog :show="!!refundTarget" :title="t('payment.orders.requestRefund')" @close="refundTarget = null">
      <div v-if="refundTarget" class="space-y-4">
        <dl class="space-y-2 rounded-md bg-muted p-4 text-sm">
          <div class="flex justify-between">
            <dt class="text-muted">{{ t('payment.orders.orderId') }}</dt>
            <dd class="font-mono text-highlighted">#{{ refundTarget.id }}</dd>
          </div>
          <div class="flex justify-between">
            <dt class="text-muted">{{ t('payment.orders.amount') }}</dt>
            <dd class="tabular-nums text-highlighted">${{ refundTarget.amount.toFixed(2) }}</dd>
          </div>
        </dl>
        <UFormField :label="t('payment.refundReason')" required>
          <UTextarea v-model="refundReason" :rows="3" class="w-full" :placeholder="t('payment.refundReasonPlaceholder')" />
        </UFormField>
      </div>
      <template #footer>
        <div class="flex justify-end gap-3">
          <UButton color="neutral" variant="subtle" @click="refundTarget = null">{{ t('common.cancel') }}</UButton>
          <UButton :disabled="actionLoading || !refundReason.trim()" @click="confirmRefund">{{ actionLoading ? t('common.processing') : t('payment.orders.requestRefund') }}</UButton>
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
