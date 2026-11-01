<template>
  <AppLayout>
    <TablePageLayout class="admin-users-workbench">
      <!-- Toolbar row (template customers page): search + filters left, selection and view tools right -->
      <template #filters>
        <AdminPageHeader
          class="mb-4"
          :title="t('admin.users.title')"
          :description="t('admin.users.description')"
        >
          <template #actions>
            <UButton icon="i-lucide-plus" :label="t('admin.users.createUser')" @click="showCreateModal = true" />
          </template>
        </AdminPageHeader>
        <PageToolbar>
          <template #left>
            <UInput
              v-model="searchQuery"
              icon="i-lucide-search"
              :placeholder="t('admin.users.searchUsers')"
              class="w-64 sm:w-80"
              @update:model-value="handleSearch"
            />

            <div v-if="visibleFilters.has('role')" class="w-32">
              <Select
                v-model="filters.role"
                :options="[
                  { value: '', label: t('admin.users.allRoles') },
                  { value: 'admin', label: t('admin.users.admin') },
                  { value: 'user', label: t('admin.users.user') }
                ]"
                @change="applyFilter"
              />
            </div>

            <div v-if="visibleFilters.has('status')" class="w-32">
              <Select
                v-model="filters.status"
                :options="[
                  { value: '', label: t('admin.users.allStatus') },
                  { value: 'active', label: t('common.active') },
                  { value: 'disabled', label: t('admin.users.disabled') }
                ]"
                @change="applyFilter"
              />
            </div>

            <div v-if="visibleFilters.has('group')" class="w-44">
              <Select
                v-model="filters.group"
                :options="groupFilterOptions"
                searchable
                creatable
                :creatable-prefix="t('admin.users.fuzzySearch')"
                :search-placeholder="t('admin.users.searchAuthorizedGroups')"
                @change="applyFilter"
              />
            </div>

            <div v-if="visibleFilters.has('apiKeyGroup')" class="w-44">
              <Select
                v-model="filters.apiKeyGroup"
                :options="apiKeyGroupFilterOptions"
                searchable
                :search-placeholder="t('admin.users.searchApiKeyGroups')"
                @change="applyFilter"
              />
            </div>

            <!-- Dynamic attribute filters -->
            <template v-for="(value, attrId) in activeAttributeFilters" :key="attrId">
              <div v-if="visibleFilters.has(`attr_${attrId}`)" class="w-36">
                <Select
                  v-if="['select', 'multi_select'].includes(getAttributeDefinition(Number(attrId))?.type || '')"
                  :model-value="value"
                  :options="[
                    { value: '', label: getAttributeDefinitionName(Number(attrId)) },
                    ...(getAttributeDefinition(Number(attrId))?.options || [])
                  ]"
                  @update:model-value="(val: unknown) => { updateAttributeFilter(Number(attrId), String(val ?? '')); applyFilter() }"
                />
                <UInput
                  v-else
                  :model-value="value"
                  :type="getAttributeDefinition(Number(attrId))?.type === 'number' ? 'number' : 'text'"
                  :placeholder="getAttributeDefinitionName(Number(attrId))"
                  class="w-full"
                  @update:model-value="(val: unknown) => updateAttributeFilter(Number(attrId), String(val ?? ''))"
                  @keyup.enter="applyFilter"
                />
              </div>
            </template>
          </template>
          <template #right>
            <template v-if="selectedCount > 0">
              <UButton
                color="neutral"
                variant="subtle"
                icon="i-lucide-gauge"
                :label="t('admin.users.bulkLimits.short')"
                data-test="bulk-edit-limits"
                @click="showBulkEditModal = true"
              >
                <template #trailing>
                  <UBadge :label="String(selectedCount)" color="neutral" variant="outline" size="sm" />
                </template>
              </UButton>
              <UButton
                color="error"
                variant="subtle"
                icon="i-lucide-trash"
                :label="t('common.delete')"
                data-test="bulk-delete-users"
                :disabled="bulkDeleting"
                @click="bulkDeleteIds = [...selectedIds]"
              >
                <template #trailing>
                  <UBadge :label="String(selectedCount)" color="error" variant="outline" size="sm" />
                </template>
              </UButton>
            </template>
            <UButton
              color="neutral"
              variant="outline"
              icon="i-lucide-refresh-cw"
              square
              :disabled="loading"
              :ui="{ leadingIcon: loading ? 'animate-spin' : '' }"
              :title="t('common.refresh')"
              :aria-label="t('common.refresh')"
              @click="loadUsers"
            />
            <UDropdownMenu :items="filterMenuItems" :content="{ align: 'end' }">
              <UButton
                color="neutral"
                variant="outline"
                icon="i-lucide-list-filter"
                :title="t('admin.users.filterSettings')"
                :aria-label="t('admin.users.filterSettings')"
              >
                <span class="hidden sm:inline">{{ t('admin.users.filterSettings') }}</span>
              </UButton>
            </UDropdownMenu>
            <UDropdownMenu :items="columnMenuItems" :content="{ align: 'end' }" :ui="{ content: 'max-h-80' }">
              <UButton
                color="neutral"
                variant="outline"
                trailing-icon="i-lucide-settings-2"
                :title="t('admin.users.displayColumns')"
                :aria-label="t('admin.users.displayColumns')"
              >
                <span class="hidden sm:inline">{{ t('admin.users.displayColumns') }}</span>
              </UButton>
            </UDropdownMenu>
            <UButton
              color="neutral"
              variant="outline"
              icon="i-lucide-tags"
              square
              :title="t('admin.users.attributes.configButton')"
              :aria-label="t('admin.users.attributes.configButton')"
              @click="showAttributesModal = true"
            />
          </template>
        </PageToolbar>
      </template>

      <!-- Users Table -->
      <template #table>
        <DataTable
          :columns="columns"
          :data="sortedUsers"
          :loading="loading"
          row-key="id"
          selectable
          :selected-keys="selectedIds"
          :selection-label="getUserSelectionLabel"
          :actions-count="3"
          :server-side-sort="true"
          default-sort-key="created_at"
          default-sort-order="desc"
          :sort-storage-key="USER_SORT_STORAGE_KEY"
          clickable-rows
          @sort="handleSort"
          @row-click="openQuickLook"
          @update:selected-keys="handleSelectedKeysUpdate"
        >
          <template #cell-email="{ value }">
            <div class="flex items-center gap-3">
              <UAvatar :alt="value" :text="(value || '').charAt(0).toUpperCase()" size="sm" />
              <span class="font-medium text-highlighted">{{ value }}</span>
            </div>
          </template>

          <template #cell-username="{ value }">
            <span class="text-default">{{ value || '-' }}</span>
          </template>

          <template #cell-notes="{ value }">
            <span
              v-if="value"
              :title="value.length > 30 ? value : undefined"
              class="block max-w-xs truncate text-muted"
            >
              {{ value.length > 30 ? value.substring(0, 25) + '...' : value }}
            </span>
            <span v-else class="text-dimmed">-</span>
          </template>

          <!-- Dynamic attribute columns -->
          <template
            v-for="def in attributeDefinitions.filter(d => d.enabled)"
            :key="def.id"
            #[`cell-attr_${def.id}`]="{ row }"
          >
            <span class="block max-w-xs truncate text-default" :title="getAttributeValue(row.id, def.id)">
              {{ getAttributeValue(row.id, def.id) }}
            </span>
          </template>

          <template #cell-role="{ value }">
            <UBadge
              :color="value === 'admin' ? 'primary' : 'neutral'"
              variant="subtle"
              :label="t('admin.users.roles.' + value)"
            />
          </template>

          <template #cell-groups="{ row }">
            <div v-if="allGroups.length > 0" class="flex flex-col items-start gap-0.5" @click.stop>
              <!-- Exclusive groups: the menu lists them; picking one opens the replace dialog -->
              <UDropdownMenu
                v-if="getUserGroups(row).exclusive.length > 0"
                :items="exclusiveGroupMenuItems(row)"
                :content="{ align: 'start' }"
              >
                <UButton
                  color="neutral"
                  variant="ghost"
                  size="xs"
                  icon="i-lucide-shield"
                  class="-mx-1.5"
                  :title="getUserGroups(row).exclusive.map(g => g.name).join('\n')"
                >
                  <span class="font-medium text-highlighted">{{ getUserGroups(row).exclusive.length }}</span>
                  <span class="text-muted">{{ t('admin.users.exclusiveLabel') }}</span>
                </UButton>
              </UDropdownMenu>
              <UTooltip v-if="getUserGroups(row).publicGroups.length > 0" :content="{ side: 'bottom', align: 'start' }">
                <span class="inline-flex cursor-default items-center gap-1 px-0.5 text-xs">
                  <UIcon name="i-lucide-globe" class="size-3.5 text-dimmed" />
                  <span class="font-medium text-default">{{ getUserGroups(row).publicGroups.length }}</span>
                  <span class="text-muted">{{ t('admin.users.publicLabel') }}</span>
                </span>
                <template #content>
                  <div class="flex flex-col gap-0.5 whitespace-nowrap">
                    <span v-for="g in getUserGroups(row).publicGroups" :key="g.id">{{ g.name }}</span>
                  </div>
                </template>
              </UTooltip>
              <span
                v-if="getUserGroups(row).exclusive.length === 0 && getUserGroups(row).publicGroups.length === 0"
                class="text-xs text-dimmed"
              >-</span>
            </div>
            <span v-else class="text-xs text-dimmed">-</span>
          </template>

          <template #cell-subscriptions="{ row }">
            <div v-if="row.subscriptions && row.subscriptions.length > 0" class="flex flex-wrap gap-1.5">
              <GroupBadge
                v-for="sub in row.subscriptions"
                :key="sub.id"
                :name="sub.group?.name || ''"
                :platform="sub.group?.platform"
                :subscription-type="sub.group?.subscription_type"
                :rate-multiplier="sub.group?.rate_multiplier"
                :days-remaining="sub.expires_at ? getDaysRemaining(sub.expires_at) : null"
                :title="sub.expires_at ? formatDateTime(sub.expires_at) : ''"
              />
            </div>
            <span v-else class="text-xs text-dimmed">{{ t('admin.users.noSubscription') }}</span>
          </template>

          <template #cell-balance="{ value, row }">
            <div class="flex items-center gap-1" @click.stop>
              <UTooltip :text="t('admin.users.balanceHistoryTip')">
                <UButton
                  color="neutral"
                  variant="link"
                  size="sm"
                  class="-mx-2 font-medium tabular-nums text-highlighted"
                  :label="`$${value.toFixed(2)}`"
                  @click="handleBalanceHistory(row)"
                />
              </UTooltip>
              <UButton
                color="success"
                variant="ghost"
                size="xs"
                :label="t('admin.users.deposit')"
                :title="t('admin.users.deposit')"
                @click.stop="handleDeposit(row)"
              />
            </div>
          </template>

          <template #cell-balance_platform_quota="{ row }">
            <UButton
              color="neutral"
              variant="ghost"
              size="sm"
              class="-mx-2 text-left"
              :title="t('admin.users.platformQuota.cellColumnTooltip')"
              @click.stop="handlePlatformQuota(row)"
            >
              <UserPlatformQuotaCell :quotas="platformQuotaStats[row.id]" />
            </UButton>
          </template>

          <!-- Usage columns: a sort menu (today / last 30 days, desc → asc → off) sorts the current page only -->
          <template
            v-for="usageKey in USAGE_COLUMN_KEYS"
            :key="usageKey"
            #[`header-${usageKey}`]="{ column }"
          >
            <div class="flex items-center gap-1">
              <span>{{ column.label }}</span>
              <UDropdownMenu :items="usageSortMenuItems(usageKey)" :content="{ align: 'end' }">
                <UButton
                  :color="usageSort && usageSort.key === usageKey ? 'primary' : 'neutral'"
                  variant="ghost"
                  size="xs"
                  :trailing-icon="usageSortIcon(usageKey)"
                  :label="usageSort && usageSort.key === usageKey ? (usageSort.metric === 'today' ? t('admin.users.today') : t('admin.users.total')) : undefined"
                  :title="t('admin.users.sortBy')"
                  :aria-label="t('admin.users.sortBy')"
                  :data-test="`usage-sort-trigger-${usageKey}`"
                />
                <template
                  v-for="metric in (['today', 'total'] as const)"
                  :key="metric"
                  #[`usage-${usageKey}-${metric}-label`]
                >
                  <span :data-test="`usage-sort-${usageKey}-${metric}`">
                    {{ metric === 'today' ? t('admin.users.today') : t('admin.users.total') }}
                  </span>
                </template>
              </UDropdownMenu>
            </div>
          </template>

          <template #cell-usage="{ row }">
            <PlatformUsageBreakdown
              :today="usageStats[row.id]?.today_actual_cost ?? 0"
              :total="usageStats[row.id]?.total_actual_cost ?? 0"
              :by-platform="usageStats[row.id]?.by_platform"
            />
          </template>
          <template #cell-usage_anthropic="{ row }"><PlatformCostCell :usage="getPlatformUsage(row.id, 'anthropic')" /></template>
          <template #cell-usage_openai="{ row }"><PlatformCostCell :usage="getPlatformUsage(row.id, 'openai')" /></template>
          <template #cell-usage_gemini="{ row }"><PlatformCostCell :usage="getPlatformUsage(row.id, 'gemini')" /></template>
          <template #cell-usage_antigravity="{ row }"><PlatformCostCell :usage="getPlatformUsage(row.id, 'antigravity')" /></template>

          <template #cell-concurrency="{ row }">
            <UserConcurrencyCell :current="row.current_concurrency ?? 0" :max="row.concurrency" />
          </template>

          <template #cell-status="{ value }">
            <UBadge
              :color="value === 'active' ? 'success' : 'error'"
              variant="subtle"
              :label="value === 'active' ? t('common.active') : t('admin.users.disabled')"
            />
          </template>

          <template #cell-created_at="{ value }">
            <span class="text-muted">{{ formatDateTime(value) }}</span>
          </template>
          <template #cell-last_used_at="{ value }">
            <span class="text-muted">{{ value ? formatDateTime(value) : '-' }}</span>
          </template>
          <template #cell-last_active_at="{ value }">
            <span class="text-muted">{{ value ? formatDateTime(value) : '-' }}</span>
          </template>

          <template #cell-actions="{ row }">
            <div class="flex items-center justify-end gap-0.5" @click.stop>
              <AdminRowAction :label="t('common.edit')" icon="edit" tone="primary" @click="handleEdit(row)" />
              <AdminRowAction
                v-if="row.role !== 'admin'"
                :label="row.status === 'active' ? t('admin.users.disable') : t('admin.users.enable')"
                :icon="row.status === 'active' ? 'ban' : 'checkCircle'"
                :tone="row.status === 'active' ? 'warning' : 'success'"
                @click="handleToggleStatus(row)"
              />
              <UDropdownMenu :items="rowMenuItems(row)" :content="{ align: 'end' }">
                <AdminRowAction :label="t('common.more')" icon="more" />
              </UDropdownMenu>
            </div>
          </template>

          <template #empty>
            <EmptyState
              :title="t('admin.users.noUsersYet')"
              :description="t('admin.users.createFirstUser')"
              :action-text="t('admin.users.createUser')"
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

    <ConfirmDialog :show="showDeleteDialog" :title="t('admin.users.deleteUser')" :message="t('admin.users.deleteConfirm', { email: deletingUser?.email })" :danger="true" @confirm="confirmDelete" @cancel="showDeleteDialog = false" />
    <ConfirmDialog
      :show="bulkDeleteIds.length > 0"
      :title="t('admin.users.bulkDelete.title')"
      :message="t('admin.users.bulkDelete.confirm', { count: bulkDeleteIds.length })"
      :confirm-text="t('common.delete')"
      danger
      @confirm="confirmBulkDelete"
      @cancel="bulkDeleteIds = []"
    />
    <UserCreateModal :show="showCreateModal" @close="showCreateModal = false" @success="loadUsers" />
    <UserEditModal :show="showEditModal" :user="editingUser" @close="closeEditModal" @success="loadUsers" />
    <BulkEditUserModal
      :show="showBulkEditModal"
      :selected-ids="selectedIds"
      @close="showBulkEditModal = false"
      @success="handleBulkLimitsSuccess"
    />
    <UserPlatformQuotaModal
      :show="showPlatformQuotaModal"
      :user="platformQuotaUser"
      @close="closePlatformQuotaModal"
      @success="loadUsers"
    />
    <UserApiKeysModal :show="showApiKeysModal" :user="viewingUser" @close="closeApiKeysModal" />
    <UserAllowedGroupsModal :show="showAllowedGroupsModal" :user="allowedGroupsUser" @close="closeAllowedGroupsModal" @success="loadUsers" />
    <UserBalanceModal :show="showBalanceModal" :user="balanceUser" :operation="balanceOperation" @close="closeBalanceModal" @success="loadUsers" />
    <UserBalanceHistoryModal :show="showBalanceHistoryModal" :user="balanceHistoryUser" @close="closeBalanceHistoryModal" @deposit="handleDepositFromHistory" @withdraw="handleWithdrawFromHistory" />
    <GroupReplaceModal :show="showGroupReplaceModal" :user="groupReplaceUser" :old-group="groupReplaceOldGroup" :all-groups="allGroups" @close="closeGroupReplaceModal" @success="loadUsers" />
    <UserAttributesConfigModal :show="showAttributesModal" @close="handleAttributesModalClose" />
    <UserQuickLookSlideover
      v-model:open="quickLookOpen"
      :user="quickLookUser"
      @edit="fromQuickLook(handleEdit)"
      @deposit="fromQuickLook(handleDeposit)"
      @withdraw="fromQuickLook(handleWithdraw)"
      @keys="fromQuickLook(handleViewApiKeys)"
      @groups="fromQuickLook(handleAllowedGroups)"
      @history="fromQuickLook(handleBalanceHistory)"
    />
  </AppLayout>
