<template>
  <AppLayout>
    <div class="crypto-view">
      <div class="card crypto-view__card">
        <div class="crypto-view__header">
          <div class="crypto-view__icon">
            <Icon name="wallet" size="md" />
          </div>
          <div class="min-w-0">
            <h1 class="crypto-view__title">{{ t('admin.crypto.title') }}</h1>
            <p class="crypto-view__desc">{{ t('admin.crypto.description') }}</p>
          </div>
        </div>

        <div class="crypto-view__tabs" role="tablist">
          <button
            v-for="tab in tabs"
            :key="tab.id"
            type="button"
            role="tab"
            class="crypto-view__tab"
            :class="{ 'crypto-view__tab--active': activeTab === tab.id }"
            :aria-selected="activeTab === tab.id"
            @click="switchTab(tab.id)"
          >
            {{ tab.label }}
          </button>
        </div>

        <!-- Configuration -->
        <div v-if="activeTab === 'config'" class="crypto-view__panel crypto-view__panel--config">
          <div v-if="loadingConfig" class="crypto-view__empty">
            <LoadingSpinner />
          </div>
          <template v-else-if="config">
            <div class="crypto-view__group">
              <h4 class="crypto-view__group-title">{{ t('admin.crypto.groupChannel') }}</h4>
              <p class="crypto-view__group-desc">{{ t('admin.crypto.groupChannelDesc') }}</p>
              <div class="crypto-view__switch-row">
                <div class="space-y-0.5">
                  <label class="crypto-view__label">{{ t('admin.crypto.enabled') }}</label>
                  <p class="crypto-view__hint">{{ t('admin.crypto.enabledHint') }}</p>
                </div>
                <Toggle v-model="config.enabled" />
              </div>
              <div class="crypto-view__field">
                <label class="crypto-view__label">{{ t('admin.crypto.chains') }}</label>
                <input
                  :value="config.chains.join(',')"
                  class="input"
                  placeholder="bsc,polygon,plasma,arbitrum,base,xlayer"
                  @change="(e) => (config!.chains = splitList((e.target as HTMLInputElement).value))"
                />
                <p class="crypto-view__hint">{{ t('admin.crypto.chainsHint') }}</p>
              </div>
            </div>


            <div class="crypto-view__group">
              <h4 class="crypto-view__group-title">{{ t('admin.crypto.groupRpc') }}</h4>
              <p class="crypto-view__group-desc">{{ t('admin.crypto.groupRpcDesc') }}</p>
              <div v-for="chain in config.chains" :key="`rpc-${chain}`" class="crypto-view__field">
                <label class="crypto-view__label">{{ chain }}</label>
                <input
                  :value="config.rpc?.[chain] ?? ''"
                  class="input"
                  :placeholder="t('admin.crypto.rpcPlaceholder')"
                  @change="(e) => setRPC(chain, (e.target as HTMLInputElement).value)"
                />
              </div>
            </div>


            <div class="crypto-view__group">
              <h4 class="crypto-view__group-title">{{ t('admin.crypto.groupSweep') }}</h4>
              <p class="crypto-view__group-desc">{{ t('admin.crypto.groupSweepDesc') }}</p>
              <div class="crypto-view__field">
                <label class="crypto-view__label">{{ t('admin.crypto.globalThreshold') }}</label>
                <input
                  v-model.number="config.sweep_global_threshold"
                  type="number"
                  step="1"
                  class="input"
                />
                <p class="crypto-view__hint">{{ t('admin.crypto.globalThresholdHint') }}</p>
              </div>
              <div class="crypto-view__field">
                <label class="crypto-view__label">{{ t('admin.crypto.creditMin') }}</label>
                <input
                  v-model.number="config.credit_min_amount"
                  type="number"
                  step="0.001"
                  min="0"
                  class="input"
                />
                <p class="crypto-view__hint">{{ t('admin.crypto.creditMinHint') }}</p>
              </div>
              <div class="crypto-view__field">
                <label class="crypto-view__label">{{ t('admin.crypto.creditMax') }}</label>
                <input
                  v-model.number="config.credit_max_amount"
                  type="number"
                  step="1"
                  min="0"
                  class="input"
                />
                <p class="crypto-view__hint">{{ t('admin.crypto.creditMaxHint') }}</p>
              </div>
              <div class="crypto-view__field">
                <label class="crypto-view__label">{{ t('admin.crypto.creditMin') }}</label>
                <input
                  v-model.number="config.credit_min_amount"
                  type="number"
                  step="0.001"
                  min="0"
                  class="input"
                />
                <p class="crypto-view__hint">{{ t('admin.crypto.creditMinHint') }}</p>
              </div>
              <div class="crypto-view__field">
                <label class="crypto-view__label">{{ t('admin.crypto.creditMax') }}</label>
                <input
                  v-model.number="config.credit_max_amount"
                  type="number"
                  step="1"
                  min="0"
                  class="input"
                />
                <p class="crypto-view__hint">{{ t('admin.crypto.creditMaxHint') }}</p>
              </div>
              <div class="crypto-view__field">
                <label class="crypto-view__label">{{ t('admin.crypto.hotWalletEvm') }}</label>
                <input
                  v-model="config.hot_wallet_evm"
                  class="input"
                  :class="{ 'input--warn': !config.hot_wallet_evm.trim() }"
                  placeholder="0x…"
                />
                <p v-if="!config.hot_wallet_evm.trim()" class="crypto-view__warn">
                  {{ t('admin.crypto.hotWalletMissing') }}
                </p>
              </div>
              <div class="crypto-view__field">
                <label class="crypto-view__label">{{ t('admin.crypto.hotWalletSolana') }}</label>
                <input v-model="config.hot_wallet_solana" class="input" placeholder="…" />
              </div>
              <div class="crypto-view__field">
                <label class="crypto-view__label">{{ t('admin.crypto.hotIndex') }}</label>
                <input v-model.number="config.hot_index" type="number" min="0" step="1" class="input" />
                <p class="crypto-view__hint">{{ t('admin.crypto.hotIndexHint') }}</p>
              </div>
              <div class="crypto-view__switch-row">
                <div class="space-y-0.5">
                  <label class="crypto-view__label">{{ t('admin.crypto.sweepAuto') }}</label>
                  <p class="crypto-view__hint">{{ t('admin.crypto.sweepAutoHint') }}</p>
                </div>
                <Toggle v-model="config.sweep_auto" />
              </div>
            </div>


            <div class="crypto-view__group">
              <h4 class="crypto-view__group-title">{{ t('admin.crypto.groupSigner') }}</h4>
              <p class="crypto-view__group-desc">{{ t('admin.crypto.groupSignerDesc') }}</p>
              <div class="crypto-view__field">
                <label class="crypto-view__label">{{ t('admin.crypto.signerSocket') }}</label>
                <input v-model="config.signer_socket" class="input" />
              </div>
            </div>


            <!-- Signer lock: without the passphrase the process holds no
                 seeds and every signing request is refused. -->
            <div class="crypto-view__group">
              <h4 class="crypto-view__group-title">{{ t('admin.crypto.signerLockTitle') }}</h4>
              <p class="crypto-view__group-desc">{{ t('admin.crypto.signerLockDesc') }}</p>

              <div class="crypto-view__lockstate">
                <span
                  class="crypto-view__badge"
                  :class="signerUnlocked ? 'crypto-view__badge--active' : 'crypto-view__badge--failed'"
                >
                  {{ signerUnlocked ? t('admin.crypto.signerUnlocked') : t('admin.crypto.signerLocked') }}
                </span>
                <button class="btn btn-secondary btn-sm" :disabled="loadingSigner" @click="loadSignerStatus">
                  {{ t('common.refresh') }}
                </button>
              </div>

              <div v-if="!signerUnlocked" class="crypto-view__field">
                <label class="crypto-view__label">{{ t('admin.crypto.signerPassphrase') }}</label>
                <input
                  v-model="signerPassphrase"
                  type="password"
                  class="input"
                  autocomplete="new-password"
                  :placeholder="t('admin.crypto.signerPassphrasePlaceholder')"
                  @keyup.enter="submitUnlock"
                />
                <p class="crypto-view__hint">{{ t('admin.crypto.signerPassphraseHint') }}</p>
              </div>

              <div v-if="!signerUnlocked" class="crypto-view__actions">
                <button
                  class="btn btn-primary"
                  :disabled="unlocking || !signerPassphrase"
                  @click="submitUnlock"
                >
                  {{ unlocking ? t('common.saving') : t('admin.crypto.signerUnlockAction') }}
                </button>
                <span v-if="signerError" class="crypto-view__warn">{{ signerError }}</span>
              </div>
            </div>

            <div class="crypto-view__actions">
              <button class="btn btn-primary" :disabled="savingConfig" @click="saveConfig">
                {{ savingConfig ? t('common.saving') : t('common.save') }}
              </button>
              <span v-if="configSaved" class="crypto-view__saved">{{ t('common.saved') }}</span>
            </div>


            <!-- Health: an empty pool blocks new deposits; a dry hot wallet
                 blocks sweeps. Both are silent failures without this. -->
            <div class="crypto-view__group">
              <h4 class="crypto-view__group-title">{{ t('admin.crypto.healthTitle') }}</h4>
              <p class="crypto-view__group-desc">{{ t('admin.crypto.healthDesc') }}</p>

              <div class="crypto-view__stats">
                <div
                  v-for="(count, family) in poolStatus?.free ?? {}"
                  :key="`pool-${family}`"
                  class="crypto-view__stat"
                  :class="{ 'crypto-view__stat--warn': count <= (poolStatus?.low_watermark ?? 0) }"
                >
                  <span class="crypto-view__stat-label">{{ t('admin.crypto.poolFree') }} · {{ family }}</span>
                  <span class="crypto-view__stat-value">{{ count }}</span>
                </div>
                <div
                  v-for="hw in hotWallets"
                  :key="`hw-${hw.chain}`"
                  class="crypto-view__stat"
                  :class="{ 'crypto-view__stat--warn': !hw.enough || hw.error }"
                >
                  <span class="crypto-view__stat-label">
                    {{ hw.symbol }} · {{ hw.chain }}
                  </span>
                  <span class="crypto-view__stat-value">
                    {{ hw.error ? t('admin.crypto.hotWalletUnreachable') : hw.balance }}
                  </span>
                </div>
              </div>
              <div class="crypto-view__actions">
                <button class="btn btn-secondary btn-sm" :disabled="loadingHealth" @click="loadHealth">
                  {{ t('common.refresh') }}
                </button>
                <span v-if="healthError" class="crypto-view__warn">{{ healthError }}</span>
              </div>
            </div>
          </template>
        </div>

        <!-- Addresses -->
        <div v-if="activeTab === 'addresses'" class="crypto-view__panel">
          <!-- Whole-set totals: they must not change when paging. -->
          <div class="crypto-view__stats">
            <div class="crypto-view__stat">
              <span class="crypto-view__stat-label">{{ t('admin.crypto.statAddresses') }}</span>
              <span class="crypto-view__stat-value">{{ addressSummary.address_count }}</span>
            </div>
            <div class="crypto-view__stat">
              <span class="crypto-view__stat-label">{{ t('admin.crypto.statDeposits') }}</span>
              <span class="crypto-view__stat-value">{{ addressSummary.deposit_count }}</span>
            </div>
            <div class="crypto-view__stat crypto-view__stat--accent">
              <span class="crypto-view__stat-label">{{ t('admin.crypto.statCredited') }}</span>
              <span class="crypto-view__stat-value">{{ fmtAmount(addressSummary.credited_total) }}</span>
            </div>
            <div class="crypto-view__stat">
              <span class="crypto-view__stat-label">{{ t('admin.crypto.statSwept') }}</span>
              <span class="crypto-view__stat-value">{{ fmtAmount(addressSummary.swept_total) }}</span>
            </div>
            <div class="crypto-view__stat">
              <span class="crypto-view__stat-label">{{ t('admin.crypto.statUnswept') }}</span>
              <span class="crypto-view__stat-value">
                {{ fmtAmount(addressSummary.credited_total - addressSummary.swept_total) }}
              </span>
            </div>
          </div>

          <div class="crypto-view__filters">
            <input
              v-model.number="addressUserId"
              type="number"
              class="input"
              :placeholder="t('admin.crypto.userId')"
              @keyup.enter="applyAddressFilter"
            />
            <select v-model="addressFamily" class="input" @change="applyAddressFilter">
              <option value="">{{ t('admin.crypto.allFamilies') }}</option>
              <option value="evm">EVM</option>
              <option value="solana">Solana</option>
            </select>
            <button class="btn btn-secondary" @click="applyAddressFilter">
              {{ t('common.search') }}
            </button>
            <button class="btn btn-secondary" :disabled="loadingAddresses" @click="loadAddresses">
              {{ t('common.refresh') }}
            </button>
          </div>

          <div v-if="loadingAddresses" class="crypto-view__empty">
            <LoadingSpinner />
          </div>
          <div v-else-if="addresses.length" class="crypto-view__table-wrap">
          <table class="crypto-view__table">
            <thead>
              <tr>
                <th
                  v-for="col in addressColumns"
                  :key="col.key ?? col.label"
                  :class="{
                    'crypto-view__th--sortable': !!col.key,
                    'crypto-view__th--active': col.key && addressSortBy === col.key
                  }"
                  @click="col.key && sortAddresses(col.key)"
                >
                  {{ t(col.label) }}
                  <span v-if="col.key" class="crypto-view__sort">{{ sortIcon(col.key) }}</span>
                </th>
                <th>{{ t('common.status') }}</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="row in addresses" :key="row.id">
                <td>{{ row.id }}</td>
                <td>{{ row.user_id }}</td>
                <td>{{ row.family }}</td>
                <td class="crypto-view__mono" :title="row.address">
                  <span class="crypto-view__copy" @click="copyAddress(row.address)">{{ short(row.address) }}</span>
                </td>
                <td>{{ fmtAmount(row.credited_total) }}</td>
                <td>{{ fmtAmount(row.swept_total) }}</td>
                <td>{{ row.deposit_count }}</td>
                <td>
                  <span class="crypto-view__badge" :class="`crypto-view__badge--${row.status}`">
                    {{ row.status }}
                  </span>
                </td>
                <td>
                  <button class="btn btn-secondary btn-sm" @click="toggleAddress(row)">
                    {{ row.status === 'active' ? t('admin.crypto.disable') : t('admin.crypto.enable') }}
                  </button>
                </td>
              </tr>
            </tbody>
          </table>
          </div>
          <div v-else class="crypto-view__empty-text">{{ t('common.noData') }}</div>

          <div v-if="addressTotalPages > 1" class="crypto-view__pager">
            <button class="btn btn-secondary btn-sm" :disabled="addressPage <= 1" @click="gotoAddressPage(addressPage - 1)">
              ‹
            </button>
            <span class="crypto-view__pager-info">
              {{ addressPage }} / {{ addressTotalPages }} · {{ addressTotal }}
            </span>
            <button
              class="btn btn-secondary btn-sm"
              :disabled="addressPage >= addressTotalPages"
              @click="gotoAddressPage(addressPage + 1)"
            >
              ›
            </button>
          </div>
        </div>

        <!-- Sweep -->
        <div v-if="activeTab === 'sweep'" class="crypto-view__panel">
          <div class="crypto-view__filters">
            <button class="btn btn-secondary" @click="loadSweepPlan">
              {{ t('admin.crypto.loadPlan') }}
            </button>
            <button class="btn btn-secondary" :disabled="sweeping" @click="doSweep(true)">
              {{ t('admin.crypto.sweepDryRun') }}
            </button>
            <button
              class="btn btn-primary"
              :disabled="sweeping || !sweepReady"
              :title="sweepReady ? '' : t('admin.crypto.sweepBlockedHint')"
              @click="requestSweep"
            >
              {{ t('admin.crypto.sweepRun') }}
            </button>
            <button class="btn btn-secondary" @click="loadSweepRecords">
              {{ t('common.refresh') }}
            </button>
          </div>

          <div v-if="sweepSummary" class="crypto-view__plan">
            <p class="crypto-view__plan-total">
              {{ t('admin.crypto.planned') }}: <strong>{{ sweepSummary.planned }}</strong> ·
              {{ t('admin.crypto.swept') }}: <strong>{{ sweepSummary.swept }}</strong> ·
              {{ t('admin.crypto.failed') }}: <strong>{{ sweepSummary.failed }}</strong> ·
              gas: <strong>{{ sweepSummary.gas_topups }}</strong>
              <span v-if="sweepSummary.dry_run" class="crypto-view__badge">dry-run</span>
            </p>
          <div class="crypto-view__table-wrap">
            <table v-if="sweepSummary.entries?.length" class="crypto-view__table">
              <thead>
                <tr>
                  <th>{{ t('admin.crypto.chain') }}</th>
                  <th>{{ t('admin.crypto.address') }}</th>
                  <th>{{ t('common.status') }}</th>
                  <th>TX</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="(e, i) in sweepSummary.entries" :key="i">
                  <td>{{ e.chain }}</td>
                  <td class="crypto-view__mono">{{ short(e.address) }}</td>
                  <td>
                    <span class="crypto-view__badge" :class="`crypto-view__badge--${e.status}`">
                      {{ e.status }}
                    </span>
                    <span v-if="e.error" class="crypto-view__hint">{{ e.error }}</span>
                  </td>
                  <td class="crypto-view__mono">{{ short(e.sweep_tx_hash || e.gas_tx_hash) }}</td>
                </tr>
              </tbody>
            </table>
          </div>
          </div>

          <div v-if="sweepPlan" class="crypto-view__plan">
            <p class="crypto-view__plan-total">
              {{ t('admin.crypto.totalToSweep') }}:
              <strong>{{ sweepPlan.TotalUSD.toFixed(2) }} USDT</strong>
              <span v-if="sweepPlan.DryRun" class="crypto-view__badge">dry-run</span>
            </p>
          <div class="crypto-view__table-wrap">
            <table v-if="sweepPlan.Entries?.length" class="crypto-view__table">
              <thead>
                <tr>
                  <th>{{ t('admin.crypto.chain') }}</th>
                  <th>{{ t('admin.crypto.address') }}</th>
                  <th>{{ t('admin.crypto.amount') }}</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="(e, i) in sweepPlan.Entries" :key="i">
                  <td>{{ e.Chain }}</td>
                  <td class="crypto-view__mono">{{ e.Address }}</td>
                  <td>{{ e.AmountDecimal.toFixed(2) }}</td>
                </tr>
              </tbody>
            </table>
          </div>
          </div>

          <div v-if="sweepRecords.length" class="crypto-view__table-wrap">
          <table class="crypto-view__table">
            <thead>
              <tr>
                <th>{{ t('admin.crypto.chain') }}</th>
                <th>{{ t('admin.crypto.from') }}</th>
                <th>{{ t('admin.crypto.amount') }}</th>
                <th>{{ t('common.status') }}</th>
                <th>TX</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="row in sweepRecords" :key="row.id">
                <td>{{ row.chain }}</td>
                <td class="crypto-view__mono">{{ short(row.from_address) }}</td>
                <td>{{ row.amount }}</td>
                <td>
                  <span class="crypto-view__badge" :class="`crypto-view__badge--${row.status}`">
                    {{ row.status }}
                  </span>
                </td>
                <td class="crypto-view__mono">{{ short(row.sweep_tx_hash) }}</td>
              </tr>
            </tbody>
          </table>
          </div>
          <div v-else-if="!sweepPlan" class="crypto-view__empty-text">{{ t('common.noData') }}</div>
        </div>

        <!-- Deposits: the reconciliation view -->
        <div v-if="activeTab === 'deposits'" class="crypto-view__panel">
          <div class="crypto-view__stats">
            <div class="crypto-view__stat">
              <span class="crypto-view__stat-label">{{ t('admin.crypto.depTotal') }}</span>
              <span class="crypto-view__stat-value">{{ depositSummary.total }}</span>
            </div>
            <div class="crypto-view__stat crypto-view__stat--accent">
              <span class="crypto-view__stat-label">{{ t('admin.crypto.depCredited') }}</span>
              <span class="crypto-view__stat-value">{{ depositSummary.credited }}</span>
            </div>
            <div class="crypto-view__stat">
              <span class="crypto-view__stat-label">{{ t('admin.crypto.depPending') }}</span>
              <span class="crypto-view__stat-value">{{ depositSummary.pending }}</span>
            </div>
            <div class="crypto-view__stat">
              <span class="crypto-view__stat-label">{{ t('admin.crypto.depIgnored') }}</span>
              <span class="crypto-view__stat-value">{{ depositSummary.ignored }}</span>
            </div>
            <div class="crypto-view__stat">
              <span class="crypto-view__stat-label">{{ t('admin.crypto.depAmount') }}</span>
              <span class="crypto-view__stat-value">{{ fmtAmount(depositSummary.amount) }}</span>
            </div>
          </div>

          <div class="crypto-view__filters">
            <select v-model="depositChain" class="input" @change="applyDepositFilter">
              <option value="">{{ t('admin.crypto.allChains') }}</option>
              <option v-for="c in config?.chains ?? []" :key="c" :value="c">{{ c }}</option>
            </select>
            <select v-model="depositStatus" class="input" @change="applyDepositFilter">
              <option value="">{{ t('common.status') }}</option>
              <option value="credited">credited</option>
              <option value="pending">pending</option>
              <option value="ignored">ignored</option>
            </select>
            <button class="btn btn-secondary" :disabled="loadingDeposits" @click="loadDeposits">
              {{ t('common.refresh') }}
            </button>
            <button class="btn btn-secondary" :disabled="exporting" @click="downloadCSV">
              {{ t('admin.crypto.exportCsv') }}
            </button>
          </div>

          <div v-if="loadingDeposits" class="crypto-view__empty"><LoadingSpinner /></div>
          <div v-else-if="deposits.length" class="crypto-view__table-wrap">
          <table class="crypto-view__table">
            <thead>
              <tr>
                <th>{{ t('admin.crypto.chain') }}</th>
                <th>{{ t('admin.crypto.from') }}</th>
                <th>{{ t('admin.crypto.address') }}</th>
                <th>{{ t('admin.crypto.token') }}</th>
                <th>{{ t('admin.crypto.amount') }}</th>
                <th>{{ t('common.status') }}</th>
                <th>TX</th>
                <th>{{ t('admin.crypto.colTime') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="row in deposits" :key="`${row.tx_hash}-${row.log_index}`">
                <td>{{ row.chain }}</td>
                <td class="crypto-view__mono" :title="row.from_address">{{ short(row.from_address) }}</td>
                <td class="crypto-view__mono" :title="row.to_address">{{ short(row.to_address) }}</td>
                <td>
                  <span class="crypto-view__badge">{{ row.token_symbol || 'USDT' }}</span>
                </td>
                <td>{{ row.amount }}</td>
                <td>
                  <span class="crypto-view__badge" :class="`crypto-view__badge--${row.status}`">
                    {{ row.status }}
                  </span>
                </td>
                <td>
                  <a
                    v-if="explorerTxURL(row.chain, row.tx_hash)"
                    class="crypto-view__link crypto-view__mono"
                    :href="explorerTxURL(row.chain, row.tx_hash)"
                    target="_blank"
                    rel="noopener noreferrer"
                  >{{ short(row.tx_hash) }}</a>
                  <span v-else class="crypto-view__mono">{{ short(row.tx_hash) }}</span>
                </td>
                <td class="crypto-view__time">{{ fmtTime(row.created_at) }}</td>
              </tr>
            </tbody>
          </table>
          </div>
          <div v-else class="crypto-view__empty-text">{{ t('common.noData') }}</div>

          <div v-if="depositTotalPages > 1" class="crypto-view__pager">
            <button class="btn btn-secondary btn-sm" :disabled="depositPage <= 1" @click="gotoDepositPage(depositPage - 1)">‹</button>
            <span class="crypto-view__pager-info">{{ depositPage }} / {{ depositTotalPages }}</span>
            <button
              class="btn btn-secondary btn-sm"
              :disabled="depositPage >= depositTotalPages"
              @click="gotoDepositPage(depositPage + 1)"
            >›</button>
          </div>
        </div>
      </div>
    </div>

    <!-- Sweeps move real funds, so a plain click must never start one. -->
    <ConfirmDialog
      :show="sweepConfirmOpen"
      :title="t('admin.crypto.sweepConfirmTitle')"
      :message="sweepConfirmMessage"
      :confirm-text="t('admin.crypto.sweepConfirmAction')"
      :cancel-text="t('common.cancel')"
      danger
      @confirm="confirmSweep"
      @cancel="sweepConfirmOpen = false"
    />
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import Toggle from '@/components/common/Toggle.vue'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'
import {
  getCryptoConfig,
  getHotWalletStatus,
  getPoolStatus,
  getSignerStatus,
  getSweepPlan,
  listAllAddresses,
  listDepositsPaged,
  listSweepRecords,
  runSweep,
  setAddressStatus,
  exportDepositsCSV,
  unlockSigner,
  updateCryptoConfig,
  type AddressSortKey,
  type AdminAddressSummary,
  type CryptoConfig,
  type DepositRecord,
  type DepositSummary,
  type DepositAddress,
  type HotWalletStatus,
  type PoolStatus,
  type SweepPlan,
  type SweepRecord,
  type SweepSummary
} from '@/api/admin/crypto'

const { t } = useI18n()
const appStore = useAppStore()

const tabs = computed(() => [
  { id: 'config', label: t('admin.crypto.tabConfig') },
  { id: 'addresses', label: t('admin.crypto.tabAddresses') },
  { id: 'sweep', label: t('admin.crypto.tabSweep') },
  { id: 'deposits', label: t('admin.crypto.tabDeposits') }
])
// Open on the address list: it is the operator's main view (the config tab
// is a one-time setup step).
const activeTab = ref('addresses')

const config = ref<CryptoConfig | null>(null)
const loadingConfig = ref(false)
const savingConfig = ref(false)
const configSaved = ref(false)

const addressUserId = ref<number | null>(null)
const addresses = ref<DepositAddress[]>([])
const addressFamily = ref('')
const addressPage = ref(1)
const addressPageSize = 50
const addressTotal = ref(0)
const addressSortBy = ref<AddressSortKey>('id')
const addressSortDir = ref<'asc' | 'desc'>('desc')
const loadingAddresses = ref(false)
const copiedAddress = ref('')
const addressSummary = ref<AdminAddressSummary>({
  address_count: 0,
  deposit_count: 0,
  credited_total: 0,
  swept_total: 0
})

// Columns mirror the body cell order exactly; key-less entries are static.
const addressColumns: Array<{ key: AddressSortKey | null; label: string }> = [
  { key: 'id', label: 'admin.crypto.colId' },
  { key: 'user_id', label: 'admin.crypto.userId' },
  { key: null, label: 'admin.crypto.family' },
  { key: null, label: 'admin.crypto.address' },
  { key: 'credited_total', label: 'admin.crypto.credited' },
  { key: 'swept_total', label: 'admin.crypto.swept' },
  { key: 'deposit_count', label: 'admin.crypto.statDeposits' }
]

const addressTotalPages = computed(() =>
  Math.max(1, Math.ceil(addressTotal.value / addressPageSize))
)

const sweepPlan = ref<SweepPlan | null>(null)
const sweepRecords = ref<SweepRecord[]>([])
const sweepSummary = ref<SweepSummary | null>(null)
const sweeping = ref(false)
const sweepConfirmOpen = ref(false)

const deposits = ref<DepositRecord[]>([])
const depositSummary = ref<DepositSummary>({ total: 0, credited: 0, pending: 0, ignored: 0, amount: 0 })
const depositChain = ref('')
const depositStatus = ref('')
const depositPage = ref(1)
const depositPageSize = 50
const loadingDeposits = ref(false)

const depositTotalPages = computed(() =>
  Math.max(1, Math.ceil(depositSummary.value.total / depositPageSize))
)

const poolStatus = ref<PoolStatus | null>(null)
const hotWallets = ref<HotWalletStatus[]>([])
const loadingHealth = ref(false)
const healthError = ref('')
const exporting = ref(false)

const signerUnlocked = ref(false)
const loadingSigner = ref(false)
const unlocking = ref(false)
const signerPassphrase = ref('')
const signerError = ref('')

// A real sweep needs a destination. Without one the backend refuses anyway;
// blocking here stops the operator before they wonder why nothing happened.
const sweepReady = computed(() => {
  const hot = config.value?.hot_wallet_evm?.trim() ?? ''
  if (!hot) return false
  return hot.replace(/^0x/i, '').replace(/0/g, '') !== ''
})

const sweepConfirmMessage = computed(() =>
  t('admin.crypto.sweepConfirmMessage', {
    count: sweepPlan.value?.Entries?.length ?? 0,
    total: (sweepPlan.value?.TotalUSD ?? 0).toFixed(2),
    hot: config.value?.hot_wallet_evm?.trim() || t('admin.crypto.hotWalletNone')
  })
)

function splitList(value: string): string[] {
  return value
    .split(',')
    .map((s) => s.trim())
    .filter(Boolean)
}

function setRPC(chain: string, value: string) {
  if (!config.value) return
  config.value.rpc ??= {}
  config.value.rpc[chain] = value.trim()
}

function short(value: string): string {
  if (!value) return '—'
  return value.length > 16 ? `${value.slice(0, 8)}…${value.slice(-6)}` : value
}

function switchTab(id: string) {
  activeTab.value = id
  // Load on entry: an empty table on first visit reads as "no data".
  if (id === 'addresses') {
    void loadAddresses()
  }
  if (id === 'sweep') {
    void loadSweepRecords()
  }
  if (id === 'deposits') {
    void loadDeposits()
  }
}

async function loadConfig() {
  loadingConfig.value = true
  try {
    const loaded = await getCryptoConfig()
    // Earlier saves may predate some keys (rpc, chains); normalise here so a
    // legacy row cannot blank the whole page on a null index.
    config.value = {
      ...loaded,
      chains: loaded.chains ?? [],
      rpc: loaded.rpc ?? {},
      sweep_min_per_address: loaded.sweep_min_per_address ?? {},
      gas_topup: loaded.gas_topup ?? {},
      confirmations: loaded.confirmations ?? {},
      hot_wallet_evm: loaded.hot_wallet_evm ?? '',
      hot_wallet_solana: loaded.hot_wallet_solana ?? '',
      hot_index: loaded.hot_index ?? 0,
      credit_min_amount: loaded.credit_min_amount ?? 0.001,
      credit_max_amount: loaded.credit_max_amount ?? 10000,
      sweep_auto: loaded.sweep_auto ?? false
    }
  } finally {
    loadingConfig.value = false
  }
}

async function saveConfig() {
  if (!config.value) return
  savingConfig.value = true
  configSaved.value = false
  try {
    await updateCryptoConfig(config.value)
    configSaved.value = true
  } finally {
    savingConfig.value = false
  }
}

// fmtAmount keeps money columns readable and never renders NaN.
function fmtAmount(value: number | null | undefined): string {
  const n = Number(value ?? 0)
  if (!Number.isFinite(n)) return '0.00'
  // At least 2 digits, up to 8: a 0.067 credit must not display as 0.07.
  return n.toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 8 })
}

