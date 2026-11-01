<template>
  <AppLayout>
    <TablePageLayout class="groups-workspace">
      <template #filters>
        <AdminPageHeader
          class="groups-page-header"
          :title="t('admin.groups.title')"
          :description="t('admin.groups.description')"
        >
          <template #actions>
            <UButton
              icon="i-lucide-plus"
              data-tour="groups-create-btn"
              @click="openCreateModal"
            >
              {{ t("admin.groups.createGroup") }}
            </UButton>
          </template>
        </AdminPageHeader>
        <PageToolbar>
          <UInput
            v-model="searchQuery"
            icon="i-lucide-search"
            :placeholder="t('admin.groups.searchGroups')"
            class="w-96 max-w-full sm:w-72"
            @input="handleSearch"
          />
          <UButton
            class="md:hidden"
            icon="i-lucide-list-filter"
            color="neutral"
            variant="outline"
            :aria-expanded="groupFiltersExpanded"
            :title="t('common.filter')"
            @click="groupFiltersExpanded = !groupFiltersExpanded"
          >
            {{ t("common.filter") }}
            <UBadge v-if="activeGroupFilterCount" color="primary" variant="subtle" size="sm">{{ activeGroupFilterCount }}</UBadge>
          </UButton>
          <div
            :class="[
              groupFiltersExpanded ? 'grid' : 'hidden',
              'w-[48rem] max-w-full grid-cols-2 gap-2 max-[359px]:grid-cols-1 md:flex md:w-auto md:items-center',
            ]"
          >
            <Select
              v-model="filters.platform"
              :options="platformFilterOptions"
              :placeholder="t('admin.groups.allPlatforms')"
              class="md:w-40"
              @change="loadGroups"
            />
            <Select
              v-model="filters.status"
              :options="statusOptions"
              :placeholder="t('admin.groups.allStatus')"
              class="md:w-36"
              @change="loadGroups"
            />
            <Select
              v-if="!authStore.isSimpleMode"
              v-model="filters.is_exclusive"
              :options="exclusiveOptions"
              :placeholder="t('admin.groups.allGroups')"
              class="md:w-36"
              @change="loadGroups"
            />
          </div>
          <template #right>
            <UButton
              icon="i-lucide-refresh-cw"
              color="neutral"
              variant="outline"
              square
              :disabled="loading"
              :loading="loading"
              :title="t('common.refresh')"
              :aria-label="t('common.refresh')"
              @click="loadGroups"
            />
            <UDropdownMenu :items="columnMenuItems" :content="{ align: 'end' }">
              <UButton
                color="neutral"
                variant="outline"
                trailing-icon="i-lucide-settings-2"
                data-testid="groups-display-columns"
                :label="t('admin.groups.displayColumns')"
              />
            </UDropdownMenu>
            <UButton
              v-if="!authStore.isSimpleMode"
              icon="i-lucide-arrow-up-down"
              color="neutral"
              variant="outline"
              :title="t('admin.groups.sortOrder')"
              :aria-label="t('admin.groups.sortOrder')"
              @click="openSortModal"
            >
              <span class="max-sm:hidden">{{ t("admin.groups.sortOrder") }}</span>
            </UButton>
          </template>
        </PageToolbar>
      </template>

      <template #table>
        <div
          v-if="selectedGroupIds.length > 0"
          class="mb-2 flex flex-wrap items-center gap-2 rounded-lg bg-elevated/60 px-3 py-2"
          data-testid="groups-bulk-bar"
        >
          <span class="text-sm text-muted">{{
            t("common.selectedCount", { count: selectedGroupIds.length })
          }}</span>
          <UButton
            size="xs"
            color="error"
            variant="soft"
            icon="i-lucide-trash-2"
            data-testid="groups-bulk-delete"
            :loading="bulkDeleting"
            :disabled="bulkDeleting"
            :label="t('admin.groups.deleteGroup')"
            @click="handleBulkDelete"
          />
          <UButton
            size="xs"
            color="neutral"
            variant="ghost"
            data-testid="groups-clear-selection"
            :label="t('common.cancel')"
            @click="clearGroupSelection"
          />
        </div>
        <DataTable
          :columns="columns"
          :data="groups"
          :loading="loading"
          row-key="id"
          selectable
          :selected-keys="selectedGroupIds"
          :server-side-sort="true"
          default-sort-key="sort_order"
          default-sort-order="asc"
          @sort="handleSort"
          @update:selected-keys="handleSelectionChange"
        >
          <template #cell-name="{ value }">
            <span class="font-medium text-highlighted">{{
              value
            }}</span>
          </template>

          <template #cell-id="{ value }">
            <span class="font-mono text-xs text-muted"
              >#{{ value }}</span
            >
          </template>

          <template #cell-platform="{ value }">
            <UBadge color="neutral" variant="subtle" class="gap-1.5">
              <PlatformIcon :platform="value" size="xs" />
              {{ t("admin.groups.platforms." + value) }}
            </UBadge>
          </template>

          <template #cell-billing_type="{ row }">
            <div class="space-y-1">
              <UBadge
                :color="row.subscription_type === 'subscription' ? 'primary' : 'neutral'"
                variant="subtle"
              >
                {{
                  row.subscription_type === "subscription"
                    ? t("admin.groups.subscription.subscription")
                    : t("admin.groups.subscription.standard")
                }}
              </UBadge>
              <!-- Subscription Limits - compact single line -->
              <div
                v-if="row.subscription_type === 'subscription'"
                class="space-y-0.5 text-xs text-muted"
              >
                <div
                  v-if="
                    row.daily_limit_usd != null ||
                    row.weekly_limit_usd != null ||
                    row.monthly_limit_usd != null
                  "
                  class="flex flex-wrap items-center gap-x-1 gap-y-0.5"
                >
                  <span v-if="row.daily_limit_usd != null" class="whitespace-nowrap">
                    <span
                      v-if="usageLoading"
                      class="font-medium text-dimmed"
                      >—</span
                    >
                    <span
                      v-else
                      :class="
                        getQuotaUsageClass(
                          usageMap.get(row.id)?.today_cost ?? 0,
                          row.daily_limit_usd
                        )
                      "
                      >{{
                        formatUsd(usageMap.get(row.id)?.today_cost ?? 0)
                      }}</span
                    >
                    <span class="text-dimmed">
                      / {{ formatUsd(row.daily_limit_usd) }}/{{
                        t("admin.groups.limitDay")
                      }}</span
                    >
                  </span>
                  <span
                    v-if="
                      row.daily_limit_usd != null &&
                      (row.weekly_limit_usd != null || row.monthly_limit_usd != null)
                    "
                    class="mx-1 text-dimmed"
                    >·</span
                  >
                  <span v-if="row.weekly_limit_usd != null" class="whitespace-nowrap"
                    >{{ formatUsd(row.weekly_limit_usd) }}/{{
                      t("admin.groups.limitWeek")
                    }}</span
                  >
                  <span
                    v-if="row.weekly_limit_usd != null && row.monthly_limit_usd != null"
                    class="mx-1 text-dimmed"
                    >·</span
                  >
                  <span v-if="row.monthly_limit_usd != null" class="whitespace-nowrap"
                    >{{ formatUsd(row.monthly_limit_usd) }}/{{
                      t("admin.groups.limitMonth")
                    }}</span
                  >
                </div>
                <span v-else class="text-dimmed">{{
                  t("admin.groups.subscription.noLimit")
                }}</span>
                <div class="text-dimmed">
                  {{ t("admin.groups.usageTotal") }}
                  <span class="ml-1 font-medium text-default"
                    >{{
                      usageLoading
                        ? "—"
                        : formatUsd(usageMap.get(row.id)?.total_cost ?? 0)
                    }}</span
                  >
                </div>
              </div>
            </div>
          </template>

          <template #cell-rate_multiplier="{ value }">
            <span class="text-sm text-default"
              >{{ value }}x</span
            >
          </template>

          <template #cell-is_exclusive="{ value }">
            <UBadge :color="value ? 'primary' : 'neutral'" variant="subtle">
              {{
                value ? t("admin.groups.exclusive") : t("admin.groups.public")
              }}
            </UBadge>
          </template>

          <template #cell-account_count="{ row }">
            <div class="space-y-0.5 text-xs text-muted">
              <div>
                {{ t("admin.groups.accountsAvailable") }}
                <span class="font-medium text-success">{{ row.active_account_count || 0 }}</span>
                <span class="text-dimmed">{{ t("admin.groups.accountsUnit") }}</span>
              </div>
              <div v-if="row.rate_limited_account_count">
                {{ t("admin.groups.accountsRateLimited") }}
                <span class="font-medium text-warning">{{ row.rate_limited_account_count }}</span>
                <span class="text-dimmed">{{ t("admin.groups.accountsUnit") }}</span>
              </div>
              <div>
                {{ t("admin.groups.accountsTotal") }}
                <span class="font-medium text-highlighted">{{ row.account_count || 0 }}</span>
                <span class="text-dimmed">{{ t("admin.groups.accountsUnit") }}</span>
              </div>
            </div>
          </template>

          <template #cell-capacity="{ row }">
            <GroupCapacityBadge
              v-if="capacityMap.get(row.id)"
              :concurrency-used="capacityMap.get(row.id)!.concurrencyUsed"
              :concurrency-max="capacityMap.get(row.id)!.concurrencyMax"
              :sessions-used="capacityMap.get(row.id)!.sessionsUsed"
              :sessions-max="capacityMap.get(row.id)!.sessionsMax"
              :rpm-used="capacityMap.get(row.id)!.rpmUsed"
              :rpm-max="capacityMap.get(row.id)!.rpmMax"
            />
            <span v-else class="text-xs text-dimmed">—</span>
          </template>

          <template #cell-usage="{ row }">
            <div v-if="usageLoading" class="text-xs text-dimmed">—</div>
            <div v-else class="space-y-0.5 text-xs">
              <div class="text-muted">
                <span class="text-dimmed">{{
                  t("admin.groups.usageToday")
                }}</span>
                <span class="ml-1 font-medium text-default"
                  >${{
                    formatCost(usageMap.get(row.id)?.today_cost ?? 0)
                  }}</span
                >
              </div>
              <div class="text-muted">
                <span class="text-dimmed">{{
                  t("admin.groups.usageYesterday")
                }}</span>
                <span class="ml-1 font-medium text-default"
                  >${{
                    formatCost(usageMap.get(row.id)?.yesterday_cost ?? 0)
                  }}</span
                >
              </div>
              <div class="text-muted">
                <span class="text-dimmed">{{
                  t("admin.groups.usageTotal")
                }}</span>
                <span class="ml-1 font-medium text-default"
                  >${{
                    formatCost(usageMap.get(row.id)?.total_cost ?? 0)
                  }}</span
                >
              </div>
            </div>
          </template>

          <template #cell-status="{ value }">
            <UBadge :color="value === 'active' ? 'success' : 'error'" variant="subtle">
              {{ t("admin.accounts.status." + value) }}
            </UBadge>
          </template>

          <template #cell-actions="{ row }">
            <div class="flex items-center gap-0.5">
              <AdminRowAction
                :label="t('common.edit')"
                icon="edit"
                tone="primary"
                @click="handleEdit(row)"
              />
              <AdminRowAction
                v-if="!authStore.isSimpleMode"
                data-testid="group-duplicate"
                :label="
                  duplicatingGroupIds.has(row.id)
                    ? t('admin.groups.duplicating')
                    : t('admin.groups.duplicate')
                "
                icon="copy"
                tone="primary"
                :disabled="duplicatingGroupIds.has(row.id)"
                @click="handleDuplicate(row)"
              />
              <UDropdownMenu :items="rowMenuItems(row)" :content="{ align: 'end' }">
                <UButton
                  icon="i-lucide-ellipsis-vertical"
                  color="neutral"
                  variant="ghost"
                  size="sm"
                  square
                  class="groups-row-more"
                  data-testid="group-row-more"
                  :title="t('common.more')"
                  :aria-label="t('common.more')"
                />
              </UDropdownMenu>
            </div>
          </template>

          <template #empty>
            <EmptyState
              :title="t('admin.groups.noGroupsYet')"
              :description="t('admin.groups.createFirstGroup')"
              :action-text="t('admin.groups.createGroup')"
              @action="openCreateModal"
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

    <!-- Create Group Modal -->
    <BaseDialog
      :show="showCreateModal"
      :title="t('admin.groups.createGroup')"
      width="wide"
      @close="closeCreateModal"
    >
      <form
        id="create-group-form"
        @submit.prevent="handleCreateGroup"
        class="space-y-8"
      >
        <section class="space-y-5">
          <h3 class="text-base font-semibold text-highlighted">{{ t("admin.groups.formSections.basic") }}</h3>
          <UFormField :label="t('admin.groups.form.name')" required>
            <UInput
              v-model="createForm.name"
              type="text"
              required
              :placeholder="t('admin.groups.enterGroupName')"
              data-tour="group-form-name"
              class="w-full"
            />
          </UFormField>
          <UFormField :label="t('admin.groups.form.description')">
            <UTextarea
              v-model="createForm.description"
              :rows="3"
              :placeholder="t('admin.groups.optionalDescription')"
              class="w-full"
            />
          </UFormField>
          <UFormField :label="t('admin.groups.form.platform')" :help="t('admin.groups.platformHint')">
            <Select
              v-model="createForm.platform"
              :options="platformOptions"
              data-tour="group-form-platform"
              @change="createForm.copy_accounts_from_group_ids = []"
            />
          </UFormField>
          <!-- 从分组复制账号（多选，账号自动去重） -->
          <UFormField
            v-if="!authStore.isSimpleMode && copyAccountsGroupOptions.length > 0"
            :help="t('admin.groups.copyAccounts.hint')"
          >
            <template #label>
              <span class="inline-flex items-center gap-1">{{ t("admin.groups.copyAccounts.title") }} <GroupFieldHelp :label="t('admin.groups.copyAccounts.title')" :text="t('admin.groups.copyAccounts.tooltip')" /></span>
            </template>
            <USelectMenu
              v-model="createForm.copy_accounts_from_group_ids"
              :items="copyAccountsGroupOptions"
              value-key="value"
              label-key="label"
              multiple
              :placeholder="t('admin.groups.copyAccounts.selectPlaceholder')"
              class="w-full"
            />
          </UFormField>
        </section>
        <template v-if="!authStore.isSimpleMode">
          <section class="space-y-5">
            <h3 class="text-base font-semibold text-highlighted">{{ t("admin.groups.formSections.billing") }}</h3>
            <UFormField
              :label="t('admin.groups.form.rateMultiplier')"
              :help="t('admin.groups.rateMultiplierHint')"
              required
            >
              <UInput
                v-model.number="createForm.rate_multiplier"
                type="number"
                step="0.001"
                min="0.001"
                required
                data-tour="group-form-multiplier"
                class="w-full"
              />
            </UFormField>
            <UFormField :label="t('admin.groups.form.rpmLimit')" :help="t('admin.groups.form.rpmLimitHint')">
              <UInput
                v-model.number="createForm.rpm_limit"
                type="number"
                min="0"
                step="1"
                :placeholder="t('admin.groups.form.rpmLimitPlaceholder')"
                class="w-full"
              />
            </UFormField>
            <div
              v-if="createForm.subscription_type !== 'subscription'"
              data-tour="group-form-exclusive"
            >
              <UFormField :description="createForm.is_exclusive
                      ? t('admin.groups.exclusive')
                      : t('admin.groups.public')" class="flex items-center justify-between gap-4" :ui="{ container: 'mt-0 shrink-0' }">
                <template #label>
                  <span class="inline-flex items-center gap-1">{{ t("admin.groups.form.exclusive") }} <GroupFieldHelp :label="t('admin.groups.form.exclusive')" :title="t('admin.groups.exclusiveTooltip.title')" :text="t('admin.groups.exclusiveTooltip.description')">
                      <p class="text-muted"><span class="font-medium text-default">{{ t("admin.groups.exclusiveTooltip.example") }}</span> {{ t("admin.groups.exclusiveTooltip.exampleContent") }}</p>
                    </GroupFieldHelp></span>
                </template>
                <Toggle v-model="createForm.is_exclusive" />
              </UFormField>
            </div>
            <!-- Subscription Configuration -->
            <div>
              <UFormField
                :label="t('admin.groups.subscription.type')"
                :help="t('admin.groups.subscription.typeHint')"
              >
                <Select
                  v-model="createForm.subscription_type"
                  :options="subscriptionTypeOptions"
                />
              </UFormField>

              <!-- Subscription limits (only show when subscription type is selected) -->
              <div
                v-if="createForm.subscription_type === 'subscription'"
                class="mt-4 grid grid-cols-1 gap-4 sm:grid-cols-3"
              >
                <UFormField :label="t('admin.groups.subscription.dailyLimit')">
                  <UInput
                    v-model.number="createForm.daily_limit_usd"
                    type="number"
                    step="0.01"
                    min="0"
                    :placeholder="t('admin.groups.subscription.noLimit')"
                    class="w-full"
                  />
                </UFormField>
                <UFormField :label="t('admin.groups.subscription.weeklyLimit')">
                  <UInput
                    v-model.number="createForm.weekly_limit_usd"
                    type="number"
                    step="0.01"
                    min="0"
                    :placeholder="t('admin.groups.subscription.noLimit')"
                    class="w-full"
                  />
                </UFormField>
                <UFormField :label="t('admin.groups.subscription.monthlyLimit')">
                  <UInput
                    v-model.number="createForm.monthly_limit_usd"
                    type="number"
                    step="0.01"
                    min="0"
                    :placeholder="t('admin.groups.subscription.noLimit')"
                    class="w-full"
                  />
                </UFormField>
              </div>
            </div>
            <!-- 高峰时段倍率配置（仅订阅类型分组） -->
            <div v-if="createForm.subscription_type === 'subscription'">
              <div class="mb-4 grid grid-cols-1 gap-3 md:grid-cols-2">
                <UCheckbox v-model="createForm.peak_rate_enabled" :label="t('admin.groups.peakRate.enable')" />
              </div>
              <div
                v-if="createForm.peak_rate_enabled"
                class="mb-4 grid grid-cols-1 gap-3 sm:grid-cols-3"
              >
                <UFormField :label="t('admin.groups.peakRate.peakStart')">
                  <TimeWheelPicker v-model="createForm.peak_start" />
                </UFormField>
                <UFormField :label="t('admin.groups.peakRate.peakEnd')">
                  <TimeWheelPicker v-model="createForm.peak_end" />
                </UFormField>
                <UFormField :label="t('admin.groups.peakRate.peakMultiplier')">
                  <UInput
                    v-model.number="createForm.peak_rate_multiplier"
                    type="number"
                    step="0.001"
                    min="0"
                    placeholder="1"
                    :title="t('admin.groups.peakRate.multiplierHint')"
                    class="w-full"
                  />
                </UFormField>
              </div>
            </div>
            <!-- 分组利润控制（五个平台 token 请求） -->
            <div v-if="isProfitControlPlatform(createForm.platform)">
              <UCheckbox
                v-model="createForm.profit_control_enabled"
                :label="t('admin.groups.profitControl.enable')"
              />
              <p class="mb-3 mt-1.5 text-xs text-muted">
                {{
                  createForm.profit_control_enabled
                    ? t("admin.groups.profitControl.enabledHint")
                    : t("admin.groups.profitControl.disabledHint")
                }}
              </p>
              <div
                v-if="createForm.profit_control_enabled"
                class="mb-3 grid grid-cols-1 gap-3 sm:grid-cols-2"
              >
                <UFormField :label="t('admin.groups.profitControl.minMargin')">
                  <UInput
                    v-model.number="createForm.profit_min_margin_percent"
                    type="number"
                    step="0.1"
                    min="0"
                    max="99.99"
                    placeholder="0"
                    :title="t('admin.groups.profitControl.minMarginHint')"
                    class="w-full"
                  />
                </UFormField>
                <UFormField :label="t('admin.groups.profitControl.safetyBuffer')">
                  <UInput
                    v-model.number="createForm.profit_safety_buffer_percent"
                    type="number"
                    step="0.1"
                    min="0"
                    max="99.99"
                    placeholder="0"
                    :title="t('admin.groups.profitControl.safetyBufferHint')"
                    class="w-full"
                  />
                </UFormField>
              </div>
            </div>
          </section>
          <section class="space-y-5">
            <h3 class="text-base font-semibold text-highlighted">{{ t("admin.groups.formSections.models") }}</h3>
            <!-- Grok 安全拒绝单次扣费（放在模型白名单上方） -->
            <div>
              <UFormField
                :label="t('admin.groups.modelAllowlist.title')"
                :description="t('admin.groups.modelAllowlist.hint')"
                class="flex items-center justify-between gap-4 mb-3"
                :ui="{ container: 'mt-0 shrink-0' }"
              >
                <Toggle v-model="createModelAllowlistState.enabled" />
              </UFormField>
              <div
                v-if="createModelAllowlistState.enabled"
                class="overflow-hidden rounded-lg border border-default"
              >
                <div
                  v-if="!createModelAllowlistLoading && createModelAllowlistState.items.length > 0"
                  class="flex items-center justify-between gap-2 border-b border-default px-3 py-1.5 text-xs"
                >
                  <span class="text-muted">
                    {{
                      t("admin.groups.modelAllowlist.selectedSummary", {
                        selected: createModelAllowlistSelectedCount,
                        total: createModelAllowlistState.items.length,
                      })
                    }}
                  </span>
                  <div class="flex items-center gap-1.5">
                    <UButton
                      @click="selectAllModelAllowlistItems(createModelAllowlistState)"
                      color="primary"
                      variant="ghost"
                      size="sm"
                    >{{ t("admin.groups.modelAllowlist.selectAll") }}</UButton>
                    <UButton
                      @click="invertModelAllowlistSelection(createModelAllowlistState)"
                      color="neutral"
                      variant="ghost"
                      size="sm"
                    >{{ t("admin.groups.modelAllowlist.invertSelection") }}</UButton>
                  </div>
                </div>
                <div
                  class="max-h-64 space-y-2 overflow-y-auto p-2"
                >
                  <p v-if="createModelAllowlistLoading" class="text-xs text-muted">
                    {{ t("admin.groups.modelAllowlist.loading") }}
                  </p>
                  <p
                    v-else-if="createModelAllowlistState.items.length === 0"
                    class="text-xs text-muted"
                  >
                    {{ t("admin.groups.modelAllowlist.empty") }}
                  </p>
                  <div
                    v-for="(item, index) in createModelAllowlistState.items"
                    :key="item.id"
                    class="flex items-center gap-2 rounded-md px-2 py-1.5 hover:bg-elevated/50"
                  >
                    <UCheckbox v-model="item.selected" />
                    <span class="min-w-0 flex-1 break-all text-sm text-default">
                      {{ item.id }}
                      <span
                        v-if="item.id.endsWith('*')"
                        class="ml-1 rounded-sm bg-primary/10 px-1.5 py-0.5 text-[10px] font-medium text-primary"
                      >
                        {{ t("admin.groups.modelAllowlist.wildcardTag") }}
                      </span>
                    </span>
                    <UButton
                      :disabled="index === 0"
                      @click="moveCreateModelAllowlistItem(index, index - 1)"
                      icon="i-lucide-arrow-up"
                      color="neutral"
                      variant="ghost"
                      size="sm"
                      square
                    />
                    <UButton
                      :disabled="index === createModelAllowlistState.items.length - 1"
                      @click="moveCreateModelAllowlistItem(index, index + 1)"
                      icon="i-lucide-arrow-down"
                      color="neutral"
                      variant="ghost"
                      size="sm"
                      square
                    />
                  </div>
                </div>
                <div class="border-t border-default px-3 py-2">
                  <div class="flex items-center gap-2">
                    <UInput
                      v-model="createAllowlistCustomEntry"
                      type="text"
                      :placeholder="t('admin.groups.modelAllowlist.customPlaceholder')"
                      @keydown.enter.prevent="submitCreateAllowlistCustomEntry"
                      size="sm"
                      class="min-w-0 flex-1"
                    />
                    <UButton @click="submitCreateAllowlistCustomEntry" color="primary" variant="solid" size="sm">{{ t("admin.groups.modelAllowlist.addCustom") }}</UButton>
                  </div>
                  <p
                    v-if="createAllowlistCustomErrorKey"
                    class="mt-1 text-xs text-error"
                  >
                    {{ t(createAllowlistCustomErrorKey) }}
                  </p>
                </div>
              </div>
            </div>
            <ReasoningEffortPolicyFields
              v-if="supportsReasoningEffortPolicyPlatform(createForm.platform)"
              ref="createReasoningEffortPolicyRef"
              id-prefix="create-group-reasoning"
              :platform="createForm.platform"
              v-model:max-effort="createForm.max_reasoning_effort"
              v-model:over-limit="createForm.max_reasoning_effort_over_limit"
              v-model:mappings="createForm.reasoning_effort_mappings"
            />
            <!-- 支持的模型系列（仅 antigravity 平台） -->
            <div v-if="createForm.platform === 'antigravity'">
              <div class="mb-1.5 flex items-center gap-1">
                <label class="text-sm font-medium text-default">
                  {{ t("admin.groups.supportedScopes.title") }}
                </label>
                <!-- Help Tooltip -->
                <GroupFieldHelp :label="t('admin.groups.supportedScopes.title')" :text="t('admin.groups.supportedScopes.tooltip')" />
              </div>
              <div class="space-y-2">
                <UCheckbox
                  :model-value="createForm.supported_model_scopes.includes('claude')"
                  @update:model-value="toggleCreateScope('claude')"
                  :label="t('admin.groups.supportedScopes.claude')"
                />
                <UCheckbox
                  :model-value="createForm.supported_model_scopes.includes('gemini_text')"
                  @update:model-value="toggleCreateScope('gemini_text')"
                  :label="t('admin.groups.supportedScopes.geminiText')"
                />
                <UCheckbox
                  :model-value="createForm.supported_model_scopes.includes('gemini_image')"
                  @update:model-value="toggleCreateScope('gemini_image')"
                  :label="t('admin.groups.supportedScopes.geminiImage')"
                />
              </div>
              <p class="mt-2 text-xs text-muted">
                {{ t("admin.groups.supportedScopes.hint") }}
              </p>
            </div>
          </section>
          <section class="space-y-5">
            <h3 class="text-base font-semibold text-highlighted">{{ t("admin.groups.formSections.pricing") }}</h3>
            <div>
              <div class="flex flex-wrap items-start justify-between gap-3">
                <div class="min-w-0 flex-1">
                  <h4 class="text-sm font-medium text-default">{{ t("admin.groups.modelPricing.title") }}</h4>
                  <p class="mt-1 text-xs text-muted">{{ t("admin.groups.modelPricing.description") }}</p>
                </div>
                <UButton
                  @click="addGroupPricing(createForm.model_pricing)"
                  icon="i-lucide-plus"
                  color="neutral"
                  variant="outline"
                  class="shrink-0 whitespace-nowrap"
                >{{ t("admin.groups.modelPricing.add") }}</UButton>
              </div>
              <UCheckbox class="mt-3" v-model="createForm.long_context_pricing_enabled">
                <template #label>
                  <span><span class="block text-sm text-default">{{ t("admin.groups.modelPricing.longContext") }}</span><span class="block text-xs text-muted">{{ t("admin.groups.modelPricing.longContextHint") }}</span></span>
                </template>
              </UCheckbox>
              <div class="mt-3 space-y-2">
                <PricingEntryCard v-for="(entry, index) in createForm.model_pricing" :key="index" :entry="entry" :platform="createForm.platform" hide-token-intervals @update="createForm.model_pricing[index] = $event" @remove="createForm.model_pricing.splice(index, 1)" />
              </div>
            </div>
            <!-- 图片生成计费配置 -->
            <div
              v-if="supportsImagePricingPlatform(createForm.platform)"
            >
              <label
                class="block mb-2 font-medium text-default"
              >
                {{ t(imagePricingI18nKey(createForm.platform, "title")) }}
              </label>
              <p class="text-xs text-muted mb-3">
                {{ t(imagePricingI18nKey(createForm.platform, "description")) }}
              </p>
              <div class="mb-4 grid grid-cols-1 gap-3 md:grid-cols-2">
                <UCheckbox
                  v-model="createForm.allow_image_generation"
                  :label="t(imagePricingI18nKey(createForm.platform, 'allowImageGeneration'))"
                />
                <UCheckbox
                  v-model="createForm.image_rate_independent"
                  :label="t(imagePricingI18nKey(createForm.platform, 'independentMultiplier'))"
                />
              </div>
              <UFormField
                :label="t(imagePricingI18nKey(createForm.platform, 'imageMultiplier'))"
                v-if="createForm.image_rate_independent"
                class="mb-4"
              >
                <UInput
                  v-model.number="createForm.image_rate_multiplier"
                  type="number"
                  step="0.0001"
                  min="0"
                  placeholder="1"
                  class="w-full"
                />
              </UFormField>
              <div class="grid grid-cols-3 gap-3">
                <UFormField label="1K ($)">
                  <UInput
                    v-model.number="createForm.image_price_1k"
                    type="number"
                    step="0.001"
                    min="0"
                    :placeholder="getImagePricePlaceholder(createForm.platform, 'image_price_1k')"
                    class="w-full"
                  />
                </UFormField>
                <UFormField label="2K ($)">
                  <UInput
                    v-model.number="createForm.image_price_2k"
                    type="number"
                    step="0.001"
                    min="0"
                    :placeholder="getImagePricePlaceholder(createForm.platform, 'image_price_2k')"
                    class="w-full"
                  />
                </UFormField>
                <UFormField label="4K ($)">
                  <UInput
                    v-model.number="createForm.image_price_4k"
                    type="number"
                    step="0.001"
                    min="0"
                    :placeholder="getImagePricePlaceholder(createForm.platform, 'image_price_4k')"
                    class="w-full"
                  />
                </UFormField>
              </div>
              <p class="mt-3 text-xs text-muted">
                {{ t(imagePricingI18nKey(createForm.platform, "modeHint")) }}
              </p>
              <div class="mt-2 rounded-lg bg-elevated/50 p-3 text-xs text-default">
                <div class="mb-1 font-medium">
                  {{ t(imagePricingI18nKey(createForm.platform, "finalPricePreview")) }}
                </div>
                <div class="grid grid-cols-3 gap-2">
                  <div
                    v-for="item in createImageFinalPricePreview"
                    :key="item.label"
                  >
                    {{ item.label }}: {{ item.value }}
                  </div>
                </div>
              </div>
              <div v-if="createForm.platform === 'gemini' && createForm.allow_image_generation" class="mt-4">
                <UCheckbox
                  v-model="createForm.allow_batch_image_generation"
                  :label="t('admin.groups.imagePricing.allowBatchImageGeneration')"
                />
                <p class="mt-2 text-xs text-muted">
                  {{ t("admin.groups.imagePricing.batchSectionHint") }}
                </p>
                <div
                  v-if="createForm.allow_batch_image_generation"
                  class="mt-3 grid grid-cols-1 gap-3 md:grid-cols-2"
                >
                  <UFormField :label="t('admin.groups.imagePricing.batchDiscountMultiplier')">
                    <UInput
                      v-model.number="createForm.batch_image_discount_multiplier"
                      type="number"
                      step="0.0001"
                      min="0"
                      placeholder="0.5"
                      class="w-full"
                    />
                  </UFormField>
                  <UFormField :label="t('admin.groups.imagePricing.batchHoldMultiplier')">
                    <UInput
                      v-model.number="createForm.batch_image_hold_multiplier"
                      type="number"
                      step="0.0001"
                      min="0"
                      placeholder="0.6"
                      class="w-full"
                    />
                  </UFormField>
                </div>
              </div>
              <p
                v-else-if="createForm.platform !== 'gemini'"
                class="mt-4 text-xs text-muted"
              >
                {{ t("admin.groups.imagePricing.batchGeminiOnlyHint") }}
              </p>
            </div>
            <MiniMaxH3PricingFields v-if="createForm.platform === 'minimax'" v-model="createForm.video_model_prices" />
            <!-- 视频生成计费配置（仅 Grok 平台） -->
            <div
              v-if="supportsVideoPricingPlatform(createForm.platform)"
            >
              <label
                class="block mb-2 font-medium text-default"
              >
                {{ t(videoPricingI18nKey("title")) }}
              </label>
              <p class="text-xs text-muted mb-3">
                {{ t(videoPricingI18nKey("description")) }}
              </p>
              <div class="mb-4">
                <UCheckbox
                  v-model="createForm.video_rate_independent"
                  :label="t(videoPricingI18nKey('independentMultiplier'))"
                />
              </div>
              <UFormField
                :label="t(videoPricingI18nKey('videoMultiplier'))"
                v-if="createForm.video_rate_independent"
                class="mb-4"
              >
                <UInput
                  v-model.number="createForm.video_rate_multiplier"
                  type="number"
                  step="0.0001"
                  min="0"
                  placeholder="1"
                  class="w-full"
                />
              </UFormField>
              <div class="grid grid-cols-3 gap-3">
                <UFormField label="480p ($/s)">
                  <UInput
                    v-model.number="createForm.video_price_480p"
                    type="number"
                    step="0.001"
                    min="0"
                    :placeholder="getVideoPricePlaceholder(createForm.platform, 'video_price_480p')"
                    class="w-full"
                  />
                </UFormField>
                <UFormField label="720p ($/s)">
                  <UInput
                    v-model.number="createForm.video_price_720p"
                    type="number"
                    step="0.001"
                    min="0"
                    :placeholder="getVideoPricePlaceholder(createForm.platform, 'video_price_720p')"
                    class="w-full"
                  />
                </UFormField>
                <UFormField label="1080p ($/s)">
                  <UInput
                    v-model.number="createForm.video_price_1080p"
                    type="number"
                    step="0.001"
                    min="0"
                    :placeholder="getVideoPricePlaceholder(createForm.platform, 'video_price_1080p')"
                    class="w-full"
                  />
                </UFormField>
              </div>
              <div
                class="mt-4"
                data-testid="create-grok-video-model-prices"
              >
                <p class="text-sm font-medium text-default">
                  {{ t("admin.groups.videoPricing.modelOverridesTitle") }}
                </p>
                <p class="mt-1 text-xs text-muted">
                  {{ t("admin.groups.videoPricing.modelOverridesDescription") }}
                </p>
                <div class="mt-3 space-y-3">
                  <div
                    v-for="family in videoModelPriceFamilyRows(createForm.video_model_prices)"
                    :key="family.key"
                    class="grid gap-2 sm:grid-cols-[minmax(0,1fr)_repeat(3,minmax(0,7rem))] sm:items-end"
                  >
                    <div class="min-w-0 pb-1 font-mono text-xs text-default">
                      {{ family.label }}
                    </div>
                    <label
                      v-for="resolution in grokVideoPriceResolutions"
                      :key="resolution.key"
                      class="block"
                    >
                      <span class="mb-1 block text-xs text-muted">
                        {{ resolution.label }} ($/s)
                      </span>
                      <UInput
                        v-model.number.nullable="createForm.video_model_prices[family.key][resolution.key]"
                        type="number"
                        step="0.001"
                        min="0"
                        :data-testid="`create-grok-video-price-${family.key}-${resolution.key}`"
                        class="w-full"
                      />
                    </label>
                  </div>
                </div>
              </div>
              <p class="mt-3 text-xs text-muted">
                {{ t(videoPricingI18nKey("modeHint")) }}
              </p>
              <div class="mt-2 rounded-lg bg-elevated/50 p-3 text-xs text-default">
                <div class="mb-1 font-medium">
                  {{ t(videoPricingI18nKey("finalPricePreview")) }}
                </div>
                <div class="grid grid-cols-3 gap-2">
                  <div
                    v-for="item in createVideoFinalPricePreview"
                    :key="item.label"
                  >
                    {{ item.label }}: {{ item.value }}
                  </div>
                </div>
              </div>
            </div>
            <!-- Grok Voice 定价 -->
            <div
              v-if="createForm.platform === 'grok'"
            >
              <h4 class="text-sm font-medium text-default mb-1">
                {{ t("admin.groups.voicePricing.title") }}
              </h4>
              <div class="grid grid-cols-1 gap-3 md:grid-cols-3">
                <UFormField
                  :label="t('admin.groups.voicePricing.audioRealtimePerMin')"
                  v-if="createForm.platform === 'grok'"
                >
                  <UInput
                    v-model.number="createForm.audio_realtime_price_per_min"
                    type="number"
                    step="0.000001"
                    min="0"
                    :placeholder="t('admin.groups.voicePricing.pricePlaceholder')"
                    data-testid="create-audio-realtime-price"
                    class="w-full"
                  />
                </UFormField>
                <UFormField
                  :label="t('admin.groups.voicePricing.audioTtsPerMillionChars')"
                  v-if="createForm.platform === 'grok'"
                >
                  <UInput
                    v-model.number="createForm.audio_tts_price_per_million_chars"
                    type="number"
                    step="0.000001"
                    min="0"
                    :placeholder="t('admin.groups.voicePricing.pricePlaceholder')"
                    data-testid="create-audio-tts-price"
                    class="w-full"
                  />
                </UFormField>
                <UFormField
                  :label="t('admin.groups.voicePricing.audioSttPerHour')"
                  v-if="createForm.platform === 'grok'"
                >
                  <UInput
                    v-model.number="createForm.audio_stt_price_per_hour"
                    type="number"
                    step="0.000001"
                    min="0"
                    :placeholder="t('admin.groups.voicePricing.pricePlaceholder')"
                    data-testid="create-audio-stt-price"
                    class="w-full"
                  />
                </UFormField>
              </div>
            </div>
            <!-- 搜索单价：所有可能走搜索的平台（Grok / DeepSeek） -->
            <div
              v-if="supportsSearchPricing(createForm.platform)"
            >
              <h4 class="text-sm font-medium text-default mb-1">
                {{ t("admin.groups.explicitPricing.title") }}
              </h4>
              <p class="text-xs text-muted mb-3">
                {{ t("admin.groups.explicitPricing.description") }}
              </p>
              <div class="grid grid-cols-1 gap-3 md:grid-cols-3">
                <UFormField :label="t('admin.groups.explicitPricing.searchPricePer1k')">
                  <UInput
                    v-model.number="createForm.search_price_per_1k"
                    type="number"
                    step="0.000001"
                    min="0"
                    :placeholder="t('admin.groups.explicitPricing.pricePlaceholder')"
                    data-testid="create-search-price"
                    class="w-full"
                  />
                </UFormField>
                <UFormField
                  :label="t('admin.groups.explicitPricing.searchProviderBaseUrl')"
                  v-if="createForm.platform === 'deepseek'"
                >
                  <UInput
                    v-model="createForm.search_provider_base_url"
                    type="text"
                    :placeholder="t('admin.groups.explicitPricing.searchProviderBaseUrlPlaceholder')"
                    data-testid="create-search-provider-url"
                    class="w-full"
                  />
                </UFormField>
                <UFormField
                  :label="t('admin.groups.explicitPricing.searchProviderApiKey')"
                  v-if="createForm.platform === 'deepseek'"
                >
                  <UInput
                    v-model="createForm.search_provider_api_key"
                    type="password"
                    autocomplete="new-password"
                    :placeholder="t('admin.groups.explicitPricing.searchProviderApiKeyPlaceholder')"
                    data-testid="create-search-provider-key"
                    class="w-full"
                  />
                </UFormField>
              </div>
            </div>
            <!-- Codex 网页搜索按次计费（仅 openai 平台） -->
            <div
              v-if="createForm.platform === 'openai'"
            >
              <h4 class="text-sm font-medium text-default mb-3">
                {{ t("admin.groups.webSearchPricing.title") }}
              </h4>
              <div class="space-y-2">
                <UFormField :label="t('admin.groups.webSearchPricing.pricePerCall')" :help="t('admin.groups.webSearchPricing.pricePerCallHint')">
                  <UInput
                    v-model.number="createForm.web_search_price_per_call"
                    type="number"
                    step="0.001"
                    min="0"
                    placeholder="0.01"
                    class="w-full"
                  />
                </UFormField>
                <div
                  class="mt-2 rounded-lg bg-elevated/50 p-3 text-xs text-default"
                >
                  {{
                    t("admin.groups.webSearchPricing.finalPricePreview", {
                      price: createWebSearchFinalPricePreview,
                    })
                  }}
                </div>
              </div>
            </div>
            <!-- Grok 安全拒绝单次扣费（放在模型白名单上方） -->
            <div
              v-if="createForm.platform === 'grok'"
            >
              <h4 class="mb-1 text-sm font-medium text-default">
                {{ t("admin.groups.moderationRefusalPricing.title") }}
              </h4>
              <p class="mb-3 text-xs text-muted">
                {{ t("admin.groups.moderationRefusalPricing.hint") }}
              </p>
              <div class="grid grid-cols-1 gap-3 md:grid-cols-3">
                <UFormField :label="t('admin.groups.moderationRefusalPricing.pricePerCall')">
                  <UInput
                    v-model.number="createForm.grok_moderation_refusal_price"
                    type="number"
                    step="0.00000001"
                    min="0"
                    :placeholder="t('admin.groups.moderationRefusalPricing.placeholder')"
                    data-testid="create-grok-moderation-refusal-price"
                    class="w-full"
                  />
                </UFormField>
              </div>
            </div>
          </section>
          <section class="space-y-5">
            <h3 class="text-base font-semibold text-highlighted">{{ t("admin.groups.formSections.dispatch") }}</h3>
            <!-- 跨协议转换开关：所有平台的分组都有（Anthropic 协议族 <-> OpenAI 协议族）。
                 账号有原生端点时始终先走原生转发，开关只决定"没有原生端点时"能否转换兜底。 -->
            <div>
              <UFormField
                :label="t('admin.groups.openaiMessages.crossProtocolEnabled')"
                :description="t('admin.groups.openaiMessages.crossProtocolHint')"
                class="flex items-center justify-between gap-4"
                :ui="{ container: 'mt-0 shrink-0' }"
              >
                <Toggle v-model="createForm.cross_protocol_conversion_enabled" />
              </UFormField>
            </div>
            <!-- OpenAI Messages 调度配置（OpenAI 与 Composite 平台） -->
            <div
              v-if="supportsMessagesDispatchPlatform(createForm.platform)"
            >
              <h4 class="text-sm font-medium text-default mb-3">
                {{ t("admin.groups.openaiMessages.title") }}
              </h4>

              <!-- 允许 Messages 调度开关 -->
              <UFormField
                :label="t('admin.groups.openaiMessages.allowDispatch')"
                :description="t('admin.groups.openaiMessages.allowDispatchHint')"
                class="flex items-center justify-between gap-4"
                :ui="{ container: 'mt-0 shrink-0' }"
              >
                <Toggle v-model="createForm.allow_messages_dispatch" />
              </UFormField>

              <div
                v-if="
                  createForm.platform === 'openai' &&
                  createForm.allow_messages_dispatch
                "
                class="mt-3"
              >
                <div
                  class="space-y-3"
                >
                  <div>
                    <div class="flex items-center gap-2">
                      <label
                        class="text-sm font-medium text-highlighted"
                        >{{
                          t("admin.groups.openaiMessages.familyMappingTitle")
                        }}</label
                      >
                    </div>
                    <p class="mt-1 text-xs text-muted">
                      {{ t("admin.groups.openaiMessages.familyMappingHint") }}
                    </p>
                  </div>
                  <div>
                    <div class="grid gap-4 md:grid-cols-3">
                      <UFormField :label="t('admin.groups.openaiMessages.opusModel')">
                        <UInput
                          v-model="createForm.opus_mapped_model"
                          type="text"
                          :placeholder=" t('admin.groups.openaiMessages.opusModelPlaceholder') "
                          class="w-full"
                        />
                      </UFormField>
                      <UFormField :label="t('admin.groups.openaiMessages.sonnetModel')">
                        <UInput
                          v-model="createForm.sonnet_mapped_model"
                          type="text"
                          :placeholder=" t('admin.groups.openaiMessages.sonnetModelPlaceholder') "
                          class="w-full"
                        />
                      </UFormField>
                      <UFormField :label="t('admin.groups.openaiMessages.haikuModel')">
                        <UInput
                          v-model="createForm.haiku_mapped_model"
                          type="text"
                          :placeholder=" t('admin.groups.openaiMessages.haikuModelPlaceholder') "
                          class="w-full"
                        />
                      </UFormField>
                    </div>
                  </div>
                </div>

                <div
                  class="mt-6 space-y-3"
                >
                  <div>
                    <div class="flex items-start justify-between gap-3">
                      <div>
                        <div class="flex items-center gap-2">
                          <label
                            class="text-sm font-medium text-highlighted"
                            >{{
                              t("admin.groups.openaiMessages.exactMappingTitle")
                            }}</label
                          >
                        </div>
                        <p
                          class="mt-1 text-xs text-muted"
                        >
                          {{ t("admin.groups.openaiMessages.exactMappingHint") }}
                        </p>
                      </div>
                    </div>
                  </div>

                  <div>
                    <div
                      v-if="createForm.exact_model_mappings.length === 0"
                      class="flex items-center justify-between gap-3 rounded-lg bg-elevated/50 px-4 py-3 text-sm text-muted"
                    >
                      <span>{{
                        t("admin.groups.openaiMessages.noExactMappings")
                      }}</span>
                      <UButton
                        @click="addCreateMessagesDispatchMapping"
                        icon="i-lucide-plus"
                        color="primary"
                        variant="ghost"
                        size="sm"
                      >{{ t("admin.groups.openaiMessages.addExactMapping") }}</UButton>
                    </div>

                    <div v-else class="space-y-3">
                      <div
                        v-for="row in createForm.exact_model_mappings"
                        :key="getCreateMessagesDispatchRowKey(row)"
                        class="group relative rounded-lg border border-default p-3"
                      >
                        <div class="flex items-center gap-4">
                          <div
                            class="grid flex-1 gap-4 md:grid-cols-[minmax(0,1fr)_auto_minmax(0,1fr)] md:items-start"
                          >
                            <UFormField :label="t('admin.groups.openaiMessages.claudeModel')">
                              <UInput
                                v-model="row.claude_model"
                                type="text"
                                :placeholder=" t( 'admin.groups.openaiMessages.claudeModelPlaceholder', ) "
                                class="w-full"
                              />
                            </UFormField>
                            <div
                              class="hidden md:flex md:justify-center md:pt-7 text-primary"
                            >
                              <Icon
                                name="arrowRight"
                                size="sm"
                                class="transition-transform group-hover:translate-x-1"
                              />
                            </div>
                            <UFormField :label="t('admin.groups.openaiMessages.targetModel')">
                              <UInput
                                v-model="row.target_model"
                                type="text"
                                :placeholder=" t( 'admin.groups.openaiMessages.targetModelPlaceholder', ) "
                                class="w-full"
                              />
                            </UFormField>
                          </div>
                          <UButton
                            @click="removeCreateMessagesDispatchMapping(row)"
                            :title=" t('admin.groups.openaiMessages.removeExactMapping') "
                            icon="i-lucide-trash-2"
                            color="error"
                            variant="ghost"
                            size="sm"
                            square
                            class="mt-6"
                          />
                        </div>
                      </div>

                      <UButton
                        @click="addCreateMessagesDispatchMapping"
                        icon="i-lucide-plus"
                        color="neutral"
                        variant="outline"
                        size="sm"
                        block
                      >{{ t("admin.groups.openaiMessages.addExactMapping") }}</UButton>
                    </div>
                  </div>
                </div>
              </div>
            </div>
            <!-- OpenAI Fast 开关（OpenAI 与 Composite 平台） -->
            <div
              v-if="supportsGroupOpenAIFast(createForm.platform)"
            >
              <h4 class="text-sm font-medium text-default mb-3">
                {{ t("admin.groups.openaiFast.title") }}
              </h4>
              <UFormField
                :label="t('admin.groups.openaiFast.force')"
                :description="t('admin.groups.openaiFast.hint')"
                class="flex items-center justify-between gap-4"
                :ui="{ container: 'mt-0 shrink-0' }"
              >
                <Toggle
                  data-testid="create-force-openai-fast"
                  :aria-label="t('admin.groups.openaiFast.force')"
                  v-model="createForm.force_openai_fast"
                />
              </UFormField>
              <UFormField
                :label="t('admin.groups.openaiFast.free')"
                :description="t('admin.groups.openaiFast.freeHint')"
                class="flex items-center justify-between gap-4 mt-4"
                :ui="{ container: 'mt-0 shrink-0' }"
              >
                <Toggle
                  data-testid="create-free-openai-fast"
                  :aria-label="t('admin.groups.openaiFast.free')"
                  v-model="createForm.free_openai_fast"
                />
              </UFormField>
            </div>
            <!-- Claude Code 客户端限制（仅 anthropic 平台） -->
            <div v-if="createForm.platform === 'anthropic'">
              <UFormField :description="createForm.claude_code_only
                      ? t('admin.groups.claudeCode.enabled')
                      : t('admin.groups.claudeCode.disabled')" class="flex items-center justify-between gap-4" :ui="{ container: 'mt-0 shrink-0' }">
                <template #label>
                  <span class="inline-flex items-center gap-1">{{ t("admin.groups.claudeCode.title") }} <GroupFieldHelp :label="t('admin.groups.claudeCode.title')" :text="t('admin.groups.claudeCode.tooltip')" /></span>
                </template>
                <Toggle v-model="createForm.claude_code_only" />
              </UFormField>
              <!-- 降级分组选择（仅当启用 claude_code_only 时显示） -->
              <UFormField
                :label="t('admin.groups.claudeCode.fallbackGroup')"
                :help="t('admin.groups.claudeCode.fallbackHint')"
                v-if="createForm.claude_code_only"
                class="mt-3"
              >
                <Select
                  v-model="createForm.fallback_group_id"
                  :options="fallbackGroupOptions"
                  :placeholder="t('admin.groups.claudeCode.noFallback')"
                />
              </UFormField>
            </div>
          </section>
          <UCollapsible v-model:open="createAdvancedOpen" :unmount-on-hide="false" class="space-y-5">
            <UButton
              color="neutral"
              variant="ghost"
              trailing-icon="i-lucide-chevron-down"
              :label="t('admin.groups.formSections.advanced')"
              class="-mx-2.5 text-base font-semibold text-highlighted"
              :ui="{ trailingIcon: 'transition-transform duration-200 group-data-[state=open]:rotate-180' }"
            />
            <template #content>
              <div class="space-y-5 pt-1">
                <div>
                  <UFormField
                    :label="t('admin.groups.returnRequestedModel.label')"
                    class="flex items-center justify-between gap-4"
                    :ui="{ container: 'mt-0 shrink-0' }"
                  >
                    <template #description><span id="create-return-requested-model-hint">{{ t("admin.groups.returnRequestedModel.hint") }}</span></template>
                    <Toggle
                      data-testid="create-return-requested-model"
                      :aria-label="t('admin.groups.returnRequestedModel.label')"
                      aria-describedby="create-return-requested-model-hint"
                      v-model="createForm.return_requested_model"
                    />
                  </UFormField>
                </div>
                <!-- Codex Live 开关（OpenAI 与 Composite 平台） -->
                <div
                  v-if="supportsLivePlatform(createForm.platform)"
                >
                  <h4 class="text-sm font-medium text-default mb-3">
                    {{ t("admin.groups.openaiLive.title") }}
                  </h4>
                  <UFormField
                    :label="t('admin.groups.openaiLive.allow')"
                    :description="t('admin.groups.openaiLive.hint')"
                    class="flex items-center justify-between gap-4"
                    :ui="{ container: 'mt-0 shrink-0' }"
                  >
                    <Toggle
                      :model-value="createForm.allow_live"
                      @update:model-value="toggleLive('create')"
                    />
                  </UFormField>
                </div>
                <!-- 账号过滤控制 (OpenAI/Antigravity/Anthropic/Gemini) -->
                <div
                  v-if="
                    ['openai', 'antigravity', 'anthropic', 'gemini'].includes(
                      createForm.platform,
                    )
                  "
                >
                  <h4 class="text-sm font-medium text-default mb-3">
                    {{ t("admin.groups.accountFilters.title") }}
                  </h4>

                  <!-- require_oauth_only toggle -->
                  <UFormField
                    :label="t('admin.groups.accountFilters.oauthOnly')"
                    :description="createForm.require_oauth_only
                            ? t('admin.groups.accountFilters.oauthOnlyEnabled')
                            : t('admin.groups.accountFilters.disabled')"
                    class="flex items-center justify-between gap-4"
                    :ui="{ container: 'mt-0 shrink-0' }"
                  >
                    <Toggle v-model="createForm.require_oauth_only" />
                  </UFormField>

                  <!-- require_privacy_set toggle -->
                  <UFormField
                    :label="t('admin.groups.accountFilters.privacySetOnly')"
                    :description="createForm.require_privacy_set
                            ? t('admin.groups.accountFilters.privacySetOnlyEnabled')
                            : t('admin.groups.accountFilters.disabled')"
                    class="flex items-center justify-between gap-4"
                    :ui="{ container: 'mt-0 shrink-0' }"
                  >
                    <Toggle v-model="createForm.require_privacy_set" />
                  </UFormField>
                </div>
                <!-- 无效请求兜底（仅 anthropic/antigravity 平台，且非订阅分组） -->
                <div
                  v-if="
                    ['anthropic', 'antigravity'].includes(createForm.platform) &&
                    createForm.subscription_type !== 'subscription'
                  "
                >
                  <UFormField :label="t('admin.groups.invalidRequestFallback.title')" :help="t('admin.groups.invalidRequestFallback.hint')">
                    <Select
                      v-model="createForm.fallback_group_id_on_invalid_request"
                      :options="invalidRequestFallbackOptions"
                      :placeholder="t('admin.groups.invalidRequestFallback.noFallback')"
                    />
                  </UFormField>
                </div>
                <!-- 模型路由配置（仅 anthropic 平台） -->
                <div v-if="createForm.platform === 'anthropic'">
                  <UFormField :description="createForm.model_routing_enabled
                          ? t('admin.groups.modelRouting.enabled')
                          : t('admin.groups.modelRouting.disabled')" class="flex items-center justify-between gap-4" :ui="{ container: 'mt-0 shrink-0' }">
                    <template #label>
                      <span class="inline-flex items-center gap-1">{{ t("admin.groups.modelRouting.title") }} <GroupFieldHelp :label="t('admin.groups.modelRouting.title')" :text="t('admin.groups.modelRouting.tooltip')" /></span>
                    </template>
                    <Toggle v-model="createForm.model_routing_enabled" />
                  </UFormField>
                  <p
                    v-if="!createForm.model_routing_enabled"
                    class="text-xs text-muted mb-3"
                  >
                    {{ t("admin.groups.modelRouting.disabledHint") }}
                  </p>
                  <p v-else class="text-xs text-muted mb-3">
                    {{ t("admin.groups.modelRouting.noRulesHint") }}
                  </p>
                  <!-- 路由规则列表（仅在启用时显示） -->
                  <div v-if="createForm.model_routing_enabled" class="space-y-3">
                    <div
                      v-for="rule in createModelRoutingRules"
                      :key="getCreateRuleRenderKey(rule)"
                      class="rounded-lg border border-default p-3"
                    >
                      <div class="flex items-start gap-3">
                        <div class="flex-1 space-y-2">
                          <UFormField :label="t('admin.groups.modelRouting.modelPattern')" size="sm">
                            <UInput
                              v-model="rule.pattern"
                              type="text"
                              :placeholder=" t('admin.groups.modelRouting.modelPatternPlaceholder') "
                              class="w-full"
                            />
                          </UFormField>
                          <div>
                            <label class="mb-1 block text-xs font-medium text-default">{{
                              t("admin.groups.modelRouting.accounts")
                            }}</label>
                            <!-- 已选账号标签 -->
                            <div
                              v-if="rule.accounts.length > 0"
                              class="flex flex-wrap gap-1.5 mb-2"
                            >
                              <span
                                v-for="account in rule.accounts"
                                :key="account.id"
                                class="inline-flex items-center gap-1 rounded-full bg-primary/10 px-2.5 py-1 text-xs font-medium text-primary"
                              >
                                {{ account.name }}
                                <UButton
                                  @click="removeSelectedAccount(rule, account.id)"
                                  icon="i-lucide-x"
                                  color="primary"
                                  variant="ghost"
                                  size="sm"
                                  square
                                  class="ml-0.5"
                                />
                              </span>
                            </div>
                            <!-- 账号搜索输入框 -->
                            <div class="relative account-search-container">
                              <UInput
                                v-model=" accountSearchKeyword[getCreateRuleSearchKey(rule)] "
                                type="text"
                                :placeholder=" t( 'admin.groups.modelRouting.searchAccountPlaceholder', ) "
                                @input="searchAccountsByRule(rule)"
                                @focus="onAccountSearchFocus(rule)"
                                class="w-full"
                              />
                              <!-- 搜索结果下拉框 -->
                              <div
                                v-if="
                                  showAccountDropdown[getCreateRuleSearchKey(rule)] &&
                                  accountSearchResults[getCreateRuleSearchKey(rule)]
                                    ?.length > 0
                                "
                                class="absolute z-50 mt-1 max-h-48 w-full overflow-auto rounded-lg border border-default bg-default shadow-lg"
                              >
                                <UButton
                                  v-for="account in accountSearchResults[ getCreateRuleSearchKey(rule) ]"
                                  :key="account.id"
                                  @click="selectAccount(rule, account)"
                                  :class="{ 'opacity-50': rule.accounts.some( (a) => a.id === account.id, ), }"
                                  :disabled=" rule.accounts.some((a) => a.id === account.id) "
                                  color="neutral"
                                  variant="ghost"
                                  size="sm"
                                  block
                                  class="justify-start"
                                ><span>{{ account.name }}</span>
                                  <span class="ml-2 text-xs text-dimmed"
                                    >#{{ account.id }}</span
                                  ></UButton>
                              </div>
                            </div>
                            <p class="text-xs text-dimmed mt-1">
                              {{ t("admin.groups.modelRouting.accountsHint") }}
                            </p>
                          </div>
                        </div>
                        <UButton
                          @click="removeCreateRoutingRule(rule)"
                          :title="t('admin.groups.modelRouting.removeRule')"
                          icon="i-lucide-trash-2"
                          color="error"
                          variant="ghost"
                          size="sm"
                          square
                          class="mt-5"
                        />
                      </div>
                    </div>
                  </div>
                  <!-- 添加规则按钮（仅在启用时显示） -->
                  <UButton
                    v-if="createForm.model_routing_enabled"
                    @click="addCreateRoutingRule"
                    icon="i-lucide-plus"
                    color="primary"
                    variant="ghost"
                    size="sm"
                    class="mt-3"
                  >{{ t("admin.groups.modelRouting.addRule") }}</UButton>
                </div>
                <!-- MCP XML 协议注入（仅 antigravity 平台） -->
                <div v-if="createForm.platform === 'antigravity'">
                  <UFormField :description="createForm.mcp_xml_inject
                          ? t('admin.groups.mcpXml.enabled')
                          : t('admin.groups.mcpXml.disabled')" class="flex items-center justify-between gap-4" :ui="{ container: 'mt-0 shrink-0' }">
                    <template #label>
                      <span class="inline-flex items-center gap-1">{{ t("admin.groups.mcpXml.title") }} <GroupFieldHelp :label="t('admin.groups.mcpXml.title')" :text="t('admin.groups.mcpXml.tooltip')" /></span>
                    </template>
                    <Toggle v-model="createForm.mcp_xml_inject" />
                  </UFormField>
                </div>
              </div>
            </template>
          </UCollapsible>
        </template>
      </form>

      <template #footer>
        <div class="flex justify-end gap-2">
          <UButton @click="closeCreateModal" color="neutral" variant="subtle">{{ t("common.cancel") }}</UButton>
          <UButton
            type="submit"
            form="create-group-form"
            :disabled="submitting"
            :loading="submitting"
            data-tour="group-form-submit"
          >
            {{ submitting ? t("admin.groups.creating") : t("common.create") }}
          </UButton>
        </div>
      </template>
    </BaseDialog>

    <!-- Edit Group Modal -->
    <BaseDialog
      :show="showEditModal"
      :title="t('admin.groups.editGroup')"
      width="wide"
      @close="closeEditModal"
    >
      <form
        v-if="editingGroup"
        id="edit-group-form"
        @submit.prevent="handleUpdateGroup"
        class="space-y-8"
      >
        <section class="space-y-5">
          <h3 class="text-base font-semibold text-highlighted">{{ t("admin.groups.formSections.basic") }}</h3>
          <UFormField :label="t('admin.groups.form.name')" required>
            <UInput
              v-model="editForm.name"
              type="text"
              required
              data-tour="edit-group-form-name"
              class="w-full"
            />
          </UFormField>
          <UFormField :label="t('admin.groups.form.description')">
            <UTextarea v-model="editForm.description" :rows="3" class="w-full" />
          </UFormField>
          <UFormField :label="t('admin.groups.form.platform')" :help="t('admin.groups.platformNotEditable')">
            <Select
              v-model="editForm.platform"
              :options="platformOptions"
              :disabled="true"
              data-tour="group-form-platform"
            />
          </UFormField>
          <template v-if="!authStore.isSimpleMode">
            <!-- 从分组复制账号（编辑时，多选，账号自动去重） -->
            <UFormField
              v-if="copyAccountsGroupOptionsForEdit.length > 0"
              :help="t('admin.groups.copyAccounts.hintEdit')"
            >
              <template #label>
                <span class="inline-flex items-center gap-1">{{ t("admin.groups.copyAccounts.title") }} <GroupFieldHelp :label="t('admin.groups.copyAccounts.title')" :text="t('admin.groups.copyAccounts.tooltipEdit')" /></span>
              </template>
              <USelectMenu
                v-model="editForm.copy_accounts_from_group_ids"
                :items="copyAccountsGroupOptionsForEdit"
                value-key="value"
                label-key="label"
                multiple
                :placeholder="t('admin.groups.copyAccounts.selectPlaceholder')"
                class="w-full"
              />
            </UFormField>
            <UFormField :label="t('admin.groups.form.status')">
              <Select v-model="editForm.status" :options="editStatusOptions" />
            </UFormField>
          </template>
        </section>
        <template v-if="!authStore.isSimpleMode">
          <section class="space-y-5">
            <h3 class="text-base font-semibold text-highlighted">{{ t("admin.groups.formSections.billing") }}</h3>
            <UFormField :label="t('admin.groups.form.rateMultiplier')" required>
              <UInput
                v-model.number="editForm.rate_multiplier"
                type="number"
                step="0.001"
                min="0.001"
                required
                data-tour="group-form-multiplier"
                class="w-full"
              />
            </UFormField>
            <UFormField :label="t('admin.groups.form.rpmLimit')" :help="t('admin.groups.form.rpmLimitHint')">
              <UInput
                v-model.number="editForm.rpm_limit"
                type="number"
                min="0"
                step="1"
                :placeholder="t('admin.groups.form.rpmLimitPlaceholder')"
                class="w-full"
              />
            </UFormField>
            <div v-if="editForm.subscription_type !== 'subscription'">
              <UFormField :description="editForm.is_exclusive
                      ? t('admin.groups.exclusive')
                      : t('admin.groups.public')" class="flex items-center justify-between gap-4" :ui="{ container: 'mt-0 shrink-0' }">
                <template #label>
                  <span class="inline-flex items-center gap-1">{{ t("admin.groups.form.exclusive") }} <GroupFieldHelp :label="t('admin.groups.form.exclusive')" :title="t('admin.groups.exclusiveTooltip.title')" :text="t('admin.groups.exclusiveTooltip.description')">
                      <p class="text-muted"><span class="font-medium text-default">{{ t("admin.groups.exclusiveTooltip.example") }}</span> {{ t("admin.groups.exclusiveTooltip.exampleContent") }}</p>
                    </GroupFieldHelp></span>
                </template>
                <Toggle v-model="editForm.is_exclusive" />
              </UFormField>
            </div>
            <!-- Subscription Configuration -->
            <div>
              <UFormField
                :label="t('admin.groups.subscription.type')"
                :help="t('admin.groups.subscription.typeNotEditable')"
              >
                <Select
                  v-model="editForm.subscription_type"
                  :options="subscriptionTypeOptions"
                  :disabled="true"
                />
              </UFormField>

              <!-- Subscription limits (only show when subscription type is selected) -->
              <div
                v-if="editForm.subscription_type === 'subscription'"
                class="mt-4 grid grid-cols-1 gap-4 sm:grid-cols-3"
              >
                <UFormField :label="t('admin.groups.subscription.dailyLimit')">
                  <UInput
                    v-model.number="editForm.daily_limit_usd"
                    type="number"
                    step="0.01"
                    min="0"
                    :placeholder="t('admin.groups.subscription.noLimit')"
                    class="w-full"
                  />
                </UFormField>
                <UFormField :label="t('admin.groups.subscription.weeklyLimit')">
                  <UInput
                    v-model.number="editForm.weekly_limit_usd"
                    type="number"
                    step="0.01"
                    min="0"
                    :placeholder="t('admin.groups.subscription.noLimit')"
                    class="w-full"
                  />
                </UFormField>
                <UFormField :label="t('admin.groups.subscription.monthlyLimit')">
                  <UInput
                    v-model.number="editForm.monthly_limit_usd"
                    type="number"
                    step="0.01"
                    min="0"
                    :placeholder="t('admin.groups.subscription.noLimit')"
                    class="w-full"
                  />
                </UFormField>
              </div>
            </div>
            <!-- 高峰时段倍率配置（仅订阅类型分组） -->
            <div v-if="editForm.subscription_type === 'subscription'">
              <div class="mb-4 grid grid-cols-1 gap-3 md:grid-cols-2">
                <UCheckbox v-model="editForm.peak_rate_enabled" :label="t('admin.groups.peakRate.enable')" />
              </div>
              <div
                v-if="editForm.peak_rate_enabled"
                class="mb-4 grid grid-cols-1 gap-3 sm:grid-cols-3"
              >
                <UFormField :label="t('admin.groups.peakRate.peakStart')">
                  <TimeWheelPicker v-model="editForm.peak_start" />
                </UFormField>
                <UFormField :label="t('admin.groups.peakRate.peakEnd')">
                  <TimeWheelPicker v-model="editForm.peak_end" />
                </UFormField>
                <UFormField :label="t('admin.groups.peakRate.peakMultiplier')">
                  <UInput
                    v-model.number="editForm.peak_rate_multiplier"
                    type="number"
                    step="0.001"
                    min="0"
                    placeholder="1"
                    :title="t('admin.groups.peakRate.multiplierHint')"
                    class="w-full"
                  />
                </UFormField>
              </div>
            </div>
            <!-- 分组利润控制（五个平台 token 请求） -->
            <div v-if="isProfitControlPlatform(editForm.platform)">
              <UCheckbox
                v-model="editForm.profit_control_enabled"
                :label="t('admin.groups.profitControl.enable')"
              />
              <p class="mb-3 mt-1.5 text-xs text-muted">
                {{
                  editForm.profit_control_enabled
                    ? t("admin.groups.profitControl.enabledHint")
                    : t("admin.groups.profitControl.disabledHint")
                }}
              </p>
              <div
                v-if="editForm.profit_control_enabled"
                class="mb-3 grid grid-cols-1 gap-3 sm:grid-cols-2"
              >
                <UFormField :label="t('admin.groups.profitControl.minMargin')">
                  <UInput
                    v-model.number="editForm.profit_min_margin_percent"
                    type="number"
                    step="0.1"
                    min="0"
                    max="99.99"
                    placeholder="0"
                    :title="t('admin.groups.profitControl.minMarginHint')"
                    class="w-full"
                  />
                </UFormField>
                <UFormField :label="t('admin.groups.profitControl.safetyBuffer')">
                  <UInput
                    v-model.number="editForm.profit_safety_buffer_percent"
                    type="number"
                    step="0.1"
                    min="0"
                    max="99.99"
                    placeholder="0"
                    :title="t('admin.groups.profitControl.safetyBufferHint')"
                    class="w-full"
                  />
                </UFormField>
              </div>
            </div>
          </section>
          <section class="space-y-5">
            <h3 class="text-base font-semibold text-highlighted">{{ t("admin.groups.formSections.models") }}</h3>
            <div>
              <UFormField
                :label="t('admin.groups.modelAllowlist.title')"
                :description="t('admin.groups.modelAllowlist.hint')"
                class="flex items-center justify-between gap-4 mb-3"
                :ui="{ container: 'mt-0 shrink-0' }"
              >
                <Toggle v-model="editModelAllowlistState.enabled" />
              </UFormField>
              <div
                v-if="editModelAllowlistState.enabled"
                class="overflow-hidden rounded-lg border border-default"
              >
                <div
                  v-if="!editModelAllowlistLoading && editModelAllowlistState.items.length > 0"
                  class="flex items-center justify-between gap-2 border-b border-default px-3 py-1.5 text-xs"
                >
                  <span class="text-muted">
                    {{
                      t("admin.groups.modelAllowlist.selectedSummary", {
                        selected: editModelAllowlistSelectedCount,
                        total: editModelAllowlistState.items.length,
                      })
                    }}
                  </span>
                  <div class="flex items-center gap-1.5">
                    <UButton
                      @click="selectAllModelAllowlistItems(editModelAllowlistState)"
                      color="primary"
                      variant="ghost"
                      size="sm"
                    >{{ t("admin.groups.modelAllowlist.selectAll") }}</UButton>
                    <UButton
                      @click="invertModelAllowlistSelection(editModelAllowlistState)"
                      color="neutral"
                      variant="ghost"
                      size="sm"
                    >{{ t("admin.groups.modelAllowlist.invertSelection") }}</UButton>
                  </div>
                </div>
                <div
                  class="max-h-64 space-y-2 overflow-y-auto p-2"
                >
                  <p v-if="editModelAllowlistLoading" class="text-xs text-muted">
                    {{ t("admin.groups.modelAllowlist.loading") }}
                  </p>
                  <p
                    v-else-if="editModelAllowlistState.items.length === 0"
                    class="text-xs text-muted"
                  >
                    {{ t("admin.groups.modelAllowlist.empty") }}
                  </p>
                  <div
                    v-for="(item, index) in editModelAllowlistState.items"
                    :key="item.id"
                    class="flex items-center gap-2 rounded-md px-2 py-1.5 hover:bg-elevated/50"
                  >
                    <UCheckbox v-model="item.selected" />
                    <span class="min-w-0 flex-1 break-all text-sm text-default">
                      {{ item.id }}
                      <span
                        v-if="item.id.endsWith('*')"
                        class="ml-1 rounded-sm bg-primary/10 px-1.5 py-0.5 text-[10px] font-medium text-primary"
                      >
                        {{ t("admin.groups.modelAllowlist.wildcardTag") }}
                      </span>
                    </span>
                    <UButton
                      :disabled="index === 0"
                      @click="moveEditModelAllowlistItem(index, index - 1)"
                      icon="i-lucide-arrow-up"
                      color="neutral"
                      variant="ghost"
                      size="sm"
                      square
                    />
                    <UButton
                      :disabled="index === editModelAllowlistState.items.length - 1"
                      @click="moveEditModelAllowlistItem(index, index + 1)"
                      icon="i-lucide-arrow-down"
                      color="neutral"
                      variant="ghost"
                      size="sm"
                      square
                    />
                  </div>
                </div>
                <div class="border-t border-default px-3 py-2">
                  <div class="flex items-center gap-2">
                    <UInput
                      v-model="editAllowlistCustomEntry"
                      type="text"
                      :placeholder="t('admin.groups.modelAllowlist.customPlaceholder')"
                      @keydown.enter.prevent="submitEditAllowlistCustomEntry"
                      size="sm"
                      class="min-w-0 flex-1"
                    />
                    <UButton @click="submitEditAllowlistCustomEntry" color="primary" variant="solid" size="sm">{{ t("admin.groups.modelAllowlist.addCustom") }}</UButton>
                  </div>
                  <p
                    v-if="editAllowlistCustomErrorKey"
                    class="mt-1 text-xs text-error"
                  >
                    {{ t(editAllowlistCustomErrorKey) }}
                  </p>
                </div>
              </div>
            </div>
            <ReasoningEffortPolicyFields
              v-if="supportsReasoningEffortPolicyPlatform(editForm.platform)"
              ref="editReasoningEffortPolicyRef"
              id-prefix="edit-group-reasoning"
              :platform="editForm.platform"
              v-model:max-effort="editForm.max_reasoning_effort"
              v-model:over-limit="editForm.max_reasoning_effort_over_limit"
              v-model:mappings="editForm.reasoning_effort_mappings"
            />
            <!-- 支持的模型系列（仅 antigravity 平台） -->
            <div v-if="editForm.platform === 'antigravity'">
              <div class="mb-1.5 flex items-center gap-1">
                <label class="text-sm font-medium text-default">
                  {{ t("admin.groups.supportedScopes.title") }}
                </label>
                <!-- Help Tooltip -->
                <GroupFieldHelp :label="t('admin.groups.supportedScopes.title')" :text="t('admin.groups.supportedScopes.tooltip')" />
              </div>
              <div class="space-y-2">
                <UCheckbox
                  :model-value="editForm.supported_model_scopes.includes('claude')"
                  @update:model-value="toggleEditScope('claude')"
                  :label="t('admin.groups.supportedScopes.claude')"
                />
                <UCheckbox
                  :model-value="editForm.supported_model_scopes.includes('gemini_text')"
                  @update:model-value="toggleEditScope('gemini_text')"
                  :label="t('admin.groups.supportedScopes.geminiText')"
                />
                <UCheckbox
                  :model-value="editForm.supported_model_scopes.includes('gemini_image')"
                  @update:model-value="toggleEditScope('gemini_image')"
                  :label="t('admin.groups.supportedScopes.geminiImage')"
                />
              </div>
              <p class="mt-2 text-xs text-muted">
                {{ t("admin.groups.supportedScopes.hint") }}
              </p>
            </div>
            <!-- 固定账号获取 Codex Model Manifest（仅 openai 平台，仅编辑对话框） -->
            <CodexManifestAccountsField
              v-if="editForm.platform === 'openai' && editingGroup"
              ref="editCodexManifestRef"
              :group-id="editingGroup.id"
              :model-value="editCodexManifestConfig"
              @update:model-value="Object.assign(editCodexManifestConfig, $event)"
              :account-names="editCodexManifestAccountNames"
            />
          </section>
          <section class="space-y-5">
            <h3 class="text-base font-semibold text-highlighted">{{ t("admin.groups.formSections.pricing") }}</h3>
            <div>
              <div class="flex flex-wrap items-start justify-between gap-3">
                <div class="min-w-0 flex-1">
                  <h4 class="text-sm font-medium text-default">{{ t("admin.groups.modelPricing.title") }}</h4>
                  <p class="mt-1 text-xs text-muted">{{ t("admin.groups.modelPricing.description") }}</p>
                </div>
                <UButton
                  @click="addGroupPricing(editForm.model_pricing)"
                  icon="i-lucide-plus"
                  color="neutral"
                  variant="outline"
                  class="shrink-0 whitespace-nowrap"
                >{{ t("admin.groups.modelPricing.add") }}</UButton>
              </div>
              <UCheckbox class="mt-3" v-model="editForm.long_context_pricing_enabled">
                <template #label>
                  <span><span class="block text-sm text-default">{{ t("admin.groups.modelPricing.longContext") }}</span><span class="block text-xs text-muted">{{ t("admin.groups.modelPricing.longContextHint") }}</span></span>
                </template>
              </UCheckbox>
              <div class="mt-3 space-y-2">
                <PricingEntryCard v-for="(entry, index) in editForm.model_pricing" :key="index" :entry="entry" :platform="editForm.platform" hide-token-intervals @update="editForm.model_pricing[index] = $event" @remove="editForm.model_pricing.splice(index, 1)" />
              </div>
            </div>
            <!-- 图片生成计费配置 -->
            <div
              v-if="supportsImagePricingPlatform(editForm.platform)"
            >
              <label
                class="block mb-2 font-medium text-default"
              >
                {{ t(imagePricingI18nKey(editForm.platform, "title")) }}
              </label>
              <p class="text-xs text-muted mb-3">
                {{ t(imagePricingI18nKey(editForm.platform, "description")) }}
              </p>
              <div class="mb-4 grid grid-cols-1 gap-3 md:grid-cols-2">
                <UCheckbox
                  v-model="editForm.allow_image_generation"
                  :label="t(imagePricingI18nKey(editForm.platform, 'allowImageGeneration'))"
                />
                <UCheckbox
                  v-model="editForm.image_rate_independent"
                  :label="t(imagePricingI18nKey(editForm.platform, 'independentMultiplier'))"
                />
              </div>
              <UFormField
                :label="t(imagePricingI18nKey(editForm.platform, 'imageMultiplier'))"
                v-if="editForm.image_rate_independent"
                class="mb-4"
              >
                <UInput
                  v-model.number="editForm.image_rate_multiplier"
                  type="number"
                  step="0.0001"
                  min="0"
                  placeholder="1"
                  class="w-full"
                />
              </UFormField>
              <div class="grid grid-cols-3 gap-3">
                <UFormField label="1K ($)">
                  <UInput
                    v-model.number="editForm.image_price_1k"
                    type="number"
                    step="0.001"
                    min="0"
                    :placeholder="getImagePricePlaceholder(editForm.platform, 'image_price_1k')"
                    class="w-full"
                  />
                </UFormField>
                <UFormField label="2K ($)">
                  <UInput
                    v-model.number="editForm.image_price_2k"
                    type="number"
                    step="0.001"
                    min="0"
                    :placeholder="getImagePricePlaceholder(editForm.platform, 'image_price_2k')"
                    class="w-full"
                  />
                </UFormField>
                <UFormField label="4K ($)">
                  <UInput
                    v-model.number="editForm.image_price_4k"
                    type="number"
                    step="0.001"
                    min="0"
                    :placeholder="getImagePricePlaceholder(editForm.platform, 'image_price_4k')"
                    class="w-full"
                  />
                </UFormField>
              </div>
              <p class="mt-3 text-xs text-muted">
                {{ t(imagePricingI18nKey(editForm.platform, "modeHint")) }}
              </p>
              <div class="mt-2 rounded-lg bg-elevated/50 p-3 text-xs text-default">
                <div class="mb-1 font-medium">
                  {{ t(imagePricingI18nKey(editForm.platform, "finalPricePreview")) }}
                </div>
                <div class="grid grid-cols-3 gap-2">
                  <div
                    v-for="item in editImageFinalPricePreview"
                    :key="item.label"
                  >
                    {{ item.label }}: {{ item.value }}
                  </div>
                </div>
              </div>
              <div v-if="editForm.platform === 'gemini' && editForm.allow_image_generation" class="mt-4">
                <UCheckbox
                  v-model="editForm.allow_batch_image_generation"
                  :label="t('admin.groups.imagePricing.allowBatchImageGeneration')"
                />
                <p class="mt-2 text-xs text-muted">
                  {{ t("admin.groups.imagePricing.batchSectionHint") }}
                </p>
                <div
                  v-if="editForm.allow_batch_image_generation"
                  class="mt-3 grid grid-cols-1 gap-3 md:grid-cols-2"
                >
                  <UFormField :label="t('admin.groups.imagePricing.batchDiscountMultiplier')">
                    <UInput
                      v-model.number="editForm.batch_image_discount_multiplier"
                      type="number"
                      step="0.0001"
                      min="0"
                      placeholder="0.5"
                      class="w-full"
                    />
                  </UFormField>
                  <UFormField :label="t('admin.groups.imagePricing.batchHoldMultiplier')">
                    <UInput
                      v-model.number="editForm.batch_image_hold_multiplier"
                      type="number"
                      step="0.0001"
                      min="0"
                      placeholder="0.6"
                      class="w-full"
                    />
                  </UFormField>
                </div>
              </div>
              <p
                v-else-if="editForm.platform !== 'gemini'"
                class="mt-4 text-xs text-muted"
              >
                {{ t("admin.groups.imagePricing.batchGeminiOnlyHint") }}
              </p>
            </div>
            <MiniMaxH3PricingFields v-if="editForm.platform === 'minimax'" v-model="editForm.video_model_prices" />
            <!-- 视频生成计费配置（仅 Grok 平台） -->
            <div
              v-if="supportsVideoPricingPlatform(editForm.platform)"
            >
              <label
                class="block mb-2 font-medium text-default"
              >
                {{ t(videoPricingI18nKey("title")) }}
              </label>
              <p class="text-xs text-muted mb-3">
                {{ t(videoPricingI18nKey("description")) }}
              </p>
              <div class="mb-4">
                <UCheckbox
                  v-model="editForm.video_rate_independent"
                  :label="t(videoPricingI18nKey('independentMultiplier'))"
                />
              </div>
              <UFormField
                :label="t(videoPricingI18nKey('videoMultiplier'))"
                v-if="editForm.video_rate_independent"
                class="mb-4"
              >
                <UInput
                  v-model.number="editForm.video_rate_multiplier"
                  type="number"
                  step="0.0001"
                  min="0"
                  placeholder="1"
                  class="w-full"
                />
              </UFormField>
              <div class="grid grid-cols-3 gap-3">
                <UFormField label="480p ($/s)">
                  <UInput
                    v-model.number="editForm.video_price_480p"
                    type="number"
                    step="0.001"
                    min="0"
                    :placeholder="getVideoPricePlaceholder(editForm.platform, 'video_price_480p')"
                    class="w-full"
                  />
                </UFormField>
                <UFormField label="720p ($/s)">
                  <UInput
                    v-model.number="editForm.video_price_720p"
                    type="number"
                    step="0.001"
                    min="0"
                    :placeholder="getVideoPricePlaceholder(editForm.platform, 'video_price_720p')"
                    class="w-full"
                  />
                </UFormField>
                <UFormField label="1080p ($/s)">
                  <UInput
                    v-model.number="editForm.video_price_1080p"
                    type="number"
                    step="0.001"
                    min="0"
                    :placeholder="getVideoPricePlaceholder(editForm.platform, 'video_price_1080p')"
                    class="w-full"
                  />
                </UFormField>
              </div>
              <div
                class="mt-4"
                data-testid="edit-grok-video-model-prices"
              >
                <p class="text-sm font-medium text-default">
                  {{ t("admin.groups.videoPricing.modelOverridesTitle") }}
                </p>
                <p class="mt-1 text-xs text-muted">
                  {{ t("admin.groups.videoPricing.modelOverridesDescription") }}
                </p>
                <div class="mt-3 space-y-3">
                  <div
                    v-for="family in videoModelPriceFamilyRows(editForm.video_model_prices)"
                    :key="family.key"
                    class="grid gap-2 sm:grid-cols-[minmax(0,1fr)_repeat(3,minmax(0,7rem))] sm:items-end"
                  >
                    <div class="min-w-0 pb-1 font-mono text-xs text-default">
                      {{ family.label }}
                    </div>
                    <label
                      v-for="resolution in grokVideoPriceResolutions"
                      :key="resolution.key"
                      class="block"
                    >
                      <span class="mb-1 block text-xs text-muted">
                        {{ resolution.label }} ($/s)
                      </span>
                      <UInput
                        v-model.number.nullable="editForm.video_model_prices[family.key][resolution.key]"
                        type="number"
                        step="0.001"
                        min="0"
                        :data-testid="`edit-grok-video-price-${family.key}-${resolution.key}`"
                        class="w-full"
                      />
                    </label>
                  </div>
                </div>
              </div>
              <p class="mt-3 text-xs text-muted">
                {{ t(videoPricingI18nKey("modeHint")) }}
              </p>
              <div class="mt-2 rounded-lg bg-elevated/50 p-3 text-xs text-default">
                <div class="mb-1 font-medium">
                  {{ t(videoPricingI18nKey("finalPricePreview")) }}
                </div>
                <div class="grid grid-cols-3 gap-2">
                  <div
                    v-for="item in editVideoFinalPricePreview"
                    :key="item.label"
                  >
                    {{ item.label }}: {{ item.value }}
                  </div>
                </div>
              </div>
            </div>
            <!-- Grok Voice 定价 -->
            <div
              v-if="editForm.platform === 'grok'"
            >
              <h4 class="text-sm font-medium text-default mb-1">
                {{ t("admin.groups.voicePricing.title") }}
              </h4>
              <div class="grid grid-cols-1 gap-3 md:grid-cols-3">
                <UFormField
                  :label="t('admin.groups.voicePricing.audioRealtimePerMin')"
                  v-if="editForm.platform === 'grok'"
                >
                  <UInput
                    v-model.number="editForm.audio_realtime_price_per_min"
                    type="number"
                    step="0.000001"
                    min="0"
                    :placeholder="t('admin.groups.voicePricing.pricePlaceholder')"
                    data-testid="edit-audio-realtime-price"
                    class="w-full"
                  />
                </UFormField>
                <UFormField
                  :label="t('admin.groups.voicePricing.audioTtsPerMillionChars')"
                  v-if="editForm.platform === 'grok'"
                >
                  <UInput
                    v-model.number="editForm.audio_tts_price_per_million_chars"
                    type="number"
                    step="0.000001"
                    min="0"
                    :placeholder="t('admin.groups.voicePricing.pricePlaceholder')"
                    data-testid="edit-audio-tts-price"
                    class="w-full"
                  />
                </UFormField>
                <UFormField
                  :label="t('admin.groups.voicePricing.audioSttPerHour')"
                  v-if="editForm.platform === 'grok'"
                >
                  <UInput
                    v-model.number="editForm.audio_stt_price_per_hour"
                    type="number"
                    step="0.000001"
                    min="0"
                    :placeholder="t('admin.groups.voicePricing.pricePlaceholder')"
                    data-testid="edit-audio-stt-price"
                    class="w-full"
                  />
                </UFormField>
              </div>
            </div>
            <!-- 搜索单价：所有可能走搜索的平台（Grok / DeepSeek） -->
            <div
              v-if="supportsSearchPricing(editForm.platform)"
            >
              <h4 class="text-sm font-medium text-default mb-1">
                {{ t("admin.groups.explicitPricing.title") }}
              </h4>
              <p class="text-xs text-muted mb-3">
                {{ t("admin.groups.explicitPricing.description") }}
              </p>
              <div class="grid grid-cols-1 gap-3 md:grid-cols-3">
                <UFormField :label="t('admin.groups.explicitPricing.searchPricePer1k')">
                  <UInput
                    v-model.number="editForm.search_price_per_1k"
                    type="number"
                    step="0.000001"
                    min="0"
                    :placeholder="t('admin.groups.explicitPricing.pricePlaceholder')"
                    data-testid="edit-search-price"
                    class="w-full"
                  />
                </UFormField>
                <UFormField
                  :label="t('admin.groups.explicitPricing.searchProviderBaseUrl')"
                  v-if="editForm.platform === 'deepseek'"
                >
                  <UInput
                    v-model="editForm.search_provider_base_url"
                    type="text"
                    :placeholder="t('admin.groups.explicitPricing.searchProviderBaseUrlPlaceholder')"
                    data-testid="edit-search-provider-url"
                    class="w-full"
                  />
                </UFormField>
                <div v-if="editForm.platform === 'deepseek'">
                  <label class="mb-1 block text-sm font-medium text-default">{{ t("admin.groups.explicitPricing.searchProviderApiKey") }}</label>
                  <UInput
                    v-model="editForm.search_provider_api_key"
                    type="password"
                    autocomplete="new-password"
                    :placeholder="t('admin.groups.explicitPricing.searchProviderApiKeyPlaceholder')"
                    data-testid="edit-search-provider-key"
                    class="w-full"
                  />
                  <p
                    v-if="editForm.searchProviderKeyConfigured && !editForm.clearSearchProviderKey"
                    class="mt-1 text-xs text-success"
                    data-testid="edit-search-provider-key-configured"
                  >
                    {{ t("admin.groups.explicitPricing.searchProviderApiKeyConfigured") }}
                  </p>
                  <UCheckbox
                    class="mt-1"
                    v-model="editForm.clearSearchProviderKey"
                    :label="t('admin.groups.explicitPricing.searchProviderApiKeyClear')"
                  />
                </div>
              </div>
            </div>
            <!-- Codex 网页搜索按次计费（仅 openai 平台） -->
            <div
              v-if="editForm.platform === 'openai'"
            >
              <h4 class="text-sm font-medium text-default mb-3">
                {{ t("admin.groups.webSearchPricing.title") }}
              </h4>
              <div class="space-y-2">
                <UFormField :label="t('admin.groups.webSearchPricing.pricePerCall')" :help="t('admin.groups.webSearchPricing.pricePerCallHint')">
                  <UInput
                    v-model.number="editForm.web_search_price_per_call"
                    type="number"
                    step="0.001"
                    min="0"
                    placeholder="0.01"
                    class="w-full"
                  />
                </UFormField>
                <div
                  class="mt-2 rounded-lg bg-elevated/50 p-3 text-xs text-default"
                >
                  {{
                    t("admin.groups.webSearchPricing.finalPricePreview", {
                      price: editWebSearchFinalPricePreview,
                    })
                  }}
                </div>
              </div>
            </div>
            <!-- Grok 安全拒绝单次扣费（放在模型白名单上方） -->
            <div
              v-if="editForm.platform === 'grok'"
            >
              <h4 class="mb-1 text-sm font-medium text-default">
                {{ t("admin.groups.moderationRefusalPricing.title") }}
              </h4>
              <p class="mb-3 text-xs text-muted">
                {{ t("admin.groups.moderationRefusalPricing.hint") }}
              </p>
              <div class="grid grid-cols-1 gap-3 md:grid-cols-3">
                <UFormField :label="t('admin.groups.moderationRefusalPricing.pricePerCall')">
                  <UInput
                    v-model.number="editForm.grok_moderation_refusal_price"
                    type="number"
                    step="0.00000001"
                    min="0"
                    :placeholder="t('admin.groups.moderationRefusalPricing.placeholder')"
                    data-testid="edit-grok-moderation-refusal-price"
                    class="w-full"
                  />
                </UFormField>
              </div>
            </div>
          </section>
          <section class="space-y-5">
            <h3 class="text-base font-semibold text-highlighted">{{ t("admin.groups.formSections.dispatch") }}</h3>
            <!-- 跨协议转换开关：所有平台的分组都有（Anthropic 协议族 <-> OpenAI 协议族）。
                 账号有原生端点时始终先走原生转发，开关只决定"没有原生端点时"能否转换兜底。 -->
            <div>
              <UFormField
                :label="t('admin.groups.openaiMessages.crossProtocolEnabled')"
                :description="t('admin.groups.openaiMessages.crossProtocolHint')"
                class="flex items-center justify-between gap-4"
                :ui="{ container: 'mt-0 shrink-0' }"
              >
                <Toggle v-model="editForm.cross_protocol_conversion_enabled" />
              </UFormField>
            </div>
            <!-- OpenAI Messages 调度配置（OpenAI 与 Composite 平台） -->
            <div
              v-if="supportsMessagesDispatchPlatform(editForm.platform)"
            >
              <h4 class="text-sm font-medium text-default mb-3">
                {{ t("admin.groups.openaiMessages.title") }}
              </h4>

              <!-- 允许 Messages 调度开关 -->
              <UFormField
                :label="t('admin.groups.openaiMessages.allowDispatch')"
                :description="t('admin.groups.openaiMessages.allowDispatchHint')"
                class="flex items-center justify-between gap-4"
                :ui="{ container: 'mt-0 shrink-0' }"
              >
                <Toggle v-model="editForm.allow_messages_dispatch" />
              </UFormField>

              <div
                v-if="
                  editForm.platform === 'openai' && editForm.allow_messages_dispatch
                "
                class="mt-3"
              >
                <div
                  class="space-y-3"
                >
                  <div>
                    <div class="flex items-center gap-2">
                      <label
                        class="text-sm font-medium text-highlighted"
                        >{{
                          t("admin.groups.openaiMessages.familyMappingTitle")
                        }}</label
                      >
                    </div>
                    <p class="mt-1 text-xs text-muted">
                      {{ t("admin.groups.openaiMessages.familyMappingHint") }}
                    </p>
                  </div>
                  <div>
                    <div class="grid gap-4 md:grid-cols-3">
                      <UFormField :label="t('admin.groups.openaiMessages.opusModel')">
                        <UInput
                          v-model="editForm.opus_mapped_model"
                          type="text"
                          :placeholder=" t('admin.groups.openaiMessages.opusModelPlaceholder') "
                          class="w-full"
                        />
                      </UFormField>
                      <UFormField :label="t('admin.groups.openaiMessages.sonnetModel')">
                        <UInput
                          v-model="editForm.sonnet_mapped_model"
                          type="text"
                          :placeholder=" t('admin.groups.openaiMessages.sonnetModelPlaceholder') "
                          class="w-full"
                        />
                      </UFormField>
                      <UFormField :label="t('admin.groups.openaiMessages.haikuModel')">
                        <UInput
                          v-model="editForm.haiku_mapped_model"
                          type="text"
                          :placeholder=" t('admin.groups.openaiMessages.haikuModelPlaceholder') "
                          class="w-full"
                        />
                      </UFormField>
                    </div>
                  </div>
                </div>

                <div
                  class="mt-6 space-y-3"
                >
                  <div>
                    <div class="flex items-start justify-between gap-3">
                      <div>
                        <div class="flex items-center gap-2">
                          <label
                            class="text-sm font-medium text-highlighted"
                            >{{
                              t("admin.groups.openaiMessages.exactMappingTitle")
                            }}</label
                          >
                        </div>
                        <p
                          class="mt-1 text-xs text-muted"
                        >
                          {{ t("admin.groups.openaiMessages.exactMappingHint") }}
                        </p>
                      </div>
                    </div>
                  </div>

                  <div>
                    <div
                      v-if="editForm.exact_model_mappings.length === 0"
                      class="flex items-center justify-between gap-3 rounded-lg bg-elevated/50 px-4 py-3 text-sm text-muted"
                    >
                      <span>{{
                        t("admin.groups.openaiMessages.noExactMappings")
                      }}</span>
                      <UButton
                        @click="addEditMessagesDispatchMapping"
                        icon="i-lucide-plus"
                        color="primary"
                        variant="ghost"
                        size="sm"
                      >{{ t("admin.groups.openaiMessages.addExactMapping") }}</UButton>
                    </div>

                    <div v-else class="space-y-3">
                      <div
                        v-for="row in editForm.exact_model_mappings"
                        :key="getEditMessagesDispatchRowKey(row)"
                        class="group relative rounded-lg border border-default p-3"
                      >
                        <div class="flex items-center gap-4">
                          <div
                            class="grid flex-1 gap-4 md:grid-cols-[minmax(0,1fr)_auto_minmax(0,1fr)] md:items-start"
                          >
                            <UFormField :label="t('admin.groups.openaiMessages.claudeModel')">
                              <UInput
                                v-model="row.claude_model"
                                type="text"
                                :placeholder=" t( 'admin.groups.openaiMessages.claudeModelPlaceholder', ) "
                                class="w-full"
                              />
                            </UFormField>
                            <div
                              class="hidden md:flex md:justify-center md:pt-7 text-primary"
                            >
                              <Icon
                                name="arrowRight"
                                size="sm"
                                class="transition-transform group-hover:translate-x-1"
                              />
                            </div>
                            <UFormField :label="t('admin.groups.openaiMessages.targetModel')">
                              <UInput
                                v-model="row.target_model"
                                type="text"
                                :placeholder=" t( 'admin.groups.openaiMessages.targetModelPlaceholder', ) "
                                class="w-full"
                              />
                            </UFormField>
                          </div>
                          <UButton
                            @click="removeEditMessagesDispatchMapping(row)"
                            :title=" t('admin.groups.openaiMessages.removeExactMapping') "
                            icon="i-lucide-trash-2"
                            color="error"
                            variant="ghost"
                            size="sm"
                            square
                            class="mt-6"
                          />
                        </div>
                      </div>

                      <UButton
                        @click="addEditMessagesDispatchMapping"
                        icon="i-lucide-plus"
                        color="neutral"
                        variant="outline"
                        size="sm"
                        block
                      >{{ t("admin.groups.openaiMessages.addExactMapping") }}</UButton>
                    </div>
                  </div>
                </div>
              </div>
            </div>
            <!-- OpenAI Fast 开关（OpenAI 与 Composite 平台） -->
            <div
              v-if="supportsGroupOpenAIFast(editForm.platform)"
            >
              <h4 class="text-sm font-medium text-default mb-3">
                {{ t("admin.groups.openaiFast.title") }}
              </h4>
              <UFormField
                :label="t('admin.groups.openaiFast.force')"
                :description="t('admin.groups.openaiFast.hint')"
                class="flex items-center justify-between gap-4"
                :ui="{ container: 'mt-0 shrink-0' }"
              >
                <Toggle
                  data-testid="edit-force-openai-fast"
                  :aria-label="t('admin.groups.openaiFast.force')"
                  v-model="editForm.force_openai_fast"
                />
              </UFormField>
              <UFormField
                :label="t('admin.groups.openaiFast.free')"
                :description="t('admin.groups.openaiFast.freeHint')"
                class="flex items-center justify-between gap-4 mt-4"
                :ui="{ container: 'mt-0 shrink-0' }"
              >
                <Toggle
                  data-testid="edit-free-openai-fast"
                  :aria-label="t('admin.groups.openaiFast.free')"
                  v-model="editForm.free_openai_fast"
                />
              </UFormField>
            </div>
            <!-- Claude Code 客户端限制（仅 anthropic 平台） -->
            <div v-if="editForm.platform === 'anthropic'">
              <UFormField :description="editForm.claude_code_only
                      ? t('admin.groups.claudeCode.enabled')
                      : t('admin.groups.claudeCode.disabled')" class="flex items-center justify-between gap-4" :ui="{ container: 'mt-0 shrink-0' }">
                <template #label>
                  <span class="inline-flex items-center gap-1">{{ t("admin.groups.claudeCode.title") }} <GroupFieldHelp :label="t('admin.groups.claudeCode.title')" :text="t('admin.groups.claudeCode.tooltip')" /></span>
                </template>
                <Toggle v-model="editForm.claude_code_only" />
              </UFormField>
              <!-- 降级分组选择（仅当启用 claude_code_only 时显示） -->
              <UFormField
                :label="t('admin.groups.claudeCode.fallbackGroup')"
                :help="t('admin.groups.claudeCode.fallbackHint')"
                v-if="editForm.claude_code_only"
                class="mt-3"
              >
                <Select
                  v-model="editForm.fallback_group_id"
                  :options="fallbackGroupOptionsForEdit"
                  :placeholder="t('admin.groups.claudeCode.noFallback')"
                />
              </UFormField>
            </div>
          </section>
          <UCollapsible v-model:open="editAdvancedOpen" :unmount-on-hide="false" class="space-y-5">
            <UButton
              color="neutral"
              variant="ghost"
              trailing-icon="i-lucide-chevron-down"
              :label="t('admin.groups.formSections.advanced')"
              class="-mx-2.5 text-base font-semibold text-highlighted"
              :ui="{ trailingIcon: 'transition-transform duration-200 group-data-[state=open]:rotate-180' }"
            />
            <template #content>
              <div class="space-y-5 pt-1">
                <div>
                  <UFormField
                    :label="t('admin.groups.returnRequestedModel.label')"
                    class="flex items-center justify-between gap-4"
                    :ui="{ container: 'mt-0 shrink-0' }"
                  >
                    <template #description><span id="edit-return-requested-model-hint">{{ t("admin.groups.returnRequestedModel.hint") }}</span></template>
                    <Toggle
                      data-testid="edit-return-requested-model"
                      :aria-label="t('admin.groups.returnRequestedModel.label')"
                      aria-describedby="edit-return-requested-model-hint"
                      v-model="editForm.return_requested_model"
                    />
                  </UFormField>
                </div>
                <!-- Codex Live 开关（OpenAI 与 Composite 平台） -->
                <div
                  v-if="supportsLivePlatform(editForm.platform)"
                >
                  <h4 class="text-sm font-medium text-default mb-3">
                    {{ t("admin.groups.openaiLive.title") }}
                  </h4>
                  <UFormField
                    :label="t('admin.groups.openaiLive.allow')"
                    :description="t('admin.groups.openaiLive.hint')"
                    class="flex items-center justify-between gap-4"
                    :ui="{ container: 'mt-0 shrink-0' }"
                  >
                    <Toggle
                      :model-value="editForm.allow_live"
                      @update:model-value="toggleLive('edit')"
                    />
                  </UFormField>
                </div>
                <!-- 账号过滤控制 (OpenAI/Antigravity/Anthropic/Gemini) -->
                <div
                  v-if="
                    ['openai', 'antigravity', 'anthropic', 'gemini'].includes(
                      editForm.platform,
                    )
                  "
                >
                  <h4 class="text-sm font-medium text-default mb-3">
                    {{ t("admin.groups.accountFilters.title") }}
                  </h4>

                  <!-- require_oauth_only toggle -->
                  <UFormField
                    :label="t('admin.groups.accountFilters.oauthOnly')"
                    :description="editForm.require_oauth_only
                            ? t('admin.groups.accountFilters.oauthOnlyEnabled')
                            : t('admin.groups.accountFilters.disabled')"
                    class="flex items-center justify-between gap-4"
                    :ui="{ container: 'mt-0 shrink-0' }"
                  >
                    <Toggle v-model="editForm.require_oauth_only" />
                  </UFormField>

                  <!-- require_privacy_set toggle -->
                  <UFormField
                    :label="t('admin.groups.accountFilters.privacySetOnly')"
                    :description="editForm.require_privacy_set
                            ? t('admin.groups.accountFilters.privacySetOnlyEnabled')
                            : t('admin.groups.accountFilters.disabled')"
                    class="flex items-center justify-between gap-4"
                    :ui="{ container: 'mt-0 shrink-0' }"
                  >
                    <Toggle v-model="editForm.require_privacy_set" />
                  </UFormField>
                </div>
                <!-- 无效请求兜底（仅 anthropic/antigravity 平台，且非订阅分组） -->
                <div
                  v-if="
                    ['anthropic', 'antigravity'].includes(editForm.platform) &&
                    editForm.subscription_type !== 'subscription'
                  "
                >
                  <UFormField :label="t('admin.groups.invalidRequestFallback.title')" :help="t('admin.groups.invalidRequestFallback.hint')">
                    <Select
                      v-model="editForm.fallback_group_id_on_invalid_request"
                      :options="invalidRequestFallbackOptionsForEdit"
                      :placeholder="t('admin.groups.invalidRequestFallback.noFallback')"
                    />
                  </UFormField>
                </div>
                <!-- 模型路由配置（仅 anthropic 平台） -->
                <div v-if="editForm.platform === 'anthropic'">
                  <UFormField :description="editForm.model_routing_enabled
                          ? t('admin.groups.modelRouting.enabled')
                          : t('admin.groups.modelRouting.disabled')" class="flex items-center justify-between gap-4" :ui="{ container: 'mt-0 shrink-0' }">
                    <template #label>
                      <span class="inline-flex items-center gap-1">{{ t("admin.groups.modelRouting.title") }} <GroupFieldHelp :label="t('admin.groups.modelRouting.title')" :text="t('admin.groups.modelRouting.tooltip')" /></span>
                    </template>
                    <Toggle v-model="editForm.model_routing_enabled" />
                  </UFormField>
                  <p
                    v-if="!editForm.model_routing_enabled"
                    class="text-xs text-muted mb-3"
                  >
                    {{ t("admin.groups.modelRouting.disabledHint") }}
                  </p>
                  <p v-else class="text-xs text-muted mb-3">
                    {{ t("admin.groups.modelRouting.noRulesHint") }}
                  </p>
                  <!-- 路由规则列表（仅在启用时显示） -->
                  <div v-if="editForm.model_routing_enabled" class="space-y-3">
                    <div
                      v-for="rule in editModelRoutingRules"
                      :key="getEditRuleRenderKey(rule)"
                      class="rounded-lg border border-default p-3"
                    >
                      <div class="flex items-start gap-3">
                        <div class="flex-1 space-y-2">
                          <UFormField :label="t('admin.groups.modelRouting.modelPattern')" size="sm">
                            <UInput
                              v-model="rule.pattern"
                              type="text"
                              :placeholder=" t('admin.groups.modelRouting.modelPatternPlaceholder') "
                              class="w-full"
                            />
                          </UFormField>
                          <div>
                            <label class="mb-1 block text-xs font-medium text-default">{{
                              t("admin.groups.modelRouting.accounts")
                            }}</label>
                            <!-- 已选账号标签 -->
                            <div
                              v-if="rule.accounts.length > 0"
                              class="flex flex-wrap gap-1.5 mb-2"
                            >
                              <span
                                v-for="account in rule.accounts"
                                :key="account.id"
                                class="inline-flex items-center gap-1 rounded-full bg-primary/10 px-2.5 py-1 text-xs font-medium text-primary"
                              >
                                {{ account.name }}
                                <UButton
                                  @click="removeSelectedAccount(rule, account.id, true)"
                                  icon="i-lucide-x"
                                  color="primary"
                                  variant="ghost"
                                  size="sm"
                                  square
                                  class="ml-0.5"
                                />
                              </span>
                            </div>
                            <!-- 账号搜索输入框 -->
                            <div class="relative account-search-container">
                              <UInput
                                v-model=" accountSearchKeyword[getEditRuleSearchKey(rule)] "
                                type="text"
                                :placeholder=" t( 'admin.groups.modelRouting.searchAccountPlaceholder', ) "
                                @input="searchAccountsByRule(rule, true)"
                                @focus="onAccountSearchFocus(rule, true)"
                                class="w-full"
                              />
                              <!-- 搜索结果下拉框 -->
                              <div
                                v-if="
                                  showAccountDropdown[getEditRuleSearchKey(rule)] &&
                                  accountSearchResults[getEditRuleSearchKey(rule)]
                                    ?.length > 0
                                "
                                class="absolute z-50 mt-1 max-h-48 w-full overflow-auto rounded-lg border border-default bg-default shadow-lg"
                              >
                                <UButton
                                  v-for="account in accountSearchResults[ getEditRuleSearchKey(rule) ]"
                                  :key="account.id"
                                  @click="selectAccount(rule, account, true)"
                                  :class="{ 'opacity-50': rule.accounts.some( (a) => a.id === account.id, ), }"
                                  :disabled=" rule.accounts.some((a) => a.id === account.id) "
                                  color="neutral"
                                  variant="ghost"
                                  size="sm"
                                  block
                                  class="justify-start"
                                ><span>{{ account.name }}</span>
                                  <span class="ml-2 text-xs text-dimmed"
                                    >#{{ account.id }}</span
                                  ></UButton>
                              </div>
                            </div>
                            <p class="text-xs text-dimmed mt-1">
                              {{ t("admin.groups.modelRouting.accountsHint") }}
                            </p>
                          </div>
                        </div>
                        <UButton
                          @click="removeEditRoutingRule(rule)"
                          :title="t('admin.groups.modelRouting.removeRule')"
                          icon="i-lucide-trash-2"
                          color="error"
                          variant="ghost"
                          size="sm"
                          square
                          class="mt-5"
                        />
                      </div>
                    </div>
                  </div>
                  <!-- 添加规则按钮（仅在启用时显示） -->
                  <UButton
                    v-if="editForm.model_routing_enabled"
                    @click="addEditRoutingRule"
                    icon="i-lucide-plus"
                    color="primary"
                    variant="ghost"
                    size="sm"
                    class="mt-3"
                  >{{ t("admin.groups.modelRouting.addRule") }}</UButton>
                </div>
                <!-- MCP XML 协议注入（仅 antigravity 平台） -->
                <div v-if="editForm.platform === 'antigravity'">
                  <UFormField :description="editForm.mcp_xml_inject
                          ? t('admin.groups.mcpXml.enabled')
                          : t('admin.groups.mcpXml.disabled')" class="flex items-center justify-between gap-4" :ui="{ container: 'mt-0 shrink-0' }">
                    <template #label>
                      <span class="inline-flex items-center gap-1">{{ t("admin.groups.mcpXml.title") }} <GroupFieldHelp :label="t('admin.groups.mcpXml.title')" :text="t('admin.groups.mcpXml.tooltip')" /></span>
                    </template>
                    <Toggle v-model="editForm.mcp_xml_inject" />
                  </UFormField>
                </div>
              </div>
            </template>
          </UCollapsible>
        </template>
      </form>

      <template #footer>
        <div class="flex justify-end gap-2">
          <UButton @click="closeEditModal" color="neutral" variant="subtle">{{ t("common.cancel") }}</UButton>
          <UButton
            type="submit"
            form="edit-group-form"
            :disabled="submitting"
            :loading="submitting"
            data-tour="group-form-submit"
          >
            {{ submitting ? t("admin.groups.updating") : t("common.update") }}
          </UButton>
        </div>
      </template>
    </BaseDialog>

    <!-- Delete Confirmation Dialog -->
    <ConfirmDialog
      :show="showDeleteDialog"
      :title="t('admin.groups.deleteGroup')"
      :message="deleteConfirmMessage"
      :confirm-text="t('common.delete')"
      :cancel-text="t('common.cancel')"
      :danger="true"
      @confirm="confirmDelete"
      @cancel="showDeleteDialog = false"
    />

    <ConfirmDialog
      :show="showUnsupportedLiveConfirm"
      :title="t('admin.groups.openaiLive.unsupportedTitle')"
      :message="t('admin.groups.openaiLive.unsupportedMessage')"
      :confirm-text="t('admin.groups.openaiLive.enableAnyway')"
      :cancel-text="t('common.cancel')"
      :danger="true"
      @confirm="confirmUnsupportedLive"
      @cancel="cancelUnsupportedLive"
    />

    <!-- Sort Order Modal -->
    <BaseDialog
      :show="showSortModal"
      :title="t('admin.groups.sortOrder')"
      width="normal"
      @close="closeSortModal"
    >
      <div class="space-y-3">
        <p class="text-sm text-muted">
          {{ t("admin.groups.sortOrderHint") }}
        </p>
        <VueDraggable
          v-model="sortableGroups"
          :animation="200"
          class="divide-y divide-default overflow-hidden rounded-lg border border-default"
        >
          <div
            v-for="group in sortableGroups"
            :key="group.id"
            class="flex cursor-grab items-center gap-3 bg-default px-3 py-2.5 hover:bg-elevated/50 active:cursor-grabbing"
          >
            <UIcon name="i-lucide-grip-vertical" class="size-4 shrink-0 text-dimmed" />
            <span class="min-w-0 flex-1 truncate text-sm font-medium text-highlighted">{{ group.name }}</span>
            <UBadge color="neutral" variant="subtle" size="sm" class="gap-1">
              <PlatformIcon :platform="group.platform" size="xs" />
              {{ t("admin.groups.platforms." + group.platform) }}
            </UBadge>
            <span class="w-12 text-right font-mono text-xs text-dimmed">#{{ group.id }}</span>
          </div>
        </VueDraggable>
      </div>

      <template #footer>
        <div class="flex justify-end gap-2">
          <UButton @click="closeSortModal" color="neutral" variant="subtle">{{ t("common.cancel") }}</UButton>
          <UButton :disabled="sortSubmitting" :loading="sortSubmitting" @click="saveSortOrder">
            {{ sortSubmitting ? t("common.saving") : t("common.save") }}
          </UButton>
        </div>
      </template>
    </BaseDialog>

    <!-- Composite Routes Modal -->
    <BaseDialog
      :show="showCompositeRoutesModal"
      :title="
        compositeRoutesGroup
          ? t('admin.groups.compositeRoutes.titleWithGroup', {
              name: compositeRoutesGroup.name,
            })
          : t('admin.groups.compositeRoutes.title')
      "
      width="wide"
      @close="closeCompositeRoutesModal"
    >
      <div class="grid gap-5 lg:grid-cols-[minmax(0,1.15fr)_minmax(320px,0.85fr)]">
        <section class="min-w-0">
          <div class="mb-3 flex items-center justify-between gap-3">
            <h3 class="text-sm font-semibold text-highlighted">
              {{ t("admin.groups.compositeRoutes.routes") }}
            </h3>
            <UButton
              icon="i-lucide-refresh-cw"
              color="neutral"
              variant="ghost"
              size="sm"
              square
              :loading="compositeRoutesLoading"
              :title="t('common.refresh')"
              :aria-label="t('common.refresh')"
              @click="loadCompositeRoutes"
            />
          </div>

          <UTable
            :data="compositeRoutes"
            :columns="compositeRouteColumns"
            :loading="compositeRoutesLoading"
            :empty="compositeRoutesLoading ? t('common.loading') : t('admin.groups.compositeRoutes.empty')"
            :meta="compositeRouteTableMeta"
            class="rounded-lg border border-default"
            :ui="{ th: 'py-2 text-xs', td: 'py-2 align-top' }"
          >
            <template #public_model-cell="{ row }">
              <div class="max-w-60">
                <div class="break-all font-medium text-highlighted">
                  {{ row.original.public_model }}
                </div>
                <div class="mt-1 flex flex-wrap items-center gap-1.5">
                  <UBadge color="neutral" variant="subtle" size="sm">{{
                    compositeRouteMatchLabel(row.original.match_type)
                  }}</UBadge>
                  <UBadge v-if="!row.original.enabled" color="error" variant="subtle" size="sm">
                    {{ t("admin.accounts.status.inactive") }}
                  </UBadge>
                </div>
              </div>
            </template>
            <template #target-cell="{ row }">
              <div class="flex items-center gap-1.5 text-highlighted">
                <PlatformIcon :platform="row.original.target_platform" size="xs" />
                <span>{{ formatCompositePlatform(row.original.target_platform) }}</span>
              </div>
              <div class="mt-1 break-all text-xs text-muted">
                {{ row.original.upstream_model || row.original.public_model }}
              </div>
            </template>
            <template #scope-cell="{ row }">
              <div class="text-default">
                {{ formatCompositeEndpoint(row.original.endpoint) }}
              </div>
              <div class="text-xs text-muted">
                {{ t("admin.groups.compositeRoutes.priority") }}:
                {{ row.original.priority }}
              </div>
            </template>
            <template #actions-cell="{ row }">
              <div class="flex justify-end gap-1">
                <UButton
                  :title="t('common.edit')"
                  :aria-label="t('common.edit')"
                  @click="editCompositeRoute(row.original)"
                  icon="i-lucide-pencil"
                  color="neutral"
                  variant="ghost"
                  size="sm"
                  square
                />
                <UButton
                  :title="t('common.delete')"
                  :aria-label="t('common.delete')"
                  @click="deleteCompositeRoute(row.original)"
                  icon="i-lucide-trash-2"
                  color="error"
                  variant="ghost"
                  size="sm"
                  square
                />
              </div>
            </template>
          </UTable>
        </section>

        <section class="space-y-5">
          <form class="space-y-3" @submit.prevent="saveCompositeRoute">
            <div class="flex items-center justify-between gap-3">
              <h3 class="text-sm font-semibold text-highlighted">
                {{
                  compositeRouteEditingId
                    ? t("admin.groups.compositeRoutes.editRoute")
                    : t("admin.groups.compositeRoutes.addRoute")
                }}
              </h3>
              <UButton
                v-if="compositeRouteEditingId"
                @click="resetCompositeRouteForm"
                color="neutral"
                variant="ghost"
                size="xs"
              >{{ t("common.cancel") }}</UButton>
            </div>

            <UFormField :label="t('admin.groups.compositeRoutes.publicModel')" required>
              <UInput
                v-model.trim="compositeRouteForm.public_model"
                type="text"
                required
                placeholder="openrouter/gpt-5"
                class="w-full"
              />
            </UFormField>

            <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
              <UFormField :label="t('admin.groups.compositeRoutes.matchType')">
                <Select
                  v-model="compositeRouteForm.match_type"
                  :options="compositeRouteMatchOptions"
                />
              </UFormField>
              <UFormField :label="t('admin.groups.compositeRoutes.endpoint')">
                <Select
                  v-model="compositeRouteForm.endpoint"
                  :options="compositeRouteEndpointOptions"
                />
              </UFormField>
            </div>

            <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
              <UFormField :label="t('admin.groups.compositeRoutes.targetPlatform')">
                <Select
                  v-model="compositeRouteForm.target_platform"
                  :options="compositeRoutePlatformOptions"
                />
              </UFormField>
              <UFormField :label="t('admin.groups.compositeRoutes.priority')">
                <UInput
                  v-model.number="compositeRouteForm.priority"
                  type="number"
                  min="1"
                  step="1"
                  class="w-full"
                />
              </UFormField>
            </div>

            <UFormField
              :label="t('admin.groups.compositeRoutes.upstreamModel')"
              :help="t('admin.groups.compositeRoutes.upstreamModelHint')"
            >
              <UInput
                v-model.trim="compositeRouteForm.upstream_model"
                type="text"
                placeholder="gpt-5"
                class="w-full"
              />
            </UFormField>

            <UFormField :label="t('admin.groups.compositeRoutes.notes')">
              <UTextarea v-model.trim="compositeRouteForm.notes" :rows="2" class="w-full" />
            </UFormField>

            <div class="flex items-center justify-between gap-3">
              <UCheckbox
                v-model="compositeRouteForm.enabled"
                :label="t('admin.groups.compositeRoutes.enabled')"
              />
              <UButton
                type="submit"
                icon="i-lucide-check"
                :disabled="compositeRouteSaving"
                :loading="compositeRouteSaving"
              >
                {{ compositeRouteEditingId ? t("common.update") : t("common.create") }}
              </UButton>
            </div>
          </form>

          <div>
            <h3 class="mb-3 text-sm font-semibold text-highlighted">
              {{ t("admin.groups.compositeRoutes.preview") }}
            </h3>
            <div class="space-y-3">
              <UInput
                v-model.trim="compositePreviewModel"
                type="text"
                placeholder="openrouter/gpt-5"
                @keyup.enter="previewCompositeRoute"
                class="w-full"
              />
              <div class="flex gap-2">
                <Select
                  v-model="compositePreviewEndpoint"
                  :options="compositeRouteEndpointOptions"
                  class="min-w-0 flex-1"
                />
                <UButton
                  :disabled="compositePreviewLoading || !compositePreviewModel"
                  @click="previewCompositeRoute"
                  icon="i-lucide-play"
                  color="neutral"
                  variant="outline"
                  square
                />
              </div>

              <div
                v-if="compositePreviewDecision"
                class="rounded-lg bg-elevated/50 p-3 text-sm"
              >
                <div class="mb-2 flex items-center gap-2">
                  <UBadge
                    :color="compositePreviewDecision.matched ? 'success' : 'error'"
                    variant="subtle"
                  >
                    {{
                      compositePreviewDecision.matched
                        ? t("admin.groups.compositeRoutes.matched")
                        : t("admin.groups.compositeRoutes.notMatched")
                    }}
                  </UBadge>
                  <UBadge color="neutral" variant="subtle">
                    {{
                      compositeRouteSourceLabel(
                        compositePreviewDecision.source,
                      )
                    }}
                  </UBadge>
                </div>
                <div
                  v-if="compositePreviewDecision.matched"
                  class="space-y-1 text-default"
                >
                  <div>
                    {{ t("admin.groups.compositeRoutes.targetPlatform") }}:
                    {{
                      formatCompositePlatform(
                        compositePreviewDecision.target_platform,
                      )
                    }}
                  </div>
                  <div class="break-all">
                    {{ t("admin.groups.compositeRoutes.upstreamModel") }}:
                    {{ compositePreviewDecision.upstream_model }}
                  </div>
                </div>
                <div
                  v-else
                  class="text-muted"
                >
                  {{ compositePreviewDecision.reason }}
                </div>
              </div>
            </div>
          </div>
        </section>
      </div>

      <template #footer>
        <div class="flex justify-end">
          <UButton @click="closeCompositeRoutesModal" color="neutral" variant="subtle">{{ t("common.close") }}</UButton>
        </div>
      </template>
    </BaseDialog>

    <!-- Group Rate Multipliers Modal -->
    <GroupRateMultipliersModal
      :show="showRateMultipliersModal"
      :group="rateMultipliersGroup"
      @close="showRateMultipliersModal = false"
    />

    <!-- Group RPM Overrides Modal -->
    <GroupRPMOverridesModal
      :show="showRPMOverridesModal"
      :group="rpmOverridesGroup"
      @close="showRPMOverridesModal = false"
    />
  </AppLayout>
