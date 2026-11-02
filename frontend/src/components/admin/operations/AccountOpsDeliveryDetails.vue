<template>
  <ul v-if="deliveries && Object.keys(deliveries).length" class="flex gap-6 text-xs" data-testid="delivery-details-list">
    <li v-for="(delivery, key) in deliveries" :key="key" class="shrink-0 space-y-1">
      <div class="flex items-center gap-3">
        <span class="max-w-48 truncate font-medium text-highlighted" :title="delivery.name || t(`accountOps.providers.${delivery.provider}`)">
          {{ delivery.name || t(`accountOps.providers.${delivery.provider}`) }}
        </span>
        <span class="whitespace-nowrap" :class="delivery.status === 'sent' ? 'text-success' : 'text-error'">
          {{ t(`accountOps.states.${delivery.status}`) }}
        </span>
      </div>
      <p class="whitespace-nowrap text-xs text-muted">
        <span v-if="delivery.name">{{ t(`accountOps.providers.${delivery.provider}`) }} · </span>
        {{ t('accountOps.deliveryAttempts', { count: delivery.attempts }) }}
        <template v-if="delivery.last_sent_at"> · {{ date(delivery.last_sent_at) }}</template>
      </p>
    </li>
  </ul>
  <span v-else class="text-xs text-dimmed">—</span>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { accountOpsMessages } from './accountOpsMessages'
import type { AccountOpsEvent } from '@/api/admin/accountOps'

defineProps<{ deliveries?: AccountOpsEvent['deliveries'] }>()
const { t } = useI18n({ useScope: 'local', messages: accountOpsMessages })
const date = (value: string) => {
  const parsed = new Date(value)
  return Number.isFinite(parsed.getTime()) ? parsed.toLocaleString() : '-'
}
</script>
