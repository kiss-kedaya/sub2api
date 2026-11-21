<template>
  <DashboardQuickLinks :items="items" :label="t('dashboard.quickActions')" />
</template>

<script setup lang="ts">
import { computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useBatchImageAccess } from '@/composables/useBatchImageAccess'
import { useAuthStore } from '@/stores/auth'
import { isRestrictedInSimpleMode } from '@/router/simpleMode'
import DashboardQuickLinks from '@/components/dashboard/DashboardQuickLinks.vue'
import type { DashboardQuickLink } from '@/components/dashboard/dashboardUi'

const { t } = useI18n()
const authStore = useAuthStore()
const { canUseBatchImage, refreshBatchImageAccess } = useBatchImageAccess()

const items = computed<DashboardQuickLink[]>(() => {
  const all: DashboardQuickLink[] = [
    { label: t('dashboard.createApiKey'), to: '/keys', icon: 'i-lucide-key-round' },
    { label: t('dashboard.infiniteCanvas'), to: '/infinite-canvas', icon: 'i-lucide-layout-grid' },
    { label: t('dashboard.viewUsage'), to: '/usage', icon: 'i-lucide-chart-column' },
    ...(canUseBatchImage.value ? [{ label: t('dashboard.batchImageAgent'), to: '/batch-image', icon: 'i-lucide-sparkles' }] : []),
    { label: t('dashboard.redeemCode'), to: '/redeem', icon: 'i-lucide-gift' },
  ]
  // Simple mode redirects these routes back to the dashboard, so offering the
  // card reads as a dead click; the sidebar hides the same entries (#400).
  return authStore.isSimpleMode ? all.filter((item) => !isRestrictedInSimpleMode(item.to)) : all
})

onMounted(() => {
  void refreshBatchImageAccess()
})
</script>
