<template>
  <UPageGrid class="grid-cols-2 gap-4 sm:gap-6 lg:grid-cols-4 lg:gap-px">
    <UPageCard icon="i-lucide-file-text" :title="t('usage.totalRequests')" variant="subtle" :ui="statCardUi" :class="statCardClass">
      <span class="text-2xl font-semibold tabular-nums text-highlighted">{{ stats?.total_requests?.toLocaleString() || '0' }}</span>
      <p class="text-xs text-muted">{{ t('usage.inSelectedRange') }}</p>
    </UPageCard>
    <UPageCard icon="i-lucide-box" :title="t('usage.totalTokens')" variant="subtle" :ui="statCardUi" :class="statCardClass">
      <span class="text-2xl font-semibold tabular-nums text-highlighted">{{ formatTokens(stats?.total_tokens || 0) }}</span>
      <p class="flex flex-wrap items-center gap-x-1 text-xs text-muted">
        <span>{{ t('usage.in') }}: {{ formatTokens(stats?.total_input_tokens || 0) }}</span>
        <span>/</span>
        <span>{{ t('usage.out') }}: {{ formatTokens(stats?.total_output_tokens || 0) }}</span>
        <span>/</span>
        <span class="group relative inline-flex cursor-help items-center gap-0.5" tabindex="0">
          <span>{{ cacheLabel() }}: {{ formatTokens(stats?.total_cache_tokens || 0) }}</span>
          <UIcon name="i-lucide-info" class="size-3.5 text-dimmed" />
          <span
            class="pointer-events-none absolute left-1/2 top-full z-30 mt-2 hidden w-56 -translate-x-1/2 rounded-md bg-default p-3 text-left text-xs text-default shadow-lg ring ring-default group-hover:block group-focus:block"
          >
            <span class="mb-2 block font-medium text-highlighted">
              {{ cacheDetailLabel() }}
            </span>
            <span class="flex items-center justify-between gap-3">
              <span>{{ t('usage.cacheCreationTokensLabel') }}</span>
              <span class="tabular-nums">
                {{ formatTokens(stats?.total_cache_creation_tokens || 0) }}
              </span>
            </span>
            <span class="mt-1 flex items-center justify-between gap-3">
              <span>{{ t('usage.cacheReadTokensLabel') }}</span>
              <span class="tabular-nums">
                {{ formatTokens(stats?.total_cache_read_tokens || 0) }}
              </span>
            </span>
          </span>
        </span>
      </p>
    </UPageCard>
    <UPageCard icon="i-lucide-circle-dollar-sign" :title="t('usage.totalCost')" variant="subtle" :ui="statCardUi" :class="statCardClass">
      <span class="text-2xl font-semibold tabular-nums text-highlighted">${{ (stats?.total_actual_cost || 0).toFixed(4) }}</span>
      <p class="text-xs text-muted">
        <template v-if="showAccountCost && totalAccountCost != null">
          <span>{{ t('usage.accountCost') }} ${{ totalAccountCost.toFixed(4) }}</span>
          <span> · </span>
        </template>
        <span>
          {{ t('usage.standardCost') }}
          <span :class="{ 'line-through': strikeStandardCost }">${{ (stats?.total_cost || 0).toFixed(4) }}</span>
        </span>
      </p>
    </UPageCard>
    <UPageCard icon="i-lucide-clock" :title="t('usage.avgDuration')" variant="subtle" :ui="statCardUi" :class="statCardClass">
      <span class="text-2xl font-semibold tabular-nums text-highlighted">{{ formatDuration(stats?.average_duration_ms || 0) }}</span>
    </UPageCard>
  </UPageGrid>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { AdminUsageStatsResponse } from '@/api/admin/usage'
import type { UsageStatsResponse } from '@/types'
import { statCardClass, statCardUi } from '@/views/admin/ops/utils/templateUi'

const props = withDefaults(defineProps<{
  stats: (AdminUsageStatsResponse | UsageStatsResponse) | null
  showAccountCost?: boolean
  strikeStandardCost?: boolean
}>(), {
  showAccountCost: true,
  strikeStandardCost: false,
})

const { t } = useI18n()

const totalAccountCost = computed(() => {
  const stats = props.stats as (AdminUsageStatsResponse & { total_account_cost?: number }) | null
  return stats?.total_account_cost ?? null
})
const showAccountCost = computed(() => props.showAccountCost)
const strikeStandardCost = computed(() => props.strikeStandardCost)

const formatDuration = (ms: number) =>
  ms < 1000 ? `${ms.toFixed(0)}ms` : `${(ms / 1000).toFixed(2)}s`

const formatTokens = (value: number) => {
  if (value >= 1e9) return (value / 1e9).toFixed(2) + 'B'
  if (value >= 1e6) return (value / 1e6).toFixed(2) + 'M'
  if (value >= 1e3) return (value / 1e3).toFixed(2) + 'K'
  return value.toLocaleString()
}

const cacheLabel = () => t('usage.cacheTotal')
const cacheDetailLabel = () => t('usage.cacheBreakdown')
</script>
