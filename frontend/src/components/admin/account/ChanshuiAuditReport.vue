<template>
  <section class="space-y-3 p-4" data-testid="chanshui-audit-report">
    <div class="flex flex-wrap items-center justify-between gap-3 rounded-lg bg-primary-50 p-3 dark:bg-primary-900/20">
      <div>
        <div class="text-xs text-gray-500">{{ t('admin.scheduledTests.auditID') }}</div>
        <div class="break-all font-mono text-xs">{{ report.audit_id || '—' }}</div>
        <div v-if="report.model" class="mt-1 text-xs text-gray-500">{{ report.model }}</div>
      </div>
      <div class="text-right">
        <div class="text-xs text-gray-500">{{ t('admin.scheduledTests.auditTotal') }}</div>
        <div class="text-2xl font-semibold" data-testid="audit-total">{{ totalScore }} / {{ totalMax }}</div>
      </div>
    </div>
    <p v-if="incomplete" class="rounded-lg bg-amber-50 p-3 text-xs text-amber-800 dark:bg-amber-900/20 dark:text-amber-300" role="status">
      {{ t('admin.scheduledTests.auditIncomplete') }}
    </p>
    <p v-if="report.decision" class="rounded-lg border border-gray-200 p-3 text-sm dark:border-dark-600" data-testid="audit-decision">{{ report.decision.reason }}</p>
    <p class="text-xs text-gray-500">{{ t('admin.scheduledTests.auditPolicyHint') }}</p>
    <details v-for="section in visibleSections" :key="section" class="rounded-lg border border-gray-200 p-3 dark:border-dark-600">
      <summary class="cursor-pointer text-sm font-medium">
        {{ t(`admin.scheduledTests.sections.${section}`) }}
        <span v-if="sectionScore(section) !== null" class="ml-2 text-xs text-gray-500">{{ sectionScore(section) }}</span>
      </summary>
      <p class="mt-2 whitespace-pre-wrap break-words text-sm text-gray-700 dark:text-gray-300">{{ sectionSummary(section) }}</p>
      <details v-if="verdict[section] != null" class="mt-2">
        <summary class="cursor-pointer text-xs text-gray-500">{{ t('admin.scheduledTests.auditEvidence') }}</summary>
        <pre class="mt-2 max-h-60 overflow-auto whitespace-pre-wrap break-words rounded bg-gray-950 p-3 text-xs text-gray-200">{{ JSON.stringify(verdict[section], null, 2) }}</pre>
      </details>
    </details>
  </section>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { auditRecord, chanshuiSections, parseChanshuiReport } from '@/utils/scheduledTestQuality'
const props = defineProps<{ report: NonNullable<ReturnType<typeof parseChanshuiReport>> }>()
const { t } = useI18n()
const verdict = computed(() => props.report.verdict || {})
// Verified against the authorized completed audit: verdict.total, NOT integrity.
const total = computed(() => auditRecord(verdict.value.total))
const totalScore = computed(() => typeof total.value.score === 'number' ? total.value.score : '—')
const totalMax = computed(() => typeof total.value.max === 'number' ? total.value.max : '—')
const skipped = computed(() => Array.isArray(props.report.skipped) ? props.report.skipped.map(auditRecord) : [])
const visibleSections = computed(() => chanshuiSections.filter(section =>
  Object.prototype.hasOwnProperty.call(verdict.value, section) || skipped.value.some(item => item.id === section)))
const incomplete = computed(() => {
  const probes = Array.isArray(props.report.probes) ? props.report.probes.map(auditRecord) : []
  return probes.some(item => item.status === 'queued' || item.status === 'running') || auditRecord(verdict.value.iq).complete === false
})
function sectionScore(section: string): string | null {
  const value = auditRecord(verdict.value[section])
  if (typeof value.score !== 'number') return null
  const max = value.max_score ?? value.max
  return typeof max === 'number' ? `${value.score} / ${max}` : String(value.score)
}
function sectionSummary(section: string): string {
  const value = auditRecord(verdict.value[section])
  if (section === 'iq' && (value.complete === false || value.score == null)) return t('admin.scheduledTests.auditNotCompleted')
  if (section === 'fingerprint' && typeof value.top_model === 'string') {
    return `${t('admin.scheduledTests.auditFingerprintCandidate')}${value.top_model}${typeof value.note === 'string' ? `\n${value.note}` : ''}`
  }
  if (section === 'knowledge' && typeof value.label === 'string') return value.label
  const reason = value.reason ?? value.note
  if (typeof reason === 'string' && reason) return reason
  if (typeof value.status === 'string') return value.status
  const skippedReason = skipped.value.find(item => item.id === section)?.reason
  return typeof skippedReason === 'string' ? skippedReason : t('admin.scheduledTests.auditNoConclusion')
}
</script>