</template>

<script setup lang="ts">
import MiniMaxH3PricingFields from "@/components/group/MiniMaxH3PricingFields.vue";
import { ref, reactive, computed, onMounted, onUnmounted, watch } from "vue";
import { useI18n } from "vue-i18n";
import { useAppStore } from "@/stores/app";
import { useAuthStore } from "@/stores/auth";
import { useOnboardingStore } from "@/stores/onboarding";
import { adminAPI } from "@/api/admin";
import type {
  AdminGroup,
  CodexModelsManifestConfig,
  CompositeModelRoute,
  CompositeModelRouteInput,
  CompositeRouteDecision,
  CompositeRouteEndpoint,
  CompositeRouteMatchType,
  GroupPlatform,
  SubscriptionType,
} from "@/types";
import {
  CONCRETE_PLATFORM_OPTIONS,
  GROUP_PLATFORM_OPTIONS,
} from "@/constants/platforms";
import type { Column } from "@/components/common/types";
import type { DropdownMenuItem, TableColumn } from "@nuxt/ui";
import AppLayout from "@/components/layout/AppLayout.vue";
import TablePageLayout from "@/components/layout/TablePageLayout.vue";
import PageToolbar from "@/components/layout/PageToolbar.vue";
import DataTable from "@/components/common/DataTable.vue";
import Pagination from "@/components/common/Pagination.vue";
import Toggle from "@/components/common/Toggle.vue";
import TimeWheelPicker from '@/components/common/TimeWheelPicker.vue'
import BaseDialog from "@/components/common/BaseDialog.vue";
import ConfirmDialog from "@/components/common/ConfirmDialog.vue";
import EmptyState from "@/components/common/EmptyState.vue";
import Select from "@/components/common/Select.vue";
import PlatformIcon from "@/components/common/PlatformIcon.vue";
import Icon from "@/components/icons/Icon.vue";
import AdminPageHeader from "@/components/admin/page/AdminPageHeader.vue";
import AdminRowAction from "@/components/admin/page/AdminRowAction.vue";
import GroupRateMultipliersModal from "@/components/admin/group/GroupRateMultipliersModal.vue";
import GroupRPMOverridesModal from "@/components/admin/group/GroupRPMOverridesModal.vue";
import GroupCapacityBadge from "@/components/common/GroupCapacityBadge.vue";
import ReasoningEffortPolicyFields from "@/components/admin/group/ReasoningEffortPolicyFields.vue";
import CodexManifestAccountsField from "@/components/admin/group/CodexManifestAccountsField.vue";
import GroupFieldHelp from "@/components/admin/group/GroupFieldHelp.vue";
import PricingEntryCard from "@/components/admin/channel/PricingEntryCard.vue";
import type { PricingFormEntry } from "@/components/admin/channel/types";
import {
  apiIntervalsToForm,
  createDefaultTimePricingForm,
  formIntervalsToAPI,
  mTokToPerToken,
  perTokenToMTok,
  toNullableNumber,
} from "@/components/admin/channel/types";
import type { ChannelModelPricing } from "@/api/admin/channels";
import { VueDraggable } from "vue-draggable-plus";
import { createStableObjectKeyResolver } from "@/utils/stableObjectKey";
import { extractApiErrorMessage } from "@/utils/apiError";
import { useKeyedDebouncedSearch } from "@/composables/useKeyedDebouncedSearch";
import { getPersistedPageSize } from "@/composables/usePersistedPageSize";
import {
  createDefaultMessagesDispatchFormState,
  messagesDispatchConfigToFormState,
  messagesDispatchFormStateToConfig,
  resetMessagesDispatchFormState,
  supportsMessagesDispatchPlatform,
  type MessagesDispatchMappingRow,
} from "./groupsMessagesDispatch";
import {
  normalizeGroupOpenAIFast,
  supportsGroupOpenAIFast,
} from "./groupsOpenAIFast";
import {
  addCustomModelAllowlistItem,
  buildModelAllowlistConfig,
  createModelAllowlistState as createInitialModelAllowlistState,
  invertModelAllowlistSelection,
  moveModelAllowlistItem,
  selectAllModelAllowlistItems,
  setModelAllowlistCandidates,
} from "./groupModelAllowlist";
import { createModelAllowlistCandidatesTracker } from "./modelAllowlistCandidates";
import { normalizeSupportedModelScopesForPlatform } from "./groupsSupportedModelScopes";

