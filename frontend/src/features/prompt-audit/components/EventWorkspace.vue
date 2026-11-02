<template>
  <section aria-labelledby="prompt-events-title" aria-describedby="prompt-events-description" class="flex flex-col gap-4">
    <h2 id="prompt-events-title" class="sr-only">{{ t('admin.promptAudit.events.title') }}</h2>
    <p id="prompt-events-description" class="sr-only">{{ t('admin.promptAudit.events.description') }}</p>

    <form class="flex flex-col gap-3" @submit.prevent="applyFilters">
      <div class="flex flex-wrap items-center justify-between gap-1.5">
        <div class="flex min-w-0 flex-1 flex-wrap items-center gap-1.5">
          <UInput
            :model-value="localFilters.keyword"
            icon="i-lucide-search"
            class="w-full sm:max-w-xs"
            :placeholder="t('admin.promptAudit.events.keyword')"
            :aria-label="t('admin.promptAudit.events.keyword')"
            @update:model-value="setField('keyword', $event)"
            @change="filtersChanged"
          />
          <USelect v-model="decisionSelect" :items="decisionItems" class="min-w-32" :aria-label="t('admin.promptAudit.events.decision')" />
          <USelect v-model="riskSelect" :items="riskItems" class="min-w-32" :aria-label="t('admin.promptAudit.events.risk')" />
          <UButton
            color="neutral"
            variant="outline"
            icon="i-lucide-sliders-horizontal"
            :label="t('admin.promptAudit.events.moreFilters')"
            :aria-expanded="showMore"
            data-test="more-filters"
            @click="showMore = !showMore"
          >
            <template v-if="advancedCount" #trailing><UBadge :label="advancedCount" color="neutral" variant="subtle" size="sm" /></template>
          </UButton>
          <UButton type="submit" :label="t('common.search')" />
          <UButton color="neutral" variant="ghost" :label="t('common.reset')" @click="resetFilters" />
        </div>
        <div class="flex flex-wrap items-center gap-1.5">
          <UButton
            v-if="selectedIds.length"
            color="error"
            variant="subtle"
            icon="i-lucide-trash-2"
            :label="t('admin.promptAudit.events.deleteSelected', { count: selectedIds.length })"
            @click="$emit('batch-delete')"
          />
          <UButton color="error" variant="outline" icon="i-lucide-funnel-x" :label="t('admin.promptAudit.events.deleteByFilter')" data-test="filter-delete" @click="$emit('preview-delete')" />
          <UDropdownMenu :items="columnMenu" :content="{ align: 'end' }">
            <UButton color="neutral" variant="outline" trailing-icon="i-lucide-columns-3" :label="t('admin.promptAudit.events.columns')" />
          </UDropdownMenu>
        </div>
      </div>
      <UCollapsible v-model:open="showMore" :unmount-on-hide="false">
        <template #content>
          <div class="grid gap-3 rounded-lg bg-elevated/50 p-3 sm:grid-cols-2 lg:grid-cols-4">
            <UFormField v-for="field in advancedFields" :key="field.key" :label="field.label" size="sm">
              <UInput
                :model-value="localFilters[field.key]"
                :type="field.type"
                class="w-full"
                :aria-label="field.label"
                @update:model-value="setField(field.key, $event)"
                @change="filtersChanged"
              />
            </UFormField>
          </div>
        </template>
      </UCollapsible>
    </form>

    <UAlert v-if="error" role="alert" color="error" variant="subtle" icon="i-lucide-circle-alert" :description="error" />

    <UTable
      class="overflow-x-auto"
      :data="events"
      :columns="columns"
      :column-visibility="columnVisibility"
      :loading="loading"
      :ui="tableUi"
    >
      <template #select-header>
        <UCheckbox :model-value="headerChecked" :aria-label="t('admin.promptAudit.events.selectAll')" @update:model-value="toggleAll" />
      </template>
      <template #select-cell="{ row }">
        <UCheckbox :model-value="selectedIds.includes(row.original.id)" :aria-label="t('admin.promptAudit.events.selectEvent', { id: row.original.id })" @update:model-value="toggleOne(row.original.id)" />
      </template>
      <template #time-cell="{ row }">
        <span class="whitespace-nowrap text-xs" :data-test="`event-${row.original.id}`">{{ formatDate(row.original.created_at) }}</span>
      </template>
      <template #identity-cell="{ row }">
        <div class="grid w-64 grid-cols-[auto_minmax(0,1fr)_auto] items-center gap-x-2 text-xs">
          <template v-for="line in identityLines(row.original)" :key="line.label">
            <span class="text-dimmed">{{ line.label }}</span>
            <span class="truncate text-default" :title="line.value">{{ line.value || '—' }}</span>
            <UButton
              v-if="line.value"
              icon="i-lucide-copy"
              color="neutral"
              variant="ghost"
              size="xs"
              :aria-label="`${t('common.copy')} ${line.label}`"
              :title="`${t('common.copy')} ${line.label}`"
              @click="copy(line.value)"
            />
            <span v-else />
          </template>
        </div>
      </template>
      <template #group-cell="{ row }">{{ row.original.snapshot.group_name || '—' }}</template>
      <template #route-cell="{ row }">
        <p class="font-medium text-highlighted">{{ row.original.snapshot.endpoint }}</p>
        <p class="mt-1 text-xs text-muted">{{ row.original.snapshot.model }} · {{ row.original.snapshot.protocol }} · {{ row.original.snapshot.stage || 'http' }}</p>
      </template>
      <template #result-cell="{ row }">
        <UBadge :color="decisionColor(row.original.decision)" variant="subtle" :label="formatDecisionRisk(row.original.decision, row.original.risk_level)" />
        <p class="mt-2 max-w-48 truncate text-xs text-muted" :title="formatCategories(row.original.categories)">{{ formatCategories(row.original.categories) }}</p>
      </template>
      <template #preview-cell="{ row }">
        <p class="line-clamp-2 max-w-xs wrap-break-word text-toned">{{ row.original.snapshot.redacted_preview || '—' }}</p>
      </template>
      <template #actions-cell="{ row }">
        <div class="flex justify-end gap-1">
          <UButton icon="i-lucide-eye" color="neutral" variant="ghost" size="sm" :aria-label="`${t('common.view')} #${row.original.id}`" :title="t('common.view')" @click="$emit('view', row.original.id)" />
          <UButton icon="i-lucide-trash-2" color="error" variant="ghost" size="sm" :aria-label="`${t('common.delete')} #${row.original.id}`" :title="t('common.delete')" @click="$emit('delete', row.original.id)" />
        </div>
      </template>
      <template #loading>
        <span aria-busy="true">{{ t('common.loading') }}</span>
      </template>
      <template #empty>{{ t('admin.promptAudit.events.empty') }}</template>
    </UTable>

    <div class="flex flex-col gap-1">
      <p class="text-sm text-muted">{{ t('admin.promptAudit.events.selectedSummary', { selected: selectedIds.length, total }) }}</p>
      <Pagination :total="total" :page="page" :page-size="pageSize" @update:page="$emit('page', $event)" @update:page-size="$emit('page-size', $event)" />
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { DropdownMenuItem, TableColumn } from '@nuxt/ui'
import Pagination from '@/components/common/Pagination.vue'
import type { PromptAuditEvent, PromptEventFilters } from '../types'
import { cloneData, emptyEventFilters, SCANNER_CATALOG } from '../viewModel'

