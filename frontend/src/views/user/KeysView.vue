<template>
  <AppLayout>
    <TablePageLayout>
      <template #actions>
        <UserPageHeader flush :title="t('keys.title')">
          <template #badge>
            <UBadge color="neutral" variant="subtle" class="tabular-nums" data-test="key-count">{{ pagination.total }}</UBadge>
          </template>
        </UserPageHeader>
      </template>

      <template #filters>
        <!-- Filter row in the console toolbar under the navbar (in place outside the frame). -->
        <PageToolbar>
          <template #left>
            <SearchInput
              v-model="filterSearch"
              :placeholder="t('keys.searchPlaceholder')"
              class="w-full sm:max-w-sm"
              @search="onFilterChange"
            />
          </template>
          <template #right>
            <Select
              :model-value="filterGroupId"
              class="w-full sm:w-40"
              :options="groupFilterOptions"
              @update:model-value="onGroupFilterChange"
            />
            <Select
              :model-value="filterStatus"
              class="w-full sm:w-36"
              :options="statusFilterOptions"
              @update:model-value="onStatusFilterChange"
            />
            <UDropdownMenu :items="columnMenuItems" :content="{ align: 'end' }" :ui="{ content: 'max-h-80' }">
              <UButton
                color="neutral"
                variant="outline"
                trailing-icon="i-lucide-settings-2"
                :label="t('keys.displayColumns')"
                :title="t('keys.columnSettings')"
                :aria-label="t('keys.columnSettings')"
              />
            </UDropdownMenu>
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
            <UButton icon="i-lucide-plus" data-tour="keys-create-btn" @click="showCreateModal = true">
              {{ t('keys.createKey') }}
            </UButton>
          </template>
        </PageToolbar>
        <EndpointPopover
          v-if="publicSettings?.api_base_url || customEndpoints.length > 0"
          :api-base-url="publicSettings?.api_base_url || ''"
          :custom-endpoints="customEndpoints"
        />
      </template>

      <template #table>
        <DataTable
          :columns="columns"
          :data="apiKeys"
          :loading="loading"
          :server-side-sort="true"
          default-sort-key="created_at"
          default-sort-order="desc"
          @sort="handleSort"
        >
          <template #cell-id="{ value }">
            <span class="font-mono text-xs text-muted">#{{ value }}</span>
          </template>

          <template #cell-key="{ value, row }">
            <div class="flex items-center gap-1">
              <code class="font-mono text-xs text-muted">{{ maskApiKey(value) }}</code>
              <UButton
                color="neutral"
                variant="ghost"
                size="xs"
                :icon="copiedKeyId === row.id ? 'i-lucide-check' : 'i-lucide-copy'"
                :class="copiedKeyId === row.id ? 'text-success' : ''"
                :title="copiedKeyId === row.id ? t('keys.copied') : t('keys.copyToClipboard')"
                :aria-label="copiedKeyId === row.id ? t('keys.copied') : t('keys.copyToClipboard')"
                @click="copyToClipboard(value, row.id)"
              />
            </div>
          </template>

          <template #cell-name="{ value, row }">
            <div class="flex items-center gap-1.5">
              <span class="font-medium text-highlighted">{{ value }}</span>
              <UIcon
                v-if="row.ip_whitelist?.length > 0 || row.ip_blacklist?.length > 0"
                name="i-lucide-shield"
                class="size-4 text-info"
                :title="t('keys.ipRestrictionEnabled')"
              />
            </div>
          </template>

          <template #cell-group="{ row }">
            <UPopover
              :open="groupSelectorKeyId === row.id"
              :content="{ align: 'start', collisionPadding: 8 }"
              @update:open="(open: boolean) => setGroupSelectorOpen(row, open)"
            >
              <UButton
                color="neutral"
                variant="ghost"
                trailing-icon="i-lucide-chevrons-up-down"
                class="-mx-2.5 max-w-full"
                :ui="{ trailingIcon: 'size-3.5 text-dimmed' }"
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
                <span v-else class="text-sm text-dimmed">{{ t('keys.noGroup') }}</span>
                <UBadge v-if="isSmartRoutingKey(row)" color="neutral" variant="subtle" size="sm">
                  {{ t('keys.smartRouting') }}
                  {{ t('keys.smartRoutingMore', { count: (row.group_ids?.length || 1) - 1 }) }}
                </UBadge>
              </UButton>
              <template #content>
                <div class="w-[min(380px,calc(100vw-16px))]">
                  <div class="border-b border-default p-2">
                    <UInput
                      v-model="groupSearchQuery"
                      icon="i-lucide-search"
                      variant="none"
                      class="w-full"
                      :placeholder="t('keys.searchGroup')"
                    />
                  </div>
                  <div class="max-h-80 overflow-y-auto p-1">
                    <UButton
                      v-for="option in filteredGroupOptions"
                      :key="option.value ?? 'null'"
                      color="neutral"
                      variant="ghost"
                      block
                      class="justify-start"
                      :class="row.group_id === option.value || (!row.group_id && option.value === null) ? 'bg-elevated' : ''"
                      :title="option.description || undefined"
                      @click="changeGroup(row, option.value)"
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
                        :selected="row.group_id === option.value || (!row.group_id && option.value === null)"
                      />
                    </UButton>
                    <p v-if="filteredGroupOptions.length === 0" class="py-4 text-center text-sm text-muted">
                      {{ t('keys.noGroupFound') }}
                    </p>
                  </div>
                </div>
              </template>
            </UPopover>
          </template>

          <template #cell-current_concurrency="{ value, row }">
            <UBadge :color="(value ?? 0) > 0 ? 'success' : 'neutral'" variant="subtle" class="tabular-nums">
              {{ value ?? 0 }}<span v-if="row.concurrency_limit" class="ml-1 opacity-70">/ {{ row.concurrency_limit }}</span>
            </UBadge>
            <span v-if="row.concurrency_limit" class="mt-1 block text-xs text-muted">{{ t('keys.queueWaiting') }}: {{ queueWaiting[row.id] ?? '—' }}</span>
          </template>

          <template #cell-usage="{ row }">
            <div class="min-w-36 space-y-0.5 text-sm">
              <div class="flex items-center gap-1.5">
                <span class="text-muted">{{ t('keys.today') }}:</span>
                <span class="font-medium tabular-nums text-highlighted">${{ (usageStats[row.id]?.today_actual_cost ?? 0).toFixed(4) }}</span>
              </div>
              <div class="flex items-center gap-1.5">
                <span class="text-muted">{{ t('keys.total') }}:</span>
                <span class="font-medium tabular-nums text-highlighted">${{ (usageStats[row.id]?.total_actual_cost ?? 0).toFixed(4) }}</span>
              </div>
              <div v-if="row.quota > 0" class="pt-1">
                <div class="flex items-center gap-1.5">
                  <span class="text-muted">{{ t('keys.quota') }}:</span>
                  <span class="font-medium tabular-nums" :class="limitTextClass(row.quota_used, row.quota)">
                    ${{ row.quota_used?.toFixed(2) || '0.00' }} / ${{ row.quota?.toFixed(2) }}
                  </span>
                </div>
                <UProgress :model-value="limitPercent(row.quota_used, row.quota)" :color="limitColor(row.quota_used, row.quota)" size="xs" class="mt-1" />
              </div>
            </div>
          </template>

          <template #cell-rate_limit="{ row }">
            <div v-if="rateWindows(row).length" class="min-w-[140px] space-y-1.5">
              <div v-for="win in rateWindows(row)" :key="win.label">
                <div class="flex items-center justify-between text-xs">
                  <span class="text-muted">{{ win.label }}</span>
                  <span class="font-medium tabular-nums" :class="limitTextClass(win.used, win.limit)">
                    ${{ win.used?.toFixed(2) || '0.00' }}/${{ win.limit?.toFixed(2) }}
                  </span>
                </div>
                <UProgress :model-value="limitPercent(win.used, win.limit)" :color="limitColor(win.used, win.limit)" size="2xs" />
                <div v-if="win.resetAt && formatResetTime(win.resetAt)" class="text-[10px] tabular-nums text-dimmed">
                  ⟳ {{ formatResetTime(win.resetAt) }}
                </div>
              </div>
              <UButton
                v-if="row.usage_5h > 0 || row.usage_1d > 0 || row.usage_7d > 0"
                color="neutral"
                variant="ghost"
                size="xs"
                icon="i-lucide-rotate-ccw"
                class="-ml-1.5"
                :title="t('keys.resetRateLimitUsage')"
                @click.stop="confirmResetRateLimitFromTable(row)"
              >
                {{ t('keys.resetUsage') }}
              </UButton>
            </div>
            <span v-else class="text-sm text-dimmed">-</span>
          </template>

          <template #cell-expires_at="{ value }">
            <span v-if="value" class="text-sm" :class="new Date(value) < new Date() ? 'text-error' : 'text-muted'">
              {{ formatDateTime(value) }}
            </span>
            <span v-else class="text-sm text-dimmed">{{ t('keys.noExpiration') }}</span>
          </template>

          <template #cell-status="{ value }">
            <UBadge :color="statusColor(value)" variant="subtle">{{ t('keys.status.' + value) }}</UBadge>
          </template>

          <template #cell-last_used_at="{ value }">
            <span v-if="value" class="text-sm text-muted">{{ formatDateTime(value) }}</span>
            <span v-else class="text-sm text-dimmed">-</span>
          </template>

          <template #cell-last_used_ip="{ value }">
            <span v-if="value" class="text-sm text-muted">{{ value }}</span>
            <span v-else class="text-sm text-dimmed">-</span>
          </template>

          <template #cell-created_at="{ value }">
            <span class="text-sm text-muted">{{ formatDateTime(value) }}</span>
          </template>

          <template #cell-actions="{ row }">
            <div class="flex items-center justify-end gap-0.5">
              <UButton color="neutral" variant="ghost" size="sm" icon="i-lucide-square-terminal" @click="openUseKeyModal(row)">
                {{ t('keys.useKey') }}
              </UButton>
              <UButton
                color="neutral"
                variant="ghost"
                size="sm"
                icon="i-lucide-pencil"
                :title="t('common.edit')"
                :aria-label="t('common.edit')"
                @click="editKey(row)"
              />
              <UDropdownMenu :items="rowMenuItems(row)" :content="{ align: 'end' }">
                <UButton
                  color="neutral"
                  variant="ghost"
                  size="sm"
                  icon="i-lucide-ellipsis-vertical"
                  :title="t('keys.moreActions')"
                  :aria-label="t('keys.moreActions')"
                />
              </UDropdownMenu>
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
      <form id="key-form" @submit.prevent="handleSubmit" class="space-y-4">
        <UFormField :label="t('keys.nameLabel')" required>
          <UInput
            v-model="formData.name"
            type="text"
            required
            class="w-full"
            :placeholder="t('keys.namePlaceholder')"
            data-tour="key-form-name"
          />
        </UFormField>

        <URadioGroup
          :model-value="activeKeyProvider"
          name="key-provider"
          variant="card"
          orientation="horizontal"
          indicator="hidden"
          :legend="t('keys.providerLabel')"
          :items="keyProviderItems"
          :ui="{ fieldset: 'grid grid-cols-2 gap-2 sm:grid-cols-4', legend: 'mb-1.5 text-sm font-medium text-default', item: 'justify-center p-3', wrapper: 'flex flex-col items-center gap-2 text-center' }"
          @update:model-value="(value) => selectKeyProvider(value as KeyProvider)"
        >
          <template #label="{ item }">
            <span class="flex h-8 items-center justify-center gap-1" aria-hidden="true">
              <span
                v-for="platform in (item as unknown as KeyProviderItem).icons"
                :key="platform"
                :class="['flex size-8 items-center justify-center rounded-lg', platformBadgeLightClass(platform)]"
              >
                <PlatformIcon :platform="platform" size="lg" />
              </span>
            </span>
            <span class="block max-w-full wrap-break-word font-medium text-highlighted">{{ (item as unknown as KeyProviderItem).label }}</span>
          </template>
        </URadioGroup>

        <UFormField :label="t('keys.groupLabel')" :description="formData.smart_routing ? t('keys.smartRoutingHint') : undefined">
          <template #hint>
            <UCheckbox v-model="formData.smart_routing" :label="t('keys.smartRouting')" />
          </template>
          <div v-if="formData.smart_routing" class="space-y-2">
            <div v-if="formData.group_ids.length" class="divide-y divide-default rounded-md ring ring-default">
              <div
                v-for="(groupId, index) in formData.group_ids"
                :key="`${groupId}-${index}`"
                class="flex items-center gap-2 px-3 py-1.5"
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
                <UButton color="neutral" variant="ghost" size="xs" icon="i-lucide-arrow-up" :aria-label="t('keys.moveRouteUp')" :title="t('keys.moveRouteUp')" @click="moveSmartRoute(index, -1)" />
                <UButton color="neutral" variant="ghost" size="xs" icon="i-lucide-arrow-down" :aria-label="t('keys.moveRouteDown')" :title="t('keys.moveRouteDown')" @click="moveSmartRoute(index, 1)" />
                <UButton color="error" variant="ghost" size="xs" icon="i-lucide-x" :aria-label="t('keys.removeRoute')" :title="t('keys.removeRoute')" @click="removeSmartRoute(index)" />
              </div>
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

        <div v-if="showDeepSeekToolInjection" class="flex items-start justify-between gap-4">
          <div>
            <span id="key-deepseek-tool-injection-label" class="text-sm font-medium text-default">{{ t('keys.deepseekToolInjection') }}</span>
            <p id="key-deepseek-tool-injection-hint" class="text-sm text-muted">{{ t('keys.deepseekToolInjectionHint') }}</p>
          </div>
          <Toggle
            v-model="formData.deepseek_tool_injection_enabled"
            aria-labelledby="key-deepseek-tool-injection-label"
            aria-describedby="key-deepseek-tool-injection-hint"
          />
        </div>

        <UFormField v-if="showEditModal" :label="t('keys.statusLabel')">
          <Select v-model="formData.status" :options="statusOptions" :placeholder="t('keys.selectStatus')" />
        </UFormField>

        <UFormField :label="t('keys.quotaLimit')" :help="t('keys.quotaAmountHint')">
          <UInput
            v-model.number="formData.quota"
            type="number"
            step="0.01"
            min="0"
            class="w-full"
            :placeholder="t('keys.quotaAmountPlaceholder')"
          >
            <template #leading><span class="text-sm text-muted">$</span></template>
          </UInput>
        </UFormField>

        <UFormField v-if="showEditModal && selectedKey && selectedKey.quota > 0" :label="t('keys.quotaUsed')">
          <div class="flex items-center gap-2">
            <div class="flex-1 rounded-md bg-elevated px-3 py-1.5 text-sm tabular-nums">
              <span class="font-medium text-highlighted">${{ selectedKey.quota_used?.toFixed(4) || '0.0000' }}</span>
              <span class="mx-2 text-dimmed">/</span>
              <span class="text-muted">${{ selectedKey.quota?.toFixed(2) || '0.00' }}</span>
            </div>
            <UButton color="neutral" variant="outline" :title="t('keys.resetQuotaUsed')" @click="confirmResetQuota">
              {{ t('keys.reset') }}
            </UButton>
          </div>
        </UFormField>

        <div class="space-y-3">
          <div class="flex items-center justify-between gap-4">
            <span id="key-expiration-label" class="text-sm font-medium text-default">{{ t('keys.expiration') }}</span>
            <Toggle v-model="formData.enable_expiration" aria-labelledby="key-expiration-label" />
          </div>
          <div v-if="formData.enable_expiration" class="space-y-3">
            <div class="flex flex-wrap gap-1.5">
              <UButton
                v-for="days in ['7', '30', '90']"
                :key="days"
                color="neutral"
                :variant="formData.expiration_preset === days ? 'subtle' : 'outline'"
                :class="formData.expiration_preset === days ? 'ring-accented font-semibold' : ''"
                @click="setExpirationDays(parseInt(days))"
              >
                {{ showEditModal ? t('keys.extendDays', { days }) : t('keys.expiresInDays', { days }) }}
              </UButton>
              <UButton
                color="neutral"
                :variant="formData.expiration_preset === 'custom' ? 'subtle' : 'outline'"
                :class="formData.expiration_preset === 'custom' ? 'ring-accented font-semibold' : ''"
                @click="formData.expiration_preset = 'custom'"
              >
                {{ t('keys.customDate') }}
              </UButton>
            </div>
            <UFormField :label="t('keys.expirationDate')" :help="t('keys.expirationDateHint')">
              <UInput v-model="formData.expiration_date" type="datetime-local" class="w-full" :ui="{ base: 'dark:[color-scheme:dark]' }" />
            </UFormField>
            <p v-if="showEditModal && selectedKey?.expires_at" class="text-sm">
              <span class="text-muted">{{ t('keys.currentExpiration') }}: </span>
              <span class="font-medium text-highlighted">{{ formatDateTime(selectedKey.expires_at) }}</span>
            </p>
          </div>
        </div>

        <UCollapsible v-model:open="advancedOpen" class="border-t border-default pt-3">
          <UButton
            color="neutral"
            variant="ghost"
            trailing-icon="i-lucide-chevron-down"
            class="-mx-2.5"
            :ui="{ trailingIcon: 'transition-transform group-data-[state=open]:rotate-180' }"
            :label="t('keys.advancedOptions')"
          />
          <template #content>
            <div class="space-y-4 pt-3">
              <div v-if="!showEditModal" class="space-y-3">
                <div class="flex items-center justify-between gap-4">
                  <span id="key-custom-key-label" class="text-sm font-medium text-default">{{ t('keys.customKeyLabel') }}</span>
                  <Toggle v-model="formData.use_custom_key" aria-labelledby="key-custom-key-label" />
                </div>
                <UFormField
                  v-if="formData.use_custom_key"
                  :error="customKeyError || undefined"
                  :help="customKeyError ? undefined : t('keys.customKeyHint')"
                >
                  <UInput v-model="formData.custom_key" type="text" class="w-full" :ui="{ base: 'font-mono' }" :placeholder="t('keys.customKeyPlaceholder')" />
                </UFormField>
              </div>

              <div class="space-y-3">
                <div class="flex items-center justify-between gap-4">
                  <span id="key-ip-restriction-label" class="text-sm font-medium text-default">{{ t('keys.ipRestriction') }}</span>
                  <Toggle v-model="formData.enable_ip_restriction" aria-labelledby="key-ip-restriction-label" />
                </div>
                <template v-if="formData.enable_ip_restriction">
                  <UFormField :label="t('keys.ipWhitelist')" :help="t('keys.ipWhitelistHint')">
                    <UTextarea v-model="formData.ip_whitelist" :rows="3" class="w-full" :ui="{ base: 'font-mono text-sm' }" :placeholder="t('keys.ipWhitelistPlaceholder')" />
                  </UFormField>
                  <UFormField :label="t('keys.ipBlacklist')" :help="t('keys.ipBlacklistHint')">
                    <UTextarea v-model="formData.ip_blacklist" :rows="3" class="w-full" :ui="{ base: 'font-mono text-sm' }" :placeholder="t('keys.ipBlacklistPlaceholder')" />
                  </UFormField>
                </template>
              </div>

              <div class="space-y-3">
                <div class="flex items-center justify-between gap-4">
                  <span id="key-rate-limit-label" class="text-sm font-medium text-default">{{ t('keys.rateLimitSection') }}</span>
                  <Toggle v-model="formData.enable_rate_limit" aria-labelledby="key-rate-limit-label" />
                </div>
                <template v-if="formData.enable_rate_limit">
                  <p class="text-xs text-muted">{{ t('keys.rateLimitHint') }}</p>
                  <UFormField :label="t('keys.rateLimit5h')">
                    <UInput v-model.number="formData.rate_limit_5h" type="number" step="0.01" min="0" class="w-full" placeholder="0">
                      <template #leading><span class="text-sm text-muted">$</span></template>
                    </UInput>
                    <div v-if="showEditModal && selectedKey && selectedKey.rate_limit_5h > 0" class="mt-2 space-y-1">
                      <p class="text-sm tabular-nums"><span class="font-medium" :class="limitTextClass(selectedKey.usage_5h, selectedKey.rate_limit_5h)">${{ selectedKey.usage_5h?.toFixed(4) || '0.0000' }}</span><span class="mx-2 text-dimmed">/</span><span class="text-muted">${{ selectedKey.rate_limit_5h?.toFixed(2) || '0.00' }}</span></p>
                      <UProgress :model-value="limitPercent(selectedKey.usage_5h, selectedKey.rate_limit_5h)" :color="limitColor(selectedKey.usage_5h, selectedKey.rate_limit_5h)" size="xs" />
                    </div>
                  </UFormField>
                  <UFormField :label="t('keys.rateLimit1d')">
                    <UInput v-model.number="formData.rate_limit_1d" type="number" step="0.01" min="0" class="w-full" placeholder="0">
                      <template #leading><span class="text-sm text-muted">$</span></template>
                    </UInput>
                    <div v-if="showEditModal && selectedKey && selectedKey.rate_limit_1d > 0" class="mt-2 space-y-1">
                      <p class="text-sm tabular-nums"><span class="font-medium" :class="limitTextClass(selectedKey.usage_1d, selectedKey.rate_limit_1d)">${{ selectedKey.usage_1d?.toFixed(4) || '0.0000' }}</span><span class="mx-2 text-dimmed">/</span><span class="text-muted">${{ selectedKey.rate_limit_1d?.toFixed(2) || '0.00' }}</span></p>
                      <UProgress :model-value="limitPercent(selectedKey.usage_1d, selectedKey.rate_limit_1d)" :color="limitColor(selectedKey.usage_1d, selectedKey.rate_limit_1d)" size="xs" />
                    </div>
                  </UFormField>
                  <UFormField :label="t('keys.rateLimit7d')">
                    <UInput v-model.number="formData.rate_limit_7d" type="number" step="0.01" min="0" class="w-full" placeholder="0">
                      <template #leading><span class="text-sm text-muted">$</span></template>
                    </UInput>
                    <div v-if="showEditModal && selectedKey && selectedKey.rate_limit_7d > 0" class="mt-2 space-y-1">
                      <p class="text-sm tabular-nums"><span class="font-medium" :class="limitTextClass(selectedKey.usage_7d, selectedKey.rate_limit_7d)">${{ selectedKey.usage_7d?.toFixed(4) || '0.0000' }}</span><span class="mx-2 text-dimmed">/</span><span class="text-muted">${{ selectedKey.rate_limit_7d?.toFixed(2) || '0.00' }}</span></p>
                      <UProgress :model-value="limitPercent(selectedKey.usage_7d, selectedKey.rate_limit_7d)" :color="limitColor(selectedKey.usage_7d, selectedKey.rate_limit_7d)" size="xs" />
                    </div>
                  </UFormField>
                  <UButton
                    v-if="showEditModal && selectedKey && (selectedKey.rate_limit_5h > 0 || selectedKey.rate_limit_1d > 0 || selectedKey.rate_limit_7d > 0)"
                    color="neutral"
                    variant="outline"
                    icon="i-lucide-rotate-ccw"
                    @click="confirmResetRateLimit"
                  >
                    {{ t('keys.resetRateLimitUsage') }}
                  </UButton>
                </template>
              </div>

              <UFormField :label="t('keys.concurrencyLimit')" :help="t('keys.concurrencyLimitHint')">
                <UInput v-model.number="formData.concurrency_limit" type="number" min="0" max="100000" step="1" class="w-full" />
              </UFormField>

              <UFormField :label="t('keys.anthropicCacheTtlMode')" :help="t('keys.anthropicCacheTtlModeHint')">
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
            </div>
          </template>
        </UCollapsible>
      </form>
      <template #footer>
        <div class="flex justify-end gap-2">
          <UButton color="neutral" variant="subtle" @click="closeModals">
            {{ t('common.cancel') }}
          </UButton>
          <UButton
            form="key-form"
            type="submit"
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
        <p class="text-sm text-muted">{{ t('keys.ccsImport.description') }}</p>
        <UFormField :label="t('keys.ccsImport.platform')" name="ccs-import-platform">
          <Select
            id="ccs-import-platform"
            :model-value="ccsImportApp"
            :options="ccsAppOptions"
            :aria-label="t('keys.ccsImport.platform')"
            @update:model-value="onCcsAppChange"
          />
        </UFormField>
        <UFormField
          :label="t('keys.ccsImport.model')"
          name="ccs-import-model"
          :help="ccsModelsLoading ? t('keys.ccsImport.modelLoading') : t('keys.ccsImport.modelHint')"
        >
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
        </UFormField>
        <UAlert
          v-if="ccsImportDeeplinkUnsupported"
          color="warning"
          variant="subtle"
          icon="i-lucide-triangle-alert"
          :description="t('keys.ccsImport.deeplinkUnsupported')"
        />
      </div>
      <template #footer>
        <div class="flex justify-end gap-2">
          <UButton color="neutral" variant="subtle" @click="closeCcsImportModal">
            {{ t('common.cancel') }}
          </UButton>
          <UButton :disabled="ccsImportDeeplinkUnsupported" @click="confirmCcsImport">
            {{ t('common.import') }}
          </UButton>
        </div>
      </template>
    </BaseDialog>
  </AppLayout>
