<template>
  <div class="card overflow-hidden"><div class="overflow-auto">
    <DataTable :columns="cols" :data="data" :loading="loading">
      <template #cell-user="{ row }"><div class="text-sm"><span class="font-medium text-gray-900 dark:text-white">{{ row.user?.email || '-' }}</span><span class="ml-1 text-xs text-gray-400">#{{ row.user_id }}</span></div></template>
      <template #cell-model="{ value }"><span class="font-medium">{{ value }}</span></template>
      <template #cell-tokens="{ row }"><div class="text-sm">In: {{ row.input_tokens.toLocaleString() }} / Out: {{ row.output_tokens.toLocaleString() }}</div></template>
      <template #cell-cost="{ row }"><span class="font-medium text-green-600">${{ row.actual_cost.toFixed(6) }}</span></template>
      <template #cell-created_at="{ value }"><span class="text-sm text-gray-500">{{ formatDateTime(value) }}</span></template>
      <template #empty><EmptyState :message="t('usage.noRecords')" /></template>
    </DataTable>
  </div></div>
</template>

<script setup lang="ts">
import { computed } from 'vue'; import { useI18n } from 'vue-i18n'; import { formatDateTime } from '@/utils/format'; import DataTable from '@/components/common/DataTable.vue'; import EmptyState from '@/components/common/EmptyState.vue'
defineProps(['data', 'loading']); const { t } = useI18n()
const cols = computed(() => [
  { key: 'user', label: t('admin.usage.user') }, { key: 'model', label: t('usage.model'), sortable: true },
  { key: 'tokens', label: t('usage.tokens') }, { key: 'cost', label: t('usage.cost') },
  { key: 'created_at', label: t('usage.time'), sortable: true }
])
</script>