<template>
  <AppLayout>
    <div class="flex flex-col gap-6">
      <div v-if="loading" class="flex items-center justify-center py-16">
        <UIcon name="i-lucide-loader-circle" class="size-8 animate-spin text-muted" />
      </div>

      <template v-else>
        <AdminPageHeader :title="t('admin.riskControl.title')" :description="t('admin.riskControl.description')">
          <template #actions>
            <UButton color="neutral" variant="outline" icon="i-lucide-refresh-cw" :loading="statusLoading" :disabled="statusLoading" :label="t('admin.riskControl.refreshStatus')" @click="loadStatus(false)" />
            <UButton icon="i-lucide-settings" :label="t('admin.riskControl.openSettings')" @click="openSettings" />
          </template>
        </AdminPageHeader>

        <div class="flex flex-col gap-2">
          <p class="text-sm text-muted" data-test="active-audit-engine">
            {{ t('admin.riskControl.activeEngine', { engine: engineLabel(status?.engine ?? savedEngine) }) }}
          </p>
          <UAlert
            v-if="status?.enabled && status.risk_control_enabled && status.mode !== 'off' && status.pre_block_api_key_available_count === 0"
            role="status"
            color="warning"
            variant="subtle"
            icon="i-lucide-triangle-alert"
            :title="t('admin.riskControl.engineUnavailable')"
          />
        </div>

        <UPageGrid class="gap-4 sm:gap-6 lg:grid-cols-4 lg:gap-px">
          <UPageCard
            v-for="item in overviewItems"
            :key="item.key"
            :icon="item.icon"
            :title="item.label"
            variant="subtle"
            :ui="{
              container: 'gap-y-1.5',
              wrapper: 'items-start',
              leading: 'p-2.5 rounded-full bg-primary/10 ring ring-inset ring-primary/25',
              title: 'font-normal text-muted text-xs uppercase'
            }"
            class="lg:rounded-none lg:first:rounded-l-lg lg:last:rounded-r-lg"
          >
            <div class="flex min-w-0 flex-wrap items-center gap-2">
              <span class="truncate text-2xl font-semibold text-highlighted">{{ item.value }}</span>
              <UBadge v-if="item.badge" :color="item.badgeColor" variant="subtle" class="text-xs">{{ item.badge }}</UBadge>
            </div>
            <p v-if="item.meta" class="truncate text-xs text-muted">{{ item.meta }}</p>
          </UPageCard>
        </UPageGrid>

        <div
          v-if="showPreBlockRuntimeCard"
          data-test="pre-block-runtime-cards"
          class="grid grid-cols-1 gap-6 xl:grid-cols-[minmax(0,520px)_minmax(0,1fr)]"
        >
          <UCard data-test="pre-block-sync-card" :ui="{ header: 'flex items-start justify-between gap-3' }">
            <template #header>
              <div class="min-w-0">
                <h2 class="font-semibold text-highlighted">{{ t('admin.riskControl.preBlockSyncStatus') }}</h2>
                <p class="mt-1 text-sm text-muted">{{ t('admin.riskControl.preBlockSyncHint') }}</p>
              </div>
              <UBadge color="neutral" variant="subtle" class="shrink-0">{{ modeLabel(status?.mode ?? configForm.mode) }}</UBadge>
            </template>
            <div data-test="pre-block-metric-grid" class="grid grid-cols-2 gap-x-4 gap-y-5 md:grid-cols-3">
              <div v-for="item in preBlockMetricItems" :key="item.key" class="min-w-0">
                <p class="text-xs text-muted">{{ item.label }}</p>
                <p class="mt-1 truncate text-2xl font-semibold" :class="item.valueClass">{{ item.value }}</p>
                <p v-if="item.meta" class="mt-0.5 truncate text-xs text-dimmed">{{ item.meta }}</p>
              </div>
            </div>
          </UCard>

          <UCard data-test="pre-block-api-key-load-card" :ui="{ header: 'flex flex-col gap-3 lg:flex-row lg:items-start lg:justify-between' }">
            <template #header>
              <div class="min-w-0">
                <h2 class="font-semibold text-highlighted">{{ t('admin.riskControl.preBlockAPIKeyLoad') }}</h2>
                <p class="mt-1 text-sm text-muted">{{ t('admin.riskControl.preBlockAPIKeyLoadHint') }}</p>
              </div>
              <p class="shrink-0 text-xs text-muted lg:max-w-xs lg:text-right">{{ preBlockAPIKeyLoadSummaryText }}</p>
            </template>
            <div
              v-if="preBlockAPIKeyLoads.length > 0"
              data-test="pre-block-api-key-load-list"
              class="max-h-[280px] divide-y divide-default overflow-y-auto pr-1"
            >
              <div v-for="item in preBlockAPIKeyLoads" :key="item.key_hash || item.index" class="py-3 first:pt-0 last:pb-0">
                <div class="flex min-w-0 flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
                  <div class="min-w-0">
                    <div class="flex min-w-0 items-center gap-2">
                      <span class="font-mono text-sm font-semibold text-highlighted">#{{ item.index + 1 }}</span>
                      <span class="truncate font-mono text-sm text-default">{{ item.masked || '-' }}</span>
                      <span class="size-2 shrink-0 rounded-full" :class="apiKeyStatusDotClass(item.status)"></span>
                    </div>
                    <p class="mt-1 text-xs text-muted">
                      {{ t('admin.riskControl.preBlockAPIKeyTotals', { total: formatNumber(item.total), success: formatNumber(item.success), errors: formatNumber(item.errors) }) }}
                    </p>
                  </div>
                  <div class="grid grid-cols-4 gap-2 text-right text-xs text-muted sm:min-w-[280px]">
                    <div>
                      <p>{{ t('admin.riskControl.preBlockKeyActiveShort') }}</p>
                      <p class="mt-1 text-sm font-semibold text-info">{{ formatNumber(item.active) }}</p>
                    </div>
                    <div>
                      <p>{{ t('admin.riskControl.preBlockKeyTotalShort') }}</p>
                      <p class="mt-1 text-sm font-semibold text-highlighted">{{ formatNumber(item.total) }}</p>
                    </div>
                    <div>
                      <p>{{ t('admin.riskControl.preBlockKeyAvgShort') }}</p>
                      <p class="mt-1 text-sm font-semibold text-highlighted">{{ formatNumber(item.avg_latency_ms) }} ms</p>
                    </div>
                    <div>
                      <p>{{ t('admin.riskControl.preBlockKeyLastShort') }}</p>
                      <p class="mt-1 text-sm font-semibold text-highlighted">{{ formatNumber(item.last_latency_ms) }} ms</p>
                    </div>
                  </div>
                </div>
                <UProgress :model-value="item.total" :max="preBlockAPIKeyMaxTotal" size="xs" color="info" class="mt-2.5" />
              </div>
            </div>
            <p v-else class="text-sm text-muted">{{ t('admin.riskControl.preBlockAPIKeyLoadEmpty') }}</p>
          </UCard>
        </div>

        <UCard v-if="showWorkerRuntimeCard" :ui="{ header: 'flex flex-col gap-3 lg:flex-row lg:items-start lg:justify-between' }">
          <template #header>
            <div class="min-w-0">
              <h2 class="font-semibold text-highlighted">{{ t('admin.riskControl.workerStatus') }}</h2>
              <p class="mt-1 text-sm text-muted">{{ t('admin.riskControl.workerStatusHint') }}</p>
            </div>
            <div class="flex flex-wrap items-center gap-2 text-xs text-muted">
              <span>{{ t('admin.riskControl.autoRefresh') }}</span>
              <span v-if="status?.last_cleanup_at">{{ t('admin.riskControl.lastCleanup', { time: formatDateTime(status.last_cleanup_at) }) }}</span>
            </div>
          </template>
          <div class="grid grid-cols-1 gap-8 xl:grid-cols-[minmax(0,360px)_1fr]">
            <div class="flex flex-col gap-6">
              <div>
                <div class="flex items-center justify-between gap-3">
                  <div>
                    <p class="text-sm font-medium text-highlighted">{{ t('admin.riskControl.queueUsage') }}</p>
                    <p class="mt-1 text-xs text-muted">{{ formatNumber(status?.queue_length ?? 0) }} / {{ formatNumber(status?.queue_size ?? configForm.queue_size) }}</p>
                  </div>
                  <span class="text-sm font-semibold text-highlighted">{{ queueUsagePercent }}</span>
                </div>
                <UProgress :model-value="queueUsageValue" :max="100" size="sm" class="mt-3" />
              </div>
              <div class="grid grid-cols-2 gap-x-4 gap-y-5">
                <div>
                  <p class="text-xs text-muted">{{ t('admin.riskControl.activeWorkers') }}</p>
                  <p class="mt-1 text-2xl font-semibold text-highlighted">{{ status?.active_workers ?? 0 }}</p>
                </div>
                <div>
                  <p class="text-xs text-muted">{{ t('admin.riskControl.idleWorkers') }}</p>
                  <p class="mt-1 text-2xl font-semibold text-success">{{ status?.idle_workers ?? configForm.worker_count }}</p>
                </div>
                <div>
                  <p class="text-xs text-muted">{{ t('admin.riskControl.processed') }}</p>
                  <p class="mt-1 text-2xl font-semibold text-highlighted">{{ formatNumber(status?.processed ?? 0) }}</p>
                </div>
                <div>
                  <p class="text-xs text-muted">{{ t('admin.riskControl.droppedErrors') }}</p>
                  <p class="mt-1 text-2xl font-semibold text-highlighted">{{ formatNumber((status?.dropped ?? 0) + (status?.errors ?? 0)) }}</p>
                </div>
              </div>
            </div>
            <div>
              <div class="mb-3 flex items-center justify-between gap-3">
                <div>
                  <p class="text-sm font-medium text-highlighted">{{ t('admin.riskControl.workerPool') }}</p>
                  <p class="mt-1 text-xs text-muted">
                    {{ t('admin.riskControl.workerPoolMeta', { active: status?.active_workers ?? 0, idle: status?.idle_workers ?? configForm.worker_count, total: status?.worker_count ?? configForm.worker_count }) }}
                  </p>
                </div>
                <UBadge color="neutral" variant="subtle">{{ modeLabel(status?.mode ?? configForm.mode) }}</UBadge>
              </div>
              <div class="grid grid-cols-2 gap-2 sm:grid-cols-4 md:grid-cols-6 xl:grid-cols-8 2xl:grid-cols-10">
                <div
                  v-for="worker in workerSlots"
                  :key="worker.id"
                  class="flex h-10 items-center justify-between rounded-md px-3 ring ring-inset"
                  :class="workerSlotClass(worker.state)"
                  :title="worker.label"
                >
                  <span class="text-sm font-medium">#{{ worker.id }}</span>
                  <span class="size-2 rounded-full" :class="workerDotClass(worker.state)"></span>
                </div>
              </div>
            </div>
          </div>
        </UCard>

        <section class="flex flex-col gap-4">
          <div class="flex flex-col gap-1">
            <h2 class="font-semibold text-highlighted">{{ t('admin.riskControl.records') }}</h2>
            <p class="text-sm text-muted">{{ t('admin.riskControl.recordsHint') }}</p>
          </div>

          <div class="flex flex-wrap items-center justify-between gap-1.5">
            <UInput
              v-model.trim="filters.search"
              type="search"
              icon="i-lucide-search"
              class="w-full sm:w-60"
              :placeholder="t('admin.riskControl.filters.search')"
              @keyup.enter="reloadLogsFromFirstPage"
            />
            <div class="flex flex-wrap items-center gap-1.5 sm:justify-end">
              <Select v-model="filters.result" class="w-full sm:w-32" :options="resultOptions" @change="reloadLogsFromFirstPage" />
              <Select v-model="filters.group_id" class="w-full sm:w-36" :options="groupFilterOptions" @change="reloadLogsFromFirstPage" />
              <Select v-model="filters.endpoint" class="w-full sm:w-40" :options="endpointOptions" @change="reloadLogsFromFirstPage" />
              <UInput v-model="filters.from" type="datetime-local" class="w-full sm:w-auto" :title="t('admin.riskControl.filters.from')" :aria-label="t('admin.riskControl.filters.from')" @change="reloadLogsFromFirstPage" />
              <UInput v-model="filters.to" type="datetime-local" class="w-full sm:w-auto" :title="t('admin.riskControl.filters.to')" :aria-label="t('admin.riskControl.filters.to')" @change="reloadLogsFromFirstPage" />
              <UDropdownMenu :items="logColumnMenuItems" :content="{ align: 'end' }">
                <UButton color="neutral" variant="outline" trailing-icon="i-lucide-settings-2" :label="t('admin.accounts.viewColumns')" />
              </UDropdownMenu>
              <UButton
                color="neutral"
                variant="outline"
                icon="i-lucide-refresh-cw"
                :loading="logsLoading"
                :disabled="logsLoading"
                :aria-label="t('admin.riskControl.refresh')"
                :title="t('admin.riskControl.refresh')"
                @click="loadLogs"
              />
            </div>
          </div>

          <div class="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1.5 text-sm">
            <UIcon name="i-lucide-list-filter" class="size-4 shrink-0 text-dimmed" />
            <span class="font-medium text-default">{{ t('admin.riskControl.modelFilter') }}</span>
            <span class="truncate text-muted">{{ modelFilterSummary }}</span>
            <UBadge v-for="model in modelFilterPreviewModels" :key="model" color="neutral" variant="subtle" class="max-w-[180px] font-mono">{{ model }}</UBadge>
            <UBadge v-if="hiddenModelFilterModelCount > 0" color="neutral" variant="subtle">+{{ hiddenModelFilterModelCount }}</UBadge>
          </div>

          <UTable
            v-model:column-visibility="logColumnVisibility"
            :data="tableLogs"
            :columns="logColumns"
            :loading="logsLoading"
            :ui="{
              base: 'border-separate border-spacing-0',
              thead: '[&>tr]:bg-elevated/50 [&>tr]:after:content-none',
              tbody: '[&>tr]:last:[&>td]:border-b-0',
              th: 'py-2 whitespace-nowrap first:rounded-l-lg last:rounded-r-lg border-y border-default first:border-l last:border-r',
              td: 'border-b border-default align-top',
              separator: 'h-0'
            }"
          >
            <template #loading>
              <p class="py-8 text-center text-sm text-muted">{{ t('common.loading') }}</p>
            </template>
            <template #empty>
              <p class="py-8 text-center text-sm text-muted">{{ t('admin.riskControl.emptyLogs') }}</p>
            </template>
            <template #time-cell="{ row }">
              <span class="whitespace-nowrap">{{ formatDateTime(row.original.created_at) }}</span>
            </template>
            <template #group-cell="{ row }">
              <span class="whitespace-nowrap">{{ row.original.group_name || '-' }}</span>
            </template>
            <template #user-cell="{ row }">
              <div class="whitespace-nowrap">
                <p class="text-highlighted">{{ row.original.user_email || '-' }}</p>
                <p v-if="row.original.user_id" class="text-xs text-dimmed">UID {{ row.original.user_id }}</p>
              </div>
            </template>
            <template #api_key-cell="{ row }">
              <span class="whitespace-nowrap">{{ row.original.api_key_name || '-' }}</span>
            </template>
            <template #endpoint-cell="{ row }">
              <div class="min-w-40 break-all">
                <p>{{ row.original.endpoint || '-' }}</p>
                <p class="text-xs text-dimmed">{{ row.original.provider || '-' }} / {{ row.original.model || '-' }}</p>
              </div>
            </template>
            <template #result-cell="{ row }">
              <UBadge :color="resultBadgeColor(row.original)" variant="subtle">{{ resultLabel(row.original) }}</UBadge>
            </template>
            <template #highest-cell="{ row }">
              <div class="whitespace-nowrap">
                <p>{{ row.original.highest_category || '-' }}</p>
                <p class="text-xs text-dimmed">{{ percent(row.original.highest_score) }}</p>
                <p v-if="row.original.matched_keyword" class="mt-0.5 text-xs font-medium text-error" :title="t('admin.riskControl.matchedKeyword') + ': ' + row.original.matched_keyword">
                  {{ t('admin.riskControl.matchedKeyword') }}: {{ row.original.matched_keyword }}
                </p>
              </div>
            </template>
            <template #action-cell="{ row }">
              <div class="whitespace-nowrap">
                <p>{{ violationCountText(row.original) }}</p>
                <p class="text-xs text-dimmed">
                  {{ row.original.email_sent ? t('admin.riskControl.emailSent') : t('admin.riskControl.emailNotSent') }}
                  <span v-if="row.original.auto_banned"> / {{ t('admin.riskControl.autoBanned') }}</span>
                </p>
                <UButton
                  v-if="canUnbanRow(row.original)"
                  size="xs"
                  color="success"
                  variant="soft"
                  icon="i-lucide-circle-check"
                  class="mt-1.5"
                  :loading="unbanningUserID === row.original.user_id"
                  :disabled="unbanningUserID === row.original.user_id"
                  :label="unbanningUserID === row.original.user_id ? t('common.processing') : t('admin.riskControl.unbanUser')"
                  @click="unbanUser(row.original)"
                />
              </div>
            </template>
            <template #latency-cell="{ row }">
              <div class="whitespace-nowrap">
                <p>{{ latencyText(row.original.upstream_latency_ms) }}</p>
                <p v-if="row.original.queue_delay_ms !== null && row.original.queue_delay_ms !== undefined" class="text-xs text-dimmed">
                  {{ t('admin.riskControl.queueDelay', { ms: row.original.queue_delay_ms }) }}
                </p>
              </div>
            </template>
            <template #input-cell="{ row }">
              <UButton
                color="neutral"
                variant="ghost"
                trailing-icon="i-lucide-eye"
                class="w-48 max-w-48 min-w-0 justify-between font-normal"
                :ui="{ label: 'truncate', trailingIcon: 'text-dimmed' }"
                :title="inputSummaryText(row.original)"
                :label="inputSummaryText(row.original)"
                @click="openInputDetail(row.original)"
              />
            </template>
          </UTable>

          <div v-if="pagination.total > 0" class="border-t border-default pt-4">
            <Pagination
              :page="pagination.page"
              :total="pagination.total"
              :page-size="pagination.page_size"
              @update:page="onPageChange"
              @update:pageSize="onPageSizeChange"
            />
          </div>
        </section>
      </template>

      <BaseDialog :show="settingsOpen" :title="t('admin.riskControl.settingsTitle')" width="extra-wide" @close="settingsOpen = false">
        <div class="flex flex-col gap-6">
          <UTabs
            :model-value="activeSettingsTab"
            :items="settingsTabItems"
            :content="false"
            variant="link"
            class="w-full overflow-x-auto"
            :ui="{ list: 'w-max min-w-full justify-start', trigger: 'grow-0 whitespace-nowrap' }"
            @update:model-value="selectSettingsTab"
          />

          <div v-if="activeSettingsTab === 'basic'" class="flex flex-col gap-6">
            <UPageCard variant="subtle">
              <UFormField :label="t('admin.riskControl.enabled')" :description="t('admin.riskControl.enabledHint')" :class="switchRowClass">
                <Toggle v-model="configForm.enabled" />
              </UFormField>
              <USeparator />
              <UFormField :label="t('admin.riskControl.mode')" :description="modeDescription(configForm.mode)" :class="fieldRowClass" :ui="fieldControlUi">
                <Select v-model="configForm.mode" :options="modeOptions" />
              </UFormField>
              <USeparator />
              <UFormField :label="t('admin.riskControl.engine')" :class="fieldRowClass" :ui="fieldControlUi">
                <Select data-test="audit-engine-select" :model-value="configForm.engine" :options="engineOptions" :disabled="apiKeyTesting || saving" @update:model-value="switchEngine" />
              </UFormField>
              <UAlert
                v-if="configForm.engine === 'typesafe'"
                role="status"
                color="warning"
                variant="subtle"
                icon="i-lucide-triangle-alert"
                :description="t('admin.riskControl.typeSafeNotice')"
              />
              <USeparator />
              <UFormField :label="t('admin.riskControl.baseUrl')" :class="fieldRowClass" :ui="fieldWideUi">
                <UInput v-model.trim="configForm.base_url" data-test="audit-base-url" type="url" class="w-full" :placeholder="configForm.engine === 'typesafe' ? 'https://api.typesafe.ai' : 'https://api.openai.com'" />
              </UFormField>
              <USeparator />
              <UFormField :label="t('admin.riskControl.model')" :class="fieldRowClass" :ui="fieldWideUi">
                <UInput v-model.trim="configForm.model" data-test="audit-model" type="text" class="w-full" :placeholder="configForm.engine === 'typesafe' ? 'jev-latest' : 'omni-moderation-latest'" />
              </UFormField>
              <USeparator />
              <UFormField :label="t('admin.riskControl.timeoutMs')" :class="fieldRowClass" :ui="fieldControlUi">
                <UInput v-model.number="configForm.timeout_ms" type="number" min="500" max="30000" class="w-full" />
              </UFormField>
              <USeparator />
              <UFormField :label="t('admin.riskControl.retryCount')" :class="fieldRowClass" :ui="fieldControlUi">
                <UInput v-model.number="configForm.retry_count" type="number" min="0" max="5" class="w-full" />
              </UFormField>
              <USeparator />
              <UFormField :label="t('admin.riskControl.sampleRate')" :class="fieldRowClass" :ui="fieldControlUi">
                <UInput v-model.number="configForm.sample_rate" type="number" min="0" max="100" step="1" class="w-full" :ui="{ trailing: 'pointer-events-none' }">
                  <template #trailing><span class="text-sm text-dimmed">%</span></template>
                </UInput>
              </UFormField>
              <USeparator />
              <UFormField :label="t('admin.riskControl.proxy')" :description="t('admin.riskControl.proxyHint')" :class="fieldRowClass" :ui="fieldControlUi">
                <ProxySelector v-model="configForm.proxy_id" :proxies="proxies" />
              </UFormField>
            </UPageCard>

            <UPageCard variant="subtle">
              <div class="flex flex-col gap-4 lg:flex-row lg:items-start lg:justify-between">
                <div class="flex items-start gap-3">
                  <UIcon name="i-lucide-key-round" class="mt-0.5 size-5 shrink-0 text-muted" />
                  <div>
                    <p class="text-sm font-medium text-highlighted">{{ t('admin.riskControl.apiKeys') }}</p>
                    <p class="mt-1 max-w-3xl text-sm text-muted">{{ t('admin.riskControl.apiKeysHint', { count: configForm.api_key_count }) }}</p>
                  </div>
                </div>
                <div class="flex flex-wrap items-center gap-2">
                  <UButton
                    color="neutral"
                    variant="outline"
                    icon="i-lucide-flask-conical"
                    :disabled="apiKeyTesting || inputApiKeyCount === 0 || configForm.clear_api_key"
                    :label="apiKeyTesting ? t('admin.riskControl.testingApiKeys') : t('admin.riskControl.testInputApiKeys')"
                    @click="testApiKeys(true)"
                  />
                  <UButton
                    color="neutral"
                    variant="outline"
                    icon="i-lucide-shield-check"
                    :disabled="apiKeyTesting || effectiveStoredApiKeyCount === 0 || pendingDeletedApiKeyCount > 0 || configForm.clear_api_key || configForm.api_keys_mode === 'replace'"
                    :label="storedApiKeyTestButtonText"
                    @click="testApiKeys(false)"
                  />
                  <UButton
                    v-if="configForm.api_key_configured"
                    :color="configForm.clear_api_key ? 'neutral' : 'error'"
                    :variant="configForm.clear_api_key ? 'outline' : 'soft'"
                    :icon="configForm.clear_api_key ? 'i-lucide-x' : 'i-lucide-trash-2'"
                    :label="configForm.clear_api_key ? t('admin.riskControl.keepApiKey') : t('admin.riskControl.clearApiKey')"
                    @click="toggleClearApiKey"
                  />
                </div>
              </div>

              <USeparator />

              <div class="grid grid-cols-1 gap-8 xl:grid-cols-[minmax(0,1fr)_minmax(360px,440px)]">
                <div class="flex flex-col gap-4">
                  <UFormField :label="t('admin.riskControl.apiKeysWriteMode')" :description="apiKeysModeHint">
                    <URadioGroup
                      :model-value="configForm.api_keys_mode"
                      :items="apiKeysModeItems"
                      orientation="horizontal"
                      :disabled="configForm.clear_api_key"
                      class="mt-1"
                      @update:model-value="onAPIKeysModeChange"
                    />
                  </UFormField>
                  <UTextarea
                    v-model="configForm.api_keys_text"
                    :rows="7"
                    class="w-full"
                    :ui="{ base: 'font-mono text-sm resize-y min-h-44' }"
                    :placeholder="apiKeysPlaceholder"
                    autocomplete="new-password"
                    :disabled="configForm.clear_api_key"
                  />
                  <div class="flex flex-wrap items-center gap-2">
                    <UBadge color="neutral" variant="subtle">{{ t('admin.riskControl.inputApiKeyCount', { count: inputApiKeyCount }) }}</UBadge>
                    <UBadge v-if="configForm.api_key_configured" color="neutral" variant="subtle">{{ t('admin.riskControl.storedApiKeyCount', { count: configForm.api_key_count }) }}</UBadge>
                    <UBadge v-if="configForm.clear_api_key" color="error" variant="subtle">{{ t('admin.riskControl.apiKeyWillClear') }}</UBadge>
                    <UBadge v-else-if="pendingDeletedApiKeyCount > 0" color="warning" variant="subtle">{{ t('admin.riskControl.apiKeyPendingDeleteCount', { count: pendingDeletedApiKeyCount }) }}</UBadge>
                  </div>
                  <p v-if="configForm.api_keys_mode === 'replace'" class="text-xs text-warning">{{ t('admin.riskControl.apiKeysReplaceWarning') }}</p>

                  <USeparator />

                  <div class="flex flex-col gap-3" @paste="handleModerationImagePaste">
                    <div class="flex items-start justify-between gap-3">
                      <div>
                        <p class="text-sm font-medium text-highlighted">{{ t('admin.riskControl.auditTestInput') }}</p>
                        <p class="mt-1 text-sm text-muted">{{ t('admin.riskControl.auditTestInputHint') }}</p>
                      </div>
                      <UButton
                        v-if="moderationTestPrompt || moderationTestImages.length > 0 || moderationTestResult"
                        size="xs"
                        color="neutral"
                        variant="ghost"
                        icon="i-lucide-x"
                        :label="t('admin.riskControl.clearAuditTest')"
                        @click="clearModerationTestInput"
                      />
                    </div>
                    <UTextarea
                      v-model="moderationTestPrompt"
                      :rows="4"
                      class="w-full"
                      :ui="{ base: 'resize-y' }"
                      :placeholder="t('admin.riskControl.auditTestPromptPlaceholder')"
                    />
                    <UFileUpload
                      :model-value="noFiles"
                      multiple
                      reset
                      accept="image/*"
                      :preview="false"
                      :interactive="false"
                      icon="i-lucide-upload"
                      :label="t('admin.riskControl.auditTestImages')"
                      :description="t('admin.riskControl.auditTestImagesHint')"
                      class="w-full"
                      :ui="{ base: 'min-h-32 p-4' }"
                      @update:model-value="handleModerationImageFiles"
                    >
                      <template #actions="{ open }">
                        <UButton color="neutral" variant="outline" size="sm" icon="i-lucide-plus" :label="t('admin.riskControl.addAuditTestImage')" @click="open()" />
                      </template>
                    </UFileUpload>
                    <div v-if="moderationTestImages.length > 0" class="grid grid-cols-2 gap-2 sm:grid-cols-4">
                      <div
                        v-for="(image, index) in moderationTestImages"
                        :key="image.slice(0, 64) + index"
                        class="relative aspect-square overflow-hidden rounded-md bg-elevated ring ring-inset ring-default"
                      >
                        <img :src="image" alt="" class="size-full object-cover" />
                        <UButton
                          size="xs"
                          color="neutral"
                          variant="solid"
                          icon="i-lucide-x"
                          class="absolute right-1.5 top-1.5 rounded-full"
                          :aria-label="t('common.remove')"
                          :title="t('common.remove')"
                          @click="removeModerationTestImage(index)"
                        />
                      </div>
                    </div>
                  </div>
                </div>

                <div data-test="audit-key-statuses" class="flex flex-col gap-3">
                  <div class="flex items-start justify-between gap-3">
                    <div class="min-w-0">
                      <p class="text-sm font-medium text-highlighted">{{ t('admin.riskControl.apiKeyHealth') }}</p>
                      <p class="mt-1 text-sm text-muted">{{ t('admin.riskControl.apiKeyFreezeRule') }}</p>
                    </div>
                    <UBadge color="neutral" variant="subtle" class="shrink-0">{{ t('admin.riskControl.apiKeyRows', { count: apiKeyRows.length }) }}</UBadge>
                  </div>

                  <UEmpty
                    v-if="apiKeyRows.length === 0"
                    variant="naked"
                    size="sm"
                    icon="i-lucide-info"
                    :title="t('admin.riskControl.apiKeyHealthEmpty')"
                    :description="t('admin.riskControl.apiKeyHealthEmptyHint')"
                  />
                  <div v-else class="flex flex-col gap-2">
                    <div class="divide-y divide-default" :class="apiKeyRowsExpanded ? 'max-h-72 overflow-y-auto pr-1' : ''">
                      <div
                        v-for="(row, index) in visibleApiKeyRows"
                        :key="apiKeyRowKey(row, index)"
                        class="py-2.5 first:pt-0"
                        :class="isStoredApiKeyPendingDelete(row) ? 'opacity-60' : ''"
                      >
                        <div class="flex items-start justify-between gap-2">
                          <div class="min-w-0">
                            <div class="flex min-w-0 flex-wrap items-center gap-2">
                              <span class="truncate font-mono text-sm font-medium text-highlighted">{{ row.masked || '-' }}</span>
                              <UBadge size="sm" variant="subtle" :color="isStoredApiKeyPendingDelete(row) ? 'warning' : row.configured ? 'neutral' : 'info'">
                                {{ isStoredApiKeyPendingDelete(row) ? t('admin.riskControl.apiKeyPendingDelete') : row.configured ? t('admin.riskControl.apiKeyConfigured') : t('admin.riskControl.apiKeyTemporary') }}
                              </UBadge>
                            </div>
                            <p class="mt-1 text-xs text-muted">{{ apiKeyStatusMeta(row) }}</p>
                          </div>
                          <div class="flex shrink-0 items-center gap-1">
                            <UBadge size="sm" variant="subtle" :color="apiKeyStatusColor(row.status)">{{ apiKeyStatusLabel(row.status) }}</UBadge>
                            <UButton
                              v-if="row.configured && !configForm.clear_api_key"
                              size="xs"
                              color="neutral"
                              variant="ghost"
                              :icon="isStoredApiKeyPendingDelete(row) ? 'i-lucide-undo-2' : 'i-lucide-trash-2'"
                              :aria-label="isStoredApiKeyPendingDelete(row) ? t('admin.riskControl.undoDeleteApiKey') : t('admin.riskControl.deleteApiKey')"
                              :title="isStoredApiKeyPendingDelete(row) ? t('admin.riskControl.undoDeleteApiKey') : t('admin.riskControl.deleteApiKey')"
                              @click="toggleDeleteStoredApiKey(row)"
                            />
                          </div>
                        </div>
                        <p v-if="row.last_error" class="mt-1.5 text-xs text-warning">{{ row.last_error }}</p>
                      </div>
                    </div>
                    <div v-if="canToggleApiKeyRows" class="flex items-center justify-between gap-3 text-xs text-muted">
                      <span class="min-w-0 truncate">
                        {{ apiKeyRowsExpanded ? t('admin.riskControl.apiKeyRowsExpanded', { count: apiKeyRows.length }) : t('admin.riskControl.apiKeyRowsCollapsed', { count: hiddenApiKeyRowCount }) }}
                      </span>
                      <UButton
                        size="xs"
                        color="neutral"
                        variant="link"
                        :icon="apiKeyRowsExpanded ? 'i-lucide-chevron-up' : 'i-lucide-chevron-down'"
                        :label="apiKeyRowsExpanded ? t('admin.riskControl.collapseApiKeyRows') : t('admin.riskControl.expandApiKeyRows')"
                        @click="apiKeyRowsExpanded = !apiKeyRowsExpanded"
                      />
                    </div>
                  </div>

                  <template v-if="moderationTestResult">
                    <USeparator />
                    <div class="flex flex-col gap-3">
                      <p v-if="moderationTestResult.engine_meta" class="wrap-break-word text-xs text-muted">
                        {{ engineLabel(moderationTestResult.engine_meta.engine) }} · {{ moderationTestResult.engine_meta.model }} · {{ moderationTestResult.engine_meta.rules_version }}
                        <span v-if="moderationTestResult.engine_meta.skipped_images"> · {{ t('admin.riskControl.skippedImages', { count: moderationTestResult.engine_meta.skipped_images }) }}</span>
                      </p>
                      <div class="flex items-start justify-between gap-3">
                        <div>
                          <p class="text-sm font-medium text-highlighted">{{ t('admin.riskControl.auditTestResult') }}</p>
                          <p class="mt-1 text-xs text-muted">
                            {{ t('admin.riskControl.auditTestHighest', { category: moderationTestResult.highest_category || '-', score: percent(moderationTestResult.highest_score) }) }}
                          </p>
                        </div>
                        <UBadge variant="subtle" :color="moderationTestResult.flagged ? 'error' : 'success'">
                          {{ moderationTestResult.flagged ? t('admin.riskControl.auditTestFlagged') : t('admin.riskControl.auditTestPassed') }}
                        </UBadge>
                      </div>
                      <div>
                        <div class="mb-1.5 flex items-center justify-between text-xs text-muted">
                          <span>{{ t('admin.riskControl.auditTestComposite') }}</span>
                          <span class="font-semibold text-highlighted">{{ percent(moderationTestResult.composite_score) }}</span>
                        </div>
                        <UProgress :model-value="percentValue(moderationTestResult.composite_score)" :max="100" size="sm" :color="moderationTestResult.flagged ? 'error' : 'success'" />
                      </div>
                      <div class="max-h-52 space-y-2.5 overflow-y-auto pr-1">
                        <div v-for="score in moderationScoreRows" :key="score.category">
                          <div class="mb-1 flex items-center justify-between gap-3 text-xs">
                            <span class="truncate text-default">{{ score.category }}</span>
                            <span class="font-mono text-muted">{{ percent(score.score) }} / {{ percent(score.threshold) }}</span>
                          </div>
                          <UProgress :model-value="percentValue(score.score)" :max="100" size="xs" :color="score.hit ? 'error' : 'primary'" />
                        </div>
                      </div>
                    </div>
                  </template>
                </div>
              </div>
            </UPageCard>
          </div>

          <div v-else-if="activeSettingsTab === 'scope'" class="flex flex-col gap-6">
            <UPageCard variant="subtle">
              <UFormField :label="t('admin.riskControl.groupScope')" :description="t('admin.riskControl.groupScopeHint')" :class="fieldRowClass">
                <URadioGroup
                  :model-value="configForm.all_groups ? 'all' : 'selected'"
                  :items="groupScopeItems"
                  orientation="horizontal"
                  @update:model-value="onGroupScopeChange"
                />
              </UFormField>
              <template v-if="!configForm.all_groups">
                <USeparator />
                <UInput v-model.trim="groupSearch" type="search" icon="i-lucide-search" class="w-full sm:max-w-sm" :placeholder="t('admin.riskControl.searchGroups')" />
                <div class="grid max-h-[420px] grid-cols-1 gap-2 overflow-y-auto pr-1 md:grid-cols-2 xl:grid-cols-3">
                  <UCheckbox
                    v-for="group in filteredGroups"
                    :key="group.id"
                    variant="card"
                    :model-value="isGroupSelected(group.id)"
                    :label="group.name"
                    :description="String(group.platform)"
                    @update:model-value="toggleGroup(group.id)"
                  />
                  <p v-if="filteredGroups.length === 0" class="text-sm text-muted">{{ t('admin.riskControl.noGroups') }}</p>
                </div>
              </template>
            </UPageCard>

            <UPageCard variant="subtle">
              <div class="flex flex-col gap-2 lg:flex-row lg:items-start lg:justify-between">
                <div>
                  <p class="text-sm font-medium text-highlighted">{{ t('admin.riskControl.modelFilter') }}</p>
                  <p class="mt-1 text-sm text-muted">{{ t('admin.riskControl.modelFilterHint') }}</p>
                </div>
                <UBadge color="neutral" variant="subtle" class="w-fit">{{ modelFilterSummary }}</UBadge>
              </div>
              <URadioGroup
                data-test="model-filter-type"
                :model-value="configForm.model_filter_type"
                :items="modelFilterOptions"
                variant="card"
                orientation="horizontal"
                :ui="{ fieldset: 'grid grid-cols-1 gap-2 md:grid-cols-3' }"
                @update:model-value="onModelFilterTypeChange"
              />
              <template v-if="configForm.model_filter_type !== 'all'">
                <USeparator />
                <UFormField :label="t('admin.riskControl.modelFilterModels')" :help="t('admin.riskControl.modelFilterModelCount', { count: modelFilterModelCount })">
                  <ModelWhitelistSelector v-model="configForm.model_filter_models" />
                </UFormField>
              </template>
            </UPageCard>
          </div>

          <div v-else-if="activeSettingsTab === 'runtime'" class="flex flex-col gap-6">
            <UPageCard variant="subtle">
              <UFormField :label="t('admin.riskControl.workerCount')" :class="fieldRowClass" :ui="fieldControlUi">
                <UInput v-model.number="configForm.worker_count" type="number" min="1" max="32" class="w-full" />
              </UFormField>
              <USeparator />
              <UFormField :label="t('admin.riskControl.queueSize')" :class="fieldRowClass" :ui="fieldControlUi">
                <UInput v-model.number="configForm.queue_size" type="number" min="100" max="100000" class="w-full" />
              </UFormField>
              <USeparator />
              <UFormField :label="t('admin.riskControl.recordNonHits')" :description="t('admin.riskControl.recordNonHitsHint')" :class="switchRowClass">
                <Toggle v-model="configForm.record_non_hits" />
              </UFormField>
              <USeparator />
              <UFormField :label="t('admin.riskControl.preHashCheck')" :description="t('admin.riskControl.preHashCheckHint')" :class="switchRowClass">
                <Toggle v-model="configForm.pre_hash_check_enabled" />
              </UFormField>
            </UPageCard>

            <UPageCard variant="subtle">
              <div class="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
                <div>
                  <p class="text-sm font-medium text-highlighted">{{ t('admin.riskControl.flaggedHashCount', { count: formatNumber(status?.flagged_hash_count ?? 0) }) }}</p>
                  <p class="mt-1 text-sm text-muted">{{ t('admin.riskControl.flaggedHashHint') }}</p>
                </div>
                <UButton
                  color="error"
                  variant="soft"
                  icon="i-lucide-trash-2"
                  class="w-fit shrink-0"
                  :loading="hashActionLoading"
                  :disabled="hashActionLoading || (status?.flagged_hash_count ?? 0) === 0"
                  :label="t('admin.riskControl.clearFlaggedHashes')"
                  @click="clearFlaggedHashes"
                />
              </div>
              <div class="flex flex-col gap-2 sm:flex-row">
                <UInput
                  v-model.trim="flaggedHashInput"
                  type="text"
                  class="w-full"
                  :ui="{ base: 'font-mono' }"
                  :placeholder="t('admin.riskControl.flaggedHashPlaceholder')"
                />
                <UButton
                  color="neutral"
                  variant="outline"
                  icon="i-lucide-trash-2"
                  class="shrink-0 justify-center"
                  :disabled="hashActionLoading || !isFlaggedHashInputValid"
                  :label="t('admin.riskControl.deleteFlaggedHash')"
                  @click="deleteFlaggedHash"
                />
              </div>
            </UPageCard>
          </div>

          <div v-else-if="activeSettingsTab === 'response'" class="flex flex-col gap-6">
            <UPageCard variant="subtle">
              <UFormField :label="t('admin.riskControl.blockStatus')" :class="fieldRowClass" :ui="fieldControlUi">
                <UInput v-model.number="configForm.block_status" type="number" min="400" max="599" class="w-full" />
              </UFormField>
              <USeparator />
              <UFormField :label="t('admin.riskControl.blockMessage')" :class="fieldRowClass" :ui="fieldWideUi">
                <UInput v-model.trim="configForm.block_message" type="text" class="w-full" />
              </UFormField>
              <USeparator />
              <UFormField :label="t('admin.riskControl.emailOnHit')" :description="t('admin.riskControl.emailOnHitHint')" :class="switchRowClass">
                <Toggle v-model="configForm.email_on_hit" />
              </UFormField>
              <USeparator />
              <UFormField :label="t('admin.riskControl.autoBan')" :description="t('admin.riskControl.autoBanHint')" :class="switchRowClass">
                <Toggle v-model="configForm.auto_ban_enabled" />
              </UFormField>
              <USeparator />
              <UFormField :label="t('admin.riskControl.cyberPolicyExcludeBan')" :description="t('admin.riskControl.cyberPolicyExcludeBanHint')" :class="switchRowClass">
                <Toggle v-model="configForm.cyber_policy_exclude_from_ban_count" />
              </UFormField>
              <USeparator />
              <UFormField :label="t('admin.riskControl.banThreshold')" :class="fieldRowClass" :ui="fieldControlUi">
                <UInput v-model.number="configForm.ban_threshold" type="number" min="1" max="1000" class="w-full" />
              </UFormField>
              <USeparator />
              <UFormField :label="t('admin.riskControl.violationWindowHours')" :class="fieldRowClass" :ui="fieldControlUi">
                <UInput v-model.number="configForm.violation_window_hours" type="number" min="1" max="8760" class="w-full" />
              </UFormField>
            </UPageCard>
          </div>

          <div v-else-if="activeSettingsTab === 'riskThresholds'" class="flex flex-col gap-4">
            <UPageCard :title="t('admin.riskControl.riskThresholds')" :description="t('admin.riskControl.riskThresholdsHint')" variant="naked" orientation="horizontal">
              <div class="flex flex-wrap items-center gap-2 lg:justify-end">
                <UBadge color="neutral" variant="subtle">{{ engineLabel(configForm.engine) }}</UBadge>
                <UButton color="neutral" variant="outline" icon="i-lucide-refresh-cw" :label="t('admin.riskControl.riskThresholdReset')" @click="resetRiskThresholds" />
              </div>
            </UPageCard>
            <UAlert
              v-if="configForm.engine === 'typesafe'"
              color="warning"
              variant="subtle"
              icon="i-lucide-triangle-alert"
              :description="t('admin.riskControl.typeSafeThresholds')"
            />
            <UPageCard variant="subtle" :ui="{ container: 'divide-y divide-default gap-0 sm:gap-0 py-2 sm:py-2' }">
              <div
                v-for="row in riskThresholdRows"
                :key="row.category"
                class="flex flex-col gap-3 py-3 sm:flex-row sm:items-center sm:justify-between"
              >
                <div class="min-w-0">
                  <label class="block truncate text-sm font-medium text-highlighted" :for="`risk-threshold-${row.category}`">{{ row.category }}</label>
                  <p class="mt-0.5 text-xs text-muted">{{ t('admin.riskControl.riskThresholdDefault', { value: formatThresholdPercent(row.defaultValue) }) }}</p>
                  <p v-if="optionalTypeSafeCategories.has(row.category)" class="mt-0.5 text-xs text-warning">{{ t('moderationCategories.optInHint') }}</p>
                </div>
                <div class="flex shrink-0 items-center gap-4">
                  <UCheckbox
                    v-if="configForm.engine === 'typesafe'"
                    :model-value="configForm.category_settings[row.category]?.enabled ?? false"
                    :label="t('moderationCategories.enabled')"
                    :data-test="`risk-category-enabled-${row.category}`"
                    @update:model-value="setCategoryEnabled(row.category, $event)"
                  />
                  <label class="sr-only" :for="`risk-threshold-${row.category}`">{{ t('admin.riskControl.riskThresholdPercent') }}</label>
                  <UInput
                    :id="`risk-threshold-${row.category}`"
                    v-model.number="configForm.thresholds[row.category]"
                    :disabled="configForm.engine === 'typesafe' && !configForm.category_settings[row.category]?.enabled"
                    :data-test="`risk-threshold-${row.category}`"
                    type="number"
                    min="0"
                    max="100"
                    step="0.1"
                    class="w-32"
                    :ui="{ base: 'font-mono', trailing: 'pointer-events-none' }"
                  >
                    <template #trailing><span class="text-sm text-dimmed">%</span></template>
                  </UInput>
                </div>
              </div>
            </UPageCard>
          </div>

          <div v-else-if="activeSettingsTab === 'keywords'" class="flex flex-col gap-6">
            <UAlert :color="keywordNotice.color" variant="subtle" :icon="keywordNotice.icon" :title="keywordNotice.title" :description="keywordNotice.description" />
            <UPageCard variant="subtle">
              <UFormField :label="t('admin.riskControl.keywordBlockingMode')">
                <URadioGroup
                  :model-value="configForm.keyword_blocking_mode"
                  :items="keywordBlockingModeOptions"
                  variant="card"
                  orientation="horizontal"
                  class="mt-1"
                  :ui="{ fieldset: 'grid grid-cols-1 gap-2 sm:grid-cols-3' }"
                  @update:model-value="onKeywordBlockingModeChange"
                />
              </UFormField>
              <USeparator />
              <UFormField :label="t('admin.riskControl.blockedKeywords')" :help="t('admin.riskControl.blockedKeywordsLimit', { max: blockedKeywordMax })">
                <template #hint>
                  <UBadge color="neutral" variant="subtle">{{ t('admin.riskControl.blockedKeywordCount', { count: blockedKeywordCount }) }}</UBadge>
                </template>
                <UTextarea
                  v-model="configForm.blocked_keywords_text"
                  :rows="9"
                  class="w-full"
                  :ui="{ base: 'font-mono text-sm resize-y min-h-52' }"
                  :placeholder="t('admin.riskControl.blockedKeywordsPlaceholder')"
                  :disabled="configForm.keyword_blocking_mode === 'api_only'"
                />
              </UFormField>
            </UPageCard>
          </div>

          <div v-else class="flex flex-col gap-6">
            <UPageCard variant="subtle">
              <UFormField :label="t('admin.riskControl.hitRetentionDays')" :class="fieldRowClass" :ui="fieldControlUi">
                <UInput v-model.number="configForm.hit_retention_days" type="number" min="1" max="3650" class="w-full" />
              </UFormField>
              <USeparator />
              <UFormField :label="t('admin.riskControl.nonHitRetentionDays')" :class="fieldRowClass" :ui="fieldControlUi">
                <UInput v-model.number="configForm.non_hit_retention_days" type="number" min="1" max="3" class="w-full" />
              </UFormField>
              <USeparator />
              <div class="flex flex-wrap items-center gap-3 text-sm text-muted">
                <UIcon name="i-lucide-database" class="size-5 text-dimmed" />
                <span>{{ t('admin.riskControl.cleanupStats', { hit: status?.last_cleanup_deleted_hit ?? 0, nonHit: status?.last_cleanup_deleted_non_hit ?? 0 }) }}</span>
              </div>
            </UPageCard>
          </div>
        </div>

        <template #footer>
          <div class="flex justify-end gap-2">
            <UButton color="neutral" variant="subtle" :label="t('common.cancel')" @click="settingsOpen = false" />
            <UButton icon="i-lucide-check" :loading="saving" :disabled="saving" :label="saving ? t('common.saving') : t('admin.riskControl.saveConfig')" @click="saveConfig" />
          </div>
        </template>
      </BaseDialog>

      <BaseDialog
        :show="inputDetailRow !== null"
        :title="t('admin.riskControl.inputDetailTitle')"
        width="wide"
        @close="closeInputDetail"
      >
        <div v-if="inputDetailRow" class="flex flex-col gap-5">
          <div class="text-sm wrap-break-word text-muted" data-test="audit-engine-meta">
            <span class="font-medium text-default">{{ t('admin.riskControl.auditSource') }}: </span>
            <template v-if="inputDetailRow.engine_meta">
              {{ engineLabel(inputDetailRow.engine_meta.engine) }} · {{ inputDetailRow.engine_meta.model || '-' }} · {{ inputDetailRow.engine_meta.rules_version || '-' }}
              <span v-if="inputDetailRow.engine_meta.skipped_images"> · {{ t('admin.riskControl.skippedImages', { count: inputDetailRow.engine_meta.skipped_images }) }}</span>
              <span v-if="inputDetailRow.engine_meta.matched_categories?.length"> · {{ t('moderationCategories.matched') }}: {{ inputDetailRow.engine_meta.matched_categories.join(', ') }}</span>
              <span v-if="inputDetailRow.engine_meta.category_settings" class="mt-1 block wrap-break-word">
                {{ t('moderationCategories.snapshot') }}:
                <span v-for="(setting, category) in inputDetailRow.engine_meta.category_settings" :key="category" class="mr-2">{{ category }}={{ setting.enabled ? t('common.enabled') : t('common.disabled') }}</span>
              </span>
            </template>
            <template v-else>{{ ['cyber_policy', 'keyword_block', 'hash_block'].includes(inputDetailRow.action) ? '-' : t('admin.riskControl.legacyAuditSource') }}</template>
          </div>

          <dl class="grid grid-cols-1 gap-x-6 gap-y-4 sm:grid-cols-2 lg:grid-cols-4">
            <div class="min-w-0">
              <dt class="text-xs text-muted">{{ t('admin.riskControl.table.time') }}</dt>
              <dd class="mt-1 truncate text-sm font-medium text-highlighted">{{ formatDateTime(inputDetailRow.created_at) }}</dd>
            </div>
            <div class="min-w-0">
              <dt class="text-xs text-muted">{{ t('admin.riskControl.table.user') }}</dt>
              <dd class="mt-1 truncate text-sm font-medium text-highlighted">{{ inputDetailRow.user_email || '-' }}</dd>
            </div>
            <div class="min-w-0">
              <dt class="text-xs text-muted">{{ t('admin.riskControl.table.result') }}</dt>
              <dd class="mt-1"><UBadge :color="resultBadgeColor(inputDetailRow)" variant="subtle">{{ resultLabel(inputDetailRow) }}</UBadge></dd>
            </div>
            <div class="min-w-0">
              <dt class="text-xs text-muted">{{ t('admin.riskControl.table.highest') }}</dt>
              <dd class="mt-1 truncate text-sm font-medium text-highlighted">{{ inputDetailRow.highest_category || '-' }} / {{ percent(inputDetailRow.highest_score) }}</dd>
            </div>
            <div v-if="inputDetailRow.matched_keyword" class="min-w-0">
              <dt class="text-xs text-error">{{ t('admin.riskControl.matchedKeyword') }}</dt>
              <dd class="mt-1 truncate text-sm font-medium text-error" :title="inputDetailRow.matched_keyword">{{ inputDetailRow.matched_keyword }}</dd>
            </div>
          </dl>

          <USeparator />

          <div class="flex flex-col gap-3">
            <div class="flex flex-wrap items-center justify-between gap-3">
              <div>
                <p class="text-sm font-medium text-highlighted">{{ t('admin.riskControl.inputDetailContent') }}</p>
                <p class="mt-1 text-xs text-muted">{{ inputDetailRow.endpoint || '-' }} · {{ inputDetailRow.provider || '-' }} / {{ inputDetailRow.model || '-' }}</p>
              </div>
              <UBadge v-if="inputDetailRow.group_name" color="neutral" variant="subtle">{{ inputDetailRow.group_name }}</UBadge>
            </div>
            <pre class="max-h-[420px] overflow-auto whitespace-pre-wrap wrap-break-word rounded-md bg-muted p-4 text-sm leading-6 text-default ring ring-inset ring-default">{{ inputDetailText }}</pre>
          </div>
        </div>

        <template #footer>
          <div class="flex justify-end">
            <UButton color="neutral" variant="subtle" :label="t('common.close')" @click="closeInputDetail" />
          </div>
        </template>
      </BaseDialog>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref, toRaw, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { DropdownMenuItem, TableColumn } from '@nuxt/ui'
