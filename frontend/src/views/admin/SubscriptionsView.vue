<template>
  <AppLayout>
    <TablePageLayout>
      <template #filters>
        <AdminPageHeader
          class="mb-4"
          :title="t('admin.subscriptions.title')"
          :description="t('admin.subscriptions.description')"
        >
          <template #actions>
            <UButton
              icon="i-lucide-plus"
              :label="t('admin.subscriptions.assignSubscription')"
              @click="showAssignModal = true"
            />
          </template>
        </AdminPageHeader>
        <PageToolbar>
          <!-- Fuzzy user search (deleted users included) -> user_id filter -->
          <div class="relative w-96 max-w-full sm:w-72" data-filter-user-search>
            <UInput
              v-model="filterUserKeyword"
              icon="i-lucide-search"
              class="w-full"
              :placeholder="t('admin.users.searchUsers')"
              @update:model-value="debounceSearchFilterUsers"
              @focus="showFilterUserDropdown = true"
            >
              <template v-if="selectedFilterUser" #trailing>
                <UButton
                  icon="i-lucide-x"
                  color="neutral"
                  variant="link"
                  size="sm"
                  :title="t('common.clear')"
                  :aria-label="t('common.clear')"
                  @click="clearFilterUser"
                />
              </template>
            </UInput>
            <div
              v-if="showFilterUserDropdown && (filterUserResults.length > 0 || filterUserKeyword)"
              class="absolute z-50 mt-1 max-h-60 w-full overflow-auto rounded-md bg-default p-1 shadow-lg ring ring-default"
            >
              <p v-if="filterUserLoading" class="px-2 py-1.5 text-sm text-muted">{{ t('common.loading') }}</p>
              <p v-else-if="filterUserResults.length === 0 && filterUserKeyword" class="px-2 py-1.5 text-sm text-muted">
                {{ t('common.noOptionsFound') }}
              </p>
              <UButton
                v-for="user in filterUserResults"
                :key="user.id"
                color="neutral"
                variant="ghost"
                block
                class="justify-start font-normal"
                @click="selectFilterUser(user)"
              >
                <span class="truncate text-highlighted">{{ user.email }}</span>
                <span class="text-xs text-muted">#{{ user.id }}</span>
              </UButton>
            </div>
          </div>
          <div class="w-96 max-w-full sm:w-40">
            <Select v-model="filters.status" :options="statusOptions" :placeholder="t('admin.subscriptions.allStatus')" @change="applyFilters" />
          </div>
          <div class="w-96 max-w-full sm:w-48">
            <Select v-model="filters.group_id" :options="groupOptions" :placeholder="t('admin.subscriptions.allGroups')" @change="applyFilters" />
          </div>
          <div class="w-96 max-w-full sm:w-40">
            <Select v-model="filters.platform" :options="platformFilterOptions" :placeholder="t('admin.subscriptions.allPlatforms')" @change="applyFilters" />
          </div>

          <template #right>
            <UButton
              icon="i-lucide-refresh-cw"
              color="neutral"
              variant="outline"
              :loading="loading"
              :title="t('common.refresh')"
              :aria-label="t('common.refresh')"
              @click="loadSubscriptions"
            />
            <UDropdownMenu :items="columnMenuItems" :content="{ align: 'end' }">
              <UButton
                :label="t('admin.subscriptions.displayColumns')"
                trailing-icon="i-lucide-settings-2"
                color="neutral"
                variant="outline"
                :title="t('admin.subscriptions.displayColumns')"
                :aria-label="t('admin.subscriptions.displayColumns')"
                :ui="{ label: 'hidden md:inline' }"
              />
            </UDropdownMenu>
            <UButton
              icon="i-lucide-circle-help"
              color="neutral"
              variant="outline"
              :title="t('admin.subscriptions.guide.showGuide')"
              :aria-label="t('admin.subscriptions.guide.showGuide')"
              @click="showGuideModal = true"
            />
          </template>
        </PageToolbar>
      </template>

      <!-- Subscriptions Table -->
      <template #table>
        <DataTable
          :columns="columns"
          :data="subscriptions"
          :loading="loading"
          :server-side-sort="true"
          default-sort-key="created_at"
          default-sort-order="desc"
          @sort="handleSort"
        >
          <template #cell-user="{ row }">
            <div class="flex items-center gap-2.5">
              <UAvatar
                size="sm"
                :text="userColumnMode === 'email'
                  ? (row.user?.email?.charAt(0).toUpperCase() || '?')
                  : (row.user?.username?.charAt(0).toUpperCase() || '?')"
              />
              <RouterLink
                :to="{ path: '/admin/usage', query: { user_id: row.user_id } }"
                class="rounded-sm font-medium text-highlighted hover:underline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary"
              >
                {{ userColumnMode === 'email'
                  ? (row.user?.email || t('admin.redeem.userPrefix', { id: row.user_id }))
                  : (row.user?.username || t('admin.redeem.userPrefix', { id: row.user_id }))
                }}
              </RouterLink>
            </div>
          </template>

          <template #cell-group="{ row }">
            <GroupBadge
              v-if="row.group"
              :name="row.group.name"
              :platform="row.group.platform"
              :subscription-type="row.group.subscription_type"
              :rate-multiplier="row.group.rate_multiplier"
              :show-rate="false"
            />
            <span v-else class="text-sm text-dimmed">-</span>
          </template>

          <template #cell-usage="{ row }">
            <div class="min-w-[280px] space-y-2">
              <!-- Daily Usage -->
              <div v-if="row.group?.daily_limit_usd" class="space-y-0.5">
                <div class="flex items-center gap-2">
                  <span class="w-10 shrink-0 text-xs text-muted">{{ t('admin.subscriptions.daily') }}</span>
                  <UProgress
                    size="sm"
                    class="flex-1"
                    :model-value="getProgressPercent(row.daily_usage_usd, row.group?.daily_limit_usd)"
                    :color="getProgressColor(row.daily_usage_usd, row.group?.daily_limit_usd)"
                  />
                  <span class="whitespace-nowrap text-xs tabular-nums text-default">
                    ${{ row.daily_usage_usd?.toFixed(2) || '0.00' }}
                    <span class="text-dimmed">/</span>
                    ${{ row.group?.daily_limit_usd?.toFixed(2) }}
                  </span>
                </div>
                <div v-if="row.daily_window_start" class="flex items-center gap-1 pl-12 text-[11px] text-muted">
                  <UIcon name="i-lucide-clock" class="size-3" />
                  <span>{{ formatDailyUsageWindow(row) }}</span>
                </div>
              </div>

              <!-- Weekly Usage -->
              <div v-if="row.group?.weekly_limit_usd" class="space-y-0.5">
                <div class="flex items-center gap-2">
                  <span class="w-10 shrink-0 text-xs text-muted">{{ t('admin.subscriptions.weekly') }}</span>
                  <UProgress
                    size="sm"
                    class="flex-1"
                    :model-value="getProgressPercent(row.weekly_usage_usd, row.group?.weekly_limit_usd)"
                    :color="getProgressColor(row.weekly_usage_usd, row.group?.weekly_limit_usd)"
                  />
                  <span class="whitespace-nowrap text-xs tabular-nums text-default">
                    ${{ row.weekly_usage_usd?.toFixed(2) || '0.00' }}
                    <span class="text-dimmed">/</span>
                    ${{ row.group?.weekly_limit_usd?.toFixed(2) }}
                  </span>
                </div>
                <div v-if="row.weekly_window_start" class="flex items-center gap-1 pl-12 text-[11px] text-muted">
                  <UIcon name="i-lucide-clock" class="size-3" />
                  <span>{{ formatResetTime(row.weekly_window_start, 'weekly') }}</span>
                </div>
              </div>

              <!-- Monthly Usage -->
              <div v-if="row.group?.monthly_limit_usd" class="space-y-0.5">
                <div class="flex items-center gap-2">
                  <span class="w-10 shrink-0 text-xs text-muted">{{ t('admin.subscriptions.monthly') }}</span>
                  <UProgress
                    size="sm"
                    class="flex-1"
                    :model-value="getProgressPercent(row.monthly_usage_usd, row.group?.monthly_limit_usd)"
                    :color="getProgressColor(row.monthly_usage_usd, row.group?.monthly_limit_usd)"
                  />
                  <span class="whitespace-nowrap text-xs tabular-nums text-default">
                    ${{ row.monthly_usage_usd?.toFixed(2) || '0.00' }}
                    <span class="text-dimmed">/</span>
                    ${{ row.group?.monthly_limit_usd?.toFixed(2) }}
                  </span>
                </div>
                <div v-if="row.monthly_window_start" class="flex items-center gap-1 pl-12 text-[11px] text-muted">
                  <UIcon name="i-lucide-clock" class="size-3" />
                  <span>{{ formatResetTime(row.monthly_window_start, 'monthly') }}</span>
                </div>
              </div>

              <!-- No Limits - Unlimited badge -->
              <UBadge
                v-if="
                  !row.group?.daily_limit_usd &&
                  !row.group?.weekly_limit_usd &&
                  !row.group?.monthly_limit_usd
                "
                color="success"
                variant="subtle"
                icon="i-lucide-infinity"
                :label="t('admin.subscriptions.unlimited')"
              />
            </div>
          </template>

          <template #cell-expires_at="{ value }">
            <div v-if="value">
              <span class="text-sm" :class="isExpiringSoon(value) ? 'text-warning' : 'text-default'">
                {{ formatDateTimeToMinute(value) }}
              </span>
              <template
                v-for="remainingExpiry in [formatRemainingExpiry(value)]"
                :key="remainingExpiry ?? 'expired'"
              >
                <div v-if="remainingExpiry" class="text-xs text-muted">
                  {{ remainingExpiry }}
                </div>
              </template>
            </div>
            <span v-else class="text-sm text-muted">{{
              t('admin.subscriptions.noExpiration')
            }}</span>
          </template>

          <template #cell-status="{ value }">
            <UBadge
              variant="subtle"
              :color="value === 'active' ? 'success' : value === 'expired' ? 'warning' : 'error'"
              :label="t(`admin.subscriptions.status.${value}`)"
            />
          </template>

          <template #cell-actions="{ row }">
            <div class="flex items-center gap-0.5">
              <AdminRowAction v-if="row.status === 'active' || row.status === 'expired'" @click="handleExtend(row)" :label="t('admin.subscriptions.adjust')" icon="calendar" />
              <UDropdownMenu v-if="row.status === 'active'" :items="rowMenuItems(row)" :content="{ align: 'end' }">
                <UButton
                  icon="i-lucide-ellipsis-vertical"
                  color="neutral"
                  variant="ghost"
                  size="sm"
                  :title="t('common.more')"
                  :aria-label="t('common.more')"
                />
              </UDropdownMenu>
              <AdminRowAction v-if="row.status === 'revoked'" @click="handleRestore(row)" :label="t('admin.subscriptions.restore')" icon="refresh" tone="success" />
            </div>
          </template>

          <template #empty>
            <EmptyState
              :title="t('admin.subscriptions.noSubscriptionsYet')"
              :description="t('admin.subscriptions.assignFirstSubscription')"
              :action-text="t('admin.subscriptions.assignSubscription')"
              @action="showAssignModal = true"
            />
          </template>
        </DataTable>
      </template>

      <!-- Pagination -->
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

    <!-- Assign Subscription Modal -->
    <BaseDialog
      :show="showAssignModal"
      :title="t('admin.subscriptions.assignSubscription')"
      width="normal"
      @close="closeAssignModal"
    >
      <UForm id="assign-subscription-form" class="space-y-4" @submit="handleAssignSubscription">
        <UFormField :label="t('admin.subscriptions.form.user')">
          <div class="relative" data-assign-user-search>
            <UInput
              v-model="userSearchKeyword"
              class="w-full"
              :placeholder="t('admin.usage.searchUserPlaceholder')"
              @update:model-value="debounceSearchUsers"
              @focus="showUserDropdown = true"
            >
              <template v-if="selectedUser" #trailing>
                <UButton
                  icon="i-lucide-x"
                  color="neutral"
                  variant="link"
                  size="sm"
                  :title="t('common.clear')"
                  :aria-label="t('common.clear')"
                  @click="clearUserSelection"
                />
              </template>
            </UInput>
            <div
              v-if="showUserDropdown && (userSearchResults.length > 0 || userSearchKeyword)"
              class="absolute z-50 mt-1 max-h-60 w-full overflow-auto rounded-md bg-default p-1 shadow-lg ring ring-default"
            >
              <p v-if="userSearchLoading" class="px-2 py-1.5 text-sm text-muted">{{ t('common.loading') }}</p>
              <p v-else-if="userSearchResults.length === 0 && userSearchKeyword" class="px-2 py-1.5 text-sm text-muted">
                {{ t('common.noOptionsFound') }}
              </p>
              <UButton
                v-for="user in userSearchResults"
                :key="user.id"
                color="neutral"
                variant="ghost"
                block
                class="justify-start font-normal"
                @click="selectUser(user)"
              >
                <span class="truncate text-highlighted">{{ user.email }}</span>
                <span class="text-xs text-muted">#{{ user.id }}</span>
              </UButton>
            </div>
          </div>
        </UFormField>
        <UFormField :label="t('admin.subscriptions.form.group')" :help="t('admin.subscriptions.groupHint')">
          <Select
            v-model="assignForm.group_id"
            :options="subscriptionGroupOptions"
            :placeholder="t('admin.subscriptions.selectGroup')"
          >
            <template #selected="{ option }">
              <GroupBadge
                v-if="option"
                :name="(option as unknown as GroupOption).label"
                :platform="(option as unknown as GroupOption).platform"
                :subscription-type="(option as unknown as GroupOption).subscriptionType"
                :rate-multiplier="(option as unknown as GroupOption).rate"
              />
              <span v-else class="text-dimmed">{{ t('admin.subscriptions.selectGroup') }}</span>
            </template>
            <template #option="{ option, selected }">
              <GroupOptionItem
                :name="(option as unknown as GroupOption).label"
                :platform="(option as unknown as GroupOption).platform"
                :subscription-type="(option as unknown as GroupOption).subscriptionType"
                :rate-multiplier="(option as unknown as GroupOption).rate"
                :description="(option as unknown as GroupOption).description"
                :selected="selected"
              />
            </template>
          </Select>
        </UFormField>
        <UFormField :label="t('admin.subscriptions.form.validityDays')" :help="t('admin.subscriptions.validityHint')">
          <UInput v-model.number="assignForm.validity_days" type="number" min="1" class="w-full" />
        </UFormField>
      </UForm>
      <template #footer>
        <div class="flex justify-end gap-2">
          <UButton color="neutral" variant="subtle" :label="t('common.cancel')" @click="closeAssignModal" />
          <UButton
            type="submit"
            form="assign-subscription-form"
            :loading="submitting"
            :label="submitting ? t('admin.subscriptions.assigning') : t('admin.subscriptions.assign')"
          />
        </div>
      </template>
    </BaseDialog>

    <!-- Adjust Subscription Modal -->
    <BaseDialog
      :show="showExtendModal"
      :title="t('admin.subscriptions.adjustSubscription')"
      width="narrow"
      @close="closeExtendModal"
    >
      <UForm
        v-if="extendingSubscription"
        id="extend-subscription-form"
        class="space-y-4"
        @submit="handleExtendSubscription"
      >
        <div class="space-y-1 rounded-md bg-elevated/50 px-3 py-2.5 text-sm text-muted">
          <p>
            {{ t('admin.subscriptions.adjustingFor') }}
            <span class="font-medium text-highlighted">{{ extendingSubscription.user?.email }}</span>
          </p>
          <p>
            {{ t('admin.subscriptions.currentExpiration') }}:
            <span class="font-medium text-highlighted">
              {{
                extendingSubscription.expires_at
                  ? formatDateTimeToMinute(extendingSubscription.expires_at)
                  : t('admin.subscriptions.noExpiration')
              }}
            </span>
          </p>
          <p v-if="extendingSubscription.expires_at">
            {{ t('admin.subscriptions.remainingDays') }}:
            <span class="font-medium text-highlighted">
              {{ getDaysRemaining(extendingSubscription.expires_at) ?? 0 }}
            </span>
          </p>
        </div>
        <UFormField :label="t('admin.subscriptions.form.adjustDays')" :help="t('admin.subscriptions.adjustHint')" required>
          <UInput
            v-model.number="extendForm.days"
            type="number"
            required
            class="w-full"
            :placeholder="t('admin.subscriptions.adjustDaysPlaceholder')"
          />
        </UFormField>
      </UForm>
      <template #footer>
        <div v-if="extendingSubscription" class="flex justify-end gap-2">
          <UButton color="neutral" variant="subtle" :label="t('common.cancel')" @click="closeExtendModal" />
          <UButton
            type="submit"
            form="extend-subscription-form"
            :loading="submitting"
            :label="submitting ? t('admin.subscriptions.adjusting') : t('admin.subscriptions.adjust')"
          />
        </div>
      </template>
    </BaseDialog>

    <!-- Revoke Confirmation Dialog -->
    <ConfirmDialog
      :show="showRevokeDialog"
      :title="t('admin.subscriptions.revokeSubscription')"
      :message="t('admin.subscriptions.revokeConfirm', { user: revokingSubscription?.user?.email })"
      :confirm-text="t('admin.subscriptions.revoke')"
      :cancel-text="t('common.cancel')"
      :danger="true"
      @confirm="confirmRevoke"
      @cancel="showRevokeDialog = false"
    />

    <!-- Restore Confirmation Dialog -->
    <ConfirmDialog
      :show="showRestoreDialog"
      :title="t('admin.subscriptions.restoreSubscription')"
      :message="t('admin.subscriptions.restoreConfirm', { user: restoringSubscription?.user?.email })"
      :confirm-text="t('admin.subscriptions.restore')"
      :cancel-text="t('common.cancel')"
      @confirm="confirmRestore"
      @cancel="showRestoreDialog = false"
    />

    <!-- Reset Quota Confirmation Dialog -->
    <ConfirmDialog
      :show="showResetQuotaConfirm"
      :title="t('admin.subscriptions.resetQuotaTitle')"
      :message="t('admin.subscriptions.resetQuotaConfirm', { user: resettingSubscription?.user?.email })"
      :confirm-text="t('admin.subscriptions.resetQuota')"
      :cancel-text="t('common.cancel')"
      @confirm="confirmResetQuota"
      @cancel="showResetQuotaConfirm = false"
    />
    <!-- Subscription Guide -->
    <BaseDialog
      :show="showGuideModal"
      :title="t('admin.subscriptions.guide.title')"
      width="wide"
      close-on-click-outside
      @close="showGuideModal = false"
    >
      <div class="space-y-5 text-sm">
        <p class="text-muted">{{ t('admin.subscriptions.guide.subtitle') }}</p>
        <section v-for="step in guideSteps" :key="step.n" class="space-y-2">
          <h4 class="flex items-center gap-2 font-semibold text-highlighted">
            <UBadge :label="String(step.n)" color="primary" variant="soft" size="sm" class="size-5 justify-center rounded-full" />
            {{ step.title }}
          </h4>
          <ol v-if="step.lines.length" class="ml-7 list-decimal space-y-1 text-default">
            <li v-for="line in step.lines" :key="line">{{ line }}</li>
          </ol>
          <UButton
            v-if="step.n === 1"
            to="/admin/groups"
            variant="link"
            color="primary"
            trailing-icon="i-lucide-arrow-right"
            class="ml-7 px-0"
            :label="t('admin.subscriptions.guide.step1.link')"
            @click="showGuideModal = false"
          />
          <dl v-if="step.n === 3" class="ml-7 divide-y divide-default border-y border-default">
            <div v-for="row in guideActionRows" :key="row.action" class="grid grid-cols-[6rem_1fr] gap-3 py-2">
              <dt class="font-medium text-highlighted">{{ row.action }}</dt>
              <dd class="text-muted">{{ row.desc }}</dd>
            </div>
          </dl>
        </section>
        <UAlert color="info" variant="subtle" icon="i-lucide-info" :description="t('admin.subscriptions.guide.tip')" />
      </div>
      <template #footer>
        <div class="flex justify-end">
          <UButton :label="t('common.close')" @click="showGuideModal = false" />
        </div>
      </template>
    </BaseDialog>
  </AppLayout>
