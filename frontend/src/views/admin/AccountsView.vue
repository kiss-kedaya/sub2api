<template>
  <AppLayout>
    <TablePageLayout class="accounts-workspace">
      <template #actions>
        <AdminPageHeader :title="t('admin.accounts.title')" :description="t('admin.accounts.description')">
          <template #actions>
            <UButton icon="i-lucide-plus" :label="t('admin.accounts.createAccount')" @click="showCreate = true" />
          </template>
        </AdminPageHeader>
      </template>
      <template #filters>
        <AccountTableFilters
          v-model:searchQuery="params.search"
          :filters="params"
          :groups="groups"
          @update:filters="(newFilters) => Object.assign(params, newFilters)"
          @change="debouncedReload"
          @update:searchQuery="debouncedReload"
        >
          <template #actions>
            <AccountBulkActionsBar
              :selected-ids="selIds"
              :total-results="pagination.total"
              :selecting-all="selectingAllResults"
              :all-results-selected="allResultsSelected"
              v-on="bulkActionListeners"
            />
            <AccountTableActions
              :loading="loading"
              @refresh="handleManualRefresh"
            >
              <template #after>
                <!-- Auto refresh -->
                <UDropdownMenu
                  v-model:open="showAutoRefreshDropdown"
                  :items="autoRefreshMenuItems"
                  :content="{ align: 'end' }"
                  :ui="{ content: 'w-56' }"
                >
                  <UButton
                    color="neutral"
                    :variant="autoRefreshEnabled ? 'soft' : 'outline'"
                    icon="i-lucide-timer"
                    :square="!autoRefreshEnabled"
                    :title="t('admin.accounts.autoRefresh')"
                    :aria-label="t('admin.accounts.autoRefresh')"
                  >
                    <span v-if="autoRefreshEnabled" class="tabular-nums">
                      {{ t('admin.accounts.autoRefreshCountdown', { seconds: autoRefreshCountdown }) }}
                    </span>
                  </UButton>
                </UDropdownMenu>

                <!-- Column visibility, like the template's "Display" menu -->
                <UDropdownMenu
                  v-model:open="showColumnsDropdown"
                  :items="columnMenuItems"
                  :content="{ align: 'end' }"
                  :ui="{ content: 'w-52' }"
                >
                  <UButton
                    color="neutral"
                    variant="outline"
                    trailing-icon="i-lucide-settings-2"
                    :label="t('admin.accounts.viewColumns')"
                    data-test="account-columns-menu"
                  />
                </UDropdownMenu>

                <!-- More tools -->
                <UDropdownMenu
                  v-model:open="showAccountToolsDropdown"
                  :items="accountToolsMenuItems"
                  :content="{ align: 'end' }"
                  :ui="{ content: 'w-64' }"
                >
                  <UButton
                    color="neutral"
                    variant="outline"
                    icon="i-lucide-ellipsis"
                    square
                    :title="t('admin.accounts.moreActions')"
                    :aria-label="t('admin.accounts.moreActions')"
                  />
                  <template #export-trailing>
                    <UBadge v-if="selIds.length" color="neutral" variant="subtle" size="sm">
                      {{ t('admin.accounts.selectedCount', { count: selIds.length }) }}
                    </UBadge>
                  </template>
                </UDropdownMenu>
              </template>
            </AccountTableActions>
          </template>
        </AccountTableFilters>
        <UAlert
          v-if="hasPendingListSync"
          color="warning"
          variant="subtle"
          icon="i-lucide-refresh-ccw"
          orientation="horizontal"
          class="mt-2"
          :description="t('admin.accounts.listPendingSyncHint')"
          :actions="[{ label: t('admin.accounts.listPendingSyncAction'), color: 'warning', variant: 'outline', size: 'xs', onClick: syncPendingListChanges }]"
        />
      </template>
      <template #table>
        <div ref="accountTableRef" class="accounts-table-stage">
        <DataTable
          ref="dataTableRef"
          :columns="cols"
          :data="accounts"
          :loading="loading"
          row-key="id"
          :server-side-sort="true"
          @sort="handleSort"
          default-sort-key="name"
          default-sort-order="asc"
          :sort-storage-key="ACCOUNT_SORT_STORAGE_KEY"
          :estimate-row-height="156"
          :overscan="5"
          :virtualize-threshold="20"
        >
          <template #header-select>
            <UCheckbox
              :model-value="headerSelectState"
              :aria-label="t('common.selectAll')"
              @click.stop
              @update:model-value="toggleSelectAllVisible"
            />
          </template>
          <template #cell-select="{ row }">
            <UCheckbox
              :model-value="isSelected(row.id)"
              :aria-label="row.name"
              @update:model-value="toggleSel(row.id)"
            />
          </template>
          <template #cell-id="{ value }">
            <span class="font-mono text-xs text-muted">#{{ value }}</span>
          </template>
          <template #cell-name="{ row, value }">
            <div class="flex min-w-0 flex-col">
              <HelpTooltip
                v-if="accountHomepageUrl(row)"
                :content="accountHomepageUrl(row)"
                width-class="w-max max-w-sm break-all"
                class="-ml-1 self-start"
              >
                <template #trigger>
                  <a
                    :href="accountHomepageUrl(row)"
                    target="_blank"
                    rel="noopener noreferrer"
                    class="border-b border-dotted border-accented font-medium text-highlighted"
                  >
                    {{ value }}
                  </a>
                </template>
              </HelpTooltip>
              <span v-else class="font-medium text-highlighted">{{ value }}</span>
              <span
                v-if="accountDisplayEmail(row) || hiddenColumns.has('id')"
                class="max-w-[220px] truncate text-xs text-muted"
                :title="accountDisplayEmail(row) + (row.parent_chatgpt_account_id ? ' · ' + row.parent_chatgpt_account_id : '')"
              >
                <span v-if="hiddenColumns.has('id')" class="font-mono">#{{ row.id }}</span>
                <template v-if="hiddenColumns.has('id') && accountDisplayEmail(row)"> · </template>
                {{ accountDisplayEmail(row) }}
              </span>
            </div>
          </template>
          <template #cell-notes="{ value }">
            <span v-if="value" :title="value" class="block max-w-xs truncate text-sm text-default">{{ value }}</span>
            <span v-else class="text-sm text-dimmed">-</span>
          </template>
          <template #cell-platform_type="{ row }">
            <div class="flex min-w-0 flex-col gap-1">
              <div class="flex flex-wrap items-center gap-1">
                <PlatformTypeBadge :platform="row.platform" :type="row.type"
                  :auth-mode="getOpenAIAuthMode(row)"
                  :plan-type="getAccountPlanType(row)"
                  :privacy-mode="row.extra?.privacy_mode || row.parent_privacy_mode"
                  :subscription-expires-at="row.credentials?.subscription_expires_at || row.parent_subscription_expires_at" />
                <UBadge
                  v-if="getAntigravityTierLabel(row)"
                  :color="getAntigravityTierColor(row)"
                  variant="subtle"
                  size="sm"
                >
                  {{ getAntigravityTierLabel(row) }}
                </UBadge>
              </div>
              <div
                v-if="getOpenAICompactMeta(row)"
                :class="['inline-flex items-center gap-1.5 pl-0.5 text-xs leading-4', getOpenAICompactMeta(row)?.className]"
                :title="getOpenAICompactTitle(row)"
              >
                <span :class="['size-1.5 rounded-full', getOpenAICompactMeta(row)?.dotClass]" />
                <span>{{ getOpenAICompactMeta(row)?.label }}</span>
              </div>
            </div>
          </template>
          <template #cell-capacity="{ row }">
            <AccountCapacityCell :account="row" />
          </template>
          <template #cell-status="{ row }">
            <AccountStatusIndicator :account="row" @show-temp-unsched="handleShowTempUnsched" />
          </template>
          <template #cell-schedulable="{ row }">
            <div class="flex min-w-24 flex-wrap items-center gap-x-1 gap-y-1">
              <USwitch
                :model-value="row.schedulable"
                :disabled="togglingSchedulable === row.id"
                size="sm"
                :title="row.schedulable ? t('admin.accounts.schedulableEnabled') : t('admin.accounts.schedulableDisabled')"
                :aria-label="row.schedulable ? t('admin.accounts.schedulableEnabled') : t('admin.accounts.schedulableDisabled')"
                @update:model-value="handleToggleSchedulable(row)"
              />
              <SchedulableTimerControl
                v-if="authStore.isAdmin && !authStore.isAdminObserver"
                :account-id="row.id"
                :refresh-key="timerRefreshKey"
                @executed="handleTimerExecuted"
              />
            </div>
          </template>
          <template #cell-today_stats="{ row }">
            <AccountTodayStatsCell
              :stats="todayStatsByAccountId[String(row.id)] ?? null"
              :loading="todayStatsLoading"
              :error="todayStatsError"
            />
          </template>
          <template #cell-groups="{ row }">
            <AccountGroupsCell :groups="accountGroupsForRow(row)" :max-display="4" />
          </template>
          <template #header-usage="{ column }">
            <div class="flex items-center">
              <span>{{ column.label }}</span>
              <HelpTooltip :content="t('admin.accounts.usageWindowsHint')" width-class="w-72" />
            </div>
          </template>
          <template #cell-usage="{ row }">
            <!-- A fixed width lets the window stats and action row wrap instead of
                 stretching the column under the sticky actions column. -->
            <div class="w-60 max-w-60 whitespace-normal">
            <AccountUsageCell
              :account="row"
              :today-stats="todayStatsByAccountId[String(row.id)] ?? null"
              :today-stats-loading="todayStatsLoading"
              :manual-refresh-token="usageManualRefreshToken"
              :batched-usage="usageBatchByAccountId[String(row.id)] ?? null"
              :batched-usage-error="usageBatchErrorByAccountId[String(row.id)] ?? null"
              :batched-usage-loading="usageBatchLoadingByAccountId[String(row.id)] === true"
              :request-batched-usage="isDesktopViewport ? queueBatchedUsage : null"
              @account-updated="handleAccountUpdated"
              @usage-loaded="handleAccountUsageLoaded(row.id, $event)"
            />
            </div>
          </template>
          <template #cell-custom_usage="{ row }">
            <CustomUsageCell
              :key="row.id + ':' + customUsagePageKey"
              :account="row"
              :state="customUsageStates[row.id]"
              @refresh="refreshCustomUsage(row.id)"
              @configure="customUsageAccount = row"
              @visibility="setCustomUsageVisible(row.id, $event)"
            />
          </template>
          <template #cell-proxy="{ row }">
            <div class="flex flex-col gap-1">
              <div v-if="row.proxy" class="flex items-center gap-1.5 text-sm">
                <span class="text-default">{{ row.proxy.name }}</span>
                <span v-if="row.proxy.country_code" class="text-xs text-muted">({{ row.proxy.country_code }})</span>
              </div>
              <span v-else class="text-sm text-dimmed">-</span>
              <div v-if="row.proxy && row.proxy.expires_at" class="flex items-center gap-1.5 text-xs">
                <span class="text-muted">{{ formatDateTime(row.proxy.expires_at) }}</span>
                <UBadge v-if="proxyExpiryColor(row.proxy) !== 'neutral'" :color="proxyExpiryColor(row.proxy)" variant="subtle" size="sm">
                  {{ proxyExpiryText(row.proxy) }}
                </UBadge>
                <span v-else class="text-muted">{{ proxyExpiryText(row.proxy) }}</span>
              </div>
              <div v-if="row.proxy_fallback_origin_id" class="flex items-center gap-1">
                <UBadge color="warning" variant="subtle" size="sm" :title="t('admin.accounts.fallbackActiveTip', { origin: row.proxy_fallback_origin_name })">
                  {{ t('admin.accounts.fallbackActive') }}
                </UBadge>
                <UButton color="neutral" variant="outline" size="xs" :label="t('admin.accounts.revertProxy')" @click="onRevertFallback(row)" />
              </div>
            </div>
          </template>
          <template #cell-rate_multiplier="{ row }">
            <span class="inline-flex items-center gap-1 font-mono text-sm tabular-nums text-default">
              <span>{{ formatMultiplier(row.rate_multiplier ?? 1) }}x</span>
              <span
                v-if="row.extra?.upstream_billing_rate_sync_enabled === true"
                class="inline-flex cursor-help text-success"
                :aria-label="t('admin.accounts.upstreamBilling.syncedRateTooltip')"
                :title="t('admin.accounts.upstreamBilling.syncedRateTooltip')"
                data-testid="account-rate-sync-indicator"
              >
                <UIcon name="i-lucide-refresh-cw" class="size-3" />
              </span>
            </span>
          </template>
          <template #header-upstream_billing_rate="{ column }">
            <div class="flex items-center gap-1">
              <span>{{ column.label }}</span>
              <span @click.stop>
                <HelpTooltip :content="t('admin.accounts.upstreamBilling.trustWarning')" width-class="w-80" />
              </span>
            </div>
          </template>
          <template #cell-upstream_billing_rate="{ row }">
            <UpstreamBillingRateCell
              :account="row"
              :global-probe-enabled="upstreamBillingProbeGloballyEnabled"
              :now="upstreamBillingNow"
              :probing="probingUpstreamBilling.has(row.id)"
              @probe="handleProbeUpstreamBilling(row)"
            />
          </template>
          <template #cell-priority="{ value }">
            <span class="text-sm tabular-nums text-default">{{ value }}</span>
          </template>
          <template #header-scheduler_score="{ column }">
            <div class="flex items-center">
              <span>{{ column.label }}</span>
              <HelpTooltip :content="t('admin.accounts.schedulerScore.hint')" width-class="w-80" />
            </div>
          </template>
          <template #cell-scheduler_score="{ row }">
            <div v-if="getSchedulerScoreRows(row).length" class="flex min-w-28 flex-col gap-0.5 font-mono text-xs leading-4">
              <div
                v-for="score in getSchedulerScoreRows(row)"
                :key="String(score.group_id)"
                class="flex items-center gap-1 whitespace-nowrap text-default"
                :title="`${formatSchedulerScoreGroup(score)} / ${formatSchedulerScore(score.base_score)} / ${formatStickySchedulerScore(score)}`"
              >
                <span class="max-w-19 truncate text-muted">{{ formatSchedulerScoreGroup(score) }}</span>
                <span class="text-dimmed">/</span>
                <span>{{ formatSchedulerScore(score.base_score) }}</span>
                <span class="text-dimmed">/</span>
                <span class="text-highlighted">{{ formatStickySchedulerScore(score) }}</span>
              </div>
            </div>
            <span v-else class="text-sm text-dimmed">-</span>
          </template>
          <template #cell-last_used_at="{ value }">
            <span class="text-sm text-muted">{{ formatRelativeTime(value) }}</span>
          </template>
          <template #cell-created_at="{ value }">
            <span class="text-sm tabular-nums text-muted">{{ formatDateTime(value) }}</span>
          </template>
          <template #cell-expires_at="{ row, value }">
            <div class="flex flex-col items-start gap-1">
              <span class="text-sm tabular-nums text-muted">{{ formatExpiresAt(value) }}</span>
              <div v-if="isExpired(value) || (row.auto_pause_on_expired && value)" class="flex items-center gap-1">
                <UBadge v-if="isExpired(value)" color="warning" variant="subtle" size="sm">
                  {{ t('admin.accounts.expired') }}
                </UBadge>
                <UBadge v-if="row.auto_pause_on_expired && value" color="neutral" variant="subtle" size="sm">
                  {{ t('admin.accounts.autoPauseOnExpired') }}
                </UBadge>
              </div>
            </div>
          </template>
          <template #cell-actions="{ row }">
            <div class="flex items-center justify-end gap-0.5">
              <AdminRowAction :label="t('admin.accounts.accountDetail.open')" icon="eye" data-test="open-account-detail" @click="openDetail(row)" />
              <AdminRowAction :label="t('common.edit')" icon="edit" @click="handleEdit(row)" />
              <AdminRowAction :label="t('common.more')" icon="more" @click="openMenu(row, $event)" />
            </div>
          </template>
        </DataTable>
        </div>
      </template>
      <template #pagination>
        <div v-if="pagination.total > 0" class="accounts-footer">
          <AccountBulkActionsBar
            placement="footer"
            :selected-ids="selIds"
            :total-results="pagination.total"
            :selecting-all="selectingAllResults"
            :all-results-selected="allResultsSelected"
            v-on="bulkActionListeners"
          />
          <Pagination :page="pagination.page" :total="pagination.total" :page-size="pagination.page_size" @update:page="handlePageChange" @update:pageSize="handlePageSizeChange" />
        </div>
      </template>
    </TablePageLayout>
      <CreateAccountModal v-if="showCreate" :show="showCreate" :proxies="proxies" :groups="groups" @close="showCreate = false" @created="reload" />
      <EditAccountModal v-if="showEdit" :show="showEdit" :account="edAcc" :proxies="proxies" :groups="groups" @close="showEdit = false" @updated="handleAccountUpdated" />
      <ReAuthAccountModal v-if="showReAuth" :show="showReAuth" :account="reAuthAcc" @close="closeReAuthModal" @reauthorized="handleAccountUpdated" />
      <AccountTestModal v-if="showTest" :show="showTest" :account="testingAcc" @close="closeTestModal" />
      <AccountBatchTestModal v-if="showBatchTest" :show="showBatchTest" :accounts="batchTestAccounts" @close="closeBatchTestModal" />
      <AccountStatsModal v-if="showStats" :show="showStats" :account="statsAcc" @close="closeStatsModal" />
      <ScheduledTestsPanel v-if="showSchedulePanel" :show="showSchedulePanel" :account-id="scheduleAcc?.id ?? null" :model-options="scheduleModelOptions" @close="closeSchedulePanel" />
    <CustomUsageConfigModal
      v-if="customUsageAccount !== null"
      :show="customUsageAccount !== null"
      :account="customUsageAccount"
      @close="customUsageAccount = null"
      @saved="handleCustomUsageSaved"
    />
    <AccountDetailSlideover
      :open="detailAccount !== null"
      :account="detailAccount"
      :groups="detailAccount ? accountGroupsForRow(detailAccount) : []"
      :today-stats="detailAccount ? (todayStatsByAccountId[String(detailAccount.id)] ?? null) : null"
      :show-timer="authStore.isAdmin && !authStore.isAdminObserver"
      :timer-refresh-key="timerRefreshKey"
      :usage-refresh-key="usageManualRefreshToken"
      :global-probe-enabled="upstreamBillingProbeGloballyEnabled"
      :now="upstreamBillingNow"
      @close="detailAccountId = null"
      @edit="handleEdit"
      @test="handleTest"
      @stats="handleViewStats"
      @schedule="handleSchedule"
      @timer-executed="handleTimerExecuted"
      @show-temp-unsched="handleShowTempUnsched"
      @account-updated="handleAccountUpdated"
      @probe-upstream-billing="handleProbeUpstreamBilling"
    />
    <AccountActionMenu @custom-usage="customUsageAccount = $event" :show="menu.show" :account="menu.acc" :anchor-rect="menu.anchorRect" @close="menu.show = false" @test="handleTest" @stats="handleViewStats" @schedule="handleSchedule" @duplicate="handleDuplicateAccount" @reauth="handleReAuth" @refresh-token="handleRefresh" @recover-state="handleRecoverState" @reset-quota="handleResetQuota" @set-privacy="handleSetPrivacy" @create-spark-shadow="handleCreateSparkShadow" @delete="handleDelete" />
      <SyncFromCrsModal v-if="showSync" :show="showSync" @close="showSync = false" @synced="reload" />
      <ImportDataModal v-if="showImportData" :show="showImportData" @close="showImportData = false" @imported="handleDataImported" />
    <BulkEditAccountModal
      v-if="showBulkEdit"
      :show="showBulkEdit"
      :account-ids="selIds"
      :selected-platforms="selPlatforms"
      :selected-types="selTypes"
      :target="bulkEditTarget ?? undefined"
      :proxies="proxies"
      :groups="groups"
      @close="showBulkEdit = false"
      @updated="handleBulkUpdated"
    />
      <TempUnschedStatusModal v-if="showTempUnsched" :show="showTempUnsched" :account="tempUnschedAcc" @close="showTempUnsched = false" @reset="handleTempUnschedReset" />
    <ConfirmDialog :show="showDeleteDialog" :title="t('admin.accounts.deleteAccount')" :message="t('admin.accounts.deleteConfirm', { name: deletingAcc?.name })" :confirm-text="t('common.delete')" :cancel-text="t('common.cancel')" :danger="true" @confirm="confirmDelete" @cancel="showDeleteDialog = false" />
    <ConfirmDialog :show="showCreateShadowDialog" :title="t('admin.accounts.createSparkShadow')" :message="t('admin.accounts.createSparkShadowConfirm', { name: creatingShadowAcc?.name })" @confirm="confirmCreateSparkShadow" @cancel="showCreateShadowDialog = false" />
    <ConfirmDialog :show="showExportDataDialog" :title="t('admin.accounts.dataExport')" :message="t('admin.accounts.dataExportConfirmMessage')" :confirm-text="t('admin.accounts.dataExportConfirm')" :cancel-text="t('common.cancel')" @confirm="handleExportData" @cancel="showExportDataDialog = false">
      <UCheckbox v-model="includeProxyOnExport" :label="t('admin.accounts.dataExportIncludeProxies')" />
    </ConfirmDialog>
      <ErrorPassthroughRulesModal v-if="showErrorPassthrough" :show="showErrorPassthrough" @close="showErrorPassthrough = false" />
      <TLSFingerprintProfilesModal v-if="showTLSFingerprintProfiles" :show="showTLSFingerprintProfiles" @close="showTLSFingerprintProfiles = false" />
    <BalanceAlertConfigModal
      v-if="showBalanceAlert"
      :show="showBalanceAlert"
      :enabled="balanceAlertSettings.enabled"
      :threshold="balanceAlertSettings.threshold"
      :recharge-url="balanceAlertSettings.rechargeUrl"
      @close="showBalanceAlert = false"
      @saved="loadBalanceAlertSettings"
    />
    <TotpStepUpDialog :controller="accountExportStepUp" />
  </AppLayout>
