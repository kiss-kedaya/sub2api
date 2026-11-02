<template>
  <AppLayout>
    <TablePageLayout>
      <template #filters>
        <AdminPageHeader
          class="mb-4"
          :title="t('admin.announcements.title')"
          :description="t('admin.announcements.description')"
        >
          <template #actions>
            <UButton icon="i-lucide-plus" :label="t('admin.announcements.createAnnouncement')" @click="openCreateDialog" />
          </template>
        </AdminPageHeader>
        <!-- Toolbar row (template customers page) -->
        <PageToolbar>
          <template #left>
            <UInput
              v-model="searchQuery"
              icon="i-lucide-search"
              :placeholder="t('admin.announcements.searchAnnouncements')"
              class="w-56 sm:w-64"
              @update:model-value="handleSearch"
            />
            <Select v-model="filters.status" :options="statusFilterOptions" class="w-40" @change="handleStatusChange" />
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
              @click="loadAnnouncements"
            />
            <UDropdownMenu :items="columnMenuItems" :content="{ align: 'end' }">
              <UButton color="neutral" variant="outline" trailing-icon="i-lucide-settings-2" :aria-label="t('admin.announcements.displayColumns')">
                <span class="hidden sm:inline">{{ t('admin.announcements.displayColumns') }}</span>
              </UButton>
            </UDropdownMenu>
          </template>
        </PageToolbar>
      </template>

      <template #table>
        <DataTable
          :columns="visibleColumns"
          :data="announcements"
          :loading="loading"
          :server-side-sort="true"
          default-sort-key="created_at"
          default-sort-order="desc"
          @sort="handleSort"
        >
          <template #cell-title="{ value, row }">
            <div class="min-w-0">
              <p class="truncate font-medium text-highlighted">{{ value }}</p>
              <p class="mt-0.5 text-xs text-muted">#{{ row.id }} · {{ formatDateTime(row.created_at) }}</p>
            </div>
          </template>

          <template #cell-status="{ value }">
            <UBadge
              :color="value === 'active' ? 'success' : value === 'draft' ? 'neutral' : 'warning'"
              variant="subtle"
              :label="statusLabel(value)"
            />
          </template>

          <template #cell-notify_mode="{ row }">
            <UBadge
              :color="row.notify_mode === 'popup' ? 'warning' : 'neutral'"
              variant="subtle"
              :label="row.notify_mode === 'popup' ? t('admin.announcements.notifyModeLabels.popup') : t('admin.announcements.notifyModeLabels.silent')"
            />
          </template>

          <template #cell-targeting="{ row }">
            <span class="text-default">{{ targetingSummary(row.targeting) }}</span>
          </template>

          <template #cell-timeRange="{ row }">
            <div class="text-xs text-muted">
              <p>{{ t('admin.announcements.form.startsAt') }}: <span class="text-default">{{ row.starts_at ? formatDateTime(row.starts_at) : t('admin.announcements.timeImmediate') }}</span></p>
              <p class="mt-0.5">{{ t('admin.announcements.form.endsAt') }}: <span class="text-default">{{ row.ends_at ? formatDateTime(row.ends_at) : t('admin.announcements.timeNever') }}</span></p>
            </div>
          </template>

          <template #cell-created_at="{ value }">
            <span class="text-muted">{{ formatDateTime(value) }}</span>
          </template>

          <template #cell-actions="{ row }">
            <div class="flex items-center justify-end gap-0.5">
              <AdminRowAction :label="t('admin.announcements.preview')" icon="eye" @click="openPreview(row)" />
              <AdminRowAction :label="t('common.edit')" icon="edit" @click="openEditDialog(row)" />
              <UDropdownMenu :items="rowMenuItems(row)" :content="{ align: 'end' }">
                <AdminRowAction :label="t('common.more')" icon="more" />
              </UDropdownMenu>
            </div>
          </template>

          <template #empty>
            <EmptyState
              :title="t('empty.noData')"
              :description="t('admin.announcements.createFirstAnnouncement')"
              :action-text="t('admin.announcements.createAnnouncement')"
              @action="openCreateDialog"
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

    <!-- Create/Edit Dialog -->
    <BaseDialog
      :show="showEditDialog"
      :title="isEditing ? t('admin.announcements.editAnnouncement') : t('admin.announcements.createAnnouncement')"
      width="wide"
      @close="closeEdit"
    >
      <UForm id="announcement-form" :state="form" class="space-y-4" @submit="handleSave">
        <UFormField :label="t('admin.announcements.form.title')" required>
          <UInput v-model="form.title" required class="w-full" />
        </UFormField>
        <UFormField :label="t('admin.announcements.form.content')" required>
          <UTextarea v-model="form.content" :rows="6" required class="w-full" />
        </UFormField>
        <div class="grid grid-cols-1 gap-4 md:grid-cols-2">
          <UFormField :label="t('admin.announcements.form.status')">
            <Select v-model="form.status" :options="statusOptions" />
          </UFormField>
          <UFormField :label="t('admin.announcements.form.notifyMode')" :help="t('admin.announcements.form.notifyModeHint')">
            <Select v-model="form.notify_mode" :options="notifyModeOptions" />
          </UFormField>
        </div>
        <div class="grid grid-cols-1 gap-4 md:grid-cols-2">
          <UFormField :label="t('admin.announcements.form.startsAt')" :help="t('admin.announcements.form.startsAtHint')">
            <UInput v-model="form.starts_at_str" type="datetime-local" max="9999-12-31T23:59" class="w-full" />
          </UFormField>
          <UFormField :label="t('admin.announcements.form.endsAt')" :help="t('admin.announcements.form.endsAtHint')">
            <UInput v-model="form.ends_at_str" type="datetime-local" max="9999-12-31T23:59" class="w-full" />
          </UFormField>
        </div>
        <AnnouncementTargetingEditor v-model="form.targeting" :groups="subscriptionGroups" />
      </UForm>
      <template #footer>
        <div class="flex justify-end gap-2">
          <UButton color="neutral" variant="subtle" :label="t('common.cancel')" @click="closeEdit" />
          <UButton type="submit" form="announcement-form" :loading="saving" :label="saving ? t('common.saving') : t('common.save')" />
        </div>
      </template>
    </BaseDialog>

    <!-- Delete Confirmation -->
    <ConfirmDialog
      :show="showDeleteDialog"
      :title="t('admin.announcements.deleteAnnouncement')"
      :message="t('admin.announcements.deleteConfirm')"
      :confirm-text="t('common.delete')"
      :cancel-text="t('common.cancel')"
      danger
      @confirm="confirmDelete"
      @cancel="showDeleteDialog = false"
    />

    <!-- Read Status Dialog -->
    <AnnouncementReadStatusDialog
      :show="showReadStatusDialog"
      :announcement-id="readStatusAnnouncementId"
      @close="showReadStatusDialog = false"
    />

    <AnnouncementPopup
      :announcement="previewAnnouncement"
      preview
      @close="previewAnnouncement = null"
    />
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { getPersistedPageSize } from '@/composables/usePersistedPageSize'
import { adminAPI } from '@/api/admin'
import { formatDateTime, formatDateTimeLocalInput, parseDateTimeLocalInput } from '@/utils/format'
import type { AdminGroup, Announcement, AnnouncementTargeting } from '@/types'
import type { Column } from '@/components/common/types'