function sortIcon(key: AddressSortKey): string {
  if (addressSortBy.value !== key) return '↕'
  return addressSortDir.value === 'asc' ? '↑' : '↓'
}

function sortAddresses(key: AddressSortKey) {
  if (addressSortBy.value === key) {
    addressSortDir.value = addressSortDir.value === 'asc' ? 'desc' : 'asc'
  } else {
    addressSortBy.value = key
    // Totals are most useful largest-first; identifiers keep their natural order.
    addressSortDir.value = key === 'id' || key === 'user_id' ? 'desc' : 'desc'
  }
  addressPage.value = 1
  void loadAddresses()
}

function applyAddressFilter() {
  addressPage.value = 1
  void loadAddresses()
}

function gotoAddressPage(page: number) {
  if (page < 1 || page > addressTotalPages.value) return
  addressPage.value = page
  void loadAddresses()
}

async function copyAddress(address: string) {
  try {
    await navigator.clipboard.writeText(address)
    copiedAddress.value = address
    window.setTimeout(() => {
      if (copiedAddress.value === address) copiedAddress.value = ''
    }, 1500)
  } catch {
    // Clipboard can be blocked (non-secure origin); the full value stays in
    // the title attribute, so this is a convenience only.
  }
}

async function loadAddresses() {
  loadingAddresses.value = true
  try {
    const { items, total, summary } = await listAllAddresses({
      userId: addressUserId.value,
      page: addressPage.value,
      pageSize: addressPageSize,
      sortBy: addressSortBy.value,
      sortDir: addressSortDir.value
    })
    addresses.value = items
    addressTotal.value = total
    addressSummary.value = summary
  } finally {
    loadingAddresses.value = false
  }
}

