<template>
  <BaseDialog
    :show="show"
    :title="t('payment.admin.refundOrder')"
    width="normal"
    @close="emit('cancel')"
  >
    <UForm id="refund-form" :state="form" class="space-y-4" @submit="handleSubmit">
      <!-- Refund request info -->
      <UAlert
        v-if="order?.refund_requested_at || order?.refund_request_reason"
        color="info"
        variant="subtle"
        icon="i-lucide-info"
        :title="t('payment.admin.refundRequestInfo')"
      >
        <template #description>
          <p v-if="order?.refund_requested_at" class="flex justify-between gap-2">
            <span>{{ t('payment.admin.refundRequestedAt') }}</span>
            <span>{{ formatDateTime(order.refund_requested_at) }}</span>
          </p>
          <p v-if="order?.refund_request_reason" class="mt-1">{{ t('payment.admin.refundRequestReason') }}: {{ order.refund_request_reason }}</p>
        </template>
      </UAlert>

      <!-- Order info -->
      <dl class="space-y-1 text-sm">
        <div class="flex justify-between gap-2">
          <dt class="text-muted">{{ t('payment.orders.orderId') }}</dt>
          <dd class="font-mono text-highlighted">#{{ order?.id }}</dd>
        </div>
        <div class="flex justify-between gap-2">
          <dt class="text-muted">{{ t('payment.orders.creditedAmount') }}</dt>
          <dd class="font-medium tabular-nums text-highlighted">{{ creditedAmountSymbol }}{{ order?.amount?.toFixed(2) }}</dd>
        </div>
        <div class="flex justify-between gap-2">
          <dt class="text-muted">{{ t('payment.orders.payAmount') }}</dt>
          <dd class="font-medium tabular-nums text-highlighted">{{ paymentAmountSymbol }}{{ order?.pay_amount?.toFixed(2) }}</dd>
        </div>
        <div v-if="actuallyRefunded > 0" class="flex justify-between gap-2">
          <dt class="text-muted">{{ t('payment.admin.alreadyRefunded') }}</dt>
          <dd class="font-medium tabular-nums text-error">{{ creditedAmountSymbol }}{{ actuallyRefunded.toFixed(2) }}</dd>
        </div>
      </dl>

      <USeparator />

      <!-- Deduct balance -->
      <div class="space-y-2">
        <UCheckbox
          id="deduct-balance"
          v-model="form.deduct_balance"
          :label="t('payment.admin.deductBalance')"
          :description="t('payment.admin.deductBalanceHint')"
        />
        <dl v-if="form.deduct_balance && userBalance != null" class="grid grid-cols-2 gap-3 text-sm">
          <div>
            <dt class="text-muted">{{ t('payment.admin.userBalance') }}</dt>
            <dd class="mt-0.5 font-semibold tabular-nums text-highlighted">{{ creditedAmountSymbol }}{{ userBalance.toFixed(2) }}</dd>
          </div>
          <div>
            <dt class="text-muted">{{ t('payment.admin.orderAmount') }}</dt>
            <dd class="mt-0.5 font-semibold tabular-nums text-highlighted">{{ creditedAmountSymbol }}{{ order?.amount?.toFixed(2) }}</dd>
          </div>
        </dl>
        <UAlert
          v-if="form.deduct_balance && balanceInsufficient"
          color="warning"
          variant="subtle"
          icon="i-lucide-triangle-alert"
          :description="t('payment.admin.insufficientBalance')"
        />
        <UAlert v-if="!form.deduct_balance" color="info" variant="subtle" icon="i-lucide-info" :description="t('payment.admin.noDeduction')" />
      </div>

      <UFormField
        :label="t('payment.admin.refundAmount')"
        :help="`${t('payment.admin.maxRefundable')}: ${creditedAmountSymbol}${maxRefundable.toFixed(2)}`"
        required
      >
        <UInput v-model.number="form.amount" type="number" step="0.01" min="0.01" :max="maxRefundable" required class="w-full">
          <template #leading><span class="text-muted">{{ creditedAmountSymbol }}</span></template>
        </UInput>
      </UFormField>

      <UFormField :label="t('payment.admin.refundReason')" required>
        <UTextarea v-model="form.reason" :rows="3" required class="w-full" :placeholder="t('payment.admin.refundReasonPlaceholder')" />
      </UFormField>

      <UAlert v-if="warning" color="warning" variant="subtle" icon="i-lucide-triangle-alert" :description="warning" />

      <UCheckbox v-if="requireForce" id="force-refund" v-model="form.force" color="error" :label="t('payment.admin.forceRefund')" />
    </UForm>

    <template #footer>
      <div class="flex justify-end gap-2">
        <UButton color="neutral" variant="subtle" :label="t('common.cancel')" @click="emit('cancel')" />
        <UButton
          type="submit"
          form="refund-form"
          color="error"
          :loading="submitting"
          :disabled="submitting || form.amount <= 0 || (requireForce && !form.force)"
          :label="submitting ? t('common.processing') : t('payment.admin.confirmRefund')"
        />
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { reactive, computed, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import type { PaymentOrder } from '@/types/payment'
import { formatOrderDateTime } from '@/components/payment/orderUtils'
import { currencySymbol } from '@/components/payment/currency'

const { t } = useI18n()

const props = defineProps<{
  show: boolean
  order: PaymentOrder | null
  submitting?: boolean
  userBalance?: number | null
  requireForce?: boolean
  warning?: string
}>()

const emit = defineEmits<{
  (e: 'confirm', data: { amount: number; reason: string; deduct_balance: boolean; force: boolean }): void
  (e: 'cancel'): void
}>()

const creditedAmountSymbol = currencySymbol('USD')

const paymentAmountSymbol = computed(() => currencySymbol(props.order?.currency))

const form = reactive({
  amount: 0,
  reason: '',
  deduct_balance: true,
  force: false,
})

// In REFUND_REQUESTED / REFUND_PENDING status, refund_amount is requested/pending, not actually refunded.
// Only PARTIALLY_REFUNDED / REFUNDED have real refund amounts.
const actuallyRefunded = computed(() => {
  if (!props.order) return 0
  const s = props.order.status
  if (s === 'PARTIALLY_REFUNDED' || s === 'REFUNDED') return props.order.refund_amount || 0
  return 0
})

const maxRefundable = computed(() => {
  if (!props.order) return 0
  return props.order.amount - actuallyRefunded.value
})

const balanceInsufficient = computed(() => {
  if (props.userBalance == null || !props.order) return false
  return props.userBalance < form.amount
})

watch(() => props.show, (val) => {
  if (val && props.order) {
    // For REFUND_REQUESTED, pre-fill with the requested amount
    if (props.order.status === 'REFUND_REQUESTED' && props.order.refund_amount) {
      form.amount = props.order.refund_amount
    } else {
      form.amount = maxRefundable.value
    }
    form.reason = props.order.refund_request_reason || ''
    form.deduct_balance = true
    form.force = false
  }
})

function formatDateTime(dateStr: string): string {
  return formatOrderDateTime(dateStr)
}

function handleSubmit() {
  if (form.amount <= 0 || form.amount > maxRefundable.value) return
  if (props.requireForce && !form.force) return
  emit('confirm', { ...form })
}
</script>
