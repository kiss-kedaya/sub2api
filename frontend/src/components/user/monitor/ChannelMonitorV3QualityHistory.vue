<template>
  <div
    v-if="enabled && groupId"
    class="channel-signal-quality-history mt-3 border-t border-white/70 pt-3 dark:border-dark-700/60"
    data-testid="channel-quality-history"
  >
    <div class="channel-signal-timeline__legend mb-1.5 flex items-center justify-between text-[10px] font-semibold uppercase tracking-widest text-gray-400">
      <span>{{ t('monitorCommon.qualityHistoryTitle', { n: events.length }) }}</span>
      <span v-if="degradedCount" class="tabular-nums text-red-500/90 dark:text-red-400/90">
        {{ t('monitorCommon.qualityHistoryDegradedCount', { n: degradedCount }) }}
      </span>
    </div>

    <div v-if="loading" class="pb-1 text-[10px] text-gray-400">{{ t('monitorCommon.qualityHistoryLoading') }}</div>
    <div v-else-if="loadFailed" class="pb-1 text-[10px] text-red-500/90 dark:text-red-400/90">{{ t('monitorCommon.qualityHistoryFailed') }}</div>
    <div v-else-if="!events.length" class="pb-1 text-[10px] text-gray-400">{{ t('monitorCommon.qualityHistoryEmpty') }}</div>

    <div v-else class="quality-history-strip flex flex-wrap gap-1.5" @mouseleave="clearHover">
      <button
        v-for="(event, index) in events"
        :key="event.id"
        type="button"
        class="quality-history-chip"
        :class="[event.status === 'degraded' ? 'is-degraded' : 'is-pass', { 'is-active': hoveredIndex === index }]"
        :data-testid="`quality-history-chip-${event.id}`"
        :aria-label="chipLabel(event)"
        @mouseenter="hoverEvent(index, $event)"
        @focus="hoverEvent(index, $event)"
        @blur="clearHover"
      >
        <span class="quality-history-chip__dot" aria-hidden="true" />
        <span v-if="event.quality_provider === 'chanshui'" class="text-[9px]">{{ t('monitorCommon.qualityChanshui') }}</span>
        <span class="quality-history-chip__time tabular-nums">{{ formatChipTime(event.created_at) }}</span>
      </button>
    </div>

    <Teleport to="body">
      <Transition name="v3-timeline-tooltip">
        <div
          v-if="hovered && hoveredEvent"
          class="quality-history-popover"
          :style="popoverStyle"
          role="tooltip"
          data-testid="quality-history-popover"
        >
          <div class="quality-history-popover__head">
            <span
              class="font-semibold"
              :class="hoveredEvent.status === 'degraded' ? 'text-red-400' : 'text-emerald-400'"
            >
              {{ hoveredEvent.status === 'degraded' ? t('monitorCommon.qualityHistoryDegraded') : t('monitorCommon.qualityHistoryPass') }}
            </span>
            <span class="font-mono text-amber-300/90" data-testid="quality-history-event-id">#{{ hoveredEvent.id }}</span>
            <span>{{ t(hoveredEvent.quality_provider === 'chanshui' ? 'monitorCommon.qualityChanshui' : 'monitorCommon.qualityPelican') }}</span>
            <span class="text-gray-300">{{ formatFullTime(hoveredEvent.created_at) }}</span>
            <span v-if="hoveredEvent.model_id" class="truncate font-mono text-gray-400">{{ hoveredEvent.model_id }}</span>
          </div>
          <div
            v-if="hoveredEvent.status === 'degraded' && hoveredEvent.error_message"
            class="quality-history-popover__reason"
          >
            {{ hoveredEvent.error_message }}
          </div>
          <div v-if="hoveredEvent.quality_provider === 'chanshui'" class="space-y-2 p-3 text-xs text-gray-200" data-testid="quality-audit-summary">
            <div>{{ t('monitorCommon.qualityAuditTotal') }}: {{ hoveredEvent.audit_summary?.score ?? '—' }} / 100</div>
            <div>{{ t('monitorCommon.qualityAuditCandidate') }}: {{ hoveredEvent.audit_summary?.candidate_model || '—' }}</div>
            <div v-for="section in hoveredEvent.audit_summary?.sections || []" :key="section.name" class="flex justify-between gap-3">
              <span>{{ t(`monitorCommon.qualitySections.${section.name}`) }}</span>
              <span>{{ section.status || '—' }}<template v-if="section.score != null"> · {{ section.score }}</template></span>
            </div>
          </div>
          <div v-else class="quality-history-popover__viewport">
            <div v-if="artworkState === 'loading'" class="grid h-full place-items-center text-[10px] text-gray-400">
              {{ t('monitorCommon.qualityArtworkLoading') }}
            </div>
            <div v-else-if="artworkState === 'failed'" class="grid h-full place-items-center text-[10px] text-red-300">
              {{ t('monitorCommon.qualityArtworkFailed') }}
            </div>
            <iframe
              v-else-if="artworkState === 'ready' && artworkHtml"
              class="quality-history-popover__frame"
              :srcdoc="artworkHtml"
              sandbox="allow-scripts"
              title="quality-check artwork"
              data-testid="quality-history-artwork"
            />
          </div>
        </div>
      </Transition>
    </Teleport>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { getQualityArtwork, getQualityEvents, type MonitorQualityEvent } from '@/api/channelMonitorV2'

