<template>
  <section class="flex flex-col gap-4" data-testid="chain-status-panel">
    <div class="flex flex-wrap items-center gap-x-4 gap-y-1 text-sm" data-testid="chain-status-summary">
      <template v-if="report">
        <span class="text-highlighted">{{ t('admin.crypto.chainStatus.summaryEnabled', { count: report.summary.enabled }) }}</span>
        <span :class="report.summary.lagging ? 'text-info' : 'text-muted'">{{ t('admin.crypto.chainStatus.summaryLagging', { count: report.summary.lagging }) }}</span>
        <span :class="report.summary.stalled ? 'text-warning' : 'text-muted'">{{ t('admin.crypto.chainStatus.summaryStalled', { count: report.summary.stalled }) }}</span>
        <span :class="report.summary.error ? 'text-error' : 'text-muted'">{{ t('admin.crypto.chainStatus.summaryError', { count: report.summary.error }) }}</span>
        <span v-if="disabledCount" class="font-medium text-warning" data-testid="chain-status-disabled-count">{{ t('admin.crypto.chainStatus.summaryDisabled', { count: disabledCount }) }}</span>
      </template>
      <span class="ms-auto flex items-center gap-2 text-xs text-muted">
        <UBadge v-if="stale" color="warning" variant="subtle" data-testid="chain-status-stale">{{ t('admin.crypto.chainStatus.stale') }}</UBadge>
        <span v-if="updatedClock" data-testid="chain-status-updated">{{ t('admin.crypto.chainStatus.updatedAt', { time: updatedClock }) }}</span>
      </span>
    </div>

    <div v-if="loading && !report" class="flex justify-center py-12"><LoadingSpinner /></div>
    <UAlert
      v-else-if="failed && !report"
      role="alert"
      color="error"
      variant="subtle"
      icon="i-lucide-circle-alert"
      data-testid="chain-status-error"
      :title="t('admin.crypto.chainStatus.loadFailed')"
      :actions="[{ label: t('common.refresh'), color: 'neutral', variant: 'outline', onClick: () => { void load() } }]"
    />
    <p v-else-if="report && !report.chains.length" class="text-sm text-muted" role="status">{{ t('admin.crypto.chainStatus.noChains') }}</p>

    <div v-else-if="report" class="flex flex-col gap-6">
      <article
        v-for="chain in report.chains"
        :key="chain.chain"
        class="flex flex-col gap-2.5"
        :data-testid="`chain-row-${chain.chain}`"
        :data-state="chain.state"
      >
        <div class="flex flex-wrap items-center gap-x-3 gap-y-1" :class="chain.enabled ? '' : 'opacity-60'">
          <h3 class="text-base font-semibold text-highlighted">{{ chainName(chain.chain) }}</h3>
          <UBadge :color="stateColor(chain.state)" variant="subtle" data-testid="chain-state">{{ stateLabel(chain.state) }}</UBadge>
          <span class="ms-auto text-xs" :class="chain.state === 'stalled' ? 'text-warning' : 'text-muted'" data-testid="chain-cursor-age">{{ cursorAge(chain) }}</span>
        </div>

        <UAlert
          v-if="!chain.enabled"
          role="alert"
          color="warning"
          variant="subtle"
          icon="i-lucide-triangle-alert"
          data-testid="chain-disabled-warning"
          :title="t('admin.crypto.chainStatus.disabledWarning')"
        >
          <template #actions>
            <UButton color="warning" variant="link" size="xs" data-testid="chain-open-config" @click="emit('open-config')">{{ t('admin.crypto.chainStatus.openConfig') }}</UButton>
          </template>
        </UAlert>

        <div class="flex flex-col gap-2.5" :class="chain.enabled ? '' : 'opacity-60'">
          <div class="flex items-center gap-3">
            <div
              class="chain-bar"
              role="progressbar"
              aria-valuemin="0"
              aria-valuemax="100"
              :aria-valuenow="barValueNow(chain)"
              :aria-label="t('admin.crypto.chainStatus.progressLabel', { chain: chainName(chain.chain) })"
              :data-state="chain.state"
              data-testid="chain-bar"
            >
              <div class="chain-bar__fill" :style="{ width: barWidth(chain) }" data-testid="chain-bar-fill" />
            </div>
            <span class="w-16 shrink-0 text-end text-sm tabular-nums text-highlighted" data-testid="chain-pct">{{ pctText(chain) }}</span>
          </div>

          <p v-if="reasonText(chain)" class="text-sm text-error" role="status" data-testid="chain-reason">{{ reasonText(chain) }}</p>

          <p class="flex flex-wrap gap-x-4 gap-y-1 text-sm text-muted" data-testid="chain-stats">
            <span v-for="item in statItems(chain)" :key="item.key" :class="item.warn ? 'text-warning' : ''" :data-stat="item.key">{{ item.text }}</span>
          </p>

          <ul class="flex flex-col gap-1">
            <li v-for="node in chain.nodes" :key="node.label" class="flex flex-wrap items-center gap-x-3 gap-y-0.5 text-sm" :data-testid="`node-${node.label}`" :data-status="node.status">
              <span class="size-2 shrink-0 rounded-full" :class="dotClass(node)" aria-hidden="true" />
              <span class="sr-only">{{ nodeStatusLabel(node.status) }}</span>
              <span class="font-mono text-xs" :class="isBadNode(node) ? 'text-error' : 'text-default'" :title="node.label">{{ node.host || node.label }}</span>
              <span v-if="node.host && nodeIndex(node)" class="-ms-1.5 text-xs tabular-nums text-dimmed" data-testid="node-index">#{{ nodeIndex(node) }}</span>
              <span class="tabular-nums" :class="node.status === 'slow' ? 'text-warning' : 'text-muted'" data-testid="node-latency">{{ latencyText(node) }}</span>
              <span v-if="node.behind" class="text-warning" data-testid="node-behind">{{ t('admin.crypto.chainStatus.behindBlocks', { n: fmt(node.behind) }) }}</span>
              <span v-if="node.error_kind" class="text-error" data-testid="node-error">{{ errorKindLabel(node.error_kind) }}</span>
            </li>
          </ul>
        </div>
      </article>
    </div>
  </section>
