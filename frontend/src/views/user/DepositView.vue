<template>
  <AppLayout>
    <div class="deposit-view">
      <div class="card deposit-view__card">
        <div class="deposit-view__header">
          <div class="deposit-view__icon">
            <Icon name="wallet" size="md" />
          </div>
          <div class="min-w-0">
            <h1 class="deposit-view__title">{{ t('deposit.title') }}</h1>
            <p class="deposit-view__desc">{{ t('deposit.description') }}</p>
          </div>
        </div>

        <div v-if="loading" class="deposit-view__loading"><LoadingSpinner /></div>

        <div v-else-if="!info?.enabled" class="deposit-view__empty">
          {{ t('deposit.disabled') }}
        </div>

        <div v-else class="deposit-view__body">
          <!-- Chain picker -->
          <div class="deposit-view__field">
            <label class="deposit-view__label">{{ t('deposit.chain') }}</label>
            <div class="deposit-view__chains">
              <button
                v-for="c in info.chains"
                :key="c.chain"
                type="button"
                class="deposit-view__chain"
                :class="{ 'deposit-view__chain--active': selectedChain === c.chain }"
                @click="selectedChain = c.chain"
              >
                {{ chainLabel(c.chain) }}
              </button>
            </div>
          </div>

          <!-- Supported tokens on the selected chain -->
          <div v-if="current" class="deposit-view__field">
            <label class="deposit-view__label">{{ t('deposit.tokens') }}</label>
            <div class="deposit-view__tokens">
              <span v-for="tk in current.tokens" :key="tk" class="deposit-view__token">{{ tk }}</span>
            </div>
          </div>

          <!-- Address + QR -->
          <div v-if="current" class="deposit-view__address-block">
            <div class="deposit-view__qr">
              <canvas ref="qrCanvas"></canvas>
            </div>
            <div class="deposit-view__address-info">
              <label class="deposit-view__label">{{ t('deposit.yourAddress') }}</label>
              <div class="deposit-view__address">
                <code>{{ current.address }}</code>
                <button class="btn btn-secondary btn-sm" @click="copyAddress">
                  {{ copied ? t('deposit.copied') : t('deposit.copy') }}
                </button>
              </div>
              <div class="deposit-view__notice">
                <p>{{ t('deposit.exclusiveNotice') }}</p>
                <p>{{ t('deposit.rateNotice', { rate: info.rate }) }}</p>
                <p>{{ t('deposit.minNotice', { min: info.min_amount }) }}</p>
              </div>
            </div>
          </div>

          <!-- History -->
          <div class="deposit-view__history">
            <h4 class="deposit-view__label">{{ t('deposit.history') }}</h4>
            <table v-if="history.length" class="deposit-view__table">
              <thead>
                <tr>
                  <th>{{ t('deposit.chain') }}</th>
                  <th>{{ t('deposit.amount') }}</th>
                  <th>{{ t('deposit.credited') }}</th>
                  <th>{{ t('common.status') }}</th>
                  <th>{{ t('deposit.time') }}</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="(row, i) in history" :key="i">
                  <td>{{ chainLabel(row.chain) }}</td>
                  <td>{{ row.amount }} {{ row.token }}</td>
                  <td>{{ row.credited ? row.credited.toFixed(2) : '—' }}</td>
                  <td>
                    <span class="deposit-view__badge" :class="`deposit-view__badge--${row.status}`">
                      {{ row.status }}
                    </span>
                    <span v-if="row.reason" class="deposit-view__reason">{{ reasonText(row.reason) }}</span>
                  </td>
                  <td>{{ formatTime(row.created_at) }}</td>
                </tr>
              </tbody>
            </table>
            <div v-else class="deposit-view__empty-text">{{ t('common.noData') }}</div>
          </div>
        </div>
      </div>
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
const qrCanvas = ref<HTMLCanvasElement | null>(null)
let pollTimer: ReturnType<typeof setInterval> | null = null

const current = computed(() => info.value?.chains.find((c) => c.chain === selectedChain.value))

function chainLabel(chain: string): string {
  switch (chain) {
    case 'bsc':
      return 'BSC (BEP20)'
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
      return code
  }
}

async function copyAddress() {
  if (!current.value) return
  try {
    await navigator.clipboard.writeText(current.value.address)
    copied.value = true
    setTimeout(() => (copied.value = false), 1500)
  } catch {
    // Clipboard can be unavailable (permissions, insecure context); the
    // address stays visible for manual selection.
  }
}

async function renderQR() {
  await nextTick()
  if (!qrCanvas.value || !current.value) return
  await QRCode.toCanvas(qrCanvas.value, current.value.address, { width: 220, margin: 1 })
}

async function load() {
  info.value = await getDepositInfo()
  if (!selectedChain.value && info.value.chains.length) {
    selectedChain.value = info.value.chains[0].chain
  }
  history.value = await getDepositHistory()
}

// flush: 'post' waits for the canvas to exist after the DOM update; without
// it the first render runs before the element is mounted and silently no-ops.
watch(current, () => void renderQR(), { flush: 'post' })
watch(selectedChain, () => void renderQR(), { flush: 'post' })

