<template>
  <section class="space-y-4">
    <button v-if="!single" type="button" class="text-xs text-primary-600 disabled:opacity-50" :disabled="modelValue.length >= 5 || !encryptionConfigured" data-testid="account-ops-add-webhook" @click="add">{{ t('accountOps.addRobot') }}</button>
    <p class="text-xs leading-5 text-gray-500">{{ t('accountOps.robotHint') }}</p>
    <p v-if="!encryptionConfigured" role="alert" class="text-xs text-amber-700">{{ t('accountOps.encryptionMissing') }}</p>
    <div v-for="hook in modelValue" :key="hook.id" class="space-y-4">
      <label class="block space-y-2 text-sm">
        <span>{{ t('accountOps.channelName') }}</span>
        <input :value="hook.name ?? ''" type="text" maxlength="80" autocomplete="off" class="input w-full" :placeholder="t('accountOps.channelNamePlaceholder')" :data-testid="`account-ops-webhook-name-${hook.id}`" @input="patch(hook.id, { name: ($event.target as HTMLInputElement).value })" />
        <span class="block text-xs text-gray-500">{{ t('accountOps.channelNameHint') }}</span>
      </label>
      <div class="flex items-center justify-between gap-2">
        <label class="flex items-center gap-2 text-sm">
          <input type="checkbox" :checked="hook.enabled" :data-testid="`account-ops-webhook-enabled-${hook.id}`" @change="patch(hook.id, { enabled: ($event.target as HTMLInputElement).checked })" />
          {{ t('accountOps.channelEnabled') }}
        </label>
        <button v-if="!single" type="button" class="text-xs text-red-600" :aria-label="t('accountOps.removeRobot')" @click="remove(hook.id)">{{ t('common.delete') }}</button>
      </div>
      <label class="block space-y-2 text-sm">
        <span>{{ t('accountOps.webhookUrl') }}</span>
        <input v-model.trim="inputs[hook.id]!.url" type="password" autocomplete="new-password" spellcheck="false" class="input w-full" :disabled="!encryptionConfigured" :placeholder="hook.url_configured ? t('accountOps.retainCredential') : t('accountOps.webhookPlaceholder')" :data-testid="`account-ops-webhook-url-${hook.id}`" />
      </label>
      <p v-if="inputs[hook.id]!.url || hook.url_configured" class="text-xs text-gray-500">{{ t('accountOps.detectedWebhook', { provider: t(`accountOps.providers.${detectedProvider(hook)}`) }) }}</p>
      <label v-if="supportsSigning(hook)" class="block space-y-2 text-sm">
        <span>{{ t('accountOps.signingSecret') }}</span>
        <input v-model="inputs[hook.id]!.secret" type="password" autocomplete="new-password" spellcheck="false" class="input w-full" :disabled="!encryptionConfigured || inputs[hook.id]!.clearSecret" :placeholder="hook.secret_configured && !inputs[hook.id]!.url ? t('accountOps.retainCredential') : t('accountOps.optionalSecret')" :data-testid="`account-ops-webhook-secret-${hook.id}`" />
      </label>
      <label v-if="hook.secret_configured && supportsSigning(hook) && !inputs[hook.id]!.url" class="flex items-center gap-2 text-xs text-gray-500">
        <input v-model="inputs[hook.id]!.clearSecret" type="checkbox" :data-testid="`account-ops-webhook-clear-secret-${hook.id}`" />{{ t('accountOps.clearSecret') }}
      </label>
      <details v-if="detectedProvider(hook) === 'custom'" class="rounded-lg border border-gray-200 p-3 dark:border-dark-600" :open="!!hook.message_template">
        <summary class="cursor-pointer text-sm font-medium">{{ t('accountOps.webhookTemplate') }}</summary>
        <div class="mt-3 space-y-3">
          <p class="text-xs leading-5 text-gray-500">{{ t('accountOps.webhookTemplateHint') }}</p>
          <textarea v-model="inputs[hook.id]!.template" rows="5" maxlength="16384" spellcheck="false" class="input w-full font-mono text-xs" :aria-label="t('accountOps.webhookTemplate')" :placeholder="defaultTemplate" :data-testid="`account-ops-webhook-template-${hook.id}`" />
          <p class="break-words font-mono text-xs text-gray-500">{{ templateVariables }}</p>
        </div>
      </details>
      <div class="flex items-center justify-between gap-2 text-xs text-gray-500">
        <span>{{ t(hook.url_configured ? 'accountOps.credentialSaved' : 'accountOps.saveBeforeTest') }}</span>
        <button type="button" class="text-primary-600 disabled:opacity-40" :disabled="!canTest(hook)" :data-testid="`account-ops-webhook-test-${hook.id}`" @click="emit('test', hook.id)">{{ t(testingId === hook.id ? 'common.loading' : 'accountOps.testRobot') }}</button>
      </div>
    </div>
  </section>
