<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useMediaQuery } from '@vueuse/core'
import { useI18n } from 'vue-i18n'
import type { TableColumn } from '@nuxt/ui'
import { compactTableUi } from '../utils/templateUi'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Pagination from '@/components/common/Pagination.vue'
import { useClipboard } from '@/composables/useClipboard'
import { useAppStore } from '@/stores'
import { opsAPI, type OpsRequestDetailsParams, type OpsRequestDetail } from '@/api/admin/ops'
import { parseTimeRangeMinutes, formatDateTime } from '../utils/opsFormatters'

export interface OpsRequestDetailsPreset {
  title: string
  kind?: OpsRequestDetailsParams['kind']
  sort?: OpsRequestDetailsParams['sort']
  min_duration_ms?: number
  max_duration_ms?: number
}

interface Props {
  modelValue: boolean
  timeRange: string
  preset: OpsRequestDetailsPreset
  platform?: string
  groupId?: number | null
  resumeState?: boolean
}

const props = defineProps<Props>()
const emit = defineEmits<{
  (e: 'update:modelValue', value: boolean): void
  (e: 'openErrorDetail', errorId: number): void
}>()

const { t } = useI18n()
const appStore = useAppStore()
const { copyToClipboard } = useClipboard()

// 与 DataTable 一致：< 768px 切换为卡片视图，避免宽表在移动端被截断。
const isDesktopViewport = useMediaQuery('(min-width: 768px)')