import AppLayout from '@/components/layout/AppLayout.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import AdminPageHeader from '@/components/admin/page/AdminPageHeader.vue'
import Select from '@/components/common/Select.vue'
import Toggle from '@/components/common/Toggle.vue'
import Pagination from '@/components/common/Pagination.vue'
import ModelWhitelistSelector from '@/components/account/ModelWhitelistSelector.vue'
import ProxySelector from '@/components/common/ProxySelector.vue'
import { adminAPI } from '@/api/admin'
import type {
  ContentModerationAPIKeyLoad,
  ContentModerationAPIKeyStatus,
  ContentModerationConfig,
  ContentModerationLog,
  ContentModerationModelFilter,
  ContentModerationModelFilterType,
  ContentModerationRuntimeStatus,
  ContentModerationTestAuditResult,
  KeywordBlockingMode,
  ModerationMode,
  ModerationEngine,
  UpdateModerationEngineConfig,
  UpdateContentModerationConfig,
} from '@/api/admin/riskControl'
import type { AdminGroup, Proxy, SelectOption } from '@/types'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'
import { formatDateTime as formatDateTimeValue } from '@/utils/format'

type SettingsTab = 'basic' | 'scope' | 'runtime' | 'response' | 'riskThresholds' | 'retention' | 'keywords'
type WorkerSlotState = 'active' | 'idle' | 'disabled'
type APIKeysWriteMode = 'append' | 'replace'
type BadgeColor = 'primary' | 'secondary' | 'success' | 'info' | 'warning' | 'error' | 'neutral'
type OverviewItem = {
  key: string
  label: string
  value: string
  meta: string
  icon: string
  badge?: string
  badgeColor?: BadgeColor
}
type LogColumnID = 'time' | 'group' | 'user' | 'api_key' | 'endpoint' | 'result' | 'highest' | 'action' | 'latency' | 'input'
type ModerationScoreRow = {
  category: string
  score: number
  threshold: number
  hit: boolean
}
type RiskThresholdRow = {
  category: string
  value: number
  defaultValue: number
}

