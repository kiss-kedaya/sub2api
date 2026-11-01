<template>
  <AppLayout>
    <div class="crypto-view flex w-full min-w-0 flex-col gap-4 sm:gap-6">
      <AdminPageHeader class="crypto-view__header" :title="t('admin.crypto.title')" :description="t('admin.crypto.description')">
        <template v-if="loadedSnapshot" #actions>
          <div class="crypto-view__header-status flex flex-wrap items-center gap-2">
            <UBadge :color="loadedSnapshot.enabled && !walletSecurityLocked ? 'success' : 'neutral'" variant="subtle">{{ t(loadedSnapshot.enabled && !walletSecurityLocked ? 'admin.crypto.channelActive' : 'admin.crypto.channelPaused') }}</UBadge>
            <UBadge :color="walletReady ? 'success' : 'warning'" variant="subtle">{{ t(walletStateLabel) }}</UBadge>
            <CryptoGrantStatus />
          </div>
        </template>
      </AdminPageHeader>

      <CryptoWalletSecurityPanel class="crypto-view__security" @change="updateWalletSecurityState" @recovered="onWalletSecurityRecovered" />

      <UCollapsible class="crypto-view__notifications" :unmount-on-hide="false">
        <UButton color="neutral" variant="ghost" trailing-icon="i-lucide-chevron-down" class="-mx-2.5 group" :ui="{ trailingIcon: 'transition-transform group-data-[state=open]:rotate-180' }">
          <span class="font-medium text-highlighted">{{ t('admin.crypto.walletNotificationSettings') }}</span>
          <span class="text-muted">{{ t('admin.crypto.walletNotificationOptional') }}</span>
        </UButton>
        <template #content>
          <ProfileBarkCard class="crypto-view__bark mt-3" :title="t('admin.crypto.walletBarkTitle')" :description="t('admin.crypto.walletBarkDescription')" />
        </template>
      </UCollapsible>

      <UAlert v-if="operationIntentError" role="alert" class="crypto-view__intent-error" color="error" variant="subtle" icon="i-lucide-circle-alert" :title="operationIntentError" />

      <UTabs
        :model-value="activeTab"
        :items="tabs"
        variant="link"
        class="crypto-view__tabs w-full gap-4 sm:gap-6"
        :ui="{ list: 'border-b border-default', trigger: 'grow-0' }"
        :aria-label="t('admin.crypto.title')"
        @update:model-value="(value: string | number) => switchTab(String(value))"
      >
        <!-- Configuration -->
        <template #config>
          <div id="crypto-panel-config" class="crypto-view__panel flex w-full flex-col gap-4 sm:gap-6 lg:max-w-3xl mx-auto">
            <div v-if="loadingConfig" class="flex justify-center py-16"><LoadingSpinner /></div>
            <UAlert
              v-else-if="!config"
              role="status"
              color="neutral"
              variant="subtle"
              icon="i-lucide-circle-alert"
              :title="configError || t('admin.crypto.configLoadFailed')"
              :actions="[{ label: t('common.refresh'), color: 'neutral', variant: 'outline', onClick: () => { void loadConfig() } }]"
            />
            <template v-else>
              <UPageCard
                class="crypto-view__save-bar"
                :title="t('admin.crypto.saveTitle')"
                :description="t('admin.crypto.saveHint')"
                variant="naked"
                orientation="horizontal"
              >
                <div class="flex items-center gap-3 lg:ms-auto">
                  <span v-if="configSaved" class="text-sm text-success" role="status">{{ t('common.saved') }}</span>
                  <UButton :loading="savingConfig" :disabled="savingConfig" class="w-fit" @click="requestSaveConfig">{{ savingConfig ? t('common.saving') : t('common.save') }}</UButton>
                </div>
              </UPageCard>

              <CryptoGasWalletPanel class="crypto-view__gas-funding" :available="walletCanDerive" :chains="loadedSnapshot?.chains ?? []" :revision="gasWalletRevision" />

              <section class="crypto-view__group">
                <UPageCard :title="t('admin.crypto.groupChannel')" :description="t('admin.crypto.groupChannelDesc')" variant="naked" class="mb-4" />
                <UPageCard variant="subtle">
                  <UFormField :label="t('admin.crypto.enabled')" :description="t('admin.crypto.enabledHint')" class="flex items-center justify-between gap-4">
                    <Toggle :model-value="config.enabled" @update:model-value="confirmChannelToggle" />
                  </UFormField>
                  <USeparator />
                  <UFormField :label="t('admin.crypto.chains')" :description="t('admin.crypto.chainsHint')">
                    <UCheckboxGroup
                      :model-value="config.chains"
                      :items="chainItems"
                      class="mt-3"
                      :ui="{ fieldset: 'grid grid-cols-1 gap-3 sm:grid-cols-2' }"
                      @update:model-value="setChains"
                    />
                  </UFormField>
                </UPageCard>
              </section>

              <!-- Seeds are managed and encrypted by the main service, with fresh 2FA for changes. -->
              <section class="crypto-view__group">
                <UPageCard :title="t('admin.crypto.walletLockTitle')" :description="t('admin.crypto.walletLockDesc')" variant="naked" class="mb-4" />
                <UPageCard variant="subtle">
                  <UFormField :label="t('admin.crypto.walletStatusLabel')" :description="t('admin.crypto.walletManagedHint')" class="flex max-sm:flex-col justify-between items-start gap-4" :ui="{ container: 'shrink-0' }">
                    <div class="crypto-view__lockstate flex items-center gap-2">
                      <UBadge :color="walletReady ? 'success' : walletCanDerive ? 'warning' : 'error'" variant="subtle">{{ t(walletStateLabel) }}</UBadge>
                      <UButton color="neutral" variant="outline" size="sm" :disabled="loadingWallet" @click="loadWalletStatus">{{ t('common.refresh') }}</UButton>
                    </div>
                  </UFormField>
                  <UAlert v-if="walletCanDerive && !walletReady" role="status" color="warning" variant="subtle" icon="i-lucide-triangle-alert" :description="t('admin.crypto.walletNotReady')" />
                  <template v-if="!walletConfigured">
                    <USeparator />
                    <UAlert color="error" variant="subtle" icon="i-lucide-triangle-alert" :description="t('admin.crypto.walletInitWarning')" />
                    <UFormField :label="t('admin.crypto.depositMnemonic')" :description="t('admin.crypto.depositMnemonicImpact')">
                      <UInput id="crypto-deposit-mnemonic" v-model="depositMnemonic" type="password" autocomplete="off" spellcheck="false" class="mt-2 w-full" />
                    </UFormField>
                    <UFormField :label="t('admin.crypto.hotMnemonic')" :description="t('admin.crypto.hotMnemonicScope')">
                      <UInput id="crypto-initial-hot-mnemonic" v-model="initialHotMnemonic" type="password" autocomplete="off" spellcheck="false" class="mt-2 w-full" />
                    </UFormField>
                    <div class="flex justify-end">
                      <UButton :loading="initializing" :disabled="initializing || !depositMnemonic.trim() || !initialHotMnemonic.trim()" @click="submitInitialize">
                        {{ initializing ? t('common.saving') : t('admin.crypto.walletInitializeAction') }}
                      </UButton>
                    </div>
                  </template>
                  <USeparator />
                  <UFormField :label="t('admin.crypto.walletToolsLabel')" :description="t('admin.crypto.walletToolsHint')" class="crypto-view__wallet-actions flex max-sm:flex-col justify-between items-start gap-4" :ui="{ container: 'shrink-0' }">
                    <div class="flex flex-wrap gap-2 sm:justify-end">
                      <UButton color="neutral" variant="outline" size="sm" data-testid="crypto-mnemonics-open" :disabled="!walletCanDerive" @click="mnemonicsDialogOpen = true">{{ t('admin.crypto.mnemonicsExportAction') }}</UButton>
                      <UButton color="neutral" variant="outline" size="sm" data-testid="crypto-hot-import-open" :disabled="!walletCanDerive" @click="openWalletDialog()">{{ t('admin.crypto.hotImportAction') }}</UButton>
                    </div>
                  </UFormField>
                </UPageCard>
              </section>

              <section id="crypto-rpc-settings" class="crypto-view__group">
                <UPageCard :title="t('admin.crypto.groupRpc')" :description="t('admin.crypto.groupRpcDesc')" variant="naked" class="mb-4" />
                <UPageCard variant="subtle">
                  <div v-if="!rpcUnlocked" class="crypto-view__rpc-overview flex max-sm:flex-col justify-between items-start gap-4">
                    <div class="min-w-0 space-y-2">
                      <p class="text-sm text-muted">{{ t('admin.crypto.rpcHiddenHint') }}</p>
                      <div class="flex flex-wrap gap-1.5">
                        <UBadge
                          v-for="chain in config.chains"
                          :key="`rpc-status-${chain}`"
                          :color="configuredRPCChains.includes(chain) ? 'success' : 'neutral'"
                          variant="subtle"
                          :data-testid="`rpc-configured-${chain}`"
                        >{{ chain }} · {{ t(configuredRPCChains.includes(chain) ? 'admin.crypto.rpcConfigured' : 'admin.crypto.rpcNotConfigured') }}</UBadge>
                      </div>
                    </div>
                    <UButton color="neutral" variant="outline" size="sm" class="shrink-0" :disabled="loadingPrivateRPC || savingConfig" @click="manageRPC">
                      {{ loadingPrivateRPC ? t('common.loading') : t('admin.crypto.rpcManage') }}
                    </UButton>
                  </div>
                  <UAlert v-if="rpcError" role="alert" color="error" variant="subtle" icon="i-lucide-circle-alert" :title="rpcError" />

                  <!-- Per-chain endpoint list: add / test / remove, and pull candidates from the public chainlist registry. -->
                  <template v-if="rpcUnlocked">
                    <template v-for="(chain, chainIndex) in config.chains" :key="`rpc-${chain}`">
                      <USeparator v-if="chainIndex > 0" />
                      <div class="crypto-view__rpc-chain space-y-3">
                        <div class="flex flex-wrap items-center justify-between gap-2">
                          <span class="font-mono text-sm font-medium text-highlighted">{{ chain }}</span>
                          <div class="flex flex-wrap gap-1.5">
                            <UButton color="neutral" variant="outline" size="xs" icon="i-lucide-radio-tower" :disabled="fetchingCandidates === chain" @click="autoFillRPC(chain)">
                              {{ fetchingCandidates === chain ? t('admin.crypto.rpcFetching') : t('admin.crypto.rpcFetch') }}
                            </UButton>
                            <UButton color="neutral" variant="outline" size="xs" icon="i-lucide-plus" @click="addRPCRow(chain)">{{ t('admin.crypto.rpcAdd') }}</UButton>
                            <UButton color="neutral" variant="outline" size="xs" icon="i-lucide-play" :disabled="testingChain === chain" @click="testAllEndpoints(chain)">
                              {{ testingChain === chain ? t('common.loading') : t('admin.crypto.rpcTestAll') }}
                            </UButton>
                          </div>
                        </div>
                        <div v-for="(row, idx) in rpcRows[chain] ?? []" :key="`${chain}-${idx}`" class="flex flex-wrap items-center gap-2 sm:flex-nowrap">
                          <UInput
                            v-model="row.url"
                            class="crypto-view__rpc-input min-w-0 flex-1 basis-full sm:basis-auto"
                            :ui="{ base: 'font-mono text-xs' }"
                            :placeholder="t('admin.crypto.rpcPlaceholder')"
                            :aria-label="`${chain} ${t('admin.crypto.rpcEndpoint')} ${idx + 1}`"
                            @change="persistRPC(chain)"
                          />
                          <UBadge v-if="row.status" :color="row.ok ? 'success' : 'error'" variant="subtle" class="shrink-0" :title="row.error || ''">
                            {{ row.ok ? `${row.latency_ms}ms · #${row.block}` : t('admin.crypto.rpcFail') }}
                          </UBadge>
                          <span v-else class="w-6 shrink-0 text-center text-sm text-dimmed">—</span>
                          <UButton color="neutral" variant="ghost" size="xs" :disabled="row.testing" @click="testEndpoint(chain, idx)">{{ t('admin.crypto.rpcTest') }}</UButton>
                          <UButton color="error" variant="ghost" size="xs" @click="removeRPCRow(chain, idx)">{{ t('admin.crypto.rpcRemove') }}</UButton>
                        </div>
                        <p v-if="!(rpcRows[chain] ?? []).length" class="text-sm text-muted">{{ t('common.noData') }}</p>
                        <p v-if="rpcFetchNotes[chain]" class="crypto-view__fetch-note text-xs text-muted">{{ rpcFetchNotes[chain] }}</p>
                      </div>
                    </template>
                  </template>
                </UPageCard>
              </section>

              <section class="crypto-view__group">
                <UPageCard :title="t('admin.crypto.groupSweep')" :description="t('admin.crypto.groupSweepDesc')" variant="naked" class="mb-4" />
                <UPageCard variant="subtle">
                  <UFormField :label="t('admin.crypto.globalThreshold')" :description="t('admin.crypto.globalThresholdHint')" class="flex max-sm:flex-col justify-between items-start gap-4">
                    <UInput v-model.number="config.sweep_global_threshold" data-testid="crypto-sweep-threshold" type="number" step="1" class="w-40" />
                  </UFormField>
                  <USeparator />
                  <UFormField :label="t('admin.crypto.creditMin')" :description="t('admin.crypto.creditMinHint')" class="flex max-sm:flex-col justify-between items-start gap-4">
                    <UInput v-model.number="config.credit_min_amount" data-testid="crypto-credit-min" type="number" step="0.001" min="0" class="w-40" />
                  </UFormField>
                  <USeparator />
                  <UFormField :label="t('admin.crypto.creditMax')" :description="t('admin.crypto.creditMaxHint')" class="flex max-sm:flex-col justify-between items-start gap-4">
                    <UInput v-model.number="config.credit_max_amount" data-testid="crypto-credit-max" type="number" step="1" min="0" class="w-40" />
                  </UFormField>
                  <template v-for="chain in config.chains" :key="`scan-limits-${chain}`">
                    <USeparator />
                    <div class="crypto-view__network-limits grid gap-4 sm:grid-cols-2">
                      <UFormField :label="`${chain} · ${t('admin.crypto.sweepMinPerAddress')}`">
                        <UInput v-model.number="config.sweep_min_per_address[chain]" :data-testid="`crypto-sweep-min-${chain}`" type="number" min="0" step="0.001" class="mt-1 w-full" />
                      </UFormField>
                      <UFormField :label="`${chain} · ${t('admin.crypto.scanRange')}`">
                        <UInput v-model.number="config.scan_range[chain]" :data-testid="`crypto-scan-range-${chain}`" type="number" min="1" step="1" class="mt-1 w-full" />
                      </UFormField>
                    </div>
                  </template>
                  <USeparator />
                  <UFormField
                    :label="t('admin.crypto.hotWalletEvm')"
                    :description="t('admin.crypto.sweepDestinationHint')"
                    :error="config.hot_wallet_evm.trim() ? false : t('admin.crypto.hotWalletMissing')"
                    class="flex max-sm:flex-col justify-between items-start gap-4"
                    :ui="{ container: 'w-full sm:max-w-sm' }"
                  >
                    <UInput v-model="config.hot_wallet_evm" placeholder="0x…" class="w-full" :ui="{ base: 'font-mono text-xs' }" />
                    <div class="mt-2 flex flex-wrap items-center gap-2">
                      <UButton
                        color="neutral"
                        variant="outline"
                        size="xs"
                        :disabled="loadingHotAddress || !walletCanDerive"
                        :title="!walletOk ? t('admin.crypto.walletUnavailable') : !walletLoaded ? t('admin.crypto.hotAddressLocked') : ''"
                        @click="fillHotWalletAddresses"
                      >
                        {{ loadingHotAddress ? t('common.loading') : t('admin.crypto.hotAddressFill') }}
                      </UButton>
                      <span v-if="hotAddressNote" class="text-xs text-muted">{{ hotAddressNote }}</span>
                    </div>
                    <p class="mt-2 text-xs text-muted">{{ t('admin.crypto.hotIndexHint') }}</p>
                  </UFormField>
                  <USeparator />
                  <UFormField :label="t('admin.crypto.sweepAuto')" :description="t('admin.crypto.sweepAutoHint')" class="flex items-center justify-between gap-4">
                    <Toggle :model-value="config.sweep_auto" @update:model-value="confirmSweepAutoToggle" />
                  </UFormField>
                </UPageCard>
              </section>

              <!-- Health: an empty pool blocks new deposits; a dry hot wallet blocks sweeps. Both are silent failures without this. -->
              <section class="crypto-view__group" data-testid="crypto-health">
                <UPageCard :title="t('admin.crypto.healthTitle')" :description="t('admin.crypto.healthDesc')" variant="naked" orientation="horizontal" :ui="{ container: 'lg:grid-cols-[minmax(0,1fr)_auto]' }" class="mb-4">
                  <div class="flex items-center gap-3 lg:ms-auto">
                    <span v-if="healthError" class="text-sm text-error">{{ healthError }}</span>
                    <UButton color="neutral" variant="outline" size="sm" class="w-fit" :disabled="loadingHealth || !walletCanDerive" @click="loadHealth">{{ t('common.refresh') }}</UButton>
                  </div>
                </UPageCard>
                <UPageGrid v-if="Object.keys(poolStatus?.free ?? {}).length || hotWallets.length" class="gap-4 sm:grid-cols-2 lg:grid-cols-3">
                  <UPageCard
                    v-for="(count, family) in poolStatus?.free ?? {}"
                    :key="`pool-${family}`"
                    variant="subtle"
                    :title="`${t('admin.crypto.poolFree')} · ${family}`"
                    :ui="statCardUi"
                  >
                    <div class="flex items-center gap-2">
                      <span class="text-2xl font-semibold text-highlighted">{{ count }}</span>
                      <UBadge v-if="count <= (poolStatus?.low_watermark ?? 0)" color="warning" variant="subtle">{{ t('admin.crypto.poolLow') }}</UBadge>
                    </div>
                  </UPageCard>
                  <UPageCard v-for="hw in hotWallets" :key="`hw-${hw.chain}`" variant="subtle" :title="`${hw.symbol} · ${hw.chain}`" :ui="statCardUi">
                    <p class="text-xs text-muted">{{ t('admin.crypto.hotGasWallet') }} · <span class="font-mono">{{ short(hw.address) }}</span></p>
                    <div class="flex items-center gap-2">
                      <span class="text-2xl font-semibold" :class="hw.error ? 'text-warning text-base' : 'text-highlighted'">
                        {{ hw.error ? t(hw.error_code === 'RPC_DISCOVERY_PENDING' ? 'admin.crypto.rpcDiscovering' : 'admin.crypto.hotWalletUnreachable') : hw.balance }}
                      </span>
                      <UBadge v-if="!hw.error && !hw.enough" color="warning" variant="subtle">{{ t('admin.crypto.gasInsufficient') }}</UBadge>
                    </div>
                    <p v-if="hw.rpc_source === 'chainlist'" class="text-xs text-muted">{{ t('admin.crypto.rpcAutoSource') }}</p>
                  </UPageCard>
                </UPageGrid>
              </section>
            </template>
          </div>
        </template>

        <!-- Addresses -->
        <template #addresses>
          <div id="crypto-panel-addresses" class="crypto-view__panel flex flex-col gap-4">
            <!-- Whole-set totals: they must not change when paging. -->
            <UPageGrid class="lg:grid-cols-5 gap-4 sm:gap-6 lg:gap-px">
              <UPageCard
                v-for="stat in addressStats"
                :key="stat.label"
                :icon="stat.icon"
                :title="t(stat.label)"
                variant="subtle"
                :ui="statCardUi"
                class="lg:rounded-none first:rounded-l-lg last:rounded-r-lg"
              >
                <span class="text-2xl font-semibold text-highlighted">{{ stat.value }}</span>
              </UPageCard>
            </UPageGrid>

            <div class="crypto-view__filters flex flex-wrap items-center justify-between gap-1.5">
              <UInput
                v-model.number="addressUserId"
                type="number"
                icon="i-lucide-search"
                class="max-w-sm"
                :placeholder="t('admin.crypto.userId')"
                @keyup.enter="applyAddressFilter"
              />
              <div class="flex flex-wrap items-center gap-1.5">
                <USelect :model-value="addressFamily || ALL" :items="familyItems" class="min-w-32" :aria-label="t('admin.crypto.family')" @update:model-value="setAddressFamily" />
                <UButton color="neutral" variant="outline" icon="i-lucide-search" @click="applyAddressFilter">{{ t('common.search') }}</UButton>
                <UButton color="neutral" variant="outline" icon="i-lucide-refresh-cw" :disabled="loadingAddresses" @click="loadAddresses">{{ t('common.refresh') }}</UButton>
                <UDropdownMenu :items="columnMenu('address')" :content="{ align: 'end' }">
                  <UButton color="neutral" variant="outline" trailing-icon="i-lucide-settings-2">{{ t('admin.crypto.columns') }}</UButton>
                </UDropdownMenu>
              </div>
            </div>

            <UTable
              v-model:column-visibility="addressVisibility"
              :data="addresses"
              :columns="addressTableColumns"
              :loading="loadingAddresses"
              :empty="t('common.noData')"
              :ui="tableUi"
            >
              <template v-for="col in sortableAddressKeys" :key="col" #[`${col}-header`]>
                <UButton
                  color="neutral"
                  variant="ghost"
                  size="sm"
                  class="-mx-2.5"
                  :class="addressSortBy === col ? 'text-highlighted' : ''"
                  :trailing-icon="addressSortBy !== col ? 'i-lucide-arrow-up-down' : addressSortDir === 'asc' ? 'i-lucide-arrow-up-narrow-wide' : 'i-lucide-arrow-down-wide-narrow'"
                  @click="sortAddresses(col)"
                >{{ addressColumnLabel(col) }}</UButton>
              </template>
              <template #address-cell="{ row }">
                <UButton color="neutral" variant="link" size="sm" class="px-0 font-mono" :title="row.original.address" @click="copyAddress(row.original.address)">
                  {{ short(row.original.address) }}
                  <UIcon :name="copiedAddress === row.original.address ? 'i-lucide-check' : 'i-lucide-copy'" class="size-3.5 text-dimmed" />
                </UButton>
              </template>
              <template #credited_total-cell="{ row }">{{ fmtAmount(row.original.credited_total) }}</template>
              <template #swept_total-cell="{ row }">{{ fmtAmount(row.original.swept_total) }}</template>
              <template #status-cell="{ row }">
                <UBadge :color="statusColor(row.original.status)" variant="subtle">{{ statusLabel(row.original.status) }}</UBadge>
              </template>
              <template #actions-cell="{ row }">
                <div class="flex justify-end gap-1">
                  <UButton
                    v-if="row.original.family === 'evm' || row.original.family === 'solana'"
                    color="neutral"
                    variant="ghost"
                    size="xs"
                    icon="i-lucide-key-round"
                    :data-export-address="row.original.id"
                    @click="privateKeyAddress = { ...row.original }"
                  >{{ t('admin.crypto.privateKeyExportAction') }}</UButton>
                  <UButton :color="row.original.status === 'active' ? 'error' : 'neutral'" variant="ghost" size="xs" @click="toggleAddress(row.original)">
                    {{ row.original.status === 'active' ? t('admin.crypto.disable') : t('admin.crypto.enable') }}
                  </UButton>
                </div>
              </template>
            </UTable>

            <div class="crypto-view__pager flex flex-wrap items-center justify-between gap-3 border-t border-default pt-4">
              <span class="text-sm text-muted">{{ t('admin.crypto.pageSummary', { page: addressPage, pages: addressTotalPages, total: addressTotal }) }}</span>
              <UPagination
                v-if="addressTotalPages > 1"
                :page="addressPage"
                :total="addressTotal"
                :items-per-page="addressPageSize"
                @update:page="gotoAddressPage"
              />
            </div>
          </div>
        </template>

        <!-- Sweep -->
        <template #sweep>
          <div id="crypto-panel-sweep" class="crypto-view__panel flex flex-col gap-4 sm:gap-6">
            <div class="crypto-view__filters flex flex-wrap items-center justify-end gap-1.5">
              <UButton color="neutral" variant="outline" icon="i-lucide-refresh-cw" @click="loadSweepRecords">{{ t('common.refresh') }}</UButton>
              <UButton color="neutral" variant="outline" icon="i-lucide-list-checks" @click="loadSweepPlan">{{ t('admin.crypto.loadPlan') }}</UButton>
              <UButton color="neutral" variant="outline" icon="i-lucide-flask-conical" :disabled="sweeping" @click="doSweep(true)">{{ t('admin.crypto.sweepDryRun') }}</UButton>
              <UButton
                icon="i-lucide-arrow-up-from-line"
                :disabled="sweeping || !sweepReady"
                :title="sweepReady ? '' : !walletOk ? t('admin.crypto.walletUnavailable') : !walletLoaded ? t('admin.crypto.hotAddressLocked') : t('admin.crypto.walletNotReady')"
                @click="requestSweep"
              >{{ t('admin.crypto.sweepRun') }}</UButton>
            </div>
            <CryptoGasWalletPanel class="crypto-view__gas-funding" :available="walletCanDerive" :chains="loadedSnapshot?.chains ?? []" :revision="gasWalletRevision" />

            <section v-if="blockedSweeps.length" class="crypto-view__blocked space-y-3">
              <UAlert role="alert" color="warning" variant="subtle" icon="i-lucide-triangle-alert" :title="t('admin.crypto.blockedTitle')" :description="t('admin.crypto.blockedHint')" />
              <UTable :data="blockedSweeps" :columns="blockedColumns" :ui="tableUi">
                <template #address-cell="{ row }"><span class="font-mono">#{{ row.original.job_id }} · {{ short(row.original.address) }}</span></template>
                <template #status-cell="{ row }">
                  <div class="flex flex-col items-start gap-1">
                    <UBadge :color="statusColor(row.original.status)" variant="subtle">{{ statusLabel(row.original.status) }}</UBadge>
                    <span v-if="row.original.error" class="text-xs text-muted">{{ row.original.error }}</span>
                  </div>
                </template>
                <template #age_minutes-cell="{ row }">{{ row.original.age_minutes }}m</template>
                <template #actions-cell="{ row }">
                  <div class="flex justify-end">
                    <UButton color="neutral" variant="outline" size="xs" :disabled="reconciling === row.original.job_id" @click="reconcileJob(row.original.job_id)">
                      {{ reconciling === row.original.job_id ? t('common.loading') : t('admin.crypto.reconcile') }}
                    </UButton>
                  </div>
                </template>
              </UTable>
            </section>

            <section v-if="sweepSummary" class="crypto-view__plan space-y-3">
              <p class="crypto-view__plan-total text-sm text-toned">
                {{ t('admin.crypto.planned') }}: <strong class="text-highlighted">{{ sweepSummary.planned }}</strong> ·
                {{ t('admin.crypto.swept') }}: <strong class="text-highlighted">{{ sweepSummary.swept }}</strong> ·
                {{ t('admin.crypto.failed') }}: <strong class="text-highlighted">{{ sweepSummary.failed }}</strong> ·
                {{ t('admin.crypto.pending') }}: <strong class="text-highlighted">{{ sweepSummary.pending ?? 0 }}</strong> ·
                gas: <strong class="text-highlighted">{{ sweepSummary.gas_topups }}</strong>
                <UBadge v-if="sweepSummary.dry_run" color="neutral" variant="subtle" class="ms-1">dry-run</UBadge>
              </p>
              <UTable v-if="sweepSummary.entries?.length" :data="sweepSummary.entries" :columns="summaryColumns" :ui="tableUi">
                <template #address-cell="{ row }"><span class="font-mono">{{ short(row.original.address) }}</span></template>
                <template #status-cell="{ row }">
                  <div class="flex flex-col items-start gap-1">
                    <UBadge :color="statusColor(row.original.status)" variant="subtle">{{ statusLabel(row.original.status) }}</UBadge>
                    <span v-if="row.original.error" class="text-xs text-muted">{{ row.original.error }}</span>
                  </div>
                </template>
                <template #tx-cell="{ row }"><span class="font-mono">{{ short(row.original.sweep_tx_hash || row.original.gas_tx_hash) }}</span></template>
              </UTable>
            </section>

            <section v-if="sweepPlan" class="crypto-view__plan space-y-3">
              <p class="crypto-view__plan-total text-sm text-toned">
                {{ t('admin.crypto.totalToSweep') }}:
                <strong class="text-highlighted">{{ sweepPlan.TotalUSD.toFixed(2) }} USDT</strong>
                <UBadge v-if="sweepPlan.DryRun" color="neutral" variant="subtle" class="ms-1">dry-run</UBadge>
              </p>
              <UTable v-if="sweepPlan.Entries?.length" :data="sweepPlan.Entries" :columns="planColumns" :ui="tableUi">
                <template #Address-cell="{ row }"><span class="font-mono">{{ row.original.Address }}</span></template>
                <template #AmountDecimal-cell="{ row }">{{ row.original.AmountDecimal.toFixed(2) }}</template>
              </UTable>
            </section>

            <UTable v-if="sweepRecords.length" :data="sweepRecords" :columns="recordColumns" :ui="tableUi">
              <template #from_address-cell="{ row }"><span class="font-mono">{{ short(row.original.from_address) }}</span></template>
              <template #status-cell="{ row }">
                <UBadge :color="statusColor(row.original.status)" variant="subtle">{{ statusLabel(row.original.status) }}</UBadge>
              </template>
              <template #sweep_tx_hash-cell="{ row }"><span class="font-mono">{{ short(row.original.sweep_tx_hash) }}</span></template>
            </UTable>
            <p v-else-if="!sweepPlan" class="py-10 text-center text-sm text-muted">{{ t('common.noData') }}</p>
          </div>
        </template>

        <!-- Deposits: the reconciliation view -->
        <template #deposits>
          <div id="crypto-panel-deposits" class="crypto-view__panel flex flex-col gap-4">
            <UPageGrid class="lg:grid-cols-5 gap-4 sm:gap-6 lg:gap-px">
              <UPageCard
                v-for="stat in depositStats"
                :key="stat.label"
                :icon="stat.icon"
                :title="t(stat.label)"
                variant="subtle"
                :ui="statCardUi"
                class="lg:rounded-none first:rounded-l-lg last:rounded-r-lg"
              >
                <span class="text-2xl font-semibold text-highlighted">{{ stat.value }}</span>
              </UPageCard>
            </UPageGrid>

            <div class="crypto-view__filters flex flex-wrap items-center justify-between gap-1.5">
              <div class="flex flex-wrap items-center gap-1.5">
                <USelect :model-value="depositChain || ALL" :items="depositChainItems" class="min-w-36" :aria-label="t('admin.crypto.chain')" @update:model-value="setDepositChain" />
                <USelect :model-value="depositStatus || ALL" :items="depositStatusItems" class="min-w-32" :aria-label="t('common.status')" @update:model-value="setDepositStatus" />
              </div>
              <div class="flex flex-wrap items-center gap-1.5">
                <UButton color="neutral" variant="outline" icon="i-lucide-refresh-cw" :disabled="loadingDeposits" @click="loadDeposits">{{ t('common.refresh') }}</UButton>
                <UButton color="neutral" variant="outline" icon="i-lucide-download" :disabled="exporting" @click="downloadCSV">{{ t('admin.crypto.exportCsv') }}</UButton>
                <UDropdownMenu :items="columnMenu('deposit')" :content="{ align: 'end' }">
                  <UButton color="neutral" variant="outline" trailing-icon="i-lucide-settings-2">{{ t('admin.crypto.columns') }}</UButton>
                </UDropdownMenu>
              </div>
            </div>

            <UTable
              v-model:column-visibility="depositVisibility"
              :data="deposits"
              :columns="depositTableColumns"
              :loading="loadingDeposits"
              :empty="t('common.noData')"
              :ui="tableUi"
            >
              <template #from_address-cell="{ row }"><span class="font-mono" :title="row.original.from_address">{{ short(row.original.from_address) }}</span></template>
              <template #to_address-cell="{ row }"><span class="font-mono" :title="row.original.to_address">{{ short(row.original.to_address) }}</span></template>
              <template #token_symbol-cell="{ row }"><UBadge color="neutral" variant="outline">{{ row.original.token_symbol || 'USDT' }}</UBadge></template>
              <template #status-cell="{ row }">
                <UBadge :color="statusColor(row.original.status)" variant="subtle">{{ statusLabel(row.original.status) }}</UBadge>
              </template>
              <template #tx_hash-cell="{ row }">
                <ULink
                  v-if="explorerTxURL(row.original.chain, row.original.tx_hash)"
                  class="font-mono text-primary hover:underline"
                  :to="explorerTxURL(row.original.chain, row.original.tx_hash)"
                  target="_blank"
                  rel="noopener noreferrer"
                >{{ short(row.original.tx_hash) }}</ULink>
                <span v-else class="font-mono">{{ short(row.original.tx_hash) }}</span>
              </template>
              <template #created_at-cell="{ row }"><span class="whitespace-nowrap text-muted">{{ fmtTime(row.original.created_at) }}</span></template>
            </UTable>

            <div class="crypto-view__pager flex flex-wrap items-center justify-between gap-3 border-t border-default pt-4">
              <span class="text-sm text-muted">{{ t('admin.crypto.pageSummary', { page: depositPage, pages: depositTotalPages, total: depositSummary.total }) }}</span>
              <UPagination
                v-if="depositTotalPages > 1"
                :page="depositPage"
                :total="depositSummary.total"
                :items-per-page="depositPageSize"
                @update:page="gotoDepositPage"
              />
            </div>
          </div>
        </template>
      </UTabs>
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
      <form id="crypto-wallet-form" class="space-y-4" autocomplete="off" @submit.prevent="submitWallet">
        <UAlert color="warning" variant="subtle" icon="i-lucide-triangle-alert" :description="t('admin.crypto.hotImportWarning')" />
        <UFormField :label="t('admin.crypto.hotMnemonic')" required>
          <UInput id="crypto-hot-mnemonic" v-model="hotMnemonic" type="password" autocomplete="off" spellcheck="false" required class="w-full" />
        </UFormField>
      </form>
      <template #footer>
        <div class="flex justify-end gap-2">
          <UButton color="neutral" variant="subtle" data-testid="crypto-wallet-cancel" @click="closeWalletDialog">{{ t('common.cancel') }}</UButton>
          <UButton type="submit" form="crypto-wallet-form" color="error" :disabled="!hotMnemonic.trim()">{{ t('admin.crypto.walletContinue') }}</UButton>
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
import AdminPageHeader from '@/components/admin/page/AdminPageHeader.vue'
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
import type { DropdownMenuItem, TableColumn } from '@nuxt/ui'
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

