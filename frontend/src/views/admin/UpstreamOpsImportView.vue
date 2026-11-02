<template>
  <AppLayout>
    <div class="mx-auto flex w-full flex-col gap-4 sm:gap-6 lg:max-w-4xl">
      <AdminPageHeader :title="title" :description="description" />
      <UpstreamOpsImportPanel />
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, defineAsyncComponent } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import AdminPageHeader from '@/components/admin/page/AdminPageHeader.vue'

// Lazy: the adapter page is not part of the accounts hot path.
const UpstreamOpsImportPanel = defineAsyncComponent(
  () => import('@/components/admin/account/UpstreamOpsImportPanel.vue')
)

const { locale } = useI18n()
const isZh = computed(() => locale.value.startsWith('zh'))
const title = computed(() => (isZh.value ? 'UpstreamOps 导入' : 'UpstreamOps import'))
const description = computed(() =>
  isZh.value
    ? '把 upstream-ops 的渠道/网关通道快照带进来：先预览、校验、去重，再走现有账号导入流程。'
    : 'Bring upstream-ops channel or gateway-provider snapshots in: preview, validate, dedupe, then use the existing account import flow.'
)
</script>
