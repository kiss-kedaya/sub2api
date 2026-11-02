<template>
  <AppLayout>
    <TablePageLayout class="signal-keys">
      <template #filters>
        <div class="flex flex-col gap-3">
          <div class="flex flex-wrap items-center gap-2">
            <SearchInput
              v-model="filterSearch"
              :placeholder="t('keys.searchPlaceholder')"
              class="w-full sm:w-64"
              @search="onFilterChange"
            />
            <Select
              :model-value="filterGroupId"
              class="w-full sm:w-40"
              :options="groupFilterOptions"
              @update:model-value="onGroupFilterChange"
            />
            <Select
              :model-value="filterStatus"
              class="w-full sm:w-40"
              :options="statusFilterOptions"
              @update:model-value="onStatusFilterChange"
            />
          </div>
          <EndpointPopover
            class="signal-key-endpoints"
            v-if="publicSettings?.api_base_url || customEndpoints.length > 0"
            :api-base-url="publicSettings?.api_base_url || ''"
            :custom-endpoints="customEndpoints"
          />
        </div>
      </template>

      <template #actions>
        <UserPageHeader flush :title="t('keys.title')">
          <template #badge>
            <UBadge color="neutral" variant="subtle" class="tabular-nums" data-test="key-count">{{ pagination.total }}</UBadge>
          </template>
          <template #actions>
            <UButton
              v-if="selectedKeyCount > 0"
              color="error"
              variant="outline"
              icon="i-lucide-trash-2"
              data-test="keys-bulk-delete"
              @click="openBulkDelete"
            >
              {{ t('common.delete') }}
              <span class="ml-1 tabular-nums opacity-70">{{ selectedKeyCount }}</span>
            </UButton>
            <UButton
              color="neutral"
              variant="outline"
              icon="i-lucide-refresh-cw"
              :ui="{ leadingIcon: loading ? 'animate-spin' : '' }"
              :disabled="loading"
              :title="t('common.refresh')"
              :aria-label="t('common.refresh')"
              @click="loadApiKeys"
            />
            <div class="relative" ref="columnDropdownRef">
              <UButton
                color="neutral"
                variant="outline"
                icon="i-lucide-columns-3"
                :title="t('keys.columnSettings')"
                :aria-label="t('keys.columnSettings')"
                :aria-expanded="showColumnDropdown"
                @click="showColumnDropdown = !showColumnDropdown"
              />
              <div
                v-if="showColumnDropdown"
                class="absolute left-0 top-full z-50 mt-1 max-h-80 w-52 overflow-y-auto rounded-lg bg-default p-1 shadow-lg ring ring-default sm:left-auto sm:right-0"
              >
                <button
                  v-for="col in toggleableColumns"
                  :key="col.key"
                  type="button"
                  @click="toggleColumn(col.key)"
                  class="flex w-full items-center justify-between rounded-md px-2.5 py-1.5 text-left text-sm text-default transition-colors hover:bg-elevated"
                >
                  <span>{{ col.label }}</span>
                  <UIcon v-if="isColumnVisible(col.key)" name="i-lucide-check" class="size-4 text-highlighted" />
                </button>
              </div>
            </div>
            <UButton icon="i-lucide-plus" data-tour="keys-create-btn" @click="showCreateModal = true">
              {{ t('keys.createKey') }}
            </UButton>
          </template>
        </UserPageHeader>
      </template>

      <template #table>
        <DataTable
          :columns="columns"
          :data="apiKeys"
          :loading="loading"
          :server-side-sort="true"
          :selectable="true"
          :row-key="keyRowId"
          v-model:selected-keys="selectedKeyIds"
          default-sort-key="created_at"
          default-sort-order="desc"
          @sort="handleSort"
        >
          <template #cell-id="{ value }">
            <span class="font-mono text-xs text-gray-500 dark:text-gray-400">#{{ value }}</span>
          </template>

          <template #cell-key="{ value, row }">
            <div class="flex items-center gap-2">
              <code class="rounded-md bg-elevated px-1.5 py-0.5 font-mono text-xs text-default ring ring-inset ring-default">
                {{ maskApiKey(value) }}
              </code>
              <button
                type="button"
                @click="copyToClipboard(value, row.id)"
                class="rounded-md p-1 transition-colors hover:bg-elevated"
                :class="
                  copiedKeyId === row.id
                    ? 'text-green-500'
                    : 'text-dimmed hover:text-highlighted'
                "
                :title="copiedKeyId === row.id ? t('keys.copied') : t('keys.copyToClipboard')"
              >
                <Icon
                  v-if="copiedKeyId === row.id"
                  name="check"
                  size="sm"
                  :stroke-width="2"
                />
                <Icon v-else name="clipboard" size="sm" />
              </button>
            </div>
          </template>

          <template #cell-name="{ value, row }">
            <div class="flex items-center gap-1.5">
              <span class="font-medium text-gray-900 dark:text-white">{{ value }}</span>
              <Icon
                v-if="row.ip_whitelist?.length > 0 || row.ip_blacklist?.length > 0"
                name="shield"
                size="sm"
                class="text-blue-500"
                :title="t('keys.ipRestrictionEnabled')"
              />
            </div>
          </template>

          <template #cell-group="{ row }">
            <div class="group/dropdown relative">
              <button
                :ref="(el) => setGroupButtonRef(row.id, el)"
                @click="openGroupSelector(row)"
                class="-mx-2 -my-1 flex cursor-pointer items-center gap-2 rounded-lg px-2 py-1 transition-all duration-200 hover:bg-gray-100 dark:hover:bg-dark-700"
                :title="isSmartRoutingKey(row) ? t('keys.smartRoutingChangeInEditor') : t('keys.clickToChangeGroup')"
              >
                <GroupBadge
                  v-if="row.group"
                  :name="row.group.name"
                  :platform="row.group.platform"
                  :subscription-type="row.group.subscription_type"
                  :rate-multiplier="row.group.rate_multiplier"
                  :user-rate-multiplier="userGroupRates[row.group.id]"
                  :peak-rate-enabled="row.group.peak_rate_enabled"
                  :peak-start="row.group.peak_start"
                  :peak-end="row.group.peak_end"
                  :peak-rate-multiplier="row.group.peak_rate_multiplier"
                />
                <span v-else class="text-sm text-gray-400 dark:text-dark-500">{{
                  t('keys.noGroup')
                }}</span>
                <UBadge v-if="isSmartRoutingKey(row)" color="primary" variant="subtle" size="sm">
                  {{ t('keys.smartRouting') }}
                  {{ t('keys.smartRoutingMore', { count: (row.group_ids?.length || 1) - 1 }) }}
                </UBadge>
                <span v-else class="text-xs text-muted">{{ t('keys.selectGroup') }}</span>
                <svg
                  class="h-3.5 w-3.5 text-gray-400 opacity-60 transition-opacity group-hover/dropdown:opacity-100"
                  fill="none"
                  stroke="currentColor"
                  viewBox="0 0 24 24"
                  stroke-width="2"
                >
                  <path
                    stroke-linecap="round"
                    stroke-linejoin="round"
                    d="M8.25 15L12 18.75 15.75 15m-7.5-6L12 5.25 15.75 9"
                  />
                </svg>
              </button>
            </div>
          </template>

          <template #cell-current_concurrency="{ value, row }">
            <span
              :class="[
                'inline-flex min-w-8 items-center justify-center rounded-sm px-2 py-1 text-sm font-semibold tabular-nums focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-current',
                (value ?? 0) > 0
                  ? 'bg-emerald-50 text-emerald-700 ring-1 ring-emerald-200 dark:bg-emerald-900/25 dark:text-emerald-300 dark:ring-emerald-800'
                  : 'bg-gray-100 text-gray-500 dark:bg-dark-700 dark:text-dark-400'
              ]"
              :title="concurrencyHint(row)"
              :aria-label="concurrencyHint(row)"
              tabindex="0"
            >
              {{ value ?? 0 }}<span v-if="row.concurrency_limit" class="ml-1 opacity-70">/ {{ row.concurrency_limit }}</span>
            </span>
            <span v-if="row.concurrency_limit" class="mt-1 block text-xs text-gray-500">{{ t('keys.queueWaiting') }}: {{ queueWaiting[row.id] ?? '—' }}</span>
          </template>

          <template #cell-usage="{ row }">
            <div class="text-sm">
              <div class="flex items-center gap-1.5">
                <span class="text-gray-500 dark:text-gray-400">{{ t('keys.today') }}:</span>
                <span class="font-medium text-gray-900 dark:text-white">
                  ${{ (usageStats[row.id]?.today_actual_cost ?? 0).toFixed(4) }}
                </span>
              </div>
              <div class="mt-0.5 flex items-center gap-1.5">
                <span class="text-gray-500 dark:text-gray-400">{{ t('keys.total') }}:</span>
                <span class="font-medium text-gray-900 dark:text-white">
                  ${{ (usageStats[row.id]?.total_actual_cost ?? 0).toFixed(4) }}
                </span>
              </div>
              <!-- Quota progress (if quota is set) -->
              <div v-if="row.quota > 0" class="mt-1.5">
                <div class="flex items-center gap-1.5">
                  <span class="text-gray-500 dark:text-gray-400">{{ t('keys.quota') }}:</span>
                  <span :class="[
                    'font-medium',
                    row.quota_used >= row.quota ? 'text-red-500' :
                    row.quota_used >= row.quota * 0.8 ? 'text-yellow-500' :
                    'text-gray-900 dark:text-white'
                  ]">
                    ${{ row.quota_used?.toFixed(2) || '0.00' }} / ${{ row.quota?.toFixed(2) }}
                  </span>
                </div>
                <div class="mt-1 h-1.5 w-full overflow-hidden rounded-full bg-gray-200 dark:bg-dark-600">
                  <div
                    :class="[
                      'h-full rounded-full transition-all',
                      row.quota_used >= row.quota ? 'bg-red-500' :
                      row.quota_used >= row.quota * 0.8 ? 'bg-yellow-500' :
                      'bg-primary-500'
                    ]"
                    :style="{ width: Math.min((row.quota_used / row.quota) * 100, 100) + '%' }"
                  />
                </div>
              </div>
            </div>
          </template>

          <template #cell-rate_limit="{ row }">
            <div v-if="row.rate_limit_5h > 0 || row.rate_limit_1d > 0 || row.rate_limit_7d > 0" class="space-y-1.5 min-w-[140px]">
              <!-- 5h window -->
              <div v-if="row.rate_limit_5h > 0">
                <div class="flex items-center justify-between text-xs">
                  <span class="text-gray-500 dark:text-gray-400">5h</span>
                  <span :class="[
                    'font-medium tabular-nums',
                    row.usage_5h >= row.rate_limit_5h ? 'text-red-500' :
                    row.usage_5h >= row.rate_limit_5h * 0.8 ? 'text-yellow-500' :
                    'text-gray-700 dark:text-gray-300'
                  ]">
                    ${{ row.usage_5h?.toFixed(2) || '0.00' }}/${{ row.rate_limit_5h?.toFixed(2) }}
                  </span>
                </div>
                <div class="h-1 w-full overflow-hidden rounded-full bg-gray-200 dark:bg-dark-600">
                  <div
                    :class="[
                      'h-full rounded-full transition-all',
                      row.usage_5h >= row.rate_limit_5h ? 'bg-red-500' :
                      row.usage_5h >= row.rate_limit_5h * 0.8 ? 'bg-yellow-500' :
                      'bg-emerald-500'
                    ]"
                    :style="{ width: Math.min((row.usage_5h / row.rate_limit_5h) * 100, 100) + '%' }"
                  />
                </div>
                <div v-if="row.reset_5h_at && formatResetTime(row.reset_5h_at)" class="text-[10px] text-gray-400 dark:text-gray-500 tabular-nums">
                  ⟳ {{ formatResetTime(row.reset_5h_at) }}
                </div>
              </div>
              <!-- 1d window -->
              <div v-if="row.rate_limit_1d > 0">
                <div class="flex items-center justify-between text-xs">
                  <span class="text-gray-500 dark:text-gray-400">1d</span>
                  <span :class="[
                    'font-medium tabular-nums',
                    row.usage_1d >= row.rate_limit_1d ? 'text-red-500' :
                    row.usage_1d >= row.rate_limit_1d * 0.8 ? 'text-yellow-500' :
                    'text-gray-700 dark:text-gray-300'
                  ]">
                    ${{ row.usage_1d?.toFixed(2) || '0.00' }}/${{ row.rate_limit_1d?.toFixed(2) }}
                  </span>
                </div>
                <div class="h-1 w-full overflow-hidden rounded-full bg-gray-200 dark:bg-dark-600">
                  <div
                    :class="[
                      'h-full rounded-full transition-all',
                      row.usage_1d >= row.rate_limit_1d ? 'bg-red-500' :
                      row.usage_1d >= row.rate_limit_1d * 0.8 ? 'bg-yellow-500' :
                      'bg-emerald-500'
                    ]"
                    :style="{ width: Math.min((row.usage_1d / row.rate_limit_1d) * 100, 100) + '%' }"
                  />
                </div>
                <div v-if="row.reset_1d_at && formatResetTime(row.reset_1d_at)" class="text-[10px] text-gray-400 dark:text-gray-500 tabular-nums">
                  ⟳ {{ formatResetTime(row.reset_1d_at) }}
                </div>
              </div>
              <!-- 7d window -->
              <div v-if="row.rate_limit_7d > 0">
                <div class="flex items-center justify-between text-xs">
                  <span class="text-gray-500 dark:text-gray-400">7d</span>
                  <span :class="[
                    'font-medium tabular-nums',
                    row.usage_7d >= row.rate_limit_7d ? 'text-red-500' :
                    row.usage_7d >= row.rate_limit_7d * 0.8 ? 'text-yellow-500' :
                    'text-gray-700 dark:text-gray-300'
                  ]">
                    ${{ row.usage_7d?.toFixed(2) || '0.00' }}/${{ row.rate_limit_7d?.toFixed(2) }}
                  </span>
                </div>
                <div class="h-1 w-full overflow-hidden rounded-full bg-gray-200 dark:bg-dark-600">
                  <div
                    :class="[
                      'h-full rounded-full transition-all',
                      row.usage_7d >= row.rate_limit_7d ? 'bg-red-500' :
                      row.usage_7d >= row.rate_limit_7d * 0.8 ? 'bg-yellow-500' :
                      'bg-emerald-500'
                    ]"
                    :style="{ width: Math.min((row.usage_7d / row.rate_limit_7d) * 100, 100) + '%' }"
                  />
                </div>
                <div v-if="row.reset_7d_at && formatResetTime(row.reset_7d_at)" class="text-[10px] text-gray-400 dark:text-gray-500 tabular-nums">
                  ⟳ {{ formatResetTime(row.reset_7d_at) }}
                </div>
              </div>
              <!-- Reset button -->
              <button
                v-if="row.usage_5h > 0 || row.usage_1d > 0 || row.usage_7d > 0"
                @click.stop="confirmResetRateLimitFromTable(row)"
                class="mt-0.5 inline-flex items-center gap-1 rounded-sm px-1.5 py-0.5 text-xs text-gray-500 transition-colors hover:bg-gray-100 hover:text-primary-600 dark:hover:bg-dark-700 dark:hover:text-primary-400"
                :title="t('keys.resetRateLimitUsage')"
              >
                <Icon name="refresh" size="xs" />
                {{ t('keys.resetUsage') }}
              </button>
            </div>
            <span v-else class="text-sm text-gray-400 dark:text-dark-500">-</span>
          </template>

          <template #cell-expires_at="{ value }">
            <span v-if="value" :class="[
              'text-sm',
              new Date(value) < new Date() ? 'text-red-500 dark:text-red-400' : 'text-gray-500 dark:text-dark-400'
            ]">
              {{ formatDateTime(value) }}
            </span>
            <span v-else class="text-sm text-gray-400 dark:text-dark-500">{{ t('keys.noExpiration') }}</span>
          </template>

          <template #cell-status="{ value }">
            <span :class="[
              'badge',
              value === 'active' ? 'badge-success' :
              value === 'quota_exhausted' ? 'badge-warning' :
              value === 'expired' ? 'badge-danger' :
              'badge-gray'
            ]">
              {{ t('keys.status.' + value) }}
            </span>
          </template>

          <template #cell-last_used_at="{ value }">
            <span v-if="value" class="text-sm text-gray-500 dark:text-dark-400">
              {{ formatDateTime(value) }}
            </span>
            <span v-else class="text-sm text-gray-400 dark:text-dark-500">-</span>
          </template>

          <template #cell-last_used_ip="{ value }">
            <span v-if="value" class="text-sm text-gray-500 dark:text-dark-400">
              {{ value }}
            </span>
            <span v-else class="text-sm text-gray-400 dark:text-dark-500">-</span>
          </template>

          <template #cell-created_at="{ value }">
            <span class="text-sm text-gray-500 dark:text-dark-400">{{ formatDateTime(value) }}</span>
          </template>

          <template #cell-actions="{ row }">
            <div class="flex items-center gap-0.5">
              <!-- Use Key Button -->
              <button type="button" class="key-row-action" @click="openUseKeyModal(row)">
                <UIcon name="i-lucide-square-terminal" class="size-4" />
                <span>{{ t('keys.useKey') }}</span>
              </button>
              <!-- Import to CC Switch Button -->
              <button
                v-if="!publicSettings?.hide_ccs_import_button"
                type="button"
                class="key-row-action"
                @click="importToCcswitch(row)"
              >
                <UIcon name="i-lucide-upload" class="size-4" />
                <span>{{ t('keys.importToCcSwitch') }}</span>
              </button>
              <!-- Toggle Status Button -->
              <button type="button" class="key-row-action" @click="toggleKeyStatus(row)">
                <UIcon v-if="row.status === 'active'" name="i-lucide-ban" class="size-4" />
                <UIcon v-else name="i-lucide-circle-check" class="size-4" />
                <span>{{ row.status === 'active' ? t('keys.disable') : t('keys.enable') }}</span>
              </button>
              <!-- Edit Button -->
              <button type="button" class="key-row-action" @click="editKey(row)">
                <UIcon name="i-lucide-pencil" class="size-4" />
                <span>{{ t('common.edit') }}</span>
              </button>
              <!-- Delete Button -->
              <button type="button" class="key-row-action key-row-action--danger" @click="confirmDelete(row)">
                <UIcon name="i-lucide-trash-2" class="size-4" />
                <span>{{ t('common.delete') }}</span>
              </button>
            </div>
          </template>

          <template #empty>
            <EmptyState
              :title="t('keys.noKeysYet')"
              :description="t('keys.createFirstKey')"
              :action-text="t('keys.createKey')"
              @action="showCreateModal = true"
            />
          </template>
        </DataTable>
      </template>

      <template #pagination>
        <Pagination
          class="signal-pagination"
          v-if="pagination.total > 0"
          :page="pagination.page"
          :total="pagination.total"
          :page-size="pagination.page_size"
          @update:page="handlePageChange"
          @update:pageSize="handlePageSizeChange"
        />
      </template>
    </TablePageLayout>

    <!-- Create/Edit Modal -->
    <BaseDialog
      :show="showCreateModal || showEditModal"
      :title="showEditModal ? t('keys.editKey') : t('keys.createKey')"
      width="normal"
      @close="closeModals"
    >
      <form id="key-form" @submit.prevent="handleSubmit" class="space-y-5">
        <UFormField :label="t('keys.nameLabel')" required size="lg">
          <UInput
            v-model="formData.name"
            type="text"
            required
            size="lg" :ui="{ base: 'py-[11px]' }"
            class="w-full"
            :placeholder="t('keys.namePlaceholder')"
            data-tour="key-form-name"
          />
        </UFormField>

        <fieldset>
          <legend class="mb-1.5 text-sm font-medium text-default">{{ t('keys.providerLabel') }}</legend>
          <div class="grid grid-cols-2 gap-2 sm:grid-cols-4">
            <label
              v-for="provider in keyProviders"
              :key="provider.value"
              class="relative min-w-0"
            >
              <input
                type="radio"
                name="key-provider"
                :value="provider.value"
                :checked="activeKeyProvider === provider.value"
                :disabled="!availableKeyProviders.has(provider.value)"
                class="peer sr-only"
                @change="selectKeyProvider(provider.value)"
              />
              <span class="flex min-h-24 flex-col items-center justify-center gap-2 rounded-lg border border-accented bg-default px-2 py-3 text-sm font-semibold text-highlighted transition-colors peer-enabled:cursor-pointer hover:peer-enabled:border-inverted peer-checked:border-inverted peer-checked:bg-elevated peer-checked:ring-1 peer-checked:ring-inverted peer-focus-visible:outline-solid peer-focus-visible:outline-2 peer-focus-visible:outline-offset-2 peer-focus-visible:outline-inverted peer-disabled:cursor-not-allowed peer-disabled:opacity-40 dark:border-dark-500 dark:bg-transparent dark:text-gray-100 dark:hover:peer-enabled:border-primary/60 dark:peer-checked:border-primary dark:peer-checked:bg-primary/10 dark:peer-checked:ring-primary dark:peer-focus-visible:outline-primary">
                <span class="flex h-8 items-center justify-center gap-1" aria-hidden="true">
                  <span
                    v-for="platform in provider.icons"
                    :key="platform"
                    :class="['flex h-8 w-8 items-center justify-center rounded-lg', platformBadgeLightClass(platform)]"
                  >
                    <PlatformIcon :platform="platform" size="lg" />
                  </span>
                </span>
                <span class="max-w-full wrap-break-word text-center">{{ provider.label }}</span>
              </span>
              <span
                v-if="activeKeyProvider === provider.value"
                class="pointer-events-none absolute right-1.5 top-1.5 flex h-4 w-4 items-center justify-center rounded-full bg-inverted text-inverted dark:bg-primary dark:text-inverted"
                aria-hidden="true"
              >
                <UIcon name="i-lucide-check" class="size-3" />
              </span>
            </label>
          </div>
        </fieldset>

        <UFormField :label="t('keys.groupLabel')" size="lg">
          <template #hint>
            <label class="flex items-center gap-2 text-sm text-default">
              <input v-model="formData.smart_routing" type="checkbox" class="size-4 rounded-sm border-accented accent-[var(--ui-primary)]" />
              {{ t('keys.smartRouting') }}
            </label>
          </template>
          <p v-if="formData.smart_routing" class="mb-3 text-xs text-muted">
            {{ t('keys.smartRoutingHint') }}
          </p>
          <div v-if="formData.smart_routing" class="space-y-2">
            <div
              v-for="(groupId, index) in formData.group_ids"
              :key="`${groupId}-${index}`"
              class="flex items-center gap-2 rounded-lg px-3 py-2 ring ring-default"
            >
              <span class="shrink-0 text-xs text-muted">{{ t('keys.smartRoutingRoute') }} {{ index + 1 }}</span>
              <div class="min-w-0 flex-1">
                <GroupBadge
                  v-if="groupOptionById(groupId)"
                  :name="groupOptionById(groupId)!.label"
                  :platform="groupOptionById(groupId)!.platform"
                  :subscription-type="groupOptionById(groupId)!.subscriptionType"
                  :rate-multiplier="groupOptionById(groupId)!.rate"
                  :user-rate-multiplier="groupOptionById(groupId)!.userRate"
                  :peak-rate-enabled="groupOptionById(groupId)!.peakRateEnabled"
                  :peak-start="groupOptionById(groupId)!.peakStart"
                  :peak-end="groupOptionById(groupId)!.peakEnd"
                  :peak-rate-multiplier="groupOptionById(groupId)!.peakRateMultiplier"
                />
                <span v-else class="text-sm text-muted">#{{ groupId }}</span>
              </div>
              <button type="button" class="key-route-action" @click="moveSmartRoute(index, -1)"><UIcon name="i-lucide-arrow-up" class="size-4" /></button>
              <button type="button" class="key-route-action" @click="moveSmartRoute(index, 1)"><UIcon name="i-lucide-arrow-down" class="size-4" /></button>
              <button type="button" class="key-route-action key-route-action--danger" @click="removeSmartRoute(index)"><UIcon name="i-lucide-x" class="size-4" /></button>
            </div>
            <Select
              :model-value="null"
              :options="smartRoutingAddOptions"
              :placeholder="t('keys.smartRoutingAdd')"
              :searchable="true"
              :search-placeholder="t('keys.searchGroup')"
              @update:model-value="addSmartRoute"
            />
            <p v-if="formData.group_ids.length === 0" class="text-xs text-muted">{{ t('keys.smartRoutingEmpty') }}</p>
          </div>
          <Select
            v-else
            v-model="formData.group_id"
            :options="providerGroupOptions"
            :placeholder="t('keys.selectGroup')"
            :searchable="true"
            :search-placeholder="t('keys.searchGroup')"
            data-tour="key-form-group"
          >
            <template #selected="{ option }">
              <GroupBadge
                v-if="option"
                :name="(option as unknown as GroupOption).label"
                :platform="(option as unknown as GroupOption).platform"
                :subscription-type="(option as unknown as GroupOption).subscriptionType"
                :rate-multiplier="(option as unknown as GroupOption).rate"
                :user-rate-multiplier="(option as unknown as GroupOption).userRate"
                :peak-rate-enabled="(option as unknown as GroupOption).peakRateEnabled"
                :peak-start="(option as unknown as GroupOption).peakStart"
                :peak-end="(option as unknown as GroupOption).peakEnd"
                :peak-rate-multiplier="(option as unknown as GroupOption).peakRateMultiplier"
              />
              <span v-else class="text-dimmed">{{ t('keys.selectGroup') }}</span>
            </template>
            <template #option="{ option, selected }">
              <GroupOptionItem
                :name="(option as unknown as GroupOption).label"
                :platform="(option as unknown as GroupOption).platform"
                :subscription-type="(option as unknown as GroupOption).subscriptionType"
                :rate-multiplier="(option as unknown as GroupOption).rate"
                :user-rate-multiplier="(option as unknown as GroupOption).userRate"
                :peak-rate-enabled="(option as unknown as GroupOption).peakRateEnabled"
                :peak-start="(option as unknown as GroupOption).peakStart"
                :peak-end="(option as unknown as GroupOption).peakEnd"
                :peak-rate-multiplier="(option as unknown as GroupOption).peakRateMultiplier"
                :description="(option as unknown as GroupOption).description"
                :selected="selected"
              />
            </template>
          </Select>
        </UFormField>

        <!-- Custom Key Section (only for create) -->
        <div v-if="!showEditModal" class="space-y-3">
          <div class="flex items-center justify-between gap-4">
            <span id="key-custom-key-label" class="text-sm font-medium text-default">{{ t('keys.customKeyLabel') }}</span>
            <Toggle v-model="formData.use_custom_key" aria-labelledby="key-custom-key-label" />
          </div>
          <UFormField
            v-if="formData.use_custom_key"
            size="lg"
            :error="customKeyError || undefined"
            :help="customKeyError ? undefined : t('keys.customKeyHint')"
          >
            <UInput
              v-model="formData.custom_key"
              type="text"
              size="lg"
              class="w-full"
              :ui="{ base: 'font-mono py-[11px]' }"
              :placeholder="t('keys.customKeyPlaceholder')"
            />
          </UFormField>
        </div>

        <UFormField v-if="showEditModal" :label="t('keys.statusLabel')" size="lg">
          <Select
            v-model="formData.status"
            :options="statusOptions"
            :placeholder="t('keys.selectStatus')"
          />
        </UFormField>

        <!-- IP Restriction Section -->
        <div class="space-y-3">
          <div class="flex items-center justify-between gap-4">
            <span id="key-ip-restriction-label" class="text-sm font-medium text-default">{{ t('keys.ipRestriction') }}</span>
            <Toggle v-model="formData.enable_ip_restriction" aria-labelledby="key-ip-restriction-label" />
          </div>

          <div v-if="formData.enable_ip_restriction" class="space-y-4 pt-1">
            <UFormField :label="t('keys.ipWhitelist')" :help="t('keys.ipWhitelistHint')" size="lg">
              <UTextarea
                v-model="formData.ip_whitelist"
                :rows="3"
                size="lg"
                class="w-full"
                :ui="{ base: 'font-mono text-sm' }"
                :placeholder="t('keys.ipWhitelistPlaceholder')"
              />
            </UFormField>
            <UFormField :label="t('keys.ipBlacklist')" :help="t('keys.ipBlacklistHint')" size="lg">
              <UTextarea
                v-model="formData.ip_blacklist"
                :rows="3"
                size="lg"
                class="w-full"
                :ui="{ base: 'font-mono text-sm' }"
                :placeholder="t('keys.ipBlacklistPlaceholder')"
              />
            </UFormField>
          </div>
        </div>

        <!-- Quota Limit Section -->
        <div class="space-y-4">
          <UFormField :label="t('keys.quotaLimit')" :help="t('keys.quotaAmountHint')" size="lg">
            <UInput
              v-model.number="formData.quota"
              type="number"
              step="0.01"
              min="0"
              size="lg" :ui="{ base: 'py-[11px]' }"
              class="w-full"
              :placeholder="t('keys.quotaAmountPlaceholder')"
            >
              <template #leading><span class="text-sm text-muted">$</span></template>
            </UInput>
          </UFormField>

          <!-- Quota used display (only in edit mode) -->
          <UFormField v-if="showEditModal && selectedKey && selectedKey.quota > 0" :label="t('keys.quotaUsed')" size="lg">
            <div class="flex items-center gap-2">
              <div class="flex-1 rounded-md bg-elevated px-3 py-2 text-sm">
                <span class="font-medium text-highlighted">
                  ${{ selectedKey.quota_used?.toFixed(4) || '0.0000' }}
                </span>
                <span class="mx-2 text-dimmed">/</span>
                <span class="text-muted">
                  ${{ selectedKey.quota?.toFixed(2) || '0.00' }}
                </span>
              </div>
              <UButton
                color="neutral"
                variant="outline"
                size="lg"
                :title="t('keys.resetQuotaUsed')"
                @click="confirmResetQuota"
              >
                {{ t('keys.reset') }}
              </UButton>
            </div>
          </UFormField>
        </div>

        <!-- Rate Limit Section -->
        <div class="space-y-3">
          <div class="flex items-center justify-between gap-4">
            <span id="key-rate-limit-label" class="text-sm font-medium text-default">{{ t('keys.rateLimitSection') }}</span>
            <Toggle v-model="formData.enable_rate_limit" aria-labelledby="key-rate-limit-label" />
          </div>

          <div v-if="formData.enable_rate_limit" class="space-y-4">
            <p class="text-xs text-muted">{{ t('keys.rateLimitHint') }}</p>
            <!-- 5-Hour Limit -->
            <UFormField :label="t('keys.rateLimit5h')" size="lg">
              <UInput v-model.number="formData.rate_limit_5h" type="number" step="0.01" min="0" size="lg" :ui="{ base: 'py-[11px]' }" class="w-full" :placeholder="'0'">
                <template #leading><span class="text-sm text-muted">$</span></template>
              </UInput>
              <!-- Usage info (edit mode only) -->
              <div v-if="showEditModal && selectedKey && selectedKey.rate_limit_5h > 0" class="mt-2">
                <div class="rounded-md bg-elevated px-3 py-2 text-sm">
                  <span :class="[
                    'font-medium',
                    selectedKey.usage_5h >= selectedKey.rate_limit_5h ? 'text-red-500' :
                    selectedKey.usage_5h >= selectedKey.rate_limit_5h * 0.8 ? 'text-yellow-500' :
                    'text-highlighted'
                  ]">
                    ${{ selectedKey.usage_5h?.toFixed(4) || '0.0000' }}
                  </span>
                  <span class="mx-2 text-dimmed">/</span>
                  <span class="text-muted">
                    ${{ selectedKey.rate_limit_5h?.toFixed(2) || '0.00' }}
                  </span>
                </div>
                <div class="mt-1 h-1.5 w-full overflow-hidden rounded-full bg-accented">
                  <div
                    :class="[
                      'h-full rounded-full transition-all',
                      selectedKey.usage_5h >= selectedKey.rate_limit_5h ? 'bg-red-500' :
                      selectedKey.usage_5h >= selectedKey.rate_limit_5h * 0.8 ? 'bg-yellow-500' :
                      'bg-green-500'
                    ]"
                    :style="{ width: Math.min((selectedKey.usage_5h / selectedKey.rate_limit_5h) * 100, 100) + '%' }"
                  />
                </div>
              </div>
            </UFormField>

            <!-- Daily Limit -->
            <UFormField :label="t('keys.rateLimit1d')" size="lg">
              <UInput v-model.number="formData.rate_limit_1d" type="number" step="0.01" min="0" size="lg" :ui="{ base: 'py-[11px]' }" class="w-full" :placeholder="'0'">
                <template #leading><span class="text-sm text-muted">$</span></template>
              </UInput>
              <!-- Usage info (edit mode only) -->
              <div v-if="showEditModal && selectedKey && selectedKey.rate_limit_1d > 0" class="mt-2">
                <div class="rounded-md bg-elevated px-3 py-2 text-sm">
                  <span :class="[
                    'font-medium',
                    selectedKey.usage_1d >= selectedKey.rate_limit_1d ? 'text-red-500' :
                    selectedKey.usage_1d >= selectedKey.rate_limit_1d * 0.8 ? 'text-yellow-500' :
                    'text-highlighted'
                  ]">
                    ${{ selectedKey.usage_1d?.toFixed(4) || '0.0000' }}
                  </span>
                  <span class="mx-2 text-dimmed">/</span>
                  <span class="text-muted">
                    ${{ selectedKey.rate_limit_1d?.toFixed(2) || '0.00' }}
                  </span>
                </div>
                <div class="mt-1 h-1.5 w-full overflow-hidden rounded-full bg-accented">
                  <div
                    :class="[
                      'h-full rounded-full transition-all',
                      selectedKey.usage_1d >= selectedKey.rate_limit_1d ? 'bg-red-500' :
                      selectedKey.usage_1d >= selectedKey.rate_limit_1d * 0.8 ? 'bg-yellow-500' :
                      'bg-green-500'
                    ]"
                    :style="{ width: Math.min((selectedKey.usage_1d / selectedKey.rate_limit_1d) * 100, 100) + '%' }"
                  />
                </div>
              </div>
            </UFormField>

            <!-- 7-Day Limit -->
            <UFormField :label="t('keys.rateLimit7d')" size="lg">
              <UInput v-model.number="formData.rate_limit_7d" type="number" step="0.01" min="0" size="lg" :ui="{ base: 'py-[11px]' }" class="w-full" :placeholder="'0'">
                <template #leading><span class="text-sm text-muted">$</span></template>
              </UInput>
              <!-- Usage info (edit mode only) -->
              <div v-if="showEditModal && selectedKey && selectedKey.rate_limit_7d > 0" class="mt-2">
                <div class="rounded-md bg-elevated px-3 py-2 text-sm">
                  <span :class="[
                    'font-medium',
                    selectedKey.usage_7d >= selectedKey.rate_limit_7d ? 'text-red-500' :
                    selectedKey.usage_7d >= selectedKey.rate_limit_7d * 0.8 ? 'text-yellow-500' :
                    'text-highlighted'
                  ]">
                    ${{ selectedKey.usage_7d?.toFixed(4) || '0.0000' }}
                  </span>
                  <span class="mx-2 text-dimmed">/</span>
                  <span class="text-muted">
                    ${{ selectedKey.rate_limit_7d?.toFixed(2) || '0.00' }}
                  </span>
                </div>
                <div class="mt-1 h-1.5 w-full overflow-hidden rounded-full bg-accented">
                  <div
                    :class="[
                      'h-full rounded-full transition-all',
                      selectedKey.usage_7d >= selectedKey.rate_limit_7d ? 'bg-red-500' :
                      selectedKey.usage_7d >= selectedKey.rate_limit_7d * 0.8 ? 'bg-yellow-500' :
                      'bg-green-500'
                    ]"
                    :style="{ width: Math.min((selectedKey.usage_7d / selectedKey.rate_limit_7d) * 100, 100) + '%' }"
                  />
                </div>
              </div>
            </UFormField>

            <!-- Reset Rate Limit button (edit mode only) -->
            <div v-if="showEditModal && selectedKey && (selectedKey.rate_limit_5h > 0 || selectedKey.rate_limit_1d > 0 || selectedKey.rate_limit_7d > 0)">
              <UButton color="neutral" variant="outline" icon="i-lucide-rotate-ccw" @click="confirmResetRateLimit">
                {{ t('keys.resetRateLimitUsage') }}
              </UButton>
            </div>
          </div>
        </div>

        <UFormField :label="t('keys.concurrencyLimit')" :help="t('keys.concurrencyLimitHint')" size="lg">
          <UInput v-model.number="formData.concurrency_limit" type="number" min="0" max="100000" step="1" size="lg" :ui="{ base: 'py-[11px]' }" class="w-full" />
        </UFormField>

        <!-- DeepSeek automatic tools -->
        <div v-if="showDeepSeekToolInjection" class="space-y-2">
          <div class="flex items-center justify-between gap-4">
            <span id="key-deepseek-tool-injection-label" class="text-sm font-medium text-default">
              {{ t('keys.deepseekToolInjection') }}
            </span>
            <Toggle
              v-model="formData.deepseek_tool_injection_enabled"
              aria-labelledby="key-deepseek-tool-injection-label"
              aria-describedby="key-deepseek-tool-injection-hint"
            />
          </div>
          <p id="key-deepseek-tool-injection-hint" class="text-xs text-muted">
            {{ t('keys.deepseekToolInjectionHint') }}
          </p>
        </div>

        <!-- Anthropic Cache TTL Override -->
        <UFormField :label="t('keys.anthropicCacheTtlMode')" :help="t('keys.anthropicCacheTtlModeHint')" size="lg">
          <Select
            v-model="formData.anthropic_cache_ttl_mode"
            :options="[
              { value: 'inherit', label: t('keys.anthropicCacheTtlInherit') },
              { value: 'off', label: t('keys.anthropicCacheTtlOff') },
              { value: '1h', label: t('keys.anthropicCacheTtl1h') },
              { value: '5m', label: t('keys.anthropicCacheTtl5m') },
            ]"
          />
        </UFormField>

        <!-- Expiration Section -->
        <div class="space-y-3">
          <div class="flex items-center justify-between gap-4">
            <span id="key-expiration-label" class="text-sm font-medium text-default">{{ t('keys.expiration') }}</span>
            <Toggle v-model="formData.enable_expiration" aria-labelledby="key-expiration-label" />
          </div>

          <div v-if="formData.enable_expiration" class="space-y-4 pt-1">
            <!-- Quick select buttons (for both create and edit mode) -->
            <div class="flex flex-wrap gap-2">
              <UButton
                v-for="days in ['7', '30', '90']"
                :key="days"
                :color="formData.expiration_preset === days ? 'primary' : 'neutral'"
                :variant="formData.expiration_preset === days ? 'soft' : 'outline'"
                @click="setExpirationDays(parseInt(days))"
              >
                {{ showEditModal ? t('keys.extendDays', { days }) : t('keys.expiresInDays', { days }) }}
              </UButton>
              <UButton
                :color="formData.expiration_preset === 'custom' ? 'primary' : 'neutral'"
                :variant="formData.expiration_preset === 'custom' ? 'soft' : 'outline'"
                @click="formData.expiration_preset = 'custom'"
              >
                {{ t('keys.customDate') }}
              </UButton>
            </div>

            <!-- Date picker (always show for precise adjustment) -->
            <UFormField :label="t('keys.expirationDate')" :help="t('keys.expirationDateHint')" size="lg">
              <UInput v-model="formData.expiration_date" type="datetime-local" size="lg" class="w-full" :ui="{ base: 'dark:[color-scheme:dark] py-[11px]' }" />
            </UFormField>

            <!-- Current expiration display (only in edit mode) -->
            <div v-if="showEditModal && selectedKey?.expires_at" class="text-sm">
              <span class="text-muted">{{ t('keys.currentExpiration') }}: </span>
              <span class="font-medium text-highlighted">
                {{ formatDateTime(selectedKey.expires_at) }}
              </span>
            </div>
          </div>
        </div>
      </form>
      <template #footer>
        <div class="flex justify-end gap-2">
          <UButton color="neutral" variant="outline" size="lg" @click="closeModals">
            {{ t('common.cancel') }}
          </UButton>
          <UButton
            form="key-form"
            type="submit"
            size="lg"
            :loading="submitting"
            :disabled="submitting"
            data-tour="key-form-submit"
          >
            {{
              submitting
                ? t('keys.saving')
                : showEditModal
                  ? t('common.update')
                  : t('common.create')
            }}
          </UButton>
        </div>
      </template>
    </BaseDialog>

    <!-- Delete Confirmation Dialog -->
    <ConfirmDialog
      :show="showDeleteDialog"
      :title="t('keys.deleteKey')"
      :message="t('keys.deleteConfirmMessage', { name: selectedKey?.name })"
      :confirm-text="t('common.delete')"
      :cancel-text="t('common.cancel')"
      :danger="true"
      @confirm="handleDelete"
      @cancel="showDeleteDialog = false"
    />

    <!-- Bulk Delete Confirmation Dialog -->
    <ConfirmDialog
      :show="showBulkDeleteDialog"
      :title="t('keys.deleteKey')"
      :message="bulkDeleteMessage"
      :confirm-text="t('common.delete')"
      :cancel-text="t('common.cancel')"
      :danger="true"
      @confirm="handleBulkDelete"
      @cancel="showBulkDeleteDialog = false"
    />

    <!-- Reset Quota Confirmation Dialog -->
    <ConfirmDialog
      :show="showResetQuotaDialog"
      :title="t('keys.resetQuotaTitle')"
      :message="t('keys.resetQuotaConfirmMessage', { name: selectedKey?.name, used: selectedKey?.quota_used?.toFixed(4) })"
      :confirm-text="t('keys.reset')"
      :cancel-text="t('common.cancel')"
      :danger="true"
      @confirm="resetQuotaUsed"
      @cancel="showResetQuotaDialog = false"
    />

    <!-- Reset Rate Limit Confirmation Dialog -->
    <ConfirmDialog
      :show="showResetRateLimitDialog"
      :title="t('keys.resetRateLimitTitle')"
      :message="t('keys.resetRateLimitConfirmMessage', { name: selectedKey?.name })"
      :confirm-text="t('keys.reset')"
      :cancel-text="t('common.cancel')"
      :danger="true"
      @confirm="resetRateLimitUsage"
      @cancel="showResetRateLimitDialog = false"
    />

    <!-- Use Key Modal -->
    <UseKeyModal
      :show="showUseKeyModal"
      :api-key="selectedKey?.key || ''"
      :base-url="publicSettings?.api_base_url || ''"
      :platform="selectedKey?.group?.platform || null"
      :allow-messages-dispatch="selectedKey?.group?.allow_messages_dispatch || false"
      @close="closeUseKeyModal"
    />

    <!-- CCS import: pick platform + model, then open ccswitch:// deeplink -->
    <BaseDialog
      :show="showCcsImportModal"
      :title="t('keys.ccsImport.title')"
      width="narrow"
      @close="closeCcsImportModal"
    >
      <div class="space-y-4">
        <p class="text-sm text-gray-600 dark:text-gray-400">
          {{ t('keys.ccsImport.description') }}
        </p>
        <div>
          <label class="input-label" for="ccs-import-platform">{{ t('keys.ccsImport.platform') }}</label>
          <Select
            id="ccs-import-platform"
            :model-value="ccsImportApp"
            :options="ccsAppOptions"
            :aria-label="t('keys.ccsImport.platform')"
            @update:model-value="onCcsAppChange"
          />
        </div>
        <div>
          <label class="input-label" for="ccs-import-model">{{ t('keys.ccsImport.model') }}</label>
          <Select
            id="ccs-import-model"
            :model-value="ccsImportModel"
            :options="ccsModelOptions"
            :placeholder="t('keys.ccsImport.modelPlaceholder')"
            :searchable="true"
            :creatable="true"
            :clearable="true"
            :aria-label="t('keys.ccsImport.model')"
            @update:model-value="onCcsModelChange"
          />
          <p class="input-hint mt-1">
            {{ ccsModelsLoading ? t('keys.ccsImport.modelLoading') : t('keys.ccsImport.modelHint') }}
          </p>
        </div>
        <UAlert
          v-if="ccsImportDeeplinkUnsupported"
          color="warning"
          variant="subtle"
          icon="i-lucide-triangle-alert"
          :description="t('keys.ccsImport.deeplinkUnsupported')"
          :ui="{ description: 'text-xs leading-5' }"
        />
      </div>
      <template #footer>
        <div class="flex justify-end gap-2">
          <UButton color="neutral" variant="outline" size="lg" @click="closeCcsImportModal">
            {{ t('common.cancel') }}
          </UButton>
          <UButton size="lg" :disabled="ccsImportDeeplinkUnsupported" @click="confirmCcsImport">
            {{ t('common.import') }}
          </UButton>
        </div>
      </template>
    </BaseDialog>

    <!-- Group Selector Dropdown (Teleported to body to avoid overflow clipping) -->
    <Teleport to="body">
      <div
        v-if="groupSelectorKeyId !== null && dropdownPosition"
        ref="dropdownRef"
        class="animate-in fade-in slide-in-from-top-2 fixed z-100000020 w-max max-w-[calc(100vw-16px)] overflow-hidden rounded-lg bg-default shadow-lg ring ring-default duration-200 sm:min-w-[380px]"
        style="pointer-events: auto !important;"
        :style="{
          top: dropdownPosition.top !== undefined ? dropdownPosition.top + 'px' : undefined,
          bottom: dropdownPosition.bottom !== undefined ? dropdownPosition.bottom + 'px' : undefined,
          left: dropdownPosition.left + 'px'
        }"
      >
        <!-- Search box -->
        <div class="border-b border-default p-2">
          <UInput
            v-model="groupSearchQuery"
            type="text"
            icon="i-lucide-search"
            variant="soft"
            class="w-full"
            :placeholder="t('keys.searchGroup')"
            @click.stop
          />
        </div>
        <!-- Group list -->
        <div class="max-h-80 overflow-y-auto p-1.5">
          <button
            v-for="option in filteredGroupOptions"
            :key="option.value ?? 'null'"
            @click="changeGroup(selectedKeyForGroup!, option.value)"
            :class="[
              'flex w-full items-center justify-between rounded-lg px-3 py-2.5 text-sm transition-colors',
              'border-b border-gray-100 last:border-0 dark:border-dark-700',
              selectedKeyForGroup?.group_id === option.value ||
              (!selectedKeyForGroup?.group_id && option.value === null)
                ? 'bg-primary-50 dark:bg-primary-900/20'
                : 'hover:bg-gray-100 dark:hover:bg-dark-700'
            ]"
            :title="option.description || undefined"
          >
            <GroupOptionItem
              :name="option.label"
              :platform="option.platform"
              :subscription-type="option.subscriptionType"
              :rate-multiplier="option.rate"
              :user-rate-multiplier="option.userRate"
              :peak-rate-enabled="option.peakRateEnabled"
              :peak-start="option.peakStart"
              :peak-end="option.peakEnd"
              :peak-rate-multiplier="option.peakRateMultiplier"
              :description="option.description"
              :selected="
                selectedKeyForGroup?.group_id === option.value ||
                (!selectedKeyForGroup?.group_id && option.value === null)
              "
            />
          </button>
          <!-- Empty state when search has no results -->
          <div v-if="filteredGroupOptions.length === 0" class="py-4 text-center text-sm text-gray-400 dark:text-gray-500">
            {{ t('keys.noGroupFound') }}
          </div>
        </div>
      </div>
    </Teleport>
  </AppLayout>
