<template>
  <AppLayout>
    <TablePageLayout>
      <template #actions>
        <div class="flex justify-end gap-3">
          <button
          @click="loadAccounts"
          :disabled="loading"
          class="btn btn-secondary"
          :title="t('common.refresh')"
        >
          <svg
            :class="['h-5 w-5', loading ? 'animate-spin' : '']"
            fill="none"
            viewBox="0 0 24 24"
            stroke="currentColor"
            stroke-width="1.5"
          >
            <path
              stroke-linecap="round"
              stroke-linejoin="round"
              d="M16.023 9.348h4.992v-.001M2.985 19.644v-4.992m0 0h4.992m-4.993 0l3.181 3.183a8.25 8.25 0 0013.803-3.7M4.031 9.865a8.25 8.25 0 0113.803-3.7l3.181 3.182m0-4.991v4.99"
            />
          </svg>
        </button>
        <button @click="showCrsSyncModal = true" class="btn btn-secondary" :title="t('admin.accounts.syncFromCrs')">
          <svg
            class="h-5 w-5"
            fill="none"
            viewBox="0 0 24 24"
            stroke="currentColor"
            stroke-width="1.5"
          >
            <path
              stroke-linecap="round"
              stroke-linejoin="round"
              d="M20.25 6.375c0 2.278-3.694 4.125-8.25 4.125S3.75 8.653 3.75 6.375m16.5 0c0-2.278-3.694-4.125-8.25-4.125S3.75 4.097 3.75 6.375m16.5 0v11.25c0 2.278-3.694 4.125-8.25 4.125s-8.25-1.847-8.25-4.125V6.375m16.5 0v3.75m-16.5-3.75v3.75m16.5 0v3.75C20.25 16.153 16.556 18 12 18s-8.25-1.847-8.25-4.125v-3.75m16.5 0c0 2.278-3.694 4.125-8.25 4.125s-8.25-1.847-8.25-4.125"
            />
          </svg>
        </button>
        <button @click="showCreateModal = true" class="btn btn-primary" data-tour="accounts-create-btn">
          <svg
            class="mr-2 h-5 w-5"
            fill="none"
            viewBox="0 0 24 24"
            stroke="currentColor"
            stroke-width="1.5"
          >
            <path stroke-linecap="round" stroke-linejoin="round" d="M12 4.5v15m7.5-7.5h-15" />
          </svg>
          {{ t('admin.accounts.createAccount') }}
        </button>
        </div>
      </template>

      <template #filters>
        <div class="flex flex-col gap-4 lg:flex-row lg:items-center lg:justify-between">
          <div class="relative max-w-md flex-1">
          <svg
            class="absolute left-3 top-1/2 h-5 w-5 -translate-y-1/2 text-gray-400"
            fill="none"
            stroke="currentColor"
            viewBox="0 0 24 24"
            stroke-width="1.5"
          >
            <path
              stroke-linecap="round"
              stroke-linejoin="round"
              d="M21 21l-5.197-5.197m0 0A7.5 7.5 0 105.196 5.196a7.5 7.5 0 0010.607 10.607z"
            />
          </svg>
          <input
            v-model="searchQuery"
            type="text"
            :placeholder="t('admin.accounts.searchAccounts')"
            class="input pl-10"
            @input="handleSearch"
          />
          </div>
          <div class="flex flex-wrap gap-3">
          <Select
            v-model="filters.platform"
            :options="platformOptions"
            :placeholder="t('admin.accounts.allPlatforms')"
            class="w-40"
            @change="loadAccounts"
          />
          <Select
            v-model="filters.type"
            :options="typeOptions"
            :placeholder="t('admin.accounts.allTypes')"
            class="w-40"
            @change="loadAccounts"
          />
          <Select
            v-model="filters.status"
            :options="statusOptions"
            :placeholder="t('admin.accounts.allStatus')"
            class="w-36"
            @change="loadAccounts"
          />
          </div>
        </div>
      </template>

      <template #table>
        <!-- Bulk Actions Bar -->
        <div
          v-if="selectedAccountIds.length > 0"
          class="mb-[5px] mt-[10px] px-5 py-1"
        >
          <div class="flex flex-wrap items-center justify-between gap-3">
            <div class="flex flex-wrap items-center gap-2">
              <span class="text-sm font-medium text-primary-900 dark:text-primary-100">
                {{ t('admin.accounts.bulkActions.selected', { count: selectedAccountIds.length }) }}
              </span>
              <button
                @click="selectCurrentPageAccounts"
                class="text-xs font-medium text-primary-700 hover:text-primary-800 dark:text-primary-300 dark:hover:text-primary-200"
              >
                {{ t('admin.accounts.bulkActions.selectCurrentPage') }}
              </button>
              <span class="text-gray-300 dark:text-primary-800">•</span>
              <button
                @click="selectedAccountIds = []"
                class="text-xs font-medium text-primary-700 hover:text-primary-800 dark:text-primary-300 dark:hover:text-primary-200"
              >
                {{ t('admin.accounts.bulkActions.clear') }}
              </button>
            </div>
            <div class="flex items-center gap-2">
              <button @click="handleBulkDelete" class="btn btn-danger btn-sm">
                <svg
                  class="mr-1.5 h-4 w-4"
                  fill="none"
                  viewBox="0 0 24 24"
                  stroke="currentColor"
                  stroke-width="1.5"
                >
                  <path
                    stroke-linecap="round"
                    stroke-linejoin="round"
                    d="M14.74 9l-.346 9m-4.788 0L9.26 9m9.968-3.21c.342.052.682.107 1.022.166m-1.022-.165L18.16 19.673a2.25 2.25 0 01-2.244 2.077H8.084a2.25 2.25 0 01-2.244-2.077L4.772 5.79m14.456 0a48.108 48.108 0 00-3.478-.397m-12 .562c.34-.059.68-.114 1.022-.165m0 0a48.11 48.11 0 013.478-.397m7.5 0v-.916c0-1.18-.91-2.164-2.09-2.201a51.964 51.964 0 00-3.32 0c-1.18.037-2.09 1.022-2.09 2.201v.916m7.5 0a48.667 48.667 0 00-7.5 0"
                  />
                </svg>
                {{ t('admin.accounts.bulkActions.delete') }}
              </button>
              <button @click="showBulkEditModal = true" class="btn btn-primary btn-sm">
                <svg
                  class="mr-1.5 h-4 w-4"
                  fill="none"
                  viewBox="0 0 24 24"
                  stroke="currentColor"
                  stroke-width="1.5"
                >
                  <path
                    stroke-linecap="round"
                    stroke-linejoin="round"
                    d="M16.862 4.487l1.687-1.688a1.875 1.875 0 112.652 2.652L10.582 16.07a4.5 4.5 0 01-1.897 1.13L6 18l.8-2.685a4.5 4.5 0 011.13-1.897l8.932-8.931zm0 0L19.5 7.125M18 14v4.75A2.25 2.25 0 0115.75 21H5.25A2.25 2.25 0 013 18.75V8.25A2.25 2.25 0 015.25 6H10"
                  />
                </svg>
                {{ t('admin.accounts.bulkActions.edit') }}
              </button>
            </div>
          </div>
        </div>

      <DataTable :columns="columns" :data="accounts" :loading="loading">
          <template #cell-select="{ row }">
            <input
              type="checkbox"
              :checked="selectedAccountIds.includes(row.id)"
              @change="toggleAccountSelection(row.id)"
              class="rounded border-gray-300 text-primary-600 focus:ring-primary-500"
            />
          </template>

          <template #cell-name="{ value }">
            <span class="font-medium text-gray-900 dark:text-white">{{ value }}</span>
          </template>

          <template #cell-platform_type="{ row }">
            <PlatformTypeBadge :platform="row.platform" :type="row.type" />
          </template>

          <template #cell-concurrency="{ row }">
            <div class="flex items-center gap-1.5">
              <span
                :class="[
                  'inline-flex items-center gap-1 rounded-md px-2 py-0.5 text-xs font-medium',
                  (row.current_concurrency || 0) >= row.concurrency
                    ? 'bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-400'
                    : (row.current_concurrency || 0) > 0
                      ? 'bg-yellow-100 text-yellow-700 dark:bg-yellow-900/30 dark:text-yellow-400'
                      : 'bg-gray-100 text-gray-600 dark:bg-gray-800 dark:text-gray-400'
                ]"
              >
                <svg
                  class="h-3 w-3"
                  fill="none"
                  viewBox="0 0 24 24"
                  stroke="currentColor"
                  stroke-width="2"
                >
                  <path
                    stroke-linecap="round"
                    stroke-linejoin="round"
                    d="M3.75 6A2.25 2.25 0 016 3.75h2.25A2.25 2.25 0 0110.5 6v2.25a2.25 2.25 0 01-2.25 2.25H6a2.25 2.25 0 01-2.25-2.25V6zM3.75 15.75A2.25 2.25 0 016 13.5h2.25a2.25 2.25 0 012.25 2.25V18a2.25 2.25 0 01-2.25 2.25H6A2.25 2.25 0 013.75 18v-2.25zM13.5 6a2.25 2.25 0 012.25-2.25H18A2.25 2.25 0 0120.25 6v2.25A2.25 2.25 0 0118 10.5h-2.25a2.25 2.25 0 01-2.25-2.25V6zM13.5 15.75a2.25 2.25 0 012.25-2.25H18a2.25 2.25 0 012.25 2.25V18A2.25 2.25 0 0118 20.25h-2.25A2.25 2.25 0 0113.5 18v-2.25z"
                  />
                </svg>
                <span class="font-mono">{{ row.current_concurrency || 0 }}</span>
                <span class="text-gray-400 dark:text-gray-500">/</span>
                <span class="font-mono">{{ row.concurrency }}</span>
              </span>
            </div>
          </template>

          <template #cell-status="{ row }">
            <AccountStatusIndicator :account="row" />
          </template>

          <template #cell-schedulable="{ row }">
            <button
              @click="handleToggleSchedulable(row)"
              :disabled="togglingSchedulable === row.id"
              class="relative inline-flex h-5 w-9 flex-shrink-0 cursor-pointer rounded-full border-2 border-transparent transition-colors duration-200 ease-in-out focus:outline-none focus:ring-2 focus:ring-primary-500 focus:ring-offset-2 disabled:cursor-not-allowed disabled:opacity-50 dark:focus:ring-offset-dark-800"
              :class="[
                row.schedulable
                  ? 'bg-primary-500 hover:bg-primary-600'
                  : 'bg-gray-200 hover:bg-gray-300 dark:bg-dark-600 dark:hover:bg-dark-500'
              ]"
              :title="
                row.schedulable
                  ? t('admin.accounts.schedulableEnabled')
                  : t('admin.accounts.schedulableDisabled')
              "
            >
              <span
                class="pointer-events-none inline-block h-4 w-4 transform rounded-full bg-white shadow ring-0 transition duration-200 ease-in-out"
                :class="[row.schedulable ? 'translate-x-4' : 'translate-x-0']"
              />
            </button>
          </template>

          <template #cell-today_stats="{ row }">
            <AccountTodayStatsCell :account="row" />
          </template>

          <template #cell-groups="{ row }">
            <div v-if="row.groups && row.groups.length > 0" class="flex flex-wrap gap-1.5">
              <GroupBadge
                v-for="group in row.groups"
                :key="group.id"
                :name="group.name"
                :platform="group.platform"
                :subscription-type="group.subscription_type"
                :rate-multiplier="group.rate_multiplier"
                :show-rate="false"
              />
            </div>
            <span v-else class="text-sm text-gray-400 dark:text-dark-500">-</span>
          </template>

          <template #cell-usage="{ row }">
            <AccountUsageCell :account="row" />
          </template>

          <template #cell-priority="{ value }">
            <span class="text-sm text-gray-700 dark:text-gray-300">{{ value }}</span>
          </template>

          <template #cell-last_used_at="{ value }">
            <span class="text-sm text-gray-500 dark:text-dark-400">
              {{ formatRelativeTime(value) }}
            </span>
          </template>

          <template #cell-actions="{ row }">
            <div class="flex items-center gap-1">
              <!-- Edit Button -->
              <button
                @click="handleEdit(row)"
                class="flex flex-col items-center gap-0.5 rounded-lg p-1.5 text-gray-500 transition-colors hover:bg-gray-100 hover:text-primary-600 dark:hover:bg-dark-700 dark:hover:text-primary-400"
              >
                <svg
                  class="h-4 w-4"
                  fill="none"
                  stroke="currentColor"
                  viewBox="0 0 24 24"
                  stroke-width="1.5"
                >
                  <path
                    stroke-linecap="round"
                    stroke-linejoin="round"
                    d="M16.862 4.487l1.687-1.688a1.875 1.875 0 112.652 2.652L10.582 16.07a4.5 4.5 0 01-1.897 1.13L6 18l.8-2.685a4.5 4.5 0 011.13-1.897l8.932-8.931zm0 0L19.5 7.125M18 14v4.75A2.25 2.25 0 0115.75 21H5.25A2.25 2.25 0 013 18.75V8.25A2.25 2.25 0 015.25 6H10"
                  />
                </svg>
                <span class="text-xs">{{ t('common.edit') }}</span>
              </button>

              <!-- Delete Button -->
              <button
                @click="handleDelete(row)"
                class="flex flex-col items-center gap-0.5 rounded-lg p-1.5 text-gray-500 transition-colors hover:bg-red-50 hover:text-red-600 dark:hover:bg-red-900/20 dark:hover:text-red-400"
              >
                <svg
                  class="h-4 w-4"
                  fill="none"
                  stroke="currentColor"
                  viewBox="0 0 24 24"
                  stroke-width="1.5"
                >
                  <path
                    stroke-linecap="round"
                    stroke-linejoin="round"
                    d="M14.74 9l-.346 9m-4.788 0L9.26 9m9.968-3.21c.342.052.682.107 1.022.166m-1.022-.165L18.16 19.673a2.25 2.25 0 01-2.244 2.077H8.084a2.25 2.25 0 01-2.244-2.077L4.772 5.79m14.456 0a48.108 48.108 0 00-3.478-.397m-12 .562c.34-.059.68-.114 1.022-.165m0 0a48.11 48.11 0 013.478-.397m7.5 0v-.916c0-1.18-.91-2.164-2.09-2.201a51.964 51.964 0 00-3.32 0c-1.18.037-2.09 1.022-2.09 2.201v.916m7.5 0a48.667 48.667 0 00-7.5 0"
                  />
                </svg>
                <span class="text-xs">{{ t('common.delete') }}</span>
              </button>

              <!-- More Actions Menu Trigger -->
              <button
                :ref="(el) => setActionButtonRef(row.id, el)"
                @click="openActionMenu(row)"
                class="action-menu-trigger flex flex-col items-center gap-0.5 rounded-lg p-1.5 text-gray-500 transition-colors hover:bg-gray-100 hover:text-gray-900 dark:hover:bg-dark-700 dark:hover:text-white"
                :class="{ 'bg-gray-100 text-gray-900 dark:bg-dark-700 dark:text-white': activeMenuId === row.id }"
              >
                <svg
                  class="h-4 w-4"
                  fill="none"
                  viewBox="0 0 24 24"
                  stroke="currentColor"
                  stroke-width="1.5"
                >
                  <path
                    stroke-linecap="round"
                    stroke-linejoin="round"
                    d="M6.75 12a.75.75 0 11-1.5 0 .75.75 0 011.5 0zM12.75 12a.75.75 0 11-1.5 0 .75.75 0 011.5 0zM18.75 12a.75.75 0 11-1.5 0 .75.75 0 011.5 0z"
                  />
                </svg>
                <span class="text-xs">{{ t('common.more') }}</span>
              </button>
            </div>
          </template>

          <template #empty>
            <EmptyState
              :title="t('admin.accounts.noAccountsYet')"
              :description="t('admin.accounts.createFirstAccount')"
              :action-text="t('admin.accounts.createAccount')"
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

    <!-- Create Account Modal -->
    <CreateAccountModal
      :show="showCreateModal"
      :proxies="proxies"
      :groups="groups"
      @close="showCreateModal = false"
      @created="() => { loadAccounts(); if (onboardingStore.isCurrentStep(`[data-tour='account-form-submit']`)) onboardingStore.nextStep(500) }"
    />

    <!-- Edit Account Modal -->
    <EditAccountModal
      :show="showEditModal"
      :account="editingAccount"
      :proxies="proxies"
      :groups="groups"
      @close="closeEditModal"
      @updated="loadAccounts"
    />

    <!-- Re-Auth Modal -->
    <ReAuthAccountModal
      :show="showReAuthModal"
      :account="reAuthAccount"
      @close="closeReAuthModal"
      @reauthorized="loadAccounts"
    />

    <!-- Test Account Modal -->
    <AccountTestModal :show="showTestModal" :account="testingAccount" @close="closeTestModal" />

    <!-- Account Stats Modal -->
    <AccountStatsModal :show="showStatsModal" :account="statsAccount" @close="closeStatsModal" />

    <!-- Delete Confirmation Dialog -->
    <ConfirmDialog
      :show="showDeleteDialog"
      :title="t('admin.accounts.deleteAccount')"
      :message="t('admin.accounts.deleteConfirm', { name: deletingAccount?.name })"
      :confirm-text="t('common.delete')"
      :cancel-text="t('common.cancel')"
      :danger="true"
      @confirm="confirmDelete"
      @cancel="showDeleteDialog = false"
    />
    <ConfirmDialog
      :show="showBulkDeleteDialog"
      :title="t('admin.accounts.bulkDeleteTitle')"
      :message="t('admin.accounts.bulkDeleteConfirm', { count: selectedAccountIds.length })"
      :confirm-text="t('common.delete')"
      :cancel-text="t('common.cancel')"
      :danger="true"
      @confirm="confirmBulkDelete"
      @cancel="showBulkDeleteDialog = false"
    />

    <SyncFromCrsModal
      :show="showCrsSyncModal"
      @close="showCrsSyncModal = false"
      @synced="handleCrsSynced"
    />

    <!-- Bulk Edit Account Modal -->
    <BulkEditAccountModal
      :show="showBulkEditModal"
      :account-ids="selectedAccountIds"
      :proxies="proxies"
      :groups="groups"
      @close="showBulkEditModal = false"
      @updated="handleBulkUpdated"
    />
    <!-- Action Menu (Teleported) -->
    <Teleport to="body">
      <div
        v-if="activeMenuId !== null && menuPosition"
        class="action-menu-content fixed z-[9999] w-52 overflow-hidden rounded-xl bg-white shadow-lg ring-1 ring-black/5 dark:bg-dark-800 dark:ring-white/10"
        :style="{ top: menuPosition.top + 'px', left: menuPosition.left + 'px' }"
      >
        <div class="py-1">
          <template v-for="account in accounts" :key="account.id">
            <template v-if="account.id === activeMenuId">
              <button
                @click="handleTest(account); closeActionMenu()"
                class="flex w-full items-center gap-2 px-4 py-2 text-sm text-gray-700 hover:bg-gray-100 dark:text-gray-300 dark:hover:bg-dark-700"
              >
                <svg class="h-4 w-4 text-green-500" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M14.752 11.168l-3.197-2.132A1 1 0 0010 9.87v4.263a1 1 0 001.555.832l3.197-2.132a1 1 0 000-1.664z" /><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M21 12a9 9 0 11-18 0 9 9 0 0118 0z" /></svg>
                {{ t('admin.accounts.testConnection') }}
              </button>
              <button
                @click="handleViewStats(account); closeActionMenu()"
                class="flex w-full items-center gap-2 px-4 py-2 text-sm text-gray-700 hover:bg-gray-100 dark:text-gray-300 dark:hover:bg-dark-700"
              >
                <svg class="h-4 w-4 text-indigo-500" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 19v-6a2 2 0 00-2-2H5a2 2 0 00-2 2v6a2 2 0 002 2h2a2 2 0 002-2zm0 0V9a2 2 0 012-2h2a2 2 0 012 2v10m-6 0a2 2 0 002 2h2a2 2 0 002-2m0 0V5a2 2 0 012-2h2a2 2 0 012 2v14a2 2 0 01-2 2h-2a2 2 0 01-2-2z" /></svg>
                {{ t('admin.accounts.viewStats') }}
              </button>
              <template v-if="account.type === 'oauth' || account.type === 'setup-token'">
                <button @click="handleReAuth(account); closeActionMenu()" class="flex w-full items-center gap-2 px-4 py-2 text-sm text-gray-700 hover:bg-gray-100 dark:text-gray-300 dark:hover:bg-dark-700">
                  <svg class="h-4 w-4 text-blue-500" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M13.828 10.172a4 4 0 00-5.656 0l-4 4a4 4 0 105.656 5.656l1.102-1.101m-.758-4.899a4 4 0 005.656 0l4-4a4 4 0 00-5.656-5.656l-1.1 1.1" /></svg>
                  {{ t('admin.accounts.reAuthorize') }}
                </button>
                <button @click="handleRefreshToken(account); closeActionMenu()" class="flex w-full items-center gap-2 px-4 py-2 text-sm text-gray-700 hover:bg-gray-100 dark:text-gray-300 dark:hover:bg-dark-700">
                  <svg class="h-4 w-4 text-purple-500" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 4v5h5M20 20v-5h-5M4 4l16 16" /></svg>
                  {{ t('admin.accounts.refreshToken') }}
                </button>
              </template>

              <div v-if="account.status === 'error' || isRateLimited(account) || isOverloaded(account)" class="my-1 border-t border-gray-100 dark:border-dark-700"></div>

              <button v-if="account.status === 'error'" @click="handleResetStatus(account); closeActionMenu()" class="flex w-full items-center gap-2 px-4 py-2 text-sm text-yellow-600 hover:bg-gray-100 dark:text-yellow-400 dark:hover:bg-dark-700">
                <svg class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M14.752 11.168l-3.197-2.132A1 1 0 0010 9.87v4.263a1 1 0 001.555.832l3.197-2.132a1 1 0 000-1.664z" /><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M21 12a9 9 0 11-18 0 9 9 0 0118 0z" /></svg>
                {{ t('admin.accounts.resetStatus') }}
              </button>
              <button v-if="isRateLimited(account) || isOverloaded(account)" @click="handleClearRateLimit(account); closeActionMenu()" class="flex w-full items-center gap-2 px-4 py-2 text-sm text-amber-600 hover:bg-gray-100 dark:text-amber-400 dark:hover:bg-dark-700">
                <svg class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 8v4l3 3m6-3a9 9 0 11-18 0 9 9 0 0118 0z" /></svg>
                {{ t('admin.accounts.clearRateLimit') }}
              </button>
            </template>
          </template>
        </div>
      </div>
    </Teleport>
  </AppLayout>
