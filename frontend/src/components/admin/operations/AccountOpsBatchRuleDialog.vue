<template>
  <BaseDialog
    :show="show"
    :title="t('accountOps.batchRuleTitle')"
    :width="isMixed ? 'wide' : 'normal'"
    @close="close"
  >
    <form id="account-ops-batch-rule-form" class="space-y-5" @submit.prevent="save">
      <div class="space-y-3">
        <p class="text-sm font-medium text-highlighted">
          {{ isMixed ? t('accountOps.batchMixedRuleScope') : t('accountOps.batchRuleScope', { count: accounts.length }) }}
        </p>
        <UAlert color="warning" variant="subtle" icon="i-lucide-triangle-alert" :description="t('accountOps.batchReplaceHint')" />
        <p v-if="!validSelection" role="alert" class="text-sm text-error">
          {{ t('accountOps.batchInvalidSelection') }}
        </p>
        <p v-else-if="mixedUnits" role="alert" class="text-sm text-error">
          {{ t('accountOps.batchMixedUnits') }}
        </p>
      </div>

      <div class="grid gap-x-8 gap-y-5" :class="{ 'sm:grid-cols-2': isMixed }">
        <fieldset
          v-for="section in sections"
          :key="section.metric"
          :disabled="busy"
          class="min-w-0 space-y-5"
        >
          <legend class="text-sm font-semibold text-highlighted">
            {{ t(section.metric === 'quota' ? 'accountOps.batchQuotaRules' : 'accountOps.batchBalanceRules') }}
          </legend>
          <UCollapsible :unmount-on-hide="false" class="pt-3">
            <UButton color="neutral" variant="link" size="xs" class="p-0" trailing-icon="i-lucide-chevron-down" :label="t('accountOps.selectedAccounts', { count: section.accounts.length })" />
            <template #content>
              <ul class="mt-2 max-h-32 space-y-2 overflow-y-auto">
                <li v-for="account in section.accounts" :key="account.account_id" class="wrap-break-word text-sm text-default">
                  <span class="font-medium">{{ account.account_name }}</span>
                  <span class="ml-2 text-xs text-muted">
                    #{{ account.account_id }}<template v-if="account.type === 'apikey'"> · {{ account.unit }}</template>
                  </span>
                </li>
              </ul>
            </template>
          </UCollapsible>
          <USwitch v-model="section.form.enabled" :label="t('accountOps.ruleEnabled')" :data-testid="`${section.testIdPrefix}-enabled`" />
          <UFormField :label="t('accountOps.thresholdValue')">
            <UInput
              :model-value="String(section.form.threshold)"
              type="number"
              @update:model-value="section.form.threshold = $event as string | number"
              :min="section.metric === 'quota' ? 0.01 : 0"
              :max="section.metric === 'quota' ? 100 : 1e12"
              step="any"
              class="w-full"
              :data-testid="`${section.testIdPrefix}-threshold`"
            >
              <template #trailing>
                <span class="text-xs text-muted">{{ section.metric === 'quota' ? '%' : mixedUnits ? '—' : unit }}</span>
              </template>
            </UInput>
          </UFormField>
          <UFormField v-if="section.metric === 'quota'" :label="t('accountOps.quotaWindow')">
            <USelect
              v-model="section.form.window"
              :items="windowItems"
              class="w-full"
              :data-testid="`${section.testIdPrefix}-window`"
            />
          </UFormField>
          <div class="space-y-3">
            <p class="text-sm font-medium text-highlighted">{{ t('accountOps.transitionNotices') }}</p>
            <UCheckbox
              v-model="section.form.notifyAlert"
              :label="t('accountOps.notifyAlert')"
              :description="t('accountOps.notifyAlertHint')"
              :data-testid="`${section.testIdPrefix}-notify-alert`"
            />
            <UCheckbox
              v-model="section.form.notifyRecovery"
              :label="t('accountOps.notifyRecovery')"
              :description="t('accountOps.notifyRecoveryHint')"
              :data-testid="`${section.testIdPrefix}-notify-recovery`"
            />
          </div>
          <p class="text-xs leading-5 text-muted">
            {{ t(section.metric === 'quota' ? 'accountOps.quotaOnceHint' : 'accountOps.balanceOnceHint') }}
          </p>
        </fieldset>
      </div>
    </form>
    <template #footer>
      <div class="flex justify-end gap-2">
        <UButton color="neutral" variant="subtle" data-testid="cancel-batch-rules" :label="t('common.cancel')" @click="close" />
        <UButton
          type="submit"
          form="account-ops-batch-rule-form"
          data-testid="save-batch-rules"
          :disabled="busy || !validSelection || mixedUnits"
          :label="busy ? t('common.loading') : t('accountOps.saveBatchRules', { count: accounts.length })"
        />
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { accountOpsMessages } from './accountOpsMessages'
import BaseDialog from '@/components/common/BaseDialog.vue'
import { saveAccountOpsRuleGroups, saveAccountOpsRulesBatch } from '@/api/admin/accountOps'
import type { AccountOpsConfig, AccountOpsRuleBatchGroup, AccountOpsRuleInput, AccountOpsThresholdAccount } from '@/api/admin/accountOps'
import { extractApiErrorMessage } from '@/utils/apiError'

interface RuleForm {
  enabled: boolean
  threshold: string | number
  window: string
  notifyAlert: boolean
  notifyRecovery: boolean
}
interface RuleSection {
  metric: 'balance' | 'quota'
  form: RuleForm
  accounts: AccountOpsThresholdAccount[]
  testIdPrefix: string
}