async function toggleAddress(row: DepositAddress) {
  const next = row.status === 'active' ? 'disabled' : 'active'
  await setAddressStatus(row.id, next)
  await loadAddresses()
}

async function loadSweepPlan() {
  sweepPlan.value = await getSweepPlan()
}

async function doSweep(dryRun: boolean) {
  sweeping.value = true
  try {
    sweepSummary.value = await runSweep(dryRun)
    await loadSweepRecords()
  } finally {
    sweeping.value = false
  }
}

// The run button opens a confirmation that restates the destination and the
// amount: two deliberate clicks before any funds move.
function requestSweep() {
  if (!sweepReady.value) return
  sweepConfirmOpen.value = true
}

async function confirmSweep() {
  sweepConfirmOpen.value = false
  await doSweep(false)
}


async function loadSweepRecords() {
  sweepRecords.value = await listSweepRecords()
}

// Block explorer links let the operator verify a deposit on-chain instead of
// trusting our own ledger.
const explorerBases: Record<string, string> = {
  bsc: 'https://bscscan.com/tx/',
  polygon: 'https://polygonscan.com/tx/',
  plasma: 'https://plasmascan.to/tx/',
  arbitrum: 'https://arbiscan.io/tx/',
  base: 'https://basescan.org/tx/',
  xlayer: 'https://web3.okx.com/explorer/xlayer/tx/',
  solana: 'https://solscan.io/tx/'
}