const maxModerationTestImages = 1
const maxModerationTestImageSize = 8 * 1024 * 1024
const maxVisibleApiKeyRows: number = 3
const blockedKeywordMax = 10000
const riskThresholdDefaults: Record<string, number> = {
  harassment: 98,
  'harassment/threatening': 90,
  hate: 65,
  'hate/threatening': 65,
  illicit: 95,
  'illicit/violent': 95,
  'self-harm': 65,
  'self-harm/intent': 85,
  'self-harm/instructions': 65,
  sexual: 65,
  'sexual/minors': 65,
  violence: 95,
  'violence/graphic': 95,
  jailbreak: 95,
  control_bypass: 95,
}
const riskThresholdCategories = Object.keys(riskThresholdDefaults)
const optionalTypeSafeCategories = new Set(['jailbreak', 'control_bypass'])
function categorySettingsFromConfig(settings?: Record<string, { enabled: boolean }>) {
  return Object.fromEntries(riskThresholdCategories.map(category => [category, { enabled: settings?.[category]?.enabled ?? !optionalTypeSafeCategories.has(category) }]))
}

import {zh as moderationZh,en as moderationEn} from '@/features/ip-risk/moderationCategories.locale'
const {t}=useI18n({useScope:'local',messages:{zh:{moderationCategories:moderationZh},en:{moderationCategories:moderationEn}},inheritLocale:true})
const appStore = useAppStore()
const defaultBlockMessage = () => t('admin.riskControl.defaultBlockMessage')

