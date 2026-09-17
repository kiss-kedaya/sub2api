<template>
  <section class="signal-charts" data-signal-charts>
    <div class="signal-chart-toolbar">
      <div class="signal-date-control">
        <span>{{ t('dashboard.timeRange') }}</span>
        <DateRangePicker :start-date="startDate" :end-date="endDate" @update:startDate="$emit('update:startDate', $event)" @update:endDate="$emit('update:endDate', $event)" @change="$emit('dateRangeChange', $event)" />
      </div>
      <div class="signal-chart-actions">
        <label for="signal-dashboard-granularity">{{ t('dashboard.granularity') }}</label>
        <Select id="signal-dashboard-granularity" class="signal-granularity" :model-value="granularity" :options="[{value:'day', label:t('dashboard.day')}, {value:'hour', label:t('dashboard.hour')}]" @update:model-value="$emit('update:granularity', $event)" @change="$emit('granularityChange')" />
        <button class="signal-refresh" :disabled="loading" :title="t('common.refresh')" :aria-label="t('common.refresh')" @click="$emit('refresh')">
          <Icon name="refresh" size="md" />
        </button>
      </div>
    </div>
    <div class="signal-chart-grid">
      <div class="signal-trend">
        <TokenUsageTrend :trend-data="trend" :loading="loading" :animation-duration="reducedMotion ? 0 : 180" />
      </div>
      <section class="signal-distribution" :aria-label="t('dashboard.modelDistribution')">
        <h2>{{ t('dashboard.modelDistribution') }}</h2>
        <div v-if="loading" class="signal-chart-overlay"><LoadingSpinner size="md" /></div>
        <div class="signal-distribution-body">
          <div class="signal-doughnut">
            <Doughnut v-if="modelData" :data="modelData" :options="doughnutOptions" />
            <div v-else class="signal-chart-empty">{{ t('dashboard.noDataAvailable') }}</div>
          </div>
          <div class="signal-model-table" tabindex="0" :aria-label="t('dashboard.modelDistribution')">
            <table>
              <thead>
                <tr>
                  <th>{{ t('dashboard.model') }}</th>
                  <th>{{ t('dashboard.requests') }}</th>
                  <th>{{ t('dashboard.tokens') }}</th>
                  <th>{{ t('dashboard.actual') }}</th>
                  <th>{{ t('dashboard.standard') }}</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="model in models" :key="model.model">
                  <td class="signal-model-name" :title="model.model">{{ model.model }}</td>
                  <td>{{ formatNumber(model.requests) }}</td>
                  <td>{{ formatTokens(model.total_tokens) }}</td>
                  <td class="signal-model-cost">${{ formatCost(model.actual_cost) }}</td>
                  <td class="signal-model-standard">${{ formatCost(model.cost) }}</td>
                </tr>
              </tbody>
            </table>
          </div>
        </div>
      </section>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { usePreferredReducedMotion } from '@vueuse/core'
import { useI18n } from 'vue-i18n'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import DateRangePicker from '@/components/common/DateRangePicker.vue'
import Select from '@/components/common/Select.vue'
import Icon from '@/components/icons/Icon.vue'
import { Doughnut } from 'vue-chartjs'
import TokenUsageTrend from '@/components/charts/TokenUsageTrend.vue'
import type { TrendDataPoint, ModelStat } from '@/types'
import { formatCostFixed as formatCost, formatNumberLocaleString as formatNumber, formatTokensK as formatTokens } from '@/utils/format'
import { Chart as ChartJS, CategoryScale, LinearScale, PointElement, LineElement, ArcElement, Title, Tooltip, Legend, Filler } from 'chart.js'

ChartJS.register(CategoryScale, LinearScale, PointElement, LineElement, ArcElement, Title, Tooltip, Legend, Filler)

const props = defineProps<{ loading: boolean, startDate: string, endDate: string, granularity: string, trend: TrendDataPoint[], models: ModelStat[] }>()
defineEmits(['update:startDate', 'update:endDate', 'update:granularity', 'dateRangeChange', 'granularityChange', 'refresh'])
const { t } = useI18n()
const preferredMotion = usePreferredReducedMotion()
const reducedMotion = computed(() => preferredMotion.value === 'reduce')

const modelData = computed(() => !props.models?.length ? null : {
  labels: props.models.map((m: ModelStat) => m.model),
  datasets: [{
    data: props.models.map((m: ModelStat) => m.total_tokens),
    backgroundColor: ['#14866e', '#5483a5', '#78867a', '#bd8741', '#526a66', '#a07684', '#65a69c', '#9ba4ab']
  }]
})

