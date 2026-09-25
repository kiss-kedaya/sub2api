<template>
  <BaseDialog :show="show" :title="t('admin.accounts.customUsage.title')" width="wide" :close-on-escape="!saving" :show-close-button="!saving" @close="close">
    <div v-if="loading" class="flex items-center justify-center gap-2 py-12 text-sm text-gray-500" role="status"><Icon name="refresh" size="sm" class="animate-spin motion-reduce:animate-none" />{{ t('admin.accounts.customUsage.loading') }}</div>
    <div v-else-if="loadFailed" class="space-y-3 py-6 text-sm" role="alert">
      <p>{{ t('admin.accounts.customUsage.loadFailed') }}</p>
      <button class="btn btn-secondary" type="button" @click="load">{{ t('admin.accounts.customUsage.retry') }}</button>
    </div>
    <form v-else id="custom-usage-form" class="space-y-5" @submit.prevent="save">
      <div class="flex items-center justify-between gap-4 rounded-lg border border-gray-200 p-4 dark:border-dark-600">
        <div><h4 class="text-sm font-medium text-gray-900 dark:text-gray-100">{{ t('admin.accounts.customUsage.enabled') }}</h4><p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ account?.name }}</p></div>
        <Toggle v-model="draft.enabled" :disabled="busy" :aria-label="t('admin.accounts.customUsage.enabled')" />
      </div>
      <fieldset :disabled="busy" class="space-y-5">
        <div>
          <span id="custom-usage-template-label" class="input-label">{{ t('admin.accounts.customUsage.template') }}</span>
          <div class="flex flex-wrap gap-2" role="group" aria-labelledby="custom-usage-template-label">
            <button v-for="template in templates" :key="template" type="button" :data-template="template" :aria-pressed="draft.template === template" :title="t('admin.accounts.customUsage.templateHint')" class="rounded-lg border px-3 py-1.5 text-sm transition-colors" :class="draft.template === template ? 'border-primary-300 bg-primary-50 text-primary-700 dark:border-primary-700 dark:bg-primary-900/20 dark:text-primary-300' : 'border-gray-200 text-gray-600 hover:bg-gray-50 dark:border-dark-600 dark:text-gray-400 dark:hover:bg-dark-700'" @click="draft.template = template; applyTemplate()">{{ t('admin.accounts.customUsage.templates.' + template) }}</button>
          </div>
        </div>
        <div class="grid gap-4 md:grid-cols-2">
        <div>
          <label for="custom-usage-base" class="input-label">{{ t('admin.accounts.customUsage.baseUrl') }}</label>
          <input id="custom-usage-base" v-model="draft.base_url" type="url" class="input" placeholder="https://api.example.com" autocomplete="off" />
        </div>
          <div v-for="field in visibleSecretFields" :key="field.name">
            <label :for="'custom-usage-' + field.name" class="input-label">{{ t('admin.accounts.customUsage.' + field.label) }}</label>
            <input :id="'custom-usage-' + field.name" v-model="draft[field.name]" type="password" class="input font-mono" autocomplete="new-password" spellcheck="false" data-1p-ignore data-lpignore="true" :disabled="draft[field.clear]" :placeholder="draft[field.has] ? t('admin.accounts.customUsage.secretSaved') : t('admin.accounts.customUsage.secretPlaceholder')" />
            <label v-if="draft[field.has]" class="mt-2 flex items-center gap-2 text-xs text-gray-500 dark:text-gray-400"><input v-model="draft[field.clear]" type="checkbox" class="checkbox" @change="draft[field.name] = ''" />{{ t('admin.accounts.customUsage.clearSecret') }}</label>
          </div>
        </div>
        <p class="input-hint">{{ t('admin.accounts.customUsage.secretHint') }}</p>
        <div :class="draft.template === 'newapi' || draft.template === 'custom' ? 'sm:grid-cols-3' : 'sm:grid-cols-2'" class="grid gap-4">
          <div v-if="draft.template === 'newapi' || draft.template === 'custom'"><label for="custom-usage-user" class="input-label">{{ t('admin.accounts.customUsage.userId') }}</label><input id="custom-usage-user" v-model="draft.user_id" class="input" autocomplete="off" /></div>
          <div><label for="custom-usage-timeout" class="input-label">{{ t('admin.accounts.customUsage.timeout') }}</label><input id="custom-usage-timeout" v-model.number="draft.timeout_seconds" type="number" min="1" max="30" step="1" class="input" /></div>
          <div><label for="custom-usage-interval" class="input-label">{{ t('admin.accounts.customUsage.interval') }}</label><input id="custom-usage-interval" v-model.number="draft.interval_minutes" type="number" min="0" step="1" class="input" /></div>
        </div>
        <p class="input-hint">{{ t('admin.accounts.customUsage.intervalHint') }}</p>
        <section class="space-y-4 border-t border-gray-200 pt-4 dark:border-dark-600">
          <div><label for="custom-usage-url" class="input-label">{{ t('admin.accounts.customUsage.requestUrl') }}</label><div class="flex items-center gap-2"><span class="rounded bg-gray-100 px-2 py-2 text-xs font-medium text-gray-500 dark:bg-dark-700">GET</span><input id="custom-usage-url" v-model="draft.request.url" class="input font-mono text-sm" spellcheck="false" autocomplete="off" /></div></div>
          <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.accounts.customUsage.variables') }} <code v-for="variable in variables" :key="variable" class="mr-2 select-all">{{ variable }}</code></p>
          <div class="grid gap-4 md:grid-cols-2">
          <div><label for="custom-usage-headers" class="input-label">{{ t('admin.accounts.customUsage.headers') }}</label><textarea id="custom-usage-headers" v-model="headersText" rows="8" class="input font-mono text-xs" spellcheck="false" autocomplete="off" /></div>
          <div><label for="custom-usage-extractor" class="input-label">{{ t('admin.accounts.customUsage.extractor') }}</label><textarea id="custom-usage-extractor" v-model="extractorText" rows="8" class="input font-mono text-xs" spellcheck="false" aria-describedby="custom-usage-extractor-hint" /></div>
          </div>
          <p id="custom-usage-extractor-hint" class="input-hint">{{ t('admin.accounts.customUsage.extractorHint') }}</p>
        </section>
      </fieldset>
      <p v-if="validation" class="text-sm text-red-600 dark:text-red-400" role="alert">{{ t('admin.accounts.customUsage.errors.' + validation) }}</p>
      <p v-if="actionError" class="text-sm text-red-600 dark:text-red-400" role="alert">{{ t('admin.accounts.customUsage.' + actionError) }}</p>
      <div v-if="testResult" class="rounded-lg border border-gray-200 p-4 dark:border-dark-600" data-testid="custom-usage-test-result"><p class="mb-2 text-sm font-medium">{{ t('admin.accounts.customUsage.testResult') }}</p><CustomUsageResult :result="testResult" /></div>
    </form>
    <template #footer>
      <div class="flex w-full flex-wrap items-center justify-between gap-3">
        <div><button type="button" class="btn btn-secondary" :disabled="busy || loading || loadFailed || !draft.enabled" @click="test"><Icon v-if="testing" name="refresh" size="sm" class="mr-1 animate-spin motion-reduce:animate-none" />{{ t('admin.accounts.customUsage.test') }}</button><span class="ml-2 text-xs text-gray-400">{{ t('admin.accounts.customUsage.testHint') }}</span></div>
        <div class="flex gap-2"><button type="button" class="btn btn-secondary" :disabled="saving" @click="close">{{ t('common.cancel') }}</button><button type="submit" form="custom-usage-form" class="btn btn-primary" :disabled="busy || loading || loadFailed">{{ saving ? t('admin.accounts.customUsage.saving') : t('common.save') }}</button></div>
      </div>
    </template>
  </BaseDialog>
