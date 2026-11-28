<template>
  <article class="channel-signal-card group relative z-0 glass-card flex min-h-[286px] flex-col overflow-visible rounded-[24px] p-5 text-left hover:z-20">
    <header class="channel-signal-card__header flex items-start gap-3">
      <span class="channel-signal-card__provider grid h-9 w-9 shrink-0 place-items-center rounded-xl ring-1 ring-black/5 dark:ring-white/10" :class="providerGradient(row.platform)">
        <ProviderIcon :provider="row.platform" :size="20" />
      </span>
      <div class="min-w-0 flex-1">
        <div class="channel-signal-card__name truncate text-base font-semibold text-highlighted">{{ groupLabel }}</div>
        <div class="channel-signal-card__meta mt-1 flex min-w-0 flex-wrap items-center gap-1.5">
          <span v-if="showPlatformBadge" class="rounded-md px-1.5 py-0.5 text-[10px] font-medium" :class="providerBadgeClass(row.platform)">{{ providerLabel(row.platform) }}</span>
          <span class="rounded-md bg-primary/10 px-1.5 py-0.5 font-mono text-[10px] font-medium tabular-nums text-primary">{{ t('channelMonitorV3.userRate') }} {{ formattedUserRate }}</span>
          <GroupRateSignals :signals="signals" />
        </div>
      </div>
      <GroupWatchBellButton
        v-if="showWatchBell && row.group_id != null && row.group_id > 0"
        class="ml-auto shrink-0"
        :platform="row.platform"
        :subscribed="subscribed"
        :pending="watchPending"
        :group-name="groupLabel"
        @toggle="emit('toggle-watch', row.group_id)"
      />
    </header>

    <div class="channel-signal-card__metrics mt-5 grid grid-cols-3 gap-2">
      <div class="channel-signal-card__metric rounded-2xl border border-default bg-muted/80 p-3">
        <div class="text-[10px] font-semibold uppercase tracking-wider text-dimmed">{{ t('channelMonitorV3.cacheRate') }}</div>
        <div class="mt-1.5 font-mono text-lg font-bold tabular-nums" :class="cacheRateClass">{{ cacheRate }}</div>
      </div>
      <div class="channel-signal-card__metric rounded-2xl border border-default bg-muted/80 p-3">
        <div class="text-[10px] font-semibold uppercase tracking-wider text-dimmed">{{ t('channelMonitorV3.successRate') }}</div>
        <div class="mt-1.5 font-mono text-lg font-bold tabular-nums" :class="availabilityClass">{{ successRate }}</div>
      </div>
      <div class="channel-signal-card__metric rounded-2xl border border-default bg-muted/80 p-3">
        <div class="text-[10px] font-semibold uppercase tracking-wider text-dimmed">{{ t('channelMonitorV3.ttft') }}</div>
        <div class="mt-1.5 font-mono text-lg font-bold tabular-nums text-highlighted">{{ ttft }}</div>
      </div>
    </div>

    <ChannelMonitorV3Timeline
      class="channel-signal-card__timeline mt-4"
      :buckets="row.buckets"
      :countdown-seconds="countdownSeconds"
      :length="timelineLength"
      :anchor-bucket-start="anchorBucketStart"
      :bucket-seconds="bucketSeconds"
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
import { availabilityTextClass, cacheTextClass, formatMonitorMs, formatMonitorPercent } from '@/features/channel-monitor-v2/monitorFormat'
import type { GroupSignalSource } from '@/utils/groupRateSignals'
import { providerGradient, useChannelMonitorFormat } from '@/composables/useChannelMonitorFormat'
import GroupRateSignals from '@/components/common/GroupRateSignals.vue'
import ProviderIcon from './ProviderIcon.vue'
import GroupWatchBellButton from './GroupWatchBellButton.vue'
import ChannelMonitorV3Timeline from './ChannelMonitorV3Timeline.vue'
import ChannelMonitorV3QualityHistory from './ChannelMonitorV3QualityHistory.vue'

const props = withDefaults(defineProps<{
  row: MonitorMatrixRow
  countdownSeconds: number
  timelineLength: number
  anchorBucketStart?: string
  bucketSeconds?: number
  userRateMultiplier?: number | null
  showPlatformBadge?: boolean
  /** Display hints of the group (recent rate change, new group) from GET /groups/available. */
  signals?: GroupSignalSource | null
  showWatchBell?: boolean
  subscribed?: boolean
  watchPending?: boolean
}>(), {
  showPlatformBadge: true,
  signals: null,
  showWatchBell: true,
  subscribed: false,
  watchPending: false,
})

const emit = defineEmits<{
  'toggle-watch': [groupId: number]
}>()
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
const cacheRate = computed(() => formatMonitorPercent(latestMetrics.value.cache_rate))
const cacheRateClass = computed(() => cacheTextClass(latestMetrics.value.cache_rate))
const availabilityPercent = computed(() => (1 - latestMetrics.value.error_rate) * 100)
const successRate = computed(() => formatMonitorPercent(availabilityPercent.value / 100))
const availabilityClass = computed(() => availabilityTextClass(availabilityPercent.value))
const ttft = computed(() => formatMonitorMs(latestMetrics.value.ttft.p50_ms))
</script>
