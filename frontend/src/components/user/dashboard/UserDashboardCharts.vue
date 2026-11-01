<template>
  <DashboardChartCard
    as="section"
    class="signal-charts"
    data-signal-charts
    :aria-busy="loading"
    :title="t('dashboard.tokenUsageTrend')"
    :value="totalLabel"
  >
    <template #actions>
      <!-- Total / breakdown switch as the template's pill tabs (role=tab, aria-selected). -->
      <UTabs
        class="signal-chart-mode"
        :model-value="mode"
        :items="modeItems"
        :content="false"
        variant="pill"
        size="xs"
        @update:model-value="setMode"
      />
    </template>
    <div v-if="error" class="flex min-h-[290px] items-center p-4 sm:p-6">
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
    <div v-else class="signal-trend px-2 pb-2 pt-4 sm:px-4">
      <TokenUsageTrend
        :trend-data="trend"
        :loading="loading"
        :mode="mode"
        :color="mode === 'total' ? primary : undefined"
        :animation-duration="reducedMotion ? 0 : 160"
      />
    </div>
  </DashboardChartCard>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { usePreferredReducedMotion } from '@vueuse/core'
import { useI18n } from 'vue-i18n'
import TokenUsageTrend from '@/components/charts/TokenUsageTrend.vue'
import DashboardChartCard from '@/components/dashboard/DashboardChartCard.vue'
import { useThemeColor } from '@/components/dashboard/dashboardUi'
import type { TrendDataPoint } from '@/types'

const props = defineProps<{ loading: boolean; error?: boolean; trend: TrendDataPoint[] }>()
defineEmits(['refresh'])
const { t } = useI18n()
const preferredMotion = usePreferredReducedMotion()
const reducedMotion = computed(() => preferredMotion.value === 'reduce')
const mode = ref<'total' | 'breakdown'>('total')
const modeItems = computed(() => [
  { label: t('dashboard.totalUsage'), value: 'total' },
  { label: t('dashboard.tokenDetails'), value: 'breakdown' },
])
const setMode = (value: string | number) => {
  mode.value = value === 'breakdown' ? 'breakdown' : 'total'
}
// Main series follows the theme primary (Appearance settings); #171717 is the default black.
const primary = useThemeColor('--ui-primary', '#171717')

const formatTokens = (n: number) => {
  if (n >= 1_000_000_000) return `${(n / 1_000_000_000).toFixed(2)}B`
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`
  if (n >= 1000) return `${(n / 1000).toFixed(1)}K`
  return n.toString()
}
// Header number: tokens over the chosen range. Not shown while loading or failed, never a fake 0.
const totalLabel = computed(() => {
  if (props.loading || props.error) return '-'
  return formatTokens(props.trend.reduce((sum, point) => sum + (Number(point.total_tokens) || 0), 0))
})
</script>

<style scoped>
.signal-trend { min-width: 0; max-width: 100%; overflow: hidden; }
.signal-trend :deep(canvas) { max-width: 100%; }
/* The trend component brings its own card and title; inside this card it is just the chart. */
/* `.card` is a layered utility, so this unlayered rule wins without !important. */
.signal-trend :deep(.card) { padding: 0; border: 0; border-radius: 0; background: transparent; }
.signal-trend :deep(.card > h3) { display: none; }
.signal-trend :deep(.h-48) { height: 280px; }
@media (max-width: 639px) {
  .signal-trend :deep(.h-48) { height: 220px; }
}
</style>
