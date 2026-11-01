<template>
  <AppLayout>
    <TablePageLayout>
      <template #filters>
        <AdminPageHeader
          class="mb-4"
          :title="t('admin.proxies.title')"
          :description="t('admin.proxies.description')"
        >
          <template #actions>
            <UButton icon="i-lucide-plus" :label="t('admin.proxies.createProxy')" @click="showCreateModal = true" />
          </template>
        </AdminPageHeader>
        <!-- Toolbar row (template customers page) -->
        <PageToolbar>
          <template #left>
            <UInput
              v-model="searchQuery"
              icon="i-lucide-search"
              :placeholder="t('admin.proxies.searchProxies')"
              class="w-56 sm:w-64"
              @update:model-value="handleSearch"
            />
            <div class="w-40">
              <Select
                v-model="filters.protocol"
                :options="protocolOptions"
                :placeholder="t('admin.proxies.allProtocols')"
                @change="handleFilterChange"
              />
            </div>
            <div class="w-36">
              <Select
                v-model="filters.status"
                :options="statusOptions"
                :placeholder="t('admin.proxies.allStatus')"
                @change="handleFilterChange"
              />
            </div>
          </template>
          <template #right>
            <UButton
              v-if="selectedCount > 0"
              color="error"
              variant="subtle"
              icon="i-lucide-trash"
              :label="t('common.delete')"
              :title="t('admin.proxies.batchDeleteAction')"
              @click="openBatchDelete"
            >
              <template #trailing>
                <UBadge :label="String(selectedCount)" color="error" variant="outline" size="sm" />
              </template>
            </UButton>
            <UButton
              color="neutral"
              variant="outline"
              icon="i-lucide-refresh-cw"
              square
              :disabled="loading"
              :ui="{ leadingIcon: loading ? 'animate-spin' : '' }"
              :title="t('common.refresh')"
              :aria-label="t('common.refresh')"
              @click="loadProxies"
            />
            <UButton
              color="neutral"
              variant="outline"
              icon="i-lucide-play"
              :disabled="batchTesting || loading"
              :ui="{ leadingIcon: batchTesting ? 'animate-pulse' : '' }"
              :title="t('admin.proxies.testConnection')"
              :label="t('admin.proxies.testConnection')"
              @click="handleBatchTest"
            />
            <UDropdownMenu :items="toolbarMenuItems" :content="{ align: 'end' }">
              <UButton color="neutral" variant="outline" trailing-icon="i-lucide-chevron-down" :label="t('common.more')" />
            </UDropdownMenu>
            <UDropdownMenu :items="columnMenuItems" :content="{ align: 'end' }">
              <UButton
                color="neutral"
                variant="outline"
                trailing-icon="i-lucide-settings-2"
                :title="t('admin.proxies.displayColumns')"
                :aria-label="t('admin.proxies.displayColumns')"
              >
                <span class="hidden sm:inline">{{ t('admin.proxies.displayColumns') }}</span>
              </UButton>
            </UDropdownMenu>
          </template>
        </PageToolbar>
      </template>

      <template #table>
        <div ref="proxyTableRef" class="flex min-h-0 flex-1 flex-col overflow-hidden">
        <DataTable
          :columns="visibleColumns"
          :data="proxies"
          :loading="loading"
          :server-side-sort="true"
          default-sort-key="id"
          default-sort-order="desc"
          @sort="handleSort"
        >
          <template #header-select>
            <div @click.stop>
              <UCheckbox
                :model-value="allVisibleSelected"
                :aria-label="t('common.all')"
                @update:model-value="(value: boolean | 'indeterminate') => toggleVisible(!!value)"
              />
            </div>
          </template>

          <template #cell-select="{ row }">
            <div @click.stop>
              <UCheckbox
                :model-value="selectedProxyIds.has(row.id)"
                :aria-label="row.name"
                @update:model-value="(value: boolean | 'indeterminate') => (value ? select(row.id) : deselect(row.id))"
              />
            </div>
          </template>

          <template #cell-name="{ value }">
            <span class="font-medium text-highlighted">{{ value }}</span>
          </template>

          <template #cell-protocol="{ value }">
            <UBadge
              v-if="value"
              :color="value.startsWith('socks5') ? 'primary' : 'neutral'"
              variant="subtle"
              :label="value.toUpperCase()"
            />
            <span v-else class="text-dimmed">-</span>
          </template>

          <template #cell-address="{ row }">
            <div class="flex items-center gap-1">
              <code class="font-mono text-xs text-default">{{ row.host }}:{{ row.port }}</code>
              <!-- Click copies the URL; right click lists the other formats -->
              <UContextMenu :items="copyFormatMenuItems(row)">
                <UButton
                  color="neutral"
                  variant="ghost"
                  size="xs"
                  square
                  icon="i-lucide-copy"
                  :title="t('admin.proxies.copyProxyUrl')"
                  :aria-label="t('admin.proxies.copyProxyUrl')"
                  @click.stop="copyProxyUrl(row)"
                />
              </UContextMenu>
            </div>
          </template>

          <template #cell-auth="{ row }">
            <div v-if="row.username || row.password" class="flex items-center gap-1">
              <div class="flex flex-col text-xs">
                <span v-if="row.username" class="text-default">{{ row.username }}</span>
                <span v-if="row.password" class="font-mono text-muted">
                  {{ visiblePasswordIds.has(row.id) ? row.password : '••••••' }}
                </span>
              </div>
              <UButton
                v-if="row.password"
                color="neutral"
                variant="ghost"
                size="xs"
                square
                :icon="visiblePasswordIds.has(row.id) ? 'i-lucide-eye-off' : 'i-lucide-eye'"
                :aria-label="t('admin.proxies.password')"
                @click.stop="visiblePasswordIds.has(row.id) ? visiblePasswordIds.delete(row.id) : visiblePasswordIds.add(row.id)"
              />
            </div>
            <span v-else class="text-dimmed">-</span>
          </template>

          <template #cell-location="{ row }">
            <div class="flex items-center gap-2">
              <img
                v-if="row.country_code"
                :src="flagUrl(row.country_code)"
                :alt="row.country || row.country_code"
                class="h-4 w-6 rounded-xs"
              />
              <span v-if="formatLocation(row)" class="text-default">{{ formatLocation(row) }}</span>
              <span v-else class="text-dimmed">-</span>
            </div>
          </template>

          <template #cell-account_count="{ row, value }">
            <UButton
              v-if="(value || 0) > 0"
              color="neutral"
              variant="soft"
              size="xs"
              :label="t('admin.groups.accountsCount', { count: value || 0 })"
              @click="openAccountsModal(row)"
            />
            <span v-else class="text-xs text-muted">{{ t('admin.groups.accountsCount', { count: 0 }) }}</span>
          </template>

          <template #cell-latency="{ row }">
            <div class="flex flex-col items-start gap-1">
              <UBadge
                v-if="row.latency_status === 'failed'"
                color="error"
                variant="subtle"
                :label="t('admin.proxies.latencyFailed')"
                :title="row.latency_message || undefined"
              />
              <UBadge
                v-else-if="typeof row.latency_ms === 'number'"
                :color="row.latency_ms < 200 ? 'success' : 'warning'"
                variant="subtle"
                class="tabular-nums"
                :label="`${row.latency_ms}ms`"
              />
              <span v-else class="text-dimmed">-</span>
              <div
                v-if="typeof row.quality_checked === 'number'"
                class="flex items-center gap-1 text-xs text-muted"
                :title="row.quality_summary || undefined"
              >
                <span>{{ t('admin.proxies.qualityInline', { grade: row.quality_grade || '-', score: row.quality_score ?? '-' }) }}</span>
                <UBadge :color="qualityOverallColor(row.quality_status)" variant="subtle" size="sm" :label="qualityOverallLabel(row.quality_status)" />
              </div>
            </div>
          </template>

          <template #cell-expiry="{ row }">
            <span v-if="!row.expires_at" class="text-muted">{{ t('admin.proxies.neverExpires') }}</span>
            <div v-else class="flex flex-col items-start gap-0.5 text-xs">
              <span class="text-default">{{ formatDateTime(row.expires_at) }}</span>
              <UBadge v-if="expiryColor(row)" :color="expiryColor(row)!" variant="subtle" size="sm" :label="expiryLabel(row)" />
              <span v-else class="text-muted">{{ expiryLabel(row) }}</span>
            </div>
          </template>

          <template #cell-created_at="{ row }">
            <span class="text-muted">{{ formatDateTime(row.created_at) }}</span>
          </template>

          <template #cell-status="{ value }">
            <UBadge :color="value === 'active' ? 'success' : 'error'" variant="subtle" :label="t('admin.accounts.status.' + value)" />
          </template>

          <template #cell-actions="{ row }">
            <div class="flex items-center justify-end gap-0.5">
              <AdminRowAction
                :disabled="testingProxyIds.has(row.id)"
                :label="t('admin.proxies.testConnection')"
                tone="success"
                @click="handleTestConnection(row)"
              >
                <template #icon>
                  <UIcon v-if="testingProxyIds.has(row.id)" name="i-lucide-loader-circle" class="size-4 animate-spin" />
                  <UIcon v-else name="i-lucide-circle-check" class="size-4" />
                </template>
              </AdminRowAction>
              <AdminRowAction :label="t('common.edit')" icon="edit" tone="primary" @click="handleEdit(row)" />
              <UDropdownMenu :items="rowMenuItems(row)" :content="{ align: 'end' }">
                <AdminRowAction :label="t('common.more')" icon="more" />
              </UDropdownMenu>
            </div>
          </template>

          <template #empty>
            <EmptyState
              :title="t('admin.proxies.noProxiesYet')"
              :description="t('admin.proxies.createFirstProxy')"
              :action-text="t('admin.proxies.createProxy')"
              @action="showCreateModal = true"
            />
          </template>
        </DataTable>
        </div>
      </template>

      <template #pagination>
        <Pagination
          v-if="pagination.total > 0"
          :page="pagination.page"
          :total="pagination.total"
          :page-size="pagination.page_size"
          @update:page="handlePageChange"
          @update:pageSize="handlePageSizeChange"
        />
      </template>
    </TablePageLayout>

    <!-- Create Proxy Modal -->
    <BaseDialog
      :show="showCreateModal"
      :title="t('admin.proxies.createProxy')"
      width="normal"
      @close="closeCreateModal"
    >
      <div class="mb-5 flex items-center justify-between gap-3">
        <UTabs
          :model-value="createMode"
          :items="createModeTabs"
          @update:model-value="(value: string | number) => (createMode = value === 'batch' ? 'batch' : 'standard')"
          :content="false"
          variant="link"
          size="sm"
        />
        <ProxyAdBanner />
      </div>

      <UForm
        v-if="createMode === 'standard'"
        id="create-proxy-form"
        :state="createForm"
        class="space-y-4"
        @submit="handleCreateProxy"
      >
        <UFormField :label="t('admin.proxies.name')" required>
          <UInput v-model="createForm.name" required class="w-full" :placeholder="t('admin.proxies.enterProxyName')" />
        </UFormField>
        <UFormField :label="t('admin.proxies.protocol')">
          <Select v-model="createForm.protocol" :options="protocolSelectOptions" />
        </UFormField>
        <div class="grid grid-cols-2 gap-4">
          <UFormField :label="t('admin.proxies.host')" required>
            <UInput v-model="createForm.host" required class="w-full" :placeholder="t('admin.proxies.form.hostPlaceholder')" />
          </UFormField>
          <UFormField :label="t('admin.proxies.port')" required>
            <UInput
              v-model.number="createForm.port"
              type="number"
              required
              min="1"
              max="65535"
              class="w-full"
              :placeholder="t('admin.proxies.form.portPlaceholder')"
            />
          </UFormField>
        </div>
        <UFormField :label="t('admin.proxies.username')">
          <UInput v-model="createForm.username" class="w-full" :placeholder="t('admin.proxies.optionalAuth')" />
        </UFormField>
        <UFormField :label="t('admin.proxies.password')">
          <UInput
            v-model="createForm.password"
            :type="createPasswordVisible ? 'text' : 'password'"
            class="w-full"
            :placeholder="t('admin.proxies.optionalAuth')"
            :ui="{ trailing: 'pe-1' }"
          >
            <template #trailing>
              <UButton
                color="neutral"
                variant="link"
                size="sm"
                :icon="createPasswordVisible ? 'i-lucide-eye-off' : 'i-lucide-eye'"
                :aria-label="t('admin.proxies.password')"
                @click="createPasswordVisible = !createPasswordVisible"
              />
            </template>
          </UInput>
        </UFormField>
        <UFormField :label="t('admin.proxies.expiresAt')">
          <div class="space-y-2">
            <div class="flex flex-wrap gap-1.5">
              <UButton
                v-for="d in EXPIRY_PRESETS"
                :key="d"
                size="xs"
                :color="createForm.expires_at === addDaysToBase('', d) ? 'primary' : 'neutral'"
                :variant="createForm.expires_at === addDaysToBase('', d) ? 'solid' : 'outline'"
                :label="t('admin.proxies.nDays', { days: d })"
                @click="createExpiresDays = d"
              />
            </div>
            <UInput v-model.number="createExpiresDays" type="number" min="0" class="w-full" :placeholder="t('admin.proxies.expiryDaysPlaceholder')" />
            <UInput v-model="createForm.expires_at" type="date" max="9999-12-31" class="w-full" />
          </div>
        </UFormField>
        <UCollapsible :unmount-on-hide="false" class="flex flex-col gap-4">
          <UButton
            color="neutral"
            variant="link"
            size="sm"
            class="self-start px-0"
            trailing-icon="i-lucide-chevron-down"
            :label="t('admin.proxies.advancedOptions')"
            :ui="{ trailingIcon: 'group-data-[state=open]:rotate-180 transition-transform' }"
          />
          <template #content>
            <div class="space-y-4">
              <UFormField :label="t('admin.proxies.fallbackMode')">
                <Select v-model="createForm.fallback_mode" :options="fallbackModeOptions" />
              </UFormField>
              <UFormField v-if="createForm.fallback_mode === 'proxy'" :label="t('admin.proxies.backupProxy')">
                <Select v-model="createForm.backup_proxy_id" :options="backupProxyOptions()" />
              </UFormField>
            </div>
          </template>
        </UCollapsible>
      </UForm>

      <!-- Batch Add Form -->
      <div v-else class="space-y-4">
        <UFormField :label="t('admin.proxies.batchInput')" :help="t('admin.proxies.batchInputHint')">
          <UTextarea
            v-model="batchInput"
            :rows="10"
            class="w-full font-mono"
            :placeholder="t('admin.proxies.batchInputPlaceholder')"
            @update:model-value="parseBatchInput"
          />
        </UFormField>
        <div v-if="batchParseResult.total > 0" class="flex flex-wrap items-center gap-4 text-sm">
          <span class="flex items-center gap-1.5 text-default">
            <UIcon name="i-lucide-circle-check" class="size-4 text-success" />
            {{ t('admin.proxies.parsedCount', { count: batchParseResult.valid }) }}
          </span>
          <span v-if="batchParseResult.invalid > 0" class="flex items-center gap-1.5 text-warning">
            <UIcon name="i-lucide-circle-alert" class="size-4" />
            {{ t('admin.proxies.invalidCount', { count: batchParseResult.invalid }) }}
          </span>
          <span v-if="batchParseResult.duplicate > 0" class="flex items-center gap-1.5 text-muted">
            <UIcon name="i-lucide-copy" class="size-4" />
            {{ t('admin.proxies.duplicateCount', { count: batchParseResult.duplicate }) }}
          </span>
        </div>
      </div>

      <template #footer>
        <div class="flex justify-end gap-2">
          <UButton color="neutral" variant="subtle" :label="t('common.cancel')" @click="closeCreateModal" />
          <UButton
            v-if="createMode === 'standard'"
            type="submit"
            form="create-proxy-form"
            :loading="submitting"
            :label="submitting ? t('admin.proxies.creating') : t('common.create')"
          />
          <UButton
            v-else
            :loading="submitting"
            :disabled="submitting || batchParseResult.valid === 0"
            :label="submitting ? t('admin.proxies.importing') : t('admin.proxies.importProxies', { count: batchParseResult.valid })"
            @click="handleBatchCreate"
          />
        </div>
      </template>
    </BaseDialog>

    <!-- Edit Proxy Modal -->
    <BaseDialog
      :show="showEditModal"
      :title="t('admin.proxies.editProxy')"
      width="normal"
      @close="closeEditModal"
    >
      <UForm
        v-if="editingProxy"
        id="edit-proxy-form"
        :state="editForm"
        class="space-y-4"
        @submit="handleUpdateProxy"
      >
        <UFormField :label="t('admin.proxies.name')" required>
          <UInput v-model="editForm.name" required class="w-full" />
        </UFormField>
        <UFormField :label="t('admin.proxies.protocol')">
          <Select v-model="editForm.protocol" :options="protocolSelectOptions" />
        </UFormField>
        <div class="grid grid-cols-2 gap-4">
          <UFormField :label="t('admin.proxies.host')" required>
            <UInput v-model="editForm.host" required class="w-full" />
          </UFormField>
          <UFormField :label="t('admin.proxies.port')" required>
            <UInput v-model.number="editForm.port" type="number" required min="1" max="65535" class="w-full" />
          </UFormField>
        </div>
        <UFormField :label="t('admin.proxies.username')">
          <UInput v-model="editForm.username" class="w-full" />
        </UFormField>
        <UFormField :label="t('admin.proxies.password')">
          <UInput
            v-model="editForm.password"
            :type="editPasswordVisible ? 'text' : 'password'"
            :placeholder="t('admin.proxies.leaveEmptyToKeep')"
            class="w-full"
            :ui="{ trailing: 'pe-1' }"
            @update:model-value="editPasswordDirty = true"
          >
            <template #trailing>
              <UButton
                color="neutral"
                variant="link"
                size="sm"
                :icon="editPasswordVisible ? 'i-lucide-eye-off' : 'i-lucide-eye'"
                :aria-label="t('admin.proxies.password')"
                @click="editPasswordVisible = !editPasswordVisible"
              />
            </template>
          </UInput>
        </UFormField>
        <UFormField :label="t('admin.proxies.status')">
          <Select v-model="editForm.status" :options="editStatusOptions" />
        </UFormField>
        <UFormField :label="t('admin.proxies.expiresAt')">
          <div class="space-y-2">
            <div class="flex flex-wrap gap-1.5">
              <UButton
                v-for="d in EXPIRY_PRESETS"
                :key="d"
                size="xs"
                :color="editForm.expires_at === addDaysToBase(editBaseDate, d) ? 'primary' : 'neutral'"
                :variant="editForm.expires_at === addDaysToBase(editBaseDate, d) ? 'solid' : 'outline'"
                :label="t('admin.proxies.nDays', { days: d })"
                @click="editExpiresDays = d"
              />
            </div>
            <UInput v-model.number="editExpiresDays" type="number" min="0" class="w-full" :placeholder="t('admin.proxies.expiryDaysPlaceholder')" />
            <UInput v-model="editForm.expires_at" type="date" max="9999-12-31" class="w-full" />
          </div>
        </UFormField>
        <UCollapsible :unmount-on-hide="false" class="flex flex-col gap-4">
          <UButton
            color="neutral"
            variant="link"
            size="sm"
            class="self-start px-0"
            trailing-icon="i-lucide-chevron-down"
            :label="t('admin.proxies.advancedOptions')"
            :ui="{ trailingIcon: 'group-data-[state=open]:rotate-180 transition-transform' }"
          />
          <template #content>
            <div class="space-y-4">
              <UFormField :label="t('admin.proxies.fallbackMode')">
                <Select v-model="editForm.fallback_mode" :options="fallbackModeOptions" />
              </UFormField>
              <UFormField v-if="editForm.fallback_mode === 'proxy'" :label="t('admin.proxies.backupProxy')">
                <Select v-model="editForm.backup_proxy_id" :options="backupProxyOptions(editingProxy?.id)" />
              </UFormField>
            </div>
          </template>
        </UCollapsible>
      </UForm>

      <template #footer>
        <div class="flex justify-end gap-2">
          <UButton color="neutral" variant="subtle" :label="t('common.cancel')" @click="closeEditModal" />
          <UButton
            v-if="editingProxy"
            type="submit"
            form="edit-proxy-form"
            :loading="submitting"
            :label="submitting ? t('admin.proxies.updating') : t('common.update')"
          />
        </div>
      </template>
    </BaseDialog>

    <!-- Delete Confirmation Dialog -->
    <ConfirmDialog
      :show="showDeleteDialog"
      :title="t('admin.proxies.deleteProxy')"
      :message="t('admin.proxies.deleteConfirm', { name: deletingProxy?.name })"
      :confirm-text="t('common.delete')"
      :cancel-text="t('common.cancel')"
      :danger="true"
      @confirm="confirmDelete"
      @cancel="showDeleteDialog = false"
    />

    <!-- Batch Delete Confirmation Dialog -->
    <ConfirmDialog
      :show="showBatchDeleteDialog"
      :title="t('admin.proxies.batchDelete')"
      :message="t('admin.proxies.batchDeleteConfirm', { count: selectedCount })"
      :confirm-text="t('common.delete')"
      :cancel-text="t('common.cancel')"
      :danger="true"
      @confirm="confirmBatchDelete"
      @cancel="showBatchDeleteDialog = false"
    />
    <ConfirmDialog
      :show="showExportDataDialog"
      :title="t('admin.proxies.dataExport')"
      :message="t('admin.proxies.dataExportConfirmMessage')"
      :confirm-text="t('admin.proxies.dataExportConfirm')"
      :cancel-text="t('common.cancel')"
      @confirm="handleExportData"
      @cancel="showExportDataDialog = false"
    />

    <ImportDataModal
      :show="showImportData"
      @close="showImportData = false"
      @imported="handleDataImported"
    />


    <BaseDialog
      :show="showQualityReportDialog"
      :title="t('admin.proxies.qualityReportTitle')"
      width="normal"
      @close="closeQualityReportDialog"
    >
      <div v-if="qualityReport" class="space-y-4">
        <div class="flex items-start justify-between gap-4">
          <div class="min-w-0">
            <p class="text-sm text-muted">{{ qualityReportProxy?.name || '-' }}</p>
            <p class="mt-1 text-sm text-default">{{ qualityReport.summary }}</p>
          </div>
          <div class="text-right">
            <p class="text-2xl font-semibold tabular-nums text-highlighted">{{ qualityReport.score }}</p>
            <p class="text-xs text-muted">{{ t('admin.proxies.qualityGrade', { grade: qualityReport.grade }) }}</p>
          </div>
        </div>
        <dl class="grid grid-cols-2 gap-x-4 gap-y-1 text-xs">
          <div class="flex gap-1"><dt class="text-muted">{{ t('admin.proxies.qualityExitIP') }}:</dt><dd class="text-default">{{ qualityReport.exit_ip || '-' }}</dd></div>
          <div class="flex gap-1"><dt class="text-muted">{{ t('admin.proxies.qualityCountry') }}:</dt><dd class="text-default">{{ qualityReport.country || '-' }}</dd></div>
          <div class="flex gap-1">
            <dt class="text-muted">{{ t('admin.proxies.qualityBaseLatency') }}:</dt>
            <dd class="text-default">{{ typeof qualityReport.base_latency_ms === 'number' ? `${qualityReport.base_latency_ms}ms` : '-' }}</dd>
          </div>
          <div class="flex gap-1"><dt class="text-muted">{{ t('admin.proxies.qualityCheckedAt') }}:</dt><dd class="text-default">{{ new Date(qualityReport.checked_at * 1000).toLocaleString() }}</dd></div>
        </dl>
        <UTable :data="qualityReport.items" :columns="qualityColumns" class="max-h-80">
          <template #target-cell="{ row }">
            <span class="text-highlighted">{{ qualityTargetLabel(row.original.target) }}</span>
          </template>
          <template #status-cell="{ row }">
            <UBadge :color="qualityStatusColor(row.original.status)" variant="subtle" :label="qualityStatusLabel(row.original.status)" />
          </template>
          <template #http_status-cell="{ row }">{{ row.original.http_status ?? '-' }}</template>
          <template #latency_ms-cell="{ row }">
            {{ typeof row.original.latency_ms === 'number' ? `${row.original.latency_ms}ms` : '-' }}
          </template>
          <template #message-cell="{ row }">
            <span class="whitespace-normal">{{ row.original.message || '-' }}</span>
            <span v-if="row.original.cf_ray" class="ml-1 text-xs text-dimmed">(cf-ray: {{ row.original.cf_ray }})</span>
          </template>
        </UTable>
      </div>
      <template #footer>
        <div class="flex justify-end">
          <UButton color="neutral" variant="subtle" :label="t('common.close')" @click="closeQualityReportDialog" />
        </div>
      </template>
    </BaseDialog>

    <!-- Proxy Accounts Dialog -->
    <BaseDialog
      :show="showAccountsModal"
      :title="t('admin.proxies.accountsTitle', { name: accountsProxy?.name || '' })"
      width="normal"
      @close="closeAccountsModal"
    >
      <p v-if="!accountsLoading && proxyAccounts.length === 0" class="py-6 text-center text-sm text-muted">
        {{ t('admin.proxies.accountsEmpty') }}
      </p>
      <UTable v-else :data="proxyAccounts" :columns="accountColumns" :loading="accountsLoading" class="max-h-80">
        <template #name-cell="{ row }">
          <span class="font-medium text-highlighted">{{ row.original.name }}</span>
        </template>
        <template #platform-cell="{ row }">
          <PlatformTypeBadge :platform="row.original.platform" :type="row.original.type" />
        </template>
        <template #notes-cell="{ row }">{{ row.original.notes || '-' }}</template>
      </UTable>
      <template #footer>
        <div class="flex justify-end">
          <UButton color="neutral" variant="subtle" :label="t('common.close')" @click="closeAccountsModal" />
        </div>
      </template>
    </BaseDialog>
  </AppLayout>