</template>

<script setup lang="ts">
	import { ref, reactive, computed, watch, onMounted, onUnmounted } from 'vue'
	import type { DropdownMenuItem } from '@nuxt/ui'
	import { useI18n } from 'vue-i18n'
	import { useAppStore } from '@/stores/app'
	import { useOnboardingStore } from '@/stores/onboarding'
	import { useClipboard } from '@/composables/useClipboard'
import { getPersistedPageSize } from '@/composables/usePersistedPageSize'

const { t } = useI18n()
import { keysAPI, authAPI, usageAPI, userGroupsAPI, endpointsAPI } from '@/api'
import AppLayout from '@/components/layout/AppLayout.vue'
import TablePageLayout from '@/components/layout/TablePageLayout.vue'
import PageToolbar from '@/components/layout/PageToolbar.vue'
	import DataTable from '@/components/common/DataTable.vue'
	import Pagination from '@/components/common/Pagination.vue'
	import BaseDialog from '@/components/common/BaseDialog.vue'
	import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
	import EmptyState from '@/components/common/EmptyState.vue'
	import Select from '@/components/common/Select.vue'
	import SearchInput from '@/components/common/SearchInput.vue'
	import Toggle from '@/components/common/Toggle.vue'
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

// "Display" menu: one checkbox per hideable column; the menu stays open while toggling.
const columnMenuItems = computed<DropdownMenuItem[]>(() =>
  toggleableColumns.value.map((col) => ({
    label: col.label,
    type: 'checkbox' as const,
    checked: isColumnVisible(col.key),
    onUpdateChecked(checked: boolean) {
      if (checked !== isColumnVisible(col.key)) toggleColumn(col.key)
    },
    onSelect(event: Event) {
      event.preventDefault()
    }
  }))
)