function explorerTxURL(chain: string, txHash: string): string {
  const base = explorerBases[chain]
  if (!base || !txHash) return ''
  return base + txHash
}

function fmtTime(value: string): string {
  if (!value) return '—'
  const d = new Date(value)
  if (Number.isNaN(d.getTime())) return '—'
  return d.toLocaleString()
}

function applyDepositFilter() {
  depositPage.value = 1
  void loadDeposits()
}

function gotoDepositPage(page: number) {
  if (page < 1 || page > depositTotalPages.value) return
  depositPage.value = page
  void loadDeposits()
}

async function loadHealth() {
  loadingHealth.value = true
  healthError.value = '' 
  try {
    const [pool, hot] = await Promise.all([getPoolStatus(), getHotWalletStatus()])
    poolStatus.value = pool
    hotWallets.value = hot
  } catch (err: unknown) {
    // The hot wallet may simply be unset: that is a configuration state,
    // not a page-breaking error.
    healthError.value = extractApiErrorMessage(err, '')
    hotWallets.value = []
  } finally {
    loadingHealth.value = false
  }
}

async function downloadCSV() {
  exporting.value = true
  try {
    const blob = await exportDepositsCSV(depositChain.value, depositStatus.value)
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = 'crypto-deposits.csv'
    a.click()
    URL.revokeObjectURL(url)
  } finally {
    exporting.value = false
  }
}