const loading = ref(true)
const saving = ref(false)
const logsLoading = ref(false)
const statusLoading = ref(false)
const apiKeyTesting = ref(false)
const hashActionLoading = ref(false)
const unbanningUserID = ref<number | null>(null)
const settingsOpen = ref(false)
const activeSettingsTab = ref<SettingsTab>('basic')
const groupSearch = ref('')
const flaggedHashInput = ref('')
const groups = ref<AdminGroup[]>([])
const proxies = ref<Proxy[]>([])
const logs = ref<ContentModerationLog[]>([])
const status = ref<ContentModerationRuntimeStatus | null>(null)
const testedApiKeyStatuses = ref<ContentModerationAPIKeyStatus[]>([])
const pendingDeleteApiKeyHashes = ref<string[]>([])
const apiKeyRowsExpanded = ref<boolean>(false)
const moderationTestPrompt = ref('')
const moderationTestImages = ref<string[]>([])
const moderationTestResult = ref<ContentModerationTestAuditResult | null>(null)
const inputDetailRow = ref<ContentModerationLog | null>(null)
const savedEngine = ref<ModerationEngine>('openai')
const engineOptions: SelectOption[] = [{ value: 'openai', label: 'OpenAI' }, { value: 'typesafe', label: 'TypeSafe AI' }]
const engineLabel = (engine: ModerationEngine) => engine === 'typesafe' ? 'TypeSafe AI' : 'OpenAI'
let statusTimer: number | null = null