</template>

<script setup lang="ts">
import { ref, reactive, computed, onMounted, onUnmounted, type ComponentPublicInstance } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { useAuthStore } from '@/stores/auth'
import { useOnboardingStore } from '@/stores/onboarding'
import { adminAPI } from '@/api/admin'
import type { Account, Proxy, Group } from '@/types'
import type { Column } from '@/components/common/types'
import AppLayout from '@/components/layout/AppLayout.vue'
import TablePageLayout from '@/components/layout/TablePageLayout.vue'
import DataTable from '@/components/common/DataTable.vue'
import Pagination from '@/components/common/Pagination.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import Select from '@/components/common/Select.vue'
import {
  CreateAccountModal,
  EditAccountModal,
  BulkEditAccountModal,
  ReAuthAccountModal,
  AccountStatsModal,
  SyncFromCrsModal
} from '@/components/account'
import AccountStatusIndicator from '@/components/account/AccountStatusIndicator.vue'
import AccountUsageCell from '@/components/account/AccountUsageCell.vue'
import AccountTodayStatsCell from '@/components/account/AccountTodayStatsCell.vue'
import AccountTestModal from '@/components/account/AccountTestModal.vue'
import GroupBadge from '@/components/common/GroupBadge.vue'
import PlatformTypeBadge from '@/components/common/PlatformTypeBadge.vue'
import { formatRelativeTime } from '@/utils/format'

