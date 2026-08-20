<template>
  <article class="group rounded-2xl border border-gray-200/80 bg-white/75 p-5 text-left shadow-card backdrop-blur-xl transition-all duration-300 hover:-translate-y-0.5 hover:border-primary-300 hover:shadow-card-hover dark:border-dark-700/70 dark:bg-dark-800/60 dark:hover:border-primary-500/40">
    <header class="flex items-start gap-3">
      <span class="grid h-9 w-9 shrink-0 place-items-center rounded-xl ring-1 ring-black/5 dark:ring-white/10" :class="providerGradient(row.platform)">
        <ProviderIcon :provider="row.platform" :size="20" />
      </span>
      <div class="min-w-0 flex-1">
        <div class="truncate text-base font-semibold text-gray-900 dark:text-gray-100">{{ platformLabel }}</div>
        <div class="mt-1 flex min-w-0 flex-wrap items-center gap-1.5">
          <span class="rounded-md px-1.5 py-0.5 text-[10px] font-medium" :class="providerBadgeClass(row.platform)">{{ row.platform }}</span>
          <span v-if="row.group_name" class="rounded-md bg-gray-100 px-1.5 py-0.5 text-[10px] font-medium text-gray-600 dark:bg-dark-700 dark:text-gray-300">{{ row.group_name }}</span>
        </div>
      </div>
      <span class="shrink-0 rounded-full px-2.5 py-1 text-xs font-semibold" :class="statusClass">{{ statusText }}</span>
    </header>

    <div class="mt-5 grid grid-cols-3 gap-2">
      <div class="rounded-xl border border-gray-100 bg-gray-50/80 p-3 dark:border-dark-700/50 dark:bg-dark-900/40">
        <div class="text-[10px] font-semibold uppercase tracking-wider text-gray-400">{{ t('channelMonitorV3.cacheRate') }}</div>
        <div class="mt-1.5 font-mono text-lg font-bold tabular-nums text-gray-900 dark:text-gray-100">{{ cacheRate }}</div>
      </div>
      <div class="rounded-xl border border-gray-100 bg-gray-50/80 p-3 dark:border-dark-700/50 dark:bg-dark-900/40">
        <div class="text-[10px] font-semibold uppercase tracking-wider text-gray-400">{{ t('channelMonitorV3.successRate') }}</div>
        <div class="mt-1.5 font-mono text-lg font-bold tabular-nums text-gray-900 dark:text-gray-100">{{ successRate }}</div>
      </div>
      <div class="rounded-xl border border-gray-100 bg-gray-50/80 p-3 dark:border-dark-700/50 dark:bg-dark-900/40">
        <div class="text-[10px] font-semibold uppercase tracking-wider text-gray-400">{{ t('channelMonitorV3.ttft') }}</div>
        <div class="mt-1.5 font-mono text-lg font-bold tabular-nums text-gray-900 dark:text-gray-100">{{ ttft }}</div>
      </div>
    </div>

    <div class="mt-4 flex items-center justify-between gap-3 text-xs text-gray-500 dark:text-gray-400">
      <span class="truncate">{{ modelText }}</span>
      <span class="shrink-0 tabular-nums">{{ t('channelMonitorV3.samples', { count: row.metrics.request_count }) }}</span>
    </div>
    <div class="mt-3 h-1.5 overflow-hidden rounded-full bg-gray-100 dark:bg-dark-700" role="progressbar" :aria-valuenow="healthScore" aria-valuemin="0" aria-valuemax="100" :aria-label="t('channelMonitorV3.healthScore')">
      <div class="h-full rounded-full transition-[width] duration-500" :class="scoreClass" :style="{ width: `${healthScore}%` }" />
    </div>
  </article>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { MonitorMatrixRow } from '@/api/channelMonitorV2'
import { formatMonitorMs, formatMonitorPercent } from '@/features/channel-monitor-v2/monitorFormat'
import { providerGradient, useChannelMonitorFormat } from '@/composables/useChannelMonitorFormat'
import ProviderIcon from './ProviderIcon.vue'

const props = defineProps<{ row: MonitorMatrixRow }>()
const { t } = useI18n()
const { providerBadgeClass } = useChannelMonitorFormat()

const platformLabel = computed(() => props.row.group_name ? `${props.row.platform} / ${props.row.group_name}` : props.row.platform)
const modelText = computed(() => props.row.model && props.row.model !== '__other__' ? props.row.model : t('channelMonitorV3.allModels'))
const cacheRate = computed(() => formatMonitorPercent(props.row.metrics.cache_rate))
const successRate = computed(() => formatMonitorPercent(1 - props.row.metrics.error_rate))
const ttft = computed(() => formatMonitorMs(props.row.metrics.ttft.p50_ms))
const healthScore = computed(() => Math.max(0, Math.min(100, Math.round(props.row.health.score ?? (1 - props.row.metrics.error_rate) * 100))))
const scoreClass = computed(() => {
  const value = healthScore.value
  if (value >= 80) return 'bg-emerald-500'
  if (value >= 50) return 'bg-amber-500'
  return 'bg-red-500'
})
const statusText = computed(() => {
  if (props.row.health.overall === 'healthy') return t('channelMonitorV3.healthy')
  if (props.row.health.overall === 'warning') return t('channelMonitorV3.warning')
  if (props.row.health.overall === 'critical') return t('channelMonitorV3.critical')
  return t('channelMonitorV3.unknown')
})
const statusClass = computed(() => {
  if (props.row.health.overall === 'healthy') return 'bg-emerald-50 text-emerald-700 dark:bg-emerald-950/40 dark:text-emerald-300'
  if (props.row.health.overall === 'warning') return 'bg-amber-50 text-amber-700 dark:bg-amber-950/40 dark:text-amber-300'
  if (props.row.health.overall === 'critical') return 'bg-red-50 text-red-700 dark:bg-red-950/40 dark:text-red-300'
  return 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-gray-300'
})
</script>