const props = defineProps<{ show: boolean; accounts: AccountOpsThresholdAccount[]; config?: AccountOpsConfig }>()
const emit = defineEmits<{
  (event: 'close'): void
  (event: 'saved', config: AccountOpsConfig): void
  (event: 'error', message: string): void
}>()
const { t } = useI18n({ useScope: 'local', messages: accountOpsMessages })
const newForm = (): RuleForm => ({
  enabled: true, threshold: '', window: 'any', notifyAlert: true, notifyRecovery: true
})
const balanceForm = reactive(newForm())
const quotaForm = reactive(newForm())
const busy = ref(false)
const balanceAccounts = computed(() => props.accounts.filter(account => account.type === 'apikey'))
const quotaAccounts = computed(() => props.accounts.filter(account => account.type === 'oauth'))
const isMixed = computed(() => balanceAccounts.value.length > 0 && quotaAccounts.value.length > 0)
const validSelection = computed(() => props.accounts.length > 0
  && props.accounts.length === balanceAccounts.value.length + quotaAccounts.value.length)
const unit = computed(() => balanceAccounts.value[0]?.unit ?? '')
const mixedUnits = computed(() => balanceAccounts.value.some(account => account.unit !== unit.value))
const commonWindows = computed(() => (quotaAccounts.value[0]?.usage_windows ?? [])
  .filter(window => window.window !== 'any'
    && quotaAccounts.value.every(account => account.usage_windows.some(candidate => candidate.window === window.window))))
const windowItems = computed(() => [{ value: 'any', label: t('accountOps.anyWindow') }, ...commonWindows.value.map(window => ({ value: window.window, label: window.label }))])
const sections = computed<RuleSection[]>(() => {
  const result: RuleSection[] = []
  if (balanceAccounts.value.length) result.push({
    metric: 'balance', form: balanceForm, accounts: balanceAccounts.value,
    testIdPrefix: isMixed.value ? 'batch-balance-rule' : 'batch-rule'
  })
  if (quotaAccounts.value.length) result.push({
    metric: 'quota', form: quotaForm, accounts: quotaAccounts.value,
    testIdPrefix: isMixed.value ? 'batch-quota-rule' : 'batch-rule'
  })
  return result
})
const scopeKey = computed(() => JSON.stringify(props.accounts.map(account => ({
  id: account.account_id,
  type: account.type,
  unit: account.type === 'apikey' ? account.unit : undefined,
  windows: account.type === 'oauth' ? account.usage_windows.map(window => window.window) : undefined
}))))
let sequence = 0

watch(() => [props.show, scopeKey.value] as const, () => {
  sequence++
  busy.value = false
  Object.assign(balanceForm, newForm())
  Object.assign(quotaForm, newForm())
  const balance = props.config?.balance_thresholds?.find(rule => rule.account_id === balanceAccounts.value[0]?.account_id)
  if (balance) Object.assign(balanceForm, { enabled: balance.enabled, threshold: balance.threshold, notifyAlert: balance.notify_alert ?? true, notifyRecovery: balance.notify_recovery ?? true })
  const quota = props.config?.quota_thresholds?.find(rule => rule.account_id === quotaAccounts.value[0]?.account_id)
  if (quota) Object.assign(quotaForm, { enabled: quota.enabled, threshold: quota.threshold_percent, window: quota.window, notifyAlert: quota.notify_alert ?? true, notifyRecovery: quota.notify_recovery ?? true })
}, { immediate: true, flush: 'pre' })

function close() {
  sequence++
  busy.value = false
  emit('close')
}

onBeforeUnmount(() => { sequence++ })

function validThreshold(section: RuleSection): boolean {
  const value = Number(section.form.threshold)
  const quota = section.metric === 'quota'
  return String(section.form.threshold).trim() !== '' && Number.isFinite(value)
    && value >= (quota ? Number.MIN_VALUE : 0) && value <= (quota ? 100 : 1e12)
}

function buildRule(section: RuleSection): AccountOpsRuleInput {
  const value = Number(section.form.threshold)
  return {
    metric: section.metric,
    enabled: section.form.enabled,
    notify_alert: section.form.notifyAlert,
    notify_recovery: section.form.notifyRecovery,
    ...(section.metric === 'quota'
      ? { threshold_percent: value, window: section.form.window }
      : { threshold: value, unit: unit.value })
  }
}

async function save() {
  if (!props.show || busy.value) return
  if (!validSelection.value) {
    emit('error', t('accountOps.batchInvalidSelection'))
    return
  }
  if (mixedUnits.value) {
    emit('error', t('accountOps.batchMixedUnits'))
    return
  }
  if (!sections.value.every(validThreshold)) {
    emit('error', t('accountOps.invalidThreshold'))
    return
  }

  const groups: AccountOpsRuleBatchGroup[] = sections.value.map(section => ({
    account_ids: section.accounts.map(account => account.account_id),
    rule: buildRule(section)
  }))
  const current = sequence
  busy.value = true
  try {
    const config = groups.length === 1
      ? await saveAccountOpsRulesBatch(groups[0]!.account_ids, groups[0]!.rule)
      : await saveAccountOpsRuleGroups(groups)
    if (sequence === current) {
      emit('saved', config)
      close()
    }
  } catch (error) {
    if (sequence === current) emit('error', extractApiErrorMessage(error, t('accountOps.saveFailed')))
  } finally {
    if (sequence === current) busy.value = false
  }
}
</script>
