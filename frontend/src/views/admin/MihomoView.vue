<template>
  <AppLayout>
    <div class="flex flex-col gap-4 sm:gap-6">
      <AdminPageHeader :title="text('受管代理出口', 'Managed outbound proxies')">
        <template #description>{{ text('维护当前服务器的订阅和节点，按需共享至站点代理列表。', 'Maintain this server’s subscriptions and nodes, then share selected exits with the site when needed.') }}</template>
        <template #actions>
          <UButton
            color="neutral"
            variant="outline"
            :icon="section === 'kernel' ? 'i-lucide-chevron-left' : 'i-lucide-settings-2'"
            :label="section === 'kernel' ? text('返回订阅', 'Back to subscriptions') : text('高级设置', 'Advanced settings')"
            @click="section = section === 'kernel' ? 'subscriptions' : 'kernel'"
          />
        </template>
      </AdminPageHeader>
      <nav :aria-label="text('代理管理', 'Proxy management')" class="-mt-2">
        <UTabs v-model="activeTab" :items="tabs" :content="false" variant="link" class="w-full" :ui="{ list: 'border-b border-default', indicator: section === 'kernel' ? 'hidden' : '' }" />
      </nav>
      <MihomoSettings :section="section" />
    </div>
  </AppLayout>
</template>
<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import AdminPageHeader from '@/components/admin/page/AdminPageHeader.vue'
import MihomoSettings from '@/features/mihomo/MihomoSettings.vue'
type Section = 'subscriptions' | 'nodes' | 'dynamic' | 'kernel'
const { locale } = useI18n()
const text = (zh: string, en: string) => locale.value.startsWith('zh') ? zh : en
const section = ref<Section>('subscriptions')
const tabs = computed(() => [
  { value: 'subscriptions', label: text('订阅', 'Subscriptions'), icon: 'i-lucide-rss' },
  { value: 'nodes', label: text('节点', 'Nodes'), icon: 'i-lucide-server' },
  { value: 'dynamic', label: text('动态代理', 'Dynamic proxies'), icon: 'i-lucide-shuffle' },
])
// The kernel section has no tab of its own (it opens from "Advanced settings"), so no tab is active there.
const activeTab = computed<string | number>({
  get: () => section.value,
  set: (value) => { section.value = value as Section },
})
</script>
