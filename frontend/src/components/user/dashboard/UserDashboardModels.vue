<template>
  <DashboardChartCard
    as="section"
    class="signal-model-card"
    :title="t('dashboard.modelDistribution')"
    :aria-label="t('dashboard.modelDistribution')"
    :aria-busy="loading"
  >
    <template #actions>
      <span class="signal-model-period text-xs text-muted tabular-nums break-words">{{ startDate }} / {{ endDate }}</span>
    </template>
    <div class="p-4 sm:p-6">
      <div v-if="loading" class="signal-model-state"><LoadingSpinner size="sm" /></div>
      <p v-else-if="error" class="signal-model-state">{{ t('dashboard.chartsFailed') }}</p>
      <div v-else-if="modelData" class="flex flex-col items-center gap-6 sm:flex-row sm:items-start">
        <div class="size-40 shrink-0"><Doughnut :data="modelData" :options="options" /></div>
        <UTable
          class="signal-model-legend max-h-72 w-full min-w-0 flex-1"
          :data="rows"
          :columns="columns"
          :ui="compactTableUi"
        >
          <template #model-cell="{ row }">
            <span class="flex min-w-0 items-center gap-2">
              <span class="size-2 shrink-0 rounded-full" :style="{ background: row.original.color }" />
              <span class="truncate text-highlighted" :title="row.original.model">{{ row.original.model }}</span>
            </span>
          </template>
          <template #tokens-header><div class="text-right">{{ t('dashboard.tokens') }}</div></template>
          <template #tokens-cell="{ row }"><div class="text-right tabular-nums">{{ formatTokensK(row.original.total_tokens) }}</div></template>
          <template #share-header><div class="text-right">{{ t('dashboard.share') }}</div></template>
          <template #share-cell="{ row }"><div class="text-right font-medium text-highlighted tabular-nums">{{ row.original.share }}</div></template>
        </UTable>
      </div>
      <p v-else class="signal-model-state">{{ t('dashboard.noDataAvailable') }}</p>
    </div>
  </DashboardChartCard>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { usePreferredReducedMotion } from '@vueuse/core'
import { useI18n } from 'vue-i18n'
import { Chart as ChartJS, ArcElement, Tooltip, type ChartOptions } from 'chart.js'
import { Doughnut } from 'vue-chartjs'
import type { TableColumn } from '@nuxt/ui'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import DashboardChartCard from '@/components/dashboard/DashboardChartCard.vue'
import { compactTableUi } from '@/components/dashboard/dashboardUi'
import type { ModelStat } from '@/types'
import { formatTokensK } from '@/utils/format'

ChartJS.register(ArcElement, Tooltip)
const props = defineProps<{ models: ModelStat[]; startDate: string; endDate: string; loading: boolean; error?: boolean }>()
const { t } = useI18n()
const motion = usePreferredReducedMotion()
// Several series: keep distinct hues so neighbouring slices stay readable.
const colors = ['#d87a99', '#efb3c6', '#c7a768', '#a491b6', '#8195a6', '#a4aaa3']
const rankedModels = computed(() => props.models.filter(model => Number.isFinite(model.total_tokens) && model.total_tokens > 0).slice().sort((a, b) => b.total_tokens - a.total_tokens))
const totalTokens = computed(() => rankedModels.value.reduce((sum, model) => sum + model.total_tokens, 0))
const modelData = computed(() => totalTokens.value > 0 ? {
  labels: rankedModels.value.map(model => model.model),
  datasets: [{ data: rankedModels.value.map(model => model.total_tokens), backgroundColor: rankedModels.value.map((_, index) => colors[index % colors.length]), borderWidth: 0 }],
} : null)

interface ModelRow { model: string; total_tokens: number; share: string; color: string }
const rows = computed<ModelRow[]>(() => rankedModels.value.map((model, index) => ({
  model: model.model,
  total_tokens: model.total_tokens,
  share: `${(model.total_tokens / totalTokens.value * 100).toFixed(1)}%`,
  color: colors[index % colors.length],
})))
const columns = computed<TableColumn<ModelRow>[]>(() => [
  { accessorKey: 'model', header: t('dashboard.model') },
  { id: 'tokens' },
  { id: 'share' },
])

const options = computed<ChartOptions<'doughnut'>>(() => ({
  responsive: true,
  maintainAspectRatio: false,
  cutout: '72%',
  animation: { duration: motion.value === 'reduce' ? 0 : 160 },
  plugins: { legend: { display: false }, tooltip: { callbacks: {
    label: context => `${context.label}: ${formatTokensK(context.parsed)} tokens`,
  } } },
}))
</script>

<style scoped>
.signal-model-state { min-height: 120px; display: grid; place-items: center; color: var(--ui-text-muted); font-size: 12px; }
</style>