// 分组级「搜索每千次单价」目前对 Grok（原生 x_search/web_search）和
// DeepSeek（/v1/tools/search 代理 + 可注入的搜索工具）开放。
function supportsSearchPricing(platform: string | undefined): boolean {
  return platform === "grok" || platform === "deepseek";
}

import {
  isProfitControlPlatform,
  profitPercentToDecimal,
  profitDecimalToPercent,
  validateProfitControlFormState,
  type ProfitControlFormState,
} from "./groupsProfitControl";
import {
  normalizeReasoningEffortForPlatform,
  normalizeReasoningEffortOverLimit,
  reasoningEffortMappingsToAPI,
  reasoningEffortMappingsToRows,
  reasoningEffortOverLimitDowngrade,
  supportsReasoningEffortPolicyPlatform,
  type ReasoningEffortMappingRow,
} from "./groupsReasoningEffort";
import {
  getDefaultImagePreviewPrice,
  getDefaultVideoPreviewPrice,
  getImagePricePlaceholder,
  getVideoPricePlaceholder,
  imagePricingI18nKey,
  supportsImagePricingPlatform,
  supportsVideoPricingPlatform,
  videoPricingI18nKey,
} from "./groupsImagePricing";
import {
  createVideoModelPricesForm,
  grokVideoPriceResolutions,
  serializeVideoModelPrices,
  videoModelPriceFamilyRows,
} from "./groupsVideoModelPricing";

