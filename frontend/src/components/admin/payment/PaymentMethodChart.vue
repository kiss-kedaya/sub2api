<template>
  <UCard :ui="{ header: 'p-4 sm:px-6', body: 'p-4 sm:p-6' }">
    <template #header>
      <p class="text-sm font-semibold text-highlighted">{{ t('payment.admin.paymentDistribution') }}</p>
    </template>
    <div
      v-if="!methods?.length"
      class="flex h-32 items-center justify-center text-sm text-muted"
    >
      {{ t('payment.admin.noData') }}
    </div>
    <div v-else class="space-y-3">
      <div v-for="method in methods" :key="method.type" class="space-y-1">
        <div class="flex items-center justify-between">
          <div class="flex items-center gap-2">
            <span :class="['inline-block h-3 w-3 rounded-full', colorMap[method.type] || 'bg-inverted/40']"></span>
            <span class="text-sm text-default">
              {{ t('payment.methods.' + method.type, method.type) }}
            </span>
          </div>
          <div class="space-y-1 text-right">
            <span v-for="[currency, amount] in sortedAmounts(method.amount)" :key="currency" class="block text-sm font-medium tabular-nums text-highlighted">
              {{ formatMoney(currency, amount) }}
            </span>
            <span class="ml-2 text-xs text-muted">
              ({{ method.count }})
            </span>
          </div>
        </div>
        <div v-for="[currency, amount] in sortedAmounts(method.amount)" :key="currency" class="flex items-center gap-2">
          <span class="w-10 text-xs text-muted">{{ currency }}</span>
          <UProgress
            :model-value="barWidth(currency, amount)"
            :max="100"
            size="sm"
            class="flex-1"
            :ui="{ indicator: barColorMap[method.type] || 'bg-inverted/40' }"
          />
        </div>
      </div>
    </div>
  </UCard>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { CurrencyAmounts, PaymentMethodStats } from '@/types/payment'

const { t } = useI18n()

const props = defineProps<{
  methods: PaymentMethodStats[]
}>()

const colorMap: Record<string, string> = {
  alipay: 'bg-blue-500',
  wxpay: 'bg-green-500',
  alipay_direct: 'bg-blue-400',
  wxpay_direct: 'bg-green-400',
  stripe: 'bg-purple-500',
}

const barColorMap: Record<string, string> = {
  alipay: 'bg-blue-500',
  wxpay: 'bg-green-500',
  alipay_direct: 'bg-blue-400',
  wxpay_direct: 'bg-green-400',
  stripe: 'bg-purple-500',
}

const maxAmounts = computed<CurrencyAmounts>(() => {
  return props.methods.reduce<CurrencyAmounts>((maximums, method) => {
    for (const [currency, amount] of Object.entries(method.amount)) {
      maximums[currency] = Math.max(maximums[currency] || 0, amount)
    }
    return maximums
  }, {})
})

function sortedAmounts(amounts: CurrencyAmounts): [string, number][] {
  return Object.entries(amounts).sort(([left], [right]) => left.localeCompare(right))
}

function barWidth(currency: string, amount: number): number {
  return Math.min((amount / (maxAmounts.value[currency] || 1)) * 100, 100)
}

function formatMoney(currency: string, amount: number): string {
  return new Intl.NumberFormat(undefined, { style: 'currency', currency }).format(amount)
}
</script>
