<template>
  <section class="signal-charts" data-signal-charts :aria-busy="loading">
    <div class="signal-chart-toolbar">
      <div class="signal-date-control">
        <DateRangePicker :start-date="startDate" :end-date="endDate" @update:startDate="$emit('update:startDate', $event)" @update:endDate="$emit('update:endDate', $event)" @change="$emit('dateRangeChange', $event)" />
      </div>
      <div class="signal-chart-actions">
        <div class="signal-chart-mode" role="group" :aria-label="t('dashboard.tokenUsageTrend')">
          <button :aria-pressed="mode === 'total'" @click="mode = 'total'">{{ t('dashboard.totalUsage') }}</button>
          <button :aria-pressed="mode === 'breakdown'" @click="mode = 'breakdown'">{{ t('dashboard.tokenDetails') }}</button>
        </div>
        <Select class="signal-granularity" :aria-label="t('dashboard.granularity')" :model-value="granularity" :options="[{value:'day', label:t('dashboard.day')}, {value:'hour', label:t('dashboard.hour')}]" @update:model-value="$emit('update:granularity', $event)" @change="$emit('granularityChange')" />
      </div>
    </div>
    <div v-if="error" class="signal-chart-error" role="alert">
      <span>{{ t('dashboard.chartsFailed') }}</span>
      <button class="btn btn-secondary" @click="$emit('refresh')"><Icon name="refresh" size="sm" />{{ t('common.refresh') }}</button>
    </div>
    <div v-else class="signal-trend">
      <TokenUsageTrend :trend-data="trend" :loading="loading" :mode="mode" :animation-duration="reducedMotion ? 0 : 160" />
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { usePreferredReducedMotion } from '@vueuse/core'
import { useI18n } from 'vue-i18n'
import DateRangePicker from '@/components/common/DateRangePicker.vue'
import Select from '@/components/common/Select.vue'
import Icon from '@/components/icons/Icon.vue'
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
.signal-charts { margin-top: 20px; min-width: 0; }
.signal-chart-toolbar { display: flex; align-items: center; justify-content: space-between; flex-wrap: wrap; gap: 12px; padding-bottom: 14px; }
.signal-chart-actions { display: flex; align-items: center; gap: 10px; min-width: 0; }
.signal-chart-mode { display: flex; padding: 3px; border: 1px solid var(--signal-line); border-radius: 6px; background: var(--signal-inset); }
.signal-chart-mode button { min-height: 30px; padding: 4px 12px; border-radius: 4px; font-size: 12px; color: var(--signal-muted); }
.signal-chart-mode [aria-pressed='true'] { background: var(--signal-raised); color: var(--signal-text); box-shadow: 0 1px 3px #00000010; }
.signal-granularity { width: 98px; }
.signal-trend { min-width: 0; max-width: 100%; overflow: hidden; }
.signal-trend :deep(canvas) { max-width: 100%; }
.signal-trend :deep(.card) { background: transparent; border: 0; border-radius: 0; padding: 16px 0 0; box-shadow: none; border-top: 1px solid var(--signal-line); }
.signal-trend :deep(.h-48) { height: 260px; }
.signal-chart-error { min-height: 290px; display: flex; align-items: center; justify-content: center; flex-wrap: wrap; gap: 12px; color: var(--signal-muted); }
.signal-chart-error button { gap: 8px; }
@media (max-width: 639px) {
  .signal-chart-actions { flex-wrap: wrap; }
  .signal-chart-mode button { min-height: 38px; }
  .signal-trend :deep(.h-48) { height: 220px; }
}
</style>
