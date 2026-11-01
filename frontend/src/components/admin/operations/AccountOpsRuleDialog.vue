<template>
  <BaseDialog :show="show" :title="t('accountOps.editRule')" width="normal" @close="close">
    <form v-if="account" id="account-ops-rule-form" class="space-y-5" @submit.prevent="save">
      <div>
        <p class="font-medium text-highlighted">{{ account.account_name }}</p>
        <p class="mt-1 text-xs text-muted">{{ account.platform }} · {{ account.type === 'apikey' ? 'API Key' : 'OAuth' }} · #{{ account.account_id }}</p>
      </div>
      <fieldset :disabled="busy" class="space-y-5">
        <USwitch v-model="enabled" :label="t('accountOps.ruleEnabled')" data-testid="rule-enabled" />
        <UFormField :label="t('accountOps.thresholdValue')">
          <UInput
            :model-value="String(threshold)"
            type="number"
            @update:model-value="threshold = $event as string | number"
            :min="isQuota ? 0.01 : 0"
            :max="isQuota ? 100 : 1e12"
            step="any"
            class="w-full"
            data-testid="rule-threshold"
          >
            <template #trailing><span class="text-xs text-muted">{{ isQuota ? '%' : unit }}</span></template>
          </UInput>
        </UFormField>
        <div v-if="!isQuota && unit !== account.unit" class="space-y-1 text-xs text-warning">
          <p>{{ t('accountOps.unitMismatchHint', { saved: unit, current: account.unit }) }}</p>
          <UButton color="warning" variant="link" size="xs" class="p-0" data-testid="rule-use-current-unit" :label="t('accountOps.useCurrentUnit', { unit: account.unit })" @click="unit = account.unit; threshold = ''" />
        </div>
        <UFormField v-if="isQuota" :label="t('accountOps.quotaWindow')">
          <USelect v-model="windowId" :items="windowItems" class="w-full" data-testid="rule-window" />
        </UFormField>
        <div class="space-y-3">
          <p class="text-sm font-medium text-highlighted">{{ t('accountOps.transitionNotices') }}</p>
          <UCheckbox v-model="notifyAlert" :label="t('accountOps.notifyAlert')" :description="t('accountOps.notifyAlertHint')" data-testid="rule-notify-alert" />
          <UCheckbox v-model="notifyRecovery" :label="t('accountOps.notifyRecovery')" :description="t('accountOps.notifyRecoveryHint')" data-testid="rule-notify-recovery" />
        </div>
        <p class="text-xs leading-5 text-muted">{{ t(isQuota ? 'accountOps.quotaOnceHint' : 'accountOps.balanceOnceHint') }}</p>
        <UCollapsible :unmount-on-hide="false" class="text-xs text-muted">
          <UButton color="neutral" variant="link" size="xs" class="p-0" trailing-icon="i-lucide-chevron-down" :label="t('accountOps.sampleDetails')" />
          <template #content>
            <div class="mt-3 space-y-2">
              <p v-if="!isQuota">{{ t('accountOps.currentValue') }}: {{ account.balance_status === 'ok' && account.balance != null ? `${account.balance.toFixed(2)} ${account.unit}` : t(`accountOps.observations.${account.balance_status}`) }}</p>
              <p v-if="account.received_at">{{ t('accountOps.sampleTime') }} {{ date(account.received_at) }}</p>
              <div v-for="window in account.usage_windows" :key="window.window">
                <p>{{ window.label }}: {{ window.status === 'ok' && window.used_percent != null ? `${window.used_percent.toFixed(1)}%` : t(`accountOps.observations.${window.status}`) }}</p>
                <p v-if="window.observed_at">{{ t('accountOps.sampleTime') }} {{ date(window.observed_at) }}</p>
                <p v-if="window.resets_at">{{ t('accountOps.resetTime') }} {{ date(window.resets_at) }}</p>
              </div>
            </div>
          </template>
        </UCollapsible>
      </fieldset>
    </form>
    <template #footer>
      <div class="flex justify-end gap-2">
        <UButton color="neutral" variant="subtle" :label="t('common.cancel')" @click="close" />
        <UButton type="submit" form="account-ops-rule-form" :disabled="busy" :label="t(busy ? 'common.loading' : 'accountOps.saveRule')" />
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { accountOpsMessages } from './accountOpsMessages'
import BaseDialog from '@/components/common/BaseDialog.vue'
import { saveAccountOpsRule } from '@/api/admin/accountOps'
import type { AccountOpsBalanceThreshold, AccountOpsQuotaThreshold, AccountOpsThresholdAccount, AccountOpsConfig, AccountOpsRuleInput } from '@/api/admin/accountOps'
import { extractApiErrorMessage } from '@/utils/apiError'
const props = defineProps<{ show: boolean; account: AccountOpsThresholdAccount | null; balanceRule: AccountOpsBalanceThreshold | null; quotaRule: AccountOpsQuotaThreshold | null }>()
const emit = defineEmits<{ (event: 'close'): void; (event: 'saved', config: AccountOpsConfig): void; (event: 'error', message: string): void }>()
const { t } = useI18n({ useScope: 'local', messages: accountOpsMessages })
const enabled = ref(true), threshold = ref<string | number>(''), windowId = ref('any'), unit = ref('USD'), notifyAlert = ref(true), notifyRecovery = ref(true), busy = ref(false)
const isQuota = computed(() => props.account?.type === 'oauth')
// Window choices: any window, the account's windows, and a saved window that no longer exists.
const windowItems = computed(() => [
  { value: 'any', label: t('accountOps.anyWindow') },
  ...(props.account?.usage_windows ?? []).map(window => ({ value: window.window, label: window.label })),
  ...(windowId.value !== 'any' && !(props.account?.usage_windows ?? []).some(window => window.window === windowId.value) ? [{ value: windowId.value, label: windowId.value }] : [])
])
let sequence = 0
watch(() => [props.show, props.account?.account_id] as const, () => {
  sequence++; busy.value = false
  const rule = isQuota.value ? props.quotaRule : props.balanceRule
  enabled.value = rule?.enabled ?? true
  unit.value = props.balanceRule?.unit ?? props.account?.unit ?? 'USD'
  threshold.value = (isQuota.value ? props.quotaRule?.threshold_percent : props.balanceRule?.threshold) ?? ''
  windowId.value = props.quotaRule?.window || 'any'; notifyAlert.value = rule?.notify_alert ?? true; notifyRecovery.value = rule?.notify_recovery ?? true
}, { immediate: true, flush: 'pre' })
const close = () => { sequence++; busy.value = false; emit('close') }
onBeforeUnmount(() => { sequence++ })
const date = (s: string) => { const d = new Date(s); return Number.isFinite(d.getTime()) ? d.toLocaleString() : '-' }
async function save() {
  if (!props.account || busy.value) return
  const value = Number(threshold.value)
  if (String(threshold.value).trim() === '' || !Number.isFinite(value) || value < (isQuota.value ? Number.MIN_VALUE : 0) || value > (isQuota.value ? 100 : 1e12)) { emit('error', t('accountOps.invalidThreshold')); return }
  const request: AccountOpsRuleInput = { metric: isQuota.value ? 'quota' : 'balance', enabled: enabled.value, notify_alert: notifyAlert.value, notify_recovery: notifyRecovery.value, ...(isQuota.value ? { threshold_percent: value, window: windowId.value } : { threshold: value, unit: unit.value }) }
  const current = sequence; busy.value = true
  try { const config = await saveAccountOpsRule(props.account.account_id, request); if (sequence === current) { emit('saved', config); close() } }
  catch (e) { if (sequence === current) emit('error', extractApiErrorMessage(e, t('accountOps.saveFailed'))) }
  finally { if (sequence === current) busy.value = false }
}
</script>
