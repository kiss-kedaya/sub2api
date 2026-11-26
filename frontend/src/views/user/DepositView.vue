<template>
  <AppLayout>
    <div class="deposit-view">
      <UserPageHeader class="deposit-view__header" flush :title="t('deposit.title')" :description="t('deposit.description')">
        <template #actions>
          <UBadge v-if="info?.enabled && !loading && !depositBlocked" color="success" variant="subtle" icon="i-lucide-shield-check">{{ t('deposit.exclusiveAddress') }}</UBadge>
        </template>
      </UserPageHeader>

      <div v-if="depositBlocked" class="deposit-view__gate">
        <KeyRotationGateNotice :status="keyRotationStatus" />
      </div>
      <template v-else>
        <KeyRotationRiskAckBanner v-if="riskAckActive" :status="keyRotationStatus" class="mb-4" />
        <!-- 首屏骨架：地址/二维码和右侧说明的版式先占位，避免白屏后整块跳变。 -->
        <div
          v-if="loading"
          class="deposit-view__layout"
          role="status"
          aria-busy="true"
          :aria-label="t('common.loading')"
          data-testid="deposit-skeleton"
        >
          <section class="card deposit-view__funding">
            <div class="mb-4 flex items-center justify-between gap-3">
              <div class="h-5 w-40 animate-pulse rounded bg-elevated/60"></div>
              <div class="h-8 w-24 animate-pulse rounded bg-elevated/60"></div>
            </div>
            <div class="grid grid-cols-2 gap-2">
              <div v-for="i in 4" :key="i" class="h-14 animate-pulse rounded-md bg-elevated/60"></div>
            </div>
            <div class="mt-5 h-40 animate-pulse rounded-md bg-elevated/60 sm:h-48"></div>
            <div class="mt-4 h-10 animate-pulse rounded-md bg-elevated/60"></div>
          </section>
          <aside class="card deposit-view__guide">
            <div class="h-5 w-28 animate-pulse rounded bg-elevated/60"></div>
            <div class="mt-3 h-4 w-full animate-pulse rounded bg-elevated/60"></div>
            <div class="mt-2 h-4 w-2/3 animate-pulse rounded bg-elevated/60"></div>
            <div class="mt-5 space-y-3">
              <div v-for="i in 3" :key="i" class="h-12 animate-pulse rounded-md bg-elevated/60"></div>
            </div>
          </aside>
        </div>
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
        <div v-else class="deposit-view__layout">
          <section class="card deposit-view__funding" :aria-label="t('deposit.yourAddress')">
            <div class="deposit-view__section-head deposit-view__section-head--network">
              <span class="deposit-view__step">01</span>
              <h2>{{ t('deposit.chooseNetwork') }}</h2>
              <button v-if="current" type="button" class="btn btn-secondary btn-sm deposit-view__network-refresh"
                :disabled="networkStatusRequest !== null" @click="refreshNetworkStatuses">
                {{ t('deposit.refreshNetworkStatus') }}
              </button>
            </div>
            <div class="deposit-view__chains" :aria-label="t('deposit.chain')">
              <button v-for="c in chainOptions" :key="c.chain" type="button" class="deposit-view__chain"
                :class="{ 'deposit-view__chain--active': selectedChain === c.chain }"
                :aria-pressed="selectedChain === c.chain" @click="selectChain(c.chain)">
                <span class="deposit-view__chain-head">
                  <span class="deposit-view__network-dot" aria-hidden="true"></span>
                  {{ c.label }}
                  <UBadge class="deposit-view__network-status" size="xs" variant="subtle"
                    :color="c.badge.color">
                    {{ c.badge.text }}
                  </UBadge>
                </span>
                <span v-if="c.eta" class="deposit-view__chain-eta">{{ c.eta }}</span>
              </button>
            </div>
            <div v-if="current && etaNotice" class="deposit-view__eta-notice" role="status">
              <span class="deposit-view__eta-mark" aria-hidden="true">i</span>
              <p>{{ etaNotice }}</p>
            </div>
            <div v-if="current && selectedNetworkNotice" class="deposit-view__network-notice" role="status">
              <span class="deposit-view__notice-mark" aria-hidden="true">!</span>
              <p>{{ t('deposit.networkStatusHint.' + selectedNetworkNotice, { network: chainLabel(current.chain) }) }}</p>
            </div>
            <div v-if="currentAssets.length > 1" class="deposit-view__assets" role="radiogroup"
              :aria-label="t('deposit.chooseAsset')">
              <button v-for="asset in currentAssets" :key="asset.symbol" type="button" class="deposit-view__asset"
                :class="{ 'deposit-view__asset--active': selectedSymbol === asset.symbol }"
                :aria-checked="selectedSymbol === asset.symbol" role="radio"
                @click="selectedAsset = asset.symbol">
                {{ assetLabel(asset) }}
              </button>
            </div>
            <template v-if="current && !networkTransferBlocked">
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
                <p v-if="selectedOption?.native">
                  {{ t('deposit.nativeNotice', { network: nativeNetworkLabel(current.chain), asset: selectedOption.symbol }) }}
                </p>
                <p v-else>{{ t('deposit.networkNotice', { network: chainLabel(current.chain), tokens: selectedSymbol || current.tokens.join(' / ') }) }}</p>
              </div>
            </template>
            <div v-else-if="!current" class="deposit-view__empty">{{ t('deposit.noNetworks') }}</div>
          </section>

          <aside class="card deposit-view__guide">
            <h2>{{ t('deposit.creditDetails') }}</h2>
            <p class="deposit-view__desc">{{ t('deposit.creditDetailsHint') }}</p>
            <dl class="deposit-view__terms">
              <div v-if="selectedMarketPriced">
                <dt>{{ t('deposit.settlementLabel') }}</dt>
                <dd class="deposit-view__settlement">{{ t('deposit.settlementMarket') }}</dd>
              </div>
              <div v-else-if="selectedFixedRate !== null">
                <dt>{{ t('deposit.rateLabel') }}</dt>
                <dd>1 {{ rateUnit }} <span>= {{ selectedFixedRate }} {{ t('deposit.balanceUnits') }}</span></dd>
              </div>
              <div>
                <dt>{{ selectedMarketPriced ? t('deposit.minLabelUsd') : t('deposit.minLabel') }}</dt>
                <dd>{{ info.min_amount }} <span>{{ limitUnit }}</span></dd>
              </div>
              <div v-if="info.max_amount > 0">
                <dt>{{ selectedMarketPriced ? t('deposit.maxLabelUsd') : t('deposit.maxLabel') }}</dt>
                <dd>{{ info.max_amount }} <span>{{ limitUnit }}</span></dd>
              </div>
            </dl>
            <p v-if="selectedMarketPriced && referencePrice" class="deposit-view__reference">
              {{ t('deposit.referencePrice', { price: '$' + referencePrice }) }}
            </p>
            <p v-if="selectedInstantCredit" class="deposit-view__instant-rule" data-testid="deposit-instant-credit-rule">
              {{ t('deposit.instantCreditRule') }}
            </p>
            <div class="deposit-view__confirmation">
              <span class="deposit-view__confirmation-dot" aria-hidden="true"></span>
              <div><strong>{{ t('deposit.autoCredit') }}</strong><p>{{ t('deposit.confirmationHint') }}</p></div>
            </div>
          </aside>
        </div>

        <!-- 记录区：Nuxt UI 的共享 TransactionListCard（收入/支出/回滚语义在组件里统一）。
             隐藏左侧圆形图标板（无需头像式圆圈），启用新记录弹出加入动画；
             明细（原因、回滚提示、客服入口、确认进度、交易哈希）走 item-details 插槽。 -->
        <TransactionListCard
          class="deposit-view__history"
          :title="t('deposit.history')"
          :subtitle="canPollHistory ? (pendingRows.length ? t('deposit.autoRefreshPending') : t('deposit.autoRefresh')) : ''"
          :items="historyItems"
          :loading="initialHistoryLoading"
          :skeleton-rows="3"
          :show-icon="false"
          :animate-items="true"
          :empty-icon="filtersActive ? 'i-lucide-filter-x' : 'i-lucide-wallet-minimal'"
          :empty-title="filtersActive ? t('deposit.noFilteredHistory') : t('deposit.noHistory')"
          :empty-description="filtersActive ? t('deposit.noFilteredHistoryHint') : (info?.enabled ? t('deposit.noHistoryHint') : '')"
          :empty-test-id="filtersActive ? 'deposit-no-filtered-history' : 'deposit-no-history'"
          :loading-label="t('common.loading')"
          test-id="deposit-history-card"
        >
          <template #actions>
            <div class="deposit-view__history-head">
              <UButton
                color="neutral"
                variant="outline"
                size="sm"
                icon="i-lucide-refresh-ccw"
                :loading="manualRefreshingHistory"
                :disabled="loading || manualRefreshingHistory"
                :label="t('common.refresh')"
                @click="refreshHistory({ manual: true })"
              />
            </div>
          </template>

          <template v-if="historyError || history.length || pendingRows.length" #toolbar>
            <div class="deposit-view__toolbar">
              <p v-if="historyError" class="deposit-view__history-error" role="alert">{{ t('deposit.historyFailed') }}</p>
              <!-- 本地筛选：只重排已加载的记录，不重新请求、不影响后台静默检测。 -->
              <DepositHistoryFilters v-if="history.length" v-model:status="statusFilter" v-model:range="timeRange" />
              <div v-if="(history.length && filtersActive) || history.length >= HISTORY_PAGE_LIMIT" class="deposit-view__filter-notes">
                <p v-if="history.length && filtersActive" class="deposit-view__filter-note" data-testid="deposit-filter-note">
                  {{ t('deposit.filterResult', { n: displayRows.length, total: history.length }) }}
                </p>
                <!-- 后端只返回最近 50 条且不分页：筛选范围要说清楚，别让人以为更早的记录没了。 -->
                <p v-if="history.length >= HISTORY_PAGE_LIMIT" class="deposit-view__filter-note" data-testid="deposit-history-limit">
                  {{ t('deposit.historyLimitHint', { n: history.length }) }}
                </p>
              </div>
              <div v-if="pendingRows.length" class="deposit-view__pending" role="status">
                <span class="deposit-view__pending-dot" aria-hidden="true"></span>
                <div class="deposit-view__pending-body">
                  <strong>{{ t('deposit.pendingSummary', { n: pendingRows.length }) }}</strong>
                  <ul class="deposit-view__pending-list">
                    <li v-for="item in pendingRows.slice(0, PENDING_SUMMARY_LIMIT)" :key="item.key">{{ pendingLine(item) }}</li>
                  </ul>
                  <p v-if="pendingRows.length > PENDING_SUMMARY_LIMIT" class="deposit-view__pending-more">
                    {{ t('deposit.pendingMore', { n: pendingRows.length - PENDING_SUMMARY_LIMIT }) }}
                  </p>
                </div>
              </div>
            </div>
          </template>

          <template #item-details="{ item }">
            <div v-if="hasRowDetails(item.row)" class="deposit-view__details">
              <div
                v-if="hasRowCallout(item.row)"
                class="deposit-view__callout"
                :class="rowCalloutClass(item.row)"
              >
                <span v-if="creditUnderReview(item.row)" class="deposit-view__status" data-testid="deposit-credit-under-review">{{ statusLabel(item.row) }}</span>
                <span v-if="revokeReasonText(item.row)" class="deposit-view__reason deposit-view__reason--revoked" data-testid="deposit-revoke-reason">{{ revokeReasonText(item.row) }}</span>
                <span v-else-if="item.row.reason" class="deposit-view__reason">{{ reasonText(item.row.reason, item.row.token === 'native', item.row.status) }}</span>
                <span v-if="item.row.status === 'revoked'" class="deposit-view__reason deposit-view__reason--revoked" data-testid="deposit-revoked-hint">{{ t('deposit.revokedHint') }}</span>
                <span v-else-if="item.row.status === 'provisional'" class="deposit-view__hint" data-testid="deposit-provisional-hint">{{ t('deposit.provisionalHint') }}</span>
                <!-- 回滚后的下一步：能点就点。地址/邮箱直接开客服，纯文本联系方式就复制，没配就直说。 -->
                <span v-if="item.row.status === 'revoked'" class="deposit-view__support">
                  <a v-if="supportLink" :href="supportLink" target="_blank" rel="noopener noreferrer"
                    class="deposit-view__support-action" data-testid="deposit-support-link">{{ t('deposit.revokedSupportAction') }}</a>
                  <template v-else-if="contactInfo">
                    <button type="button" class="deposit-view__support-action" data-testid="deposit-support-copy"
                      @click="copySupportContact">{{ t('deposit.revokedSupportAction') }}</button>
                    <span class="deposit-view__support-contact">{{ contactInfo }}</span>
                    <span v-if="supportCopied" class="deposit-view__support-copied" role="status"
                      data-testid="deposit-support-copied">{{ t('deposit.supportCopied') }}</span>
                  </template>
                  <template v-else>{{ t('deposit.supportUnavailable') }}</template>
                </span>
              </div>
              <div v-if="isConfirmingRow(item.row)" class="deposit-view__progress">
                <div class="deposit-view__progress-head">
                  <span class="deposit-view__progress-text" :data-testid="'deposit-progress-text-' + item.row.status">{{ progressText(item.row, item.progress) }}</span>
                  <span v-if="item.progress" class="deposit-view__progress-pct">{{ item.progress.percent }}%</span>
                </div>
                <span v-if="item.progress" class="deposit-view__progress-track" role="progressbar" aria-valuemin="0"
                  :aria-valuemax="item.progress.required" :aria-valuenow="item.progress.done" :aria-label="progressText(item.row, item.progress)">
                  <span class="deposit-view__progress-fill" :style="{ width: item.progress.percent + '%' }"></span>
                </span>
              </div>
              <div v-if="item.row.tx_hash" class="deposit-view__transaction">
                <div class="deposit-view__hash-row">
                  <template v-if="explorerTxURL(item.row.chain, item.row.tx_hash)">
                    <a :href="explorerTxURL(item.row.chain, item.row.tx_hash)"
                      target="_blank" rel="noopener noreferrer" class="deposit-view__hash"
                      :title="item.row.tx_hash"
                      :aria-label="t('deposit.viewTransaction', { hash: item.row.tx_hash })">{{ item.row.tx_hash }}</a>
                    <UIcon name="i-lucide-arrow-up-right" class="deposit-view__hash-ext" aria-hidden="true" />
                  </template>
                  <code v-else class="deposit-view__hash" :title="item.row.tx_hash">{{ item.row.tx_hash }}</code>
                  <UButton color="neutral" variant="ghost" size="xs"
                    :icon="copiedTxKey === item.id ? 'i-lucide-check' : 'i-lucide-copy'" square
                    class="deposit-view__hash-copy"
                    :class="{ 'deposit-view__hash-copy--done': copiedTxKey === item.id }"
                    :aria-label="t('deposit.copyTxHash')" :title="t('deposit.copyTxHash')"
                    @click="copyTxHash(item.row.tx_hash, String(item.id))" />
                </div>
              </div>
            </div>
          </template>

          <template v-if="filtersActive" #empty-actions>
            <UButton color="neutral" variant="outline" size="sm" data-testid="deposit-reset-filters"
              :label="t('deposit.resetFilters')" @click="resetFilters" />
          </template>
        </TransactionListCard>
      </template>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import QRCode from 'qrcode'
