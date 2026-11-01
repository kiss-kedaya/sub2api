<template>
  <section aria-labelledby="prompt-policy-title">
    <UPageCard variant="naked" orientation="horizontal" :ui="{ container: 'lg:grid-cols-[minmax(0,1fr)_auto]' }" class="mb-4" :description="t('admin.promptAudit.policy.description')">
      <template #title><h2 id="prompt-policy-title">{{ t('admin.promptAudit.policy.title') }}</h2></template>
    </UPageCard>

    <UPageCard variant="subtle">
      <!-- Rows holding several controls are labelled groups: a UFormField hands its single input id to every nested control. -->
      <div role="group" aria-labelledby="prompt-policy-scope" class="flex justify-between gap-4 text-sm max-sm:flex-col sm:items-start">
        <p id="prompt-policy-scope" class="font-medium text-default">{{ t('admin.promptAudit.policy.scope') }}</p>
        <div class="w-full sm:max-w-sm">
          <URadioGroup
            :model-value="draft.all_groups ? 'all' : 'selected'"
            orientation="horizontal"
            :items="[{ label: t('admin.promptAudit.policy.allGroups'), value: 'all' }, { label: t('admin.promptAudit.policy.selectedGroups'), value: 'selected' }]"
            @update:model-value="setScope"
          />
          <div v-if="!draft.all_groups" class="mt-3 space-y-2">
            <UInput v-model="groupSearch" type="search" icon="i-lucide-search" class="w-full" :placeholder="t('admin.promptAudit.policy.searchGroups')" :aria-label="t('admin.promptAudit.policy.searchGroups')" />
            <div class="max-h-52 overflow-y-auto rounded-md border border-default">
              <div v-for="group in filteredGroups" :key="group.id" class="flex items-center justify-between gap-3 px-3 py-2 not-last:border-b not-last:border-default">
                <UCheckbox :model-value="draft.group_ids.includes(group.id)" :label="group.name" @update:model-value="toggleGroup(group.id)" />
                <span class="shrink-0 text-xs text-muted">{{ group.platform }} · {{ group.status }}</span>
              </div>
              <p v-if="filteredGroups.length === 0" class="px-3 py-4 text-center text-sm text-muted">{{ t('admin.promptAudit.policy.noGroups') }}</p>
            </div>
            <UAlert v-if="missingGroupIds.length" color="warning" variant="subtle" :description="`${t('admin.promptAudit.policy.missingGroups')}: ${missingGroupIds.join(', ')}`" />
            <p class="text-xs text-muted">{{ t('admin.promptAudit.policy.selectedCount', { count: draft.group_ids.length }) }}</p>
          </div>
        </div>
      </div>
      <USeparator />
      <div role="group" aria-labelledby="prompt-policy-scanners" class="flex justify-between gap-4 text-sm max-sm:flex-col sm:items-start">
        <p id="prompt-policy-scanners" class="font-medium text-default">{{ t('admin.promptAudit.policy.scanners') }}</p>
        <div class="grid w-full gap-2 sm:max-w-sm sm:grid-cols-2">
          <UCheckbox
            v-for="scanner in SCANNER_CATALOG"
            :key="scanner.id"
            :model-value="draft.scanners.includes(scanner.id)"
            :label="scannerLabel(scanner.id)"
            :aria-label="scannerLabel(scanner.id)"
            @update:model-value="toggleScanner(scanner.id)"
          />
        </div>
      </div>
      <USeparator />
      <UFormField :label="t('admin.promptAudit.policy.workerCount')" class="flex items-start justify-between gap-4 max-sm:flex-col">
        <UInput :model-value="draft.worker_count" type="number" min="1" max="32" class="w-40" :aria-label="t('admin.promptAudit.policy.workerCount')" @update:model-value="patch({ worker_count: Number($event) })" />
      </UFormField>
      <USeparator />
      <UFormField :label="t('admin.promptAudit.policy.queueCapacity')" class="flex items-start justify-between gap-4 max-sm:flex-col">
        <UInput :model-value="draft.queue_capacity" type="number" min="1" max="100000" class="w-40" :aria-label="t('admin.promptAudit.policy.queueCapacity')" @update:model-value="patch({ queue_capacity: Number($event) })" />
      </UFormField>
      <USeparator />
      <UFormField :label="t('admin.promptAudit.policy.strategy')" :description="t('admin.promptAudit.policy.strategyHint')" class="flex items-start justify-between gap-4 max-sm:flex-col">
        <UBadge color="neutral" variant="subtle" label="priority" />
      </UFormField>
    </UPageCard>
  </section>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import type { PromptAuditDraft, PromptAuditGroup } from '../types'
import { cloneData, SCANNER_CATALOG } from '../viewModel'

const props = defineProps<{ draft: PromptAuditDraft; groups: PromptAuditGroup[] }>()
const emit = defineEmits<{ (event: 'update:draft', value: PromptAuditDraft): void }>()
const { t } = useI18n()
const groupSearch = ref('')

const filteredGroups = computed(() => {
  const query = groupSearch.value.trim().toLowerCase()
  if (!query) return props.groups
  return props.groups.filter((group) => `${group.name} ${group.id} ${group.platform}`.toLowerCase().includes(query))
})
const knownGroupIds = computed(() => new Set(props.groups.map((group) => group.id)))
const missingGroupIds = computed(() => props.draft.group_ids.filter((id) => !knownGroupIds.value.has(id)))

function patch(value: Partial<PromptAuditDraft>) {
  emit('update:draft', { ...cloneData(props.draft), ...value })
}
function setScope(value: unknown) {
  if (value === 'all') patch({ all_groups: true, group_ids: [] })
  else patch({ all_groups: false })
}
function toggleGroup(id: number) {
  const selected = new Set(props.draft.group_ids)
  if (selected.has(id)) selected.delete(id)
  else selected.add(id)
  patch({ group_ids: [...selected].sort((a, b) => a - b) })
}
function toggleScanner(id: string) {
  const selected = new Set(props.draft.scanners)
  if (selected.has(id)) selected.delete(id)
  else selected.add(id)
  patch({ scanners: SCANNER_CATALOG.map((item) => item.id).filter((item) => selected.has(item)) })
}
function scannerLabel(id: string): string {
  return t(`admin.promptAudit.scanners.${id}`)
}
</script>