</template>
<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import { getChainStatus, type ChainNodeStatus, type ChainState, type ChainStatus, type ChainStatusReport } from '@/api/admin/crypto'

// Polls only while the tab is the active one AND the page is visible. Leaving either
// clears the timer and drops any request still in flight, so a hidden page costs the
// server nothing.
const props = withDefaults(defineProps<{ active?: boolean }>(), { active: true })
const emit = defineEmits<{ (event: 'open-config'): void }>()
const { t } = useI18n()

const POLL_MS = 3000
const report = ref<ChainStatusReport | null>(null)
const loading = ref(false)
const failed = ref(false)
const pageVisible = ref(typeof document === 'undefined' || document.visibilityState !== 'hidden')

let timer: ReturnType<typeof setInterval> | undefined
let generation = 0
let inFlight = false

async function load() {
  if (inFlight) return
  inFlight = true
  const mine = generation
  loading.value = true
  try {
    const next = await getChainStatus()
    if (mine !== generation) return
    report.value = next
    failed.value = false
  } catch {
    // Keep the last good report on screen and say it is out of date.
    if (mine === generation) failed.value = true
  } finally {
    if (mine === generation) {
      inFlight = false
      loading.value = false
    }
  }
}

function stopPolling() {
  if (timer !== undefined) {
    clearInterval(timer)
    timer = undefined
  }
  generation++
  inFlight = false
  loading.value = false
}

function startPolling() {
  if (timer !== undefined) return
  void load()
  timer = setInterval(() => { void load() }, POLL_MS)
}

const shouldPoll = computed(() => props.active && pageVisible.value)
watch(shouldPoll, on => { if (on) startPolling(); else stopPolling() }, { immediate: true })

function onVisibility() {
  pageVisible.value = document.visibilityState !== 'hidden'
}
onMounted(() => {
  document.addEventListener('visibilitychange', onVisibility)
  onVisibility()
})
onUnmounted(() => {
  document.removeEventListener('visibilitychange', onVisibility)
  stopPolling()
})

const stale = computed(() => failed.value && !!report.value)
const disabledCount = computed(() => report.value?.chains.filter(c => !c.enabled).length ?? 0)
const updatedClock = computed(() => {
  const at = report.value?.generated_at
  if (!at) return ''
  const d = new Date(at)
  if (Number.isNaN(d.getTime())) return ''
  const p = (n: number) => String(n).padStart(2, '0')
  return `${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`
})

const CHAIN_NAMES: Record<string, string> = {
  bsc: 'BSC', ethereum: 'Ethereum', polygon: 'Polygon', plasma: 'Plasma',
  arbitrum: 'Arbitrum', base: 'Base', xlayer: 'X Layer'
}
const chainName = (chain: string) => CHAIN_NAMES[chain] ?? chain.toUpperCase()
const fmt = (n: number) => n.toLocaleString('en-US')

