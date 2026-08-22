<template>
  <div class="mt-4 border-t border-white/70 pt-3 dark:border-dark-700/60">
    <div class="mb-2 flex justify-between text-[10px] font-semibold uppercase tracking-widest text-gray-400">
      <span>{{ t('monitorCommon.history60pts', { n: length }) }}</span>
      <span class="tabular-nums">{{ t('monitorCommon.nextUpdateIn', { n: countdownSeconds }) }}</span>
    </div>

    <div class="v3-timeline-bars" @mouseleave="clearHoveredBar">
      <div
        v-for="(bar, index) in displayBars"
        :key="bar.key"
        class="v3-bar-slot"
        @mouseenter="setHoveredBar(index)"
        @mouseleave="clearHoveredBar"
      >
        <button
          type="button"
          class="v3-bar-hitbox"
          :class="{
            'is-active': hoveredBarIndex === index,
            'is-neighbor': isNeighbor(index),
          }"
          :aria-label="bar.title || '-'"
          @focus="setHoveredBar(index)"
          @blur="clearHoveredBar"
        >
          <span
            class="v3-soft-glass-bar"
            :class="bar.colorClass"
            :style="{ height: `${bar.heightPct}%`, animationDelay: `${index * 18}ms` }"
            aria-hidden="true"
          />
        </button>

        <Transition name="v3-timeline-tooltip">
          <div
            v-if="hoveredBarIndex === index && bar.title"
            class="v3-timeline-tooltip"
            role="tooltip"
          >
            {{ bar.title }}
          </div>
        </Transition>
      </div>
    </div>

    <div class="mt-1 flex justify-between text-[9px] uppercase tracking-widest text-gray-400">
      <span>{{ t('monitorCommon.past') }}</span>
      <span>{{ t('monitorCommon.now') }}</span>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
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
const hoveredBarIndex = ref<number | null>(null)

function setHoveredBar(index: number) {
  hoveredBarIndex.value = index
}

function clearHoveredBar() {
  hoveredBarIndex.value = null
}

function isNeighbor(index: number) {
  return hoveredBarIndex.value !== null && Math.abs(index - hoveredBarIndex.value) === 1
}

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
	display: block;
	width: 100%;
	min-height: 3px;
	border-radius: 3px;
  transform-origin: bottom;
  animation: v3-soft-glass-rise 0.7s cubic-bezier(0.22, 1, 0.36, 1) both;
}

.v3-timeline-bars {
	display: flex;
	align-items: flex-end;
	gap: 3px;
	height: 20px;
	width: 100%;
	isolation: isolate;
}

.v3-bar-slot {
	position: relative;
	display: flex;
	align-items: flex-end;
	min-width: 0;
	height: 100%;
	flex: 1 1 0%;
}

.v3-bar-hitbox {
	position: relative;
	display: flex;
	align-items: flex-end;
	width: 100%;
	height: 100%;
	min-width: 0;
	padding: 0;
	border: 0;
	background: transparent;
	cursor: crosshair;
	transform: scaleX(1);
	transition: transform 140ms cubic-bezier(0.22, 1, 0.36, 1), opacity 140ms ease;
}

.v3-bar-hitbox:focus-visible {
	outline: 2px solid rgb(59 130 246 / 0.6);
	outline-offset: 2px;
	border-radius: 4px;
}

.v3-bar-hitbox.is-active {
	z-index: 3;
	transform: translateY(-1px) scaleX(1.42);
}

.v3-bar-hitbox.is-active .v3-soft-glass-bar {
	filter: saturate(1.12) brightness(1.05);
	box-shadow: 0 4px 10px rgb(15 118 110 / 0.24);
}

.v3-bar-hitbox.is-neighbor {
	z-index: 2;
	opacity: 0.72;
	transform: scaleX(0.74);
}

.v3-timeline-tooltip {
	position: absolute;
	left: 50%;
	bottom: calc(100% + 8px);
	z-index: 10;
	width: max-content;
	max-width: min(250px, 72vw);
	transform: translateX(-50%);
	border: 1px solid rgb(255 255 255 / 0.84);
	border-radius: 9px;
	background: rgb(15 23 42 / 0.92);
	padding: 6px 9px;
	color: rgb(248 250 252);
	font-size: 10px;
	font-weight: 600;
	line-height: 1.35;
	letter-spacing: 0;
	white-space: normal;
	box-shadow: 0 10px 24px rgb(15 23 42 / 0.2);
	pointer-events: none;
}

.v3-timeline-tooltip::after {
	position: absolute;
	left: 50%;
	bottom: -4px;
	width: 7px;
	height: 7px;
	transform: translateX(-50%) rotate(45deg);
	border-right: 1px solid rgb(255 255 255 / 0.84);
	border-bottom: 1px solid rgb(255 255 255 / 0.84);
	background: rgb(15 23 42 / 0.92);
	content: '';
}

.v3-timeline-tooltip-enter-active,
.v3-timeline-tooltip-leave-active {
	transition: opacity 100ms ease, transform 120ms cubic-bezier(0.22, 1, 0.36, 1);
}

.v3-timeline-tooltip-enter-from,
.v3-timeline-tooltip-leave-to {
	opacity: 0;
	transform: translateX(-50%) translateY(3px) scale(0.96);
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

  .v3-bar-hitbox,
  .v3-timeline-tooltip-enter-active,
  .v3-timeline-tooltip-leave-active {
    transition: none;
  }
}
</style>
