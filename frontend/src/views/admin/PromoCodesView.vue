<template>
  <AppLayout>
    <TablePageLayout>
      <template #filters>
        <AdminPageHeader
          class="mb-4"
          :title="t('admin.promo.title')"
          :description="t('admin.promo.description')"
        >
          <template #actions>
            <UButton icon="i-lucide-plus" :label="t('admin.promo.createCode')" @click="showCreateDialog = true" />
          </template>
        </AdminPageHeader>
        <!-- Toolbar row (template customers page) -->
        <PageToolbar>
          <template #left>
            <UInput
              v-model="searchQuery"
              icon="i-lucide-search"
              :placeholder="t('admin.promo.searchCodes')"
              class="w-56 sm:w-64"
              @update:model-value="handleSearch"
            />
            <Select v-model="filters.status" :options="filterStatusOptions" class="w-36" @change="loadCodes" />
          </template>
          <template #right>
            <UButton
              color="neutral"
              variant="outline"
              icon="i-lucide-refresh-cw"
              square
              :disabled="loading"
              :ui="{ leadingIcon: loading ? 'animate-spin' : '' }"
              :title="t('common.refresh')"
              :aria-label="t('common.refresh')"
              @click="loadCodes"
            />
            <UDropdownMenu :items="columnMenuItems" :content="{ align: 'end' }">
              <UButton color="neutral" variant="outline" trailing-icon="i-lucide-settings-2" :aria-label="t('admin.promo.displayColumns')">
                <span class="hidden sm:inline">{{ t('admin.promo.displayColumns') }}</span>
              </UButton>
            </UDropdownMenu>
          </template>
        </PageToolbar>
      </template>

      <template #table>
        <DataTable
          :columns="visibleColumns"
          :data="codes"
          :loading="loading"
          :server-side-sort="true"
          default-sort-key="created_at"
          default-sort-order="desc"
          @sort="handleSort"
        >
          <template #cell-code="{ value }">
            <div class="flex items-center gap-1">
              <code class="font-mono text-sm text-highlighted">{{ value }}</code>
              <UButton
                :color="copiedCode === value ? 'success' : 'neutral'"
                variant="ghost"
                size="xs"
                square
                :icon="copiedCode === value ? 'i-lucide-check' : 'i-lucide-copy'"
                :title="copiedCode === value ? t('admin.promo.copied') : t('keys.copyToClipboard')"
                :aria-label="copiedCode === value ? t('admin.promo.copied') : t('keys.copyToClipboard')"
                @click="copyToClipboard(value)"
              />
            </div>
          </template>

          <template #cell-bonus_amount="{ value }">
            <span class="font-medium tabular-nums text-highlighted">${{ value.toFixed(2) }}</span>
          </template>

          <template #cell-usage="{ row }">
            <span class="tabular-nums text-default">{{ row.used_count }} / {{ row.max_uses === 0 ? '∞' : row.max_uses }}</span>
          </template>

          <template #cell-status="{ value, row }">
            <UBadge :color="getStatusColor(value, row)" variant="subtle" :label="getStatusLabel(value, row)" />
          </template>

          <template #cell-expires_at="{ value }">
            <span class="text-muted">{{ value ? formatDateTime(value) : t('admin.promo.neverExpires') }}</span>
          </template>

          <template #cell-created_at="{ value }">
            <span class="text-muted">{{ formatDateTime(value) }}</span>
          </template>

          <template #cell-actions="{ row }">
            <div class="flex items-center justify-end gap-0.5">
              <AdminRowAction :label="t('admin.promo.copyRegisterLink')" icon="link" tone="success" @click="copyRegisterLink(row)" />
              <AdminRowAction :label="t('common.edit')" icon="edit" @click="handleEdit(row)" />
              <UDropdownMenu :items="rowMenuItems(row)" :content="{ align: 'end' }">
                <AdminRowAction :label="t('common.more')" icon="more" />
              </UDropdownMenu>
            </div>
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

    <!-- Create Dialog -->
    <BaseDialog :show="showCreateDialog" :title="t('admin.promo.createCode')" width="normal" @close="showCreateDialog = false">
      <UForm id="create-promo-form" :state="createForm" class="space-y-4" @submit="handleCreate">
        <UFormField :label="t('admin.promo.code')" :hint="t('admin.promo.autoGenerate')">
          <UInput v-model="createForm.code" class="w-full" :ui="{ base: 'font-mono uppercase' }" :placeholder="t('admin.promo.codePlaceholder')" />
        </UFormField>
        <UFormField :label="t('admin.promo.bonusAmount')" required>
          <UInput v-model.number="createForm.bonus_amount" type="number" step="0.01" min="0" required class="w-full" />
        </UFormField>
        <UFormField :label="t('admin.promo.maxUses')" :hint="t('admin.promo.zeroUnlimited')">
          <UInput v-model.number="createForm.max_uses" type="number" min="0" class="w-full" />
        </UFormField>
        <UFormField :label="t('admin.promo.expiresAt')" :hint="t('common.optional')">
          <UInput v-model="createForm.expires_at_str" type="datetime-local" class="w-full" />
        </UFormField>
        <UFormField :label="t('admin.promo.notes')" :hint="t('common.optional')">
          <UTextarea v-model="createForm.notes" :rows="2" class="w-full" :placeholder="t('admin.promo.notesPlaceholder')" />
        </UFormField>
      </UForm>
      <template #footer>
        <div class="flex justify-end gap-2">
          <UButton color="neutral" variant="subtle" :label="t('common.cancel')" @click="showCreateDialog = false" />
          <UButton type="submit" form="create-promo-form" :loading="creating" :label="creating ? t('common.creating') : t('common.create')" />
        </div>
      </template>
    </BaseDialog>

    <!-- Edit Dialog -->
    <BaseDialog :show="showEditDialog" :title="t('admin.promo.editCode')" width="normal" @close="closeEditDialog">
      <UForm id="edit-promo-form" :state="editForm" class="space-y-4" @submit="handleUpdate">
        <UFormField :label="t('admin.promo.code')">
          <UInput v-model="editForm.code" class="w-full" :ui="{ base: 'font-mono uppercase' }" />
        </UFormField>
        <UFormField :label="t('admin.promo.bonusAmount')" required>
          <UInput v-model.number="editForm.bonus_amount" type="number" step="0.01" min="0" required class="w-full" />
        </UFormField>
        <UFormField :label="t('admin.promo.maxUses')" :hint="t('admin.promo.zeroUnlimited')">
          <UInput v-model.number="editForm.max_uses" type="number" min="0" class="w-full" />
        </UFormField>
        <UFormField :label="t('admin.promo.status')">
          <Select v-model="editForm.status" :options="statusOptions" />
        </UFormField>
        <UFormField :label="t('admin.promo.expiresAt')" :hint="t('common.optional')">
          <UInput v-model="editForm.expires_at_str" type="datetime-local" class="w-full" />
        </UFormField>
        <UFormField :label="t('admin.promo.notes')" :hint="t('common.optional')">
          <UTextarea v-model="editForm.notes" :rows="2" class="w-full" />
        </UFormField>
      </UForm>
      <template #footer>
        <div class="flex justify-end gap-2">
          <UButton color="neutral" variant="subtle" :label="t('common.cancel')" @click="closeEditDialog" />
          <UButton type="submit" form="edit-promo-form" :loading="updating" :label="updating ? t('common.saving') : t('common.save')" />
        </div>
      </template>
    </BaseDialog>

    <!-- Usages Dialog -->
    <BaseDialog :show="showUsagesDialog" :title="t('admin.promo.usageRecords')" width="wide" @close="showUsagesDialog = false">
      <div v-if="usagesLoading" class="flex items-center justify-center py-8">
        <UIcon name="i-lucide-loader-circle" class="size-6 animate-spin text-dimmed" />
      </div>
      <p v-else-if="usages.length === 0" class="py-8 text-center text-sm text-muted">{{ t('admin.promo.noUsages') }}</p>
      <div v-else>
        <ul class="divide-y divide-default">
          <li v-for="usage in usages" :key="usage.id" class="flex items-center justify-between gap-3 py-3">
            <div class="flex min-w-0 items-center gap-3">
              <UAvatar :alt="usage.user?.email || String(usage.user_id)" size="sm" />
              <div class="min-w-0">
                <p class="truncate text-sm font-medium text-highlighted">
                  {{ usage.user?.email || t('admin.promo.userPrefix', { id: usage.user_id }) }}
                </p>
                <p class="text-xs text-muted">{{ formatDateTime(usage.used_at) }}</p>
              </div>
            </div>
            <span class="text-sm font-medium tabular-nums text-success">+${{ usage.bonus_amount.toFixed(2) }}</span>
          </li>
        </ul>
        <div v-if="usagesTotal > usagesPageSize" class="mt-4">
          <Pagination
            :page="usagesPage"
            :total="usagesTotal"
            :page-size="usagesPageSize"
            @update:page="handleUsagesPageChange"
            @update:page-size="(size: number) => { usagesPageSize = size; usagesPage = 1; loadUsages() }"
          />
        </div>
      </div>
      <template #footer>
        <div class="flex justify-end">
          <UButton color="neutral" variant="subtle" :label="t('common.close')" @click="showUsagesDialog = false" />
        </div>
      </template>
    </BaseDialog>

    <!-- Delete Confirmation Dialog -->
    <ConfirmDialog
      :show="showDeleteDialog"
      :title="t('admin.promo.deleteCode')"
      :message="t('admin.promo.deleteCodeConfirm')"
      :confirm-text="t('common.delete')"
      :cancel-text="t('common.cancel')"
      danger
      @confirm="confirmDelete"
      @cancel="showDeleteDialog = false"
    />
  </AppLayout>