</template>
<script setup lang="ts">
import { computed, onBeforeUnmount, reactive, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { accountOpsMessages } from './accountOpsMessages'
import type { AccountOpsWebhook, AccountOpsWebhookInput, AccountOpsWebhookProvider } from '@/api/admin/accountOps'
const props = defineProps<{ modelValue: AccountOpsWebhook[]; encryptionConfigured: boolean; testingId?: string | null; single?: boolean }>()
const emit = defineEmits<{ (event: 'update:modelValue', value: AccountOpsWebhook[]): void; (event: 'test', id: string): void }>()
const { t } = useI18n({ useScope: 'local', messages: accountOpsMessages })
const defaultTemplate = '{"text":"{{message}}"}'
const templateVariables = '{{message}} · {{title}} · {{account}} · {{balance}} · {{threshold}} · {{time}}'
const inputs = reactive<Record<string, { url: string; secret: string; clearSecret: boolean; template: string }>>({})
watch(() => props.modelValue.map(h => h.id), ids => {
  for (const hook of props.modelValue) inputs[hook.id] ??= { url: '', secret: '', clearSecret: false, template: hook.message_template ?? '' }
  for (const id of Object.keys(inputs)) if (!ids.includes(id)) delete inputs[id]
}, { immediate: true, flush: 'sync' })
const hasSensitiveChanges = computed(() => props.modelValue.some(h => {
  const i = inputs[h.id]
  return !!i && (!!i.url || !!i.secret || i.clearSecret || i.template !== (h.message_template ?? ''))
}))
function detectedProvider(hook: AccountOpsWebhook): AccountOpsWebhookProvider {
  const raw = inputs[hook.id]?.url
  if (!raw) return hook.provider
  try {
    const hostname = new URL(raw).hostname.toLowerCase()
    if (hostname === 'qyapi.weixin.qq.com') return 'wecom'
    if (hostname === 'oapi.dingtalk.com') return 'dingtalk'
    if (hostname === 'open.feishu.cn') return 'feishu'
  } catch { /* Server validation reports invalid addresses on save. */ }
  return 'custom'
}
const supportsSigning = (h: AccountOpsWebhook) => ['dingtalk', 'feishu'].includes(detectedProvider(h))
const patch = (id: string, values: Partial<AccountOpsWebhook>) => emit('update:modelValue', props.modelValue.map(h => h.id === id ? { ...h, ...values } : h))
const add = () => {
  if (props.modelValue.length >= 5 || !props.encryptionConfigured) return
  const id = Array.from(crypto.getRandomValues(new Uint8Array(16)), byte => byte.toString(16).padStart(2, '0')).join('')
  emit('update:modelValue', [...props.modelValue, { id, provider: 'custom', enabled: true, url_configured: false, secret_configured: false }])
}
const remove = (id: string) => { delete inputs[id]; emit('update:modelValue', props.modelValue.filter(h => h.id !== id)) }
const canTest = (h: AccountOpsWebhook) => {
  const i = inputs[h.id]
  return !!h.url_configured && !props.testingId && !i?.url && !i?.secret && !i?.clearSecret && (i?.template ?? '') === (h.message_template ?? '')
}
const prepare = (): AccountOpsWebhookInput[] => props.modelValue.map(h => {
  const i = inputs[h.id]
  const custom = detectedProvider(h) === 'custom'
  return {
    id: h.id, provider: 'auto', enabled: h.enabled,
    ...(h.name !== undefined ? { name: h.name.trim() } : {}),
    ...(i?.url ? { url: i.url } : {}),
    ...(supportsSigning(h) && i?.secret && !i.clearSecret ? { secret: i.secret } : {}),
    ...(supportsSigning(h) && i?.clearSecret ? { clear_secret: true } : {}),
    ...(custom && i?.template !== (h.message_template ?? '') ? { message_template: i?.template ?? '' } : {}),
    ...(!custom && h.message_template ? { message_template: '' } : {})
  }
})
const clearInputs = () => { for (const h of props.modelValue) inputs[h.id] = { url: '', secret: '', clearSecret: false, template: h.message_template ?? '' } }
onBeforeUnmount(clearInputs)
defineExpose({ prepare, clearInputs, hasSensitiveChanges })
</script>
