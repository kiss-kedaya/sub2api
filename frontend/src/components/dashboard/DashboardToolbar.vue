<template>
  <UDashboardToolbar
    class="dash-toolbar"
    :ui="{ root: 'border-b-0 px-0 sm:px-0 min-h-0 flex-wrap gap-y-2 overflow-visible', left: 'flex-wrap', right: 'ms-auto' }"
  >
    <template #left>
      <DateRangePicker
        class="dash-range -ms-1"
        :start-date="startDate"
        :end-date="endDate"
        @update:startDate="emit('update:startDate', $event)"
        @update:endDate="emit('update:endDate', $event)"
        @change="emit('dateRangeChange', $event)"
      />
      <USelect
        :model-value="granularity"
        :items="granularityItems"
        :aria-label="granularityLabel"
        variant="ghost"
        class="data-[state=open]:bg-elevated"
        :ui="{ base: 'text-sm', trailingIcon: 'group-data-[state=open]:rotate-180 transition-transform duration-200' }"
        @update:model-value="onGranularity"
      />
    </template>
    <template #right>
      <UButton
        color="neutral"
        variant="ghost"
        icon="i-lucide-refresh-cw"
        :ui="{ leadingIcon: refreshing ? 'animate-spin' : '' }"
        :title="refreshLabel"
        :aria-label="refreshLabel"
        :disabled="refreshing"
        @click="emit('refresh')"
      />
    </template>
  </UDashboardToolbar>
</template>

<script setup lang="ts">
// Filter row at the top of a dashboard body, as the template's UDashboardToolbar:
// date range and granularity as quiet ghost controls, refresh on the right.
import DateRangePicker from '@/components/common/DateRangePicker.vue'

defineProps<{
  startDate: string
  endDate: string
  granularity: string
  granularityItems: { label: string; value: string }[]
  granularityLabel: string
  refreshLabel: string
  refreshing?: boolean
}>()

const emit = defineEmits<{
  'update:startDate': [value: string]
  'update:endDate': [value: string]
  'update:granularity': [value: string]
  dateRangeChange: [range: { startDate: string; endDate: string; preset: string | null }]
  granularityChange: [value: string]
  refresh: []
}>()

function onGranularity(value: unknown) {
  emit('update:granularity', String(value))
  emit('granularityChange', String(value))
}
</script>

<style scoped>
/* The shared picker keeps its own dropdown; only its trigger becomes a ghost button here.
   Two classes on the root outrank the console's `html .console-signal .signal-main` rule. */
.dash-toolbar .dash-range.dash-range :deep(.date-picker-trigger) {
  min-height: 32px;
  padding: 6px 10px;
  gap: 6px;
  border-color: transparent;
  background: transparent;
  box-shadow: none;
  color: var(--ui-text);
  font-weight: 500;
}

.dash-toolbar .dash-range.dash-range :deep(.date-picker-trigger:hover),
.dash-toolbar .dash-range.dash-range :deep(.date-picker-trigger-open) {
  border-color: transparent;
  background: var(--ui-bg-elevated);
  box-shadow: none;
}

.dash-toolbar .dash-range.dash-range :deep(.date-picker-trigger:focus-visible) {
  outline: 2px solid var(--ui-border-inverted);
  outline-offset: 2px;
}

.dash-toolbar .dash-range.dash-range :deep(:is(.date-picker-icon, .date-picker-chevron)) {
  color: var(--ui-text-dimmed);
}

.dash-toolbar .dash-range.dash-range :deep(.date-picker-icon) {
  color: var(--ui-text);
}
</style>
