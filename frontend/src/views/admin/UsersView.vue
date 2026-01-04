<template>
  <AppLayout>
    <TablePageLayout>
      <template #filters>
        <div class="flex flex-col gap-4 lg:flex-row lg:items-center lg:justify-between">
          <div class="flex flex-1 flex-wrap items-center gap-3">
            <div class="relative w-64">
              <svg class="absolute left-3 top-1/2 h-5 w-5 -translate-y-1/2 text-gray-400" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" d="M21 21l-5.197-5.197m0 0A7.5 7.5 0 105.196 5.196a7.5 7.5 0 0010.607 10.607z" /></svg>
              <input v-model="searchQuery" type="text" :placeholder="t('admin.users.searchUsers')" class="input pl-10" @input="handleSearch" />
            </div>
            <div v-if="visibleFilters.has('role')" class="w-32">
              <Select v-model="filters.role" :options="[{ value: '', label: t('admin.users.allRoles') }, { value: 'admin', label: t('admin.users.admin') }, { value: 'user', label: t('admin.users.user') }]" @change="applyFilter" />
            </div>
            <div v-if="visibleFilters.has('status')" class="w-32">
              <Select v-model="filters.status" :options="[{ value: '', label: t('admin.users.allStatus') }, { value: 'active', label: t('common.active') }, { value: 'disabled', label: t('admin.users.disabled') }]" @change="applyFilter" />
            </div>
            <template v-for="(value, attrId) in activeAttributeFilters" :key="attrId">
              <div v-if="visibleFilters.has(`attr_${attrId}`)" class="relative">
                <input v-if="['text', 'textarea', 'email', 'url', 'date'].includes(getAttributeDefinition(Number(attrId))?.type || 'text')" :value="value" @input="(e) => updateAttributeFilter(Number(attrId), (e.target as HTMLInputElement).value)" @keyup.enter="applyFilter" :placeholder="getAttributeDefinitionName(Number(attrId))" class="input w-36" />
                <input v-else-if="getAttributeDefinition(Number(attrId))?.type === 'number'" :value="value" type="number" @input="(e) => updateAttributeFilter(Number(attrId), (e.target as HTMLInputElement).value)" @keyup.enter="applyFilter" :placeholder="getAttributeDefinitionName(Number(attrId))" class="input w-32" />
                <template v-else-if="['select', 'multi_select'].includes(getAttributeDefinition(Number(attrId))?.type || '')">
                  <div class="w-36">
                    <Select :model-value="value" :options="[{ value: '', label: getAttributeDefinitionName(Number(attrId)) }, ...(getAttributeDefinition(Number(attrId))?.options?.map(o => ({ value: o.value, label: o.label })) || [])]" @update:model-value="(val) => { updateAttributeFilter(Number(attrId), String(val ?? '')); applyFilter() }" />
                  </div>
                </template>
              </div>
            </template>
          </div>
          <div class="flex items-center gap-3">
            <button @click="loadUsers" :disabled="loading" class="btn btn-secondary"><svg :class="['h-5 w-5', loading ? 'animate-spin' : '']" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path stroke-linecap="round" stroke-linejoin="round" d="M16.023 9.348h4.992v-.001M2.985 19.644v-4.992m0 0h4.992m-4.993 0l3.181 3.183a8.25 8.25 0 0013.803-3.7M4.031 9.865a8.25 8.25 0 0113.803-3.7l3.181 3.182m0-4.991v4.99" /></svg></button>
            <div class="relative" ref="filterDropdownRef">
              <button @click="showFilterDropdown = !showFilterDropdown" class="btn btn-secondary"><svg class="mr-1.5 h-4 w-4" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" d="M12 3c2.755 0 5.455.232 8.083.678.533.09.917.556.917 1.096v1.044a2.25 2.25 0 01-.659 1.591l-5.432 5.432a2.25 2.25 0 00-.659 1.591v2.927a2.25 2.25 0 01-1.244 2.013L9.75 21v-6.568a2.25 2.25 0 00-.659-1.591L3.659 7.409A2.25 2.25 0 013 5.818V4.774c0-.54.384-1.006.917-1.096A48.32 48.32 0 0112 3z" /></svg>{{ t('admin.users.filterSettings') }}</button>
              <div v-if="showFilterDropdown" class="absolute right-0 top-full z-50 mt-1 w-48 rounded-lg border bg-white py-1 shadow-lg dark:border-dark-600 dark:bg-dark-800">
                <button v-for="f in builtInFilters" :key="f.key" @click="toggleBuiltInFilter(f.key)" class="flex w-full items-center justify-between px-4 py-2 text-sm hover:bg-gray-100"><span>{{ f.name }}</span><svg v-if="visibleFilters.has(f.key)" class="h-4 w-4 text-primary-500" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2"><path stroke-linecap="round" stroke-linejoin="round" d="M5 13l4 4L19 7" /></svg></button>
                <div v-if="filterableAttributes.length > 0" class="my-1 border-t dark:border-dark-700"></div>
                <button v-for="a in filterableAttributes" :key="a.id" @click="toggleAttributeFilter(a)" class="flex w-full items-center justify-between px-4 py-2 text-sm hover:bg-gray-100"><span>{{ a.name }}</span><svg v-if="visibleFilters.has(`attr_${a.id}`)" class="h-4 w-4 text-primary-500" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2"><path stroke-linecap="round" stroke-linejoin="round" d="M5 13l4 4L19 7" /></svg></button>
              </div>
            </div>
            <button @click="showAttributesModal = true" class="btn btn-secondary"><svg class="mr-1.5 h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path stroke-linecap="round" stroke-linejoin="round" d="M9.594 3.94c.09-.542.56-.94 1.11-.94h2.593c.55 0 1.02.398 1.11.94l.213 1.281c.063.374.313.686.645.87.074.04.147.083.22.127.324.196.72.257 1.075.124l1.217-.456a1.125 1.125 0 011.37.49l1.296 2.247a1.125 1.125 0 01-.26 1.431l-1.003.827c-.293.24-.438.613-.431.992a6.759 6.759 0 010 .255c-.007.378.138.75.43.99l1.005.828c.424.35.534.954.26 1.43l-1.298 2.247a1.125 1.125 0 01-1.369.491l-1.217-.456c-.355-.133-.75-.072-1.076.124a6.57 6.57 0 01-.22.128c-.331.183-.581.495-.644.869l-.213 1.28c-.09.543-.56.941-1.11.941h-2.594c-.55 0-1.02-.398-1.11-.94l-.213-1.281c-.062-.374-.312-.686-.644-.87a6.52 6.52 0 01-.22-.127c-.325-.196-.72-.257-1.076-.124l-1.217.456a1.125 1.125 0 01-1.369-.49l-1.297-2.247a1.125 1.125 0 01.26-1.431l1.004-.827c.292-.24.437-.613.43-.992a6.932 6.932 0 010-.255c.007-.378-.138-.75-.43-.99l-1.004-.828a1.125 1.125 0 01-.26-1.43l1.297-2.247a1.125 1.125 0 011.37-.491l1.216.456c.356.133.751.072 1.076-.124.072-.044.146-.087.22-.128.332-.183.582-.495.644-.869l.214-1.281z" /><path stroke-linecap="round" stroke-linejoin="round" d="M15 12a3 3 0 11-6 0 3 3 0 016 0z" /></svg>{{ t('admin.users.attributes.configButton') }}</button>
            <button @click="showCreateModal = true" class="btn btn-primary"><svg class="mr-2 h-5 w-5" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path stroke-linecap="round" stroke-linejoin="round" d="M12 4.5v15m7.5-7.5h-15" /></svg>{{ t('admin.users.createUser') }}</button>
          </div>
        </div>
      </template>

      <template #table>
        <DataTable :columns="columns" :data="users" :loading="loading" :actions-count="7">
          <template #cell-email="{ value }"><div class="flex items-center gap-2"><div class="flex h-8 w-8 items-center justify-center rounded-full bg-primary-100 font-medium text-primary-700"><span>{{ value.charAt(0).toUpperCase() }}</span></div><span class="font-medium text-gray-900 dark:text-white">{{ value }}</span></div></template>
          <template #cell-role="{ value }"><span :class="['badge', value === 'admin' ? 'badge-purple' : 'badge-gray']">{{ t('admin.users.roles.' + value) }}</span></template>
          <template #cell-balance="{ value }"><span class="font-medium">${{ value.toFixed(2) }}</span></template>
          <template #cell-status="{ value }"><div class="flex items-center gap-1.5"><span :class="['h-2 w-2 rounded-full', value === 'active' ? 'bg-green-500' : 'bg-red-500']"></span><span class="text-sm">{{ t('admin.accounts.status.' + (value === 'disabled' ? 'inactive' : value)) }}</span></div></template>
          <template #cell-created_at="{ value }"><span class="text-sm text-gray-500">{{ formatDateTime(value) }}</span></template>
          <template #cell-actions="{ row }"><div class="flex items-center gap-1"><button @click="handleEdit(row)" class="flex h-8 w-8 items-center justify-center rounded-lg text-gray-500 hover:bg-gray-100"><svg class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path stroke-linecap="round" stroke-linejoin="round" d="M16.862 4.487l1.687-1.688a1.875 1.875 0 112.652 2.652L10.582 16.07a4.5 4.5 0 01-1.897 1.13L6 18l.8-2.685a4.5 4.5 0 011.13-1.897l8.932-8.931zm0 0L19.5 7.125M18 14v4.75A2.25 2.25 0 0115.75 21H5.25A2.25 2.25 0 013 18.75V8.25A2.25 2.25 0 015.25 6H10" /></svg></button><button :ref="(el) => setActionButtonRef(row.id, el)" @click="openActionMenu(row)" class="action-menu-trigger flex h-8 w-8 items-center justify-center rounded-lg text-gray-500 hover:bg-gray-100"><svg class="h-5 w-5" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path stroke-linecap="round" stroke-linejoin="round" d="M6.75 12a.75.75 0 11-1.5 0 .75.75 0 011.5 0zM12.75 12a.75.75 0 11-1.5 0 .75.75 0 011.5 0zM18.75 12a.75.75 0 11-1.5 0 .75.75 0 011.5 0z" /></svg></button></div></template>
        </DataTable>
      </template>

      <template #pagination>
        <Pagination v-if="pagination.total > 0" :page="pagination.page" :total="pagination.total" :page-size="pagination.page_size" @update:page="handlePageChange" @update:pageSize="handlePageSizeChange" />
      </template>
    </TablePageLayout>

    <Teleport to="body">
      <div v-if="activeMenuId !== null && menuPosition" class="action-menu-content fixed z-[9999] w-48 overflow-hidden rounded-xl bg-white shadow-lg ring-1 ring-black/5 dark:bg-dark-800" :style="{ top: menuPosition.top + 'px', left: menuPosition.left + 'px' }">
        <div class="py-1">
          <template v-for="user in users" :key="user.id">
            <template v-if="user.id === activeMenuId">
              <button @click="handleViewApiKeys(user); closeActionMenu()" class="flex w-full items-center gap-2 px-4 py-2 text-sm hover:bg-gray-100"><svg class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M15 7a2 2 0 012 2m4 0a6 6 0 01-7.743 5.743L11.536 16.207l-1.414 1.414a2 2 0 01-2.828 0l-1.414-1.414a2 2 0 010-2.828l-1.414-1.414a2 2 0 010-2.828l1.414-1.414L10.257 6.257A6 6 0 1121 11.257V11.257" /></svg>{{ t('admin.users.apiKeys') }}</button>
              <button @click="handleAllowedGroups(user); closeActionMenu()" class="flex w-full items-center gap-2 px-4 py-2 text-sm hover:bg-gray-100"><svg class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M17 20h5v-2a3 3 0 00-5.356-1.857M17 20H7m10 0v-2c0-.656-.126-1.283-.356-1.857M7 20H2v-2a3 3 0 015.356-1.857M7 20v-2c0-.656.126-1.283.356-1.857m0 0a5.002 5.002 0 019.288 0M15 7a3 3 0 11-6 0 3 3 0 016 0zm6 3a2 2 0 11-4 0 2 2 0 014 0zM7 10a2 2 0 11-4 0 2 2 0 014 0z" /></svg>{{ t('admin.users.groups') }}</button>
              <div class="my-1 border-t dark:border-dark-700"></div>
              <button @click="handleDeposit(user); closeActionMenu()" class="flex w-full items-center gap-2 px-4 py-2 text-sm hover:bg-gray-100 text-emerald-600"><svg class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 6v6m0 0v6m0-6h6m-6 0H6" /></svg>{{ t('admin.users.deposit') }}</button>
              <button @click="handleWithdraw(user); closeActionMenu()" class="flex w-full items-center gap-2 px-4 py-2 text-sm hover:bg-gray-100 text-amber-600"><svg class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M20 12H4" /></svg>{{ t('admin.users.withdraw') }}</button>
              <div class="my-1 border-t dark:border-dark-700"></div>
              <button v-if="user.role !== 'admin'" @click="handleToggleStatus(user); closeActionMenu()" class="flex w-full items-center gap-2 px-4 py-2 text-sm hover:bg-gray-100"><svg v-if="user.status === 'active'" class="h-4 w-4 text-orange-500" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M18.364 18.364A9 9 0 005.636 5.636m12.728 12.728A9 9 0 015.636 5.636m12.728 12.728L5.636 5.636" /></svg><svg v-else class="h-4 w-4 text-green-500" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 12l2 2 4-4m6 2a9 9 0 11-18 0 9 9 0 0118 0z" /></svg>{{ user.status === 'active' ? t('admin.users.disable') : t('admin.users.enable') }}</button>
              <button v-if="user.role !== 'admin'" @click="handleDelete(user); closeActionMenu()" class="flex w-full items-center gap-2 px-4 py-2 text-sm text-red-600 hover:bg-red-50"><svg class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16" /></svg>{{ t('common.delete') }}</button>
            </template>
          </template>
        </div>
      </div>
    </Teleport>

    <ConfirmDialog :show="showDeleteDialog" :title="t('admin.users.deleteUser')" :message="t('admin.users.deleteConfirm', { email: deletingUser?.email })" :confirm-text="t('common.delete')" :cancel-text="t('common.cancel')" :danger="true" @confirm="confirmDelete" @cancel="showDeleteDialog = false" />
    <UserCreateModal :show="showCreateModal" @close="showCreateModal = false" @success="loadUsers" />
    <UserEditModal :show="showEditModal" :user="editingUser" @close="closeEditModal" @success="loadUsers" />
    <UserApiKeysModal :show="showApiKeysModal" :user="viewingUser" @close="closeApiKeysModal" />
    <UserAllowedGroupsModal :show="showAllowedGroupsModal" :user="allowedGroupsUser" @close="closeAllowedGroupsModal" @success="loadUsers" />
    <UserBalanceModal :show="showBalanceModal" :user="balanceUser" :operation="balanceOperation" @close="closeBalanceModal" @success="loadUsers" />
    <UserAttributesConfigModal :show="showAttributesModal" @close="handleAttributesModalClose" />
  </AppLayout>