async function loadSignerStatus() {
  loadingSigner.value = true
  try {
    const st = await getSignerStatus()
    signerUnlocked.value = st.unlocked
  } catch {
    // A locked or unreachable signer is not an error the page must shout
    // about: the lock badge already tells the operator what to do.
    signerUnlocked.value = false
  } finally {
    loadingSigner.value = false
  }
}

async function submitUnlock() {
  if (!signerPassphrase.value) return
  unlocking.value = true
  signerError.value = ""
  try {
    await unlockSigner(signerPassphrase.value)
    signerPassphrase.value = ""
    await loadSignerStatus()
    appStore.showSuccess(t("admin.crypto.signerUnlockDone"))
  } catch (err: unknown) {
    signerError.value = extractApiErrorMessage(err, t("common.error"))
  } finally {
    unlocking.value = false
  }
}

async function loadDeposits() {
  loadingDeposits.value = true
  try {
    const { items, summary } = await listDepositsPaged(
      depositChain.value, depositStatus.value, depositPage.value, depositPageSize
    )
    deposits.value = items
    depositSummary.value = summary
  } finally {
    loadingDeposits.value = false
  }
}

onMounted(() => {
  void loadConfig()
  // The default tab is addresses, so load it without waiting for a click.
  void loadAddresses()
  // The lock badge lives in the config tab but reflects live state.
  void loadSignerStatus()
  void loadHealth()
})
</script>

