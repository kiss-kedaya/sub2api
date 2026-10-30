<template>
  <AppLayout>
    <div class="crypto-view">
      <header class="crypto-view__header">
        <div>
          <h1 class="crypto-view__title">{{ t('admin.crypto.title') }}</h1>
          <p class="crypto-view__desc">{{ t('admin.crypto.description') }}</p>
        </div>
        <div v-if="loadedSnapshot" class="crypto-view__header-status">
          <span class="crypto-view__badge" :class="loadedSnapshot.enabled && !walletSecurityLocked ? 'crypto-view__badge--active' : 'crypto-view__badge--disabled'">{{ t(loadedSnapshot.enabled && !walletSecurityLocked ? 'admin.crypto.channelActive' : 'admin.crypto.channelPaused') }}</span>
          <span class="crypto-view__badge" :class="walletReady ? 'crypto-view__badge--active' : 'crypto-view__badge--pending'">{{ t(walletStateLabel) }}</span>
          <CryptoGrantStatus />
        </div>
      </header>
      <CryptoWalletSecurityPanel class="crypto-view__security" @change="updateWalletSecurityState" @recovered="onWalletSecurityRecovered" />
      <details class="crypto-view__notifications">
        <summary>{{ t('admin.crypto.walletNotificationSettings') }}<span>{{ t('admin.crypto.walletNotificationOptional') }}</span></summary>
        <ProfileBarkCard class="crypto-view__bark" :title="t('admin.crypto.walletBarkTitle')" :description="t('admin.crypto.walletBarkDescription')" />
      </details>
      <p v-if="operationIntentError" role="alert" class="crypto-view__approval-notice crypto-view__intent-error">{{ operationIntentError }}</p>
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
          <div v-else-if="!config" class="crypto-view__empty" role="status">
            <div class="space-y-3 text-center">
              <p>{{ configError || t('admin.crypto.configLoadFailed') }}</p>
              <button type="button" class="btn btn-primary" @click="loadConfig">{{ t('common.refresh') }}</button>
            </div>
          </div>
          <template v-else>
            <CryptoGasWalletPanel class="crypto-view__gas-funding" :available="walletCanDerive" :chains="loadedSnapshot?.chains ?? []" :revision="gasWalletRevision" />
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
                <fieldset>
                  <legend class="crypto-view__label mb-2">{{ t('admin.crypto.chains') }}</legend>
                  <div class="grid grid-cols-1 gap-2 sm:grid-cols-2 xl:grid-cols-3">
                    <label v-for="network in depositNetworks" :key="network.id" class="flex cursor-pointer items-start gap-3 rounded-lg border border-gray-200 p-3 dark:border-dark-600">
                      <input v-model="config.chains" type="checkbox" :value="network.id" :data-testid="`crypto-chain-${network.id}`" class="mt-1 h-4 w-4 rounded border-gray-300 text-primary-600 focus:ring-primary-500" @change="syncRPCRows" />
                      <span class="min-w-0">
                        <span class="block text-sm font-medium">{{ network.label }}</span>
                        <span class="block text-xs text-gray-500 dark:text-gray-400">{{ network.tokens }}</span>
                      </span>
                    </label>
                  </div>
                </fieldset>
                <p class="crypto-view__hint">{{ t('admin.crypto.chainsHint') }}</p>
              </div>
            </details>

            <!-- Seeds are managed and encrypted by the main service, with fresh 2FA for changes. -->
            <details class="crypto-view__group" :open="!walletConfigured">
              <summary class="crypto-view__group-summary">
                <span class="crypto-view__group-title">{{ t('admin.crypto.walletLockTitle') }}</span>
                <span class="crypto-view__group-desc">{{ t('admin.crypto.walletLockDesc') }}</span>
                <span class="crypto-view__group-chevron">›</span>
              </summary>

              <div class="crypto-view__lockstate">
                <span
                  class="crypto-view__badge"
                  :class="walletReady ? 'crypto-view__badge--active' : walletCanDerive ? 'crypto-view__badge--pending' : 'crypto-view__badge--failed'"
                >
                  {{ t(walletStateLabel) }}
                </span>
                <button class="btn btn-secondary btn-sm" :disabled="loadingWallet" @click="loadWalletStatus">
                  {{ t('common.refresh') }}
                </button>
              </div>

              <p v-if="walletCanDerive && !walletReady" class="crypto-view__approval-notice" role="status">
                {{ t('admin.crypto.walletNotReady') }}
              </p>

              <p class="crypto-view__hint">{{ t('admin.crypto.walletManagedHint') }}</p>
              <div v-if="!walletConfigured" class="crypto-view__field">
                <p class="crypto-view__approval-notice">{{ t('admin.crypto.walletInitWarning') }}</p>
                <label for="crypto-deposit-mnemonic" class="crypto-view__label">{{ t('admin.crypto.depositMnemonic') }}</label>
                <input id="crypto-deposit-mnemonic" v-model="depositMnemonic" type="password" class="input" autocomplete="off" spellcheck="false" />
                <p class="crypto-view__hint">{{ t('admin.crypto.depositMnemonicImpact') }}</p>
                <label for="crypto-initial-hot-mnemonic" class="crypto-view__label">{{ t('admin.crypto.hotMnemonic') }}</label>
                <input id="crypto-initial-hot-mnemonic" v-model="initialHotMnemonic" type="password" class="input" autocomplete="off" spellcheck="false" />
                <p class="crypto-view__hint">{{ t('admin.crypto.hotMnemonicScope') }}</p>
                <div class="crypto-view__actions">
                  <button class="btn btn-primary" :disabled="initializing || !depositMnemonic.trim() || !initialHotMnemonic.trim()" @click="submitInitialize">
                    {{ initializing ? t('common.saving') : t('admin.crypto.walletInitializeAction') }}
                  </button>
                </div>
              </div>
              <div class="crypto-view__wallet-actions">
                <p class="crypto-view__hint">{{ t('admin.crypto.walletToolsHint') }}</p>
                <button type="button" class="btn btn-secondary" :disabled="!walletCanDerive" @click="mnemonicsDialogOpen = true">{{ t('admin.crypto.mnemonicsExportAction') }}</button>
                <button type="button" class="btn btn-secondary" :disabled="!walletCanDerive" @click="openWalletDialog()">{{ t('admin.crypto.hotImportAction') }}</button>
              </div>
            </details>


            <details id="crypto-rpc-settings" class="crypto-view__group crypto-view__group--wide">
              <summary class="crypto-view__group-summary">
                <span class="crypto-view__group-title">{{ t('admin.crypto.groupRpc') }}</span>
                <span class="crypto-view__group-desc">{{ t('admin.crypto.groupRpcDesc') }}</span>
                <span class="crypto-view__group-chevron">›</span>
              </summary>

              <div v-if="!rpcUnlocked" class="crypto-view__rpc-overview">
                <p class="crypto-view__hint">{{ t('admin.crypto.rpcHiddenHint') }}</p>
                <div class="crypto-view__rpc-statuses">
                  <span v-for="chain in config.chains" :key="`rpc-status-${chain}`" class="crypto-view__badge" :data-testid="`rpc-configured-${chain}`">
                    {{ chain }} · {{ t(configuredRPCChains.includes(chain) ? 'admin.crypto.rpcConfigured' : 'admin.crypto.rpcNotConfigured') }}
                  </span>
                </div>
                <button type="button" class="btn btn-secondary btn-sm" :disabled="loadingPrivateRPC || savingConfig" @click="manageRPC">
                  {{ loadingPrivateRPC ? t('common.loading') : t('admin.crypto.rpcManage') }}
                </button>
              </div>
              <p v-if="rpcError" role="alert" class="crypto-view__warn">{{ rpcError }}</p>

              <!-- Per-chain endpoint table: add / test / remove, and pull
                   candidates from the public chainlist registry. -->
              <template v-if="rpcUnlocked">
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
              </template>
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
                  data-testid="crypto-sweep-threshold"
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
                  data-testid="crypto-credit-min"
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
                  data-testid="crypto-credit-max"
                  v-model.number="config.credit_max_amount"
                  type="number"
                  step="1"
                  min="0"
                  class="input"
                />
                <p class="crypto-view__hint">{{ t('admin.crypto.creditMaxHint') }}</p>
              </div>
              </div>
              <div v-for="chain in config.chains" :key="`scan-limits-${chain}`" class="crypto-view__network-limits">
                <label>
                  <span class="crypto-view__label">{{ chain }} · {{ t('admin.crypto.sweepMinPerAddress') }}</span>
                  <input v-model.number="config.sweep_min_per_address[chain]" :data-testid="`crypto-sweep-min-${chain}`" type="number" min="0" step="0.001" class="input" />
                </label>
                <label>
                  <span class="crypto-view__label">{{ chain }} · {{ t('admin.crypto.scanRange') }}</span>
                  <input v-model.number="config.scan_range[chain]" :data-testid="`crypto-scan-range-${chain}`" type="number" min="1" step="1" class="input" />
                </label>
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
                <p class="crypto-view__hint">{{ t('admin.crypto.sweepDestinationHint') }}</p>
              </div>
              <div class="crypto-view__actions">
                <button
                  class="btn btn-secondary btn-sm"
                  :disabled="loadingHotAddress || !walletCanDerive"
                  :title="!walletOk ? t('admin.crypto.walletUnavailable') : !walletLoaded ? t('admin.crypto.hotAddressLocked') : ''"
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








            <!-- Health: an empty pool blocks new deposits; a dry hot wallet
                 blocks sweeps. Both are silent failures without this. -->
            <details class="crypto-view__group crypto-view__group--wide">
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
                    {{ hw.error ? t(hw.error_code === 'RPC_DISCOVERY_PENDING' ? 'admin.crypto.rpcDiscovering' : 'admin.crypto.hotWalletUnreachable') : hw.balance }}
                  </span>
                  <span v-if="hw.rpc_source === 'chainlist'" class="crypto-view__stat-sub">{{ t('admin.crypto.rpcAutoSource') }}</span>
                </div>
              </div>
              <div class="crypto-view__actions">
                <button class="btn btn-secondary btn-sm" :disabled="loadingHealth || !walletCanDerive" @click="loadHealth">
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
                  <button
                    v-if="row.family === 'evm' || row.family === 'solana'"
                    type="button"
                    class="btn btn-secondary btn-sm"
                    :data-export-address="row.id"
                    @click="privateKeyAddress = { ...row }"
                  >
                    {{ t('admin.crypto.privateKeyExportAction') }}
                  </button>
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
          <CryptoGasWalletPanel class="crypto-view__gas-funding" :available="walletCanDerive" :chains="loadedSnapshot?.chains ?? []" :revision="gasWalletRevision" />
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
              :title="sweepReady ? '' : !walletOk ? t('admin.crypto.walletUnavailable') : !walletLoaded ? t('admin.crypto.hotAddressLocked') : t('admin.crypto.walletNotReady')"
              @click="requestSweep"
            >
              {{ t('admin.crypto.sweepRun') }}
            </button>
            <button class="btn btn-secondary" @click="loadSweepRecords">
              {{ t('common.refresh') }}
            </button>
          </div>

          <div v-if="blockedSweeps.length" class="crypto-view__blocked" role="alert">
            <div class="crypto-view__blocked-head">
              <strong>{{ t('admin.crypto.blockedTitle') }}</strong>
              <span class="crypto-view__hint">{{ t('admin.crypto.blockedHint') }}</span>
            </div>
            <table class="crypto-view__table">
              <thead>
                <tr>
                  <th>{{ t('admin.crypto.chain') }}</th>
                  <th>{{ t('admin.crypto.address') }}</th>
                  <th>{{ t('common.status') }}</th>
                  <th>{{ t('admin.crypto.blockedAge') }}</th>
                  <th></th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="b in blockedSweeps" :key="b.job_id">
                  <td>{{ b.chain }}</td>
                  <td class="crypto-view__mono">#{{ b.job_id }} · {{ short(b.address) }}</td>
                  <td>
                    <span class="crypto-view__badge" :class="`crypto-view__badge--${b.status}`">{{ statusLabel(b.status) }}</span>
                    <span v-if="b.error" class="crypto-view__hint">{{ b.error }}</span>
                  </td>
                  <td>{{ b.age_minutes }}m</td>
                  <td>
                    <button class="btn btn-secondary" :disabled="reconciling === b.job_id" @click="reconcileJob(b.job_id)">
                      {{ reconciling === b.job_id ? t('common.loading') : t('admin.crypto.reconcile') }}
                    </button>
                  </td>
                </tr>
              </tbody>
            </table>
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

    <CryptoMnemonicsDialog :show="mnemonicsDialogOpen" @close="mnemonicsDialogOpen = false" />
    <CryptoPrivateKeyDialog :address="privateKeyAddress" @close="privateKeyAddress = null" />
    <BaseDialog :show="walletDialog !== null" :title="t('admin.crypto.hotImportAction')" @close="closeWalletDialog">
      <form id="crypto-wallet-form" class="crypto-view__wallet-form" autocomplete="off" @submit.prevent="submitWallet">
        <p class="crypto-view__approval-notice">{{ t('admin.crypto.hotImportWarning') }}</p>
          <label for="crypto-hot-mnemonic">{{ t('admin.crypto.hotMnemonic') }}</label>
          <input id="crypto-hot-mnemonic" v-model="hotMnemonic" type="password" class="input" autocomplete="off" spellcheck="false" required />
      </form>
      <template #footer>
        <div class="crypto-view__actions">
          <button type="button" class="btn btn-secondary" @click="closeWalletDialog">{{ t('common.cancel') }}</button>
          <button type="submit" form="crypto-wallet-form" class="btn btn-danger" :disabled="!hotMnemonic.trim()">{{ t('admin.crypto.walletContinue') }}</button>
        </div>
      </template>
    </BaseDialog>
    <CryptoSecurityDialog ref="securityDialog" />
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import CryptoSecurityDialog from '@/components/crypto/CryptoSecurityDialog.vue'
import CryptoGrantStatus from '@/components/crypto/CryptoGrantStatus.vue'
import CryptoMnemonicsDialog from '@/components/crypto/CryptoMnemonicsDialog.vue'
import CryptoPrivateKeyDialog from '@/components/crypto/CryptoPrivateKeyDialog.vue'
import CryptoWalletSecurityPanel from '@/components/crypto/CryptoWalletSecurityPanel.vue'
import CryptoGasWalletPanel from '@/components/crypto/CryptoGasWalletPanel.vue'
import ProfileBarkCard from '@/components/user/profile/ProfileBarkCard.vue'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import Toggle from '@/components/common/Toggle.vue'
import { useAppStore } from '@/stores/app'
import { useClipboard } from '@/composables/useClipboard'
import { extractApiErrorMessage } from '@/utils/apiError'
import { explorerTxURL } from '@/utils/cryptoExplorer'
import {
  getCryptoConfig,
  getCryptoConfigPrivate,
  getHotWalletAddress,
  getHotWalletStatus,
  getPoolStatus,
  getWalletStatus,
  getSweepPlan,
  listAllAddresses,
  listDepositsPaged,
  listSweepRecords,
  listBlockedSweeps,
  reconcileSweep,
  runSweep,
  setAddressStatus,
  exportDepositsCSV,
  initializeWallet,
  updateCryptoConfig,
  testRPCEndpoint,
  autoPickRPC,
  importHotMnemonic,
  reportWalletOperationIntent,
  type AddressSortKey,
  type AdminAddressSummary,
  type CryptoConfig,
  type CryptoConfigPatch,
  type DepositRecord,
  type DepositSummary,
  type DepositAddress,
  type HotWalletStatus,
  type PoolStatus,
  type SweepPlan,
  type BlockedSweep,
  type SweepRecord,
  type SweepSummary,
  type WalletSecurityStatus,
  type WalletOperationAction,
  type CryptoVerification
} from '@/api/admin/crypto'

