<template>
  <UBadge
    class="payment-order-status"
    :data-status="status"
    :color="statusColor"
    variant="subtle"
  >
    {{ statusLabel }}
  </UBadge>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { OrderStatus } from '@/types/payment'

const props = defineProps<{
  status: OrderStatus
}>()

const { t } = useI18n()

type BadgeColor = 'warning' | 'info' | 'success' | 'neutral' | 'error' | 'primary'

// Same groups as before: pending = warning, paid / recharging = info, completed = success,
// expired / cancelled = neutral, failures = error, refund flow = warning, refunded = primary.
const statusMap: Record<OrderStatus, { key: string; color: BadgeColor }> = {
  PENDING: { key: 'payment.status.pending', color: 'warning' },
  PAID: { key: 'payment.status.paid', color: 'info' },
  RECHARGING: { key: 'payment.status.recharging', color: 'info' },
  COMPLETED: { key: 'payment.status.completed', color: 'success' },
  EXPIRED: { key: 'payment.status.expired', color: 'neutral' },
  CANCELLED: { key: 'payment.status.cancelled', color: 'neutral' },
  FAILED: { key: 'payment.status.failed', color: 'error' },
  REFUND_REQUESTED: { key: 'payment.status.refund_requested', color: 'warning' },
  REFUNDING: { key: 'payment.status.refunding', color: 'warning' },
  REFUND_PENDING: { key: 'payment.status.refund_pending', color: 'warning' },
  REFUNDED: { key: 'payment.status.refunded', color: 'primary' },
  PARTIALLY_REFUNDED: { key: 'payment.status.partially_refunded', color: 'primary' },
  REFUND_FAILED: { key: 'payment.status.refund_failed', color: 'error' },
}

const statusLabel = computed(() => {
  const entry = statusMap[props.status]
  return entry ? t(entry.key) : props.status
})

const statusColor = computed<BadgeColor>(() => statusMap[props.status]?.color ?? 'neutral')
</script>