const configForm = reactive({
  engine: 'openai' as ModerationEngine,
  enabled: false,
  mode: 'pre_block' as ModerationMode,
  base_url: 'https://api.openai.com',
  model: 'omni-moderation-latest',
  proxy_id: null as number | null,
  api_keys_text: '',
  api_key_configured: false,
  api_key_masked: '',
  api_key_count: 0,
  api_key_masks: [] as string[],
  api_key_statuses: [] as ContentModerationAPIKeyStatus[],
  api_keys_mode: 'append' as APIKeysWriteMode,
  clear_api_key: false,
  timeout_ms: 3000,
  retry_count: 2,
  sample_rate: 100,
  all_groups: true,
  group_ids: [] as number[],
  record_non_hits: false,
  worker_count: 4,
  queue_size: 32768,
  block_status: 403,
  block_message: defaultBlockMessage(),
  email_on_hit: true,
  auto_ban_enabled: true,
  cyber_policy_exclude_from_ban_count: false,
  ban_threshold: 10,
  violation_window_hours: 720,
  hit_retention_days: 180,
  non_hit_retention_days: 3,
  pre_hash_check_enabled: false,
  thresholds: { ...riskThresholdDefaults } as Record<string, number>,
  category_settings: categorySettingsFromConfig(),
  blocked_keywords_text: '',
  keyword_blocking_mode: 'keyword_and_api' as KeywordBlockingMode,
  model_filter_type: 'all' as ContentModerationModelFilterType,
  model_filter_models: [] as string[],
})

const engineFields = ['base_url', 'model', 'proxy_id', 'api_keys_text', 'api_key_configured', 'api_key_masked', 'api_key_count', 'api_key_masks', 'api_key_statuses', 'api_keys_mode', 'clear_api_key', 'timeout_ms', 'retry_count', 'thresholds', 'category_settings'] as const
type EngineDraft = Pick<typeof configForm, typeof engineFields[number]> & { pendingDeletes: string[] }
const engineDrafts = ref<Partial<Record<ModerationEngine, EngineDraft>>>({})

function captureEngineDraft(): EngineDraft {
  return structuredClone({ ...Object.fromEntries(engineFields.map(key => [key, toRaw(configForm)[key]])), pendingDeletes: [...pendingDeleteApiKeyHashes.value] }) as EngineDraft
}

function switchEngine(value: string | number | boolean | null) {
  if ((value !== 'openai' && value !== 'typesafe') || apiKeyTesting.value || saving.value) return
  engineDrafts.value[configForm.engine] = captureEngineDraft()
  configForm.engine = value
  const draft = engineDrafts.value[value]
  if (draft) {
    const { pendingDeletes, ...fields } = structuredClone(toRaw(draft))
    Object.assign(configForm, fields)
    pendingDeleteApiKeyHashes.value = pendingDeletes
  }
  testedApiKeyStatuses.value = []
  moderationTestResult.value = null
}

function engineDraftFromConfig(config: ContentModerationConfig | undefined, engine: ModerationEngine): EngineDraft {
  return {
    base_url: config?.base_url || (engine === 'typesafe' ? 'https://api.typesafe.ai' : 'https://api.openai.com'),
    model: config?.model || (engine === 'typesafe' ? 'jev-latest' : 'omni-moderation-latest'),
    proxy_id: config?.proxy_id ?? null, api_keys_text: '', api_key_configured: config?.api_key_configured ?? false,
    api_key_masked: config?.api_key_masked ?? '', api_key_count: config?.api_key_count ?? 0,
    api_key_masks: [...(config?.api_key_masks ?? [])], api_key_statuses: [...(config?.api_key_statuses ?? [])],
    api_keys_mode: 'append', clear_api_key: false, pendingDeletes: [],
    timeout_ms: config?.timeout_ms ?? 3000, retry_count: config?.retry_count ?? 2,
    thresholds: riskThresholdsFromConfig(config?.thresholds),
    category_settings: categorySettingsFromConfig(config?.category_settings),
  }
}

function engineDraftPayload(draft: EngineDraft): UpdateModerationEngineConfig {
  const keys = parseApiKeys(draft.api_keys_text)
  if (!draft.clear_api_key && draft.api_keys_mode === 'replace' && keys.length === 0) throw new Error('empty replacement keys')
  return {
    base_url: draft.base_url, model: draft.model, proxy_id: draft.proxy_id ?? 0,
    timeout_ms: draft.timeout_ms, retry_count: draft.retry_count,
    category_settings: structuredClone(toRaw(draft.category_settings)),
    thresholds: Object.fromEntries(riskThresholdCategories.map(k => [k, clampPercent(draft.thresholds[k]) / 100])),
    clear_api_key: draft.clear_api_key, api_keys: keys.length ? keys : undefined,
    api_keys_mode: draft.api_keys_mode, delete_api_key_hashes: draft.pendingDeletes,
  }
}

const pagination = reactive({
  page: 1,
  page_size: 20,
  total: 0,
  pages: 1,
})

const filters = reactive({
  result: '',
  group_id: 0,
  endpoint: '',
  search: '',
  from: '',
  to: '',
})

const settingsTabs = computed<Array<{ id: SettingsTab; label: string }>>(() => [
  { id: 'basic', label: t('admin.riskControl.tabs.basic') },
  { id: 'scope', label: t('admin.riskControl.tabs.scope') },
  { id: 'runtime', label: t('admin.riskControl.tabs.runtime') },
  { id: 'response', label: t('admin.riskControl.tabs.response') },
  { id: 'riskThresholds', label: t('admin.riskControl.tabs.riskThresholds') },
  { id: 'keywords', label: t('admin.riskControl.tabs.keywords') },
  { id: 'retention', label: t('admin.riskControl.tabs.retention') },
])

const modeOptions = computed<SelectOption[]>(() => [
  { value: 'pre_block', label: t('admin.riskControl.modePreBlock') },
  { value: 'observe', label: t('admin.riskControl.modeObserve') },
  { value: 'off', label: t('admin.riskControl.modeOff') },
])

const keywordBlockingModeOptions = computed<Array<{ value: KeywordBlockingMode; label: string; description: string }>>(() => [
  {
    value: 'keyword_and_api',
    label: t('admin.riskControl.keywordModeKeywordAndApi'),
    description: t('admin.riskControl.keywordModeKeywordAndApiDesc'),
  },
  {
    value: 'keyword_only',
    label: t('admin.riskControl.keywordModeKeywordOnly'),
    description: t('admin.riskControl.keywordModeKeywordOnlyDesc'),
  },
  {
    value: 'api_only',
    label: t('admin.riskControl.keywordModeApiOnly'),
    description: t('admin.riskControl.keywordModeApiOnlyDesc'),
  },
])

const modelFilterOptions = computed<Array<{ value: ContentModerationModelFilterType; label: string; description: string }>>(() => [
  {
    value: 'all',
    label: t('admin.riskControl.modelFilterAll'),
    description: t('admin.riskControl.modelFilterAllDesc'),
  },
  {
    value: 'include',
    label: t('admin.riskControl.modelFilterInclude'),
    description: t('admin.riskControl.modelFilterIncludeDesc'),
  },
  {
    value: 'exclude',
    label: t('admin.riskControl.modelFilterExclude'),
    description: t('admin.riskControl.modelFilterExcludeDesc'),
  },
])

type KeywordNoticeView = {
  title: string
  description: string
  icon: string
  color: BadgeColor
}

const keywordNoticeTones = {
  info: { icon: 'i-lucide-info', color: 'neutral' as BadgeColor },
  warning: { icon: 'i-lucide-triangle-alert', color: 'warning' as BadgeColor },
}

const keywordNotice = computed<KeywordNoticeView>(() => {
  const strategy = configForm.keyword_blocking_mode
  if (strategy === 'api_only') {
    return {
      ...keywordNoticeTones.info,
      title: t('admin.riskControl.keywordModeApiOnlyNotice'),
      description: t('admin.riskControl.keywordModeApiOnlyDesc'),
    }
  }
  if (configForm.mode !== 'pre_block') {
    return {
      ...keywordNoticeTones.warning,
      title: t('admin.riskControl.blockedKeywordsModeWarning', { mode: modeLabel(configForm.mode) }),
      description: t('admin.riskControl.blockedKeywordsDescription'),
    }
  }
  if (strategy === 'keyword_only') {
    return {
      ...keywordNoticeTones.info,
      title: t('admin.riskControl.keywordModeKeywordOnlyNotice'),
      description: t('admin.riskControl.keywordModeKeywordOnlyDesc'),
    }
  }
  return {
    ...keywordNoticeTones.info,
    title: t('admin.riskControl.blockedKeywordsPreBlockHint'),
    description: t('admin.riskControl.blockedKeywordsDescription'),
  }
})

const resultOptions = computed<SelectOption[]>(() => [
  { value: '', label: t('admin.riskControl.result.all') },
  { value: 'hit', label: t('admin.riskControl.result.hit') },
  { value: 'blocked', label: t('admin.riskControl.result.blocked') },
  { value: 'pass', label: t('admin.riskControl.result.pass') },
  { value: 'error', label: t('admin.riskControl.result.error') },
])

const endpointOptions = computed<SelectOption[]>(() => [
  { value: '', label: t('admin.riskControl.filters.allEndpoints') },
  { value: '/v1/messages', label: '/v1/messages' },
  { value: '/v1/responses', label: '/v1/responses' },
  { value: '/v1/chat/completions', label: '/v1/chat/completions' },
  { value: '/v1beta/models', label: '/v1beta/models' },
  { value: '/v1/images/generations', label: '/v1/images/generations' },
  { value: '/v1/images/edits', label: '/v1/images/edits' },
])

const groupFilterOptions = computed<SelectOption[]>(() => [
  { value: 0, label: t('admin.riskControl.filters.allGroups') },
  ...groups.value.map((group) => ({
    value: group.id,
    label: `${group.name} (${group.platform})`,
  })),
])

const selectedGroupCount = computed(() => String(configForm.group_ids.length))

const modelFilterModelCount = computed(() => configForm.model_filter_models.length)

const modelFilterSummary = computed(() => {
  if (configForm.model_filter_type === 'include') {
    return t('admin.riskControl.modelFilterIncludeSummary', { count: modelFilterModelCount.value })
  }
  if (configForm.model_filter_type === 'exclude') {
    return t('admin.riskControl.modelFilterExcludeSummary', { count: modelFilterModelCount.value })
  }
  return t('admin.riskControl.modelFilterAllSummary')
})

const modelFilterPreviewModels = computed(() => configForm.model_filter_models.slice(0, 6))

const hiddenModelFilterModelCount = computed(() => Math.max(0, configForm.model_filter_models.length - modelFilterPreviewModels.value.length))

const filteredGroups = computed(() => {
  const keyword = groupSearch.value.trim().toLowerCase()
  if (!keyword) return groups.value
  return groups.value.filter((group) => {
    return group.name.toLowerCase().includes(keyword) || String(group.platform).toLowerCase().includes(keyword)
  })
})

const inputApiKeyCount = computed(() => parseApiKeys(configForm.api_keys_text).length)

