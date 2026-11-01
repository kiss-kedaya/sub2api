<template>
  <BaseDialog :show="show" :title="t('admin.groups.rateMultipliersTitle')" width="wide" @close="handleClose">
    <div v-if="group" class="space-y-6">
      <!-- 分组信息：标题下的一行说明 -->
      <p class="-mt-1 flex flex-wrap items-center gap-x-2 gap-y-1 text-sm text-muted">
        <span class="inline-flex items-center gap-1.5 text-default">
          <PlatformIcon :platform="group.platform" size="sm" />
          {{ t('admin.groups.platforms.' + group.platform) }}
        </span>
        <span class="text-dimmed" aria-hidden="true">·</span>
        <span class="font-medium text-highlighted">{{ group.name }}</span>
        <span class="text-dimmed" aria-hidden="true">·</span>
        <span>{{ t('admin.groups.columns.rateMultiplier') }}: {{ group.rate_multiplier }}x</span>
      </p>

      <!-- 添加用户 -->
      <UFormField :label="t('admin.groups.addUserRate')">
        <div class="flex items-start gap-2">
          <div class="relative min-w-0 flex-1">
            <UInput
              v-model="searchQuery"
              icon="i-lucide-search"
              autocomplete="off"
              class="w-full"
              :placeholder="t('admin.groups.searchUserPlaceholder')"
              @update:model-value="handleSearchUsers"
              @focus="showDropdown = true"
            />
            <div
              v-if="showDropdown && searchResults.length > 0"
              class="absolute inset-x-0 top-full z-10 mt-1 max-h-48 overflow-y-auto rounded-md bg-default p-1 shadow-lg ring ring-default"
            >
              <UButton
                v-for="user in searchResults"
                :key="user.id"
                color="neutral"
                variant="ghost"
                block
                class="justify-start gap-2 font-normal"
                @click="selectUser(user)"
              >
                <span class="text-dimmed">#{{ user.id }}</span>
                <span class="truncate text-highlighted">{{ user.username || user.email }}</span>
                <span v-if="user.username" class="truncate text-xs text-muted">{{ user.email }}</span>
              </UButton>
            </div>
          </div>
          <UInput
            v-model.number="newRate"
            type="number"
            step="0.001"
            min="0"
            autocomplete="off"
            class="w-24 shrink-0"
            :ui="numberInputUi"
            placeholder="1.0"
            :aria-label="t('admin.groups.columns.rateMultiplier')"
          />
          <UButton
            icon="i-lucide-plus"
            :label="t('common.add')"
            class="shrink-0"
            :disabled="!selectedUser || !newRate"
            @click="handleAddLocal"
          />
        </div>
      </UFormField>

      <!-- 加载状态 -->
      <div v-if="loading" class="flex justify-center py-6">
        <UIcon name="i-lucide-loader-circle" class="size-6 animate-spin text-muted" />
      </div>

      <!-- 已设置的用户列表 -->
      <div v-else class="space-y-3">
        <div class="flex flex-wrap items-center justify-between gap-2">
          <h4 class="text-sm font-medium text-highlighted">
            {{ t('admin.groups.rateMultipliers') }} ({{ localEntries.length }})
          </h4>
          <!-- 批量调整 + 全部清空 -->
          <div v-if="localEntries.length > 0" class="flex flex-wrap items-center gap-2">
            <span class="text-xs text-muted">{{ t('admin.groups.batchAdjust') }}</span>
            <span class="text-xs text-dimmed" aria-hidden="true">×</span>
            <UInput
              v-model.number="batchFactor"
              type="number"
              step="0.1"
              min="0"
              autocomplete="off"
              size="sm"
              class="w-20"
              :ui="centeredNumberInputUi"
              placeholder="0.5"
              :aria-label="t('admin.groups.batchAdjust')"
            />
            <UButton
              size="sm"
              color="neutral"
              variant="outline"
              :label="t('admin.groups.applyMultiplier')"
              :disabled="!batchFactor || batchFactor <= 0"
              @click="applyBatchFactor"
            />
            <UButton
              size="sm"
              color="error"
              variant="subtle"
              icon="i-lucide-eraser"
              :label="t('admin.groups.clearAll')"
              @click="clearAllLocal"
            />
          </div>
        </div>

        <p v-if="localEntries.length === 0" class="py-6 text-center text-sm text-muted">
          {{ t('admin.groups.noRateMultipliers') }}
        </p>

        <template v-else>
          <UTable
            :data="paginatedLocalEntries"
            :columns="tableColumns"
            :get-row-id="getRowId"
            sticky
            :ui="tableUi"
          >
            <template #user_id-cell="{ row }">
              <span class="text-dimmed">{{ row.original.user_id }}</span>
            </template>
            <template #user_name-cell="{ row }">
              <span class="text-highlighted">{{ row.original.user_name || '-' }}</span>
            </template>
            <template #user_notes-cell="{ row }">
              <span class="block max-w-[160px] truncate" :title="row.original.user_notes">{{ row.original.user_notes || '-' }}</span>
            </template>
            <template #user_status-cell="{ row }">
              <UBadge
                :color="row.original.user_status === 'active' ? 'success' : 'neutral'"
                variant="subtle"
                :label="row.original.user_status"
              />
            </template>
            <template #rate_multiplier-cell="{ row }">
              <UInput
                type="number"
                step="0.001"
                min="0.001"
                autocomplete="off"
                size="sm"
                class="w-24"
                :ui="centeredNumberInputUi"
                :model-value="String(row.original.rate_multiplier ?? '')"
                :placeholder="String(props.group?.rate_multiplier ?? 1)"
                :aria-label="t('admin.groups.columns.rateMultiplier')"
                @change="updateLocalRate(row.original.user_id, ($event.target as HTMLInputElement).value)"
              />
            </template>
            <template #final_rate-header>
              <span class="text-primary">{{ t('admin.groups.finalRate') }}</span>
            </template>
            <template #final_rate-cell="{ row }">
              <span class="font-medium text-primary">{{ computeFinalRate(row.original.rate_multiplier) }}</span>
            </template>
            <template #actions-cell="{ row }">
              <UButton
                icon="i-lucide-trash-2"
                color="neutral"
                variant="ghost"
                size="sm"
                class="text-muted hover:text-error"
                :aria-label="t('common.delete')"
                @click="removeLocal(row.original.user_id)"
              />
            </template>
          </UTable>

          <!-- 分页 -->
          <Pagination
            :total="localEntries.length"
            :page="currentPage"
            :page-size="pageSize"
            @update:page="currentPage = $event"
            @update:pageSize="handlePageSizeChange"
          />
        </template>
      </div>
    </div>

    <template v-if="group" #footer>
      <!-- 左侧：未保存提示 + 撤销 -->
      <div v-if="isDirty" class="me-auto flex items-center gap-2 text-xs">
        <span class="text-warning">{{ t('admin.groups.unsavedChanges') }}</span>
        <UButton
          size="xs"
          variant="link"
          :label="t('admin.groups.revertChanges')"
          @click="handleCancel"
        />
      </div>
      <!-- 右侧：关闭 / 保存 -->
      <UButton color="neutral" variant="subtle" :label="t('common.close')" @click="handleClose" />
      <UButton
        v-if="isDirty"
        :label="t('common.save')"
        :loading="saving"
        @click="handleSave"
      />
    </template>
  </BaseDialog>

