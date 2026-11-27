<template>
  <AppLayout>
    <TablePageLayout class="signal-keys">
      <template #filters>
        <div class="flex flex-col gap-3">
          <TwoFactorSuggestionBanner storage-key="keys" />
          <!-- 一键处理：直接把待整改的 Key 收拢成一屏，逐把轮换或加白名单 -->
          <div
            v-if="showRotationOnly"
            class="flex flex-wrap items-center justify-between gap-2 rounded-xl border border-amber-300 bg-amber-50 px-4 py-3 text-sm text-amber-950 dark:border-amber-400/40 dark:bg-amber-400/10 dark:text-amber-50"
            data-test="keys-rotation-filter"
          >
            <span class="flex items-center gap-2">
              <UIcon name="i-lucide-shield-alert" class="size-4 shrink-0" aria-hidden="true" />
              {{ t('keys.rotationFilterHint', { count: rotationOnlyRows.length }) }}
            </span>
            <UButton
              size="xs"
              color="neutral"
              variant="ghost"
              data-test="keys-rotation-show-all"
              @click="stopRotationRemediation"
            >
              {{ t('keys.rotationShowAll') }}
            </UButton>
          </div>
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
            <!-- 与充值/兑换门禁同源的待整改汇总（覆盖所有分页） -->
            <UBadge
              v-if="pendingRotationCount > 0"
              color="warning"
              variant="subtle"
              icon="i-lucide-shield-alert"
              class="tabular-nums"
              data-test="keys-rotation-count"
              :title="t('keys.rotationPendingHint')"
            >
              {{ t('keys.rotationPendingSummary', { count: pendingRotationCount }) }}
            </UBadge>
          </template>
          <template #actions>
            <UButton
              v-if="pendingRotationCount > 0"
              color="warning"
              variant="outline"
              icon="i-lucide-list-checks"
              data-test="keys-rotation-process"
              @click="startRotationRemediation"
            >
              {{ t('keys.rotationPendingAction') }}
            </UButton>
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
              v-if="selectedKeyCount > 0"
              color="primary"
              variant="outline"
              icon="i-lucide-pencil"
              data-test="keys-bulk-edit"
              @click="showBulkEditDialog = true"
            >
              {{ t('keys.bulkEdit.entry') }}
              <span class="ml-1 tabular-nums opacity-70">{{ selectedKeyCount }}</span>
            </UButton>
            <UButton
              v-if="selectedKeyCount > 0"
              color="warning"
              variant="outline"
              icon="i-lucide-refresh-cw"
              :loading="batchRotateRunning"
              data-test="keys-bulk-rotate"
              @click="openBatchRotate"
            >
              {{ t('keys.batchRotate.entry') }}
              <span class="ml-1 tabular-nums opacity-70">{{ selectedKeyCount }}</span>
            </UButton>
            <UButton
              color="neutral"
              variant="outline"
              icon="i-lucide-refresh-cw"
              :ui="{ leadingIcon: loading ? 'animate-spin motion-reduce:animate-none' : '' }"
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
                @click="toggleColumnDropdown"
              />
              <Teleport to="body">
                <div
                  v-if="showColumnDropdown"
                  ref="columnDropdownMenuRef"
                  :style="columnDropdownStyle"
                  data-test="keys-column-dropdown"
                  :class="[
                    'fixed z-50 max-h-[min(20rem,calc(100dvh-16px))] w-52 overflow-y-auto rounded-lg bg-default p-1 shadow-(--app-shadow-popover) ring ring-default',
                    columnDropdownPlacement === 'top' ? 'origin-bottom' : 'origin-top'
                  ]"
                >
                  <button
                    v-for="col in toggleableColumns"
                    :key="col.key"
                    type="button"
                    @click="toggleColumn(col.key)"
                    class="flex min-h-11 w-full items-center justify-between rounded-md px-2.5 py-1.5 text-left text-sm text-default transition-colors focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary sm:min-h-0 [@media(hover:hover)_and_(pointer:fine)]:hover:bg-elevated"
                  >
                    <span>{{ col.label }}</span>
                    <UIcon v-if="isColumnVisible(col.key)" name="i-lucide-check" class="size-4 text-highlighted" />
                  </button>
                </div>
              </Teleport>
            </div>
            <UButton
              type="button"
              color="neutral"
              variant="outline"
              icon="i-lucide-book-open"
              class="hidden sm:inline-flex"
              data-test="keys-agent-guide"
              @click="openAgentGuide"
            >
              {{ t('keys.agentGuide') }}
            </UButton>
            <UButton icon="i-lucide-plus" data-tour="keys-create-btn" class="min-h-11 lg:min-h-8" @click="openCreateModal">
              {{ t('keys.createKey') }}
            </UButton>
          </template>
        </UserPageHeader>
      </template>

      <template #table>
        <DataTable
          :columns="columns"
          :data="tableRows"
          :loading="loading || rotationOnlyLoading"
          :server-side-sort="true"
          :selectable="true"
          :row-key="keyRowId"
          v-model:selected-keys="selectedKeyIds"
          default-sort-key="created_at"
          default-sort-order="desc"
          @sort="handleSort"
        >
          <template #cell-id="{ value }">
            <span class="font-mono text-xs tabular-nums text-muted">#{{ value }}</span>
          </template>

          <template #cell-key="{ row }">
            <!-- 列表单元格永远只渲染后端掩码：内存里的明文绝不进表格，避免超长 Key
                 撑宽列、撑高行。本标签页创建/轮换过的完整明文仍可通过右侧复制按钮
                 或右下角「再复制一次」浮条取用（见 copyRowKey / EphemeralKeyBanner）。
                 掩码等宽、单行截断，列宽/行高因此与其它行保持一致。 -->
            <div class="flex max-w-[12.5rem] items-center gap-1.5" data-test="key-cell-content">
              <code
                class="key-mask min-w-0 truncate rounded-md bg-elevated px-1.5 py-0.5 font-mono text-xs text-default ring ring-inset ring-default"
                :title="keyCellHint(row)"
                data-test="key-value"
              >{{ keyDisplayValue(row) }}</code>
              <UButton
                color="neutral"
                variant="ghost"
                size="xs"
                :icon="copiedKeyId === row.id ? 'i-lucide-check' : 'i-lucide-copy'"
                :aria-label="t('keys.copyKey')"
                :title="t('keys.copyKey')"
                class="shrink-0"
                data-test="key-copy"
                @click.stop="copyRowKey(row)"
              />
            </div>
          </template>

          <template #cell-name="{ value, row }">
            <div class="flex flex-col gap-1">
              <div class="flex items-center gap-1.5">
                <span class="font-medium text-highlighted">{{ value }}</span>
                <Icon
                  v-if="row.ip_whitelist?.length > 0 || row.ip_blacklist?.length > 0"
                  name="shield"
                  size="sm"
                  class="text-primary"
                  :title="t('keys.ipRestrictionEnabled')"
                />
              </div>
              <!-- 待整改：充值/兑换门禁与列表共用同一判定，角标在整改成功后立即消失。 -->
              <div v-if="row.needs_rotation" class="flex flex-wrap items-center gap-1.5">
                <span
                  class="inline-flex items-center gap-1 rounded-full bg-amber-100 px-2 py-0.5 text-[11px] font-semibold text-amber-800 ring ring-inset ring-amber-300 dark:bg-amber-400/15 dark:text-amber-200 dark:ring-amber-400/40"
                  data-test="key-needs-rotation-badge"
                >
                  <UIcon name="i-lucide-shield-alert" class="size-3.5 shrink-0" aria-hidden="true" />
                  {{ t('keys.rotationBadge') }}
                </span>
                <span v-if="rotationReasonText(row)" class="text-[11px] text-amber-700 dark:text-amber-300" data-test="key-needs-rotation-reason">
                  {{ rotationReasonText(row) }}
                </span>
                <button
                  type="button"
                  class="key-risk-rotate"
                  :disabled="rotatingKeyId === row.id"
                  :title="t('keys.riskRotateHint')"
                  data-test="key-needs-rotation-rotate"
                  @click="rotateKeyValue(row)"
                >
                  <UIcon
                    name="i-lucide-refresh-cw"
                    :class="['size-3.5', rotatingKeyId === row.id ? 'animate-spin motion-reduce:animate-none' : '']"
                  />
                  <span>{{ t('keys.rotationFixByRotate') }}</span>
                </button>
                <button
                  type="button"
                  class="key-risk-rotate"
                  :title="t('keys.rotationFixByWhitelistHint')"
                  data-test="key-needs-rotation-whitelist"
                  @click="editKey(row)"
                >
                  <UIcon name="i-lucide-network" class="size-3.5" />
                  <span>{{ t('keys.rotationFixByWhitelist') }}</span>
                </button>
              </div>
              <!-- Gateway risk control disabled this key: state the reason and offer the
                   one-click rotation that restores it. -->
              <div v-if="row.risk_disabled" class="flex flex-wrap items-center gap-1.5">
                <span class="inline-flex items-center gap-1 text-[11px] font-medium text-amber-600 dark:text-amber-400">
                  <UIcon name="i-lucide-shield-alert" class="size-3.5 shrink-0" />
                  {{ t('keys.riskWarning') }}
                </span>
                <button
                  type="button"
                  class="key-risk-rotate"
                  :disabled="rotatingKeyId === row.id"
                  :title="t('keys.riskRotateHint')"
                  @click="rotateKeyValue(row)"
                >
                  <UIcon
                    name="i-lucide-refresh-cw"
                    :class="['size-3.5', rotatingKeyId === row.id ? 'animate-spin motion-reduce:animate-none' : '']"
                  />
                  <span>{{ t('keys.riskRotateAction') }}</span>
                </button>
              </div>
            </div>
          </template>

          <template #cell-group="{ row }">
            <!-- The cell is the click target; the real <button> inside is the keyboard stop. The rate hints sit
                 beside the rate and are separate controls, so they cannot live inside that button. -->
            <div
              :ref="(el) => setGroupButtonRef(row.id, el)"
              data-group-cell
              class="group/dropdown relative -mx-2 -my-1 flex w-fit max-w-full cursor-pointer items-center gap-2 rounded-lg px-2 py-1 transition-colors duration-200 [@media(hover:hover)_and_(pointer:fine)]:hover:bg-elevated"
              @click="onGroupCellClick($event, row)"
            >
              <button
                type="button"
                class="flex items-center gap-2 text-left"
                :title="isSmartRoutingKey(row) ? t('keys.smartRoutingChangeInEditor') : t('keys.clickToChangeGroup')"
              >
                <GroupBadge
                  v-if="groupForKey(row, primaryGroupId(row))"
                  :name="groupForKey(row, primaryGroupId(row))!.name"
                  :platform="groupForKey(row, primaryGroupId(row))!.platform"
                  :subscription-type="groupForKey(row, primaryGroupId(row))!.subscription_type"
                  :rate-multiplier="groupForKey(row, primaryGroupId(row))!.rate_multiplier"
                  :user-rate-multiplier="userGroupRates[primaryGroupId(row)!]"
                  :peak-rate-enabled="groupForKey(row, primaryGroupId(row))!.peak_rate_enabled"
                  :peak-start="groupForKey(row, primaryGroupId(row))!.peak_start"
                  :peak-end="groupForKey(row, primaryGroupId(row))!.peak_end"
                  :peak-rate-multiplier="groupForKey(row, primaryGroupId(row))!.peak_rate_multiplier"
                  data-test="key-bound-group"
                  :data-group-id="primaryGroupId(row)"
                />
                <UBadge v-else-if="primaryGroupId(row) != null" color="neutral" variant="subtle" size="sm" data-test="key-bound-group" :data-group-id="primaryGroupId(row)">#{{ primaryGroupId(row) }}</UBadge>
                <span v-else class="text-sm text-dimmed">{{ t('keys.noGroup') }}</span>
              </button>
              <GroupRateSignals
                :signals="availableGroupsById.get(primaryGroupId(row) ?? -1)"
                :show-rate-change="showRateChangeFor(groupForKey(row, primaryGroupId(row)))"
              />
              <UBadge v-if="isSmartRoutingKey(row)" color="primary" variant="subtle" size="sm" data-test="key-smart-routing-count">
                {{ t('keys.smartRouting') }}
                {{ t('keys.smartRoutingMore', { count: keyGroupIds(row).length - 1 }) }}
              </UBadge>
              <svg
                class="h-3.5 w-3.5 text-dimmed opacity-60 transition-opacity [@media(hover:hover)_and_(pointer:fine)]:group-hover/dropdown:opacity-100"
                fill="none"
                stroke="currentColor"
                viewBox="0 0 24 24"
                stroke-width="2"
                aria-hidden="true"
              >
                <path
                  stroke-linecap="round"
                  stroke-linejoin="round"
                  d="M8.25 15L12 18.75 15.75 15m-7.5-6L12 5.25 15.75 9"
                />
              </svg>
            </div>
          </template>

          <template #cell-current_concurrency="{ value, row }">
            <span
              :class="[
                'inline-flex min-w-8 items-center justify-center rounded-sm px-2 py-1 text-sm font-semibold tabular-nums focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-current',
                (value ?? 0) > 0
                  ? 'bg-success/10 text-success ring-1 ring-success/30'
                  : 'bg-elevated text-muted'
              ]"
              :title="concurrencyHint(row)"
              :aria-label="concurrencyHint(row)"
              tabindex="0"
            >
              {{ value ?? 0 }}<span v-if="row.concurrency_limit" class="ml-1 opacity-70">/ {{ row.concurrency_limit }}</span>
            </span>
            <span v-if="row.concurrency_limit" class="mt-1 block text-xs tabular-nums text-muted">{{ t('keys.queueWaiting') }}: {{ queueWaiting[row.id] ?? '—' }}</span>
          </template>

          <template #cell-usage="{ row }">
            <div class="text-sm">
              <div class="flex items-center gap-1.5">
                <span class="text-muted">{{ t('keys.today') }}:</span>
                <span class="font-medium tabular-nums text-highlighted">
                  ${{ (usageStats[row.id]?.today_actual_cost ?? 0).toFixed(4) }}
                </span>
              </div>
              <div class="mt-0.5 flex items-center gap-1.5">
                <span class="text-muted">{{ t('keys.total') }}:</span>
                <span class="font-medium tabular-nums text-highlighted">
                  ${{ (usageStats[row.id]?.total_actual_cost ?? 0).toFixed(4) }}
                </span>
              </div>
              <!-- Quota progress (if quota is set) -->
              <div v-if="row.quota > 0" class="mt-1.5">
                <div class="flex items-center gap-1.5 whitespace-nowrap">
                  <span class="text-muted">{{ t('keys.quota') }}:</span>
                  <span :class="[
                    'font-medium tabular-nums',
                    row.quota_used >= row.quota ? 'text-error' :
                    row.quota_used >= row.quota * 0.8 ? 'text-warning' :
                    'text-highlighted'
                  ]">
                    ${{ row.quota_used?.toFixed(2) || '0.00' }} / ${{ row.quota?.toFixed(2) }}
                  </span>
                </div>
                <div class="mt-1 h-1.5 w-full overflow-hidden rounded-full bg-accented">
                  <div
                    :class="[
                      'h-full w-full origin-left rounded-full transition-[transform,background-color] duration-200 ease-[cubic-bezier(0.23,1,0.32,1)] motion-reduce:transition-none',
                      row.quota_used >= row.quota ? 'bg-error' :
                      row.quota_used >= row.quota * 0.8 ? 'bg-warning' :
                      'bg-primary'
                    ]"
                    :style="{ transform: `scaleX(${Math.min(Math.max(row.quota_used / row.quota, 0), 1)})` }"
                  />
                </div>
              </div>
              <button
                type="button"
                class="mt-1.5 inline-flex items-center gap-1 rounded-sm px-1.5 py-0.5 text-xs font-medium text-primary transition-colors focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary [@media(hover:hover)_and_(pointer:fine)]:hover:bg-primary/10"
                data-test="key-usage-details"
                @click="openUsageDialog(row)"
              >
                <UIcon name="i-lucide-chart-no-axes-column" class="size-3.5" />
                {{ t('keys.usageDialog.open') }}
              </button>
            </div>
          </template>

          <template #cell-overview="{ row }">
            <!-- 一屏概览：近 24h 调用/消耗（批量接口）+ 到期时间 + IP 白名单状态。 -->
            <div class="space-y-1 text-sm" data-test="key-overview">
              <div class="flex items-center gap-1.5">
                <span class="text-muted">{{ t('keys.overview.calls24h') }}:</span>
                <span class="font-medium tabular-nums text-highlighted" data-test="key-overview-calls">
                  {{ overviewFor(row).requests_24h.toLocaleString() }}
                </span>
                <span class="text-dimmed">/</span>
                <span class="font-medium tabular-nums text-highlighted" data-test="key-overview-cost">
                  ${{ overviewFor(row).cost_24h.toFixed(4) }}
                </span>
              </div>
              <div class="text-xs" :class="overviewIPStatus(row).tone" data-test="key-overview-ip">
                {{ overviewIPStatus(row).text }}
              </div>
              <div
                class="text-xs tabular-nums"
                :class="row.expires_at && new Date(row.expires_at) < new Date() ? 'text-error' : 'text-dimmed'"
                data-test="key-overview-expiry"
              >
                {{ row.expires_at
                  ? t('keys.overview.expiresAt', { date: formatDateTime(row.expires_at) })
                  : t('keys.overview.neverExpires') }}
              </div>
            </div>
          </template>

          <template #cell-rate_limit="{ row }">
            <div v-if="rateLimitWindows(row).length" class="space-y-1.5 min-w-[140px]">
              <div v-for="w in rateLimitWindows(row)" :key="w.label">
                <div class="flex items-center justify-between text-xs">
                  <span class="text-muted">{{ w.label }}</span>
                  <span :class="['font-medium tabular-nums', RATE_LIMIT_TEXT_CLASS[w.tone]]">
                    ${{ w.used.toFixed(2) }}/${{ w.limit.toFixed(2) }}
                  </span>
                </div>
                <div class="h-1 w-full overflow-hidden rounded-full bg-accented">
                  <div
                    :class="['h-full w-full origin-left rounded-full transition-[transform,background-color] duration-200 ease-[cubic-bezier(0.23,1,0.32,1)] motion-reduce:transition-none', RATE_LIMIT_BAR_CLASS[w.tone]]"
                    :style="{ transform: `scaleX(${Math.min(Math.max(w.percent / 100, 0), 1)})` }"
                  />
                </div>
                <div v-if="w.resetAt && formatResetTime(w.resetAt)" class="text-[10px] tabular-nums text-dimmed">
                  ⟳ {{ formatResetTime(w.resetAt) }}
                </div>
              </div>
              <!-- Reset button -->
              <button
                v-if="row.usage_5h > 0 || row.usage_1d > 0 || row.usage_7d > 0"
                @click.stop="confirmResetRateLimitFromTable(row)"
                class="mt-0.5 inline-flex items-center gap-1 rounded-sm px-1.5 py-0.5 text-xs text-muted transition-colors focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary [@media(hover:hover)_and_(pointer:fine)]:hover:bg-elevated [@media(hover:hover)_and_(pointer:fine)]:hover:text-primary"
                :title="t('keys.resetRateLimitUsage')"
              >
                <Icon name="refresh" size="xs" />
                {{ t('keys.resetUsage') }}
              </button>
            </div>
            <span v-else class="text-sm text-dimmed">-</span>
          </template>

          <template #cell-expires_at="{ value }">
            <span v-if="value" :class="[
              'text-sm tabular-nums',
              new Date(value) < new Date() ? 'text-error' : 'text-muted'
            ]">
              {{ formatDateTime(value) }}
            </span>
            <span v-else class="text-sm text-dimmed">{{ t('keys.noExpiration') }}</span>
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
            <span v-if="value" class="text-sm tabular-nums text-muted">
              {{ formatDateTime(value) }}
            </span>
            <span v-else class="text-sm text-dimmed">-</span>
          </template>

          <template #cell-last_used_ip="{ value }">
            <span v-if="value" class="font-mono text-sm tabular-nums text-muted">
              {{ value }}
            </span>
            <span v-else class="text-sm text-dimmed">-</span>
          </template>

          <template #cell-created_at="{ value }">
            <span class="text-sm tabular-nums text-muted">{{ formatDateTime(value) }}</span>
          </template>

          <template #cell-actions="{ row }">
            <div class="flex items-center gap-0.5">
              <!-- 明文 key 只在创建/轮换当次可见，"使用密钥 / 导入 CCS"入口移到
                   明文回执弹窗里，避免让用户以为列表可以随时复制完整 key。 -->
              <!-- Toggle Status Button -->
              <button type="button" class="key-row-action rounded-md" @click="toggleKeyStatus(row)">
                <UIcon v-if="row.status === 'active'" name="i-lucide-ban" class="size-4" />
                <UIcon v-else name="i-lucide-circle-check" class="size-4" />
                <span>{{ row.status === 'active' ? t('keys.disable') : t('keys.enable') }}</span>
              </button>
              <!-- Edit Button -->
              <button type="button" class="key-row-action rounded-md" @click="editKey(row)">
                <UIcon name="i-lucide-pencil" class="size-4" />
                <span>{{ t('common.edit') }}</span>
              </button>
              <!-- Delete Button -->
              <button type="button" class="key-row-action key-row-action--danger rounded-md" @click="confirmDelete(row)">
                <UIcon name="i-lucide-trash-2" class="size-4" />
                <span>{{ t('common.delete') }}</span>
              </button>
            </div>
          </template>

          <template #empty>
            <EmptyState
              v-if="showRotationOnly"
              :title="t('keys.rotationNoneTitle')"
              :description="t('keys.rotationNoneDescription')"
            />
            <EmptyState
              v-else
              :title="t('keys.noKeysYet')"
              :description="t('keys.createFirstKey')"
              :action-text="t('keys.createKey')"
              @action="openCreateModal"
            />
          </template>
        </DataTable>
      </template>

      <template #pagination>
        <Pagination
          class="signal-pagination"
          v-if="!showRotationOnly && pagination.total > 0"
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
      :description="createDialogDescription"
      width="normal"
      @close="closeModals"
    >
      <form id="key-form" @submit.prevent="handleSubmit" class="space-y-5">
        <!-- 创建：开了 TOTP / Passkey 的账号分两步（填信息 → 身份校验），其余账号只有一步，直接创建；
             编辑保持原有单页流程。Quiet 步骤头（和注册页同一套）两步都显示；原来的文字面包屑放在第一步容器里，
             第二步就看不到了。步骤头只在确认需要校验之后才出现：绝大多数账号不需要，不能让它每次先闪一下再消失。 -->
        <QuietStepHead
          v-if="showCreateFlow && requiresCreateVerification"
          :current="createStep"
          :total="2"
          :label="t('auth.stepProgress', { current: createStep, total: 2 })"
          data-test="key-create-steps"
        />
        <div
          v-show="showEditModal || createStep === 1"
          :class="['space-y-5', showCreateFlow ? 'key-step-pane' : '']"
        >
        <UFormField
          :label="t('keys.nameLabel')"
          required
          size="lg"
          :error="nameError"
          :class="{ 'key-field-nudge': nudge && nameError }"
          data-test="key-field-name"
        >
          <UInput
            ref="nameInputRef"
            v-model="formData.name"
            type="text"
            name="api-key-name"
            autocomplete="off"
            data-lpignore="true"
            data-1p-ignore="true"
            required
            :color="nameError ? 'error' : undefined"
            :highlight="!!nameError"
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
              <span class="flex min-h-24 flex-col items-center justify-center gap-2 rounded-lg border border-accented bg-default px-2 py-3 text-sm font-semibold text-highlighted transition-colors peer-enabled:cursor-pointer [@media(hover:hover)_and_(pointer:fine)]:hover:peer-enabled:border-primary/60 peer-checked:border-primary peer-checked:bg-primary/10 peer-checked:ring-1 peer-checked:ring-primary peer-focus-visible:outline-solid peer-focus-visible:outline-2 peer-focus-visible:outline-offset-2 peer-focus-visible:outline-primary peer-disabled:cursor-not-allowed peer-disabled:opacity-40">
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
                class="pointer-events-none absolute right-1.5 top-1.5 flex h-4 w-4 items-center justify-center rounded-full bg-primary text-inverted"
                aria-hidden="true"
              >
                <UIcon name="i-lucide-check" class="size-3" />
              </span>
            </label>
          </div>
        </fieldset>

        <UFormField
          :label="t('keys.groupLabel')"
          :required="groupIsRequired"
          size="lg"
          :error="groupError"
          :class="{ 'key-field-nudge': nudge && groupError }"
          data-test="key-field-group"
        >
          <template #hint>
            <USwitch
              v-model="formData.smart_routing"
              :label="t('keys.smartRouting')"
              size="sm"
              data-test="key-smart-routing"
            />
          </template>
          <p v-if="formData.smart_routing" class="mb-3 text-xs text-muted">
            {{ t('keys.smartRoutingHint') }}
          </p>
          <div v-if="formData.smart_routing" class="space-y-3" data-test="smart-routing-editor">
            <!-- 主路由始终可见，其余路由收进下面的 Disclosure，避免一开表单就铺满整屏。 -->
            <div
              v-if="formData.group_ids.length > 0"
              class="flex items-center gap-2 rounded-lg bg-elevated/40 px-3 py-2 ring ring-default"
              data-test="smart-route-primary"
            >
              <span class="shrink-0 text-xs font-medium text-muted">
                {{ t('keys.smartRoutingPrimaryRoute') }}
              </span>
              <div class="min-w-0 flex-1">
                <GroupBadge
                  v-if="smartRouteBadgeProps(formData.group_ids[0])"
                  v-bind="smartRouteBadgeProps(formData.group_ids[0])!"
                />
                <span v-else class="text-sm text-muted">#{{ formData.group_ids[0] }}</span>
              </div>
              <UButton
                icon="i-lucide-arrow-down"
                type="button"
                color="neutral"
                variant="ghost"
                size="xs"
                square
                :disabled="formData.group_ids.length < 2"
                :aria-label="t('keys.smartRoutingMoveDown')"
                data-test="smart-route-primary-down"
                @click="moveSmartRoute(0, 1)"
              />
              <UButton
                icon="i-lucide-x"
                type="button"
                color="error"
                variant="ghost"
                size="xs"
                square
                :aria-label="t('keys.smartRoutingRemove')"
                data-test="smart-route-primary-remove"
                @click="removeSmartRoute(0)"
              />
            </div>

            <!-- 其余分组：默认折叠，展开后按顺序显示完整候选路由。 -->
            <UCollapsible
              v-if="formData.group_ids.length > 1"
              v-model:open="smartRoutesOpen"
              data-test="smart-routes-collapsible"
            >
              <UButton
                type="button"
                color="neutral"
                variant="outline"
                block
                trailing-icon="i-lucide-chevron-down"
                :label="t('keys.smartRoutingOtherRoutes', { count: formData.group_ids.length - 1 })"
                :ui="{ trailingIcon: 'transition-transform duration-200 ease-out motion-reduce:transition-none group-data-[state=open]:rotate-180' }"
                data-test="smart-routes-toggle"
              />
              <template #content>
                <div class="mt-2 space-y-2" data-test="smart-routes-list">
                  <div
                    v-for="(groupId, offset) in formData.group_ids.slice(1)"
                    :key="`${groupId}-${offset + 1}`"
                    class="flex items-center gap-2 rounded-lg px-3 py-2 ring ring-default"
                    data-test="smart-route-item"
                  >
                    <span class="shrink-0 text-xs text-muted">{{ t('keys.smartRoutingRoute') }} {{ offset + 2 }}</span>
                    <div class="min-w-0 flex-1">
                      <GroupBadge
                        v-if="smartRouteBadgeProps(groupId)"
                        v-bind="smartRouteBadgeProps(groupId)!"
                      />
                      <span v-else class="text-sm text-muted">#{{ groupId }}</span>
                    </div>
                    <UButton
                      icon="i-lucide-arrow-up"
                      type="button"
                      color="neutral"
                      variant="ghost"
                      size="xs"
                      square
                      :aria-label="t('keys.smartRoutingMoveUp')"
                      data-test="smart-route-up"
                      @click="moveSmartRoute(offset + 1, -1)"
                    />
                    <UButton
                      icon="i-lucide-arrow-down"
                      type="button"
                      color="neutral"
                      variant="ghost"
                      size="xs"
                      square
                      :aria-label="t('keys.smartRoutingMoveDown')"
                      data-test="smart-route-down"
                      @click="moveSmartRoute(offset + 1, 1)"
                    />
                    <UButton
                      icon="i-lucide-x"
                      type="button"
                      color="error"
                      variant="ghost"
                      size="xs"
                      square
                      :aria-label="t('keys.smartRoutingRemove')"
                      data-test="smart-route-remove"
                      @click="removeSmartRoute(offset + 1)"
                    />
                  </div>
                </div>
              </template>
            </UCollapsible>

            <Select
              :model-value="null"
              :options="smartRoutingAddOptions"
              :placeholder="t('keys.smartRoutingAdd')"
              :error="!!groupError"
              :searchable="true"
              :search-placeholder="t('keys.searchGroup')"
              data-test="smart-route-add"
              @update:model-value="addSmartRoute"
            >
              <!-- 候选分组与普通分组下拉共用同一套富样式行：平台图标 + 分组名 + 倍率徽标 + 描述。 -->
              <template #option="{ option, selected }">
                <GroupOptionItem v-bind="groupOptionRowProps(option as unknown as GroupOption, selected)" />
              </template>
            </Select>
            <p v-if="formData.group_ids.length === 0" class="text-xs text-muted">{{ t('keys.smartRoutingEmpty') }}</p>
          </div>
          <Select
            v-else
            v-model="formData.group_id"
            :options="providerGroupOptions"
            :placeholder="t('keys.selectGroup')"
            :error="!!groupError"
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
              <GroupOptionItem v-bind="groupOptionRowProps(option as unknown as GroupOption, selected)" />
            </template>
          </Select>
          <p
            v-if="!showEditModal && groupsLoaded && !hasAvailableGroups"
            class="mt-2 text-xs text-warning"
            data-test="key-no-available-groups-hint"
          >
            {{ t('keys.createWithoutGroupHint') }}
          </p>
        </UFormField>

        <!-- 创建时只露出名称 / 提供商 / 分组，其余设置收进「更多设置」；编辑保持原样全部展开。
             内容用 v-show 保持挂载：字段状态、校验和现有选择器都不受折叠影响。 -->
        <button
          v-if="!showEditModal"
          type="button"
          class="key-more-toggle rounded-lg"
          :aria-expanded="createAdvancedOpen"
          aria-controls="key-advanced-settings"
          data-test="key-advanced-toggle"
          @click="createAdvancedOpen = !createAdvancedOpen"
        >
          <UIcon
            name="i-lucide-chevron-down"
            class="key-more-chevron size-4 shrink-0"
            :class="createAdvancedOpen ? 'rotate-180' : ''"
          />
          <span class="shrink-0 whitespace-nowrap font-medium text-highlighted">{{ t('keys.createStep.moreSettings') }}</span>
          <span class="min-w-0 truncate text-muted">{{ t('keys.createStep.moreSettingsHint') }}</span>
        </button>
        <Transition
          :css="false"
          @enter="onAdvancedEnter"
          @leave="onAdvancedLeave"
          @enter-cancelled="cancelCollapseMotion"
          @leave-cancelled="cancelCollapseMotion"
        >
        <div
          id="key-advanced-settings"
          v-show="showEditModal || createAdvancedOpen"
          class="space-y-5"
          data-test="key-advanced-settings"
        >
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
              <IpWhitelistAssist v-model="formData.ip_whitelist" class="mt-2" />
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
              <div class="mt-3 space-y-2">
                <div class="flex items-center justify-between gap-4">
                  <span id="key-auto-block-datacenter-ips-label" class="text-sm font-medium text-default">
                    {{ t('keys.autoBlockDatacenterIPs') }}
                  </span>
                  <USwitch
                    v-model="formData.auto_block_datacenter_ips"
                    aria-labelledby="key-auto-block-datacenter-ips-label"
                    aria-describedby="key-auto-block-datacenter-ips-hint"
                    data-test="key-auto-block-datacenter-ips"
                  />
                </div>
                <p id="key-auto-block-datacenter-ips-hint" class="text-xs text-muted">
                  {{ t('keys.autoBlockDatacenterIPsHint') }}
                </p>
              </div>
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
              <div class="flex-1 rounded-md bg-elevated px-3 py-2 text-sm tabular-nums">
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
                <div class="rounded-md bg-elevated px-3 py-2 text-sm tabular-nums">
                  <span :class="[
                    'font-medium',
                    selectedKey.usage_5h >= selectedKey.rate_limit_5h ? 'text-error' :
                    selectedKey.usage_5h >= selectedKey.rate_limit_5h * 0.8 ? 'text-warning' :
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
                      'h-full w-full origin-left rounded-full transition-[transform,background-color] duration-200 ease-[cubic-bezier(0.23,1,0.32,1)] motion-reduce:transition-none',
                      selectedKey.usage_5h >= selectedKey.rate_limit_5h ? 'bg-error' :
                      selectedKey.usage_5h >= selectedKey.rate_limit_5h * 0.8 ? 'bg-warning' :
                      'bg-success'
                    ]"
                    :style="{ transform: `scaleX(${Math.min(Math.max(selectedKey.usage_5h / selectedKey.rate_limit_5h, 0), 1)})` }"
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
                <div class="rounded-md bg-elevated px-3 py-2 text-sm tabular-nums">
                  <span :class="[
                    'font-medium',
                    selectedKey.usage_1d >= selectedKey.rate_limit_1d ? 'text-error' :
                    selectedKey.usage_1d >= selectedKey.rate_limit_1d * 0.8 ? 'text-warning' :
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
                      'h-full w-full origin-left rounded-full transition-[transform,background-color] duration-200 ease-[cubic-bezier(0.23,1,0.32,1)] motion-reduce:transition-none',
                      selectedKey.usage_1d >= selectedKey.rate_limit_1d ? 'bg-error' :
                      selectedKey.usage_1d >= selectedKey.rate_limit_1d * 0.8 ? 'bg-warning' :
                      'bg-success'
                    ]"
                    :style="{ transform: `scaleX(${Math.min(Math.max(selectedKey.usage_1d / selectedKey.rate_limit_1d, 0), 1)})` }"
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
                <div class="rounded-md bg-elevated px-3 py-2 text-sm tabular-nums">
                  <span :class="[
                    'font-medium',
                    selectedKey.usage_7d >= selectedKey.rate_limit_7d ? 'text-error' :
                    selectedKey.usage_7d >= selectedKey.rate_limit_7d * 0.8 ? 'text-warning' :
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
                      'h-full w-full origin-left rounded-full transition-[transform,background-color] duration-200 ease-[cubic-bezier(0.23,1,0.32,1)] motion-reduce:transition-none',
                      selectedKey.usage_7d >= selectedKey.rate_limit_7d ? 'bg-error' :
                      selectedKey.usage_7d >= selectedKey.rate_limit_7d * 0.8 ? 'bg-warning' :
                      'bg-success'
                    ]"
                    :style="{ transform: `scaleX(${Math.min(Math.max(selectedKey.usage_7d / selectedKey.rate_limit_7d, 0), 1)})` }"
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
        </div>
        </Transition>
        </div>

        <!-- 第二步：按服务端返回的可用因子渲染密码 / TOTP / Passkey，
             只显示该账号真实可用的选项；一个都没有时直接跳过本步。
             说明文字在对话框标题栏（createDialogDescription），这里不再重复一遍。 -->
        <div v-if="isCreateVerificationStep" class="key-step-pane space-y-3" data-test="key-create-verification">
          <div
            v-if="verificationFactorsLoading"
            class="rounded-lg bg-muted p-3 text-xs text-muted ring ring-inset ring-default"
            data-test="key-create-factors-loading"
          >
            {{ t('keys.reveal.factorsLoading') }}
          </div>
          <KeyFactorVerification
            v-else
            ref="createVerificationRef"
            :factors="verificationFactors"
            mode="create"
            hide-description
            :loading="submitting"
          />
        </div>
      </form>
      <template #footer>
        <!-- 创建：Quiet 页脚——左边是安静的文字按钮（第一步取消 / 第二步返回），右边是主按钮。 -->
        <div v-if="showCreateFlow" class="flex items-center justify-between gap-3" data-test="key-create-footer">
          <button
            v-if="isCreateVerificationStep"
            type="button"
            class="auth-step-back"
            :disabled="submitting"
            data-test="key-create-back"
            @click="backToCreateDetails"
          >
            {{ t('keys.createStep.back') }}
          </button>
          <button
            v-else
            type="button"
            class="auth-step-back"
            data-test="key-create-cancel"
            @click="closeModals"
          >
            {{ t('common.cancel') }}
          </button>

          <UButton
            v-if="createStep === 1 && requiresCreateVerification"
            type="button"
            size="lg"
            :ui="quietPrimaryUi"
            :disabled="submitting || verificationFactorsLoading"
            data-test="key-create-next"
            @click="goToCreateVerification"
          >
            {{ t('keys.createStep.next') }}
          </UButton>
          <UButton
            v-else
            form="key-form"
            type="submit"
            size="lg"
            :ui="quietPrimaryUi"
            :loading="submitting"
            :disabled="submitting || verificationFactorsLoading || (isCreateVerificationStep && !requiresCreateVerification)"
            data-tour="key-form-submit"
          >
            {{ submitting ? t('keys.saving') : t('common.create') }}
          </UButton>
        </div>
        <!-- 编辑：保持原有页脚 -->
        <div v-else class="flex justify-end gap-2">
          <UButton color="neutral" variant="outline" size="lg" @click="closeModals">
            {{ t('common.cancel') }}
          </UButton>
          <UButton
            form="key-form"
            type="submit"
            size="lg"
            :loading="submitting"
            :disabled="submitting || verificationFactorsLoading"
            data-tour="key-form-submit"
          >
            {{ submitting ? t('keys.saving') : t('common.update') }}
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

    <!-- Bulk Edit: applies only the ticked fields to every selected key. -->
    <BulkEditKeysModal
      :open="showBulkEditDialog"
      :selected-ids="selectedKeyIds.map((id) => Number(id))"
      :group-options="groupOptions"
      @close="showBulkEditDialog = false"
      @applied="handleBulkEditApplied"
    />

    <!-- Bulk Rotate: 二次确认，明确旧 Key 立即失效。 -->
    <ConfirmDialog
      :show="showBatchRotateDialog"
      :title="t('keys.batchRotate.title')"
      :message="t('keys.batchRotate.confirm', { count: selectedKeyCount })"
      :confirm-text="t('keys.batchRotate.confirmAction')"
      :cancel-text="t('common.cancel')"
      :danger="true"
      @confirm="openBatchRotateVerification"
      @cancel="showBatchRotateDialog = false"
    />

    <!-- Bulk Rotate 身份校验：批量轮换会下发每把 Key 的新明文，与创建同级，
         必须先完成三选一校验；一个可用因子都没有时直接跳过本步。 -->
    <BaseDialog
      :show="showBatchRotateVerifyDialog"
      :title="t('keys.batchRotate.verifyTitle')"
      @close="cancelBatchRotateVerification"
    >
      <div class="space-y-3" data-test="batch-rotate-verification">
        <p class="text-sm text-muted">{{ t('keys.batchRotate.verifyHint', { count: selectedKeyCount }) }}</p>
        <div
          v-if="batchRotateFactorsLoading"
          class="rounded-lg bg-muted p-3 text-xs text-muted ring ring-inset ring-default"
          data-test="batch-rotate-factors-loading"
        >
          {{ t('keys.reveal.factorsLoading') }}
        </div>
        <KeyFactorVerification
          v-else
          ref="batchRotateVerificationRef"
          :factors="batchRotateFactors"
          mode="create"
          :loading="batchRotateRunning"
        />
      </div>
      <template #footer>
        <div class="flex justify-end gap-2">
          <UButton
            color="neutral"
            variant="outline"
            size="lg"
            :disabled="batchRotateRunning"
            data-test="batch-rotate-verify-cancel"
            @click="cancelBatchRotateVerification"
          >
            {{ t('common.cancel') }}
          </UButton>
          <UButton
            color="primary"
            size="lg"
            :loading="batchRotateRunning"
            :disabled="batchRotateRunning || batchRotateFactorsLoading"
            data-test="batch-rotate-verify-submit"
            @click="confirmBatchRotateWithVerification"
          >
            {{ t('keys.batchRotate.confirmAction') }}
          </UButton>
        </div>
      </template>
    </BaseDialog>

    <!-- 单条轮换身份校验：轮换会下发新明文，与创建同级必须先完成三选一校验。 -->
    <BaseDialog
      :show="showRotateVerifyDialog"
      :title="t('keys.rotateVerify.title')"
      @close="cancelRotateVerification"
    >
      <div class="space-y-3" data-test="rotate-verification">
        <p class="text-sm text-muted">
          {{ t('keys.rotateVerify.hint', { name: rotateVerifyTarget?.name ?? '' }) }}
        </p>
        <div
          v-if="rotateVerifyLoading"
          class="rounded-lg bg-muted p-3 text-xs text-muted ring ring-inset ring-default"
          data-test="rotate-factors-loading"
        >
          {{ t('keys.reveal.factorsLoading') }}
        </div>
        <KeyFactorVerification
          v-else
          ref="rotateVerificationRef"
          :factors="verificationFactors"
          mode="create"
        />
      </div>
      <template #footer>
        <div class="flex justify-end gap-2">
          <UButton color="neutral" variant="outline" size="lg" @click="cancelRotateVerification">
            {{ t('common.cancel') }}
          </UButton>
          <UButton
            color="primary"
            size="lg"
            data-test="rotate-verify-submit"
            @click="confirmRotateVerification"
          >
            {{ t('keys.rotateVerify.confirm') }}
          </UButton>
        </div>
      </template>
    </BaseDialog>

    <!-- Bulk Rotate result: 逐条状态 + 一键全部复制 + 逐条重新封存。 -->
    <BaseDialog
      :show="showBatchRotateResult"
      :title="t('keys.batchRotate.resultTitle')"
      width="wide"
      @close="closeBatchRotateResult"
    >
      <div v-if="batchRotateResult" class="space-y-4" data-test="batch-rotate-result">
        <div class="flex flex-wrap items-center gap-2 text-sm">
          <span class="font-medium">{{ t('keys.batchRotate.summary', {
            succeeded: batchRotateResult.succeeded_count,
            failed: batchRotateResult.failed_count
          }) }}</span>
          <UButton
            v-if="batchRotateOkItems.length > 0"
            color="primary"
            size="xs"
            :icon="batchRotateCopiedAll ? 'i-lucide-check' : 'i-lucide-copy'"
            :label="batchRotateCopiedAll ? t('common.copied') : t('keys.batchRotate.copyAll')"
            data-test="batch-rotate-copy-all"
            @click="copyAllRotatedKeys"
          />
        </div>

        <div class="max-h-72 space-y-2 overflow-y-auto">
          <div
            v-for="item in batchRotateResult.results"
            :key="item.id"
            class="rounded-lg border border-default p-2.5 text-sm"
            :data-test="`batch-rotate-result-${item.id}`"
          >
            <div class="flex items-center justify-between gap-2">
              <span class="font-medium">{{ batchRotateNames[item.id] ?? `#${item.id}` }}</span>
              <span
                :class="['text-xs font-semibold', item.status === 'ok' ? 'text-success' : 'text-error']"
              >
                {{ item.status === 'ok' ? t('keys.batchRotate.itemOk') : t('keys.batchRotate.itemFailed') }}
              </span>
            </div>
            <code
              v-if="item.status === 'ok' && item.key"
              class="mt-1 block wrap-anywhere rounded bg-elevated px-1.5 py-0.5 font-mono text-xs"
              data-test="batch-rotate-plaintext"
            >{{ item.key }}</code>
            <p v-else class="mt-1 text-xs text-muted">{{ item.error_code || t('keys.batchRotate.failed') }}</p>
            <p
              v-if="batchRotateSealStatus[item.id]"
              class="mt-1 text-xs"
              :class="batchRotateSealStatus[item.id] === 'sealed' ? 'text-success' : 'text-error'"
              data-test="batch-rotate-seal-status"
            >
              {{ batchRotateSealStatus[item.id] === 'sealed' ? t('keys.batchRotate.itemSealed') : t('keys.batchRotate.itemSealFailed') }}
            </p>
          </div>
        </div>

        <!-- 轮换删除了旧密文：原本用登录密码封存过的 Key 可以在这里逐条重新封存。 -->
        <div
          v-if="canSealKeys && batchRotateSealTargets.length > 0 && batchRotateOkItems.length > 0"
          class="space-y-2 rounded-lg border border-default p-3"
          data-test="batch-rotate-seal"
        >
          <UCheckbox
            v-model="batchRotateSealEnabled"
            :disabled="batchRotateSealing"
            :label="t('keys.batchRotate.sealOptIn')"
            :description="t('keys.batchRotate.sealHint')"
            data-test="batch-rotate-seal-checkbox"
          />
          <div v-if="batchRotateSealEnabled" class="space-y-1.5 pl-6">
            <label for="batch-rotate-seal-password" class="block text-xs font-medium text-default">
              {{ t('keys.seal.passwordLabel') }}
            </label>
            <UInput
              id="batch-rotate-seal-password"
              v-model="batchRotateSealPassword"
              type="password"
              autocomplete="current-password"
              :disabled="batchRotateSealing"
              class="w-full"
              data-test="batch-rotate-seal-password"
            />
            <div class="flex flex-wrap items-center gap-2">
              <UButton
                color="neutral"
                variant="soft"
                size="xs"
                :loading="batchRotateSealing"
                :disabled="batchRotateSealing"
                :label="t('keys.batchRotate.sealAction', { count: batchRotateSealableIds.length })"
                data-test="batch-rotate-seal-submit"
                @click="resealRotatedKeys"
              />
              <span
                v-if="batchRotateSealStatus && Object.keys(batchRotateSealStatus).length > 0"
                class="text-xs text-muted"
                data-test="batch-rotate-seal-summary"
              >
                {{ t('keys.batchRotate.sealSummary', {
                  sealed: batchRotateSealSummary.sealed,
                  failed: batchRotateSealSummary.failed
                }) }}
              </span>
            </div>
          </div>
          <p v-if="batchRotateSealError" class="text-xs text-error" role="alert" data-test="batch-rotate-seal-error">
            {{ batchRotateSealError }}
          </p>
        </div>

        <div class="flex justify-end">
          <UButton color="primary" :label="t('keys.secretDialog.done')" data-test="batch-rotate-done" @click="closeBatchRotateResult" />
        </div>
      </div>
    </BaseDialog>

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
    <KeyUsageDialog
      :show="showUsageDialog"
      :api-key="usageDialogKey"
      @close="closeUsageDialog"
    />

    <UseKeyModal
      :show="showUseKeyModal"
      :api-key="useKeyPlaintext"
      :base-url="publicSettings?.api_base_url || ''"
      :platform="selectedKey?.group?.platform || null"
      :allow-messages-dispatch="selectedKey?.group?.allow_messages_dispatch || false"
      show-guide-link
      @close="closeUseKeyModal"
      @assign-group="assignGroupForRevealedKey"
      @open-guide="openAgentGuideFromModal"
    />

    <!-- 明文密钥只展示这一次：复制、使用引导、导入 CCS 都从这里进入。
         同时在这里让用户用登录密码把 key 加密后存到服务端，刷新后才能再查看。 -->
    <ApiKeySecretDialog
      :show="showSecretDialog"
      :api-key="secretRow"
      :secret="secretPlaintext"
      :hide-ccs-import="!!publicSettings?.hide_ccs_import_button"
      :can-seal="sealAvailableForCreate"
      :auth-factor="createAuthFactor"
      :verified-password="receiptVerifiedPassword"
      @close="closeSecretDialog"
      @use="useRevealedKey"
      @import-ccs="importRevealedToCcswitch"
      @sealed="markKeySealedLocally"
    />

    <!-- 刷新后查看 key：只在浏览器本地用登录密码解密服务端密文；解密后同样可直接导入 CCS 或查看使用配置 -->
    <RevealApiKeyDialog
      :show="showRevealDialog"
      :api-key="revealRow"
      :hide-ccs-import="!!publicSettings?.hide_ccs_import_button"
      @close="closeRevealDialog"
      @rotate="rotateFromReveal"
      @use="useDecryptedKey"
      @import-ccs="importDecryptedToCcswitch"
    />

    <!-- 创建时没启用后续查看：说明无法找回，只能轮换 -->
    <ConfirmDialog
      :show="showNoSealDialog"
      :title="t('keys.noSeal.title')"
      :message="t('keys.noSeal.message')"
      :confirm-text="t('keys.noSeal.rotate')"
      :cancel-text="t('common.cancel')"
      @confirm="rotateFromNoSeal"
      @cancel="closeNoSealDialog"
    />

    <!-- CCS import: pick platform + model, then open ccswitch:// deeplink -->
    <BaseDialog
      :show="showCcsImportModal"
      :title="t('keys.ccsImport.title')"
      width="narrow"
      @close="closeCcsImportModal"
    >
      <div class="space-y-4">
        <p class="text-sm text-muted">
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
          <div class="mb-1.5 flex items-center justify-between gap-2">
            <label class="input-label mb-0" for="ccs-import-model">{{ t('keys.ccsImport.model') }}</label>
            <button
              v-if="pendingCcsRow && !ccsModelsLoading"
              type="button"
              data-test="ccs-import-model-reload"
              class="inline-flex min-h-11 items-center gap-1 rounded px-1 text-xs font-medium text-primary focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary [@media(hover:hover)_and_(pointer:fine)]:hover:underline [@media(pointer:fine)]:min-h-0"
              @click="loadCcsModels(pendingCcsRow)"
            >
              <UIcon name="i-lucide-refresh-cw" class="size-3" aria-hidden="true" />
              <span>{{ ccsModelsError || ccsAvailableModels.length === 0 ? t('keys.ccsImport.modelRetry') : t('keys.ccsImport.modelRefresh') }}</span>
            </button>
          </div>
          <Select
            id="ccs-import-model"
            :model-value="ccsImportModel"
            :options="ccsModelOptions"
            :loading="ccsModelsLoading"
            :disabled="ccsModelsLoading"
            :placeholder="ccsModelsLoading ? t('keys.ccsImport.modelLoading') : t('keys.ccsImport.modelPlaceholder')"
            :empty-text="ccsModelsError ? t('keys.ccsImport.modelLoadFailed') : t('keys.ccsImport.modelEmpty')"
            :searchable="true"
            :creatable="ccsModelsError"
            :clearable="true"
            :aria-label="t('keys.ccsImport.model')"
            @update:model-value="onCcsModelChange"
          />
          <p
            class="input-hint mt-1"
            :class="ccsModelsError ? 'text-error' : ''"
            data-test="ccs-import-model-hint"
          >
            {{ ccsModelHintText }}
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
        <div
          v-if="ccsDeeplinkFallbackVisible && ccsDeeplink"
          data-test="ccs-import-fallback"
          class="space-y-2.5 rounded-xl border border-default bg-muted/60 p-3 text-xs text-default"
        >
          <p class="leading-5 text-toned">{{ t('keys.ccSwitchNotInstalled') }}</p>
          <div class="flex flex-wrap items-center gap-2">
            <a
              :href="ccsDeeplink"
              data-test="ccs-import-retry"
              class="inline-flex min-h-8 items-center gap-1 rounded-lg bg-primary px-2.5 py-1 text-xs font-medium text-inverted transition-colors focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary [@media(hover:hover)_and_(pointer:fine)]:hover:bg-primary/90 [@media(pointer:coarse)]:min-h-11"
              @click="onCcsDeeplinkClick"
            >
              {{ t('keys.ccsImport.modelRetry') }}
            </a>
            <button
              type="button"
              data-test="ccs-import-copy-deeplink"
              class="inline-flex min-h-8 items-center gap-1 rounded-lg border border-accented bg-default px-2.5 py-1 text-xs font-medium text-default transition-colors focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary [@media(hover:hover)_and_(pointer:fine)]:hover:bg-elevated [@media(pointer:coarse)]:min-h-11"
              @click="copyCcsDeeplink"
            >
              {{ ccsDeeplinkCopied ? t('common.copied') : t('common.copy') }}
            </button>
            <a
              :href="ccSwitchDownloadUrl"
              target="_blank"
              rel="noopener noreferrer"
              data-test="ccs-import-download"
              class="inline-flex min-h-8 items-center gap-1 rounded-lg border border-accented bg-default px-2.5 py-1 text-xs font-medium text-default transition-colors focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary [@media(hover:hover)_and_(pointer:fine)]:hover:bg-elevated [@media(pointer:coarse)]:min-h-11"
            >
              {{ t('docs.agentSetup.downloadCcSwitch') }}
            </a>
          </div>
        </div>
      </div>
      <template #footer>
        <div class="flex justify-end gap-2">
          <UButton color="neutral" variant="outline" size="lg" @click="closeCcsImportModal">
            {{ t('common.cancel') }}
          </UButton>
          <a
            v-if="!ccsImportDeeplinkUnsupported && !ccsModelsLoading && ccsDeeplink"
            :href="ccsDeeplink"
            data-test="ccs-import-launch"
            class="inline-flex h-10 items-center justify-center gap-1.5 rounded-xl bg-primary px-4 text-sm font-medium text-inverted transition-colors focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary [@media(hover:hover)_and_(pointer:fine)]:hover:bg-primary/90 [@media(pointer:coarse)]:min-h-11"
            @click="onCcsDeeplinkClick"
          >
            {{ t('common.import') }}
          </a>
          <UButton v-else size="lg" disabled>
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
        data-test="group-selector-dropdown"
        :class="[
          'fixed z-50 flex w-max max-w-[calc(100vw-16px)] max-h-[calc(100dvh-16px)] flex-col overflow-hidden rounded-lg bg-default shadow-(--app-shadow-popover) ring ring-default transition-[opacity,transform] duration-150 ease-[cubic-bezier(0.23,1,0.32,1)] motion-reduce:transition-none sm:min-w-[380px]',
          dropdownPosition.bottom !== undefined ? 'origin-bottom' : 'origin-top'
        ]"
        style="pointer-events: auto !important;"
        :style="{
          top: dropdownPosition.top !== undefined ? dropdownPosition.top + 'px' : undefined,
          bottom: dropdownPosition.bottom !== undefined ? dropdownPosition.bottom + 'px' : undefined,
          left: dropdownPosition.left + 'px',
          maxHeight: dropdownPosition.maxHeight + 'px'
        }"
      >
        <!-- Search box -->
        <div class="shrink-0 border-b border-default p-2">
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
        <div class="min-h-0 flex-1 max-h-80 overflow-y-auto p-1.5">
          <button
            v-for="option in filteredGroupOptions"
            :key="option.value ?? 'null'"
            @click="changeGroup(selectedKeyForGroup!, option.value)"
            :class="[
              'flex w-full items-center justify-between rounded-lg px-3 py-2.5 text-sm transition-colors',
              'border-b border-muted last:border-0',
              selectedKeyForGroup?.group_id === option.value ||
              (!selectedKeyForGroup?.group_id && option.value === null)
                ? 'bg-primary/10'
                : '[@media(hover:hover)_and_(pointer:fine)]:hover:bg-elevated'
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
              :signals="option.signals"
              :show-rate-change="option.showRateChange"
              :status-class="groupStatusClasses[option.value] ?? null"
              :selected="
                selectedKeyForGroup?.group_id === option.value ||
                (!selectedKeyForGroup?.group_id && option.value === null)
              "
            />
          </button>
          <!-- Empty state when search has no results -->
          <div v-if="filteredGroupOptions.length === 0" class="py-4 text-center text-sm text-dimmed">
            {{ t('keys.noGroupFound') }}
          </div>
        </div>
      </div>
    </Teleport>
    <!-- 创建/轮换后 5 分钟内的「再复制一次」浮条（不是 modal）；明文只从内存 store 取。 -->
    <EphemeralKeyBanner
      v-if="ephemeralBarKeyId !== null"
      :key="ephemeralBarKeyId"
      :key-id="ephemeralBarKeyId"
      @expired="ephemeralBarKeyId = null"
    />
  </AppLayout>
