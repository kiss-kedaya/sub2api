<template>
  <UCard
    as="nav"
    class="signal-actions min-w-0 shadow-xs sm:col-span-2 xl:col-span-1 dark:shadow-none"
    :aria-label="t('dashboard.quickActions')"
    :ui="{ header: 'px-4 py-3 sm:px-5', body: 'p-2 sm:p-2' }"
  >
    <template #header>
      <h2 class="signal-actions-label text-sm font-semibold text-highlighted">{{ t('dashboard.quickActions') }}</h2>
    </template>
    <div class="grid grid-cols-2 gap-1 sm:grid-cols-4 xl:grid-cols-2">
      <UButton v-bind="actionProps" icon="i-lucide-key-round" @click="router.push('/keys')"><span class="flex-1 truncate text-left">{{ t('dashboard.createApiKey') }}</span></UButton>
      <UButton v-bind="actionProps" icon="i-lucide-layout-grid" @click="router.push('/infinite-canvas')"><span class="flex-1 truncate text-left">{{ t('dashboard.infiniteCanvas') }}</span></UButton>
      <UButton v-bind="actionProps" icon="i-lucide-chart-column" @click="router.push('/usage')"><span class="flex-1 truncate text-left">{{ t('dashboard.viewUsage') }}</span></UButton>
      <UButton v-if="canUseBatchImage" v-bind="actionProps" icon="i-lucide-sparkles" @click="router.push('/batch-image')"><span class="flex-1 truncate text-left">{{ t('dashboard.batchImageAgent') }}</span></UButton>
      <UButton v-bind="actionProps" icon="i-lucide-gift" @click="router.push('/redeem')"><span class="flex-1 truncate text-left">{{ t('dashboard.redeemCode') }}</span></UButton>
    </div>
  </UCard>
</template>

<script setup lang="ts">
import { onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useBatchImageAccess } from '@/composables/useBatchImageAccess'
const router = useRouter()
const { t } = useI18n()
const { canUseBatchImage, refreshBatchImageAccess } = useBatchImageAccess()

// Shared look for the shortcut rows: quiet neutral rows with a trailing arrow.
const actionProps = {
  color: 'neutral',
  variant: 'ghost',
  size: 'lg',
  block: true,
  trailingIcon: 'i-lucide-arrow-right',
  ui: { leadingIcon: 'text-muted', trailingIcon: 'text-dimmed' },
} as const

onMounted(() => {
  void refreshBatchImageAccess()
})
</script>
