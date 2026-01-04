<template>
  <AppLayout>
    <TablePageLayout>
      <template #filters>
        <div class="flex flex-col gap-4 lg:flex-row lg:items-center lg:justify-between">
          <div class="flex flex-1 flex-wrap items-center gap-3">
            <div class="w-64">
              <SearchInput v-model="params.search" :placeholder="t('admin.users.searchUsers')" @search="reload" />
            </div>
            <div class="w-32">
              <Select v-model="params.role" :options="[{ value: '', label: t('admin.users.allRoles') }, { value: 'admin', label: t('admin.users.admin') }, { value: 'user', label: t('admin.users.user') }]" @change="reload" />
            </div>
            <div class="w-32">
              <Select v-model="params.status" :options="[{ value: '', label: t('admin.users.allStatus') }, { value: 'active', label: t('common.active') }, { value: 'disabled', label: t('admin.users.disabled') }]" @change="reload" />
            </div>
          </div>
          <div class="flex items-center gap-3">
            <button @click="load" :disabled="loading" class="btn btn-secondary"><svg :class="['h-5 w-5', loading ? 'animate-spin' : '']" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path stroke-linecap="round" stroke-linejoin="round" d="M16.023 9.348h4.992v-.001M2.985 19.644v-4.992m0 0h4.992m-4.993 0l3.181 3.183a8.25 8.25 0 0013.803-3.7M4.031 9.865a8.25 8.25 0 0113.803-3.7l3.181 3.182m0-4.991v4.99" /></svg></button>
            <button @click="showCreateModal = true" class="btn btn-primary"><svg class="mr-2 h-5 w-5" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path stroke-linecap="round" stroke-linejoin="round" d="M12 4.5v15m7.5-7.5h-15" /></svg>{{ t('admin.users.createUser') }}</button>
          </div>
        </div>
      </template>

      <template #table>
        <DataTable :columns="columns" :data="users" :loading="loading">
          <template #cell-email="{ value }"><div class="flex items-center gap-2"><div class="flex h-8 w-8 items-center justify-center rounded-full bg-primary-100 font-medium text-primary-700"><span>{{ value.charAt(0).toUpperCase() }}</span></div><span class="font-medium text-gray-900 dark:text-white">{{ value }}</span></div></template>
          <template #cell-role="{ value }"><span :class="['badge', value === 'admin' ? 'badge-purple' : 'badge-gray']">{{ t('admin.users.roles.' + value) }}</span></template>
          <template #cell-balance="{ value }"><span class="font-medium">${{ value.toFixed(2) }}</span></template>
          <template #cell-status="{ value }"><StatusBadge :status="value === 'disabled' ? 'inactive' : value" :label="t('admin.accounts.status.' + (value === 'disabled' ? 'inactive' : value))" /></template>
          <template #cell-actions="{ row }"><div class="flex gap-1"><button @click="handleEdit(row)" class="btn btn-sm btn-secondary">{{ t('common.edit') }}</button><button @click="openActionMenu(row, $event)" class="btn btn-sm btn-secondary">{{ t('common.more') }}</button></div></template>
        </DataTable>
      </template>

      <template #pagination>
        <Pagination v-if="pagination.total > 0" :page="pagination.page" :total="pagination.total" :page-size="pagination.page_size" @update:page="handlePageChange" />
      </template>
    </TablePageLayout>

    <Teleport to="body">
      <div v-if="activeMenuId !== null && menuPosition" class="action-menu-content fixed z-[9999] w-48 overflow-hidden rounded-xl bg-white shadow-lg ring-1 ring-black/5 dark:bg-dark-800" :style="{ top: menuPosition.top + 'px', left: menuPosition.left + 'px' }">
        <div class="py-1">
          <template v-for="user in users" :key="user.id">
            <template v-if="user.id === activeMenuId">
              <button @click="handleViewApiKeys(user); closeActionMenu()" class="flex w-full items-center gap-2 px-4 py-2 text-sm hover:bg-gray-100">{{ t('admin.users.apiKeys') }}</button>
              <button @click="handleAllowedGroups(user); closeActionMenu()" class="flex w-full items-center gap-2 px-4 py-2 text-sm hover:bg-gray-100">{{ t('admin.users.groups') }}</button>
              <button @click="handleDeposit(user); closeActionMenu()" class="flex w-full items-center gap-2 px-4 py-2 text-sm hover:bg-gray-100 text-emerald-600">{{ t('admin.users.deposit') }}</button>
              <button @click="handleWithdraw(user); closeActionMenu()" class="flex w-full items-center gap-2 px-4 py-2 text-sm hover:bg-gray-100 text-amber-600">{{ t('admin.users.withdraw') }}</button>
              <button v-if="user.role !== 'admin'" @click="handleToggleStatus(user); closeActionMenu()" class="flex w-full items-center gap-2 px-4 py-2 text-sm hover:bg-gray-100">{{ user.status === 'active' ? t('admin.users.disable') : t('admin.users.enable') }}</button>
              <button v-if="user.role !== 'admin'" @click="handleDelete(user); closeActionMenu()" class="flex w-full items-center gap-2 px-4 py-2 text-sm text-red-600 hover:bg-red-50">{{ t('common.delete') }}</button>
            </template>
          </template>
        </div>
      </div>
    </Teleport>

    <ConfirmDialog :show="showDeleteDialog" :title="t('admin.users.deleteUser')" :message="t('admin.users.deleteConfirm', { email: deletingUser?.email })" :danger="true" @confirm="confirmDelete" @cancel="showDeleteDialog = false" />
    <UserCreateModal :show="showCreateModal" @close="showCreateModal = false" @success="reload" />
    <UserEditModal :show="showEditModal" :user="editingUser" @close="closeEditModal" @success="load" />
    <UserApiKeysModal :show="showApiKeysModal" :user="viewingUser" @close="closeApiKeysModal" />
    <UserAllowedGroupsModal :show="showAllowedGroupsModal" :user="allowedGroupsUser" @close="closeAllowedGroupsModal" @success="load" />
    <UserBalanceModal :show="showBalanceModal" :user="balanceUser" :operation="balanceOperation" @close="closeBalanceModal" @success="load" />
  </AppLayout>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'; import { useAppStore } from '@/stores/app'
