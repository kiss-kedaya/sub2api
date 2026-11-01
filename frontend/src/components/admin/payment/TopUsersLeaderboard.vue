<template>
  <UCard :ui="{ header: 'p-4 sm:px-6', body: 'p-4 sm:p-6' }">
    <template #header>
      <p class="text-sm font-semibold text-highlighted">{{ t('payment.admin.topUsers') }}</p>
    </template>
    <div
      v-if="!hasUsers(props.users)"
      class="flex h-32 items-center justify-center text-sm text-muted"
    >
      {{ t('payment.admin.noData') }}
    </div>
    <div v-else class="space-y-2">
      <div v-for="[currency, currencyUsers] in sortedUsers(props.users)" :key="currency" class="space-y-2">
        <p class="text-xs font-medium text-muted">{{ currency }}</p>
        <div
          v-for="(user, idx) in currencyUsers"
          :key="user.user_id"
          class="flex items-center justify-between gap-3 py-1.5"
        >
          <div class="flex items-center gap-3">
            <UBadge
              :color="idx === 0 ? 'primary' : 'neutral'"
              :variant="idx < 3 ? 'subtle' : 'outline'"
              size="sm"
              class="w-6 justify-center tabular-nums"
              :label="String(idx + 1)"
            />
            <span class="truncate text-sm text-default">{{ user.email }}</span>
          </div>
          <span class="text-sm font-medium tabular-nums text-highlighted">
            {{ formatMoney(currency, user.amount) }}
          </span>
        </div>
      </div>
    </div>
  </UCard>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { TopUserPaymentStats } from '@/types/payment'

const { t } = useI18n()

const props = defineProps<{
  users: Record<string, TopUserPaymentStats[]>
}>()

function hasUsers(usersByCurrency: Record<string, TopUserPaymentStats[]>): boolean {
  return Object.values(usersByCurrency).some(users => users.length > 0)
}

function sortedUsers(usersByCurrency: Record<string, TopUserPaymentStats[]>): [string, TopUserPaymentStats[]][] {
  return Object.entries(usersByCurrency).sort(([left], [right]) => left.localeCompare(right))
}

function formatMoney(currency: string, amount: number): string {
  return new Intl.NumberFormat(undefined, { style: 'currency', currency }).format(amount)
}
</script>