const supportsLivePlatform = (platform: string): boolean =>
  platform === "openai" || platform === "composite";

const emptyGroupPricing = (): PricingFormEntry => ({
  models: [],
  billing_mode: "token",
  input_price: null,
  output_price: null,
  cache_write_price: null,
  cache_write_1h_price: null,
  cache_read_price: null,
  image_input_price: null,
  image_output_price: null,
  per_request_price: null,
  intervals: [],
  time_pricing: createDefaultTimePricingForm(),
});

const addGroupPricing = (entries: PricingFormEntry[]) =>
  entries.push(emptyGroupPricing());

const groupPricingFromAPI = (
  pricing: ChannelModelPricing[] | undefined,
): PricingFormEntry[] =>
  (pricing || []).map((entry) => ({
    models: entry.models || [],
    billing_mode: entry.billing_mode || "token",
    input_price: perTokenToMTok(entry.input_price),
    output_price: perTokenToMTok(entry.output_price),
    cache_write_price: perTokenToMTok(entry.cache_write_price),
    cache_write_1h_price: perTokenToMTok(entry.cache_write_1h_price),
    cache_read_price: perTokenToMTok(entry.cache_read_price),
    image_input_price: perTokenToMTok(entry.image_input_price),
    image_output_price: perTokenToMTok(entry.image_output_price),
    per_request_price: entry.per_request_price,
    intervals: apiIntervalsToForm(entry.intervals || []),
    time_pricing: createDefaultTimePricingForm(),
  }));

