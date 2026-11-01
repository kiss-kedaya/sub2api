<template>
  <div class="space-y-3 sm:col-span-2" data-testid="scheduled-quality-config">
    <UFormField :label="t('admin.scheduledTests.auditServiceURL')">
      <UInput type="url" class="w-full" :model-value="config.base_url" placeholder="https://chanshui.dev"
        data-testid="chanshui-service-url" @update:model-value="update('base_url', String($event ?? ''))" />
    </UFormField>
    <p class="text-xs text-warning">{{ t('admin.scheduledTests.auditKeyNotice') }}</p>
    <div class="grid gap-3 sm:grid-cols-2">
      <UFormField :label="t('admin.scheduledTests.auditProtocol')">
        <Select :model-value="config.protocol" :options="protocolOptions" :aria-label="t('admin.scheduledTests.auditProtocol')" @update:model-value="updateProtocol" />
      </UFormField>
      <UFormField :label="t('admin.scheduledTests.auditTimeout')">
        <UInput type="number" min="10" max="600" class="w-full" :model-value="String(config.timeout)"
          @update:model-value="update('timeout', Number($event))" />
      </UFormField>
    </div>
    <UCollapsible :unmount-on-hide="false" data-testid="chanshui-sections">
      <UButton color="neutral" variant="link" class="p-0" trailing-icon="i-lucide-chevron-down">
        {{ t('admin.scheduledTests.auditSections') }} · {{ config.sections.length ? `${config.sections.length}` : t('admin.scheduledTests.auditAll') }}
      </UButton>
      <template #content>
      <div class="mt-3 grid gap-2 sm:grid-cols-2">
        <UCheckbox class="sm:col-span-2" :model-value="config.sections.length === 0" :label="t('admin.scheduledTests.auditAll')" @update:model-value="update('sections', [])" />
        <UCheckbox
          v-for="section in chanshuiSections"
          :key="section"
          :data-section="section"
          :model-value="config.sections.length === 0 || config.sections.includes(section)"
          :disabled="config.sections.length === 1 && config.sections[0] === section"
          :label="t(`admin.scheduledTests.sections.${section}`)"
          @update:model-value="toggleSection(section, $event === true)"
        />
      </div>
      </template>
    </UCollapsible>
    <p class="text-xs text-gray-500">{{ t('admin.scheduledTests.auditLimits') }}</p>
    <ChanshuiStopConditions :model-value="config.stop_condition" :model="model" @update:model-value="update('stop_condition', $event)" />
    <p v-if="!isChanshuiPolicyValid(config)" class="text-xs text-red-600" role="alert">{{ t('admin.scheduledTests.invalidStopCondition') }}</p>
    <p class="text-xs text-gray-500">{{ t('admin.scheduledTests.auditPolicyHint') }}</p>
    <p v-if="config.sections.length === 1 && config.sections[0] === 'thinking' && !model.startsWith('claude-')"
      class="text-xs text-red-600" role="alert">{{ t('admin.scheduledTests.thinkingOnlyClaude') }}</p>
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import Select from '@/components/common/Select.vue'
import type { ChanshuiQualityConfig } from '@/types'
import { chanshuiSections, isChanshuiPolicyValid } from '@/utils/scheduledTestQuality'
import ChanshuiStopConditions from './ChanshuiStopConditions.vue'

const props = defineProps<{ provider: 'pelican' | 'chanshui'; config: ChanshuiQualityConfig; model: string }>()
const emit = defineEmits<{
  (event: 'update:provider', value: 'pelican' | 'chanshui'): void
  (event: 'update:config', value: ChanshuiQualityConfig): void
}>()
const { t } = useI18n()
const protocolOptions = ['auto', 'chat', 'anthropic', 'openai'].map(value => ({ value, label: value }))
function updateProtocol(value: unknown) {
  if (value === 'auto' || value === 'chat' || value === 'anthropic' || value === 'openai') update('protocol', value)
}
function update<K extends keyof ChanshuiQualityConfig>(key: K, value: ChanshuiQualityConfig[K]) {
  emit('update:config', { ...props.config, [key]: value })
}
function toggleSection(section: string, checked: boolean) {
  const current = props.config.sections.length ? props.config.sections : [...chanshuiSections]
  const next = checked ? [...new Set([...current, section])] : current.filter(item => item !== section)
  // Empty means all in the wire contract; never silently turn "none" into all.
  if (next.length) update('sections', next)
}
</script>