const { t } = useI18n()
const appStore = useAppStore()
const { copyToClipboard } = useClipboard()
const securityDialog = ref<InstanceType<typeof CryptoSecurityDialog> | null>(null)
const privateKeyAddress = ref<DepositAddress | null>(null)
const operationIntentError = ref('')

async function notifyOperationIntent(action: WalletOperationAction): Promise<boolean> {
  operationIntentError.value = ''
  try {
    await reportWalletOperationIntent(action)
    return true
  } catch {
    operationIntentError.value = t('admin.crypto.walletOperationNotifyFailed')
    appStore.showError(operationIntentError.value)
    return false
  }
}

async function withCryptoVerification(label: string, callback: (verification: CryptoVerification) => Promise<unknown>): Promise<boolean> {
  const operationTab = activeTab.value
  return await securityDialog.value?.verify(label, async (verification) => {
    if (activeTab.value !== operationTab) return
    await callback(verification)
  }) ?? false
}

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
const configError = ref('')
const rpcUnlocked = ref(false)
const configuredRPCChains = ref<string[]>([])
const loadingPrivateRPC = ref(false)
const rpcError = ref('')

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
const blockedSweeps = ref<BlockedSweep[]>([])
const reconciling = ref<number | null>(null)
const sweepSummary = ref<SweepSummary | null>(null)
const sweeping = ref(false)
const channelConfirmOpen = ref(false)
const sweepAutoConfirmOpen = ref(false)
const pendingChannelEnabled = ref(false)
const pendingSweepAuto = ref(false)

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

