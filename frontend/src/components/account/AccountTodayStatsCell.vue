<template>
  <!-- inline: one wrapped line for the usage-window cell; renders nothing when there is nothing to show. -->
  <template v-if="inline">
    <div v-if="stats" class="mb-0.5 flex flex-wrap items-center gap-x-2 text-xs tabular-nums text-muted">
      <span>{{ requests }} req</span>
      <span>{{ tokens }}</span>
      <span :title="t('usage.accountBilled')">A ${{ cost.toFixed(2) }}</span>
      <span v-if="stats.user_cost != null" :title="t('usage.userBilled')">U ${{ stats.user_cost.toFixed(2) }}</span>
    </div>
    <div v-else-if="loading" class="mb-0.5 flex items-center gap-1">
      <USkeleton class="h-3 w-10" />
      <USkeleton class="h-3 w-8" />
      <USkeleton class="h-3 w-12" />
    </div>
  </template>

  <!-- list: the dedicated "today" column and the detail drawer. -->
  <div v-else>
    <div v-if="loading && !stats" class="space-y-1">
      <USkeleton class="h-3 w-12" />
      <USkeleton class="h-3 w-16" />
      <USkeleton class="h-3 w-10" />
    </div>
    <div v-else-if="error && !stats" class="text-xs text-error">{{ error }}</div>
    <div v-else-if="stats" class="space-y-0.5 text-xs tabular-nums">
      <div v-for="row in rows" :key="row.label" class="flex items-center gap-1">
        <span class="text-muted">{{ row.label }}:</span>
        <span class="text-highlighted">{{ row.value }}</span>
      </div>
    </div>
    <div v-else class="text-xs text-dimmed">-</div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { WindowStats } from '@/types'
import { formatCompactNumber, formatCurrency } from '@/utils/format'

const props = withDefaults(
  defineProps<{
    stats?: WindowStats | null
    loading?: boolean
    error?: string | null
    inline?: boolean
  }>(),
  { stats: null, loading: false, error: null, inline: false }
)

const { t } = useI18n()

// One formatter for both layouts, so the same number never reads differently in two columns.
const requests = computed(() => formatCompactNumber(props.stats?.requests, { allowBillions: false }))
const tokens = computed(() => formatCompactNumber(props.stats?.tokens))
const cost = computed(() => props.stats?.cost ?? 0)

const rows = computed(() => {
  const list = [
    { label: t('admin.accounts.stats.requests'), value: requests.value },
    { label: t('admin.accounts.stats.tokens'), value: tokens.value },
    { label: t('usage.accountBilled'), value: formatCurrency(cost.value) }
  ]
  if (props.stats?.user_cost != null) list.push({ label: t('usage.userBilled'), value: formatCurrency(props.stats.user_cost) })
  return list
})
</script>