// Shared colour rules for quota and rate-limit bars: warn at 80 %, error when used up.
const limitPercent = (used: number | null | undefined, limit: number) => Math.min(((used ?? 0) / limit) * 100, 100)
const limitColor = (used: number | null | undefined, limit: number) =>
  (used ?? 0) >= limit ? 'error' : (used ?? 0) >= limit * 0.8 ? 'warning' : 'primary'
const limitTextClass = (used: number | null | undefined, limit: number) =>
  (used ?? 0) >= limit ? 'text-error' : (used ?? 0) >= limit * 0.8 ? 'text-warning' : 'text-highlighted'

const rateWindows = (row: ApiKey) => [
  { label: '5h', limit: row.rate_limit_5h, used: row.usage_5h, resetAt: row.reset_5h_at },
  { label: '1d', limit: row.rate_limit_1d, used: row.usage_1d, resetAt: row.reset_1d_at },
  { label: '7d', limit: row.rate_limit_7d, used: row.usage_7d, resetAt: row.reset_7d_at }
].filter((win) => win.limit > 0)

const statusColor = (status: string) =>
  status === 'active' ? 'success' : status === 'quota_exhausted' ? 'warning' : status === 'expired' ? 'error' : 'neutral'

// Row "…" menu: everything except the two primary row actions (use key, edit).
const rowMenuItems = (row: ApiKey): DropdownMenuItem[][] => [
  [
    ...(!publicSettings.value?.hide_ccs_import_button
      ? [{ label: t('keys.importToCcSwitch'), icon: 'i-lucide-upload', onSelect: () => importToCcswitch(row) }]
      : []),
    {
      label: row.status === 'active' ? t('keys.disable') : t('keys.enable'),
      icon: row.status === 'active' ? 'i-lucide-ban' : 'i-lucide-circle-check',
      onSelect: () => toggleKeyStatus(row)
    }
  ],
  [{ label: t('common.delete'), icon: 'i-lucide-trash-2', color: 'error' as const, onSelect: () => confirmDelete(row) }]
]