const { t } = useI18n()
const appStore = useAppStore()
const authStore = useAuthStore()
const onboardingStore = useOnboardingStore()

// Table columns
const columns = computed<Column[]>(() => {
  const cols: Column[] = [
    { key: 'select', label: '', sortable: false },
    { key: 'name', label: t('admin.accounts.columns.name'), sortable: true },
    { key: 'platform_type', label: t('admin.accounts.columns.platformType'), sortable: false },
    { key: 'concurrency', label: t('admin.accounts.columns.concurrencyStatus'), sortable: false },
    { key: 'status', label: t('admin.accounts.columns.status'), sortable: true },
    { key: 'schedulable', label: t('admin.accounts.columns.schedulable'), sortable: true },
    { key: 'today_stats', label: t('admin.accounts.columns.todayStats'), sortable: false }
  ]

  // 简易模式下不显示分组列
  if (!authStore.isSimpleMode) {
    cols.push({ key: 'groups', label: t('admin.accounts.columns.groups'), sortable: false })
  }

  cols.push(
    { key: 'usage', label: t('admin.accounts.columns.usageWindows'), sortable: false },
    { key: 'priority', label: t('admin.accounts.columns.priority'), sortable: true },
    { key: 'last_used_at', label: t('admin.accounts.columns.lastUsed'), sortable: true },
    { key: 'actions', label: t('admin.accounts.columns.actions'), sortable: false }
  )

  return cols
})