const blockedKeywordList = computed(() => parseBlockedKeywords(configForm.blocked_keywords_text))

const blockedKeywordCount = computed(() => blockedKeywordList.value.length)

const pendingDeletedApiKeyCount = computed(() => pendingDeleteApiKeyHashes.value.length)

const effectiveStoredApiKeyCount = computed(() => Math.max(0, configForm.api_key_count - pendingDeletedApiKeyCount.value))

const apiKeysPlaceholder = computed(() => (
  configForm.api_keys_mode === 'replace'
    ? t('admin.riskControl.apiKeysPlaceholderReplace')
    : t('admin.riskControl.apiKeysPlaceholder')
))

const apiKeysModeHint = computed(() => (
  configForm.api_keys_mode === 'replace'
    ? t('admin.riskControl.apiKeysModeReplaceHint')
    : t('admin.riskControl.apiKeysModeAppendHint')
))

const hasModerationAuditInput = computed(() => {
  return moderationTestPrompt.value.trim() !== '' || moderationTestImages.value.length > 0
})

const isFlaggedHashInputValid = computed(() => /^[a-fA-F0-9]{64}$/.test(flaggedHashInput.value.trim()))

const storedApiKeyTestButtonText = computed(() => {
  if (apiKeyTesting.value) return t('admin.riskControl.testingApiKeys')
  if (hasModerationAuditInput.value) return t('admin.riskControl.testContentWithStoredApiKey')
  return t('admin.riskControl.testStoredApiKeys')
})

const savedApiKeyRows = computed<ContentModerationAPIKeyStatus[]>(() => {
  const rows = (status.value?.engine ?? 'openai') === configForm.engine && status.value?.api_key_statuses?.length
    ? status.value.api_key_statuses
    : configForm.api_key_statuses
  return Array.isArray(rows) ? rows : []
})

const apiKeyRows = computed<ContentModerationAPIKeyStatus[]>(() => [
  ...savedApiKeyRows.value,
  ...testedApiKeyStatuses.value,
])

const visibleApiKeyRows = computed<ContentModerationAPIKeyStatus[]>(() => {
  if (apiKeyRowsExpanded.value) return apiKeyRows.value
  return apiKeyRows.value.slice(0, maxVisibleApiKeyRows)
})

const hiddenApiKeyRowCount = computed<number>(() => Math.max(0, apiKeyRows.value.length - visibleApiKeyRows.value.length))

const canToggleApiKeyRows = computed<boolean>(() => apiKeyRows.value.length > maxVisibleApiKeyRows)

const activeSavedApiKeyRows = computed<ContentModerationAPIKeyStatus[]>(() => (
  savedApiKeyRows.value.filter((row) => !isStoredApiKeyPendingDelete(row))
))

const apiKeyHealthBadges = computed<Array<{ status: ContentModerationAPIKeyStatus['status']; count: number }>>(() => {
  const counts: Record<ContentModerationAPIKeyStatus['status'], number> = {
    ok: 0,
    error: 0,
    frozen: 0,
    unknown: 0,
  }
  for (const row of activeSavedApiKeyRows.value) {
    counts[row.status] = (counts[row.status] ?? 0) + 1
  }
  if (activeSavedApiKeyRows.value.length === 0 && effectiveStoredApiKeyCount.value > 0) {
    counts.unknown = effectiveStoredApiKeyCount.value
  }
  return (['ok', 'frozen', 'error', 'unknown'] as Array<ContentModerationAPIKeyStatus['status']>)
    .map((item) => ({ status: item, count: counts[item] }))
    .filter((item) => item.count > 0)
})

const apiKeyHealthSummary = computed(() => {
  if (!configForm.api_key_configured) return ''
  if (apiKeyHealthBadges.value.length === 0) return t('admin.riskControl.apiKeyStatusUnknown')
  return apiKeyHealthBadges.value
    .map((badge) => `${apiKeyStatusLabel(badge.status)} ${badge.count}`)
    .join(' · ')
})

const overviewItems = computed<OverviewItem[]>(() => [
  {
    key: 'status',
    label: t('admin.riskControl.overview.status'),
    value: configForm.enabled ? t('admin.riskControl.overview.enabled') : t('admin.riskControl.overview.disabled'),
    meta: modeLabel(configForm.mode),
    icon: 'i-lucide-shield',
    badge: runtimeBadgeText.value,
    badgeColor: runtimeBadgeColor.value,
  },
  {
    key: 'api-key',
    label: t('admin.riskControl.overview.apiKey'),
    value: configForm.api_key_configured ? t('admin.riskControl.apiKeyCount', { count: configForm.api_key_count }) : t('admin.riskControl.notConfigured'),
    meta: configForm.api_key_configured ? apiKeyHealthSummary.value || configForm.model || '-' : configForm.model || '-',
    icon: 'i-lucide-key-round',
  },
  {
    key: 'scope',
    label: t('admin.riskControl.overview.groupScope'),
    value: configForm.all_groups ? t('admin.riskControl.allGroups') : selectedGroupCount.value,
    meta: modelFilterSummary.value,
    icon: 'i-lucide-users',
  },
  {
    key: 'logs',
    label: t('admin.riskControl.overview.logs'),
    value: formatNumber(pagination.total),
    meta: t('admin.riskControl.overview.currentFilter'),
    icon: 'i-lucide-file-text',
  },
])

const moderationScoreRows = computed<ModerationScoreRow[]>(() => {
  const result = moderationTestResult.value
  if (!result) return []
  return Object.entries(result.category_scores || {})
    .map(([category, score]) => {
      const threshold = result.thresholds?.[category] ?? 1
      return {
        category,
        score,
        threshold,
        hit: score >= threshold,
      }
    })
    .sort((a, b) => b.score - a.score)
})

const riskThresholdRows = computed<RiskThresholdRow[]>(() => (
  riskThresholdCategories.filter(category => configForm.engine === 'typesafe' || !optionalTypeSafeCategories.has(category)).map((category) => ({
    category,
    value: configForm.thresholds[category] ?? riskThresholdDefaults[category],
    defaultValue: riskThresholdDefaults[category],
  }))
))

const inputDetailText = computed(() => {
  if (!inputDetailRow.value) return '-'
  return inputDetailRow.value.input_excerpt || inputDetailRow.value.error || '-'
})

const queueUsagePercent = computed(() => `${Math.min(100, Math.max(0, status.value?.queue_usage_percent ?? 0)).toFixed(1)}%`)

const queueUsageValue = computed(() => Math.min(100, Math.max(0, status.value?.queue_usage_percent ?? 0)))

const runtimeMode = computed<ModerationMode>(() => status.value?.mode ?? configForm.mode)

const showPreBlockRuntimeCard = computed(() => runtimeMode.value === 'pre_block')

const showWorkerRuntimeCard = computed(() => runtimeMode.value === 'observe')

const preBlockMetricItems = computed(() => [
  {
    key: 'active',
    label: t('admin.riskControl.preBlockActive'),
    value: formatNumber(status.value?.pre_block_active ?? 0),
    meta: t('admin.riskControl.preBlockActiveHint'),
    valueClass: 'text-info',
  },
  {
    key: 'checked',
    label: t('admin.riskControl.preBlockChecked'),
    value: formatNumber(status.value?.pre_block_checked ?? 0),
    meta: t('admin.riskControl.preBlockCheckedHint'),
    valueClass: 'text-highlighted',
  },
  {
    key: 'allowed',
    label: t('admin.riskControl.preBlockAllowed'),
    value: formatNumber(status.value?.pre_block_allowed ?? 0),
    meta: t('admin.riskControl.preBlockAllowedHint'),
    valueClass: 'text-success',
  },
  {
    key: 'blocked',
    label: t('admin.riskControl.preBlockBlocked'),
    value: formatNumber(status.value?.pre_block_blocked ?? 0),
    meta: t('admin.riskControl.preBlockBlockedHint'),
    valueClass: 'text-error',
  },
  {
    key: 'errors',
    label: t('admin.riskControl.preBlockErrors'),
    value: formatNumber(status.value?.pre_block_errors ?? 0),
    meta: t('admin.riskControl.preBlockErrorsHint'),
    valueClass: 'text-warning',
  },
  {
    key: 'latency',
    label: t('admin.riskControl.preBlockAvgLatency'),
    value: `${formatNumber(status.value?.pre_block_avg_latency_ms ?? 0)} ms`,
    meta: t('admin.riskControl.preBlockAvgLatencyHint'),
    valueClass: 'text-highlighted',
  },
])

const preBlockAPIKeyLoads = computed<ContentModerationAPIKeyLoad[]>(() => (
  [...(status.value?.pre_block_api_key_loads ?? [])].sort((a, b) => a.index - b.index)
))

const preBlockAPIKeyMaxTotal = computed(() => Math.max(1, ...preBlockAPIKeyLoads.value.map((item) => item.total || 0)))

const preBlockAPIKeyLoadSummaryText = computed(() => t('admin.riskControl.preBlockAPIKeyLoadSummary', {
  active: formatNumber(status.value?.pre_block_api_key_active ?? 0),
  available: formatNumber(status.value?.pre_block_api_key_available_count ?? 0),
  total: formatNumber(status.value?.pre_block_api_key_total_calls ?? 0),
  workerActive: formatNumber(status.value?.active_workers ?? 0),
  workerTotal: formatNumber(status.value?.worker_count ?? configForm.worker_count),
}))

const workerSlots = computed(() => {
  const total = Math.max(0, status.value?.worker_count ?? configForm.worker_count)
  const active = Math.max(0, status.value?.active_workers ?? 0)
  const enabled = Boolean(status.value?.risk_control_enabled && status.value?.enabled && status.value?.mode !== 'off')
  return Array.from({ length: total }, (_, index) => ({
    id: index + 1,
    state: (!enabled ? 'disabled' : index < active ? 'active' : 'idle') as WorkerSlotState,
    label: !enabled
      ? t('admin.riskControl.workerDisabled')
      : index < active
        ? t('admin.riskControl.workerActive')
        : t('admin.riskControl.workerIdle'),
  }))
})

const runtimeBadgeText = computed(() => {
  if (!status.value?.risk_control_enabled) return t('admin.riskControl.riskSwitchOff')
  if (!configForm.enabled || configForm.mode === 'off') return t('admin.riskControl.overview.disabled')
  return t('admin.riskControl.overview.enabled')
})

const runtimeBadgeColor = computed<BadgeColor>(() => {
  if (!status.value?.risk_control_enabled || !configForm.enabled || configForm.mode === 'off') return 'neutral'
  return 'success'
})

// Settings dialog: tab bar, radio choices and their typed change handlers.
const settingsTabItems = computed(() => settingsTabs.value.map((tab) => ({ label: tab.label, value: tab.id })))

function selectSettingsTab(value: string | number) {
  const tab = settingsTabs.value.find((item) => item.id === value)
  if (tab) activeSettingsTab.value = tab.id
}

const apiKeysModeItems = computed(() => [
  { value: 'append', label: t('admin.riskControl.apiKeysModeAppend') },
  { value: 'replace', label: t('admin.riskControl.apiKeysModeReplace') },
])

function onAPIKeysModeChange(value: unknown) {
  if (value === 'append' || value === 'replace') setAPIKeysMode(value)
}

const groupScopeItems = computed(() => [
  { value: 'all', label: t('admin.riskControl.allGroups') },
  { value: 'selected', label: t('admin.riskControl.selectedGroups') },
])

function onGroupScopeChange(value: unknown) {
  configForm.all_groups = value === 'all'
}

function onModelFilterTypeChange(value: unknown) {
  setModelFilterType(normalizeModelFilterType(value))
}

function onKeywordBlockingModeChange(value: unknown) {
  configForm.keyword_blocking_mode = normalizeKeywordBlockingMode(value)
}

function setCategoryEnabled(category: string, value: boolean | 'indeterminate') {
  const setting = configForm.category_settings[category]
  if (setting) setting.enabled = value === true
  else configForm.category_settings[category] = { enabled: value === true }
}

// Settings rows in the dashboard-template manner: label + description left, control right.
const fieldRowClass = 'flex max-sm:flex-col justify-between items-start gap-4'
const switchRowClass = 'flex items-center justify-between gap-4'
const fieldControlUi = { container: 'w-full sm:w-64 shrink-0' }
const fieldWideUi = { container: 'w-full sm:w-96 shrink-0' }

// Audit records table: most-used columns by default, the rest behind the column menu.
const logColumnStorageKey = 'risk-control-log-columns'
const defaultLogColumnVisibility: Record<string, boolean> = { group: false, api_key: false, latency: false }

function loadLogColumnVisibility(): Record<string, boolean> {
  try {
    const saved = JSON.parse(localStorage.getItem(logColumnStorageKey) || 'null')
    if (saved && typeof saved === 'object' && !Array.isArray(saved)) return { ...defaultLogColumnVisibility, ...saved }
  } catch {
    // Unreadable preference: fall back to the defaults.
  }
  return { ...defaultLogColumnVisibility }
}

const logColumnVisibility = ref<Record<string, boolean>>(loadLogColumnVisibility())
watch(logColumnVisibility, (value) => {
  try {
    localStorage.setItem(logColumnStorageKey, JSON.stringify(value))
  } catch {
    // Storage unavailable: the choice lasts for this visit only.
  }
}, { deep: true })

const logColumnDefs = computed<Array<{ id: LogColumnID; label: string }>>(() => [
  { id: 'time', label: t('admin.riskControl.table.time') },
  { id: 'group', label: t('admin.riskControl.table.group') },
  { id: 'user', label: t('admin.riskControl.table.user') },
  { id: 'api_key', label: t('admin.riskControl.table.apiKey') },
  { id: 'endpoint', label: t('admin.riskControl.table.endpoint') },
  { id: 'result', label: t('admin.riskControl.table.result') },
  { id: 'highest', label: t('admin.riskControl.table.highest') },
  { id: 'action', label: t('admin.riskControl.table.actionMeta') },
  { id: 'latency', label: t('admin.riskControl.table.latency') },
  { id: 'input', label: t('admin.riskControl.table.input') },
])