</template>

<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { adminAPI } from '@/api/admin'
import type { GroupRateMultiplierEntry } from '@/api/admin/groups'
import type { AdminGroup, AdminUser } from '@/types'
import type { TableColumn } from '@nuxt/ui'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Pagination from '@/components/common/Pagination.vue'
import PlatformIcon from '@/components/common/PlatformIcon.vue'

interface LocalEntry extends GroupRateMultiplierEntry {}

// Spinner-free number fields (the old `hide-spinner` rule, now on UInput's inner input).
const numberInputUi = {
  base: '[appearance:textfield] [&::-webkit-inner-spin-button]:appearance-none [&::-webkit-outer-spin-button]:appearance-none'
}
const centeredNumberInputUi = { base: `${numberInputUi.base} text-center` }
// Quiet list: compact rows, solid sticky header (no blur), scrolls inside the dialog.
const tableUi = {
  root: 'max-h-[420px]',
  thead: 'bg-default backdrop-blur-none',
  th: 'px-3 py-2 text-xs font-medium text-muted',
  td: 'px-3 py-2'
}
const getRowId = (entry: LocalEntry) => String(entry.user_id)

const props = defineProps<{
  show: boolean
  group: AdminGroup | null
}>()

const emit = defineEmits<{
  close: []
  success: []
}>()

