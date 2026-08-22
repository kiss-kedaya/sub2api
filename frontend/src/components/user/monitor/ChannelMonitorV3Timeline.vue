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
        @mouseenter="setHoveredBar(index, $event)"
      >
        <button
          type="button"
          class="v3-bar-hitbox"
          :class="{
            'is-active': hoveredBarIndex === index,
            'is-neighbor': barDistance(index) === 1,
            'is-pressed': hoveredBarIndex !== null && barDistance(index) > 0,
          }"
          :aria-label="bar.title || '-'"
          @focus="setHoveredBar(index, $event)"
          @blur="clearHoveredBar"
        >
          <span class="v3-bar-visual" :style="barMotionStyle(index)" aria-hidden="true">
            <span
              class="v3-soft-glass-bar"
              :class="bar.colorClass"
              :style="{ height: `${bar.heightPct}%`, animationDelay: `${index * 18}ms` }"
            />
          </span>
        </button>

        <Teleport to="body">
          <Transition name="v3-timeline-tooltip">
            <div
              v-if="hoveredBarIndex === index && bar.title"
              class="v3-timeline-tooltip"
              role="tooltip"
              :style="tooltipStyle"
            >
              {{ bar.title }}
            </div>
          </Transition>
        </Teleport>
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
const tooltipPosition = ref({ left: 0, top: 0, x: '-50%' })

function setHoveredBar(index: number, event?: Event) {
  hoveredBarIndex.value = index
  const target = event?.currentTarget
  if (!(target instanceof HTMLElement) || typeof window === 'undefined') return

  const rect = target.getBoundingClientRect()
  const viewportGutter = 16
  const maxTooltipWidth = Math.min(280, window.innerWidth - viewportGutter * 2)
  const center = rect.left + rect.width / 2
  if (center + maxTooltipWidth / 2 > window.innerWidth - viewportGutter) {
    tooltipPosition.value = { left: window.innerWidth - viewportGutter, top: rect.top - 8, x: '-100%' }
  } else if (center - maxTooltipWidth / 2 < viewportGutter) {
    tooltipPosition.value = { left: viewportGutter, top: rect.top - 8, x: '0%' }
  } else {
    tooltipPosition.value = { left: center, top: rect.top - 8, x: '-50%' }
  }
}

function clearHoveredBar() {
  hoveredBarIndex.value = null
}

function barDistance(index: number) {
  return hoveredBarIndex.value === null ? 0 : Math.abs(index - hoveredBarIndex.value)
}

function barMotionStyle(index: number) {
  const distance = barDistance(index)
  if (hoveredBarIndex.value === null) {
    return {
      '--bar-scale': '1',
      '--bar-opacity': '1',
      '--bar-lift': '0px',
    }
  }

  // Keep the hit target fixed. Only the visual layer responds, so its
  // transformed bounds can never move the pointer between neighboring bars.
  const pressure = Math.exp(-distance / 2.8)
  const scaleY = distance === 0 ? 1.1 : 1 - 0.06 * pressure
  const opacity = distance === 0 ? 1 : 0.8 + (0.2 * (1 - pressure))
  return {
    // Keep every slot's horizontal footprint fixed so edge bars cannot expand
    // the page or move the pointer between neighboring hit targets.
    '--bar-scale-x': '1',
    '--bar-scale-y': scaleY.toFixed(3),
    '--bar-opacity': opacity.toFixed(3),
    '--bar-lift': distance === 0 ? '-1px' : '0px',
  }
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

const tooltipStyle = computed(() => ({
  '--tooltip-left': `${tooltipPosition.value.left}px`,
  '--tooltip-top': `${tooltipPosition.value.top}px`,
  '--tooltip-x': tooltipPosition.value.x,
}))
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
	position: relative;
	height: 20px;
	width: 100%;
	gap: 4px;
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
	appearance: none;
}

.v3-bar-visual {
	display: flex;
	align-items: flex-end;
	width: 100%;
	height: 100%;
	transform: translateY(var(--bar-lift, 0px)) scaleX(var(--bar-scale-x, 1)) scaleY(var(--bar-scale-y, 1));
	transform-origin: center bottom;
	opacity: var(--bar-opacity, 1);
	pointer-events: none;
	will-change: transform, opacity;
	transition: transform 240ms cubic-bezier(0.22, 1, 0.36, 1), opacity 220ms ease;
}

.v3-bar-hitbox:focus-visible {
	outline: 2px solid rgb(59 130 246 / 0.6);
	outline-offset: 2px;
	border-radius: 4px;
}

.v3-bar-hitbox.is-active {
	z-index: 3;
}

.v3-bar-hitbox.is-active .v3-soft-glass-bar {
	filter: saturate(1.12) brightness(1.05);
	box-shadow: 0 4px 10px rgb(15 118 110 / 0.24);
}

.v3-bar-hitbox.is-pressed {
	z-index: 2;
}

.v3-timeline-tooltip {
	position: fixed;
	left: var(--tooltip-left, 50%);
	top: var(--tooltip-top, 0px);
	z-index: 50;
	width: max-content;
	max-width: min(280px, calc(100vw - 32px));
	transform: translateX(var(--tooltip-x, -50%)) translateY(-100%);
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
	transform: translateX(var(--tooltip-x, -50%)) translateY(calc(-100% + 3px)) scale(0.96);
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