onMounted(async () => {
  try {
    await load()
  } finally {
    loading.value = false
  }
  // The canvas lives behind v-if="loading"; it only exists after loading
  // flips and the DOM updates, so render after both.
  await nextTick()
  await renderQR()
  // Refresh history periodically so a deposit shows up without a reload.
  pollTimer = setInterval(() => {
    void getDepositHistory().then((rows) => (history.value = rows))
  }, 30000)
})

onUnmounted(() => {
  if (pollTimer) clearInterval(pollTimer)
})
</script>

<style scoped>
.deposit-view {
  padding: 1rem;
  max-width: 56rem;
  margin: 0 auto;
}
.deposit-view__card {
  padding: 0;
  overflow: hidden;
}
.deposit-view__header {
  display: flex;
  align-items: center;
  gap: 0.75rem;
  padding: 1rem 1.25rem;
  border-bottom: 1px solid var(--signal-line, rgba(127, 127, 127, 0.18));
}
.deposit-view__icon {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 2.25rem;
  height: 2.25rem;
  border-radius: 0.5rem;
  background: rgba(99, 102, 241, 0.12);
  color: #6366f1;
}
.deposit-view__title {
  font-size: 1.125rem;
  font-weight: 600;
}
.deposit-view__desc {
  font-size: 0.8125rem;
  color: var(--signal-muted, #6b7280);
}
.deposit-view__loading,
.deposit-view__empty,
.deposit-view__empty-text {
  display: flex;
  justify-content: center;
  padding: 2.5rem;
  font-size: 0.875rem;
  color: var(--signal-muted, #6b7280);
}
.deposit-view__body {
  padding: 1.25rem;
  display: grid;
  gap: 1.25rem;
}
.deposit-view__label {
  font-size: 0.8125rem;
  font-weight: 500;
  margin-bottom: 0.375rem;
  display: block;
}
.deposit-view__chains {
  display: flex;
  flex-wrap: wrap;
  gap: 0.5rem;
}
.deposit-view__chain {
  padding: 0.5rem 0.9rem;
  border: 1px solid var(--signal-line, rgba(127, 127, 127, 0.25));
  border-radius: 0.5rem;
  font-size: 0.8125rem;
  transition: all 0.15s;
}
.deposit-view__chain--active {
  border-color: #6366f1;
  background: rgba(99, 102, 241, 0.08);
  font-weight: 600;
}
.deposit-view__tokens {
  display: flex;
  flex-wrap: wrap;
  gap: 0.375rem;
}
.deposit-view__token {
  display: inline-block;
  padding: 0.25rem 0.625rem;
  border-radius: 0.375rem;
  background: rgba(34, 197, 94, 0.1);
  border: 1px solid rgba(34, 197, 94, 0.3);
  color: #16a34a;
  font-size: 0.75rem;
  font-weight: 600;
  letter-spacing: 0.02em;
}
.deposit-view__address-block {
  display: grid;
  grid-template-columns: auto 1fr;
  gap: 1.25rem;
  align-items: start;
}
@media (max-width: 640px) {
  .deposit-view__address-block {
    grid-template-columns: 1fr;
  }
}
.deposit-view__qr {
  padding: 0.75rem;
  border: 1px solid var(--signal-line, rgba(127, 127, 127, 0.18));
  border-radius: 0.75rem;
  background: #fff;
}
.deposit-view__address {
  display: flex;
  align-items: center;
  gap: 0.5rem;
}
.deposit-view__address code {
  flex: 1;
  padding: 0.5rem 0.75rem;
  border-radius: 0.5rem;
  background: rgba(127, 127, 127, 0.08);
  font-size: 0.8125rem;
  word-break: break-all;
}
.deposit-view__notice {
  margin-top: 0.75rem;
  padding: 0.75rem;
  border-radius: 0.5rem;
  background: rgba(245, 158, 11, 0.08);
  border: 1px solid rgba(245, 158, 11, 0.25);
  font-size: 0.8125rem;
  display: grid;
  gap: 0.25rem;
}
.deposit-view__table {
  width: 100%;
  border-collapse: collapse;
}
.deposit-view__table th,
.deposit-view__table td {
  padding: 0.5rem 0.75rem;
  border-bottom: 1px solid var(--signal-line, rgba(127, 127, 127, 0.12));
  text-align: left;
  font-size: 0.8125rem;
}
.deposit-view__table th {
  font-weight: 500;
  color: var(--signal-muted, #6b7280);
}
.deposit-view__badge {
  display: inline-block;
  padding: 0.125rem 0.5rem;
  border-radius: 0.375rem;
  font-size: 0.75rem;
  background: rgba(127, 127, 127, 0.12);
}
.deposit-view__badge--credited {
  background: rgba(34, 197, 94, 0.15);
  color: #16a34a;
}
.deposit-view__badge--pending {
  background: rgba(245, 158, 11, 0.15);
  color: #d97706;
}
.deposit-view__badge--ignored,
.deposit-view__badge--reverted {
  background: rgba(239, 68, 68, 0.15);
  color: #dc2626;
}
.deposit-view__reason {
  display: block;
  margin-top: 0.25rem;
  font-size: 0.75rem;
  color: var(--signal-muted, #6b7280);
}
.deposit-view__reason {
  display: block;
  margin-top: 0.25rem;
  font-size: 0.75rem;
  color: var(--signal-muted, #6b7280);
}
</style>