import AppLayout from '@/components/layout/AppLayout.vue'
import TablePageLayout from '@/components/layout/TablePageLayout.vue'
import DataTable from '@/components/common/DataTable.vue'
import Pagination from '@/components/common/Pagination.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import Select from '@/components/common/Select.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import AdminPageHeader from '@/components/admin/page/AdminPageHeader.vue'
import PageToolbar from '@/components/layout/PageToolbar.vue'
import type { DropdownMenuItem } from '@nuxt/ui'
import { useListColumnMenu } from './useListColumnMenu'

import AdminRowAction from '@/components/admin/page/AdminRowAction.vue'
import AnnouncementTargetingEditor from '@/components/admin/announcements/AnnouncementTargetingEditor.vue'
import AnnouncementReadStatusDialog from '@/components/admin/announcements/AnnouncementReadStatusDialog.vue'
import AnnouncementPopup from '@/components/common/AnnouncementPopup.vue'

const { t } = useI18n()
const appStore = useAppStore()

const announcements = ref<Announcement[]>([])
const loading = ref(false)

const filters = reactive({
  status: '',
})
const searchQuery = ref('')

const pagination = reactive({
  page: 1,
  page_size: getPersistedPageSize(),
  total: 0,
  pages: 0
})

const sortState = reactive({
  sort_by: 'created_at',
  sort_order: 'desc' as 'asc' | 'desc'
})