// Filter options
const platformOptions = computed(() => [
  { value: '', label: t('admin.accounts.allPlatforms') },
  { value: 'anthropic', label: t('admin.accounts.platforms.anthropic') },
  { value: 'openai', label: t('admin.accounts.platforms.openai') },
  { value: 'gemini', label: t('admin.accounts.platforms.gemini') },
  { value: 'antigravity', label: t('admin.accounts.platforms.antigravity') }
])

const typeOptions = computed(() => [
  { value: '', label: t('admin.accounts.allTypes') },
  { value: 'oauth', label: t('admin.accounts.oauthType') },
  { value: 'setup-token', label: t('admin.accounts.setupToken') },
  { value: 'apikey', label: t('admin.accounts.apiKey') }
])

const statusOptions = computed(() => [
  { value: '', label: t('admin.accounts.allStatus') },
  { value: 'active', label: t('common.active') },
  { value: 'inactive', label: t('common.inactive') },
  { value: 'error', label: t('common.error') }
])

// State
const accounts = ref<Account[]>([])
const proxies = ref<Proxy[]>([])
const groups = ref<Group[]>([])
const loading = ref(false)
const searchQuery = ref('')
const filters = reactive({
  platform: '',
  type: '',
  status: ''
})
const pagination = reactive({
  page: 1,
  page_size: 20,
  total: 0,
  pages: 0
})
let abortController: AbortController | null = null

