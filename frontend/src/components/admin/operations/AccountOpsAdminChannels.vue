<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import Toggle from '@/components/common/Toggle.vue'
import type { AccountOpsAdminDefaults, AccountOpsConfig } from '@/api/admin/accountOps'
import { accountOpsMessages } from './accountOpsMessages'

const props = defineProps<{
  config: AccountOpsConfig
  status?: AccountOpsAdminDefaults
  smtpConfigured: boolean
  busy: boolean
  testingId: string | null
}>()
const emit = defineEmits<{
  toggle: [channel: 'email' | 'bark', enabled: boolean]
  test: [channel: 'email' | 'bark']
}>()
const { t } = useI18n({ useScope: 'local', messages: accountOpsMessages })
const reason = (value?: string) => t(`accountOps.adminReasons.${value || 'unavailable'}`)
</script>

<template>
  <section class="space-y-3" data-testid="account-ops-admin-defaults">
    <div>
      <h2 class="text-base font-semibold text-highlighted">{{ t('accountOps.adminDefaults') }}</h2>
      <p class="mt-1 text-sm text-muted">{{ t('accountOps.adminDefaultsHint') }}</p>
    </div>
    <UPageCard variant="subtle" :ui="{ container: 'p-0 sm:p-0 gap-y-0' }">
      <ul class="divide-y divide-default">
        <li class="flex flex-wrap items-center justify-between gap-3 px-4 py-3" data-testid="admin-email-row">
          <div class="min-w-0">
            <p id="admin-email-label" class="text-sm font-medium text-highlighted">{{ t('accountOps.adminEmail') }}</p>
            <p class="mt-0.5 break-all text-xs text-muted">{{ t('accountOps.adminProfileSource') }} · {{ status?.email_masked || reason(status?.email_unavailable_reason) }}</p>
            <p v-if="!smtpConfigured" class="mt-1 text-xs text-warning">{{ t('accountOps.smtpMissing') }}</p>
          </div>
          <div class="flex items-center gap-3">
            <Toggle :model-value="config.use_admin_email === true" aria-labelledby="admin-email-label" :disabled="busy" data-testid="use-admin-email" @update:model-value="emit('toggle', 'email', $event)" />
            <UButton color="neutral" variant="ghost" size="xs" icon="i-lucide-send" :disabled="busy || !!testingId || !status?.email_available || !smtpConfigured"
              data-testid="test-admin-email" :label="t(testingId === 'admin-email' ? 'common.loading' : 'accountOps.testRobot')" @click="emit('test', 'email')" />
          </div>
        </li>
        <li class="flex flex-wrap items-center justify-between gap-3 px-4 py-3" data-testid="admin-bark-row">
          <div class="min-w-0">
            <p id="admin-bark-label" class="text-sm font-medium text-highlighted">{{ t('accountOps.adminBark') }}</p>
            <p class="mt-0.5 text-xs text-muted">{{ t('accountOps.adminProfileSource') }} · {{ status?.bark_available ? t('accountOps.channelConfigured') : reason(status?.bark_unavailable_reason) }}</p>
            <p v-if="!config.use_admin_bark" class="mt-1 text-xs text-muted">{{ t('accountOps.channelUnused') }}</p>
            <UButton v-if="!status?.bark_available" color="neutral" variant="link" size="xs" class="px-0" to="/profile?tab=notifications" data-testid="configure-admin-bark" :label="t('accountOps.configureInProfile')" />
          </div>
          <div class="flex items-center gap-3">
            <Toggle :model-value="config.use_admin_bark === true" aria-labelledby="admin-bark-label" :disabled="busy || (!config.use_admin_bark && (config.webhooks?.length ?? 0) >= 5)"
              data-testid="use-admin-bark" @update:model-value="emit('toggle', 'bark', $event)" />
            <UButton color="neutral" variant="ghost" size="xs" icon="i-lucide-send" :disabled="busy || !!testingId || !status?.bark_available"
              data-testid="test-admin-bark" :label="t(testingId === 'admin-bark' ? 'common.loading' : 'accountOps.testRobot')" @click="emit('test', 'bark')" />
          </div>
        </li>
      </ul>
    </UPageCard>
    <p v-if="props.config.use_admin_email && props.config.recipient" class="text-xs text-muted">{{ t('accountOps.adminEmailOverridesCustom') }}</p>
    <p v-if="props.config.use_admin_bark && props.config.webhooks?.length" class="text-xs text-muted">{{ t('accountOps.adminBarkDuplicateHint') }}</p>
  </section>
</template>