const apiKeys = ref<ApiKey[]>([])
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
let abortController: AbortController | null = null

// Rarely used key settings sit in a collapsed "Advanced options" block; it opens by
// itself when the key being edited already uses one of them.
const advancedOpen = ref(false)

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

interface KeyProviderItem { value: KeyProvider; label: string; icons: GroupPlatform[]; disabled: boolean }
const keyProviderItems = computed<KeyProviderItem[]>(() =>
  keyProviders.value.map((provider) => ({ ...provider, disabled: !availableKeyProviders.value.has(provider.value) }))
)

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
    await keysAPI.toggleStatus(key.id, newStatus)
    appStore.showSuccess(
      newStatus === 'active' ? t('keys.keyEnabledSuccess') : t('keys.keyDisabledSuccess')
    )
    loadApiKeys()
  } catch (error) {
    appStore.showError(t('keys.failedToUpdateStatus'))
  }
}

// The group cell is a per-row popover: positioning, outside clicks and Escape are
// handled by UPopover; only one row's selector is open at a time.
const setGroupSelectorOpen = (key: ApiKey, open: boolean) => {
  if (!open) {
    if (groupSelectorKeyId.value === key.id) groupSelectorKeyId.value = null
    return
  }
  if (isSmartRoutingKey(key)) {
    appStore.showError(t('keys.smartRoutingChangeInEditor'))
    return
  }
  groupSelectorKeyId.value = key.id
  groupSearchQuery.value = ''
}

