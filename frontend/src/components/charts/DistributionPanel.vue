<template>
  <!-- Doughnut + compact UTable, shared by the model / group / endpoint distribution cards. -->
  <div class="flex flex-col items-center gap-4 sm:flex-row sm:gap-6">
    <div class="size-48 shrink-0">
      <Doughnut :data="chartData" :options="chartOptions" />
    </div>
    <div class="max-h-56 w-full min-w-0 flex-1 overflow-auto">
      <UTable
        :data="rows"
        :columns="columns"
        :get-row-id="getRowId"
        :expanded="expandedState"
        :on-select="selectable ? onRowSelect : undefined"
        :meta="tableMeta"
        sticky
        class="text-xs"
        :ui="{
          base: 'text-xs',
          thead: 'bg-default',
          th: 'px-2 py-1.5 text-xs font-medium text-muted first:pl-0 last:pr-0',
          td: 'px-2 py-1.5 text-xs text-muted first:pl-0 last:pr-0 whitespace-nowrap',
          separator: 'h-px bg-(--ui-border-muted)',
          tbody: 'divide-y divide-default'
        }"
      >
        <template v-for="(_, name) in $slots" #[name]="slotProps">
          <slot :name="name" v-bind="slotProps || {}" />
        </template>
      </UTable>
    </div>
  </div>
</template>

<script setup lang="ts" generic="T extends object">
import { computed } from 'vue'
import { Doughnut } from 'vue-chartjs'
import type { TableColumn, TableRow } from '@nuxt/ui'

const props = defineProps<{
  chartData: any
  chartOptions: any
  rows: T[]
  columns: TableColumn<T>[]
  getRowId: (row: T, index: number) => string
  /** Id of the row whose breakdown is open (rendered through the `expanded` slot). */
  expandedId?: string | null
  /** Rows the user can click; others stay static. */
  isSelectable?: (row: T) => boolean
}>()

const emit = defineEmits<{ select: [row: T] }>()

const selectable = computed(() => !!props.isSelectable)
const expandedState = computed(() => (props.expandedId ? { [props.expandedId]: true } : {}))

const tableMeta = computed(() => ({
  class: {
    tr: (row: TableRow<T>) => (props.isSelectable?.(row.original) ? 'cursor-pointer hover:bg-elevated/50' : '')
  }
}))

function onRowSelect(_event: Event, row: TableRow<T>) {
  if (props.isSelectable?.(row.original)) emit('select', row.original)
}
</script>
