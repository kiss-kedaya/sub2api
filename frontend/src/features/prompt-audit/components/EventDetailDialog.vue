<template>
  <BaseDialog :show="show" :title="t('admin.promptAudit.events.detailTitle')" width="extra-wide" @close="$emit('close')">
    <div v-if="loading" class="py-12 text-center text-sm text-gray-500" aria-busy="true">{{ t('common.loading') }}</div>
    <div v-else-if="event" class="space-y-5">
      <div class="flex flex-wrap gap-2 border-b border-gray-200 pb-3 dark:border-dark-700" role="tablist">
        <button v-for="tab in tabs" :key="tab" type="button" role="tab" :aria-selected="activeTab === tab" class="rounded-md px-3 py-1.5 text-sm" :class="activeTab === tab ? 'bg-primary-50 text-primary-700 dark:bg-primary-950/40 dark:text-primary-300' : 'text-gray-600 dark:text-dark-300'" @click="activeTab = tab">
          {{ t(`admin.promptAudit.events.tabs.${tab}`) }}
        </button>
      </div>

      <div v-if="activeTab === 'summary'" class="grid gap-5 lg:grid-cols-2">
        <div>
          <h4 class="text-sm font-medium text-gray-900 dark:text-white">{{ t('admin.promptAudit.events.redactedPreview') }}</h4>
          <pre class="mt-2 max-h-56 overflow-auto whitespace-pre-wrap break-words rounded-lg bg-gray-50 p-4 text-sm text-gray-700 dark:bg-dark-900 dark:text-dark-200">{{ event.snapshot.redacted_preview || '—' }}</pre>
        </div>
        <dl class="grid grid-cols-[auto_1fr] gap-x-4 gap-y-2 text-sm">
          <dt class="text-gray-500">{{ t('admin.promptAudit.events.decision') }}</dt><dd class="font-medium text-gray-900 dark:text-white">{{ event.decision }} · {{ event.action }}</dd>
          <dt class="text-gray-500">{{ t('admin.promptAudit.events.user') }}</dt><dd>{{ event.snapshot.username || '—' }}</dd>
          <dt class="text-gray-500">{{ t('admin.promptAudit.events.email') }}</dt><dd>{{ event.snapshot.user_email || '—' }}</dd>
          <dt class="text-gray-500">{{ t('admin.promptAudit.events.apiKey') }}</dt><dd>{{ event.snapshot.api_key_name || '—' }}</dd>
          <dt class="text-gray-500">{{ t('admin.promptAudit.events.group') }}</dt><dd>{{ event.snapshot.group_name || '—' }}</dd>
          <dt class="text-gray-500">{{ t('admin.promptAudit.events.model') }}</dt><dd>{{ event.snapshot.model || '—' }}</dd>
          <dt class="text-gray-500">{{ t('admin.promptAudit.events.categories') }}</dt><dd>{{ event.categories.join(', ') || '—' }}</dd>
        </dl>
      </div>

      <div v-else-if="activeTab === 'risks'" class="space-y-3">
        <article v-for="issue in event.issue_summaries" :key="`${issue.scanner_id}-${issue.code}`" class="border-l-2 border-red-400 pl-4">
          <div class="flex flex-wrap items-center gap-2">
            <h4 class="font-medium text-gray-900 dark:text-white">{{ issue.title }}</h4>
            <span class="text-xs uppercase text-red-600 dark:text-red-300">{{ issue.severity_label }} · {{ issue.action_label }}</span>
          </div>
          <p class="mt-1 text-sm text-gray-600 dark:text-dark-300">{{ issue.description }}</p>
          <p class="mt-2 break-words text-xs text-gray-500 dark:text-dark-400">{{ issue.scanner_id }} · {{ issue.code }} · {{ issue.score }} · {{ issue.evidence }}</p>
        </article>
        <p v-if="event.issue_summaries.length === 0" class="py-8 text-center text-sm text-gray-500">{{ t('admin.promptAudit.events.noRisks') }}</p>
      </div>

      <dl v-else class="grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-2 text-sm">
        <dt class="text-gray-500">Request ID</dt><dd class="break-all font-mono">{{ event.snapshot.request_id || '—' }}</dd>
        <dt class="text-gray-500">Prompt SHA-256</dt><dd class="break-all font-mono">{{ event.snapshot.prompt_hash }}</dd>
        <dt class="text-gray-500">Scanner</dt><dd>{{ event.scanner_backend }} · {{ event.scanner_version }}</dd>
        <dt class="text-gray-500">Policy</dt><dd>{{ event.policy_id }} · v{{ event.policy_version }}</dd>
        <dt class="text-gray-500">Guard endpoint</dt><dd>{{ event.guard_endpoint_id }}</dd>
        <dt class="text-gray-500">Config</dt><dd>v{{ event.config_version }}</dd>
        <dt class="text-gray-500">Chunks</dt><dd>{{ event.chunk_total }}</dd>
        <dt class="text-gray-500">Latency</dt><dd>{{ event.latency_ms }} ms</dd>
        <dt class="text-gray-500">{{ t('admin.promptAudit.events.stage') }}</dt><dd>{{ event.snapshot.stage || 'http' }}</dd>
        <dt class="text-gray-500">Protocol</dt><dd>{{ event.snapshot.protocol }} · {{ event.snapshot.endpoint }}</dd>
      </dl>
    </div>
  </BaseDialog>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import type { PromptAuditEvent } from '../types'

const props = defineProps<{ show: boolean; event: PromptAuditEvent | null; loading: boolean }>()
defineEmits<{ (event: 'close'): void }>()
const { t } = useI18n()
const tabs = ['summary', 'risks', 'technical'] as const
const activeTab = ref<(typeof tabs)[number]>('summary')
watch(() => props.event?.id, () => { activeTab.value = 'summary' })
</script>
