<template>
  <UCard
    as="section"
    class="signal-charts mt-4 min-w-0 shadow-xs dark:shadow-none"
    :ui="{ root: 'overflow-visible', header: 'px-4 py-3 sm:px-5', body: 'p-4 sm:p-5' }"
    data-signal-charts
    :aria-busy="loading"
  >
    <template #header>
      <div class="flex flex-wrap items-center justify-between gap-3">
        <div class="min-w-0">
          <DateRangePicker :start-date="startDate" :end-date="endDate" @update:startDate="$emit('update:startDate', $event)" @update:endDate="$emit('update:endDate', $event)" @change="$emit('dateRangeChange', $event)" />
        </div>
        <div class="flex min-w-0 flex-wrap items-center gap-2">
          <div class="signal-chart-mode" role="group" :aria-label="t('dashboard.tokenUsageTrend')">
            <button type="button" :aria-pressed="mode === 'total'" @click="mode = 'total'">{{ t('dashboard.totalUsage') }}</button>
            <button type="button" :aria-pressed="mode === 'breakdown'" @click="mode = 'breakdown'">{{ t('dashboard.tokenDetails') }}</button>
          </div>
          <Select class="w-[98px]" :aria-label="t('dashboard.granularity')" :model-value="granularity" :options="[{value:'day', label:t('dashboard.day')}, {value:'hour', label:t('dashboard.hour')}]" @update:model-value="$emit('update:granularity', $event)" @change="$emit('granularityChange')" />
        </div>
      </div>
    </template>
    <div v-if="error" class="flex min-h-[290px] items-center">
      <UAlert
        role="alert"
        color="error"
        variant="subtle"
        orientation="horizontal"
        icon="i-lucide-circle-alert"
        :title="t('dashboard.chartsFailed')"
      >
        <template #actions>
          <UButton color="neutral" variant="outline" size="sm" icon="i-lucide-refresh-cw" @click="$emit('refresh')">{{ t('common.refresh') }}</UButton>
        </template>
      </UAlert>
    </div>
    <div v-else class="signal-trend">
      <TokenUsageTrend :trend-data="trend" :loading="loading" :mode="mode" :animation-duration="reducedMotion ? 0 : 160" />
    </div>
  </UCard>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { usePreferredReducedMotion } from '@vueuse/core'
import { useI18n } from 'vue-i18n'
import DateRangePicker from '@/components/common/DateRangePicker.vue'
import Select from '@/components/common/Select.vue'
import TokenUsageTrend from '@/components/charts/TokenUsageTrend.vue'
import type { TrendDataPoint } from '@/types'

defineProps<{ loading: boolean; error?: boolean; startDate: string; endDate: string; granularity: string; trend: TrendDataPoint[] }>()
defineEmits(['update:startDate', 'update:endDate', 'update:granularity', 'dateRangeChange', 'granularityChange', 'refresh'])
const { t } = useI18n()
const preferredMotion = usePreferredReducedMotion()
const reducedMotion = computed(() => preferredMotion.value === 'reduce')
const mode = ref<'total' | 'breakdown'>('total')
</script>

<style scoped>
/* Segmented control: neutral track, raised pill for the active mode. */
.signal-chart-mode { display: inline-flex; gap: 2px; padding: 3px; border-radius: 8px; background: var(--ui-bg-elevated); }
.signal-chart-mode button { min-height: 30px; padding: 4px 12px; border-radius: 6px; font-size: 12px; font-weight: 500; color: var(--ui-text-muted); transition: background-color 150ms ease, color 150ms ease, box-shadow 150ms ease; }
.signal-chart-mode button:hover { color: var(--ui-text-highlighted); }
.signal-chart-mode button:focus-visible { outline: 2px solid var(--ui-border-inverted); outline-offset: 1px; }
.signal-chart-mode [aria-pressed='true'] { background: var(--ui-bg); color: var(--ui-text-highlighted); box-shadow: 0 1px 2px rgba(10, 10, 10, 0.08), 0 0 0 1px var(--ui-border); }
.signal-trend { min-width: 0; max-width: 100%; overflow: hidden; }
.signal-trend :deep(canvas) { max-width: 100%; }
/* The trend component brings its own card; inside this card it is just the chart. */
.signal-trend :deep(.card) { padding: 0 !important; border: 0 !important; border-radius: 0 !important; background: transparent !important; box-shadow: none !important; transform: none !important; }
.signal-trend :deep(.card > h3) { color: var(--ui-text-highlighted); }
.signal-trend :deep(.h-48) { height: 260px; }
@media (max-width: 639px) {
  .signal-chart-mode button { min-height: 38px; }
  .signal-trend :deep(.h-48) { height: 220px; }
}
@media (prefers-reduced-motion: reduce) { .signal-chart-mode button { transition: none; } }
</style>