const props = defineProps<{
  events: PromptAuditEvent[]; total: number; page: number; pageSize: number
  filters: PromptEventFilters; selectedIds: number[]; loading: boolean; error: string
}>()
const emit = defineEmits<{
  (event: 'filters-change', value: PromptEventFilters): void
  (event: 'search', value: PromptEventFilters): void
  (event: 'selection', value: number[]): void
  (event: 'page', value: number): void
  (event: 'page-size', value: number): void
  (event: 'view', id: number): void
  (event: 'delete', id: number): void
  (event: 'batch-delete'): void
  (event: 'preview-delete'): void
}>()
const { t, locale } = useI18n()
const localFilters = reactive<PromptEventFilters>(cloneData(props.filters))
watch(() => props.filters, (value) => Object.assign(localFilters, cloneData(value)), { deep: true })
const allSelected = computed(() => props.events.length > 0 && props.events.every((event) => props.selectedIds.includes(event.id)))
const headerChecked = computed<boolean | 'indeterminate'>(() => allSelected.value ? true : props.events.some((event) => props.selectedIds.includes(event.id)) ? 'indeterminate' : false)

// USelect items cannot carry an empty value, so "all" is a sentinel mapped back to ''.
const ALL = '__all__'
const decisionSelect = computed({
  get: () => localFilters.decision || ALL,
  set: (value: string) => { localFilters.decision = value === ALL ? '' : value; filtersChanged() },
})
const riskSelect = computed({
  get: () => localFilters.risk_level || ALL,
  set: (value: string) => { localFilters.risk_level = value === ALL ? '' : value; filtersChanged() },
})
const decisionItems = computed(() => [
  { label: t('admin.promptAudit.events.allDecisions'), value: ALL },
  ...['pass', 'flag', 'critical'].map((value) => ({ label: t(`admin.promptAudit.decisions.${value}`), value })),
])
const riskItems = computed(() => [
  { label: t('admin.promptAudit.events.allRisks'), value: ALL },
  ...['low', 'medium', 'high', 'critical'].map((value) => ({ label: t(`admin.promptAudit.riskLevels.${value}`), value })),
])
type AdvancedKey = Exclude<keyof PromptEventFilters, 'decision' | 'risk_level' | 'keyword'>
const advancedFields = computed<Array<{ key: AdvancedKey; label: string; type: 'text' | 'number' | 'datetime-local' }>>(() => [
  { key: 'endpoint', label: t('admin.promptAudit.events.endpoint'), type: 'text' },
  { key: 'group_id', label: t('admin.promptAudit.events.groupId'), type: 'number' },
  { key: 'user_id', label: t('admin.promptAudit.events.userId'), type: 'number' },
  { key: 'api_key_id', label: t('admin.promptAudit.events.apiKeyId'), type: 'number' },
  { key: 'request_id', label: t('admin.promptAudit.events.requestId'), type: 'text' },
  { key: 'prompt_hash', label: t('admin.promptAudit.events.promptHash'), type: 'text' },
  { key: 'start_at', label: t('admin.promptAudit.events.startAt'), type: 'datetime-local' },
  { key: 'end_at', label: t('admin.promptAudit.events.endAt'), type: 'datetime-local' },
])
const advancedCount = computed(() => advancedFields.value.filter((field) => localFilters[field.key]).length)
// Rarely used conditions stay folded unless the current filters already use one of them.
const showMore = ref(advancedCount.value > 0)
// UInput type=number hands back numbers; the filter model keeps strings, like the native inputs did.
function setField(key: keyof PromptEventFilters, value: unknown) {
  localFilters[key] = value == null ? '' : String(value)
}

