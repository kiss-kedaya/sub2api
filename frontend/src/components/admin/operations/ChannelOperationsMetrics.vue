<template>
  <dl class="grid grid-cols-2 gap-px overflow-hidden rounded-lg bg-[var(--ui-border)] ring ring-default md:grid-cols-4">
    <div v-for="field in fields" :key="field.key" class="bg-elevated/50 px-4 py-3">
      <dt class="text-xs text-muted">{{ t(`channelOps.${field.label}`) }}</dt>
      <dd class="mt-1 text-lg font-semibold tabular-nums text-highlighted">{{ display(metrics[field.key], field.ms) }}</dd>
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