</template>

<script setup lang="ts">
import '@/styles/console-workspace.css'
	import { ref, reactive, computed, watch, onMounted, onUnmounted, type ComponentPublicInstance } from 'vue'
	import { useI18n } from 'vue-i18n'
	import { useAppStore } from '@/stores/app'
	import { useOnboardingStore } from '@/stores/onboarding'
	import { useClipboard } from '@/composables/useClipboard'
import { getPersistedPageSize } from '@/composables/usePersistedPageSize'

const { t } = useI18n()

// The concurrency cell carries its own explanation: hover shows this sentence, keyboard
// focus and screen readers get it from aria-label.
const concurrencyHint = (row: Pick<ApiKey, 'concurrency_limit'>) => {
  const limit = row.concurrency_limit
  return limit ? t('keys.currentConcurrencyHint', { limit }) : t('keys.currentConcurrencyNoLimitHint')
}
import { keysAPI, authAPI, usageAPI, userGroupsAPI, endpointsAPI } from '@/api'
import AppLayout from '@/components/layout/AppLayout.vue'
import TablePageLayout from '@/components/layout/TablePageLayout.vue'
	import DataTable from '@/components/common/DataTable.vue'
	import Pagination from '@/components/common/Pagination.vue'
	import BaseDialog from '@/components/common/BaseDialog.vue'
	import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
	import EmptyState from '@/components/common/EmptyState.vue'
	import Select from '@/components/common/Select.vue'
	import SearchInput from '@/components/common/SearchInput.vue'
	import Toggle from '@/components/common/Toggle.vue'
	import Icon from '@/components/icons/Icon.vue'
	import UseKeyModal from '@/components/keys/UseKeyModal.vue'
	import EndpointPopover from '@/components/keys/EndpointPopover.vue'
	import GroupBadge from '@/components/common/GroupBadge.vue'
	import GroupOptionItem from '@/components/common/GroupOptionItem.vue'
	import PlatformIcon from '@/components/common/PlatformIcon.vue'
	import UserPageHeader from '@/components/user/UserPageHeader.vue'
	import type { ApiKey, CustomEndpoint, Group, PublicSettings, SubscriptionType, GroupPlatform, UpdateApiKeyRequest } from '@/types'