</template>
<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { AccountListItem } from '@/types'
import { getConfig, saveConfig, queryUsage, type CustomUsageConfig, type CustomUsageResult as UsageResult, type CustomUsageTemplate } from '@/api/admin/customUsage'
import { createUsageDraft, parseUsageEditors, usagePayload, usageTemplate, validateUsageDraft, type UsageValidationError } from '@/utils/customUsage'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Toggle from '@/components/common/Toggle.vue'
import Icon from '@/components/icons/Icon.vue'
import CustomUsageResult from './CustomUsageResult.vue'
const props = defineProps<{ show: boolean; account: AccountListItem | null }>()
const emit = defineEmits<{ close: []; saved: [id: number, config: Pick<CustomUsageConfig, 'enabled' | 'interval_minutes'>] }>()
const { t } = useI18n()
const templates: CustomUsageTemplate[] = ['general', 'newapi', 'sub2api', 'custom']
const variables = ['{{baseUrl}}', '{{apiKey}}', '{{accessToken}}', '{{userId}}']
const secretFields = [
  { name: 'api_key', has: 'has_api_key', clear: 'clear_api_key', label: 'apiKey' },
  { name: 'access_token', has: 'has_access_token', clear: 'clear_access_token', label: 'accessToken' }
] as const
const visibleSecretFields = computed(() => secretFields.filter(field => draft.value.template === 'custom' || field.name === (draft.value.template === 'newapi' ? 'access_token' : 'api_key')))
const draft = ref<CustomUsageConfig>(createUsageDraft())
const headersText = ref('')
const extractorText = ref('')
const loading = ref(false)
const loadFailed = ref(false)
const testing = ref(false)
const saving = ref(false)
const busy = computed(() => testing.value || saving.value)
const validation = ref<UsageValidationError | null>(null)
const actionError = ref<'queryFailed' | 'saveFailed' | null>(null)
const testResult = ref<UsageResult | null>(null)
let controller: AbortController | undefined
function reset() {
  controller?.abort()
  draft.value = createUsageDraft()
  headersText.value = ''; extractorText.value = ''
  validation.value = null; actionError.value = null; testResult.value = null
  testing.value = false; saving.value = false; loading.value = false; loadFailed.value = false
}
function syncEditors() {
  headersText.value = JSON.stringify(draft.value.request.headers, null, 2)
  extractorText.value = JSON.stringify(draft.value.extractor, null, 2)
}
function applyTemplate() {
  Object.assign(draft.value, usageTemplate(draft.value.template))
  syncEditors()
  validation.value = null; testResult.value = null
}
async function load() {
  reset()
  if (!props.show || !props.account) return
  loading.value = true
  const id = props.account.id
  const request = new AbortController(); controller = request
  try {
    const config = await getConfig(id, request.signal)
    if (request.signal.aborted) return
    const defaults = createUsageDraft(String(props.account.credentials?.base_url ?? ''))
    const emptyConfig = !config.configured && !config.request?.url && !Object.keys(config.extractor ?? {}).length
    draft.value = {
      ...defaults, ...(emptyConfig ? { has_api_key: config.has_api_key, has_access_token: config.has_access_token } : config),
      base_url: config.base_url || defaults.base_url,
      api_key: '', access_token: '', clear_api_key: false, clear_access_token: false
    }
    syncEditors()
  } catch { if (!request.signal.aborted) loadFailed.value = true }
  finally { if (!request.signal.aborted) loading.value = false }
}
function payload() {
  const parsed = parseUsageEditors(headersText.value, extractorText.value)
  validation.value = parsed.error ?? null
  if (parsed.error || !parsed.headers || !parsed.extractor) return null
  const result = usagePayload({ ...draft.value, request: { ...draft.value.request, headers: parsed.headers }, extractor: parsed.extractor })
  validation.value = validateUsageDraft(result)
  return validation.value ? null : result
}
async function test() {
  if (busy.value || !props.account || !draft.value.enabled) return
  const config = payload(); if (!config) return
  const request = new AbortController(); controller?.abort(); controller = request
  testing.value = true; actionError.value = null; testResult.value = null
  try {
    const result = await queryUsage(props.account.id, { force: true, config }, request.signal)
    if (!request.signal.aborted) testResult.value = result
  } catch { if (!request.signal.aborted) actionError.value = 'queryFailed' }
  finally { if (!request.signal.aborted) testing.value = false }
}
async function save() {
  if (busy.value || !props.account) return
  const config = payload(); if (!config) return
  const id = props.account.id
  const request = new AbortController(); controller?.abort(); controller = request
  saving.value = true; actionError.value = null
  try {
    await saveConfig(id, config, request.signal)
    if (request.signal.aborted) return
    emit('saved', id, { enabled: config.enabled, interval_minutes: config.interval_minutes })
    reset(); emit('close')
  } catch { if (!request.signal.aborted) actionError.value = 'saveFailed' }
  finally { if (!request.signal.aborted) saving.value = false }
}
function close() { if (!saving.value) { reset(); emit('close') } }
watch(() => [props.show, props.account?.id], () => { if (props.show) void load(); else reset() }, { immediate: true })
watch([draft, headersText, extractorText], () => { testResult.value = null; validation.value = null; actionError.value = null }, { deep: true })
onUnmounted(reset)
</script>