// Modal states
const showCreateModal = ref(false)
const showEditModal = ref(false)
const showReAuthModal = ref(false)
const showDeleteDialog = ref(false)
const showBulkDeleteDialog = ref(false)
const showTestModal = ref(false)
const showStatsModal = ref(false)
const showCrsSyncModal = ref(false)
const showBulkEditModal = ref(false)
const editingAccount = ref<Account | null>(null)
const reAuthAccount = ref<Account | null>(null)
const deletingAccount = ref<Account | null>(null)
const testingAccount = ref<Account | null>(null)
const statsAccount = ref<Account | null>(null)
const togglingSchedulable = ref<number | null>(null)
const bulkDeleting = ref(false)

// Action Menu State
const activeMenuId = ref<number | null>(null)
const menuPosition = ref<{ top: number; left: number } | null>(null)
const actionButtonRefs = ref<Map<number, HTMLElement>>(new Map())

const setActionButtonRef = (accountId: number, el: Element | ComponentPublicInstance | null) => {
  if (el instanceof HTMLElement) {
    actionButtonRefs.value.set(accountId, el)
  } else {
    actionButtonRefs.value.delete(accountId)
  }
}

const openActionMenu = (account: Account) => {
  if (activeMenuId.value === account.id) {
    closeActionMenu()
  } else {
    const buttonEl = actionButtonRefs.value.get(account.id)
    if (buttonEl) {
      const rect = buttonEl.getBoundingClientRect()
      // Position menu to the left of the button, slightly below
      menuPosition.value = {
        top: rect.bottom + 4,
        left: rect.right - 208 // w-52 is 208px
      }
    }
    activeMenuId.value = account.id
  }
}

