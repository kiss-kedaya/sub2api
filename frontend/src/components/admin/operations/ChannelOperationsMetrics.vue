<template>
  <dl class="grid grid-cols-2 gap-3 md:grid-cols-4">
    <div v-for="field in fields" :key="field.key" class="rounded-xl bg-gray-50 p-3 dark:bg-dark-800">
      <dt class="text-xs text-gray-500">{{ t(`channelOps.${field.label}`) }}</dt>
      <dd class="mt-1 text-lg font-semibold tabular-nums">{{ display(metrics[field.key], field.ms) }}</dd>
    </div>
  </dl>
</template>
<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { Metrics } from '@/api/admin/channelOperations'
import { channelOpsMessages } from './channelOpsMessages'
defineProps<{ metrics: Metrics }>()
const { t } = useI18n({ useScope: 'local', messages: channelOpsMessages })
const fields: Array<{ key: keyof Metrics; label: string; ms?: boolean }> = [
  { key: 'requests', label: 'requests' }, { key: 'samples', label: 'known' }, { key: 'unknown_samples', label: 'unknown' },
  { key: 'duration_samples', label: 'durationSamples' }, { key: 'p50_ms', label: 'p50', ms: true },
  { key: 'p90_ms', label: 'p90', ms: true }, { key: 'p95_ms', label: 'p95', ms: true }, { key: 'duration_p90_ms', label: 'durationP90', ms: true }
]
function display(value: number | string | null, ms?: boolean) { return value == null ? '—' : typeof value === 'number' ? `${Math.round(value).toLocaleString()}${ms ? ' ms' : ''}` : value }
</script>