const walletConfigured = ref(false)
const walletLoaded = ref(false)
const walletChannelEnabled = ref(false)
const walletOk = ref(false)
const walletSecurityLocked = ref(false)
const walletCanDerive = computed(() => walletOk.value && walletLoaded.value && !walletSecurityLocked.value)
const walletReady = computed(() => walletCanDerive.value && walletChannelEnabled.value)
const walletStateLabel = computed(() => walletSecurityLocked.value ? 'admin.crypto.walletSecurityLocked'
  : !walletOk.value ? 'admin.crypto.walletUnavailable'
  : !walletConfigured.value ? 'admin.crypto.walletNotConfigured'
    : !walletLoaded.value ? 'admin.crypto.walletUnavailable'
    : walletReady.value ? 'admin.crypto.walletReady' : 'admin.crypto.walletNeedsConfig')
const loadingHotAddress = ref(false)
const gasWalletRevision = ref(0)
const hotAddressNote = ref('')
const loadingWallet = ref(false)
const initializing = ref(false)
const depositMnemonic = ref('')
const initialHotMnemonic = ref('')
const walletDialog = ref<'import' | null>(null)
const mnemonicsDialogOpen = ref(false)
const hotMnemonic = ref('')

// A real sweep needs a destination. Without one the backend refuses anyway;
// blocking here stops the operator before they wonder why nothing happened.
const sweepReady = computed(() => {
  if (!walletReady.value) return false
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

function statusLabel(status: string): string {
  const labels: Record<string, string> = {
    active: 'admin.crypto.statusActive', disabled: 'admin.crypto.statusDisabled',
    credited: 'deposit.statusCredited', pending: 'deposit.statusPending',
    ignored: 'deposit.statusIgnored', reverted: 'deposit.statusReverted',
    swept: 'admin.crypto.statusSwept', failed: 'admin.crypto.failed',
    planned: 'admin.crypto.planned', gas_pending: 'admin.crypto.statusGasPending',
    gas_confirmed: 'admin.crypto.statusGasConfirmed', sweep_pending: 'admin.crypto.statusTokenPending',
    token_pending: 'admin.crypto.statusTokenPending', manual_review: 'admin.crypto.statusManualReview'
  }
  return labels[status] ? t(labels[status]) : status
}

const depositNetworks = [
  { id: 'bsc', label: 'BSC (BEP20)', tokens: 'USDT / USDC' },
  { id: 'ethereum', label: 'Ethereum (ERC20)', tokens: 'USDT / USDC' },
  { id: 'polygon', label: 'Polygon (PoS)', tokens: 'USDT / USDC' },
  { id: 'plasma', label: 'Plasma', tokens: 'USDT / USDC' },
  { id: 'arbitrum', label: 'Arbitrum One', tokens: 'USDT / USDC' },
  { id: 'base', label: 'Base', tokens: 'USDC' },
  { id: 'xlayer', label: 'X Layer', tokens: 'USDT / USDC / USDC.e' },
]

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
  if (!row || !row.url.trim()) return false
  const endpoint = row.url.trim()
  row.testing = true
  try {
    return await withCryptoVerification(t('admin.crypto.rpcVerify', { chain, endpoint }), async (verification) => {
      const result = await testRPCEndpoint(chain, endpoint, verification)
      row.status = true
      row.ok = result.ok
      row.latency_ms = result.latency_ms
      row.block = result.block ?? 0
      row.error = result.error ?? ''
    })
  } finally {
    row.testing = false
  }
}

async function testAllEndpoints(chain: string) {
  testingChain.value = chain
  try {
    const rows = rpcRows.value[chain] ?? []
    for (let i = 0; i < rows.length; i++) {
      if (rows[i].url.trim() && !await testEndpoint(chain, i)) break
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
    await withCryptoVerification(t('admin.crypto.rpcAutoVerify', { chain }), async (verification) => {
    const { picked, probed } = await autoPickRPC(chain, exclude, want, verification)
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
    })
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
  if (id !== activeTab.value) {
    securityDialog.value?.forceClose()
    clearWalletInputs()
  }
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
  if (loadingConfig.value) return
  loadingConfig.value = true
  configError.value = ''
  try {
    const loaded = await getCryptoConfig()
    // Earlier saves may predate some keys (rpc, chains); normalise here so a
    // legacy row cannot blank the whole page on a null index.
    config.value = {
      ...loaded,
      chains: loaded.chains ?? [],
      // Public reads contain only configured-chain metadata. Do not retain
      // credentials even if an older server accidentally includes them here.
      rpc: {},
      sweep_min_per_address: loaded.sweep_min_per_address ?? {},
      gas_topup: loaded.gas_topup ?? {},
      confirmations: loaded.confirmations ?? {},
      scan_range: loaded.scan_range ?? {},
      hot_wallet_evm: loaded.hot_wallet_evm ?? '',
      hot_wallet_solana: loaded.hot_wallet_solana ?? '',
      hot_index: 0,
      credit_min_amount: loaded.credit_min_amount ?? 0.001,
      credit_max_amount: loaded.credit_max_amount ?? 10000,
      sweep_auto: loaded.sweep_auto ?? false
    }
    configuredRPCChains.value = [...(loaded.rpc_configured ?? [])]
    rpcUnlocked.value = false
    rpcError.value = ''
    loadedSnapshot.value = JSON.parse(JSON.stringify(config.value))
    syncRPCRows()
  } catch (err) {
    configError.value = extractApiErrorMessage(err, t('admin.crypto.configLoadFailed'))
  } finally {
    loadingConfig.value = false
  }
}

async function manageRPC() {
  if (!config.value || loadingPrivateRPC.value || savingConfig.value) return
  loadingPrivateRPC.value = true
  rpcError.value = ''
  try {
    await withCryptoVerification(t('admin.crypto.securityConfigRead'), async (verification) => {
      const loaded = await getCryptoConfigPrivate(verification)
      if (!config.value || !loadedSnapshot.value || activeTab.value !== 'config') return
      const rpc = { ...(loaded.rpc ?? {}) }
      // Reading credentials changes neither the form's other edits nor the
      // save diff. Only subsequent endpoint edits may submit the RPC map.
      config.value.rpc = { ...rpc }
      loadedSnapshot.value.rpc = { ...rpc }
      configuredRPCChains.value = Object.keys(rpc).filter(chain => rpc[chain]?.trim())
      rpcUnlocked.value = true
      syncRPCRows()
    })
  } catch (err) {
    rpcError.value = extractApiErrorMessage(err, t('admin.crypto.rpcLoadFailed'))
  } finally {
    loadingPrivateRPC.value = false
  }
}

// Keep this strict allowlist aligned with the server. Any other changed field
// requires payment verification, including fields added in the future.
const LOW_RISK_CONFIG_FIELDS = new Set<keyof CryptoConfigPatch>([
  'sweep_global_threshold', 'sweep_min_per_address', 'scan_range',
  'credit_min_amount', 'credit_max_amount'
])

const RISKY_FIELDS: Array<{ key: keyof CryptoConfig; label: string }> = [
  { key: 'enabled', label: 'admin.crypto.enabled' },
  { key: 'hot_wallet_evm', label: 'admin.crypto.hotWalletEvm' },
  { key: 'hot_wallet_solana', label: 'admin.crypto.hotWalletSolana' },
  { key: 'sweep_auto', label: 'admin.crypto.sweepAuto' },
  { key: 'credit_multiplier', label: 'admin.crypto.creditMultiplier' },
  { key: 'chains', label: 'admin.crypto.chains' },
  { key: 'rpc', label: 'admin.crypto.rpc' },
  { key: 'gas_topup', label: 'admin.crypto.gasTopup' },
  { key: 'confirmations', label: 'admin.crypto.confirmations' }
]

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
    if (field.key === 'rpc') {
      out.push(t('admin.crypto.rpcChanged'))
      continue
    }
    out.push(`${t(field.label)}: ${formatChange(a)} → ${formatChange(b)}`)
  }
  return out
})

