<template>
  <AppLayout>
    <div class="flex w-full min-w-0 flex-col gap-6 pb-8">
      <PageToolbar><template #left><UTabs v-model="tabModel" :items="tabs" :content="false" variant="link" class="-mx-1" :ui="{ list: 'border-b-0' }" :aria-label="t('ipAudit.title')" /></template></PageToolbar>
      <KeepAlive>
        <IPRiskSettings v-if="activeTab === 'config'" expanded />
        <IPAuditPanel v-else @configure="selectTab('config')" />
      </KeepAlive>
    </div>
  </AppLayout>
</template>
<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import AppLayout from '@/components/layout/AppLayout.vue'
import PageToolbar from '@/components/layout/PageToolbar.vue'
import IPRiskSettings from '@/features/ip-risk/IPRiskSettings.vue'
import IPAuditPanel from './IPAuditPanel.vue'

const { t } = useI18n()
const route = useRoute(), router = useRouter()
type IPAuditTab = 'sources' | 'config'
const activeTab = computed<IPAuditTab>(() => route.query.tab === 'config' || route.hash === '#ip-risk-settings' ? 'config' : 'sources')
const tabs = computed(() => [
  { value: 'sources', label: t('ipAudit.tabs.sources'), icon: 'i-lucide-shield-check' },
  { value: 'config', label: t('ipAudit.tabs.config'), icon: 'i-lucide-settings-2' },
])
const tabModel = computed({ get: () => activeTab.value, set: (value: string | number) => selectTab(value === 'config' ? 'config' : 'sources') })
function selectTab(tab: IPAuditTab) {
  void router.push({ query: { ...route.query, tab }, hash: tab === 'config' ? '#ip-risk-settings' : '' })
}
</script>
