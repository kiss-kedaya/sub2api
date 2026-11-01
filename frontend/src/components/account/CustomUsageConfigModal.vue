<template>
  <BaseDialog :show="show" :title="t('admin.accounts.customUsage.title')" width="wide" :close-on-escape="!saving" :show-close-button="!saving" @close="close">
    <div v-if="loading" class="flex items-center justify-center gap-2 py-12 text-sm text-muted" role="status"><UIcon name="i-lucide-loader-circle" class="size-4 animate-spin motion-reduce:animate-none" />{{ t('admin.accounts.customUsage.loading') }}</div>
    <div v-else-if="loadFailed" class="space-y-3 py-6 text-sm" role="alert">
      <p>{{ t('admin.accounts.customUsage.loadFailed') }}</p>
      <UButton color="neutral" variant="outline" :label="t('admin.accounts.customUsage.retry')" @click="load" />
    </div>
    <form v-else id="custom-usage-form" class="space-y-5" @submit.prevent="save">
      <div class="flex items-center justify-between gap-4">
        <div><h4 class="text-sm font-medium text-highlighted">{{ t('admin.accounts.customUsage.enabled') }}</h4><p class="mt-0.5 text-xs text-muted">{{ account?.name }}</p></div>
        <Toggle v-model="draft.enabled" :disabled="busy" :aria-label="t('admin.accounts.customUsage.enabled')" />
      </div>
      <fieldset :disabled="busy" class="space-y-5">
        <UFormField :label="t('admin.accounts.customUsage.template')" :help="t('admin.accounts.customUsage.templateHint')">
          <div class="flex flex-wrap gap-2" role="group" :aria-label="t('admin.accounts.customUsage.template')">
            <UButton
              v-for="template in templates"
              :key="template"
              color="neutral"
              :variant="draft.template === template ? 'soft' : 'outline'"
              :data-template="template"
              :aria-pressed="draft.template === template"
              :title="t('admin.accounts.customUsage.templateHint')"
              :label="t('admin.accounts.customUsage.templates.' + template)"
              @click="draft.template = template; applyTemplate()"
            />
          </div>
        </UFormField>
        <div class="grid gap-4 md:grid-cols-2">
          <UFormField :label="t('admin.accounts.customUsage.baseUrl')">
            <UInput id="custom-usage-base" v-model="draft.base_url" type="url" class="w-full" placeholder="https://api.example.com" autocomplete="off" />
          </UFormField>
          <UFormField v-for="field in visibleSecretFields" :key="field.name" :label="t('admin.accounts.customUsage.' + field.label)">
            <UInput :id="'custom-usage-' + field.name" v-model="draft[field.name]" type="password" class="w-full font-mono" autocomplete="new-password" spellcheck="false" data-1p-ignore data-lpignore="true" :disabled="draft[field.clear]" :placeholder="draft[field.has] ? t('admin.accounts.customUsage.secretSaved') : t('admin.accounts.customUsage.secretPlaceholder')" />
            <UCheckbox v-if="draft[field.has]" v-model="draft[field.clear]" class="mt-2" :label="t('admin.accounts.customUsage.clearSecret')" @update:model-value="draft[field.name] = ''" />
          </UFormField>
        </div>
        <div v-if="credentialMode || visibleSecretFields.length" class="flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-muted">
          <span class="font-medium text-default">{{ t('admin.accounts.customUsage.credential') }}</span>
          <UBadge v-if="credentialMode" color="success" variant="subtle" size="sm">{{ credentialMode }}</UBadge>
          <span v-if="visibleSecretFields.length" class="text-dimmed">{{ t('admin.accounts.customUsage.credentialOverride') }}</span>
        </div>
        <div :class="draft.template === 'newapi' || draft.template === 'custom' ? 'sm:grid-cols-3' : 'sm:grid-cols-2'" class="grid gap-4">
          <UFormField v-if="draft.template === 'newapi' || draft.template === 'custom'" :label="t('admin.accounts.customUsage.userId')"><UInput id="custom-usage-user" v-model="draft.user_id" class="w-full" autocomplete="off" /></UFormField>
          <UFormField :label="t('admin.accounts.customUsage.timeout')"><UInput id="custom-usage-timeout" v-model.number="draft.timeout_seconds" type="number" min="1" max="30" step="1" class="w-full" /></UFormField>
          <UFormField :label="t('admin.accounts.customUsage.interval')" :help="t('admin.accounts.customUsage.intervalDefault')"><UInput id="custom-usage-interval" v-model.number="draft.interval_minutes" type="number" min="0" step="1" class="w-full" /></UFormField>
        </div>
        <USeparator />
        <UFormField :label="t('admin.accounts.customUsage.requestUrl')">
          <UInput id="custom-usage-url" v-model="draft.request.url" class="w-full font-mono" spellcheck="false" autocomplete="off">
            <template #leading><span class="text-xs font-medium text-muted">GET</span></template>
          </UInput>
          <template #help>{{ t('admin.accounts.customUsage.variables') }} <code v-for="variable in variables" :key="variable" class="mr-2 select-all">{{ variable }}</code></template>
        </UFormField>
        <div class="grid gap-4 md:grid-cols-2">
          <UFormField :label="t('admin.accounts.customUsage.headers')"><UTextarea id="custom-usage-headers" v-model="headersText" :rows="8" class="w-full font-mono" spellcheck="false" autocomplete="off" /></UFormField>
          <UFormField :label="t('admin.accounts.customUsage.extractor')"><UTextarea id="custom-usage-extractor" v-model="extractorText" :rows="8" class="w-full font-mono" spellcheck="false" aria-describedby="custom-usage-extractor-hint" /></UFormField>
        </div>
        <p id="custom-usage-extractor-hint" class="text-xs text-muted">{{ t('admin.accounts.customUsage.extractorHint') }}</p>
      </fieldset>
      <p v-if="validation" class="text-sm text-error" role="alert">{{ t('admin.accounts.customUsage.errors.' + validation) }}</p>
      <p v-if="actionError" class="text-sm text-error" role="alert">{{ t('admin.accounts.customUsage.' + actionError) }}</p>
      <div v-if="testResult" class="rounded-md bg-muted p-4" data-testid="custom-usage-test-result"><p class="mb-2 text-sm font-medium text-highlighted">{{ t('admin.accounts.customUsage.testResult') }}</p><CustomUsageResult :result="testResult" /></div>
    </form>
    <template #footer>
      <div class="flex w-full flex-wrap items-center justify-between gap-3">
        <div class="flex items-center gap-2">
          <UButton color="neutral" variant="outline" :disabled="busy || loading || loadFailed || !draft.enabled" @click="test">
            <UIcon v-if="testing" name="i-lucide-loader-circle" class="size-4 animate-spin motion-reduce:animate-none" />{{ t('admin.accounts.customUsage.test') }}
          </UButton>
          <span class="text-xs text-dimmed">{{ t('admin.accounts.customUsage.testHint') }}</span>
        </div>
        <div class="flex gap-2">
          <UButton color="neutral" variant="subtle" :disabled="saving" :label="t('common.cancel')" @click="close" />
          <UButton type="submit" form="custom-usage-form" :disabled="busy || loading || loadFailed" :label="saving ? t('admin.accounts.customUsage.saving') : t('common.save')" />
        </div>
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
const visibleSecretFields = computed(() => secretFields.filter(field => {
  if (draft.value.template === 'custom') return true
  if (draft.value.template === 'newapi') return field.name === 'access_token' && (!draft.value.uses_account_access_token || draft.value.has_access_token)
  return field.name === 'api_key' && (!draft.value.uses_account_api_key || draft.value.has_api_key)
}))
const credentialMode = computed(() => {
  if (draft.value.template === 'newapi' && draft.value.uses_account_access_token) return t('admin.accounts.customUsage.followAccountCredential')
  if (draft.value.template !== 'newapi' && draft.value.template !== 'custom' && draft.value.uses_account_api_key) return t('admin.accounts.customUsage.followAccountCredential')
  return null
})
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