function formatChange(value: unknown): string {
  if (value === null || value === undefined || value === '') return '—'
  if (typeof value === 'boolean') return value ? t('common.enabled') : t('common.disabled')
  if (typeof value === 'object') return JSON.stringify(value)
  return String(value)
}

async function saveConfig() {
  if (!config.value || savingConfig.value) return
  const patch = JSON.parse(JSON.stringify(buildConfigPatch())) as CryptoConfigPatch
  if (!Object.keys(patch).length) return
  const snapshot = JSON.parse(JSON.stringify(config.value)) as CryptoConfig
  const label = `${t('admin.crypto.saveConfirmMessage', { count: riskyChanges.value.length })}\n${riskyChanges.value.join('\n')}`
  savingConfig.value = true
  configSaved.value = false
  const operationTab = activeTab.value
  try {
    if (('hot_wallet_evm' in patch || 'hot_wallet_solana' in patch) && !await notifyOperationIntent('destination_change')) return
    if (activeTab.value !== operationTab) return
    // Send only the fields that actually changed. The backend patch applies
    // pointers onto the stored config, so posting the whole object used to
    // rewrite maps the UI never renders (rpc, gas_topup, confirmations,
    // scan_range, per-chain thresholds) — a null map could blank them.
    const save = async (verification?: CryptoVerification) => {
      await updateCryptoConfig(patch, verification)
      loadedSnapshot.value = snapshot
      if (rpcUnlocked.value) configuredRPCChains.value = Object.keys(snapshot.rpc).filter(chain => snapshot.rpc[chain]?.trim())
      configSaved.value = true
      await loadWalletStatus()
    }
    const requiresVerification = Object.keys(patch).some(key => !LOW_RISK_CONFIG_FIELDS.has(key as keyof CryptoConfigPatch))
    if (requiresVerification) await withCryptoVerification(label, save)
    else await save()
  } catch (err) {
    appStore.showError(extractApiErrorMessage(err, t('common.error')))
  } finally {
    savingConfig.value = false
  }
}

