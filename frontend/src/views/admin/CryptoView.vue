<template>
  <AppLayout>
    <div class="crypto-view">
      <header class="crypto-view__header">
        <div>
          <h1 class="crypto-view__title">{{ t('admin.crypto.title') }}</h1>
          <p class="crypto-view__desc">{{ t('admin.crypto.description') }}</p>
        </div>
        <div v-if="loadedSnapshot" class="crypto-view__header-status">
          <span class="crypto-view__badge" :class="loadedSnapshot.enabled ? 'crypto-view__badge--active' : 'crypto-view__badge--disabled'">{{ t(loadedSnapshot.enabled ? 'admin.crypto.channelActive' : 'admin.crypto.channelPaused') }}</span>
          <span class="crypto-view__badge" :class="signerUnlocked && signerSigningEnabled ? 'crypto-view__badge--active' : 'crypto-view__badge--pending'">{{ t(!signerUnlocked ? 'admin.crypto.signerLocked' : signerSigningEnabled ? 'admin.crypto.signerReady' : 'admin.crypto.signerApprovalPending') }}</span>
        </div>
      </header>
      <div class="card crypto-view__card">
        <div class="crypto-view__tabs" role="tablist" :aria-label="t('admin.crypto.title')">
          <button
            v-for="tab in tabs"
            :key="tab.id"
            type="button"
            role="tab"
            class="crypto-view__tab"
            :class="{ 'crypto-view__tab--active': activeTab === tab.id }"
            :id="'crypto-tab-' + tab.id"
            :aria-controls="'crypto-panel-' + tab.id"
            :aria-selected="activeTab === tab.id"
            :tabindex="activeTab === tab.id ? 0 : -1"
            @keydown="onTabKeydown"
            @click="switchTab(tab.id)"
          >
            {{ tab.label }}
          </button>
        </div>

        <!-- Configuration -->
        <div v-if="activeTab === 'config'" id="crypto-panel-config" role="tabpanel" aria-labelledby="crypto-tab-config" class="crypto-view__panel crypto-view__panel--config">
          <div v-if="loadingConfig" class="crypto-view__empty">
            <LoadingSpinner />
          </div>
          <template v-else-if="config">
            <details class="crypto-view__group" open>
              <summary class="crypto-view__group-summary">
                <span class="crypto-view__group-title">{{ t('admin.crypto.groupChannel') }}</span>
                <span class="crypto-view__group-desc">{{ t('admin.crypto.groupChannelDesc') }}</span>
                <span class="crypto-view__group-chevron">›</span>
              </summary>
              <div class="crypto-view__switch-row">
                <div class="space-y-0.5">
                  <label class="crypto-view__label">{{ t('admin.crypto.enabled') }}</label>
                  <p class="crypto-view__hint">{{ t('admin.crypto.enabledHint') }}</p>
                </div>
                <Toggle :model-value="config.enabled" @update:model-value="confirmChannelToggle" />
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
            </details>

            <!-- Signer lock: without the passphrase the process holds no
                 seeds and every signing request is refused. -->
            <details class="crypto-view__group" open>
              <summary class="crypto-view__group-summary">
                <span class="crypto-view__group-title">{{ t('admin.crypto.signerLockTitle') }}</span>
                <span class="crypto-view__group-desc">{{ t('admin.crypto.signerLockDesc') }}</span>
                <span class="crypto-view__group-chevron">›</span>
              </summary>

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

              <p v-if="signerUnlocked && !signerSigningEnabled" class="crypto-view__approval-notice" role="status">
                {{ t('admin.crypto.signerApprovalMissing') }}
              </p>

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
            </details>


            <details class="crypto-view__group crypto-view__group--wide">
              <summary class="crypto-view__group-summary">
                <span class="crypto-view__group-title">{{ t('admin.crypto.groupRpc') }}</span>
                <span class="crypto-view__group-desc">{{ t('admin.crypto.groupRpcDesc') }}</span>
                <span class="crypto-view__group-chevron">›</span>
              </summary>

              <!-- Per-chain endpoint table: add / test / remove, and pull
                   candidates from the public chainlist registry. -->
              <div class="crypto-view__rpc-chain" v-for="chain in config.chains" :key="`rpc-${chain}`">
                <div class="crypto-view__rpc-head">
                  <span class="crypto-view__rpc-chain-name">{{ chain }}</span>
                  <div class="crypto-view__rpc-head-actions">
                    <button
                      class="btn btn-secondary btn-sm"
                      :disabled="fetchingCandidates === chain"
                      @click="autoFillRPC(chain)"
                    >
                      {{ fetchingCandidates === chain ? t('admin.crypto.rpcFetching') : t('admin.crypto.rpcFetch') }}
                    </button>
                    <button class="btn btn-secondary btn-sm" @click="addRPCRow(chain)">
                      {{ t('admin.crypto.rpcAdd') }}
                    </button>
                    <button
                      class="btn btn-secondary btn-sm"
                      :disabled="testingChain === chain"
                      @click="testAllEndpoints(chain)"
                    >
                      {{ testingChain === chain ? t('common.loading') : t('admin.crypto.rpcTestAll') }}
                    </button>
                  </div>
                </div>

                <div class="crypto-view__table-wrap">
                  <table class="crypto-view__table">
                    <thead>
                      <tr>
                        <th>{{ t('admin.crypto.rpcEndpoint') }}</th>
                        <th>{{ t('common.status') }}</th>
                        <th></th>
                      </tr>
                    </thead>
                    <tbody>
                      <tr v-for="(row, idx) in rpcRows[chain] ?? []" :key="`${chain}-${idx}`">
                        <td>
                          <input
                            v-model="row.url"
                            class="input crypto-view__rpc-input"
                            :placeholder="t('admin.crypto.rpcPlaceholder')"
                            @change="persistRPC(chain)"
                          />
                        </td>
                        <td>
                          <span
                            v-if="row.status"
                            class="crypto-view__badge"
                            :class="row.ok ? 'crypto-view__badge--active' : 'crypto-view__badge--failed'"
                            :title="row.error || ''"
                          >
                            {{ row.ok ? `${row.latency_ms}ms · #${row.block}` : t('admin.crypto.rpcFail') }}
                          </span>
                          <span v-else class="crypto-view__hint">—</span>
                        </td>
                        <td>
                          <button class="btn btn-secondary btn-sm" :disabled="row.testing" @click="testEndpoint(chain, idx)">
                            {{ t('admin.crypto.rpcTest') }}
                          </button>
                          <button class="btn btn-secondary btn-sm" @click="removeRPCRow(chain, idx)">
                            {{ t('admin.crypto.rpcRemove') }}
                          </button>
                        </td>
                      </tr>
                      <tr v-if="!(rpcRows[chain] ?? []).length">
                        <td colspan="3" class="crypto-view__empty-text">{{ t('common.noData') }}</td>
                      </tr>
                    </tbody>
                  </table>
                </div>

                <p v-if="rpcFetchNotes[chain]" class="crypto-view__hint crypto-view__fetch-note">{{ rpcFetchNotes[chain] }}</p>
              </div>
            </details>


            <details class="crypto-view__group crypto-view__group--wide" open>
              <summary class="crypto-view__group-summary">
                <span class="crypto-view__group-title">{{ t('admin.crypto.groupSweep') }}</span>
                <span class="crypto-view__group-desc">{{ t('admin.crypto.groupSweepDesc') }}</span>
                <span class="crypto-view__group-chevron">›</span>
              </summary>
              <div class="crypto-view__amount-fields">
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
                <p class="crypto-view__hint">{{ t('admin.crypto.signerPolicyHint') }}</p>
              </div>
              <div class="crypto-view__actions">
                <button
                  class="btn btn-secondary btn-sm"
                  :disabled="loadingHotAddress || !signerUnlocked || !signerSigningEnabled"
                  :title="!signerUnlocked ? t('admin.crypto.hotAddressLocked') : !signerSigningEnabled ? t('admin.crypto.signerApprovalMissing') : ''"
                  @click="fillHotWalletAddresses"
                >
                  {{ loadingHotAddress ? t('common.loading') : t('admin.crypto.hotAddressFill') }}
                </button>
                <span v-if="hotAddressNote" class="crypto-view__hint">{{ hotAddressNote }}</span>
              </div>
              <div class="crypto-view__field">
                <p class="crypto-view__hint">{{ t('admin.crypto.hotIndexHint') }}</p>
              </div>
              <div class="crypto-view__switch-row">
                <div class="space-y-0.5">
                  <label class="crypto-view__label">{{ t('admin.crypto.sweepAuto') }}</label>
                  <p class="crypto-view__hint">{{ t('admin.crypto.sweepAutoHint') }}</p>
                </div>
                <Toggle :model-value="config.sweep_auto" @update:model-value="confirmSweepAutoToggle" />
              </div>
            </details>


            <details class="crypto-view__group crypto-view__group--wide">
              <summary class="crypto-view__group-summary">
                <span class="crypto-view__group-title">{{ t('admin.crypto.groupSigner') }}</span>
                <span class="crypto-view__group-desc">{{ t('admin.crypto.groupSignerDesc') }}</span>
                <span class="crypto-view__group-chevron">›</span>
              </summary>
              <div class="crypto-view__field">
                <label class="crypto-view__label">{{ t('admin.crypto.signerSocket') }}</label>
                <input v-model="config.signer_socket" class="input" />
              </div>
            </details>







            <!-- Health: an empty pool blocks new deposits; a dry hot wallet
                 blocks sweeps. Both are silent failures without this. -->
            <details class="crypto-view__group crypto-view__group--wide" open>
              <summary class="crypto-view__group-summary">
                <span class="crypto-view__group-title">{{ t('admin.crypto.healthTitle') }}</span>
                <span class="crypto-view__group-desc">{{ t('admin.crypto.healthDesc') }}</span>
                <span class="crypto-view__group-chevron">›</span>
              </summary>

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
                  <span class="crypto-view__stat-sub">
                    {{ t('admin.crypto.hotGasWallet') }} · {{ short(hw.address) }}
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
            </details>
            <div class="crypto-view__save-bar">
              <p>{{ t('admin.crypto.saveHint') }}</p>
              <span v-if="configSaved" class="crypto-view__saved" role="status">{{ t('common.saved') }}</span>
              <button class="btn btn-primary" :disabled="savingConfig" @click="requestSaveConfig">{{ savingConfig ? t('common.saving') : t('common.save') }}</button>
            </div>
          </template>
        </div>

        <!-- Addresses -->
        <div v-if="activeTab === 'addresses'" id="crypto-panel-addresses" role="tabpanel" aria-labelledby="crypto-tab-addresses" class="crypto-view__panel">
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
                    {{ statusLabel(row.status) }}
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
        <div v-if="activeTab === 'sweep'" id="crypto-panel-sweep" role="tabpanel" aria-labelledby="crypto-tab-sweep" class="crypto-view__panel">
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
              {{ t('admin.crypto.pending') }}: <strong>{{ sweepSummary.pending ?? 0 }}</strong> ·
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
                      {{ statusLabel(e.status) }}
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
                    {{ statusLabel(row.status) }}
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
        <div v-if="activeTab === 'deposits'" id="crypto-panel-deposits" role="tabpanel" aria-labelledby="crypto-tab-deposits" class="crypto-view__panel">
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
              <option value="credited">{{ t('deposit.statusCredited') }}</option>
              <option value="pending">{{ t('deposit.statusPending') }}</option>
              <option value="ignored">{{ t('deposit.statusIgnored') }}</option>
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
                    {{ statusLabel(row.status) }}
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

    <ConfirmDialog
      :show="channelConfirmOpen"
      :title="t('admin.crypto.channelConfirmTitle')"
      :message="channelConfirmMessage"
      :confirm-text="t('admin.crypto.channelConfirmAction')"
      :cancel-text="t('common.cancel')"
      :danger="!config?.enabled"
      @confirm="commitChannelToggle"
      @cancel="channelConfirmOpen = false"
    />

    <ConfirmDialog
      :show="sweepAutoConfirmOpen"
      :title="t('admin.crypto.sweepAutoConfirmTitle')"
      :message="sweepAutoConfirmMessage"
      :confirm-text="t('admin.crypto.sweepAutoConfirmAction')"
      :cancel-text="t('common.cancel')"
      danger
      @confirm="commitSweepAutoToggle"
      @cancel="sweepAutoConfirmOpen = false"
    />

    <ConfirmDialog
      :show="addressConfirmOpen"
      :title="t('admin.crypto.addressConfirmTitle')"
      :message="addressConfirmMessage"
      :confirm-text="t('admin.crypto.addressConfirmAction')"
      :cancel-text="t('common.cancel')"
      danger
      @confirm="commitToggleAddress"
      @cancel="addressConfirmOpen = false"
    />

    <!-- Saving config can move sweep destinations / automatic sweeps. Name
         the exact fields that changed before writing them. -->
    <ConfirmDialog
      :show="saveConfirmOpen"
      :title="t('admin.crypto.saveConfirmTitle')"
      :message="saveConfirmMessage"
      :confirm-text="t('admin.crypto.saveConfirmAction')"
      :cancel-text="t('common.cancel')"
      danger
      @confirm="saveConfig"
      @cancel="saveConfirmOpen = false"
    >
      <ul class="crypto-view__risk-list">
        <li v-for="change in riskyChanges" :key="change">{{ change }}</li>
      </ul>
    </ConfirmDialog>
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
  getHotWalletAddress,
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
  testRPCEndpoint,
  autoPickRPC,
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
// Snapshot of the last loaded/saved config: the save confirmation diffs
// against it to name exactly which dangerous fields are changing.
const loadedSnapshot = ref<CryptoConfig | null>(null)
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
const channelConfirmOpen = ref(false)
const sweepAutoConfirmOpen = ref(false)
const addressConfirmOpen = ref(false)
const pendingChannelEnabled = ref(false)
const pendingSweepAuto = ref(false)
const pendingAddress = ref<DepositAddress | null>(null)

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
const signerSigningEnabled = ref(false)
const loadingHotAddress = ref(false)
const hotAddressNote = ref('')
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

