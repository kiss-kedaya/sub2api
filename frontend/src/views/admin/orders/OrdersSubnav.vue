<template>
  <!-- Sub-nav of the 订单管理 area (template settings page); each item is one of its routes -->
  <PageToolbar>
    <template #left>
      <UNavigationMenu :items="items" highlight class="-mx-1" />
    </template>
  </PageToolbar>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import PageToolbar from '@/components/layout/PageToolbar.vue'

const { t } = useI18n()

const items = computed(() => [
  { label: t('nav.paymentDashboard'), icon: 'i-lucide-layout-dashboard', to: '/admin/orders/dashboard' },
  { label: t('nav.orderManagement'), icon: 'i-lucide-receipt', to: '/admin/orders', exact: true },
  { label: t('nav.paymentPlans'), icon: 'i-lucide-package', to: '/admin/orders/plans' }
])
</script>
