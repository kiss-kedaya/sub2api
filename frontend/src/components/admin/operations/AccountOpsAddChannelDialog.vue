<template>
  <BaseDialog :show="show" :title="t('accountOps.addChannel')" width="normal" @close="emit('close')">
    <div class="grid gap-2 sm:grid-cols-3">
      <UButton
        color="neutral"
        variant="outline"
        class="flex-col items-start gap-1 p-3 text-left"
        :disabled="emailConfigured"
        data-testid="add-channel-email"
        @click="emit('select', 'email')"
      >
        <span class="text-sm font-medium text-highlighted">{{ t('accountOps.providers.email') }}</span>
        <span class="block text-xs font-normal text-muted">{{ t(emailConfigured ? 'accountOps.channelAlreadyAdded' : 'accountOps.emailChannelHint') }}</span>
      </UButton>
      <UButton
        color="neutral"
        variant="outline"
        class="flex-col items-start gap-1 p-3 text-left"
        :disabled="!encryptionConfigured || webhookCount >= 5"
        data-testid="add-channel-webhook"
        @click="emit('select', 'webhook')"
      >
        <span class="text-sm font-medium text-highlighted">Webhook</span>
        <span class="block text-xs font-normal text-muted">{{ t('accountOps.robotChannelHint') }}</span>
      </UButton>
      <UButton
        color="neutral"
        variant="outline"
        class="flex-col items-start gap-1 p-3 text-left"
        :disabled="!encryptionConfigured || webhookCount >= 5"
        data-testid="add-channel-bark"
        @click="emit('select', 'bark')"
      >
        <span class="text-sm font-medium text-highlighted">Bark</span>
        <span class="block text-xs font-normal text-muted">{{ t('accountOps.barkChannelHint') }}</span>
      </UButton>
    </div>
    <p v-if="webhookCount >= 5" class="mt-4 text-xs text-muted">{{ t('accountOps.robotLimitReached') }}</p>
    <p v-else-if="!encryptionConfigured" class="mt-4 text-xs text-warning">{{ t('accountOps.encryptionMissing') }}</p>
    <template #footer>
      <div class="flex justify-end">
        <UButton color="neutral" variant="subtle" :label="t('common.cancel')" @click="emit('close')" />
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { accountOpsMessages } from './accountOpsMessages'
import BaseDialog from '@/components/common/BaseDialog.vue'

defineProps<{ show: boolean; emailConfigured: boolean; webhookCount: number; encryptionConfigured: boolean }>()
const emit = defineEmits<{ (event: 'close'): void; (event: 'select', provider: 'email' | 'webhook' | 'bark'): void }>()
const { t } = useI18n({ useScope: 'local', messages: accountOpsMessages })
</script>
