<template>
  <BaseDialog
    :show="show"
    :title="t('payment.admin.orderDetail')"
    width="wide"
    @close="emit('close')"
  >
    <div v-if="order" class="space-y-4">
      <dl class="grid grid-cols-2 gap-x-6 gap-y-3 text-sm">
        <div><dt class="text-xs text-muted">{{ t('payment.orders.orderId') }}</dt><dd class="font-mono font-medium text-highlighted">#{{ order.id }}</dd></div>
        <div>
          <dt class="text-xs text-muted">{{ t('payment.orders.status') }}</dt>
          <dd><UBadge :color="statusColor(order.status)" variant="subtle" :label="t('payment.status.' + order.status.toLowerCase(), order.status)" /></dd>
        </div>
        <div><dt class="text-xs text-muted">{{ t('payment.orders.baseAmount') }}</dt><dd class="font-medium tabular-nums text-highlighted">{{ paymentAmountSymbol }}{{ baseAmount.toFixed(2) }}</dd></div>
        <div v-if="order.fee_rate > 0"><dt class="text-xs text-muted">{{ t('payment.orders.fee') }} ({{ order.fee_rate }}%)</dt><dd class="font-medium tabular-nums text-highlighted">{{ paymentAmountSymbol }}{{ feeAmount.toFixed(2) }}</dd></div>
        <div><dt class="text-xs text-muted">{{ t('payment.orders.payAmount') }}</dt><dd class="font-medium tabular-nums text-highlighted">{{ paymentAmountSymbol }}{{ order.pay_amount.toFixed(2) }}</dd></div>
        <div v-if="order.amount !== order.pay_amount"><dt class="text-xs text-muted">{{ t('payment.orders.creditedAmount') }}</dt><dd class="font-medium tabular-nums text-highlighted">{{ creditedAmountSymbol }}{{ order.amount.toFixed(2) }}</dd></div>
        <div><dt class="text-xs text-muted">{{ t('payment.orders.paymentMethod') }}</dt><dd class="text-default">{{ t('payment.methods.' + order.payment_type, order.payment_type) }}</dd></div>
        <div><dt class="text-xs text-muted">{{ t('payment.admin.orderType') }}</dt><dd class="text-default">{{ t('payment.admin.' + order.order_type + 'Order', order.order_type) }}</dd></div>
        <div><dt class="text-xs text-muted">{{ t('payment.orders.userId') }}</dt><dd class="text-default">#{{ order.user_id }}</dd></div>
        <div><dt class="text-xs text-muted">{{ t('payment.orders.createdAt') }}</dt><dd class="text-default">{{ formatDateTime(order.created_at) }}</dd></div>
        <div><dt class="text-xs text-muted">{{ t('payment.admin.expiresAt') }}</dt><dd class="text-default">{{ formatDateTime(order.expires_at) }}</dd></div>
        <div v-if="order.paid_at"><dt class="text-xs text-muted">{{ t('payment.admin.paidAt') }}</dt><dd class="text-default">{{ formatDateTime(order.paid_at) }}</dd></div>
        <div v-if="order.completed_at"><dt class="text-xs text-muted">{{ t('payment.admin.completedAt') }}</dt><dd class="text-default">{{ formatDateTime(order.completed_at) }}</dd></div>
      </dl>

      <template v-if="order.refund_amount">
        <USeparator :label="t('payment.admin.refundInfo')" />
        <dl class="grid grid-cols-2 gap-x-6 gap-y-3 text-sm">
          <div><dt class="text-xs text-muted">{{ t('payment.admin.refundAmount') }}</dt><dd class="font-medium tabular-nums text-error">{{ creditedAmountSymbol }}{{ order.refund_amount.toFixed(2) }}</dd></div>
          <div v-if="order.refund_reason" class="col-span-2"><dt class="text-xs text-muted">{{ t('payment.admin.refundReason') }}</dt><dd class="text-default">{{ order.refund_reason }}</dd></div>
        </dl>
      </template>

      <div v-if="order.status === 'PENDING' || order.status === 'FAILED' || canRefund(order)" class="flex items-center justify-end gap-2 pt-2">
        <UButton v-if="order.status === 'PENDING'" color="warning" variant="subtle" size="sm" :label="t('payment.orders.cancel')" @click="emit('cancel', order)" />
        <UButton v-if="order.status === 'FAILED'" color="neutral" variant="subtle" size="sm" :label="t('payment.admin.retry')" @click="emit('retry', order)" />
        <UButton v-if="canRefund(order)" color="error" variant="subtle" size="sm" :label="t('payment.admin.refund')" @click="emit('refund', order)" />
      </div>
    </div>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import type { PaymentOrder } from '@/types/payment'
import { statusBadgeClass, canRefund as canRefundStatus, formatOrderDateTime } from '@/components/payment/orderUtils'
import { currencySymbol } from '@/components/payment/currency'

const { t } = useI18n()

const props = defineProps<{
  show: boolean
  order: PaymentOrder | null
}>()

const creditedAmountSymbol = currencySymbol('USD')

const paymentAmountSymbol = computed(() => currencySymbol(props.order?.currency))

/** 充值金额 (base amount before fee) = pay_amount - fee = pay_amount / (1 + fee_rate/100) */
const baseAmount = computed(() => {
  if (!props.order) return 0
  const feeRate = Number(props.order.fee_rate) || 0
  if (feeRate <= 0) return props.order.pay_amount
  return props.order.pay_amount / (1 + feeRate / 100)
})

/** 手续费 = pay_amount - baseAmount */
const feeAmount = computed(() => {
  if (!props.order) return 0
  const feeRate = Number(props.order.fee_rate) || 0
  if (feeRate <= 0) return 0
  return props.order.pay_amount - baseAmount.value
})

const emit = defineEmits<{
  (e: 'close'): void
  (e: 'cancel', order: PaymentOrder): void
  (e: 'retry', order: PaymentOrder): void
  (e: 'refund', order: PaymentOrder): void
}>()

function canRefund(order: PaymentOrder): boolean {
  return canRefundStatus(order.status)
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