// buildConfigPatch diffs the form against the loaded snapshot. Map fields are
// compared as a whole: if any chain entry changed, that map is sent in full.
function buildConfigPatch(): CryptoConfigPatch {
  const before = loadedSnapshot.value
  const after = config.value
  if (!before || !after) return {}
  const patch: Record<string, unknown> = {}
  for (const key of Object.keys(after) as Array<keyof CryptoConfig>) {
    // Metadata is read-only; a hidden public RPC placeholder cannot erase
    // configured endpoints when another setting is saved.
    if (key === 'hot_index' || key === 'rpc_configured' || (key === 'rpc' && !rpcUnlocked.value)) continue
    if (JSON.stringify(before[key]) !== JSON.stringify(after[key])) {
      patch[key] = after[key]
    }
  }
  return patch as CryptoConfigPatch
}

// requestSaveConfig gates the write behind a confirmation whenever the diff
// touches money-moving fields.
function requestSaveConfig() {
  void saveConfig()
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
  if (!(await copyToClipboard(address))) return
  copiedAddress.value = address
  window.setTimeout(() => {
    if (copiedAddress.value === address) copiedAddress.value = ''
  }, 1500)
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
  const label = t('admin.crypto.addressConfirmMessage', {
    address: row.address,
    action: t(next === 'disabled' ? 'admin.crypto.disable' : 'admin.crypto.enable')
  })
  await withCryptoVerification(label, async (verification) => {
    await setAddressStatus(row.id, next, verification)
  }) && await loadAddresses()
}

