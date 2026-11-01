<template>
  <BaseDialog
    :show="show"
    :title="t('admin.announcements.readStatus')"
    width="extra-wide"
    @close="handleClose"
  >
    <div class="space-y-4">
      <div class="flex items-center gap-1.5">
        <UInput
          v-model="search"
          icon="i-lucide-search"
          class="w-full sm:max-w-sm"
          :placeholder="t('admin.announcements.searchUsers')"
          @update:model-value="handleSearch"
        />
        <UButton
          color="neutral"
          variant="outline"
          icon="i-lucide-refresh-cw"
          square
          class="ms-auto"
          :disabled="loading"
          :ui="{ leadingIcon: loading ? 'animate-spin' : '' }"
          :title="t('common.refresh')"
          :aria-label="t('common.refresh')"
          @click="load"
        />
      </div>

      <DataTable
        :columns="columns"
        :data="items"
        :loading="loading"
        :server-side-sort="true"
        default-sort-key="email"
        default-sort-order="asc"
        @sort="handleSort"
      >
        <template #cell-email="{ value }">
          <span class="font-medium text-highlighted">{{ value }}</span>
        </template>
        <template #cell-balance="{ value }">
          <span class="font-medium tabular-nums text-highlighted">${{ Number(value ?? 0).toFixed(2) }}</span>
        </template>
        <template #cell-eligible="{ value }">
          <UBadge :color="value ? 'success' : 'neutral'" variant="subtle" :label="value ? t('admin.announcements.eligible') : t('common.no')" />
        </template>
        <template #cell-read_at="{ value }">
          <span class="text-muted">{{ value ? formatDateTime(value) : t('admin.announcements.unread') }}</span>
        </template>
      </DataTable>

      <Pagination
        v-if="pagination.total > 0"
        :page="pagination.page"
        :total="pagination.total"
        :page-size="pagination.page_size"
        @update:page="handlePageChange"
        @update:pageSize="handlePageSizeChange"
      />
    </div>

    <template #footer>
      <div class="flex justify-end">
        <UButton color="neutral" variant="subtle" :label="t('common.close')" @click="handleClose" />
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, onUnmounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { adminAPI } from '@/api/admin'
import { formatDateTime } from '@/utils/format'
import type { AnnouncementUserReadStatus } from '@/types'
import type { Column } from '@/components/common/types'
import { getPersistedPageSize } from '@/composables/usePersistedPageSize'

import BaseDialog from '@/components/common/BaseDialog.vue'
import DataTable from '@/components/common/DataTable.vue'
import Pagination from '@/components/common/Pagination.vue'

const { t } = useI18n()
const appStore = useAppStore()

const props = defineProps<{
  show: boolean
  announcementId: number | null
}>()

const emit = defineEmits<{
  (e: 'close'): void
}>()

const loading = ref(false)
const search = ref('')

const pagination = reactive({
  page: 1,
  page_size: getPersistedPageSize(),
  total: 0,
  pages: 0
})

const sortState = reactive({
  sort_by: 'email',
  sort_order: 'asc' as 'asc' | 'desc'
})

const items = ref<AnnouncementUserReadStatus[]>([])

const columns = computed<Column[]>(() => [
  { key: 'email', label: t('common.email'), sortable: true },
  { key: 'username', label: t('admin.users.columns.username'), sortable: true },
  { key: 'balance', label: t('common.balance'), sortable: true },
  { key: 'eligible', label: t('admin.announcements.eligible') },
  { key: 'read_at', label: t('admin.announcements.readAt') }
])

let currentController: AbortController | null = null
let searchDebounceTimer: number | null = null

function resetDialogState() {
  loading.value = false
  search.value = ''
  items.value = []
  pagination.page = 1
  pagination.total = 0
  pagination.pages = 0
  sortState.sort_by = 'email'
  sortState.sort_order = 'asc'
}

function cancelPendingLoad(resetState = false) {
  if (searchDebounceTimer) {
    window.clearTimeout(searchDebounceTimer)
    searchDebounceTimer = null
  }
  currentController?.abort()
  currentController = null
  if (resetState) {
    resetDialogState()
  }
}

async function load() {
  if (!props.show || !props.announcementId) return

  currentController?.abort()
  const requestController = new AbortController()
  currentController = requestController
  const { signal } = requestController

  try {
    loading.value = true
    const res = await adminAPI.announcements.getReadStatus(
      props.announcementId,
      pagination.page,
      pagination.page_size,
      {
        search: search.value,
        sort_by: sortState.sort_by,
        sort_order: sortState.sort_order
      },
      { signal }
    )

    if (signal.aborted || currentController !== requestController) return

    items.value = res.items
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
    console.error('Failed to load read status:', error)
    appStore.showError(error.response?.data?.detail || t('admin.announcements.failedToLoadReadStatus'))
  } finally {
    if (currentController === requestController) {
      loading.value = false
      currentController = null
    }
  }
}

function handlePageChange(page: number) {
  pagination.page = page
  load()
}

function handlePageSizeChange(pageSize: number) {
  pagination.page_size = pageSize
  pagination.page = 1
  load()
}

function handleSort(key: string, order: 'asc' | 'desc') {
  sortState.sort_by = key
  sortState.sort_order = order
  pagination.page = 1
  load()
}

function handleSearch() {
  if (searchDebounceTimer) window.clearTimeout(searchDebounceTimer)
  searchDebounceTimer = window.setTimeout(() => {
    pagination.page = 1
    load()
  }, 300)
}

function handleClose() {
  cancelPendingLoad(true)
  emit('close')
}

watch(
  () => props.show,
  (v) => {
    if (!v) {
      cancelPendingLoad(true)
      return
    }
    pagination.page = 1
    load()
  }
)

watch(
  () => props.announcementId,
  () => {
    if (!props.show) return
    pagination.page = 1
    load()
  }
)

onUnmounted(() => {
  cancelPendingLoad()
})
</script>
