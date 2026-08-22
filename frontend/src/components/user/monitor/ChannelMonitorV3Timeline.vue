<template>
  <div class="mt-4 border-t border-white/70 pt-3 dark:border-dark-700/60">
    <div class="mb-2 flex justify-between text-[10px] font-semibold uppercase tracking-widest text-gray-400">
      <span>{{ t('monitorCommon.history60pts', { n: length }) }}</span>
      <span class="tabular-nums">{{ t('monitorCommon.nextUpdateIn', { n: countdownSeconds }) }}</span>
    </div>

    <div class="flex h-5 w-full items-end gap-[3px]">
      <div
        v-for="(bar, index) in displayBars"
        :key="bar.key"
        class="v3-soft-glass-bar min-w-0 flex-1 rounded"
        :class="bar.colorClass"
        :style="{ height: `${bar.heightPct}%`, animationDelay: `${index * 18}ms` }"
        :title="bar.title"
      />
    </div>

    <div class="mt-1 flex justify-between text-[9px] uppercase tracking-widest text-gray-400">
      <span>{{ t('monitorCommon.past') }}</span>
      <span>{{ t('monitorCommon.now') }}</span>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { MonitorMatrixBucket } from '@/api/channelMonitorV2'
import { availabilityBarClass, formatMonitorMs, formatMonitorPercent } from '@/features/channel-monitor-v2/monitorFormat'

const props = withDefaults(defineProps<{
  buckets?: MonitorMatrixBucket[]
  countdownSeconds: number
  length?: number
}>(), {
  buckets: () => [],
  length: 18,
})

const { t, locale } = useI18n()

const STATUS_STYLE = {
  healthy: { colorClass: 'bg-emerald-500', heightPct: 100 },
  warning: { colorClass: 'bg-amber-500', heightPct: 65 },
  critical: { colorClass: 'bg-red-500', heightPct: 35 },
  unknown: { colorClass: 'bg-gray-300 dark:bg-dark-600', heightPct: 15 },
} as const

interface TimelineBar {
  key: string
  colorClass: string
  heightPct: number
  title: string
}

function formatBucketTime(value: string) {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '-'
  return new Intl.DateTimeFormat(locale.value || undefined, {
    month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit',
  }).format(date)
}

const displayBars = computed<TimelineBar[]>(() => {
  const real = [...props.buckets]
    .sort((a, b) => Date.parse(a.bucket_start) - Date.parse(b.bucket_start))
    .slice(-props.length)
  const bars: TimelineBar[] = Array.from({ length: Math.max(0, props.length - real.length) }, (_, index) => ({
    key: `empty-${index}`,
    ...STATUS_STYLE.unknown,
    title: '',
  }))

  for (const bucket of real) {
    const state = bucket.health.overall === 'healthy' || bucket.health.overall === 'warning' || bucket.health.overall === 'critical'
      ? bucket.health.overall
      : 'unknown'
    const availabilityPercent = (1 - bucket.metrics.error_rate) * 100
    const style = {
      ...(STATUS_STYLE[state]),
      colorClass: availabilityBarClass(availabilityPercent),
    }
    bars.push({
      key: bucket.bucket_start,
      ...style,
      title: t('channelMonitorV3.timelineTooltip', {
        time: formatBucketTime(bucket.bucket_start),
        availability: formatMonitorPercent(1 - bucket.metrics.error_rate, locale.value || 'zh-CN'),
        cache: formatMonitorPercent(bucket.metrics.cache_rate, locale.value || 'zh-CN'),
        ttft: formatMonitorMs(bucket.metrics.ttft.p50_ms),
      }),
    })
  }
  return bars
})
</script>

<style scoped>
.v3-soft-glass-bar {
  transform-origin: bottom;
  animation: v3-soft-glass-rise 0.7s cubic-bezier(0.22, 1, 0.36, 1) both;
}

@keyframes v3-soft-glass-rise {
  from {
    transform: scaleY(0.15);
    opacity: 0.3;
  }
  to {
    transform: scaleY(1);
    opacity: 1;
  }
}

@media (prefers-reduced-motion: reduce) {
  .v3-soft-glass-bar {
    animation: none;
  }
}
</style>