const closeActionMenu = () => {
  activeMenuId.value = null
  menuPosition.value = null
}

// Close menu when clicking outside
const handleClickOutside = (event: MouseEvent) => {
  const target = event.target as HTMLElement
  if (!target.closest('.action-menu-trigger') && !target.closest('.action-menu-content')) {
    closeActionMenu()
  }
}

// Bulk selection
const selectedAccountIds = ref<number[]>([])
const selectCurrentPageAccounts = () => {
  const pageIds = accounts.value.map((account) => account.id)
  const merged = new Set([...selectedAccountIds.value, ...pageIds])
  selectedAccountIds.value = Array.from(merged)
}

// Rate limit / Overload helpers
const isRateLimited = (account: Account): boolean => {
  if (!account.rate_limit_reset_at) return false
  return new Date(account.rate_limit_reset_at) > new Date()
}

const isOverloaded = (account: Account): boolean => {
  if (!account.overload_until) return false
  return new Date(account.overload_until) > new Date()
}

// Data loading
const loadAccounts = async () => {
  abortController?.abort()
  const currentAbortController = new AbortController()
  abortController = currentAbortController
  loading.value = true
  try {
    const response = await adminAPI.accounts.list(pagination.page, pagination.page_size, {
      platform: filters.platform || undefined,
      type: filters.type || undefined,
      status: filters.status || undefined,
      search: searchQuery.value || undefined
    }, {
      signal: currentAbortController.signal
    })
    if (currentAbortController.signal.aborted) return
    accounts.value = response.items
    pagination.total = response.total
    pagination.pages = response.pages
  } catch (error) {
    const errorInfo = error as { name?: string; code?: string }
    if (errorInfo?.name === 'AbortError' || errorInfo?.name === 'CanceledError' || errorInfo?.code === 'ERR_CANCELED') {
      return
    }
    appStore.showError(t('admin.accounts.failedToLoad'))
    console.error('Error loading accounts:', error)
  } finally {
    if (abortController === currentAbortController) {
      loading.value = false
    }
  }
}