import type { Column } from '@/components/common/types'
import type { BatchApiKeyUsageStats } from '@/api/usage'
import { formatDateTime } from '@/utils/format'
import { maskApiKey } from '@/utils/maskApiKey'
import { platformBadgeLightClass } from '@/utils/platformColors'
import {
  buildCcSwitchImportDeeplink,
  CC_SWITCH_APP_LABEL_KEYS,
  CC_SWITCH_APPS,
  CC_SWITCH_MODEL_SUGGESTIONS,
  ccSwitchProviderDeeplinkSupported,
  defaultCcSwitchModel,
  fetchCcSwitchModels,
  isCcSwitchApp,
  pickCcSwitchModel,
  resolveCcSwitchImportConfig,
  type CcSwitchApp
} from '@/utils/ccswitchImport'

// Helper to format date for datetime-local input
const formatDateTimeLocal = (isoDate: string): string => {
  const date = new Date(isoDate)
  const pad = (n: number) => n.toString().padStart(2, '0')
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`
}

interface GroupOption {
  value: number
  label: string
  description: string | null
  rate: number
  userRate: number | null
  peakRateEnabled: boolean
  peakStart: string
  peakEnd: string
  peakRateMultiplier: number
  subscriptionType: SubscriptionType
  platform: GroupPlatform
}

const appStore = useAppStore()
const onboardingStore = useOnboardingStore()
const { copyToClipboard: clipboardCopy } = useClipboard()

const allColumns = computed<Column[]>(() => [
  { key: 'name', label: t('common.name'), sortable: true },
  { key: 'id', label: t('keys.id'), sortable: true },
  { key: 'key', label: t('keys.apiKey'), sortable: false },
  { key: 'group', label: t('keys.group'), sortable: false },
  { key: 'current_concurrency', label: t('keys.currentConcurrency'), sortable: true },
  { key: 'usage', label: t('keys.usage'), sortable: false },
  { key: 'rate_limit', label: t('keys.rateLimitColumn'), sortable: false },
  { key: 'expires_at', label: t('keys.expiresAt'), sortable: true },
  { key: 'status', label: t('common.status'), sortable: true },
  { key: 'last_used_at', label: t('keys.lastUsedAt'), sortable: true },
  { key: 'last_used_ip', label: t('keys.lastUsedIP'), sortable: false },
  { key: 'created_at', label: t('keys.created'), sortable: true },
  { key: 'actions', label: t('common.actions'), sortable: false }
])

const ALWAYS_VISIBLE_COLUMNS = new Set(['name', 'actions'])
const DEFAULT_HIDDEN_COLUMNS = ['id', 'rate_limit', 'last_used_at', 'last_used_ip']
const HIDDEN_COLUMNS_KEY = 'api-key-hidden-columns'
const COLUMN_SETTINGS_VERSION_KEY = 'api-key-column-settings-version'
const COLUMN_SETTINGS_VERSION = 3
const VERSION_NEW_HIDDEN_COLUMNS: Record<number, string[]> = {
  2: ['last_used_ip'],
  3: ['id']
}

const toggleableColumns = computed(() =>
  allColumns.value.filter((col) => !ALWAYS_VISIBLE_COLUMNS.has(col.key))
)

const hiddenColumns = reactive<Set<string>>(new Set())

const saveColumnsToStorage = () => {
  try {
    localStorage.setItem(HIDDEN_COLUMNS_KEY, JSON.stringify([...hiddenColumns]))
    localStorage.setItem(COLUMN_SETTINGS_VERSION_KEY, String(COLUMN_SETTINGS_VERSION))
  } catch (error) {
    console.error('Failed to save API key table columns:', error)
  }
}

const loadSavedColumns = () => {
  hiddenColumns.clear()
  try {
    const saved = localStorage.getItem(HIDDEN_COLUMNS_KEY)
    if (saved) {
      const parsed = JSON.parse(saved) as string[]
      const validColumnKeys = new Set(allColumns.value.map((col) => col.key))
      parsed
        .filter((key) =>
          typeof key === 'string' &&
          validColumnKeys.has(key) &&
          !ALWAYS_VISIBLE_COLUMNS.has(key)
        )
        .forEach((key) => hiddenColumns.add(key))
      const storedVersion = Number(localStorage.getItem(COLUMN_SETTINGS_VERSION_KEY) ?? '1')
      if (storedVersion < COLUMN_SETTINGS_VERSION) {
        for (let v = storedVersion + 1; v <= COLUMN_SETTINGS_VERSION; v++) {
          for (const key of VERSION_NEW_HIDDEN_COLUMNS[v] ?? []) {
            if (validColumnKeys.has(key) && !ALWAYS_VISIBLE_COLUMNS.has(key)) {
              hiddenColumns.add(key)
            }
          }
        }
        saveColumnsToStorage()
      } else {
        localStorage.setItem(COLUMN_SETTINGS_VERSION_KEY, String(COLUMN_SETTINGS_VERSION))
      }
    } else {
      DEFAULT_HIDDEN_COLUMNS.forEach((key) => hiddenColumns.add(key))
      localStorage.setItem(COLUMN_SETTINGS_VERSION_KEY, String(COLUMN_SETTINGS_VERSION))
    }
  } catch (error) {
    console.error('Failed to load API key table columns:', error)
    DEFAULT_HIDDEN_COLUMNS.forEach((key) => hiddenColumns.add(key))
  }
}

const toggleColumn = (key: string) => {
  if (ALWAYS_VISIBLE_COLUMNS.has(key)) return
  if (hiddenColumns.has(key)) {
    hiddenColumns.delete(key)
  } else {
    hiddenColumns.add(key)
  }
  saveColumnsToStorage()
}

const isColumnVisible = (key: string) => !hiddenColumns.has(key)

const columns = computed<Column[]>(() =>
  allColumns.value.filter((col) => ALWAYS_VISIBLE_COLUMNS.has(col.key) || !hiddenColumns.has(col.key))
)

const apiKeys = ref<ApiKey[]>([])
// Selection is scoped to the rows on screen: it is pruned on every reload and
// every local removal, so a bulk delete can never touch a row the user cannot see.
const selectedKeyIds = ref<Array<string | number>>([])
const showBulkDeleteDialog = ref(false)
const keyRowId = (row: ApiKey) => row.id
const groups = ref<Group[]>([])
const loading = ref(false)
const submitting = ref(false)
const now = ref(new Date())
let resetTimer: ReturnType<typeof setInterval> | null = null
const usageStats = ref<Record<string, BatchApiKeyUsageStats>>({})
const userGroupRates = ref<Record<number, number>>({})

const pagination = ref({
  page: 1,
  page_size: getPersistedPageSize(),
  total: 0,
  pages: 0
})
const sortState = ref({
  sort_by: 'created_at',
  sort_order: 'desc' as 'asc' | 'desc'
})

// Filter state
const filterSearch = ref('')
const filterStatus = ref('')
const filterGroupId = ref<string | number>('')

const showCreateModal = ref(false)
const showEditModal = ref(false)
const showDeleteDialog = ref(false)
const showResetQuotaDialog = ref(false)
const showResetRateLimitDialog = ref(false)
const showUseKeyModal = ref(false)
const showCcsImportModal = ref(false)
const showColumnDropdown = ref(false)
const pendingCcsRow = ref<ApiKey | null>(null)
const ccsImportApp = ref<CcSwitchApp>('claude')
const ccsImportModel = ref('')
const ccsAvailableModels = ref<string[]>([])
const ccsModelsLoading = ref(false)
let ccsModelsAbort: AbortController | null = null

const ccsAppOptions = computed(() =>
  CC_SWITCH_APPS.map((value) => ({
    value,
    label: t(`keys.ccsImport.${CC_SWITCH_APP_LABEL_KEYS[value]}`)
  }))
)

const ccsImportDeeplinkUnsupported = computed(
  () => !ccSwitchProviderDeeplinkSupported(ccsImportApp.value)
)

const ccsModelOptions = computed(() => {
  const suggestions =
    ccsAvailableModels.value.length > 0
      ? ccsAvailableModels.value
      : CC_SWITCH_MODEL_SUGGESTIONS[ccsImportApp.value] || []
  const values = [...suggestions]
  const current = ccsImportModel.value.trim()
  if (current && !values.includes(current)) {
    values.unshift(current)
  }
  return values.map((value) => ({ value, label: value }))
})
const selectedKey = ref<ApiKey | null>(null)
const copiedKeyId = ref<number | null>(null)
const groupSelectorKeyId = ref<number | null>(null)
const publicSettings = ref<PublicSettings | null>(null)
const customEndpoints = ref<CustomEndpoint[]>([])
const dropdownRef = ref<HTMLElement | null>(null)
const columnDropdownRef = ref<HTMLElement | null>(null)
const dropdownPosition = ref<{ top?: number; bottom?: number; left: number } | null>(null)
const groupButtonRefs = ref<Map<number, HTMLElement>>(new Map())
let abortController: AbortController | null = null

// Get the currently selected key for group change
const selectedKeyForGroup = computed(() => {
  if (groupSelectorKeyId.value === null) return null
  return apiKeys.value.find((k) => k.id === groupSelectorKeyId.value) || null
})

const setGroupButtonRef = (keyId: number, el: Element | ComponentPublicInstance | null) => {
  if (el instanceof HTMLElement) {
    groupButtonRefs.value.set(keyId, el)
  } else {
    groupButtonRefs.value.delete(keyId)
  }
}

const formData = ref({
  name: '',
  group_id: null as number | null,
  group_ids: [] as number[],
  smart_routing: false,
  status: 'active' as 'active' | 'inactive',
  use_custom_key: false,
  custom_key: '',
  enable_ip_restriction: false,
  ip_whitelist: '',
  ip_blacklist: '',
  // Quota settings (empty = unlimited)
  enable_quota: false,
  quota: null as number | null,
  // Rate limit settings
  enable_rate_limit: false,
  rate_limit_5h: null as number | null,
  rate_limit_1d: null as number | null,
  rate_limit_7d: null as number | null,
  enable_expiration: false,
  expiration_preset: '30' as '7' | '30' | '90' | 'custom',
  expiration_date: '',
  // Anthropic cache_control TTL 覆盖：inherit/off/1h/5m
  anthropic_cache_ttl_mode: 'inherit' as 'inherit' | 'off' | '1h' | '5m',
  concurrency_limit: 0,
  deepseek_tool_injection_enabled: true,
})

// 自定义Key验证
const customKeyError = computed(() => {
  if (!formData.value.use_custom_key || !formData.value.custom_key) {
    return ''
  }
  const key = formData.value.custom_key
  if (key.length < 16) {
    return t('keys.customKeyTooShort')
  }
  // 检查字符：只允许字母、数字、下划线、连字符
  if (!/^[a-zA-Z0-9_-]+$/.test(key)) {
    return t('keys.customKeyInvalidChars')
  }
  return ''
})

const statusOptions = computed(() => [
  { value: 'active', label: t('common.active') },
  { value: 'inactive', label: t('common.inactive') }
])

const shouldSubmitEditStatus = (key: ApiKey, status: 'active' | 'inactive') => {
  if (key.status === 'quota_exhausted' || key.status === 'expired') {
    return status === 'active'
  }
  return true
}

// Filter dropdown options
const groupFilterOptions = computed(() => [
  { value: '', label: t('keys.allGroups') },
  { value: 0, label: t('keys.noGroup') },
  ...groups.value.map((g) => ({ value: g.id, label: g.name }))
])

const statusFilterOptions = computed(() => [
  { value: '', label: t('keys.allStatus') },
  { value: 'active', label: t('keys.status.active') },
  { value: 'inactive', label: t('keys.status.inactive') },
  { value: 'quota_exhausted', label: t('keys.status.quota_exhausted') },
  { value: 'expired', label: t('keys.status.expired') }
])

const onFilterChange = () => {
  pagination.value.page = 1
  loadApiKeys()
}

const onGroupFilterChange = (value: string | number | boolean | null) => {
  filterGroupId.value = value as string | number
  onFilterChange()
}

const onStatusFilterChange = (value: string | number | boolean | null) => {
  filterStatus.value = value as string
  onFilterChange()
}

// Convert groups to Select options format with rate multiplier and subscription type
const groupOptions = computed(() =>
  groups.value.map((group) => ({
    value: group.id,
    label: group.name,
    description: group.description,
    rate: group.rate_multiplier,
    userRate: userGroupRates.value[group.id] ?? null,
    peakRateEnabled: group.peak_rate_enabled,
    peakStart: group.peak_start,
    peakEnd: group.peak_end,
    peakRateMultiplier: group.peak_rate_multiplier,
    subscriptionType: group.subscription_type,
    platform: group.platform
  }))
)

const groupOptionById = (id: number) => groupOptions.value.find((opt) => opt.value === id)

// DeepSeek 工具注入只对 DeepSeek 平台的分组有意义：非该平台隐藏开关，
// 后端也只在 DeepSeek 账号上执行注入。
const selectedGroupIds = computed(() => {
  const ids = formData.value.smart_routing ? formData.value.group_ids : []
  if (ids.length > 0) return ids
  return formData.value.group_id ? [formData.value.group_id] : []
})
const showDeepSeekToolInjection = computed(() =>
  selectedGroupIds.value.some((id) => groupOptionById(id)?.platform === 'deepseek'),
)

type KeyProvider = 'anthropic' | 'openai' | 'cn' | 'other'

const keyProviders = computed<Array<{ value: KeyProvider; label: string; icons: GroupPlatform[] }>>(() => [
  { value: 'anthropic', label: 'Anthropic', icons: ['anthropic'] },
  { value: 'openai', label: 'OpenAI', icons: ['openai'] },
  { value: 'cn', label: t('keys.providerCN'), icons: ['deepseek', 'kimi'] },
  { value: 'other', label: t('keys.providerOther'), icons: ['gemini', 'grok'] },
])

const keyProviderForPlatform = (platform: GroupPlatform): KeyProvider => {
  switch (platform) {
    case 'anthropic':
    case 'openai':
      return platform
    case 'kimi':
    case 'zhipu':
    case 'deepseek':
    case 'minimax':
      return 'cn'
    default:
      return 'other'
  }
}

const selectedKeyProvider = ref<KeyProvider | null>(null)
const availableKeyProviders = computed(() => new Set(groups.value.map((group) => keyProviderForPlatform(group.platform))))
const activeKeyProvider = computed(() => {
  if (selectedKeyProvider.value) return selectedKeyProvider.value
  const group = groupOptions.value.find((option) => option.value === formData.value.group_id)
  const platform = group?.platform ?? selectedKey.value?.group?.platform
  if (platform) return keyProviderForPlatform(platform)
  return keyProviders.value.find((provider) => availableKeyProviders.value.has(provider.value))?.value ?? 'anthropic'
})
const providerGroupOptions = computed(() => groupOptions.value.filter((option) => keyProviderForPlatform(option.platform) === activeKeyProvider.value))

const selectKeyProvider = (provider: KeyProvider) => {
  selectedKeyProvider.value = provider
  if (!formData.value.smart_routing && !providerGroupOptions.value.some((option) => option.value === formData.value.group_id)) {
    formData.value.group_id = null
    formData.value.group_ids = []
  }
}

const isSmartRoutingKey = (key: ApiKey) => (key.group_ids?.length ?? 0) > 1

watch(
  () => formData.value.smart_routing,
  (enabled) => {
    if (enabled) {
      if (formData.value.group_ids.length === 0 && formData.value.group_id != null) {
        formData.value.group_ids = [formData.value.group_id]
      }
      return
    }
    formData.value.group_ids = formData.value.group_id != null ? [formData.value.group_id] : []
    selectedKeyProvider.value = null
  }
)

const smartRoutingAddOptions = computed(() => {
  const selected = new Set(formData.value.group_ids)
  return providerGroupOptions.value.filter((opt) => !selected.has(opt.value))
})

const addSmartRoute = (value: string | number | boolean | null) => {
  if (typeof value !== 'number') return
  if (formData.value.group_ids.includes(value)) return
  formData.value.group_ids = [...formData.value.group_ids, value]
  formData.value.group_id = formData.value.group_ids[0] ?? null
}

const removeSmartRoute = (index: number) => {
  formData.value.group_ids = formData.value.group_ids.filter((_, i) => i !== index)
  formData.value.group_id = formData.value.group_ids[0] ?? null
}

const moveSmartRoute = (index: number, delta: number) => {
  const next = index + delta
  if (next < 0 || next >= formData.value.group_ids.length) return
  const copy = [...formData.value.group_ids]
  const [item] = copy.splice(index, 1)
  copy.splice(next, 0, item)
  formData.value.group_ids = copy
  formData.value.group_id = copy[0] ?? null
}

// Group dropdown search
const groupSearchQuery = ref('')
const filteredGroupOptions = computed(() => {
  const query = groupSearchQuery.value.trim().toLowerCase()
  if (!query) return groupOptions.value
  return groupOptions.value.filter((opt) => {
    return opt.label.toLowerCase().includes(query) ||
      (opt.description && opt.description.toLowerCase().includes(query))
  })
})

const copyToClipboard = async (text: string, keyId: number) => {
  const success = await clipboardCopy(text, t('keys.copied'))
  if (success) {
    copiedKeyId.value = keyId
    setTimeout(() => {
      copiedKeyId.value = null
    }, 800)
  }
}

const isAbortError = (error: unknown) => {
  if (!error || typeof error !== 'object') return false
  const { name, code } = error as { name?: string; code?: string }
  return name === 'AbortError' || code === 'ERR_CANCELED'
}

const queueWaiting = ref<Record<number, number>>({})
const loadQueueSnapshot = async (ids: number[], signal: AbortSignal) => {
  queueWaiting.value = {}
  if (!ids.length || typeof keysAPI.getConcurrencyQueue !== 'function') return
  try {
    const snapshot = await keysAPI.getConcurrencyQueue(ids.slice(0, 100), signal)
    if (!signal.aborted) queueWaiting.value = Object.fromEntries(snapshot.items.map(item => [item.id, item.current_waiting]))
  } catch { /* A failed snapshot stays unknown; the key table remains usable. */ }
}
const loadApiKeys = async () => {
  abortController?.abort()
  const controller = new AbortController()
  abortController = controller
  const { signal } = controller
  loading.value = true
  try {
    // Build filters
    const filters: {
      search?: string
      status?: string
      group_id?: number | string
      sort_by?: string
      sort_order?: 'asc' | 'desc'
    } = {}
    if (filterSearch.value) filters.search = filterSearch.value
    if (filterStatus.value) filters.status = filterStatus.value
    if (filterGroupId.value !== '') filters.group_id = filterGroupId.value
    filters.sort_by = sortState.value.sort_by
    filters.sort_order = sortState.value.sort_order

    const response = await keysAPI.list(pagination.value.page, pagination.value.page_size, filters, {
      signal
    })
    if (signal.aborted) return
    apiKeys.value = response.items
    pruneSelection()
    void loadQueueSnapshot(response.items.filter(key => (key.concurrency_limit ?? 0) > 0).map(key => key.id), signal)
    pagination.value.total = response.total
    pagination.value.pages = response.pages
    loading.value = false

    // Usage numbers are secondary; don't keep the key table spinning while
    // 30-day aggregates scan usage_logs.
    if (response.items.length > 0) {
      const keyIds = response.items.map((k) => k.id)
      try {
        const usageResponse = await usageAPI.getDashboardApiKeysUsage(keyIds, { signal })
        if (signal.aborted) return
        usageStats.value = usageResponse.stats
      } catch (e) {
        if (!isAbortError(e)) {
          console.error('Failed to load usage stats:', e)
        }
      }
    }
  } catch (error) {
    if (isAbortError(error)) {
      return
    }
    appStore.showError(t('keys.failedToLoad'))
  } finally {
    if (abortController === controller) {
      loading.value = false
    }
  }
}

const loadGroups = async () => {
  try {
    groups.value = await userGroupsAPI.getAvailable()
  } catch (error) {
    console.error('Failed to load groups:', error)
  }
}

const loadUserGroupRates = async () => {
  try {
    userGroupRates.value = await userGroupsAPI.getUserGroupRates()
  } catch (error) {
    console.error('Failed to load user group rates:', error)
  }
}

const loadPublicSettings = async () => {
  try {
    publicSettings.value = await authAPI.getPublicSettings()
  } catch (error) {
    console.error('Failed to load public settings:', error)
  }
}

const loadCustomEndpoints = async () => {
  try {
    customEndpoints.value = await endpointsAPI.getCustomEndpoints()
  } catch (error) {
    // Ineligible users and transient auth failures should not expose or retain stale URLs.
    customEndpoints.value = []
    console.error('Failed to load custom endpoints:', error)
  }
}

const openUseKeyModal = (key: ApiKey) => {
  selectedKey.value = key
  showUseKeyModal.value = true
}

const closeUseKeyModal = () => {
  showUseKeyModal.value = false
  selectedKey.value = null
}

const handlePageChange = (page: number) => {
  pagination.value.page = page
  loadApiKeys()
}

const handlePageSizeChange = (pageSize: number) => {
  pagination.value.page_size = pageSize
  pagination.value.page = 1
  loadApiKeys()
}

const handleSort = (key: string, order: 'asc' | 'desc') => {
  sortState.value.sort_by = key
  sortState.value.sort_order = order
  pagination.value.page = 1
  loadApiKeys()
}

const editKey = (key: ApiKey) => {
  selectedKeyProvider.value = null
  selectedKey.value = key
  const hasIPRestriction = (key.ip_whitelist?.length > 0) || (key.ip_blacklist?.length > 0)
  const hasExpiration = !!key.expires_at
  formData.value = {
    name: key.name,
    group_id: key.group_id,
    group_ids: key.group_ids && key.group_ids.length > 0 ? [...key.group_ids] : (key.group_id ? [key.group_id] : []),
    smart_routing: !!(key.group_ids && key.group_ids.length > 1),
    status: key.status === 'quota_exhausted' || key.status === 'expired' ? 'inactive' : key.status,
    use_custom_key: false,
    custom_key: '',
    enable_ip_restriction: hasIPRestriction,
    ip_whitelist: (key.ip_whitelist || []).join('\n'),
    ip_blacklist: (key.ip_blacklist || []).join('\n'),
    enable_quota: key.quota > 0,
    quota: key.quota > 0 ? key.quota : null,
    enable_rate_limit: (key.rate_limit_5h > 0) || (key.rate_limit_1d > 0) || (key.rate_limit_7d > 0),
    rate_limit_5h: key.rate_limit_5h || null,
    rate_limit_1d: key.rate_limit_1d || null,
    rate_limit_7d: key.rate_limit_7d || null,
    enable_expiration: hasExpiration,
    expiration_preset: 'custom',
    expiration_date: key.expires_at ? formatDateTimeLocal(key.expires_at) : '',
    anthropic_cache_ttl_mode: (key.anthropic_cache_ttl_mode || 'inherit') as 'inherit' | 'off' | '1h' | '5m',
    concurrency_limit: key.concurrency_limit ?? 0,
    deepseek_tool_injection_enabled: key.deepseek_tool_injection_enabled !== false,
  }
  showEditModal.value = true
}

const toggleKeyStatus = async (key: ApiKey) => {
  const newStatus = key.status === 'active' ? 'inactive' : 'active'
  try {
    const updated = await keysAPI.toggleStatus(key.id, newStatus)
    const merged = mergeKeyLocally(updated)
    if (merged && !matchesActiveFilters(merged)) {
      // The row no longer belongs under the active status filter; drop it locally.
      const removed = removeKeysLocally([merged.id])
      applyTotalDelta(-removed.length)
      await backfillPageIfEmpty()
    }
    appStore.showSuccess(
      newStatus === 'active' ? t('keys.keyEnabledSuccess') : t('keys.keyDisabledSuccess')
    )
  } catch (error) {
    appStore.showError(t('keys.failedToUpdateStatus'))
  }
}

const openGroupSelector = (key: ApiKey) => {
  if (isSmartRoutingKey(key)) {
    appStore.showError(t('keys.smartRoutingChangeInEditor'))
    return
  }
  if (groupSelectorKeyId.value === key.id) {
    groupSelectorKeyId.value = null
    dropdownPosition.value = null
  } else {
    const buttonEl = groupButtonRefs.value.get(key.id)
    if (buttonEl) {
      const rect = buttonEl.getBoundingClientRect()
      const dropdownEstHeight = 400 // estimated max dropdown height
      const dropdownEstWidth = Math.min(380, window.innerWidth - 16)
      const spaceBelow = window.innerHeight - rect.bottom
      const spaceAbove = rect.top
      // 夹取 left，避免窄屏下浮层超出视口右缘
      const left = Math.max(8, Math.min(rect.left, window.innerWidth - dropdownEstWidth - 8))

      if (spaceBelow < dropdownEstHeight && spaceAbove > spaceBelow) {
        // Not enough space below, pop upward
        dropdownPosition.value = {
          bottom: window.innerHeight - rect.top + 4,
          left
        }
      } else {
        // Default: pop downward
        dropdownPosition.value = {
          top: rect.bottom + 4,
          left
        }
      }
    }
    groupSelectorKeyId.value = key.id
    groupSearchQuery.value = ''
  }
}

const changeGroup = async (key: ApiKey, newGroupId: number | null) => {
  groupSelectorKeyId.value = null
  dropdownPosition.value = null
  if (isSmartRoutingKey(key)) {
    appStore.showError(t('keys.smartRoutingChangeInEditor'))
    return
  }
  if (key.group_id === newGroupId) return

  try {
    key.group_id = newGroupId
    const updated = await keysAPI.update(key.id, { group_id: newGroupId })
    // Merge the response so the row and its group badge update in place.
    mergeKeyLocally(updated)
    appStore.showSuccess(t('keys.groupChangedSuccess'))
  } catch (error) {
    appStore.showError(t('keys.failedToChangeGroup'))
  }
}

const closeGroupSelector = (event: MouseEvent) => {
  const target = event.target as HTMLElement
  // Check if click is inside the dropdown or the trigger button
  if (!target.closest('.group\\/dropdown') && !dropdownRef.value?.contains(target)) {
    groupSelectorKeyId.value = null
    dropdownPosition.value = null
  }
  if (columnDropdownRef.value && !columnDropdownRef.value.contains(target)) {
    showColumnDropdown.value = false
  }
}

const confirmDelete = (key: ApiKey) => {
  selectedKey.value = key
  showDeleteDialog.value = true
}

const selectedKeyCount = computed(() => selectedKeyIds.value.length)

const bulkDeleteMessage = computed(
  () => `${t('keys.deleteKey')} ${t('common.selectedCount', { count: selectedKeyCount.value })}`
)

const pruneSelection = () => {
  if (selectedKeyIds.value.length === 0) return
  const visible = new Set(apiKeys.value.map((key) => key.id))
  selectedKeyIds.value = selectedKeyIds.value.filter((id) => visible.has(Number(id)))
}

/** Adjust the server-side total locally instead of refetching the page. */
const applyTotalDelta = (delta: number) => {
  if (delta === 0) return
  const total = Math.max(0, pagination.value.total + delta)
  pagination.value.total = total
  pagination.value.pages =
    pagination.value.page_size > 0 ? Math.ceil(total / pagination.value.page_size) : 0
  if (total === 0) pagination.value.page = 1
}

type RemovedKeySnapshot = { index: number; key: ApiKey; usage?: BatchApiKeyUsageStats }

const removeKeysLocally = (ids: number[]): RemovedKeySnapshot[] => {
  const idSet = new Set(ids)
  const removed: RemovedKeySnapshot[] = []
  apiKeys.value.forEach((key, index) => {
    if (idSet.has(key.id)) removed.push({ index, key, usage: usageStats.value[key.id] })
  })
  if (removed.length === 0) return removed
  apiKeys.value = apiKeys.value.filter((key) => !idSet.has(key.id))
  removed.forEach((snapshot) => {
    delete usageStats.value[snapshot.key.id]
  })
  selectedKeyIds.value = selectedKeyIds.value.filter((id) => !idSet.has(Number(id)))
  if (selectedKey.value && idSet.has(selectedKey.value.id)) selectedKey.value = null
  return removed
}

const restoreRemovedKeys = (removed: RemovedKeySnapshot[]) => {
  if (removed.length === 0) return
  const next = [...apiKeys.value]
  for (const snapshot of [...removed].sort((a, b) => a.index - b.index)) {
    next.splice(Math.min(snapshot.index, next.length), 0, snapshot.key)
    if (snapshot.usage) usageStats.value[snapshot.key.id] = snapshot.usage
  }
  apiKeys.value = next
}

/**
 * Rows were already removed locally. When the page emptied out but older pages
 * still hold keys, walk back one page instead of reloading everything.
 */
const backfillPageIfEmpty = async () => {
  if (apiKeys.value.length > 0) return
  if (pagination.value.total <= 0) return
  if (pagination.value.page <= 1) return
  pagination.value.page -= 1
  await loadApiKeys()
}

const matchesActiveFilters = (key: ApiKey) => {
  if (filterStatus.value && key.status !== filterStatus.value) return false
  if (filterGroupId.value !== '' && String(key.group_id ?? '') !== String(filterGroupId.value)) {
    return false
  }
  const query = filterSearch.value.trim().toLowerCase()
  if (query) {
    const haystack = `${key.name ?? ''} ${key.key ?? ''}`.toLowerCase()
    if (!haystack.includes(query)) return false
  }
  return true
}

const compareRowsForCurrentSort = (left: ApiKey, right: ApiKey): number => {
  const field = sortState.value.sort_by
  const direction = sortState.value.sort_order === 'asc' ? 1 : -1
  const leftValue = (left as unknown as Record<string, unknown>)[field]
  const rightValue = (right as unknown as Record<string, unknown>)[field]
  if (leftValue == null && rightValue == null) return 0
  if (leftValue == null) return 1
  if (rightValue == null) return -1
  if (typeof leftValue === 'number' && typeof rightValue === 'number') {
    return (leftValue - rightValue) * direction
  }
  return String(leftValue).localeCompare(String(rightValue)) * direction
}

/** Patch one row in place from a server response; never reloads the list. */
const mergeKeyLocally = (updated: ApiKey | null | undefined): ApiKey | null => {
  if (!updated || typeof updated !== 'object' || typeof updated.id !== 'number') return null
  const index = apiKeys.value.findIndex((key) => key.id === updated.id)
  if (index === -1) return null
  const merged: ApiKey = { ...apiKeys.value[index] }
  const patch = updated as unknown as Record<string, unknown>
  const target = merged as unknown as Record<string, unknown>
  for (const [field, value] of Object.entries(patch)) {
    // `group` comes back empty on update responses; keep the badge we already have.
    if (value === undefined) continue
    if (value === null && (field === 'group' || field === 'user')) continue
    target[field] = value
  }
  if (merged.group_id == null) {
    merged.group = undefined
  } else {
    const localGroup = groups.value.find((group) => group.id === merged.group_id)
    if (localGroup) merged.group = localGroup
  }
  apiKeys.value[index] = merged
  if (selectedKey.value?.id === merged.id) selectedKey.value = merged
  return merged
}

/** Insert a freshly created key without refetching the page. */
const insertKeyLocally = (created: ApiKey | null | undefined) => {
  if (!created || typeof created !== 'object' || typeof created.id !== 'number') return false
  applyTotalDelta(1)
  if (pagination.value.page !== 1 || !matchesActiveFilters(created)) return false
  const index = apiKeys.value.findIndex((key) => compareRowsForCurrentSort(key, created) > 0)
  const next = [...apiKeys.value]
  next.splice(index === -1 ? next.length : index, 0, created)
  apiKeys.value = next
  return true
}

const handleSubmit = async () => {
  // Validate group_id is required
  if (formData.value.smart_routing) {
    if (formData.value.group_ids.length === 0) {
      appStore.showError(t('keys.groupRequired'))
      return
    }
    formData.value.group_id = formData.value.group_ids[0] ?? null
  } else if (formData.value.group_id === null) {
    appStore.showError(t('keys.groupRequired'))
    return
  }

  // Validate custom key if enabled
  if (!showEditModal.value && formData.value.use_custom_key) {
    if (!formData.value.custom_key) {
      appStore.showError(t('keys.customKeyRequired'))
      return
    }
    if (customKeyError.value) {
      appStore.showError(customKeyError.value)
      return
    }
  }

  // Parse IP lists only if IP restriction is enabled
  const parseIPList = (text: string): string[] =>
    text.split('\n').map(ip => ip.trim()).filter(ip => ip.length > 0)
  const ipWhitelist = formData.value.enable_ip_restriction ? parseIPList(formData.value.ip_whitelist) : []
  const ipBlacklist = formData.value.enable_ip_restriction ? parseIPList(formData.value.ip_blacklist) : []

  // Calculate quota value (null/empty/0 = unlimited, stored as 0)
  const quota = formData.value.quota && formData.value.quota > 0 ? formData.value.quota : 0

  // Calculate expiration
  let expiresInDays: number | undefined
  let expiresAt: string | null | undefined
  if (formData.value.enable_expiration && formData.value.expiration_date) {
    if (!showEditModal.value) {
      // Create mode: calculate days from date
      const expDate = new Date(formData.value.expiration_date)
      const now = new Date()
      const diffDays = Math.ceil((expDate.getTime() - now.getTime()) / (1000 * 60 * 60 * 24))
      expiresInDays = diffDays > 0 ? diffDays : 1
    } else {
      // Edit mode: use custom date directly
      expiresAt = new Date(formData.value.expiration_date).toISOString()
    }
  } else if (showEditModal.value) {
    // Edit mode: if expiration disabled or date cleared, send empty string to clear
    expiresAt = ''
  }

  // Calculate rate limit values (send 0 when toggle is off)
  const rateLimitData = formData.value.enable_rate_limit ? {
    rate_limit_5h: formData.value.rate_limit_5h && formData.value.rate_limit_5h > 0 ? formData.value.rate_limit_5h : 0,
    rate_limit_1d: formData.value.rate_limit_1d && formData.value.rate_limit_1d > 0 ? formData.value.rate_limit_1d : 0,
    rate_limit_7d: formData.value.rate_limit_7d && formData.value.rate_limit_7d > 0 ? formData.value.rate_limit_7d : 0,
  } : { rate_limit_5h: 0, rate_limit_1d: 0, rate_limit_7d: 0 }

  submitting.value = true
  try {
    if (showEditModal.value && selectedKey.value) {
      const updates: UpdateApiKeyRequest = {
        name: formData.value.name,
        group_id: formData.value.group_id,
        group_ids: formData.value.smart_routing ? formData.value.group_ids : (formData.value.group_id != null ? [formData.value.group_id] : []),
        ip_whitelist: ipWhitelist,
        ip_blacklist: ipBlacklist,
        quota: quota,
        expires_at: expiresAt,
        rate_limit_5h: rateLimitData.rate_limit_5h,
        rate_limit_1d: rateLimitData.rate_limit_1d,
        rate_limit_7d: rateLimitData.rate_limit_7d,
        anthropic_cache_ttl_mode: formData.value.anthropic_cache_ttl_mode,
        concurrency_limit: formData.value.concurrency_limit,
        deepseek_tool_injection_enabled: formData.value.deepseek_tool_injection_enabled,
      }
      if (shouldSubmitEditStatus(selectedKey.value, formData.value.status)) {
        updates.status = formData.value.status
      }
      const updated = await keysAPI.update(selectedKey.value.id, updates)
      mergeKeyLocally(updated)
      appStore.showSuccess(t('keys.keyUpdatedSuccess'))
    } else {
      const customKey = formData.value.use_custom_key ? formData.value.custom_key : undefined
      const created = await keysAPI.create(
        formData.value.name,
        formData.value.group_id,
        customKey,
        ipWhitelist,
        ipBlacklist,
        quota,
        expiresInDays,
        rateLimitData,
        formData.value.smart_routing ? formData.value.group_ids : undefined,
        formData.value.anthropic_cache_ttl_mode,
        { deepseekToolInjectionEnabled: formData.value.deepseek_tool_injection_enabled, ...(formData.value.concurrency_limit > 0 ? { concurrencyLimit: formData.value.concurrency_limit } : {}) }
      )
      insertKeyLocally(created)
      appStore.showSuccess(t('keys.keyCreatedSuccess'))
      // Only advance tour if active, on submit step, and creation succeeded
      if (onboardingStore.isCurrentStep('[data-tour="key-form-submit"]')) {
        onboardingStore.nextStep(500)
      }
    }
    closeModals()
  } catch (error: any) {
    const errorMsg = error.response?.data?.detail || t('keys.failedToSave')
    appStore.showError(errorMsg)
    // Don't advance tour on error
  } finally {
    submitting.value = false
  }
}

/**
 * 处理删除 API Key 的操作
 * 优化：错误处理改进，优先显示后端返回的具体错误消息（如权限不足等），
 * 若后端未返回消息则显示默认的国际化文本
 */
const handleDelete = async () => {
  const key = selectedKey.value
  if (!key) return

  // Optimistic: drop the row immediately and keep a snapshot for rollback.
  showDeleteDialog.value = false
  selectedKey.value = null
  const removed = removeKeysLocally([key.id])
  applyTotalDelta(-removed.length)

  try {
    await keysAPI.delete(key.id)
    appStore.showSuccess(t('keys.keyDeletedSuccess'))
    await backfillPageIfEmpty()
  } catch (error: any) {
    // 优先使用后端返回的错误消息，提供更具体的错误信息给用户
    restoreRemovedKeys(removed)
    applyTotalDelta(removed.length)
    const errorMsg = error?.response?.data?.detail || error?.message || t('keys.failedToDelete')
    appStore.showError(errorMsg)
  }
}

const openBulkDelete = () => {
  if (selectedKeyCount.value === 0) return
  showBulkDeleteDialog.value = true
}

const handleBulkDelete = async () => {
  const ids = selectedKeyIds.value.map((id) => Number(id)).filter((id) => Number.isFinite(id))
  showBulkDeleteDialog.value = false
  if (ids.length === 0) return

  const removed = removeKeysLocally(ids)
  applyTotalDelta(-removed.length)

  try {
    const result = await keysAPI.deleteMany(ids)
    if (result.failed.length === 0) {
      appStore.showSuccess(t('common.deleted'))
    } else {
      // Only the failures go back into the table.
      const failedIds = new Set(result.failed.map((id) => Number(id)))
      const failedRows = removed.filter((snapshot) => failedIds.has(snapshot.key.id))
      restoreRemovedKeys(failedRows)
      applyTotalDelta(failedRows.length)
      appStore.showError(t('keys.failedToDelete'))
    }
    await backfillPageIfEmpty()
  } catch (error) {
    restoreRemovedKeys(removed)
    applyTotalDelta(removed.length)
    appStore.showError(t('keys.failedToDelete'))
  }
}

const closeModals = () => {
  selectedKeyProvider.value = null
  showCreateModal.value = false
  showEditModal.value = false
  selectedKey.value = null
  formData.value = {
    name: '',
    group_id: null,
    group_ids: [],
    smart_routing: false,
    status: 'active',
    use_custom_key: false,
    custom_key: '',
    enable_ip_restriction: false,
    ip_whitelist: '',
    ip_blacklist: '',
    enable_quota: false,
    quota: null,
    enable_rate_limit: false,
    rate_limit_5h: null,
    rate_limit_1d: null,
    rate_limit_7d: null,
    enable_expiration: false,
    expiration_preset: '30',
    expiration_date: '',
    anthropic_cache_ttl_mode: 'inherit',
    concurrency_limit: 0,
  deepseek_tool_injection_enabled: true,
  }
}

// Show reset quota confirmation dialog
const confirmResetQuota = () => {
  showResetQuotaDialog.value = true
}

// Set expiration date based on quick select days
const setExpirationDays = (days: number) => {
  formData.value.expiration_preset = days.toString() as '7' | '30' | '90'
  const expDate = new Date()
  expDate.setDate(expDate.getDate() + days)
  formData.value.expiration_date = formatDateTimeLocal(expDate.toISOString())
}

// Reset quota used for an API key
const resetQuotaUsed = async () => {
  const key = selectedKey.value
  if (!key) return
  showResetQuotaDialog.value = false
  try {
    const updatedKey = await keysAPI.update(key.id, { reset_quota: true })
    appStore.showSuccess(t('keys.quotaResetSuccess'))
    key.quota_used = updatedKey.quota_used
    if (key.status !== updatedKey.status) {
      key.status = updatedKey.status
      if (selectedKey.value?.id === key.id) {
        formData.value.status = updatedKey.status === 'active' ? 'active' : 'inactive'
      }
    }
  } catch (error: any) {
    const errorMsg = error.response?.data?.detail || t('keys.failedToResetQuota')
    appStore.showError(errorMsg)
  }
}

// Show reset rate limit confirmation dialog (from edit modal)
const confirmResetRateLimit = () => {
  showResetRateLimitDialog.value = true
}

// Show reset rate limit confirmation dialog (from table row)
const confirmResetRateLimitFromTable = (row: ApiKey) => {
  selectedKey.value = row
  showResetRateLimitDialog.value = true
}

// Reset rate limit usage for an API key
const resetRateLimitUsage = async () => {
  if (!selectedKey.value) return
  showResetRateLimitDialog.value = false
  try {
    const target = selectedKey.value
    const updated = await keysAPI.update(target.id, { reset_rate_limit_usage: true })
    appStore.showSuccess(t('keys.rateLimitResetSuccess'))
    // Patch only this key instead of reloading the whole page.
    if (mergeKeyLocally(updated)) return
    const refreshedKey = apiKeys.value.find(k => k.id === target.id)
    if (refreshedKey) {
      selectedKey.value = refreshedKey
    }
  } catch (error: any) {
    const errorMsg = error.response?.data?.detail || t('keys.failedToResetRateLimit')
    appStore.showError(errorMsg)
  }
}

const applyCcsModelDefault = () => {
  ccsImportModel.value = pickCcSwitchModel(
    defaultCcSwitchModel(ccsImportApp.value),
    ccsAvailableModels.value
  )
}

const loadCcsModels = async (row: ApiKey) => {
  ccsModelsAbort?.abort()
  const abort = new AbortController()
  ccsModelsAbort = abort
  ccsAvailableModels.value = []
  ccsModelsLoading.value = true
  try {
    const baseUrl = publicSettings.value?.api_base_url || window.location.origin
    const ids = await fetchCcSwitchModels(baseUrl, row.key, abort.signal)
    if (abort.signal.aborted) return
    ccsAvailableModels.value = ids
    applyCcsModelDefault()
  } catch {
    if (abort.signal.aborted) return
    applyCcsModelDefault()
  } finally {
    if (ccsModelsAbort === abort) {
      ccsModelsLoading.value = false
    }
  }
}

const importToCcswitch = (row: ApiKey) => {
  const platform = row.group?.platform || 'anthropic'
  const baseUrl = publicSettings.value?.api_base_url || window.location.origin
  const config = resolveCcSwitchImportConfig(
    platform,
    platform === 'gemini' ? 'gemini' : 'claude',
    baseUrl
  )
  pendingCcsRow.value = row
  ccsImportApp.value = config.app
  ccsImportModel.value = config.model || defaultCcSwitchModel(config.app)
  showCcsImportModal.value = true
  void loadCcsModels(row)
}

const onCcsAppChange = (value: string | number | boolean | null) => {
  const app = String(value)
  if (!isCcSwitchApp(app)) return
  ccsImportApp.value = app
  applyCcsModelDefault()
}

const onCcsModelChange = (value: string | number | boolean | null) => {
  ccsImportModel.value = value == null ? '' : String(value)
}

const executeCcsImport = (row: ApiKey, app: CcSwitchApp, model: string) => {
  const baseUrl = publicSettings.value?.api_base_url || window.location.origin
  const platform = row.group?.platform || 'anthropic'

  const usageScript = `({
    request: {
      url: "{{baseUrl}}/v1/usage",
      method: "GET",
      headers: { "Authorization": "Bearer {{apiKey}}" }
    },
    extractor: function(response) {
      const remaining = response?.remaining ?? response?.quota?.remaining ?? response?.balance;
      const unit = response?.unit ?? response?.quota?.unit ?? "USD";
      return {
        isValid: response?.is_active ?? response?.isValid ?? true,
        remaining,
        unit
      };
    }
  })`
  const providerName = (publicSettings.value?.site_name || 'sub2api').trim() || 'sub2api'
  const deeplink = buildCcSwitchImportDeeplink({
    baseUrl,
    platform,
    providerName,
    apiKey: row.key,
    usageScript,
    app,
    model
  })

  try {
    window.open(deeplink, '_self')

    // Check if the protocol handler worked by detecting if we're still focused
    setTimeout(() => {
      if (document.hasFocus()) {
        // Still focused means the protocol handler likely failed
        appStore.showError(t('keys.ccSwitchNotInstalled'))
      }
    }, 100)
  } catch (error) {
    appStore.showError(t('keys.ccSwitchNotInstalled'))
  }
}

const confirmCcsImport = () => {
  if (!pendingCcsRow.value) return
  if (!ccSwitchProviderDeeplinkSupported(ccsImportApp.value)) return
  executeCcsImport(pendingCcsRow.value, ccsImportApp.value, ccsImportModel.value.trim())
  closeCcsImportModal()
}

const closeCcsImportModal = () => {
  ccsModelsAbort?.abort()
  ccsModelsAbort = null
  ccsModelsLoading.value = false
  ccsAvailableModels.value = []
  showCcsImportModal.value = false
  pendingCcsRow.value = null
}

function formatResetTime(resetAt: string | null): string {
  if (!resetAt) return ''
  const diff = new Date(resetAt).getTime() - now.value.getTime()
  if (diff <= 0) return t('keys.resetNow')
  const days = Math.floor(diff / 86400000)
  const hours = Math.floor((diff % 86400000) / 3600000)
  const mins = Math.floor((diff % 3600000) / 60000)
  if (days > 0) return `${days}d ${hours}h`
  if (hours > 0) return `${hours}h ${mins}m`
  return `${mins}m`
}

onMounted(() => {
  loadSavedColumns()
  loadApiKeys()
  loadGroups()
  loadUserGroupRates()
  loadPublicSettings()
  loadCustomEndpoints()
  document.addEventListener('click', closeGroupSelector)
  resetTimer = setInterval(() => { now.value = new Date() }, 60000)
})

onUnmounted(() => {
  document.removeEventListener('click', closeGroupSelector)
  if (resetTimer) clearInterval(resetTimer)
  ccsModelsAbort?.abort()
})
</script>

<style scoped>
/* Row actions: quiet ghost buttons, icon over a short label. */
.key-row-action { display: inline-flex; flex-direction: column; align-items: center; gap: 2px; padding: 5px 6px; border-radius: 6px; color: var(--ui-text-muted); font-size: 11px; line-height: 14px; white-space: nowrap; transition: background-color 150ms ease, color 150ms ease; }
.key-row-action:hover { background: var(--ui-bg-elevated); color: var(--ui-text-highlighted); }
.key-row-action:focus-visible { outline: 2px solid var(--ui-border-inverted); outline-offset: 1px; }
.key-row-action--danger:hover { background: color-mix(in srgb, var(--ui-error) 10%, transparent); color: var(--ui-error); }
.key-route-action { display: inline-flex; padding: 4px; border-radius: 6px; color: var(--ui-text-dimmed); transition: background-color 150ms ease, color 150ms ease; }
.key-route-action:hover { background: var(--ui-bg-elevated); color: var(--ui-text-highlighted); }
.key-route-action--danger:hover { color: var(--ui-error); }
@media (prefers-reduced-motion: reduce) { .key-row-action, .key-route-action { transition: none; } }
</style>
