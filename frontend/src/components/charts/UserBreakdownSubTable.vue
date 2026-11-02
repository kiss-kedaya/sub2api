<template>
  <div class="bg-elevated/40">
    <div v-if="loading" class="flex items-center justify-center py-3">
      <LoadingSpinner />
    </div>
    <div v-else-if="items.length === 0" class="py-2 text-center text-xs text-dimmed">
      {{ t('admin.dashboard.noDataAvailable') }}
    </div>
    <UTable
      v-else
      :data="items"
      :columns="columns"
      :get-row-id="userRowId"
      :ui="{
        base: 'text-xs',
        thead: 'hidden',
        separator: 'hidden',
        td: 'px-2 py-1 text-xs whitespace-nowrap first:pl-6 last:pr-1',
        tbody: 'divide-y divide-(--ui-border-muted)'
      }"
    />
  </div>
</template>

<script setup lang="ts">
import { computed, h } from 'vue'
import { useI18n } from 'vue-i18n'
import type { TableColumn } from '@nuxt/ui'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import type { UserBreakdownItem } from '@/types'

const { t } = useI18n()

const props = withDefaults(defineProps<{
  items: UserBreakdownItem[]
  loading?: boolean
  showAccountCost?: boolean
}>(), {
  loading: false,
  showAccountCost: true,
})

const showAccountCost = computed(() => props.showAccountCost)

const numberCell = (value: string, tone = 'text-muted') => h('span', { class: `block text-right tabular-nums ${tone}` }, value)
const userRowId = (user: UserBreakdownItem) => String(user.user_id)

// Same column order as the parent distribution table (no header row).
const columns = computed<TableColumn<UserBreakdownItem>[]>(() => [
  {
    id: 'user',
    cell: ({ row }) => h('span', { class: 'block max-w-[120px] truncate text-default', title: row.original.email }, row.original.email || `User #${row.original.user_id}`)
  },
  { id: 'requests', cell: ({ row }) => numberCell(row.original.requests.toLocaleString()) },
  { id: 'tokens', cell: ({ row }) => numberCell(formatTokens(row.original.total_tokens)) },
  { id: 'actual', cell: ({ row }) => numberCell(`$${formatCost(row.original.actual_cost)}`, 'text-success') },
  ...(showAccountCost.value
    ? [{ id: 'account', cell: ({ row }: { row: { original: UserBreakdownItem } }) => numberCell(`$${formatCost(row.original.account_cost)}`, 'text-warning') }]
    : []),
  { id: 'standard', cell: ({ row }) => numberCell(`$${formatCost(row.original.cost)}`, 'text-dimmed') }
])

const formatTokens = (value: number): string => {
  if (value >= 1_000_000_000) return `${(value / 1_000_000_000).toFixed(2)}B`
  if (value >= 1_000_000) return `${(value / 1_000_000).toFixed(2)}M`
  if (value >= 1_000) return `${(value / 1_000).toFixed(2)}K`
  return value.toLocaleString()
}

const formatCost = (value: number | undefined | null): string => {
  if (value == null) return '0.0000'
  if (value >= 1000) return (value / 1000).toFixed(2) + 'K'
  if (value >= 1) return value.toFixed(2)
  if (value >= 0.01) return value.toFixed(3)
  return value.toFixed(4)
}
</script>