</template>

<script setup lang="ts">
import { ref, reactive, computed, onMounted, onUnmounted, toRaw, watch, defineAsyncComponent } from 'vue'
import { useIntervalFn } from '@vueuse/core'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { useAuthStore } from '@/stores/auth'
import SchedulableTimerControl from '@/components/account/SchedulableTimerControl.vue'
import timerZh from '@/components/account/schedulableTimer.zh'
import timerEn from '@/components/account/schedulableTimer.en'
import { adminAPI } from '@/api/admin'
import { useTableLoader } from '@/composables/useTableLoader'
import { useSwipeSelect, type SwipeSelectVirtualContext } from '@/composables/useSwipeSelect'
import { useTableSelection } from '@/composables/useTableSelection'
import { useStepUp, isStepUpBlocked, isStepUpCancelled, stepUpBlockReason } from '@/composables/useStepUp'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
import AppLayout from '@/components/layout/AppLayout.vue'
import TablePageLayout from '@/components/layout/TablePageLayout.vue'
import DataTable from '@/components/common/DataTable.vue'
import HelpTooltip from '@/components/common/HelpTooltip.vue'
import Pagination from '@/components/common/Pagination.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import AccountTableActions from '@/components/admin/account/AccountTableActions.vue'
import AccountTableFilters from '@/components/admin/account/AccountTableFilters.vue'
import AccountBulkActionsBar from '@/components/admin/account/AccountBulkActionsBar.vue'
import AccountActionMenu from '@/components/admin/account/AccountActionMenu.vue'
import AccountDetailSlideover from '@/components/admin/account/AccountDetailSlideover.vue'
import type { SelectOption } from '@/components/common/Select.vue'
import AccountStatusIndicator from '@/components/account/AccountStatusIndicator.vue'
import AccountUsageCell from '@/components/account/AccountUsageCell.vue'
import CustomUsageCell from '@/components/account/CustomUsageCell.vue'
import { useCustomUsage } from '@/composables/useCustomUsage'
import type { CustomUsageConfig } from '@/api/admin/customUsage'
import AccountTodayStatsCell from '@/components/account/AccountTodayStatsCell.vue'
import AccountGroupsCell from '@/components/account/AccountGroupsCell.vue'
import AccountCapacityCell from '@/components/account/AccountCapacityCell.vue'
import UpstreamBillingRateCell from '@/components/account/UpstreamBillingRateCell.vue'
import PlatformTypeBadge from '@/components/common/PlatformTypeBadge.vue'
import AdminPageHeader from '@/components/admin/page/AdminPageHeader.vue'
import AdminRowAction from '@/components/admin/page/AdminRowAction.vue'
import type { DropdownMenuItem } from '@nuxt/ui'
import { fetchAllAccountIds } from '@/utils/accountSelection'

// 弹窗只在点开时才需要：异步组件 + 模板里的 v-if 一起用，才能既拆出
// AccountsView 的 829KB 主块，又不让它们在页面挂载时就预取。
const CreateAccountModal = defineAsyncComponent(() => import('@/components/account/CreateAccountModal.vue'))
const EditAccountModal = defineAsyncComponent(() => import('@/components/account/EditAccountModal.vue'))
const BulkEditAccountModal = defineAsyncComponent(() => import('@/components/account/BulkEditAccountModal.vue'))
const SyncFromCrsModal = defineAsyncComponent(() => import('@/components/account/SyncFromCrsModal.vue'))
const TempUnschedStatusModal = defineAsyncComponent(() => import('@/components/account/TempUnschedStatusModal.vue'))
const ReAuthAccountModal = defineAsyncComponent(() => import('@/components/admin/account/ReAuthAccountModal.vue'))
const AccountTestModal = defineAsyncComponent(() => import('@/components/admin/account/AccountTestModal.vue'))
const AccountBatchTestModal = defineAsyncComponent(() => import('@/components/admin/account/AccountBatchTestModal.vue'))
const AccountStatsModal = defineAsyncComponent(() => import('@/components/admin/account/AccountStatsModal.vue'))
const ScheduledTestsPanel = defineAsyncComponent(() => import('@/components/admin/account/ScheduledTestsPanel.vue'))
const ImportDataModal = defineAsyncComponent(() => import('@/components/admin/account/ImportDataModal.vue'))
const CustomUsageConfigModal = defineAsyncComponent(() => import('@/components/account/CustomUsageConfigModal.vue'))
const ErrorPassthroughRulesModal = defineAsyncComponent(() => import('@/components/admin/ErrorPassthroughRulesModal.vue'))
const TLSFingerprintProfilesModal = defineAsyncComponent(() => import('@/components/admin/TLSFingerprintProfilesModal.vue'))
const BalanceAlertConfigModal = defineAsyncComponent(() => import('@/components/account/BalanceAlertConfigModal.vue'))
import { resolveBatchTestAccounts } from '@/utils/accountModelTest'
import { buildGrokUsageRefreshKey, buildOpenAIUsageRefreshKey } from '@/utils/accountUsageRefresh'
import { formatDateTime, formatRelativeTime } from '@/utils/format'
import { proxyExpiryBadgeClass, proxyExpiryLabelKey } from '@/utils/proxyExpiry'
import type { BadgeProps } from '@nuxt/ui'
import { extractApiErrorMessage } from '@/utils/apiError'
import { sanitizeUrl } from '@/utils/url'
import { formatMultiplier } from '@/utils/formatters'
import type { Account, AccountListItem, AccountPlatform, AccountSchedulerGroupScore, AccountType, AccountUsageInfo, Proxy as AccountProxy, AdminGroup, WindowStats, ClaudeModel, UpstreamBillingProbeSnapshot } from '@/types'
import { confirm } from '@/composables/useConfirmation'

const { t } = useI18n({ useScope: 'local', messages: { zh: { admin: { accounts: { timer: timerZh } } }, en: { admin: { accounts: { timer: timerEn } } } } })
const appStore = useAppStore()
const authStore = useAuthStore()