const statusFilterOptions = computed(() => [
  { value: '', label: t('admin.announcements.allStatus') },
  { value: 'draft', label: t('admin.announcements.statusLabels.draft') },
  { value: 'active', label: t('admin.announcements.statusLabels.active') },
  { value: 'archived', label: t('admin.announcements.statusLabels.archived') }
])

const statusOptions = computed(() => [
  { value: 'draft', label: t('admin.announcements.statusLabels.draft') },
  { value: 'active', label: t('admin.announcements.statusLabels.active') },
  { value: 'archived', label: t('admin.announcements.statusLabels.archived') }
])

const notifyModeOptions = computed(() => [
  { value: 'silent', label: t('admin.announcements.notifyModeLabels.silent') },
  { value: 'popup', label: t('admin.announcements.notifyModeLabels.popup') }
])

const allColumns = computed<Column[]>(() => [
  { key: 'title', label: t('admin.announcements.columns.title'), sortable: true },
  { key: 'status', label: t('admin.announcements.columns.status'), sortable: true },
  { key: 'notify_mode', label: t('admin.announcements.columns.notifyMode'), sortable: true },
  { key: 'targeting', label: t('admin.announcements.columns.targeting') },
  { key: 'timeRange', label: t('admin.announcements.columns.timeRange') },
  { key: 'created_at', label: t('admin.announcements.columns.createdAt'), sortable: true },
  { key: 'actions', label: t('admin.announcements.columns.actions') }
])

// "显示列": the title cell already shows the created time, so the created column starts hidden
const { visibleColumns, menuItems: columnMenuItems } = useListColumnMenu({
  storageKey: 'admin-announcements-hidden-columns',
  columns: allColumns,
  fixed: ['title', 'actions'],
  defaultHidden: ['created_at']
})

// Row "…" menu: read status, delete
const rowMenuItems = (row: Announcement): DropdownMenuItem[][] => [
  [{ label: t('admin.announcements.readStatus'), icon: 'i-lucide-chart-column', onSelect: () => openReadStatus(row) }],
  [{ label: t('common.delete'), icon: 'i-lucide-trash', color: 'error', onSelect: () => handleDelete(row) }]
]

const statusLabel = (status: string) => {
  if (status === 'draft') return t('admin.announcements.statusLabels.draft')
  if (status === 'active') return t('admin.announcements.statusLabels.active')
  if (status === 'archived') return t('admin.announcements.statusLabels.archived')
  return status
}

const targetingSummary = (targeting: AnnouncementTargeting) => {
  const anyOf = targeting?.any_of ?? []
  if (!anyOf || anyOf.length === 0) return t('admin.announcements.targetingSummaryAll')
  return t('admin.announcements.targetingSummaryCustom', { groups: anyOf.length })
}

// ===== CRUD / list =====
let currentController: AbortController | null = null