import AppLayout from '@/components/layout/AppLayout.vue'
import UserPageHeader from '@/components/user/UserPageHeader.vue'
import Icon from '@/components/icons/Icon.vue'
import KeyRotationGateNotice from '@/components/user/KeyRotationGateNotice.vue'
import KeyRotationRiskAckBanner from '@/components/user/KeyRotationRiskAckBanner.vue'
import DepositHistoryFilters from '@/components/deposit/DepositHistoryFilters.vue'
import TransactionListCard from '@/components/common/TransactionListCard.vue'
import type { TransactionItem, TransactionStatus, TransactionTone } from '@/components/common/transactionList'
import {
  getDepositHistory, getDepositInfo, getDepositNetworkStatus,
  type DepositChain, type DepositInfo, type DepositNetworkState, type DepositNetworkStatus, type DepositRecord,
} from '@/api/crypto'
import { isKeyRotationRequiredError } from '@/api/keyRotationGate'
import { useClipboard } from '@/composables/useClipboard'
import { useKeyRotationGate } from '@/composables/useKeyRotationGate'
import { useAppStore } from '@/stores/app'
import { useAuthStore } from '@/stores/auth'
import { explorerTxURL } from '@/utils/cryptoExplorer'
import {
  depositTransferIdentity, findNewDepositKeys, isSameDepositHistory, uniqueDepositRowKeys,
} from '@/utils/depositHistorySync'
import { depositRevokeReasonCopy } from '@/utils/depositRevoke'
import { supportContactHref } from '@/utils/depositSupport'
import {
  confirmationProgress, depositProgressCopy, isConfirmingDeposit,
  type ConfirmationProgress,
} from '@/utils/depositProgress'
import {
  isDepositFilterActive, matchesDepositStatus, matchesDepositTimeRange,
  type DepositStatusFilter, type DepositTimeRange,
} from '@/utils/depositHistoryFilter'
import {
  cardEtaText, etaNoticeText, networkBadge, networkStateOf, parseEtaSeconds, pruneNetworkStatuses,
  type DepositNetworkBadge,
} from '@/utils/depositNetwork'

