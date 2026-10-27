<template>
  <AppLayout>
    <div class="w-full min-w-0 pb-8">
      <h1 class="mb-4 text-2xl font-semibold">{{ t('ipAudit.title') }}</h1>
      <div class="mb-4 flex flex-wrap gap-1" role="tablist" :aria-label="t('ipAudit.title')">
        <button
          v-for="tab in tabs"
          :key="tab.id"
          type="button"
          role="tab"
          class="tab"
          :class="{ 'tab-active': activeTab === tab.id }"
          :aria-selected="activeTab === tab.id"
          :data-test="`ip-tab-${tab.id}`"
          @click="selectTab(tab.id)"
        >{{ tab.label }}</button>
      </div>
      <KeepAlive>
        <IPRiskSettings v-if="activeTab === 'config'" expanded />
        <IPAuditPanel v-else class="card px-4 sm:px-6 lg:px-8" @configure="selectTab('config')" />
      </KeepAlive>
    </div>
  </AppLayout>
</template>
<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import AppLayout from '@/components/layout/AppLayout.vue'
import IPRiskSettings from '@/features/ip-risk/IPRiskSettings.vue'
import IPAuditPanel from './IPAuditPanel.vue'

const { t } = useI18n()
const route = useRoute(), router = useRouter()
type IPAuditTab = 'sources' | 'config'
const activeTab = computed<IPAuditTab>(() => route.query.tab === 'config' || route.hash === '#ip-risk-settings' ? 'config' : 'sources')
const tabs = computed(() => [
  { id: 'sources' as const, label: t('ipAudit.tabs.sources') },
  { id: 'config' as const, label: t('ipAudit.tabs.config') },
])
function selectTab(tab: IPAuditTab) {
  void router.push({ query: { ...route.query, tab }, hash: tab === 'config' ? '#ip-risk-settings' : '' })
}
</script>
