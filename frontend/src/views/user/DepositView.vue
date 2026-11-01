<template>
  <AppLayout>
    <div class="deposit-view">
      <UserPageHeader class="deposit-view__header" flush :title="t('deposit.title')" :description="t('deposit.description')">
        <template #actions>
          <UBadge v-if="info?.enabled && !loadError" color="success" variant="subtle" icon="i-lucide-shield-check">{{ t('deposit.exclusiveAddress') }}</UBadge>
        </template>
      </UserPageHeader>

      <div v-if="loading" class="card deposit-view__empty" role="status"><LoadingSpinner /></div>
      <div v-else-if="loadError" class="card deposit-view__empty" role="alert">
        <strong>{{ t('deposit.loadFailed') }}</strong>
        <p>{{ t('deposit.loadFailedHint') }}</p>
        <button type="button" class="btn btn-secondary" @click="load">{{ t('deposit.retry') }}</button>
      </div>
      <div v-else-if="!info?.enabled" class="card deposit-view__empty">
        <Icon name="wallet" size="md" />
        <strong>{{ t('deposit.disabled') }}</strong>
        <p>{{ t('deposit.disabledHint') }}</p>
      </div>
      <template v-else>
        <div class="deposit-view__layout">
          <section class="card deposit-view__funding" :aria-label="t('deposit.yourAddress')">
            <div class="deposit-view__section-head">
              <span class="deposit-view__step">01</span>
              <h2>{{ t('deposit.chooseNetwork') }}</h2>
            </div>
            <div class="deposit-view__chains" :aria-label="t('deposit.chain')">
              <button v-for="c in info.chains" :key="c.chain" type="button" class="deposit-view__chain"
                :class="{ 'deposit-view__chain--active': selectedChain === c.chain }"
                :aria-pressed="selectedChain === c.chain" @click="selectedChain = c.chain">
                <span class="deposit-view__network-dot" aria-hidden="true"></span>
                {{ chainLabel(c.chain) }}
              </button>
            </div>
            <template v-if="current">
              <div class="deposit-view__section-head deposit-view__section-head--address">
                <span class="deposit-view__step">02</span>
                <h2>{{ t('deposit.transferToAddress') }}</h2>
                <span v-for="tk in current.tokens" :key="tk" class="deposit-view__token">{{ tk }}</span>
              </div>
              <div class="deposit-view__address-block">
                <div class="deposit-view__qr-wrap">
                  <div class="deposit-view__qr">
                    <canvas v-show="!qrError" ref="qrCanvas" role="img" :aria-label="t('deposit.qrLabel')"></canvas>
                    <span v-if="qrError">{{ t('deposit.qrFailed') }}</span>
                  </div>
                  <p>{{ t('deposit.scanAddress') }}</p>
                </div>
                <div class="deposit-view__address-info">
                  <span class="deposit-view__label">{{ t('deposit.yourAddress') }} · {{ chainLabel(current.chain) }}</span>
                  <code class="deposit-view__address">{{ current.address }}</code>
                  <button type="button" class="btn btn-primary deposit-view__copy" @click="copyAddress">
                    {{ copied ? t('deposit.copied') : t('deposit.copyAddress') }}
                  </button>
                  <p class="deposit-view__copy-status" role="status" aria-live="polite">{{ copyError ? t('deposit.copyFailed') : t('deposit.exclusiveNotice') }}</p>
                </div>
              </div>
              <div class="deposit-view__notice">
                <span class="deposit-view__notice-mark" aria-hidden="true">!</span>
                <p>{{ t('deposit.networkNotice', { network: chainLabel(current.chain), tokens: current.tokens.join(' / ') }) }}</p>
              </div>
            </template>
            <div v-else class="deposit-view__empty">{{ t('deposit.noNetworks') }}</div>
          </section>

          <aside class="card deposit-view__guide">
            <h2>{{ t('deposit.creditDetails') }}</h2>
            <p class="deposit-view__desc">{{ t('deposit.creditDetailsHint') }}</p>
            <dl class="deposit-view__terms">
              <div><dt>{{ t('deposit.rateLabel') }}</dt><dd>1 USDT <span>= {{ info.rate }} {{ t('deposit.balanceUnits') }}</span></dd></div>
              <div><dt>{{ t('deposit.minLabel') }}</dt><dd>{{ info.min_amount }} <span>USDT</span></dd></div>
              <div v-if="info.max_amount > 0"><dt>{{ t('deposit.maxLabel') }}</dt><dd>{{ info.max_amount }} <span>USDT</span></dd></div>
            </dl>
            <div class="deposit-view__confirmation">
              <span class="deposit-view__confirmation-dot" aria-hidden="true"></span>
              <div><strong>{{ t('deposit.autoCredit') }}</strong><p>{{ t('deposit.confirmationHint') }}</p></div>
            </div>
          </aside>
        </div>

        <section class="card deposit-view__history">
          <div class="deposit-view__history-head">
            <div><h2>{{ t('deposit.history') }}</h2><p class="deposit-view__desc">{{ pendingRows.length ? t('deposit.autoRefreshPending') : t('deposit.autoRefresh') }}</p></div>
            <button type="button" class="btn btn-secondary btn-sm" :disabled="refreshingHistory" @click="refreshHistory">
              {{ refreshingHistory ? t('common.loading') : t('common.refresh') }}
            </button>
          </div>
          <p v-if="historyError" class="deposit-view__history-error" role="alert">{{ t('deposit.historyFailed') }}</p>
          <div v-if="pendingRows.length" class="deposit-view__pending" role="status">
            <span class="deposit-view__pending-dot" aria-hidden="true"></span>
            <div class="deposit-view__pending-body">
              <strong>{{ t('deposit.pendingSummary', { n: pendingRows.length }) }}</strong>
              <ul class="deposit-view__pending-list">
                <li v-for="(item, i) in pendingRows.slice(0, PENDING_SUMMARY_LIMIT)" :key="item.row.tx_hash + '-' + i">{{ pendingLine(item) }}</li>
              </ul>
              <p v-if="pendingRows.length > PENDING_SUMMARY_LIMIT" class="deposit-view__pending-more">
                {{ t('deposit.pendingMore', { n: pendingRows.length - PENDING_SUMMARY_LIMIT }) }}
              </p>
            </div>
          </div>
          <div v-if="history.length" class="deposit-view__table-wrap">
            <table class="deposit-view__table">
              <thead><tr><th>{{ t('deposit.chain') }}</th><th>{{ t('deposit.amount') }}</th><th>{{ t('deposit.credited') }}</th><th>{{ t('common.status') }}</th><th>{{ t('deposit.txHash') }}</th><th>{{ t('deposit.time') }}</th></tr></thead>
              <tbody>
                <tr v-for="({ row, progress }, i) in rows" :key="row.tx_hash + '-' + i">
                  <td :data-label="t('deposit.chain')">{{ chainLabel(row.chain) }}</td>
                  <td :data-label="t('deposit.amount')" class="deposit-view__number">{{ row.amount }} <span class="deposit-view__muted">{{ row.token }}</span></td>
                  <td :data-label="t('deposit.credited')" class="deposit-view__number">{{ row.status === 'credited' ? fmtCredited(row.credited) : '—' }}</td>
                  <td :data-label="t('common.status')" class="deposit-view__status">
                    <span class="deposit-view__badge" :class="'deposit-view__badge--' + row.status">{{ statusText(row.status) }}</span><span v-if="row.reason" class="deposit-view__reason">{{ reasonText(row.reason) }}</span>
                    <div v-if="progress" class="deposit-view__progress">
                      <span class="deposit-view__progress-text">{{ progressText(progress) }}</span>
                      <span class="deposit-view__progress-track" role="progressbar" aria-valuemin="0"
                        :aria-valuemax="progress.required" :aria-valuenow="progress.done" :aria-label="progressText(progress)">
                        <span class="deposit-view__progress-fill" :style="{ width: progress.percent + '%' }"></span>
                      </span>
                    </div>
                  </td>
                  <td :data-label="t('deposit.txHash')" class="deposit-view__transaction">
                    <div v-if="row.tx_hash" class="deposit-view__hash-row">
                      <a v-if="explorerTxURL(row.chain, row.tx_hash)" :href="explorerTxURL(row.chain, row.tx_hash)"
                        target="_blank" rel="noopener noreferrer" class="deposit-view__hash"
                        :aria-label="t('deposit.viewTransaction', { hash: row.tx_hash })">{{ row.tx_hash }}</a>
                      <code v-else class="deposit-view__hash">{{ row.tx_hash }}</code>
                      <button type="button" class="deposit-view__hash-copy" :aria-label="t('deposit.copyTxHash')"
                        :title="t('deposit.copyTxHash')" @click="copyToClipboard(row.tx_hash)">
                        <Icon name="copy" size="sm" />
                      </button>
                    </div>
                    <span v-else class="deposit-view__muted">—</span>
                  </td>
                  <td :data-label="t('deposit.time')" class="deposit-view__time">{{ formatTime(row.created_at) }}</td>
                </tr>
              </tbody>
            </table>
          </div>
          <div v-else-if="!historyError" class="deposit-view__empty"><strong>{{ t('deposit.noHistory') }}</strong><p>{{ t('deposit.noHistoryHint') }}</p></div>
        </section>
      </template>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import QRCode from 'qrcode'
