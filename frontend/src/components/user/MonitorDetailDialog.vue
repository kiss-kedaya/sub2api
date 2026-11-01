<template>
  <BaseDialog
    :show="show"
    :title="title"
    width="wide"
    @close="$emit('close')"
  >
    <div v-if="loading" class="py-8 text-center text-sm text-muted">
      {{ t('common.loading') }}
    </div>
    <div v-else-if="!detail" class="py-8 text-center text-sm text-muted">
      {{ t('channelStatus.detailLoadError') }}
    </div>
    <UTable
      v-else
      :data="detail.models"
      :columns="detailColumns"
      :ui="{ th: 'py-2 text-xs font-medium text-muted', td: 'py-2 text-toned' }"
    >
      <template #model-cell="{ row }"><span class="font-medium text-highlighted">{{ formatMonitorModel(row.original.model) }}</span></template>
      <template #latest_status-cell="{ row }">
        <span class="inline-flex items-center rounded-full px-2 py-0.5 text-[11px]" :class="statusBadgeClass(row.original.latest_status)">{{ statusLabel(row.original.latest_status) }}</span>
      </template>
      <template #latest_latency_ms-cell="{ row }">{{ formatLatency(row.original.latest_latency_ms) }}</template>
      <template #availability_7d-cell="{ row }">{{ formatPercent(row.original.availability_7d) }}</template>
      <template #availability_15d-cell="{ row }">{{ formatPercent(row.original.availability_15d) }}</template>
      <template #availability_30d-cell="{ row }">{{ formatPercent(row.original.availability_30d) }}</template>
      <template #avg_latency_7d_ms-cell="{ row }">{{ formatLatency(row.original.avg_latency_7d_ms) }}</template>
    </UTable>

    <template #footer>
      <div class="flex justify-end">
        <UButton color="neutral" variant="subtle" @click="$emit('close')">
          {{ t('channelStatus.closeDetail') }}
        </UButton>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import type { TableColumn } from '@nuxt/ui'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'
import {
  status as fetchChannelMonitorDetail,
  type UserMonitorDetail,
} from '@/api/channelMonitor'
import BaseDialog from '@/components/common/BaseDialog.vue'
import { useChannelMonitorFormat } from '@/composables/useChannelMonitorFormat'

const props = defineProps<{
  show: boolean
  monitorId: number | null
  title: string
}>()

defineEmits<{
  (e: 'close'): void
}>()

const { t } = useI18n()
const appStore = useAppStore()
const { statusLabel, statusBadgeClass, formatLatency, formatPercent, formatMonitorModel } = useChannelMonitorFormat()

const detail = ref<UserMonitorDetail | null>(null)
const loading = ref(false)

type DetailModel = UserMonitorDetail['models'][number]
const detailColumns = computed<TableColumn<DetailModel>[]>(() => [
  { accessorKey: 'model', header: t('channelStatus.detailColumns.model') },
  { accessorKey: 'latest_status', header: t('channelStatus.detailColumns.latestStatus') },
  { accessorKey: 'latest_latency_ms', header: t('channelStatus.detailColumns.latestLatency') },
  { accessorKey: 'availability_7d', header: t('channelStatus.detailColumns.availability7d') },
  { accessorKey: 'availability_15d', header: t('channelStatus.detailColumns.availability15d') },
  { accessorKey: 'availability_30d', header: t('channelStatus.detailColumns.availability30d') },
  { accessorKey: 'avg_latency_7d_ms', header: t('channelStatus.detailColumns.avgLatency7d') }
])

async function load(id: number) {
  detail.value = null
  loading.value = true
  try {
    detail.value = await fetchChannelMonitorDetail(id)
  } catch (err: unknown) {
    appStore.showError(extractApiErrorMessage(err, t('channelStatus.detailLoadError')))
  } finally {
    loading.value = false
  }
}

watch(
  () => [props.show, props.monitorId] as const,
  ([show, id]) => {
    if (!show) {
      detail.value = null
      return
    }
    if (id != null) void load(id)
  },
  { immediate: true },
)
</script>