const props = withDefaults(defineProps<{
  groupId?: number
  enabled?: boolean
}>(), {
  groupId: undefined,
  enabled: false,
})

const { t, locale } = useI18n()
const events = ref<MonitorQualityEvent[]>([])
const loading = ref(false)
const loadFailed = ref(false)
const hovered = ref(false)
const hoveredIndex = ref<number | null>(null)
const popoverPosition = ref({ left: 0, top: 0, x: '-50%' })
const artworkHtml = ref('')
const artworkState = ref<'idle' | 'loading' | 'ready' | 'failed'>('idle')

const artworkCache = new Map<number, string>()
let artworkAbort: AbortController | null = null

const hoveredEvent = computed(() => (hoveredIndex.value === null ? null : events.value[hoveredIndex.value] ?? null))
const degradedCount = computed(() => events.value.filter((event) => event.status === 'degraded').length)
let loadGeneration = 0

async function load() {
  const generation = ++loadGeneration
  const groupId = props.groupId
  if (!props.enabled || !props.groupId) {
    events.value = []
    return
  }
  loading.value = true
  loadFailed.value = false
  try {
    const list = await getQualityEvents(groupId!, 30)
    if (generation !== loadGeneration) return
    events.value = Array.isArray(list) ? list : []
  } catch {
    if (generation !== loadGeneration) return
    loadFailed.value = true
    events.value = []
  } finally {
    if (generation === loadGeneration) loading.value = false
  }
}

watch([() => props.groupId, () => props.enabled], () => { void load() }, { immediate: true })

function formatChipTime(value: string) {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '--:--'
  return new Intl.DateTimeFormat(locale.value || undefined, { hour: '2-digit', minute: '2-digit' }).format(date)
}

function formatFullTime(value: string) {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '-'
  return new Intl.DateTimeFormat(locale.value || undefined, {
    month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit',
  }).format(date)
}

function chipLabel(event: MonitorQualityEvent) {
  const status = event.status === 'degraded' ? t('monitorCommon.qualityHistoryDegraded') : t('monitorCommon.qualityHistoryPass')
  return `#${event.id} · ${formatFullTime(event.created_at)} · ${status}`
}

function positionPopover(event?: Event) {
  const target = event?.currentTarget
  if (!(target instanceof HTMLElement) || typeof window === 'undefined') return
  const rect = target.getBoundingClientRect()
  const viewportGutter = 16
  const maxWidth = Math.min(360, window.innerWidth - viewportGutter * 2)
  const center = rect.left + rect.width / 2
  if (center + maxWidth / 2 > window.innerWidth - viewportGutter) {
    popoverPosition.value = { left: window.innerWidth - viewportGutter, top: rect.top - 8, x: '-100%' }
  } else if (center - maxWidth / 2 < viewportGutter) {
    popoverPosition.value = { left: viewportGutter, top: rect.top - 8, x: '0%' }
  } else {
    popoverPosition.value = { left: center, top: rect.top - 8, x: '-50%' }
  }
}

function hoverEvent(index: number, event?: Event) {
  positionPopover(event)
  hovered.value = true
  hoveredIndex.value = index
  const current = events.value[index]
  if (current) void loadArtwork(current)
}

function clearHover() {
  hovered.value = false
  hoveredIndex.value = null
  artworkAbort?.abort()
  artworkAbort = null
  artworkState.value = 'idle'
  artworkHtml.value = ''
}