const columnLabels = computed(() => ({
  time: t('admin.promptAudit.events.time'),
  identity: t('admin.promptAudit.events.identity'),
  group: t('admin.promptAudit.events.group'),
  route: t('admin.promptAudit.events.route'),
  result: t('admin.promptAudit.events.result'),
  preview: t('admin.promptAudit.events.preview'),
}))
type HideableColumn = keyof typeof columnLabels.value
const HIDEABLE: HideableColumn[] = ['time', 'identity', 'group', 'route', 'result', 'preview']
const columns = computed<TableColumn<PromptAuditEvent>[]>(() => [
  { id: 'select', enableHiding: false, meta: { class: { th: 'w-10', td: 'w-10' } } },
  ...HIDEABLE.map((id) => ({ id, header: columnLabels.value[id] })),
  { id: 'actions', header: t('admin.promptAudit.common.actions'), enableHiding: false, meta: { class: { th: 'text-right', td: 'text-right' } } },
])
// "Columns" menu: the least used column starts hidden; the choice is remembered per browser.
const COLUMN_STORAGE_KEY = 'prompt-audit-event-columns-hidden'
function readHiddenColumns(): string[] {
  try {
    const parsed = JSON.parse(localStorage.getItem(COLUMN_STORAGE_KEY) || 'null')
    if (Array.isArray(parsed)) return parsed.filter((value): value is string => typeof value === 'string')
  } catch { /* unreadable storage: use the default */ }
  return ['group']
}
const hiddenColumns = ref<string[]>(readHiddenColumns())
watch(hiddenColumns, (value) => {
  try { localStorage.setItem(COLUMN_STORAGE_KEY, JSON.stringify(value)) } catch { /* storage unavailable */ }
})
const columnVisibility = computed(() => Object.fromEntries(hiddenColumns.value.map((id) => [id, false])))
const columnMenu = computed<DropdownMenuItem[]>(() => HIDEABLE.map((id) => ({
  label: columnLabels.value[id],
  type: 'checkbox' as const,
  checked: !hiddenColumns.value.includes(id),
  onUpdateChecked: (checked: boolean) => {
    hiddenColumns.value = checked ? hiddenColumns.value.filter((column) => column !== id) : [...new Set([...hiddenColumns.value, id])]
  },
  onSelect: (event: Event) => event.preventDefault(),
})))
const tableUi = {
  base: 'min-w-[960px] border-separate border-spacing-0',
  thead: '[&>tr]:bg-elevated/50 [&>tr]:after:content-none',
  tbody: '[&>tr]:last:[&>td]:border-b-0',
  th: 'py-2 first:rounded-l-lg last:rounded-r-lg border-y border-default first:border-l last:border-r',
  td: 'border-b border-default align-top whitespace-normal',
  separator: 'h-0',
}

