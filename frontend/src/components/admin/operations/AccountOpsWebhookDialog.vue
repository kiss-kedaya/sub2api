<template>
  <BaseDialog :show="show" :title="t(hook ? 'accountOps.editRobot' : 'accountOps.addRobot')" width="normal" @close="close">
    <form id="account-ops-webhook-form" class="max-h-[68vh] space-y-3 overflow-y-auto pr-1" @submit.prevent="save"><fieldset :disabled="busy"><AccountOpsChannelFields ref="fields" v-model="draft" :encryption-configured="encryptionConfigured" single :testing-id="testingId" @test="emit('test', $event)" /></fieldset></form>
    <template #footer>
      <div class="flex justify-end gap-2">
        <UButton color="neutral" variant="subtle" :label="t('common.cancel')" @click="close" />
        <UButton type="submit" form="account-ops-webhook-form" :disabled="busy || !encryptionConfigured" :label="t(busy ? 'common.loading' : 'accountOps.saveChannel')" />
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { accountOpsMessages } from './accountOpsMessages'
import BaseDialog from '@/components/common/BaseDialog.vue'
import AccountOpsChannelFields from './AccountOpsChannelFields.vue'
import { saveAccountOpsWebhook } from '@/api/admin/accountOps'
import type { AccountOpsConfig, AccountOpsWebhook } from '@/api/admin/accountOps'
import { extractApiErrorMessage } from '@/utils/apiError'
import type { AccountOpsWebhookProvider } from '@/api/admin/accountOps'
const props = defineProps<{ show: boolean; hook: AccountOpsWebhook | null; initialProvider?: AccountOpsWebhookProvider; encryptionConfigured: boolean; testingId?: string | null }>()
const emit = defineEmits<{ (event: 'close'): void; (event: 'saved', config: AccountOpsConfig): void; (event: 'error', message: string): void; (event: 'test', id: string): void }>()
const { t } = useI18n({ useScope: 'local', messages: accountOpsMessages }), fields = ref<InstanceType<typeof AccountOpsChannelFields> | null>(null), draft = ref<AccountOpsWebhook[]>([]), busy = ref(false)
let sequence = 0
watch(() => [props.show, props.hook?.id] as const, () => {
  sequence++; fields.value?.clearInputs(); busy.value = false
  const id = props.hook?.id ?? Array.from(crypto.getRandomValues(new Uint8Array(16)), byte => byte.toString(16).padStart(2, '0')).join('')
  draft.value = [{ id, name: props.hook?.name ?? '', provider: props.hook?.provider ?? props.initialProvider ?? 'custom', enabled: props.hook?.enabled ?? true, url_configured: props.hook?.url_configured ?? false, secret_configured: props.hook?.secret_configured ?? false, message_template: props.hook?.message_template ?? '' }]
}, { immediate: true, flush: 'pre' })
const close = () => { sequence++; fields.value?.clearInputs(); busy.value = false; emit('close') }
onBeforeUnmount(() => { sequence++; fields.value?.clearInputs() })
async function save() {
  if (busy.value || !props.encryptionConfigured) return
  const input = fields.value?.prepare()[0]
  if (!input) return
  const { id, ...payload } = input, current = sequence
  busy.value = true
  try { const config = await saveAccountOpsWebhook(id, payload); if (current === sequence) { emit('saved', config); close() } }
  catch (e) { if (current === sequence) emit('error', extractApiErrorMessage(e, t('accountOps.saveFailed'))) }
  finally { if (current === sequence) busy.value = false }
}
</script>