const logColumns = computed<TableColumn<ContentModerationLog>[]>(() => logColumnDefs.value.map((column) => ({
  id: column.id,
  header: column.label,
  enableHiding: column.id !== 'time',
})))

const logColumnMenuItems = computed<DropdownMenuItem[]>(() => logColumnDefs.value
  .filter((column) => column.id !== 'time')
  .map((column) => ({
    label: column.label,
    type: 'checkbox' as const,
    checked: logColumnVisibility.value[column.id] !== false,
    onUpdateChecked(checked: boolean) {
      logColumnVisibility.value = { ...logColumnVisibility.value, [column.id]: !!checked }
    },
    onSelect(event?: Event) {
      event?.preventDefault()
    },
  })))

// While a page loads the table shows its loading row instead of the previous page.
const tableLogs = computed<ContentModerationLog[]>(() => (logsLoading.value ? [] : logs.value))

// UFileUpload hands over the picked or dropped files; the component keeps no list of its own.
const noFiles: File[] = []

async function handleModerationImageFiles(value: File[] | File | null | undefined) {
  if (!value) return
  await addModerationTestFiles(Array.isArray(value) ? value : [value])
}

function applyConfig(config: ContentModerationConfig) {
  savedEngine.value = config.engine ?? 'openai'
  configForm.engine = savedEngine.value
  configForm.enabled = config.enabled
  configForm.mode = config.mode
  configForm.base_url = config.base_url || 'https://api.openai.com'
  configForm.model = config.model || 'omni-moderation-latest'
  configForm.proxy_id = config.proxy_id || null
  configForm.api_keys_text = ''
  configForm.api_key_configured = config.api_key_configured
  configForm.api_key_masked = config.api_key_masked || ''
  configForm.api_key_count = config.api_key_count || 0
  configForm.api_key_masks = Array.isArray(config.api_key_masks) ? [...config.api_key_masks] : []
  configForm.api_key_statuses = Array.isArray(config.api_key_statuses) ? [...config.api_key_statuses] : []
  configForm.api_keys_mode = 'append'
  configForm.clear_api_key = false
  pendingDeleteApiKeyHashes.value = []
  testedApiKeyStatuses.value = []
  apiKeyRowsExpanded.value = false
  configForm.timeout_ms = config.timeout_ms || 3000
  configForm.retry_count = config.retry_count ?? 2
  configForm.sample_rate = config.sample_rate ?? 100
  configForm.all_groups = config.all_groups
  configForm.group_ids = Array.isArray(config.group_ids) ? [...config.group_ids] : []
  configForm.record_non_hits = config.record_non_hits
  configForm.worker_count = config.worker_count || 4
  configForm.queue_size = config.queue_size || 32768
  configForm.block_status = config.block_status || 403
  configForm.block_message = config.block_message || defaultBlockMessage()
  configForm.email_on_hit = config.email_on_hit ?? true
  configForm.auto_ban_enabled = config.auto_ban_enabled ?? true
  configForm.cyber_policy_exclude_from_ban_count = config.cyber_policy_exclude_from_ban_count ?? false
  configForm.ban_threshold = config.ban_threshold || 10
  configForm.violation_window_hours = config.violation_window_hours || 720
  configForm.hit_retention_days = config.hit_retention_days || 180
  configForm.non_hit_retention_days = Math.min(Math.max(config.non_hit_retention_days || 3, 1), 3)
  configForm.pre_hash_check_enabled = config.pre_hash_check_enabled ?? false
  configForm.thresholds = riskThresholdsFromConfig(config.thresholds)
  configForm.category_settings = categorySettingsFromConfig(config.category_settings)
  configForm.blocked_keywords_text = Array.isArray(config.blocked_keywords) ? config.blocked_keywords.join('\n') : ''
  configForm.keyword_blocking_mode = normalizeKeywordBlockingMode(config.keyword_blocking_mode)
  const modelFilter = normalizeModelFilter(config.model_filter)
  configForm.model_filter_type = modelFilter.type
  configForm.model_filter_models = modelFilter.models
  engineDrafts.value = {
    openai: engineDraftFromConfig(config.engine_configs?.openai ?? (savedEngine.value === 'openai' ? config : undefined), 'openai'),
    typesafe: engineDraftFromConfig(config.engine_configs?.typesafe ?? (savedEngine.value === 'typesafe' ? config : undefined), 'typesafe'),
  }
  const { pendingDeletes, ...fields } = structuredClone(toRaw(engineDrafts.value[configForm.engine]!))
  Object.assign(configForm, fields)
  pendingDeleteApiKeyHashes.value = [...pendingDeletes]
}

async function loadAll() {
  loading.value = true
  try {
    const [config, groupItems, runtimeStatus, proxyItems] = await Promise.all([
      adminAPI.riskControl.getConfig(),
      adminAPI.groups.getAll(),
      adminAPI.riskControl.getStatus(),
      // 代理列表加载失败不阻塞风控页面（仅影响下拉可选项）
      adminAPI.proxies.getAll().catch(() => [] as Proxy[]),
    ])
    applyConfig(config)
    groups.value = groupItems
    status.value = runtimeStatus
    proxies.value = proxyItems
    if ((runtimeStatus.engine ?? 'openai') === configForm.engine && Array.isArray(runtimeStatus.api_key_statuses)) {
      configForm.api_key_statuses = [...runtimeStatus.api_key_statuses]
      prunePendingDeleteAPIKeyHashes()
    }
    await loadLogs()
  } catch (err: unknown) {
    appStore.showError(extractApiErrorMessage(err, t('admin.riskControl.loadFailed')))
  } finally {
    loading.value = false
  }
}

async function loadStatus(silent = true) {
  statusLoading.value = true
  try {
    const runtimeStatus = await adminAPI.riskControl.getStatus()
    status.value = runtimeStatus
    if ((runtimeStatus.engine ?? 'openai') === configForm.engine && Array.isArray(runtimeStatus.api_key_statuses)) {
      configForm.api_key_statuses = [...runtimeStatus.api_key_statuses]
      prunePendingDeleteAPIKeyHashes()
    }
  } catch (err: unknown) {
    if (!silent) {
      appStore.showError(extractApiErrorMessage(err, t('admin.riskControl.statusFailed')))
    }
  } finally {
    statusLoading.value = false
  }
}

async function saveConfig() {
  saving.value = true
  try {
    const modelFilterPayload = buildModelFilterPayload()
    if (modelFilterPayload.type !== 'all' && modelFilterPayload.models.length === 0) {
      appStore.showError(t('admin.riskControl.modelFilterModelsRequired'))
      return
    }
    const payload: UpdateContentModerationConfig = {
      engine: configForm.engine,
      enabled: configForm.enabled,
      mode: configForm.mode,
      base_url: configForm.base_url,
      model: configForm.model,
      // 后端语义：0 清除代理（直连），>0 指定代理
      proxy_id: configForm.proxy_id ?? 0,
      timeout_ms: Number(configForm.timeout_ms) || 3000,
      retry_count: Number(configForm.retry_count) || 0,
      sample_rate: Number(configForm.sample_rate) || 0,
      all_groups: configForm.all_groups,
      group_ids: configForm.all_groups ? [] : [...configForm.group_ids],
      record_non_hits: configForm.record_non_hits,
      clear_api_key: configForm.clear_api_key,
      worker_count: Number(configForm.worker_count) || 4,
      queue_size: Number(configForm.queue_size) || 32768,
      block_status: Number(configForm.block_status) || 403,
      block_message: configForm.block_message || defaultBlockMessage(),
      email_on_hit: configForm.email_on_hit,
      auto_ban_enabled: configForm.auto_ban_enabled,
      cyber_policy_exclude_from_ban_count: configForm.cyber_policy_exclude_from_ban_count,
      ban_threshold: Number(configForm.ban_threshold) || 10,
      violation_window_hours: Number(configForm.violation_window_hours) || 720,
      hit_retention_days: Number(configForm.hit_retention_days) || 180,
      non_hit_retention_days: Math.min(Math.max(Number(configForm.non_hit_retention_days) || 3, 1), 3),
      pre_hash_check_enabled: configForm.pre_hash_check_enabled,
      thresholds: buildRiskThresholdPayload(),
      category_settings: structuredClone(toRaw(configForm.category_settings)),
      blocked_keywords: blockedKeywordList.value,
      keyword_blocking_mode: configForm.keyword_blocking_mode,
      model_filter: modelFilterPayload,
    }
    const keys = parseApiKeys(configForm.api_keys_text)
    if (!payload.clear_api_key && configForm.api_keys_mode === 'replace' && keys.length === 0) {
      appStore.showError(t('admin.riskControl.apiKeysReplaceNoInput'))
      return
    }
    if (keys.length > 0) {
      payload.api_keys = keys
      payload.api_keys_mode = configForm.api_keys_mode
      payload.clear_api_key = false
    }
    if (!payload.clear_api_key && configForm.api_keys_mode !== 'replace' && pendingDeleteApiKeyHashes.value.length > 0) {
      payload.delete_api_key_hashes = [...pendingDeleteApiKeyHashes.value]
    }

    engineDrafts.value[configForm.engine] = captureEngineDraft()
    payload.engine_configs = Object.fromEntries(Object.entries(engineDrafts.value).map(([engine, draft]) => [engine, engineDraftPayload(draft)]))
    const updated = await adminAPI.riskControl.updateConfig(payload)
    applyConfig(updated)
    settingsOpen.value = false
    appStore.showSuccess(t('admin.riskControl.saved'))
    await Promise.all([loadStatus(true), loadLogs()])
  } catch (err: unknown) {
    appStore.showError(extractApiErrorMessage(err, t('admin.riskControl.saveFailed')))
  } finally {
    saving.value = false
  }
}

async function loadLogs() {
  logsLoading.value = true
  try {
    const params = {
      page: pagination.page,
      page_size: pagination.page_size,
      result: filters.result || undefined,
      group_id: filters.group_id || undefined,
      endpoint: filters.endpoint || undefined,
      search: filters.search || undefined,
      from: normalizeDateTimeLocal(filters.from),
      to: normalizeDateTimeLocal(filters.to),
    }
    const result = await adminAPI.riskControl.listLogs(params)
    logs.value = result.items
    pagination.total = result.total
    pagination.page = result.page
    pagination.page_size = result.page_size
    pagination.pages = result.pages
  } catch (err: unknown) {
    appStore.showError(extractApiErrorMessage(err, t('admin.riskControl.logsFailed')))
  } finally {
    logsLoading.value = false
  }
}

function canUnbanRow(row: ContentModerationLog): boolean {
  return Boolean(row.auto_banned && row.user_id && row.user_status === 'disabled')
}

function inputSummaryText(row: ContentModerationLog): string {
  const text = row.input_excerpt || row.error || '-'
  if (text.length <= 80) return text
  return `${text.slice(0, 80)}…`
}

function openInputDetail(row: ContentModerationLog) {
  inputDetailRow.value = row
}

function closeInputDetail() {
  inputDetailRow.value = null
}

async function unbanUser(row: ContentModerationLog) {
  if (!row.user_id || unbanningUserID.value !== null) return
  unbanningUserID.value = row.user_id
  try {
    const result = await adminAPI.riskControl.unbanUser(row.user_id)
    logs.value = logs.value.map((item) => {
      if (item.user_id !== row.user_id) return item
      return { ...item, user_status: result.status }
    })
    appStore.showSuccess(t('admin.riskControl.unbanSuccess'))
  } catch (err: unknown) {
    appStore.showError(extractApiErrorMessage(err, t('admin.riskControl.unbanFailed')))
  } finally {
    unbanningUserID.value = null
  }
}

async function deleteFlaggedHash() {
  if (!isFlaggedHashInputValid.value || hashActionLoading.value) return
  hashActionLoading.value = true
  try {
    const result = await adminAPI.riskControl.deleteFlaggedHash(flaggedHashInput.value)
    flaggedHashInput.value = ''
    await loadStatus(true)
    appStore.showSuccess(result.deleted ? t('admin.riskControl.flaggedHashDeleted') : t('admin.riskControl.flaggedHashNotFound'))
  } catch (err: unknown) {
    appStore.showError(extractApiErrorMessage(err, t('admin.riskControl.flaggedHashDeleteFailed')))
  } finally {
    hashActionLoading.value = false
  }
}

async function clearFlaggedHashes() {
  if (hashActionLoading.value) return
  const confirmed = window.confirm(t('admin.riskControl.clearFlaggedHashesConfirm'))
  if (!confirmed) return
  hashActionLoading.value = true
  try {
    const result = await adminAPI.riskControl.clearFlaggedHashes()
    await loadStatus(true)
    appStore.showSuccess(t('admin.riskControl.flaggedHashesCleared', { count: result.deleted }))
  } catch (err: unknown) {
    appStore.showError(extractApiErrorMessage(err, t('admin.riskControl.flaggedHashesClearFailed')))
  } finally {
    hashActionLoading.value = false
  }
}

function openSettings() {
  activeSettingsTab.value = 'basic'
  settingsOpen.value = true
}

function reloadLogsFromFirstPage() {
  pagination.page = 1
  void loadLogs()
}

function onPageChange(page: number) {
  pagination.page = page
  void loadLogs()
}

function onPageSizeChange(pageSize: number) {
  pagination.page = 1
  pagination.page_size = pageSize
  void loadLogs()
}

