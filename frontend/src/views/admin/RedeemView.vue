<template>
  <AppLayout>
    <TablePageLayout>
      <template #filters>
        <AdminPageHeader
          class="mb-4"
          :title="t('admin.redeem.title')"
          :description="t('admin.redeem.description')"
        >
          <template #actions>
            <UButton data-test="generate-open" icon="i-lucide-plus" :label="t('admin.redeem.generateCodes')" @click="showGenerateDialog = true" />
          </template>
        </AdminPageHeader>
        <!-- Toolbar row (template customers page) -->
        <div class="flex flex-wrap items-center justify-between gap-1.5">
          <div class="flex min-w-0 flex-1 flex-wrap items-center gap-1.5">
            <UInput
              v-model="searchQuery"
              icon="i-lucide-search"
              :placeholder="t('admin.redeem.searchCodes')"
              class="w-full sm:max-w-xs"
              @update:model-value="handleSearch"
            />
            <Select v-model="filters.type" :options="filterTypeOptions" class="w-full sm:w-36" @change="loadCodes" />
            <Select v-model="filters.status" :options="filterStatusOptions" class="w-full sm:w-36" @change="loadCodes" />
          </div>

          <div class="flex flex-wrap items-center gap-1.5">
            <UButton
              v-if="selectedCount > 0"
              color="neutral"
              variant="ghost"
              :label="t('admin.redeem.clearSelection')"
              @click="clearSelectedCodes"
            />
            <UButton
              data-test="batch-update-open"
              color="neutral"
              variant="subtle"
              icon="i-lucide-pencil"
              :label="t('admin.redeem.batchUpdate')"
              :disabled="selectedCount === 0 || batchUpdating"
              @click="openBatchUpdateDialog"
            >
              <template v-if="selectedCount > 0" #trailing>
                <UBadge :label="String(selectedCount)" color="neutral" variant="outline" size="sm" />
              </template>
            </UButton>
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
            <UDropdownMenu :items="toolbarMenuItems" :content="{ align: 'end' }">
              <UButton color="neutral" variant="outline" trailing-icon="i-lucide-chevron-down" :label="t('common.more')" />
            </UDropdownMenu>
            <UDropdownMenu :items="columnMenuItems" :content="{ align: 'end' }">
              <UButton color="neutral" variant="outline" trailing-icon="i-lucide-settings-2" :aria-label="t('admin.redeem.displayColumns')">
                <span class="hidden sm:inline">{{ t('admin.redeem.displayColumns') }}</span>
              </UButton>
            </UDropdownMenu>
          </div>
        </div>
      </template>

      <template #table>
        <DataTable
          :columns="visibleColumns"
          :data="codes"
          :loading="loading"
          :server-side-sort="true"
          default-sort-key="id"
          default-sort-order="desc"
          @sort="handleSort"
        >
          <template #header-select>
            <div @click.stop>
              <UCheckbox
                data-test="select-all-codes"
                :model-value="allVisibleSelected"
                :aria-label="t('common.all')"
                @update:model-value="(value) => toggleVisible(!!value)"
              />
            </div>
          </template>

          <template #cell-select="{ row }">
            <div @click.stop>
              <UCheckbox
                data-test="select-code"
                :model-value="selectedCodeIds.has(row.id)"
                :aria-label="row.code"
                @update:model-value="(value) => (value ? select(row.id) : deselect(row.id))"
              />
            </div>
          </template>

          <template #cell-code="{ value }">
            <div class="flex items-center gap-1">
              <code class="font-mono text-sm text-highlighted">{{ value }}</code>
              <UButton
                :color="copiedCode === value ? 'success' : 'neutral'"
                variant="ghost"
                size="xs"
                square
                :icon="copiedCode === value ? 'i-lucide-check' : 'i-lucide-copy'"
                :title="copiedCode === value ? t('admin.redeem.copied') : t('keys.copyToClipboard')"
                :aria-label="copiedCode === value ? t('admin.redeem.copied') : t('keys.copyToClipboard')"
                @click="copyToClipboard(value)"
              />
            </div>
          </template>

          <template #cell-type="{ value }">
            <UBadge
              :color="value === 'balance' ? 'success' : value === 'subscription' ? 'warning' : 'primary'"
              variant="subtle"
              :label="t('admin.redeem.types.' + value)"
            />
          </template>

          <template #cell-value="{ value, row }">
            <span class="font-medium tabular-nums text-highlighted">
              <template v-if="row.type === 'balance'">${{ value.toFixed(2) }}</template>
              <template v-else-if="row.type === 'subscription'">
                {{ row.validity_days || 30 }} {{ t('admin.redeem.days') }}
                <span v-if="row.group" class="ml-1 text-xs font-normal text-muted">({{ row.group.name }})</span>
              </template>
              <template v-else>{{ value }}</template>
            </span>
          </template>

          <template #cell-status="{ value }">
            <UBadge
              :color="value === 'unused' ? 'success' : value === 'used' ? 'neutral' : 'error'"
              variant="subtle"
              :label="t('admin.redeem.status.' + value)"
            />
          </template>

          <template #cell-used_by="{ value, row }">
            <span class="text-muted">{{ row.user?.email || (value ? t('admin.redeem.userPrefix', { id: value }) : '-') }}</span>
          </template>

          <template #cell-used_at="{ value }">
            <span class="text-muted">{{ value ? formatDateTime(value) : '-' }}</span>
          </template>

          <template #cell-expires_at="{ value, row }">
            <span :class="row.status === 'expired' ? 'text-error' : 'text-muted'">
              {{ value ? formatDateTime(value) : t('admin.redeem.neverExpires') }}
            </span>
          </template>

          <template #cell-actions="{ row }">
            <div class="flex items-center justify-end">
              <AdminRowAction v-if="row.status === 'unused'" :label="t('common.delete')" icon="trash" tone="danger" @click="handleDelete(row)" />
              <span v-else class="text-dimmed">-</span>
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

    <!-- Delete Confirmation Dialog -->
    <ConfirmDialog
      :show="showDeleteDialog"
      :title="t('admin.redeem.deleteCode')"
      :message="t('admin.redeem.deleteCodeConfirm')"
      :confirm-text="t('common.delete')"
      :cancel-text="t('common.cancel')"
      danger
      @confirm="confirmDelete"
      @cancel="showDeleteDialog = false"
    />

    <!-- Delete Unused Codes Dialog -->
    <ConfirmDialog
      :show="showDeleteUnusedDialog"
      :title="t('admin.redeem.deleteAllUnused')"
      :message="t('admin.redeem.deleteAllUnusedConfirm')"
      :confirm-text="t('admin.redeem.deleteAll')"
      :cancel-text="t('common.cancel')"
      danger
      @confirm="confirmDeleteUnused"
      @cancel="showDeleteUnusedDialog = false"
    />


    <!-- Generate Codes Dialog -->
    <BaseDialog
      :show="showGenerateDialog"
      :title="t('admin.redeem.generateCodesTitle')"
      width="normal"
      close-on-click-outside
      @close="showGenerateDialog = false"
    >
      <UForm id="redeem-generate-form" data-test="generate-form" :state="generateForm" class="space-y-4" @submit="handleGenerateCodes">
        <UFormField :label="t('admin.redeem.codeType')">
          <Select v-model="generateForm.type" :options="typeOptions" />
        </UFormField>
        <UFormField
          v-if="generateForm.type !== 'subscription' && generateForm.type !== 'invitation'"
          :label="generateForm.type === 'balance' ? t('admin.redeem.amount') : t('admin.redeem.columns.value')"
          required
        >
          <UInput
            v-model.number="generateForm.value"
            type="number"
            :step="generateForm.type === 'balance' ? '0.01' : '1'"
            :min="generateForm.type === 'balance' ? '0.01' : '1'"
            required
            class="w-full"
          />
        </UFormField>
        <UAlert
          v-if="generateForm.type === 'invitation'"
          color="info"
          variant="subtle"
          icon="i-lucide-info"
          :description="t('admin.redeem.invitationHint')"
        />
        <template v-if="generateForm.type === 'subscription'">
          <UFormField :label="t('admin.redeem.selectGroup')">
            <Select
              v-model="generateForm.group_id"
              :options="subscriptionGroupOptions"
              :placeholder="t('admin.redeem.selectGroupPlaceholder')"
            >
              <template #selected="{ option }">
                <GroupBadge
                  v-if="option"
                  :name="(option as unknown as GroupOption).label"
                  :platform="(option as unknown as GroupOption).platform"
                  :subscription-type="(option as unknown as GroupOption).subscriptionType"
                  :rate-multiplier="(option as unknown as GroupOption).rate"
                />
                <span v-else class="text-dimmed">{{ t('admin.redeem.selectGroupPlaceholder') }}</span>
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
          <UFormField :label="t('admin.redeem.validityDays')" required>
            <UInput v-model.number="generateForm.validity_days" type="number" min="1" max="36500" required class="w-full" />
          </UFormField>
        </template>
        <UFormField :label="t('admin.redeem.codeExpiry')">
          <div class="flex flex-wrap gap-1.5">
            <UButton
              v-for="option in redeemCodeExpiryOptions"
              :key="option.value"
              size="sm"
              :color="generateForm.expiry_option === option.value ? 'primary' : 'neutral'"
              :variant="generateForm.expiry_option === option.value ? 'solid' : 'outline'"
              :label="option.label"
              @click="generateForm.expiry_option = option.value"
            />
          </div>
          <UInput
            v-if="generateForm.expiry_option === 'custom'"
            v-model.number="generateForm.custom_expiry_days"
            type="number"
            min="1"
            max="3650"
            required
            class="mt-2 w-full"
            :placeholder="t('admin.redeem.customExpiryDays')"
          />
        </UFormField>
        <UFormField :label="t('admin.redeem.count')" required>
          <UInput
            v-model.number="generateForm.count"
            data-test="generate-count"
            type="number"
            min="1"
            :max="MAX_REDEEM_CODES_PER_BATCH"
            required
            class="w-full"
          />
        </UFormField>
      </UForm>
      <template #footer>
        <div class="flex justify-end gap-2">
          <UButton color="neutral" variant="subtle" :label="t('common.cancel')" @click="showGenerateDialog = false" />
          <UButton
            type="submit"
            form="redeem-generate-form"
            :loading="generating"
            :label="generating ? t('admin.redeem.generating') : t('admin.redeem.generate')"
          />
        </div>
      </template>
    </BaseDialog>

    <!-- Batch Update Dialog: tick a field to change it -->
    <BaseDialog
      :show="showBatchUpdateDialog"
      :title="t('admin.redeem.batchUpdateTitle')"
      width="normal"
      close-on-click-outside
      @close="closeBatchUpdateDialog"
    >
      <p class="mb-4 text-sm text-muted">{{ t('admin.redeem.selectedCount', { count: selectedCount }) }}</p>
      <UForm id="redeem-batch-update-form" data-test="batch-update-form" :state="batchUpdateForm" class="space-y-4" @submit="handleBatchUpdate">
        <div class="space-y-2">
          <UCheckbox v-model="batchUpdateForm.update_status" data-test="batch-field-status" :label="t('admin.redeem.batchFields.status')" />
          <Select v-if="batchUpdateForm.update_status" v-model="batchUpdateForm.status" data-test="batch-status-select" :options="batchStatusOptions" />
        </div>
        <div class="space-y-2">
          <UCheckbox v-model="batchUpdateForm.update_expires_at" :label="t('admin.redeem.batchFields.expiresAt')" />
          <template v-if="batchUpdateForm.update_expires_at">
            <Select v-model="batchUpdateForm.expires_mode" :options="batchExpiryModeOptions" />
            <UFormField
              v-if="batchUpdateForm.expires_mode === 'custom'"
              :help="t('admin.redeem.localTimeZoneHint', { timezone: browserTimeZone })"
            >
              <UInput v-model="batchUpdateForm.expires_at_local" type="datetime-local" class="w-full" />
            </UFormField>
          </template>
        </div>
        <div class="space-y-2">
          <UCheckbox v-model="batchUpdateForm.update_notes" data-test="batch-field-notes" :label="t('admin.redeem.batchFields.notes')" />
          <UTextarea
            v-if="batchUpdateForm.update_notes"
            v-model="batchUpdateForm.notes"
            data-test="batch-notes-input"
            :rows="3"
            class="w-full"
            :placeholder="t('admin.redeem.batchNotesPlaceholder')"
          />
        </div>
        <div class="space-y-2">
          <UCheckbox v-model="batchUpdateForm.update_group_id" :label="t('admin.redeem.batchFields.group')" />
          <Select
            v-if="batchUpdateForm.update_group_id"
            v-model="batchUpdateForm.group_id"
            :options="batchGroupOptions"
            :placeholder="t('admin.redeem.selectGroupPlaceholder')"
          />
        </div>
      </UForm>
      <template #footer>
        <div class="flex justify-end gap-2">
          <UButton color="neutral" variant="subtle" :label="t('common.cancel')" @click="closeBatchUpdateDialog" />
          <UButton
            data-test="batch-update-submit"
            type="submit"
            form="redeem-batch-update-form"
            :loading="batchUpdating"
            :label="batchUpdating ? t('common.submitting') : t('admin.redeem.batchUpdate')"
          />
        </div>
      </template>
    </BaseDialog>

    <!-- Generated Codes Result Dialog -->
    <BaseDialog
      :show="showResultDialog"
      :title="t('admin.redeem.generatedSuccessfully')"
      width="normal"
      close-on-click-outside
      @close="closeResultDialog"
    >
      <p class="mb-3 text-sm text-muted">{{ t('admin.redeem.codesCreated', { count: generatedCodes.length }) }}</p>
      <UTextarea
        readonly
        :model-value="generatedCodesText"
        :style="{ height: textareaHeight }"
        class="w-full"
        :ui="{ base: 'resize-none font-mono' }"
      />
      <template #footer>
        <div class="flex justify-end gap-2">
          <UButton
            :color="copiedAll ? 'success' : 'neutral'"
            variant="subtle"
            :icon="copiedAll ? 'i-lucide-check' : 'i-lucide-copy'"
            :label="copiedAll ? t('admin.redeem.copied') : t('admin.redeem.copyAll')"
            @click="copyGeneratedCodes"
          />
          <UButton icon="i-lucide-download" :label="t('admin.redeem.download')" @click="downloadGeneratedCodes" />
        </div>
      </template>
    </BaseDialog>
  </AppLayout>