const proxies = ref<AccountProxy[]>([])
const groups = ref<AdminGroup[]>([])
const groupsByID = computed(() => new Map(groups.value.map(group => [group.id, group])))
const accountGroupsForRow = (account: Pick<AccountListItem, 'group_ids'>): AdminGroup[] => {
  const groupIDs = account.group_ids ?? []
  if (groupIDs.length === 0) return []
  return groupIDs.map(id => groupsByID.value.get(id)).filter((group): group is AdminGroup => Boolean(group))
}
const accountTableRef = ref<HTMLElement | null>(null)
const dataTableRef = ref<InstanceType<typeof DataTable> | null>(null)
type AccountBulkEditTarget =
  | {
      mode: 'selected'
      accountIds: number[]
      selectedPlatforms: AccountPlatform[]
      selectedTypes: AccountType[]
    }
  | {
      mode: 'filtered'
      filters: {
        platform?: string
        type?: string
        status?: string
        group?: string
        search?: string
        privacy_mode?: string
        sort_by?: string
        sort_order?: AccountSortOrder
      }
      previewCount: number
      selectedPlatforms: AccountPlatform[]
      selectedTypes: AccountType[]
    }
const selPlatforms = computed<AccountPlatform[]>(() => {
  const platforms = new Set(
    accounts.value
      .filter(a => isSelected(a.id))
      .map(a => a.platform)
  )
  return [...platforms]
})
const selTypes = computed<AccountType[]>(() => {
  const types = new Set(
    accounts.value
      .filter(a => isSelected(a.id))
      .map(a => a.type)
  )
  return [...types]
})
const showCreate = ref(false)
const showEdit = ref(false)
const showSync = ref(false)
const showImportData = ref(false)
const showExportDataDialog = ref(false)
const includeProxyOnExport = ref(true)
const showBulkEdit = ref(false)
const bulkEditTarget = ref<AccountBulkEditTarget | null>(null)
const showTempUnsched = ref(false)
const showDeleteDialog = ref(false)
const showCreateShadowDialog = ref(false)
const showReAuth = ref(false)
const showTest = ref(false)
const showBatchTest = ref(false)
const showStats = ref(false)
const showErrorPassthrough = ref(false)
const showTLSFingerprintProfiles = ref(false)
const showBalanceAlert = ref(false)
const balanceAlertSettings = ref({ enabled: false, threshold: 5, rechargeUrl: '' })
const edAcc = ref<Account | null>(null)
const tempUnschedAcc = ref<Account | null>(null)
const deletingAcc = ref<Account | null>(null)
const creatingShadowAcc = ref<Account | null>(null)
const reAuthAcc = ref<Account | null>(null)
const testingAcc = ref<Account | null>(null)
const statsAcc = ref<Account | null>(null)
const showSchedulePanel = ref(false)
const scheduleAcc = ref<Account | null>(null)
const scheduleModelOptions = ref<SelectOption[]>([])
const togglingSchedulable = ref<number | null>(null)
const menu = reactive<{show:boolean, acc:Account|null, anchorRect:DOMRect|null}>({ show: false, acc: null, anchorRect: null })
const exportingData = ref(false)
const probingUpstreamBilling = reactive(new Set<number>())
const upstreamBillingProbeGloballyEnabled = ref<boolean | undefined>(undefined)
const upstreamBillingNow = ref(Date.now())
const upstreamBillingRateETag = ref<string | null>(null)
const upstreamBillingRateRefreshing = ref(false)
let upstreamBillingRateAbortController: AbortController | null = null
useIntervalFn(() => { upstreamBillingNow.value = Date.now() }, 60_000)

// Account tools and column dropdowns (UDropdownMenu places, closes and scrolls them)
const showAccountToolsDropdown = ref(false)
const showColumnsDropdown = ref(false)
const hiddenColumns = reactive<Set<string>>(new Set())
// Fresh layouts start with the most used columns; the rest are one click away in "列显示".
// The account id also shows under the name while its own column is hidden.
// Saved layouts keep their own choice; only a fresh layout starts with this set.
const DEFAULT_HIDDEN_COLUMNS = [
  'id', 'today_stats', 'proxy', 'notes', 'scheduler_score', 'rate_multiplier', 'expires_at',
  'priority', 'created_at', 'custom_usage', 'upstream_billing_rate'
]
const HIDDEN_COLUMNS_KEY = 'account-hidden-columns'
// One-time migration: hide scheduler score for existing admins too, because showing it opt-ins to heavy backend scoring.
const HIDDEN_COLUMNS_VERSION_KEY = 'account-hidden-columns-version'
const HIDDEN_COLUMNS_CURRENT_VERSION = 'scheduler-score-hidden-by-default'

// Sorting settings
const ACCOUNT_SORT_STORAGE_KEY = 'account-table-sort'
type AccountSortOrder = 'asc' | 'desc'
type AccountSortState = {
  sort_by: string
  sort_order: AccountSortOrder
}
const ACCOUNT_SORTABLE_KEYS = new Set([
  'id',
  'name',
  'status',
  'schedulable',
  'priority',
  'rate_multiplier',
  'upstream_billing_rate',
  'last_used_at',
  'created_at',
  'expires_at'
])
const loadInitialAccountSortState = (): AccountSortState => {
  const fallback: AccountSortState = { sort_by: 'name', sort_order: 'asc' }
  try {
    const raw = localStorage.getItem(ACCOUNT_SORT_STORAGE_KEY)
    if (!raw) return fallback
    const parsed = JSON.parse(raw) as { key?: string; order?: string }
    const key = typeof parsed.key === 'string' ? parsed.key : ''
    if (!ACCOUNT_SORTABLE_KEYS.has(key)) return fallback
    return {
      sort_by: key,
      sort_order: parsed.order === 'desc' ? 'desc' : 'asc'
    }
  } catch {
    return fallback
  }
}
const sortState = reactive<AccountSortState>(loadInitialAccountSortState())

// Auto refresh settings
const showAutoRefreshDropdown = ref(false)
const AUTO_REFRESH_STORAGE_KEY = 'account-auto-refresh'
const autoRefreshIntervals = [5, 10, 15, 30] as const
const autoRefreshEnabled = ref(false)
const autoRefreshIntervalSeconds = ref<(typeof autoRefreshIntervals)[number]>(30)
const autoRefreshCountdown = ref(0)
const autoRefreshETag = ref<string | null>(null)
const autoRefreshFetching = ref(false)
const AUTO_REFRESH_SILENT_WINDOW_MS = 15000
const autoRefreshSilentUntil = ref(0)
const hasPendingListSync = ref(false)
const todayStatsByAccountId = ref<Record<string, WindowStats>>({})
const todayStatsLoading = ref(false)
const todayStatsError = ref<string | null>(null)
const todayStatsReqSeq = ref(0)
const pendingTodayStatsRefresh = ref(false)
const usageManualRefreshToken = ref(0)

const desktopViewportQuery = '(min-width: 768px)'
const isDesktopViewport = ref(
  typeof window === 'undefined' ? true : window.matchMedia(desktopViewportQuery).matches
)
let desktopViewportMediaQuery: MediaQueryList | null = null
let desktopViewportListener: ((event: MediaQueryListEvent) => void) | null = null

const usageBatchByAccountId = ref<Record<string, AccountUsageInfo | null>>({})
const usageBatchErrorByAccountId = ref<Record<string, string | null>>({})
const usageBatchLoadingByAccountId = ref<Record<string, boolean>>({})
const usageBatchRequestTokenByAccountId = ref<Record<string, number>>({})
const usageBatchCache = new Map<number, { data: AccountUsageInfo; ts: number }>()
const USAGE_BATCH_CACHE_TTL = 5 * 60 * 1000
const pendingUsageBatchIds = new Set<number>()
let usageBatchFlushTimer: ReturnType<typeof setTimeout> | null = null
let queuedUsageBatchForce = false
let usageBatchRequestToken = 0

const buildDefaultTodayStats = (): WindowStats => ({
  requests: 0,
  tokens: 0,
  cost: 0,
  standard_cost: 0,
  user_cost: 0
})

const accountSupportsBatchUsage = (account: Account) => {
  if (account.platform === 'anthropic') {
    return account.type === 'oauth' || account.type === 'setup-token'
  }
  if (account.platform === 'gemini') return true
  if (account.platform === 'antigravity') return account.type === 'oauth'
  if (account.platform === 'openai') return account.type === 'oauth'
  if (account.platform === 'grok') return account.type === 'oauth'
  return false
}

const setUsageBatchLoading = (accountID: number, loadingState: boolean) => {
  usageBatchLoadingByAccountId.value = {
    ...usageBatchLoadingByAccountId.value,
    [String(accountID)]: loadingState
  }
}

const setUsageBatchState = (accountID: number, usage: AccountUsageInfo | null, error: string | null) => {
  const key = String(accountID)
  usageBatchByAccountId.value = {
    ...usageBatchByAccountId.value,
    [key]: usage
  }
  usageBatchErrorByAccountId.value = {
    ...usageBatchErrorByAccountId.value,
    [key]: error
  }
}

const handleAccountUsageLoaded = (accountID: number, usage: AccountUsageInfo) => {
  if (usageBatchByAccountId.value[String(accountID)] === usage) return
  setUsageBatchState(accountID, usage, null)
}

const flushQueuedUsageBatch = async () => {
  usageBatchFlushTimer = null
  const accountIDs = Array.from(pendingUsageBatchIds)
  const force = queuedUsageBatchForce
  pendingUsageBatchIds.clear()
  queuedUsageBatchForce = false

  if (accountIDs.length === 0) return

  const requestTokensByAccount = accountIDs.reduce<Record<string, number>>((acc, accountID) => {
    acc[String(accountID)] = usageBatchRequestTokenByAccountId.value[String(accountID)] ?? 0
    return acc
  }, {})

  try {
    const result = await adminAPI.accounts.getBatchUsage(accountIDs, force)

    const usageMap = result.usage ?? {}
    const errorMap = result.errors ?? {}
    const now = Date.now()
    const nextUsage = { ...usageBatchByAccountId.value }
    const nextErrors = { ...usageBatchErrorByAccountId.value }
    const nextLoading = { ...usageBatchLoadingByAccountId.value }

    for (const accountID of accountIDs) {
      const key = String(accountID)
      if ((usageBatchRequestTokenByAccountId.value[key] ?? 0) !== requestTokensByAccount[key]) {
        continue
      }
      const usage = usageMap[key] ?? null
      nextUsage[key] = usage
      nextErrors[key] = errorMap[key] ?? null
      nextLoading[key] = false
      if (usage) {
        usageBatchCache.set(accountID, { data: usage, ts: now })
      } else {
        usageBatchCache.delete(accountID)
      }
    }

    usageBatchByAccountId.value = nextUsage
    usageBatchErrorByAccountId.value = nextErrors
    usageBatchLoadingByAccountId.value = nextLoading
  } catch (error) {
    const nextErrors = { ...usageBatchErrorByAccountId.value }
    const nextLoading = { ...usageBatchLoadingByAccountId.value }
    for (const accountID of accountIDs) {
      const key = String(accountID)
      if ((usageBatchRequestTokenByAccountId.value[key] ?? 0) !== requestTokensByAccount[key]) {
        continue
      }
      nextErrors[key] = 'Failed'
      nextLoading[key] = false
    }
    usageBatchErrorByAccountId.value = nextErrors
    usageBatchLoadingByAccountId.value = nextLoading
    console.error('Failed to load account usage batch:', error)
  }
}

const queueBatchedUsage = (account: Account, options?: { force?: boolean }) => {
  if (!isDesktopViewport.value) return
  if (!accountSupportsBatchUsage(account)) return

  const force = options?.force === true
  const cacheKey = account.id
  const key = String(cacheKey)

  if (force) {
    usageBatchCache.delete(cacheKey)
  } else {
    const cached = usageBatchCache.get(cacheKey)
    if (cached && Date.now() - cached.ts < USAGE_BATCH_CACHE_TTL) {
      setUsageBatchState(cacheKey, cached.data, null)
      setUsageBatchLoading(cacheKey, false)
      return
    }
  }

  usageBatchErrorByAccountId.value = {
    ...usageBatchErrorByAccountId.value,
    [key]: null
  }
  usageBatchRequestTokenByAccountId.value = {
    ...usageBatchRequestTokenByAccountId.value,
    [key]: ++usageBatchRequestToken
  }
  setUsageBatchLoading(cacheKey, true)
  pendingUsageBatchIds.add(cacheKey)
  queuedUsageBatchForce = queuedUsageBatchForce || force

  if (usageBatchFlushTimer !== null) return
  usageBatchFlushTimer = setTimeout(() => {
    void flushQueuedUsageBatch()
  }, 0)
}

const refreshTodayStatsBatch = async () => {
  // Why this checks both columns:
  // - today_stats column shows dedicated today's metrics.
  // - usage column also embeds today's stats for Key/Bedrock rows.
  // So we only skip fetching when BOTH columns are hidden.
  if (hiddenColumns.has('today_stats') && hiddenColumns.has('usage')) {
    todayStatsLoading.value = false
    todayStatsError.value = null
    return
  }

  const accountIDs = accounts.value.map(account => account.id)
  const reqSeq = ++todayStatsReqSeq.value
  if (accountIDs.length === 0) {
    todayStatsByAccountId.value = {}
    todayStatsError.value = null
    todayStatsLoading.value = false
    return
  }

  todayStatsLoading.value = true
  todayStatsError.value = null

  try {
    const result = await adminAPI.accounts.getBatchTodayStats(accountIDs)
    if (reqSeq !== todayStatsReqSeq.value) return
    const serverStats = result.stats ?? {}
    const nextStats: Record<string, WindowStats> = {}
    for (const accountID of accountIDs) {
      const key = String(accountID)
      nextStats[key] = serverStats[key] ?? buildDefaultTodayStats()
    }
    todayStatsByAccountId.value = nextStats
  } catch (error) {
    if (reqSeq !== todayStatsReqSeq.value) return
    todayStatsError.value = 'Failed'
    console.error('Failed to load account today stats:', error)
  } finally {
    if (reqSeq === todayStatsReqSeq.value) {
      todayStatsLoading.value = false
    }
  }
}