const channelConfirmMessage = computed(() =>
  t('admin.crypto.channelConfirmMessage', {
    state: pendingChannelEnabled.value ? t('admin.crypto.enabled') : t('admin.crypto.disabled')
  })
)

const sweepAutoConfirmMessage = computed(() =>
  t('admin.crypto.sweepAutoConfirmMessage', {
    state: pendingSweepAuto.value ? t('common.enabled') : t('common.disabled')
  })
)

const addressConfirmMessage = computed(() => {
  const row = pendingAddress.value
  if (!row) return ''
  const action = row.status === 'active' ? t('admin.crypto.disable') : t('admin.crypto.enable')
  return t('admin.crypto.addressConfirmMessage', {
    action,
    address: row.address
  })
})

const saveConfirmMessage = computed(() =>
  t('admin.crypto.saveConfirmMessage', { count: riskyChanges.value.length })
)

function statusLabel(status: string): string {
  const labels: Record<string, string> = {
    active: 'admin.crypto.statusActive', disabled: 'admin.crypto.statusDisabled',
    credited: 'deposit.statusCredited', pending: 'deposit.statusPending',
    ignored: 'deposit.statusIgnored', reverted: 'deposit.statusReverted',
    swept: 'admin.crypto.statusSwept', failed: 'admin.crypto.failed',
    planned: 'admin.crypto.planned', gas_pending: 'admin.crypto.statusGasPending',
    token_pending: 'admin.crypto.statusTokenPending', manual_review: 'admin.crypto.statusManualReview'
  }
  return labels[status] ? t(labels[status]) : status
}

