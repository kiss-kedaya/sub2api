<template>
  <UCard
    as="section"
    class="signal-model-card col-span-2 min-w-0 shadow-xs xl:col-span-1 dark:shadow-none"
    :ui="{ body: 'p-4 sm:p-5' }"
    :aria-label="t('dashboard.modelDistribution')"
    :aria-busy="loading"
  >
    <h2 class="text-xs font-medium text-muted">{{ t('dashboard.modelDistribution') }}</h2>
    <p class="signal-model-period mt-1 text-[11px] text-dimmed tabular-nums break-words">{{ startDate }} / {{ endDate }}</p>
    <div v-if="loading" class="signal-model-state"><LoadingSpinner size="sm" /></div>
    <p v-else-if="error" class="signal-model-state">{{ t('dashboard.chartsFailed') }}</p>
    <div v-else-if="modelData" class="mt-4 flex max-w-[400px] items-center gap-3 xl:max-w-none">
      <div class="size-[68px] shrink-0"><Doughnut :data="modelData" :options="options" /></div>
      <ol class="signal-model-legend grid min-w-0 flex-1 gap-2">
        <li v-for="(model, index) in rankedModels.slice(0, 3)" :key="model.model" class="flex min-w-0 items-center gap-1.5 text-[11px] text-muted tabular-nums" :title="`${model.model}: ${formatTokensK(model.total_tokens)} tokens`">
          <span class="size-1.5 shrink-0 rounded-full" :style="{ background: colors[index] }" />
          <span class="min-w-0 flex-1 truncate text-default">{{ model.model }}</span>
          <span>{{ (model.total_tokens / totalTokens * 100).toFixed(1) }}%</span>
        </li>
      </ol>
    </div>
    <p v-else class="signal-model-state">{{ t('dashboard.noDataAvailable') }}</p>
  </UCard>
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
const colors = ['#d87a99', '#efb3c6', '#c7a768', '#a491b6', '#8195a6', '#a4aaa3']
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
.signal-model-state { min-height: 88px; display: grid; place-items: center; color: var(--ui-text-muted); font-size: 12px; }
</style>
