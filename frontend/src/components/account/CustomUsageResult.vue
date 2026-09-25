<template>
  <div class="space-y-1 text-xs" aria-live="polite">
    <div class="flex flex-wrap items-baseline gap-x-3 gap-y-1">
      <span v-if="result.remaining != null" class="font-mono text-sm font-medium text-gray-900 dark:text-gray-100" data-testid="custom-usage-remaining">{{ number(result.remaining) }} <span class="text-xs font-normal">{{ result.unit }}</span></span>
      <span v-else class="text-gray-400">—</span>
      <span v-if="result.stale" class="text-amber-600 dark:text-amber-400">{{ t('admin.accounts.customUsage.stale') }}</span>
    </div>
    <div v-if="result.used != null || result.total != null" class="text-gray-500 dark:text-gray-400">
      <span v-if="result.used != null">{{ t('admin.accounts.customUsage.used') }} {{ number(result.used) }} {{ result.unit }}</span>
      <span v-if="result.total != null" class="ml-2">{{ t('admin.accounts.customUsage.total') }} {{ number(result.total) }} {{ result.unit }}</span>
    </div>
    <div v-if="result.plan_name" class="max-w-56 truncate text-gray-500 dark:text-gray-400" :title="result.plan_name">{{ result.plan_name }}</div>
    <div v-if="result.updated_at" class="text-gray-400 dark:text-dark-400">{{ t('admin.accounts.customUsage.updatedAt') }} {{ formatDateTime(result.updated_at) }}</div>
    <div v-if="result.error" class="text-amber-600 dark:text-amber-400" role="status">{{ t('admin.accounts.customUsage.queryFailed') }}</div>
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