function splitList(value: string): string[] {
  return value
    .split(',')
    .map((s) => s.trim())
    .filter(Boolean)
}

// --- RPC endpoint table -------------------------------------------------
// config.rpc stores a comma-separated string per chain; the table edits it
// as rows. Both directions go through the same helpers so the saved value
// and the visible rows can never disagree.

interface RPCRow {
  url: string
  testing: boolean
  status: boolean | null
  ok: boolean
  latency_ms: number
  block: number
  error: string
}

const rpcRows = ref<Record<string, RPCRow[]>>({})
const fetchingCandidates = ref('')
const testingChain = ref('')
// How many healthy endpoints one click of "fetch from chainlist" adds.
const RPC_AUTO_FILL_COUNT = 3
// Per-chain note under the RPC table (how many probed / added, or an error).
const rpcFetchNotes = ref<Record<string, string>>({})

function rowsFromConfig(chain: string): RPCRow[] {
  const raw = config.value?.rpc?.[chain] ?? ''
  return raw
    .split(',')
    .map((s) => s.trim())
    .filter(Boolean)
    .map((url) => ({ url, testing: false, status: null, ok: false, latency_ms: 0, block: 0, error: '' }))
}

// Rebuild rows when the channels list changes (e.g. after loading config).
function syncRPCRows() {
  const next: Record<string, RPCRow[]> = {}
  for (const chain of config.value?.chains ?? []) {
    next[chain] = rowsFromConfig(chain)
  }
  rpcRows.value = next
}