const changeGroup = async (key: ApiKey, newGroupId: number | null) => {
  groupSelectorKeyId.value = null
  if (isSmartRoutingKey(key)) {
    appStore.showError(t('keys.smartRoutingChangeInEditor'))
    return
  }
  if (key.group_id === newGroupId) return

  try {
    await keysAPI.update(key.id, { group_id: newGroupId })
    appStore.showSuccess(t('keys.groupChangedSuccess'))
    loadApiKeys()
  } catch (error) {
    appStore.showError(t('keys.failedToChangeGroup'))
  }
}

const confirmDelete = (key: ApiKey) => {
  selectedKey.value = key
  showDeleteDialog.value = true
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
      await keysAPI.update(selectedKey.value.id, updates)
      appStore.showSuccess(t('keys.keyUpdatedSuccess'))
    } else {
      const customKey = formData.value.use_custom_key ? formData.value.custom_key : undefined
      await keysAPI.create(
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
      appStore.showSuccess(t('keys.keyCreatedSuccess'))
      // Only advance tour if active, on submit step, and creation succeeded
      if (onboardingStore.isCurrentStep('[data-tour="key-form-submit"]')) {
        onboardingStore.nextStep(500)
      }
    }
    closeModals()
    loadApiKeys()
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
  if (!selectedKey.value) return

  try {
    await keysAPI.delete(selectedKey.value.id)
    appStore.showSuccess(t('keys.keyDeletedSuccess'))
    showDeleteDialog.value = false
    loadApiKeys()
  } catch (error: any) {
    // 优先使用后端返回的错误消息，提供更具体的错误信息给用户
    const errorMsg = error?.message || t('keys.failedToDelete')
    appStore.showError(errorMsg)
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
    await keysAPI.update(selectedKey.value.id, { reset_rate_limit_usage: true })
    appStore.showSuccess(t('keys.rateLimitResetSuccess'))
    // Refresh key data
    await loadApiKeys()
    // Update the editing key with fresh data
    const refreshedKey = apiKeys.value.find(k => k.id === selectedKey.value!.id)
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
  resetTimer = setInterval(() => { now.value = new Date() }, 60000)
})

watch(
  () => showCreateModal.value || showEditModal.value,
  (open) => {
    if (!open) return
    const form = formData.value
    advancedOpen.value = form.use_custom_key || form.enable_ip_restriction || form.enable_rate_limit ||
      (form.concurrency_limit ?? 0) > 0 || form.anthropic_cache_ttl_mode !== 'inherit'
  }
)

onUnmounted(() => {
  if (resetTimer) clearInterval(resetTimer)
  ccsModelsAbort?.abort()
})
</script>
