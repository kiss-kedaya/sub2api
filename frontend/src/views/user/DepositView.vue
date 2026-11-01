<template>
  <AppLayout>
    <div class="mx-auto flex w-full max-w-5xl flex-col gap-4 sm:gap-6">
      <UserPageHeader flush :title="t('deposit.title')" :description="t('deposit.description')">
        <template #badge>
          <UBadge v-if="info?.enabled && !loadError" color="success" variant="subtle" icon="i-lucide-shield-check">{{ t('deposit.exclusiveAddress') }}</UBadge>
        </template>
      </UserPageHeader>

      <div v-if="loading" class="flex justify-center py-16" role="status"><LoadingSpinner /></div>
      <div v-else-if="loadError" class="flex flex-col items-center gap-2 py-16 text-center" role="alert">
        <UIcon name="i-lucide-circle-alert" class="size-8 text-error" />
        <strong class="text-highlighted">{{ t('deposit.loadFailed') }}</strong>
        <p class="text-sm text-muted">{{ t('deposit.loadFailedHint') }}</p>
        <UButton color="neutral" variant="outline" class="mt-2" @click="load">{{ t('deposit.retry') }}</UButton>
      </div>
      <div v-else-if="!info?.enabled" class="flex flex-col items-center gap-2 py-16 text-center">
        <UIcon name="i-lucide-wallet" class="size-8 text-dimmed" />
        <strong class="text-highlighted">{{ t('deposit.disabled') }}</strong>
        <p class="text-sm text-muted">{{ t('deposit.disabledHint') }}</p>
      </div>
      <template v-else>
        <div class="grid gap-4 lg:grid-cols-[minmax(0,1fr)_300px] lg:items-start">
          <UPageCard variant="subtle" :aria-label="t('deposit.yourAddress')" :ui="{ container: 'gap-y-5' }">
            <UFormField :label="t('deposit.chooseNetwork')">
              <div class="flex flex-wrap gap-1.5" :aria-label="t('deposit.chain')">
                <UButton
                  v-for="c in info.chains"
                  :key="c.chain"
                  color="neutral"
                  :variant="selectedChain === c.chain ? 'subtle' : 'outline'"
                  :class="selectedChain === c.chain ? 'font-semibold text-highlighted ring-2 ring-inset ring-primary' : ''"
                  :aria-pressed="selectedChain === c.chain"
                  @click="selectedChain = c.chain"
                >
                  {{ chainLabel(c.chain) }}
                </UButton>
              </div>
            </UFormField>

            <template v-if="current">
              <USeparator />
              <div class="flex flex-col gap-5 sm:flex-row sm:items-start">
                <div class="flex shrink-0 flex-col items-center gap-2">
                  <div class="grid size-50 place-items-center overflow-hidden rounded-lg bg-white ring ring-default">
                    <canvas v-show="!qrError" ref="qrCanvas" class="block h-auto! max-w-full" role="img" :aria-label="t('deposit.qrLabel')"></canvas>
                    <span v-if="qrError" class="text-sm text-muted">{{ t('deposit.qrFailed') }}</span>
                  </div>
                  <p class="text-xs text-muted">{{ t('deposit.scanAddress') }}</p>
                </div>
                <div class="flex min-w-0 flex-1 flex-col gap-3">
                  <div class="flex flex-wrap items-center gap-2">
                    <span class="text-sm font-medium text-highlighted">{{ t('deposit.transferToAddress') }}</span>
                    <UBadge v-for="tk in current.tokens" :key="tk" color="neutral" variant="subtle">{{ tk }}</UBadge>
                  </div>
                  <span class="text-xs text-muted">{{ t('deposit.yourAddress') }} · {{ chainLabel(current.chain) }}</span>
                  <code class="block break-all rounded-md bg-default px-3 py-2.5 font-mono text-sm text-highlighted ring ring-default">{{ current.address }}</code>
                  <UButton size="lg" block class="justify-center" :icon="copied ? 'i-lucide-check' : 'i-lucide-copy'" @click="copyAddress">
                    {{ copied ? t('deposit.copied') : t('deposit.copyAddress') }}
                  </UButton>
                  <p class="text-xs text-muted" role="status" aria-live="polite">{{ copyError ? t('deposit.copyFailed') : t('deposit.exclusiveNotice') }}</p>
                </div>
              </div>
              <UAlert color="warning" variant="subtle" icon="i-lucide-triangle-alert" :description="t('deposit.networkNotice', { network: chainLabel(current.chain), tokens: current.tokens.join(' / ') })" />
            </template>
            <p v-else class="py-8 text-center text-sm text-muted">{{ t('deposit.noNetworks') }}</p>
          </UPageCard>

          <UPageCard :title="t('deposit.creditDetails')" :description="t('deposit.creditDetailsHint')" variant="subtle">
            <dl class="divide-y divide-default">
              <div class="flex items-baseline justify-between gap-3 py-2.5"><dt class="text-sm text-muted">{{ t('deposit.rateLabel') }}</dt><dd class="text-sm font-semibold tabular-nums text-highlighted">1 USDT <span class="font-normal text-muted">= {{ info.rate }} {{ t('deposit.balanceUnits') }}</span></dd></div>
              <div class="flex items-baseline justify-between gap-3 py-2.5"><dt class="text-sm text-muted">{{ t('deposit.minLabel') }}</dt><dd class="text-sm font-semibold tabular-nums text-highlighted">{{ info.min_amount }} <span class="font-normal text-muted">USDT</span></dd></div>
              <div v-if="info.max_amount > 0" class="flex items-baseline justify-between gap-3 py-2.5"><dt class="text-sm text-muted">{{ t('deposit.maxLabel') }}</dt><dd class="text-sm font-semibold tabular-nums text-highlighted">{{ info.max_amount }} <span class="font-normal text-muted">USDT</span></dd></div>
            </dl>
            <div class="flex gap-2.5 rounded-md bg-success/10 p-3">
              <UIcon name="i-lucide-badge-check" class="mt-0.5 size-4 shrink-0 text-success" />
              <div>
                <p class="text-sm font-medium text-highlighted">{{ t('deposit.autoCredit') }}</p>
                <p class="mt-0.5 text-xs text-muted">{{ t('deposit.confirmationHint') }}</p>
              </div>
            </div>
          </UPageCard>
        </div>

        <section>
          <UPageCard :title="t('deposit.history')" :description="t('deposit.autoRefresh')" variant="naked" orientation="horizontal" class="mb-4">
            <UButton color="neutral" variant="outline" icon="i-lucide-refresh-cw" class="w-fit lg:ms-auto" :ui="{ leadingIcon: refreshingHistory ? 'animate-spin' : '' }" :disabled="refreshingHistory" @click="refreshHistory">
              {{ refreshingHistory ? t('common.loading') : t('common.refresh') }}
            </UButton>
          </UPageCard>
          <UAlert v-if="historyError" class="mb-3" role="alert" color="error" variant="subtle" icon="i-lucide-circle-alert" :description="t('deposit.historyFailed')" />
          <UTable
            v-if="history.length"
            :data="history"
            :columns="historyColumns"
            :ui="{
              root: 'rounded-lg ring ring-default',
              base: 'border-separate border-spacing-0',
              thead: '[&>tr]:bg-elevated/50 [&>tr]:after:content-none',
              tbody: '[&>tr]:last:[&>td]:border-b-0',
              th: 'py-2 border-b border-default',
              td: 'border-b border-default',
              separator: 'h-0'
            }"
          >
            <template #chain-cell="{ row }">{{ chainLabel(row.original.chain) }}</template>
            <template #amount-cell="{ row }"><span class="tabular-nums text-highlighted">{{ row.original.amount }}</span> <span class="text-muted">{{ row.original.token }}</span></template>
            <template #credited-cell="{ row }"><span class="tabular-nums">{{ row.original.status === 'credited' ? fmtCredited(row.original.credited) : '—' }}</span></template>
            <template #status-cell="{ row }">
              <UBadge :color="statusColor(row.original.status)" variant="subtle">{{ statusText(row.original.status) }}</UBadge>
              <span v-if="row.original.reason" class="mt-1 block text-xs text-muted">{{ reasonText(row.original.reason) }}</span>
            </template>
            <template #tx_hash-cell="{ row }">
              <div v-if="row.original.tx_hash" class="flex min-w-0 max-w-72 items-center gap-1">
                <a
                  v-if="explorerTxURL(row.original.chain, row.original.tx_hash)"
                  :href="explorerTxURL(row.original.chain, row.original.tx_hash)"
                  target="_blank"
                  rel="noopener noreferrer"
                  class="deposit-view__hash truncate font-mono text-xs text-highlighted underline-offset-2 hover:underline"
                  :aria-label="t('deposit.viewTransaction', { hash: row.original.tx_hash })"
                >{{ row.original.tx_hash }}</a>
                <code v-else class="deposit-view__hash truncate font-mono text-xs text-highlighted">{{ row.original.tx_hash }}</code>
                <UButton
                  color="neutral"
                  variant="ghost"
                  size="xs"
                  icon="i-lucide-copy"
                  class="deposit-view__hash-copy shrink-0"
                  :aria-label="t('deposit.copyTxHash')"
                  :title="t('deposit.copyTxHash')"
                  @click="copyToClipboard(row.original.tx_hash)"
                />
              </div>
              <span v-else class="text-muted">—</span>
            </template>
            <template #created_at-cell="{ row }"><span class="whitespace-nowrap tabular-nums text-muted">{{ formatTime(row.original.created_at) }}</span></template>
          </UTable>
          <div v-else-if="!historyError" class="flex flex-col items-center gap-1 rounded-lg py-10 text-center ring ring-default">
            <strong class="text-sm text-highlighted">{{ t('deposit.noHistory') }}</strong>
            <p class="text-sm text-muted">{{ t('deposit.noHistoryHint') }}</p>
          </div>
        </section>
      </template>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { TableColumn } from '@nuxt/ui'
import QRCode from 'qrcode'
import AppLayout from '@/components/layout/AppLayout.vue'
import UserPageHeader from '@/components/user/UserPageHeader.vue'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import { getDepositHistory, getDepositInfo, type DepositInfo, type DepositRecord } from '@/api/crypto'
import { useClipboard } from '@/composables/useClipboard'
import { explorerTxURL } from '@/utils/cryptoExplorer'

const { t } = useI18n()
const { copyToClipboard } = useClipboard()

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

// History columns; the order matches the old table (tx hash stays the 5th column).
const historyColumns = computed<TableColumn<DepositRecord>[]>(() => [
  { accessorKey: 'chain', header: t('deposit.chain') },
  { accessorKey: 'amount', header: t('deposit.amount') },
  { accessorKey: 'credited', header: t('deposit.credited') },
  { accessorKey: 'status', header: t('common.status') },
  { accessorKey: 'tx_hash', header: t('deposit.txHash') },
  { accessorKey: 'created_at', header: t('deposit.time') }
])

function statusColor(status: string) {
  switch (status) {
    case 'credited': return 'success' as const
    case 'pending': return 'warning' as const
    case 'reverted': return 'error' as const
    default: return 'neutral' as const
  }
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