import AppLayout from '@/components/layout/AppLayout.vue'
import UserPageHeader from '@/components/user/UserPageHeader.vue'
import Icon from '@/components/icons/Icon.vue'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import { getDepositHistory, getDepositInfo, type DepositInfo, type DepositRecord } from '@/api/crypto'
import { useClipboard } from '@/composables/useClipboard'
import { useAppStore } from '@/stores/app'
import { useAuthStore } from '@/stores/auth'
import { explorerTxURL } from '@/utils/cryptoExplorer'

const { t } = useI18n()
const { copyToClipboard } = useClipboard()
const appStore = useAppStore()
const authStore = useAuthStore()

// While a deposit is confirming, re-read history quickly so the confirmation
// count moves and the credit lands without a manual refresh. Otherwise keep
// the slower refresh so a brand-new transfer still shows up on its own.
const PENDING_POLL_MS = 8000
const IDLE_POLL_MS = 30000
const PENDING_SUMMARY_LIMIT = 3

interface ConfirmationProgress {
  done: number
  required: number
  remaining: number
  percent: number
}

interface HistoryRow {
  row: DepositRecord
  progress: ConfirmationProgress | null
}

const info = ref<DepositInfo | null>(null)
const history = ref<DepositRecord[]>([])
const loading = ref(true)
const selectedChain = ref('')
const copied = ref(false)
const copyError = ref(false)
const qrError = ref(false)
const loadError = ref(false)
const historyError = ref(false)
const refreshingHistory = ref(false)
let disposed = false
let copyTimer: ReturnType<typeof setTimeout> | null = null
const qrCanvas = ref<HTMLCanvasElement | null>(null)
let pollTimer: ReturnType<typeof setTimeout> | null = null

