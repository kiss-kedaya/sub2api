<template>
  <BaseDialog :show="show" :title="editor ? tr(editingExisting ? 'editRule' : 'addRule') : tr('manageBlacklist')" width="wide" @close="emit('close')">
    <div class="space-y-4">
      <p class="text-sm text-gray-500">{{ tr('blacklistDraftHint') }}</p>
      <div v-if="!editor" ref="listView" class="space-y-4">
        <div class="flex flex-wrap items-center justify-between gap-2">
          <span class="text-sm text-gray-500">{{ tr('ruleCount', { count: rules.length }) }}</span>
          <button type="button" class="btn btn-primary btn-sm" ref="addButton" data-testid="risk-add-rule" @click="openEditor(null)">{{ tr('newRule') }}</button>
        </div>
        <div v-if="rules.length" class="overflow-x-auto rounded-lg border border-gray-200 dark:border-dark-700">
          <table class="w-full min-w-[640px] text-left text-sm" data-testid="risk-blacklist-table">
            <caption class="sr-only">{{ tr('blacklist') }}</caption>
            <thead class="bg-gray-50 text-gray-500 dark:bg-dark-800 dark:text-gray-400">
              <tr>
                <th scope="col" class="px-4 py-3 font-medium">{{ tr('network') }}</th>
                <th scope="col" class="px-4 py-3 font-medium">{{ tr('note') }}</th>
                <th scope="col" class="px-4 py-3 font-medium">{{ tr('ruleStatus') }}</th>
                <th scope="col" class="px-4 py-3 font-medium">{{ tr('expiresAt') }}</th>
                <th scope="col" class="px-4 py-3 text-right font-medium">{{ tr('ruleActions') }}</th>
              </tr>
            </thead>
            <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
              <tr v-for="(rule, index) in rules" :key="rule.key" :data-testid="`risk-rule-row-${index}`">
                <td class="px-4 py-3"><code class="break-all">{{ rule.network }}</code></td>
                <td class="max-w-64 wrap-break-word px-4 py-3">{{ rule.note || '—' }}</td>
                <td class="whitespace-nowrap px-4 py-3">
                  <span class="rounded-full px-2 py-1 text-xs" :class="rule.enabled ? 'bg-emerald-50 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300' : 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-gray-400'">{{ tr(rule.enabled ? 'protectionEnabled' : 'protectionDisabled') }}</span>
                </td>
                <td class="whitespace-nowrap px-4 py-3 text-gray-500">{{ rule.expires_at ? new Date(rule.expires_at * 1000).toLocaleString() : tr('permanent') }}</td>
                <td class="px-4 py-3">
                  <div class="flex justify-end gap-3 whitespace-nowrap">
                    <button type="button" class="text-primary-600 hover:underline dark:text-primary-400" :aria-label="`${tr('edit')} ${rule.network}`" :data-testid="`risk-rule-edit-${index}`" :data-rule-key="rule.key" @click="openEditor(rule)">{{ tr('edit') }}</button>
                    <button type="button" class="text-red-600 hover:underline dark:text-red-400" :aria-label="`${tr('remove')} ${rule.network}`" :data-testid="`risk-rule-remove-${index}`" @click="rules.splice(index, 1)">{{ tr('remove') }}</button>
                  </div>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
        <p v-else class="rounded-lg bg-gray-50 p-6 text-center text-sm text-gray-500 dark:bg-dark-800">{{ tr('noRules') }}</p>
      </div>
      <form v-if="editor" id="ip-risk-rule-form" class="grid gap-4 sm:grid-cols-2" data-testid="risk-rule-form" @submit.prevent="saveRule">
        <p class="text-sm text-gray-500 sm:col-span-2">{{ tr('ruleEditorHint') }}</p>
        <label class="text-sm">{{ tr('network') }}<input ref="networkInput" v-model.trim="editor.network" class="input mt-1 w-full font-mono" placeholder="198.51.100.0/24" data-testid="risk-rule-network" required /></label>
        <label class="text-sm">{{ tr('note') }}<input v-model="editor.note" class="input mt-1 w-full" maxlength="256" data-testid="risk-rule-note" /></label>
        <label class="text-sm">{{ tr('expires') }}<input v-model="editor.expiration" type="datetime-local" class="input mt-1 w-full" data-testid="risk-rule-expiry" /></label>
        <label class="flex items-center gap-2 text-sm"><input v-model="editor.enabled" type="checkbox" data-testid="risk-rule-enabled" />{{ tr('ruleEnabled') }}</label>
      </form>
    </div>
    <template #footer>
      <template v-if="editor">
        <button type="button" class="btn btn-secondary" data-testid="risk-rule-cancel" @click="closeEditor">{{ tr('cancel') }}</button>
        <button type="submit" form="ip-risk-rule-form" class="btn btn-primary" data-testid="risk-rule-save">{{ tr('saveRule') }}</button>
      </template>
      <template v-else>
        <button type="button" class="btn btn-secondary" data-testid="risk-blacklist-cancel" @click="emit('close')">{{ tr('cancel') }}</button>
        <button type="button" class="btn btn-primary" data-testid="risk-blacklist-apply" @click="apply">{{ tr('applyBlacklist') }}</button>
      </template>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { nextTick, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
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
const networkInput = ref<HTMLInputElement | null>(null)
let nextKey = 0
const listView = ref<HTMLDivElement | null>(null)
const addButton = ref<HTMLButtonElement | null>(null)
let editorReturnKey: number | null = null

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
  networkInput.value?.focus()
}
async function closeEditor() {
  editor.value = null
  await nextTick()
  const button = editorReturnKey === null ? addButton.value : listView.value?.querySelector<HTMLButtonElement>(`[data-rule-key="${editorReturnKey}"]`)
  button?.focus()
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
