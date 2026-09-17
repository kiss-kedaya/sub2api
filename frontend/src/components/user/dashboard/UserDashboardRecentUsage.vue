<template>
  <section class="signal-recent">
    <header class="signal-recent-heading">
      <h2>{{ t('dashboard.recentUsage') }}</h2>
      <span>{{ t('dashboard.last7Days') }}</span>
    </header>
    <div v-if="loading" class="signal-recent-loading"><LoadingSpinner size="lg" /></div>
    <div v-else-if="data.length === 0" class="signal-recent-empty">
      <EmptyState :title="t('dashboard.noUsageRecords')" :description="t('dashboard.startUsingApi')" />
    </div>
    <template v-else>
      <ol class="signal-request-list">
        <li v-for="log in data" :key="log.id" class="signal-request">
          <div class="signal-request-model">
            <Icon name="beaker" size="md" />
            <div>
              <p>{{ log.model }}</p>
              <time :datetime="log.created_at">{{ formatDateTime(log.created_at) }}</time>
            </div>
          </div>
          <div class="signal-request-tokens">
            <span>{{ (log.input_tokens + log.output_tokens).toLocaleString() }}</span>
            <span class="signal-request-label">tokens</span>
          </div>
          <div class="signal-request-cost">
            <div><span class="signal-request-label">{{ t('dashboard.actual') }}</span><span class="signal-actual">${{ formatCost(log.actual_cost) }}</span></div>
            <div><span class="signal-request-label">{{ t('dashboard.standard') }}</span><span class="signal-standard">${{ formatCost(log.total_cost) }}</span></div>
          </div>
        </li>
      </ol>
      <router-link to="/usage" class="signal-view-all">{{ t('dashboard.viewAllUsage') }}<Icon name="arrowRight" size="sm" /></router-link>
    </template>
  </section>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import Icon from '@/components/icons/Icon.vue'
import { formatDateTime } from '@/utils/format'
import type { UsageLog } from '@/types'

defineProps<{
  data: UsageLog[]
  loading: boolean
}>()
const { t } = useI18n()
const formatCost = (c: number) => c.toFixed(4)
</script>

<style scoped>
.signal-recent { min-width: 0; padding-top: 20px; }
.signal-recent-heading { display: flex; align-items: baseline; justify-content: space-between; gap: 12px; margin-bottom: 12px; }
.signal-recent-heading h2 { font-size: 14px; font-weight: 600; }
.signal-recent-heading > span { font-size: 12px; color: var(--signal-muted, #65716a); }
.signal-recent-loading { display: flex; justify-content: center; padding: 48px 0; }
.signal-recent-empty { padding: 24px 0; }
.signal-request-list { border-top: 1px solid var(--signal-line, #dce2df); }
.signal-request { display: grid; grid-template-columns: minmax(0, 1.6fr) minmax(100px, .5fr) minmax(180px, .65fr); gap: 20px; align-items: center; padding: 14px 12px; border-bottom: 1px solid var(--signal-line, #dce2df); font-variant-numeric: tabular-nums; }
.signal-request:hover { background: var(--signal-surface, #fff); }
.signal-request-model { display: flex; align-items: center; gap: 12px; min-width: 0; }
.signal-request-model > :deep(svg) { flex-shrink: 0; color: var(--signal-muted, #65716a); }
.signal-request-model > div { min-width: 0; }
.signal-request-model p { font-size: 13px; font-weight: 500; overflow-wrap: anywhere; }
.signal-request-model time { display: block; margin-top: 4px; font-size: 11px; color: var(--signal-muted, #65716a); }
.signal-request-tokens { display: flex; align-items: baseline; justify-content: flex-end; flex-wrap: wrap; gap: 5px; font-size: 13px; overflow-wrap: anywhere; min-width: 0; }
.signal-request-label { font-size: 11px; color: var(--signal-muted, #65716a); }
.signal-request-cost { display: flex; flex-direction: column; gap: 3px; min-width: 0; }
.signal-request-cost > div { display: flex; justify-content: flex-end; align-items: baseline; gap: 12px; font-size: 13px; }
.signal-request-cost > div > span:last-child { min-width: 88px; text-align: right; overflow-wrap: anywhere; }
.signal-actual { color: var(--signal-amber, #99640c); font-weight: 600; }
.signal-standard { color: var(--signal-muted, #65716a); }
.signal-view-all { display: flex; align-items: center; justify-content: flex-end; gap: 8px; min-height: 44px; padding-top: 8px; font-size: 12px; font-weight: 500; color: var(--signal-accent, #087f68); }
.signal-view-all:focus-visible { outline: 2px solid var(--signal-accent, #087f68); outline-offset: 2px; }
@media (max-width: 640px) {
  .signal-request { grid-template-columns: minmax(0, 1fr) minmax(0, 1fr); gap: 8px 12px; padding: 14px 0; }
  .signal-request-model { grid-column: 1 / -1; align-items: flex-start; }
  .signal-request-model > :deep(svg) { margin-top: 2px; }
  .signal-request-tokens { justify-content: flex-start; padding-left: 32px; }
  .signal-request-cost > div { gap: 6px; }
  .signal-request-cost > div > span:last-child { min-width: 0; }
}
</style>
