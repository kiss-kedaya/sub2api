<template>
  <BaseDialog :show="show" :title="t('admin.promptAudit.events.filterDeleteDialogTitle')" width="wide" @close="$emit('close')">
    <div class="space-y-5 text-sm">
      <p class="text-muted">{{ t('admin.promptAudit.events.filterDeleteDialogDesc') }}</p>

      <div role="group" aria-labelledby="prompt-delete-range-label">
        <p id="prompt-delete-range-label" class="font-medium text-default">{{ t('admin.promptAudit.events.filterTimeRange') }}</p>
        <URadioGroup
          :model-value="preset"
          orientation="horizontal"
          color="error"
          class="mt-2"
          :ui="{ fieldset: 'flex-wrap gap-x-4 gap-y-2' }"
          :aria-label="t('admin.promptAudit.events.filterTimeRange')"
          :items="presetItems"
          @update:model-value="selectPreset"
        />
        <p class="mt-2 text-xs text-muted">{{ t('admin.promptAudit.events.filterTimeRangeHint') }}</p>
        <div v-if="preset === 'custom'" class="mt-3 grid gap-3 sm:grid-cols-2" data-test="custom-range">
          <UFormField :label="t('admin.promptAudit.events.startAt')" size="sm">
            <UInput v-model="local.start_at" type="datetime-local" class="w-full" :aria-label="t('admin.promptAudit.events.startAt')" @update:model-value="criteriaChanged" />
          </UFormField>
          <UFormField :label="t('admin.promptAudit.events.endAt')" size="sm">
            <UInput v-model="local.end_at" type="datetime-local" class="w-full" :aria-label="t('admin.promptAudit.events.endAt')" @update:model-value="criteriaChanged" />
          </UFormField>
          <p v-if="!canPreview" class="text-xs text-error sm:col-span-2">{{ t('admin.promptAudit.events.customRangeInvalid') }}</p>
        </div>
      </div>

      <div class="grid gap-3 sm:grid-cols-2">
        <UFormField :label="t('admin.promptAudit.events.decision')" size="sm">
          <USelect v-model="decisionSelect" :items="decisionItems" class="w-full" :aria-label="t('admin.promptAudit.events.decision')" data-test="delete-decision" />
        </UFormField>
        <UFormField :label="t('admin.promptAudit.events.risk')" size="sm">
          <USelect v-model="riskSelect" :items="riskItems" class="w-full" :aria-label="t('admin.promptAudit.events.risk')" data-test="delete-risk" />
        </UFormField>
      </div>

      <UCollapsible :unmount-on-hide="false" data-test="more-conditions">
        <template #default="{ open }">
          <UButton color="neutral" variant="ghost" size="sm" class="-ms-2.5" :trailing-icon="open ? 'i-lucide-chevron-up' : 'i-lucide-chevron-down'" :label="t('admin.promptAudit.events.moreConditions')" />
        </template>
        <template #content>
          <div class="mt-2 grid gap-3 sm:grid-cols-2">
            <UFormField v-for="field in moreFields" :key="field.key" :label="field.label" size="sm">
              <UInput :model-value="local[field.key]" :type="field.type" class="w-full" :aria-label="field.label" @update:model-value="setField(field.key, $event)" />
            </UFormField>
          </div>
        </template>
      </UCollapsible>

      <UAlert v-if="preview" color="error" variant="subtle" icon="i-lucide-triangle-alert" :title="t('admin.promptAudit.events.filterDeleteCount', { count: preview.matched_count })" data-test="delete-preview-result">
        <template #description>
          <dl class="mt-1 grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-xs text-toned">
            <dt>{{ t('admin.promptAudit.events.snapshotMax') }}</dt>
            <dd>{{ preview.snapshot_max_id }}</dd>
            <dt>Filter SHA-256</dt>
            <dd class="break-all font-mono">{{ preview.filter_hash }}</dd>
            <dt>{{ t('admin.promptAudit.events.expiresAt') }}</dt>
            <dd>{{ formatDate(preview.expires_at) }}</dd>
          </dl>
          <p class="mt-2 text-xs text-warning">{{ t('admin.promptAudit.events.filterDeleteWarning') }}</p>
        </template>
      </UAlert>
      <p v-else class="flex items-start gap-2 text-xs text-muted" data-test="delete-preview-empty">
        <UIcon name="i-lucide-info" class="mt-px size-4 shrink-0" />
        {{ t('admin.promptAudit.events.filterDeleteNeedPreview') }}
      </p>
    </div>

    <template #footer>
      <div class="flex flex-wrap items-center justify-end gap-2">
        <p v-if="confirmDisabledReason" class="me-auto text-xs text-muted" data-test="confirm-disabled-reason">
          {{ t(confirmDisabledReason) }}
        </p>
        <UButton color="neutral" variant="subtle" :label="t('common.cancel')" @click="$emit('close')" />
        <UButton
          color="neutral"
          variant="outline"
          :loading="previewing"
          :disabled="!canPreview || previewing || deleting"
          :label="previewing ? t('admin.promptAudit.events.filterDeletePreviewing') : t('admin.promptAudit.events.filterDeletePreviewAction')"
          data-test="run-delete-preview"
          @click="requestPreview"
        />
        <UButton
          color="error"
          :loading="deleting"
          :disabled="confirmDisabled"
          :title="confirmDisabledReason ? t(confirmDisabledReason) : undefined"
          :label="deleting ? t('common.submitting') : t('admin.promptAudit.events.confirmFilterDelete')"
          data-test="confirm-filter-delete"
          @click="requestConfirm"
        />
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import type { PromptDeletePreview, PromptEventFilters } from '../types'
import {
  DELETE_RANGE_PRESETS,
  cloneData,
  emptyEventFilters,
  hasExplicitDeleteRange,
  resolveDeleteRangeFilters,
  type DeleteRangePreset,
} from '../viewModel'