const groupPricingToAPI = (
  pricing: PricingFormEntry[],
  platform: string,
): ChannelModelPricing[] =>
  pricing
    .filter((entry) => entry.models.length > 0)
    .map((entry) => ({
      platform,
      models: entry.models,
      billing_mode: entry.billing_mode,
      input_price: mTokToPerToken(entry.input_price),
      output_price: mTokToPerToken(entry.output_price),
      cache_write_price: mTokToPerToken(entry.cache_write_price),
      cache_write_1h_price: mTokToPerToken(entry.cache_write_1h_price),
      cache_read_price: mTokToPerToken(entry.cache_read_price),
      image_input_price: mTokToPerToken(entry.image_input_price),
      image_output_price: mTokToPerToken(entry.image_output_price),
      per_request_price: toNullableNumber(entry.per_request_price),
      intervals:
        entry.billing_mode === "token"
          ? []
          : formIntervalsToAPI(entry.intervals || []),
      time_pricing: null,
    }));

const { t } = useI18n();
const appStore = useAppStore();
const authStore = useAuthStore();
const onboardingStore = useOnboardingStore();

const ALWAYS_VISIBLE_COLUMNS = new Set(["name", "actions"]);
// Default hidden columns, applied to fresh layouts only (no saved setting yet). Capacity
// (live concurrency badges) and type (exclusive/public, also a toolbar filter) are behind
// "显示列" so the default columns fit next to the sidebar. A layout an admin already saved
// is kept exactly as saved: do not add new columns to VERSION_NEW_HIDDEN_COLUMNS for this.
const DEFAULT_HIDDEN_COLUMNS = ["id", "capacity", "is_exclusive"];
const HIDDEN_COLUMNS_KEY = "group-hidden-columns";
const COLUMN_SETTINGS_VERSION_KEY = "group-column-settings-version";
const COLUMN_SETTINGS_VERSION = 2;
const VERSION_NEW_HIDDEN_COLUMNS: Record<number, string[]> = {
  2: ["id"],
};

const allColumns = computed<Column[]>(() => {
  const basic: Column[] = [
    { key: "name", label: t("admin.groups.columns.name"), sortable: true },
    { key: "id", label: t("admin.groups.columns.id"), sortable: true },
    { key: "platform", label: t("admin.groups.columns.platform"), sortable: true },
    { key: "account_count", label: t("admin.groups.columns.accounts"), sortable: true },
    { key: "status", label: t("admin.groups.columns.status"), sortable: true },
    { key: "actions", label: t("admin.groups.columns.actions"), sortable: false },
  ];
  if (authStore.isSimpleMode) return basic;
  return [
    ...basic.slice(0, 3),
    { key: "billing_type", label: t("admin.groups.columns.billingType"), sortable: true },
    { key: "rate_multiplier", label: t("admin.groups.columns.rateMultiplier"), sortable: true },
    { key: "is_exclusive", label: t("admin.groups.columns.type"), sortable: true },
    basic[3],
    { key: "capacity", label: t("admin.groups.columns.capacity"), sortable: false },
    { key: "usage", label: t("admin.groups.columns.usage"), sortable: false },
    ...basic.slice(4),
  ];
});

const toggleableColumns = computed(() =>
  allColumns.value.filter((col) => !ALWAYS_VISIBLE_COLUMNS.has(col.key)),
);
const hiddenColumns = reactive<Set<string>>(new Set());

// "显示列" menu: one checkbox per optional column; the menu stays open while toggling.
const columnMenuItems = computed<DropdownMenuItem[]>(() =>
  toggleableColumns.value.map((col) => ({
    label: col.label,
    type: "checkbox" as const,
    checked: isColumnVisible(col.key),
    onUpdateChecked: () => toggleColumn(col.key),
    onSelect: (e: Event) => e.preventDefault(),
  })),
);

// Row "…" menu: the less frequent group actions (edit / duplicate stay as row buttons).
const rowMenuItems = (row: AdminGroup): DropdownMenuItem[][] => {
  const advanced: DropdownMenuItem[] = authStore.isSimpleMode
    ? []
    : [
        ...(row.platform === "composite"
          ? [{
              label: t("admin.groups.compositeRoutes.action"),
              icon: "i-lucide-shuffle",
              class: "groups-row-composite-routes",
              onSelect: () => handleCompositeRoutes(row),
            }]
          : []),
        {
          label: t("admin.groups.rateMultipliers"),
          icon: "i-lucide-circle-dollar-sign",
          class: "groups-row-rate-multipliers",
          onSelect: () => handleRateMultipliers(row),
        },
        {
          label: t("admin.groups.rpmOverrides"),
          icon: "i-lucide-zap",
          class: "groups-row-rpm-overrides",
          onSelect: () => handleRPMOverrides(row),
        },
      ];
  const remove: DropdownMenuItem[] = [{
    label: t("common.delete"),
    icon: "i-lucide-trash-2",
    color: "error",
    class: "groups-row-delete",
    onSelect: () => handleDelete(row),
  }];
  return advanced.length ? [advanced, remove] : [remove];
};

// Disabled routes stay listed but dimmed.
const compositeRouteTableMeta = {
  class: { tr: (row: { original: CompositeModelRoute }) => (row.original.enabled ? "" : "opacity-60") },
};

const compositeRouteColumns = computed<TableColumn<CompositeModelRoute>[]>(() => [
  { accessorKey: "public_model", header: t("admin.groups.compositeRoutes.publicModel") },
  { id: "target", header: t("admin.groups.compositeRoutes.target") },
  { id: "scope", header: t("admin.groups.compositeRoutes.scope") },
  { id: "actions", header: t("admin.groups.columns.actions"), meta: { class: { th: "text-right" } } },
]);

const getValidHiddenColumnKeys = () =>
  new Set(toggleableColumns.value.map((col) => col.key));

const loadSavedColumns = () => {
  hiddenColumns.clear();
  try {
    const saved = localStorage.getItem(HIDDEN_COLUMNS_KEY);
    const validKeys = getValidHiddenColumnKeys();

    if (saved) {
      const parsed = JSON.parse(saved);
      if (Array.isArray(parsed)) {
        parsed
          .filter(
            (key): key is string =>
              typeof key === "string" && validKeys.has(key),
          )
          .forEach((key) => hiddenColumns.add(key));
      }

      // Existing admins: auto-hide columns newly added as default-hidden.
      const storedVersion = Number(
        localStorage.getItem(COLUMN_SETTINGS_VERSION_KEY) ?? "1",
      );
      if (storedVersion < COLUMN_SETTINGS_VERSION) {
        let mutated = false;
        for (let v = storedVersion + 1; v <= COLUMN_SETTINGS_VERSION; v++) {
          for (const key of VERSION_NEW_HIDDEN_COLUMNS[v] ?? []) {
            if (validKeys.has(key) && !hiddenColumns.has(key)) {
              hiddenColumns.add(key);
              mutated = true;
            }
          }
        }
        if (mutated) {
          saveColumnsToStorage();
        } else {
          localStorage.setItem(
            COLUMN_SETTINGS_VERSION_KEY,
            String(COLUMN_SETTINGS_VERSION),
          );
        }
      }
    } else {
      DEFAULT_HIDDEN_COLUMNS.forEach((key) => {
        if (validKeys.has(key)) hiddenColumns.add(key);
      });
      saveColumnsToStorage();
    }
  } catch (error) {
    console.error("Failed to load group column settings:", error);
    DEFAULT_HIDDEN_COLUMNS.forEach((key) => hiddenColumns.add(key));
  }
};

const saveColumnsToStorage = () => {
  try {
    const validKeys = getValidHiddenColumnKeys();
    const keys = [...hiddenColumns].filter((key) => validKeys.has(key));
    localStorage.setItem(HIDDEN_COLUMNS_KEY, JSON.stringify(keys));
    localStorage.setItem(
      COLUMN_SETTINGS_VERSION_KEY,
      String(COLUMN_SETTINGS_VERSION),
    );
  } catch (error) {
    console.error("Failed to save group column settings:", error);
  }
};

const isColumnVisible = (key: string) => !hiddenColumns.has(key);
const hasVisibleUsageSummaryConsumer = computed(
  () => !authStore.isSimpleMode && (isColumnVisible("usage") || isColumnVisible("billing_type")),
);
const hasVisibleCapacityColumn = computed(() => !authStore.isSimpleMode && isColumnVisible("capacity"));

const toggleColumn = (key: string) => {
  const validKeys = getValidHiddenColumnKeys();
  if (!validKeys.has(key)) return;

  const wasHidden = hiddenColumns.has(key);
  if (wasHidden) {
    hiddenColumns.delete(key);
  } else {
    hiddenColumns.add(key);
  }
  saveColumnsToStorage();

  if (wasHidden && (key === "usage" || key === "billing_type")) {
    loadUsageSummary();
  }
  if (wasHidden && key === "capacity") {
    loadCapacitySummary();
  }
};

const columns = computed<Column[]>(() =>
  allColumns.value.filter(
    (col) => ALWAYS_VISIBLE_COLUMNS.has(col.key) || !hiddenColumns.has(col.key),
  ),
);

if (typeof window !== "undefined") {
  loadSavedColumns();
}

// Filter options
const statusOptions = computed(() => [
  { value: "", label: t("admin.groups.allStatus") },
  { value: "active", label: t("admin.accounts.status.active") },
  { value: "inactive", label: t("admin.accounts.status.inactive") },
]);

const exclusiveOptions = computed(() => [
  { value: "", label: t("admin.groups.allGroups") },
  { value: "true", label: t("admin.groups.exclusive") },
  { value: "false", label: t("admin.groups.nonExclusive") },
]);

const platformOptions = computed(() =>
  GROUP_PLATFORM_OPTIONS.filter(
    (option) => !authStore.isSimpleMode || option.value !== "composite",
  ),
);

const platformFilterOptions = computed(() => [
  { value: "", label: t("admin.groups.allPlatforms") },
  ...GROUP_PLATFORM_OPTIONS,
]);

const compositeRoutePlatformOptions = computed(() => [
  ...CONCRETE_PLATFORM_OPTIONS,
]);

const compositeRouteEndpointOptions = computed(() => [
  { value: "any", label: t("admin.groups.compositeRoutes.endpoints.any") },
  {
    value: "messages",
    label: t("admin.groups.compositeRoutes.endpoints.messages"),
  },
  {
    value: "count_tokens",
    label: t("admin.groups.compositeRoutes.endpoints.countTokens"),
  },
  {
    value: "responses",
    label: t("admin.groups.compositeRoutes.endpoints.responses"),
  },
  {
    value: "chat_completions",
    label: t("admin.groups.compositeRoutes.endpoints.chatCompletions"),
  },
  {
    value: "embeddings",
    label: t("admin.groups.compositeRoutes.endpoints.embeddings"),
  },
  { value: "images", label: t("admin.groups.compositeRoutes.endpoints.images") },
  { value: "gemini", label: t("admin.groups.compositeRoutes.endpoints.gemini") },
]);

const compositeRouteMatchOptions = computed(() => [
  { value: "exact", label: t("admin.groups.compositeRoutes.match.exact") },
  { value: "prefix", label: t("admin.groups.compositeRoutes.match.prefix") },
]);

const editStatusOptions = computed(() => [
  { value: "active", label: t("admin.accounts.status.active") },
  { value: "inactive", label: t("admin.accounts.status.inactive") },
]);

const subscriptionTypeOptions = computed(() => [
  { value: "standard", label: t("admin.groups.subscription.standard") },
  { value: "subscription", label: t("admin.groups.subscription.subscription") },
]);

// 降级分组选项（创建时）- 仅包含 anthropic 平台且未启用 claude_code_only 的分组
const fallbackGroupOptions = computed(() => {
  const options: { value: number | null; label: string }[] = [
    { value: null, label: t("admin.groups.claudeCode.noFallback") },
  ];
  const eligibleGroups = groups.value.filter(
    (g) =>
      g.platform === "anthropic" &&
      !g.claude_code_only &&
      g.status === "active",
  );
  eligibleGroups.forEach((g) => {
    options.push({ value: g.id, label: g.name });
  });
  return options;
});

// 降级分组选项（编辑时）- 排除自身
const fallbackGroupOptionsForEdit = computed(() => {
  const options: { value: number | null; label: string }[] = [
    { value: null, label: t("admin.groups.claudeCode.noFallback") },
  ];
  const currentId = editingGroup.value?.id;
  const eligibleGroups = groups.value.filter(
    (g) =>
      g.platform === "anthropic" &&
      !g.claude_code_only &&
      g.status === "active" &&
      g.id !== currentId,
  );
  eligibleGroups.forEach((g) => {
    options.push({ value: g.id, label: g.name });
  });
  return options;
});

// 无效请求兜底分组选项（创建时）- 仅包含 anthropic 平台、非订阅且未配置兜底的分组
const invalidRequestFallbackOptions = computed(() => {
  const options: { value: number | null; label: string }[] = [
    { value: null, label: t("admin.groups.invalidRequestFallback.noFallback") },
  ];
  const eligibleGroups = groups.value.filter(
    (g) =>
      g.platform === "anthropic" &&
      g.status === "active" &&
      g.subscription_type !== "subscription" &&
      g.fallback_group_id_on_invalid_request === null,
  );
  eligibleGroups.forEach((g) => {
    options.push({ value: g.id, label: g.name });
  });
  return options;
});

// 无效请求兜底分组选项（编辑时）- 排除自身
const invalidRequestFallbackOptionsForEdit = computed(() => {
  const options: { value: number | null; label: string }[] = [
    { value: null, label: t("admin.groups.invalidRequestFallback.noFallback") },
  ];
  const currentId = editingGroup.value?.id;
  const eligibleGroups = groups.value.filter(
    (g) =>
      g.platform === "anthropic" &&
      g.status === "active" &&
      g.subscription_type !== "subscription" &&
      g.fallback_group_id_on_invalid_request === null &&
      g.id !== currentId,
  );
  eligibleGroups.forEach((g) => {
    options.push({ value: g.id, label: g.name });
  });
  return options;
});

const canCopyAccountsFromGroup = (targetPlatform: GroupPlatform, sourcePlatform: GroupPlatform) =>
  targetPlatform === "composite" || sourcePlatform === targetPlatform;

