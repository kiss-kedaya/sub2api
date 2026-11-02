<template>
  <section class="space-y-4">
    <UButton v-if="!single" variant="link" size="xs" icon="i-lucide-plus" class="p-0" :disabled="modelValue.length >= 5 || !encryptionConfigured" data-testid="account-ops-add-webhook" :label="t('accountOps.addRobot')" @click="add" />
    <p class="text-xs text-muted">{{ t('accountOps.robotHint') }}</p>
    <p v-if="!encryptionConfigured" role="alert" class="text-xs text-warning">{{ t('accountOps.encryptionMissing') }}</p>
    <div v-for="hook in modelValue" :key="hook.id" class="space-y-4">
      <UFormField :label="t('accountOps.channelName')" :help="t('accountOps.channelNameHint')">
        <UInput
          :model-value="hook.name ?? ''"
          maxlength="80"
          autocomplete="off"
          class="w-full"
          :placeholder="t('accountOps.channelNamePlaceholder')"
          :data-testid="`account-ops-webhook-name-${hook.id}`"
          @update:model-value="patch(hook.id, { name: String($event ?? '') })"
        />
      </UFormField>
      <div class="flex items-center justify-between gap-2">
        <UCheckbox
          :model-value="hook.enabled"
          :label="t('accountOps.channelEnabled')"
          :data-testid="`account-ops-webhook-enabled-${hook.id}`"
          @update:model-value="patch(hook.id, { enabled: $event === true })"
        />
        <UButton v-if="!single" color="error" variant="ghost" size="xs" :aria-label="t('accountOps.removeRobot')" :label="t('common.delete')" @click="remove(hook.id)" />
      </div>
      <UFormField :label="t('accountOps.webhookUrl')">
        <UInput
          v-model.trim="inputs[hook.id]!.url"
          type="password"
          autocomplete="new-password"
          spellcheck="false"
          class="w-full"
          :disabled="!encryptionConfigured"
          :placeholder="hook.url_configured ? t('accountOps.retainCredential') : t('accountOps.webhookPlaceholder')"
          :data-testid="`account-ops-webhook-url-${hook.id}`"
        />
      </UFormField>
      <p v-if="inputs[hook.id]!.url || hook.url_configured" class="text-xs text-muted">{{ t('accountOps.detectedWebhook', { provider: t(`accountOps.providers.${detectedProvider(hook)}`) }) }}</p>
      <UFormField v-if="supportsSigning(hook)" :label="t('accountOps.signingSecret')">
        <UInput
          v-model="inputs[hook.id]!.secret"
          type="password"
          autocomplete="new-password"
          spellcheck="false"
          class="w-full"
          :disabled="!encryptionConfigured || inputs[hook.id]!.clearSecret"
          :placeholder="hook.secret_configured && !inputs[hook.id]!.url ? t('accountOps.retainCredential') : t('accountOps.optionalSecret')"
          :data-testid="`account-ops-webhook-secret-${hook.id}`"
        />
      </UFormField>
      <UCheckbox
        v-if="hook.secret_configured && supportsSigning(hook) && !inputs[hook.id]!.url"
        v-model="inputs[hook.id]!.clearSecret"
        :label="t('accountOps.clearSecret')"
        :data-testid="`account-ops-webhook-clear-secret-${hook.id}`"
      />
      <UCollapsible v-if="detectedProvider(hook) === 'custom'" :default-open="!!hook.message_template" :unmount-on-hide="false">
        <UButton color="neutral" variant="link" class="p-0 font-medium" trailing-icon="i-lucide-chevron-down" :label="t('accountOps.webhookTemplate')" />
        <template #content>
          <div class="mt-3 space-y-3">
            <p class="text-xs leading-5 text-muted">{{ t('accountOps.webhookTemplateHint') }}</p>
            <UTextarea
              v-model="inputs[hook.id]!.template"
              :rows="5"
              maxlength="16384"
              spellcheck="false"
              class="w-full font-mono"
              :aria-label="t('accountOps.webhookTemplate')"
              :placeholder="defaultTemplate"
              :data-testid="`account-ops-webhook-template-${hook.id}`"
            />
            <p class="wrap-break-word font-mono text-xs text-muted">{{ templateVariables }}</p>
          </div>
        </template>
      </UCollapsible>
      <div class="flex items-center justify-between gap-2 text-xs text-muted">
        <span>{{ t(hook.url_configured ? 'accountOps.credentialSaved' : 'accountOps.saveBeforeTest') }}</span>
        <UButton color="neutral" variant="outline" size="xs" :disabled="!canTest(hook)" :data-testid="`account-ops-webhook-test-${hook.id}`" :label="t(testingId === hook.id ? 'common.loading' : 'accountOps.testRobot')" @click="emit('test', hook.id)" />
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
    if (hostname === 'api.day.app') return 'bark'
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
