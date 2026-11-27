<template>
  <div
    class="relative z-10 grid grid-cols-2 gap-4 transition-opacity lg:grid-cols-4"
    :class="{ 'opacity-60': refreshing }"
    :aria-busy="loading || undefined"
  >
    <div class="card p-4 flex items-center gap-3">
      <div class="usage-stat-icon flex size-10 shrink-0 items-center justify-center rounded-full bg-elevated/90 text-highlighted ring-1 ring-inset ring-default">
        <Icon name="document" size="md" />
      </div>
      <div class="min-w-0">
        <p class="text-xs font-medium text-muted">{{ t('usage.totalRequests') }}</p>
        <p class="text-xl font-bold text-highlighted tabular-nums">
          <span v-if="pending" class="inline-block rounded-sm bg-elevated align-middle motion-safe:animate-pulse h-5 w-16" aria-hidden="true" />
          <template v-else>{{ stats?.total_requests?.toLocaleString() || '0' }}</template>
        </p>
        <p class="text-xs text-muted">{{ t('usage.inSelectedRange') }}</p>
      </div>
    </div>
    <div class="card p-4 flex items-center gap-3">
      <div class="usage-stat-icon flex size-10 shrink-0 items-center justify-center rounded-full bg-elevated/90 text-highlighted ring-1 ring-inset ring-default"><svg class="h-5 w-5" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="m21 7.5-9-5.25L3 7.5m18 0-9 5.25m9-5.25v9l-9 5.25M3 7.5l9 5.25M3 7.5v9l9 5.25m0-9v9" /></svg></div>
      <div class="min-w-0">
        <p class="text-xs font-medium text-muted">{{ t('usage.totalTokens') }}</p>
        <p class="text-xl font-bold text-highlighted tabular-nums">
          <span v-if="pending" class="inline-block rounded-sm bg-elevated align-middle motion-safe:animate-pulse h-5 w-16" aria-hidden="true" />
          <template v-else>{{ formatTokens(stats?.total_tokens || 0) }}</template>
        </p>
        <p v-if="pending" class="text-xs"><span class="inline-block rounded-sm bg-elevated align-middle motion-safe:animate-pulse h-3 w-36" aria-hidden="true" /></p>
        <p v-else class="flex flex-wrap items-center gap-x-1 text-xs text-muted tabular-nums">
          <span>{{ t('usage.in') }}: {{ formatTokens(stats?.total_input_tokens || 0) }}</span>
          <span>/</span>
          <span>{{ t('usage.out') }}: {{ formatTokens(stats?.total_output_tokens || 0) }}</span>
          <span>/</span>
          <span class="group relative inline-flex cursor-help items-center gap-0.5" tabindex="0">
            <span>{{ cacheLabel() }}: {{ formatTokens(stats?.total_cache_tokens || 0) }}</span>
            <svg
              class="h-3.5 w-3.5 text-dimmed"
              fill="none"
              stroke="currentColor"
              viewBox="0 0 24 24"
            >
              <path
                stroke-linecap="round"
                stroke-linejoin="round"
                stroke-width="2"
                d="M13 16h-1v-4h-1m1-4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z"
              />
            </svg>
            <!--
              Aligned to the trigger's right edge, not centred on it: the trigger is the last item
              of the meta line, so its right edge can never pass the card/strip boundary. Centring
              pushed this 14rem panel outside the stats strip (768px: 18.7px, 390px: 15.1px), which
              also meant a horizontal overflow inside the layout. No ancestor clips it any more,
              and `pointer-events-none` keeps it from stealing hover from the trigger.
            -->
            <span
              class="pointer-events-none absolute right-0 top-full z-50 mt-2 hidden w-56 rounded-lg border border-default bg-default p-3 text-left text-xs text-default shadow-lg group-hover:block group-focus:block group-focus-within:block"
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
      </div>
    </div>
    <div class="card p-4 flex items-center gap-3">
      <div class="usage-stat-icon flex size-10 shrink-0 items-center justify-center rounded-full bg-elevated/90 text-highlighted ring-1 ring-inset ring-default">
        <Icon name="dollar" size="md" />
      </div>
      <div class="min-w-0 flex-1">
        <p class="text-xs font-medium text-muted">{{ t('usage.totalCost') }}</p>
        <p class="text-xl font-bold tabular-nums text-emerald-700 dark:text-emerald-400">
          <span v-if="pending" class="inline-block rounded-sm bg-elevated align-middle motion-safe:animate-pulse h-5 w-20" aria-hidden="true" />
          <template v-else>${{ (stats?.total_actual_cost || 0).toFixed(4) }}</template>
        </p>
        <p v-if="pending" class="text-xs"><span class="inline-block rounded-sm bg-elevated align-middle motion-safe:animate-pulse h-3 w-24" aria-hidden="true" /></p>
        <p v-else class="text-xs text-muted tabular-nums">
          <template v-if="showAccountCost && totalAccountCost != null">
            <span class="text-amber-700 dark:text-amber-400">{{ t('usage.accountCost') }} ${{ totalAccountCost.toFixed(4) }}</span>
            <span> · </span>
          </template>
          <span>
            {{ t('usage.standardCost') }}
            <span :class="{ 'line-through': strikeStandardCost }">${{ (stats?.total_cost || 0).toFixed(4) }}</span>
          </span>
        </p>
      </div>
    </div>
    <div class="card p-4 flex items-center gap-3">
      <div class="usage-stat-icon flex size-10 shrink-0 items-center justify-center rounded-full bg-elevated/90 text-highlighted ring-1 ring-inset ring-default">
        <Icon name="clock" size="md" />
      </div>
      <div class="min-w-0">
        <p class="text-xs font-medium text-muted">{{ t('usage.avgDuration') }}</p>
        <p class="text-xl font-bold text-highlighted tabular-nums">
          <span v-if="pending" class="inline-block rounded-sm bg-elevated align-middle motion-safe:animate-pulse h-5 w-14" aria-hidden="true" />
          <template v-else>{{ formatDuration(stats?.average_duration_ms || 0) }}</template>
        </p>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { AdminUsageStatsResponse } from '@/api/admin/usage'
import type { UsageStatsResponse } from '@/types'
import Icon from '@/components/icons/Icon.vue'

const props = withDefaults(defineProps<{
  stats: (AdminUsageStatsResponse | UsageStatsResponse) | null
  showAccountCost?: boolean
  strikeStandardCost?: boolean
  // While the totals are on their way: placeholders on the first load (no number to show yet, and a 0 would be
  // a wrong one), dimmed numbers on a refresh.
  loading?: boolean
}>(), {
  showAccountCost: true,
  strikeStandardCost: false,
  loading: false,
})

const { t } = useI18n()

const totalAccountCost = computed(() => {
  const stats = props.stats as (AdminUsageStatsResponse & { total_account_cost?: number }) | null
  return stats?.total_account_cost ?? null
})
const pending = computed(() => props.loading && !props.stats)
const refreshing = computed(() => props.loading && !!props.stats)
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