const autoRefreshIntervalLabel = (sec: number) => {
  if (sec === 5) return t('admin.accounts.refreshInterval5s')
  if (sec === 10) return t('admin.accounts.refreshInterval10s')
  if (sec === 15) return t('admin.accounts.refreshInterval15s')
  if (sec === 30) return t('admin.accounts.refreshInterval30s')
  return `${sec}s`
}

const formatSchedulerScore = (value: unknown): string => {
  const num = Number(value)
  if (!Number.isFinite(num)) return '-'
  return num.toFixed(6).replace(/\.?0+$/, '')
}

const formatStickySchedulerScore = (score: AccountSchedulerGroupScore): string => {
  if (!score) return '-'
  if (score.sticky_score_infinity) return '+∞'
  return formatSchedulerScore(score.sticky_score)
}

const getSchedulerScoreRows = (account: Account): AccountSchedulerGroupScore[] => {
  const groupRows = Array.isArray(account.scheduler_scores)
    ? account.scheduler_scores.filter(score => score.group_id != null)
    : []
  if (groupRows.length) return groupRows
  // 未分组账号没有分组维度分数，回退展示后端返回的基础分
  if (account.scheduler_score) {
    return [{ group_id: null, ...account.scheduler_score }]
  }
  return []
}

const formatSchedulerScoreGroup = (score: AccountSchedulerGroupScore): string => {
  if ('group_name' in score && score.group_name) return score.group_name
  if ('group_id' in score && score.group_id != null) return `#${score.group_id}`
  return t('admin.accounts.schedulerScore.ungrouped')
}

const loadSavedColumns = () => {
  try {
    const saved = localStorage.getItem(HIDDEN_COLUMNS_KEY)
    if (saved) {
      const parsed = JSON.parse(saved) as string[]
      parsed.forEach(key => {
        hiddenColumns.add(key)
      })
      // Older saved column layouts may have scheduler_score visible; migrate them to the new safe default once.
      if (localStorage.getItem(HIDDEN_COLUMNS_VERSION_KEY) !== HIDDEN_COLUMNS_CURRENT_VERSION) {
        hiddenColumns.add('scheduler_score')
        localStorage.setItem(HIDDEN_COLUMNS_KEY, JSON.stringify([...hiddenColumns]))
        localStorage.setItem(HIDDEN_COLUMNS_VERSION_KEY, HIDDEN_COLUMNS_CURRENT_VERSION)
      }
    } else {
      DEFAULT_HIDDEN_COLUMNS.forEach(key => {
        hiddenColumns.add(key)
      })
      localStorage.setItem(HIDDEN_COLUMNS_VERSION_KEY, HIDDEN_COLUMNS_CURRENT_VERSION)
    }
  } catch (e) {
    console.error('Failed to load saved columns:', e)
    DEFAULT_HIDDEN_COLUMNS.forEach(key => {
      hiddenColumns.add(key)
    })
  }
}

const saveColumnsToStorage = () => {
  try {
    localStorage.setItem(HIDDEN_COLUMNS_KEY, JSON.stringify([...hiddenColumns]))
    localStorage.setItem(HIDDEN_COLUMNS_VERSION_KEY, HIDDEN_COLUMNS_CURRENT_VERSION)
  } catch (e) {
    console.error('Failed to save columns:', e)
  }
}

const loadSavedAutoRefresh = () => {
  try {
    const saved = localStorage.getItem(AUTO_REFRESH_STORAGE_KEY)
    if (!saved) return
    const parsed = JSON.parse(saved) as { enabled?: boolean; interval_seconds?: number }
    autoRefreshEnabled.value = parsed.enabled === true
    const interval = Number(parsed.interval_seconds)
    if (autoRefreshIntervals.includes(interval as any)) {
      autoRefreshIntervalSeconds.value = interval as any
    }
  } catch (e) {
    console.error('Failed to load saved auto refresh settings:', e)
  }
}

const saveAutoRefreshToStorage = () => {
  try {
    localStorage.setItem(
      AUTO_REFRESH_STORAGE_KEY,
      JSON.stringify({
        enabled: autoRefreshEnabled.value,
        interval_seconds: autoRefreshIntervalSeconds.value
      })
    )
  } catch (e) {
    console.error('Failed to save auto refresh settings:', e)
  }
}

if (typeof window !== 'undefined') {
  loadSavedColumns()
  loadSavedAutoRefresh()
}

const setAutoRefreshEnabled = (enabled: boolean) => {
  autoRefreshEnabled.value = enabled
  saveAutoRefreshToStorage()
  if (enabled) {
    autoRefreshCountdown.value = autoRefreshIntervalSeconds.value
    resumeAutoRefresh()
  } else {
    pauseAutoRefresh()
    autoRefreshCountdown.value = 0
  }
}

const setAutoRefreshInterval = (seconds: (typeof autoRefreshIntervals)[number]) => {
  autoRefreshIntervalSeconds.value = seconds
  saveAutoRefreshToStorage()
  if (autoRefreshEnabled.value) {
    autoRefreshCountdown.value = seconds
  }
}

const toggleColumn = (key: string) => {
  const wasHidden = hiddenColumns.has(key)
  if (hiddenColumns.has(key)) {
    hiddenColumns.delete(key)
  } else {
    hiddenColumns.add(key)
  }
  saveColumnsToStorage()
  if ((key === 'today_stats' || key === 'usage') && wasHidden) {
    refreshTodayStatsBatch().catch((error) => {
      console.error('Failed to load account today stats after showing column:', error)
    })
  }
  if (key === 'scheduler_score') {
    // The server only returns scheduler scores when this column is visible, so reload the current page immediately.
    syncAccountListDerivedParams()
    load().catch((error) => {
      console.error('Failed to reload accounts after toggling scheduler score column:', error)
    })
  }
}

const isColumnVisible = (key: string) => !hiddenColumns.has(key)
const shouldIncludeSchedulerScore = () => isColumnVisible('scheduler_score')
const syncAccountListDerivedParams = () => {
  // Keep every load path, including auto-refresh and sorting, aligned with the current column visibility.
  const requestParams = params as any
  requestParams.include_scheduler_score = shouldIncludeSchedulerScore() ? '1' : '0'
}

const {
  items: accounts,
  loading,
  params,
  pagination,
  load: baseLoad,
  reload: baseReload,
  debouncedReload: baseDebouncedReload,
  handlePageChange: baseHandlePageChange,
  handlePageSizeChange: baseHandlePageSizeChange
} = useTableLoader<AccountListItem, any>({
  fetchFn: adminAPI.accounts.list,
  initialParams: {
    platform: '',
    type: '',
    status: '',
    privacy_mode: '',
    group: '',
    search: '',
    lite: '1',
    include_scheduler_score: shouldIncludeSchedulerScore() ? '1' : '0',
    sort_by: sortState.sort_by,
    sort_order: sortState.sort_order
  }
})

const customUsageAccount = ref<AccountListItem | null>(null)
const customUsagePageKey = computed(() => JSON.stringify([pagination.page, pagination.page_size, params]))
const customUsageActive = computed(() => !loading.value && customUsageAccount.value === null && !hiddenColumns.has('custom_usage'))
const {
  states: customUsageStates,
  setVisible: setCustomUsageVisible,
  refresh: refreshCustomUsage,
  configSaved: customUsageConfigSaved
} = useCustomUsage(accounts, customUsageActive, customUsagePageKey)
const handleCustomUsageSaved = (id: number, config: Pick<CustomUsageConfig, 'enabled' | 'interval_minutes'>) => {
  customUsageConfigSaved(id, config)
  appStore.showSuccess(t('admin.accounts.customUsage.saved'))
}

const {
  selectedSet,
  selectedIds: selIds,
  allVisibleSelected,
  isSelected,
  setSelectedIds,
  select,
  deselect,
  toggle: toggleSel,
  clear: clearSelectedIds,
  removeMany: removeSelectedAccounts,
  toggleVisible,
  selectVisible: selectCurrentPage,
  batchUpdate
} = useTableSelection<AccountListItem>({
  rows: accounts,
  getId: (account) => account.id
})

const selectingAllResults = ref(false)
const selectedAllResultIDs = ref<Set<number> | null>(null)
const selectionRequestVersion = ref(0)
const allResultsSelected = computed(() => {
  const snapshot = selectedAllResultIDs.value
  if (!snapshot || snapshot.size === 0 || snapshot.size !== selectedSet.value.size) return false
  return Array.from(snapshot).every(id => selectedSet.value.has(id))
})

const clearSelection = () => {
  selectionRequestVersion.value++
  selectingAllResults.value = false
  selectedAllResultIDs.value = null
  clearSelectedIds()
}

const selectPage = () => {
  selectCurrentPage()
}

const swipeVirtualContext: SwipeSelectVirtualContext = {
  getVirtualizer: () => dataTableRef.value?.virtualizer ?? null,
  getSortedData: () => dataTableRef.value?.sortedData ?? accounts.value,
  getRowId: (row: any) => row.id,
}

useSwipeSelect(accountTableRef, {
  isSelected,
  select,
  deselect,
  batchUpdate
}, swipeVirtualContext)

const resetAutoRefreshCache = () => {
  autoRefreshETag.value = null
  upstreamBillingRateETag.value = null
}

type AccountLoadOptions = {
  refreshTodayStats?: boolean
}

const load = async (options: AccountLoadOptions = {}) => {
  const requestParams = params as any
  syncAccountListDerivedParams()
  hasPendingListSync.value = false
  resetAutoRefreshCache()
  pendingTodayStatsRefresh.value = false
  requestParams.lite = '1'
  await baseLoad()
  if (options.refreshTodayStats !== false) await refreshTodayStatsBatch()
}

const reload = async () => {
  syncAccountListDerivedParams()
  hasPendingListSync.value = false
  resetAutoRefreshCache()
  pendingTodayStatsRefresh.value = false
  await baseReload()
  await refreshTodayStatsBatch()
}

const buildUpstreamBillingRateFilters = () => {
  const rawParams = toRaw(params) as Record<string, unknown>
  return {
    platform: typeof rawParams.platform === 'string' ? rawParams.platform : '',
    type: typeof rawParams.type === 'string' ? rawParams.type : '',
    status: typeof rawParams.status === 'string' ? rawParams.status : '',
    group: typeof rawParams.group === 'string' ? rawParams.group : '',
    search: typeof rawParams.search === 'string' ? rawParams.search : '',
    privacy_mode: typeof rawParams.privacy_mode === 'string' ? rawParams.privacy_mode : '',
    sort_by: sortState.sort_by,
    sort_order: sortState.sort_order
  }
}

const sameAccountIDOrder = (left: number[], right: number[]) =>
  left.length === right.length && left.every((id, index) => id === right[index])

const upstreamBillingRateContextKey = () => JSON.stringify({
  page: pagination.page,
  pageSize: pagination.page_size,
  filters: buildUpstreamBillingRateFilters()
})

const applyUpstreamBillingRateSnapshots = async (
  result: NonNullable<Awaited<ReturnType<typeof adminAPI.accounts.getUpstreamBillingRatesWithEtag>>['data']>
) => {
  const nextIDs = result.items.map(item => item.account_id)
  const currentIDs = accounts.value.map(account => account.id)

  // The compact response cannot fill a row that crossed a page boundary.
  // Only that case needs the expensive, full account-list request.
  if (result.total !== pagination.total || !sameAccountIDOrder(nextIDs, currentIDs)) {
    try {
      await load({ refreshTodayStats: false })
    } catch (error) {
      console.error('Failed to reconcile upstream billing sort:', error)
    }
    return
  }

  const itemsByID = new Map(result.items.map(item => [item.account_id, item]))
  let changed = false
  const nextAccounts = accounts.value.map(account => {
    const item = itemsByID.get(account.id)
    if (!item) return account
    const nextSnapshot = item.snapshot ?? null
    const previousSnapshot = account.extra?.upstream_billing_probe ?? null
    if (JSON.stringify(previousSnapshot) === JSON.stringify(nextSnapshot)) return account

    const nextExtra = { ...(account.extra ?? {}) }
    if (nextSnapshot) nextExtra.upstream_billing_probe = nextSnapshot
    else delete nextExtra.upstream_billing_probe
    const nextAccount = {
      ...account,
      ...(typeof nextSnapshot?.synced_rate_multiplier === 'number'
        ? { rate_multiplier: nextSnapshot.synced_rate_multiplier }
        : {}),
      extra: nextExtra
    }
    syncAccountRefs(nextAccount)
    changed = true
    return nextAccount
  })

  if (changed) {
    accounts.value = nextAccounts
    upstreamBillingNow.value = Date.now()
  }
}

const refreshUpstreamBillingRates = async (force = false) => {
  if (upstreamBillingRateRefreshing.value || loading.value || accounts.value.length === 0) return
  if (!force && (
    probingUpstreamBilling.size > 0 ||
    isAnyModalOpen.value ||
    menu.show ||
    showAccountToolsDropdown.value ||
    showColumnsDropdown.value ||
    showAutoRefreshDropdown.value ||
    (typeof document !== 'undefined' && document.hidden)
  )) return

  const controller = new AbortController()
  upstreamBillingRateAbortController = controller
  upstreamBillingRateRefreshing.value = true
  try {
    syncAccountListDerivedParams()
    const requestContextKey = upstreamBillingRateContextKey()
    const result = await adminAPI.accounts.getUpstreamBillingRatesWithEtag(
      pagination.page,
      pagination.page_size,
      buildUpstreamBillingRateFilters(),
      { etag: force ? null : upstreamBillingRateETag.value, signal: controller.signal }
    )
    if (loading.value || requestContextKey !== upstreamBillingRateContextKey()) return
    if (result.etag) upstreamBillingRateETag.value = result.etag
    if (!result.notModified && result.data) await applyUpstreamBillingRateSnapshots(result.data)
  } catch (error) {
    const refreshError = error as { name?: string; code?: string }
    if (refreshError.name !== 'AbortError' && refreshError.name !== 'CanceledError' && refreshError.code !== 'ERR_CANCELED') {
      console.error('Failed to refresh upstream billing rates:', error)
    }
  } finally {
    if (upstreamBillingRateAbortController === controller) upstreamBillingRateAbortController = null
    upstreamBillingRateRefreshing.value = false
  }
}