function confirmChannelToggle(value: boolean) {
  if (value && walletSecurityLocked.value) {
    appStore.showError(t('admin.crypto.walletSecurityRecoveryHint'))
    return
  }
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
  if (sweeping.value) return
  sweeping.value = true
  const operationTab = activeTab.value
  try {
    if (!dryRun && !await notifyOperationIntent('sweep')) return
    if (activeTab.value !== operationTab) return
    if (!dryRun) await loadSweepPlan()
    if (activeTab.value !== operationTab) return
    const label = dryRun ? t('admin.crypto.sweepDryRun') : sweepConfirmMessage.value
    const completed = await withCryptoVerification(label, async (verification) => {
      sweepSummary.value = await runSweep(dryRun, verification)
    })
    if (completed) await Promise.all([loadSweepRecords(), loadSweepPlan()])
  } finally {
    sweeping.value = false
  }
}

// The run button opens a confirmation that restates the destination and the
// amount: two deliberate clicks before any funds move.
function requestSweep() {
  if (!sweepReady.value) return
  void doSweep(false)
}


async function loadSweepRecords() {
  sweepRecords.value = await listSweepRecords()
  blockedSweeps.value = await listBlockedSweeps()
}

// Reconcile never signs a transaction: it only reads the chain and either
// advances a confirmed job or releases an address whose tx never landed.
async function reconcileJob(id: number) {
  if (reconciling.value !== null) return
  reconciling.value = id
  try {
    await withCryptoVerification(t('admin.crypto.reconcile'), async (verification) => {
      const result = await reconcileSweep(id, verification)
      appStore.showSuccess(result.message)
    })
    await Promise.all([loadSweepRecords(), loadSweepPlan()])
  } finally {
    reconciling.value = null
  }
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
  if (loadingHealth.value) return
  loadingHealth.value = true
  healthError.value = ''
  try {
    const pool = await getPoolStatus()
    let hot: HotWalletStatus[] = []
    if (walletCanDerive.value) {
      hot = await getHotWalletStatus()
    }
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
  if (exporting.value) return
  exporting.value = true
  try {
    const chain = depositChain.value
    const status = depositStatus.value
    const operationTab = activeTab.value
    await withCryptoVerification(t('admin.crypto.csvVerify'), async (verification) => {
      const blob = await exportDepositsCSV(chain, status, verification)
      if (operationTab !== activeTab.value) return
      downloadBlob(blob, 'crypto-deposits.csv')
    })
  } finally {
    exporting.value = false
  }
}

async function loadWalletStatus() {
  loadingWallet.value = true
  try {
    const st = await getWalletStatus()
    walletOk.value = st.ok === true
    walletConfigured.value = st.configured === true
    walletLoaded.value = st.ready === true
    walletChannelEnabled.value = st.enabled === true
    if (st.security) updateWalletSecurityState(st.security)
  } catch {
    // Reflect unavailable state in the wallet badge without breaking the page.
    walletOk.value = false
    walletLoaded.value = false
    walletChannelEnabled.value = false
  } finally {
    loadingWallet.value = false
  }
}

function updateWalletSecurityState(state: WalletSecurityStatus) {
  walletSecurityLocked.value = state.locked || state.deposits_paused
  if (walletSecurityLocked.value) reflectSecurityChannelPause()
}

function reflectSecurityChannelPause() {
  walletChannelEnabled.value = false
  // The security guard pauses the channel, and recovery deliberately keeps it
  // disabled. Preserve other unsaved edits and never reread RPC credentials
  // with a second TOTP prompt just to update the public on/off indicator.
  if (config.value) config.value.enabled = false
  if (loadedSnapshot.value) loadedSnapshot.value.enabled = false
}

async function onWalletSecurityRecovered() {
  reflectSecurityChannelPause()
  await loadWalletStatus()
}

// Derive the Gas wallet addresses in the main service and fill the destination.
async function fillHotWalletAddresses() {
  if (!walletCanDerive.value || loadingHotAddress.value) return
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

async function submitInitialize() {
  if (!depositMnemonic.value.trim() || !initialHotMnemonic.value.trim() || initializing.value) return
  initializing.value = true
  const operationTab = activeTab.value
  try {
    const completed = await withCryptoVerification(t('admin.crypto.walletInitWarning'), async (verification) => {
      if (operationTab !== activeTab.value) return
      await initializeWallet(depositMnemonic.value.trim(), initialHotMnemonic.value.trim(), verification)
    })
    if (completed && operationTab === activeTab.value) {
      await loadWalletStatus()
      appStore.showSuccess(t('admin.crypto.walletInitializeDone'))
    }
  } finally {
    depositMnemonic.value = ''
    initialHotMnemonic.value = ''
    initializing.value = false
  }
}

function downloadBlob(blob: Blob, filename: string) {
  const url = URL.createObjectURL(blob)
  const anchor = document.createElement('a')
  anchor.href = url
  anchor.download = filename
  document.body.appendChild(anchor)
  try { anchor.click() } finally { anchor.remove(); URL.revokeObjectURL(url) }
}

function closeWalletDialog() {
  walletDialog.value = null
  hotMnemonic.value = ''
}

function openWalletDialog() {
  closeWalletDialog()
  walletDialog.value = 'import'
}

async function submitWallet() {
  if (!walletDialog.value || !hotMnemonic.value.trim()) return
  const operationTab = activeTab.value
  let mnemonic = hotMnemonic.value.trim()
  walletDialog.value = null
  try {
    const completed = await withCryptoVerification(t('admin.crypto.hotImportWarning'), async (verification) => {
      if (operationTab !== activeTab.value) return
      const result = await importHotMnemonic(mnemonic, verification)
      if (!result.saved) throw new Error(t('common.error'))
    })
    if (completed && operationTab === activeTab.value) {
      hotAddressNote.value = ''
      gasWalletRevision.value++
      await loadWalletStatus()
      appStore.showSuccess(t('admin.crypto.hotImportDone'))
    }
  } finally { mnemonic = ''; closeWalletDialog() }
}

function clearWalletInputs() {
  closeWalletDialog()
  depositMnemonic.value = ''
  initialHotMnemonic.value = ''
}

watch(activeTab, clearWalletInputs)
watch(activeTab, () => { mnemonicsDialogOpen.value = false })
watch(activeTab, () => { privateKeyAddress.value = null }, { flush: 'sync' })
onUnmounted(clearWalletInputs)

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
  void loadWalletStatus()
})
</script>