async function loadArtwork(event: MonitorQualityEvent) {
  if (event.quality_provider === 'chanshui') {
    artworkAbort?.abort()
    artworkAbort = null
    artworkState.value = 'idle'
    artworkHtml.value = ''
    return
  }
  if (!props.groupId) return
  const cached = artworkCache.get(event.id)
  if (cached !== undefined) {
    artworkHtml.value = cached
    artworkState.value = cached ? 'ready' : 'failed'
    return
  }
  artworkAbort?.abort()
  const controller = new AbortController()
  artworkAbort = controller
  artworkState.value = 'loading'
  artworkHtml.value = ''
  try {
    const html = await getQualityArtwork(props.groupId, event.id, controller.signal)
    artworkCache.set(event.id, html)
    if (artworkAbort !== controller) return
    artworkHtml.value = html
    artworkState.value = html ? 'ready' : 'failed'
  } catch (error) {
    const name = (error as { name?: string } | null)?.name
    if (name === 'CanceledError' || name === 'AbortError') return
    if (artworkAbort !== controller) return
    artworkState.value = 'failed'
  }
}

let refreshTimer: ReturnType<typeof setInterval> | undefined
onMounted(() => { refreshTimer = setInterval(() => { if (props.enabled && !loading.value && !hovered.value) void load() }, 60000) })
onBeforeUnmount(() => {
  loadGeneration++
  if (refreshTimer) clearInterval(refreshTimer)
  artworkAbort?.abort()
})

const popoverStyle = computed(() => ({
  '--tooltip-left': `${popoverPosition.value.left}px`,
  '--tooltip-top': `${popoverPosition.value.top}px`,
  '--tooltip-x': popoverPosition.value.x,
}))
</script>

<style scoped>
.quality-history-strip {
  max-height: 3.6rem;
  overflow: auto;
}

.quality-history-chip {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  border: 1px solid rgb(148 163 184 / 0.35);
  border-radius: 999px;
  background: rgb(248 250 252 / 0.9);
  padding: 2px 7px;
  font-size: 10px;
  font-weight: 600;
  color: rgb(71 85 105);
  cursor: crosshair;
  transition: transform 160ms cubic-bezier(0.22, 1, 0.36, 1), box-shadow 160ms ease;
}

.quality-history-chip.is-active {
  transform: translateY(-1px);
  box-shadow: 0 4px 10px rgb(15 118 110 / 0.18);
}

.quality-history-chip__dot {
  width: 6px;
  height: 6px;
  border-radius: 999px;
}

.quality-history-chip.is-pass .quality-history-chip__dot {
  background: rgb(16 185 129);
}

.quality-history-chip.is-degraded .quality-history-chip__dot {
  background: rgb(239 68 68);
}

.quality-history-chip.is-degraded {
  border-color: rgb(248 113 113 / 0.55);
  color: rgb(185 28 28);
}

.dark .quality-history-chip {
  border-color: rgb(71 85 105 / 0.6);
  background: rgb(15 23 42 / 0.55);
  color: rgb(203 213 225);
}

.quality-history-popover {
  position: fixed;
  left: var(--tooltip-left, 50%);
  top: var(--tooltip-top, 0px);
  z-index: 50;
  width: max-content;
  max-width: min(360px, calc(100vw - 32px));
  transform: translateX(var(--tooltip-x, -50%)) translateY(-100%);
  border: 1px solid rgb(255 255 255 / 0.14);
  border-radius: 10px;
  background: rgb(15 23 42 / 0.96);
  padding: 8px 10px;
  color: rgb(248 250 252);
  box-shadow: 0 14px 30px rgb(15 23 42 / 0.35);
  pointer-events: none;
}

.quality-history-popover__head {
  display: flex;
  align-items: baseline;
  gap: 8px;
  font-size: 10px;
  line-height: 1.4;
  white-space: nowrap;
}

.quality-history-popover__reason {
  margin-top: 4px;
  max-width: 320px;
  font-size: 10px;
  line-height: 1.45;
  color: rgb(252 165 165);
}

.quality-history-popover__viewport {
  position: relative;
  width: 320px;
  height: 213px;
  margin-top: 6px;
  overflow: hidden;
  border-radius: 8px;
  background: #fff;
}

.quality-history-popover__frame {
  width: 960px;
  height: 640px;
  border: 0;
  background: #fff;
  transform: scale(0.33333);
  transform-origin: top left;
}

@media (prefers-reduced-motion: reduce) {
  .quality-history-chip {
    transition: none;
  }
}
</style>
