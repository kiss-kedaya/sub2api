<template>
  <div class="flex min-w-0 items-center">
    <CapacityMeterBadge
      :label="t('admin.users.concurrencyLabel')"
      :hint="t('admin.users.concurrencyHint')"
      :used="current"
      :max="max"
      icon="i-lucide-layout-grid"
    />
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import CapacityMeterBadge from '@/components/common/CapacityMeterBadge.vue'

const { t } = useI18n()

defineProps<{
  current: number
  max: number
}>()
</script>