// UTabs (reka) owns roving focus and arrow/Home/End keys; each panel is a named slot.
const tabs = computed(() => [
  { value: 'config', slot: 'config' as const, label: t('admin.crypto.tabConfig') },
  { value: 'addresses', slot: 'addresses' as const, label: t('admin.crypto.tabAddresses') },
  { value: 'sweep', slot: 'sweep' as const, label: t('admin.crypto.tabSweep') },
  { value: 'deposits', slot: 'deposits' as const, label: t('admin.crypto.tabDeposits') }
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

// Table columns in body order; keyed entries sort server-side over the whole set.
const addressColumns: Array<{ id: keyof DepositAddress; key: AddressSortKey | null; label: string }> = [
  { id: 'id', key: 'id', label: 'admin.crypto.colId' },
  { id: 'user_id', key: 'user_id', label: 'admin.crypto.userId' },
  { id: 'family', key: null, label: 'admin.crypto.family' },
  { id: 'address', key: null, label: 'admin.crypto.address' },
  { id: 'credited_total', key: 'credited_total', label: 'admin.crypto.credited' },
  { id: 'swept_total', key: 'swept_total', label: 'admin.crypto.swept' },
  { id: 'deposit_count', key: 'deposit_count', label: 'admin.crypto.statDeposits' }
]
const sortableAddressKeys = addressColumns.flatMap(col => col.key ? [col.key] : [])
const addressColumnLabel = (key: AddressSortKey) => t(addressColumns.find(col => col.key === key)?.label ?? key)
const addressTableColumns = computed<TableColumn<DepositAddress>[]>(() => [
  ...addressColumns.map(col => ({ accessorKey: col.id, header: t(col.label) })),
  { accessorKey: 'status', header: t('common.status') },
  { id: 'actions', header: '', enableHiding: false }
])

// Nuxt UI's Select cannot hold an empty-string item; this sentinel stands for "all" and maps back to ''.
const ALL = '__all__'
const familyItems = computed(() => [
  { label: t('admin.crypto.allFamilies'), value: ALL },
  { label: 'EVM', value: 'evm' },
  { label: 'Solana', value: 'solana' }
])
function setAddressFamily(value: string) {
  addressFamily.value = value === ALL ? '' : value
  applyAddressFilter()
}
const depositChainItems = computed(() => [
  { label: t('admin.crypto.allChains'), value: ALL },
  ...(config.value?.chains ?? []).map(chain => ({ label: chain, value: chain }))
])
const depositStatusItems = computed(() => [
  { label: t('admin.crypto.allStatuses'), value: ALL },
  { label: t('deposit.statusCredited'), value: 'credited' },
  { label: t('deposit.statusPending'), value: 'pending' },
  { label: t('deposit.statusIgnored'), value: 'ignored' }
])
function setDepositChain(value: string) {
  depositChain.value = value === ALL ? '' : value
  applyDepositFilter()
}
function setDepositStatus(value: string) {
  depositStatus.value = value === ALL ? '' : value
  applyDepositFilter()
}

// Template look shared by every table on the page (dashboard template customers.vue).
const tableUi = {
  base: 'border-separate border-spacing-0',
  thead: '[&>tr]:bg-elevated/50 [&>tr]:after:content-none',
  tbody: '[&>tr]:last:[&>td]:border-b-0',
  th: 'py-2 first:rounded-l-lg last:rounded-r-lg border-y border-default first:border-l last:border-r',
  td: 'border-b border-default',
  separator: 'h-0'
}
// Stat cards as in the template's HomeStats.
const statCardUi = {
  container: 'gap-y-1.5',
  wrapper: 'items-start',
  leading: 'p-2.5 rounded-full bg-primary/10 ring ring-inset ring-primary/25',
  title: 'font-normal text-muted text-xs uppercase'
}

// Rarely used columns stay behind the "Columns" menu; the choice persists per table.
function persistedVisibility(key: string, defaults: Record<string, boolean>) {
  let stored: Record<string, boolean> = {}
  try { stored = JSON.parse(localStorage.getItem(key) || '{}') ?? {} } catch { stored = {} }
  const state = ref<Record<string, boolean>>({ ...defaults, ...stored })
  watch(state, value => {
    try { localStorage.setItem(key, JSON.stringify(value)) } catch { /* storage unavailable */ }
  }, { deep: true })
  return state
}
const addressVisibility = persistedVisibility('admin.crypto.addressColumns', { family: false, deposit_count: false })
const depositVisibility = persistedVisibility('admin.crypto.depositColumns', { from_address: false, token_symbol: false })
// Templates unwrap refs, so the table is named here and its visibility ref looked up.
function columnMenu(table: 'address' | 'deposit'): DropdownMenuItem[] {
  const visibility = table === 'address' ? addressVisibility : depositVisibility
  const columns: TableColumn<any>[] = table === 'address' ? addressTableColumns.value : depositTableColumns.value
  return columns.flatMap((col) => {
    const id = (col as { accessorKey?: string }).accessorKey ?? col.id
    if (!id || col.enableHiding === false) return []
    return [{
      label: typeof col.header === 'string' ? col.header : id,
      type: 'checkbox' as const,
      checked: visibility.value[id] !== false,
      onUpdateChecked(checked: boolean) { visibility.value = { ...visibility.value, [id]: !!checked } },
      onSelect(event: Event) { event.preventDefault() }
    }]
  })
}

const blockedColumns = computed<TableColumn<BlockedSweep>[]>(() => [
  { accessorKey: 'chain', header: t('admin.crypto.chain') },
  { accessorKey: 'address', header: t('admin.crypto.address') },
  { accessorKey: 'status', header: t('common.status') },
  { accessorKey: 'age_minutes', header: t('admin.crypto.blockedAge') },
  { id: 'actions', header: '' }
])
const summaryColumns = computed<TableColumn<SweepSummary['entries'][number]>[]>(() => [
  { accessorKey: 'chain', header: t('admin.crypto.chain') },
  { accessorKey: 'address', header: t('admin.crypto.address') },
  { accessorKey: 'status', header: t('common.status') },
  { id: 'tx', header: 'TX' }
])
const planColumns = computed<TableColumn<NonNullable<SweepPlan['Entries']>[number]>[]>(() => [
  { accessorKey: 'Chain', header: t('admin.crypto.chain') },
  { accessorKey: 'Address', header: t('admin.crypto.address') },
  { accessorKey: 'AmountDecimal', header: t('admin.crypto.amount') }
])
const recordColumns = computed<TableColumn<SweepRecord>[]>(() => [
  { accessorKey: 'chain', header: t('admin.crypto.chain') },
  { accessorKey: 'from_address', header: t('admin.crypto.from') },
  { accessorKey: 'amount', header: t('admin.crypto.amount') },
  { accessorKey: 'status', header: t('common.status') },
  { accessorKey: 'sweep_tx_hash', header: 'TX' }
])
const depositTableColumns = computed<TableColumn<DepositRecord>[]>(() => [
  { accessorKey: 'chain', header: t('admin.crypto.chain') },
  { accessorKey: 'from_address', header: t('admin.crypto.from') },
  { accessorKey: 'to_address', header: t('admin.crypto.address') },
  { accessorKey: 'token_symbol', header: t('admin.crypto.token') },
  { accessorKey: 'amount', header: t('admin.crypto.amount') },
  { accessorKey: 'status', header: t('common.status') },
  { accessorKey: 'tx_hash', header: 'TX' },
  { accessorKey: 'created_at', header: t('admin.crypto.colTime') }
])

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
// Checkbox group items; the test id lands on each checkbox button.
const chainItems = depositNetworks.map(network => ({
  value: network.id,
  label: network.label,
  description: network.tokens,
  'data-testid': `crypto-chain-${network.id}`
}))
function setChains(value: unknown[]) {
  if (!config.value) return
  config.value.chains = value.map(String)
  syncRPCRows()
}

// Badge colors keep the old meaning: green = done/active, amber = in flight, red = stopped/rejected.
function statusColor(status: string): 'success' | 'warning' | 'error' | 'neutral' {
  if (['active', 'swept', 'credited'].includes(status)) return 'success'
  if (['pending', 'gas_pending', 'gas_confirmed', 'token_pending', 'sweep_pending', 'manual_review', 'planned'].includes(status)) return 'warning'
  if (['disabled', 'failed', 'ignored', 'reverted'].includes(status)) return 'error'
  return 'neutral'
}

const addressStats = computed(() => [
  { label: 'admin.crypto.statAddresses', icon: 'i-lucide-wallet-cards', value: addressSummary.value.address_count },
  { label: 'admin.crypto.statDeposits', icon: 'i-lucide-inbox', value: addressSummary.value.deposit_count },
  { label: 'admin.crypto.statCredited', icon: 'i-lucide-arrow-down-to-line', value: fmtAmount(addressSummary.value.credited_total) },
  { label: 'admin.crypto.statSwept', icon: 'i-lucide-arrow-up-from-line', value: fmtAmount(addressSummary.value.swept_total) },
  { label: 'admin.crypto.statUnswept', icon: 'i-lucide-hourglass', value: fmtAmount(addressSummary.value.credited_total - addressSummary.value.swept_total) }
])
const depositStats = computed(() => [
  { label: 'admin.crypto.depTotal', icon: 'i-lucide-list-checks', value: depositSummary.value.total },
  { label: 'admin.crypto.depCredited', icon: 'i-lucide-circle-check', value: depositSummary.value.credited },
  { label: 'admin.crypto.depPending', icon: 'i-lucide-hourglass', value: depositSummary.value.pending },
  { label: 'admin.crypto.depIgnored', icon: 'i-lucide-circle-alert', value: depositSummary.value.ignored },
  { label: 'admin.crypto.depAmount', icon: 'i-lucide-coins', value: fmtAmount(depositSummary.value.amount) }
])

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