const loadProxies = async () => {
  try {
    proxies.value = await adminAPI.proxies.getAllWithCount()
  } catch (error) {
    console.error('Error loading proxies:', error)
  }
}

const loadGroups = async () => {
  try {
    // Load groups for all platforms to support both Anthropic and OpenAI accounts
    groups.value = await adminAPI.groups.getAll()
  } catch (error) {
    console.error('Error loading groups:', error)
  }
}

// Search handling
let searchTimeout: ReturnType<typeof setTimeout>
const handleSearch = () => {
  clearTimeout(searchTimeout)
  searchTimeout = setTimeout(() => {
    pagination.page = 1
    loadAccounts()
  }, 300)
}

// Pagination
const handlePageChange = (page: number) => {
  pagination.page = page
  loadAccounts()
}

const handlePageSizeChange = (pageSize: number) => {
  pagination.page_size = pageSize
  pagination.page = 1
  loadAccounts()
}

const handleCrsSynced = () => {
  showCrsSyncModal.value = false
  loadAccounts()
}

// Edit modal
const handleEdit = (account: Account) => {
  editingAccount.value = account
  showEditModal.value = true
}

const closeEditModal = () => {
  showEditModal.value = false
  editingAccount.value = null
}

// Re-Auth modal
const handleReAuth = (account: Account) => {
  reAuthAccount.value = account
  showReAuthModal.value = true
}

const closeReAuthModal = () => {
  showReAuthModal.value = false
  reAuthAccount.value = null
}

// Token refresh
const handleRefreshToken = async (account: Account) => {
  try {
    await adminAPI.accounts.refreshCredentials(account.id)
    appStore.showSuccess(t('admin.accounts.tokenRefreshed'))
    loadAccounts()
  } catch (error: any) {
    appStore.showError(error.response?.data?.detail || t('admin.accounts.failedToRefresh'))
    console.error('Error refreshing token:', error)
  }
}