const copyAccountsGroupLabel = (g: AdminGroup) => {
  const count = g.account_count || 0;
  const platform = t("admin.groups.platforms." + g.platform);
  return `${g.name} - ${platform} (${t("admin.groups.accountsCount", { count })})`;
};

// 复制账号的源分组选项（创建时）- 相同平台；composite 分组可汇总各平台账号
const copyAccountsGroupOptions = computed(() => {
  const eligibleGroups = groups.value.filter(
    (g) =>
      canCopyAccountsFromGroup(createForm.platform, g.platform) &&
      (g.account_count || 0) > 0,
  );
  return eligibleGroups.map((g) => ({
    value: g.id,
    label: copyAccountsGroupLabel(g),
  }));
});

// 复制账号的源分组选项（编辑时）- 相同平台；composite 分组可汇总各平台账号，排除自身
const copyAccountsGroupOptionsForEdit = computed(() => {
  const currentId = editingGroup.value?.id;
  const eligibleGroups = groups.value.filter(
    (g) =>
      canCopyAccountsFromGroup(editForm.platform, g.platform) &&
      (g.account_count || 0) > 0 &&
      g.id !== currentId,
  );
  return eligibleGroups.map((g) => ({
    value: g.id,
    label: copyAccountsGroupLabel(g),
  }));
});

const groups = ref<AdminGroup[]>([]);
const selectedGroupIds = ref<Array<string | number>>([]);
const bulkDeleting = ref(false);
const loading = ref(false);
type GroupUsageSummary = {
  today_cost: number;
  yesterday_cost: number;
  total_cost: number;
};

const usageMap = ref<Map<number, GroupUsageSummary>>(new Map());
const usageLoading = ref(false);
const capacityMap = ref<
  Map<
    number,
    {
      concurrencyUsed: number;
      concurrencyMax: number;
      sessionsUsed: number;
      sessionsMax: number;
      rpmUsed: number;
      rpmMax: number;
    }
  >
>(new Map());
const searchQuery = ref("");
const filters = reactive({
  platform: "",
  status: "",
  is_exclusive: "",
});
const groupFiltersExpanded = ref(false);
const activeGroupFilterCount = computed(() =>
  [filters.platform, filters.status, filters.is_exclusive].filter(Boolean).length,
);
const pagination = reactive({
  page: 1,
  page_size: getPersistedPageSize(),
  total: 0,
  pages: 0,
});
const sortState = reactive({
  sort_by: "sort_order",
  sort_order: "asc" as "asc" | "desc",
});

let abortController: AbortController | null = null;

const showCreateModal = ref(false);
const showEditModal = ref(false);
const createAdvancedOpen = ref(false);
const editAdvancedOpen = ref(false);
const showDeleteDialog = ref(false);
const pendingLiveForm = ref<"create" | "edit" | null>(null);
const showUnsupportedLiveConfirm = computed(
  () => pendingLiveForm.value !== null,
);
const liveCapability = ref<{ supported: boolean; reason?: string } | null>(null);
let liveCapabilityRequest: Promise<{
  supported: boolean;
  reason?: string;
}> | null = null;
const showSortModal = ref(false);
const submitting = ref(false);
const sortSubmitting = ref(false);
const editingGroup = ref<AdminGroup | null>(null);
const deletingGroup = ref<AdminGroup | null>(null);
const duplicatingGroupIds = reactive(new Set<number>());
const showRateMultipliersModal = ref(false);
const rateMultipliersGroup = ref<AdminGroup | null>(null);
const showRPMOverridesModal = ref(false);
const rpmOverridesGroup = ref<AdminGroup | null>(null);
const sortableGroups = ref<AdminGroup[]>([]);
type ConcreteGroupPlatform = Exclude<GroupPlatform, "composite">;
type CompositeRouteFormState = {
  public_model: string;
  match_type: CompositeRouteMatchType;
  target_platform: ConcreteGroupPlatform;
  upstream_model: string;
  endpoint: CompositeRouteEndpoint;
  priority: number;
  enabled: boolean;
  notes: string;
};

const showCompositeRoutesModal = ref(false);
const compositeRoutesGroup = ref<AdminGroup | null>(null);
const compositeRoutes = ref<CompositeModelRoute[]>([]);
const compositeRoutesLoading = ref(false);
const compositeRouteSaving = ref(false);
const compositeRouteEditingId = ref<number | null>(null);
const compositePreviewModel = ref("");
const compositePreviewEndpoint = ref<CompositeRouteEndpoint>("any");
const compositePreviewLoading = ref(false);
const compositePreviewDecision = ref<CompositeRouteDecision | null>(null);
const compositeRouteForm = reactive<CompositeRouteFormState>({
  public_model: "",
  match_type: "exact",
  target_platform: "openai",
  upstream_model: "",
  endpoint: "any",
  priority: 100,
  enabled: true,
  notes: "",
});
const createMessagesDispatchDefaults = createDefaultMessagesDispatchFormState();
const editMessagesDispatchDefaults = createDefaultMessagesDispatchFormState();
const createModelAllowlistState = reactive(createInitialModelAllowlistState());
const editModelAllowlistState = reactive(createInitialModelAllowlistState());
const createModelAllowlistLoading = ref(false);
const editModelAllowlistLoading = ref(false);
type ReasoningEffortPolicyFieldsExpose = {
  validate: () => boolean;
  resetValidation: () => void;
};
const createReasoningEffortPolicyRef = ref<ReasoningEffortPolicyFieldsExpose | null>(null);
const editReasoningEffortPolicyRef = ref<ReasoningEffortPolicyFieldsExpose | null>(null);

// 固定账号获取 Codex Model Manifest（仅 openai 分组编辑对话框）
type CodexManifestAccountsFieldExpose = {
  validate: () => boolean;
  resetValidation: () => void;
};
const editCodexManifestRef = ref<CodexManifestAccountsFieldExpose | null>(null);
const createCodexManifestDefaults = (): CodexModelsManifestConfig => ({
  enabled: false,
  account_ids: [],
  fallback_to_scheduler: false,
});
const editCodexManifestConfig = ref<CodexModelsManifestConfig>(createCodexManifestDefaults());
const editCodexManifestAccountNames = ref<Record<number, string>>({});
const modelAllowlistCandidatesTracker = createModelAllowlistCandidatesTracker();
const createModelAllowlistSelectedCount = computed(
  () => createModelAllowlistState.items.filter((item) => item.selected).length,
);
const editModelAllowlistSelectedCount = computed(
  () => editModelAllowlistState.items.filter((item) => item.selected).length,
);
const createAllowlistCustomEntry = ref("");
const editAllowlistCustomEntry = ref("");
const createAllowlistCustomErrorKey = ref<string | null>(null);
const editAllowlistCustomErrorKey = ref<string | null>(null);
const submitCreateAllowlistCustomEntry = () => {
  const error = addCustomModelAllowlistItem(
    createModelAllowlistState,
    createAllowlistCustomEntry.value,
  );
  if (error === null) {
    createAllowlistCustomEntry.value = "";
    createAllowlistCustomErrorKey.value = null;
  } else {
    createAllowlistCustomErrorKey.value = `admin.groups.modelAllowlist.errors.${error}`;
  }
};
const submitEditAllowlistCustomEntry = () => {
  const error = addCustomModelAllowlistItem(
    editModelAllowlistState,
    editAllowlistCustomEntry.value,
  );
  if (error === null) {
    editAllowlistCustomEntry.value = "";
    editAllowlistCustomErrorKey.value = null;
  } else {
    editAllowlistCustomErrorKey.value = `admin.groups.modelAllowlist.errors.${error}`;
  }
};

const createForm = reactive({
  name: "",
  description: "",
  platform: "anthropic" as GroupPlatform,
  rate_multiplier: 1.0,
  is_exclusive: false,
  subscription_type: "standard" as SubscriptionType,
  daily_limit_usd: null as number | null,
  weekly_limit_usd: null as number | null,
  monthly_limit_usd: null as number | null,
  long_context_pricing_enabled: true,
  force_openai_fast: false,
  free_openai_fast: false,
  model_pricing: [] as PricingFormEntry[],
  // 图片生成计费配置
  allow_image_generation: false,
  allow_batch_image_generation: false,
  image_rate_independent: false,
  image_rate_multiplier: 1,
  batch_image_discount_multiplier: 0.5,
  batch_image_hold_multiplier: 0.6,
  image_price_1k: null as number | null,
  image_price_2k: null as number | null,
  image_price_4k: null as number | null,
  // 视频生成计费配置（仅 Grok 平台）
  video_rate_independent: false,
  video_rate_multiplier: 1,
  video_price_480p: null as number | null,
  video_price_720p: null as number | null,
  video_price_1080p: null as number | null,
  video_model_prices: createVideoModelPricesForm(),
  // Codex 网页搜索按次计费（仅 openai 平台使用）；null = 使用默认价 0.01
  // Grok 安全拒绝单次扣费；null = 默认 0.1 USD，0 = 不扣费
  grok_moderation_refusal_price: null as number | null,
  web_search_price_per_call: null as number | null,
  search_price_per_1k: null as number | null,
  search_provider_base_url: '' as string,
  search_provider_api_key: '' as string,
  clearSearchProviderKey: false,
  searchProviderKeyConfigured: false,
  audio_realtime_price_per_min: null as number | null,
  audio_tts_price_per_million_chars: null as number | null,
  audio_stt_price_per_hour: null as number | null,
  // 高峰时段倍率配置
  peak_rate_enabled: false,
  peak_start: "",
  peak_end: "",
  peak_rate_multiplier: 1.0,
  // 分组利润控制（五个 token 平台）；界面按百分比输入，提交时转小数
  profit_control_enabled: false,
  profit_min_margin_percent: 0,
  profit_safety_buffer_percent: 0,
  // Claude Code 客户端限制（仅 anthropic 平台使用）
  claude_code_only: false,
  fallback_group_id: null as number | null,
  fallback_group_id_on_invalid_request: null as number | null,
  // OpenAI Messages 调度配置（仅 openai 平台使用）
  allow_messages_dispatch: false,
  // 跨协议转换（Anthropic 协议族 <-> OpenAI 协议族），默认关闭
  cross_protocol_conversion_enabled: false,
  return_requested_model: false,
  allow_live: false,
  opus_mapped_model: createMessagesDispatchDefaults.opus_mapped_model,
  sonnet_mapped_model: createMessagesDispatchDefaults.sonnet_mapped_model,
  haiku_mapped_model: createMessagesDispatchDefaults.haiku_mapped_model,
  exact_model_mappings: [] as MessagesDispatchMappingRow[],
  // 账号过滤控制（OpenAI/Antigravity 平台）
  require_oauth_only: false,
  require_privacy_set: false,
  // 模型路由开关
  model_routing_enabled: false,
  // 支持的模型系列（仅 antigravity 平台）
  supported_model_scopes: ["claude", "gemini_text", "gemini_image"] as string[],
  // MCP XML 协议注入开关（仅 antigravity 平台）
  mcp_xml_inject: true,
  // 从分组复制账号
  copy_accounts_from_group_ids: [] as number[],
  // 分组级 RPM 限制（每用户每分钟最大请求数；0 = 不限制）
  rpm_limit: 0 as number,
  max_reasoning_effort: "",
  max_reasoning_effort_over_limit: reasoningEffortOverLimitDowngrade,
  reasoning_effort_mappings: [] as ReasoningEffortMappingRow[],
});

// 简单账号类型（用于模型路由选择）
interface SimpleAccount {
  id: number;
  name: string;
}

// 模型路由规则类型
interface ModelRoutingRule {
  pattern: string;
  accounts: SimpleAccount[]; // 选中的账号对象数组
}

// 创建表单的模型路由规则
const createModelRoutingRules = ref<ModelRoutingRule[]>([]);

// 编辑表单的模型路由规则
const editModelRoutingRules = ref<ModelRoutingRule[]>([]);

// 规则对象稳定 key（避免使用 index 导致状态错位）
const resolveCreateRuleKey =
  createStableObjectKeyResolver<ModelRoutingRule>("create-rule");
const resolveEditRuleKey =
  createStableObjectKeyResolver<ModelRoutingRule>("edit-rule");
const resolveCreateMessagesDispatchRowKey =
  createStableObjectKeyResolver<MessagesDispatchMappingRow>(
    "create-messages-dispatch-row",
  );
const resolveEditMessagesDispatchRowKey =
  createStableObjectKeyResolver<MessagesDispatchMappingRow>(
    "edit-messages-dispatch-row",
  );

const getCreateRuleRenderKey = (rule: ModelRoutingRule) =>
  resolveCreateRuleKey(rule);
const getEditRuleRenderKey = (rule: ModelRoutingRule) =>
  resolveEditRuleKey(rule);
const getCreateMessagesDispatchRowKey = (row: MessagesDispatchMappingRow) =>
  resolveCreateMessagesDispatchRowKey(row);
const getEditMessagesDispatchRowKey = (row: MessagesDispatchMappingRow) =>
  resolveEditMessagesDispatchRowKey(row);

const getCreateRuleSearchKey = (rule: ModelRoutingRule) =>
  `create-${resolveCreateRuleKey(rule)}`;
const getEditRuleSearchKey = (rule: ModelRoutingRule) =>
  `edit-${resolveEditRuleKey(rule)}`;

const getRuleSearchKey = (rule: ModelRoutingRule, isEdit: boolean = false) => {
  return isEdit ? getEditRuleSearchKey(rule) : getCreateRuleSearchKey(rule);
};

// 账号搜索相关状态
const accountSearchKeyword = ref<Record<string, string>>({});
const accountSearchResults = ref<Record<string, SimpleAccount[]>>({});
const showAccountDropdown = ref<Record<string, boolean>>({});

const clearAccountSearchStateByKey = (key: string) => {
  delete accountSearchKeyword.value[key];
  delete accountSearchResults.value[key];
  delete showAccountDropdown.value[key];
};

const clearAllAccountSearchState = () => {
  accountSearchKeyword.value = {};
  accountSearchResults.value = {};
  showAccountDropdown.value = {};
};

const accountSearchRunner = useKeyedDebouncedSearch<SimpleAccount[]>({
  delay: 300,
  search: async (keyword, { signal }) => {
    const res = await adminAPI.accounts.list(
      1,
      20,
      {
        search: keyword,
        platform: "anthropic",
      },
      { signal },
    );
    return res.items.map((account) => ({ id: account.id, name: account.name }));
  },
  onSuccess: (key, result) => {
    accountSearchResults.value[key] = result;
  },
  onError: (key) => {
    accountSearchResults.value[key] = [];
  },
});

// 搜索账号（仅限 anthropic 平台）
const searchAccounts = (key: string) => {
  accountSearchRunner.trigger(key, accountSearchKeyword.value[key] || "");
};

const searchAccountsByRule = (
  rule: ModelRoutingRule,
  isEdit: boolean = false,
) => {
  searchAccounts(getRuleSearchKey(rule, isEdit));
};

// 选择账号
const selectAccount = (
  rule: ModelRoutingRule,
  account: SimpleAccount,
  isEdit: boolean = false,
) => {
  if (!rule) return;

  // 检查是否已选择
  if (!rule.accounts.some((a) => a.id === account.id)) {
    rule.accounts.push(account);
  }

  // 清空搜索
  const key = getRuleSearchKey(rule, isEdit);
  accountSearchKeyword.value[key] = "";
  showAccountDropdown.value[key] = false;
};

// 移除已选账号
const removeSelectedAccount = (
  rule: ModelRoutingRule,
  accountId: number,
  _isEdit: boolean = false,
) => {
  if (!rule) return;

  rule.accounts = rule.accounts.filter((a) => a.id !== accountId);
};

// 切换创建表单的模型系列选择
const toggleCreateScope = (scope: string) => {
  const idx = createForm.supported_model_scopes.indexOf(scope);
  if (idx === -1) {
    createForm.supported_model_scopes.push(scope);
  } else {
    createForm.supported_model_scopes.splice(idx, 1);
  }
};

// 切换编辑表单的模型系列选择
const toggleEditScope = (scope: string) => {
  const idx = editForm.supported_model_scopes.indexOf(scope);
  if (idx === -1) {
    editForm.supported_model_scopes.push(scope);
  } else {
    editForm.supported_model_scopes.splice(idx, 1);
  }
};

// 处理账号搜索输入框聚焦
const onAccountSearchFocus = (
  rule: ModelRoutingRule,
  isEdit: boolean = false,
) => {
  const key = getRuleSearchKey(rule, isEdit);
  showAccountDropdown.value[key] = true;
  // 如果没有搜索结果，触发一次搜索
  if (!accountSearchResults.value[key]?.length) {
    searchAccounts(key);
  }
};

// 添加创建表单的路由规则
const addCreateRoutingRule = () => {
  createModelRoutingRules.value.push({ pattern: "", accounts: [] });
};

// 删除创建表单的路由规则
const removeCreateRoutingRule = (rule: ModelRoutingRule) => {
  const index = createModelRoutingRules.value.indexOf(rule);
  if (index === -1) return;

  const key = getCreateRuleSearchKey(rule);
  accountSearchRunner.clearKey(key);
  clearAccountSearchStateByKey(key);
  createModelRoutingRules.value.splice(index, 1);
};

// 添加编辑表单的路由规则
const addEditRoutingRule = () => {
  editModelRoutingRules.value.push({ pattern: "", accounts: [] });
};

// 删除编辑表单的路由规则
const removeEditRoutingRule = (rule: ModelRoutingRule) => {
  const index = editModelRoutingRules.value.indexOf(rule);
  if (index === -1) return;

  const key = getEditRuleSearchKey(rule);
  accountSearchRunner.clearKey(key);
  clearAccountSearchStateByKey(key);
  editModelRoutingRules.value.splice(index, 1);
};

const resetModelAllowlistState = (
  state: typeof createModelAllowlistState,
  config?: Parameters<typeof createInitialModelAllowlistState>[0],
) => {
  const fresh = createInitialModelAllowlistState(config);
  state.enabled = fresh.enabled;
  state.savedModels = fresh.savedModels;
  state.items = fresh.items;
};

const loadModelAllowlistCandidates = async (
  mode: "create" | "edit",
  groupID: number,
  platform: GroupPlatform,
) => {
  if (authStore.isSimpleMode) return;
  const request = { mode, groupID, platform };
  const requestID = modelAllowlistCandidatesTracker.next(request);
  const state = mode === "create" ? createModelAllowlistState : editModelAllowlistState;
  const loadingRef = mode === "create" ? createModelAllowlistLoading : editModelAllowlistLoading;
  loadingRef.value = true;
  try {
    const models = await adminAPI.groups.getModelAllowlistCandidates(groupID, platform);
    if (!modelAllowlistCandidatesTracker.isCurrent(requestID, request)) {
      return;
    }
    setModelAllowlistCandidates(state, models);
  } catch (error) {
    if (!modelAllowlistCandidatesTracker.isCurrent(requestID, request)) {
      return;
    }
    console.error("Error loading group models list candidates:", error);
  } finally {
    if (modelAllowlistCandidatesTracker.isCurrent(requestID, request)) {
      loadingRef.value = false;
    }
  }
};

const moveCreateModelAllowlistItem = (fromIndex: number, toIndex: number) => {
  moveModelAllowlistItem(createModelAllowlistState, fromIndex, toIndex);
};

const moveEditModelAllowlistItem = (fromIndex: number, toIndex: number) => {
  moveModelAllowlistItem(editModelAllowlistState, fromIndex, toIndex);
};

// 将 UI 格式的路由规则转换为 API 格式
const convertRoutingRulesToApiFormat = (
  rules: ModelRoutingRule[],
): Record<string, number[]> | null => {
  const result: Record<string, number[]> = {};
  let hasValidRules = false;

  for (const rule of rules) {
    const pattern = rule.pattern.trim();
    if (!pattern) continue;

    const accountIds = rule.accounts.map((a) => a.id).filter((id) => id > 0);

    if (accountIds.length > 0) {
      result[pattern] = accountIds;
      hasValidRules = true;
    }
  }

  return hasValidRules ? result : null;
};

// 将 API 格式的路由规则转换为 UI 格式（需要加载账号名称）
const convertApiFormatToRoutingRules = async (
  apiFormat: Record<string, number[]> | null,
): Promise<ModelRoutingRule[]> => {
  if (!apiFormat) return [];

  const rules: ModelRoutingRule[] = [];
  for (const [pattern, accountIds] of Object.entries(apiFormat)) {
    // 加载账号信息
    const accounts: SimpleAccount[] = [];
    for (const id of accountIds) {
      try {
        const account = await adminAPI.accounts.getById(id);
        accounts.push({ id: account.id, name: account.name });
      } catch {
        // 如果账号不存在，仍然显示 ID
        accounts.push({ id, name: `#${id}` });
      }
    }
    rules.push({ pattern, accounts });
  }
  return rules;
};

const editForm = reactive({
  name: "",
  description: "",
  platform: "anthropic" as GroupPlatform,
  rate_multiplier: 1.0,
  is_exclusive: false,
  status: "active" as "active" | "inactive",
  subscription_type: "standard" as SubscriptionType,
  daily_limit_usd: null as number | null,
  weekly_limit_usd: null as number | null,
  monthly_limit_usd: null as number | null,
  long_context_pricing_enabled: true,
  force_openai_fast: false,
  free_openai_fast: false,
  model_pricing: [] as PricingFormEntry[],
  // 图片生成计费配置
  allow_image_generation: false,
  allow_batch_image_generation: false,
  image_rate_independent: false,
  image_rate_multiplier: 1,
  batch_image_discount_multiplier: 0.5,
  batch_image_hold_multiplier: 0.6,
  image_price_1k: null as number | null,
  image_price_2k: null as number | null,
  image_price_4k: null as number | null,
  // 视频生成计费配置（仅 Grok 平台）
  video_rate_independent: false,
  video_rate_multiplier: 1,
  video_price_480p: null as number | null,
  video_price_720p: null as number | null,
  video_price_1080p: null as number | null,
  video_model_prices: createVideoModelPricesForm(),
  // Codex 网页搜索按次计费（仅 openai 平台使用）；null = 使用默认价 0.01
  // Grok 安全拒绝单次扣费；null = 默认 0.1 USD，0 = 不扣费
  grok_moderation_refusal_price: null as number | null,
  web_search_price_per_call: null as number | null,
  search_price_per_1k: null as number | null,
  audio_realtime_price_per_min: null as number | null,
  search_provider_base_url: '' as string,
  search_provider_api_key: '' as string,
  clearSearchProviderKey: false,
  searchProviderKeyConfigured: false,
  audio_tts_price_per_million_chars: null as number | null,
  audio_stt_price_per_hour: null as number | null,
  // 高峰时段倍率配置
  peak_rate_enabled: false,
  peak_start: "",
  peak_end: "",
  peak_rate_multiplier: 1.0,
  // 分组利润控制（五个 token 平台）；界面按百分比输入，提交时转小数
  profit_control_enabled: false,
  profit_min_margin_percent: 0,
  profit_safety_buffer_percent: 0,
  // Claude Code 客户端限制（仅 anthropic 平台使用）
  claude_code_only: false,
  fallback_group_id: null as number | null,
  fallback_group_id_on_invalid_request: null as number | null,
  // OpenAI Messages 调度配置（仅 openai 平台使用）
  allow_messages_dispatch: false,
  // 跨协议转换（Anthropic 协议族 <-> OpenAI 协议族），默认关闭
  cross_protocol_conversion_enabled: false,
  return_requested_model: false,
  allow_live: false,
  default_mapped_model: '',
  opus_mapped_model: editMessagesDispatchDefaults.opus_mapped_model,
  sonnet_mapped_model: editMessagesDispatchDefaults.sonnet_mapped_model,
  haiku_mapped_model: editMessagesDispatchDefaults.haiku_mapped_model,
  exact_model_mappings: [] as MessagesDispatchMappingRow[],
  // 账号过滤控制（OpenAI/Antigravity 平台）
  require_oauth_only: false,
  require_privacy_set: false,
  // 模型路由开关
  model_routing_enabled: false,
  // 支持的模型系列（仅 antigravity 平台）
  supported_model_scopes: ["claude", "gemini_text", "gemini_image"] as string[],
  // MCP XML 协议注入开关（仅 antigravity 平台）
  mcp_xml_inject: true,
  // 从分组复制账号
  copy_accounts_from_group_ids: [] as number[],
  // 分组级 RPM 限制（每用户每分钟最大请求数；0 = 不限制）
  rpm_limit: 0 as number,
  max_reasoning_effort: "",
  max_reasoning_effort_over_limit: reasoningEffortOverLimitDowngrade,
  reasoning_effort_mappings: [] as ReasoningEffortMappingRow[],
});

type ImagePricingFormState = {
  platform: GroupPlatform;
  allow_image_generation: boolean;
  allow_batch_image_generation: boolean;
  rate_multiplier: number;
  image_rate_independent: boolean;
  image_rate_multiplier: number;
  batch_image_discount_multiplier: number;
  batch_image_hold_multiplier: number;
  image_price_1k: number | string | null;
  image_price_2k: number | string | null;
  image_price_4k: number | string | null;
  peak_rate_enabled: boolean;
  peak_start: string;
  peak_end: string;
  peak_rate_multiplier: number;
};

type VideoPricingFormState = {
  platform: GroupPlatform;
  rate_multiplier: number;
  video_rate_independent: boolean;
  video_rate_multiplier: number;
  video_price_480p: number | string | null;
  video_price_720p: number | string | null;
  video_price_1080p: number | string | null;
};

const imagePricingTiers = [
  { key: "image_price_1k", label: "1K" },
  { key: "image_price_2k", label: "2K" },
  { key: "image_price_4k", label: "4K" },
] as const;

const videoPricingTiers = [
  { key: "video_price_480p", label: "480p" },
  { key: "video_price_720p", label: "720p" },
  { key: "video_price_1080p", label: "1080p" },
] as const;

const normalizePreviewNumber = (value: number | string | null | undefined, fallback = 0) => {
  if (value === null || value === undefined || value === "") {
    return fallback;
  }
  const parsed = Number(value);
  return Number.isFinite(parsed) ? parsed : fallback;
};

const parsePreviewPrice = (value: number | string | null | undefined) => {
  if (value === null || value === undefined || value === "") {
    return null;
  }
  const parsed = Number(value);
  return Number.isFinite(parsed) && parsed >= 0 ? parsed : null;
};

const formatImagePricePreview = (value: number | string | null | undefined) => {
  if (value === null || value === undefined || value === "") {
    return t("admin.groups.imagePricing.notConfigured");
  }
  const price = Number(value);
  if (!Number.isFinite(price) || price < 0) {
    return t("admin.groups.imagePricing.notConfigured");
  }
  return `$${price.toFixed(6).replace(/0+$/, "").replace(/\.$/, "")}`;
};

const formatVideoPricePreview = (value: number | string | null | undefined) => {
  if (value === null || value === undefined || value === "") {
    return t("admin.groups.videoPricing.notConfigured");
  }
  const price = Number(value);
  if (!Number.isFinite(price) || price < 0) {
    return t("admin.groups.videoPricing.notConfigured");
  }
  return `$${price.toFixed(6).replace(/0+$/, "").replace(/\.$/, "")}`;
};

const buildImageFinalPricePreview = (form: ImagePricingFormState) => {
  const imageMultiplier = form.image_rate_independent
    ? normalizePreviewNumber(form.image_rate_multiplier, 1)
    : normalizePreviewNumber(form.rate_multiplier, 1);
  const multiplier = imageMultiplier;
  return imagePricingTiers.map((tier) => {
    const basePrice =
      parsePreviewPrice(form[tier.key]) ??
      getDefaultImagePreviewPrice(form.platform, tier.key);
    return {
      label: tier.label,
      value: basePrice !== null
        ? formatImagePricePreview(basePrice * multiplier)
        : t("admin.groups.imagePricing.notConfigured"),
    };
  });
};

const buildVideoFinalPricePreview = (form: VideoPricingFormState) => {
  const multiplier = form.video_rate_independent
    ? normalizePreviewNumber(form.video_rate_multiplier, 1)
    : normalizePreviewNumber(form.rate_multiplier, 1);
  return videoPricingTiers.map((tier) => {
    const basePrice =
      parsePreviewPrice(form[tier.key]) ??
      getDefaultVideoPreviewPrice(form.platform, tier.key);
    return {
      label: tier.label,
      value: basePrice !== null
        ? formatVideoPricePreview(basePrice * multiplier)
        : t("admin.groups.videoPricing.notConfigured"),
    };
  });
};