const { t } = useI18n()
const appStore = useAppStore()

const loading = ref(false)
const saving = ref(false)
const serverEntries = ref<GroupRateMultiplierEntry[]>([])
const localEntries = ref<LocalEntry[]>([])
const searchQuery = ref('')
const searchResults = ref<AdminUser[]>([])
const showDropdown = ref(false)
const selectedUser = ref<AdminUser | null>(null)
const newRate = ref<number | null>(null)
const currentPage = ref(1)
const pageSize = ref(10)
const batchFactor = ref<number | null>(null)

let searchTimeout: ReturnType<typeof setTimeout>

// 是否显示"最终倍率"预览列
const showFinalRate = computed(() => {
  return batchFactor.value != null && batchFactor.value > 0 && batchFactor.value !== 1
})

const tableColumns = computed<TableColumn<LocalEntry>[]>(() => [
  { accessorKey: 'user_email', header: t('admin.groups.columns.userEmail') },
  { accessorKey: 'user_id', header: 'ID' },
  { accessorKey: 'user_name', header: t('admin.groups.columns.userName') },
  { accessorKey: 'user_notes', header: t('admin.groups.columns.userNotes') },
  { accessorKey: 'user_status', header: t('admin.groups.columns.userStatus') },
  { accessorKey: 'rate_multiplier', header: t('admin.groups.columns.rateMultiplier') },
  ...(showFinalRate.value ? [{ id: 'final_rate' }] : []),
  { id: 'actions', meta: { class: { th: 'w-10', td: 'w-10' } } }
])

// 计算最终倍率预览
const computeFinalRate = (rate: number | null | undefined) => {
  const base = rate ?? props.group?.rate_multiplier ?? 1
  if (!batchFactor.value) return base
  return parseFloat((base * batchFactor.value).toFixed(6))
}

// 检测是否有未保存的修改
const isDirty = computed(() => {
  if (localEntries.value.length !== serverEntries.value.length) return true
  const serverMap = new Map(serverEntries.value.map(e => [e.user_id, e.rate_multiplier ?? null]))
  return localEntries.value.some(e => serverMap.get(e.user_id) !== (e.rate_multiplier ?? null))
})

const paginatedLocalEntries = computed(() => {
  const start = (currentPage.value - 1) * pageSize.value
  return localEntries.value.slice(start, start + pageSize.value)
})

const cloneEntries = (entries: GroupRateMultiplierEntry[]): LocalEntry[] => {
  return entries.map(e => ({ ...e }))
}

const loadEntries = async () => {
  if (!props.group) return
  loading.value = true
  try {
    const raw = await adminAPI.groups.getGroupRateMultipliers(props.group.id)
    // 仅显示已设置 rate_multiplier 的条目；rpm_override 在另一个弹窗管理，保留不动
    serverEntries.value = raw.filter(e => e.rate_multiplier != null)
    localEntries.value = cloneEntries(serverEntries.value)
    adjustPage()
  } catch (error) {
    appStore.showError(t('admin.groups.failedToLoad'))
    console.error('Error loading group rate multipliers:', error)
  } finally {
    loading.value = false
  }
}

const adjustPage = () => {
  const totalPages = Math.max(1, Math.ceil(localEntries.value.length / pageSize.value))
  if (currentPage.value > totalPages) {
    currentPage.value = totalPages
  }
}