const current = computed(() => info.value?.chains.find((c) => c.chain === selectedChain.value))

// Older backends send no confirmation fields, and a chain without a
// confirmation target sends 0: both fall back to the plain status badge.
function confirmationProgress(row: DepositRecord): ConfirmationProgress | null {
  if (row.status !== 'pending') return null
  const required = Math.floor(Number(row.required_confirmations))
  const confirmations = Number(row.confirmations)
  if (!Number.isFinite(required) || required <= 0 || !Number.isFinite(confirmations)) return null
  const done = Math.min(Math.max(Math.floor(confirmations), 0), required)
  return { done, required, remaining: required - done, percent: Math.round((done / required) * 100) }
}

function progressText(p: ConfirmationProgress): string {
  return p.remaining > 0
    ? t('deposit.confirmProgress', { done: p.done, required: p.required, remaining: p.remaining })
    : t('deposit.confirmProgressDone', { done: p.done, required: p.required })
}

const rows = computed<HistoryRow[]>(() => history.value.map(row => ({ row, progress: confirmationProgress(row) })))
const pendingRows = computed(() => rows.value.filter(item => item.row.status === 'pending'))

// One summary line per confirming deposit: "10 USDT · BSC (BEP20) · 2/3".
function pendingLine(item: HistoryRow): string {
  const parts = [`${item.row.amount} ${item.row.token}`, chainLabel(item.row.chain)]
  if (item.progress) parts.push(`${item.progress.done}/${item.progress.required}`)
  return parts.join(' · ')
}

function chainLabel(chain: string): string {
  switch (chain) {
    case 'bsc':
      return 'BSC (BEP20)'
    case 'ethereum':
      return 'Ethereum (ERC20)'
    case 'polygon':
      return 'Polygon (PoS)'
    case 'plasma':
      return 'Plasma'
    case 'arbitrum':
      return 'Arbitrum One'
    case 'base':
      return 'Base'
    case 'xlayer':
      return 'X Layer'
    case 'solana':
      return 'Solana'
    default:
      return chain
  }
}

