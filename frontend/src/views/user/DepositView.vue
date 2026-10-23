<template>
  <AppLayout>
    <div class="deposit-view">
      <header class="deposit-view__header">
        <div class="deposit-view__icon"><Icon name="wallet" size="md" /></div>
        <div>
          <h1 class="deposit-view__title">{{ t('deposit.title') }}</h1>
          <p class="deposit-view__desc">{{ t('deposit.description') }}</p>
        </div>
        <span v-if="info?.enabled && !loadError" class="deposit-view__live">{{ t('deposit.exclusiveAddress') }}</span>
      </header>

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
            <div><h2>{{ t('deposit.history') }}</h2><p class="deposit-view__desc">{{ t('deposit.autoRefresh') }}</p></div>
            <button type="button" class="btn btn-secondary btn-sm" :disabled="refreshingHistory" @click="refreshHistory">
              {{ refreshingHistory ? t('common.loading') : t('common.refresh') }}
            </button>
          </div>
          <p v-if="historyError" class="deposit-view__history-error" role="alert">{{ t('deposit.historyFailed') }}</p>
          <div v-if="history.length" class="deposit-view__table-wrap">
            <table class="deposit-view__table">
              <thead><tr><th>{{ t('deposit.chain') }}</th><th>{{ t('deposit.amount') }}</th><th>{{ t('deposit.credited') }}</th><th>{{ t('common.status') }}</th><th>{{ t('deposit.time') }}</th></tr></thead>
              <tbody>
                <tr v-for="(row, i) in history" :key="row.tx_hash + '-' + i">
                  <td :data-label="t('deposit.chain')">{{ chainLabel(row.chain) }}</td>
                  <td :data-label="t('deposit.amount')" class="deposit-view__number">{{ row.amount }} <span class="deposit-view__muted">{{ row.token }}</span></td>
                  <td :data-label="t('deposit.credited')" class="deposit-view__number">{{ row.status === 'credited' ? fmtCredited(row.credited) : '—' }}</td>
                  <td :data-label="t('common.status')"><span class="deposit-view__badge" :class="'deposit-view__badge--' + row.status">{{ statusText(row.status) }}</span><span v-if="row.reason" class="deposit-view__reason">{{ reasonText(row.reason) }}</span></td>
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
import Icon from '@/components/icons/Icon.vue'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import { getDepositHistory, getDepositInfo, type DepositInfo, type DepositRecord } from '@/api/crypto'

const { t } = useI18n()

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
let pollTimer: ReturnType<typeof setInterval> | null = null