// persistRPC writes the rows back into the comma-separated config value.
function persistRPC(chain: string) {
  if (!config.value) return
  config.value.rpc ??= {}
  config.value.rpc[chain] = (rpcRows.value[chain] ?? [])
    .map((r) => r.url.trim())
    .filter(Boolean)
    .join(',')
}

function addRPCRow(chain: string) {
  rpcRows.value[chain] ??= []
  rpcRows.value[chain].push({ url: '', testing: false, status: null, ok: false, latency_ms: 0, block: 0, error: '' })
}

function removeRPCRow(chain: string, idx: number) {
  rpcRows.value[chain]?.splice(idx, 1)
  persistRPC(chain)
}

async function testEndpoint(chain: string, idx: number) {
  const row = rpcRows.value[chain]?.[idx]
  if (!row || !row.url.trim()) return
  row.testing = true
  try {
    const result = await testRPCEndpoint(chain, row.url.trim())
    row.status = true
    row.ok = result.ok
    row.latency_ms = result.latency_ms
    row.block = result.block ?? 0
    row.error = result.error ?? ''
  } catch (e) {
    row.status = true
    row.ok = false
    row.error = extractApiErrorMessage(e)
  } finally {
    row.testing = false
  }
}

async function testAllEndpoints(chain: string) {
  testingChain.value = chain
  try {
    const rows = rpcRows.value[chain] ?? []
    for (let i = 0; i < rows.length; i++) {
      if (rows[i].url.trim()) await testEndpoint(chain, i)
    }
  } finally {
    testingChain.value = ''
  }
}

