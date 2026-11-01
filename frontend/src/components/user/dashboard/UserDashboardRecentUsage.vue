<template>
  <section class="signal-recent min-w-0 pt-4">
    <header v-if="startDate && endDate" class="mb-3 flex justify-end">
      <span class="text-xs text-muted tabular-nums">{{ startDate }} / {{ endDate }}</span>
    </header>
    <div v-if="loading" class="flex justify-center py-12"><LoadingSpinner size="lg" /></div>
    <div v-else-if="data.length === 0" class="py-6">
      <EmptyState :title="t('dashboard.noUsageRecords')" :description="t('dashboard.startUsingApi')" />
    </div>
    <template v-else>
      <UTable
        :data="data"
        :columns="columns"
        :meta="{ class: { tr: 'signal-request' } }"
        :ui="dashboardTableUi"
      >
        <template #model-cell="{ row }">
          <span class="signal-request-model font-medium text-highlighted break-all">{{ row.original.model }}</span>
        </template>
        <template #created_at-cell="{ row }">
          <time :datetime="row.original.created_at">{{ formatDateTime(row.original.created_at) }}</time>
        </template>
        <template #tokens-header><div class="text-right">{{ t('dashboard.tokens') }}</div></template>
        <template #tokens-cell="{ row }">
          <div class="signal-request-tokens text-right tabular-nums">{{ totalTokens(row.original).toLocaleString() }}</div>
        </template>
        <template #actual-header><div class="text-right">{{ t('dashboard.actual') }}</div></template>
        <template #actual-cell="{ row }">
          <div class="text-right"><span class="signal-actual font-medium text-highlighted tabular-nums">${{ formatCost(row.original.actual_cost) }}</span></div>
        </template>
        <template #standard-header><div class="text-right">{{ t('dashboard.standard') }}</div></template>
        <template #standard-cell="{ row }">
          <div class="text-right"><span class="signal-standard tabular-nums">${{ formatCost(row.original.total_cost) }}</span></div>
        </template>
      </UTable>
      <div class="flex justify-end pt-2">
        <UButton to="/usage" color="neutral" variant="ghost" trailing-icon="i-lucide-arrow-right" class="signal-view-all">{{ t('dashboard.viewAllUsage') }}</UButton>
      </div>
    </template>
  </section>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { TableColumn } from '@nuxt/ui'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import { dashboardTableUi } from '@/components/dashboard/dashboardUi'
import { formatDateTime } from '@/utils/format'
import type { UsageLog } from '@/types'

defineProps<{
  data: UsageLog[]
  loading: boolean
  startDate?: string
  endDate?: string
}>()
const { t } = useI18n()
const formatCost = (c: number) => c.toFixed(4)
const totalTokens = (log: UsageLog) => log.input_tokens + log.output_tokens + (log.cache_read_tokens || 0) + (log.cache_creation_tokens || 0)

const columns = computed<TableColumn<UsageLog>[]>(() => [
  { accessorKey: 'model', header: t('dashboard.model') },
  { accessorKey: 'created_at', header: t('usage.time') },
  { id: 'tokens' },
  { id: 'actual' },
  { id: 'standard' },
])
</script>
