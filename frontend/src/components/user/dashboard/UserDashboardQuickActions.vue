<template>
  <DashboardQuickLinks :items="items" :label="t('dashboard.quickActions')" />
</template>

<script setup lang="ts">
import { computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useBatchImageAccess } from '@/composables/useBatchImageAccess'
import DashboardQuickLinks from '@/components/dashboard/DashboardQuickLinks.vue'
import type { DashboardQuickLink } from '@/components/dashboard/dashboardUi'

const { t } = useI18n()
const { canUseBatchImage, refreshBatchImageAccess } = useBatchImageAccess()

const items = computed<DashboardQuickLink[]>(() => [
  { label: t('dashboard.createApiKey'), to: '/keys', icon: 'i-lucide-key-round' },
  { label: t('dashboard.infiniteCanvas'), to: '/infinite-canvas', icon: 'i-lucide-layout-grid' },
  { label: t('dashboard.viewUsage'), to: '/usage', icon: 'i-lucide-chart-column' },
  ...(canUseBatchImage.value ? [{ label: t('dashboard.batchImageAgent'), to: '/batch-image', icon: 'i-lucide-sparkles' }] : []),
  { label: t('dashboard.redeemCode'), to: '/redeem', icon: 'i-lucide-gift' },
])

onMounted(() => {
  void refreshBatchImageAccess()
})
</script>
