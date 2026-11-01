<template>
  <div v-if="user" class="space-y-6">
    <UPageCard :title="t('admin.users.detail.tabs.balance')" variant="naked" orientation="horizontal">
      <div class="flex flex-wrap gap-2 lg:ms-auto">
        <UButton color="neutral" variant="outline" icon="i-lucide-history" :label="t('admin.users.balanceHistory')" @click="detail.openBalanceHistory()" />
        <UButton color="neutral" variant="outline" icon="i-lucide-circle-minus" :label="t('admin.users.withdraw')" @click="detail.openBalance('subtract')" />
        <UButton icon="i-lucide-circle-plus" :label="t('admin.users.deposit')" @click="detail.openBalance('add')" />
      </div>
    </UPageCard>
    <UPageGrid class="gap-4 sm:grid-cols-2">
      <UPageCard :title="t('admin.users.currentBalance')" icon="i-lucide-wallet" variant="subtle" :ui="statUi">
        <p class="text-2xl font-semibold tabular-nums text-highlighted">${{ (user.balance ?? 0).toFixed(2) }}</p>
      </UPageCard>
      <UPageCard :title="t('admin.users.detail.quota')" icon="i-lucide-chart-column" variant="subtle" :ui="statUi">
        <UButton color="neutral" variant="link" class="-mx-2.5" trailing-icon="i-lucide-arrow-right" :label="t('admin.users.platformQuota.menuItem')" @click="detail.openPlatformQuota()" />
      </UPageCard>
    </UPageGrid>
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { useUserDetail } from './userDetailContext'

const { t } = useI18n()
const detail = useUserDetail()
const user = detail.user

// Same stat card look as the payment dashboard (template HomeStats)
const statUi = {
  container: 'gap-y-1.5',
  wrapper: 'items-start',
  leading: 'p-2.5 rounded-full bg-primary/10 ring ring-inset ring-primary/25',
  title: 'font-normal text-muted text-xs uppercase'
}
</script>