const { t } = useI18n()
const { copyToClipboard } = useClipboard()
const appStore = useAppStore()
const authStore = useAuthStore()
// 密钥轮换门禁：未完成整改时 USDT/链上充值入口整体替换为整改卡片。
const { required: keyRotationRequired, status: keyRotationStatus } = useKeyRotationGate()
// An explicit denial must survive an older shared status read finishing afterwards.
const keyRotationDenied = ref(false)
const depositBlocked = computed(() => keyRotationRequired.value || keyRotationDenied.value)
const riskAckActive = computed(
  () => !keyRotationRequired.value && Boolean(keyRotationStatus.value?.risk_ack_expires_at)
)

// A transfer can be mined between two reads, so the page keeps one cadence
// whether or not anything is already confirming. Five seconds is short enough
// that a fresh deposit appears without a manual refresh, while hidden tabs and
// unmounted views schedule nothing.
const HISTORY_POLL_MS = 5000
const FRESH_HIGHLIGHT_MS = 2400
const PENDING_SUMMARY_LIMIT = 3
// The history endpoint returns the most recent 50 rows and is not paginated;
// the local filters therefore only ever see those rows.
const HISTORY_PAGE_LIMIT = 50

interface HistoryRow {
  key: string
  row: DepositRecord
  progress: ConfirmationProgress | null
}

// One currency a network accepts. `native` marks the chain's mainnet coin
// (ETH, BNB), which shares the deposit address but has no token contract.
// `market_priced` marks an asset credited at the market price observed when it
// lands; only fixed-rate assets carry a `rate`.
interface AssetOption {
  symbol: string
  native: boolean
  market_priced?: boolean
  rate?: number | null
  reference_price_usd?: string | null
}

const info = ref<DepositInfo | null>(null)
const history = ref<DepositRecord[]>([])
const loading = ref(true)
const initialHistoryLoading = ref(true)
const historyLoadedOnce = ref(false)
const manualRefreshingHistory = ref(false)
const freshRecordIds = ref<Set<string>>(new Set())
const selectedChain = ref('')
const selectedAsset = ref('')
const copied = ref(false)
const copyError = ref(false)
const copiedTxKey = ref('')
const qrError = ref(false)
const loadError = ref(false)
const historyError = ref(false)
const canPollHistory = computed(() =>
  !depositBlocked.value && !loading.value && info.value?.enabled === true && !historyError.value
)
let disposed = false
let historyInFlight = false
let copyTimer: ReturnType<typeof setTimeout> | null = null
let copiedTxTimer: ReturnType<typeof setTimeout> | null = null
const freshTimers = new Map<string, ReturnType<typeof setTimeout>>()
const qrCanvas = ref<HTMLCanvasElement | null>(null)
let pollTimer: ReturnType<typeof setTimeout> | null = null

const current = computed(() => info.value?.chains.find((c) => c.chain === selectedChain.value))
const networkStatuses = shallowRef<DepositNetworkStatus[]>([])
const networkStatusRequest = shallowRef<AbortController | null>(null)
let networkExpiryTimer: ReturnType<typeof setTimeout> | null = null
// Only rows the backend still vouches for: an expired row is not evidence and
// must not keep a "delayed" badge on screen. Maps keep the template lookups O(1).
const networkStatusByChain = computed<Map<string, DepositNetworkStatus>>(() =>
  new Map(networkStatuses.value.map(row => [row.chain, row]))
)

function chainInfo(chain: string): DepositChain | undefined {
  return info.value?.chains.find(c => c.chain === chain)
}

function networkState(chain: string): DepositNetworkState {
  return networkStateOf(networkStatusByChain.value, chain)
}

// The fresh status row is the primary estimate; deposit-info repeats the same
// field so a backend that only reports it there still gets a badge.
function networkEta(chain: string): number | null {
  return parseEtaSeconds(networkStatusByChain.value.get(chain)?.eta_seconds)
    ?? parseEtaSeconds(chainInfo(chain)?.eta_seconds)
}

function instantCredit(chain: string): boolean {
  return chainInfo(chain)?.instant_credit === true
}

function networkView(chain: string) {
  return { state: networkState(chain), etaSeconds: networkEta(chain), instantCredit: instantCredit(chain) }
}

interface ChainOption {
  chain: string
  label: string
  badge: DepositNetworkBadge
  eta: string | null
}

// One entry per network card: name, accuracy badge and the arrival estimate.
const chainOptions = computed<ChainOption[]>(() => (info.value?.chains ?? []).map(c => {
  const view = networkView(c.chain)
  return { chain: c.chain, label: chainLabel(c.chain), badge: networkBadge(view, t), eta: cardEtaText(view, t) }
}))

const selectedNetworkState = computed(() => networkState(selectedChain.value))
const selectedInstantCredit = computed(() => instantCredit(selectedChain.value))
// Top notice: the arrival estimate normally, the instant-credit rule for a chain
// that credits on detection and can roll that credit back.
const etaNotice = computed(() => etaNoticeText(networkView(selectedChain.value), t))
const networkTransferBlocked = computed(() =>
  selectedNetworkState.value === 'paused' || selectedNetworkState.value === 'stalled'
)
// 'unknown' means the backend has no fresh evidence: that is not a delay and
// must not raise a notice. Only an observed slow/delayed/failed network does.
const NOTICE_STATES: readonly DepositNetworkState[] = ['slow', 'delayed', 'paused', 'stalled']
const selectedNetworkNotice = computed<DepositNetworkState | null>(() =>
  NOTICE_STATES.includes(selectedNetworkState.value) ? selectedNetworkState.value : null
)

// Switching network changes which chain the page is about, so its freshness
// matters. The refresh is silent: a failed read leaves the previous, still
// unexpired observations in place.
function selectChain(chain: string) {
  if (chain === selectedChain.value) return
  selectedChain.value = chain
  void refreshNetworkStatuses()
}

// A backend that predates native-coin support sends only `tokens`. Those are
// all ERC-20 assets, so the derived list keeps the page identical for it.
function chainAssets(chain: DepositChain): AssetOption[] {
  const declared = (chain.assets ?? []).filter(a => a && a.symbol)
  const assets: AssetOption[] = declared.length
    ? declared
    : chain.tokens.map(symbol => ({ symbol, native: false, market_priced: false }))
  // Stablecoins first: an existing user's page must not start on a currency
  // worth a thousand times more. The mainnet coin is an explicit choice.
  return [...assets.filter(a => !a.native), ...assets.filter(a => a.native)]
}