function formatTime(iso: string): string {
  if (!iso) return '—'
  return new Date(iso).toLocaleString()
}

// Credited amounts are exact balance units (0.01 USDT × 6.7 = 0.067). A
// fixed 2-decimal rounding displayed 0.067 as 0.07 — visibly wrong next to
// the deposit it came from. Show up to 8 decimals, trailing zeros trimmed.
function fmtCredited(value: number): string {
  if (!Number.isFinite(value)) return '—'
  return Number(value.toFixed(8)).toString()
}

// Map the backend's machine reason codes to localized text. The thresholds
// come from deposit-info so the text always matches the live config.
function reasonText(code: string): string {
  switch (code) {
    case 'below_min':
      return t('deposit.reasonBelowMin', { min: info.value?.min_amount ?? 0 })
    case 'above_max':
      return t('deposit.reasonAboveMax', { max: info.value?.max_amount ?? 0 })
    case 'address_unassigned':
      return t('deposit.reasonUnassigned')
    case 'address_unknown':
      return t('deposit.reasonUnknown')
    default:
      // A new backend code must never surface as a bare machine string.
      return t('deposit.reasonOther', { code })
  }
}

// Status badges use the same vocabulary as the ledger, translated.
function statusText(status: string): string {
  switch (status) {
    case 'credited':
      return t('deposit.statusCredited')
    case 'pending':
      return t('deposit.statusConfirming')
    case 'ignored':
      return t('deposit.statusIgnored')
    case 'reverted':
      return t('deposit.statusReverted')
    default:
      return t('deposit.statusOther', { code: status })
  }
}

async function copyAddress() {
  if (!current.value) return
  copyError.value = false
  if (await copyToClipboard(current.value.address)) {
    copied.value = true
    if (copyTimer) clearTimeout(copyTimer)
    copyTimer = setTimeout(() => (copied.value = false), 1800)
  } else {
    copyError.value = true
  }
}

async function renderQR() {
  await nextTick()
  if (!qrCanvas.value || !current.value) return
  try {
    await QRCode.toCanvas(qrCanvas.value, current.value.address, { width: 184, margin: 1 })
    qrError.value = false
  } catch {
    qrError.value = true
  }
}

function depositKey(row: DepositRecord): string {
  return row.chain + ':' + row.tx_hash
}

// A deposit that was confirming on the last read and is credited now has just
// landed: tell the user and reload the balance shown in the header.
function announceCredited(previous: DepositRecord[], next: DepositRecord[]) {
  const wasPending = new Set(previous.filter(r => r.status === 'pending' && r.tx_hash).map(depositKey))
  const landed = next.filter(r => r.status === 'credited' && r.tx_hash && wasPending.has(depositKey(r)))
  if (!landed.length) return
  for (const r of landed) appStore.showSuccess(t('deposit.creditedToast', { amount: fmtCredited(r.credited) }))
  void authStore.refreshUser().catch(() => undefined)
}

function stopPolling() {
  if (pollTimer) clearTimeout(pollTimer)
  pollTimer = null
}

// Re-armed after every read so the cadence follows the latest data: fast while
// something is confirming, slow otherwise, and paused while the tab is hidden.
function schedulePoll() {
  stopPolling()
  if (disposed || !info.value?.enabled || loadError.value) return
  if (typeof document !== 'undefined' && document.visibilityState === 'hidden') return
  pollTimer = setTimeout(() => void refreshHistory(), pendingRows.value.length ? PENDING_POLL_MS : IDLE_POLL_MS)
}

async function refreshHistory() {
  if (refreshingHistory.value) return
  refreshingHistory.value = true
  try {
    const next = await getDepositHistory()
    if (disposed) return
    announceCredited(history.value, next)
    history.value = next
    historyError.value = false
  } catch {
    historyError.value = true
  } finally {
    refreshingHistory.value = false
    schedulePoll()
  }
}

function onVisibilityChange() {
  if (document.visibilityState === 'hidden') {
    stopPolling()
    return
  }
  if (info.value?.enabled && !loadError.value && !loading.value) void refreshHistory()
}