</template>

<script setup lang="ts">
import { ref, reactive, computed, onMounted, onUnmounted, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { useClipboard } from '@/composables/useClipboard'
import { useTableSelection } from '@/composables/useTableSelection'
import { getPersistedPageSize } from '@/composables/usePersistedPageSize'
import { adminAPI } from '@/api/admin'
import { fetchPaginatedItems } from '@/utils/paginatedItems'
import {
  formatDateTime,
  getBrowserTimeZone,
  parseDateTimeLocalInput
} from '@/utils/format'
import type {
  RedeemCode,
  RedeemCodeType,
  Group,
  GroupPlatform,
  SubscriptionType,
  BatchUpdateRedeemCodeFields
} from '@/types'
import type { Column } from '@/components/common/types'
import AppLayout from '@/components/layout/AppLayout.vue'
import TablePageLayout from '@/components/layout/TablePageLayout.vue'
import DataTable from '@/components/common/DataTable.vue'
import Pagination from '@/components/common/Pagination.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import Select from '@/components/common/Select.vue'
import GroupBadge from '@/components/common/GroupBadge.vue'
import GroupOptionItem from '@/components/common/GroupOptionItem.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import AdminPageHeader from '@/components/admin/page/AdminPageHeader.vue'
import type { DropdownMenuItem } from '@nuxt/ui'
import { useListColumnMenu } from './useListColumnMenu'

import AdminRowAction from '@/components/admin/page/AdminRowAction.vue'
const { t } = useI18n()
const appStore = useAppStore()
const { copyToClipboard: clipboardCopy } = useClipboard()
const browserTimeZone = getBrowserTimeZone()

const MAX_REDEEM_CODES_PER_BATCH = 1000

interface GroupOption {
  value: number
  label: string
  description: string | null
  platform: GroupPlatform
  subscriptionType: SubscriptionType
  rate: number
}

const showGenerateDialog = ref(false)
const showResultDialog = ref(false)
const generatedCodes = ref<RedeemCode[]>([])
const subscriptionGroups = ref<Group[]>([])

// 订阅类型分组选项
const subscriptionGroupOptions = computed(() => {
  return subscriptionGroups.value
    .filter((g) => g.subscription_type === 'subscription')
    .map((g) => ({
      value: g.id,
      label: g.name,
      description: g.description,
      platform: g.platform,
      subscriptionType: g.subscription_type,
      rate: g.rate_multiplier
    }))
})

const batchGroupOptions = computed(() => [
  { value: null, label: t('admin.redeem.clearGroup') },
  ...subscriptionGroupOptions.value
])

const generatedCodesText = computed(() => {
  return generatedCodes.value.map((code) => code.code).join('\n')
})

const textareaHeight = computed(() => {
  const lineCount = generatedCodes.value.length
  const lineHeight = 24 // approximate line height in px
  const padding = 24 // top + bottom padding
  const minHeight = 60
  const maxHeight = 240
  const calculatedHeight = Math.min(
    Math.max(lineCount * lineHeight + padding, minHeight),
    maxHeight
  )
  return `${calculatedHeight}px`
})

const copiedAll = ref(false)

const closeResultDialog = () => {
  showResultDialog.value = false
  generatedCodes.value = []
  copiedAll.value = false
}

const copyGeneratedCodes = async () => {
  const success = await clipboardCopy(generatedCodesText.value, t('admin.redeem.copied'))
  if (success) {
    copiedAll.value = true
    setTimeout(() => {
      copiedAll.value = false
    }, 2000)
  }
}

const downloadGeneratedCodes = () => {
  const blob = new Blob([generatedCodesText.value], { type: 'text/plain' })
  const url = window.URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url
  link.download = `redeem-codes-${new Date().toISOString().split('T')[0]}.txt`
  document.body.appendChild(link)
  link.click()
  document.body.removeChild(link)
  window.URL.revokeObjectURL(url)
}

const allColumns = computed<Column[]>(() => [
  { key: 'select', label: '' },
  { key: 'code', label: t('admin.redeem.columns.code') },
  { key: 'type', label: t('admin.redeem.columns.type'), sortable: true },
  { key: 'value', label: t('admin.redeem.columns.value'), sortable: true },
  { key: 'status', label: t('admin.redeem.columns.status'), sortable: true },
  { key: 'used_by', label: t('admin.redeem.columns.usedBy') },
  { key: 'used_at', label: t('admin.redeem.columns.usedAt'), sortable: true },
  { key: 'expires_at', label: t('admin.redeem.columns.expiresAt'), sortable: true },
  { key: 'actions', label: t('admin.redeem.columns.actions') }
])

// "显示列": used time starts hidden; code, selection and actions are fixed
const { visibleColumns, menuItems: columnMenuItems } = useListColumnMenu({
  storageKey: 'admin-redeem-hidden-columns',
  columns: allColumns,
  fixed: ['select', 'code', 'actions'],
  defaultHidden: ['used_at']
})

const typeOptions = computed(() => [
  { value: 'balance', label: t('admin.redeem.balance') },
  { value: 'concurrency', label: t('admin.redeem.concurrency') },
  { value: 'subscription', label: t('admin.redeem.subscription') },
  { value: 'invitation', label: t('admin.redeem.invitation') }
])

const filterTypeOptions = computed(() => [
  { value: '', label: t('admin.redeem.allTypes') },
  { value: 'balance', label: t('admin.redeem.balance') },
  { value: 'concurrency', label: t('admin.redeem.concurrency') },
  { value: 'subscription', label: t('admin.redeem.subscription') },
  { value: 'invitation', label: t('admin.redeem.invitation') }
])

const filterStatusOptions = computed(() => [
  { value: '', label: t('admin.redeem.allStatus') },
  { value: 'unused', label: t('admin.redeem.unused') },
  { value: 'used', label: t('admin.redeem.used') },
  { value: 'expired', label: t('admin.redeem.status.expired') },
  { value: 'disabled', label: t('admin.redeem.status.disabled') }
])

const batchStatusOptions = computed(() => [
  { value: 'unused', label: t('admin.redeem.status.unused') },
  { value: 'disabled', label: t('admin.redeem.status.disabled') }
])

const batchExpiryModeOptions = computed(() => [
  { value: 'clear', label: t('admin.redeem.neverExpires') },
  { value: 'custom', label: t('admin.redeem.customExpiry') }
])

const codes = ref<RedeemCode[]>([])
const loading = ref(false)
const generating = ref(false)
const batchUpdating = ref(false)
const searchQuery = ref('')
const filters = reactive({
  type: '',
  status: ''
})
const pagination = reactive({
  page: 1,
  page_size: getPersistedPageSize(),
  total: 0,
  pages: 0
})
const sortState = reactive({
  sort_by: 'id',
  sort_order: 'desc' as 'asc' | 'desc'
})

let abortController: AbortController | null = null

const showDeleteDialog = ref(false)
const showDeleteUnusedDialog = ref(false)
const showBatchUpdateDialog = ref(false)
const deletingCode = ref<RedeemCode | null>(null)
const copiedCode = ref<string | null>(null)

const {
  selectedSet: selectedCodeIds,
  selectedCount,
  allVisibleSelected,
  select,
  deselect,
  clear: clearSelectedCodes,
  toggleVisible
} = useTableSelection<RedeemCode>({
  rows: codes,
  getId: (code) => code.id
})

const batchUpdateForm = reactive({
  update_status: false,
  status: 'disabled' as 'unused' | 'disabled',
  update_expires_at: false,
  expires_mode: 'clear' as 'clear' | 'custom',
  expires_at_local: '',
  update_notes: false,
  notes: '',
  update_group_id: false,
  group_id: null as number | null
})

type RedeemCodeExpiryOption = 'never' | '1' | '3' | '7' | 'custom'

const redeemCodeExpiryOptions = computed<{ value: RedeemCodeExpiryOption; label: string }[]>(() => [
  { value: 'never', label: t('admin.redeem.neverExpires') },
  { value: '1', label: t('admin.redeem.expiryPresetDays', { days: 1 }) },
  { value: '3', label: t('admin.redeem.expiryPresetDays', { days: 3 }) },
  { value: '7', label: t('admin.redeem.expiryPresetDays', { days: 7 }) },
  { value: 'custom', label: t('admin.redeem.customExpiry') }
])

const generateForm = reactive({
  type: 'balance' as RedeemCodeType,
  value: 10,
  count: 1,
  group_id: null as number | null,
  validity_days: 30,
  expiry_option: 'never' as RedeemCodeExpiryOption,
  custom_expiry_days: 7
})

// 监听类型变化，邀请码类型时自动设置 value 为 0
watch(
  () => generateForm.type,
  (newType) => {
    if (newType === 'invitation') {
      generateForm.value = 0
    } else if (generateForm.value === 0) {
      generateForm.value = 10
    }
  }
)

const buildRedeemQueryFilters = () => ({
  type: (filters.type || undefined) as RedeemCodeType | undefined,
  status: (filters.status || undefined) as 'used' | 'expired' | 'unused' | 'disabled' | undefined,
  search: searchQuery.value || undefined,
  sort_by: sortState.sort_by,
  sort_order: sortState.sort_order
})

const loadCodes = async () => {
  if (abortController) {
    abortController.abort()
  }
  const currentController = new AbortController()
  abortController = currentController
  loading.value = true
  try {
    const response = await adminAPI.redeem.list(
      pagination.page,
      pagination.page_size,
      buildRedeemQueryFilters(),
      {
        signal: currentController.signal
      }
    )
    if (currentController.signal.aborted) {
      return
    }
    codes.value = response.items
    pagination.total = response.total
    pagination.pages = response.pages
  } catch (error: any) {
    if (
      currentController.signal.aborted ||
      error?.name === 'AbortError' ||
      error?.code === 'ERR_CANCELED'
    ) {
      return
    }
    appStore.showError(t('admin.redeem.failedToLoad'))
    console.error('Error loading redeem codes:', error)
  } finally {
    if (abortController === currentController && !currentController.signal.aborted) {
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

// Toolbar "更多" menu: CSV export, and deleting every unused code while the unused filter is on
const toolbarMenuItems = computed<DropdownMenuItem[][]>(() => {
  const groups: DropdownMenuItem[][] = [
    [{ label: t('admin.redeem.exportCsv'), icon: 'i-lucide-download', onSelect: () => handleExportCodes() }]
  ]
  if (filters.status === 'unused') {
    groups.push([{
      label: t('admin.redeem.deleteAllUnused'),
      icon: 'i-lucide-trash',
      color: 'error',
      onSelect: () => { showDeleteUnusedDialog.value = true }
    }])
  }
  return groups
})

const getRedeemCodeExpiresInDays = () => {
  if (generateForm.expiry_option === 'never') {
    return undefined
  }
  if (generateForm.expiry_option === 'custom') {
    if (
      !Number.isFinite(generateForm.custom_expiry_days) ||
      generateForm.custom_expiry_days < 1
    ) {
      return null
    }
    return Math.floor(generateForm.custom_expiry_days)
  }
  return Number(generateForm.expiry_option)
}

const toDatetimeLocalInputValue = (date: Date) => {
  const pad = (value: number) => String(value).padStart(2, '0')
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(
    date.getHours()
  )}:${pad(date.getMinutes())}`
}

const resetBatchUpdateForm = () => {
  batchUpdateForm.update_status = false
  batchUpdateForm.status = 'disabled'
  batchUpdateForm.update_expires_at = false
  batchUpdateForm.expires_mode = 'clear'
  batchUpdateForm.expires_at_local = toDatetimeLocalInputValue(
    new Date(Date.now() + 24 * 60 * 60 * 1000)
  )
  batchUpdateForm.update_notes = false
  batchUpdateForm.notes = ''
  batchUpdateForm.update_group_id = false
  batchUpdateForm.group_id = null
}

const openBatchUpdateDialog = () => {
  if (selectedCount.value === 0) {
    appStore.showInfo(t('admin.redeem.selectCodesFirst'))
    return
  }
  resetBatchUpdateForm()
  showBatchUpdateDialog.value = true
}

const closeBatchUpdateDialog = () => {
  showBatchUpdateDialog.value = false
}

const buildBatchUpdateFields = (): BatchUpdateRedeemCodeFields | null => {
  const fields: BatchUpdateRedeemCodeFields = {}

  if (batchUpdateForm.update_status) {
    fields.status = batchUpdateForm.status
  }
  if (batchUpdateForm.update_expires_at) {
    if (batchUpdateForm.expires_mode === 'clear') {
      fields.expires_at = null
    } else {
      const expiresAt = parseDateTimeLocalInput(batchUpdateForm.expires_at_local)
      if (expiresAt === null) {
        appStore.showError(t('admin.redeem.expiryDateRequired'))
        return null
      }
      fields.expires_at = new Date(expiresAt * 1000).toISOString()
    }
  }
  if (batchUpdateForm.update_notes) {
    fields.notes = batchUpdateForm.notes
  }
  if (batchUpdateForm.update_group_id) {
    fields.group_id =
      batchUpdateForm.group_id == null ? null : Number(batchUpdateForm.group_id)
  }

  return Object.keys(fields).length > 0 ? fields : null
}

const handleGenerateCodes = async () => {
  // 订阅类型必须选择分组
  if (generateForm.type === 'subscription' && !generateForm.group_id) {
    appStore.showError(t('admin.redeem.groupRequired'))
    return
  }

  const expiresInDays = getRedeemCodeExpiresInDays()
  if (expiresInDays === null) {
    appStore.showError(t('admin.redeem.expiryDaysRequired'))
    return
  }

  generating.value = true
  try {
    const result = await adminAPI.redeem.generate(
      generateForm.count,
      generateForm.type,
      generateForm.value,
      generateForm.type === 'subscription' ? generateForm.group_id : undefined,
      generateForm.type === 'subscription' ? generateForm.validity_days : undefined,
      expiresInDays
    )
    showGenerateDialog.value = false
    generatedCodes.value = result
    showResultDialog.value = true
    // 重置表单
    generateForm.group_id = null
    generateForm.validity_days = 30
    generateForm.expiry_option = 'never'
    generateForm.custom_expiry_days = 7
    loadCodes()
  } catch (error: any) {
    appStore.showError(error.response?.data?.detail || t('admin.redeem.failedToGenerate'))
    console.error('Error generating codes:', error)
  } finally {
    generating.value = false
  }
}

const copyToClipboard = async (text: string) => {
  const success = await clipboardCopy(text, t('admin.redeem.copied'))
  if (success) {
    copiedCode.value = text
    setTimeout(() => {
      copiedCode.value = null
    }, 2000)
  }
}

const handleExportCodes = async () => {
  try {
    const blob = await adminAPI.redeem.exportCodes(buildRedeemQueryFilters())

    // Create download link
    const url = window.URL.createObjectURL(blob)
    const link = document.createElement('a')
    link.href = url
    link.download = `redeem-codes-${new Date().toISOString().split('T')[0]}.csv`
    document.body.appendChild(link)
    link.click()
    document.body.removeChild(link)
    window.URL.revokeObjectURL(url)

    appStore.showSuccess(t('admin.redeem.codesExported'))
  } catch (error: any) {
    appStore.showError(error.response?.data?.detail || t('admin.redeem.failedToExport'))
    console.error('Error exporting codes:', error)
  }
}

const handleDelete = (code: RedeemCode) => {
  deletingCode.value = code
  showDeleteDialog.value = true
}

const confirmDelete = async () => {
  if (!deletingCode.value) return

  try {
    await adminAPI.redeem.delete(deletingCode.value.id)
    appStore.showSuccess(t('admin.redeem.codeDeleted'))
    showDeleteDialog.value = false
    deletingCode.value = null
    loadCodes()
  } catch (error: any) {
    appStore.showError(error.response?.data?.detail || t('admin.redeem.failedToDelete'))
    console.error('Error deleting code:', error)
  }
}

const confirmDeleteUnused = async () => {
  try {
    // Get all unused codes and delete them
    const unusedCodes = await fetchPaginatedItems(
      (page, size) => adminAPI.redeem.list(page, size, { status: 'unused' }), 1000, 1000
    )
    const unusedCodeIds = unusedCodes.map((code) => code.id)

    if (unusedCodeIds.length === 0) {
      appStore.showInfo(t('admin.redeem.noUnusedCodes'))
      showDeleteUnusedDialog.value = false
      return
    }

    const result = await adminAPI.redeem.batchDelete(unusedCodeIds)
    appStore.showSuccess(t('admin.redeem.codesDeleted', { count: result.deleted }))
    showDeleteUnusedDialog.value = false
    loadCodes()
  } catch (error: any) {
    appStore.showError(error.response?.data?.detail || t('admin.redeem.failedToDeleteUnused'))
    console.error('Error deleting unused codes:', error)
  }
}

const handleBatchUpdate = async () => {
  const ids = Array.from(selectedCodeIds.value)
  if (ids.length === 0) {
    appStore.showInfo(t('admin.redeem.selectCodesFirst'))
    return
  }

  const hasSelectedFields =
    batchUpdateForm.update_status ||
    batchUpdateForm.update_expires_at ||
    batchUpdateForm.update_notes ||
    batchUpdateForm.update_group_id
  if (!hasSelectedFields) {
    appStore.showError(t('admin.redeem.noBatchFieldsSelected'))
    return
  }

  const fields = buildBatchUpdateFields()
  if (!fields) {
    return
  }

  batchUpdating.value = true
  try {
    const result = await adminAPI.redeem.batchUpdate(ids, fields)
    appStore.showSuccess(t('admin.redeem.batchUpdateSuccess', { count: result.updated }))
    showBatchUpdateDialog.value = false
    clearSelectedCodes()
    loadCodes()
  } catch (error: any) {
    appStore.showError(error.response?.data?.detail || t('admin.redeem.failedToBatchUpdate'))
    console.error('Error batch updating codes:', error)
  } finally {
    batchUpdating.value = false
  }
}

// 加载订阅类型分组
const loadSubscriptionGroups = async () => {
  try {
    const groups = await adminAPI.groups.getAll()
    subscriptionGroups.value = groups
  } catch (error) {
    console.error('Error loading subscription groups:', error)
  }
}

onMounted(() => {
  loadCodes()
  loadSubscriptionGroups()
})

onUnmounted(() => {
  clearTimeout(searchTimeout)
  abortController?.abort()
})
</script>