</template>

<script setup lang="ts">
import '@/styles/console-workspace.css'
	import { ref, reactive, computed, watch, nextTick, onMounted, onUnmounted, inject, type ComponentPublicInstance } from 'vue'
	import { routeLocationKey, routerKey } from 'vue-router'
	import { useI18n } from 'vue-i18n'
	import { useAppStore } from '@/stores/app'
	import { useOnboardingStore } from '@/stores/onboarding'
	import { useAuthStore } from '@/stores/auth'
	import { useEphemeralKeyStore } from '@/stores/ephemeralKeys'
import { cancelCollapseMotion, collapseHeight, expandHeight } from '@/utils/collapseMotion'
import BulkEditKeysModal from '@/components/keys/BulkEditKeysModal.vue'
import IpWhitelistAssist from '@/components/keys/IpWhitelistAssist.vue'
import { getPersistedPageSize } from '@/composables/usePersistedPageSize'
import { useClipboard } from '@/composables/useClipboard'
import { useAppUpdate } from '@/composables/useAppUpdate'

const { t, locale } = useI18n()
const router = inject(routerKey, null)
const route = inject(routeLocationKey, null)

// The concurrency cell carries its own explanation: hover shows this sentence, keyboard
// focus and screen readers get it from aria-label.
const concurrencyHint = (row: Pick<ApiKey, 'concurrency_limit'>) => {
  const limit = row.concurrency_limit
  return limit ? t('keys.currentConcurrencyHint', { limit }) : t('keys.currentConcurrencyNoLimitHint')
}
import { keysAPI, authAPI, usageAPI, userGroupsAPI, endpointsAPI, keyRotationGateAPI } from '@/api'
import AppLayout from '@/components/layout/AppLayout.vue'
import TablePageLayout from '@/components/layout/TablePageLayout.vue'
	import DataTable from '@/components/common/DataTable.vue'
	import Pagination from '@/components/common/Pagination.vue'
	import BaseDialog from '@/components/common/BaseDialog.vue'
	import QuietStepHead from '@/components/common/QuietStepHead.vue'
	import KeyUsageDialog from '@/components/user/KeyUsageDialog.vue'
	import ApiKeySecretDialog from '@/components/keys/ApiKeySecretDialog.vue'
	import EphemeralKeyBanner from '@/components/keys/EphemeralKeyBanner.vue'
	import RevealApiKeyDialog from '@/components/keys/RevealApiKeyDialog.vue'
	import TwoFactorSuggestionBanner from '@/components/user/TwoFactorSuggestionBanner.vue'
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
	import { loadGroupStatusClasses } from '@/features/channel-monitor-v2/groupStatusDot'
	import GroupRateSignals from '@/components/common/GroupRateSignals.vue'
	import PlatformIcon from '@/components/common/PlatformIcon.vue'
	import KeyFactorVerification from '@/components/keys/KeyFactorVerification.vue'
	import { quietPrimaryUi } from '@/components/keys/quietUi'
	import { needsKeyVerification } from '@/utils/apiKeyVerification'
	import UserPageHeader from '@/components/user/UserPageHeader.vue'
	import type { ApiKey, ApiKeyVerificationFactors, ApiKeyVerificationPayload, CustomEndpoint, Group, PublicSettings, SubscriptionType, GroupPlatform, UpdateApiKeyRequest } from '@/types'