const currentAssets = computed<AssetOption[]>(() => (current.value ? chainAssets(current.value) : []))

// The first asset is the default; it is also the fallback when the selected
// symbol is not accepted on the newly chosen network.
const selectedOption = computed<AssetOption | null>(() =>
  currentAssets.value.find(a => a.symbol === selectedAsset.value) ?? currentAssets.value[0] ?? null
)
const selectedSymbol = computed(() => selectedOption.value?.symbol ?? '')
// Rate and limits are expressed per unit of the asset being sent, so the
// sidebar must name that asset rather than assume USDT.
const rateUnit = computed(() => selectedSymbol.value || 'USDT')

// A market-priced asset (ETH, BNB) settles at the price observed when the
// deposit lands. There is no fixed rate to print, and the thresholds are
// compared as USD notional (amount × price), not as a coin amount.
const selectedMarketPriced = computed(() => selectedOption.value?.market_priced === true)

// Fixed rate for the selected asset. New backends send it per asset; an older
// backend only sends the asset-independent rate, which stays the fallback so a
// rolling deploy still renders the stablecoin line.
const selectedFixedRate = computed<number | null>(() => {
  if (selectedMarketPriced.value) return null
  const assetRate = selectedOption.value?.rate
  if (typeof assetRate === 'number' && Number.isFinite(assetRate)) return assetRate
  const legacyRate = info.value?.rate
  return typeof legacyRate === 'number' && Number.isFinite(legacyRate) ? legacyRate : null
})

// Display-only market quote ("about $3210.12"). Empty when the backend could
// not reach the price feed: the page then shows no reference line at all.
const referencePrice = computed(() => {
  if (!selectedMarketPriced.value) return ''
  const price = selectedOption.value?.reference_price_usd
  return typeof price === 'string' ? price.trim() : ''
})

const limitUnit = computed(() => (selectedMarketPriced.value ? 'USD' : rateUnit.value))

function assetLabel(asset: AssetOption): string {
  if (!asset.native) return asset.symbol
  return `${asset.symbol} (${nativeNetworkLabel(selectedChain.value)})`
}

// A native coin is the network's own currency, not a token standard, so it is
// labelled with the bare network name.
function nativeNetworkLabel(chain: string): string {
  switch (chain) {
    case 'ethereum':
      return 'Ethereum'
    case 'bsc':
      return 'BSC'
    default:
      return chainLabel(chain).replace(/\s*\([^)]*\)$/, '')
  }
}

function rowProgress(row: DepositRecord): ConfirmationProgress | null {
  return confirmationProgress(row)
}

// Both in-flight states (pending and provisional) get a progress line; a row
// without counters still says "confirming" rather than inventing n/m.
function isConfirmingRow(row: DepositRecord): boolean {
  return isConfirmingDeposit(row.status)
}

function progressText(row: DepositRecord, progress: ConfirmationProgress | null): string {
  const copy = depositProgressCopy(row, progress)
  return copy.params ? t(copy.key, copy.params) : t(copy.key)
}

const rows = computed<HistoryRow[]>(() => {
  const keys = uniqueDepositRowKeys(history.value)
  return history.value.map((row, index) => ({ key: keys[index], row, progress: rowProgress(row) }))
})
const pendingRows = computed(() => rows.value.filter(item => item.row.status === 'pending'))

// ---- 充值记录本地筛选 ----
// The history endpoint is not paginated (one page of recent rows), so the
// filters run in the browser: they never trigger a request and never interrupt
// the 5s refresh loop. Filtering on `rows` preserves every row's stable key
// computed from the full snapshot so duplicate tx_hash entries keep their identity.
const statusFilter = ref<DepositStatusFilter>('all')
const timeRange = ref<DepositTimeRange>('all')
const filtersActive = computed(() => isDepositFilterActive(statusFilter.value, timeRange.value))
const displayRows = computed<HistoryRow[]>(() => {
  const now = Date.now()
  return rows.value.filter(
    entry => matchesDepositStatus(entry.row, statusFilter.value) && matchesDepositTimeRange(entry.row, timeRange.value, now)
  )
})

function resetFilters() {
  statusFilter.value = 'all'
  timeRange.value = 'all'
}

// Switching filters indicates the user is actively browsing; clear any transient
// "newly arrived" highlight so rows returning into view never replay pop-in motion.
watch([statusFilter, timeRange], () => {
  clearFreshRecords()
})

function hasRowCallout(row: DepositRecord): boolean {
  return Boolean(
    creditUnderReview(row) ||
    revokeReasonText(row) ||
    row.reason ||
    row.status === 'revoked' ||
    row.status === 'provisional'
  )
}

function hasRowDetails(row: DepositRecord): boolean {
  return hasRowCallout(row) || isConfirmingRow(row) || Boolean(row.tx_hash)
}

function rowCalloutClass(row: DepositRecord): string {
  if (row.status === 'revoked') return 'deposit-view__callout--revoked'
  if (row.status === 'provisional' || creditUnderReview(row)) return 'deposit-view__callout--warning'
  return 'deposit-view__callout--neutral'
}

// ---- 记录列表行 ----
// 充值记录映射成共享 TransactionListCard 的行模型：右列只放「余额」语义的金额，
// 收入(绿)/支出(墨色)/回滚(玫红)三色由 tone 决定；待确认或未入账的行不放金额，
// 只保留状态徽标和明细，避免把「10 USDT」误读成已到账余额。
interface DepositHistoryItem extends TransactionItem {
  row: DepositRecord
  progress: ConfirmationProgress | null
}

function historyTone(row: DepositRecord): TransactionTone {
  if (row.status === 'revoked') return 'reversal'
  if (row.status === 'provisional') return 'positive'
  if (row.status === 'pending' || creditUnderReview(row)) return 'pending'
  if (row.status === 'credited') return 'positive'
  return 'neutral'
}

function historyStatus(row: DepositRecord): TransactionStatus | undefined {
  if (row.status === 'credited' && !creditUnderReview(row)) return undefined
  if (creditUnderReview(row)) {
    return { label: t('deposit.creditUnknown'), color: 'warning', testId: 'deposit-credit-unknown' }
  }
  const color: TransactionStatus['color'] =
    row.status === 'revoked' ? 'error'
      : row.status === 'pending' || row.status === 'provisional' ? 'warning'
        : 'neutral'
  return { label: statusLabel(row), color, testId: 'deposit-history-status' }
}

function toHistoryItem({ row, progress }: HistoryRow, id: string, isNew: boolean): DepositHistoryItem {
  // 已回滚的行只有在原始入账金额可信时才显示 -$X；未知就是未知，不编 0。
  const creditedAmount =
    (row.status === 'credited' || row.status === 'provisional' || row.status === 'revoked') && hasKnownCredit(row)
      ? fmtCredited(row.credited)
      : undefined
  return {
    id,
    title: `${t(row.status === 'revoked' ? 'deposit.historyRowReversal' : 'deposit.historyRowDeposit')} · ${chainLabel(row.chain)}`,
    subtitle: `${row.amount} ${row.token} · ${formatTime(row.created_at)}`,
    type: row.status === 'revoked' ? 'reversal' : 'chain',
    amount: creditedAmount,
    amountPrefix: creditedAmount ? '$' : undefined,
    tone: historyTone(row),
    status: historyStatus(row),
    isNew,
    testId: 'deposit-history-row',
    row,
    progress
  }
}

const historyItems = computed<DepositHistoryItem[]>(() => {
  const fresh = freshRecordIds.value
  return displayRows.value.map(entry => toHistoryItem(entry, entry.key, fresh.has(entry.key)))
})

// ---- 回滚行的客服入口 ----
// Reuse the site's only public contact field (same source as the user menu).
// A URL/email/Telegram handle becomes a link; free text ("QQ: 123456789")
// becomes a copy action, so the user always gets the value even without a URL.
const contactInfo = computed(() => (appStore.contactInfo || '').trim())
const supportLink = computed(() => supportContactHref(contactInfo.value))
const supportCopied = ref(false)
let supportCopiedTimer: ReturnType<typeof setTimeout> | null = null