const refreshUpstreamBillingSortedList = async (force = false) => {
  if (!force && sortState.sort_by !== 'upstream_billing_rate') return
  await refreshUpstreamBillingRates(force)
}

useIntervalFn(() => { void refreshUpstreamBillingRates() }, 5 * 60_000, { immediate: false })

const debouncedReload = () => {
  clearSelection()
  syncAccountListDerivedParams()
  hasPendingListSync.value = false
  resetAutoRefreshCache()
  pendingTodayStatsRefresh.value = true
  baseDebouncedReload()
}

const handlePageChange = (page: number) => {
  syncAccountListDerivedParams()
  hasPendingListSync.value = false
  resetAutoRefreshCache()
  pendingTodayStatsRefresh.value = true
  baseHandlePageChange(page)
}

const handlePageSizeChange = (size: number) => {
  syncAccountListDerivedParams()
  hasPendingListSync.value = false
  resetAutoRefreshCache()
  pendingTodayStatsRefresh.value = true
  baseHandlePageSizeChange(size)
}

const handleSort = (key: string, order: AccountSortOrder) => {
  sortState.sort_by = key
  sortState.sort_order = order
  const requestParams = params as any
  requestParams.sort_by = key
  requestParams.sort_order = order
  syncAccountListDerivedParams()
  pagination.page = 1
  hasPendingListSync.value = false
  resetAutoRefreshCache()
  pendingTodayStatsRefresh.value = true
  load()
}

watch(loading, (isLoading, wasLoading) => {
  if (wasLoading && !isLoading) {
    upstreamBillingNow.value = Date.now()
  }
  if (wasLoading && !isLoading && pendingTodayStatsRefresh.value) {
    pendingTodayStatsRefresh.value = false
    refreshTodayStatsBatch().catch((error) => {
      console.error('Failed to refresh account today stats after table load:', error)
    })
  }
})

watch(accounts, (rows) => {
  const visibleIDs = new Set(rows.map((row) => String(row.id)))
  usageBatchByAccountId.value = Object.fromEntries(
    Object.entries(usageBatchByAccountId.value).filter(([key]) => visibleIDs.has(key))
  )
  usageBatchErrorByAccountId.value = Object.fromEntries(
    Object.entries(usageBatchErrorByAccountId.value).filter(([key]) => visibleIDs.has(key))
  )
  usageBatchLoadingByAccountId.value = Object.fromEntries(
    Object.entries(usageBatchLoadingByAccountId.value).filter(([key]) => visibleIDs.has(key))
  )
  usageBatchRequestTokenByAccountId.value = Object.fromEntries(
    Object.entries(usageBatchRequestTokenByAccountId.value).filter(([key]) => visibleIDs.has(key))
  )
})

const isAnyModalOpen = computed(() => {
  return (
    showCreate.value ||
    showEdit.value ||
    showSync.value ||
    showImportData.value ||
    showExportDataDialog.value ||
    showBulkEdit.value ||
    showTempUnsched.value ||
    showDeleteDialog.value ||
    showReAuth.value ||
    showTest.value ||
    showBatchTest.value ||
    showStats.value ||
    showSchedulePanel.value ||
    showErrorPassthrough.value ||
    showTLSFingerprintProfiles.value ||
    showBalanceAlert.value
  )
})

const enterAutoRefreshSilentWindow = () => {
  autoRefreshSilentUntil.value = Date.now() + AUTO_REFRESH_SILENT_WINDOW_MS
  autoRefreshCountdown.value = autoRefreshIntervalSeconds.value
}

const inAutoRefreshSilentWindow = () => {
  return Date.now() < autoRefreshSilentUntil.value
}

const shouldReplaceAutoRefreshRow = (current: Account, next: Account) => {
  return (
    current.updated_at !== next.updated_at ||
    current.current_concurrency !== next.current_concurrency ||
    current.current_window_cost !== next.current_window_cost ||
    current.active_sessions !== next.active_sessions ||
    current.schedulable !== next.schedulable ||
    current.status !== next.status ||
    current.rate_limit_reset_at !== next.rate_limit_reset_at ||
    current.overload_until !== next.overload_until ||
    current.temp_unschedulable_until !== next.temp_unschedulable_until ||
    buildOpenAIUsageRefreshKey(current) !== buildOpenAIUsageRefreshKey(next) ||
    buildGrokUsageRefreshKey(current) !== buildGrokUsageRefreshKey(next)
  )
}

const syncAccountRefs = (nextAccount: Account) => {
  if (edAcc.value?.id === nextAccount.id) edAcc.value = nextAccount
  if (reAuthAcc.value?.id === nextAccount.id) reAuthAcc.value = nextAccount
  if (tempUnschedAcc.value?.id === nextAccount.id) tempUnschedAcc.value = nextAccount
  if (deletingAcc.value?.id === nextAccount.id) deletingAcc.value = nextAccount
  if (menu.acc?.id === nextAccount.id) menu.acc = nextAccount
}

const mergeAccountsIncrementally = (nextRows: Account[]) => {
  const currentRows = accounts.value
  const currentByID = new Map(currentRows.map(row => [row.id, row]))
  let changed = nextRows.length !== currentRows.length
  const mergedRows = nextRows.map((nextRow) => {
    const currentRow = currentByID.get(nextRow.id)
    if (!currentRow) {
      changed = true
      return nextRow
    }
    if (shouldReplaceAutoRefreshRow(currentRow, nextRow)) {
      changed = true
      syncAccountRefs(nextRow)
      return nextRow
    }
    return currentRow
  })
  if (!changed) {
    for (let i = 0; i < mergedRows.length; i += 1) {
      if (mergedRows[i].id !== currentRows[i]?.id) {
        changed = true
        break
      }
    }
  }
  if (changed) {
    accounts.value = mergedRows
  }
}

const refreshAccountsIncrementally = async () => {
  if (autoRefreshFetching.value) return
  syncAccountListDerivedParams()
  autoRefreshFetching.value = true
  try {
    const result = await adminAPI.accounts.listWithEtag(
      pagination.page,
      pagination.page_size,
      toRaw(params) as {
        platform?: string
        type?: string
        status?: string
        privacy_mode?: string
        group?: string
        search?: string
        sort_by?: string
        sort_order?: AccountSortOrder

      },
      { etag: autoRefreshETag.value }
    )

    if (result.etag) {
      autoRefreshETag.value = result.etag
    }
    if (!result.notModified && result.data) {
      pagination.total = result.data.total || 0
      pagination.pages = result.data.pages || 0
      mergeAccountsIncrementally(result.data.items || [])
      hasPendingListSync.value = false
    }
    upstreamBillingNow.value = Date.now()

    await refreshTodayStatsBatch()
  } catch (error) {
    console.error('Auto refresh failed:', error)
  } finally {
    autoRefreshFetching.value = false
  }
}

const handleManualRefresh = async () => {
  await Promise.all([load(), loadUpstreamBillingProbeGlobalState()])
  // Force usage cells to refetch /usage on explicit user refresh.
  usageManualRefreshToken.value += 1
}

const loadUpstreamBillingProbeGlobalState = async () => {
  try {
    const settings = await adminAPI.accounts.getUpstreamBillingProbeSettings()
    upstreamBillingProbeGloballyEnabled.value = settings.enabled
  } catch (error) {
    console.error('Failed to load upstream billing probe settings:', error)
  }
}

const closeAccountToolsDropdown = () => {
  showAccountToolsDropdown.value = false
}

const openSyncFromCrs = () => {
  closeAccountToolsDropdown()
  showSync.value = true
}

const openImportData = () => {
  closeAccountToolsDropdown()
  showImportData.value = true
}

const openExportDataDialogFromMenu = () => {
  closeAccountToolsDropdown()
  openExportDataDialog()
}

const openErrorPassthrough = () => {
  closeAccountToolsDropdown()
  showErrorPassthrough.value = true
}

const openTLSFingerprintProfiles = () => {
  closeAccountToolsDropdown()
  showTLSFingerprintProfiles.value = true
}

const loadBalanceAlertSettings = async () => {
  try {
    const s = await adminAPI.settings.getSettings()
    balanceAlertSettings.value = {
      enabled: s.balance_low_notify_enabled ?? false,
      threshold: s.balance_low_notify_threshold ?? 5,
      rechargeUrl: s.balance_low_notify_recharge_url ?? '',
    }
  } catch (e) {
    console.error('Failed to load balance alert settings:', e)
  }
}

const openBalanceAlert = async () => {
  closeAccountToolsDropdown()
  await loadBalanceAlertSettings()
  showBalanceAlert.value = true
}

const syncPendingListChanges = async () => {
  hasPendingListSync.value = false
  await load()
  // Keep behavior consistent with manual refresh.
  usageManualRefreshToken.value += 1
}

const { pause: pauseAutoRefresh, resume: resumeAutoRefresh } = useIntervalFn(
  async () => {
    if (!autoRefreshEnabled.value) return
    if (document.hidden) return
    if (loading.value || autoRefreshFetching.value) return
    if (isAnyModalOpen.value) return
    if (menu.show || showAccountToolsDropdown.value || showColumnsDropdown.value || showAutoRefreshDropdown.value) return
    if (inAutoRefreshSilentWindow()) {
      autoRefreshCountdown.value = Math.max(
        0,
        Math.ceil((autoRefreshSilentUntil.value - Date.now()) / 1000)
      )
      return
    }

    if (autoRefreshCountdown.value <= 0) {
      autoRefreshCountdown.value = autoRefreshIntervalSeconds.value
      await refreshAccountsIncrementally()
      return
    }

    autoRefreshCountdown.value -= 1
  },
  1000,
  { immediate: false }
)

const GROK_QUOTA_SIGNAL_MAX_AGE_MS = 24 * 60 * 60 * 1000
const GROK_QUOTA_SIGNAL_MAX_FUTURE_SKEW_MS = 5 * 60 * 1000

function firstNonBlankString(...values: unknown[]): string | undefined {
  return values.find((value): value is string => (
    typeof value === 'string' && value.trim().length > 0
  ))
}

function normalizeGrokPlanKey(value: unknown): string {
  if (typeof value !== 'string') return ''
  return value
    .trim()
    .toLowerCase()
    .replace(/[\s_-]+/g, '')
}

function grokPersistedQuotaSnapshot(extra: Record<string, any>): Record<string, any> | undefined {
  const usage = extra.grok_usage_snapshot
  if (usage && typeof usage === 'object' && !Array.isArray(usage)) {
    return usage as Record<string, any>
  }
  const legacy = extra.grok_quota_snapshot
  if (legacy && typeof legacy === 'object' && !Array.isArray(legacy)) {
    return legacy as Record<string, any>
  }
  return undefined
}

function isGrokQuotaTimestampFresh(raw: unknown): boolean {
  const value = String(raw || '').trim()
  if (!value) return false
  const observedAt = Date.parse(value)
  if (!Number.isFinite(observedAt)) return false
  const age = Date.now() - observedAt
  return age <= GROK_QUOTA_SIGNAL_MAX_AGE_MS && age >= -GROK_QUOTA_SIGNAL_MAX_FUTURE_SKEW_MS
}

