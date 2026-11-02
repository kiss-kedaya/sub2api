<template>
  <div class="space-y-0.5 text-xs" aria-live="polite">
    <div class="flex flex-wrap items-baseline gap-x-2 gap-y-0.5">
      <span v-if="result.remaining != null" class="font-mono text-sm font-semibold text-highlighted" data-testid="custom-usage-remaining">{{ number(result.remaining) }} <span class="text-xs font-normal text-muted">{{ result.unit }}</span></span>
      <span v-else class="text-dimmed">—</span>
      <span v-if="result.stale" class="text-xs text-warning">{{ t('admin.accounts.customUsage.stale') }}</span>
    </div>
    <div v-if="result.used != null || result.total != null" class="font-mono text-xs text-muted">
      <span v-if="result.used != null">{{ number(result.used) }}</span>
      <span v-if="result.used != null && result.total != null" class="mx-1 text-dimmed">/</span>
      <span v-if="result.total != null">{{ number(result.total) }}</span>
      <span class="ml-1 font-sans">{{ result.unit }}</span>
    </div>
    <time v-if="result.updated_at" class="block text-xs text-dimmed" :datetime="result.updated_at" :title="formatDateTime(result.updated_at)">{{ formatDateTime(result.updated_at) }}</time>
    <div v-if="result.error" class="text-warning" role="status">{{ t('admin.accounts.customUsage.queryFailed') }}</div>
  </div>
</template>
<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { CustomUsageResult } from '@/api/admin/customUsage'
import { formatDateTime } from '@/utils/format'
defineProps<{ result: CustomUsageResult }>()
const { t, locale } = useI18n()
const number = (value: number) => Number.isFinite(value) ? new Intl.NumberFormat(locale.value, { maximumFractionDigits: 4 }).format(value) : '—'
</script>