// autoFillRPC pulls the public registry, probes every candidate in parallel
// on the server and appends the fastest healthy endpoints it does not
// already have — three by default. One click, ready-to-scan nodes.
async function autoFillRPC(chain: string) {
  fetchingCandidates.value = chain
  rpcFetchNotes.value[chain] = ''
  try {
    const exclude = (rpcRows.value[chain] ?? []).map((r) => r.url.trim()).filter(Boolean)
    const want = Math.max(1, RPC_AUTO_FILL_COUNT)
    const { picked, probed } = await autoPickRPC(chain, exclude, want)
    if (!picked.length) {
      rpcFetchNotes.value[chain] = t('admin.crypto.rpcFetchEmpty', { probed })
      return
    }
    for (const endpoint of picked) {
      addRPCRow(chain)
      const rows = rpcRows.value[chain] ?? []
      const row = rows[rows.length - 1]
      row.url = endpoint.endpoint
      // Keep the probe numbers: the operator sees latency without re-testing.
      row.status = true
      row.ok = true
      row.latency_ms = endpoint.latency_ms
      row.block = endpoint.block ?? 0
    }
    persistRPC(chain)
    rpcFetchNotes.value[chain] = t('admin.crypto.rpcFetchDone', { count: picked.length, probed })
  } catch (e) {
    rpcFetchNotes.value[chain] = extractApiErrorMessage(e)
  } finally {
    fetchingCandidates.value = ''
  }
}

function short(value: string): string {
  if (!value) return '—'
  return value.length > 16 ? `${value.slice(0, 8)}…${value.slice(-6)}` : value
}

function onTabKeydown(event: KeyboardEvent) {
  if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return
  const currentIndex = tabs.value.findIndex(tab => tab.id === activeTab.value)
  const offset = event.key === 'ArrowLeft' ? -1 : 1
  const index = event.key === 'Home' ? 0 : event.key === 'End' ? tabs.value.length - 1
    : (currentIndex + offset + tabs.value.length) % tabs.value.length
  const nextTab = tabs.value[index]
  if (!nextTab) return
  event.preventDefault()
  switchTab(nextTab.id)
  const list = (event.currentTarget as HTMLElement).parentElement
  list?.querySelector<HTMLButtonElement>('#crypto-tab-' + nextTab.id)?.focus()
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
      scan_range: loaded.scan_range ?? {},
      hot_wallet_evm: loaded.hot_wallet_evm ?? '',
      hot_wallet_solana: loaded.hot_wallet_solana ?? '',
      hot_index: loaded.hot_index ?? 0,
      credit_min_amount: loaded.credit_min_amount ?? 0.001,
      credit_max_amount: loaded.credit_max_amount ?? 10000,
      sweep_auto: loaded.sweep_auto ?? false
    }
    loadedSnapshot.value = JSON.parse(JSON.stringify(config.value))
    syncRPCRows()
  } finally {
    loadingConfig.value = false
  }
}

// Fields whose change moves real money or changes who receives it. Editing
// any of these must pass a confirmation that names the exact field.
const RISKY_FIELDS: Array<{ key: keyof CryptoConfig; label: string }> = [
  { key: 'enabled', label: 'admin.crypto.enabled' },
  { key: 'hot_wallet_evm', label: 'admin.crypto.hotWalletEvm' },
  { key: 'hot_wallet_solana', label: 'admin.crypto.hotWalletSolana' },
  { key: 'hot_index', label: 'admin.crypto.hotIndex' },
  { key: 'sweep_auto', label: 'admin.crypto.sweepAuto' },
  { key: 'sweep_global_threshold', label: 'admin.crypto.globalThreshold' },
  { key: 'credit_min_amount', label: 'admin.crypto.creditMin' },
  { key: 'credit_max_amount', label: 'admin.crypto.creditMax' },
  { key: 'credit_multiplier', label: 'admin.crypto.creditMultiplier' }
]

const saveConfirmOpen = ref(false)