</template>

<script setup lang="ts">
import { ref, reactive, computed, onMounted, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { adminAPI } from '@/api/admin'
import type { Proxy, ProxyAccountSummary, ProxyProtocol, ProxyQualityCheckItem, ProxyQualityCheckResult } from '@/types'
import type { Column } from '@/components/common/types'
import type { ContextMenuItem, DropdownMenuItem, TableColumn } from '@nuxt/ui'
import { useListColumnMenu } from './useListColumnMenu'
import AppLayout from '@/components/layout/AppLayout.vue'
import TablePageLayout from '@/components/layout/TablePageLayout.vue'
import DataTable from '@/components/common/DataTable.vue'
import Pagination from '@/components/common/Pagination.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import ImportDataModal from '@/components/admin/proxy/ImportDataModal.vue'
import Select from '@/components/common/Select.vue'
import ProxyAdBanner from '@/components/common/ProxyAdBanner.vue'
import AdminPageHeader from '@/components/admin/page/AdminPageHeader.vue'
import PageToolbar from '@/components/layout/PageToolbar.vue'
import AdminRowAction from '@/components/admin/page/AdminRowAction.vue'
import PlatformTypeBadge from '@/components/common/PlatformTypeBadge.vue'
import { useClipboard } from '@/composables/useClipboard'
import { useSwipeSelect } from '@/composables/useSwipeSelect'
import { useTableSelection } from '@/composables/useTableSelection'
import { getPersistedPageSize } from '@/composables/usePersistedPageSize'
import { formatDateTime } from '@/utils/format'
import { proxyExpiryBadgeClass, proxyExpiryLabelKey } from '@/utils/proxyExpiry'

const { t } = useI18n()
const appStore = useAppStore()
const { copyToClipboard } = useClipboard()

const allColumns = computed<Column[]>(() => [
  { key: 'select', label: '', sortable: false },
  { key: 'name', label: t('admin.proxies.columns.name'), sortable: true },
  { key: 'protocol', label: t('admin.proxies.columns.protocol'), sortable: true },
  { key: 'address', label: t('admin.proxies.columns.address'), sortable: false },
  { key: 'auth', label: t('admin.proxies.columns.auth'), sortable: false },
  { key: 'location', label: t('admin.proxies.columns.location'), sortable: false },
  { key: 'account_count', label: t('admin.proxies.columns.accounts'), sortable: true },
  { key: 'latency', label: t('admin.proxies.columns.latency'), sortable: false },
  { key: 'expiry', label: t('admin.proxies.columns.expiry'), sortable: true },
  { key: 'created_at', label: t('admin.proxies.columns.createdAt'), sortable: true },
  { key: 'status', label: t('admin.proxies.columns.status'), sortable: true },
  { key: 'actions', label: t('admin.proxies.columns.actions'), sortable: false }
])

// "显示列": the most used columns stay visible, auth and created time start hidden
const { visibleColumns, menuItems: columnMenuItems } = useListColumnMenu({
  storageKey: 'admin-proxies-hidden-columns',
  columns: allColumns,
  fixed: ['select', 'name', 'actions'],
  defaultHidden: ['auth', 'created_at']
})

const createModeTabs = computed(() => [
  { label: t('admin.proxies.standardAdd'), value: 'standard', icon: 'i-lucide-plus' },
  { label: t('admin.proxies.batchAdd'), value: 'batch', icon: 'i-lucide-list' }
])

const fallbackModeOptions = computed(() => [
  { label: t('admin.proxies.fallbackNone'), value: 'none' },
  { label: t('admin.proxies.fallbackProxy'), value: 'proxy' },
  { label: t('admin.proxies.fallbackDirect'), value: 'direct' }
])

// Filter options
const protocolOptions = computed(() => [
  { value: '', label: t('admin.proxies.allProtocols') },
  { value: 'http', label: 'HTTP' },
  { value: 'https', label: 'HTTPS' },
  { value: 'socks5', label: 'SOCKS5' },
  { value: 'socks5h', label: 'SOCKS5H' }
])

const statusOptions = computed(() => [
  { value: '', label: t('admin.proxies.allStatus') },
  { value: 'active', label: t('admin.accounts.status.active') },
  { value: 'inactive', label: t('admin.accounts.status.inactive') },
  { value: 'expired', label: t('admin.proxies.expired') }
])

// Form options
const protocolSelectOptions = computed(() => [
  { value: 'http', label: t('admin.proxies.protocols.http') },
  { value: 'https', label: t('admin.proxies.protocols.https') },
  { value: 'socks5', label: t('admin.proxies.protocols.socks5') },
  { value: 'socks5h', label: t('admin.proxies.protocols.socks5h') }
])

const editStatusOptions = computed(() => [
  { value: 'active', label: t('admin.accounts.status.active') },
  { value: 'inactive', label: t('admin.accounts.status.inactive') }
])

const proxies = ref<Proxy[]>([])
const visiblePasswordIds = reactive(new Set<number>())
const loading = ref(false)
const searchQuery = ref('')
const filters = reactive({
  protocol: '',
  status: ''
})
const pagination = reactive({
  page: 1,
  page_size: getPersistedPageSize(),
  total: 0,
  pages: 0
})
const sortState = reactive({
  sort_by: 'id',
  sort_order: 'desc' as 'asc' | 'desc'
})

const showCreateModal = ref(false)
const createPasswordVisible = ref(false)
const showEditModal = ref(false)
const editPasswordVisible = ref(false)
const editPasswordDirty = ref(false)
const showImportData = ref(false)
const showDeleteDialog = ref(false)
const showBatchDeleteDialog = ref(false)
const showExportDataDialog = ref(false)
const showAccountsModal = ref(false)
const submitting = ref(false)
const exportingData = ref(false)
const testingProxyIds = ref<Set<number>>(new Set())
const qualityCheckingProxyIds = ref<Set<number>>(new Set())
const batchTesting = ref(false)
const batchQualityChecking = ref(false)
const proxyTableRef = ref<HTMLElement | null>(null)
const {
  selectedSet: selectedProxyIds,
  selectedCount,
  allVisibleSelected,
  isSelected,
  select,
  deselect,
  clear: clearSelectedProxies,
  removeMany: removeSelectedProxies,
  toggleVisible,
  batchUpdate
} = useTableSelection<Proxy>({
  rows: proxies,
  getId: (proxy) => proxy.id
})
useSwipeSelect(proxyTableRef, {
  isSelected,
  select,
  deselect,
  batchUpdate
})
const accountsProxy = ref<Proxy | null>(null)
const proxyAccounts = ref<ProxyAccountSummary[]>([])
const accountsLoading = ref(false)
const editingProxy = ref<Proxy | null>(null)
const deletingProxy = ref<Proxy | null>(null)
const showQualityReportDialog = ref(false)
const qualityReportProxy = ref<Proxy | null>(null)
const qualityReport = ref<ProxyQualityCheckResult | null>(null)

// Batch import state
const createMode = ref<'standard' | 'batch'>('standard')
const batchInput = ref('')
const batchParseResult = reactive({
  total: 0,
  valid: 0,
  invalid: 0,
  duplicate: 0,
  proxies: [] as Array<{
    protocol: ProxyProtocol
    host: string
    port: number
    username: string
    password: string
  }>
})

const createForm = reactive({
  name: '',
  protocol: 'http' as ProxyProtocol,
  host: '',
  port: 8080,
  username: '',
  password: '',
  expires_at: '' as string,
  fallback_mode: 'none' as 'none' | 'proxy' | 'direct',
  backup_proxy_id: null as number | null,
  expiry_warn_days: 7 as number,
})

const editForm = reactive({
  name: '',
  protocol: 'http' as ProxyProtocol,
  host: '',
  port: 8080,
  username: '',
  password: '',
  status: 'active' as 'active' | 'inactive' | 'expired',
  expires_at: '' as string,
  fallback_mode: 'none' as 'none' | 'proxy' | 'direct',
  backup_proxy_id: null as number | null,
  expiry_warn_days: 7 as number,
})

const allProxiesForBackup = ref<Proxy[]>([])
const loadBackupProxyOptions = async () => {
  allProxiesForBackup.value = await adminAPI.proxies.getAllWithCount()
}
const backupProxyOptions = (excludeId?: number) =>
  allProxiesForBackup.value
    .filter(p => p.id !== excludeId)
    .map(p => ({ label: `${p.name} (${p.host}:${p.port})`, value: p.id }))

let abortController: AbortController | null = null

const isAbortError = (error: unknown) => {
  if (!error || typeof error !== 'object') return false
  const maybeError = error as { name?: string; code?: string }
  return maybeError.name === 'AbortError' || maybeError.code === 'ERR_CANCELED'
}

const buildProxyQueryFilters = () => ({
  protocol: filters.protocol || undefined,
  status: (filters.status || undefined) as 'active' | 'inactive' | 'expired' | undefined,
  search: searchQuery.value || undefined,
  sort_by: sortState.sort_by,
  sort_order: sortState.sort_order
})

const loadProxies = async () => {
  if (abortController) {
    abortController.abort()
  }
  const currentAbortController = new AbortController()
  abortController = currentAbortController
  loading.value = true
  try {
    const response = await adminAPI.proxies.list(
      pagination.page,
      pagination.page_size,
      buildProxyQueryFilters(),
      { signal: currentAbortController.signal }
    )
    if (currentAbortController.signal.aborted || abortController !== currentAbortController) {
      return
    }
    proxies.value = response.items
    pagination.total = response.total
    pagination.pages = response.pages
  } catch (error) {
    if (isAbortError(error)) {
      return
    }
    appStore.showError(t('admin.proxies.failedToLoad'))
    console.error('Error loading proxies:', error)
  } finally {
    if (abortController === currentAbortController) {
      loading.value = false
      abortController = null
    }
  }
}

const handleFilterChange = () => {
  pagination.page = 1
  loadProxies()
}

let searchTimeout: ReturnType<typeof setTimeout>
const handleSearch = () => {
  clearTimeout(searchTimeout)
  searchTimeout = setTimeout(() => {
    pagination.page = 1
    loadProxies()
  }, 300)
}

const handlePageChange = (page: number) => {
  pagination.page = page
  loadProxies()
}

const handlePageSizeChange = (pageSize: number) => {
  pagination.page_size = pageSize
  pagination.page = 1
  loadProxies()
}

const handleSort = (key: string, order: 'asc' | 'desc') => {
  sortState.sort_by = key
  sortState.sort_order = order
  pagination.page = 1
  loadProxies()
}

const closeCreateModal = () => {
  showCreateModal.value = false
  createMode.value = 'standard'
  createForm.name = ''
  createForm.protocol = 'http'
  createForm.host = ''
  createForm.port = 8080
  createForm.username = ''
  createForm.password = ''
  createForm.expires_at = ''
  createForm.fallback_mode = 'none'
  createForm.backup_proxy_id = null
  createForm.expiry_warn_days = 7
  createPasswordVisible.value = false
  batchInput.value = ''
  batchParseResult.total = 0
  batchParseResult.valid = 0
  batchParseResult.invalid = 0
  batchParseResult.duplicate = 0
  batchParseResult.proxies = []
}

const handleDataImported = () => {
  showImportData.value = false
  loadProxies()
}

// Parse proxy URL: protocol://user:pass@host:port or protocol://host:port
// Host may be a domain, IPv4, or bracketed IPv6 ([2001:db8::1]).
const parseProxyUrl = (
  line: string
): {
  protocol: ProxyProtocol
  host: string
  port: number
  username: string
  password: string
} | null => {
  const trimmed = line.trim()
  if (!trimmed) return null

  // Regex to parse proxy URL (supports http, https, socks5, socks5h).
  // Host alternatives: [bracketed-IPv6] | hostname/IPv4 (colon-free, so the
  // match stops before the final :port).
  const regex =
    /^(https?|socks5h?):\/\/(?:([^:@\[\]]+):([^@\[\]]+)@)?(\[[0-9a-f:.]+\]|[^:\[\]]+):(\d+)$/i
  const match = trimmed.match(regex)

  if (!match) return null

  const [, protocol, username, password, rawHost, port] = match
  const portNum = parseInt(port, 10)

  if (portNum < 1 || portNum > 65535) return null

  // Strip brackets from IPv6 literals; the backend re-brackets via net.JoinHostPort.
  const host = rawHost.replace(/^\[|\]$/g, '').trim()

  return {
    protocol: protocol.toLowerCase() as ProxyProtocol,
    host,
    port: portNum,
    username: username?.trim() || '',
    password: password?.trim() || ''
  }
}

const parseBatchInput = () => {
  const lines = batchInput.value.split('\n').filter((l) => l.trim())
  const seen = new Set<string>()
  const proxies: typeof batchParseResult.proxies = []
  let invalid = 0
  let duplicate = 0

  for (const line of lines) {
    const parsed = parseProxyUrl(line)
    if (!parsed) {
      invalid++
      continue
    }

    // Check for duplicates (same host:port:username:password)
    const key = `${parsed.host}:${parsed.port}:${parsed.username}:${parsed.password}`
    if (seen.has(key)) {
      duplicate++
      continue
    }
    seen.add(key)
    proxies.push(parsed)
  }

  batchParseResult.total = lines.length
  batchParseResult.valid = proxies.length
  batchParseResult.invalid = invalid
  batchParseResult.duplicate = duplicate
  batchParseResult.proxies = proxies
}

const handleBatchCreate = async () => {
  if (batchParseResult.valid === 0) return

  submitting.value = true
  try {
    const result = await adminAPI.proxies.batchCreate(batchParseResult.proxies)
    const created = result.created || 0
    const skipped = result.skipped || 0

    if (created > 0) {
      appStore.showSuccess(t('admin.proxies.batchImportSuccess', { created, skipped }))
    } else {
      appStore.showInfo(t('admin.proxies.batchImportAllSkipped', { skipped }))
    }

    closeCreateModal()
    loadProxies()
  } catch (error: any) {
    appStore.showError(error.response?.data?.detail || t('admin.proxies.failedToImport'))
    console.error('Error batch creating proxies:', error)
  } finally {
    submitting.value = false
  }
}

const handleCreateProxy = async () => {
  if (!createForm.name.trim()) {
    appStore.showError(t('admin.proxies.nameRequired'))
    return
  }
  if (!createForm.host.trim()) {
    appStore.showError(t('admin.proxies.hostRequired'))
    return
  }
  if (createForm.port < 1 || createForm.port > 65535) {
    appStore.showError(t('admin.proxies.portInvalid'))
    return
  }
  submitting.value = true
  try {
    await adminAPI.proxies.create({
      name: createForm.name.trim(),
      protocol: createForm.protocol,
      host: createForm.host.trim(),
      port: createForm.port,
      username: createForm.username.trim() || null,
      password: createForm.password.trim() || null,
      expires_at: createForm.expires_at ? Math.floor(new Date(createForm.expires_at).getTime() / 1000) : null,
      fallback_mode: createForm.fallback_mode,
      backup_proxy_id: createForm.fallback_mode === 'proxy' ? createForm.backup_proxy_id : null,
      expiry_warn_days: createForm.expiry_warn_days,
    })
    appStore.showSuccess(t('admin.proxies.proxyCreated'))
    closeCreateModal()
    loadProxies()
  } catch (error: any) {
    appStore.showError(error.response?.data?.detail || t('admin.proxies.failedToCreate'))
    console.error('Error creating proxy:', error)
  } finally {
    submitting.value = false
  }
}

const handleEdit = (proxy: Proxy) => {
  editingProxy.value = proxy
  editForm.name = proxy.name
  editForm.protocol = proxy.protocol
  editForm.host = proxy.host
  editForm.port = proxy.port
  editForm.username = proxy.username || ''
  editForm.password = proxy.password || ''
  editForm.status = proxy.status === 'expired' ? 'inactive' : proxy.status
  editForm.expires_at = proxy.expires_at ? proxy.expires_at.slice(0, 10) : ''
  editForm.fallback_mode = proxy.fallback_mode || 'none'
  editForm.backup_proxy_id = proxy.backup_proxy_id ?? null
  editForm.expiry_warn_days = proxy.expiry_warn_days ?? 7
  editPasswordVisible.value = false
  editPasswordDirty.value = false
  showEditModal.value = true
}

const closeEditModal = () => {
  showEditModal.value = false
  editingProxy.value = null
  editPasswordVisible.value = false
  editPasswordDirty.value = false
}

const handleUpdateProxy = async () => {
  if (!editingProxy.value) return
  if (!editForm.name.trim()) {
    appStore.showError(t('admin.proxies.nameRequired'))
    return
  }
  if (!editForm.host.trim()) {
    appStore.showError(t('admin.proxies.hostRequired'))
    return
  }
  if (editForm.port < 1 || editForm.port > 65535) {
    appStore.showError(t('admin.proxies.portInvalid'))
    return
  }

  submitting.value = true
  try {
    const updateData: any = {
      name: editForm.name.trim(),
      protocol: editForm.protocol,
      host: editForm.host.trim(),
      port: editForm.port,
      username: editForm.username.trim() || null,
      status: editForm.status,
      expires_at: editForm.expires_at ? Math.floor(new Date(editForm.expires_at).getTime() / 1000) : null,
      fallback_mode: editForm.fallback_mode,
      backup_proxy_id: editForm.fallback_mode === 'proxy' ? editForm.backup_proxy_id : null,
      expiry_warn_days: editForm.expiry_warn_days,
    }

    // Only include password if user actually modified the field
    if (editPasswordDirty.value) {
      updateData.password = editForm.password.trim() || null
    }

    await adminAPI.proxies.update(editingProxy.value.id, updateData)
    appStore.showSuccess(t('admin.proxies.proxyUpdated'))
    closeEditModal()
    loadProxies()
  } catch (error: any) {
    appStore.showError(error.response?.data?.detail || t('admin.proxies.failedToUpdate'))
    console.error('Error updating proxy:', error)
  } finally {
    submitting.value = false
  }
}

const applyLatencyResult = (
  proxyId: number,
  result: {
    success: boolean
    latency_ms?: number
    message?: string
    ip_address?: string
    country?: string
    country_code?: string
    region?: string
    city?: string
  }
) => {
  const target = proxies.value.find((proxy) => proxy.id === proxyId)
  if (!target) return
  if (result.success) {
    target.latency_status = 'success'
    target.latency_ms = result.latency_ms
    target.ip_address = result.ip_address
    target.country = result.country
    target.country_code = result.country_code
    target.region = result.region
    target.city = result.city
  } else {
    target.latency_status = 'failed'
    target.latency_ms = undefined
    target.ip_address = undefined
    target.country = undefined
    target.country_code = undefined
    target.region = undefined
    target.city = undefined
  }
  target.latency_message = result.message
}

const summarizeQualityStatus = (result: ProxyQualityCheckResult): Proxy['quality_status'] => {
  if (result.challenge_count > 0) return 'challenge'
  if (result.failed_count > 0) return 'failed'
  if (result.warn_count > 0) return 'warn'
  return 'healthy'
}

const applyQualityResult = (proxyId: number, result: ProxyQualityCheckResult) => {
  const target = proxies.value.find((proxy) => proxy.id === proxyId)
  if (!target) return
  target.quality_status = summarizeQualityStatus(result)
  target.quality_score = result.score
  target.quality_grade = result.grade
  target.quality_summary = result.summary
  target.quality_checked = result.checked_at
}

const formatLocation = (proxy: Proxy) => {
  const parts = [proxy.country, proxy.city].filter(Boolean) as string[]
  return parts.join(' · ')
}

const flagUrl = (code: string) =>
  `https://unpkg.com/flag-icons/flags/4x3/${code.toLowerCase()}.svg`

const startTestingProxy = (proxyId: number) => {
  testingProxyIds.value = new Set([...testingProxyIds.value, proxyId])
}

const stopTestingProxy = (proxyId: number) => {
  const next = new Set(testingProxyIds.value)
  next.delete(proxyId)
  testingProxyIds.value = next
}

const startQualityCheckingProxy = (proxyId: number) => {
  qualityCheckingProxyIds.value = new Set([...qualityCheckingProxyIds.value, proxyId])
}

const stopQualityCheckingProxy = (proxyId: number) => {
  const next = new Set(qualityCheckingProxyIds.value)
  next.delete(proxyId)
  qualityCheckingProxyIds.value = next
}

const runProxyTest = async (proxyId: number, notify: boolean) => {
  startTestingProxy(proxyId)
  try {
    const result = await adminAPI.proxies.testProxy(proxyId)
    applyLatencyResult(proxyId, result)
    if (notify) {
      if (result.success) {
        const message = result.latency_ms
          ? t('admin.proxies.proxyWorkingWithLatency', { latency: result.latency_ms })
          : t('admin.proxies.proxyWorking')
        appStore.showSuccess(message)
      } else {
        appStore.showError(result.message || t('admin.proxies.proxyTestFailed'))
      }
    }
    return result
  } catch (error: any) {
    const message = error.response?.data?.detail || t('admin.proxies.failedToTest')
    applyLatencyResult(proxyId, { success: false, message })
    if (notify) {
      appStore.showError(message)
    }
    console.error('Error testing proxy:', error)
    return null
  } finally {
    stopTestingProxy(proxyId)
  }
}

const handleTestConnection = async (proxy: Proxy) => {
  await runProxyTest(proxy.id, true)
}

const handleQualityCheck = async (proxy: Proxy) => {
  startQualityCheckingProxy(proxy.id)
  try {
    const result = await adminAPI.proxies.checkProxyQuality(proxy.id)
    qualityReportProxy.value = proxy
    qualityReport.value = result
    showQualityReportDialog.value = true

    const baseStep = result.items.find((item) => item.target === 'base_connectivity')
    if (baseStep && baseStep.status === 'pass') {
      applyLatencyResult(proxy.id, {
        success: true,
        latency_ms: result.base_latency_ms,
        message: result.summary,
        ip_address: result.exit_ip,
        country: result.country,
        country_code: result.country_code
      })
    }
    applyQualityResult(proxy.id, result)

    appStore.showSuccess(
      t('admin.proxies.qualityCheckDone', { score: result.score, grade: result.grade })
    )
  } catch (error: any) {
    const message = error.response?.data?.detail || t('admin.proxies.qualityCheckFailed')
    appStore.showError(message)
    console.error('Error checking proxy quality:', error)
  } finally {
    stopQualityCheckingProxy(proxy.id)
  }
}

const runBatchProxyQualityChecks = async (ids: number[]) => {
  if (ids.length === 0) return { total: 0, healthy: 0, warn: 0, challenge: 0, failed: 0 }

  const concurrency = 3
  let index = 0
  let healthy = 0
  let warn = 0
  let challenge = 0
  let failed = 0

  const worker = async () => {
    while (index < ids.length) {
      const current = ids[index]
      index++
      startQualityCheckingProxy(current)
      try {
        const result = await adminAPI.proxies.checkProxyQuality(current)
        const target = proxies.value.find((proxy) => proxy.id === current)
        if (target) {
          const baseStep = result.items.find((item) => item.target === 'base_connectivity')
          if (baseStep && baseStep.status === 'pass') {
            applyLatencyResult(current, {
              success: true,
              latency_ms: result.base_latency_ms,
              message: result.summary,
              ip_address: result.exit_ip,
              country: result.country,
              country_code: result.country_code
            })
          }
        }
        applyQualityResult(current, result)
        if (result.challenge_count > 0) {
          challenge++
        } else if (result.failed_count > 0) {
          failed++
        } else if (result.warn_count > 0) {
          warn++
        } else {
          healthy++
        }
      } catch {
        failed++
      } finally {
        stopQualityCheckingProxy(current)
      }
    }
  }

  const workers = Array.from({ length: Math.min(concurrency, ids.length) }, () => worker())
  await Promise.all(workers)
  return {
    total: ids.length,
    healthy,
    warn,
    challenge,
    failed
  }
}

const closeQualityReportDialog = () => {
  showQualityReportDialog.value = false
  qualityReportProxy.value = null
  qualityReport.value = null
}

type BadgeColor = 'success' | 'warning' | 'error'

const qualityStatusColor = (status: string): BadgeColor => {
  if (status === 'pass') return 'success'
  if (status === 'warn') return 'warning'
  return 'error'
}

const qualityStatusLabel = (status: string) => {
  if (status === 'pass') return t('admin.proxies.qualityStatusPass')
  if (status === 'warn') return t('admin.proxies.qualityStatusWarn')
  if (status === 'challenge') return t('admin.proxies.qualityStatusChallenge')
  return t('admin.proxies.qualityStatusFail')
}

// 有效期「选天数」⇄ 日历联动:天数自 base 起算(创建=今天;编辑=代理创建日),本地日历日 round-trip 稳定;canonical 仍是 expires_at 日期串
const EXPIRY_PRESETS = [7, 30, 90, 180]
const toLocalDateStr = (dt: Date): string => {
  const y = dt.getFullYear()
  const m = String(dt.getMonth() + 1).padStart(2, '0')
  const d = String(dt.getDate()).padStart(2, '0')
  return `${y}-${m}-${d}`
}
// base 为空 → 今天本地 00:00;否则该日期本地 00:00
const baseDateOrToday = (baseDateStr: string): Date => {
  const base = baseDateStr ? new Date(`${baseDateStr}T00:00:00`) : new Date()
  base.setHours(0, 0, 0, 0)
  return base
}
// base + N 天 → 本地 YYYY-MM-DD;N≤0/空 → '' 表示永不过期
const addDaysToBase = (baseDateStr: string, n: number | null): string => {
  const days = Number(n)
  if (!days || days <= 0) return ''
  const dt = baseDateOrToday(baseDateStr)
  dt.setDate(dt.getDate() + days)
  return toLocalDateStr(dt)
}
// target 相对 base 的整天数(本地日历差,避免时区/时刻抖动)
const daysFromBase = (baseDateStr: string, targetDateStr: string): number | null => {
  if (!targetDateStr) return null
  const target = new Date(`${targetDateStr}T00:00:00`)
  return Math.round((target.getTime() - baseDateOrToday(baseDateStr).getTime()) / 86400000)
}
// 编辑时有效期自「代理创建日」起算;创建时无 created_at → base='' 用今天
const editBaseDate = computed(() =>
  editingProxy.value?.created_at ? editingProxy.value.created_at.slice(0, 10) : '',
)
const createExpiresDays = computed<number | null>({
  get: () => daysFromBase('', createForm.expires_at),
  set: (v) => {
    createForm.expires_at = addDaysToBase('', v)
  },
})
const editExpiresDays = computed<number | null>({
  get: () => daysFromBase(editBaseDate.value, editForm.expires_at),
  set: (v) => {
    editForm.expires_at = addDaysToBase(editBaseDate.value, v)
  },
})

const expiryLabel = (row: Proxy): string => {
  const { key, params } = proxyExpiryLabelKey(row.expires_at, row.status)
  return params ? t(key, params) : t(key)
}

// Same thresholds as the shared expiry helper, mapped onto badge colors (no badge = plain muted text)
const expiryColor = (row: Proxy): BadgeColor | null => {
  const cls = proxyExpiryBadgeClass(row.expires_at, row.status)
  if (cls.includes('badge-danger')) return 'error'
  if (cls.includes('badge-warning')) return 'warning'
  return null
}

const qualityOverallColor = (status?: string): BadgeColor => {
  if (status === 'healthy') return 'success'
  if (status === 'warn') return 'warning'
  return 'error'
}

const qualityOverallLabel = (status?: string) => {
  if (status === 'healthy') return t('admin.proxies.qualityStatusHealthy')
  if (status === 'warn') return t('admin.proxies.qualityStatusWarn')
  if (status === 'challenge') return t('admin.proxies.qualityStatusChallenge')
  return t('admin.proxies.qualityStatusFail')
}

const qualityTargetLabel = (target: string) => {
  switch (target) {
    case 'base_connectivity':
      return t('admin.proxies.qualityTargetBase')
    case 'openai':
      return 'OpenAI'
    case 'anthropic':
      return 'Anthropic'
    case 'gemini':
      return 'Gemini'
    case 'grok':
      return 'Grok'
    case 'kimi':
      return 'Kimi'
    case 'zhipu':
      return 'Zhipu GLM'
    case 'deepseek':
      return 'DeepSeek'
    case 'minimax':
      return 'MiniMax'
    default:
      return target
  }
}

const fetchAllProxiesForBatch = async (): Promise<Proxy[]> => {
  const pageSize = 200
  const result: Proxy[] = []
  let page = 1
  let totalPages = 1

  while (page <= totalPages) {
    const response = await adminAPI.proxies.list(
      page,
      pageSize,
      {
        protocol: filters.protocol || undefined,
        status: filters.status as any,
        search: searchQuery.value || undefined,
        sort_by: sortState.sort_by,
        sort_order: sortState.sort_order
      }
    )
    result.push(...response.items)
    totalPages = response.pages || 1
    page++
  }

  return result
}

const runBatchProxyTests = async (ids: number[]) => {
  if (ids.length === 0) return
  const concurrency = 5
  let index = 0

  const worker = async () => {
    while (index < ids.length) {
      const current = ids[index]
      index++
      await runProxyTest(current, false)
    }
  }

  const workers = Array.from({ length: Math.min(concurrency, ids.length) }, () => worker())
  await Promise.all(workers)
}

const handleBatchTest = async () => {
  if (batchTesting.value) return

  batchTesting.value = true
  try {
    let ids: number[] = []
    if (selectedCount.value > 0) {
      ids = Array.from(selectedProxyIds.value)
    } else {
      const allProxies = await fetchAllProxiesForBatch()
      ids = allProxies.map((proxy) => proxy.id)
    }

    if (ids.length === 0) {
      appStore.showInfo(t('admin.proxies.batchTestEmpty'))
      return
    }

    await runBatchProxyTests(ids)
    appStore.showSuccess(t('admin.proxies.batchTestDone', { count: ids.length }))
    loadProxies()
  } catch (error: any) {
    appStore.showError(error.response?.data?.detail || t('admin.proxies.batchTestFailed'))
    console.error('Error batch testing proxies:', error)
  } finally {
    batchTesting.value = false
  }
}

const handleBatchQualityCheck = async () => {
  if (batchQualityChecking.value) return

  batchQualityChecking.value = true
  try {
    let ids: number[] = []
    if (selectedCount.value > 0) {
      ids = Array.from(selectedProxyIds.value)
    } else {
      const allProxies = await fetchAllProxiesForBatch()
      ids = allProxies.map((proxy) => proxy.id)
    }

    if (ids.length === 0) {
      appStore.showInfo(t('admin.proxies.batchQualityEmpty'))
      return
    }

    const summary = await runBatchProxyQualityChecks(ids)
    appStore.showSuccess(
      t('admin.proxies.batchQualityDone', {
        count: summary.total,
        healthy: summary.healthy,
        warn: summary.warn,
        challenge: summary.challenge,
        failed: summary.failed
      })
    )
    loadProxies()
  } catch (error: any) {
    appStore.showError(error.response?.data?.detail || t('admin.proxies.batchQualityFailed'))
    console.error('Error batch checking quality:', error)
  } finally {
    batchQualityChecking.value = false
  }
}

const formatExportTimestamp = () => {
  const now = new Date()
  const pad2 = (value: number) => String(value).padStart(2, '0')
  return `${now.getFullYear()}${pad2(now.getMonth() + 1)}${pad2(now.getDate())}${pad2(now.getHours())}${pad2(now.getMinutes())}${pad2(now.getSeconds())}`
}

const handleExportData = async () => {
  if (exportingData.value) return
  exportingData.value = true
  try {
    const dataPayload = await adminAPI.proxies.exportData(
      selectedCount.value > 0
        ? { ids: Array.from(selectedProxyIds.value) }
        : {
            filters: buildProxyQueryFilters()
          }
    )
    const timestamp = formatExportTimestamp()
    const filename = `sub2api-proxy-${timestamp}.json`
    const blob = new Blob([JSON.stringify(dataPayload, null, 2)], { type: 'application/json' })
    const url = URL.createObjectURL(blob)
    const link = document.createElement('a')
    link.href = url
    link.download = filename
    link.click()
    URL.revokeObjectURL(url)
    appStore.showSuccess(t('admin.proxies.dataExported'))
  } catch (error: any) {
    appStore.showError(error?.message || t('admin.proxies.dataExportFailed'))
  } finally {
    exportingData.value = false
    showExportDataDialog.value = false
  }
}

const handleDelete = (proxy: Proxy) => {
  if ((proxy.account_count || 0) > 0) {
    appStore.showError(t('admin.proxies.deleteBlockedInUse'))
    return
  }
  deletingProxy.value = proxy
  showDeleteDialog.value = true
}

const openBatchDelete = () => {
  if (selectedCount.value === 0) {
    return
  }
  showBatchDeleteDialog.value = true
}

const confirmDelete = async () => {
  if (!deletingProxy.value) return

  try {
    await adminAPI.proxies.delete(deletingProxy.value.id)
    appStore.showSuccess(t('admin.proxies.proxyDeleted'))
    showDeleteDialog.value = false
    removeSelectedProxies([deletingProxy.value.id])
    deletingProxy.value = null
    loadProxies()
  } catch (error: any) {
    appStore.showError(error.response?.data?.detail || t('admin.proxies.failedToDelete'))
    console.error('Error deleting proxy:', error)
  }
}

const confirmBatchDelete = async () => {
  const ids = Array.from(selectedProxyIds.value)
  if (ids.length === 0) {
    showBatchDeleteDialog.value = false
    return
  }

  try {
    const result = await adminAPI.proxies.batchDelete(ids)
    const deleted = result.deleted_ids?.length || 0
    const skipped = result.skipped?.length || 0

    if (deleted > 0) {
      appStore.showSuccess(t('admin.proxies.batchDeleteDone', { deleted, skipped }))
    } else if (skipped > 0) {
      appStore.showInfo(t('admin.proxies.batchDeleteSkipped', { skipped }))
    }

    clearSelectedProxies()
    showBatchDeleteDialog.value = false
    loadProxies()
  } catch (error: any) {
    appStore.showError(error.response?.data?.detail || t('admin.proxies.batchDeleteFailed'))
    console.error('Error batch deleting proxies:', error)
  }
}

const openAccountsModal = async (proxy: Proxy) => {
  accountsProxy.value = proxy
  proxyAccounts.value = []
  accountsLoading.value = true
  showAccountsModal.value = true

  try {
    proxyAccounts.value = await adminAPI.proxies.getProxyAccounts(proxy.id)
  } catch (error: any) {
    appStore.showError(error.response?.data?.detail || t('admin.proxies.accountsFailed'))
    console.error('Error loading proxy accounts:', error)
  } finally {
    accountsLoading.value = false
  }
}

const closeAccountsModal = () => {
  showAccountsModal.value = false
  accountsProxy.value = null
  proxyAccounts.value = []
}

// ── Proxy URL copy ──
function buildAuthPart(row: any): string {
  const user = row.username ? encodeURIComponent(row.username) : ''
  const pass = row.password ? encodeURIComponent(row.password) : ''
  if (user && pass) return `${user}:${pass}@`
  if (user) return `${user}@`
  if (pass) return `:${pass}@`
  return ''
}

function buildProxyUrl(row: any): string {
  return `${row.protocol}://${buildAuthPart(row)}${row.host}:${row.port}`
}

function getCopyFormats(row: any) {
  const hasAuth = row.username || row.password
  const fullUrl = buildProxyUrl(row)
  const formats = [
    { label: fullUrl, value: fullUrl },
  ]
  if (hasAuth) {
    const withoutProtocol = fullUrl.replace(/^[^:]+:\/\//, '')
    formats.push({ label: withoutProtocol, value: withoutProtocol })
  }
  formats.push({ label: `${row.host}:${row.port}`, value: `${row.host}:${row.port}` })
  return formats
}

function copyProxyUrl(row: any) {
  copyToClipboard(buildProxyUrl(row), t('admin.proxies.urlCopied'))
}

function copyFormat(value: string) {
  copyToClipboard(value, t('admin.proxies.urlCopied'))
}

// Right click on the copy button: every copy format (UContextMenu)
const copyFormatMenuItems = (row: Proxy): ContextMenuItem[] =>
  getCopyFormats(row).map((fmt) => ({
    label: fmt.label,
    class: 'font-mono text-xs',
    onSelect: () => copyFormat(fmt.value)
  }))

// Toolbar "更多" menu: batch quality check, import, export
const toolbarMenuItems = computed<DropdownMenuItem[][]>(() => [
  [
    {
      label: t('admin.proxies.batchQualityCheck'),
      icon: 'i-lucide-shield-check',
      disabled: batchQualityChecking.value || loading.value,
      onSelect: () => handleBatchQualityCheck()
    }
  ],
  [
    { label: t('admin.proxies.dataImport'), icon: 'i-lucide-upload', onSelect: () => { showImportData.value = true } },
    {
      label: selectedCount.value > 0 ? t('admin.proxies.dataExportSelected') : t('admin.proxies.dataExport'),
      icon: 'i-lucide-download',
      onSelect: () => { showExportDataDialog.value = true }
    }
  ]
])

// Row "…" menu: quality check, delete
const rowMenuItems = (row: Proxy): DropdownMenuItem[][] => [
  [
    {
      label: t('admin.proxies.qualityCheck'),
      icon: 'i-lucide-shield',
      disabled: qualityCheckingProxyIds.value.has(row.id),
      onSelect: () => handleQualityCheck(row)
    }
  ],
  [{ label: t('common.delete'), icon: 'i-lucide-trash', color: 'error', onSelect: () => handleDelete(row) }]
]

const qualityColumns = computed<TableColumn<ProxyQualityCheckItem>[]>(() => [
  { accessorKey: 'target', header: t('admin.proxies.qualityTableTarget') },
  { accessorKey: 'status', header: t('admin.proxies.qualityTableStatus') },
  { accessorKey: 'http_status', header: 'HTTP' },
  { accessorKey: 'latency_ms', header: t('admin.proxies.qualityTableLatency') },
  { accessorKey: 'message', header: t('admin.proxies.qualityTableMessage') }
])

const accountColumns = computed<TableColumn<ProxyAccountSummary>[]>(() => [
  { accessorKey: 'name', header: t('admin.proxies.accountName') },
  { accessorKey: 'platform', header: t('admin.accounts.columns.platformType') },
  { accessorKey: 'notes', header: t('admin.proxies.accountNotes') }
])

onMounted(() => {
  loadProxies()
  loadBackupProxyOptions()
})

onUnmounted(() => {
  clearTimeout(searchTimeout)
  abortController?.abort()
})
</script>
