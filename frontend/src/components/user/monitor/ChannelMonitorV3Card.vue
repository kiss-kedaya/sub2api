<template>
  <article class="group flex min-h-[286px] flex-col rounded-2xl border border-gray-200/80 bg-white/75 p-5 text-left shadow-card backdrop-blur-xl transition-all duration-300 hover:-translate-y-0.5 hover:border-primary-300 hover:shadow-card-hover dark:border-dark-700/70 dark:bg-dark-800/60 dark:hover:border-primary-500/40">
    <header class="flex items-start gap-3">
      <span class="grid h-9 w-9 shrink-0 place-items-center rounded-xl ring-1 ring-black/5 dark:ring-white/10" :class="providerGradient(row.platform)">
        <ProviderIcon :provider="row.platform" :size="20" />
      </span>
      <div class="min-w-0 flex-1">
        <div class="truncate text-base font-semibold text-gray-900 dark:text-gray-100">{{ groupLabel }}</div>
        <div class="mt-1 flex min-w-0 flex-wrap items-center gap-1.5">
          <span class="rounded-md px-1.5 py-0.5 text-[10px] font-medium" :class="providerBadgeClass(row.platform)">{{ providerLabel(row.platform) }}</span>
          <span class="rounded-md bg-gray-100 px-1.5 py-0.5 font-mono text-[10px] font-medium text-gray-500 dark:bg-dark-700 dark:text-gray-300">{{ t('channelMonitorV3.userRate') }} {{ formattedUserRate }}</span>
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
        <div class="mt-1.5 font-mono text-lg font-bold tabular-nums" :class="availabilityClass">{{ successRate }}</div>
      </div>
      <div class="rounded-xl border border-gray-100 bg-gray-50/80 p-3 dark:border-dark-700/50 dark:bg-dark-900/40">
        <div class="text-[10px] font-semibold uppercase tracking-wider text-gray-400">{{ t('channelMonitorV3.ttft') }}</div>
        <div class="mt-1.5 font-mono text-lg font-bold tabular-nums text-gray-900 dark:text-gray-100">{{ ttft }}</div>
      </div>
    </div>

    <ChannelMonitorV3Timeline
      class="mt-auto"
      :buckets="row.buckets"
      :countdown-seconds="countdownSeconds"
      :length="timelineLength"
    />
  </article>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { MonitorStatus } from '@/api/admin/channelMonitor'
import type { MonitorMatrixRow } from '@/api/channelMonitorV2'
import { availabilityTextClass, formatMonitorMs, formatMonitorPercent } from '@/features/channel-monitor-v2/monitorFormat'
import { providerGradient, useChannelMonitorFormat } from '@/composables/useChannelMonitorFormat'
import ProviderIcon from './ProviderIcon.vue'
import ChannelMonitorV3Timeline from './ChannelMonitorV3Timeline.vue'

const props = defineProps<{
  row: MonitorMatrixRow
  countdownSeconds: number
  timelineLength: number
  userRateMultiplier?: number | null
}>()
const { t } = useI18n()
const { statusLabel, statusBadgeClass, providerLabel, providerBadgeClass } = useChannelMonitorFormat()

const groupLabel = computed(() => props.row.group_name || t('channelMonitorV3.unknownGroup'))
const formattedUserRate = computed(() => {
  const value = props.userRateMultiplier
  return typeof value === 'number' && Number.isFinite(value) ? `${value.toFixed(2)}x` : '-'
})
const latestBucket = computed(() => [...props.row.buckets]
  .filter(bucket => bucket.bucket_start && bucket.metrics)
  .sort((a, b) => Date.parse(a.bucket_start) - Date.parse(b.bucket_start))
  .at(-1))
const latestMetrics = computed(() => latestBucket.value?.metrics ?? props.row.metrics)
const latestHealth = computed(() => latestBucket.value?.health ?? props.row.health)
// The cards show the newest completed monitoring bucket, not the selected-range aggregate.
const cacheRate = computed(() => formatMonitorPercent(latestMetrics.value.cache_rate))
const availabilityPercent = computed(() => (1 - latestMetrics.value.error_rate) * 100)
const successRate = computed(() => formatMonitorPercent(availabilityPercent.value / 100))
const availabilityClass = computed(() => availabilityTextClass(availabilityPercent.value))
const ttft = computed(() => formatMonitorMs(latestMetrics.value.ttft.p50_ms))
const monitorStatus = computed<MonitorStatus | null>(() => {
  if (latestHealth.value.overall === 'healthy') return 'operational'
  if (latestHealth.value.overall === 'warning') return 'degraded'
  if (latestHealth.value.overall === 'critical') return 'failed'
  return null
})
const statusText = computed(() => monitorStatus.value ? statusLabel(monitorStatus.value) : t('channelMonitorV3.unknown'))
const statusClass = computed(() => monitorStatus.value
  ? statusBadgeClass(monitorStatus.value)
  : 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-gray-300')
</script>