import { adminAPI } from '@/api/admin'; import { useTableLoader } from '@/composables/useTableLoader'
import type { User } from '@/types'
import AppLayout from '@/components/layout/AppLayout.vue'; import TablePageLayout from '@/components/layout/TablePageLayout.vue'
import DataTable from '@/components/common/DataTable.vue'; import Pagination from '@/components/common/Pagination.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'; import Select from '@/components/common/Select.vue'
import SearchInput from '@/components/common/SearchInput.vue'; import StatusBadge from '@/components/common/StatusBadge.vue'
import UserCreateModal from '@/components/admin/user/UserCreateModal.vue'
import UserEditModal from '@/components/admin/user/UserEditModal.vue'
import UserApiKeysModal from '@/components/admin/user/UserApiKeysModal.vue'
import UserAllowedGroupsModal from '@/components/admin/user/UserAllowedGroupsModal.vue'
import UserBalanceModal from '@/components/admin/user/UserBalanceModal.vue'

const { t } = useI18n(); const appStore = useAppStore()
const { items: users, loading, params, pagination, load, reload, handlePageChange } = useTableLoader<User, any>({ fetchFn: adminAPI.users.list, initialParams: { role: '', status: '', search: '' } })

const showCreateModal = ref(false); const showEditModal = ref(false); const showDeleteDialog = ref(false); const showApiKeysModal = ref(false)
const editingUser = ref<User | null>(null); const deletingUser = ref<User | null>(null); const viewingUser = ref<User | null>(null)
const activeMenuId = ref<number | null>(null); const menuPosition = ref<{ top: number; left: number } | null>(null)
const showAllowedGroupsModal = ref(false); const allowedGroupsUser = ref<User | null>(null); const showBalanceModal = ref(false); const balanceUser = ref<User | null>(null); const balanceOperation = ref<'add' | 'subtract'>('add')
const columns = computed(() => [{ key: 'email', label: t('admin.users.columns.user'), sortable: true }, { key: 'role', label: t('admin.users.columns.role'), sortable: true }, { key: 'balance', label: t('admin.users.columns.balance'), sortable: true }, { key: 'status', label: t('admin.users.columns.status'), sortable: true }, { key: 'actions', label: t('admin.users.columns.actions') }])

const handleEdit = (u: User) => { editingUser.value = u; showEditModal.value = true }
const closeEditModal = () => { showEditModal.value = false; editingUser.value = null }
const handleViewApiKeys = (u: User) => { viewingUser.value = u; showApiKeysModal.value = true }
const closeApiKeysModal = () => { showApiKeysModal.value = false; viewingUser.value = null }
const handleAllowedGroups = (u: User) => { allowedGroupsUser.value = u; showAllowedGroupsModal.value = true }
const closeAllowedGroupsModal = () => { showAllowedGroupsModal.value = false; allowedGroupsUser.value = null }
const handleDelete = (u: User) => { deletingUser.value = u; showDeleteDialog.value = true }
const confirmDelete = async () => { if (!deletingUser.value) return; try { await adminAPI.users.delete(deletingUser.value.id); appStore.showSuccess(t('common.success')); showDeleteDialog.value = false; reload() } catch {} }
const handleDeposit = (u: User) => { balanceUser.value = u; balanceOperation.value = 'add'; showBalanceModal.value = true }
const handleWithdraw = (u: User) => { balanceUser.value = u; balanceOperation.value = 'subtract'; showBalanceModal.value = true }
const closeBalanceModal = () => { showBalanceModal.value = false; balanceUser.value = null }
const handleToggleStatus = async (user: User) => { const next = user.status === 'active' ? 'disabled' : 'active'; try { await adminAPI.users.toggleStatus(user.id, next as any); appStore.showSuccess(t('common.success')); load() } catch {} }
const openActionMenu = (u: User, e: MouseEvent) => { if (activeMenuId.value === u.id) { activeMenuId.value = null; menuPosition.value = null } else { activeMenuId.value = u.id; menuPosition.value = { top: e.clientY, left: e.clientX - 150 } } }
const closeActionMenu = () => { activeMenuId.value = null; menuPosition.value = null }
onMounted(load)
</script>
