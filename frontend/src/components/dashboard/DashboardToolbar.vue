<template>
  <!-- The frame's toolbar row under the navbar (in place when there is no frame). -->
  <PageToolbar>
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
  </PageToolbar>
</template>

<script setup lang="ts">
// Dashboard filters in the frame's toolbar row (the template's UDashboardToolbar):
// date range and granularity as quiet ghost controls on the left, refresh on the right.
import DateRangePicker from '@/components/common/DateRangePicker.vue'
import PageToolbar from '@/components/layout/PageToolbar.vue'

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
   The doubled class outranks the frame's 32px rule for legacy controls in the toolbar. */
.dash-range.dash-range :deep(.date-picker-trigger) {
  min-height: 32px;
  padding: 6px 10px;
  gap: 6px;
  border-color: transparent;
  background: transparent;
  box-shadow: none;
  color: var(--ui-text);
  font-weight: 500;
}

.dash-range.dash-range :deep(.date-picker-trigger:hover),
.dash-range.dash-range :deep(.date-picker-trigger-open) {
  border-color: transparent;
  background: var(--ui-bg-elevated);
  box-shadow: none;
}

.dash-range.dash-range :deep(.date-picker-trigger:focus-visible) {
  outline: 2px solid var(--ui-border-inverted);
  outline-offset: 2px;
}

.dash-range.dash-range :deep(:is(.date-picker-icon, .date-picker-chevron)) {
  color: var(--ui-text-dimmed);
}

.dash-range.dash-range :deep(.date-picker-icon) {
  color: var(--ui-text);
}
</style>
