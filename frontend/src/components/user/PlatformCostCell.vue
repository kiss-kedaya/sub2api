<template>
  <div v-if="usage" class="text-sm">
    <div class="flex items-center gap-1.5">
      <span class="text-muted">{{ t('admin.users.today') }}:</span>
      <span class="font-medium tabular-nums text-highlighted">${{ usage.today_actual_cost.toFixed(4) }}</span>
    </div>
    <div class="mt-0.5 flex items-center gap-1.5">
      <span class="text-muted">{{ t('admin.users.total') }}:</span>
      <span class="font-medium tabular-nums text-highlighted">${{ usage.total_actual_cost.toFixed(4) }}</span>
    </div>
  </div>
  <span v-else class="text-sm text-dimmed">—</span>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { PlatformUsage } from '@/api/admin/dashboard'

defineProps<{
  usage?: PlatformUsage
}>()

const { t } = useI18n()
</script>