function toggleClearApiKey() {
  configForm.clear_api_key = !configForm.clear_api_key
  if (configForm.clear_api_key) {
    configForm.api_keys_text = ''
    configForm.api_keys_mode = 'append'
    testedApiKeyStatuses.value = []
    pendingDeleteApiKeyHashes.value = []
  }
}

function setAPIKeysMode(mode: APIKeysWriteMode) {
  configForm.api_keys_mode = mode
  if (mode === 'replace') {
    pendingDeleteApiKeyHashes.value = []
  }
}

function setModelFilterType(type: ContentModerationModelFilterType) {
  configForm.model_filter_type = type
  if (type === 'all') {
    configForm.model_filter_models = []
  }
}

async function testApiKeys(useInputKeys: boolean) {
  const keys = useInputKeys ? parseApiKeys(configForm.api_keys_text) : []
  if (useInputKeys && keys.length === 0) {
    appStore.showError(t('admin.riskControl.apiKeyTestNoInput'))
    return
  }
  apiKeyTesting.value = true
  try {
    const result = await adminAPI.riskControl.testAPIKeys({
      engine: configForm.engine,
      thresholds: buildRiskThresholdPayload(),
      category_settings: structuredClone(toRaw(configForm.category_settings)),
      api_keys: keys,
      base_url: configForm.base_url,
      model: configForm.model,
      timeout_ms: Number(configForm.timeout_ms) || 3000,
      // 与保存语义一致：0 强制直连，>0 指定代理，确保测试与实际审计走同一条链路
      proxy_id: configForm.proxy_id ?? 0,
      prompt: moderationTestPrompt.value,
      images: moderationTestImages.value,
    })
    moderationTestResult.value = result.audit_result ?? null
    if (useInputKeys) {
      testedApiKeyStatuses.value = result.items.map((item) => ({ ...item, configured: false }))
    } else {
      mergeConfiguredAPIKeyStatuses(result.items)
      testedApiKeyStatuses.value = []
      await loadStatus(true)
    }
    if (result.items.length === 0 || result.items.some(item => item.status === 'error' || item.status === 'frozen')) {
      appStore.showError(t('admin.riskControl.apiKeyTestFailed'))
    } else {
      appStore.showSuccess(t('admin.riskControl.apiKeyTestDone', { count: result.items.length }))
    }
  } catch (err: unknown) {
    appStore.showError(extractApiErrorMessage(err, t('admin.riskControl.apiKeyTestFailed')))
  } finally {
    apiKeyTesting.value = false
  }
}

function mergeConfiguredAPIKeyStatuses(items: ContentModerationAPIKeyStatus[]) {
  if (!hasModerationAuditInput.value || configForm.api_key_statuses.length === 0) {
    configForm.api_key_statuses = items
    return
  }
  const updates = new Map(items.map((item) => [item.key_hash, item]))
  configForm.api_key_statuses = configForm.api_key_statuses.map((item) => updates.get(item.key_hash) ?? item)
}

function toggleDeleteStoredApiKey(row: ContentModerationAPIKeyStatus) {
  if (!row.configured || !row.key_hash) return
  const index = pendingDeleteApiKeyHashes.value.indexOf(row.key_hash)
  if (index >= 0) {
    pendingDeleteApiKeyHashes.value.splice(index, 1)
    return
  }
  pendingDeleteApiKeyHashes.value.push(row.key_hash)
}

function isStoredApiKeyPendingDelete(row: ContentModerationAPIKeyStatus): boolean {
  return row.configured && row.key_hash !== '' && pendingDeleteApiKeyHashes.value.includes(row.key_hash)
}

function prunePendingDeleteAPIKeyHashes() {
  const currentHashes = new Set(savedApiKeyRows.value.map((row) => row.key_hash).filter(Boolean))
  pendingDeleteApiKeyHashes.value = pendingDeleteApiKeyHashes.value.filter((hash) => currentHashes.has(hash))
}

function clearModerationTestInput() {
  moderationTestPrompt.value = ''
  moderationTestImages.value = []
  moderationTestResult.value = null
}

function removeModerationTestImage(index: number) {
  moderationTestImages.value.splice(index, 1)
}

async function handleModerationImagePaste(event: ClipboardEvent) {
  const files = Array.from(event.clipboardData?.files ?? []).filter((file) => file.type.startsWith('image/'))
  if (files.length === 0) return
  event.preventDefault()
  await addModerationTestFiles(files)
}

async function addModerationTestFiles(files: FileList | File[] | null) {
  if (!files) return
  const items = Array.from(files).filter((file) => file.type.startsWith('image/'))
  for (const file of items) {
    if (moderationTestImages.value.length >= maxModerationTestImages) {
      appStore.showError(t('admin.riskControl.auditTestImageLimit', { count: maxModerationTestImages }))
      return
    }
    if (file.size > maxModerationTestImageSize) {
      appStore.showError(t('admin.riskControl.auditTestImageTooLarge'))
      continue
    }
    try {
      moderationTestImages.value.push(await fileToDataURL(file))
    } catch {
      appStore.showError(t('admin.riskControl.auditTestImageReadFailed'))
    }
  }
}

function fileToDataURL(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => resolve(String(reader.result || ''))
    reader.onerror = () => reject(reader.error)
    reader.readAsDataURL(file)
  })
}

function toggleGroup(groupID: number) {
  const index = configForm.group_ids.indexOf(groupID)
  if (index >= 0) {
    configForm.group_ids.splice(index, 1)
  } else {
    configForm.group_ids.push(groupID)
  }
}

function isGroupSelected(groupID: number): boolean {
  return configForm.group_ids.includes(groupID)
}

function modeLabel(mode: ModerationMode): string {
  const found = modeOptions.value.find((option) => option.value === mode)
  return found?.label ?? mode
}

function modeDescription(mode: ModerationMode): string {
  const descriptions: Record<ModerationMode, string> = {
    pre_block: t('admin.riskControl.modePreBlockDesc'),
    observe: t('admin.riskControl.modeObserveDesc'),
    off: t('admin.riskControl.modeOffDesc'),
  }
  return descriptions[mode] ?? ''
}

function resultLabel(row: ContentModerationLog): string {
  if (row.action === 'cyber_policy') return t('admin.riskControl.action.cyberPolicy')
  if (row.action === 'keyword_block') return t('admin.riskControl.action.keywordBlock')
  if (row.action === 'block') return t('admin.riskControl.action.block')
  if (row.action === 'error' || row.error) return t('admin.riskControl.action.error')
  if (row.flagged) return t('admin.riskControl.result.hit')
  return t('admin.riskControl.result.pass')
}

function resultBadgeColor(row: ContentModerationLog): BadgeColor {
  if (row.action === 'block' || row.action === 'keyword_block' || row.action === 'cyber_policy') return 'error'
  if (row.action === 'error' || row.error) return 'neutral'
  if (row.flagged) return 'warning'
  return 'success'
}

function workerSlotClass(state: WorkerSlotState): string {
  if (state === 'active') return 'bg-info/10 text-info ring-info/25'
  if (state === 'idle') return 'bg-success/10 text-success ring-success/25'
  return 'bg-default text-dimmed ring-default'
}

function workerDotClass(state: WorkerSlotState): string {
  if (state === 'active') return 'bg-info'
  if (state === 'idle') return 'bg-success'
  return 'bg-accented'
}

function percent(value: number): string {
  if (!Number.isFinite(value)) return '-'
  return `${(value * 100).toFixed(1)}%`
}

function percentValue(value: number): number {
  if (!Number.isFinite(value)) return 0
  return Math.min(100, Math.max(0, value * 100))
}

function latencyText(value: number | null): string {
  if (value === null || value === undefined) return '-'
  return `${value} ms`
}

function apiKeyRowKey(row: ContentModerationAPIKeyStatus, index: number): string {
  return `${row.configured ? 'saved' : 'test'}-${row.key_hash || index}`
}

function apiKeyStatusLabel(statusValue: ContentModerationAPIKeyStatus['status']): string {
  const labels: Record<ContentModerationAPIKeyStatus['status'], string> = {
    ok: t('admin.riskControl.apiKeyStatusOk'),
    error: t('admin.riskControl.apiKeyStatusError'),
    frozen: t('admin.riskControl.apiKeyStatusFrozen'),
    unknown: t('admin.riskControl.apiKeyStatusUnknown'),
  }
  return labels[statusValue] ?? labels.unknown
}

function apiKeyStatusColor(statusValue: ContentModerationAPIKeyStatus['status']): BadgeColor {
  const colors: Record<ContentModerationAPIKeyStatus['status'], BadgeColor> = {
    ok: 'success',
    error: 'warning',
    frozen: 'error',
    unknown: 'neutral',
  }
  return colors[statusValue] ?? colors.unknown
}

function apiKeyStatusDotClass(statusValue: ContentModerationAPIKeyStatus['status']): string {
  const classes: Record<ContentModerationAPIKeyStatus['status'], string> = {
    ok: 'bg-success',
    error: 'bg-warning',
    frozen: 'bg-error',
    unknown: 'bg-accented',
  }
  return classes[statusValue] ?? classes.unknown
}

function apiKeyStatusMeta(row: ContentModerationAPIKeyStatus): string {
  const parts: string[] = []
  parts.push(t('admin.riskControl.apiKeyFailureCount', { count: row.failure_count || 0 }))
  if (row.last_latency_ms > 0) {
    parts.push(t('admin.riskControl.apiKeyLatency', { ms: row.last_latency_ms }))
  }
  if (row.last_http_status > 0) {
    parts.push(t('admin.riskControl.apiKeyHTTPStatus', { status: row.last_http_status }))
  }
  if (row.frozen_until) {
    parts.push(t('admin.riskControl.apiKeyFrozenUntil', { time: formatDateTime(row.frozen_until) }))
  } else if (row.last_checked_at) {
    parts.push(t('admin.riskControl.apiKeyLastChecked', { time: formatDateTime(row.last_checked_at) }))
  } else {
    parts.push(t('admin.riskControl.apiKeyNotTested'))
  }
  return parts.join(' / ')
}

function parseApiKeys(value: string): string[] {
  return value
    .split(/\r?\n/)
    .map((item) => item.trim())
    .filter((item, index, arr) => item && arr.indexOf(item) === index)
}

function normalizeKeywordBlockingMode(value: unknown): KeywordBlockingMode {
  if (value === 'keyword_only' || value === 'api_only' || value === 'keyword_and_api') {
    return value
  }
  return 'keyword_and_api'
}

function normalizeModelFilter(value: unknown): ContentModerationModelFilter {
  if (!value || typeof value !== 'object') {
    return { type: 'all', models: [] }
  }
  const raw = value as Partial<ContentModerationModelFilter>
  const type = normalizeModelFilterType(raw.type)
  const models = type === 'all' ? [] : normalizeModelNames(raw.models)
  return { type, models }
}

function normalizeModelFilterType(value: unknown): ContentModerationModelFilterType {
  if (value === 'include' || value === 'exclude' || value === 'all') {
    return value
  }
  return 'all'
}

function normalizeModelNames(models: unknown): string[] {
  if (!Array.isArray(models)) return []
  const seen = new Set<string>()
  const out: string[] = []
  for (const item of models) {
    const model = String(item ?? '').trim()
    if (!model) continue
    const key = model.toLowerCase()
    if (seen.has(key)) continue
    seen.add(key)
    out.push(model)
  }
  return out
}

function buildModelFilterPayload(): ContentModerationModelFilter {
  const type = normalizeModelFilterType(configForm.model_filter_type)
  if (type === 'all') {
    return { type: 'all', models: [] }
  }
  return {
    type,
    models: normalizeModelNames(configForm.model_filter_models),
  }
}

function riskThresholdsFromConfig(thresholds: Record<string, number> | null | undefined): Record<string, number> {
  const out: Record<string, number> = { ...riskThresholdDefaults }
  for (const category of riskThresholdCategories) {
    const value = thresholds?.[category]
    if (Number.isFinite(value)) {
      out[category] = clampPercent(Number(value) * 100)
    }
  }
  return out
}

function buildRiskThresholdPayload(): Record<string, number> {
  const payload: Record<string, number> = {}
  for (const category of riskThresholdCategories) {
    payload[category] = Number((clampPercent(configForm.thresholds[category]) / 100).toFixed(4))
  }
  return payload
}

function resetRiskThresholds() {
  configForm.thresholds = { ...riskThresholdDefaults }
}

function clampPercent(value: unknown): number {
  const numeric = Number(value)
  if (!Number.isFinite(numeric)) {
    return 0
  }
  return Math.min(100, Math.max(0, numeric))
}

function formatThresholdPercent(value: number): string {
  return `${clampPercent(value).toFixed(1)}%`
}

function parseBlockedKeywords(value: string): string[] {
  const seen = new Set<string>()
  const out: string[] = []
  for (const line of value.split(/\r?\n/)) {
    const kw = line.trim()
    if (!kw) continue
    const key = kw.toLowerCase()
    if (seen.has(key)) continue
    seen.add(key)
    out.push(kw)
  }
  return out
}

function violationCountText(row: ContentModerationLog): string {
  if (!row.flagged) return '-'
  if (row.violation_count === 0) return t('admin.riskControl.violationNotCounted')
  return t('admin.riskControl.violationCount', { count: row.violation_count || 1 })
}

function normalizeDateTimeLocal(value: string): string | undefined {
  if (!value) return undefined
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return undefined
  return date.toISOString()
}

function formatDateTime(value: string): string {
  return formatDateTimeValue(value) || '-'
}

function formatNumber(value: number): string {
  return new Intl.NumberFormat().format(value)
}

onMounted(() => {
  void loadAll()
  statusTimer = window.setInterval(() => {
    void loadStatus(true)
  }, 15000)
})

onUnmounted(() => {
  if (statusTimer !== null) {
    window.clearInterval(statusTimer)
    statusTimer = null
  }
})
</script>
