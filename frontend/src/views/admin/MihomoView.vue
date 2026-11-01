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
      <PageToolbar>
        <template #left>
        <UNavigationMenu v-if="routed" :items="navItems" highlight class="-mx-1" :aria-label="text('代理管理', 'Proxy management')" />
        <nav v-else :aria-label="text('代理管理', 'Proxy management')" class="-mx-1">
        <UTabs v-model="activeTab" :items="tabs" :content="false" variant="link" :ui="{ list: 'border-b-0', indicator: section === 'kernel' ? 'hidden' : '' }" />
      </nav>
        </template>
      </PageToolbar>
      <MihomoSettings :section="section" />
    </div>
  </AppLayout>
</template>
<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import PageToolbar from '@/components/layout/PageToolbar.vue'
import AdminPageHeader from '@/components/admin/page/AdminPageHeader.vue'
import MihomoSettings from '@/features/mihomo/MihomoSettings.vue'
import { useSectionRoute } from './sysSectionRoute'
type Section = 'subscriptions' | 'nodes' | 'dynamic' | 'kernel'
const { locale } = useI18n()
const text = (zh: string, en: string) => locale.value.startsWith('zh') ? zh : en
const { section, routed, to } = useSectionRoute<Section>('subscriptions', { subscriptions: 'AdminMihomo', nodes: 'AdminMihomoNodes', dynamic: 'AdminMihomoDynamic', kernel: 'AdminMihomoKernel' })
const tabs = computed(() => [
  { value: 'subscriptions', label: text('订阅', 'Subscriptions'), icon: 'i-lucide-rss' },
  { value: 'nodes', label: text('节点', 'Nodes'), icon: 'i-lucide-server' },
  { value: 'dynamic', label: text('动态代理', 'Dynamic proxies'), icon: 'i-lucide-shuffle' },
])
const navItems = computed(() => tabs.value.map(tab => ({ label: tab.label, icon: tab.icon, to: to(tab.value as Section) })))
// The kernel section has no tab of its own (it opens from "Advanced settings"), so no tab is active there.
const activeTab = computed<string | number>({
  get: () => section.value,
  set: (value) => { section.value = value as Section },
})
</script>
