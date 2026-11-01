<template>
  <form id="account-ops-global-form" class="py-3" @submit.prevent="save">
    <fieldset :disabled="busy || disabled" class="flex flex-wrap items-end justify-between gap-x-6 gap-y-4">
      <div>
        <h4 class="text-xs font-medium text-muted">{{ t('accountOps.globalSettings') }}</h4>
        <div class="mt-3 flex flex-wrap items-center gap-x-6 gap-y-3">
          <UCheckbox v-model="draft.balance_low" :label="t('accountOps.balance_low')" data-testid="global-balance-low" @update:model-value="dirty = true" />
          <UCheckbox v-model="draft.weekly_quota" :label="t('accountOps.weekly_quota')" data-testid="global-weekly-quota" @update:model-value="dirty = true" />
          <div class="flex flex-wrap items-center gap-2 text-sm">
            <span>{{ t('accountOps.cooldown') }}</span>
            <UInput v-model.number="draft.cooldown_minutes" type="number" min="5" max="1440" step="1" required size="sm" class="w-24" data-testid="global-cooldown" :aria-label="t('accountOps.cooldown')" @update:model-value="dirty = true" />
            <span class="text-muted">{{ t('accountOps.minutes') }}</span>
          </div>
        </div>
      </div>
      <UButton type="submit" color="neutral" variant="outline" :disabled="!dirty" :label="t(busy ? 'common.loading' : 'accountOps.saveGlobalSettings')" />
    </fieldset>
    <p class="mt-3 text-xs leading-5 text-muted">{{ t('accountOps.globalFailuresHint') }}</p>
  </form>
</template>

<script setup lang="ts">
import { onBeforeUnmount, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { accountOpsMessages } from './accountOpsMessages'
import { saveAccountOpsNotificationSettings } from '@/api/admin/accountOps'
import type { AccountOpsConfig } from '@/api/admin/accountOps'
import { extractApiErrorMessage } from '@/utils/apiError'

const props = defineProps<{ config: AccountOpsConfig; disabled?: boolean }>()
const emit = defineEmits<{ (event: 'saved', config: AccountOpsConfig): void; (event: 'error', message: string): void; (event: 'saving', value: boolean): void }>()
const { t } = useI18n({ useScope: 'local', messages: accountOpsMessages })
const draft = reactive({ balance_low: true, weekly_quota: true, cooldown_minutes: 60 })
const dirty = ref(false), busy = ref(false)
let alive = true
watch(() => [props.config.balance_low, props.config.weekly_quota, props.config.cooldown_minutes], () => {
  if (!dirty.value && !busy.value) reset(props.config)
}, { immediate: true })
function reset(config: AccountOpsConfig) {
  Object.assign(draft, { balance_low: config.balance_low, weekly_quota: config.weekly_quota, cooldown_minutes: config.cooldown_minutes })
  dirty.value = false
}
onBeforeUnmount(() => { alive = false; emit('saving', false) })
async function save() {
  if (busy.value || props.disabled || !dirty.value) return
  if (!Number.isInteger(draft.cooldown_minutes) || draft.cooldown_minutes < 5 || draft.cooldown_minutes > 1440) {
    emit('error', t('accountOps.cooldownInvalid'))
    return
  }
  busy.value = true
  emit('saving', true)
  try {
    const config = await saveAccountOpsNotificationSettings({ ...draft })
    if (alive) { reset(config); emit('saved', config) }
  } catch (e) {
    if (alive) emit('error', extractApiErrorMessage(e, t('accountOps.saveFailed')))
  } finally {
    if (alive) { busy.value = false; emit('saving', false) }
  }
}
</script>
