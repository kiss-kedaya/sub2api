<template>
  <article class="channel-signal-card group relative z-0 glass-card flex min-h-[286px] flex-col overflow-visible rounded-[24px] p-5 text-left hover:z-20">
    <header class="channel-signal-card__header flex items-start gap-3">
      <span class="channel-signal-card__provider grid h-9 w-9 shrink-0 place-items-center rounded-xl ring-1 ring-black/5 dark:ring-white/10" :class="providerGradient(row.platform)">
        <ProviderIcon :provider="row.platform" :size="20" />
      </span>
      <div class="min-w-0 flex-1">
        <div class="channel-signal-card__name truncate text-base font-semibold text-gray-900 dark:text-gray-100">{{ groupLabel }}</div>
        <div class="channel-signal-card__meta mt-1 flex min-w-0 flex-wrap items-center gap-1.5">
          <span v-if="showPlatformBadge" class="rounded-md px-1.5 py-0.5 text-[10px] font-medium" :class="providerBadgeClass(row.platform)">{{ providerLabel(row.platform) }}</span>
          <span class="rounded-md bg-primary-50 px-1.5 py-0.5 font-mono text-[10px] font-medium text-primary-700 dark:bg-dark-700 dark:text-gray-300">{{ t('channelMonitorV3.userRate') }} {{ formattedUserRate }}</span>
          <HelpTooltip
            v-if="rateChange"
            data-testid="channel-status-rate-change"
            :content="rateChangeTooltip"
            width-class="w-56"
          >
            <template #trigger>
              <span
                class="inline-flex items-center gap-0.5 rounded-md px-1 py-0.5 font-mono text-[10px] font-semibold tabular-nums"
                :class="rateChangeClass"
                data-testid="channel-status-rate-change-chip"
                :data-direction="rateChange.direction"
              >
                <UIcon :name="rateChangeIcon" class="size-3" />
                {{ rateChangeLabel }}
              </span>
            </template>
          </HelpTooltip>
        </div>
      </div>
      <HelpTooltip
        v-if="isNew"
        data-testid="channel-status-new-badge"
        class="shrink-0"
        :content="t('channelMonitorV3.newGroupTip')"
        width-class="w-56"
      >
        <template #trigger>
          <span class="inline-flex shrink-0 items-center rounded-full bg-primary-50 px-2 py-0.5 text-[10px] font-semibold uppercase tracking-wide text-primary-700 ring-1 ring-primary-500/15 dark:bg-primary-500/10 dark:text-primary-300 dark:ring-primary-400/20">NEW</span>
        </template>
      </HelpTooltip>
    </header>

    <div class="channel-signal-card__metrics mt-5 grid grid-cols-3 gap-2">
      <div class="channel-signal-card__metric rounded-2xl border border-slate-200/80 bg-slate-50/85 p-3 dark:border-dark-700/50 dark:bg-dark-900/40">
        <div class="text-[10px] font-semibold uppercase tracking-wider text-gray-400">{{ t('channelMonitorV3.cacheRate') }}</div>
        <div class="mt-1.5 font-mono text-lg font-bold tabular-nums text-gray-900 dark:text-gray-100">{{ cacheRate }}</div>
      </div>
      <div class="channel-signal-card__metric rounded-2xl border border-slate-200/80 bg-slate-50/85 p-3 dark:border-dark-700/50 dark:bg-dark-900/40">
        <div class="text-[10px] font-semibold uppercase tracking-wider text-gray-400">{{ t('channelMonitorV3.successRate') }}</div>
        <div class="mt-1.5 font-mono text-lg font-bold tabular-nums" :class="availabilityClass">{{ successRate }}</div>
      </div>
      <div class="channel-signal-card__metric rounded-2xl border border-slate-200/80 bg-slate-50/85 p-3 dark:border-dark-700/50 dark:bg-dark-900/40">
        <div class="text-[10px] font-semibold uppercase tracking-wider text-gray-400">{{ t('channelMonitorV3.ttft') }}</div>
        <div class="mt-1.5 font-mono text-lg font-bold tabular-nums text-gray-900 dark:text-gray-100">{{ ttft }}</div>
      </div>
    </div>

    <ChannelMonitorV3Timeline
      class="channel-signal-card__timeline mt-4"
      :buckets="row.buckets"
      :countdown-seconds="countdownSeconds"
      :length="timelineLength"
    />

    <ChannelMonitorV3QualityHistory
      :group-id="row.group_id ?? undefined"
      :enabled="row.quality_enabled ?? false"
    />
  </article>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { MonitorMatrixRow } from '@/api/channelMonitorV2'
