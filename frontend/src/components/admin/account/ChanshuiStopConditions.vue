<template>
  <div class="space-y-3 rounded-lg border border-gray-300 p-3 dark:border-dark-600" data-testid="chanshui-stop-conditions">
    <div class="text-sm font-medium">{{ t('admin.scheduledTests.stopConditions') }}</div>
    <label class="block text-xs">
      {{ t('admin.scheduledTests.stopMatch') }}
      <Select :model-value="condition.match" :options="matchOptions" :aria-label="t('admin.scheduledTests.stopMatch')" @update:model-value="setMatch" />
    </label>
    <label v-if="condition.rules.some(rule => rule.type === 'fingerprint_mismatch')" class="block text-xs">
      {{ t('admin.scheduledTests.expectedModel') }}
      <input type="text" class="input mt-1 w-full" :value="condition.expected_model" :placeholder="model"
        @input="emit('update:modelValue', { ...condition, expected_model: ($event.target as HTMLInputElement).value })" />
    </label>
    <div v-for="(rule, index) in condition.rules" :key="index" class="space-y-2 border-t border-gray-200 pt-3 dark:border-dark-600" data-testid="chanshui-stop-rule">
      <div class="flex items-center gap-2">
        <Select class="min-w-0 flex-1" :model-value="rule.type" :options="ruleOptions" :aria-label="t('admin.scheduledTests.stopRuleType')" @update:model-value="setType(index, $event)" />
        <button type="button" class="btn btn-secondary" :disabled="condition.rules.length === 1" @click="remove(index)">{{ t('common.delete') }}</button>
      </div>
      <label v-if="rule.type === 'total_score_below'" class="block text-xs">
        {{ t('admin.scheduledTests.minimumTotal') }}
        <input type="number" class="input mt-1 w-full" min="0" max="100" step="0.1" :value="rule.threshold ?? ''" data-testid="chanshui-score-threshold"
          @input="setThreshold(index, ($event.target as HTMLInputElement).value)" />
      </label>
      <div v-if="rule.type === 'section_status'" class="grid gap-2 sm:grid-cols-2">
        <Select :model-value="rule.section" :options="sectionOptions" :aria-label="t('admin.scheduledTests.stopSection')" @update:model-value="setSection(index, $event)" />
        <input type="text" class="input" :value="rule.status" :placeholder="t('admin.scheduledTests.stopStatusPlaceholder')"
          @input="patch(index, { status: ($event.target as HTMLInputElement).value })" />
      </div>
    </div>
    <button type="button" class="btn btn-secondary text-xs" :disabled="condition.rules.length >= 10" @click="add">{{ t('admin.scheduledTests.addStopRule') }}</button>
    <p class="text-xs text-gray-500">{{ t('admin.scheduledTests.stopConditionHint') }}</p>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import Select from '@/components/common/Select.vue'
import type { ChanshuiStopCondition, ChanshuiStopRule } from '@/types'
import { defaultChanshuiStopCondition } from '@/utils/scheduledTestQuality'
const props = defineProps<{ modelValue?: ChanshuiStopCondition; model: string }>()
const emit = defineEmits<{ (event: 'update:modelValue', value: ChanshuiStopCondition): void }>()
const { t } = useI18n()
const condition = computed(() => props.modelValue || defaultChanshuiStopCondition())
const matchOptions = computed(() => [{ value: 'any', label: t('admin.scheduledTests.matchAny') }, { value: 'all', label: t('admin.scheduledTests.matchAll') }])
const ruleOptions = computed(() => [
  { value: 'fingerprint_mismatch', label: t('admin.scheduledTests.fingerprintMismatch') },
  { value: 'total_score_below', label: t('admin.scheduledTests.totalBelow') },
  { value: 'section_status', label: t('admin.scheduledTests.sectionStatusEquals') },
])
const supportedSections = ['tools', 'web', 'cache', 'hidden', 'knowledge']
const sectionOptions = computed(() => supportedSections.map(value => ({ value, label: t(`admin.scheduledTests.sections.${value}`) })))
function setMatch(value: unknown) { if (value === 'any' || value === 'all') emit('update:modelValue', { ...condition.value, match: value }) }
function patch(index: number, value: Partial<ChanshuiStopRule>) {
  emit('update:modelValue', { ...condition.value, rules: condition.value.rules.map((rule, i) => i === index ? { ...rule, ...value } : { ...rule }) })
}
function setType(index: number, value: unknown) {
  if (value !== 'fingerprint_mismatch' && value !== 'total_score_below' && value !== 'section_status') return
  const rules = condition.value.rules.map(rule => ({ ...rule }))
  rules[index] = value === 'section_status' ? { type: value, section: 'tools', status: '' } : value === 'total_score_below' ? { type: value, threshold: null } : { type: value }
  emit('update:modelValue', { ...condition.value, rules })
}
function setThreshold(index: number, value: string) { patch(index, { threshold: value === '' ? null : Number(value) }) }
function setSection(index: number, value: unknown) { if (typeof value === 'string' && supportedSections.includes(value)) patch(index, { section: value }) }
function remove(index: number) { if (condition.value.rules.length > 1) emit('update:modelValue', { ...condition.value, rules: condition.value.rules.filter((_, i) => i !== index) }) }
function add() { if (condition.value.rules.length < 10) emit('update:modelValue', { ...condition.value, rules: [...condition.value.rules, { type: 'fingerprint_mismatch' }] }) }
</script>