</template>

<script setup lang="ts">
import { ref, reactive, computed, onMounted, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import type { DropdownMenuItem } from '@nuxt/ui'
import { useAppStore } from '@/stores/app'
import { adminAPI } from '@/api/admin'
import type { AdminUser, UserSubscription, Group, GroupPlatform, SubscriptionType } from '@/types'
import type { SimpleUser } from '@/api/admin/usage'
import type { Column } from '@/components/common/types'
import { formatDateTimeToMinute } from '@/utils/format'
import { getPersistedPageSize } from '@/composables/usePersistedPageSize'
import AppLayout from '@/components/layout/AppLayout.vue'
import TablePageLayout from '@/components/layout/TablePageLayout.vue'
import DataTable from '@/components/common/DataTable.vue'
import Pagination from '@/components/common/Pagination.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import Select from '@/components/common/Select.vue'
import GroupBadge from '@/components/common/GroupBadge.vue'
import GroupOptionItem from '@/components/common/GroupOptionItem.vue'
import AdminPageHeader from '@/components/admin/page/AdminPageHeader.vue'
import AdminRowAction from '@/components/admin/page/AdminRowAction.vue'
import PageToolbar from '@/components/layout/PageToolbar.vue'
import {
  getRemainingDurationParts,
  getRemainingExpiryDuration,
  isOneTimeDailyQuota,
  type RemainingDurationParts
} from '@/utils/subscriptionQuota'
import { GROUP_PLATFORM_OPTIONS } from '@/constants/platforms'

const { t } = useI18n()
const appStore = useAppStore()

interface GroupOption {
  value: number
  label: string
  description: string | null
  platform: GroupPlatform
  subscriptionType: SubscriptionType
  rate: number
}

// Guide modal state
const showGuideModal = ref(false)

const guideActionRows = computed(() => [
  { action: t('admin.subscriptions.guide.actions.adjust'), desc: t('admin.subscriptions.guide.actions.adjustDesc') },
  { action: t('admin.subscriptions.guide.actions.resetQuota'), desc: t('admin.subscriptions.guide.actions.resetQuotaDesc') },
  { action: t('admin.subscriptions.guide.actions.revoke'), desc: t('admin.subscriptions.guide.actions.revokeDesc') }
])

// Guide steps: step 1 also links to the groups page, step 3 lists the row actions.
const guideSteps = computed(() => [
  {
    n: 1,
    title: t('admin.subscriptions.guide.step1.title'),
    lines: [t('admin.subscriptions.guide.step1.line1'), t('admin.subscriptions.guide.step1.line2'), t('admin.subscriptions.guide.step1.line3')]
  },
  {
    n: 2,
    title: t('admin.subscriptions.guide.step2.title'),
    lines: [t('admin.subscriptions.guide.step2.line1'), t('admin.subscriptions.guide.step2.line2'), t('admin.subscriptions.guide.step2.line3')]
  },
  { n: 3, title: t('admin.subscriptions.guide.step3.title'), lines: [] as string[] }
])

// User column display mode: 'email' or 'username'
const userColumnMode = ref<'email' | 'username'>('email')
const USER_COLUMN_MODE_KEY = 'subscription-user-column-mode'

const loadUserColumnMode = () => {
  try {
    const saved = localStorage.getItem(USER_COLUMN_MODE_KEY)
    if (saved === 'email' || saved === 'username') {
      userColumnMode.value = saved
    }
  } catch (e) {
    console.error('Failed to load user column mode:', e)
  }
}

const saveUserColumnMode = () => {
  try {
    localStorage.setItem(USER_COLUMN_MODE_KEY, userColumnMode.value)
  } catch (e) {
    console.error('Failed to save user column mode:', e)
  }
}

const setUserColumnMode = (mode: 'email' | 'username') => {
  userColumnMode.value = mode
  saveUserColumnMode()
}

// All available columns
const allColumns = computed<Column[]>(() => [
  {
    key: 'user',
    label: userColumnMode.value === 'email'
      ? t('admin.subscriptions.columns.user')
      : t('admin.users.columns.username'),
    sortable: false
  },
  { key: 'group', label: t('admin.subscriptions.columns.group'), sortable: false },
  { key: 'usage', label: t('admin.subscriptions.columns.usage'), sortable: false },
  { key: 'expires_at', label: t('admin.subscriptions.columns.expires'), sortable: true },
  { key: 'status', label: t('admin.subscriptions.columns.status'), sortable: true },
  { key: 'actions', label: t('admin.subscriptions.columns.actions'), sortable: false }
])

// Columns that can be toggled (exclude user and actions which are always visible)
const toggleableColumns = computed(() =>
  allColumns.value.filter(col => col.key !== 'user' && col.key !== 'actions')
)

// Hidden columns set
const hiddenColumns = reactive<Set<string>>(new Set())

// Default hidden columns
const DEFAULT_HIDDEN_COLUMNS: string[] = []

// localStorage key
const HIDDEN_COLUMNS_KEY = 'subscription-hidden-columns'

// Load saved column settings
const loadSavedColumns = () => {
  try {
    const saved = localStorage.getItem(HIDDEN_COLUMNS_KEY)
    if (saved) {
      const parsed = JSON.parse(saved) as string[]
      parsed.forEach(key => hiddenColumns.add(key))
    } else {
      DEFAULT_HIDDEN_COLUMNS.forEach(key => hiddenColumns.add(key))
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
  } catch (e) {
    console.error('Failed to save columns:', e)
  }
}

// Toggle column visibility
const toggleColumn = (key: string) => {
  if (hiddenColumns.has(key)) {
    hiddenColumns.delete(key)
  } else {
    hiddenColumns.add(key)
  }
  saveColumnsToStorage()
}

// Check if column is visible
const isColumnVisible = (key: string) => !hiddenColumns.has(key)

// Filtered columns for display
const columns = computed<Column[]>(() =>
  allColumns.value.filter(col =>
    col.key === 'user' || col.key === 'actions' || !hiddenColumns.has(col.key)
  )
)

// "显示列" menu: user column mode (email / username), then the toggleable columns.
// onSelect keeps the menu open so several columns can be switched in one go.
const keepMenuOpen = (e: Event) => e.preventDefault()
const columnMenuItems = computed<DropdownMenuItem[][]>(() => [
  [
    { type: 'label', label: t('admin.subscriptions.columns.user') },
    {
      type: 'checkbox',
      label: t('admin.users.columns.email'),
      checked: userColumnMode.value === 'email',
      onUpdateChecked: () => setUserColumnMode('email'),
      onSelect: keepMenuOpen
    },
    {
      type: 'checkbox',
      label: t('admin.users.columns.username'),
      checked: userColumnMode.value === 'username',
      onUpdateChecked: () => setUserColumnMode('username'),
      onSelect: keepMenuOpen
    }
  ],
  toggleableColumns.value.map((col) => ({
    type: 'checkbox' as const,
    label: col.label,
    checked: isColumnVisible(col.key),
    onUpdateChecked: () => toggleColumn(col.key),
    onSelect: keepMenuOpen
  }))
])

// Row "…" menu for active subscriptions (adjust stays a visible icon button).
const rowMenuItems = (row: UserSubscription): DropdownMenuItem[][] => [[
  {
    label: t('admin.subscriptions.resetQuota'),
    icon: 'i-lucide-rotate-ccw',
    disabled: resettingQuota.value && resettingSubscription.value?.id === row.id,
    onSelect: () => handleResetQuota(row)
  },
  {
    label: t('admin.subscriptions.revoke'),
    icon: 'i-lucide-ban',
    color: 'error',
    onSelect: () => handleRevoke(row)
  }
]]

// Filter options
const statusOptions = computed(() => [
  { value: '', label: t('admin.subscriptions.allStatus') },
  { value: 'active', label: t('admin.subscriptions.status.active') },
  { value: 'expired', label: t('admin.subscriptions.status.expired') },
  { value: 'revoked', label: t('admin.subscriptions.status.revoked') }
])

const subscriptions = ref<UserSubscription[]>([])
const groups = ref<Group[]>([])
const loading = ref(false)
let abortController: AbortController | null = null

// Toolbar user filter (fuzzy search -> select user_id)
const filterUserKeyword = ref('')
const filterUserResults = ref<SimpleUser[]>([])
const filterUserLoading = ref(false)
const showFilterUserDropdown = ref(false)
const selectedFilterUser = ref<SimpleUser | null>(null)
let filterUserSearchTimeout: ReturnType<typeof setTimeout> | null = null

// User search state
const userSearchKeyword = ref('')
const userSearchResults = ref<AdminUser[]>([])
const userSearchLoading = ref(false)
const showUserDropdown = ref(false)
const selectedUser = ref<AdminUser | null>(null)
let userSearchTimeout: ReturnType<typeof setTimeout> | null = null

const filters = reactive({
  status: 'active',
  group_id: '',
  platform: '',
  user_id: null as number | null
})

// Sorting state
const sortState = reactive({
  sort_by: 'created_at',
  sort_order: 'desc' as 'asc' | 'desc'
})

const pagination = reactive({
  page: 1,
  page_size: getPersistedPageSize(),
  total: 0,
  pages: 0
})

const showAssignModal = ref(false)
const showExtendModal = ref(false)
const showRevokeDialog = ref(false)
const showRestoreDialog = ref(false)
const showResetQuotaConfirm = ref(false)
const submitting = ref(false)
const resettingSubscription = ref<UserSubscription | null>(null)
const resettingQuota = ref(false)
const extendingSubscription = ref<UserSubscription | null>(null)
const revokingSubscription = ref<UserSubscription | null>(null)
const restoringSubscription = ref<UserSubscription | null>(null)

const assignForm = reactive({
  user_id: null as number | null,
  group_id: null as number | null,
  validity_days: 30
})

const extendForm = reactive({
  days: 30
})

// Group options for filter (all groups)
const groupOptions = computed(() => [
  { value: '', label: t('admin.subscriptions.allGroups') },
  ...groups.value.map((g) => ({ value: g.id.toString(), label: g.name }))
])

const platformFilterOptions = computed(() => [
  { value: '', label: t('admin.subscriptions.allPlatforms') },
  ...GROUP_PLATFORM_OPTIONS
])

// Group options for assign (only subscription type groups)
const subscriptionGroupOptions = computed(() =>
  groups.value
    .filter((g) => g.subscription_type === 'subscription' && g.status === 'active')
    .map((g) => ({
      value: g.id,
      label: g.name,
      description: g.description,
      platform: g.platform,
      subscriptionType: g.subscription_type,
      rate: g.rate_multiplier
    }))
)

const applyFilters = () => {
  pagination.page = 1
  loadSubscriptions()
}

const loadSubscriptions = async () => {
  if (abortController) {
    abortController.abort()
  }
  const requestController = new AbortController()
  abortController = requestController
  const { signal } = requestController

  loading.value = true
  try {
    const response = await adminAPI.subscriptions.list(
      pagination.page,
      pagination.page_size,
      {
        status: (filters.status as any) || undefined,
        group_id: filters.group_id ? parseInt(filters.group_id) : undefined,
        platform: filters.platform || undefined,
        user_id: filters.user_id || undefined,
        sort_by: sortState.sort_by,
        sort_order: sortState.sort_order
      },
      {
        signal
      }
    )
    if (signal.aborted || abortController !== requestController) return
    subscriptions.value = response.items
    pagination.total = response.total
    pagination.pages = response.pages
  } catch (error: any) {
    if (signal.aborted || error?.name === 'AbortError' || error?.code === 'ERR_CANCELED') {
      return
    }
    appStore.showError(t('admin.subscriptions.failedToLoad'))
    console.error('Error loading subscriptions:', error)
  } finally {
    if (abortController === requestController) {
      loading.value = false
      abortController = null
    }
  }
}

const loadGroups = async () => {
  try {
    groups.value = await adminAPI.groups.getAll()
  } catch (error) {
    console.error('Error loading groups:', error)
  }
}

// Toolbar user filter search with debounce
const debounceSearchFilterUsers = () => {
  if (filterUserSearchTimeout) {
    clearTimeout(filterUserSearchTimeout)
  }
  filterUserSearchTimeout = setTimeout(searchFilterUsers, 300)
}

const searchFilterUsers = async () => {
  const keyword = filterUserKeyword.value.trim()

  // Clear active user filter if user modified the search keyword
  if (selectedFilterUser.value && keyword !== selectedFilterUser.value.email) {
    selectedFilterUser.value = null
    filters.user_id = null
    applyFilters()
  }

  if (!keyword) {
    filterUserResults.value = []
    return
  }

  filterUserLoading.value = true
  try {
    filterUserResults.value = await adminAPI.usage.searchUsers(keyword)
  } catch (error) {
    console.error('Failed to search users:', error)
    filterUserResults.value = []
  } finally {
    filterUserLoading.value = false
  }
}

const selectFilterUser = (user: SimpleUser) => {
  selectedFilterUser.value = user
  filterUserKeyword.value = user.email
  showFilterUserDropdown.value = false
  filters.user_id = user.id
  applyFilters()
}

const clearFilterUser = () => {
  selectedFilterUser.value = null
  filterUserKeyword.value = ''
  filterUserResults.value = []
  showFilterUserDropdown.value = false
  filters.user_id = null
  applyFilters()
}

// User search with debounce
const debounceSearchUsers = () => {
  // Invalidate the assignment target before the debounced search runs.
  if (selectedUser.value && userSearchKeyword.value.trim() !== selectedUser.value.email) {
    selectedUser.value = null
    assignForm.user_id = null
  }
  if (userSearchTimeout) {
    clearTimeout(userSearchTimeout)
  }
  userSearchTimeout = setTimeout(searchUsers, 300)
}

const searchUsers = async () => {
  const keyword = userSearchKeyword.value.trim()

  if (!keyword) {
    userSearchResults.value = []
    return
  }

  userSearchLoading.value = true
  try {
    const result = await adminAPI.users.list(1, 30, {
      search: keyword, sort_by: 'email', sort_order: 'asc'
    })
    userSearchResults.value = result.items
  } catch (error) {
    console.error('Failed to search users:', error)
    userSearchResults.value = []
  } finally {
    userSearchLoading.value = false
  }
}

const selectUser = (user: AdminUser) => {
  selectedUser.value = user
  userSearchKeyword.value = user.email
  showUserDropdown.value = false
  assignForm.user_id = user.id
}

const clearUserSelection = () => {
  selectedUser.value = null
  userSearchKeyword.value = ''
  userSearchResults.value = []
  assignForm.user_id = null
}

const handlePageChange = (page: number) => {
  pagination.page = page
  loadSubscriptions()
}

const handlePageSizeChange = (pageSize: number) => {
  pagination.page_size = pageSize
  pagination.page = 1
  loadSubscriptions()
}

const handleSort = (key: string, order: 'asc' | 'desc') => {
  sortState.sort_by = key
  sortState.sort_order = order
  pagination.page = 1
  loadSubscriptions()
}

const closeAssignModal = () => {
  showAssignModal.value = false
  assignForm.user_id = null
  assignForm.group_id = null
  assignForm.validity_days = 30
  // Clear user search state
  selectedUser.value = null
  userSearchKeyword.value = ''
  userSearchResults.value = []
  showUserDropdown.value = false
}

const handleAssignSubscription = async () => {
  if (!assignForm.user_id) {
    appStore.showError(t('admin.subscriptions.pleaseSelectUser'))
    return
  }
  if (!assignForm.group_id) {
    appStore.showError(t('admin.subscriptions.pleaseSelectGroup'))
    return
  }
  if (!assignForm.validity_days || assignForm.validity_days < 1) {
    appStore.showError(t('admin.subscriptions.validityDaysRequired'))
    return
  }

  submitting.value = true
  try {
    await adminAPI.subscriptions.assign({
      user_id: assignForm.user_id,
      group_id: assignForm.group_id,
      validity_days: assignForm.validity_days
    })
    appStore.showSuccess(t('admin.subscriptions.subscriptionAssigned'))
    closeAssignModal()
    loadSubscriptions()
  } catch (error: any) {
    appStore.showError(error.response?.data?.detail || t('admin.subscriptions.failedToAssign'))
    console.error('Error assigning subscription:', error)
  } finally {
    submitting.value = false
  }
}

const handleExtend = (subscription: UserSubscription) => {
  extendingSubscription.value = subscription
  extendForm.days = 30
  showExtendModal.value = true
}

const closeExtendModal = () => {
  showExtendModal.value = false
  extendingSubscription.value = null
}

const handleExtendSubscription = async () => {
  if (!extendingSubscription.value) return

  // 前端验证：调整后的过期时间必须在未来
  if (extendingSubscription.value.expires_at) {
    const expiresAt = new Date(extendingSubscription.value.expires_at)
    const newExpiresAt = new Date(expiresAt.getTime() + extendForm.days * 24 * 60 * 60 * 1000)
    if (newExpiresAt <= new Date()) {
      appStore.showError(t('admin.subscriptions.adjustWouldExpire'))
      return
    }
  }

  submitting.value = true
  try {
    await adminAPI.subscriptions.extend(extendingSubscription.value.id, {
      days: extendForm.days
    })
    appStore.showSuccess(t('admin.subscriptions.subscriptionAdjusted'))
    closeExtendModal()
    loadSubscriptions()
  } catch (error: any) {
    appStore.showError(error.response?.data?.detail || t('admin.subscriptions.failedToAdjust'))
    console.error('Error adjusting subscription:', error)
  } finally {
    submitting.value = false
  }
}

const handleRevoke = (subscription: UserSubscription) => {
  revokingSubscription.value = subscription
  showRevokeDialog.value = true
}

const confirmRevoke = async () => {
  if (!revokingSubscription.value) return

  try {
    await adminAPI.subscriptions.revoke(revokingSubscription.value.id)
    appStore.showSuccess(t('admin.subscriptions.subscriptionRevoked'))
    showRevokeDialog.value = false
    revokingSubscription.value = null
    loadSubscriptions()
  } catch (error: any) {
    appStore.showError(error.response?.data?.detail || t('admin.subscriptions.failedToRevoke'))
    console.error('Error revoking subscription:', error)
  }
}

const handleRestore = (subscription: UserSubscription) => {
  restoringSubscription.value = subscription
  showRestoreDialog.value = true
}

const confirmRestore = async () => {
  if (!restoringSubscription.value) return

  try {
    await adminAPI.subscriptions.restore(restoringSubscription.value.id)
    appStore.showSuccess(t('admin.subscriptions.subscriptionRestored'))
    showRestoreDialog.value = false
    restoringSubscription.value = null
    loadSubscriptions()
  } catch (error: any) {
    appStore.showError(error.response?.data?.detail || t('admin.subscriptions.failedToRestore'))
    console.error('Error restoring subscription:', error)
  }
}

const handleResetQuota = (subscription: UserSubscription) => {
  resettingSubscription.value = subscription
  showResetQuotaConfirm.value = true
}

const confirmResetQuota = async () => {
  if (!resettingSubscription.value) return
  if (resettingQuota.value) return
  resettingQuota.value = true
  try {
    await adminAPI.subscriptions.resetQuota(resettingSubscription.value.id, { daily: true, weekly: true, monthly: true })
    appStore.showSuccess(t('admin.subscriptions.quotaResetSuccess'))
    showResetQuotaConfirm.value = false
    resettingSubscription.value = null
    await loadSubscriptions()
  } catch (error: any) {
    appStore.showError(error.response?.data?.detail || t('admin.subscriptions.failedToResetQuota'))
    console.error('Error resetting quota:', error)
  } finally {
    resettingQuota.value = false
  }
}

// Helper functions
const getDaysRemaining = (expiresAt: string): number | null => {
  const now = new Date()
  const expires = new Date(expiresAt)
  const diff = expires.getTime() - now.getTime()
  if (diff < 0) return null
  return Math.ceil(diff / (1000 * 60 * 60 * 24))
}

const formatRemainingExpiry = (expiresAt: string): string | null => {
  const duration = getRemainingExpiryDuration(expiresAt)
  if (!duration) return null
  if (duration.unit === 'days') {
    return t('admin.subscriptions.daysRemaining', { days: duration.days })
  }
  if (duration.hours) {
    return t('admin.subscriptions.hoursMinutesRemaining', {
      hours: duration.hours,
      minutes: duration.minutes
    })
  }
  return t('admin.subscriptions.minutesRemaining', { minutes: duration.minutes })
}

const isExpiringSoon = (expiresAt: string): boolean => {
  const days = getDaysRemaining(expiresAt)
  return days !== null && days <= 7
}

const getProgressPercent = (used: number | null | undefined, limit: number | null | undefined): number => {
  if (!limit || limit === 0) return 0
  const usedValue = used ?? 0
  return Math.min((usedValue / limit) * 100, 100)
}

const getProgressColor = (
  used: number | null | undefined,
  limit: number | null | undefined
): 'neutral' | 'error' | 'warning' | 'success' => {
  if (!limit || limit === 0) return 'neutral'
  const usedValue = used ?? 0
  const percentage = (usedValue / limit) * 100
  if (percentage >= 90) return 'error'
  if (percentage >= 70) return 'warning'
  return 'success'
}

const formatResetDuration = (parts: RemainingDurationParts): string => {
  if (parts.days > 0) {
    return t('admin.subscriptions.resetInDaysHours', { days: parts.days, hours: parts.hours })
  }

  if (parts.hours > 0) {
    return t('admin.subscriptions.resetInHoursMinutes', { hours: parts.hours, minutes: parts.minutes })
  }

  return t('admin.subscriptions.resetInMinutes', { minutes: parts.minutes })
}

const formatQuotaEndDuration = (parts: RemainingDurationParts): string => {
  if (parts.days > 0) {
    return t('admin.subscriptions.quotaEndsInDaysHours', { days: parts.days, hours: parts.hours })
  }

  if (parts.hours > 0) {
    return t('admin.subscriptions.quotaEndsInHoursMinutes', { hours: parts.hours, minutes: parts.minutes })
  }

  return t('admin.subscriptions.quotaEndsInMinutes', { minutes: parts.minutes })
}

const formatDailyUsageWindow = (subscription: UserSubscription): string => {
  if (isOneTimeDailyQuota(subscription) && subscription.expires_at) {
    const parts = getRemainingDurationParts(subscription.expires_at)
    return parts ? formatQuotaEndDuration(parts) : t('admin.subscriptions.windowNotActive')
  }

  return formatResetTime(subscription.daily_window_start, 'daily')
}

// Format reset time based on window start and period type
const formatResetTime = (windowStart: string | null, period: 'daily' | 'weekly' | 'monthly'): string => {
  if (!windowStart) return t('admin.subscriptions.windowNotActive')

  const start = new Date(windowStart)
  const now = new Date()

  // Calculate reset time based on period
  let resetTime: Date
  switch (period) {
    case 'daily':
      resetTime = new Date(start.getTime() + 24 * 60 * 60 * 1000)
      break
    case 'weekly':
      resetTime = new Date(start.getTime() + 7 * 24 * 60 * 60 * 1000)
      break
    case 'monthly':
      resetTime = new Date(start.getTime() + 30 * 24 * 60 * 60 * 1000)
      break
  }

  const parts = getRemainingDurationParts(resetTime, now)

  return parts ? formatResetDuration(parts) : t('admin.subscriptions.windowNotActive')
}

// Handle click outside to close dropdowns
const handleClickOutside = (event: MouseEvent) => {
  const target = event.target as HTMLElement
  if (!target.closest('[data-assign-user-search]')) showUserDropdown.value = false
  if (!target.closest('[data-filter-user-search]')) showFilterUserDropdown.value = false
}

onMounted(() => {
  loadUserColumnMode()
  loadSavedColumns()
  loadSubscriptions()
  loadGroups()
  document.addEventListener('click', handleClickOutside)
})

onUnmounted(() => {
  document.removeEventListener('click', handleClickOutside)
  if (filterUserSearchTimeout) {
    clearTimeout(filterUserSearchTimeout)
  }
  if (userSearchTimeout) {
    clearTimeout(userSearchTimeout)
  }
})
</script>
