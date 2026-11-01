<template>
  <!-- Stats row like the dashboard template (HomeStats): UPageGrid of subtle UPageCards -->
  <UPageGrid class="gap-4 sm:gap-6 lg:grid-cols-4 lg:gap-px">
    <UPageCard
      v-for="card in cards"
      :key="card.key"
      :icon="card.icon"
      :title="card.title"
      variant="subtle"
      :ui="{
        container: 'gap-y-1.5',
        wrapper: 'items-start',
        leading: 'p-2.5 rounded-full bg-primary/10 ring ring-inset ring-primary/25',
        title: 'font-normal text-muted text-xs uppercase'
      }"
      class="first:rounded-l-lg last:rounded-r-lg lg:rounded-none"
    >
      <div>
        <template v-if="card.amounts">
          <p v-for="[currency, amount] in sortedAmounts(card.amounts)" :key="currency" class="text-2xl font-semibold tabular-nums text-highlighted">
            {{ formatMoney(currency, amount) }}
          </p>
        </template>
        <p v-else class="text-2xl font-semibold tabular-nums text-highlighted">{{ card.value }}</p>
        <p v-if="card.hint" class="mt-0.5 text-xs text-muted">{{ card.hint }}</p>
      </div>
    </UPageCard>
  </UPageGrid>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { CurrencyAmounts, DashboardStats } from '@/types/payment'

const { t } = useI18n()

const props = defineProps<{
  stats: DashboardStats
}>()

interface StatCard {
  key: string
  icon: string
  title: string
  amounts?: CurrencyAmounts
  value?: number
  hint?: string
}

const cards = computed<StatCard[]>(() => [
  {
    key: 'today-revenue',
    icon: 'i-lucide-circle-dollar-sign',
    title: t('payment.admin.todayRevenue'),
    amounts: props.stats.today_amount,
    hint: `${props.stats.today_count} ${t('payment.admin.orders')}`
  },
  {
    key: 'total-revenue',
    icon: 'i-lucide-credit-card',
    title: t('payment.admin.totalRevenue'),
    amounts: props.stats.total_amount,
    hint: `${props.stats.total_count} ${t('payment.admin.orders')}`
  },
  { key: 'today-orders', icon: 'i-lucide-shopping-bag', title: t('payment.admin.todayOrders'), value: props.stats.today_count },
  { key: 'avg-amount', icon: 'i-lucide-calculator', title: t('payment.admin.avgAmount'), amounts: props.stats.avg_amount }
])

function sortedAmounts(amounts: CurrencyAmounts): [string, number][] {
  return Object.entries(amounts).sort(([left], [right]) => left.localeCompare(right))
}

function formatMoney(currency: string, amount: number): string {
  return new Intl.NumberFormat(undefined, { style: 'currency', currency }).format(amount)
}
</script>
