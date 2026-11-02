<template>
  <BaseDialog :show="show" :title="editor ? tr(editingExisting ? 'editRule' : 'addRule') : tr('manageBlacklist')" width="wide" @close="emit('close')">
    <div class="flex flex-col gap-4">
      <p class="text-sm text-muted">{{ tr('blacklistDraftHint') }}</p>
      <div v-if="!editor" ref="listView" class="flex flex-col gap-3">
        <div class="flex flex-wrap items-center justify-between gap-2">
          <UBadge color="neutral" variant="subtle">{{ tr('ruleCount', { count: rules.length }) }}</UBadge>
          <UButton :label="tr('newRule')" icon="i-lucide-plus" size="sm" data-testid="risk-add-rule" @click="openEditor(null)" />
        </div>
        <UTable v-if="rules.length" :data="rules" :columns="ruleColumns" :caption="tr('blacklist')" class="min-w-0" :ui="tableUi" data-testid="risk-blacklist-table">
          <template #network-cell="{ row }">
            <div :data-testid="`risk-rule-row-${row.index}`"><code class="break-all text-default">{{ row.original.network }}</code></div>
          </template>
          <template #note-cell="{ row }"><span class="block max-w-64 whitespace-normal wrap-break-word">{{ row.original.note || '—' }}</span></template>
          <template #status-cell="{ row }">
            <UBadge :color="row.original.enabled ? 'success' : 'neutral'" variant="subtle">{{ tr(row.original.enabled ? 'protectionEnabled' : 'protectionDisabled') }}</UBadge>
          </template>
          <template #expires-cell="{ row }">{{ row.original.expires_at ? new Date(row.original.expires_at * 1000).toLocaleString() : tr('permanent') }}</template>
          <template #actions-cell="{ row }">
            <div class="flex justify-end gap-1">
              <UButton icon="i-lucide-pencil" color="neutral" variant="ghost" size="sm" square :aria-label="`${tr('edit')} ${row.original.network}`" :title="tr('edit')" :data-testid="`risk-rule-edit-${row.index}`" :data-rule-key="row.original.key" @click="openEditor(row.original)" />
              <UButton icon="i-lucide-trash-2" color="error" variant="ghost" size="sm" square :aria-label="`${tr('remove')} ${row.original.network}`" :title="tr('remove')" :data-testid="`risk-rule-remove-${row.index}`" @click="rules.splice(row.index, 1)" />
            </div>
          </template>
        </UTable>
        <p v-else class="py-6 text-center text-sm text-muted">{{ tr('noRules') }}</p>
      </div>
      <form v-if="editor" id="ip-risk-rule-form" class="flex flex-col gap-4" data-testid="risk-rule-form" @submit.prevent="saveRule">
        <p class="text-sm text-muted">{{ tr('ruleEditorHint') }}</p>
        <UFormField :label="tr('network')" required>
          <UInput v-model.trim="editor.network" class="w-full" :ui="{ base: 'font-mono' }" placeholder="198.51.100.0/24" required data-testid="risk-rule-network" />
        </UFormField>
        <UFormField :label="tr('note')">
          <UInput v-model="editor.note" class="w-full" maxlength="256" data-testid="risk-rule-note" />
        </UFormField>
        <UFormField :label="tr('expires')">
          <UInput v-model="editor.expiration" type="datetime-local" class="w-full sm:w-64" data-testid="risk-rule-expiry" />
        </UFormField>
        <UFormField :label="tr('ruleEnabled')" class="flex items-center justify-between gap-4">
          <USwitch v-model="editor.enabled" data-testid="risk-rule-enabled" />
        </UFormField>
      </form>
    </div>
    <template #footer>
      <template v-if="editor">
        <UButton :label="tr('cancel')" color="neutral" variant="subtle" data-testid="risk-rule-cancel" @click="closeEditor" />
        <UButton :label="tr('saveRule')" type="submit" form="ip-risk-rule-form" data-testid="risk-rule-save" />
      </template>
      <template v-else>
        <UButton :label="tr('cancel')" color="neutral" variant="subtle" data-testid="risk-blacklist-cancel" @click="emit('close')" />
        <UButton :label="tr('applyBlacklist')" data-testid="risk-blacklist-apply" @click="apply" />
      </template>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { TableColumn } from '@nuxt/ui'