const createImageFinalPricePreview = computed(() =>
  buildImageFinalPricePreview(createForm),
);
const editImageFinalPricePreview = computed(() =>
  buildImageFinalPricePreview(editForm),
);
const createVideoFinalPricePreview = computed(() =>
  buildVideoFinalPricePreview(createForm),
);
const editVideoFinalPricePreview = computed(() =>
  buildVideoFinalPricePreview(editForm),
);

// Codex 网页搜索单次默认价（与后端 defaultWebSearchPricePerCall 一致，官方 $10/1000 次）
const DEFAULT_WEB_SEARCH_PRICE_PER_CALL = 0.01;

const buildWebSearchFinalPricePreview = (form: {
  web_search_price_per_call: number | string | null;
  rate_multiplier: number | string | null;
}) => {
  const basePrice =
    parsePreviewPrice(form.web_search_price_per_call) ??
    DEFAULT_WEB_SEARCH_PRICE_PER_CALL;
  const multiplier = normalizePreviewNumber(form.rate_multiplier, 1);
  return formatImagePricePreview(basePrice * multiplier);
};

const createWebSearchFinalPricePreview = computed(() =>
  buildWebSearchFinalPricePreview(createForm),
);
const editWebSearchFinalPricePreview = computed(() =>
  buildWebSearchFinalPricePreview(editForm),
);

const resetDisabledBatchImagePricing = (
  form: Pick<
    ImagePricingFormState,
    "platform" | "allow_image_generation" | "allow_batch_image_generation" | "batch_image_discount_multiplier" | "batch_image_hold_multiplier"
  >,
) => {
  if (form.platform !== "gemini" || !form.allow_image_generation) {
    form.allow_batch_image_generation = false;
  }
  if (!form.allow_batch_image_generation) {
    form.batch_image_discount_multiplier = 0.5;
    form.batch_image_hold_multiplier = 0.6;
  }
};

// 根据分组类型返回不同的删除确认消息
const deleteConfirmMessage = computed(() => {
  if (!deletingGroup.value) {
    return "";
  }
  if (deletingGroup.value.subscription_type === "subscription") {
    return t("admin.groups.deleteConfirmSubscription", {
      name: deletingGroup.value.name,
    });
  }
  return t("admin.groups.deleteConfirm", { name: deletingGroup.value.name });
});

const loadLiveCapability = async () => {
  if (liveCapability.value) return liveCapability.value;
  if (!liveCapabilityRequest) {
    liveCapabilityRequest = adminAPI.groups
      .getLiveCapability()
      .catch(() => ({ supported: false }))
      .finally(() => {
        liveCapabilityRequest = null;
      });
  }
  liveCapability.value = await liveCapabilityRequest;
  return liveCapability.value ?? { supported: false };
};

const toggleLive = async (target: "create" | "edit") => {
  const form = target === "create" ? createForm : editForm;
  if (form.allow_live) {
    form.allow_live = false;
    return;
  }
  const capability = await loadLiveCapability();
  if (capability.supported) {
    form.allow_live = true;
    return;
  }
  pendingLiveForm.value = target;
};

const confirmUnsupportedLive = () => {
  if (pendingLiveForm.value === "create") createForm.allow_live = true;
  if (pendingLiveForm.value === "edit") editForm.allow_live = true;
  pendingLiveForm.value = null;
};

const cancelUnsupportedLive = () => {
  pendingLiveForm.value = null;
};

const loadGroups = async () => {
  if (abortController) {
    abortController.abort();
  }
  const currentController = new AbortController();
  abortController = currentController;
  const { signal } = currentController;
  loading.value = true;
  try {
    const response = await adminAPI.groups.list(
      pagination.page,
      pagination.page_size,
      {
        platform: (filters.platform as GroupPlatform) || undefined,
        status: filters.status as any,
        is_exclusive: !authStore.isSimpleMode && filters.is_exclusive
          ? filters.is_exclusive === "true"
          : undefined,
        search: searchQuery.value.trim() || undefined,
        sort_by: sortState.sort_by,
        sort_order: sortState.sort_order,
      },
      { signal },
    );
    if (signal.aborted) return;
    groups.value = response.items;
    selectedGroupIds.value = [];
    pagination.total = response.total;
    pagination.pages = response.pages;
    if (hasVisibleUsageSummaryConsumer.value) {
      loadUsageSummary();
    } else {
      usageLoading.value = false;
    }
    if (hasVisibleCapacityColumn.value) {
      loadCapacitySummary();
    }
  } catch (error: any) {
    if (
      signal.aborted ||
      error?.name === "AbortError" ||
      error?.code === "ERR_CANCELED"
    ) {
      return;
    }
    appStore.showError(t("admin.groups.failedToLoad"));
    console.error("Error loading groups:", error);
  } finally {
    if (abortController === currentController && !signal.aborted) {
      loading.value = false;
    }
  }
};

const formatCost = (cost: number): string => {
  if (cost >= 1000) return cost.toFixed(0);
  if (cost >= 100) return cost.toFixed(1);
  return cost.toFixed(2);
};

const formatUsd = (cost: number | null | undefined): string =>
  `$${formatCost(cost ?? 0)}`;

const getQuotaUsageClass = (
  used: number,
  limit: number | null | undefined,
): string => {
  if (!limit || limit <= 0) {
    return "font-medium text-gray-700 dark:text-gray-300";
  }
  const ratio = used / limit;
  if (ratio >= 1) {
    return "font-semibold text-red-600 dark:text-red-400";
  }
  if (ratio >= 0.8) {
    return "font-semibold text-amber-600 dark:text-amber-400";
  }
  return "font-medium text-gray-700 dark:text-gray-300";
};

const loadUsageSummary = async () => {
  if (!hasVisibleUsageSummaryConsumer.value) {
    usageLoading.value = false;
    return;
  }
  usageLoading.value = true;
  try {
    const data = await adminAPI.groups.getUsageSummary();
    const map = new Map<number, GroupUsageSummary>();
    for (const item of data) {
      map.set(item.group_id, {
        today_cost: item.today_cost,
        yesterday_cost: item.yesterday_cost,
        total_cost: item.total_cost,
      });
    }
    usageMap.value = map;
  } catch (error) {
    console.error("Error loading group usage summary:", error);
  } finally {
    usageLoading.value = false;
  }
};

const loadCapacitySummary = async () => {
  if (!hasVisibleCapacityColumn.value) {
    return;
  }
  try {
    const data = await adminAPI.groups.getCapacitySummary();
    const map = new Map<
      number,
      {
        concurrencyUsed: number;
        concurrencyMax: number;
        sessionsUsed: number;
        sessionsMax: number;
        rpmUsed: number;
        rpmMax: number;
      }
    >();
    for (const item of data) {
      map.set(item.group_id, {
        concurrencyUsed: item.concurrency_used,
        concurrencyMax: item.concurrency_max,
        sessionsUsed: item.sessions_used,
        sessionsMax: item.sessions_max,
        rpmUsed: item.rpm_used,
        rpmMax: item.rpm_max,
      });
    }
    capacityMap.value = map;
  } catch (error) {
    console.error("Error loading group capacity summary:", error);
  }
};

let searchTimeout: ReturnType<typeof setTimeout>;
const handleSearch = () => {
  clearTimeout(searchTimeout);
  searchTimeout = setTimeout(() => {
    pagination.page = 1;
    loadGroups();
  }, 300);
};

const handlePageChange = (page: number) => {
  pagination.page = page;
  loadGroups();
};

const handlePageSizeChange = (pageSize: number) => {
  pagination.page_size = pageSize;
  pagination.page = 1;
  loadGroups();
};

const handleSort = (key: string, order: 'asc' | 'desc') => {
  sortState.sort_by = key;
  sortState.sort_order = order;
  pagination.page = 1;
  loadGroups();
};

const openCreateModal = () => {
  showCreateModal.value = true;
  loadModelAllowlistCandidates("create", 0, createForm.platform);
};

const closeCreateModal = () => {
  showCreateModal.value = false;
  createModelRoutingRules.value.forEach((rule) => {
    accountSearchRunner.clearKey(getCreateRuleSearchKey(rule));
  });
  clearAllAccountSearchState();
  createForm.name = "";
  createForm.description = "";
  createForm.platform = "anthropic";
  createForm.rate_multiplier = 1.0;
  createForm.is_exclusive = false;
  createForm.subscription_type = "standard";
  createForm.daily_limit_usd = null;
  createForm.weekly_limit_usd = null;
  createForm.monthly_limit_usd = null;
  createForm.allow_image_generation = false;
  createForm.allow_batch_image_generation = false;
  createForm.image_rate_independent = false;
  createForm.image_rate_multiplier = 1;
  createForm.batch_image_discount_multiplier = 0.5;
  createForm.batch_image_hold_multiplier = 0.6;
  createForm.image_price_1k = null;
  createForm.image_price_2k = null;
  createForm.image_price_4k = null;
  createForm.video_rate_independent = false;
  createForm.video_rate_multiplier = 1;
  createForm.video_price_480p = null;
  createForm.video_price_720p = null;
  createForm.video_price_1080p = null;
  createForm.video_model_prices = createVideoModelPricesForm();
  createForm.long_context_pricing_enabled = true;
  createForm.force_openai_fast = false;
  createForm.free_openai_fast = false;
  createForm.model_pricing = [];
  createForm.web_search_price_per_call = null;
  createForm.search_price_per_1k = null;
  createForm.grok_moderation_refusal_price = null;
  createForm.search_provider_base_url = '';
  createForm.search_provider_api_key = '';
  createForm.audio_realtime_price_per_min = null;
  createForm.audio_tts_price_per_million_chars = null;
  createForm.audio_stt_price_per_hour = null;
  createForm.peak_rate_enabled = false;
  createForm.peak_start = "";
  createForm.peak_end = "";
  createForm.peak_rate_multiplier = 1.0;
  createForm.profit_control_enabled = false;
  createForm.profit_min_margin_percent = 0;
  createForm.profit_safety_buffer_percent = 0;
  createForm.claude_code_only = false;
  createForm.return_requested_model = false;
  createForm.fallback_group_id = null;
  createForm.fallback_group_id_on_invalid_request = null;
  resetMessagesDispatchFormState(createForm);
  createForm.allow_live = false;
  createForm.require_oauth_only = false;
  createForm.require_privacy_set = false;
  createForm.supported_model_scopes = ["claude", "gemini_text", "gemini_image"];
  createForm.mcp_xml_inject = true;
  createForm.copy_accounts_from_group_ids = [];
  createForm.rpm_limit = 0;
  createForm.max_reasoning_effort = "";
  createForm.max_reasoning_effort_over_limit = reasoningEffortOverLimitDowngrade;
  createForm.reasoning_effort_mappings = [];
  createReasoningEffortPolicyRef.value?.resetValidation();
  resetModelAllowlistState(createModelAllowlistState);
  createModelRoutingRules.value = [];
};

const normalizeOptionalLimit = (
  value: number | string | null | undefined,
): number | null => {
  if (value === null || value === undefined) {
    return null;
  }

  if (typeof value === "string") {
    const trimmed = value.trim();
    if (!trimmed) {
      return null;
    }
    const parsed = Number(trimmed);
    return Number.isFinite(parsed) && parsed >= 0 ? parsed : null;
  }

  return Number.isFinite(value) && value >= 0 ? value : null;
};

const normalizeRateMultiplier = (
  value: number | string | null | undefined,
): number => {
  if (value === null || value === undefined || value === "") {
    return 1;
  }
  const parsed = Number(value);
  return Number.isFinite(parsed) && parsed >= 0 ? parsed : 1;
};

// 利润控制表单辅助（换算与校验逻辑见 groupsProfitControl.ts，便于单测）。
const percentToDecimal = profitPercentToDecimal;
const decimalToPercent = profitDecimalToPercent;

const validateProfitControlForm = (form: ProfitControlFormState): boolean => {
  const errorKey = validateProfitControlFormState(form);
  if (errorKey) {
    appStore.showError(t(`admin.groups.profitControl.${errorKey}`));
    return false;
  }
  return true;
};

const handleCreateGroup = async () => {
  if (!createForm.name.trim()) {
    appStore.showError(t("admin.groups.nameRequired"));
    return;
  }
  if (
    supportsReasoningEffortPolicyPlatform(createForm.platform) &&
    createReasoningEffortPolicyRef.value &&
    !createReasoningEffortPolicyRef.value.validate()
  ) {
    return;
  }
  if (!validateProfitControlForm(createForm)) {
    return;
  }
  // 模型白名单：开启且没有任何条目时阻止提交，与后端 400 对齐。
  if (
    createModelAllowlistState.enabled &&
    createModelAllowlistSelectedCount.value === 0
  ) {
    appStore.showError(t("admin.groups.modelAllowlist.emptySelectionError"));
    return;
  }
  submitting.value = true;
  try {
    const {
      video_model_prices: _createFormVideoModelPrices,
      ...createGroupForm
    } = createForm;
    const videoModelPrices = serializeVideoModelPrices(
      createForm.video_model_prices,
    );
    // 构建请求数据，包含模型路由配置
    const requestData = {
      ...createGroupForm,
      force_openai_fast: normalizeGroupOpenAIFast(
        createForm.platform,
        createForm.force_openai_fast,
      ),
      free_openai_fast: normalizeGroupOpenAIFast(
        createForm.platform,
        createForm.free_openai_fast,
      ),
      model_pricing: groupPricingToAPI(
        createForm.model_pricing,
        createForm.platform,
      ),
      daily_limit_usd: normalizeOptionalLimit(
        createForm.daily_limit_usd as number | string | null,
      ),
      weekly_limit_usd: normalizeOptionalLimit(
        createForm.weekly_limit_usd as number | string | null,
      ),
      monthly_limit_usd: normalizeOptionalLimit(
        createForm.monthly_limit_usd as number | string | null,
      ),
      ...(Object.keys(videoModelPrices).length > 0
        ? { video_model_prices: videoModelPrices }
        : {}),
      model_routing: convertRoutingRulesToApiFormat(
        createModelRoutingRules.value,
      ),
      model_allowlist: buildModelAllowlistConfig(createModelAllowlistState),
      // 创建时固定账号 manifest 固定发送关闭状态（后端创建路径禁止开启）
      codex_models_manifest_config: createCodexManifestDefaults(),
      supported_model_scopes: normalizeSupportedModelScopesForPlatform(
        createForm.platform,
        createForm.supported_model_scopes,
      ),
      cross_protocol_conversion_enabled:
        createForm.cross_protocol_conversion_enabled,
      messages_dispatch_model_config:
        createForm.platform === "openai"
          ? messagesDispatchFormStateToConfig({
              allow_messages_dispatch: createForm.allow_messages_dispatch,
              opus_mapped_model: createForm.opus_mapped_model,
              sonnet_mapped_model: createForm.sonnet_mapped_model,
              haiku_mapped_model: createForm.haiku_mapped_model,
              exact_model_mappings: createForm.exact_model_mappings,
            })
          : undefined,
      reasoning_effort_mappings: reasoningEffortMappingsToAPI(
        createForm.reasoning_effort_mappings,
      ),
      // 利润控制：界面百分比转小数提交；仅五个 token 平台可启用
      profit_control_enabled:
        isProfitControlPlatform(createForm.platform) &&
        createForm.profit_control_enabled,
      profit_min_margin: percentToDecimal(createForm.profit_min_margin_percent),
      profit_safety_buffer: percentToDecimal(
        createForm.profit_safety_buffer_percent,
      ),
    };
    delete (requestData as Record<string, unknown>).profit_min_margin_percent;
    delete (requestData as Record<string, unknown>).profit_safety_buffer_percent;
    // v-model.number 清空输入框时产生 ""，转为 null 让后端设为无限制
    const emptyToNull = (v: any) => (v === "" ? null : v);
    requestData.daily_limit_usd = emptyToNull(requestData.daily_limit_usd);
    requestData.weekly_limit_usd = emptyToNull(requestData.weekly_limit_usd);
    requestData.monthly_limit_usd = emptyToNull(requestData.monthly_limit_usd);
    requestData.image_rate_multiplier = normalizeRateMultiplier(
      requestData.image_rate_multiplier,
    );
    resetDisabledBatchImagePricing(requestData);
    requestData.batch_image_discount_multiplier = normalizeRateMultiplier(
      requestData.batch_image_discount_multiplier,
    );
    requestData.batch_image_hold_multiplier = normalizeRateMultiplier(
      requestData.batch_image_hold_multiplier,
    );
    requestData.video_rate_multiplier = normalizeRateMultiplier(
      requestData.video_rate_multiplier,
    );
    // 媒体价格输入清空时 v-model.number 产生 ""，直接提交会被后端 *float64 反序列化拒绝（400），
    // 创建时按"未配置"（null）处理。
    requestData.image_price_1k = emptyToNull(requestData.image_price_1k);
    requestData.image_price_2k = emptyToNull(requestData.image_price_2k);
    requestData.image_price_4k = emptyToNull(requestData.image_price_4k);
    requestData.video_price_480p = emptyToNull(requestData.video_price_480p);
    requestData.video_price_720p = emptyToNull(requestData.video_price_720p);
    requestData.video_price_1080p = emptyToNull(requestData.video_price_1080p);
    requestData.grok_moderation_refusal_price = emptyToNull(
      requestData.grok_moderation_refusal_price,
    );
    requestData.search_price_per_1k = emptyToNull(
      requestData.search_price_per_1k,
    );
    requestData.audio_realtime_price_per_min = emptyToNull(
      requestData.audio_realtime_price_per_min,
    );
    requestData.audio_tts_price_per_million_chars = emptyToNull(
      requestData.audio_tts_price_per_million_chars,
    );
    requestData.audio_stt_price_per_hour = emptyToNull(
      requestData.audio_stt_price_per_hour,
    );
    requestData.web_search_price_per_call = emptyToNull(
      requestData.web_search_price_per_call,
    );
    // 搜索专用凭据：新建时留空即「不配置」。
    requestData.search_provider_base_url = (createForm.search_provider_base_url ?? "").trim();
    requestData.search_provider_api_key = (createForm.search_provider_api_key ?? "").trim();
    requestData.peak_rate_enabled = createForm.peak_rate_enabled;
    requestData.peak_start = createForm.peak_start;
    requestData.peak_end = createForm.peak_end;
    requestData.peak_rate_multiplier = normalizeRateMultiplier(
      createForm.peak_rate_multiplier,
    );
    const payload = authStore.isSimpleMode
      ? {
          name: createForm.name,
          description: createForm.description,
          platform: createForm.platform,
        }
      : requestData;
    await adminAPI.groups.create(payload);
    appStore.showSuccess(t("admin.groups.groupCreated"));
    closeCreateModal();
    loadGroups();
    // Only advance tour if active, on submit step, and creation succeeded
    if (onboardingStore.isCurrentStep('[data-tour="group-form-submit"]')) {
      onboardingStore.nextStep(500);
    }
  } catch (error: any) {
    appStore.showError(
      extractApiErrorMessage(error, t("admin.groups.failedToCreate")),
    );
    console.error("Error creating group:", error);
    // Don't advance tour on error
  } finally {
    submitting.value = false;
  }
};

const handleEdit = async (group: AdminGroup) => {
  editingGroup.value = group;
  editForm.name = group.name;
  editForm.description = group.description || "";
  editForm.platform = group.platform;
  editForm.rate_multiplier = group.rate_multiplier;
  editForm.is_exclusive = group.is_exclusive;
  editForm.status = group.status;
  editForm.subscription_type = group.subscription_type || "standard";
  editForm.daily_limit_usd = group.daily_limit_usd;
  editForm.weekly_limit_usd = group.weekly_limit_usd;
  editForm.monthly_limit_usd = group.monthly_limit_usd;
  editForm.long_context_pricing_enabled =
    group.long_context_pricing_enabled ?? true;
  editForm.force_openai_fast = group.force_openai_fast ?? false;
  editForm.free_openai_fast = group.free_openai_fast ?? false;
  editForm.model_pricing = groupPricingFromAPI(group.model_pricing);
  editForm.allow_image_generation = group.allow_image_generation ?? false;
  editForm.allow_batch_image_generation =
    group.allow_batch_image_generation ?? false;
  editForm.image_rate_independent = group.image_rate_independent ?? false;
  editForm.image_rate_multiplier = group.image_rate_multiplier ?? 1;
  editForm.batch_image_discount_multiplier =
    group.batch_image_discount_multiplier ?? 0.5;
  editForm.batch_image_hold_multiplier = group.batch_image_hold_multiplier ?? 0.6;
  editForm.image_price_1k = group.image_price_1k;
  editForm.image_price_2k = group.image_price_2k;
  editForm.image_price_4k = group.image_price_4k;
  editForm.video_rate_independent = group.video_rate_independent ?? false;
  editForm.video_rate_multiplier = group.video_rate_multiplier ?? 1;
  editForm.video_price_480p = group.video_price_480p;
  editForm.video_price_720p = group.video_price_720p;
  editForm.video_price_1080p = group.video_price_1080p;
  editForm.video_model_prices = createVideoModelPricesForm(
    group.video_model_prices,
  );
  editForm.web_search_price_per_call = group.web_search_price_per_call ?? null;
  editForm.search_price_per_1k = group.search_price_per_1k ?? null;
  editForm.grok_moderation_refusal_price = group.grok_moderation_refusal_price ?? null;
  editForm.search_provider_base_url = group.search_provider_base_url ?? '';
  editForm.search_provider_api_key = '';
  editForm.clearSearchProviderKey = false;
  // Server never returns the key itself, only whether one is stored.
  editForm.searchProviderKeyConfigured = group.search_provider_api_key_set === true;
  // Server never returns the key itself, only whether one is stored.
  editForm.searchProviderKeyConfigured = group.search_provider_api_key_set === true;
  editForm.audio_realtime_price_per_min = group.audio_realtime_price_per_min ?? null;
  editForm.audio_tts_price_per_million_chars = group.audio_tts_price_per_million_chars ?? null;
  editForm.audio_stt_price_per_hour = group.audio_stt_price_per_hour ?? null;
  editForm.peak_rate_enabled = group.peak_rate_enabled ?? false;
  editForm.peak_start = group.peak_start ?? "";
  editForm.peak_end = group.peak_end ?? "";
  editForm.peak_rate_multiplier = group.peak_rate_multiplier ?? 1.0;
  editForm.profit_control_enabled = group.profit_control_enabled ?? false;
  editForm.profit_min_margin_percent = decimalToPercent(
    group.profit_min_margin ?? 0,
  );
  editForm.profit_safety_buffer_percent = decimalToPercent(
    group.profit_safety_buffer ?? 0,
  );
  editForm.claude_code_only = group.claude_code_only || false;
  editForm.fallback_group_id = group.fallback_group_id;
  editForm.fallback_group_id_on_invalid_request =
    group.fallback_group_id_on_invalid_request;
  const messagesDispatchFormState = messagesDispatchConfigToFormState(
    group.messages_dispatch_model_config,
  );
  editForm.allow_messages_dispatch =
    group.allow_messages_dispatch ||
    messagesDispatchFormState.allow_messages_dispatch;
  editForm.allow_live = group.allow_live ?? false;
  // 必须回填已保存的值：否则编辑任意分组都显示"关"，一点保存就把开关静默关掉。
  editForm.cross_protocol_conversion_enabled =
    group.cross_protocol_conversion_enabled ?? false;
  editForm.return_requested_model = group.return_requested_model ?? false;
  editForm.opus_mapped_model = messagesDispatchFormState.opus_mapped_model;
  editForm.sonnet_mapped_model = messagesDispatchFormState.sonnet_mapped_model;
  editForm.haiku_mapped_model = messagesDispatchFormState.haiku_mapped_model;
  editForm.exact_model_mappings =
    messagesDispatchFormState.exact_model_mappings;
  editForm.require_oauth_only = group.require_oauth_only ?? false;
  editForm.require_privacy_set = group.require_privacy_set ?? false;
  editForm.model_routing_enabled = group.model_routing_enabled || false;
  editForm.supported_model_scopes = group.supported_model_scopes || [
    "claude",
    "gemini_text",
    "gemini_image",
  ];
  editForm.mcp_xml_inject = group.mcp_xml_inject ?? true;
  editForm.copy_accounts_from_group_ids = []; // 复制账号字段每次编辑时重置为空
  editForm.rpm_limit = group.rpm_limit ?? 0;
  editForm.max_reasoning_effort = normalizeReasoningEffortForPlatform(
    group.platform,
    group.max_reasoning_effort,
  );
  editForm.max_reasoning_effort_over_limit = normalizeReasoningEffortOverLimit(
    group.max_reasoning_effort_over_limit,
  );
  editForm.reasoning_effort_mappings = reasoningEffortMappingsToRows(
    group.reasoning_effort_mappings,
    group.platform,
  );
  resetModelAllowlistState(editModelAllowlistState, group.model_allowlist);
  // 固定账号 manifest 配置：回显配置并异步解析已存账号名称（失败显示 #<id>）
  const savedCodexManifestConfig =
    group.codex_models_manifest_config ?? createCodexManifestDefaults();
  editCodexManifestConfig.value = {
    enabled: savedCodexManifestConfig.enabled ?? false,
    account_ids: [...(savedCodexManifestConfig.account_ids ?? [])],
    fallback_to_scheduler: savedCodexManifestConfig.fallback_to_scheduler ?? false,
  };
  editCodexManifestAccountNames.value = {};
  for (const id of editCodexManifestConfig.value.account_ids) {
    adminAPI.accounts
      .getById(id)
      .then((account) => {
        editCodexManifestAccountNames.value = {
          ...editCodexManifestAccountNames.value,
          [id]: account.name,
        };
      })
      .catch(() => {
        // 无法解析名称时由组件回退展示 #<id>，提示管理员清理脏 ID。
      });
  }
  // 加载模型路由规则（异步加载账号名称）
  editModelRoutingRules.value = await convertApiFormatToRoutingRules(
    group.model_routing,
  );
  loadModelAllowlistCandidates("edit", group.id, group.platform);
  // Open "advanced options" when the group already uses one of them, so nothing set stays hidden.
  editAdvancedOpen.value =
    editForm.return_requested_model ||
    editForm.allow_live ||
    editForm.require_oauth_only ||
    editForm.require_privacy_set ||
    editForm.fallback_group_id_on_invalid_request != null ||
    editForm.model_routing_enabled ||
    (editForm.platform === "antigravity" && !editForm.mcp_xml_inject);
  showEditModal.value = true;
};

const closeEditModal = () => {
  editModelRoutingRules.value.forEach((rule) => {
    accountSearchRunner.clearKey(getEditRuleSearchKey(rule));
  });
  clearAllAccountSearchState();
  showEditModal.value = false;
  editingGroup.value = null;
  editForm.max_reasoning_effort = "";
  editForm.max_reasoning_effort_over_limit = reasoningEffortOverLimitDowngrade;
  editForm.reasoning_effort_mappings = [];
  editReasoningEffortPolicyRef.value?.resetValidation();
  editModelRoutingRules.value = [];
  editForm.copy_accounts_from_group_ids = [];
  editForm.peak_rate_enabled = false;
  editForm.peak_start = "";
  editForm.peak_end = "";
  editForm.peak_rate_multiplier = 1.0;
  editForm.profit_control_enabled = false;
  editForm.profit_min_margin_percent = 0;
  editForm.profit_safety_buffer_percent = 0;
  editForm.video_rate_independent = false;
  editForm.video_rate_multiplier = 1;
  editForm.video_price_480p = null;
  editForm.video_price_720p = null;
  editForm.video_price_1080p = null;
  editForm.video_model_prices = createVideoModelPricesForm();
  editForm.long_context_pricing_enabled = true;
  editForm.force_openai_fast = false;
  editForm.free_openai_fast = false;
  editForm.model_pricing = [];
  editForm.web_search_price_per_call = null;
  editForm.search_price_per_1k = null;
  editForm.grok_moderation_refusal_price = null;
  editForm.search_provider_base_url = '';
  editForm.search_provider_api_key = '';
  editForm.clearSearchProviderKey = false;
  editForm.searchProviderKeyConfigured = false;
  editForm.searchProviderKeyConfigured = false;
  editForm.audio_realtime_price_per_min = null;
  editForm.audio_tts_price_per_million_chars = null;
  editForm.audio_stt_price_per_hour = null;
  resetMessagesDispatchFormState(editForm);
  editForm.allow_live = false;
  editForm.cross_protocol_conversion_enabled = false;
  editForm.return_requested_model = false;
  resetModelAllowlistState(editModelAllowlistState);
  editCodexManifestConfig.value = createCodexManifestDefaults();
  editCodexManifestAccountNames.value = {};
  editCodexManifestRef.value?.resetValidation?.();
};