<style scoped>
.crypto-view {
  padding: 1rem;
}
.crypto-view__card {
  overflow: hidden;
  padding: 0;
}
.crypto-view__header {
  display: flex;
  align-items: center;
  gap: 0.75rem;
  padding: 1rem 1.25rem;
  border-bottom: 1px solid var(--signal-line, rgba(127, 127, 127, 0.18));
}
.crypto-view__icon {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 2.25rem;
  height: 2.25rem;
  border-radius: 0.5rem;
  background: var(--signal-accent-soft, rgba(99, 102, 241, 0.12));
  color: var(--signal-accent, #6366f1);
  font-size: 1.125rem;
}
.crypto-view__title {
  font-size: 1.125rem;
  font-weight: 600;
  letter-spacing: -0.01em;
}
.crypto-view__desc {
  font-size: 0.8125rem;
  color: var(--signal-muted, #6b7280);
}
.crypto-view__tabs {
  display: grid;
  /* Follow the tab count: a hard-coded 3 wrapped the 4th tab onto a new row. */
  grid-auto-flow: column;
  grid-auto-columns: 1fr;
  gap: 0.25rem;
  margin: 0.75rem 1.25rem 0;
  padding: 0.25rem;
  border-radius: 0.75rem;
  background: rgba(127, 127, 127, 0.08);
}
.crypto-view__tab {
  padding: 0.5rem;
  border-radius: 0.5rem;
  font-size: 0.875rem;
  font-weight: 500;
  color: var(--signal-muted, #6b7280);
  transition: all 0.15s;
}
.crypto-view__tab--active {
  background: var(--signal-surface, #fff);
  color: var(--signal-text, #111827);
  box-shadow: 0 1px 2px rgba(0, 0, 0, 0.08);
}
.crypto-view__panel {
  padding: 1.25rem;
}
.crypto-view__group {
  display: grid;
  gap: 0.75rem;
}
.crypto-view__panel--config {
  /* Groups become cards so the eye can separate them without dividers. */
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(22rem, 1fr));
  gap: 0.875rem;
  align-items: start;
}
.crypto-view__panel--config .crypto-view__group {
  padding: 0.875rem 1rem 1rem;
  border: 1px solid var(--signal-line, rgba(127, 127, 127, 0.18));
  border-radius: 0.75rem;
  background: var(--signal-surface, rgba(127, 127, 127, 0.03));
}
/* The save row spans the full width under the cards. */
.crypto-view__panel--config > .crypto-view__actions {
  grid-column: 1 / -1;
  margin-top: 0.25rem;
}
.crypto-view__group-title {
  font-size: 0.875rem;
  font-weight: 600;
}
.crypto-view__group-desc {
  font-size: 0.75rem;
  color: var(--signal-muted, #6b7280);
  margin-top: -0.5rem;
}
.crypto-view__field {
  display: grid;
  gap: 0.375rem;
}
.crypto-view__label {
  font-size: 0.8125rem;
  font-weight: 500;
}
.crypto-view__hint {
  font-size: 0.6875rem;
  color: var(--signal-muted, #6b7280);
}
.crypto-view__switch-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.75rem;
  padding: 0.75rem;
  border: 1px solid var(--signal-line, rgba(127, 127, 127, 0.18));
  border-radius: 0.5rem;
}
.crypto-view__divider {
  height: 1px;
  margin: 1.25rem 0;
  background: var(--signal-line, rgba(127, 127, 127, 0.18));
}
.crypto-view__filters {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.5rem;
  margin-bottom: 0.75rem;
}
/* Buttons keep their label on one line; without this the flex row squeezes
   them until "搜索" stacks vertically. */
.crypto-view__filters .btn {
  flex-shrink: 0;
  white-space: nowrap;
}
/* Inputs take the leftover space instead of stretching edge to edge. */
.crypto-view__filters .input {
  flex: 1 1 10rem;
  min-width: 8rem;
  max-width: 22rem;
}
.crypto-view__stats {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(9rem, 1fr));
  gap: 0.5rem;
  margin-bottom: 1rem;
}
.crypto-view__stat {
  display: flex;
  flex-direction: column;
  gap: 0.25rem;
  padding: 0.75rem 0.875rem;
  border: 1px solid var(--signal-line, rgba(127, 127, 127, 0.18));
  border-radius: 0.625rem;
  background: var(--signal-surface, rgba(127, 127, 127, 0.04));
}
.crypto-view__stat--accent {
  border-color: color-mix(in srgb, var(--signal-accent, #6366f1) 45%, transparent);
  background: var(--signal-accent-soft, rgba(99, 102, 241, 0.08));
}
.crypto-view__stat--warn {
  border-color: color-mix(in srgb, #d97706 55%, transparent);
  background: rgba(217, 119, 6, 0.08);
}
.crypto-view__stat-label {
  font-size: 0.6875rem;
  letter-spacing: 0.02em;
  text-transform: uppercase;
  color: var(--signal-muted, #6b7280);
}
.crypto-view__stat-value {
  font-size: 1.125rem;
  font-weight: 600;
  font-variant-numeric: tabular-nums;
}
.crypto-view__th--sortable {
  cursor: pointer;
  user-select: none;
  white-space: nowrap;
}
.crypto-view__th--sortable:hover {
  color: var(--signal-accent, #6366f1);
}
.crypto-view__th--active {
  color: var(--signal-accent, #6366f1);
}
.crypto-view__sort {
  margin-left: 0.25rem;
  font-size: 0.6875rem;
  opacity: 0.7;
}
.crypto-view__copy {
  cursor: pointer;
  border-bottom: 1px dashed transparent;
}
.crypto-view__copy:hover {
  border-bottom-color: currentColor;
}
.crypto-view__pager {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 0.75rem;
  margin-top: 0.875rem;
}
.crypto-view__pager-info {
  font-size: 0.8125rem;
  font-variant-numeric: tabular-nums;
  color: var(--signal-muted, #6b7280);
}
.crypto-view__warn {
  margin-top: 0.25rem;
  font-size: 0.75rem;
  color: #d97706;
}
.input--warn {
  border-color: #d97706;
}
.crypto-view__link {
  color: var(--signal-accent, #6366f1);
  text-decoration: none;
  border-bottom: 1px dashed currentColor;
}
.crypto-view__link:hover {
  opacity: 0.8;
}
.crypto-view__time {
  white-space: nowrap;
  font-size: 0.75rem;
  color: var(--signal-muted, #6b7280);
}
.crypto-view__lockstate {
  display: flex;
  align-items: center;
  gap: 0.75rem;
  margin-bottom: 0.75rem;
}
.crypto-view__table {
  width: 100%;
  border-collapse: collapse;
}
.crypto-view__panel--config .crypto-view__stats {
  /* Inside a card the row sits flush with the group's own gap. */
  margin-bottom: 0.25rem;
}
/* Tables carry many columns (address, tx hash, amounts); on a narrow screen
   they must scroll rather than stretch the page. */
.crypto-view__table-wrap {
  overflow-x: auto;
  max-width: 100%;
}
.crypto-view__table th,
.crypto-view__table td {
  padding: 0.5rem 0.75rem;
  border-bottom: 1px solid var(--signal-line, rgba(127, 127, 127, 0.12));
  text-align: left;
  font-size: 0.8125rem;
}
.crypto-view__table th {
  font-weight: 500;
  color: var(--signal-muted, #6b7280);
  white-space: nowrap;
}
.crypto-view__table td {
  white-space: nowrap;
}
/* The address column is the one that may need to truncate, not wrap the row. */
.crypto-view__table td.crypto-view__mono {
  max-width: 18rem;
  overflow: hidden;
  text-overflow: ellipsis;
}
.crypto-view__mono {
  font-family: ui-monospace, SFMono-Regular, monospace;
}
.crypto-view__badge {
  display: inline-block;
  padding: 0.125rem 0.5rem;
  border-radius: 0.375rem;
  font-size: 0.75rem;
  background: rgba(127, 127, 127, 0.12);
}
.crypto-view__badge--active,
.crypto-view__badge--swept {
  background: rgba(34, 197, 94, 0.15);
  color: #16a34a;
}
.crypto-view__badge--disabled,
.crypto-view__badge--failed {
  background: rgba(239, 68, 68, 0.15);
  color: #dc2626;
}
.crypto-view__empty {
  display: flex;
  justify-content: center;
  padding: 2rem;
}
.crypto-view__empty-text {
  padding: 2rem;
  text-align: center;
  font-size: 0.8125rem;
  color: var(--signal-muted, #6b7280);
}
.crypto-view__actions {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 0.75rem;
  margin-top: 1.25rem;
}
.crypto-view__saved {
  font-size: 0.8125rem;
  color: #16a34a;
}
.crypto-view__plan {
  margin-bottom: 1rem;
}
.crypto-view__plan-total {
  margin-bottom: 0.5rem;
  font-size: 0.875rem;
}
</style>
