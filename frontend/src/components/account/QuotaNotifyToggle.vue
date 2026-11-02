<script setup lang="ts">
import { QUOTA_THRESHOLD_TYPE_FIXED, QUOTA_THRESHOLD_TYPE_PERCENTAGE, type QuotaThresholdType } from '@/constants/account'

defineProps<{
  enabled: boolean | null
  threshold: number | null
  thresholdType: QuotaThresholdType | null
}>()

const emit = defineEmits<{
  'update:enabled': [value: boolean | null]
  'update:threshold': [value: number | null]
  'update:thresholdType': [value: QuotaThresholdType | null]
}>()

const thresholdTypeItems = [
  { value: QUOTA_THRESHOLD_TYPE_FIXED, label: '$' },
  { value: QUOTA_THRESHOLD_TYPE_PERCENTAGE, label: '%' }
]
</script>

<template>
  <div class="flex items-center gap-1.5">
    <USwitch size="sm" :model-value="enabled" @update:model-value="emit('update:enabled', $event === true)" />
    <template v-if="enabled">
      <UInput
        :model-value="threshold == null ? '' : String(threshold)"
        type="number"
        min="0"
        :max="thresholdType === QUOTA_THRESHOLD_TYPE_PERCENTAGE ? 100 : undefined"
        :step="thresholdType === QUOTA_THRESHOLD_TYPE_PERCENTAGE ? 1 : 0.01"
        size="sm"
        class="min-w-0 flex-1"
        @update:model-value="emit('update:threshold', parseFloat(String($event)) || null)"
      />
      <USelect
        :model-value="thresholdType || QUOTA_THRESHOLD_TYPE_FIXED"
        :items="thresholdTypeItems"
        size="sm"
        class="w-18 shrink-0"
        @update:model-value="emit('update:thresholdType', $event as QuotaThresholdType)"
      />
    </template>
  </div>
</template>
