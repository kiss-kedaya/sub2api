<template>
  <AppLayout>
    <div class="space-y-5">
      <div class="flex flex-wrap items-start justify-between gap-3">
        <div><h1 class="text-2xl font-bold text-gray-900 dark:text-white">{{ text('受管代理出口', 'Managed outbound proxies') }}</h1><p class="mt-1 text-sm text-gray-500">{{ text('维护当前服务器的订阅和节点，按需共享至站点代理列表。', 'Maintain this server’s subscriptions and nodes, then share selected exits with the site when needed.') }}</p></div>
        <button type="button" class="btn btn-secondary" @click="section = section === 'kernel' ? 'subscriptions' : 'kernel'">{{ section === 'kernel' ? text('返回订阅', 'Back to subscriptions') : text('高级设置', 'Advanced settings') }}</button>
      </div>
      <div class="flex gap-2" role="tablist" :aria-label="text('代理管理', 'Proxy management')">
        <button v-for="tab in tabs" :key="tab.id" type="button" class="btn" :class="section === tab.id ? 'btn-primary' : 'btn-secondary'" role="tab" :aria-selected="section === tab.id" @click="section = tab.id">{{ tab.label }}</button>
      </div>
      <MihomoSettings :section="section" />
    </div>
  </AppLayout>
</template>
<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import MihomoSettings from '@/features/mihomo/MihomoSettings.vue'
const { locale } = useI18n()
const text = (zh: string, en: string) => locale.value.startsWith('zh') ? zh : en
const section = ref<'subscriptions' | 'nodes' | 'dynamic' | 'kernel'>('subscriptions')
const tabs = computed(() => [ { id: 'subscriptions' as const, label: text('订阅', 'Subscriptions') }, { id: 'nodes' as const, label: text('节点', 'Nodes') }, { id: 'dynamic' as const, label: text('动态代理', 'Dynamic proxies') } ])
</script>
