<template>
  <DashboardChartCard :title="title">
    <template #actions>
      <UButton :to="to" color="neutral" variant="ghost" size="sm" trailing-icon="i-lucide-arrow-right" class="dashboard-view-all -me-2">
        {{ viewAllLabel }}
      </UButton>
    </template>
    <div class="p-4 sm:px-6">
      <div v-if="loading" class="flex min-h-40 items-center justify-center"><LoadingSpinner size="sm" /></div>
      <p v-else-if="error" class="flex min-h-40 items-center justify-center text-sm text-muted">{{ errorLabel }}</p>
      <p v-else-if="rows.length === 0" class="flex min-h-40 items-center justify-center text-sm text-muted">{{ emptyLabel }}</p>
      <UTable
        v-else
        class="dashboard-top-list"
        :data="rows"
        :columns="columns"
        :ui="compactTableUi"
        v-bind="selectable ? { onSelect: onRowSelect } : {}"
      />
    </div>
  </DashboardChartCard>
</template>

<script setup lang="ts" generic="T extends object">
// A short "attention" list for an overview page: the first few rows of a list whose
// full version lives on another page, with a "view all" link to that page.
import type { TableColumn, TableRow } from '@nuxt/ui'
import type { RouteLocationRaw } from 'vue-router'
import LoadingSpinner from '@/components/common/LoadingSpinner.vue'
import DashboardChartCard from './DashboardChartCard.vue'
import { compactTableUi } from './dashboardUi'

const props = defineProps<{
  title: string
  rows: T[]
  columns: TableColumn<T>[]
  to: RouteLocationRaw
  viewAllLabel: string
  emptyLabel: string
  errorLabel: string
  loading?: boolean
  error?: boolean
  /** Rows open their own detail (click / Enter / Space) when set. */
  selectable?: boolean
}>()

const emit = defineEmits<{ select: [row: T] }>()

function onRowSelect(_event: Event, row: TableRow<T>) {
  if (props.selectable) emit('select', row.original)
}
</script>