async function copySupportContact() {
  if (!contactInfo.value) return
  if (!(await copyToClipboard(contactInfo.value))) return
  supportCopied.value = true
  if (supportCopiedTimer) clearTimeout(supportCopiedTimer)
  supportCopiedTimer = setTimeout(() => (supportCopied.value = false), 1800)
}

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

// Deposits are positive. Older backends use 0 when no original settlement
// order can be found; never present that unknown value as a confirmed credit.
function hasKnownCredit(row: DepositRecord): boolean {
  return row.credited_known !== false && Number.isFinite(row.credited) && row.credited > 0
}

// A credited row without a trustworthy amount is "under review", not "zero".
function creditUnderReview(row: DepositRecord): boolean {
  return row.status === 'credited' && !hasKnownCredit(row)
}

function statusLabel(row: DepositRecord): string {
  return creditUnderReview(row) ? t('deposit.statusReconciliation') : statusText(row.status)
}

// A revoked credit carries the machine reason in revoke_reason; translate it
// instead of printing the raw code. Empty when the backend omits the field
// (older instances), so the plain status wording remains readable.
function revokeReasonText(row: DepositRecord): string {
  if (!row.revoke_reason) return ''
  const copy = depositRevokeReasonCopy(row.revoke_reason)
  return copy.params ? t(copy.key, copy.params) : t(copy.key)
}

// History stays readable without deposit-info, so omit thresholds we don't have.
// `status` only changes the fallback wording: a rolled-back credit was once
// credited, so its unknown reason must not read as "not credited".
function reasonText(code: string, nativeAsset = false, status = ''): string {
  const limits = info.value?.enabled ? info.value : null
  switch (code) {
    case 'below_min':
      if (!limits) return t('deposit.reasonOther', { code })
      return t(nativeAsset ? 'deposit.reasonBelowMinUsd' : 'deposit.reasonBelowMin', { min: limits.min_amount })
    case 'above_max':
      if (!limits) return t('deposit.reasonOther', { code })
      return t(nativeAsset ? 'deposit.reasonAboveMaxUsd' : 'deposit.reasonAboveMax', { max: limits.max_amount })
    case 'address_unassigned':
      return t('deposit.reasonUnassigned')
    case 'address_unknown':
      return t('deposit.reasonUnknown')
    default:
      // A new backend code must never surface as a bare machine string.
      return status === 'revoked'
        ? t('deposit.reasonRevoked', { code })
        : t('deposit.reasonOther', { code })
  }
}

// Status badges use the same vocabulary as the ledger, translated.
function statusText(status: string): string {
  switch (status) {
    case 'credited':
      return t('deposit.statusCredited')
    case 'pending':
      return t('deposit.statusConfirming')
    // Instant-credit chains: the balance is credited before the transfer is
    // final, and withdrawn again if the transaction ultimately fails.
    case 'provisional':
      return t('deposit.statusProvisional')
    case 'revoked':
      return t('deposit.statusRevoked')
    case 'ignored':
      return t('deposit.statusIgnored')
    case 'reverted':
      return t('deposit.statusReverted')
    default:
      return t('deposit.statusOther', { code: status })
  }
}

async function copyAddress() {
  if (depositBlocked.value || networkTransferBlocked.value || !current.value) return
  copyError.value = false
  if (await copyToClipboard(current.value.address)) {
    copied.value = true
    if (copyTimer) clearTimeout(copyTimer)
    copyTimer = setTimeout(() => (copied.value = false), 1800)
  } else {
    copyError.value = true
  }
}

async function copyTxHash(txHash: string, rowKey: string) {
  if (!txHash) return
  if (await copyToClipboard(txHash)) {
    copiedTxKey.value = rowKey
    if (copiedTxTimer) clearTimeout(copiedTxTimer)
    copiedTxTimer = setTimeout(() => {
      if (copiedTxKey.value === rowKey) copiedTxKey.value = ''
    }, 1800)
  }
}

async function renderQR() {
  const canvas = qrCanvas.value
  const chain = current.value
  if (!canvas || !chain || depositBlocked.value || networkTransferBlocked.value) return
  try {
    await QRCode.toCanvas(canvas, chain.address, { width: 184, margin: 1 })
    if (qrCanvas.value === canvas && current.value === chain) qrError.value = false
  } catch {
    if (qrCanvas.value === canvas && current.value === chain) qrError.value = true
  }
}

function depositKey(row: DepositRecord): string {
  return depositTransferIdentity(row)
}

function clearFreshRecords() {
  for (const timer of freshTimers.values()) clearTimeout(timer)
  freshTimers.clear()
  if (freshRecordIds.value.size > 0) freshRecordIds.value = new Set()
}

function markFreshRecords(keys: string[]) {
  if (!keys.length) return
  const next = new Set(freshRecordIds.value)
  for (const key of keys) {
    next.add(key)
    const existing = freshTimers.get(key)
    if (existing) clearTimeout(existing)
    freshTimers.set(key, setTimeout(() => {
      freshTimers.delete(key)
      if (!freshRecordIds.value.has(key)) return
      const updated = new Set(freshRecordIds.value)
      updated.delete(key)
      freshRecordIds.value = updated
    }, FRESH_HIGHLIGHT_MS))
  }
  freshRecordIds.value = next
}

// A deposit that was confirming on the last read and is credited now has just
// landed: tell the user and reload the balance shown in the header.
function announceCredited(previous: DepositRecord[], next: DepositRecord[]) {
  const wasPending = new Set(previous.filter(r => r.status === 'pending' && r.tx_hash).map(depositKey))
  const landed = next.filter(r => r.status === 'credited' && r.tx_hash && wasPending.has(depositKey(r)))
  if (!landed.length) return
  for (const r of landed.filter(hasKnownCredit)) appStore.showSuccess(t('deposit.creditedToast', { amount: fmtCredited(r.credited) }))
  void authStore.refreshUser({ force: true }).catch(() => undefined)
}

function stopPolling() {
  if (pollTimer) clearTimeout(pollTimer)
  pollTimer = null
}

function schedulePoll() {
  stopPolling()
  if (disposed || !canPollHistory.value || document.visibilityState === 'hidden') return
  pollTimer = setTimeout(() => void refreshHistory(), HISTORY_POLL_MS)
}

async function refreshHistory(options: { manual?: boolean } = {}) {
  if (disposed || depositBlocked.value || loading.value) return
  if (historyInFlight) {
    // A silent background poll is already reading the latest history: reflect
    // loading feedback on the button until that in-flight read completes.
    if (options.manual) manualRefreshingHistory.value = true
    return
  }
  stopPolling()
  historyInFlight = true
  if (options.manual) manualRefreshingHistory.value = true
  try {
    const next = await getDepositHistory()
    if (disposed || depositBlocked.value) return
    const hadLoaded = historyLoadedOnce.value
    historyLoadedOnce.value = true
    if (historyError.value) historyError.value = false
    // 后台检测若无新增记录也无状态变化，不替换数组引用，避免无意义重绘和页面抖动。
    if (hadLoaded && isSameDepositHistory(history.value, next)) return
    const addedKeys = hadLoaded ? findNewDepositKeys(history.value, next) : []
    announceCredited(history.value, next)
    history.value = next
    if (addedKeys.length) markFreshRecords(addedKeys)
  } catch (error) {
    if (isKeyRotationRequiredError(error)) keyRotationDenied.value = true
    else historyError.value = true
  } finally {
    historyInFlight = false
    initialHistoryLoading.value = false
    manualRefreshingHistory.value = false
    schedulePoll()
  }
}

// Replace the observations with the ones that are still fresh, and arm a timer
// that drops them the moment they expire — locally, even while another read is
// pending or no read ever succeeds again. A stale "delayed" badge must never
// outlive its own evidence.
function applyNetworkStatuses(next: DepositNetworkStatus[]) {
  if (networkExpiryTimer !== null) clearTimeout(networkExpiryTimer)
  networkExpiryTimer = null
  const now = Date.now()
  const pruned = pruneNetworkStatuses(next, now)
  networkStatuses.value = pruned.rows
  if (pruned.nextExpiry !== null) {
    networkExpiryTimer = setTimeout(
      () => applyNetworkStatuses(networkStatuses.value), Math.min(pruned.nextExpiry - now, 2_147_483_647)
    )
  }
}

