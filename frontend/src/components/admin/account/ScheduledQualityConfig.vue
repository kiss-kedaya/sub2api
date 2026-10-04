<template>
  <div class="space-y-3 sm:col-span-2" data-testid="scheduled-quality-config">
    <label class="block text-xs font-medium text-gray-600 dark:text-gray-400">
      {{ t('admin.scheduledTests.provider') }}
      <Select :model-value="provider" :options="providerOptions" :aria-label="t('admin.scheduledTests.provider')" @update:model-value="updateProvider" />
    </label>
    <template v-if="provider === 'chanshui'">
      <label class="block text-xs font-medium text-gray-600 dark:text-gray-400">
        {{ t('admin.scheduledTests.auditServiceURL') }}
        <input class="input mt-1 w-full" type="url" :value="config.base_url" placeholder="https://chanshui.dev"
          data-testid="chanshui-service-url" @input="update('base_url', ($event.target as HTMLInputElement).value)" />
      </label>
      <p class="text-xs text-amber-700 dark:text-amber-400">{{ t('admin.scheduledTests.auditKeyNotice') }}</p>
      <div class="grid gap-3 sm:grid-cols-2">
        <label class="block text-xs font-medium text-gray-600 dark:text-gray-400">
          {{ t('admin.scheduledTests.auditProtocol') }}
          <Select :model-value="config.protocol" :options="protocolOptions" :aria-label="t('admin.scheduledTests.auditProtocol')" @update:model-value="updateProtocol" />
        </label>
        <label class="block text-xs font-medium text-gray-600 dark:text-gray-400">
          {{ t('admin.scheduledTests.auditTimeout') }}
          <input class="input mt-1 w-full" type="number" min="10" max="600" :value="config.timeout"
            @input="update('timeout', Number(($event.target as HTMLInputElement).value))" />
        </label>
      </div>
      <details class="rounded-lg border border-gray-300 p-3 dark:border-dark-600" data-testid="chanshui-sections">
        <summary class="cursor-pointer text-sm text-gray-700 dark:text-gray-300">
          {{ t('admin.scheduledTests.auditSections') }} · {{ config.sections.length ? `${config.sections.length}` : t('admin.scheduledTests.auditAll') }}
        </summary>
        <div class="mt-3 grid gap-2 sm:grid-cols-2">
          <label class="flex items-center gap-2 text-sm sm:col-span-2">
            <input type="checkbox" :checked="config.sections.length === 0" @change="update('sections', [])" />
            {{ t('admin.scheduledTests.auditAll') }}
          </label>
          <label v-for="section in chanshuiSections" :key="section" class="flex items-center gap-2 text-sm">
            <input type="checkbox" :data-section="section" :checked="config.sections.length === 0 || config.sections.includes(section)"
              :disabled="config.sections.length === 1 && config.sections[0] === section"
              @change="toggleSection(section, ($event.target as HTMLInputElement).checked)" />
            {{ t(`admin.scheduledTests.sections.${section}`) }}
          </label>
        </div>
      </details>
      <p class="text-xs text-gray-500">{{ t('admin.scheduledTests.auditLimits') }}</p>
      <ChanshuiStopConditions :model-value="config.stop_condition" :model="model" @update:model-value="update('stop_condition', $event)" />
      <p v-if="!isChanshuiPolicyValid(config)" class="text-xs text-red-600" role="alert">{{ t('admin.scheduledTests.invalidStopCondition') }}</p>
      <p class="text-xs text-gray-500">{{ t('admin.scheduledTests.auditPolicyHint') }}</p>
      <p v-if="config.sections.length === 1 && config.sections[0] === 'thinking' && !model.startsWith('claude-')"
        class="text-xs text-red-600" role="alert">{{ t('admin.scheduledTests.thinkingOnlyClaude') }}</p>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
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
const providerOptions = computed(() => [
  { value: 'pelican', label: t('admin.scheduledTests.pelican') },
  { value: 'chanshui', label: t('admin.scheduledTests.chanshui') },
])
const protocolOptions = ['auto', 'chat', 'anthropic', 'openai'].map(value => ({ value, label: value }))
function updateProvider(value: unknown) {
  if (value === 'pelican' || value === 'chanshui') emit('update:provider', value)
}
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