async function load() {
  loading.value = true
  loadError.value = false
  try {
    info.value = await getDepositInfo()
    if (!info.value.chains.some(c => c.chain === selectedChain.value)) {
      selectedChain.value = info.value.chains[0]?.chain ?? ''
    }
    if (info.value.enabled) await refreshHistory()
  } catch {
    loadError.value = true
  } finally {
    loading.value = false
  }
  if (!disposed) await renderQR()
}

watch(current, () => {
  copied.value = false
  copyError.value = false
  void renderQR()
}, { flush: 'post' })

onMounted(() => {
  document.addEventListener('visibilitychange', onVisibilityChange)
  void load()
})

onUnmounted(() => {
  disposed = true
  document.removeEventListener('visibilitychange', onVisibilityChange)
  stopPolling()
  if (copyTimer) clearTimeout(copyTimer)
})
</script>

<style scoped>
.deposit-view { width: 100%; max-width: 76rem; margin: 0 auto; padding: clamp(1rem, 2.4vw, 2rem); color: var(--signal-text, inherit); }
.deposit-view__header { margin-bottom: 1.5rem; }
.deposit-view__layout { display: grid; grid-template-columns: minmax(0, 1fr) 18rem; gap: 1.25rem; align-items: start; }
.deposit-view__funding, .deposit-view__guide, .deposit-view__history { min-width: 0; border: 1px solid var(--ui-border); border-radius: 8px; padding: 1.5rem; background: var(--ui-bg); box-shadow: 0 1px 2px rgba(10, 10, 10, 0.05); }
.dark .deposit-view__funding, .dark .deposit-view__guide, .dark .deposit-view__history { box-shadow: none; }
.deposit-view h2 { font-size: .9375rem; line-height: 1.5; font-weight: 650; }
.deposit-view__section-head { display: flex; align-items: center; gap: .625rem; margin-bottom: 1rem; }
.deposit-view__section-head--address { padding-top: 1.25rem; margin-top: 1.5rem; border-top: 1px solid var(--signal-line, #64748b30); }
.deposit-view__step { color: var(--signal-muted, #8c99a6); font: .75rem ui-monospace, monospace; }
.deposit-view__chains { display: flex; flex-wrap: wrap; gap: .5rem; }
.deposit-view__chain { display: inline-flex; align-items: center; justify-content: center; gap: .5rem; min-height: 2.5rem; padding: .55rem .875rem; border: 1px solid var(--ui-border-accented); border-radius: 6px; font-size: .8125rem; transition: border-color .15s, background .15s; }
.deposit-view__chain:hover { background: var(--signal-accent-soft, #0891b212); }
.deposit-view__chain--active { border-color: var(--signal-accent, #0891b2); background: var(--signal-accent-soft, #0891b218); font-weight: 600; }
.deposit-view__network-dot { width: .375rem; height: .375rem; flex-shrink: 0; border-radius: 50%; background: var(--signal-muted, #8c99a6); }
.deposit-view__chain--active .deposit-view__network-dot { background: var(--signal-accent, #0891b2); }
.deposit-view__token { padding: .15rem .5rem; border-radius: .375rem; border: 1px solid #10b98145; background: #10b98112; color: #10b981; font-size: .6875rem; font-weight: 600; }
.deposit-view__address-block { display: grid; grid-template-columns: 12.5rem minmax(0, 1fr); gap: 1.25rem; align-items: start; }
.deposit-view__qr-wrap { text-align: center; }
.deposit-view__qr { width: 12.5rem; min-height: 12.5rem; display: grid; place-items: center; border: 1px solid #e5e5e5; border-radius: 8px; background: white; color: #334155; overflow: hidden; }
.deposit-view__qr canvas { display: block; max-width: 100%; height: auto !important; }
.deposit-view__qr-wrap p { margin-top: .625rem; font-size: .75rem; color: var(--signal-muted, #8c99a6); }
.deposit-view__label { display: block; margin: .2rem 0 .625rem; font-size: .75rem; color: var(--signal-muted, #8c99a6); }
.deposit-view__address { display: block; padding: .875rem; min-width: 0; border: 1px solid var(--ui-border); border-radius: 6px; background: #64748b09; font-size: .8125rem; line-height: 1.8; overflow-wrap: anywhere; user-select: all; }
.deposit-view__copy { width: 100%; min-height: 2.75rem; margin-top: .75rem; }
.deposit-view__copy-status { margin-top: .75rem; font-size: .75rem; line-height: 1.7; color: var(--signal-muted, #8c99a6); }
.deposit-view__notice { display: flex; gap: .625rem; align-items: flex-start; padding: .875rem 1rem; margin-top: 1.5rem; border: 1px solid #d9770638; border-radius: .625rem; background: #d9770609; font-size: .75rem; line-height: 1.7; }
.deposit-view__notice-mark { display: grid; place-items: center; width: 1rem; height: 1rem; flex-shrink: 0; margin-top: .15rem; border: 1px solid #d97706; border-radius: 50%; color: #d97706; font-size: .6875rem; font-weight: 700; }
.deposit-view__terms { margin-top: 1.25rem; }
.deposit-view__terms > div { padding: 1rem 0; border-top: 1px solid var(--signal-line, #64748b30); }
.deposit-view__terms dt { color: var(--signal-muted, #8c99a6); font-size: .75rem; }
.deposit-view__terms dd { margin-top: .5rem; font-size: 1.1875rem; font-weight: 600; font-variant-numeric: tabular-nums; }
.deposit-view__terms dd span { font-size: .8125rem; font-weight: 400; }
.deposit-view__confirmation { display: flex; align-items: flex-start; gap: .625rem; margin-top: .5rem; padding: 1rem; border-radius: .625rem; background: #10b9810a; }
.deposit-view__confirmation-dot { width: .375rem; height: .375rem; flex-shrink: 0; margin-top: .4rem; background: #10b981; border-radius: 50%; }
.deposit-view__confirmation strong { font-size: .8125rem; font-weight: 600; }
.deposit-view__confirmation p { margin-top: .4rem; color: var(--signal-muted, #8c99a6); font-size: .75rem; line-height: 1.7; }
.deposit-view__history { margin-top: 1.25rem; }
.deposit-view__transaction { min-width: 14rem; max-width: 24rem; }
.deposit-view__hash-row { display: flex; align-items: flex-start; gap: .5rem; }
.deposit-view__hash { min-width: 0; word-break: break-all; white-space: normal; font: .75rem/1.6 ui-monospace, monospace; }
a.deposit-view__hash { color: var(--signal-accent, #0891b2); text-decoration: underline; text-underline-offset: .15rem; }
.deposit-view__hash-copy { flex: 0 0 auto; padding: .25rem; border-radius: .25rem; color: var(--signal-muted, #64748b); }
.deposit-view__hash-copy:hover, .deposit-view__hash-copy:focus-visible { color: var(--signal-accent, #0891b2); outline: 2px solid currentColor; outline-offset: 2px; }
.deposit-view__history-head { display: flex; justify-content: space-between; align-items: center; gap: 1rem; margin-bottom: 1rem; }
.deposit-view__table-wrap { max-width: 100%; overflow-x: auto; }
.deposit-view__table { width: 100%; border-collapse: collapse; text-align: left; }
.deposit-view__table th { color: var(--signal-muted, #8c99a6); font-size: .6875rem; font-weight: 500; background: #64748b08; }
.deposit-view__table th, .deposit-view__table td { padding: .875rem .75rem; border-bottom: 1px solid var(--signal-line, #64748b25); }
.deposit-view__table td { font-size: .8125rem; }
.deposit-view__table tr:last-child td { border-bottom: 0; }
.deposit-view__number { white-space: nowrap; font-variant-numeric: tabular-nums; }
.deposit-view__time { color: var(--signal-muted, #8c99a6); white-space: nowrap; font-size: .75rem !important; }
.deposit-view__muted { color: var(--signal-muted, #8c99a6); font-size: .75rem; }
.deposit-view__badge { display: inline-flex; padding: .2rem .5rem; border: 1px solid transparent; border-radius: .375rem; font-size: .6875rem; background: #64748b18; }
.deposit-view__badge--credited { background: #10b98112; border-color: #10b98130; color: #10b981; }
.deposit-view__badge--pending { background: #d9770612; border-color: #d9770630; color: #d97706; }
.deposit-view__badge--ignored, .deposit-view__badge--reverted { background: #ef444412; border-color: #ef444430; color: #ef4444; }
.deposit-view__reason { display: block; max-width: 21rem; margin-top: .4rem; color: var(--signal-muted, #8c99a6); font-size: .75rem; line-height: 1.6; }
.deposit-view__status { min-width: 9rem; }
.deposit-view__progress { display: grid; gap: .35rem; max-width: 12rem; margin-top: .45rem; }
.deposit-view__progress-text { color: var(--signal-muted, #8c99a6); font-size: .75rem; line-height: 1.5; font-variant-numeric: tabular-nums; overflow-wrap: anywhere; }
.deposit-view__progress-track { display: block; height: .25rem; border-radius: 99px; background: #d9770624; overflow: hidden; }
.deposit-view__progress-fill { display: block; height: 100%; border-radius: inherit; background: #d97706; transition: width .3s ease; }
.deposit-view__pending { display: flex; align-items: flex-start; gap: .625rem; min-width: 0; margin-bottom: 1rem; padding: .875rem 1rem; border: 1px solid #d9770638; border-radius: .625rem; background: #d9770609; font-size: .8125rem; line-height: 1.6; }
.deposit-view__pending-dot { width: .5rem; height: .5rem; flex-shrink: 0; margin-top: .45rem; border-radius: 50%; background: #d97706; animation: deposit-view-pulse 1.6s ease-in-out infinite; }
.deposit-view__pending-body { min-width: 0; flex: 1 1 auto; }
.deposit-view__pending-body strong { font-weight: 600; }
.deposit-view__pending-list { margin: .25rem 0 0; padding: 0; list-style: none; font-variant-numeric: tabular-nums; }
.deposit-view__pending-list li { overflow-wrap: anywhere; }
.deposit-view__pending-more { margin-top: .25rem; color: var(--signal-muted, #8c99a6); font-size: .75rem; }
@keyframes deposit-view-pulse { 0%, 100% { opacity: 1; } 50% { opacity: .35; } }
.deposit-view__empty { display: flex; flex-direction: column; align-items: center; gap: .75rem; padding: 3rem 1.25rem; text-align: center; font-size: .875rem; }
.deposit-view__empty p { max-width: 27rem; font-size: .8125rem; color: var(--signal-muted, #8c99a6); line-height: 1.7; }
.deposit-view__history-error { margin-bottom: 1rem; color: #d97706; font-size: .8125rem; line-height: 1.6; }
.deposit-view button:focus-visible { outline: 2px solid var(--signal-accent, #0891b2); outline-offset: 3px; }
@media (max-width: 1100px) { .deposit-view__layout { grid-template-columns: minmax(0, 1fr); } .deposit-view__terms { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 1rem; } }
@media (max-width: 640px) {
  .deposit-view { padding: 1rem .75rem; }
  .deposit-view__live { display: none; }
  .deposit-view__funding, .deposit-view__guide, .deposit-view__history { padding: 1.125rem; border-radius: .875rem; }
  .deposit-view__chains { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); }
  .deposit-view__chain { padding: .65rem .4rem; font-size: .75rem; }
  .deposit-view__address-block { grid-template-columns: minmax(0, 1fr); }
  .deposit-view__qr-wrap { justify-self: center; }
  .deposit-view__terms { grid-template-columns: minmax(0, 1fr); gap: 0; }
  .deposit-view__terms > div { display: flex; align-items: center; justify-content: space-between; gap: 1rem; }
  .deposit-view__terms dd { margin-top: 0; font-size: 1rem; text-align: right; }
  .deposit-view__table, .deposit-view__table tbody { display: block; }
  .deposit-view__table thead { display: none; }
  .deposit-view__table tr { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: .875rem; padding: 1rem 0; border-top: 1px solid var(--signal-line, #64748b30); }
  .deposit-view__table td { display: block; padding: 0; border: 0; min-width: 0; }
  .deposit-view__table td::before { content: attr(data-label); display: block; margin-bottom: .35rem; color: var(--signal-muted, #8c99a6); font-size: .6875rem; }
  .deposit-view__table td:first-child, .deposit-view__table td:last-child { grid-column: 1 / -1; }
  .deposit-view__table td:nth-child(4) { grid-column: 1 / -1; }
  .deposit-view__table td.deposit-view__transaction { grid-column: 1 / -1; min-width: 0; max-width: none; }
  .deposit-view__table td.deposit-view__status { min-width: 0; }
  .deposit-view__progress { max-width: none; }
  .deposit-view__pending { padding: .75rem .875rem; }
}
@media (prefers-reduced-motion: reduce) { .deposit-view__chain, .deposit-view__progress-fill { transition: none; } .deposit-view__pending-dot { animation: none; } }
</style>