// riskyChanges lists the dangerous edits between the loaded snapshot and the
// current form, as human-readable "label: before → after" lines.
const riskyChanges = computed<string[]>(() => {
  const before = loadedSnapshot.value
  const after = config.value
  if (!before || !after) return []
  const out: string[] = []
  for (const field of RISKY_FIELDS) {
    const a = before[field.key]
    const b = after[field.key]
    if (JSON.stringify(a) === JSON.stringify(b)) continue
    out.push(`${t(field.label)}: ${formatChange(a)} → ${formatChange(b)}`)
  }
  return out
})

function formatChange(value: unknown): string {
  if (value === null || value === undefined || value === '') return '—'
  if (typeof value === 'boolean') return value ? t('common.enabled') : t('common.disabled')
  return String(value)
}

// saveConfig is the raw write; it is only reachable after the confirmation
// when risky fields changed.
async function saveConfig() {
  if (!config.value) return
  saveConfirmOpen.value = false
  savingConfig.value = true
  configSaved.value = false
  try {
    // Send only the fields that actually changed. The backend patch applies
    // pointers onto the stored config, so posting the whole object used to
    // rewrite maps the UI never renders (rpc, gas_topup, confirmations,
    // scan_range, per-chain thresholds) — a null map could blank them.
    await updateCryptoConfig(buildConfigPatch())
    loadedSnapshot.value = JSON.parse(JSON.stringify(config.value))
    configSaved.value = true
  } finally {
    savingConfig.value = false
  }
}

// buildConfigPatch diffs the form against the loaded snapshot. Map fields are
// compared as a whole: if any chain entry changed, that map is sent in full.
function buildConfigPatch(): Partial<CryptoConfig> {
  const before = loadedSnapshot.value
  const after = config.value
  if (!before || !after) return {}
  const patch: Record<string, unknown> = {}
  for (const key of Object.keys(after) as Array<keyof CryptoConfig>) {
    if (JSON.stringify(before[key]) !== JSON.stringify(after[key])) {
      patch[key] = after[key]
    }
  }
  return patch as Partial<CryptoConfig>
}

// requestSaveConfig gates the write behind a confirmation whenever the diff
// touches money-moving fields.
function requestSaveConfig() {
  if (!config.value) return
  if (riskyChanges.value.length === 0) {
    void saveConfig()
    return
  }
  saveConfirmOpen.value = true
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
  pendingAddress.value = row
  addressConfirmOpen.value = true
}

async function commitToggleAddress() {
  const row = pendingAddress.value
  addressConfirmOpen.value = false
  pendingAddress.value = null
  if (!row) return
  const next = row.status === 'active' ? 'disabled' : 'active'
  await setAddressStatus(row.id, next)
  await loadAddresses()
}

function confirmChannelToggle(value: boolean) {
  pendingChannelEnabled.value = value
  channelConfirmOpen.value = true
}

function commitChannelToggle() {
  if (!config.value) return
  config.value.enabled = pendingChannelEnabled.value
  channelConfirmOpen.value = false
}

function confirmSweepAutoToggle(value: boolean) {
  pendingSweepAuto.value = value
  sweepAutoConfirmOpen.value = true
}

function commitSweepAutoToggle() {
  if (!config.value) return
  config.value.sweep_auto = pendingSweepAuto.value
  sweepAutoConfirmOpen.value = false
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
    signerSigningEnabled.value = st.signing_enabled === true
  } catch {
    // A locked or unreachable signer is not an error the page must shout
    // about: the lock badge already tells the operator what to do.
    signerUnlocked.value = false
    signerSigningEnabled.value = false
  } finally {
    loadingSigner.value = false
  }
}

