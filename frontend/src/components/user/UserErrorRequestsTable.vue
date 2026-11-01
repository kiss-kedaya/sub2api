<template>
  <div class="flex min-h-0 flex-1 flex-col gap-3">
    <div class="flex min-h-0 flex-1 flex-col overflow-hidden rounded-lg ring ring-default">
      <IpGeoBatchToolbar :ips="rows.map((r) => r.client_ip)" @failed="emit('ipGeoBatchFailed')" />

      <DataTable
        :columns="columns"
        :data="rows"
        :loading="loading"
        clickable-rows
        server-side-sort
        default-sort-key="created_at"
        default-sort-order="desc"
        @sort="onSort"
        @rowClick="(row) => openDetail(row.id)"
      >
        <template #cell-model="{ row }">
          <span v-if="row.model" class="text-sm font-medium text-highlighted">{{ row.model }}</span>
          <span v-else class="text-sm text-dimmed">-</span>
        </template>

        <template #cell-key_name="{ row }">
          <div class="flex items-center gap-1 text-sm">
            <span class="text-highlighted">{{ row.key_name || '-' }}</span>
            <UBadge v-if="row.key_deleted" color="error" variant="subtle" size="sm">{{ t('usage.errors.keyDeleted') }}</UBadge>
          </div>
        </template>

        <template #cell-endpoint="{ row }">
          <div class="max-w-[320px] break-all text-xs text-toned">
            <span class="font-medium text-muted">{{ t('usage.inbound') }}:</span>
            <span class="ml-1">{{ row.inbound_endpoint?.trim() || '-' }}</span>
          </div>
        </template>

        <template #cell-status="{ row }">
          <UBadge :color="statusColor(row.status_code)" variant="subtle" class="tabular-nums">{{ row.status_code || '-' }}</UBadge>
        </template>

        <template #cell-category="{ row }">
          <span class="text-sm text-highlighted">{{ t('usage.errors.categories.' + row.category) }}</span>
        </template>

        <template #cell-message="{ row }">
          <span v-if="row.message" class="block max-w-[280px] truncate text-sm text-muted" :title="row.message">{{ row.message }}</span>
          <span v-else class="text-sm text-dimmed">-</span>
        </template>

        <template #cell-group="{ row }">
          <UBadge v-if="row.group_name" color="neutral" variant="subtle">{{ row.group_name }}</UBadge>
          <span v-else class="text-sm text-dimmed">-</span>
        </template>

        <template #cell-type="{ row }">
          <UBadge v-if="requestTypeBadge(row)" :color="requestTypeBadge(row)!.color" variant="subtle">{{ requestTypeBadge(row)!.label }}</UBadge>
          <span v-else class="text-sm text-dimmed">-</span>
        </template>

        <template #cell-platform="{ row }">
          <span class="text-sm text-highlighted">{{ row.platform || '-' }}</span>
        </template>

        <template #cell-client_ip="{ row }">
          <div @click.stop>
            <div v-if="row.client_ip">
              <span class="font-mono text-sm text-muted">{{ row.client_ip }}</span>
              <IpGeoCell :ip="row.client_ip" />
            </div>
            <span v-else class="text-sm text-dimmed">-</span>
          </div>
        </template>

        <template #cell-created_at="{ row }">
          <span class="text-sm text-muted">{{ formatDateTime(row.created_at) }}</span>
        </template>

        <template #cell-user_agent="{ row }">
          <span v-if="row.user_agent" class="block max-w-[320px] truncate text-sm text-muted" :title="row.user_agent">{{ row.user_agent }}</span>
          <span v-else class="text-sm text-dimmed">-</span>
        </template>

        <template #empty><EmptyState :message="t('usage.errors.empty')" /></template>
      </DataTable>
    </div>

    <div class="flex-shrink-0">
      <Pagination
        v-if="total > 0"
        :page="page"
        :page-size="pageSize"
        :total="total"
        @update:page="$emit('update:page', $event)"
        @update:pageSize="$emit('update:pageSize', $event)"
      />
    </div>

    <UserErrorDetailModal v-model:show="showDetail" :error-id="selectedId" />
  </div>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import DataTable from '@/components/common/DataTable.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import Pagination from '@/components/common/Pagination.vue'
import UserErrorDetailModal from '@/components/user/UserErrorDetailModal.vue'
import IpGeoCell from '@/components/common/IpGeoCell.vue'
import IpGeoBatchToolbar from '@/components/common/IpGeoBatchToolbar.vue'
import { formatDateTime } from '@/utils/format'
import {
  mapErrorSortKey,
  numericRequestTypeKind,
  requestTypeLabelKey,
} from '@/utils/errorBadges'
import type { UserErrorRequest } from '@/types'
import type { Column } from '@/components/common/types'

const props = defineProps<{
  rows: UserErrorRequest[]
  total: number
  loading: boolean
  page: number
  pageSize: number
  /** 列设置:仅显示这些 key 的列;不传则全显(key 须与 allColumns 一致) */
  visibleColumnKeys?: string[]
}>()

const emit = defineEmits<{
  (e: 'update:page', v: number): void
  (e: 'update:pageSize', v: number): void
  (e: 'ipGeoBatchFailed'): void
  (e: 'sort', sortBy: string, sortOrder: 'asc' | 'desc'): void
}>()

function onSort(key: string, order: 'asc' | 'desc') {
  emit('sort', mapErrorSortKey(key), order)
}

const { t } = useI18n()

// 列序对齐用户端用量明细:Key → 模型 → 端点 → IP → 分组 → 类型 → 平台 → 分类
// → 结果(状态→消息)→ 时间 → UA(用量明细 UA 同在时间之后的尾部)
const allColumns = computed<Column[]>(() => [
  { key: 'key_name', label: t('usage.errors.keyName') },
  { key: 'model', label: t('usage.errors.model'), sortable: true },
  { key: 'endpoint', label: t('usage.errors.endpoint') },
  { key: 'client_ip', label: 'IP' },
  { key: 'group', label: t('admin.usage.group') },
  { key: 'type', label: t('usage.type') },
  { key: 'platform', label: t('usage.errors.platform') },
  { key: 'category', label: t('usage.errors.category') },
  { key: 'status', label: t('usage.errors.status'), sortable: true },
  { key: 'message', label: t('usage.errors.message') },
  { key: 'created_at', label: t('usage.errors.time'), sortable: true },
  { key: 'user_agent', label: t('usage.userAgent') },
])

const columns = computed<Column[]>(() =>
  props.visibleColumnKeys
    ? allColumns.value.filter((c) => props.visibleColumnKeys!.includes(c.key))
    : allColumns.value
)

type BadgeColor = 'error' | 'warning' | 'info' | 'success' | 'neutral'

// Same buckets as utils/errorBadges, expressed as Nuxt UI badge colours.
const requestTypeColors: Record<string, BadgeColor> = { cyber: 'error', live: 'success', ws_v2: 'info', stream: 'info', sync: 'neutral' }

function requestTypeBadge(row: UserErrorRequest): { label: string; color: BadgeColor } | null {
  const kind = numericRequestTypeKind(row.request_type, row.stream)
  if (!kind) return null
  return { label: t(requestTypeLabelKey(kind)), color: requestTypeColors[kind] ?? 'warning' }
}

const showDetail = ref(false)
const selectedId = ref<number | null>(null)

function openDetail(id: number) {
  selectedId.value = id
  showDetail.value = true
}

function statusColor(code: number): BadgeColor {
  if (code >= 500) return 'error'
  if (code === 429) return 'info'
  if (code >= 400) return 'warning'
  return 'neutral'
}
</script>