async function loadAnnouncements() {
  currentController?.abort()
  const requestController = new AbortController()
  currentController = requestController
  const { signal } = requestController

  try {
    loading.value = true
    const res = await adminAPI.announcements.list(pagination.page, pagination.page_size, {
      status: filters.status || undefined,
      search: searchQuery.value || undefined,
      sort_by: sortState.sort_by,
      sort_order: sortState.sort_order
    }, { signal })

    if (signal.aborted || currentController !== requestController) return

    announcements.value = res.items
    pagination.total = res.total
    pagination.pages = res.pages
    pagination.page = res.page
    pagination.page_size = res.page_size
  } catch (error: any) {
    if (
      signal.aborted ||
      currentController !== requestController ||
      error?.name === 'AbortError' ||
      error?.code === 'ERR_CANCELED'
    ) {
      return
    }
    console.error('Error loading announcements:', error)
    appStore.showError(error.response?.data?.detail || t('admin.announcements.failedToLoad'))
  } finally {
    if (currentController === requestController) {
      loading.value = false
      currentController = null
    }
  }
}

function handlePageChange(page: number) {
  pagination.page = page
  loadAnnouncements()
}

function handlePageSizeChange(pageSize: number) {
  pagination.page_size = pageSize
  pagination.page = 1
  loadAnnouncements()
}

function handleStatusChange() {
  pagination.page = 1
  loadAnnouncements()
}

function handleSort(key: string, order: 'asc' | 'desc') {
  sortState.sort_by = key
  sortState.sort_order = order
  pagination.page = 1
  loadAnnouncements()
}

let searchDebounceTimer: number | null = null
function handleSearch() {
  if (searchDebounceTimer) window.clearTimeout(searchDebounceTimer)
  searchDebounceTimer = window.setTimeout(() => {
    pagination.page = 1
    loadAnnouncements()
  }, 300)
}

// ===== Create/Edit dialog =====
const showEditDialog = ref(false)
const saving = ref(false)
const editingAnnouncement = ref<Announcement | null>(null)

const isEditing = computed(() => !!editingAnnouncement.value)

const form = reactive({
  title: '',
  content: '',
  status: 'draft',
  notify_mode: 'silent',
  starts_at_str: '',
  ends_at_str: '',
  targeting: { any_of: [] } as AnnouncementTargeting
})

const subscriptionGroups = ref<AdminGroup[]>([])

async function loadSubscriptionGroups() {
  try {
    const all = await adminAPI.groups.getAll()
    subscriptionGroups.value = (all || []).filter((g) => g.subscription_type === 'subscription')
  } catch (error: any) {
    console.error('Error loading groups:', error)
    // not fatal
  }
}

function resetForm() {
  form.title = ''
  form.content = ''
  form.status = 'draft'
  form.notify_mode = 'silent'
  form.starts_at_str = ''
  form.ends_at_str = ''
  form.targeting = { any_of: [] }
}

function fillFormFromAnnouncement(a: Announcement) {
  form.title = a.title
  form.content = a.content
  form.status = a.status
  form.notify_mode = a.notify_mode || 'silent'

  // Backend returns RFC3339 strings
  form.starts_at_str = a.starts_at ? formatDateTimeLocalInput(Math.floor(new Date(a.starts_at).getTime() / 1000)) : ''
  form.ends_at_str = a.ends_at ? formatDateTimeLocalInput(Math.floor(new Date(a.ends_at).getTime() / 1000)) : ''

  form.targeting = a.targeting ?? { any_of: [] }
}

function openCreateDialog() {
  editingAnnouncement.value = null
  resetForm()
  showEditDialog.value = true
}

function openEditDialog(row: Announcement) {
  editingAnnouncement.value = row
  fillFormFromAnnouncement(row)
  showEditDialog.value = true
}

function closeEdit() {
  showEditDialog.value = false
  editingAnnouncement.value = null
}

function buildCreatePayload() {
  const startsAt = parseDateTimeLocalInput(form.starts_at_str)
  const endsAt = parseDateTimeLocalInput(form.ends_at_str)

  return {
    title: form.title,
    content: form.content,
    status: form.status as any,
    notify_mode: form.notify_mode as any,
    targeting: form.targeting,
    starts_at: startsAt ?? undefined,
    ends_at: endsAt ?? undefined
  }
}