const doughnutOptions = computed(() => ({
  responsive: true,
  maintainAspectRatio: false,
  animation: { duration: reducedMotion.value ? 0 : 180 },
  cutout: '72%',
  borderWidth: 2,
  plugins: {
    legend: { display: false },
    tooltip: {
      callbacks: {
        label: (context: any) => `${context.label}: ${formatTokens(context.parsed)} tokens`
      }
    }
  }
}))
</script>

<style scoped>
.signal-charts { margin-top: 12px; border-top: 1px solid var(--signal-line, #dce2df); }
.signal-chart-toolbar { display: flex; align-items: center; justify-content: space-between; gap: 12px; flex-wrap: wrap; padding: 16px 0; }
.signal-date-control, .signal-chart-actions { display: flex; align-items: center; gap: 10px; min-width: 0; }
.signal-date-control > span, .signal-chart-actions label { font-size: 12px; color: var(--signal-muted, #65716a); flex-shrink: 0; }
.signal-granularity { width: 108px; }
.signal-refresh { display: grid; place-items: center; width: 38px; height: 38px; flex-shrink: 0; color: var(--signal-muted, #65716a); border: 1px solid var(--signal-line, #dce2df); border-radius: 6px; background: var(--signal-surface, #fff); }
.signal-refresh:hover { color: var(--signal-accent, #087f68); }
.signal-refresh:focus-visible { outline: 2px solid var(--signal-accent, #087f68); outline-offset: 3px; }
.signal-refresh:disabled { opacity: .45; cursor: wait; }
.signal-chart-grid { display: grid; grid-template-columns: minmax(0, 1.15fr) minmax(0, 1fr); gap: 24px; padding-bottom: 24px; border-bottom: 1px solid var(--signal-line, #dce2df); }
.signal-trend, .signal-distribution { min-width: 0; background: var(--signal-surface, #fff); }
.signal-trend :deep(.card) { border: 0; border-radius: 0; box-shadow: none; background: transparent; padding: 16px; }
.signal-trend :deep(h3), .signal-distribution h2 { margin: 0 0 18px; font-size: 14px; font-weight: 600; color: var(--signal-text, #202423); }
.signal-trend :deep(.h-48) { height: 240px; }
.signal-distribution { position: relative; padding: 16px; }
.signal-distribution-body { display: flex; flex-direction: column; align-items: center; gap: 14px; }
.signal-doughnut { width: 120px; height: 120px; flex-shrink: 0; }
.signal-chart-empty { display: grid; place-items: center; height: 100%; font-size: 12px; color: var(--signal-muted, #65716a); text-align: center; }
.signal-chart-overlay { position: absolute; inset: 0; z-index: 1; display: grid; place-items: center; background: var(--signal-surface, #fff); opacity: .85; }
.signal-model-table { width: 100%; max-height: 122px; overflow: auto; scrollbar-gutter: stable; }
.signal-model-table:focus-visible { outline: 2px solid var(--signal-accent, #087f68); outline-offset: 2px; }
.signal-model-table table { width: 100%; font-size: 11px; font-variant-numeric: tabular-nums; }
.signal-model-table th { padding: 0 8px 8px; color: var(--signal-muted, #65716a); font-weight: 500; white-space: nowrap; text-align: right; }
.signal-model-table th:first-child { text-align: left; padding-left: 0; }
.signal-model-table td { border-top: 1px solid var(--signal-line, #dce2df); padding: 7px 8px; text-align: right; white-space: nowrap; }
.signal-model-table td.signal-model-name { max-width: 160px; text-align: left; padding-left: 0; font-weight: 500; overflow: hidden; text-overflow: ellipsis; }
.signal-model-cost { color: var(--signal-amber, #99640c); }
.signal-model-standard { color: var(--signal-muted, #65716a); }
@media (max-width: 1200px) {
  .signal-chart-grid { grid-template-columns: minmax(0, 1fr); gap: 16px; }
  .signal-distribution-body { flex-direction: row; gap: 24px; }
  .signal-model-table { max-height: 200px; }
}
@media (max-width: 640px) {
  .signal-chart-toolbar { align-items: stretch; }
  .signal-date-control { width: 100%; flex-wrap: wrap; }
  .signal-chart-actions { width: 100%; justify-content: flex-end; }
  .signal-chart-grid { gap: 12px; }
  .signal-distribution-body { flex-direction: column; gap: 16px; }
  .signal-trend :deep(.card), .signal-distribution { padding: 14px 10px; }
  .signal-trend :deep(.h-48) { height: 250px; }
}
</style>