function stateLabel(state: ChainState): string {
  switch (state) {
    case 'synced': return t('admin.crypto.chainStatus.stateSynced')
    case 'catching_up': return t('admin.crypto.chainStatus.stateCatchingUp')
    case 'stalled': return t('admin.crypto.chainStatus.stateStalled')
    case 'error': return t('admin.crypto.chainStatus.stateError')
    case 'no_rpc': return t('admin.crypto.chainStatus.stateNoRpc')
    case 'disabled': return t('admin.crypto.chainStatus.stateDisabled')
    default: return String(state)
  }
}

function stateColor(state: ChainState): 'success' | 'info' | 'warning' | 'error' | 'neutral' {
  switch (state) {
    case 'synced': return 'success'
    case 'catching_up': return 'info'
    case 'stalled': return 'warning'
    case 'error':
    case 'no_rpc': return 'error'
    default: return 'neutral'
  }
}

function nodeStatusLabel(status: ChainNodeStatus['status']): string {
  switch (status) {
    case 'ok': return t('admin.crypto.chainStatus.nodeOk')
    case 'slow': return t('admin.crypto.chainStatus.nodeSlow')
    case 'behind': return t('admin.crypto.chainStatus.nodeBehind')
    case 'error': return t('admin.crypto.chainStatus.nodeError')
    case 'wrong_chain': return t('admin.crypto.chainStatus.nodeWrongChain')
    default: return String(status)
  }
}

function errorKindLabel(kind: string): string {
  switch (kind) {
    case 'timeout': return t('admin.crypto.chainStatus.errTimeout')
    case 'connection': return t('admin.crypto.chainStatus.errConnection')
    case 'http_429': return t('admin.crypto.chainStatus.errHttp429')
    case 'http_5xx': return t('admin.crypto.chainStatus.errHttp5xx')
    case 'http_4xx': return t('admin.crypto.chainStatus.errHttp4xx')
    case 'rpc_error': return t('admin.crypto.chainStatus.errRpc')
    case 'bad_response': return t('admin.crypto.chainStatus.errBadResponse')
    case 'wrong_chain': return t('admin.crypto.chainStatus.errWrongChain')
    default: return kind
  }
}

const isBadNode = (node: ChainNodeStatus) => node.status === 'error' || node.status === 'wrong_chain'

// Two keys of one provider share a host name, so the list position ("blockpi#1" -> 1) tells them apart.
const nodeIndex = (node: ChainNodeStatus) => (node.label.includes('#') ? node.label.slice(node.label.lastIndexOf('#') + 1) : '')

function dotClass(node: ChainNodeStatus): string {
  if (node.status === 'ok') return 'bg-green-500'
  if (node.status === 'slow' || node.status === 'behind') return 'bg-amber-500'
  return 'bg-red-500'
}

const latencyText = (node: ChainNodeStatus) =>
  node.latency_ms === null ? '—' : t('admin.crypto.chainStatus.latencyMs', { ms: fmt(node.latency_ms) })

const speedText = (v: number | null) =>
  v === null ? t('admin.crypto.chainStatus.calculating')
    : t('admin.crypto.chainStatus.perMinute', { value: v >= 10 ? fmt(Math.round(v)) : String(v) })

function etaDuration(seconds: number): string {
  if (seconds < 60) return t('admin.crypto.chainStatus.etaLessThanMinute')
  const minutes = Math.round(seconds / 60)
  if (minutes < 60) return t('admin.crypto.chainStatus.etaMinutes', { n: minutes })
  const hours = Math.round(minutes / 60)
  if (hours < 48) return t('admin.crypto.chainStatus.etaHours', { n: hours })
  return t('admin.crypto.chainStatus.etaDays', { n: Math.round(hours / 24) })
}

function cursorAge(chain: ChainStatus): string {
  const s = chain.stalled_seconds
  if (s === null) return chain.last_error?.stage === 'cursor' ? '' : t('admin.crypto.chainStatus.cursorNone')
  if (s < 60) return t('admin.crypto.chainStatus.cursorAgeSeconds', { n: s })
  if (s < 3600) return t('admin.crypto.chainStatus.cursorAgeMinutes', { n: Math.floor(s / 60) })
  if (s < 86400) return t('admin.crypto.chainStatus.cursorAgeHours', { n: Math.floor(s / 3600) })
  return t('admin.crypto.chainStatus.cursorAgeDays', { n: Math.floor(s / 86400) })
}

interface StatItem { key: string; text: string; warn?: boolean }

function etaText(chain: ChainStatus): string {
  if (chain.eta_seconds !== null) return t('admin.crypto.chainStatus.eta', { time: etaDuration(chain.eta_seconds) })
  // Both speeds known but no finite ETA: the scanner is not outrunning the chain.
  if (chain.scan_speed_bpm !== null && chain.head_speed_bpm !== null) return t('admin.crypto.chainStatus.cannotCatchUp')
  return t('admin.crypto.chainStatus.etaCalculating')
}