// An empty read is not evidence that every network went unknown (a refresh is
// best-effort and must stay silent on failure): keep the last observations until
// their own expiry retires them, so a hiccup cannot flash the page back to
// "checking". An authoritative row for a chain always replaces it.
function acceptNetworkStatuses(next: DepositNetworkStatus[]) {
  applyNetworkStatuses(next.length ? next : networkStatuses.value)
}

async function refreshNetworkStatuses() {
  if (disposed || depositBlocked.value || networkStatusRequest.value) return
  const request = new AbortController()
  networkStatusRequest.value = request
  try {
    const next = await getDepositNetworkStatus(request.signal)
    if (!disposed && !depositBlocked.value && !request.signal.aborted) acceptNetworkStatuses(next)
  } catch (error) {
    if (disposed || request.signal.aborted) return
    if (isKeyRotationRequiredError(error)) keyRotationDenied.value = true
    // A background refresh that fails is silent: the existing observations keep
    // running until their expiry, and the user is never told a working network
    // is delayed because one read failed.
    else applyNetworkStatuses(networkStatuses.value)
  } finally {
    if (networkStatusRequest.value === request) networkStatusRequest.value = null
  }
}

// Window focus is the cheapest signal that the user came back to the page, so
// the badge is re-read once without a poll. Failures stay silent.
function onWindowFocus() {
  if (document.visibilityState === 'hidden') return
  void refreshNetworkStatuses()
}

function resetNetworkStatuses() {
  networkStatusRequest.value?.abort()
  networkStatusRequest.value = null
  applyNetworkStatuses([])
}

function onVisibilityChange() {
  if (document.visibilityState === 'hidden') {
    stopPolling()
    return
  }
  // Browser timers can be throttled while the tab is hidden.
  applyNetworkStatuses(networkStatuses.value)
  void refreshNetworkStatuses()
  if (canPollHistory.value) void refreshHistory()
}

async function load() {
  stopPolling()
  loading.value = true
  loadError.value = false
  void refreshNetworkStatuses()
  try {
    info.value = await getDepositInfo()
    if (!info.value.chains.some(c => c.chain === selectedChain.value)) {
      selectedChain.value = info.value.chains[0]?.chain ?? ''
    }
  } catch (error) {
    info.value = null
    if (isKeyRotationRequiredError(error)) keyRotationDenied.value = true
    else loadError.value = true
  } finally {
    loading.value = false
  }
  // Wait for deposit-info to reach the existing gate before reading history.
  // Closed funding and ordinary read errors still allow the history request.
  void refreshHistory()
}

watch(depositBlocked, (blocked) => {
  if (blocked) {
    stopPolling()
    resetNetworkStatuses()
    clearFreshRecords()
    historyLoadedOnce.value = false
    history.value = []
  } else {
    initialHistoryLoading.value = !historyLoadedOnce.value
    void refreshNetworkStatuses()
    void refreshHistory()
  }
})

// The canvas is recreated when a pause ends or the status expires, even if
// neither the address nor the selected network changed.
watch([current, qrCanvas], () => {
  copied.value = false
  copyError.value = false
  // A symbol accepted on the previous network may not exist on this one.
  if (!currentAssets.value.some(a => a.symbol === selectedAsset.value)) {
    selectedAsset.value = currentAssets.value[0]?.symbol ?? ''
  }
  void renderQR()
}, { flush: 'post' })

onMounted(() => {
  document.addEventListener('visibilitychange', onVisibilityChange)
  window.addEventListener('focus', onWindowFocus)
  void load()
})

onUnmounted(() => {
  disposed = true
  document.removeEventListener('visibilitychange', onVisibilityChange)
  window.removeEventListener('focus', onWindowFocus)
  stopPolling()
  resetNetworkStatuses()
  clearFreshRecords()
  if (copyTimer) clearTimeout(copyTimer)
  if (copiedTxTimer) clearTimeout(copiedTxTimer)
  if (supportCopiedTimer) clearTimeout(supportCopiedTimer)
})
</script>