const props = defineProps<{
  show: boolean
  initialFilters: PromptEventFilters
  preview: PromptDeletePreview | null
  previewing: boolean
  deleting: boolean
}>()
const emit = defineEmits<{
  (event: 'close'): void
  (event: 'preview', value: PromptEventFilters): void
  (event: 'confirm', value: PromptEventFilters): void
  (event: 'criteria-change'): void
}>()
const { t, locale } = useI18n()

const preset = ref<DeleteRangePreset>('7d')
const local = reactive<PromptEventFilters>(emptyEventFilters())

watch(
  () => props.show,
  (visible) => {
    if (!visible) return
    const initial = cloneData(props.initialFilters)
    // Only inherit an explicit list-filter range; otherwise default to the
    // seven-day preset so a careless click can never target everything.
    preset.value = hasExplicitDeleteRange(initial) ? 'custom' : '7d'
    Object.assign(local, initial)
  },
  { immediate: true },
)

const canPreview = computed(() => preset.value !== 'custom' || hasExplicitDeleteRange(local))

const presetItems = computed(() => DELETE_RANGE_PRESETS.map((option) => ({ value: option.id, label: t(`admin.promptAudit.events.timePresets.${option.id}`) })))
function selectPreset(value: unknown) {
  const next = DELETE_RANGE_PRESETS.find((option) => option.id === value)
  if (!next) return
  preset.value = next.id
  criteriaChanged()
}
// USelect items cannot carry an empty value, so "all" is a sentinel mapped back to ''.
const ALL = '__all__'
const decisionSelect = computed({
  get: () => local.decision || ALL,
  set: (value: string) => { local.decision = value === ALL ? '' : value; criteriaChanged() },
})
const riskSelect = computed({
  get: () => local.risk_level || ALL,
  set: (value: string) => { local.risk_level = value === ALL ? '' : value; criteriaChanged() },
})
const decisionItems = computed(() => [
  { label: t('common.all'), value: ALL },
  ...['pass', 'flag', 'critical'].map((value) => ({ label: t(`admin.promptAudit.decisions.${value}`), value })),
])
const riskItems = computed(() => [
  { label: t('common.all'), value: ALL },
  ...['low', 'medium', 'high', 'critical'].map((value) => ({ label: t(`admin.promptAudit.riskLevels.${value}`), value })),
])
type MoreKey = 'endpoint' | 'keyword' | 'group_id' | 'user_id'
const moreFields = computed<Array<{ key: MoreKey; label: string; type: 'text' | 'number' }>>(() => [
  { key: 'endpoint', label: t('admin.promptAudit.events.endpoint'), type: 'text' },
  { key: 'keyword', label: t('admin.promptAudit.events.keyword'), type: 'text' },
  { key: 'group_id', label: t('admin.promptAudit.events.groupId'), type: 'number' },
  { key: 'user_id', label: t('admin.promptAudit.events.userId'), type: 'number' },
])
// UInput type=number hands back numbers; the criteria keep strings, like the native inputs did.
function setField(key: MoreKey, value: unknown) {
  local[key] = value == null ? '' : String(value)
  criteriaChanged()
}

// One-click flow: a valid criteria selection is enough to confirm — the parent
// mints the server-side confirmation token on the fly. The button stays
// disabled only when the range is invalid, work is in flight, or a fresh
// preview already proved there is nothing to delete.
const confirmDisabled = computed(
  () => !canPreview.value || props.previewing || props.deleting || (props.preview !== null && props.preview.matched_count === 0),
)
const confirmDisabledReason = computed(() => {
  if (props.previewing || props.deleting) return ''
  if (!canPreview.value) return 'admin.promptAudit.events.filterDeleteConfirmInvalidRange'
  if (props.preview && props.preview.matched_count === 0) return 'admin.promptAudit.events.filterDeleteConfirmNoMatches'
  return ''
})

function criteriaChanged() {
  emit('criteria-change')
}
function requestPreview() {
  if (!canPreview.value) return
  emit('preview', resolveDeleteRangeFilters(local, preset.value))
}
function requestConfirm() {
  if (confirmDisabled.value) return
  emit('confirm', resolveDeleteRangeFilters(local, preset.value))
}
function formatDate(value: string): string {
  return new Intl.DateTimeFormat(locale.value, { dateStyle: 'medium', timeStyle: 'medium' }).format(new Date(value))
}
</script>
