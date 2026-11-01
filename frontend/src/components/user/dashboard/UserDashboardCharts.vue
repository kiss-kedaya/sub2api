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
      <div class="signal-chart-mode" role="group" :aria-label="t('dashboard.tokenUsageTrend')">
        <button type="button" :aria-pressed="mode === 'total'" @click="mode = 'total'">{{ t('dashboard.totalUsage') }}</button>
        <button type="button" :aria-pressed="mode === 'breakdown'" @click="mode = 'breakdown'">{{ t('dashboard.tokenDetails') }}</button>
      </div>
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
/* Segmented control: neutral track, raised pill for the active mode. */
.signal-chart-mode { display: inline-flex; gap: 2px; padding: 3px; border-radius: 8px; background: var(--ui-bg-elevated); }
.signal-chart-mode button { min-height: 30px; padding: 4px 12px; border-radius: 6px; font-size: 12px; font-weight: 500; color: var(--ui-text-muted); transition: background-color 150ms ease, color 150ms ease, box-shadow 150ms ease; }
.signal-chart-mode button:hover { color: var(--ui-text-highlighted); }
.signal-chart-mode button:focus-visible { outline: 2px solid var(--ui-border-inverted); outline-offset: 1px; }
.signal-chart-mode [aria-pressed='true'] { background: var(--ui-bg); color: var(--ui-text-highlighted); box-shadow: 0 0 0 1px var(--ui-border); }
.signal-trend { min-width: 0; max-width: 100%; overflow: hidden; }
.signal-trend :deep(canvas) { max-width: 100%; }
/* The trend component brings its own card and title; inside this card it is just the chart. */
.signal-trend :deep(.card) { padding: 0 !important; border: 0 !important; border-radius: 0 !important; background: transparent !important; box-shadow: none !important; transform: none !important; }
.signal-trend :deep(.card > h3) { display: none; }
.signal-trend :deep(.h-48) { height: 280px; }
@media (max-width: 639px) {
  .signal-chart-mode button { min-height: 36px; }
  .signal-trend :deep(.h-48) { height: 220px; }
}
@media (prefers-reduced-motion: reduce) { .signal-chart-mode button { transition: none; } }
</style>