// One line under the bar. Figures that need history (speeds, ETA) read "calculating"
// until the server has enough samples; figures that failed to load read "—".
function statItems(chain: ChainStatus): StatItem[] {
  const items: StatItem[] = []
  if (chain.cursor !== null && chain.head !== null) {
    items.push({ key: 'scanned', text: t('admin.crypto.chainStatus.scannedOf', { cursor: fmt(chain.cursor), head: fmt(chain.head) }) })
  }
  if (chain.unscanned !== null) {
    items.push({ key: 'remaining', text: t('admin.crypto.chainStatus.remaining', { count: fmt(chain.unscanned) }) })
  }
  if (chain.state === 'catching_up') items.push({ key: 'eta', text: etaText(chain) })
  if (chain.head !== null) {
    items.push({ key: 'scanSpeed', text: t('admin.crypto.chainStatus.scanSpeed', { value: speedText(chain.scan_speed_bpm) }) })
    items.push({ key: 'headSpeed', text: t('admin.crypto.chainStatus.headSpeed', { value: speedText(chain.head_speed_bpm) }) })
  }
  const pending = chain.pending_deposits
  items.push(pending === null
    ? { key: 'pending', text: t('admin.crypto.chainStatus.pendingUnknown') }
    : { key: 'pending', text: t('admin.crypto.chainStatus.pending', { count: fmt(pending) }), warn: pending > 0 && chain.state !== 'synced' })
  return items
}

function barWidth(chain: ChainStatus): string {
  if (chain.state === 'synced' || chain.state === 'error' || chain.state === 'no_rpc') return '100%'
  return chain.progress_pct === null ? '0%' : `${Math.min(100, Math.max(0, chain.progress_pct))}%`
}

function barValueNow(chain: ChainStatus): number | undefined {
  if (chain.state === 'synced') return 100
  if (chain.state === 'error' || chain.state === 'no_rpc') return undefined
  return chain.progress_pct ?? undefined
}

function pctText(chain: ChainStatus): string {
  if (chain.state === 'synced') return '100%'
  if (chain.state === 'error' || chain.state === 'no_rpc') return '—'
  if (chain.progress_pct === null) return chain.state === 'catching_up' ? t('admin.crypto.chainStatus.calculating') : '—'
  return `${chain.progress_pct}%`
}

function reasonText(chain: ChainStatus): string {
  if (chain.state === 'no_rpc') return t('admin.crypto.chainStatus.reasonNoRpc')
  if (chain.state !== 'error') return ''
  const err = chain.last_error
  if (err?.stage === 'cursor') return t('admin.crypto.chainStatus.reasonCursor')
  const base = t('admin.crypto.chainStatus.reasonHead')
  return err ? `${base} · ${errorKindLabel(err.kind)}` : base
}
</script>
<style scoped>
.chain-bar {
  --bar: var(--color-green-500, #22c55e);
  position: relative;
  flex: 1 1 auto;
  min-width: 0;
  height: 0.5rem;
  overflow: hidden;
  border-radius: 9999px;
  background-color: color-mix(in oklab, var(--ui-text-muted, #94a3b8) 22%, transparent);
}
.chain-bar__fill {
  height: 100%;
  border-radius: inherit;
  background-color: var(--bar);
  transition: width 0.4s ease;
}
.chain-bar[data-state='catching_up'] { --bar: var(--color-blue-500, #3b82f6); }
.chain-bar[data-state='stalled'] { --bar: var(--color-orange-500, #f97316); }
.chain-bar[data-state='error'],
.chain-bar[data-state='no_rpc'] { --bar: var(--color-red-500, #ef4444); }
.chain-bar[data-state='disabled'] { --bar: var(--color-neutral-400, #a3a3a3); }
.chain-bar[data-state='error'] .chain-bar__fill,
.chain-bar[data-state='no_rpc'] .chain-bar__fill { opacity: 0.45; }
/* Downloader-style flowing stripes, only while the scanner is catching up. */
.chain-bar[data-state='catching_up'] .chain-bar__fill {
  background-image: linear-gradient(
    135deg,
    rgb(255 255 255 / 0.28) 25%, transparent 25%, transparent 50%,
    rgb(255 255 255 / 0.28) 50%, rgb(255 255 255 / 0.28) 75%, transparent 75%
  );
  background-size: 1rem 1rem;
  animation: chain-bar-flow 1s linear infinite;
}
@keyframes chain-bar-flow { to { background-position: 1rem 0; } }
@media (prefers-reduced-motion: reduce) {
  .chain-bar__fill { transition: none; }
  .chain-bar[data-state='catching_up'] .chain-bar__fill { animation: none; }
}
</style>
