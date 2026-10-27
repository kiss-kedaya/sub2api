<template>
  <BaseDialog :show="show" :title="t('accountOps.addChannel')" width="normal" @close="emit('close')">
    <div class="grid gap-3 sm:grid-cols-2">
      <button type="button" :disabled="emailConfigured" class="rounded-lg border border-gray-200 p-4 text-left hover:border-primary-500 focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary-500 disabled:cursor-not-allowed disabled:opacity-50 dark:border-dark-600" data-testid="add-channel-email" @click="emit('select', 'email')">
        <span class="text-sm font-medium">{{ t('accountOps.providers.email') }}</span>
        <span class="mt-1 block text-xs text-gray-500">{{ t(emailConfigured ? 'accountOps.channelAlreadyAdded' : 'accountOps.emailChannelHint') }}</span>
      </button>
      <button type="button" :disabled="!encryptionConfigured || webhookCount >= 5" class="rounded-lg border border-gray-200 p-4 text-left hover:border-primary-500 focus-visible:outline focus-visible:outline-2 focus-visible:outline-primary-500 disabled:cursor-not-allowed disabled:opacity-50 dark:border-dark-600" data-testid="add-channel-webhook" @click="emit('select', 'webhook')">
        <span class="text-sm font-medium">Webhook</span>
        <span class="mt-1 block text-xs text-gray-500">{{ t('accountOps.robotChannelHint') }}</span>
      </button>
    </div>
    <p v-if="webhookCount >= 5" class="mt-4 text-xs text-gray-500">{{ t('accountOps.robotLimitReached') }}</p>
    <p v-else-if="!encryptionConfigured" class="mt-4 text-xs text-amber-700">{{ t('accountOps.encryptionMissing') }}</p>
    <template #footer><button type="button" class="btn btn-secondary" @click="emit('close')">{{ t('common.cancel') }}</button></template>
  </BaseDialog>
</template>
<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { accountOpsMessages } from './accountOpsMessages'
import BaseDialog from '@/components/common/BaseDialog.vue'

defineProps<{ show: boolean; emailConfigured: boolean; webhookCount: number; encryptionConfigured: boolean }>()
const emit = defineEmits<{ (event: 'close'): void; (event: 'select', provider: 'email' | 'webhook'): void }>()
const { t } = useI18n({ useScope: 'local', messages: accountOpsMessages })
</script>