function isGrok45ResponsesQuotaModel(model: unknown): boolean {
  const value = String(model || '')
    .trim()
    .toLowerCase()
    .replace(/^(x-ai|xai)\//, '')
  return value === 'grok-4.5' || value.startsWith('grok-4.5-')
}

function grokQuotaLooksHeavy(snapshot: Record<string, any> | undefined): boolean {
  const req = Number(snapshot?.requests?.limit ?? 0)
  const tok = Number(snapshot?.tokens?.limit ?? 0)
  return req >= 8300 || tok >= 53_000_000
}

function grok45ResponsesPlanIsHeavy(snapshot: Record<string, any> | undefined): boolean {
  if (!snapshot) return false
  const hint = normalizeGrokPlanKey(snapshot.plan_from_45_responses)
  if (hint === 'supergrokheavy' && isGrokQuotaTimestampFresh(snapshot.plan_from_45_responses_at)) {
    return true
  }
  const observedAt = snapshot.last_headers_seen_at || snapshot.updated_at
  return (
    isGrok45ResponsesQuotaModel(snapshot.model) &&
    isGrokQuotaTimestampFresh(observedAt) &&
    grokQuotaLooksHeavy(snapshot)
  )
}

// JWT / unambiguous credentials outrank snapshots. SuperGrokPro is ambiguous
// (covers SuperGrok and Heavy). 8300/53M only upgrades when the window came
// from grok-4.5 Responses (or a carried 4.5 hint).
function getAccountPlanType(row: any): string | undefined {
  if (!row) return undefined
  if (row.platform === 'grok') {
    const extra = (row.extra || {}) as Record<string, any>
    const billing = extra.grok_billing_snapshot as Record<string, any> | undefined
    const usage = extra.grok_usage_snapshot as Record<string, any> | undefined
    const legacyQuota = extra.grok_quota_snapshot as Record<string, any> | undefined
    const quota = grokPersistedQuotaSnapshot(extra)
    const cred = firstNonBlankString(row.credentials?.subscription_tier)
    const credKey = normalizeGrokPlanKey(cred)
    if (credKey && credKey !== 'supergrokpro') {
      return cred
    }
    if (
      grok45ResponsesPlanIsHeavy(quota) &&
      (credKey === 'supergrokpro' ||
        normalizeGrokPlanKey(billing?.plan) === 'supergrok' ||
        normalizeGrokPlanKey(billing?.plan) === 'supergrokpro')
    ) {
      return 'SuperGrok Heavy'
    }
    if (credKey === 'supergrokpro') {
      return firstNonBlankString(billing?.plan) || 'SuperGrok'
    }
    return firstNonBlankString(
      billing?.plan,
      usage?.subscription_tier,
      legacyQuota?.subscription_tier,
      extra.subscription_tier,
      row.credentials?.plan_type,
      row.parent_plan_type
    )
  }
  return firstNonBlankString(row.credentials?.plan_type, row.parent_plan_type)
}

function getOpenAIAuthMode(row: any): string | undefined {
  if (!row || row.platform !== 'openai' || row.type !== 'oauth') return undefined
  const authMode = row.credentials?.auth_mode
  return typeof authMode === 'string' && authMode.trim() ? authMode : undefined
}

// Antigravity 订阅等级辅助函数
function getAntigravityTierFromRow(row: any): string | null {
  if (row.platform !== 'antigravity') return null
  const extra = row.extra as Record<string, unknown> | undefined
  if (!extra) return null
  const lca = extra.load_code_assist as Record<string, unknown> | undefined
  if (!lca) return null
  const paid = lca.paidTier as Record<string, unknown> | undefined
  if (paid && typeof paid.id === 'string') return paid.id
  const current = lca.currentTier as Record<string, unknown> | undefined
  if (current && typeof current.id === 'string') return current.id
  return null
}

function getAntigravityTierLabel(row: any): string | null {
  const tier = getAntigravityTierFromRow(row)
  switch (tier) {
    case 'free-tier': return t('admin.accounts.tier.free')
    case 'g1-pro-tier': return t('admin.accounts.tier.pro')
    case 'g1-ultra-tier': return t('admin.accounts.tier.ultra')
    default: return null
  }
}

// 账号显示邮箱:优先账号自身(extra/credentials),影子账号回退母账号 parent_email。
// 供名称单元格 v-if/标题/文本三处共用,避免同一回退链在模板里重复三次。
function accountDisplayEmail(row: any): string {
  return row.extra?.email_address || row.extra?.email || row.credentials?.email || row.parent_email || ''
}

function accountHomepageUrl(row: Account): string {
  if (row.type !== 'apikey' || typeof row.credentials?.base_url !== 'string') return ''
  const baseUrl = sanitizeUrl(row.credentials.base_url)
  return baseUrl ? new URL(baseUrl).origin : ''
}

type OpenAICompactBadgeState = 'active' | 'blocked' | 'auto'

function getOpenAICompactState(row: any): OpenAICompactBadgeState | null {
  if (row.platform !== 'openai' || (row.type !== 'oauth' && row.type !== 'apikey')) return null
  const extra = row.extra as Record<string, unknown> | undefined
  const mode = typeof extra?.openai_compact_mode === 'string' ? extra.openai_compact_mode : 'auto'
  if (mode === 'force_on') return 'active'
  if (mode === 'force_off') return 'blocked'
  if (typeof extra?.openai_compact_supported === 'boolean') {
    return extra.openai_compact_supported ? 'active' : 'blocked'
  }
  return 'auto'
}

function getOpenAICompactMeta(row: any): { label: string; className: string; dotClass: string } | null {
  const state = getOpenAICompactState(row)
  if (!state) return null
  switch (state) {
    case 'active':
      return {
        label: t('admin.accounts.openai.compactSupported'),
        className: 'text-emerald-600 dark:text-emerald-300',
        dotClass: 'bg-emerald-500 shadow-[0_0_0_2px_rgba(16,185,129,0.14)]'
      }
    case 'blocked':
      return {
        label: t('admin.accounts.openai.compactUnsupported'),
        className: 'text-rose-600 dark:text-rose-300',
        dotClass: 'bg-rose-500 shadow-[0_0_0_2px_rgba(244,63,94,0.14)]'
      }
    case 'auto':
      return {
        label: t('admin.accounts.openai.compactAuto'),
        className: 'text-slate-500 dark:text-slate-400',
        dotClass: 'bg-slate-300 dark:bg-slate-500'
      }
  }
}

function getOpenAICompactTitle(row: any): string {
  const extra = row.extra as Record<string, unknown> | undefined
  const checkedAt = typeof extra?.openai_compact_checked_at === 'string' ? extra.openai_compact_checked_at : ''
  const label = getOpenAICompactMeta(row)?.label || ''
  if (!checkedAt) return label
  return `${label} | ${t('admin.accounts.openai.compactLastChecked')}: ${formatDateTime(new Date(checkedAt))}`
}

function getAntigravityTierColor(row: any): BadgeProps['color'] {
  const tier = getAntigravityTierFromRow(row)
  switch (tier) {
    case 'g1-pro-tier': return 'info'
    case 'g1-ultra-tier': return 'primary'
    default: return 'neutral'
  }
}

// All available columns
const allColumns = computed(() => {
  const c = [
    { key: 'select', label: '', sortable: false },
    { key: 'name', label: t('admin.accounts.columns.name'), sortable: true },
    { key: 'id', label: t('admin.accounts.columns.id'), sortable: true },
    { key: 'platform_type', label: t('admin.accounts.columns.platformType'), sortable: false },
    { key: 'capacity', label: t('admin.accounts.columns.capacity'), sortable: false },
    { key: 'status', label: t('admin.accounts.columns.status'), sortable: true },
    { key: 'schedulable', label: t('admin.accounts.columns.schedulable'), sortable: true },
    { key: 'today_stats', label: t('admin.accounts.columns.todayStats'), sortable: false }
  ]
  if (!authStore.isSimpleMode) {
    c.push({ key: 'groups', label: t('admin.accounts.columns.groups'), sortable: false })
  }
  c.push({ key: 'usage', label: t('admin.accounts.columns.usageWindows'), sortable: false })
  c.push({ key: 'custom_usage', label: t('admin.accounts.customUsage.column'), sortable: false })
  c.push(
    { key: 'proxy', label: t('admin.accounts.columns.proxy'), sortable: false },
    { key: 'priority', label: t('admin.accounts.columns.priority'), sortable: true },
    { key: 'scheduler_score', label: t('admin.accounts.columns.schedulerScore'), sortable: false },
    { key: 'rate_multiplier', label: t('admin.accounts.columns.billingRateMultiplier'), sortable: true },
    { key: 'upstream_billing_rate', label: t('admin.accounts.columns.upstreamBillingRate'), sortable: true },
    { key: 'last_used_at', label: t('admin.accounts.columns.lastUsed'), sortable: true },
    { key: 'created_at', label: t('admin.accounts.columns.createdAt'), sortable: true },
    { key: 'expires_at', label: t('admin.accounts.columns.expiresAt'), sortable: true },
    { key: 'notes', label: t('admin.accounts.columns.notes'), sortable: false },
    { key: 'actions', label: t('admin.accounts.columns.actions'), sortable: false }
  )
  return c
})

// Columns that can be toggled (exclude select, name, and actions)
const toggleableColumns = computed(() =>
  allColumns.value.filter(col => col.key !== 'select' && col.key !== 'name' && col.key !== 'actions')
)

// Toolbar menus: the same actions as before, as Nuxt UI dropdown items.
// Toggles keep the menu open (the old popovers did too); actions close it.
const keepMenuOpen = (event: Event) => event.preventDefault()
const autoRefreshMenuItems = computed<DropdownMenuItem[][]>(() => [
  [{
    type: 'checkbox',
    label: t('admin.accounts.enableAutoRefresh'),
    checked: autoRefreshEnabled.value,
    onUpdateChecked: () => setAutoRefreshEnabled(!autoRefreshEnabled.value),
    onSelect: keepMenuOpen
  }],
  autoRefreshIntervals.map((sec) => ({
    type: 'checkbox' as const,
    label: autoRefreshIntervalLabel(sec),
    checked: autoRefreshIntervalSeconds.value === sec,
    onUpdateChecked: () => setAutoRefreshInterval(sec),
    onSelect: keepMenuOpen
  }))
])
const accountToolsMenuItems = computed<DropdownMenuItem[][]>(() => [
  [
    { type: 'label', label: t('admin.accounts.dataActions') },
    { label: t('admin.accounts.syncFromCrs'), icon: 'i-lucide-refresh-cw', onSelect: openSyncFromCrs },
    { label: t('admin.accounts.dataImport'), icon: 'i-lucide-upload', onSelect: openImportData },
    {
      label: selIds.value.length ? t('admin.accounts.dataExportSelected') : t('admin.accounts.dataExport'),
      icon: 'i-lucide-download',
      slot: 'export',
      onSelect: openExportDataDialogFromMenu
    }
  ],
  [
    { type: 'label', label: t('admin.accounts.toolActions') },
    { label: t('admin.errorPassthrough.title'), icon: 'i-lucide-shield', onSelect: openErrorPassthrough },
    { label: t('admin.tlsFingerprintProfiles.title'), icon: 'i-lucide-lock', onSelect: openTLSFingerprintProfiles },
    { label: t('admin.accounts.balanceAlert.title'), icon: 'i-lucide-bell', onSelect: openBalanceAlert }
  ]
])
// "列显示" menu: one checkbox per optional column (persisted in localStorage).
const columnMenuItems = computed<DropdownMenuItem[]>(() =>
  toggleableColumns.value.map((col) => ({
    type: 'checkbox' as const,
    label: col.label,
    checked: isColumnVisible(col.key),
    onUpdateChecked: () => toggleColumn(col.key),
    onSelect: keepMenuOpen
  }))
)

// Filtered columns based on visibility
const cols = computed(() =>
  allColumns.value.filter(col =>
    col.key === 'select' || col.key === 'name' || col.key === 'actions' || !hiddenColumns.has(col.key)
  )
)

const accountDetailLoading = new Set<number>()
const loadAccountDetails = async (account: Pick<AccountListItem, 'id'>): Promise<Account | null> => {
  if (accountDetailLoading.has(account.id)) return null
  accountDetailLoading.add(account.id)
  try {
    return await adminAPI.accounts.getById(account.id)
  } catch (error) {
    console.error('Failed to load account details:', error)
    appStore.showError(extractApiErrorMessage(error, t('common.error')))
    return null
  } finally {
    accountDetailLoading.delete(account.id)
  }
}

// Account detail (slideover): follows the live row, so auto refresh keeps it current.
const detailAccountId = ref<number | null>(null)
const detailAccount = computed(() => accounts.value.find(account => account.id === detailAccountId.value) ?? null)
const openDetail = (a: AccountListItem) => { detailAccountId.value = a.id }

const handleEdit = async (a: AccountListItem) => {
  const account = await loadAccountDetails(a)
  if (!account) return
  edAcc.value = account
  showEdit.value = true
}
const openMenu = (a: Account, e: MouseEvent) => {
  menu.acc = a
  const target = e.currentTarget as HTMLElement
  menu.anchorRect = target.getBoundingClientRect()
  menu.show = true
}
// Header checkbox: checked when every visible row is selected, indeterminate when only some are.
const headerSelectState = computed<boolean | 'indeterminate'>(() => {
  if (allVisibleSelected.value) return true
  return accounts.value.some(account => isSelected(account.id)) ? 'indeterminate' : false
})
const toggleSelectAllVisible = (value: boolean | 'indeterminate') => {
  toggleVisible(value === true)
}
const handleBulkDelete = async () => {
  const accountIds = [...selIds.value]
  if (!(await confirm(t('admin.accounts.bulkActions.confirmDelete', { count: accountIds.length })))) return
  try {
    const result = await adminAPI.accounts.batchDelete(accountIds)
    if (result.failed > 0) {
      appStore.showError(t('admin.accounts.bulkActions.partialSuccess', {
        success: result.success,
        failed: result.failed
      }))
      setSelectedIds(result.failed_ids?.length ? result.failed_ids : accountIds)
    } else {
      appStore.showSuccess(t('admin.accounts.bulkActions.deleteSuccess', { count: result.success }))
      clearSelection()
    }
    await reload()
  } catch (error) {
    console.error('Failed to bulk delete accounts:', error)
    appStore.showError(String(error))
  }
}
const handleBulkResetStatus = async () => {
  if (!(await confirm(t('common.confirm')))) return
  try {
    const result = await adminAPI.accounts.batchClearError(selIds.value)
    if (result.failed > 0) {
      appStore.showError(t('admin.accounts.bulkActions.partialSuccess', { success: result.success, failed: result.failed }))
    } else {
      appStore.showSuccess(t('admin.accounts.bulkActions.resetStatusSuccess', { count: result.success }))
      clearSelection()
    }
    reload()
  } catch (error) {
    console.error('Failed to bulk reset status:', error)
    appStore.showError(String(error))
  }
}
const handleBulkRefreshToken = async () => {
  if (!(await confirm(t('common.confirm')))) return
  const accountIds = [...selIds.value]
  try {
    const result = await adminAPI.accounts.batchRefresh(accountIds)
    if (result.failed > 0) {
      appStore.showError(t('admin.accounts.bulkActions.partialSuccess', { success: result.success, failed: result.failed }))
      const failedIds = result.errors?.map(error => error.account_id) ?? []
      setSelectedIds(failedIds.length > 0 ? failedIds : accountIds)
    } else {
      appStore.showSuccess(t('admin.accounts.bulkActions.refreshTokenSuccess', { count: result.success }))
      clearSelection()
    }
    reload()
  } catch (error) {
    console.error('Failed to bulk refresh token:', error)
    appStore.showError(String(error))
  }
}
const handleBulkProbeUpstreamBilling = async () => {
  const accountIDs = [...selIds.value]
  if (accountIDs.length === 0) {
    appStore.showError(t('admin.accounts.upstreamBilling.noEligibleAccounts'))
    return
  }
  if (accountIDs.length > 20) {
    appStore.showError(t('admin.accounts.upstreamBilling.batchLimit'))
    return
  }
  accountIDs.forEach(id => probingUpstreamBilling.add(id))
  try {
    const results = await adminAPI.accounts.probeUpstreamBillingBatch(accountIDs)
    let patched = false
    results.forEach(result => {
      if (result.snapshot) {
        patchUpstreamBillingSnapshot(result.account_id, result.snapshot)
        patched = true
      }
    })
    if (patched) await refreshAccountsAfterUpstreamBillingProbe()
    const failed = results.filter(result => result.error).length
    if (failed > 0) {
      appStore.showError(t('admin.accounts.upstreamBilling.batchPartial', { success: results.length - failed, failed }))
    } else {
      appStore.showSuccess(t('admin.accounts.upstreamBilling.batchCompleted', { count: results.length }))
    }
  } catch (error) {
    console.error('Failed to probe upstream billing in batch:', error)
    appStore.showError(extractApiErrorMessage(error, t('admin.accounts.upstreamBilling.probeFailed')))
  } finally {
    accountIDs.forEach(id => probingUpstreamBilling.delete(id))
  }
}
const updateSchedulableInList = (accountIds: number[], schedulable: boolean) => {
  if (accountIds.length === 0) return
  const idSet = new Set(accountIds)
  accounts.value = accounts.value.map((account) => (idSet.has(account.id) ? { ...account, schedulable } : account))
}
const normalizeBulkSchedulableResult = (
  result: {
    success?: number
    failed?: number
    success_ids?: number[]
    failed_ids?: number[]
    results?: Array<{ account_id: number; success: boolean }>
  },
  accountIds: number[]
) => {
  const responseSuccessIds = Array.isArray(result.success_ids) ? result.success_ids : []
  const responseFailedIds = Array.isArray(result.failed_ids) ? result.failed_ids : []
  if (responseSuccessIds.length > 0 || responseFailedIds.length > 0) {
    return {
      successIds: responseSuccessIds,
      failedIds: responseFailedIds,
      successCount: typeof result.success === 'number' ? result.success : responseSuccessIds.length,
      failedCount: typeof result.failed === 'number' ? result.failed : responseFailedIds.length,
      hasIds: true,
      hasCounts: true
    }
  }

  const results = Array.isArray(result.results) ? result.results : []
  if (results.length > 0) {
    const successIds = results.filter(item => item.success).map(item => item.account_id)
    const failedIds = results.filter(item => !item.success).map(item => item.account_id)
    return {
      successIds,
      failedIds,
      successCount: typeof result.success === 'number' ? result.success : successIds.length,
      failedCount: typeof result.failed === 'number' ? result.failed : failedIds.length,
      hasIds: true,
      hasCounts: true
    }
  }

  const hasExplicitCounts = typeof result.success === 'number' || typeof result.failed === 'number'
  const successCount = typeof result.success === 'number' ? result.success : 0
  const failedCount = typeof result.failed === 'number' ? result.failed : 0
  if (hasExplicitCounts && failedCount === 0 && successCount === accountIds.length && accountIds.length > 0) {
    return {
      successIds: accountIds,
      failedIds: [],
      successCount,
      failedCount,
      hasIds: true,
      hasCounts: true
    }
  }

  return {
    successIds: [],
    failedIds: [],
    successCount,
    failedCount,
    hasIds: false,
    hasCounts: hasExplicitCounts
  }
}
const timerRefreshKey = ref(0)
let timerListRefresh: ReturnType<typeof setTimeout> | undefined
const handleTimerExecuted = () => {
  if (timerListRefresh !== undefined) return
  timerListRefresh = setTimeout(() => {
    timerListRefresh = undefined
    void load({ refreshTodayStats: false })
  }, 150)
}
const handleBulkToggleSchedulable = async (schedulable: boolean) => {
  const accountIds = [...selIds.value]
  if (!accountIds.length) return
  if (!(await confirm(t('admin.accounts.timer.bulkOverrideConfirm')))) return
  try {
    const result = await adminAPI.accounts.bulkUpdate(accountIds, { schedulable })
    timerRefreshKey.value++
    const { successIds, failedIds, successCount, failedCount, hasIds, hasCounts } = normalizeBulkSchedulableResult(result, accountIds)
    if (!hasIds && !hasCounts) {
      appStore.showError(t('admin.accounts.bulkSchedulableResultUnknown'))
      setSelectedIds(accountIds)
      load().catch((error) => {
        console.error('Failed to refresh accounts:', error)
      })
      return
    }
    if (successIds.length > 0) {
      updateSchedulableInList(successIds, schedulable)
    }
    if (successCount > 0 && failedCount === 0) {
      const message = schedulable
        ? t('admin.accounts.bulkSchedulableEnabled', { count: successCount })
        : t('admin.accounts.bulkSchedulableDisabled', { count: successCount })
      appStore.showSuccess(message)
    }
    if (failedCount > 0) {
      const message = hasCounts || hasIds
        ? t('admin.accounts.bulkSchedulablePartial', { success: successCount, failed: failedCount })
        : t('admin.accounts.bulkSchedulableResultUnknown')
      appStore.showError(message)
      setSelectedIds(failedIds.length > 0 ? failedIds : accountIds)
    } else {
      if (hasIds) clearSelection()
      else setSelectedIds(accountIds)
    }
  } catch (error) {
    console.error('Failed to bulk toggle schedulable:', error)
    appStore.showError(t('common.error'))
  }
}
const buildBulkEditFilterSnapshot = () => {
  const rawParams = toRaw(params) as Record<string, unknown>
  const sortOrder: AccountSortOrder = rawParams.sort_order === 'desc' ? 'desc' : 'asc'
  return {
    platform: typeof rawParams.platform === 'string' ? rawParams.platform : '',
    type: typeof rawParams.type === 'string' ? rawParams.type : '',
    status: typeof rawParams.status === 'string' ? rawParams.status : '',
    group: typeof rawParams.group === 'string' ? rawParams.group : '',
    search: typeof rawParams.search === 'string' ? rawParams.search : '',
    privacy_mode: typeof rawParams.privacy_mode === 'string' ? rawParams.privacy_mode : '',
    sort_by: typeof rawParams.sort_by === 'string' ? rawParams.sort_by : '',
    sort_order: sortOrder
  }
}

const handleSelectAllResults = async () => {
  if (selectingAllResults.value || pagination.total === 0) return

  const requestVersion = ++selectionRequestVersion.value
  const filters = buildBulkEditFilterSnapshot()
  selectingAllResults.value = true
  try {
    const ids = await fetchAllAccountIds(
      (page, pageSize, requestFilters) => adminAPI.accounts.list(page, pageSize, requestFilters),
      filters
    )
    if (requestVersion !== selectionRequestVersion.value) return

    setSelectedIds(ids)
    selectedAllResultIDs.value = new Set(ids)
  } catch (error) {
    if (requestVersion !== selectionRequestVersion.value) return
    console.error('Failed to select all account results:', error)
    appStore.showError(t('admin.accounts.bulkActions.selectAllFailed'))
  } finally {
    if (requestVersion === selectionRequestVersion.value) {
      selectingAllResults.value = false
    }
  }
}

const collectSelectionMetadata = (rows: Account[]) => {
  const selectedPlatforms = Array.from(new Set(rows.map(account => account.platform)))
  const selectedTypes = Array.from(new Set(rows.map(account => account.type)))
  return { selectedPlatforms, selectedTypes }
}

const openBulkEditSelected = () => {
  bulkEditTarget.value = {
    mode: 'selected',
    accountIds: [...selIds.value],
    selectedPlatforms: [...selPlatforms.value],
    selectedTypes: [...selTypes.value]
  }
  showBulkEdit.value = true
}

const openBulkEditFiltered = async () => {
  const filters = buildBulkEditFilterSnapshot()
  const preview = await adminAPI.accounts.list(1, 100, filters)
  const { selectedPlatforms, selectedTypes } = collectSelectionMetadata(preview.items)
  bulkEditTarget.value = {
    mode: 'filtered',
    filters,
    previewCount: preview.total,
    selectedPlatforms,
    selectedTypes
  }
  showBulkEdit.value = true
}

const handleBulkUpdated = () => {
  showBulkEdit.value = false
  bulkEditTarget.value = null
  clearSelection()
  reload()
}
const handleDataImported = () => { showImportData.value = false; reload() }
const ACCOUNT_UNGROUPED_GROUP_QUERY_VALUE = 'ungrouped'
const ACCOUNT_PRIVACY_MODE_UNSET_QUERY_VALUE = '__unset__'
const buildAccountQueryFilters = () => ({
  platform: params.platform || '',
  type: params.type || '',
  status: params.status || '',
  group: params.group || '',
  privacy_mode: params.privacy_mode || '',
  search: params.search || '',
  sort_by: sortState.sort_by,
  sort_order: sortState.sort_order
})
const accountMatchesCurrentFilters = (account: Account) => {
  const filters = buildAccountQueryFilters()
  if (filters.platform && account.platform !== filters.platform) return false
  if (filters.type && account.type !== filters.type) return false
  if (filters.status) {
    const now = Date.now()
    const rateLimitResetAt = account.rate_limit_reset_at ? new Date(account.rate_limit_reset_at).getTime() : Number.NaN
    const isRateLimited = Number.isFinite(rateLimitResetAt) && rateLimitResetAt > now
    const tempUnschedUntil = account.temp_unschedulable_until ? new Date(account.temp_unschedulable_until).getTime() : Number.NaN
    const isTempUnschedulable = Number.isFinite(tempUnschedUntil) && tempUnschedUntil > now

    if (filters.status === 'active') {
      if (account.status !== 'active' || isRateLimited || isTempUnschedulable || !account.schedulable) return false
    } else if (filters.status === 'rate_limited') {
      if (account.status !== 'active' || !isRateLimited || isTempUnschedulable) return false
    } else if (filters.status === 'temp_unschedulable') {
      if (account.status !== 'active' || !isTempUnschedulable) return false
    } else if (filters.status === 'unschedulable') {
      if (account.status !== 'active' || account.schedulable || isRateLimited || isTempUnschedulable) return false
    } else if (account.status !== filters.status) {
      return false
    }
  }
  if (filters.group) {
    const groupIds = account.group_ids ?? account.groups?.map((group) => group.id) ?? []
    if (filters.group === ACCOUNT_UNGROUPED_GROUP_QUERY_VALUE) {
      if (groupIds.length > 0) return false
    } else if (!groupIds.includes(Number(filters.group))) {
      return false
    }
  }
  const privacyMode = typeof account.extra?.privacy_mode === 'string' ? account.extra.privacy_mode : ''
  if (filters.privacy_mode) {
    if (filters.privacy_mode === ACCOUNT_PRIVACY_MODE_UNSET_QUERY_VALUE) {
      if (privacyMode.trim() !== '') return false
    } else if (privacyMode !== filters.privacy_mode) {
      return false
    }
  }
  const search = String(filters.search || '').trim().toLowerCase()
  if (search && !account.name.toLowerCase().includes(search)) return false
  return true
}
const mergeRuntimeFields = (oldAccount: Account, updatedAccount: Account): Account => ({
  ...updatedAccount,
  current_concurrency: updatedAccount.current_concurrency ?? oldAccount.current_concurrency,
  current_window_cost: updatedAccount.current_window_cost ?? oldAccount.current_window_cost,
  active_sessions: updatedAccount.active_sessions ?? oldAccount.active_sessions
})

const syncPaginationAfterLocalRemoval = () => {
  const nextTotal = Math.max(0, pagination.total - 1)
  pagination.total = nextTotal
  pagination.pages = nextTotal > 0 ? Math.ceil(nextTotal / pagination.page_size) : 0

  const maxPage = Math.max(1, pagination.pages || 1)

  if (pagination.page > maxPage) {
    pagination.page = maxPage
  }
  // 行被本地移除后不立刻全量补页，改为提示用户手动同步。
  hasPendingListSync.value = nextTotal > 0
}

const patchAccountInList = (updatedAccount: Account) => {
  const index = accounts.value.findIndex(account => account.id === updatedAccount.id)
  if (index === -1) return
  const mergedAccount = mergeRuntimeFields(accounts.value[index], updatedAccount)
  if (!accountMatchesCurrentFilters(mergedAccount)) {
    accounts.value = accounts.value.filter(account => account.id !== mergedAccount.id)
    syncPaginationAfterLocalRemoval()
    removeSelectedAccounts([mergedAccount.id])
    if (menu.acc?.id === mergedAccount.id) {
      menu.show = false
      menu.acc = null
    }
    return
  }
  const nextAccounts = [...accounts.value]
  nextAccounts[index] = mergedAccount
  accounts.value = nextAccounts
  syncAccountRefs(mergedAccount)
}
const patchUpstreamBillingSnapshot = (accountID: number, snapshot: UpstreamBillingProbeSnapshot) => {
  const account = accounts.value.find(item => item.id === accountID)
  if (!account) return
  upstreamBillingNow.value = Date.now()
  patchAccountInList({
    ...account,
    ...(typeof snapshot.synced_rate_multiplier === 'number'
      ? { rate_multiplier: snapshot.synced_rate_multiplier }
      : {}),
    extra: { ...account.extra, upstream_billing_probe: snapshot }
  })
}
const refreshAccountsAfterUpstreamBillingProbe = async () => {
  await refreshUpstreamBillingSortedList(true)
}
const handleProbeUpstreamBilling = async (account: Account) => {
  if (probingUpstreamBilling.has(account.id)) return
  probingUpstreamBilling.add(account.id)
  try {
    const result = await adminAPI.accounts.probeUpstreamBilling(account.id)
    if (result.snapshot) {
      patchUpstreamBillingSnapshot(account.id, result.snapshot)
      await refreshAccountsAfterUpstreamBillingProbe()
    }
  } catch (error) {
    console.error('Failed to probe upstream billing:', error)
    appStore.showError(extractApiErrorMessage(error, t('admin.accounts.upstreamBilling.probeFailed')))
  } finally {
    probingUpstreamBilling.delete(account.id)
  }
}
const handleAccountUpdated = (updatedAccount: Account) => {
  patchAccountInList(updatedAccount)
  enterAutoRefreshSilentWindow()
}
const formatExportTimestamp = () => {
  const now = new Date()
  const pad2 = (value: number) => String(value).padStart(2, '0')
  return `${now.getFullYear()}${pad2(now.getMonth() + 1)}${pad2(now.getDate())}${pad2(now.getHours())}${pad2(now.getMinutes())}${pad2(now.getSeconds())}`
}
const openExportDataDialog = () => {
  includeProxyOnExport.value = true
  showExportDataDialog.value = true
}
const handleExportData = async () => {
  if (exportingData.value) return
  exportingData.value = true
  try {
    const dataPayload = await accountExportStepUp.run(() => adminAPI.accounts.exportData(
      selIds.value.length > 0
        ? { ids: selIds.value, includeProxies: includeProxyOnExport.value }
        : {
            includeProxies: includeProxyOnExport.value,
            filters: buildAccountQueryFilters()
          }
    ))
    const timestamp = formatExportTimestamp()
    const filename = `sub2api-account-${timestamp}.json`
    const blob = new Blob([JSON.stringify(dataPayload, null, 2)], { type: 'application/json' })
    const url = URL.createObjectURL(blob)
    const link = document.createElement('a')
    link.href = url
    link.download = filename
    link.click()
    URL.revokeObjectURL(url)
    // spark 影子账号被后端排除出备份(其凭据透传母账号、调度配置不可经凭据型导入重建);
    // 跳过非零时明确提示用户,避免「下载成功但少了账号」的静默丢失。
    if (dataPayload.skipped_shadows && dataPayload.skipped_shadows > 0) {
      appStore.showWarning(t('admin.accounts.dataExportedSkippedShadows', { count: dataPayload.skipped_shadows }))
    } else {
      appStore.showSuccess(t('admin.accounts.dataExported'))
    }
  } catch (error: any) {
    if (isStepUpCancelled(error)) {
      // 用户主动取消 step-up 验证，静默返回，不弹错误提示。
    } else if (isStepUpBlocked(error)) {
      appStore.showError(
        stepUpBlockReason(error) === 'STEP_UP_ADMIN_API_KEY_FORBIDDEN'
          ? t('stepUp.adminApiKeyForbidden')
          : t('stepUp.notEnabled')
      )
    } else {
      appStore.showError(error?.message || t('admin.accounts.dataExportFailed'))
    }
  } finally {
    exportingData.value = false
    showExportDataDialog.value = false
  }
}
const accountExportStepUp = useStepUp()
const closeTestModal = () => { showTest.value = false; testingAcc.value = null }
const batchTestAccounts = computed(() => resolveBatchTestAccounts(selIds.value, accounts.value))
const openBatchTestModal = () => {
  if (selIds.value.length === 0) return
  showBatchTest.value = true
}
const closeBatchTestModal = () => { showBatchTest.value = false }
const closeStatsModal = () => { showStats.value = false; statsAcc.value = null }
const closeReAuthModal = () => { showReAuth.value = false; reAuthAcc.value = null }
const handleTest = async (a: AccountListItem) => {
  const account = await loadAccountDetails(a)
  if (!account) return
  testingAcc.value = account
  showTest.value = true
}
const handleViewStats = async (a: AccountListItem) => {
  const account = await loadAccountDetails(a)
  if (!account) return
  statsAcc.value = account
  showStats.value = true
}
const handleSchedule = async (a: Account) => {
  scheduleAcc.value = a
  scheduleModelOptions.value = []
  showSchedulePanel.value = true
  try {
    const models = await adminAPI.accounts.getAvailableModels(a.id)
    scheduleModelOptions.value = models.map((m: ClaudeModel) => ({ value: m.id, label: m.display_name || m.id }))
  } catch {
    scheduleModelOptions.value = []
  }
}
const closeSchedulePanel = () => { showSchedulePanel.value = false; scheduleAcc.value = null; scheduleModelOptions.value = [] }
const handleReAuth = (a: Account) => { reAuthAcc.value = a; showReAuth.value = true }
const duplicatingAccountIDs = new Set<number>()
const handleDuplicateAccount = async (a: Account) => {
  if (duplicatingAccountIDs.has(a.id)) return
  duplicatingAccountIDs.add(a.id)
  try {
    const duplicate = await adminAPI.accounts.duplicate(a.id)
    appStore.showSuccess(t('admin.accounts.duplicateSuccess', { name: duplicate.name }))
    reload()
  } catch (error: any) {
    console.error('Failed to duplicate account:', error)
    appStore.showError(error?.message || t('admin.accounts.duplicateFailed'))
  } finally {
    duplicatingAccountIDs.delete(a.id)
  }
}
const handleRefresh = async (a: Account) => {
  try {
    const updated = await adminAPI.accounts.refreshCredentials(a.id)
    patchAccountInList(updated)
    enterAutoRefreshSilentWindow()
    if (updated.warning && updated.message) appStore.showWarning(updated.message)
  } catch (error) {
    console.error('Failed to refresh credentials:', error)
  }
}
const handleRecoverState = async (a: Account) => {
  try {
    const updated = await adminAPI.accounts.recoverState(a.id)
    patchAccountInList(updated)
    enterAutoRefreshSilentWindow()
    appStore.showSuccess(t('admin.accounts.recoverStateSuccess'))
  } catch (error: any) {
    console.error('Failed to recover account state:', error)
    appStore.showError(error?.message || t('admin.accounts.recoverStateFailed'))
  }
}
const handleResetQuota = async (a: Account) => {
  try {
    const updated = await adminAPI.accounts.resetAccountQuota(a.id)
    patchAccountInList(updated)
    enterAutoRefreshSilentWindow()
    appStore.showSuccess(t('common.success'))
  } catch (error) {
    console.error('Failed to reset quota:', error)
  }
}

const privacyResultMessageKey = (account: Account): { type: 'success' | 'error'; key: string } => {
  const mode = typeof account.extra?.privacy_mode === 'string' ? account.extra.privacy_mode : ''
  if (account.platform === 'openai') {
    switch (mode) {
      case 'training_off':
        return { type: 'success', key: 'admin.accounts.privacyTrainingOff' }
      case 'training_set_cf_blocked':
        return { type: 'error', key: 'admin.accounts.privacyCfBlocked' }
      default:
        return { type: 'error', key: 'admin.accounts.privacyFailed' }
    }
  }
  if (account.platform === 'antigravity') {
    if (mode === 'privacy_set') {
      return { type: 'success', key: 'admin.accounts.privacyAntigravitySet' }
    }
    return { type: 'error', key: 'admin.accounts.privacyAntigravityFailed' }
  }
  return { type: 'error', key: 'admin.accounts.privacyFailed' }
}

const handleSetPrivacy = async (a: Account) => {
  try {
    const updated = await adminAPI.accounts.setPrivacy(a.id)
    patchAccountInList(updated)
    enterAutoRefreshSilentWindow()
    const result = privacyResultMessageKey(updated)
    if (result.type === 'success') {
      appStore.showSuccess(t(result.key))
    } else {
      appStore.showError(t(result.key))
    }
  } catch (error: any) {
    console.error('Failed to set privacy:', error)
    appStore.showError(error?.response?.data?.message || t('admin.accounts.privacyFailed'))
  }
}
const onRevertFallback = async (a: Account) => {
  try {
    await adminAPI.accounts.revertProxyFallback(a.id)
    appStore.showSuccess(t('admin.accounts.revertProxySuccess'))
    reload()
  } catch (error: any) {
    console.error('Failed to revert proxy fallback:', error)
    appStore.showError(error?.response?.data?.message || t('admin.accounts.revertProxyFailed'))
  }
}
const handleCreateSparkShadow = (a: Account) => {
  creatingShadowAcc.value = a
  showCreateShadowDialog.value = true
}
const confirmCreateSparkShadow = async () => {
  const a = creatingShadowAcc.value
  if (!a) return
  try {
    await adminAPI.accounts.createSparkShadow(a.id, { name: `${a.name} (Spark)` })
    showCreateShadowDialog.value = false
    creatingShadowAcc.value = null
    appStore.showSuccess(t('admin.accounts.createSparkShadowSuccess'))
    reload()
  } catch (error: any) {
    console.error('Failed to create spark shadow:', error)
    appStore.showError(error?.response?.data?.message || t('admin.accounts.createSparkShadowFailed'))
  }
}
const handleDelete = (a: Account) => { deletingAcc.value = a; showDeleteDialog.value = true }
const confirmDelete = async () => { if(!deletingAcc.value) return; try { await adminAPI.accounts.delete(deletingAcc.value.id); showDeleteDialog.value = false; deletingAcc.value = null; reload() } catch (error) { console.error('Failed to delete account:', error) } }
const handleToggleSchedulable = async (a: Account) => {
  if (!(await confirm(t('admin.accounts.timer.manualOverrideConfirm')))) return
  const nextSchedulable = !a.schedulable
  togglingSchedulable.value = a.id
  try {
    const updated = await adminAPI.accounts.setSchedulable(a.id, nextSchedulable)
    timerRefreshKey.value++
    updateSchedulableInList([a.id], updated?.schedulable ?? nextSchedulable)
    enterAutoRefreshSilentWindow()
  } catch (error) {
    console.error('Failed to toggle schedulable:', error)
    appStore.showError(t('admin.accounts.failedToToggleSchedulable'))
  } finally {
    togglingSchedulable.value = null
  }
}
const handleShowTempUnsched = (a: Account) => { tempUnschedAcc.value = a; showTempUnsched.value = true }
const handleTempUnschedReset = async (updated: Account) => {
  showTempUnsched.value = false
  tempUnschedAcc.value = null
  patchAccountInList(updated)
  enterAutoRefreshSilentWindow()
}
const formatExpiresAt = (value: number | null) => {
  if (!value) return '-'
  return formatDateTime(
    new Date(value * 1000),
    {
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
      hour12: false
    },
    'sv-SE'
  )
}
const isExpired = (value: number | null) => {
  if (!value) return false
  return value * 1000 <= Date.now()
}
// 所绑定代理的有效期(逻辑同 /admin/proxies,见 utils/proxyExpiry):危险红、临近黄,其余为普通文字。
const proxyExpiryColor = (p: AccountProxy): 'error' | 'warning' | 'neutral' => {
  const level = proxyExpiryBadgeClass(p.expires_at, p.status)
  if (level.includes('badge-danger')) return 'error'
  if (level.includes('badge-warning')) return 'warning'
  return 'neutral'
}
const proxyExpiryText = (p: AccountProxy): string => {
  const { key, params } = proxyExpiryLabelKey(p.expires_at, p.status)
  return params ? t(key, params) : t(key)
}

// 表格滚动时关闭行操作菜单（顶部工具菜单由 UDropdownMenu 自己定位和关闭）。
const handleScroll = (event: Event) => {
  if (event.target instanceof Element && event.target.closest('.action-menu-content')) return
  menu.show = false
}

// The bulk bar renders twice (toolbar actions, footer selection helpers); both share these handlers.
const bulkActionListeners = {
  delete: handleBulkDelete,
  'reset-status': handleBulkResetStatus,
  'refresh-token': handleBulkRefreshToken,
  'probe-upstream-billing': handleBulkProbeUpstreamBilling,
  'test-models': openBatchTestModal,
  'edit-selected': openBulkEditSelected,
  'edit-filtered': openBulkEditFiltered,
  clear: clearSelection,
  'select-page': selectPage,
  'select-all-results': handleSelectAllResults,
  'toggle-schedulable': handleBulkToggleSchedulable
}

onMounted(async () => {
  if (typeof window !== 'undefined') {
    desktopViewportMediaQuery = window.matchMedia(desktopViewportQuery)
    isDesktopViewport.value = desktopViewportMediaQuery.matches
    desktopViewportListener = (event: MediaQueryListEvent) => {
      isDesktopViewport.value = event.matches
    }
    if (typeof desktopViewportMediaQuery.addEventListener === 'function') {
      desktopViewportMediaQuery.addEventListener('change', desktopViewportListener)
    } else {
      desktopViewportMediaQuery.addListener(desktopViewportListener)
    }
  }

  load()
  loadUpstreamBillingProbeGlobalState()
  const [proxiesResult, groupsResult] = await Promise.allSettled([
    adminAPI.proxies.getAll(),
    adminAPI.groups.getAll()
  ])
  if (proxiesResult.status === 'fulfilled') {
    proxies.value = proxiesResult.value
  } else {
    console.error('Failed to load proxies:', proxiesResult.reason)
  }
  if (groupsResult.status === 'fulfilled') {
    groups.value = groupsResult.value
  } else {
    console.error('Failed to load groups:', groupsResult.reason)
  }
  window.addEventListener('scroll', handleScroll, true)

  if (autoRefreshEnabled.value) {
    autoRefreshCountdown.value = autoRefreshIntervalSeconds.value
    resumeAutoRefresh()
  } else {
    pauseAutoRefresh()
  }
})

onUnmounted(() => {
  clearTimeout(timerListRefresh)
  upstreamBillingRateAbortController?.abort()
  if (usageBatchFlushTimer !== null) {
    clearTimeout(usageBatchFlushTimer)
    usageBatchFlushTimer = null
  }
  pendingUsageBatchIds.clear()
  window.removeEventListener('scroll', handleScroll, true)
  if (desktopViewportMediaQuery && desktopViewportListener) {
    if (typeof desktopViewportMediaQuery.removeEventListener === 'function') {
      desktopViewportMediaQuery.removeEventListener('change', desktopViewportListener)
    } else {
      desktopViewportMediaQuery.removeListener(desktopViewportListener)
    }
  }
  desktopViewportListener = null
  desktopViewportMediaQuery = null
})
</script>

<style scoped>
.accounts-workspace {
  width: 100%;
  max-width: none;
  min-width: 0;
  gap: 12px;
}

.accounts-workspace :deep(.layout-section-scrollable),
.accounts-workspace :deep(.table-scroll-container) {
  width: 100%;
  max-width: none;
  min-width: 0;
}

.accounts-workspace :deep(.layout-section-scrollable) {
  overflow: hidden;
}

.accounts-table-stage :deep(.table-wrapper) {
  width: 100%;
  max-width: 100%;
  min-width: 0;
  overflow-x: auto;
}

.accounts-table-stage {
  display: flex;
  width: 100%;
  max-width: 100%;
  min-height: 0;
  min-width: 0;
  flex: 1 1 auto;
  flex-direction: column;
  overflow: hidden;
}

/* Footer: selected count + selection helpers above the pager, like the template's
   "n of m row(s) selected" line. */
.accounts-footer {
  display: flex;
  min-width: 0;
  flex-direction: column;
  gap: 6px;
}

.accounts-footer > :first-child {
  padding-inline: 4px;
}

@media (max-width: 767px) {
  .accounts-workspace {
    gap: 10px;
  }
}
</style>
