<template>
  <BaseDialog :show="show" :title="t(config.recipient ? 'accountOps.editEmail' : 'accountOps.addEmail')" width="normal" @close="close">
    <form id="account-ops-email-form" class="space-y-4" @submit.prevent="save">
      <fieldset :disabled="busy" class="space-y-4">
        <UFormField :label="t('accountOps.channelName')" :help="t('accountOps.channelNameHint')">
          <UInput id="account-ops-email-name" v-model.trim="name" maxlength="80" autocomplete="off" :placeholder="t('accountOps.channelNamePlaceholder')" class="w-full" />
        </UFormField>
        <UFormField :label="t('accountOps.recipient')" :help="t('accountOps.recipientHint')" required>
          <UInput id="account-ops-email" v-model.trim="recipient" type="email" maxlength="254" required autocomplete="email" placeholder="ops@example.com" class="w-full" />
        </UFormField>
        <div v-if="recipient" class="flex items-center justify-between gap-2 text-xs text-muted">
          <span>{{ t(smtpConfigured ? 'accountOps.smtpReady' : 'accountOps.smtpMissing') }}</span>
          <a href="/admin/settings" class="text-primary hover:underline">{{ t('accountOps.mailSettings') }}</a>
        </div>
      </fieldset>
    </form>
    <template #footer>
      <div class="flex justify-end gap-2">
        <UButton color="neutral" variant="subtle" :label="t('common.cancel')" @click="close" />
        <UButton type="submit" form="account-ops-email-form" :disabled="busy" :label="t(busy ? 'common.loading' : 'accountOps.saveChannel')" />
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { accountOpsMessages } from './accountOpsMessages'
import BaseDialog from '@/components/common/BaseDialog.vue'
import { saveAccountOpsNotificationSettings } from '@/api/admin/accountOps'
import type { AccountOpsConfig } from '@/api/admin/accountOps'
import { extractApiErrorMessage } from '@/utils/apiError'

const props = defineProps<{ show: boolean; config: AccountOpsConfig; smtpConfigured: boolean }>()
const emit = defineEmits<{ (event: 'close'): void; (event: 'saved', config: AccountOpsConfig): void; (event: 'error', message: string): void }>()
const { t } = useI18n({ useScope: 'local', messages: accountOpsMessages })
const busy = ref(false), recipient = ref(''), name = ref('')
let sequence = 0
watch(() => props.show, () => {
  sequence++
  busy.value = false
  recipient.value = props.show ? props.config.recipient : ''
  name.value = props.show ? props.config.email_name ?? '' : ''
}, { immediate: true })
const close = () => { sequence++; busy.value = false; recipient.value = ''; name.value = ''; emit('close') }
onBeforeUnmount(() => { sequence++; recipient.value = '' })
async function save() {
  if (busy.value) return
  if (!recipient.value) { emit('error', t('accountOps.emailRequired')); return }
  const current = sequence
  busy.value = true
  try {
    const config = await saveAccountOpsNotificationSettings({ recipient: recipient.value, email_name: name.value })
    if (current === sequence) { emit('saved', config); close() }
  } catch (e) {
    if (current === sequence) emit('error', extractApiErrorMessage(e, t('accountOps.saveFailed')))
  } finally {
    if (current === sequence) busy.value = false
  }
}
</script>
