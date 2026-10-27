<template>
  <BaseDialog :show="show" :title="t('accountOps.editRule')" width="normal" @close="close">
    <form v-if="account" id="account-ops-rule-form" class="space-y-5" @submit.prevent="save">
      <div><p class="font-medium text-gray-900 dark:text-gray-100">{{ account.account_name }}</p><p class="mt-1 text-xs text-gray-500">{{ account.platform }} · {{ account.type === 'apikey' ? 'API Key' : 'OAuth' }} · #{{ account.account_id }}</p></div>
      <fieldset :disabled="busy" class="space-y-5">
        <label class="flex items-center justify-between text-sm"><span>{{ t('accountOps.ruleEnabled') }}</span><input v-model="enabled" type="checkbox" data-testid="rule-enabled" /></label>
        <label class="block space-y-2 text-sm"><span>{{ t('accountOps.thresholdValue') }}</span><div class="flex items-center gap-2"><input v-model="threshold" type="number" :min="isQuota ? 0.01 : 0" :max="isQuota ? 100 : 1e12" step="any" class="input w-full" data-testid="rule-threshold" /><span class="shrink-0 text-gray-500">{{ isQuota ? '%' : unit }}</span></div></label>
        <div v-if="!isQuota && unit !== account.unit" class="space-y-2 rounded-lg bg-amber-50 p-3 text-xs text-amber-800 dark:bg-amber-950/20"><p>{{ t('accountOps.unitMismatchHint', { saved: unit, current: account.unit }) }}</p><button type="button" class="underline" data-testid="rule-use-current-unit" @click="unit = account.unit; threshold = ''">{{ t('accountOps.useCurrentUnit', { unit: account.unit }) }}</button></div>
        <label v-if="isQuota" class="block space-y-2 text-sm"><span>{{ t('accountOps.quotaWindow') }}</span><select v-model="windowId" class="input w-full" data-testid="rule-window"><option value="any">{{ t('accountOps.anyWindow') }}</option><option v-for="window in account.usage_windows" :key="window.window" :value="window.window">{{ window.label }}</option><option v-if="windowId !== 'any' && !account.usage_windows.some(w => w.window === windowId)" :value="windowId">{{ windowId }}</option></select></label>
        <div class="space-y-3 border-y border-gray-200 py-4 dark:border-dark-700"><p class="text-sm font-medium">{{ t('accountOps.transitionNotices') }}</p><label class="flex items-start gap-3 text-sm"><input v-model="notifyAlert" type="checkbox" class="mt-1" data-testid="rule-notify-alert" /><span>{{ t('accountOps.notifyAlert') }}<small class="mt-1 block text-xs leading-5 text-gray-500">{{ t('accountOps.notifyAlertHint') }}</small></span></label><label class="flex items-start gap-3 text-sm"><input v-model="notifyRecovery" type="checkbox" class="mt-1" data-testid="rule-notify-recovery" /><span>{{ t('accountOps.notifyRecovery') }}<small class="mt-1 block text-xs leading-5 text-gray-500">{{ t('accountOps.notifyRecoveryHint') }}</small></span></label></div>
        <p class="text-xs leading-5 text-gray-500">{{ t(isQuota ? 'accountOps.quotaOnceHint' : 'accountOps.balanceOnceHint') }}</p>
        <details class="text-xs text-gray-500"><summary class="cursor-pointer">{{ t('accountOps.sampleDetails') }}</summary><div class="mt-3 space-y-2"><p v-if="!isQuota">{{ t('accountOps.currentValue') }}: {{ account.balance_status === 'ok' && account.balance != null ? `${account.balance.toFixed(2)} ${account.unit}` : t(`accountOps.observations.${account.balance_status}`) }}</p><p v-if="account.received_at">{{ t('accountOps.sampleTime') }} {{ date(account.received_at) }}</p><div v-for="window in account.usage_windows" :key="window.window"><p>{{ window.label }}: {{ window.status === 'ok' && window.used_percent != null ? `${window.used_percent.toFixed(1)}%` : t(`accountOps.observations.${window.status}`) }}</p><p v-if="window.observed_at">{{ t('accountOps.sampleTime') }} {{ date(window.observed_at) }}</p><p v-if="window.resets_at">{{ t('accountOps.resetTime') }} {{ date(window.resets_at) }}</p></div></div></details>
      </fieldset>
    </form>
    <template #footer><button type="button" class="btn btn-secondary" @click="close">{{ t('common.cancel') }}</button><button type="submit" form="account-ops-rule-form" :disabled="busy" class="btn btn-primary">{{ t(busy ? 'common.loading' : 'accountOps.saveRule') }}</button></template>
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