// Delete
const handleDelete = (account: Account) => {
  deletingAccount.value = account
  showDeleteDialog.value = true
}

const confirmDelete = async () => {
  if (!deletingAccount.value) return

  try {
    await adminAPI.accounts.delete(deletingAccount.value.id)
    appStore.showSuccess(t('admin.accounts.accountDeleted'))
    showDeleteDialog.value = false
    deletingAccount.value = null
    loadAccounts()
  } catch (error: any) {
    appStore.showError(error.response?.data?.detail || t('admin.accounts.failedToDelete'))
    console.error('Error deleting account:', error)
  }
}

const handleBulkDelete = () => {
  if (selectedAccountIds.value.length === 0) return
  showBulkDeleteDialog.value = true
}

const confirmBulkDelete = async () => {
  if (bulkDeleting.value || selectedAccountIds.value.length === 0) return

  bulkDeleting.value = true
  const ids = [...selectedAccountIds.value]
  try {
    const results = await Promise.allSettled(ids.map((id) => adminAPI.accounts.delete(id)))
    const success = results.filter((result) => result.status === 'fulfilled').length
    const failed = results.length - success

    if (failed === 0) {
      appStore.showSuccess(t('admin.accounts.bulkDeleteSuccess', { count: success }))
    } else {
      appStore.showError(t('admin.accounts.bulkDeletePartial', { success, failed }))
    }

    showBulkDeleteDialog.value = false
    selectedAccountIds.value = []
    loadAccounts()
  } catch (error: any) {
    appStore.showError(error.response?.data?.detail || t('admin.accounts.bulkDeleteFailed'))
    console.error('Error deleting accounts:', error)
  } finally {
    bulkDeleting.value = false
  }
}

// Clear rate limit
const handleClearRateLimit = async (account: Account) => {
  try {
    await adminAPI.accounts.clearRateLimit(account.id)
    appStore.showSuccess(t('admin.accounts.rateLimitCleared'))
    loadAccounts()
  } catch (error: any) {
    appStore.showError(error.response?.data?.detail || t('admin.accounts.failedToClearRateLimit'))
    console.error('Error clearing rate limit:', error)
  }
}

// Reset account status (clear error and rate limit)
const handleResetStatus = async (account: Account) => {
  try {
    // Clear error status
    await adminAPI.accounts.clearError(account.id)
    // Also clear rate limit if exists
    if (isRateLimited(account) || isOverloaded(account)) {
      await adminAPI.accounts.clearRateLimit(account.id)
    }
    appStore.showSuccess(t('admin.accounts.statusReset'))
    loadAccounts()
  } catch (error: any) {
    appStore.showError(error.response?.data?.detail || t('admin.accounts.failedToResetStatus'))
    console.error('Error resetting account status:', error)
  }
}

// Toggle schedulable
const handleToggleSchedulable = async (account: Account) => {
  togglingSchedulable.value = account.id
  try {
    const updatedAccount = await adminAPI.accounts.setSchedulable(account.id, !account.schedulable)
    const index = accounts.value.findIndex((a) => a.id === account.id)
    if (index !== -1) {
      accounts.value[index] = updatedAccount
    }
    appStore.showSuccess(
      updatedAccount.schedulable
        ? t('admin.accounts.schedulableEnabled')
        : t('admin.accounts.schedulableDisabled')
    )
  } catch (error: any) {
    appStore.showError(
      error.response?.data?.detail || t('admin.accounts.failedToToggleSchedulable')
    )
    console.error('Error toggling schedulable:', error)
  } finally {
    togglingSchedulable.value = null
  }
}

// Test modal
const handleTest = (account: Account) => {
  testingAccount.value = account
  showTestModal.value = true
}

const closeTestModal = () => {
  showTestModal.value = false
  testingAccount.value = null
}

// Stats modal
const handleViewStats = (account: Account) => {
  statsAccount.value = account
  showStatsModal.value = true
}

const closeStatsModal = () => {
  showStatsModal.value = false
  statsAccount.value = null
}

// Bulk selection toggle
const toggleAccountSelection = (accountId: number) => {
  const index = selectedAccountIds.value.indexOf(accountId)
  if (index === -1) {
    selectedAccountIds.value.push(accountId)
  } else {
    selectedAccountIds.value.splice(index, 1)
  }
}

// Bulk update handler
const handleBulkUpdated = () => {
  showBulkEditModal.value = false
  selectedAccountIds.value = []
  loadAccounts()
}

// Initialize
onMounted(() => {
  loadAccounts()
  loadProxies()
  loadGroups()
  document.addEventListener('click', handleClickOutside)
})

onUnmounted(() => {
  abortController?.abort()
  abortController = null
  document.removeEventListener('click', handleClickOutside)
})
</script>