function buildUpdatePayload(original: Announcement) {
  const payload: any = {}

  if (form.title !== original.title) payload.title = form.title
  if (form.content !== original.content) payload.content = form.content
  if (form.status !== original.status) payload.status = form.status
  if (form.notify_mode !== (original.notify_mode || 'silent')) payload.notify_mode = form.notify_mode

  // starts_at / ends_at: distinguish unchanged vs clear(0) vs set
  const originalStarts = original.starts_at ? Math.floor(new Date(original.starts_at).getTime() / 1000) : null
  const originalEnds = original.ends_at ? Math.floor(new Date(original.ends_at).getTime() / 1000) : null

  const newStarts = parseDateTimeLocalInput(form.starts_at_str)
  const newEnds = parseDateTimeLocalInput(form.ends_at_str)

  if (newStarts !== originalStarts) {
    payload.starts_at = newStarts === null ? 0 : newStarts
  }
  if (newEnds !== originalEnds) {
    payload.ends_at = newEnds === null ? 0 : newEnds
  }

  // targeting: do shallow compare by JSON
  if (JSON.stringify(form.targeting ?? {}) !== JSON.stringify(original.targeting ?? {})) {
    payload.targeting = form.targeting
  }

  return payload
}

async function handleSave() {
  // Frontend validation for targeting (to avoid ANNOUNCEMENT_INVALID_TARGET)
  const anyOf = form.targeting?.any_of ?? []
  if (anyOf.length > 50) {
    appStore.showError(t('admin.announcements.failedToCreate'))
    return
  }
  for (const g of anyOf) {
    const allOf = g?.all_of ?? []
    if (allOf.length > 50) {
      appStore.showError(t('admin.announcements.failedToCreate'))
      return
    }
  }

  saving.value = true
  try {
    if (!editingAnnouncement.value) {
      const payload = buildCreatePayload()
      await adminAPI.announcements.create(payload)
      appStore.showSuccess(t('common.success'))
      showEditDialog.value = false
      await loadAnnouncements()
      return
    }

    const original = editingAnnouncement.value
    const payload = buildUpdatePayload(original)
    await adminAPI.announcements.update(original.id, payload)
    appStore.showSuccess(t('common.success'))
    showEditDialog.value = false
    editingAnnouncement.value = null
    await loadAnnouncements()
  } catch (error: any) {
    console.error('Failed to save announcement:', error)
    appStore.showError(error.response?.data?.detail || (editingAnnouncement.value ? t('admin.announcements.failedToUpdate') : t('admin.announcements.failedToCreate')))
  } finally {
    saving.value = false
  }
}

// ===== Delete =====
const showDeleteDialog = ref(false)
const deletingAnnouncement = ref<Announcement | null>(null)

function handleDelete(row: Announcement) {
  deletingAnnouncement.value = row
  showDeleteDialog.value = true
}

async function confirmDelete() {
  if (!deletingAnnouncement.value) return

  try {
    await adminAPI.announcements.delete(deletingAnnouncement.value.id)
    appStore.showSuccess(t('common.success'))
    showDeleteDialog.value = false
    deletingAnnouncement.value = null
    await loadAnnouncements()
  } catch (error: any) {
    console.error('Failed to delete announcement:', error)
    appStore.showError(error.response?.data?.detail || t('admin.announcements.failedToDelete'))
  }
}

// ===== Read status =====
const showReadStatusDialog = ref(false)
const readStatusAnnouncementId = ref<number | null>(null)
const previewAnnouncement = ref<Announcement | null>(null)

function openPreview(row: Announcement) {
  previewAnnouncement.value = row
}

function openReadStatus(row: Announcement) {
  readStatusAnnouncementId.value = row.id
  showReadStatusDialog.value = true
}

onMounted(async () => {
  await loadSubscriptionGroups()
  await loadAnnouncements()
})

onUnmounted(() => {
  if (searchDebounceTimer) window.clearTimeout(searchDebounceTimer)
  currentController?.abort()
})
</script>
