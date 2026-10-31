<template>
  <section class="signal-recent min-w-0 pt-4">
    <header class="mb-3 flex items-baseline justify-between gap-3">
      <h2 class="text-sm font-semibold text-highlighted">{{ t('dashboard.recentUsage') }}</h2>
      <span v-if="startDate && endDate" class="text-xs text-muted tabular-nums">{{ startDate }} / {{ endDate }}</span>
    </header>
    <div v-if="loading" class="flex justify-center py-12"><LoadingSpinner size="lg" /></div>
    <div v-else-if="data.length === 0" class="py-6">
      <EmptyState :title="t('dashboard.noUsageRecords')" :description="t('dashboard.startUsingApi')" />
    </div>
    <template v-else>
      <ol class="divide-y divide-default border-y border-default">
        <li v-for="log in data" :key="log.id" class="signal-request">
          <div class="signal-request-model">
            <UIcon name="i-lucide-flask-conical" class="signal-request-icon size-5 shrink-0 text-muted" />
            <div class="min-w-0">
              <p class="text-[13px] font-medium text-highlighted break-all">{{ log.model }}</p>
              <time class="mt-1 block text-[11px] text-muted" :datetime="log.created_at">{{ formatDateTime(log.created_at) }}</time>
            </div>
          </div>
          <div class="signal-request-tokens">
            <span class="text-highlighted">{{ (log.input_tokens + log.output_tokens + (log.cache_read_tokens || 0) + (log.cache_creation_tokens || 0)).toLocaleString() }}</span>
            <span class="signal-request-label">tokens</span>
          </div>
          <div class="signal-request-cost">
            <div><span class="signal-request-label">{{ t('dashboard.actual') }}</span><span class="signal-actual">${{ formatCost(log.actual_cost) }}</span></div>
            <div><span class="signal-request-label">{{ t('dashboard.standard') }}</span><span class="signal-standard">${{ formatCost(log.total_cost) }}</span></div>
          </div>
        </li>
      </ol>
      <div class="flex justify-end pt-2">
        <router-link to="/usage" class="signal-view-all">{{ t('dashboard.viewAllUsage') }}<UIcon name="i-lucide-arrow-right" class="size-4" /></router-link>
      </div>
    </template>
  </section>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import { formatDateTime } from '@/utils/format'
import type { UsageLog } from '@/types'

defineProps<{
  data: UsageLog[]
  loading: boolean
  startDate?: string
  endDate?: string
}>()
const { t } = useI18n()
const formatCost = (c: number) => c.toFixed(4)
</script>

<style scoped>
.signal-request { display: grid; grid-template-columns: minmax(0, 1.6fr) minmax(100px, .5fr) minmax(180px, .65fr); gap: 20px; align-items: center; padding: 12px; font-variant-numeric: tabular-nums; transition: background-color 150ms ease; }
.signal-request:hover { background: color-mix(in srgb, var(--ui-bg-elevated) 50%, transparent); }
.signal-request-model { display: flex; align-items: center; gap: 12px; min-width: 0; }
.signal-request-tokens { display: flex; align-items: baseline; justify-content: flex-end; flex-wrap: wrap; gap: 5px; min-width: 0; font-size: 13px; overflow-wrap: anywhere; }
.signal-request-label { font-size: 11px; color: var(--ui-text-muted); }
.signal-request-cost { display: flex; flex-direction: column; gap: 3px; min-width: 0; }
.signal-request-cost > div { display: flex; justify-content: flex-end; align-items: baseline; gap: 12px; font-size: 13px; }
.signal-request-cost > div > span:last-child { min-width: 88px; text-align: right; overflow-wrap: anywhere; }
.signal-actual { color: var(--signal-amber, #906727); font-weight: 600; }
.signal-standard { color: var(--ui-text-muted); }
.signal-view-all { display: inline-flex; align-items: center; gap: 6px; min-height: 36px; padding: 0 8px; border-radius: 6px; font-size: 13px; font-weight: 500; color: var(--ui-text-highlighted); transition: background-color 150ms ease; }
.signal-view-all:hover { background: var(--ui-bg-elevated); }
.signal-view-all:focus-visible { outline: 2px solid var(--ui-border-inverted); outline-offset: 2px; }
@media (max-width: 640px) {
  .signal-request { grid-template-columns: minmax(0, 1fr) minmax(0, 1fr); gap: 8px 12px; padding: 12px 0; }
  .signal-request-model { grid-column: 1 / -1; align-items: flex-start; }
  .signal-request-tokens { justify-content: flex-start; padding-left: 32px; }
  .signal-request-cost > div { gap: 6px; }
  .signal-request-cost > div > span:last-child { min-width: 0; }
}
@media (prefers-reduced-motion: reduce) { .signal-request, .signal-view-all { transition: none; } }
</style>