// fillHotWalletAddresses asks the signer what the hot seed derives to and
// writes both addresses into the form. The signer must be unlocked (the
// addresses come from the seed); a locked signer surfaces the reason.
async function fillHotWalletAddresses() {
  loadingHotAddress.value = true
  hotAddressNote.value = ''
  try {
    const addr = await getHotWalletAddress()
    if (!config.value) return
    config.value.hot_wallet_evm = addr.evm
    config.value.hot_wallet_solana = addr.solana
    hotAddressNote.value = t('admin.crypto.hotAddressFilled', { index: addr.index })
  } catch (e) {
    hotAddressNote.value = extractApiErrorMessage(e)
  } finally {
    loadingHotAddress.value = false
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
  width: 100%;
  max-width: 90rem;
  margin: 0 auto;
  padding: clamp(1rem, 2.4vw, 2rem);
}
.crypto-view__card {
  overflow: hidden;
  min-width: 0;
  padding: 0;
  border-radius: 1rem;
}
.crypto-view__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 1rem;
  margin-bottom: 1.5rem;
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
  font-size: 1.5rem;
  font-weight: 650;
  letter-spacing: -0.025em;
}
.crypto-view__desc {
  font-size: 0.8125rem;
  line-height: 1.6;
  margin-top: 0.375rem;
  color: var(--signal-muted, #8c99a6);
}
.crypto-view__tabs {
  display: flex;
  gap: 1.5rem;
  overflow-x: auto;
  padding: 0 1.5rem;
  border-bottom: 1px solid var(--signal-line, #64748b30);
}
.crypto-view__tab {
  position: relative;
  flex-shrink: 0;
  min-height: 3.5rem;
  padding: 1rem 0.25rem;
  border-bottom: 2px solid transparent;
  font-size: 0.875rem;
  font-weight: 500;
  color: var(--signal-muted, #8c99a6);
  transition: color 0.15s, border-color 0.15s;
}
.crypto-view__tab--active {
  border-color: var(--signal-accent, #0891b2);
  color: var(--signal-text, inherit);
  font-weight: 650;
}
.crypto-view__panel {
  min-width: 0;
  padding: 1.5rem;
}
.crypto-view__group {
  display: block;
  min-width: 0;
}
.crypto-view__panel--config {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 1rem;
  align-items: start;
}
.crypto-view__panel--config .crypto-view__group {
  padding: 0;
  border: 1px solid var(--signal-line, rgba(127, 127, 127, 0.18));
  border-radius: 0.75rem;
  background: var(--signal-surface, rgba(127, 127, 127, 0.03));
}
/* Collapsible group header: title + one-line description + chevron. */
.crypto-view__group-summary {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  gap: 0.375rem 0.75rem;
  padding: 1rem 1.25rem;
  cursor: pointer;
  user-select: none;
  list-style: none;
}
.crypto-view__group-summary::-webkit-details-marker {
  display: none;
}
.crypto-view__group-summary:hover {
  background: rgba(127, 127, 127, 0.05);
}
.crypto-view__group-chevron {
  grid-column: 2;
  grid-row: 1 / 3;
  align-self: center;
  font-size: 1.25rem;
  color: var(--signal-muted, #8c99a6);
  transition: transform 0.15s ease;
}
details[open] > .crypto-view__group-summary .crypto-view__group-chevron {
  transform: rotate(90deg);
}
/* Body content keeps its own padding; the summary owns the header area. */
.crypto-view__group > :not(summary) {
  margin-left: 1rem;
  margin-right: 1rem;
}
.crypto-view__group > :last-child {
  margin-bottom: 1rem;
}
/* The save row spans the full width under the cards. */
.crypto-view__panel--config > .crypto-view__actions {
  grid-column: 1 / -1;
  margin-top: 0.25rem;
}
/* The RPC card holds wide tables; it spans the full grid width so endpoint
   rows, status badges and action buttons never get clipped. */
.crypto-view__panel--config > .crypto-view__group--wide {
  grid-column: 1 / -1;
}

.crypto-view__group-title {
  font-size: 0.875rem;
  font-weight: 600;
}
.crypto-view__group-desc {
  grid-column: 1;
  font-size: 0.75rem;
  line-height: 1.6;
  color: var(--signal-muted, #8c99a6);
}
.crypto-view__field {
  display: grid;
  min-width: 0;
  gap: 0.5rem;
  margin-top: 1rem;
}
.crypto-view__label {
  font-size: 0.8125rem;
  font-weight: 500;
}
.crypto-view__hint {
  font-size: 0.75rem;
  line-height: 1.7;
  color: var(--signal-muted, #8c99a6);
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
  font-size: 0.75rem;
  color: var(--signal-muted, #8c99a6);
}
.crypto-view__stat-sub {
  display: block;
  font-size: 0.6875rem;
  font-family: ui-monospace, SFMono-Regular, monospace;
  color: var(--signal-muted, #6b7280);
  max-width: 12rem;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.crypto-view__stat-value {
  font-size: 1.375rem;
  font-weight: 650;
  font-variant-numeric: tabular-nums;
  letter-spacing: -0.025em;
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
/* Diff list inside the save confirmation: each line names one risky field. */
.crypto-view__risk-list {
  margin: 0.5rem 0 0;
  padding-left: 1.1rem;
  display: grid;
  gap: 0.25rem;
  font-size: 0.75rem;
  font-family: ui-monospace, SFMono-Regular, monospace;
  color: #d97706;
  list-style: disc;
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
/* --- RPC endpoint table --- */
.crypto-view__rpc-chain {
  margin-bottom: 1rem;
  padding: 0.75rem;
  border: 1px solid var(--signal-line, rgba(127, 127, 127, 0.18));
  border-radius: 0.5rem;
}
.crypto-view__rpc-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.75rem;
  margin-bottom: 0.5rem;
  flex-wrap: wrap;
}
.crypto-view__rpc-chain-name {
  font-size: 0.875rem;
  font-weight: 600;
  text-transform: none;
}
.crypto-view__rpc-head-actions {
  display: flex;
  gap: 0.5rem;
  flex-wrap: wrap;
}
.crypto-view__rpc-input {
  width: 100%;
  min-width: 16rem;
  font-family: ui-monospace, SFMono-Regular, monospace;
  font-size: 0.75rem;
}
.crypto-view__candidates {
  margin-top: 0.625rem;
}
.crypto-view__candidate-tags {
  display: flex;
  flex-wrap: wrap;
  gap: 0.375rem;
  margin-top: 0.375rem;
  max-height: 10rem;
  overflow-y: auto;
}
.crypto-view__candidate {
  padding: 0.25rem 0.5rem;
  border: 1px solid var(--signal-line, rgba(127, 127, 127, 0.25));
  border-radius: 0.375rem;
  font-size: 0.6875rem;
  font-family: ui-monospace, SFMono-Regular, monospace;
  cursor: pointer;
  background: transparent;
  color: inherit;
}
.crypto-view__candidate:hover:not(:disabled) {
  border-color: #6366f1;
  background: rgba(99, 102, 241, 0.08);
}
.crypto-view__candidate--used {
  opacity: 0.45;
  cursor: default;
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
  border: 1px solid var(--signal-line, #64748b25);
  border-radius: 0.75rem;
}
.crypto-view__table th,
.crypto-view__table td {
  padding: 0.875rem 1rem;
  border-bottom: 1px solid var(--signal-line, #64748b25);
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
.crypto-view__header-status { display: flex; flex-wrap: wrap; gap: .5rem; }
.crypto-view__header-status .crypto-view__badge { padding: .375rem .625rem; border-radius: 99px; }
.crypto-view__group[open] > .crypto-view__group-summary { margin-bottom: 1rem; border-bottom: 1px solid var(--signal-line, #64748b25); }
.crypto-view__amount-fields { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 1.25rem; }
.crypto-view__amount-fields .crypto-view__field { margin-top: 0; }
.crypto-view__save-bar { grid-column: 1 / -1; display: flex; align-items: center; gap: .75rem; padding: 1rem 0 0; border-top: 1px solid var(--signal-line, #64748b30); }
.crypto-view__save-bar p { margin-right: auto; font-size: .75rem; line-height: 1.6; color: var(--signal-muted, #8c99a6); }
.crypto-view__save-bar button { flex-shrink: 0; min-width: 6rem; min-height: 2.75rem; }
.crypto-view__approval-notice { padding: .75rem 1rem; margin-top: .75rem; border: 1px solid #d9770635; border-radius: .5rem; background: #d977060a; font-size: .75rem; line-height: 1.7; }
.crypto-view__badge--pending, .crypto-view__badge--gas_pending, .crypto-view__badge--token_pending, .crypto-view__badge--manual_review { background: #d9770615; color: #d97706; }
.crypto-view__badge--credited { background: #10b98115; color: #10b981; }
.crypto-view__badge--ignored, .crypto-view__badge--reverted { background: #ef444415; color: #ef4444; }
.crypto-view__table th { background: #64748b08; }
.crypto-view__table tbody tr:last-child td { border-bottom: 0; }
.crypto-view button:focus-visible, .crypto-view summary:focus-visible { outline: 2px solid var(--signal-accent, #0891b2); outline-offset: 3px; }
@media (max-width: 900px) {
  .crypto-view__panel--config { grid-template-columns: minmax(0, 1fr); }
}
@media (max-width: 640px) {
  .crypto-view { padding: 1rem .75rem; }
  .crypto-view__title { font-size: 1.25rem; }
  .crypto-view__panel { padding: 1rem; }
  .crypto-view__tabs { gap: 1.25rem; padding: 0 1rem; }
  .crypto-view__tab { min-height: 3rem; font-size: .8125rem; }
  .crypto-view__amount-fields { grid-template-columns: minmax(0, 1fr); gap: 1rem; }
  .crypto-view__group-summary { padding: 1rem; }
  .crypto-view__rpc-head { align-items: flex-start; flex-direction: column; }
  .crypto-view__rpc-head-actions { flex-wrap: wrap; }
  .crypto-view__filters .input { max-width: none; min-width: 0; }
  .crypto-view__save-bar { flex-wrap: wrap; }
  .crypto-view__save-bar p { flex-basis: 100%; }
}
@media (prefers-reduced-motion: reduce) { .crypto-view__tab, .crypto-view__group-chevron { transition: none; } }
</style>