const loading = ref(false)
const items = ref<OpsRequestDetail[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = ref(10)

const close = () => emit('update:modelValue', false)

const showTTFT = computed(() => props.preset.sort === 'ttft_desc')
const latencyLabel = computed(() => t(showTTFT.value ? 'admin.ops.ttftLabel' : 'admin.ops.requestDetails.table.duration'))

function formatLatency(row: OpsRequestDetail): string {
  const value = showTTFT.value ? row.first_token_ms : row.duration_ms
  return typeof value === 'number' ? `${value} ms` : '-'
}

const rangeLabel = computed(() => {
  const minutes = parseTimeRangeMinutes(props.timeRange)
  if (minutes >= 60) return t('admin.ops.requestDetails.rangeHours', { n: Math.round(minutes / 60) })
  return t('admin.ops.requestDetails.rangeMinutes', { n: minutes })
})

function buildTimeParams(): Pick<OpsRequestDetailsParams, 'start_time' | 'end_time'> {
  const minutes = parseTimeRangeMinutes(props.timeRange)
  const endTime = new Date()
  const startTime = new Date(endTime.getTime() - minutes * 60 * 1000)
  return {
    start_time: startTime.toISOString(),
    end_time: endTime.toISOString()
  }
}

const fetchData = async () => {
  if (!props.modelValue) return
  loading.value = true
  try {
    const params: OpsRequestDetailsParams = {
      ...buildTimeParams(),
      page: page.value,
      page_size: pageSize.value,
      kind: props.preset.kind ?? 'all',
      sort: props.preset.sort ?? 'created_at_desc'
    }

    const platform = (props.platform || '').trim()
    if (platform) params.platform = platform
    if (typeof props.groupId === 'number' && props.groupId > 0) params.group_id = props.groupId

    if (typeof props.preset.min_duration_ms === 'number') params.min_duration_ms = props.preset.min_duration_ms
    if (typeof props.preset.max_duration_ms === 'number') params.max_duration_ms = props.preset.max_duration_ms

    const res = await opsAPI.listRequestDetails(params)
    items.value = res.items || []
    total.value = res.total || 0
  } catch (e: any) {
    console.error('[OpsRequestDetailsModal] Failed to fetch request details', e)
    appStore.showError(e?.message || t('admin.ops.requestDetails.failedToLoad'))
    items.value = []
    total.value = 0
  } finally {
    loading.value = false
  }
}

watch(
  () => props.modelValue,
  (open) => {
    if (open) {
      if (props.resumeState) return
      page.value = 1
      pageSize.value = 10
      fetchData()
    }
  }
)

watch(
  () => [
    props.timeRange,
    props.platform,
    props.groupId,
    props.preset.kind,
    props.preset.sort,
    props.preset.min_duration_ms,
    props.preset.max_duration_ms
  ],
  () => {
    if (!props.modelValue) return
    page.value = 1
    fetchData()
  }
)

function handlePageChange(next: number) {
  page.value = next
  fetchData()
}

function handlePageSizeChange(next: number) {
  pageSize.value = next
  page.value = 1
  fetchData()
}

async function handleCopyRequestId(requestId: string) {
  const ok = await copyToClipboard(requestId, t('admin.ops.requestDetails.requestIdCopied'))
  if (ok) return
  // `useClipboard` already shows toast on failure; this keeps UX consistent with older ops modal.
  appStore.showWarning(t('admin.ops.requestDetails.copyFailed'))
}

function openErrorDetail(errorId: number | null | undefined) {
  if (!errorId) return
  emit('openErrorDetail', errorId)
}

const requestTableUi = { ...compactTableUi, thead: 'bg-elevated/50 [&>tr]:after:content-none', th: 'px-4 py-3 text-[11px] font-semibold uppercase tracking-wider text-muted border-b border-default', td: 'px-4 py-3 border-b border-default' }
const requestColumns = computed<TableColumn<OpsRequestDetail>[]>(() => [
  { id: 'time', header: t('admin.ops.requestDetails.table.time') },
  { id: 'kind', header: t('admin.ops.requestDetails.table.kind') },
  { id: 'platform', header: t('admin.ops.requestDetails.table.platform') },
  { id: 'model', header: t('admin.ops.requestDetails.table.model') },
  { id: 'latency', header: latencyLabel.value },
  { id: 'status', header: t('admin.ops.requestDetails.table.status') },
  { id: 'request-id', header: t('admin.ops.requestDetails.table.requestId') },
  { id: 'actions', header: t('admin.ops.requestDetails.table.actions'), meta: { class: { th: 'text-right' } } },
])
</script>

<template>
  <BaseDialog :show="modelValue" :title="props.preset.title || t('admin.ops.requestDetails.title')" width="full" @close="close">
    <template #default>
      <div class="flex h-full min-h-0 flex-col">
        <div class="mb-4 flex flex-shrink-0 items-center justify-between">
          <div class="text-xs text-muted">
            {{ t('admin.ops.requestDetails.rangeLabel', { range: rangeLabel }) }}
          </div>
          <UButton
            type="button"
            color="neutral"
            variant="outline"
            size="sm"
            @click="fetchData"
          >
            {{ t('common.refresh') }}
          </UButton>
        </div>

        <!-- Loading -->
        <div v-if="loading" class="flex flex-1 items-center justify-center py-16">
          <div class="flex flex-col items-center gap-3">
            <svg class="h-8 w-8 animate-spin text-primary" fill="none" viewBox="0 0 24 24">
              <circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4"></circle>
              <path
                class="opacity-75"
                fill="currentColor"
                d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"
              ></path>
            </svg>
            <span class="text-sm font-medium text-muted">{{ t('common.loading') }}</span>
          </div>
        </div>

        <!-- Table -->
        <div v-else class="flex min-h-0 flex-1 flex-col">
          <div v-if="items.length === 0" class="rounded-xl border border-dashed border-default p-10 text-center">
            <div class="text-sm font-medium text-toned">{{ t('admin.ops.requestDetails.empty') }}</div>
            <div class="mt-1 text-xs text-dimmed">{{ t('admin.ops.requestDetails.emptyHint') }}</div>
          </div>

          <div v-else class="flex min-h-0 flex-1 flex-col overflow-hidden rounded-xl border border-default">
            <div class="min-h-0 flex-1 overflow-auto">
              <div v-if="!isDesktopViewport" class="divide-y divide-default">
                <div v-for="(row, idx) in items" :key="idx" class="space-y-2 p-4">
                  <div class="flex flex-wrap items-center gap-2">
                    <UBadge :color="row.kind === 'error' ? 'error' : 'success'" variant="subtle" size="sm">
                      {{ row.kind === 'error' ? t('admin.ops.requestDetails.kind.error') : t('admin.ops.requestDetails.kind.success') }}
                    </UBadge>
                    <span class="text-xs font-medium text-default">{{ (row.platform || 'unknown').toUpperCase() }}</span>
                    <span class="ml-auto text-[11px] text-muted">{{ formatDateTime(row.created_at) }}</span>
                  </div>
                  <div class="break-all text-xs text-toned">{{ row.model || '-' }}</div>
                  <div class="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-toned">
                    <span>{{ latencyLabel }}: {{ formatLatency(row) }}</span>
                    <span>{{ row.status_code ?? '-' }}</span>
                  </div>
                  <div v-if="row.request_id" class="flex items-center gap-2">
                    <span class="min-w-0 flex-1 truncate font-mono text-[11px] text-default" :title="row.request_id">
                      {{ row.request_id }}
                    </span>
                    <UButton
                      type="submit"
                      color="neutral"
                      variant="ghost"
                      size="sm"
                      class="shrink-0 text-[10px] font-bold"
                      @click="handleCopyRequestId(row.request_id)"
                    >
                      {{ t('admin.ops.requestDetails.copy') }}
                    </UButton>
                  </div>
                  <UButton
                    type="submit"
                    color="error"
                    variant="ghost"
                    size="sm"
                    class="w-full text-xs font-bold"
                    v-if="row.kind === 'error' && row.error_id"
                    @click="openErrorDetail(row.error_id)"
                  >
                    {{ t('admin.ops.requestDetails.viewError') }}
                  </UButton>
                </div>
              </div>
              <UTable v-else :data="items" :columns="requestColumns" sticky :ui="requestTableUi">
                <template #time-cell="{ row }">
                  <span class="whitespace-nowrap text-xs text-toned">{{ formatDateTime(row.original.created_at) }}</span>
                </template>
                <template #kind-cell="{ row }">
                  <UBadge :color="row.original.kind === 'error' ? 'error' : 'success'" variant="subtle" size="sm">
                    {{ row.original.kind === 'error' ? t('admin.ops.requestDetails.kind.error') : t('admin.ops.requestDetails.kind.success') }}
                  </UBadge>
                </template>
                <template #platform-cell="{ row }">
                  <span class="whitespace-nowrap text-xs font-medium text-default">{{ (row.original.platform || 'unknown').toUpperCase() }}</span>
                </template>
                <template #model-cell="{ row }">
                  <span class="block max-w-[240px] truncate text-xs text-toned" :title="row.original.model || ''">{{ row.original.model || '-' }}</span>
                </template>
                <template #latency-cell="{ row }">
                  <span class="whitespace-nowrap text-xs text-toned">{{ formatLatency(row.original) }}</span>
                </template>
                <template #status-cell="{ row }">
                  <span class="whitespace-nowrap text-xs text-toned">{{ row.original.status_code ?? '-' }}</span>
                </template>
                <template #request-id-cell="{ row }">
                  <div v-if="row.original.request_id" class="flex items-center gap-2">
                    <span class="max-w-[220px] truncate font-mono text-[11px] text-default" :title="row.original.request_id">
                      {{ row.original.request_id }}
                    </span>
                    <UButton type="button" color="neutral" variant="ghost" size="xs" icon="i-lucide-copy" :label="t('admin.ops.requestDetails.copy')" @click="handleCopyRequestId(row.original.request_id)" />
                  </div>
                  <span v-else class="text-xs text-dimmed">-</span>
                </template>
                <template #actions-cell="{ row }">
                  <div class="flex justify-end">
                    <UButton
                      v-if="row.original.kind === 'error' && row.original.error_id"
                      type="button"
                      color="error"
                      variant="ghost"
                      size="xs"
                      :label="t('admin.ops.requestDetails.viewError')"
                      @click="openErrorDetail(row.original.error_id)"
                    />
                    <span v-else class="text-xs text-dimmed">-</span>
                  </div>
                </template>
              </UTable>
            </div>

            <Pagination
              :total="total"
              :page="page"
              :page-size="pageSize"
              @update:page="handlePageChange"
              @update:pageSize="handlePageSizeChange"
            />
          </div>
        </div>
      </div>
    </template>
  </BaseDialog>
</template>