<style scoped>
.deposit-view { width: 100%; max-width: 76rem; margin: 0 auto; padding: clamp(1rem, 2.4vw, 2rem); color: var(--signal-text, inherit); }
.deposit-view__header { margin-bottom: 1.5rem; }
.deposit-view__layout { display: grid; grid-template-columns: minmax(0, 1fr) 18rem; gap: 1.25rem; align-items: start; }
.deposit-view__funding, .deposit-view__guide { min-width: 0; border: 1px solid var(--ui-border); border-radius: 8px; padding: 1.5rem; background: var(--ui-bg); box-shadow: var(--app-shadow-card, 0 1px 2px rgba(10, 10, 10, 0.05)); }
.deposit-view h2 { font-size: .9375rem; line-height: 1.5; font-weight: 650; }
.deposit-view__section-head { display: flex; align-items: center; gap: .625rem; margin-bottom: 1rem; }
.deposit-view__section-head--network { flex-wrap: wrap; }
.deposit-view__network-refresh { margin-inline-start: auto; }
.deposit-view__section-head--address { padding-top: 1.25rem; margin-top: 1.5rem; border-top: 1px solid var(--signal-line, var(--ui-border)); }
.deposit-view__step { color: var(--signal-muted, var(--ui-text-muted)); font: .75rem ui-monospace, monospace; }
.deposit-view__chains { display: flex; flex-wrap: wrap; gap: .5rem; }
.deposit-view__chain { display: inline-flex; flex-direction: column; align-items: flex-start; justify-content: center; gap: .3rem; min-height: 2.5rem; padding: .55rem .875rem; border: 1px solid var(--ui-border-accented); border-radius: 6px; font-size: .8125rem; transition: border-color .15s, background .15s; }
.deposit-view__chain:focus-visible { outline: 2px solid var(--signal-accent, var(--ui-primary)); outline-offset: 2px; }
.deposit-view__chain--active { border-color: var(--signal-accent, var(--ui-primary)); background: var(--signal-accent-soft, color-mix(in oklab, var(--ui-primary) 10%, transparent)); font-weight: 600; }
.deposit-view__chain-head { display: inline-flex; align-items: center; flex-wrap: wrap; gap: .5rem; }
/* 每个网络卡片下的到账预估，跟着徽标一起回答「多久到」。 */
.deposit-view__chain-eta { color: var(--signal-muted, var(--ui-text-muted)); font-size: .6875rem; font-weight: 400; line-height: 1.4; }
.deposit-view__network-dot { width: .375rem; height: .375rem; flex-shrink: 0; border-radius: 50%; background: var(--signal-muted, var(--ui-text-muted)); }
.deposit-view__chain--active .deposit-view__network-dot { background: var(--signal-accent, var(--ui-primary)); }
.deposit-view__eta-notice { display: flex; gap: .625rem; align-items: flex-start; padding: .75rem 1rem; margin-top: .75rem; border: 1px solid color-mix(in oklab, var(--signal-accent, var(--ui-primary)) 26%, transparent); border-radius: .5rem; background: color-mix(in oklab, var(--signal-accent, var(--ui-primary)) 6%, transparent); font-size: .75rem; line-height: 1.7; }
.deposit-view__eta-mark { display: grid; place-items: center; width: 1rem; height: 1rem; flex-shrink: 0; margin-top: .15rem; border: 1px solid var(--signal-accent, var(--ui-primary)); border-radius: 50%; color: var(--signal-accent, var(--ui-primary)); font-size: .6875rem; font-weight: 700; }
.deposit-view__instant-rule { margin-top: .75rem; padding: .625rem .75rem; border: 1px solid color-mix(in oklab, var(--signal-accent, var(--ui-primary)) 24%, transparent); border-radius: .5rem; background: color-mix(in oklab, var(--signal-accent, var(--ui-primary)) 6%, transparent); color: var(--signal-muted, var(--ui-text-muted)); font-size: .75rem; line-height: 1.7; }
.deposit-view__assets { display: flex; flex-wrap: wrap; gap: .5rem; margin-top: .75rem; }
.deposit-view__asset { display: inline-flex; align-items: center; justify-content: center; min-height: 2.25rem; padding: .45rem .75rem; border: 1px solid var(--ui-border); border-radius: 6px; font-size: .8125rem; transition: border-color .15s, background .15s; }
.deposit-view__asset:focus-visible { outline: 2px solid var(--signal-accent, var(--ui-primary)); outline-offset: 2px; }
.deposit-view__asset--active { border-color: var(--signal-accent, var(--ui-primary)); background: var(--signal-accent-soft, color-mix(in oklab, var(--ui-primary) 10%, transparent)); font-weight: 600; }
.deposit-view__token { padding: .15rem .5rem; border-radius: .375rem; border: 1px solid #10b98145; background: #10b98112; color: #10b981; font-size: .6875rem; font-weight: 600; }
.deposit-view__address-block { display: grid; grid-template-columns: 12.5rem minmax(0, 1fr); gap: 1.25rem; align-items: start; }
.deposit-view__qr-wrap { text-align: center; }
.deposit-view__qr { width: 12.5rem; min-height: 12.5rem; display: grid; place-items: center; border: 1px solid #e5e5e5; border-radius: 8px; background: white; color: #334155; overflow: hidden; }
.deposit-view__qr canvas { display: block; max-width: 100%; height: auto !important; }
.deposit-view__qr-wrap p { margin-top: .625rem; font-size: .75rem; color: var(--signal-muted, var(--ui-text-muted)); }
.deposit-view__label { display: block; margin: .2rem 0 .625rem; font-size: .75rem; color: var(--signal-muted, var(--ui-text-muted)); }
.deposit-view__address { display: block; padding: .875rem; min-width: 0; border: 1px solid var(--ui-border); border-radius: 6px; background: var(--ui-bg-muted); font-size: .8125rem; line-height: 1.8; overflow-wrap: anywhere; user-select: all; }
.deposit-view__copy { width: 100%; min-height: 2.75rem; margin-top: .75rem; }
.deposit-view__copy-status { margin-top: .75rem; font-size: .75rem; line-height: 1.7; color: var(--signal-muted, var(--ui-text-muted)); }
.deposit-view__notice, .deposit-view__network-notice { display: flex; gap: .625rem; align-items: flex-start; padding: .875rem 1rem; margin-top: 1.5rem; border: 1px solid #d9770638; border-radius: .5rem; background: #d977060a; font-size: .75rem; line-height: 1.7; }
.deposit-view__network-notice { margin-top: .75rem; }
.deposit-view__notice-mark { display: grid; place-items: center; width: 1rem; height: 1rem; flex-shrink: 0; margin-top: .15rem; border: 1px solid #d97706; border-radius: 50%; color: #d97706; font-size: .6875rem; font-weight: 700; }
.deposit-view__terms { margin-top: 1rem; }
.deposit-view__desc { margin-top: .25rem; color: var(--signal-muted, var(--ui-text-muted)); font-size: .75rem; line-height: 1.55; }
.deposit-view__terms > div { padding: 1rem 0; border-top: 1px solid var(--signal-line, var(--ui-border)); }
.deposit-view__terms dt { color: var(--signal-muted, var(--ui-text-muted)); font-size: .75rem; }
.deposit-view__terms dd { margin-top: .5rem; font-size: 1.1875rem; font-weight: 600; font-variant-numeric: tabular-nums; }
.deposit-view__terms dd span { font-size: .8125rem; font-weight: 400; }
.deposit-view__terms dd.deposit-view__settlement { font-size: .9375rem; line-height: 1.55; font-weight: 600; }
.deposit-view__reference { margin-top: .75rem; color: var(--signal-muted, var(--ui-text-muted)); font-size: .75rem; line-height: 1.6; }
.deposit-view__confirmation { display: flex; align-items: flex-start; gap: .625rem; margin-top: .5rem; padding: 1rem; border-radius: .5rem; background: #10b9810d; }
.deposit-view__confirmation-dot { width: .375rem; height: .375rem; flex-shrink: 0; margin-top: .4rem; background: #10b981; border-radius: 50%; }
.deposit-view__confirmation strong { font-size: .8125rem; font-weight: 600; }
.deposit-view__confirmation p { margin-top: .4rem; color: var(--signal-muted, var(--ui-text-muted)); font-size: .75rem; line-height: 1.7; }

/* ---- 我的充值记录 ---- */
.deposit-view__history { margin-top: 1.25rem; }
.deposit-view__history-head { display: flex; justify-content: flex-end; align-items: center; gap: .75rem; }
.deposit-view__toolbar { display: flex; flex-direction: column; gap: .75rem; }
.deposit-view__filter-notes { display: flex; flex-wrap: wrap; align-items: center; gap: .25rem 1rem; }
.deposit-view__filter-note { margin: 0; color: var(--ui-text-muted); font-size: .75rem; line-height: 1.5; }
.deposit-view__history-error { margin: 0; padding: .5rem .75rem; border: 1px solid color-mix(in oklab, var(--signal-amber, #d97706) 35%, transparent); border-radius: .5rem; background: color-mix(in oklab, var(--signal-amber, #d97706) 8%, transparent); color: var(--signal-amber, #b45309); font-size: .8125rem; line-height: 1.55; }

.deposit-view__pending { display: flex; align-items: center; gap: .625rem; min-width: 0; padding: .625rem .875rem; border: 1px solid color-mix(in oklab, #d97706 30%, var(--ui-border)); border-radius: .5rem; background: color-mix(in oklab, #d97706 7%, var(--ui-bg)); font-size: .8125rem; line-height: 1.55; }
.deposit-view__pending-dot { width: .5rem; height: .5rem; flex-shrink: 0; border-radius: 50%; background: #d97706; box-shadow: 0 0 0 3px color-mix(in oklab, #d97706 20%, transparent); animation: deposit-view-pulse 1.8s ease-in-out infinite; }
.deposit-view__pending-body { min-width: 0; flex: 1 1 auto; display: flex; flex-wrap: wrap; align-items: center; gap: .375rem .75rem; }
.deposit-view__pending-body strong { font-size: .8125rem; font-weight: 600; color: var(--ui-text-highlighted); }
.deposit-view__pending-list { display: flex; flex-wrap: wrap; gap: .375rem; margin: 0; padding: 0; list-style: none; font-variant-numeric: tabular-nums; }
.deposit-view__pending-list li { display: inline-flex; align-items: center; padding: .125rem .55rem; border: 1px solid color-mix(in oklab, #d97706 24%, var(--ui-border)); border-radius: 999px; background: var(--ui-bg); color: var(--ui-text); font-size: .75rem; line-height: 1.45; overflow-wrap: anywhere; }
.deposit-view__pending-more { margin: 0; color: var(--ui-text-muted); font-size: .75rem; }

/* 记录行明细：状态说明条、确认进度与交易哈希胶囊。 */
.deposit-view__details { display: flex; flex-direction: column; align-items: flex-start; gap: .625rem; margin-top: .625rem; width: 100%; }
.deposit-view__callout { display: flex; flex-direction: column; align-items: flex-start; gap: .25rem; width: 100%; max-width: 38rem; padding: .5rem .75rem; border-radius: .375rem; border: 1px solid var(--ui-border); border-inline-start-width: 3px; background: var(--ui-bg-muted); }
.deposit-view__callout--revoked { border-color: color-mix(in oklab, var(--app-amount-reversal) 24%, var(--ui-border)); border-inline-start-color: var(--app-amount-reversal); background: color-mix(in oklab, var(--app-amount-reversal) 6%, var(--ui-bg)); }
.deposit-view__callout--warning { border-color: color-mix(in oklab, #d97706 26%, var(--ui-border)); border-inline-start-color: #d97706; background: color-mix(in oklab, #d97706 6%, var(--ui-bg)); }
.deposit-view__callout--neutral { border-color: var(--ui-border); border-inline-start-color: var(--ui-border-accented); background: var(--ui-bg-muted); }

.deposit-view__transaction { width: 100%; max-width: 100%; }
.deposit-view__hash-row { display: inline-flex; align-items: center; gap: .25rem; max-width: min(100%, 28rem); padding: .15rem .2rem .15rem .6rem; border: 1px solid var(--ui-border); border-radius: .375rem; background: var(--ui-bg-muted); transition: border-color 140ms ease, background-color 140ms ease; }
.deposit-view__hash { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font: .75rem/1.5 ui-monospace, SFMono-Regular, Menlo, monospace; color: var(--ui-text-muted); }
a.deposit-view__hash { text-decoration: none; transition: color 140ms ease; }
.deposit-view__hash-ext { width: .75rem; height: .75rem; flex: 0 0 auto; color: var(--ui-text-dimmed); transition: color 140ms ease; pointer-events: none; }
.deposit-view__hash-copy { flex: 0 0 auto; color: var(--ui-text-muted); }
.deposit-view__hash-copy--done { color: var(--app-amount-positive); }

.deposit-view__reason { display: block; color: var(--ui-text-muted); font-size: .75rem; line-height: 1.55; }
.deposit-view__reason--revoked { color: var(--app-amount-reversal); }
/* 回滚行的客服入口：链接/按钮 + 纯文本联系方式的复制兜底。 */
.deposit-view__support { display: inline-flex; flex-wrap: wrap; align-items: baseline; gap: .35rem .5rem; margin-top: .125rem; color: var(--ui-text-muted); font-size: .75rem; line-height: 1.55; }
.deposit-view__support-action { padding: 0; color: var(--ui-primary); font-size: .75rem; font-weight: 600; text-decoration: underline; text-underline-offset: .15rem; }
.deposit-view__support-contact { color: var(--ui-text); font-variant-numeric: tabular-nums; overflow-wrap: anywhere; }
.deposit-view__support-copied { color: var(--app-amount-positive); font-weight: 500; }
.deposit-view__hint { display: block; color: var(--signal-amber, #b45309); font-size: .75rem; line-height: 1.55; }
.deposit-view__status { display: block; color: var(--signal-amber, #b45309); font-size: .75rem; line-height: 1.55; }

.deposit-view__progress { display: flex; flex-direction: column; gap: .375rem; width: 100%; max-width: 28rem; }
.deposit-view__progress-head { display: flex; align-items: baseline; justify-content: space-between; gap: .75rem; }
.deposit-view__progress-text { color: var(--ui-text-muted); font-size: .75rem; line-height: 1.45; font-variant-numeric: tabular-nums; }
.deposit-view__progress-pct { flex: 0 0 auto; color: var(--signal-amber, #b45309); font-size: .6875rem; font-weight: 600; font-variant-numeric: tabular-nums; }
.deposit-view__progress-track { display: block; height: .375rem; border-radius: 999px; background: color-mix(in oklab, #d97706 16%, var(--ui-bg-elevated)); overflow: hidden; }
.deposit-view__progress-fill { display: block; height: 100%; border-radius: inherit; background: linear-gradient(90deg, #d97706, #f59e0b); transition: width .35s cubic-bezier(0.22, 1, 0.36, 1); }

@keyframes deposit-view-pulse { 0%, 100% { opacity: 1; } 50% { opacity: .4; } }
.deposit-view__empty { display: flex; flex-direction: column; align-items: center; gap: .75rem; padding: 3rem 1.25rem; text-align: center; font-size: .875rem; }
.deposit-view__empty p { max-width: 27rem; font-size: .8125rem; color: var(--signal-muted, var(--ui-text-muted)); line-height: 1.7; }
.deposit-view button:focus-visible { outline: 2px solid var(--signal-accent, var(--ui-primary)); outline-offset: 3px; }
@media (max-width: 1100px) { .deposit-view__layout { grid-template-columns: minmax(0, 1fr); } .deposit-view__terms { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 1rem; } }
@media (max-width: 640px) {
  /* 底部安全区：iPhone 上最后一屏内容 / 复制按钮不被 Home 指示条压住。 */
  .deposit-view { padding: 1rem .75rem calc(1rem + env(safe-area-inset-bottom)); }
  .deposit-view__funding, .deposit-view__guide { padding: 1.125rem; border-radius: .875rem; }
  .deposit-view__chains { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); }
  .deposit-view__chain { padding: .65rem .4rem; font-size: .75rem; }
  .deposit-view__assets { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); }
  .deposit-view__address-block { grid-template-columns: minmax(0, 1fr); }
  .deposit-view__qr-wrap { justify-self: center; }
  .deposit-view__terms { grid-template-columns: minmax(0, 1fr); gap: 0; }
  .deposit-view__terms > div { display: flex; align-items: center; justify-content: space-between; gap: 1rem; }
  .deposit-view__terms dd { margin-top: 0; font-size: 1rem; text-align: right; }
  .deposit-view__callout { max-width: none; }
  .deposit-view__hash-row { width: 100%; max-width: 100%; justify-content: space-between; }
  .deposit-view__progress { max-width: none; }
  .deposit-view__pending { align-items: flex-start; padding: .625rem .75rem; }
  .deposit-view__pending-dot { margin-top: .375rem; }
}
/* 触屏上 :hover 会「粘」在上一次点过的元素上；悬停反馈只在真有指针的设备生效。 */
@media (hover: hover) and (pointer: fine) {
  .deposit-view__chain:hover { background: var(--signal-accent-soft, color-mix(in oklab, var(--ui-primary) 8%, transparent)); }
  .deposit-view__asset:hover { background: var(--signal-accent-soft, color-mix(in oklab, var(--ui-primary) 8%, transparent)); }
  .deposit-view__hash-row:hover { border-color: color-mix(in oklab, var(--ui-primary) 38%, var(--ui-border)); }
  .deposit-view__hash-row:hover .deposit-view__hash-ext { color: var(--ui-primary); }
  a.deposit-view__hash:hover { color: var(--ui-primary); text-decoration: underline; text-underline-offset: .15rem; }
  .deposit-view__hash-copy:hover { color: var(--ui-primary); }
}
@media (prefers-reduced-motion: reduce) { .deposit-view__chain, .deposit-view__progress-fill { transition: none; } .deposit-view__pending-dot { animation: none; } }
</style>

<style>
html.dark .deposit-view__funding,
html.dark .deposit-view__guide {
  box-shadow: var(--app-shadow-card);
}

html.dark .deposit-view__pending {
  border-color: rgb(251 207 104 / 0.24);
  background: rgb(251 207 104 / 0.07);
}

html.dark .deposit-view__pending-dot {
  background: #fbcf68;
  box-shadow: 0 0 0 3px rgb(251 207 104 / 0.18);
}

html.dark .deposit-view__pending-list li {
  border-color: rgb(251 207 104 / 0.2);
  background: var(--ui-bg-elevated);
}

html.dark .deposit-view__callout--revoked {
  border-color: rgb(252 165 165 / 0.24);
  border-inline-start-color: var(--app-amount-reversal);
  background: rgb(252 165 165 / 0.07);
}

html.dark .deposit-view__callout--warning {
  border-color: rgb(251 207 104 / 0.24);
  border-inline-start-color: #fbcf68;
  background: rgb(251 207 104 / 0.07);
}

html.dark .deposit-view__callout--neutral {
  background: var(--ui-bg-elevated);
}

html.dark .deposit-view__hint,
html.dark .deposit-view__status,
html.dark .deposit-view__progress-pct {
  color: #fbcf68;
}

html.dark .deposit-view__hash-row {
  background: var(--ui-bg-elevated);
}

html.dark .deposit-view__progress-track {
  background: rgb(251 207 104 / 0.14);
}

html.dark .deposit-view__progress-fill {
  background: linear-gradient(90deg, #f59e0b, #fbcf68);
}
</style>
