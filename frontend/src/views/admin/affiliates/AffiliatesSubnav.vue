<template>
  <!-- Sub-nav of the 邀请返利 area (template settings page); each item is one of its record pages -->
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
  { label: t('nav.affiliateInviteRecords'), icon: 'i-lucide-user-plus', to: '/admin/affiliates/invites' },
  { label: t('nav.affiliateRebateRecords'), icon: 'i-lucide-coins', to: '/admin/affiliates/rebates' },
  { label: t('nav.affiliateTransferRecords'), icon: 'i-lucide-arrow-left-right', to: '/admin/affiliates/transfers' }
])
</script>