watch(() => props.show, (val) => {
  if (val && props.group) {
    currentPage.value = 1
    batchFactor.value = null
    searchQuery.value = ''
    searchResults.value = []
    selectedUser.value = null
    newRate.value = null
    loadEntries()
  }
})

const handlePageSizeChange = (newSize: number) => {
  pageSize.value = newSize
  currentPage.value = 1
}

const handleSearchUsers = () => {
  clearTimeout(searchTimeout)
  selectedUser.value = null
  if (!searchQuery.value.trim()) {
    searchResults.value = []
    showDropdown.value = false
    return
  }
  searchTimeout = setTimeout(async () => {
    try {
      const res = await adminAPI.users.list(1, 10, { search: searchQuery.value.trim() })
      searchResults.value = res.items
      showDropdown.value = true
    } catch {
      searchResults.value = []
    }
  }, 300)
}

const selectUser = (user: AdminUser) => {
  selectedUser.value = user
  searchQuery.value = user.email
  showDropdown.value = false
  searchResults.value = []
}

// 本地添加（或覆盖已有用户）
const handleAddLocal = () => {
  if (!selectedUser.value || !newRate.value) return
  const user = selectedUser.value
  const idx = localEntries.value.findIndex(e => e.user_id === user.id)
  const entry: LocalEntry = {
    user_id: user.id,
    user_name: user.username || '',
    user_email: user.email,
    user_notes: user.notes || '',
    user_status: user.status || 'active',
    rate_multiplier: newRate.value,
    rpm_override: null
  }
  if (idx >= 0) {
    localEntries.value[idx] = entry
  } else {
    localEntries.value.push(entry)
  }
  searchQuery.value = ''
  selectedUser.value = null
  newRate.value = null
  adjustPage()
}

// 本地修改倍率
const updateLocalRate = (userId: number, value: string) => {
  const entry = localEntries.value.find(e => e.user_id === userId)
  if (!entry) return
  if (value.trim() === '') {
    entry.rate_multiplier = null
    return
  }
  const num = parseFloat(value)
  if (isNaN(num)) return
  entry.rate_multiplier = num
}

// 本地删除
const removeLocal = (userId: number) => {
  localEntries.value = localEntries.value.filter(e => e.user_id !== userId)
  adjustPage()
}

// 批量乘数应用到本地
const applyBatchFactor = () => {
  if (!batchFactor.value || batchFactor.value <= 0) return
  for (const entry of localEntries.value) {
    if (entry.rate_multiplier != null) {
      entry.rate_multiplier = parseFloat((entry.rate_multiplier * batchFactor.value).toFixed(6))
    }
  }
  batchFactor.value = null
}

// 本地清空
const clearAllLocal = () => {
  localEntries.value = []
}

// 取消：恢复到服务器数据
const handleCancel = () => {
  localEntries.value = cloneEntries(serverEntries.value)
  batchFactor.value = null
  adjustPage()
}

// 保存：一次性提交所有数据（只提交 rate_multiplier；rpm_override 由独立弹窗管理）
const handleSave = async () => {
  if (!props.group) return
  saving.value = true
  try {
    const entries = localEntries.value
      .filter(e => e.rate_multiplier != null)
      .map(e => ({
        user_id: e.user_id,
        rate_multiplier: e.rate_multiplier as number
      }))
    await adminAPI.groups.batchSetGroupRateMultipliers(props.group.id, entries)
    appStore.showSuccess(t('admin.groups.rateSaved'))
    emit('success')
    emit('close')
  } catch (error) {
    appStore.showError(t('admin.groups.failedToSave'))
    console.error('Error saving rate multipliers:', error)
  } finally {
    saving.value = false
  }
}

// 关闭时如果有未保存修改，先恢复
const handleClose = () => {
  if (isDirty.value) {
    localEntries.value = cloneEntries(serverEntries.value)
  }
  emit('close')
}

// 点击外部关闭下拉
const handleClickOutside = () => {
  showDropdown.value = false
}

if (typeof document !== 'undefined') {
  document.addEventListener('click', handleClickOutside)
}
</script>