<style scoped>
.crypto-view__gas-funding { grid-column: 1 / -1; margin-bottom: 1rem; }
.crypto-view__wallet-form { display: grid; gap: .75rem; font-size: .875rem; line-height: 1.7; }
.crypto-view__wallet-actions { display: flex; flex-wrap: wrap; gap: .75rem; margin-top: 1rem; }
.crypto-view__wallet-actions p { flex-basis: 100%; }
.crypto-view__security { margin-bottom: .625rem; padding: .875rem 1rem; }
.crypto-view__security :deep(.wallet-security__hint) { margin-top: .25rem; font-size: .75rem; }
.crypto-view__security :deep(.wallet-security__badges) { margin-top: .5rem; }
.crypto-view__notifications { margin-bottom: 1rem; border: 1px solid var(--signal-line, #64748b30); border-radius: .75rem; }
.crypto-view__notifications > summary { padding: .75rem 1rem; cursor: pointer; font-size: .8125rem; font-weight: 600; }
.crypto-view__notifications > summary > span { margin-inline-start: .75rem; font-size: .75rem; font-weight: 400; color: var(--signal-muted, #64748b); }
.crypto-view__bark { margin: 0; border: 0; box-shadow: none; }
.crypto-view__bark :deep(.px-6) { padding: .875rem 1rem; }
.crypto-view__bark :deep(.space-y-6 > :not([hidden]) ~ :not([hidden])) { margin-top: .875rem; }
.crypto-view__rpc-overview { padding: 0 1rem 1rem; }
.crypto-view__rpc-statuses { display: flex; flex-wrap: wrap; gap: .5rem; margin: .75rem 0; }
.crypto-view__network-limits { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 1rem; padding: 0 1.25rem 1rem; }
.crypto-view__network-limits label { display: grid; gap: .5rem; min-width: 0; }
.crypto-view__intent-error { margin-bottom: 1.25rem; color: #dc2626; }
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
.crypto-view__blocked {
  margin: 0 0 16px;
  padding: 14px 16px;
  border: 1px solid var(--danger-500, #dc2626);
  border-radius: 12px;
  background: color-mix(in srgb, var(--danger-500, #dc2626) 8%, transparent);
}

.crypto-view__blocked-head {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: 8px;
  margin-bottom: 10px;
}

.crypto-view__blocked .crypto-view__table {
  margin: 0;
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