import { availabilityTextClass, formatMonitorMs, formatMonitorPercent } from '@/features/channel-monitor-v2/monitorFormat'
import type { GroupRateChange } from '@/features/channel-monitor-v2/groupSignals'
import { formatRateDelta } from '@/features/channel-monitor-v2/groupSignals'
import { providerGradient, useChannelMonitorFormat } from '@/composables/useChannelMonitorFormat'
import HelpTooltip from '@/components/common/HelpTooltip.vue'
import ProviderIcon from './ProviderIcon.vue'
import ChannelMonitorV3Timeline from './ChannelMonitorV3Timeline.vue'
import ChannelMonitorV3QualityHistory from './ChannelMonitorV3QualityHistory.vue'

const props = withDefaults(defineProps<{
  row: MonitorMatrixRow
  countdownSeconds: number
  timelineLength: number
  userRateMultiplier?: number | null
  showPlatformBadge?: boolean
  /** Group created inside the trailing 24h window (resolved by the view). */
  isNew?: boolean
  /** Effective-rate change inside the trailing 24h window (resolved by the view). */
  rateChange?: GroupRateChange | null
}>(), {
  showPlatformBadge: true,
  isNew: false,
})
const { t, locale } = useI18n()
const { providerLabel, providerBadgeClass } = useChannelMonitorFormat()

const groupLabel = computed(() => props.row.group_name || t('channelMonitorV3.unknownGroup'))
const formattedUserRate = computed(() => {
  const value = props.userRateMultiplier
  return typeof value === 'number' && Number.isFinite(value) ? `${value.toFixed(2)}x` : '-'
})
const rateChangeLabel = computed(() => (props.rateChange ? `${formatRateDelta(props.rateChange.delta)}x` : ''))
const rateChangeIcon = computed(() => (props.rateChange?.direction === 'up'
  ? 'i-lucide-arrow-up-right'
  : 'i-lucide-arrow-down-right'))
const rateChangeClass = computed(() => (props.rateChange?.direction === 'up'
  ? 'bg-red-50 text-red-600 dark:bg-red-500/10 dark:text-red-400'
  : 'bg-emerald-50 text-emerald-600 dark:bg-emerald-500/10 dark:text-emerald-400'))
const rateChangeTooltip = computed(() => {
  const change = props.rateChange
  if (!change) return ''
  return t('channelMonitorV3.rateChangeTip', {
    previous: `${change.previous.toFixed(2)}x`,
    current: `${change.current.toFixed(2)}x`,
    time: formatChangedAt(change.changedAt),
  })
})
function formatChangedAt(value: string): string {
  const parsed = Date.parse(value)
  if (!Number.isFinite(parsed)) return '-'
  return new Intl.DateTimeFormat(locale?.value || undefined, { dateStyle: 'short', timeStyle: 'short' }).format(parsed)
}
const latestBucket = computed(() => [...props.row.buckets]
  .filter(bucket => bucket.bucket_start && bucket.metrics)
  .sort((a, b) => Date.parse(a.bucket_start) - Date.parse(b.bucket_start))
  .at(-1))
const latestMetrics = computed(() => latestBucket.value?.metrics ?? props.row.metrics)
const cacheRate = computed(() => formatMonitorPercent(latestMetrics.value.cache_rate))
const availabilityPercent = computed(() => (1 - latestMetrics.value.error_rate) * 100)
const successRate = computed(() => formatMonitorPercent(availabilityPercent.value / 100))
const availabilityClass = computed(() => availabilityTextClass(availabilityPercent.value))
const ttft = computed(() => formatMonitorMs(latestMetrics.value.ttft.p50_ms))
</script>