const handleUpdateGroup = async () => {
  if (!editingGroup.value) return;
  if (!editForm.name.trim()) {
    appStore.showError(t("admin.groups.nameRequired"));
    return;
  }
  if (
    supportsReasoningEffortPolicyPlatform(editForm.platform) &&
    editReasoningEffortPolicyRef.value &&
    !editReasoningEffortPolicyRef.value.validate()
  ) {
    return;
  }
  if (!validateProfitControlForm(editForm)) {
    return;
  }
  // 模型白名单：开启且没有任何条目时阻止提交，与后端 400 对齐。
  if (
    editModelAllowlistState.enabled &&
    editModelAllowlistSelectedCount.value === 0
  ) {
    appStore.showError(t("admin.groups.modelAllowlist.emptySelectionError"));
    return;
  }
  // 固定账号 manifest：开启后至少一个账号，前端阻止提交并提示。
  if (
    editForm.platform === "openai" &&
    editCodexManifestConfig.value.enabled &&
    editCodexManifestConfig.value.account_ids.length === 0
  ) {
    appStore.showError(t("admin.groups.codexModelsManifest.selectAtLeastOne"));
    editCodexManifestRef.value?.validate();
    return;
  }

  submitting.value = true;
  try {
    // 转换 fallback_group_id: null -> 0 (后端使用 0 表示清除)
    const payload = {
      ...editForm,
      force_openai_fast: normalizeGroupOpenAIFast(
        editForm.platform,
        editForm.force_openai_fast,
      ),
      free_openai_fast: normalizeGroupOpenAIFast(
        editForm.platform,
        editForm.free_openai_fast,
      ),
      model_pricing: groupPricingToAPI(
        editForm.model_pricing,
        editForm.platform,
      ),
      daily_limit_usd: normalizeOptionalLimit(
        editForm.daily_limit_usd as number | string | null,
      ),
      weekly_limit_usd: normalizeOptionalLimit(
        editForm.weekly_limit_usd as number | string | null,
      ),
      monthly_limit_usd: normalizeOptionalLimit(
        editForm.monthly_limit_usd as number | string | null,
      ),
      video_model_prices: serializeVideoModelPrices(
        editForm.video_model_prices,
      ),
      fallback_group_id:
        editForm.fallback_group_id === null ? 0 : editForm.fallback_group_id,
      fallback_group_id_on_invalid_request:
        editForm.fallback_group_id_on_invalid_request === null
          ? 0
          : editForm.fallback_group_id_on_invalid_request,
      model_routing: convertRoutingRulesToApiFormat(
        editModelRoutingRules.value,
      ),
      model_allowlist: buildModelAllowlistConfig(editModelAllowlistState),
      // 非 openai 平台提交关闭状态，与后端归一化一致
      codex_models_manifest_config:
        editForm.platform === "openai"
          ? {
              enabled: editCodexManifestConfig.value.enabled,
              account_ids: [...editCodexManifestConfig.value.account_ids],
              fallback_to_scheduler: editCodexManifestConfig.value.fallback_to_scheduler,
            }
          : createCodexManifestDefaults(),
      supported_model_scopes: normalizeSupportedModelScopesForPlatform(
        editForm.platform,
        editForm.supported_model_scopes,
      ),
      messages_dispatch_model_config:
        editForm.platform === "openai"
          ? messagesDispatchFormStateToConfig({
              allow_messages_dispatch: editForm.allow_messages_dispatch,
              opus_mapped_model: editForm.opus_mapped_model,
              sonnet_mapped_model: editForm.sonnet_mapped_model,
              haiku_mapped_model: editForm.haiku_mapped_model,
              exact_model_mappings: editForm.exact_model_mappings,
            })
          : undefined,
      cross_protocol_conversion_enabled:
        editForm.cross_protocol_conversion_enabled,
      reasoning_effort_mappings: reasoningEffortMappingsToAPI(
        editForm.reasoning_effort_mappings,
      ),
      // 利润控制：界面百分比转小数提交；仅五个 token 平台可启用
      profit_control_enabled:
        isProfitControlPlatform(editForm.platform) &&
        editForm.profit_control_enabled,
      profit_min_margin: percentToDecimal(editForm.profit_min_margin_percent),
      profit_safety_buffer: percentToDecimal(
        editForm.profit_safety_buffer_percent,
      ),
    };
    delete (payload as Record<string, unknown>).profit_min_margin_percent;
    delete (payload as Record<string, unknown>).profit_safety_buffer_percent;
    // v-model.number 清空输入框时产生 ""，转为 null 让后端设为无限制
    const emptyToNull = (v: any) => (v === "" ? null : v);
    payload.daily_limit_usd = emptyToNull(payload.daily_limit_usd);
    payload.weekly_limit_usd = emptyToNull(payload.weekly_limit_usd);
    payload.monthly_limit_usd = emptyToNull(payload.monthly_limit_usd);
    payload.image_rate_multiplier = normalizeRateMultiplier(
      payload.image_rate_multiplier,
    );
    resetDisabledBatchImagePricing(payload);
    payload.batch_image_discount_multiplier = normalizeRateMultiplier(
      payload.batch_image_discount_multiplier,
    );
    payload.batch_image_hold_multiplier = normalizeRateMultiplier(
      payload.batch_image_hold_multiplier,
    );
    payload.video_rate_multiplier = normalizeRateMultiplier(
      payload.video_rate_multiplier,
    );
    // 媒体价格输入清空时 v-model.number 产生 ""，直接提交会被后端 *float64 反序列化拒绝（400）。
    // 更新语义中 null 表示"不修改"，因此清空后的字段发送 -1：后端 normalizePrice 将负价归一为
    // NULL，从而真正清除已配置的价格。
    const emptyPriceToClear = (v: any) => (v === "" || v === null ? -1 : v);
    payload.image_price_1k = emptyPriceToClear(payload.image_price_1k);
    payload.image_price_2k = emptyPriceToClear(payload.image_price_2k);
    payload.image_price_4k = emptyPriceToClear(payload.image_price_4k);
    payload.video_price_480p = emptyPriceToClear(payload.video_price_480p);
    payload.video_price_720p = emptyPriceToClear(payload.video_price_720p);
    payload.video_price_1080p = emptyPriceToClear(payload.video_price_1080p);
    payload.grok_moderation_refusal_price = emptyPriceToClear(
      payload.grok_moderation_refusal_price,
    );
    payload.search_price_per_1k = emptyPriceToClear(
      payload.search_price_per_1k,
    );
    // base_url 每次都提交（空串 = 清除）。
    payload.search_provider_base_url = (editForm.search_provider_base_url ?? "").trim();
    // api_key 留空表示「不修改」，避免编辑其它字段时把已存的 key 清掉；
    // 要清除必须显式勾选 clearSearchProviderKey。
    if ((editForm.search_provider_api_key ?? "").trim()) {
      payload.search_provider_api_key = editForm.search_provider_api_key.trim();
    } else if (editForm.clearSearchProviderKey) {
      payload.search_provider_api_key = "";
    } else {
      // 不修改：整条字段从请求里去掉，后端按 nil 处理（不触碰该列）。
      delete (payload as Record<string, unknown>).search_provider_api_key;
    }
    payload.audio_realtime_price_per_min = emptyPriceToClear(
      payload.audio_realtime_price_per_min,
    );
    payload.audio_tts_price_per_million_chars = emptyPriceToClear(
      payload.audio_tts_price_per_million_chars,
    );
    payload.audio_stt_price_per_hour = emptyPriceToClear(
      payload.audio_stt_price_per_hour,
    );
    payload.web_search_price_per_call = emptyPriceToClear(
      payload.web_search_price_per_call,
    );
    payload.peak_rate_enabled = editForm.peak_rate_enabled;
    payload.peak_start = editForm.peak_start;
    payload.peak_end = editForm.peak_end;
    payload.peak_rate_multiplier = normalizeRateMultiplier(
      editForm.peak_rate_multiplier,
    );
    const requestData = authStore.isSimpleMode
      ? {
          name: editForm.name,
          description: editForm.description,
        }
      : payload;
    const updatedGroup = await adminAPI.groups.update(
      editingGroup.value.id,
      requestData,
    );
    appStore.showSuccess(t("admin.groups.groupUpdated"));
    const updatedGroupId = editingGroup.value.id;
    closeEditModal();
    mergeGroupIntoList(updatedGroupId, updatedGroup);
  } catch (error: any) {
    appStore.showError(
      extractApiErrorMessage(error, t("admin.groups.failedToUpdate")),
    );
    console.error("Error updating group:", error);
  } finally {
    submitting.value = false;
  }
};

const addCreateMessagesDispatchMapping = () => {
  createForm.exact_model_mappings.push({ claude_model: "", target_model: "" });
};

const removeCreateMessagesDispatchMapping = (
  row: MessagesDispatchMappingRow,
) => {
  const index = createForm.exact_model_mappings.indexOf(row);
  if (index !== -1) {
    createForm.exact_model_mappings.splice(index, 1);
  }
};

const addEditMessagesDispatchMapping = () => {
  editForm.exact_model_mappings.push({ claude_model: "", target_model: "" });
};

const removeEditMessagesDispatchMapping = (row: MessagesDispatchMappingRow) => {
  const index = editForm.exact_model_mappings.indexOf(row);
  if (index !== -1) {
    editForm.exact_model_mappings.splice(index, 1);
  }
};

const handleRateMultipliers = (group: AdminGroup) => {
  rateMultipliersGroup.value = group;
  showRateMultipliersModal.value = true;
};

const handleRPMOverrides = (group: AdminGroup) => {
  rpmOverridesGroup.value = group;
  showRPMOverridesModal.value = true;
};

const handleDuplicate = async (group: AdminGroup) => {
  if (duplicatingGroupIds.has(group.id)) return;

  duplicatingGroupIds.add(group.id);
  try {
    const duplicate = await adminAPI.groups.duplicate(group.id);
    appStore.showSuccess(
      t("admin.groups.duplicateSuccess", { name: duplicate.name }),
    );
    await loadGroups();
  } catch (error: unknown) {
    appStore.showError(
      extractApiErrorMessage(error, t("admin.groups.duplicateFailed")),
    );
  } finally {
    duplicatingGroupIds.delete(group.id);
  }
};

const compositeRouteMatchLabel = (matchType: CompositeRouteMatchType) =>
  compositeRouteMatchOptions.value.find((option) => option.value === matchType)
    ?.label || matchType;

const formatCompositeEndpoint = (endpoint: CompositeRouteEndpoint) =>
  compositeRouteEndpointOptions.value.find((option) => option.value === endpoint)
    ?.label || endpoint;

const formatCompositePlatform = (platform: string) => {
  if (!platform) return "—";
  return t(`admin.groups.platforms.${platform}`);
};

const compositeRouteSourceLabel = (source: string) => {
  if (source === "route") return t("admin.groups.compositeRoutes.sources.route");
  if (source === "detector") {
    return t("admin.groups.compositeRoutes.sources.detector");
  }
  return source || "—";
};

const resetCompositeRouteForm = () => {
  compositeRouteEditingId.value = null;
  compositeRouteForm.public_model = "";
  compositeRouteForm.match_type = "exact";
  compositeRouteForm.target_platform = "openai";
  compositeRouteForm.upstream_model = "";
  compositeRouteForm.endpoint = "any";
  compositeRouteForm.priority = 100;
  compositeRouteForm.enabled = true;
  compositeRouteForm.notes = "";
};

const toCompositeRouteInput = (): CompositeModelRouteInput => ({
  public_model: compositeRouteForm.public_model.trim(),
  match_type: compositeRouteForm.match_type,
  target_platform: compositeRouteForm.target_platform,
  upstream_model: compositeRouteForm.upstream_model.trim(),
  endpoint: compositeRouteForm.endpoint,
  priority: Number(compositeRouteForm.priority) || 100,
  enabled: compositeRouteForm.enabled,
  notes: compositeRouteForm.notes.trim(),
});

const loadCompositeRoutes = async () => {
  if (!compositeRoutesGroup.value) return;
  compositeRoutesLoading.value = true;
  try {
    const routes = await adminAPI.groups.listCompositeRoutes(
      compositeRoutesGroup.value.id,
    );
    compositeRoutes.value = routes.sort((a, b) => {
      if (a.priority !== b.priority) return a.priority - b.priority;
      return a.id - b.id;
    });
  } catch (error: any) {
    appStore.showError(
      error.response?.data?.detail ||
        error.response?.data?.message ||
        t("admin.groups.compositeRoutes.failedToLoad"),
    );
    console.error("Error loading composite routes:", error);
  } finally {
    compositeRoutesLoading.value = false;
  }
};

const handleCompositeRoutes = async (group: AdminGroup) => {
  compositeRoutesGroup.value = group;
  compositePreviewModel.value = "";
  compositePreviewEndpoint.value = "any";
  compositePreviewDecision.value = null;
  resetCompositeRouteForm();
  showCompositeRoutesModal.value = true;
  await loadCompositeRoutes();
};

const closeCompositeRoutesModal = () => {
  showCompositeRoutesModal.value = false;
  compositeRoutesGroup.value = null;
  compositeRoutes.value = [];
  compositePreviewDecision.value = null;
  resetCompositeRouteForm();
};

const editCompositeRoute = (route: CompositeModelRoute) => {
  compositeRouteEditingId.value = route.id;
  compositeRouteForm.public_model = route.public_model;
  compositeRouteForm.match_type = route.match_type;
  compositeRouteForm.target_platform = route.target_platform;
  compositeRouteForm.upstream_model = route.upstream_model;
  compositeRouteForm.endpoint = route.endpoint;
  compositeRouteForm.priority = route.priority || 100;
  compositeRouteForm.enabled = route.enabled;
  compositeRouteForm.notes = route.notes || "";
};

const saveCompositeRoute = async () => {
  if (!compositeRoutesGroup.value) return;
  if (!compositeRouteForm.public_model.trim()) {
    appStore.showError(t("admin.groups.compositeRoutes.publicModelRequired"));
    return;
  }
  compositeRouteSaving.value = true;
  try {
    const payload = toCompositeRouteInput();
    if (compositeRouteEditingId.value) {
      await adminAPI.groups.updateCompositeRoute(
        compositeRoutesGroup.value.id,
        compositeRouteEditingId.value,
        payload,
      );
      appStore.showSuccess(t("admin.groups.compositeRoutes.routeUpdated"));
    } else {
      await adminAPI.groups.createCompositeRoute(
        compositeRoutesGroup.value.id,
        payload,
      );
      appStore.showSuccess(t("admin.groups.compositeRoutes.routeCreated"));
    }
    resetCompositeRouteForm();
    await loadCompositeRoutes();
  } catch (error: any) {
    appStore.showError(
      error.response?.data?.detail ||
        error.response?.data?.message ||
        t("admin.groups.compositeRoutes.failedToSave"),
    );
    console.error("Error saving composite route:", error);
  } finally {
    compositeRouteSaving.value = false;
  }
};

const deleteCompositeRoute = async (route: CompositeModelRoute) => {
  if (!compositeRoutesGroup.value) return;
  if (!window.confirm(t("admin.groups.compositeRoutes.deleteConfirm"))) return;
  try {
    await adminAPI.groups.deleteCompositeRoute(
      compositeRoutesGroup.value.id,
      route.id,
    );
    if (compositeRouteEditingId.value === route.id) {
      resetCompositeRouteForm();
    }
    appStore.showSuccess(t("admin.groups.compositeRoutes.routeDeleted"));
    await loadCompositeRoutes();
  } catch (error: any) {
    appStore.showError(
      error.response?.data?.detail ||
        error.response?.data?.message ||
        t("admin.groups.compositeRoutes.failedToDelete"),
    );
    console.error("Error deleting composite route:", error);
  }
};

const previewCompositeRoute = async () => {
  if (!compositeRoutesGroup.value || !compositePreviewModel.value.trim()) {
    return;
  }
  compositePreviewLoading.value = true;
  try {
    compositePreviewDecision.value = await adminAPI.groups.previewCompositeRoute(
      compositeRoutesGroup.value.id,
      {
        model: compositePreviewModel.value.trim(),
        endpoint: compositePreviewEndpoint.value,
      },
    );
  } catch (error: any) {
    appStore.showError(
      error.response?.data?.detail ||
        error.response?.data?.message ||
        t("admin.groups.compositeRoutes.failedToPreview"),
    );
    console.error("Error previewing composite route:", error);
  } finally {
    compositePreviewLoading.value = false;
  }
};

const handleDelete = (group: AdminGroup) => {
  deletingGroup.value = group;
  showDeleteDialog.value = true;
};

const clearGroupSelection = () => {
  selectedGroupIds.value = [];
};

const handleSelectionChange = (keys: Array<string | number>) => {
  selectedGroupIds.value = keys;
};

const selectedGroupIDs = computed(() =>
  selectedGroupIds.value
    .map((key) => Number(key))
    .filter((id) => Number.isFinite(id) && id > 0),
);

type GroupDeleteSnapshot = Array<{ group: AdminGroup; index: number }>;

const applyTotalDelta = (delta: number) => {
  pagination.total = Math.max(0, pagination.total + delta);
  pagination.pages =
    pagination.page_size > 0
      ? Math.ceil(pagination.total / pagination.page_size)
      : 0;
};

/**
 * Drop the rows from the list right away and keep them (with their original
 * position) so a failed request can put them back where the admin expects.
 */
const removeGroupsOptimistically = (ids: number[]): GroupDeleteSnapshot => {
  const idSet = new Set(ids);
  const snapshot: GroupDeleteSnapshot = [];
  const remaining: AdminGroup[] = [];
  groups.value.forEach((group, index) => {
    if (idSet.has(group.id)) snapshot.push({ group, index });
    else remaining.push(group);
  });
  if (snapshot.length === 0) return snapshot;
  groups.value = remaining;
  applyTotalDelta(-snapshot.length);
  return snapshot;
};

const restoreGroupsOptimistically = (snapshot: GroupDeleteSnapshot) => {
  if (snapshot.length === 0) return;
  const restored = [...groups.value];
  const ordered = [...snapshot].sort((a, b) => a.index - b.index);
  for (const entry of ordered) {
    restored.splice(Math.min(entry.index, restored.length), 0, entry.group);
  }
  groups.value = restored;
  applyTotalDelta(ordered.length);
};

/**
 * Patch a single row with the object the server returned instead of reloading
 * the whole page. Only fall back to a fetch when the row is no longer visible.
 */
const mergeGroupIntoList = (
  id: number,
  updated: AdminGroup | null | undefined,
) => {
  const index = groups.value.findIndex((group) => group.id === id);
  if (!updated || index === -1) {
    void loadGroups();
    return;
  }
  groups.value[index] = { ...groups.value[index], ...updated };
};

const confirmDelete = async () => {
  if (!deletingGroup.value) return;
  const target = deletingGroup.value;
  showDeleteDialog.value = false;
  deletingGroup.value = null;
  selectedGroupIds.value = selectedGroupIds.value.filter(
    (key) => Number(key) !== target.id,
  );

  const snapshot = removeGroupsOptimistically([target.id]);
  try {
    await adminAPI.groups.delete(target.id);
    appStore.showSuccess(t("admin.groups.groupDeleted"));
  } catch (error: any) {
    restoreGroupsOptimistically(snapshot);
    appStore.showError(
      error.response?.data?.detail || t("admin.groups.failedToDelete"),
    );
    console.error("Error deleting group:", error);
  }
};

const handleBulkDelete = async () => {
  if (bulkDeleting.value) return;
  const ids = selectedGroupIDs.value;
  if (ids.length === 0) return;

  const confirmed = window.confirm(
    `${t("admin.groups.deleteGroup")} ${t("common.selectedCount", {
      count: ids.length,
    })}`,
  );
  if (!confirmed) return;

  bulkDeleting.value = true;
  const snapshot = removeGroupsOptimistically(ids);
  clearGroupSelection();
  try {
    const result = await adminAPI.groups.batchDelete(ids);
    if (result.failed > 0) {
      const failedIDs = new Set(result.failed_ids);
      const failedRows = snapshot.filter((entry) =>
        failedIDs.has(entry.group.id),
      );
      restoreGroupsOptimistically(failedRows);
      selectedGroupIds.value = failedRows.map((entry) => entry.group.id);
      appStore.showError(t("admin.groups.failedToDelete"));
      return;
    }
    appStore.showSuccess(t("admin.groups.groupDeleted"));
  } catch (error: any) {
    restoreGroupsOptimistically(snapshot);
    selectedGroupIds.value = snapshot.map((entry) => entry.group.id);
    appStore.showError(
      error?.response?.data?.detail || t("admin.groups.failedToDelete"),
    );
    console.error("Error bulk deleting groups:", error);
  } finally {
    bulkDeleting.value = false;
  }
};

// 监听 subscription_type 变化，订阅模式时 is_exclusive 默认为 true；标准模式清空高峰配置
watch(
  () => createForm.subscription_type,
  (newVal) => {
    if (newVal === "subscription") {
      createForm.is_exclusive = true;
      createForm.fallback_group_id_on_invalid_request = null;
    } else {
      createForm.peak_rate_enabled = false;
      createForm.peak_start = "";
      createForm.peak_end = "";
      createForm.peak_rate_multiplier = 1.0;
    }
  },
);

// 编辑表单：切回标准模式时清空高峰配置，避免残留随更新请求提交被后端拒绝
watch(
  () => editForm.subscription_type,
  (newVal) => {
    if (newVal !== "subscription") {
      editForm.peak_rate_enabled = false;
      editForm.peak_start = "";
      editForm.peak_end = "";
      editForm.peak_rate_multiplier = 1.0;
    }
  },
);

watch(
  () => createForm.platform,
  (newVal) => {
    if (!["anthropic", "antigravity"].includes(newVal)) {
      createForm.fallback_group_id_on_invalid_request = null;
    }
    if (!supportsMessagesDispatchPlatform(newVal)) {
      resetMessagesDispatchFormState(createForm);
    }
    if (!supportsLivePlatform(newVal)) {
      createForm.allow_live = false;
    }
    if (!isProfitControlPlatform(newVal)) {
      createForm.profit_control_enabled = false;
      createForm.profit_min_margin_percent = 0;
      createForm.profit_safety_buffer_percent = 0;
    }
    createForm.max_reasoning_effort = normalizeReasoningEffortForPlatform(
      newVal,
      createForm.max_reasoning_effort,
    );
    createForm.max_reasoning_effort_over_limit = supportsReasoningEffortPolicyPlatform(
      newVal,
    )
      ? normalizeReasoningEffortOverLimit(
          createForm.max_reasoning_effort_over_limit,
        )
      : reasoningEffortOverLimitDowngrade;
    createForm.reasoning_effort_mappings = reasoningEffortMappingsToRows(
      reasoningEffortMappingsToAPI(createForm.reasoning_effort_mappings),
      newVal,
    );
    createReasoningEffortPolicyRef.value?.resetValidation();
    if (!["openai", "antigravity", "anthropic", "gemini"].includes(newVal)) {
      createForm.require_oauth_only = false;
      createForm.require_privacy_set = false;
    }
    resetDisabledBatchImagePricing(createForm);
    resetModelAllowlistState(createModelAllowlistState);
    loadModelAllowlistCandidates("create", 0, newVal);
  },
);

watch(
  () => createForm.allow_image_generation,
  () => {
    resetDisabledBatchImagePricing(createForm);
  },
);

watch(
  () => createForm.allow_batch_image_generation,
  () => {
    resetDisabledBatchImagePricing(createForm);
  },
);

watch(
  () => editForm.platform,
  (newVal) => {
    if (!["anthropic", "antigravity"].includes(newVal)) {
      editForm.fallback_group_id_on_invalid_request = null;
    }
    if (!supportsMessagesDispatchPlatform(newVal)) {
      resetMessagesDispatchFormState(editForm);
    }
    if (!supportsLivePlatform(newVal)) {
      editForm.allow_live = false;
    }
    if (!isProfitControlPlatform(newVal)) {
      editForm.profit_control_enabled = false;
      editForm.profit_min_margin_percent = 0;
      editForm.profit_safety_buffer_percent = 0;
    }
    editForm.max_reasoning_effort = normalizeReasoningEffortForPlatform(
      newVal,
      editForm.max_reasoning_effort,
    );
    editForm.max_reasoning_effort_over_limit = supportsReasoningEffortPolicyPlatform(
      newVal,
    )
      ? normalizeReasoningEffortOverLimit(
          editForm.max_reasoning_effort_over_limit,
        )
      : reasoningEffortOverLimitDowngrade;
    editForm.reasoning_effort_mappings = reasoningEffortMappingsToRows(
      reasoningEffortMappingsToAPI(editForm.reasoning_effort_mappings),
      newVal,
    );
    editReasoningEffortPolicyRef.value?.resetValidation();
    if (!["openai", "antigravity", "anthropic", "gemini"].includes(newVal)) {
      editForm.require_oauth_only = false;
      editForm.require_privacy_set = false;
    }
    resetDisabledBatchImagePricing(editForm);
    if (editingGroup.value) {
      resetModelAllowlistState(editModelAllowlistState, editForm.platform === editingGroup.value.platform ? editingGroup.value.model_allowlist : undefined);
      loadModelAllowlistCandidates("edit", editingGroup.value.id, newVal);
    }
  },
);

watch(
  () => editForm.allow_image_generation,
  () => {
    resetDisabledBatchImagePricing(editForm);
  },
);

watch(
  () => editForm.allow_batch_image_generation,
  () => {
    resetDisabledBatchImagePricing(editForm);
  },
);

watch(
  () => editForm.platform,
  (newVal) => {
    if (!['anthropic', 'antigravity'].includes(newVal)) {
      editForm.fallback_group_id_on_invalid_request = null
    }
    if (!supportsMessagesDispatchPlatform(newVal)) {
      editForm.allow_messages_dispatch = false
      editForm.default_mapped_model = ''
    }
    if (!supportsLivePlatform(newVal)) {
      editForm.allow_live = false
    }
  }
)

// 点击外部关闭账号搜索下拉框
const handleClickOutside = (event: MouseEvent) => {
  const target = event.target as HTMLElement;
  // 检查是否点击在下拉框或输入框内
  if (!target.closest(".account-search-container")) {
    Object.keys(showAccountDropdown.value).forEach((key) => {
      showAccountDropdown.value[key] = false;
    });
  }
};

// 打开排序弹窗
const openSortModal = async () => {
  try {
    // 获取所有分组（不分页）
    const allGroups = await adminAPI.groups.getAll();
    // 按 sort_order 排序
    sortableGroups.value = [...allGroups].sort(
      (a, b) => a.sort_order - b.sort_order,
    );
    showSortModal.value = true;
  } catch (error) {
    appStore.showError(t("admin.groups.failedToLoad"));
    console.error("Error loading groups for sorting:", error);
  }
};

// 关闭排序弹窗
const closeSortModal = () => {
  showSortModal.value = false;
  sortableGroups.value = [];
};

// 保存排序
const saveSortOrder = async () => {
  sortSubmitting.value = true;
  try {
    const updates = sortableGroups.value.map((g, index) => ({
      id: g.id,
      sort_order: index * 10,
    }));
    await adminAPI.groups.updateSortOrder(updates);
    appStore.showSuccess(t("admin.groups.sortOrderUpdated"));
    closeSortModal();
    loadGroups();
  } catch (error: any) {
    appStore.showError(
      error.response?.data?.detail || t("admin.groups.failedToUpdateSortOrder"),
    );
    console.error("Error updating sort order:", error);
  } finally {
    sortSubmitting.value = false;
  }
};

onMounted(() => {
  loadGroups();
  if (!authStore.isSimpleMode) {
    void loadLiveCapability();
    loadModelAllowlistCandidates("create", 0, createForm.platform);
  }
  document.addEventListener("click", handleClickOutside);
});

onUnmounted(() => {
  document.removeEventListener("click", handleClickOutside);
  accountSearchRunner.clearAll();
  clearAllAccountSearchState();
});
</script>

<style scoped>
.groups-workspace {
  gap: 12px;
}

.groups-page-header {
  margin-bottom: 16px;
}

@media (max-width: 767px) {
  .groups-workspace {
    gap: 10px;
  }

  .groups-page-header {
    margin-bottom: 12px;
  }
}
</style>