</template>

<script setup lang="ts">
import { ref, reactive, computed, onMounted, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { formatDateTime } from '@/utils/format'
import { adminAPI } from '@/api/admin'
import type { User, UserAttributeDefinition } from '@/types'
import type { BatchUserUsageStats } from '@/api/admin/dashboard'
import type { Column } from '@/components/common/types'
import AppLayout from '@/components/layout/AppLayout.vue'
import TablePageLayout from '@/components/layout/TablePageLayout.vue'
import DataTable from '@/components/common/DataTable.vue'
import Pagination from '@/components/common/Pagination.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import Select from '@/components/common/Select.vue'
import UserAttributesConfigModal from '@/components/user/UserAttributesConfigModal.vue'
import UserCreateModal from '@/components/admin/user/UserCreateModal.vue'
import UserEditModal from '@/components/admin/user/UserEditModal.vue'
import UserApiKeysModal from '@/components/admin/user/UserApiKeysModal.vue'
import UserAllowedGroupsModal from '@/components/admin/user/UserAllowedGroupsModal.vue'
import UserBalanceModal from '@/components/admin/user/UserBalanceModal.vue'

const { t } = useI18n(); const appStore = useAppStore()
const attributeDefinitions = ref<UserAttributeDefinition[]>([]); const userAttributeValues = ref<Record<number, Record<number, string>>>({}); const usageStats = ref<Record<string, BatchUserUsageStats>>({})
const users = ref<User[]>([]); const loading = ref(false); const searchQuery = ref('')
const filters = reactive({ role: '', status: '' }); const activeAttributeFilters = reactive<Record<number, string>>({})
const visibleFilters = reactive<Set<string>>(new Set()); const showFilterDropdown = ref(false); const showColumnDropdown = ref(false)
const filterDropdownRef = ref<HTMLElement | null>(null); const columnDropdownRef = ref<HTMLElement | null>(null)
const hiddenColumns = reactive<Set<string>>(new Set()); const pagination = reactive({ page: 1, page_size: 20, total: 0, pages: 0 })
let abortController: AbortController | null = null; let searchT: any = null

const showCreateModal = ref(false); const showEditModal = ref(false); const showDeleteDialog = ref(false); const showApiKeysModal = ref(false); const showAttributesModal = ref(false)
const editingUser = ref<User | null>(null); const deletingUser = ref<User | null>(null); const viewingUser = ref<User | null>(null)
const activeMenuId = ref<number | null>(null); const menuPosition = ref<{ top: number; left: number } | null>(null); const actionButtonRefs = ref<Map<number, HTMLElement>>(new Map())
const showAllowedGroupsModal = ref(false); const allowedGroupsUser = ref<User | null>(null); const showBalanceModal = ref(false); const balanceUser = ref<User | null>(null); const balanceOperation = ref<'add' | 'subtract'>('add')

const attributeColumns = computed<Column[]>(() => attributeDefinitions.value.filter(d => d.enabled).map(d => ({ key: `attr_${d.id}`, label: d.name, sortable: false })))
const allColumns = computed<Column[]>(() => [
  { key: 'email', label: t('admin.users.columns.user'), sortable: true },
  { key: 'username', label: t('admin.users.columns.username'), sortable: true },
  ...attributeColumns.value,
  { key: 'role', label: t('admin.users.columns.role'), sortable: true },
  { key: 'balance', label: t('admin.users.columns.balance'), sortable: true },
  { key: 'status', label: t('admin.users.columns.status'), sortable: true },
  { key: 'created_at', label: t('admin.users.columns.created'), sortable: true },
  { key: 'actions', label: t('admin.users.columns.actions'), sortable: false }
])
const toggleableColumns = computed(() => allColumns.value.filter(c => c.key !== 'email' && c.key !== 'actions'))
const columns = computed<Column[]>(() => allColumns.value.filter(c => c.key === 'email' || c.key === 'actions' || !hiddenColumns.has(c.key)))
const filterableAttributes = computed(() => attributeDefinitions.value.filter(d => d.enabled))
const builtInFilters = computed(() => [{ key: 'role', name: t('admin.users.columns.role') }, { key: 'status', name: t('admin.users.columns.status') }])

const getAttributeDefinition = (id: number) => attributeDefinitions.value.find(d => d.id === id)
const getAttributeDefinitionName = (id: number) => getAttributeDefinition(id)?.name || String(id)
const getAttributeValue = (userId: number, attrId: number): string => {
  const v = userAttributeValues.value[userId]?.[attrId]; if (!v) return '-'; const d = getAttributeDefinition(attrId); if (!d) return v
  if (d.type === 'multi_select') try { const a = JSON.parse(v); if(Array.isArray(a)) return a.map(x => d.options?.find(o => o.value === x)?.label || x).join(', ') } catch { return v }
  return d.options?.find(o => o.value === v)?.label || v
}

const loadSavedColumns = () => { try { const s = localStorage.getItem('user-hidden-columns'); if(s) JSON.parse(s).forEach((k:string) => hiddenColumns.add(k)); else ['username'].forEach(k => hiddenColumns.add(k)) } catch { hiddenColumns.add('username') } }
const saveColumnsToStorage = () => localStorage.setItem('user-hidden-columns', JSON.stringify([...hiddenColumns]))
const toggleColumn = (k: string) => { if(hiddenColumns.has(k)) hiddenColumns.delete(k); else hiddenColumns.add(k); saveColumnsToStorage() }
const isColumnVisible = (k: string) => !hiddenColumns.has(k)

const loadSavedFilters = () => {
  try { const v = localStorage.getItem('user-visible-filters'); if(v) JSON.parse(v).forEach((k:string) => visibleFilters.add(k))
    const val = localStorage.getItem('user-filter-values'); if(val) { const p = JSON.parse(val); filters.role = p.role || ''; filters.status = p.status || ''; if(p.attributes) Object.assign(activeAttributeFilters, p.attributes) }
  } catch {}
}
const saveFiltersToStorage = () => { localStorage.setItem('user-visible-filters', JSON.stringify([...visibleFilters])); localStorage.setItem('user-filter-values', JSON.stringify({ role: filters.role, status: filters.status, attributes: activeAttributeFilters })) }

const loadAttributeDefinitions = async () => { try { attributeDefinitions.value = await adminAPI.userAttributes.listEnabledDefinitions() } catch {} }
const handleAttributesModalClose = async () => { showAttributesModal.value = false; await loadAttributeDefinitions(); loadUsers() }

const loadUsers = async () => {
  abortController?.abort(); const c = new AbortController(); abortController = c; loading.value = true
  try {
    const af: any = {}; for(const [id, v] of Object.entries(activeAttributeFilters)) if(v) af[id] = v
    const res = await adminAPI.users.list(pagination.page, pagination.page_size, { role: filters.role as any, status: filters.status as any, search: searchQuery.value || undefined, attributes: Object.keys(af).length > 0 ? af : undefined }, { signal: c.signal })
    if(c.signal.aborted) return; users.value = res.items; pagination.total = res.total; pagination.pages = res.pages
    if(res.items.length > 0) { const ids = res.items.map(u => u.id); adminAPI.dashboard.getBatchUsersUsage(ids).then(r => { if(!c.signal.aborted) usageStats.value = r.stats }); if(attributeDefinitions.value.length > 0) adminAPI.userAttributes.getBatchUserAttributes(ids).then(r => { if(!c.signal.aborted) userAttributeValues.value = r.attributes }) }
  } catch(e:any) { if(e.name !== 'AbortError' && e.code !== 'ERR_CANCELED') appStore.showError(t('admin.users.failedToLoad')) }
  finally { if (abortController === c) loading.value = false }
}

const handleSearch = () => { clearTimeout(searchT); searchT = setTimeout(() => { pagination.page = 1; loadUsers() }, 300) }
const handlePageChange = (p:number) => { pagination.page = p; loadUsers() }
const handlePageSizeChange = (s:number) => { pagination.page_size = s; pagination.page = 1; loadUsers() }
const toggleBuiltInFilter = (k:string) => { if(visibleFilters.has(k)) { visibleFilters.delete(k); if(k === 'role') filters.role = ''; if(k === 'status') filters.status = '' } else visibleFilters.add(k); saveFiltersToStorage(); loadUsers() }
const toggleAttributeFilter = (a:any) => { const k = `attr_${a.id}`; if(visibleFilters.has(k)) { visibleFilters.delete(k); delete activeAttributeFilters[a.id] } else { visibleFilters.add(k); activeAttributeFilters[a.id] = '' }; saveFiltersToStorage(); loadUsers() }
const updateAttributeFilter = (id:number, v:string) => activeAttributeFilters[id] = v
const applyFilter = () => { saveFiltersToStorage(); loadUsers() }

const handleEdit = (u:User) => { editingUser.value = u; showEditModal.value = true }
const closeEditModal = () => { showEditModal.value = false; editingUser.value = null }
const handleToggleStatus = async (user: User) => {
  const next = user.status === 'active' ? 'disabled' : 'active'
  try { await adminAPI.users.toggleStatus(user.id, next as any); appStore.showSuccess(t('common.success')); loadUsers() } catch {}
}
const handleViewApiKeys = (u:User) => { viewingUser.value = u; showApiKeysModal.value = true }
const closeApiKeysModal = () => { showApiKeysModal.value = false; viewingUser.value = null }
const handleAllowedGroups = (u:User) => { allowedGroupsUser.value = u; showAllowedGroupsModal.value = true }
const closeAllowedGroupsModal = () => { showAllowedGroupsModal.value = false; allowedGroupsUser.value = null }
const handleDelete = (u:User) => { deletingUser.value = u; showDeleteDialog.value = true }
const confirmDelete = async () => { if(!deletingUser.value) return; try { await adminAPI.users.delete(deletingUser.value.id); appStore.showSuccess(t('common.success')); showDeleteDialog.value = false; loadUsers() } catch {} }
const handleDeposit = (u:User) => { balanceUser.value = u; balanceOperation.value = 'add'; showBalanceModal.value = true }
const handleWithdraw = (u:User) => { balanceUser.value = u; balanceOperation.value = 'subtract'; showBalanceModal.value = true }
const closeBalanceModal = () => { showBalanceModal.value = false; balanceUser.value = null }

const setActionButtonRef = (id:number, el:any) => { if(el instanceof HTMLElement) actionButtonRefs.value.set(id, el); else actionButtonRefs.value.delete(id) }
const openActionMenu = (u:User) => {
  if(activeMenuId.value === u.id) closeActionMenu()
  else {
    const b = actionButtonRefs.value.get(u.id)
    if(b) {
      const r = b.getBoundingClientRect()
      menuPosition.value = { top: Math.min(r.bottom + 4, window.innerHeight - 250), left: Math.min(r.right - 192, window.innerWidth - 200) }
    }
    activeMenuId.value = u.id
  }
}
const closeActionMenu = () => { activeMenuId.value = null; menuPosition.value = null }
const handleClickOutside = (e:MouseEvent) => {
  const t = e.target as HTMLElement
  if (!t.closest('.action-menu-trigger') && !t.closest('.action-menu-content')) closeActionMenu()
  if (filterDropdownRef.value && !filterDropdownRef.value.contains(t)) showFilterDropdown.value = false
  if (columnDropdownRef.value && !columnDropdownRef.value.contains(t)) showColumnDropdown.value = false
}

onMounted(async () => { await loadAttributeDefinitions(); loadSavedFilters(); loadSavedColumns(); loadUsers(); document.addEventListener('click', handleClickOutside) })
onUnmounted(() => { abortController?.abort(); document.removeEventListener('click', handleClickOutside) })
</script>