</template>

<script setup lang="ts">
import { ref, reactive, computed, onMounted, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { useClipboard } from '@/composables/useClipboard'
import { getPersistedPageSize } from '@/composables/usePersistedPageSize'
import { adminAPI } from '@/api/admin'
import { formatDateTime, formatDateTimeLocalInput } from '@/utils/format'
import type { PromoCode, PromoCodeUsage } from '@/types'
import type { Column } from '@/components/common/types'
import AppLayout from '@/components/layout/AppLayout.vue'
import TablePageLayout from '@/components/layout/TablePageLayout.vue'
import DataTable from '@/components/common/DataTable.vue'
import Pagination from '@/components/common/Pagination.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Select from '@/components/common/Select.vue'
import type { DropdownMenuItem } from '@nuxt/ui'
import { useListColumnMenu } from './useListColumnMenu'
import AdminPageHeader from '@/components/admin/page/AdminPageHeader.vue'
import PageToolbar from '@/components/layout/PageToolbar.vue'

import AdminRowAction from '@/components/admin/page/AdminRowAction.vue'
const { t } = useI18n()
const appStore = useAppStore()
const { copyToClipboard: clipboardCopy } = useClipboard()

// State
const codes = ref<PromoCode[]>([])
const loading = ref(false)
const creating = ref(false)
const updating = ref(false)
const searchQuery = ref('')
const copiedCode = ref<string | null>(null)

const filters = reactive({
  status: ''
})

const pagination = reactive({
  page: 1,
  page_size: getPersistedPageSize(),
  total: 0
})
const sortState = reactive({
  sort_by: 'created_at',
  sort_order: 'desc' as 'asc' | 'desc'
})

// Dialogs
const showCreateDialog = ref(false)
const showEditDialog = ref(false)
const showDeleteDialog = ref(false)
const showUsagesDialog = ref(false)

const editingCode = ref<PromoCode | null>(null)
const deletingCode = ref<PromoCode | null>(null)

// Usages
const usages = ref<PromoCodeUsage[]>([])
const usagesLoading = ref(false)
const currentViewingCode = ref<PromoCode | null>(null)
const usagesPage = ref(1)
const usagesPageSize = ref(20)
const usagesTotal = ref(0)

// Forms
const createForm = reactive({
  code: '',
  bonus_amount: 1,
  max_uses: 0,
  expires_at_str: '',
  notes: ''
})

const editForm = reactive({
  code: '',
  bonus_amount: 0,
  max_uses: 0,
  status: 'active' as 'active' | 'disabled',
  expires_at_str: '',
  notes: ''
})

// Options
const filterStatusOptions = computed(() => [
  { value: '', label: t('admin.promo.allStatus') },
  { value: 'active', label: t('admin.promo.statusActive') },
  { value: 'disabled', label: t('admin.promo.statusDisabled') }
])

const statusOptions = computed(() => [
  { value: 'active', label: t('admin.promo.statusActive') },
  { value: 'disabled', label: t('admin.promo.statusDisabled') }
])

const allColumns = computed<Column[]>(() => [
  { key: 'code', label: t('admin.promo.columns.code') },
  { key: 'bonus_amount', label: t('admin.promo.columns.bonusAmount'), sortable: true },
  { key: 'usage', label: t('admin.promo.columns.usage') },
  { key: 'status', label: t('admin.promo.columns.status'), sortable: true },
  { key: 'expires_at', label: t('admin.promo.columns.expiresAt'), sortable: true },
  { key: 'created_at', label: t('admin.promo.columns.createdAt'), sortable: true },
  { key: 'actions', label: t('admin.promo.columns.actions') }
])

// "显示列": code and actions are fixed; every column starts visible
const { visibleColumns, menuItems: columnMenuItems } = useListColumnMenu({
  storageKey: 'admin-promo-hidden-columns',
  columns: allColumns,
  fixed: ['code', 'actions']
})

// Row "…" menu: usage records, delete
const rowMenuItems = (row: PromoCode): DropdownMenuItem[][] => [
  [{ label: t('admin.promo.viewUsages'), icon: 'i-lucide-eye', onSelect: () => handleViewUsages(row) }],
  [{ label: t('common.delete'), icon: 'i-lucide-trash', color: 'error', onSelect: () => handleDelete(row) }]
]

// Helpers
const getStatusColor = (status: string, row: PromoCode): 'error' | 'neutral' | 'success' => {
  if (row.expires_at && new Date(row.expires_at) < new Date()) {
    return 'error'
  }
  if (row.max_uses > 0 && row.used_count >= row.max_uses) {
    return 'neutral'
  }
  return status === 'active' ? 'success' : 'neutral'
}

const getStatusLabel = (status: string, row: PromoCode) => {
  if (row.expires_at && new Date(row.expires_at) < new Date()) {
    return t('admin.promo.statusExpired')
  }
  if (row.max_uses > 0 && row.used_count >= row.max_uses) {
    return t('admin.promo.statusMaxUsed')
  }
  return status === 'active' ? t('admin.promo.statusActive') : t('admin.promo.statusDisabled')
}

// API calls
let abortController: AbortController | null = null

const loadCodes = async () => {
  if (abortController) {
    abortController.abort()
  }
  const currentController = new AbortController()
  abortController = currentController
  loading.value = true

  try {
    const response = await adminAPI.promo.list(
      pagination.page,
      pagination.page_size,
      {
        status: filters.status || undefined,
        search: searchQuery.value || undefined,
        sort_by: sortState.sort_by,
        sort_order: sortState.sort_order
      },
      { signal: currentController.signal }
    )
    if (currentController.signal.aborted || abortController !== currentController) return

    codes.value = response.items
    pagination.total = response.total
  } catch (error: any) {
    if (
      currentController.signal.aborted ||
      abortController !== currentController ||
      error?.name === 'AbortError' ||
      error?.code === 'ERR_CANCELED'
    ) {
      return
    }
    appStore.showError(t('admin.promo.failedToLoad'))
    console.error('Error loading promo codes:', error)
  } finally {
    if (abortController === currentController) {
      loading.value = false
      abortController = null
    }
  }
}

let searchTimeout: ReturnType<typeof setTimeout>
const handleSearch = () => {
  clearTimeout(searchTimeout)
  searchTimeout = setTimeout(() => {
    pagination.page = 1
    loadCodes()
  }, 300)
}

const handlePageChange = (page: number) => {
  pagination.page = page
  loadCodes()
}

const handlePageSizeChange = (pageSize: number) => {
  pagination.page_size = pageSize
  pagination.page = 1
  loadCodes()
}

const handleSort = (key: string, order: 'asc' | 'desc') => {
  sortState.sort_by = key
  sortState.sort_order = order
  pagination.page = 1
  loadCodes()
}

const copyToClipboard = async (text: string) => {
  const success = await clipboardCopy(text, t('admin.promo.copied'))
  if (success) {
    copiedCode.value = text
    setTimeout(() => {
      copiedCode.value = null
    }, 2000)
  }
}

// Create
const handleCreate = async () => {
  creating.value = true
  try {
    await adminAPI.promo.create({
      code: createForm.code || undefined,
      bonus_amount: createForm.bonus_amount,
      max_uses: createForm.max_uses,
      expires_at: createForm.expires_at_str ? Math.floor(new Date(createForm.expires_at_str).getTime() / 1000) : undefined,
      notes: createForm.notes || undefined
    })
    appStore.showSuccess(t('admin.promo.codeCreated'))
    showCreateDialog.value = false
    resetCreateForm()
    loadCodes()
  } catch (error: any) {
    appStore.showError(error.response?.data?.detail || t('admin.promo.failedToCreate'))
  } finally {
    creating.value = false
  }
}

const resetCreateForm = () => {
  createForm.code = ''
  createForm.bonus_amount = 1
  createForm.max_uses = 0
  createForm.expires_at_str = ''
  createForm.notes = ''
}

// Edit
const handleEdit = (code: PromoCode) => {
  editingCode.value = code
  editForm.code = code.code
  editForm.bonus_amount = code.bonus_amount
  editForm.max_uses = code.max_uses
  editForm.status = code.status
  editForm.expires_at_str = code.expires_at
    ? formatDateTimeLocalInput(Math.floor(new Date(code.expires_at).getTime() / 1000))
    : ''
  editForm.notes = code.notes || ''
  showEditDialog.value = true
}

const closeEditDialog = () => {
  showEditDialog.value = false
  editingCode.value = null
}

const handleUpdate = async () => {
  if (!editingCode.value) return

  updating.value = true
  try {
    await adminAPI.promo.update(editingCode.value.id, {
      code: editForm.code,
      bonus_amount: editForm.bonus_amount,
      max_uses: editForm.max_uses,
      status: editForm.status,
      expires_at: editForm.expires_at_str ? Math.floor(new Date(editForm.expires_at_str).getTime() / 1000) : 0,
      notes: editForm.notes
    })
    appStore.showSuccess(t('admin.promo.codeUpdated'))
    closeEditDialog()
    loadCodes()
  } catch (error: any) {
    appStore.showError(error.response?.data?.detail || t('admin.promo.failedToUpdate'))
  } finally {
    updating.value = false
  }
}

// Copy Register Link
const copyRegisterLink = async (code: PromoCode) => {
  const baseUrl = window.location.origin
  const registerLink = `${baseUrl}/register?promo=${encodeURIComponent(code.code)}`

  try {
    await navigator.clipboard.writeText(registerLink)
    appStore.showSuccess(t('admin.promo.registerLinkCopied'))
  } catch (error) {
    // Fallback for older browsers
    const textArea = document.createElement('textarea')
    textArea.value = registerLink
    document.body.appendChild(textArea)
    textArea.select()
    document.execCommand('copy')
    document.body.removeChild(textArea)
    appStore.showSuccess(t('admin.promo.registerLinkCopied'))
  }
}

// Delete
const handleDelete = (code: PromoCode) => {
  deletingCode.value = code
  showDeleteDialog.value = true
}

const confirmDelete = async () => {
  if (!deletingCode.value) return

  try {
    await adminAPI.promo.delete(deletingCode.value.id)
    appStore.showSuccess(t('admin.promo.codeDeleted'))
    showDeleteDialog.value = false
    deletingCode.value = null
    loadCodes()
  } catch (error: any) {
    appStore.showError(error.response?.data?.detail || t('admin.promo.failedToDelete'))
  }
}

// View Usages
const handleViewUsages = async (code: PromoCode) => {
  currentViewingCode.value = code
  showUsagesDialog.value = true
  usagesPage.value = 1
  await loadUsages()
}

const loadUsages = async () => {
  if (!currentViewingCode.value) return
  usagesLoading.value = true
  usages.value = []

  try {
    const response = await adminAPI.promo.getUsages(
      currentViewingCode.value.id,
      usagesPage.value,
      usagesPageSize.value
    )
    usages.value = response.items
    usagesTotal.value = response.total
  } catch (error: any) {
    appStore.showError(error.response?.data?.detail || t('admin.promo.failedToLoadUsages'))
  } finally {
    usagesLoading.value = false
  }
}

const handleUsagesPageChange = (page: number) => {
  usagesPage.value = page
  loadUsages()
}

onMounted(() => {
  loadCodes()
})

onUnmounted(() => {
  clearTimeout(searchTimeout)
  abortController?.abort()
})
</script>