import type { BatchRotateKeyItem, BatchRotateKeysResult, ApiKeyOverview24h } from '@/api/keys'
import { sealApiKey } from '@/utils/apikeySeal'
import type { Column } from '@/components/common/types'
import type { GroupSignalSource } from '@/utils/groupRateSignals'
import type { BatchApiKeyUsageStats } from '@/api/usage'
import { formatDateTime } from '@/utils/format'
import { accountLacksLoginPassword } from '@/utils/accountPassword'
import { rateLimitWindows, RATE_LIMIT_BAR_CLASS, RATE_LIMIT_TEXT_CLASS } from '@/utils/keyRateLimits'
import { platformBadgeLightClass } from '@/utils/platformColors'
import {
  buildCcSwitchImportDeeplink,
  CC_SWITCH_APP_LABEL_KEYS,
  CC_SWITCH_APPS,
  ccSwitchProviderDeeplinkSupported,
  defaultCcSwitchModel,
  fetchCcSwitchModels,
  isCcSwitchApp,
  pickCcSwitchModel,
  resolveCcSwitchImportConfig,
  type CcSwitchApp
} from '@/utils/ccswitchImport'
import { CC_SWITCH_HOME_EN, CC_SWITCH_HOME_ZH } from '@/data/agentSetupGuides'

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
  signals: GroupSignalSource
  showRateChange: boolean
}

