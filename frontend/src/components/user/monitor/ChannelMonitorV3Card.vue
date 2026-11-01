<template>
  <UCard
    as="article"
    variant="outline"
    class="group relative z-0 flex min-h-[286px] flex-col overflow-visible text-left hover:z-20"
    :ui="{ root: 'overflow-visible', body: 'flex flex-1 flex-col p-4 sm:p-5' }"
  >
    <header class="flex items-start gap-3">
      <span class="grid size-9 shrink-0 place-items-center rounded-md bg-elevated">
        <ProviderIcon :provider="row.platform" :size="20" />
      </span>
      <div class="min-w-0 flex-1">
        <div class="truncate text-base font-semibold text-highlighted">{{ groupLabel }}</div>
        <div class="mt-1 flex min-w-0 flex-wrap items-center gap-1.5">
          <span v-if="showPlatformBadge" class="rounded-md px-1.5 py-0.5 text-[10px] font-medium" :class="providerBadgeClass(row.platform)">{{ providerLabel(row.platform) }}</span>
          <UBadge color="neutral" variant="subtle" size="sm" class="font-mono">{{ t('channelMonitorV3.userRate') }} {{ formattedUserRate }}</UBadge>
        </div>
      </div>
      <UBadge
        v-if="showInsufficientSample"
        data-testid="channel-status-sample-badge"
        color="neutral"
        variant="subtle"
        class="shrink-0"
      >{{ t('channelMonitorV3.unknown') }}</UBadge>
    </header>

    <dl class="mt-5 grid grid-cols-3 gap-3 border-y border-default py-3">
      <div>
        <dt class="text-xs text-muted">{{ t('channelMonitorV3.cacheRate') }}</dt>
        <dd class="mt-1 font-mono text-lg font-semibold tabular-nums text-highlighted">{{ cacheRate }}</dd>
      </div>
      <div>
        <dt class="text-xs text-muted">{{ t('channelMonitorV3.successRate') }}</dt>
        <dd class="mt-1 font-mono text-lg font-semibold tabular-nums" :class="availabilityClass">{{ successRate }}</dd>
      </div>
      <div>
        <dt class="text-xs text-muted">{{ t('channelMonitorV3.ttft') }}</dt>
        <dd class="mt-1 font-mono text-lg font-semibold tabular-nums text-highlighted">{{ ttft }}</dd>
      </div>
    </dl>

    <ChannelMonitorV3Timeline
      class="mt-4"
      :buckets="row.buckets"
      :countdown-seconds="countdownSeconds"
      :length="timelineLength"
    />

    <ChannelMonitorV3QualityHistory
      :group-id="row.group_id ?? undefined"
      :enabled="row.quality_enabled ?? false"
    />
  </UCard>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { MonitorMatrixRow } from '@/api/channelMonitorV2'
import { availabilityTextClass, formatMonitorMs, formatMonitorPercent } from '@/features/channel-monitor-v2/monitorFormat'
import { useChannelMonitorFormat } from '@/composables/useChannelMonitorFormat'
import ProviderIcon from './ProviderIcon.vue'
import ChannelMonitorV3Timeline from './ChannelMonitorV3Timeline.vue'
import ChannelMonitorV3QualityHistory from './ChannelMonitorV3QualityHistory.vue'

const props = withDefaults(defineProps<{
  row: MonitorMatrixRow
  countdownSeconds: number
  timelineLength: number
  userRateMultiplier?: number | null
  showPlatformBadge?: boolean
}>(), {
  showPlatformBadge: true,
})
const { t } = useI18n()
const { providerLabel, providerBadgeClass } = useChannelMonitorFormat()

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
const showInsufficientSample = computed(() => {
  const overall = latestHealth.value.overall
  return overall !== 'healthy' && overall !== 'warning' && overall !== 'critical'
})
</script>
