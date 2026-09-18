<template>
  <section class="signal-model-card" :aria-label="t('dashboard.modelDistribution')" :aria-busy="loading">
    <h2>{{ t('dashboard.modelDistribution') }}</h2>
    <p class="signal-model-period">{{ startDate }} / {{ endDate }}</p>
    <div v-if="loading" class="signal-model-state"><LoadingSpinner size="sm" /></div>
    <p v-else-if="error" class="signal-model-state">{{ t('dashboard.chartsFailed') }}</p>
    <div v-else-if="modelData" class="signal-model-summary">
      <div class="signal-model-ring"><Doughnut :data="modelData" :options="options" /></div>
      <ol class="signal-model-legend">
        <li v-for="(model, index) in rankedModels.slice(0, 3)" :key="model.model" :title="`${model.model}: ${formatTokensK(model.total_tokens)} tokens`">
          <span class="signal-model-dot" :style="{ background: colors[index] }" />
          <span class="signal-model-label">{{ model.model }}</span>
          <span>{{ (model.total_tokens / totalTokens * 100).toFixed(1) }}%</span>
        </li>
      </ol>
    </div>
    <p v-else class="signal-model-state">{{ t('dashboard.noDataAvailable') }}</p>
  </section>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { usePreferredReducedMotion } from '@vueuse/core'
import { useI18n } from 'vue-i18n'
import { Chart as ChartJS, ArcElement, Tooltip, type ChartOptions } from 'chart.js'
import { Doughnut } from 'vue-chartjs'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import type { ModelStat } from '@/types'
import { formatTokensK } from '@/utils/format'

ChartJS.register(ArcElement, Tooltip)
const props = defineProps<{ models: ModelStat[]; startDate: string; endDate: string; loading: boolean; error?: boolean }>()
const { t } = useI18n()
const motion = usePreferredReducedMotion()
const colors = ['#9bc2ff', '#61b9a2', '#c7a768', '#b397b6', '#8195a6', '#a4aaa3']
const rankedModels = computed(() => props.models.filter(model => Number.isFinite(model.total_tokens) && model.total_tokens > 0).slice().sort((a, b) => b.total_tokens - a.total_tokens))
const totalTokens = computed(() => rankedModels.value.reduce((sum, model) => sum + model.total_tokens, 0))
const modelData = computed(() => totalTokens.value > 0 ? {
  labels: rankedModels.value.map(model => model.model),
  datasets: [{ data: rankedModels.value.map(model => model.total_tokens), backgroundColor: rankedModels.value.map((_, index) => colors[index % colors.length]), borderWidth: 0 }],
} : null)
const options = computed<ChartOptions<'doughnut'>>(() => ({
  responsive: true,
  maintainAspectRatio: false,
  cutout: '74%',
  animation: { duration: motion.value === 'reduce' ? 0 : 160 },
  plugins: { legend: { display: false }, tooltip: { callbacks: {
    label: context => `${context.label}: ${formatTokensK(context.parsed)} tokens`,
  } } },
}))
</script>

<style scoped>
.signal-model-card { min-width: 0; padding: 18px 16px; border: 1px solid var(--signal-line); border-radius: 8px; background: var(--signal-surface); }
.signal-model-card h2 { font-size: 12px; font-weight: 500; color: var(--signal-muted); }
.signal-model-period { margin-top: 6px; font-size: 10px; color: var(--signal-muted); font-variant-numeric: tabular-nums; overflow-wrap: anywhere; }
.signal-model-summary { display: flex; align-items: center; gap: 12px; margin-top: 18px; }
.signal-model-ring { width: 68px; height: 68px; flex: 0 0 68px; }
.signal-model-legend { flex: 1; min-width: 0; display: grid; gap: 8px; }
.signal-model-legend li { display: flex; align-items: center; gap: 5px; min-width: 0; font-size: 10px; color: var(--signal-muted); font-variant-numeric: tabular-nums; }
.signal-model-dot { width: 5px; height: 5px; border-radius: 50%; flex: 0 0 auto; }
.signal-model-label { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; color: var(--signal-text); }
.signal-model-state { min-height: 88px; display: grid; place-items: center; color: var(--signal-muted); font-size: 12px; }
</style>