const appStore = useAppStore()
const onboardingStore = useOnboardingStore()
const authStore = useAuthStore()
// 本标签页创建/轮换得到的明文只放内存，刷新或登出即失效。
const ephemeralKeys = useEphemeralKeyStore()
const { copyToClipboard } = useClipboard()
// 第三方创建且没有邮箱/密码的账号无法用登录密码派生密钥，隐藏该选项。
const canSealKeys = computed(() => !!authStore.user && !accountLacksLoginPassword(authStore.user))

const allColumns = computed<Column[]>(() => [
  { key: 'name', label: t('common.name'), sortable: true },
  { key: 'id', label: t('keys.id'), sortable: true },
  { key: 'key', label: t('keys.apiKey'), sortable: false },
  { key: 'group', label: t('keys.group'), sortable: false },
  { key: 'current_concurrency', label: t('keys.currentConcurrency'), sortable: true },
  { key: 'usage', label: t('keys.usage'), sortable: false },
  { key: 'overview', label: t('keys.overview.column'), sortable: false },
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
const showBulkEditDialog = ref(false)

// 批量轮换：二次确认（旧 Key 立即失效）→ 逐条结果 → 一键全部复制 → 逐条重新封存。
const showBatchRotateDialog = ref(false)
const batchRotateRunning = ref(false)
// 批量轮换的身份校验步：轮换会下发新明文，与创建同级必须三选一校验。
const showBatchRotateVerifyDialog = ref(false)
const batchRotateFactors = ref<ApiKeyVerificationFactors | null>(null)
const batchRotateFactorsLoading = ref(false)
const batchRotateVerificationPayload = ref<ApiKeyVerificationPayload | null>(null)
const batchRotateVerificationRef = ref<InstanceType<typeof KeyFactorVerification> | null>(null)
// 单条轮换的身份校验步（轮换即下发明文，与创建同级）。
const showRotateVerifyDialog = ref(false)
const rotateVerifyTarget = ref<ApiKey | null>(null)
const rotateVerifyLoading = ref(false)
const rotateVerifyPayload = ref<ApiKeyVerificationPayload | null>(null)
const rotateVerificationRef = ref<InstanceType<typeof KeyFactorVerification> | null>(null)
let rotateVerifyResolve: ((payload: ApiKeyVerificationPayload | null) => void) | null = null
const batchRotateResult = ref<BatchRotateKeysResult | null>(null)
const showBatchRotateResult = ref(false)
const batchRotateNames = ref<Record<number, string>>({})
const batchRotateCopiedAll = ref(false)
const batchRotateSealEnabled = ref(false)
const batchRotateSealPassword = ref('')
const batchRotateSealing = ref(false)
const batchRotateSealError = ref('')
const batchRotateSealStatus = ref<Record<number, 'sealed' | 'failed' | 'skipped'>>({})

// A bulk edit never touches a row we cannot see: reload the current page after
// the modal reports back, so group moves and status flips are visible at once.
const handleBulkEditApplied = async (result: { updated: number; failed: number }) => {
  showBulkEditDialog.value = false
  if (result.failed === 0) {
    appStore.showSuccess(t('keys.bulkEdit.appliedOk', { count: result.updated }))
  } else {
    appStore.showError(t('keys.bulkEdit.appliedPartial', { updated: result.updated, failed: result.failed }))
  }
  await loadApiKeys()
  void syncRotationRemediation()
}
const keyRowId = (row: ApiKey) => row.id
const groups = ref<Group[]>([])
// 分组列表是否成功加载过：为空数组和"还没加载/加载失败"必须区分，
// 只有确认「一个可绑定分组都没有」时才允许建无分组 key。
const groupsLoaded = ref(false)
const hasAvailableGroups = computed(() => groups.value.length > 0)
const availableGroupsById = computed(() => new Map(groups.value.map((group) => [group.id, group])))
const loading = ref(false)
const submitting = ref(false)
const now = ref(new Date())
let resetTimer: ReturnType<typeof setInterval> | null = null
const usageStats = ref<Record<string, BatchApiKeyUsageStats>>({})
// 近 24h 概览（调用次数 / 消耗额度）按 Key id 索引，来自一次批量接口。
const keyOverview = ref<Record<number, ApiKeyOverview24h>>({})
// 「再复制一次」浮条：只记当前展示的 Key id，明文永远从内存 store 取。
const ephemeralBarKeyId = ref<number | null>(null)
const showUsageDialog = ref(false)
const usageDialogKey = ref<ApiKey | null>(null)
const openUsageDialog = (key: ApiKey) => {
  usageDialogKey.value = key
  showUsageDialog.value = true
}
const closeUsageDialog = () => {
  showUsageDialog.value = false
  usageDialogKey.value = null
}
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

// ---- 待整改（与充值/兑换门禁同源）----
// 汇总优先用门禁状态接口（覆盖所有分页）；接口不可用时退化为当前页行数。
const rotationSummaryPending = ref<number | null>(null)
const pagePendingRotationCount = computed(
  () => apiKeys.value.filter((key) => key.needs_rotation === true).length
)
const pendingRotationCount = computed(() => {
  const server = rotationSummaryPending.value
  return typeof server === 'number' && server >= 0 ? server : pagePendingRotationCount.value
})

// 一键处理：一次拉回当前筛选下的密钥（后端单页上限 1000），只显示待整改的行。
const showRotationOnly = ref(false)
const rotationOnlyRows = ref<ApiKey[]>([])
const rotationOnlyLoading = ref(false)

const tableRows = computed(() => (showRotationOnly.value ? rotationOnlyRows.value : apiKeys.value))

// 待整改原因只做分类展示，判定本身完全由后端 needs_rotation 决定。
const rotationReasonText = (row: ApiKey): string => {
  const reasons = row.rotation_reasons ?? []
  const parts: string[] = []
  if (reasons.includes('created_before_cutoff')) parts.push(t('keys.rotationReasonCreatedBeforeCutoff'))
  if (reasons.includes('no_ip_allowlist')) parts.push(t('keys.rotationReasonNoIPAllowlist'))
  return parts.join(' · ')
}

const activeListFilters = () => {
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
  return filters
}

const loadRotationSummary = async () => {
  const api = keyRotationGateAPI as
    | { getStatus?: () => Promise<{ pending_key_count?: number }> }
    | undefined
  if (!api || typeof api.getStatus !== 'function') return
  try {
    const status = await api.getStatus()
    if (status && typeof status.pending_key_count === 'number') {
      rotationSummaryPending.value = status.pending_key_count
    }
  } catch {
    // 门禁状态读失败不影响密钥页；行内角标仍来自每行的 needs_rotation。
  }
}

const fetchRotationOnlyRows = async (): Promise<ApiKey[]> => {
  // Walk the filtered pages until every pending key is collected: the backend
  // may clamp page_size below the requested 1000 (admin table settings).
  const collected: ApiKey[] = []
  const pageSize = 1000
  for (let page = 1; page <= 40; page += 1) {
    const response = await keysAPI.list(page, pageSize, activeListFilters())
    collected.push(...response.items.filter((key) => key.needs_rotation === true))
    const pages = typeof response.pages === 'number' ? response.pages : 1
    if (page >= pages || response.items.length === 0) break
  }
  return collected
}

const startRotationRemediation = async () => {
  showRotationOnly.value = true
  rotationOnlyLoading.value = true
  try {
    rotationOnlyRows.value = await fetchRotationOnlyRows()
    if (rotationOnlyRows.value.length === 0) {
      appStore.showSuccess(t('keys.rotationAllClear'))
      showRotationOnly.value = false
    }
  } catch {
    showRotationOnly.value = false
    appStore.showError(t('keys.failedToLoad'))
  } finally {
    rotationOnlyLoading.value = false
  }
}

const stopRotationRemediation = async () => {
  showRotationOnly.value = false
  rotationOnlyRows.value = []
  await loadApiKeys()
}

// 整改动作成功后：刷新汇总数字；如果正处在"只看待整改"视图里，同步收拢剩余行。
const syncRotationRemediation = async () => {
  void loadRotationSummary()
  if (!showRotationOnly.value) return
  try {
    rotationOnlyRows.value = await fetchRotationOnlyRows()
    if (rotationOnlyRows.value.length === 0) {
      appStore.showSuccess(t('keys.rotationAllClear'))
      showRotationOnly.value = false
      await loadApiKeys()
    }
  } catch {
    // 保留当前行，下一次操作或刷新会纠正。
  }
}

const showCreateModal = ref(false)
const showEditModal = ref(false)
// 创建 Key 的三选一身份校验（密码 / TOTP / Passkey）：第二步按实际绑定的因子渲染。
const createStep = ref<1 | 2>(1)
const verificationFactors = ref<ApiKeyVerificationFactors | null>(null)
const verificationFactorsLoading = ref(false)
const verificationPayload = ref<ApiKeyVerificationPayload | null>(null)
const createAuthFactor = ref('')
// 这次创建 / 轮换里用户已经输入过（并且服务端已校验过）的登录密码：只在内存里，交给回执弹窗做「以后可查看」，
// 免得再让用户输一遍；回执关闭时清空，不进任何存储。
const receiptVerifiedPassword = ref('')
const verifiedPasswordOf = (verification: ApiKeyVerificationPayload | string | null | undefined): string =>
  typeof verification === 'object' && verification?.method === 'password' ? (verification.password ?? '') : ''
const createVerificationRef = ref<InstanceType<typeof KeyFactorVerification> | null>(null)
const showCreateFlow = computed(() => showCreateModal.value && !showEditModal.value)
// 创建时「更多设置」默认收起；编辑不折叠（见模板里的 key-advanced-settings）。
const createAdvancedOpen = ref(false)
// 「更多设置」展开 / 收起的高度动画。编辑弹窗里它始终展开、弹窗关闭时的复位也不该再播一遍，所以这两种情况直接完成。
const onAdvancedEnter = (el: Element, done: () => void) => (showEditModal.value || !showCreateModal.value ? done() : expandHeight(el, done))
const onAdvancedLeave = (el: Element, done: () => void) => (showEditModal.value || !showCreateModal.value ? done() : collapseHeight(el, done))
// 标题栏下的一句话跟着步骤走；编辑弹窗没有。
const createDialogDescription = computed(() => {
  if (!showCreateFlow.value) return undefined
  return isCreateVerificationStep.value
    ? t('keys.createStep.verifyHint')
    : t('keys.createStep.detailsHint')
})
const appUpdate = useAppUpdate()
const isCreateVerificationStep = computed(() => showCreateFlow.value && createStep.value === 2)
// 只有开了 TOTP / 绑了 Passkey 的账号才有校验这一步；只有登录密码的账号（含 Google / GitHub 登录的用户）
// 直接创建，不再被卡在输密码上。口径以服务端为准，见 utils/apiKeyVerification.ts。
const requiresCreateVerification = computed(() => needsKeyVerification(verificationFactors.value))
// 「以后可查看」以服务端判定为准（本地解密必须知道登录密码），旧接口缺字段时回退本地判断。
const sealAvailableForCreate = computed(
  () => verificationFactors.value?.seal_available ?? canSealKeys.value,
)

/** 打开创建弹窗：重置步骤并拉取该账号真实可用的验证因子。 */
const openCreateModal = () => {
  // 页面是更新前打开的、已经确认过期：旧代码调用升级过的创建接口只会失败，直接要求刷新。
  if (appUpdate.updateAvailable.value) {
    appUpdate.blockingOpen.value = true
    return
  }
  createStep.value = 1
  resetCreateValidation()
  createAdvancedOpen.value = false
  verificationPayload.value = null
  createAuthFactor.value = ''
  createVerificationRef.value?.reset()
  showCreateModal.value = true
  void loadVerificationFactors()
  // 同时在后台做一次实时检查（不挡着弹窗弹出）：发现过期就收起这个弹窗，换成必须刷新的对话框。
  void appUpdate.ensureFresh().then((fresh) => {
    if (!fresh) closeModals()
  })
}

const loadVerificationFactors = async (): Promise<ApiKeyVerificationFactors> => {
  verificationFactorsLoading.value = true
  try {
    verificationFactors.value = await keysAPI.getVerificationFactors()
  } catch {
    // 接口临时不可用时：服务端仍然是校验的唯一依据，这里只决定前端展示，不能因为一次抖动
    // 把创建流程整个堵死，更不能再把所有人拦在输密码这一步。开了 TOTP / Passkey 的账号如果
    // 恰好撞上这次抖动，服务端会拒绝并给出带刷新指引的提示，重新打开弹窗即可。
    verificationFactors.value = {
      password_available: canSealKeys.value,
      totp_enabled: false,
      passkey_count: 0,
      preferred: canSealKeys.value ? 'password' : '',
      available_methods: canSealKeys.value ? ['password'] : [],
      seal_available: canSealKeys.value,
      create_verification_required: false,
    }
  } finally {
    verificationFactorsLoading.value = false
  }
  return verificationFactors.value as ApiKeyVerificationFactors
}

/** 第一步 → 第二步：先做与提交相同的必填校验，通过后进入身份校验。 */
const goToCreateVerification = () => {
  if (!validateCreateBasics()) return
  createStep.value = 2
}

const backToCreateDetails = () => {
  createStep.value = 1
}
const showDeleteDialog = ref(false)
const showResetQuotaDialog = ref(false)
const showResetRateLimitDialog = ref(false)
const showUseKeyModal = ref(false)
const showCcsImportModal = ref(false)
const showColumnDropdown = ref(false)
// 正在轮换（重置）密钥的 key ID，用于禁用按钮并显示转圈。
const rotatingKeyId = ref<number | null>(null)
// CCS 导入只在刚拿到明文回执时可用：这里带上明文，用完即弃。
type ApiKeyWithPlaintext = ApiKey & { key: string }
const pendingCcsRow = ref<ApiKeyWithPlaintext | null>(null)
const ccsImportApp = ref<CcSwitchApp>('claude')
const ccsImportModel = ref('')
const ccsAvailableModels = ref<string[]>([])
const ccsModelsLoading = ref(false)
const ccsModelsLoaded = ref(false)
const ccsModelsError = ref(false)
const ccsDeeplinkFallbackVisible = ref(false)
const ccsDeeplinkCopied = ref(false)
let ccsModelsAbort: AbortController | null = null
let ccsDeeplinkTimer: ReturnType<typeof setTimeout> | null = null
let ccsDeeplinkCleanup: (() => void) | null = null
let ccsDeeplinkCopiedTimer: ReturnType<typeof setTimeout> | null = null

const CCS_USAGE_SCRIPT = `({
    request: {
      url: "{{baseUrl}}".replace(/\\/+$/, "").replace(/\\/v1$/, "") + "/v1/usage",
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

const ccsDeeplink = computed(() => {
  if (!pendingCcsRow.value || !ccSwitchProviderDeeplinkSupported(ccsImportApp.value)) return ''
  const baseUrl = publicSettings.value?.api_base_url || (typeof window !== 'undefined' ? window.location.origin : '')
  const platform = pendingCcsRow.value.group?.platform || 'anthropic'
  const providerName = (publicSettings.value?.site_name || 'sub2api').trim() || 'sub2api'
  return buildCcSwitchImportDeeplink({
    baseUrl,
    platform,
    providerName,
    apiKey: pendingCcsRow.value.key,
    usageScript: CCS_USAGE_SCRIPT,
    app: ccsImportApp.value,
    model: ccsImportModel.value.trim()
  })
})

const ccSwitchDownloadUrl = computed(() =>
  String(locale?.value ?? '').toLowerCase().startsWith('zh') ? CC_SWITCH_HOME_ZH : CC_SWITCH_HOME_EN
)

const ccsAppOptions = computed(() =>
  CC_SWITCH_APPS.map((value) => ({
    value,
    label: t(`keys.ccsImport.${CC_SWITCH_APP_LABEL_KEYS[value]}`)
  }))
)

const ccsImportDeeplinkUnsupported = computed(
  () => !ccSwitchProviderDeeplinkSupported(ccsImportApp.value)
)

// 只展示该密钥通过 /v1/models 真实拉取到的可用模型，不再混入写死的静态猜测模型。
const ccsModelOptions = computed(() => {
  const values = [...ccsAvailableModels.value]
  const current = ccsImportModel.value.trim()
  if (ccsModelsError.value && current && !values.includes(current)) {
    values.unshift(current)
  }
  return values.map((value) => ({ value, label: value }))
})

const ccsModelHintText = computed(() => {
  if (ccsModelsLoading.value) return t('keys.ccsImport.modelLoading')
  if (ccsModelsError.value) return t('keys.ccsImport.modelLoadFailed')
  if (ccsModelsLoaded.value && ccsAvailableModels.value.length === 0) {
    return t('keys.ccsImport.modelEmpty')
  }
  if (ccsAvailableModels.value.length > 0) {
    return t('keys.ccsImport.modelLoadedHint', { count: ccsAvailableModels.value.length })
  }
  return t('keys.ccsImport.modelHint')
})
const selectedKey = ref<ApiKey | null>(null)
const groupSelectorKeyId = ref<number | null>(null)
// 明文 key 的短生命周期状态：只在创建/轮换回执弹窗与"使用密钥"弹窗之间流转，
// 关掉弹窗即清空，不写 localStorage、不进列表行。
const secretPlaintext = ref('')
const secretRow = ref<ApiKey | null>(null)
const showSecretDialog = ref(false)
// 刷新后查看：带密文的 key 先输入登录密码，再在浏览器本地解密。
const showRevealDialog = ref(false)
const revealRow = ref<ApiKey | null>(null)
// 创建时没启用「以后查看」的 key：只能提示轮换。
const showNoSealDialog = ref(false)
const noSealRow = ref<ApiKey | null>(null)
// 列表复制成功后的短暂打勾状态。
const copiedKeyId = ref<number | null>(null)
let copiedKeyTimer: ReturnType<typeof setTimeout> | null = null
const useKeyPlaintext = ref('')
// Group health dots, keyed by group id. Loaded lazily the first time a picker
// opens and cached for a minute, so the key page never pays for it up front.
const groupStatusClasses = ref<Record<number, string>>({})
const publicSettings = ref<PublicSettings | null>(null)
const customEndpoints = ref<CustomEndpoint[]>([])
const dropdownRef = ref<HTMLElement | null>(null)
const columnDropdownRef = ref<HTMLElement | null>(null)
const columnDropdownMenuRef = ref<HTMLElement | null>(null)
const columnDropdownStyle = ref<Record<string, string>>({})
const columnDropdownPlacement = ref<'top' | 'bottom'>('bottom')
const dropdownPosition = ref<{ top?: number; bottom?: number; left: number; maxHeight: number } | null>(null)

const updateColumnDropdownPosition = () => {
  if (!columnDropdownRef.value || typeof window === 'undefined') return
  const rect = columnDropdownRef.value.getBoundingClientRect()
  const vw = window.innerWidth || 1024
  const vh = window.innerHeight || 768
  const margin = 8
  const gap = 4
  const menuWidth = Math.min(208, vw - margin * 2)
  const left = Math.max(margin, Math.min(rect.right - menuWidth, vw - menuWidth - margin))
  const spaceBelow = vh - rect.bottom - margin - gap
  const spaceAbove = rect.top - margin - gap
  const placeAbove = spaceBelow < 220 && spaceAbove > spaceBelow
  const availableSpace = placeAbove ? spaceAbove : spaceBelow
  const viewportCap = Math.max(120, vh - margin * 2)
  const maxHeight = Math.min(320, viewportCap, Math.max(140, availableSpace))
  columnDropdownPlacement.value = placeAbove ? 'top' : 'bottom'
  if (placeAbove) {
    const bottom = Math.max(margin, Math.min(vh - rect.top + gap, vh - margin - maxHeight))
    columnDropdownStyle.value = {
      bottom: `${bottom}px`,
      left: `${left}px`,
      maxHeight: `${maxHeight}px`,
      '--transform-origin': 'bottom right',
    }
  } else {
    const top = Math.max(margin, Math.min(rect.bottom + gap, vh - margin - maxHeight))
    columnDropdownStyle.value = {
      top: `${top}px`,
      left: `${left}px`,
      maxHeight: `${maxHeight}px`,
      '--transform-origin': 'top right',
    }
  }
}

const toggleColumnDropdown = () => {
  showColumnDropdown.value = !showColumnDropdown.value
  if (showColumnDropdown.value) {
    updateColumnDropdownPosition()
  }
}
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
  // 开启后由后台定时任务把最近使用的机房/代理 IP 追加进 IP 黑名单。
  auto_block_datacenter_ips: false,
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

// A subscription group's badge shows a label instead of a per-token rate, so an arrow beside it would point at nothing.
const showRateChangeFor = (group: Pick<Group, 'subscription_type'> | undefined): boolean =>
  group?.subscription_type !== 'subscription'

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
    platform: group.platform,
    signals: group,
    showRateChange: showRateChangeFor(group)
  }))
)

const groupOptionById = (id: number) => groupOptions.value.find((opt) => opt.value === id)

/**
 * Select 的 #option 槽给到的是这条分组选项的原始记录（groupOptions 的条目）。
 * 普通分组下拉和智能路由的「添加分组」必须画成同一行：复用 GroupOptionItem
 * 的富样式（平台图标 + 分组名 + 倍率徽标 + 描述），只在这里做一次字段映射。
 */
const groupOptionRowProps = (option: GroupOption, selected: boolean) => ({
  name: option.label,
  platform: option.platform,
  subscriptionType: option.subscriptionType,
  rateMultiplier: option.rate,
  userRateMultiplier: option.userRate,
  peakRateEnabled: option.peakRateEnabled,
  peakStart: option.peakStart,
  peakEnd: option.peakEnd,
  peakRateMultiplier: option.peakRateMultiplier,
  description: option.description,
  signals: option.signals,
  showRateChange: option.showRateChange,
  statusClass: groupStatusClasses.value[option.value] ?? null,
  selected
})

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

/** The create DTO returns route group IDs, while its embedded Group is only the primary group. */
const keyGroupIds = (key: ApiKey): number[] => {
  if (Array.isArray(key.group_ids) && key.group_ids.length > 0) return [...new Set(key.group_ids)]
  return key.group_id == null ? [] : [key.group_id]
}

/** The table only shows the first route plus the remaining route count, so many groups cannot widen the row. */
const primaryGroupId = (key: ApiKey): number | null => keyGroupIds(key)[0] ?? null

const groupForKey = (key: ApiKey, groupId: number | null): Group | undefined => {
  if (groupId == null) return undefined
  return key.group?.id === groupId ? key.group : availableGroupsById.value.get(groupId)
}

/**
 * 创建/轮换回执与列表行保持同构：服务端回执可能只带 group_id（或只带主分组），
 * 这里用已加载的分组补上 group_ids 与 group 对象，避免「刚建完立刻用」时
 * 被当成未分配分组，也避免列表 badge 和平台向导缺数据。
 */
const resolveKeyGroupRow = (row: ApiKey): ApiKey => {
  const groupId = primaryGroupId(row)
  const resolved: ApiKey = { ...row, group_ids: keyGroupIds(row) }
  resolved.group = groupId == null ? undefined : groupForKey(row, groupId)
  return resolved
}

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

// 折叠状态：默认收起，关闭弹窗或只剩一条路由时复位。
const smartRoutesOpen = ref(false)

const smartRouteBadgeProps = (groupId: number) => {
  const option = groupOptionById(groupId)
  if (!option) return null
  return {
    name: option.label,
    platform: option.platform,
    subscriptionType: option.subscriptionType,
    rateMultiplier: option.rate,
    userRateMultiplier: option.userRate,
    peakRateEnabled: option.peakRateEnabled,
    peakStart: option.peakStart,
    peakEnd: option.peakEnd,
    peakRateMultiplier: option.peakRateMultiplier,
  }
}

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
    const response = await keysAPI.list(pagination.value.page, pagination.value.page_size, activeListFilters(), {
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
      // 一屏概览的近 24h 调用/消耗：一次批量查询，按 id 合并回列表行。
      try {
        const overviewResponse = await keysAPI.getOverview(keyIds)
        if (signal.aborted) return
        const next: Record<number, ApiKeyOverview24h> = {}
        overviewResponse.items.forEach((item) => {
          next[item.id] = item
        })
        keyOverview.value = next
      } catch (e) {
        if (!isAbortError(e)) {
          console.error('Failed to load key overview:', e)
        }
      }
    } else {
      keyOverview.value = {}
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
    groupsLoaded.value = true
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

// 明文密钥回执：创建 / 轮换后唯一一次展示。
const openSecretDialog = (row: ApiKey | null | undefined, secret: string) => {
  if (!secret) return
  secretRow.value = row ?? null
  secretPlaintext.value = secret
  showSecretDialog.value = true
}

const closeSecretDialog = () => {
  showSecretDialog.value = false
  secretPlaintext.value = ''
  secretRow.value = null
  createAuthFactor.value = ''
  receiptVerifiedPassword.value = ''
}

// 列表列只展示后端口径的掩码：优先 key_masked；老数据只剩前缀时补「****」。
// 内存里的明文（ephemeralKeys）绝不进表格，只用于复制按钮 / 浮条 / 回执弹窗。
const keyDisplayValue = (row: ApiKey): string => {
  if (row.key_masked) return row.key_masked
  if (row.key_prefix) return `${row.key_prefix}****`
  return '-'
}

const keyCellHint = (row: ApiKey): string => {
  if (ephemeralKeys.has(row.id)) return t('keys.ephemeralHint')
  if (row.has_sealed_secret) return t('keys.sealedHint')
  return t('keys.maskedOnlyHint')
}

// 一屏概览：近 24h 调用/消耗来自一次批量接口；到期时间与 IP 白名单状态
// 直接读列表行自身字段，不再为每行发请求。
const overviewFor = (row: ApiKey): ApiKeyOverview24h =>
  keyOverview.value[row.id] ?? { id: row.id, requests_24h: 0, cost_24h: 0 }

const overviewIPStatus = (row: ApiKey): { text: string; tone: string } => {
  const whitelist = row.ip_whitelist?.length ?? 0
  const blacklist = row.ip_blacklist?.length ?? 0
  if (whitelist > 0 && blacklist > 0) {
    return {
      text: t('keys.overview.ipBoth', { whitelist, blacklist }),
      tone: 'text-primary'
    }
  }
  if (whitelist > 0) {
    return {
      text: t('keys.overview.ipWhitelist', { count: whitelist }),
      tone: 'text-primary'
    }
  }
  if (blacklist > 0) {
    return {
      text: t('keys.overview.ipBlacklist', { count: blacklist }),
      tone: 'text-warning'
    }
  }
  return { text: t('keys.overview.ipOpen'), tone: 'text-dimmed' }
}

const flashCopiedKey = (id: number) => {
  copiedKeyId.value = id
  if (copiedKeyTimer) clearTimeout(copiedKeyTimer)
  copiedKeyTimer = setTimeout(() => {
    copiedKeyTimer = null
    copiedKeyId.value = null
  }, 1500)
}

/**
 * 列表复制入口：
 *  - 本标签页创建/轮换过 → 内存明文直接复制；
 *  - 服务端存了密文 → 弹密码框，本地解密后再复制；
 *  - 都没有 → 说明无法找回，只能轮换。
 */
const copyRowKey = async (row: ApiKey) => {
  const plaintext = ephemeralKeys.recall(row.id)
  if (plaintext) {
    const ok = await copyToClipboard(plaintext)
    if (ok) flashCopiedKey(row.id)
    return
  }
  if (row.has_sealed_secret) {
    revealRow.value = row
    showRevealDialog.value = true
    return
  }
  noSealRow.value = row
  showNoSealDialog.value = true
}

const closeRevealDialog = () => {
  showRevealDialog.value = false
  revealRow.value = null
}

const rotateFromReveal = () => {
  const row = revealRow.value
  closeRevealDialog()
  if (row) void rotateKeyValue(row)
}

const closeNoSealDialog = () => {
  showNoSealDialog.value = false
  noSealRow.value = null
}

const rotateFromNoSeal = () => {
  const row = noSealRow.value
  closeNoSealDialog()
  if (row) void rotateKeyValue(row)
}

// 密文保存成功后只更新这一行，避免整页重新拉取。
const markKeySealedLocally = () => {
  const row = secretRow.value
  if (row) mergeKeyLocally({ ...row, has_sealed_secret: true })
}

const openUseKeyModal = (key: ApiKey, secret: string) => {
  selectedKey.value = key
  useKeyPlaintext.value = secret
  showUseKeyModal.value = true
}

const closeUseKeyModal = () => {
  showUseKeyModal.value = false
  selectedKey.value = null
  useKeyPlaintext.value = ''
}

// 从明文回执弹窗进入"使用密钥"引导：这次会话内仍然持有明文，可以继续渲染客户端配置。
// 子弹窗叠在回执之上，回执本身不关：关掉子弹窗后还能回到明文、再复制、再换入口。
const useRevealedKey = () => {
  const row = secretRow.value
  const secret = secretPlaintext.value
  if (!row || !secret) return
  openUseKeyModal(row, secret)
}

// 从「查看已保存密钥」解密弹窗进入"使用密钥"引导：同样把子弹窗叠在解密弹窗之上，便于连续操作。
const useDecryptedKey = (secret: string) => {
  const row = revealRow.value
  if (!row || !secret) return
  openUseKeyModal(resolveKeyGroupRow(row), secret)
}

const openAgentGuide = () => {
  void router?.push({ path: '/docs', query: { page: 'agent-setup' } })
}

const openAgentGuideFromModal = () => {
  closeUseKeyModal()
  closeSecretDialog()
  closeRevealDialog()
  openAgentGuide()
}

// 「使用密钥」里发现无分组时：关掉子弹窗和回执，定位到这一行的分组列直接选分组。
const assignGroupForRevealedKey = () => {
  const row = selectedKey.value ?? secretRow.value
  closeUseKeyModal()
  closeSecretDialog()
  if (!row) return
  const listed = apiKeys.value.find((key) => key.id === row.id) ?? row
  nextTick(() => {
    if (isSmartRoutingKey(listed)) {
      // 智能路由 key 的分组只能在编辑弹窗里改。
      editKey(listed)
      return
    }
    const cell = groupButtonRefs.value.get(listed.id)
    if (cell) {
      cell.scrollIntoView?.({ block: 'center', behavior: 'smooth' })
      openGroupSelector(listed)
      return
    }
    // 行不在当前页/被筛掉时退回到编辑弹窗，避免按钮点了没反应。
    editKey(listed)
  })
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
  smartRoutesOpen.value = false
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
    auto_block_datacenter_ips: key.auto_block_datacenter_ips === true,
  }
  showEditModal.value = true
}

// 轮换一把 key 的值：旧值立即失效，成功回执里带回新明文。
// 走的是既有 PUT /keys/:id 更新流程（regenerate_key），不新增后端路由。
// 风控停用的 key（risk_disabled）和待整改的 key（needs_rotation）共用这条路径。
// 轮换与创建同级会下发明文，所以账号有可用因子时先过三选一身份校验。
const rotateKeyValue = async (key: ApiKey) => {
  if (rotatingKeyId.value !== null) return
  rotatingKeyId.value = key.id
  try {
    const verification = await requestKeyRotateVerification()
    if (verification === null) {
      // 用户在校验弹窗点了取消：不轮换（旧 Key 保持原样）。
      return
    }
    const payload = verification === ROTATE_VERIFY_NO_FACTOR ? undefined : verification
    const updated = await keysAPI.rotateKey(key.id, payload)
    const { key: rotatedSecret, ...rotatedMasked } = updated
    const merged = mergeKeyLocally(rotatedMasked as ApiKey)
    // 轮换后旧明文立即作废：先删掉内存里的旧条目，再用新明文覆盖同一 id。
    ephemeralKeys.forget(key.id)
    if (rotatedSecret) ephemeralKeys.remember(key.id, rotatedSecret)
    if (rotatedSecret) ephemeralBarKeyId.value = key.id
    // 旧 key 已立刻失效，新明文必须当场给用户，否则这把 key 就废了。
    // 回执行同样要带分组，否则立刻点「使用密钥」会被误判成无分组。
    receiptVerifiedPassword.value = verifiedPasswordOf(verification)
    openSecretDialog(merged ?? resolveKeyGroupRow(rotatedMasked as ApiKey), rotatedSecret)
    appStore.showSuccess(t('keys.riskRotateSuccess'))
    // 轮换即整改：角标就地消失，门禁汇总数量同步减少。
    await syncRotationRemediation()
  } catch (error: any) {
    const detail = error?.reason
      ? createVerificationErrorText(error.reason)
      : t('keys.riskRotateFailed')
    appStore.showError(detail)
  } finally {
    rotatingKeyId.value = null
  }
}

const ROTATE_VERIFY_NO_FACTOR = 'no-factor'

/** 单条轮换的身份校验：有可用因子时弹校验步；返回 null 表示用户取消。 */
const requestKeyRotateVerification = async (): Promise<ApiKeyVerificationPayload | null | 'no-factor'> => {
  rotateVerifyLoading.value = true
  const factors = await loadVerificationFactors()
  rotateVerifyLoading.value = false
  if (!needsKeyVerification(factors)) {
    // 不需要校验（没开 TOTP / Passkey）：服务端显式放行（skipped_no_factor），不弹校验步。
    return 'no-factor'
  }
  rotateVerifyPayload.value = null
  showRotateVerifyDialog.value = true
  return await new Promise<ApiKeyVerificationPayload | null>((resolve) => {
    rotateVerifyResolve = resolve
  })
}

const confirmRotateVerification = () => {
  const built = rotateVerificationRef.value?.buildVerification() ?? null
  if (!built) return
  rotateVerifyPayload.value = built
  showRotateVerifyDialog.value = false
  rotateVerifyResolve?.(built)
  rotateVerifyResolve = null
}

const cancelRotateVerification = () => {
  showRotateVerifyDialog.value = false
  rotateVerifyResolve?.(null)
  rotateVerifyResolve = null
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
    // 启用/停用会改变这把 Key 是否参与门禁，汇总数量随之刷新。
    void syncRotationRemediation()
  } catch (error) {
    appStore.showError(t('keys.failedToUpdateStatus'))
  }
}

// The rate hints in the cell are their own controls (tap or hover opens their detail), so a click on them must not
// also open the group selector.
const onGroupCellClick = (event: MouseEvent, key: ApiKey) => {
  if (event.target instanceof Element && event.target.closest('[data-group-signals]')) return
  openGroupSelector(key)
}

const updateGroupSelectorPosition = (keyId: number) => {
  const buttonEl = groupButtonRefs.value.get(keyId)
  if (!buttonEl || typeof window === 'undefined') return
  const rect = buttonEl.getBoundingClientRect()
  const vw = window.innerWidth || 1024
  const vh = window.innerHeight || 768
  const margin = 8
  const gap = 4
  const dropdownEstHeight = 400 // estimated max dropdown height
  const dropdownEstWidth = Math.min(380, vw - margin * 2)
  const spaceBelow = vh - rect.bottom - margin - gap
  const spaceAbove = rect.top - margin - gap
  // 夹取 left，避免窄屏下浮层超出视口右缘
  const left = Math.max(margin, Math.min(rect.left, vw - dropdownEstWidth - margin))
  const placeAbove = spaceBelow < dropdownEstHeight && spaceAbove > spaceBelow
  const availableSpace = placeAbove ? spaceAbove : spaceBelow
  const viewportCap = Math.max(120, vh - margin * 2)
  const maxHeight = Math.min(viewportCap, Math.max(160, availableSpace))

  if (placeAbove) {
    // Not enough space below, pop upward
    const bottom = Math.max(margin, Math.min(vh - rect.top + gap, vh - margin - maxHeight))
    dropdownPosition.value = {
      bottom,
      left,
      maxHeight
    }
  } else {
    // Default: pop downward
    const top = Math.max(margin, Math.min(rect.bottom + gap, vh - margin - maxHeight))
    dropdownPosition.value = {
      top,
      left,
      maxHeight
    }
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
    updateGroupSelectorPosition(key.id)
    groupSelectorKeyId.value = key.id
    groupSearchQuery.value = ''
    void loadGroupStatusClasses().then((classes) => {
      groupStatusClasses.value = classes
    })
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
  if (
    columnDropdownRef.value &&
    !columnDropdownRef.value.contains(target) &&
    !columnDropdownMenuRef.value?.contains(target)
  ) {
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
  if (rotationOnlyRows.value.some((key) => idSet.has(key.id))) {
    rotationOnlyRows.value = rotationOnlyRows.value.filter((key) => !idSet.has(key.id))
  }
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
  // 待整改视图里删除失败的行也要放回去，否则角标汇总会和列表对不上。
  const rotationExtras = removed
    .map((snapshot) => snapshot.key)
    .filter((key) => key.needs_rotation && !rotationOnlyRows.value.some((row) => row.id === key.id))
  if (rotationExtras.length > 0) {
    rotationOnlyRows.value = [...rotationOnlyRows.value, ...rotationExtras]
  }
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
  if (filterGroupId.value !== '') {
    const routeGroupIds = Array.isArray(key.group_ids) && key.group_ids.length > 0
      ? key.group_ids
      : key.group_id == null ? [] : [key.group_id]
    if (String(filterGroupId.value) === '0') {
      if (routeGroupIds.length > 0) return false
    } else if (!routeGroupIds.some((groupId) => String(groupId) === String(filterGroupId.value))) {
      return false
    }
  }
  const query = filterSearch.value.trim().toLowerCase()
  if (query) {
    const haystack = `${key.name ?? ''} ${key.key_masked ?? ''} ${key.key_prefix ?? ''}`.toLowerCase()
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
    // 明文 key 只属于回执弹窗，绝不落到列表行里。
    if (field === 'key') continue
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
  const resolved = resolveKeyGroupRow(merged)
  apiKeys.value[index] = resolved
  if (selectedKey.value?.id === resolved.id) selectedKey.value = resolved
  // 「只看待整改」视图里同一把 key 也要跟着变：整改完成的立即移出列表。
  const rotationIndex = rotationOnlyRows.value.findIndex((key) => key.id === resolved.id)
  if (rotationIndex !== -1) {
    const nextRotationRows = [...rotationOnlyRows.value]
    if (resolved.needs_rotation) {
      nextRotationRows[rotationIndex] = resolved
    } else {
      nextRotationRows.splice(rotationIndex, 1)
    }
    rotationOnlyRows.value = nextRotationRows
  }
  return resolved
}

/** Insert a freshly created key without refetching the page; returns the normalized row. */
const insertKeyLocally = (created: ApiKey | null | undefined): ApiKey | null => {
  if (!created || typeof created !== 'object' || typeof created.id !== 'number') return null
  // 创建回执可能只带主分组/分组 ID：补齐 group_ids 与 group，供列表行和
  // 紧随其后的「使用密钥」弹窗直接复用，不需要为了一次回执再拉整页列表。
  const createdRow = resolveKeyGroupRow(created)
  applyTotalDelta(1)
  if (pagination.value.page !== 1 || !matchesActiveFilters(createdRow)) return createdRow
  const index = apiKeys.value.findIndex((key) => compareRowsForCurrentSort(key, createdRow) > 0)
  const next = [...apiKeys.value]
  next.splice(index === -1 ? next.length : index, 0, createdRow)
  apiKeys.value = next
  return createdRow
}

/**
 * 第一步的必填项：名称不能为空白，分组必须选（只有确认「当前账号一个可绑定分组都没有」时才允许建无分组 key，
 * 分组还没加载完不算「没有」，不能静默放行）。「下一步」是 type="button"，不会触发浏览器的原生表单校验，
 * 所以这两项必须在这里自己拦；错误在用户点过一次「下一步 / 创建」之后才亮，之后随输入实时消失。
 */
const groupIsRequired = computed(() => !(groupsLoaded.value && !hasAvailableGroups.value))
const nameMissing = computed(() => formData.value.name.trim() === '')
const groupMissing = computed(() => {
  if (!groupIsRequired.value) return false
  return formData.value.smart_routing ? formData.value.group_ids.length === 0 : formData.value.group_id === null
})
// 点过一次提交才开始标红：刚打开的空表单不该一片红。关弹窗 / 重新打开时清掉。
const attemptedSubmit = ref(false)
// 抖动动画的开关：每次被拦下都先摘后挂（中间隔一个 tick），所以连续点两次也会再抖一次。
const nudge = ref(false)
const nameError = computed(() => (attemptedSubmit.value && nameMissing.value ? t('keys.nameRequired') : undefined))
const groupError = computed(() => (attemptedSubmit.value && groupMissing.value ? t('keys.groupRequired') : undefined))
const nameInputRef = ref<{ inputRef?: HTMLInputElement | null } | null>(null)
const resetCreateValidation = () => {
  attemptedSubmit.value = false
  nudge.value = false
}

/** 被拦下时：把页面带到第一个未完成项并聚焦；同时重放一次轻微的抖动（尊重「减少动态效果」）。 */
const revealFirstMissingField = async () => {
  attemptedSubmit.value = true
  nudge.value = false
  await nextTick()
  nudge.value = true
  const first = nameMissing.value
    ? document.querySelector<HTMLElement>('[data-tour="key-form-name"]')
    : document.querySelector<HTMLElement>('[data-test="key-field-group"] button, [data-tour="key-form-group"] button, [data-test="smart-route-add"] button')
  first?.scrollIntoView({ block: 'nearest', behavior: 'smooth' })
  first?.focus({ preventScroll: true })
}

/** 创建 / 编辑共用的必填校验；顺带归一化分组字段。 */
const validateCreateBasics = (): boolean => {
  if (nameMissing.value) {
    void revealFirstMissingField()
    return false
  }
  // 分组是默认必选项：无分组 key 不能路由任何请求。
  if (groupMissing.value) {
    void revealFirstMissingField()
    return false
  }
  if (formData.value.smart_routing && formData.value.group_ids.length > 0) {
    formData.value.group_id = formData.value.group_ids[0] ?? null
  }

  // Validate custom key if enabled
  if (!showEditModal.value && formData.value.use_custom_key) {
    if (!formData.value.custom_key) {
      appStore.showError(t('keys.customKeyRequired'))
      return false
    }
    if (customKeyError.value) {
      appStore.showError(customKeyError.value)
      return false
    }
  }
  return true
}

const handleSubmit = async () => {
  // 填表期间页面被判定过期（部署了新版本）：不再发注定失败的请求。
  if (showCreateFlow.value && appUpdate.updateAvailable.value) {
    appUpdate.blockingOpen.value = true
    return
  }
  if (!validateCreateBasics()) return

  // 创建 Key 前必须完成一次身份校验：第一步只负责推进到校验步；
  // 第二步拿到真实载荷（密码 / TOTP / Passkey 票据）后才会真正提交。
  // 账号一个可用因子都没有时（OAuth-only 且未绑 2FA）跳过，但仍只做一次性显示。
  let verification: ApiKeyVerificationPayload | undefined
  if (showCreateFlow.value && requiresCreateVerification.value) {
    if (createStep.value !== 2) {
      createStep.value = 2
      return
    }
    const built = createVerificationRef.value?.buildVerification() ?? null
    if (!built) return
    verification = built
    verificationPayload.value = built
  } else {
    verificationPayload.value = null
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
        // 黑名单只在开启 IP 限制时提交；开关随黑名单一起失效，避免后台
        // 持续追加、下次保存又被清空。
        auto_block_datacenter_ips: formData.value.enable_ip_restriction ? formData.value.auto_block_datacenter_ips : false,
      }
      if (shouldSubmitEditStatus(selectedKey.value, formData.value.status)) {
        updates.status = formData.value.status
      }
      const updated = await keysAPI.update(selectedKey.value.id, updates)
      mergeKeyLocally(updated)
      appStore.showSuccess(t('keys.keyUpdatedSuccess'))
      // 编辑里可能刚补了 IP 白名单：待整改角标与汇总数量要立刻刷新。
      await syncRotationRemediation()
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
        {
          deepseekToolInjectionEnabled: formData.value.deepseek_tool_injection_enabled,
          autoBlockDatacenterIPs: formData.value.enable_ip_restriction && formData.value.auto_block_datacenter_ips,
          ...(formData.value.concurrency_limit > 0 ? { concurrencyLimit: formData.value.concurrency_limit } : {}),
          ...(verification ? { verification } : {}),
        }
      )
      // 明文只在当次响应里：列表行只放脱敏视图，明文交给回执弹窗。
      const { key: createdSecret, ...createdMasked } = created
      if (createdSecret) {
        const createdRow = insertKeyLocally(createdMasked)
        // 本标签页内一直能查看/复制这把 key：明文只进内存，刷新即失效。
        ephemeralKeys.remember(createdMasked.id, createdSecret)
        // 关闭回执弹窗后仍能在 5 分钟内从右下角浮条再复制一次。
        ephemeralBarKeyId.value = createdMasked.id
        // 记录本次身份校验因子：封存密文时原样上报，二次查看要求同一因子。
        createAuthFactor.value = verification?.method ?? ''
        receiptVerifiedPassword.value = verifiedPasswordOf(verification)
        appStore.showSuccess(t('keys.keyCreatedSuccess'))
        // 回执弹窗拿补齐分组后的行：立刻点「使用密钥」时平台/分组都在。
        openSecretDialog(createdRow ?? createdMasked, createdSecret)
      } else {
        // 重试回执可能是历史快照，行状态与总数以当前列表为准。
        appStore.showSuccess(t('keys.keyCreatedReplaySuccess'))
        void loadApiKeys()
      }
      // Only advance tour if active, on submit step, and creation succeeded
      if (onboardingStore.isCurrentStep('[data-tour="key-form-submit"]')) {
        onboardingStore.nextStep(500)
      }
    }
    closeModals()
  } catch (error: any) {
    // 身份校验失败要显示具体原因（密码不正确 / 验证码错误 / Passkey 失败），
    // 不能笼统提示「保存失败」让用户猜。
    const reason = error?.reason
    const verifiedReason = reason === 'VERIFY_PASSWORD_INVALID' || reason === 'VERIFY_TOTP_INVALID'
      || reason === 'VERIFY_PASSKEY_FAILED' || reason === 'VERIFY_FACTOR_UNAVAILABLE'
      || reason === 'VERIFY_REQUIRED' || reason === 'VERIFY_PASSWORD_RATE_LIMITED'
    const errorMsg = verifiedReason
      ? createVerificationErrorText(reason)
      : reason === 'API_KEY_TOO_PREDICTABLE'
        ? t('keys.customKeyTooPredictable')
        : reason === 'API_KEY_GROUP_REQUIRED'
          // 实例里已有可分配分组（但当前账号没拿到）：明确告诉用户去要分组，别以为是前端坏了。
          ? t('keys.groupRequiredByServer')
          : error.response?.data?.detail || t('keys.failedToSave')
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
    ephemeralKeys.forget(key.id)
    appStore.showSuccess(t('keys.keyDeletedSuccess'))
    await backfillPageIfEmpty()
    void syncRotationRemediation()
  } catch (error: any) {
    // 优先使用后端返回的错误消息，提供更具体的错误信息给用户
    restoreRemovedKeys(removed)
    applyTotalDelta(removed.length)
    const errorMsg = error?.response?.data?.detail || error?.message || t('keys.failedToDelete')
    appStore.showError(errorMsg)
  }
}

// 批量轮换成功后：成功条目（带新明文）与需要重新封存的条目。
const batchRotateOkItems = computed<BatchRotateKeyItem[]>(
  () => batchRotateResult.value?.results.filter((item) => item.status === 'ok' && !!item.key) ?? []
)
const batchRotateSealTargets = ref<number[]>([])
const batchRotateSealableIds = computed(() =>
  batchRotateOkItems.value
    .filter((item) => batchRotateSealTargets.value.includes(item.id))
    .map((item) => item.id)
)
const batchRotateSealSummary = computed(() => {
  const statuses = Object.values(batchRotateSealStatus.value)
  return {
    sealed: statuses.filter((value) => value === 'sealed').length,
    failed: statuses.filter((value) => value === 'failed').length
  }
})

const openBatchRotate = () => {
  if (selectedKeyCount.value === 0 || batchRotateRunning.value) return
  const ids = selectedKeyIds.value.map((id) => Number(id)).filter((id) => Number.isFinite(id))
  const rows = tableRows.value.filter((row) => ids.includes(row.id))
  batchRotateNames.value = Object.fromEntries(rows.map((row) => [row.id, row.name]))
  // 只有原本用登录密码封存过的 Key 才需要「再封存一次」：轮换在同一事务里
  // 删掉了旧密文，用户如果不重新封存，刷新后就再也看不到新明文。
  batchRotateSealTargets.value = canSealKeys.value
    ? rows.filter((row) => row.has_sealed_secret).map((row) => row.id)
    : []
  batchRotateSealEnabled.value = batchRotateSealTargets.value.length > 0
  batchRotateSealPassword.value = ''
  batchRotateSealStatus.value = {}
  batchRotateSealError.value = ''
  batchRotateResult.value = null
  batchRotateCopiedAll.value = false
  showBatchRotateResult.value = false
  showBatchRotateDialog.value = true
}

const closeBatchRotateResult = () => {
  showBatchRotateResult.value = false
  batchRotateResult.value = null
  batchRotateSealPassword.value = ''
  batchRotateSealStatus.value = {}
  batchRotateSealError.value = ''
  batchRotateCopiedAll.value = false
}

/** 身份校验失败原因 → 可直接展示的文案（创建与批量轮换共用）。 */
const createVerificationErrorText = (reason?: string): string =>
  reason === 'VERIFY_PASSWORD_INVALID'
    ? t('keys.factor.passwordInvalid')
    : reason === 'VERIFY_TOTP_INVALID'
      ? t('keys.factor.totpInvalid')
      : reason === 'VERIFY_PASSKEY_FAILED'
        ? t('keys.factor.passkeyFailed')
        : reason === 'VERIFY_FACTOR_UNAVAILABLE'
          ? t('keys.factor.unavailable')
          : reason === 'VERIFY_REQUIRED'
            ? t('keys.factor.requiredAll')
            : reason === 'VERIFY_PASSWORD_RATE_LIMITED'
              ? t('keys.factor.passwordRateLimited')
              : t('keys.batchRotate.failed')

const handleBatchRotate = async () => {
  const ids = selectedKeyIds.value.map((id) => Number(id)).filter((id) => Number.isFinite(id))
  if (ids.length === 0 || batchRotateRunning.value) return
  batchRotateRunning.value = true
  try {
    // 批量轮换会一次性下发每把 Key 的新明文，和创建 Key 同级：当前步骤里
    // 拿到的三选一校验载荷必须一并提交。账号没有任何可用因子时
    // batchRotateFactors 为空，服务端会显式放行并记 skipped_no_factor。
    const verification = batchRotateVerificationPayload.value ?? verificationPayload.value
    const result = await keysAPI.batchRotateKeys(ids, verification ?? undefined)
    batchRotateResult.value = result
    // 成功条目的新明文只进内存 store（与单条轮换一致），刷新/登出即失效。
    result.results.forEach((item) => {
      if (item.status === 'ok' && item.key) ephemeralKeys.remember(item.id, item.key)
    })
    showBatchRotateResult.value = true
    if (result.succeeded_count > 0) {
      const firstOk = result.results.find((item) => item.status === 'ok' && item.key)
      if (firstOk) ephemeralBarKeyId.value = firstOk.id
      appStore.showSuccess(t('keys.batchRotate.success', { count: result.succeeded_count }))
    }
    if (result.failed_count > 0) {
      appStore.showError(t('keys.batchRotate.partial', { failed: result.failed_count }))
    }
    selectedKeyIds.value = []
    await loadApiKeys()
    void syncRotationRemediation()
  } catch (error: any) {
    const detail = error?.reason
      ? createVerificationErrorText(error.reason)
      : error?.response?.data?.detail || t('keys.batchRotate.failed')
    appStore.showError(detail)
  } finally {
    batchRotateRunning.value = false
  }
}

/** 确认批量轮换：账号开了 TOTP / Passkey 时先进身份校验步，否则直接轮换。 */
const openBatchRotateVerification = async () => {
  showBatchRotateDialog.value = false
  batchRotateVerificationPayload.value = null
  batchRotateFactorsLoading.value = true
  const factors = await loadVerificationFactors()
  batchRotateFactors.value = factors
  batchRotateFactorsLoading.value = false
  // 不需要校验（没开 TOTP / Passkey，绝大多数账号）：根本不弹校验对话框，直接轮换，服务端显式放行
  // （skipped_no_factor）。校验对话框只在确认需要之后才打开：先打开再收起，用户会看到一闪而过的
  // 「验证身份」；而且这条路径上它从不被关闭，会一直留在结果弹窗后面。
  if (!needsKeyVerification(factors)) {
    await handleBatchRotate()
    return
  }
  showBatchRotateVerifyDialog.value = true
}

const cancelBatchRotateVerification = () => {
  if (batchRotateRunning.value) return
  showBatchRotateVerifyDialog.value = false
  batchRotateVerificationPayload.value = null
  batchRotateFactors.value = null
}

const confirmBatchRotateWithVerification = async () => {
  if (batchRotateFactorsLoading.value) return
  // 这个对话框只在确认需要校验之后才会打开（openBatchRotateVerification），这里不会遇到「不需要校验」。
  const built = batchRotateVerificationRef.value?.buildVerification() ?? null
  if (!built) return
  batchRotateVerificationPayload.value = built
  showBatchRotateVerifyDialog.value = false
  await handleBatchRotate()
}

const copyAllRotatedKeys = async () => {
  const items = batchRotateOkItems.value
  if (items.length === 0) return
  const text = items
    .map((item) => `${batchRotateNames.value[item.id] ?? `#${item.id}`}: ${item.key}`)
    .join('\n')
  const ok = await copyToClipboard(text)
  if (!ok) return
  batchRotateCopiedAll.value = true
  setTimeout(() => {
    batchRotateCopiedAll.value = false
  }, 2000)
}

// 「再走一次封存流程」：逐条加密+上传，单条失败不阻塞其他条，结果给逐条状态。
const resealRotatedKeys = async () => {
  if (!canSealKeys.value || batchRotateSealing.value) return
  const targets = batchRotateOkItems.value.filter(
    (item) => batchRotateSealableIds.value.includes(item.id) && !!item.key
  )
  if (targets.length === 0) return
  if (!batchRotateSealPassword.value) {
    batchRotateSealError.value = t('keys.seal.passwordRequired')
    return
  }
  batchRotateSealing.value = true
  batchRotateSealError.value = ''
  const status: Record<number, 'sealed' | 'failed' | 'skipped'> = {}
  for (const item of targets) {
    try {
      const payload = await sealApiKey(item.key!, batchRotateSealPassword.value)
      await keysAPI.putSecretSeal(item.id, payload, {
        password: batchRotateSealPassword.value,
        authFactor: 'password',
      })
      status[item.id] = 'sealed'
    } catch (error) {
      status[item.id] = 'failed'
      // 登录密码不对 / 密码失败预算用尽：剩下的每一条都会同样失败，别再往下打——服务端按用户限制
      // 密码输错的次数，把剩下的全打一遍只会把这个账号锁一分钟。
      const reason = (error as { reason?: string } | null)?.reason
      if (reason === 'VERIFY_PASSWORD_INVALID' || reason === 'VERIFY_PASSWORD_RATE_LIMITED') {
        batchRotateSealError.value = reason === 'VERIFY_PASSWORD_INVALID'
          ? t('keys.seal.wrongPassword')
          : t('keys.factor.passwordRateLimited')
        break
      }
    }
  }
  batchRotateSealStatus.value = status
  batchRotateSealing.value = false
  const sealedCount = Object.values(status).filter((value) => value === 'sealed').length
  if (sealedCount === targets.length) {
    appStore.showSuccess(t('keys.batchRotate.sealSuccess', { count: sealedCount }))
  } else {
    appStore.showError(t('keys.batchRotate.sealPartial', { sealed: sealedCount, failed: targets.length - sealedCount }))
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
      result.succeeded.forEach((id) => ephemeralKeys.forget(id))
      appStore.showSuccess(t('common.deleted'))
    } else {
      // Only the failures go back into the table.
      const failedIds = new Set(result.failed.map((id) => Number(id)))
      const failedRows = removed.filter((snapshot) => failedIds.has(snapshot.key.id))
      restoreRemovedKeys(failedRows)
      applyTotalDelta(failedRows.length)
      ids.filter((id) => !failedIds.has(id)).forEach((id) => ephemeralKeys.forget(id))
      appStore.showError(t('keys.failedToDelete'))
    }
    await backfillPageIfEmpty()
    void syncRotationRemediation()
  } catch (error) {
    restoreRemovedKeys(removed)
    applyTotalDelta(removed.length)
    appStore.showError(t('keys.failedToDelete'))
  }
}

const closeModals = () => {
  resetCreateValidation()
  smartRoutesOpen.value = false
  createAdvancedOpen.value = false
  selectedKeyProvider.value = null
  showCreateModal.value = false
  showEditModal.value = false
  createStep.value = 1
  // 保留 verificationFactors：回执弹窗紧接着要用 seal_available 决定是否展示
  // 「以后可查看」；下次打开创建弹窗会重新拉取。
  verificationPayload.value = null
  createVerificationRef.value = null
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
    auto_block_datacenter_ips: false,
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
  if (ccsAvailableModels.value.length > 0) {
    ccsImportModel.value = pickCcSwitchModel(
      defaultCcSwitchModel(ccsImportApp.value),
      ccsAvailableModels.value,
      ccsImportApp.value
    )
    return
  }
  if (!ccsModelsError.value) {
    ccsImportModel.value = ''
  }
}

const fetchRealModelsForKey = async (
  baseUrl: string,
  apiKey: string,
  signal: AbortSignal
): Promise<string[]> => {
  try {
    return await fetchCcSwitchModels(baseUrl, apiKey, signal)
  } catch (err) {
    if (signal.aborted) throw err
    // 若站点配置了独立 api_base_url 且跨域拦截了浏览器 fetch，回退当前同源拉取 /v1/models。
    const sameOrigin = typeof window !== 'undefined' ? window.location.origin : ''
    let differs = false
    if (sameOrigin) {
      try {
        differs = new URL(baseUrl).origin !== sameOrigin
      } catch {
        differs = false
      }
    }
    if (differs) {
      return await fetchCcSwitchModels(sameOrigin, apiKey, signal)
    }
    throw err
  }
}

const loadCcsModels = async (row: ApiKeyWithPlaintext) => {
  ccsModelsAbort?.abort()
  const abort = new AbortController()
  ccsModelsAbort = abort
  ccsAvailableModels.value = []
  ccsModelsLoaded.value = false
  ccsModelsError.value = false
  ccsModelsLoading.value = true
  try {
    const baseUrl = publicSettings.value?.api_base_url || window.location.origin
    const ids = await fetchRealModelsForKey(baseUrl, row.key, abort.signal)
    if (abort.signal.aborted) return
    ccsAvailableModels.value = ids
    ccsModelsLoaded.value = true
    applyCcsModelDefault()
  } catch {
    if (abort.signal.aborted) return
    ccsModelsError.value = true
    ccsModelsLoaded.value = true
    ccsImportModel.value = ''
  } finally {
    if (ccsModelsAbort === abort) {
      ccsModelsLoading.value = false
    }
  }
}

const clearCcsDeeplinkWatch = () => {
  if (ccsDeeplinkTimer) {
    clearTimeout(ccsDeeplinkTimer)
    ccsDeeplinkTimer = null
  }
  if (ccsDeeplinkCleanup) {
    ccsDeeplinkCleanup()
    ccsDeeplinkCleanup = null
  }
}

const importToCcswitch = (row: ApiKeyWithPlaintext) => {
  const platform = row.group?.platform || 'anthropic'
  const baseUrl = publicSettings.value?.api_base_url || window.location.origin
  const config = resolveCcSwitchImportConfig(
    platform,
    platform === 'gemini' ? 'gemini' : 'claude',
    baseUrl
  )
  clearCcsDeeplinkWatch()
  ccsDeeplinkFallbackVisible.value = false
  ccsDeeplinkCopied.value = false
  pendingCcsRow.value = row
  ccsImportApp.value = config.app
  ccsImportModel.value = ''
  showCcsImportModal.value = true
  void loadCcsModels(row)
}

// 明文回执弹窗里点"导入 CCS"：把这次拿到的明文交给既有 CCS 导入流程。
// CCS 弹窗同样叠在回执之上，关闭后回到回执，明文不会丢。
const importRevealedToCcswitch = () => {
  const row = secretRow.value
  const secret = secretPlaintext.value
  if (!row || !secret) return
  importToCcswitch({ ...row, key: secret })
}

// 解密已保存密钥后直接点"导入 CCS"：复用同一套按真实 /v1/models 选模型的导入弹窗。
const importDecryptedToCcswitch = (secret: string) => {
  const row = revealRow.value
  if (!row || !secret) return
  importToCcswitch({ ...resolveKeyGroupRow(row), key: secret })
}

const onCcsAppChange = (value: string | number | boolean | null) => {
  const app = String(value)
  if (!isCcSwitchApp(app)) return
  ccsImportApp.value = app
  ccsDeeplinkFallbackVisible.value = false
  applyCcsModelDefault()
}

const onCcsModelChange = (value: string | number | boolean | null) => {
  ccsImportModel.value = value == null ? '' : String(value)
  ccsDeeplinkFallbackVisible.value = false
}

const onCcsDeeplinkClick = () => {
  if (!ccsDeeplink.value) return
  clearCcsDeeplinkWatch()
  ccsDeeplinkFallbackVisible.value = false
  let handled = false
  const markHandled = () => {
    handled = true
    clearCcsDeeplinkWatch()
  }
  const onVisibilityChange = () => {
    if (document.hidden) markHandled()
  }
  window.addEventListener('blur', markHandled, { once: true })
  document.addEventListener('visibilitychange', onVisibilityChange)
  ccsDeeplinkCleanup = () => {
    window.removeEventListener('blur', markHandled)
    document.removeEventListener('visibilitychange', onVisibilityChange)
  }
  ccsDeeplinkTimer = setTimeout(() => {
    ccsDeeplinkTimer = null
    ccsDeeplinkCleanup?.()
    ccsDeeplinkCleanup = null
    if (!handled && showCcsImportModal.value) {
      ccsDeeplinkFallbackVisible.value = true
    }
  }, 1300)
}

const copyCcsDeeplink = async () => {
  if (!ccsDeeplink.value) return
  const ok = await copyToClipboard(ccsDeeplink.value, t('common.copied'))
  if (!ok) return
  ccsDeeplinkCopied.value = true
  if (ccsDeeplinkCopiedTimer) clearTimeout(ccsDeeplinkCopiedTimer)
  ccsDeeplinkCopiedTimer = setTimeout(() => {
    ccsDeeplinkCopied.value = false
    ccsDeeplinkCopiedTimer = null
  }, 1800)
}

const closeCcsImportModal = () => {
  ccsModelsAbort?.abort()
  ccsModelsAbort = null
  clearCcsDeeplinkWatch()
  if (ccsDeeplinkCopiedTimer) {
    clearTimeout(ccsDeeplinkCopiedTimer)
    ccsDeeplinkCopiedTimer = null
  }
  ccsDeeplinkFallbackVisible.value = false
  ccsDeeplinkCopied.value = false
  ccsModelsLoading.value = false
  ccsModelsLoaded.value = false
  ccsModelsError.value = false
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

/** 支持从文档等入口携带 ?action=create 直达创建弹窗，并在消费后清理查询参数避免刷新重复弹出。 */
const consumeQuickCreateQuery = () => {
  if (!route) return
  const rawAction = route.query.action
  const action = Array.isArray(rawAction) ? rawAction[0] : rawAction
  if (action !== 'create') return
  if (!showCreateModal.value && !showEditModal.value) {
    openCreateModal()
  }
  if (router) {
    const nextQuery = { ...route.query }
    delete nextQuery.action
    void router.replace({ path: route.path, query: nextQuery, hash: route.hash }).catch(() => {})
  }
}

watch(
  () => route?.query.action,
  () => {
    consumeQuickCreateQuery()
  },
)

const handleFloatingDropdownReposition = () => {
  if (showColumnDropdown.value) {
    updateColumnDropdownPosition()
  }
  if (groupSelectorKeyId.value !== null) {
    updateGroupSelectorPosition(groupSelectorKeyId.value)
  }
}

onMounted(() => {
  loadSavedColumns()
  loadApiKeys()
  void loadRotationSummary()
  loadGroups()
  loadUserGroupRates()
  loadPublicSettings()
  loadCustomEndpoints()
  consumeQuickCreateQuery()
  document.addEventListener('click', closeGroupSelector)
  window.addEventListener('resize', handleFloatingDropdownReposition)
  window.addEventListener('scroll', handleFloatingDropdownReposition, true)
  resetTimer = setInterval(() => { now.value = new Date() }, 60000)
})

onUnmounted(() => {
  document.removeEventListener('click', closeGroupSelector)
  window.removeEventListener('resize', handleFloatingDropdownReposition)
  window.removeEventListener('scroll', handleFloatingDropdownReposition, true)
  if (resetTimer) clearInterval(resetTimer)
  if (copiedKeyTimer) clearTimeout(copiedKeyTimer)
  if (ccsDeeplinkCopiedTimer) clearTimeout(ccsDeeplinkCopiedTimer)
  clearCcsDeeplinkWatch()
  ccsModelsAbort?.abort()
})
</script>

<style scoped>
/* 创建弹窗的 Quiet 细节：数值和注册页一致（auth-quiet.css），按下/展开都只动 transform + opacity。 */
.key-more-toggle {
  display: flex;
  width: 100%;
  align-items: center;
  gap: 0.5rem;
  border-radius: 0.5rem;
  padding-block: 0.25rem;
  font-size: 14px;
  text-align: left;
  color: var(--ui-text-muted);
  transition: color 150ms var(--ease-out, cubic-bezier(0.23, 1, 0.32, 1));
}

@media (hover: hover) and (pointer: fine) {
  .key-more-toggle:hover {
    color: var(--ui-text-highlighted);
  }
}

.key-more-toggle:focus-visible {
  outline: 2px solid var(--ui-primary);
  outline-offset: 2px;
}

/* Tailwind v4 的 rotate-180 写的是独立的 rotate 属性，过渡要把它列出来。 */
.key-more-chevron {
  transition-property: transform, rotate;
  transition-duration: 200ms;
  transition-timing-function: var(--ease-out, cubic-bezier(0.23, 1, 0.32, 1));
}

/* 步骤切换：新那一步淡入并上移 6px（关键帧来自 auth-quiet.css，display 从 none 变回来时会重放）。 */
.key-step-pane {
  animation: auth-step-in 200ms var(--ease-out, cubic-bezier(0.23, 1, 0.32, 1)) both;
}

/* 必填项被拦下：那一格横向轻轻抖一下（只动 transform，约 320ms），红边和提示文字由组件自己的 error 状态负责。
   类名在每次被拦下时先摘后挂，所以连续点两次也会再抖一次。 */
.key-field-nudge {
  animation: key-field-nudge 320ms cubic-bezier(0.36, 0.07, 0.19, 0.97) both;
}

@keyframes key-field-nudge {
  10%, 90% { transform: translateX(-1px); }
  20%, 80% { transform: translateX(2px); }
  30%, 50%, 70% { transform: translateX(-4px); }
  40%, 60% { transform: translateX(4px); }
}

@media (prefers-reduced-motion: reduce) {
  .key-step-pane,
  .key-field-nudge {
    animation: none;
  }

  .key-more-chevron,
  .key-more-toggle {
    transition: none;
  }
}

/* Row actions: quiet ghost buttons, icon over a short label. */
.key-row-action { display: inline-flex; flex-direction: column; align-items: center; justify-content: center; gap: 2px; padding: 5px 6px; border-radius: 6px; color: var(--ui-text-muted); font-size: 11px; line-height: 14px; white-space: nowrap; transition: background-color 150ms var(--ease-out, cubic-bezier(0.23, 1, 0.32, 1)), color 150ms var(--ease-out, cubic-bezier(0.23, 1, 0.32, 1)); }
.key-row-action:focus-visible { outline: 2px solid var(--ui-primary); outline-offset: 1px; }
.key-risk-rotate { display: inline-flex; align-items: center; gap: 3px; padding: 1px 7px; border-radius: 999px; border: 1px solid color-mix(in srgb, var(--ui-warning) 45%, transparent); color: color-mix(in srgb, var(--ui-warning) 90%, var(--ui-text)); font-size: 11px; line-height: 16px; white-space: nowrap; transition: background-color 150ms var(--ease-out, cubic-bezier(0.23, 1, 0.32, 1)), color 150ms var(--ease-out, cubic-bezier(0.23, 1, 0.32, 1)); }
@media (pointer: coarse) {
  .key-row-action {
    min-height: 44px;
    min-width: 44px;
  }
}
/* 触屏设备的 :hover 会粘在最后点过的按钮上；悬停反馈只在指针设备生效。 */
@media (hover: hover) and (pointer: fine) {
  .key-row-action:hover { background: var(--ui-bg-elevated); color: var(--ui-text-highlighted); }
  .key-row-action--danger:hover { background: color-mix(in srgb, var(--ui-error) 10%, transparent); color: var(--ui-error); }
  .key-risk-rotate:hover { background: color-mix(in srgb, var(--ui-warning) 12%, transparent); color: var(--ui-warning); }
}
.key-risk-rotate:focus-visible { outline: 2px solid var(--ui-primary); outline-offset: 1px; }
.key-risk-rotate:disabled { opacity: 0.6; cursor: progress; }
@media (prefers-reduced-motion: reduce) { .key-risk-rotate { transition: none; } }
@media (prefers-reduced-motion: reduce) { .key-row-action { transition: none; } }
</style>