const current = computed(() => info.value?.chains.find((c) => c.chain === selectedChain.value))

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
      return t('deposit.statusPending')
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
  try {
    await navigator.clipboard.writeText(current.value.address)
    copied.value = true
    if (copyTimer) clearTimeout(copyTimer)
    copyTimer = setTimeout(() => (copied.value = false), 1800)
  } catch {
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

async function refreshHistory() {
  if (refreshingHistory.value) return
  refreshingHistory.value = true
  try {
    history.value = await getDepositHistory()
    historyError.value = false
  } catch {
    historyError.value = true
  } finally {
    refreshingHistory.value = false
  }
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

onMounted(async () => {
  await load()
  if (disposed) return
  pollTimer = setInterval(() => {
    if (info.value?.enabled && !loadError.value && !loading.value) void refreshHistory()
  }, 30000)
})

onUnmounted(() => {
  disposed = true
  if (pollTimer) clearInterval(pollTimer)
  if (copyTimer) clearTimeout(copyTimer)
})
</script>

<style scoped>
.deposit-view { width: 100%; max-width: 76rem; margin: 0 auto; padding: clamp(1rem, 2.4vw, 2rem); color: var(--signal-text, inherit); }
.deposit-view__header { display: flex; align-items: center; gap: .875rem; margin-bottom: 1.5rem; }
.deposit-view__icon { display: grid; place-items: center; flex: 0 0 2.75rem; height: 2.75rem; border: 1px solid var(--signal-line, #64748b33); border-radius: .875rem; background: var(--signal-accent-soft, #0891b212); color: var(--signal-accent, #0891b2); }
.deposit-view__title { font-size: 1.5rem; line-height: 1.3; font-weight: 650; letter-spacing: -.025em; }
.deposit-view__desc { margin-top: .3rem; font-size: .8125rem; line-height: 1.6; color: var(--signal-muted, #8c99a6); }
.deposit-view__live { margin-left: auto; padding: .4rem .7rem; border: 1px solid #10b98140; border-radius: 99px; background: #10b9810d; color: var(--signal-accent, #059669); font-size: .75rem; white-space: nowrap; }
.deposit-view__layout { display: grid; grid-template-columns: minmax(0, 1fr) 18rem; gap: 1.25rem; align-items: start; }
.deposit-view__funding, .deposit-view__guide, .deposit-view__history { min-width: 0; border: 1px solid var(--signal-line, #64748b30); border-radius: 1rem; padding: 1.5rem; }
.deposit-view h2 { font-size: .9375rem; line-height: 1.5; font-weight: 650; }
.deposit-view__section-head { display: flex; align-items: center; gap: .625rem; margin-bottom: 1rem; }
.deposit-view__section-head--address { padding-top: 1.25rem; margin-top: 1.5rem; border-top: 1px solid var(--signal-line, #64748b30); }
.deposit-view__step { color: var(--signal-muted, #8c99a6); font: .75rem ui-monospace, monospace; }
.deposit-view__chains { display: flex; flex-wrap: wrap; gap: .5rem; }
.deposit-view__chain { display: inline-flex; align-items: center; justify-content: center; gap: .5rem; min-height: 2.75rem; padding: .65rem .875rem; border: 1px solid var(--signal-line, #64748b40); border-radius: .625rem; font-size: .8125rem; transition: border-color .15s, background .15s; }
.deposit-view__chain:hover { background: var(--signal-accent-soft, #0891b212); }
.deposit-view__chain--active { border-color: var(--signal-accent, #0891b2); background: var(--signal-accent-soft, #0891b218); font-weight: 600; }
.deposit-view__network-dot { width: .375rem; height: .375rem; flex-shrink: 0; border-radius: 50%; background: var(--signal-muted, #8c99a6); }
.deposit-view__chain--active .deposit-view__network-dot { background: var(--signal-accent, #0891b2); }
.deposit-view__token { padding: .15rem .5rem; border-radius: .375rem; border: 1px solid #10b98145; background: #10b98112; color: #10b981; font-size: .6875rem; font-weight: 600; }
.deposit-view__address-block { display: grid; grid-template-columns: 12.5rem minmax(0, 1fr); gap: 1.25rem; align-items: start; }
.deposit-view__qr-wrap { text-align: center; }
.deposit-view__qr { width: 12.5rem; min-height: 12.5rem; display: grid; place-items: center; border: 1px solid #dbe3eb; border-radius: .875rem; background: white; color: #334155; overflow: hidden; }
.deposit-view__qr canvas { display: block; max-width: 100%; height: auto !important; }
.deposit-view__qr-wrap p { margin-top: .625rem; font-size: .75rem; color: var(--signal-muted, #8c99a6); }
.deposit-view__label { display: block; margin: .2rem 0 .625rem; font-size: .75rem; color: var(--signal-muted, #8c99a6); }
.deposit-view__address { display: block; padding: .875rem; min-width: 0; border: 1px solid var(--signal-line, #64748b30); border-radius: .625rem; background: #64748b09; font-size: .8125rem; line-height: 1.8; overflow-wrap: anywhere; user-select: all; }
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
.deposit-view__empty { display: flex; flex-direction: column; align-items: center; gap: .75rem; padding: 3rem 1.25rem; text-align: center; font-size: .875rem; }
.deposit-view__empty p { max-width: 27rem; font-size: .8125rem; color: var(--signal-muted, #8c99a6); line-height: 1.7; }
.deposit-view__history-error { margin-bottom: 1rem; color: #d97706; font-size: .8125rem; line-height: 1.6; }
.deposit-view button:focus-visible { outline: 2px solid var(--signal-accent, #0891b2); outline-offset: 3px; }
@media (max-width: 1100px) { .deposit-view__layout { grid-template-columns: minmax(0, 1fr); } .deposit-view__terms { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 1rem; } }
@media (max-width: 640px) {
  .deposit-view { padding: 1rem .75rem; }
  .deposit-view__title { font-size: 1.25rem; }
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
}
@media (prefers-reduced-motion: reduce) { .deposit-view__chain { transition: none; } }
</style>