import BaseDialog from '@/components/common/BaseDialog.vue'
import type { IPBlockRule } from './api'

const props = defineProps<{ show: boolean; blacklist: IPBlockRule[] }>()
const emit = defineEmits<{ close: []; apply: [rules: IPBlockRule[]] }>()
const { t } = useI18n()
const tr = (key: string, values: Record<string, number> = {}) => t(`admin.settings.ipRisk.${key}`, values)
type RuleRow = IPBlockRule & { key: number }
type RuleDraft = RuleRow & { expiration: string; originalExpiration: string }
const rules = ref<RuleRow[]>([])
const editor = ref<RuleDraft | null>(null)
const editingExisting = ref(false)
let nextKey = 0
const listView = ref<HTMLDivElement | null>(null)
let editorReturnKey: number | null = null
const tableUi = {
  thead: '[&>tr]:bg-elevated/50 [&>tr]:after:content-none',
  tbody: '[&>tr]:last:[&>td]:border-b-0',
  th: 'py-2 first:rounded-l-lg last:rounded-r-lg border-y border-default first:border-l last:border-r',
  td: 'border-b border-default',
  separator: 'h-0'
}
const ruleColumns = computed<TableColumn<RuleRow>[]>(() => [
  { id: 'network', header: tr('network') },
  { id: 'note', header: tr('note') },
  { id: 'status', header: tr('ruleStatus') },
  { id: 'expires', header: tr('expiresAt') },
  { id: 'actions', header: tr('ruleActions'), meta: { class: { th: 'text-right' } } }
])

function localDate(seconds: number) {
  if (!seconds) return ''
  const value = new Date(seconds * 1000)
  return new Date(value.getTime() - value.getTimezoneOffset() * 60000).toISOString().slice(0, 16)
}
watch(() => props.show, show => {
  editor.value = null
  editorReturnKey = null
  if (show) rules.value = props.blacklist.map(rule => ({ ...rule, key: nextKey++ }))
}, { immediate: true })

async function openEditor(rule: RuleRow | null) {
  editorReturnKey = rule?.key ?? null
  editingExisting.value = rule !== null
  const value = rule ?? { key: nextKey++, network: '', note: '', enabled: true, expires_at: 0 }
  const expiration = localDate(value.expires_at)
  editor.value = { ...value, expiration, originalExpiration: expiration }
  await nextTick()
  document.querySelector<HTMLInputElement>('#ip-risk-rule-form [data-testid="risk-rule-network"]')?.focus()
}
async function closeEditor() {
  editor.value = null
  await nextTick()
  const selector = editorReturnKey === null ? '[data-testid="risk-add-rule"]' : `[data-rule-key="${editorReturnKey}"]`
  listView.value?.querySelector<HTMLButtonElement>(selector)?.focus()
}
function saveRule() {
  const rule = editor.value
  if (!rule || !rule.network.trim()) return
  const updated: RuleRow = {
    key: rule.key, network: rule.network.trim(), note: rule.note, enabled: rule.enabled,
    expires_at: rule.expiration === rule.originalExpiration ? rule.expires_at : rule.expiration ? Math.floor(new Date(rule.expiration).getTime() / 1000) : 0
  }
  const index = rules.value.findIndex(item => item.key === rule.key)
  if (index === -1) rules.value.push(updated)
  else rules.value[index] = updated
  void closeEditor()
}
function apply() {
  emit('apply', rules.value.map(rule => ({
    network: rule.network, note: rule.note, enabled: rule.enabled, expires_at: rule.expires_at
  })))
}
</script>