</template>

<script setup lang="ts">
import { ref, reactive, computed, onMounted, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { getPersistedPageSize } from '@/composables/usePersistedPageSize'
import { useTableSelection } from '@/composables/useTableSelection'
import { formatDateTime } from '@/utils/format'
import type { DropdownMenuItem } from '@nuxt/ui'
import AdminPageHeader from '@/components/admin/page/AdminPageHeader.vue'
import PageToolbar from '@/components/layout/PageToolbar.vue'
import AdminRowAction from '@/components/admin/page/AdminRowAction.vue'

const { t } = useI18n()
import { adminAPI } from '@/api/admin'
import type { AdminUser, AdminGroup, UserAttributeDefinition } from '@/types'
import type { BatchUserUsageStats } from '@/api/admin/dashboard'
import type { PlatformQuotaItem } from '@/api/admin/users'
import type { Column } from '@/components/common/types'
import type { SelectOption } from '@/components/common/Select.vue'
import AppLayout from '@/components/layout/AppLayout.vue'
import TablePageLayout from '@/components/layout/TablePageLayout.vue'
import DataTable from '@/components/common/DataTable.vue'
import Pagination from '@/components/common/Pagination.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import GroupBadge from '@/components/common/GroupBadge.vue'
import Select from '@/components/common/Select.vue'
import { buildApiKeyGroupFilterOptions } from './apiKeyGroupFilterOptions'
import UserAttributesConfigModal from '@/components/user/UserAttributesConfigModal.vue'
import UserConcurrencyCell from '@/components/user/UserConcurrencyCell.vue'
import PlatformUsageBreakdown from '@/components/user/PlatformUsageBreakdown.vue'
import PlatformCostCell from '@/components/user/PlatformCostCell.vue'
import UserPlatformQuotaCell from '@/components/user/UserPlatformQuotaCell.vue'
import UserCreateModal from '@/components/admin/user/UserCreateModal.vue'
import UserEditModal from '@/components/admin/user/UserEditModal.vue'
import BulkEditUserModal from '@/components/admin/user/BulkEditUserModal.vue'
import UserPlatformQuotaModal from '@/components/admin/user/UserPlatformQuotaModal.vue'
import UserApiKeysModal from '@/components/admin/user/UserApiKeysModal.vue'
import UserAllowedGroupsModal from '@/components/admin/user/UserAllowedGroupsModal.vue'
import UserBalanceModal from '@/components/admin/user/UserBalanceModal.vue'
import UserBalanceHistoryModal from '@/components/admin/user/UserBalanceHistoryModal.vue'
import GroupReplaceModal from '@/components/admin/user/GroupReplaceModal.vue'
import UserQuickLookSlideover from '@/components/admin/user/UserQuickLookSlideover.vue'

const appStore = useAppStore()

// Generate dynamic attribute columns from enabled definitions
const attributeColumns = computed<Column[]>(() =>
  attributeDefinitions.value
    .filter(def => def.enabled)
    .map(def => ({
      key: `attr_${def.id}`,
      label: def.name,
      sortable: false
    }))
)

// Get formatted attribute value for display in table
const getAttributeValue = (userId: number, attrId: number): string => {
  const userAttrs = userAttributeValues.value[userId]
  if (!userAttrs) return '-'
  const value = userAttrs[attrId]
  if (!value) return '-'

  // Find definition for this attribute
  const def = attributeDefinitions.value.find(d => d.id === attrId)
  if (!def) return value

  // Format based on type
  if (def.type === 'multi_select' && value) {
    try {
      const arr = JSON.parse(value)
      if (Array.isArray(arr)) {
        // Map values to labels
        return arr.map(v => {
          const opt = def.options?.find(o => o.value === v)
          return opt?.label || v
        }).join(', ')
      }
    } catch {
      return value
    }
  }

  if (def.type === 'select' && value && def.options) {
    const opt = def.options.find(o => o.value === value)
    return opt?.label || value
  }

  return value
}

// All possible columns (for column settings)
const allColumns = computed<Column[]>(() => [
  { key: 'email', label: t('admin.users.columns.user'), sortable: true },
  { key: 'id', label: t('admin.users.columns.id'), sortable: true },
  { key: 'username', label: t('admin.users.columns.username'), sortable: true },
  { key: 'notes', label: t('admin.users.columns.notes'), sortable: false },
  // Dynamic attribute columns
  ...attributeColumns.value,
  { key: 'role', label: t('admin.users.columns.role'), sortable: true },
  { key: 'groups', label: t('admin.users.columns.groups'), sortable: false },
  { key: 'subscriptions', label: t('admin.users.columns.subscriptions'), sortable: false },
  { key: 'balance', label: t('admin.users.columns.balance'), sortable: true },
  { key: 'balance_platform_quota', label: t('admin.users.columns.balancePlatformQuota'), sortable: false },
  { key: 'usage', label: t('admin.users.columns.usage'), sortable: false },
  { key: 'usage_anthropic', label: t('admin.users.columns.usageAnthropic'), sortable: false },
  { key: 'usage_openai', label: t('admin.users.columns.usageOpenAI'), sortable: false },
  { key: 'usage_gemini', label: t('admin.users.columns.usageGemini'), sortable: false },
  { key: 'usage_antigravity', label: t('admin.users.columns.usageAntigravity'), sortable: false },
  { key: 'concurrency', label: t('admin.users.columns.concurrency'), sortable: true },
  { key: 'status', label: t('admin.users.columns.status'), sortable: true },
  { key: 'last_active_at', label: t('admin.users.columns.lastActive'), sortable: true },
  { key: 'last_used_at', label: t('admin.users.columns.lastUsed'), sortable: true },
  { key: 'created_at', label: t('admin.users.columns.created'), sortable: true },
  { key: 'actions', label: t('admin.users.columns.actions'), sortable: false }
])

// Columns that can be toggled (exclude email and actions which are always visible)
const toggleableColumns = computed(() =>
  allColumns.value.filter(col => col.key !== 'email' && col.key !== 'actions')
)

// Hidden columns (stored in Set - columns NOT in this set are visible)
// This way, new columns are visible by default
const hiddenColumns = reactive<Set<string>>(new Set())

// Default hidden columns (columns hidden by default on first load)
const DEFAULT_HIDDEN_COLUMNS = [
  'notes', 'groups', 'subscriptions', 'usage', 'concurrency',
  'usage_anthropic', 'usage_openai', 'usage_gemini', 'usage_antigravity',
  'balance_platform_quota'
]
const REMOVED_COLUMNS = new Set(['last_login_at'])
// 强制可见列：加载时会被强制移出 hiddenColumns，并在列设置 UI 上 disabled。
// 当前没有列需要强制可见 —— last_active_at 已改为可被用户隐藏。
const FORCED_VISIBLE_COLUMNS = new Set<string>()

// localStorage keys for column settings
const HIDDEN_COLUMNS_KEY = 'user-hidden-columns'
// 列设置 schema 版本号。每次给 DEFAULT_HIDDEN_COLUMNS 新增列时 bump 一次，
// 并在 VERSION_NEW_HIDDEN_COLUMNS 中登记该版本新增的 key。
// 这样老用户升级后这些新列会被自动隐藏一次，而不会影响他们对其它老列的偏好。
const COLUMN_SETTINGS_VERSION_KEY = 'user-column-settings-version'
const COLUMN_SETTINGS_VERSION = 3
const VERSION_NEW_HIDDEN_COLUMNS: Record<number, string[]> = {
  2: ['usage_anthropic', 'usage_openai', 'usage_gemini', 'usage_antigravity'],
  3: ['balance_platform_quota']
}

// Load saved column settings
const loadSavedColumns = () => {
  try {
    const saved = localStorage.getItem(HIDDEN_COLUMNS_KEY)
    if (saved) {
      const parsed = JSON.parse(saved) as string[]
      parsed
        .filter(key => !REMOVED_COLUMNS.has(key) && !FORCED_VISIBLE_COLUMNS.has(key))
        .forEach(key => hiddenColumns.add(key))

      // 老用户升级：把每个未应用过的版本里新增的默认隐藏列自动追加到 hiddenColumns。
      const storedVersion = Number(localStorage.getItem(COLUMN_SETTINGS_VERSION_KEY) ?? '1')
      if (storedVersion < COLUMN_SETTINGS_VERSION) {
        let mutated = false
        for (let v = storedVersion + 1; v <= COLUMN_SETTINGS_VERSION; v++) {
          for (const key of VERSION_NEW_HIDDEN_COLUMNS[v] ?? []) {
            if (REMOVED_COLUMNS.has(key) || FORCED_VISIBLE_COLUMNS.has(key)) continue
            if (!hiddenColumns.has(key)) {
              hiddenColumns.add(key)
              mutated = true
            }
          }
        }
        if (mutated) saveColumnsToStorage()
        else localStorage.setItem(COLUMN_SETTINGS_VERSION_KEY, String(COLUMN_SETTINGS_VERSION))
      }
    } else {
      // Use default hidden columns on first load
      DEFAULT_HIDDEN_COLUMNS.forEach(key => hiddenColumns.add(key))
      localStorage.setItem(COLUMN_SETTINGS_VERSION_KEY, String(COLUMN_SETTINGS_VERSION))
    }
  } catch (e) {
    console.error('Failed to load saved columns:', e)
    DEFAULT_HIDDEN_COLUMNS.forEach(key => hiddenColumns.add(key))
  }
}

// Save column settings to localStorage
const saveColumnsToStorage = () => {
  try {
    localStorage.setItem(HIDDEN_COLUMNS_KEY, JSON.stringify([...hiddenColumns]))
    localStorage.setItem(COLUMN_SETTINGS_VERSION_KEY, String(COLUMN_SETTINGS_VERSION))
  } catch (e) {
    console.error('Failed to save columns:', e)
  }
}

// Toggle column visibility
const isForcedVisibleColumn = (key: string) => FORCED_VISIBLE_COLUMNS.has(key)
const toggleColumn = (key: string) => {
  // 强制可见列(如 last_active_at)在加载时会被恢复成可见，
  // 这里阻止用户在当前会话隐藏它，避免"取消勾选 → 刷新又恢复"的反直觉行为。
  if (FORCED_VISIBLE_COLUMNS.has(key)) return
  const wasHidden = hiddenColumns.has(key)
  if (hiddenColumns.has(key)) {
    hiddenColumns.delete(key)
  } else {
    hiddenColumns.add(key)
  }
  saveColumnsToStorage()
  if (wasHidden && (key === 'usage' || key.startsWith('usage_') || key.startsWith('attr_') || key === 'balance_platform_quota')) {
    refreshCurrentPageSecondaryData()
  }
  if (key === 'subscriptions') {
    loadUsers()
  }
  if (wasHidden && key === 'groups') {
    loadAllGroups()
  }
}

// Check if column is visible (not in hidden set)
const isColumnVisible = (key: string) => !hiddenColumns.has(key)

// "显示列" menu: the page's own column settings as checkbox items
const columnMenuItems = computed<DropdownMenuItem[]>(() =>
  toggleableColumns.value.map((col) => ({
    label: col.label,
    type: 'checkbox' as const,
    checked: isColumnVisible(col.key),
    disabled: isForcedVisibleColumn(col.key),
    onUpdateChecked: () => toggleColumn(col.key),
    onSelect: (event: Event) => event.preventDefault()
  }))
)
// usage 主列或任意 usage_<platform> 子列可见时都需要批量拉取用量数据
// 列 key → 平台名（'usage' 主列汇总所有平台时为 null）
// 显式数组取代 Object.keys()：保证迭代顺序（决定列头排序按钮渲染顺序）
// 不会因 JS 引擎差异或 USAGE_COLUMN_PLATFORMS 属性顺序调整而静默变化。
const USAGE_COLUMN_KEYS: readonly string[] = ['usage', 'usage_anthropic', 'usage_openai', 'usage_gemini', 'usage_antigravity']
const USAGE_COLUMN_PLATFORMS: Record<string, string | null> = {
  usage: null,
  usage_anthropic: 'anthropic',
  usage_openai: 'openai',
  usage_gemini: 'gemini',
  usage_antigravity: 'antigravity'
}
const PLATFORM_USAGE_COLUMNS = USAGE_COLUMN_KEYS.filter((k) => k !== 'usage')
const hasVisibleUsageColumn = computed(
  () => !hiddenColumns.has('usage') || PLATFORM_USAGE_COLUMNS.some((k) => !hiddenColumns.has(k))
)
const hasVisibleGroupsColumn = computed(() => !hiddenColumns.has('groups'))
const hasVisiblePlatformQuotaColumn = computed(() => !hiddenColumns.has('balance_platform_quota'))
const hasVisibleAttributeColumns = computed(() =>
  attributeDefinitions.value.some((def) => def.enabled && !hiddenColumns.has(`attr_${def.id}`))
)

// Filtered columns based on visibility
const columns = computed<Column[]>(() =>
  allColumns.value.filter(col =>
    col.key === 'email' || col.key === 'actions' || !hiddenColumns.has(col.key)
  )
)

const users = ref<AdminUser[]>([])
const loading = ref(false)
const searchQuery = ref('')
const USER_SORT_STORAGE_KEY = 'admin-users-table-sort'
const loadInitialSortState = (): { sort_by: string; sort_order: 'asc' | 'desc' } => {
  const fallback = { sort_by: 'created_at', sort_order: 'desc' as 'asc' | 'desc' }
  const sortable = new Set(['email', 'id', 'username', 'role', 'balance', 'concurrency', 'status', 'last_used_at', 'last_active_at', 'created_at'])
  try {
    const raw = localStorage.getItem(USER_SORT_STORAGE_KEY)
    if (!raw) return fallback
    const parsed = JSON.parse(raw) as { key?: string; order?: string }
    const key = typeof parsed.key === 'string' ? parsed.key : ''
    if (!sortable.has(key)) return fallback
    return {
      sort_by: key,
      sort_order: parsed.order === 'asc' ? 'asc' : 'desc'
    }
  } catch {
    return fallback
  }
}
const sortState = reactive(loadInitialSortState())

// Groups data for the groups column and the existing "authorised group" filter (active only)
const allGroups = ref<AdminGroup[]>([])
const loadAllGroups = async () => {
  if (allGroups.value.length > 0) return
  try {
    allGroups.value = await adminAPI.groups.getAll()
  } catch (e) {
    console.error('Failed to load groups:', e)
  }
}

// Groups for the API Key group filter — includes disabled groups so admins can
// filter users whose keys are still bound to a now-disabled group.
const allGroupsForApiKeyFilter = ref<AdminGroup[]>([])
const loadAllGroupsForApiKeyFilter = async () => {
  if (allGroupsForApiKeyFilter.value.length > 0) return
  try {
    allGroupsForApiKeyFilter.value = await adminAPI.groups.getAllIncludingInactive()
  } catch (e) {
    console.error('Failed to load groups for API key filter:', e)
  }
}
// Resolve user's accessible groups: exclusive groups first, then public groups
const getUserGroups = (user: AdminUser) => {
  const exclusive: AdminGroup[] = []
  const publicGroups: AdminGroup[] = []
  for (const g of allGroups.value) {
    if (g.status !== 'active' || g.subscription_type !== 'standard') continue
    if (g.is_exclusive) {
      if (user.allowed_groups?.includes(g.id)) {
        exclusive.push(g)
      }
    } else {
      publicGroups.push(g)
    }
  }
  return { exclusive, publicGroups }
}

// Group filter options: "All Groups" + active exclusive groups (value = group name for fuzzy match)
const groupFilterOptions = computed(() => {
  const options: { value: string; label: string }[] = [
    { value: '', label: t('admin.users.allAuthorizedGroups') }
  ]
  for (const g of allGroups.value) {
    if (g.status !== 'active' || !g.is_exclusive || g.subscription_type !== 'standard') continue
    options.push({ value: g.name, label: g.name })
  }
  return options
})

// API Key group filter options: "All" + groups partitioned by type (value = group id).
// Uses allGroupsForApiKeyFilter which includes disabled groups.
const apiKeyGroupFilterOptions = computed(() =>
  buildApiKeyGroupFilterOptions(allGroupsForApiKeyFilter.value, {
    all: t('admin.users.allApiKeyGroups'),
    exclusive: t('admin.users.apiKeyGroupExclusive'),
    public: t('admin.users.apiKeyGroupPublic'),
    subscription: t('admin.users.apiKeyGroupSubscription'),
    disabled: t('admin.users.apiKeyGroupDisabled'),
  }) as SelectOption[]
)

// Filter values (role, status, and custom attributes)
const filters = reactive({
  role: '',
  status: '',
  group: '',  // group name for fuzzy match, '' = all
  apiKeyGroup: null as number | null  // group id bound to the user's API keys, null = all
})
const activeAttributeFilters = reactive<Record<number, string>>({})

// Visible filters tracking (which filters are shown in the UI)
// Keys: 'role', 'status', 'attr_${id}'
const visibleFilters = reactive<Set<string>>(new Set())

// localStorage keys
const FILTER_VALUES_KEY = 'user-filter-values'
const VISIBLE_FILTERS_KEY = 'user-visible-filters'

// All filterable attribute definitions (enabled attributes)
const filterableAttributes = computed(() =>
  attributeDefinitions.value.filter(def => def.enabled)
)

// Built-in filter definitions
const builtInFilters = computed(() => [
  { key: 'role', name: t('admin.users.columns.role'), type: 'select' as const },
  { key: 'status', name: t('admin.users.columns.status'), type: 'select' as const },
  { key: 'group', name: t('admin.users.authorizedGroupFilter'), type: 'select' as const },
  { key: 'apiKeyGroup', name: t('admin.users.apiKeyGroupFilter'), type: 'select' as const }
])

// "筛选设置" menu: built-in filters, then custom attribute filters
const filterMenuItems = computed<DropdownMenuItem[][]>(() => {
  const groups: DropdownMenuItem[][] = [
    builtInFilters.value.map((filter) => ({
      label: filter.name,
      type: 'checkbox' as const,
      checked: visibleFilters.has(filter.key),
      onUpdateChecked: () => toggleBuiltInFilter(filter.key),
      onSelect: (event: Event) => event.preventDefault()
    }))
  ]
  if (filterableAttributes.value.length > 0) {
    groups.push(filterableAttributes.value.map((attr) => ({
      label: attr.name,
      type: 'checkbox' as const,
      checked: visibleFilters.has(`attr_${attr.id}`),
      onUpdateChecked: () => toggleAttributeFilter(attr),
      onSelect: (event: Event) => event.preventDefault()
    })))
  }
  return groups
})

// Load saved filters from localStorage
const loadSavedFilters = () => {
  try {
    // Load visible filters
    const savedVisible = localStorage.getItem(VISIBLE_FILTERS_KEY)
    if (savedVisible) {
      const parsed = JSON.parse(savedVisible) as string[]
      parsed.forEach(key => visibleFilters.add(key))
    }
    // Load filter values
    const savedValues = localStorage.getItem(FILTER_VALUES_KEY)
    if (savedValues) {
      const parsed = JSON.parse(savedValues)
      if (parsed.role) filters.role = parsed.role
      if (parsed.status) filters.status = parsed.status
      if (parsed.group) filters.group = parsed.group
      if (typeof parsed.apiKeyGroup === 'number') filters.apiKeyGroup = parsed.apiKeyGroup
      if (parsed.attributes) {
        Object.assign(activeAttributeFilters, parsed.attributes)
      }
    }
  } catch (e) {
    console.error('Failed to load saved filters:', e)
  }
}

// Save filters to localStorage
const saveFiltersToStorage = () => {
  try {
    // Save visible filters
    localStorage.setItem(VISIBLE_FILTERS_KEY, JSON.stringify([...visibleFilters]))
    // Save filter values
    const values = {
      role: filters.role,
      status: filters.status,
      group: filters.group,
      apiKeyGroup: filters.apiKeyGroup,
      attributes: activeAttributeFilters
    }
    localStorage.setItem(FILTER_VALUES_KEY, JSON.stringify(values))
  } catch (e) {
    console.error('Failed to save filters:', e)
  }
}

// Get attribute definition by ID
const getAttributeDefinition = (attrId: number): UserAttributeDefinition | undefined => {
  return attributeDefinitions.value.find(d => d.id === attrId)
}
const usageStats = ref<Record<string, BatchUserUsageStats>>({})
const platformQuotaStats = ref<Record<number, PlatformQuotaItem[]>>({})

const getPlatformUsage = (userId: number, platform: string) =>
  usageStats.value[userId]?.by_platform?.find((p) => p.platform === platform)

// 用量列前端排序：DataTable 工作在 server-side-sort 模式，所有 sortable
// 字段都会触发后端查询，而用量列数据是异步批量拉取后再合并到当前页，
// 因此采用独立的前端排序状态对当前页 users 做本地排序。
// 排序状态独立于后端 sortState 持久化；缺失数据按 0 处理（desc 沉底、asc 置顶）。
type UsageMetric = 'today' | 'total'
type UsageSortState = { key: string; metric: UsageMetric; order: 'asc' | 'desc' } | null
const USAGE_SORT_STORAGE_KEY = 'admin-users-usage-sort'

const loadInitialUsageSort = (): UsageSortState => {
  try {
    const raw = localStorage.getItem(USAGE_SORT_STORAGE_KEY)
    if (!raw) return null
    const parsed = JSON.parse(raw) as Partial<{ key: string; metric: string; order: string }>
    if (!parsed.key || !USAGE_COLUMN_KEYS.includes(parsed.key)) return null
    const metric: UsageMetric = parsed.metric === 'total' ? 'total' : 'today'
    const order: 'asc' | 'desc' = parsed.order === 'asc' ? 'asc' : 'desc'
    return { key: parsed.key, metric, order }
  } catch {
    return null
  }
}
const usageSort = ref<UsageSortState>(loadInitialUsageSort())
const persistUsageSort = () => {
  try {
    if (usageSort.value) {
      localStorage.setItem(USAGE_SORT_STORAGE_KEY, JSON.stringify(usageSort.value))
    } else {
      localStorage.removeItem(USAGE_SORT_STORAGE_KEY)
    }
  } catch (e) {
    console.error('Failed to persist usage sort:', e)
  }
}
const clearUsageSort = () => {
  if (!usageSort.value) return
  usageSort.value = null
  persistUsageSort()
}

const isUsageSortActive = (key: string, metric: UsageMetric) =>
  !!usageSort.value && usageSort.value.key === key && usageSort.value.metric === metric
const getUsageSortOrder = (key: string, metric: UsageMetric): 'asc' | 'desc' | null =>
  isUsageSortActive(key, metric) ? usageSort.value!.order : null

// 三态循环：desc → asc → off。选完即关闭菜单（用户大多希望"选中即应用"，
// 想再切换 order 时重新打开菜单点同一项即可）。
const toggleUsageSort = (key: string, metric: UsageMetric) => {
  const cur = usageSort.value
  if (cur && cur.key === key && cur.metric === metric) {
    usageSort.value = cur.order === 'desc' ? { key, metric, order: 'asc' } : null
  } else {
    usageSort.value = { key, metric, order: 'desc' }
  }
  persistUsageSort()
}

// 列头按钮只开关菜单；首次排序由用户在菜单内选择 metric 触发（默认 desc，详见 toggleUsageSort）。
const usageSortIcon = (key: string) => {
  if (!usageSort.value || usageSort.value.key !== key) return 'i-lucide-arrow-up-down'
  return usageSort.value.order === 'desc' ? 'i-lucide-arrow-down-wide-narrow' : 'i-lucide-arrow-up-narrow-wide'
}

const usageSortMenuItems = (key: string): DropdownMenuItem[][] => [
  (['today', 'total'] as const).map((metric) => {
    const order = getUsageSortOrder(key, metric)
    return {
      label: metric === 'today' ? t('admin.users.today') : t('admin.users.total'),
      slot: `usage-${key}-${metric}`,
      color: order ? ('primary' as const) : undefined,
      trailingIcon: order === 'desc' ? 'i-lucide-arrow-down-wide-narrow' : order === 'asc' ? 'i-lucide-arrow-up-narrow-wide' : undefined,
      onSelect: () => toggleUsageSort(key, metric)
    }
  }),
  [{ type: 'label' as const, label: t('admin.users.sortCurrentPageOnly') }]
]

const getUsageValue = (userId: number, key: string, metric: UsageMetric): number => {
  const stats = usageStats.value[userId]
  if (!stats) return 0
  const platform = USAGE_COLUMN_PLATFORMS[key]
  if (platform === null) {
    return metric === 'today' ? stats.today_actual_cost ?? 0 : stats.total_actual_cost ?? 0
  }
  const p = stats.by_platform?.find((x) => x.platform === platform)
  if (!p) return 0
  return metric === 'today' ? p.today_actual_cost ?? 0 : p.total_actual_cost ?? 0
}

// 在 server-side 排序结果之上叠加用量列的本地排序；无 usageSort 时直接透传原数组。
// 稳定排序：等值按原 index 保序，避免拉取新用量数据时表行抖动。
const sortedUsers = computed(() => {
  const s = usageSort.value
  if (!s) return users.value
  return [...users.value]
    .map((row, index) => ({ row, index }))
    .sort((a, b) => {
      const av = getUsageValue(a.row.id, s.key, s.metric)
      const bv = getUsageValue(b.row.id, s.key, s.metric)
      if (av !== bv) return s.order === 'asc' ? av - bv : bv - av
      return a.index - b.index
    })
    .map((x) => x.row)
})

const {
  selectedIds,
  selectedCount,
  setSelectedIds,
  clear: clearSelection,
  removeMany: removeSelectedIds
} = useTableSelection<AdminUser>({
  rows: sortedUsers,
  getId: (user) => user.id
})

const handleSelectedKeysUpdate = (keys: Array<string | number>) => {
  setSelectedIds(keys.filter((key): key is number => typeof key === 'number'))
}

const getUserSelectionLabel = (user: AdminUser) =>
  t('admin.users.bulkLimits.selectUser', { email: user.email })

// User attribute definitions and values
const attributeDefinitions = ref<UserAttributeDefinition[]>([])
const userAttributeValues = ref<Record<number, Record<number, string>>>({})
const pagination = reactive({
  page: 1,
  page_size: getPersistedPageSize(),
  total: 0,
  pages: 0
})

const showCreateModal = ref(false)
const showEditModal = ref(false)
const showBulkEditModal = ref(false)
const showDeleteDialog = ref(false)
const bulkDeleteIds = ref<number[]>([])
const bulkDeleting = ref(false)
const showApiKeysModal = ref(false)
const showAttributesModal = ref(false)
const showPlatformQuotaModal = ref(false)
const editingUser = ref<AdminUser | null>(null)
const deletingUser = ref<AdminUser | null>(null)
const viewingUser = ref<AdminUser | null>(null)
const platformQuotaUser = ref<AdminUser | null>(null)

const handlePlatformQuota = (user: AdminUser) => {
  platformQuotaUser.value = user
  showPlatformQuotaModal.value = true
}

const closePlatformQuotaModal = () => {
  showPlatformQuotaModal.value = false
  platformQuotaUser.value = null
}
let abortController: AbortController | null = null
let secondaryDataSeq = 0

const loadUsersSecondaryData = async (
  userIds: number[],
  signal?: AbortSignal,
  expectedSeq?: number
) => {
  if (userIds.length === 0) return

  const tasks: Promise<void>[] = []

  if (hasVisibleUsageColumn.value) {
    tasks.push(
      (async () => {
        try {
          const usageResponse = await adminAPI.dashboard.getBatchUsersUsage(userIds)
          if (signal?.aborted) return
          if (typeof expectedSeq === 'number' && expectedSeq !== secondaryDataSeq) return
          usageStats.value = usageResponse.stats
        } catch (e) {
          if (signal?.aborted) return
          console.error('Failed to load usage stats:', e)
        }
      })()
    )
  }

  if (attributeDefinitions.value.length > 0 && hasVisibleAttributeColumns.value) {
    tasks.push(
      (async () => {
        try {
          const attrResponse = await adminAPI.userAttributes.getBatchUserAttributes(userIds)
          if (signal?.aborted) return
          if (typeof expectedSeq === 'number' && expectedSeq !== secondaryDataSeq) return
          userAttributeValues.value = attrResponse.attributes
        } catch (e) {
          if (signal?.aborted) return
          console.error('Failed to load user attribute values:', e)
        }
      })()
    )
  }

  if (hasVisiblePlatformQuotaColumn.value) {
    tasks.push(
      (async () => {
        try {
          // 无批量端点：对当前页用户逐个拉取，分块并发（每批 6），批间检查中止条件，避免大 pageSize 时请求洪峰
          const CHUNK = 6
          for (let i = 0; i < userIds.length; i += CHUNK) {
            if (signal?.aborted) return
            if (typeof expectedSeq === 'number' && expectedSeq !== secondaryDataSeq) return
            const chunk = userIds.slice(i, i + CHUNK)
            const results = await Promise.allSettled(
              chunk.map((id) => adminAPI.users.getPlatformQuotas(id))
            )
            if (signal?.aborted) return
            if (typeof expectedSeq === 'number' && expectedSeq !== secondaryDataSeq) return
            const merged = { ...platformQuotaStats.value }
            results.forEach((r, idx) => {
              if (r.status === 'fulfilled') {
                merged[chunk[idx]] = r.value.platform_quotas || []
              }
            })
            platformQuotaStats.value = merged
          }
        } catch (e) {
          if (signal?.aborted) return
          console.error('Failed to load platform quotas:', e)
        }
      })()
    )
  }

  if (tasks.length > 0) {
    await Promise.allSettled(tasks)
  }
}

const refreshCurrentPageSecondaryData = () => {
  const userIds = users.value.map((u) => u.id)
  if (userIds.length === 0) return
  const seq = ++secondaryDataSeq
  void loadUsersSecondaryData(userIds, undefined, seq)
}

// Row "…" menu (UDropdownMenu handles positioning, outside click, Escape and scroll)
const rowMenuItems = (user: AdminUser): DropdownMenuItem[][] => {
  const groups: DropdownMenuItem[][] = [
    [
      { label: t('admin.users.apiKeys'), icon: 'i-lucide-key-round', onSelect: () => handleViewApiKeys(user) },
      { label: t('admin.users.groups'), icon: 'i-lucide-users', onSelect: () => handleAllowedGroups(user) }
    ],
    [
      { label: t('admin.users.deposit'), icon: 'i-lucide-circle-plus', onSelect: () => handleDeposit(user) },
      { label: t('admin.users.withdraw'), icon: 'i-lucide-circle-minus', onSelect: () => handleWithdraw(user) },
      { label: t('admin.users.platformQuota.menuItem'), icon: 'i-lucide-chart-column', onSelect: () => handlePlatformQuota(user) },
      { label: t('admin.users.balanceHistory'), icon: 'i-lucide-wallet', onSelect: () => handleBalanceHistory(user) }
    ]
  ]
  if (user.role !== 'admin') {
    groups.push([{ label: t('common.delete'), icon: 'i-lucide-trash', color: 'error', onSelect: () => handleDelete(user) }])
  }
  return groups
}

// Exclusive groups of a row: picking one opens the replace-group dialog
const exclusiveGroupMenuItems = (user: AdminUser): DropdownMenuItem[][] => [
  [{ type: 'label', label: t('admin.users.clickToReplace') }],
  getUserGroups(user).exclusive.map((g) => ({
    label: g.name,
    icon: 'i-lucide-arrow-left-right',
    onSelect: () => openGroupReplace(user, g)
  }))
]

// Allowed groups modal state
const showAllowedGroupsModal = ref(false)
const allowedGroupsUser = ref<AdminUser | null>(null)

// Group replace modal state
const showGroupReplaceModal = ref(false)
const groupReplaceUser = ref<AdminUser | null>(null)
const groupReplaceOldGroup = ref<{ id: number; name: string } | null>(null)

// Balance (Deposit/Withdraw) modal state
const showBalanceModal = ref(false)
const balanceUser = ref<AdminUser | null>(null)
const balanceOperation = ref<'add' | 'subtract'>('add')

// Balance History modal state
const showBalanceHistoryModal = ref(false)
const balanceHistoryUser = ref<AdminUser | null>(null)

// 计算剩余天数
const getDaysRemaining = (expiresAt: string): number => {
  const now = new Date()
  const expires = new Date(expiresAt)
  const diffMs = expires.getTime() - now.getTime()
  return Math.ceil(diffMs / (1000 * 60 * 60 * 24))
}

const loadAttributeDefinitions = async () => {
  try {
    attributeDefinitions.value = await adminAPI.userAttributes.listEnabledDefinitions()
  } catch (e) {
    console.error('Failed to load attribute definitions:', e)
  }
}

// Handle attributes modal close - reload definitions and users
const handleAttributesModalClose = async () => {
  showAttributesModal.value = false
  await loadAttributeDefinitions()
  loadUsers()
}

const loadUsers = async () => {
  abortController?.abort()
  const currentAbortController = new AbortController()
  abortController = currentAbortController
  const { signal } = currentAbortController
  loading.value = true
  try {
    // Build attribute filters from active filters
    const attrFilters: Record<number, string> = {}
    for (const [attrId, value] of Object.entries(activeAttributeFilters)) {
      if (value) {
        attrFilters[Number(attrId)] = value
      }
    }

    const response = await adminAPI.users.list(
      pagination.page,
      pagination.page_size,
      {
        role: filters.role as any,
        status: filters.status as any,
        search: searchQuery.value || undefined,
        group_name: filters.group || undefined,
        api_key_group_id: filters.apiKeyGroup ?? undefined,
        attributes: Object.keys(attrFilters).length > 0 ? attrFilters : undefined,
        // 始终请求 subscriptions：列隐藏时仍需用于 UserPlatformQuotaModal 的 active-subscription 警示 banner
        include_subscriptions: true,
        sort_by: sortState.sort_by,
        sort_order: sortState.sort_order
      },
      { signal }
    )
    if (signal.aborted) {
      return
    }
    users.value = response.items
    pagination.total = response.total
    pagination.pages = response.pages
    usageStats.value = {}
    userAttributeValues.value = {}
    platformQuotaStats.value = {}

    // Defer heavy secondary data so table can render first.
    if (response.items.length > 0) {
      const userIds = response.items.map((u) => u.id)
      const seq = ++secondaryDataSeq
      window.setTimeout(() => {
        if (signal.aborted || seq !== secondaryDataSeq) return
        void loadUsersSecondaryData(userIds, signal, seq)
      }, 50)
    }
  } catch (error: any) {
    const errorInfo = error as { name?: string; code?: string }
    if (errorInfo?.name === 'AbortError' || errorInfo?.name === 'CanceledError' || errorInfo?.code === 'ERR_CANCELED') {
      return
    }
    const message = error.response?.data?.detail || error.message || t('admin.users.failedToLoad')
    appStore.showError(message)
    console.error('Error loading users:', error)
  } finally {
    if (abortController === currentAbortController) {
      loading.value = false
    }
  }
}

const handleBulkLimitsSuccess = async () => {
  clearSelection()
  await loadUsers()
}

let searchTimeout: ReturnType<typeof setTimeout>
const handleSearch = () => {
  clearTimeout(searchTimeout)
  searchTimeout = setTimeout(() => {
    pagination.page = 1
    loadUsers()
  }, 300)
}

const handlePageChange = (page: number) => {
  // 确保页码在有效范围内
  const validPage = Math.max(1, Math.min(page, pagination.pages || 1))
  pagination.page = validPage
  loadUsers()
}

const handlePageSizeChange = (pageSize: number) => {
  pagination.page_size = pageSize
  pagination.page = 1
  loadUsers()
}

const handleSort = (key: string, order: 'asc' | 'desc') => {
  clearUsageSort()
  sortState.sort_by = key
  sortState.sort_order = order
  pagination.page = 1
  loadUsers()
}

// Filter helpers
const getAttributeDefinitionName = (attrId: number): string => {
  const def = attributeDefinitions.value.find(d => d.id === attrId)
  return def?.name || String(attrId)
}

// Toggle a built-in filter (role/status)
const toggleBuiltInFilter = (key: string) => {
  if (visibleFilters.has(key)) {
    visibleFilters.delete(key)
    if (key === 'role') filters.role = ''
    if (key === 'status') filters.status = ''
    if (key === 'group') filters.group = ''
    if (key === 'apiKeyGroup') filters.apiKeyGroup = null
  } else {
    visibleFilters.add(key)
    if (key === 'group') loadAllGroups()
    if (key === 'apiKeyGroup') loadAllGroupsForApiKeyFilter()
  }
  saveFiltersToStorage()
  pagination.page = 1
  loadUsers()
}

// Toggle a custom attribute filter
const toggleAttributeFilter = (attr: UserAttributeDefinition) => {
  const key = `attr_${attr.id}`
  if (visibleFilters.has(key)) {
    visibleFilters.delete(key)
    delete activeAttributeFilters[attr.id]
  } else {
    visibleFilters.add(key)
    activeAttributeFilters[attr.id] = ''
  }
  saveFiltersToStorage()
  pagination.page = 1
  loadUsers()
}

const updateAttributeFilter = (attrId: number, value: string) => {
  activeAttributeFilters[attrId] = value
}

// Apply filter and save to localStorage
const applyFilter = () => {
  saveFiltersToStorage()
  loadUsers()
}

const handleEdit = (user: AdminUser) => {
  editingUser.value = user
  showEditModal.value = true
}

const closeEditModal = () => {
  showEditModal.value = false
  editingUser.value = null
}

const handleToggleStatus = async (user: AdminUser) => {
  const newStatus = user.status === 'active' ? 'disabled' : 'active'
  try {
    await adminAPI.users.toggleStatus(user.id, newStatus)
    appStore.showSuccess(
      newStatus === 'active' ? t('admin.users.userEnabled') : t('admin.users.userDisabled')
    )
    loadUsers()
  } catch (error: any) {
    appStore.showError(error.response?.data?.detail || t('admin.users.failedToToggle'))
    console.error('Error toggling user status:', error)
  }
}

const handleViewApiKeys = (user: AdminUser) => {
  viewingUser.value = user
  showApiKeysModal.value = true
}

const closeApiKeysModal = () => {
  showApiKeysModal.value = false
  viewingUser.value = null
}

const handleAllowedGroups = (user: AdminUser) => {
  allowedGroupsUser.value = user
  showAllowedGroupsModal.value = true
}

const closeAllowedGroupsModal = () => {
  showAllowedGroupsModal.value = false
  allowedGroupsUser.value = null
}

const openGroupReplace = (user: AdminUser, group: { id: number; name: string }) => {
  groupReplaceUser.value = user
  groupReplaceOldGroup.value = group
  showGroupReplaceModal.value = true
}

const closeGroupReplaceModal = () => {
  showGroupReplaceModal.value = false
  groupReplaceUser.value = null
  groupReplaceOldGroup.value = null
}

const handleDelete = (user: AdminUser) => {
  deletingUser.value = user
  showDeleteDialog.value = true
}

const confirmDelete = async () => {
  if (!deletingUser.value) return
  try {
    await adminAPI.users.delete(deletingUser.value.id)
    appStore.showSuccess(t('common.success'))
    showDeleteDialog.value = false
    deletingUser.value = null
    loadUsers()
  } catch (error: any) {
    appStore.showError(error.response?.data?.detail || t('admin.users.failedToDelete'))
    console.error('Error deleting user:', error)
  }
}

const confirmBulkDelete = async () => {
  const ids = bulkDeleteIds.value
  bulkDeleteIds.value = []
  bulkDeleting.value = true
  const deletedIds: number[] = []
  for (const id of ids) {
    try {
      await adminAPI.users.delete(id)
      deletedIds.push(id)
    } catch (error) {
      console.error('Error deleting user:', error)
    }
  }
  removeSelectedIds(deletedIds)
  if (deletedIds.length > 0) {
    appStore.showSuccess(t('admin.users.bulkDelete.success', { count: deletedIds.length }))
    pagination.page = 1
  }
  const failed = ids.length - deletedIds.length
  if (failed > 0) appStore.showError(t('admin.users.bulkDelete.failed', { count: failed }))
  await loadUsers()
  bulkDeleting.value = false
}

const handleDeposit = (user: AdminUser) => {
  balanceUser.value = user
  balanceOperation.value = 'add'
  showBalanceModal.value = true
}

const handleWithdraw = (user: AdminUser) => {
  balanceUser.value = user
  balanceOperation.value = 'subtract'
  showBalanceModal.value = true
}

const closeBalanceModal = () => {
  showBalanceModal.value = false
  balanceUser.value = null
}

const handleBalanceHistory = (user: AdminUser) => {
  balanceHistoryUser.value = user
  showBalanceHistoryModal.value = true
}

const closeBalanceHistoryModal = () => {
  showBalanceHistoryModal.value = false
  balanceHistoryUser.value = null
}

// Handle deposit from balance history modal
const handleDepositFromHistory = () => {
  if (balanceHistoryUser.value) {
    handleDeposit(balanceHistoryUser.value)
  }
}

// Handle withdraw from balance history modal
const handleWithdrawFromHistory = () => {
  if (balanceHistoryUser.value) {
    handleWithdraw(balanceHistoryUser.value)
  }
}

// Row click: quick look slideover; its buttons close it and open the list's own dialogs
const quickLookOpen = ref(false)
const quickLookUser = ref<AdminUser | null>(null)
const openQuickLook = (user: AdminUser) => {
  quickLookUser.value = user
  quickLookOpen.value = true
}
const fromQuickLook = (action: (user: AdminUser) => void) => {
  const user = quickLookUser.value
  quickLookOpen.value = false
  if (user) action(user)
}

onMounted(async () => {
  await loadAttributeDefinitions()
  loadSavedFilters()
  loadSavedColumns()
  loadUsers()
  if (hasVisibleGroupsColumn.value || visibleFilters.has('group')) {
    loadAllGroups()
  }
  if (visibleFilters.has('apiKeyGroup')) {
    loadAllGroupsForApiKeyFilter()
  }
})

onUnmounted(() => {
  clearTimeout(searchTimeout)
  abortController?.abort()
})
</script>