function identityLines(event: PromptAuditEvent) {
  return [
    { label: t('admin.promptAudit.events.user'), value: event.snapshot.username },
    { label: t('admin.promptAudit.events.email'), value: event.snapshot.user_email },
    { label: t('admin.promptAudit.events.apiKey'), value: event.snapshot.api_key_name },
  ]
}
function copy(value: string) {
  void navigator.clipboard?.writeText(value)
}
function filtersChanged() {
  emit('filters-change', cloneData(localFilters))
}
function applyFilters() {
  const value = cloneData(localFilters)
  emit('filters-change', value)
  emit('search', value)
}
function resetFilters() {
  Object.assign(localFilters, emptyEventFilters())
  applyFilters()
}
function toggleOne(id: number) {
  const selected = new Set(props.selectedIds)
  if (selected.has(id)) selected.delete(id)
  else selected.add(id)
  emit('selection', [...selected])
}
function toggleAll() {
  emit('selection', allSelected.value ? [] : props.events.map((event) => event.id))
}
function formatDate(value: string): string {
  return new Intl.DateTimeFormat(locale.value, { dateStyle: 'short', timeStyle: 'medium' }).format(new Date(value))
}
function decisionColor(decision: string): 'error' | 'warning' | 'success' {
  if (decision === 'critical') return 'error'
  if (decision === 'flag') return 'warning'
  return 'success'
}
const DECISIONS = new Set(['pass', 'flag', 'critical'])
const RISK_LEVELS = new Set(['low', 'medium', 'high', 'critical'])

function translateDecision(decision: string): string {
  return DECISIONS.has(decision) ? t(`admin.promptAudit.decisions.${decision}`) : decision
}
function translateRiskLevel(riskLevel: string): string {
  return RISK_LEVELS.has(riskLevel) ? t(`admin.promptAudit.riskLevels.${riskLevel}`) : riskLevel
}
function translateCategory(category: string): string {
  return SCANNER_CATALOG.some((scanner) => scanner.id === category)
    ? t(`admin.promptAudit.scanners.${category}`)
    : category
}
function formatDecisionRisk(decision: string, riskLevel: string): string {
  return `${translateDecision(decision)} · ${translateRiskLevel(riskLevel)}`
}
function formatCategories(categories: string[]): string {
  if (!categories.length) return '—'
  return categories.map(translateCategory).join(', ')
}
</script>
