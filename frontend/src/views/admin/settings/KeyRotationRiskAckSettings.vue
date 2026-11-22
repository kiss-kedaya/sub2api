<template>
  <SettingsSection
    :title="t('admin.settings.keyRotationRiskAck.title')"
    :description="t('admin.settings.keyRotationRiskAck.description')"
    :loading="loading"
    :aria-busy="loading || saving"
  >
    <template #actions>
      <UButton v-if="loadFailed" color="neutral" variant="outline" data-testid="key-rotation-risk-ack-retry" @click="load">
        {{ t('admin.settings.keyRotationRiskAck.retry') }}
      </UButton>
      <UButton
        data-testid="key-rotation-risk-ack-save"
        :loading="saving"
        :disabled="saving || loadFailed"
        @click="save"
      >
        {{ saving ? t('common.saving') : t('common.save') }}
      </UButton>
    </template>
    <SettingPanel hide-summary>
      <UAlert
        v-if="error"
        role="alert"
        color="error"
        variant="subtle"
        icon="i-lucide-circle-alert"
        :description="error"
      />
      <SettingGroup>
        <SettingRow
          :label="t('admin.settings.keyRotationRiskAck.enabled')"
          :hint="t('admin.settings.keyRotationRiskAck.enabledHint')"
          :on="form.enabled"
        >
          <template #control>
            <Toggle
              id="key-rotation-risk-ack-enabled"
              v-model="form.enabled"
              :disabled="saving || loadFailed"
            />
          </template>
        </SettingRow>
        <SettingRow
          :label="t('admin.settings.keyRotationRiskAck.hours')"
          :hint="t('admin.settings.keyRotationRiskAck.hoursHint')"
          :on="true"
          :in-summary="false"
        >
          <template #control>
            <UInput
              id="key-rotation-risk-ack-hours"
              v-model.number="form.hours"
              type="number"
              min="1"
              max="720"
              step="1"
              :disabled="saving || loadFailed || !form.enabled"
              class="w-28"
            />
          </template>
        </SettingRow>
      </SettingGroup>
      <UAlert
        color="warning"
        variant="subtle"
        icon="i-lucide-info"
        :description="t('admin.settings.keyRotationRiskAck.auditHint')"
      />
    </SettingPanel>
  </SettingsSection>
  <TotpStepUpDialog :controller="stepUp" />
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'

import {
  getKeyRotationRiskAckSettings,
  updateKeyRotationRiskAckSettings,
  type KeyRotationRiskAckSettings,
} from '@/api/admin/keyRotationRiskAck'
import { useAppStore } from '@/stores'
import { extractApiErrorMessage } from '@/utils/apiError'
import { isStepUpCancelled, useStepUp } from '@/composables/useStepUp'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
import Toggle from '@/components/common/Toggle.vue'
import SettingPanel from '@/components/common/SettingPanel.vue'
import SettingGroup from '@/components/common/SettingGroup.vue'
import SettingRow from '@/components/common/SettingRow.vue'
import SettingsSection from './SettingsSection.vue'

const { t } = useI18n()
const appStore = useAppStore()
const stepUp = useStepUp()
const loading = ref(true)
const saving = ref(false)
const loadFailed = ref(false)
const error = ref('')
// 后端默认开启、24 小时；加载失败时也保持这个产品默认值展示。
const form = reactive<KeyRotationRiskAckSettings>({ enabled: true, hours: 24 })

function apply(settings: KeyRotationRiskAckSettings) {
  form.enabled = settings?.enabled ?? true
  form.hours = settings?.hours && settings.hours >= 1 && settings.hours <= 720 ? settings.hours : 24
}

async function load() {
  loading.value = true
  error.value = ''
  try {
    apply(await getKeyRotationRiskAckSettings())
    loadFailed.value = false
  } catch (err) {
    loadFailed.value = true
    error.value = extractApiErrorMessage(err, t('common.error'))
  } finally {
    loading.value = false
  }
}

async function save() {
  if (saving.value || loadFailed.value) return
  if (!Number.isInteger(form.hours) || form.hours < 1 || form.hours > 720) {
    error.value = t('admin.settings.keyRotationRiskAck.invalidHours')
    return
  }
  saving.value = true
  error.value = ''
  try {
    apply(await stepUp.run(() => updateKeyRotationRiskAckSettings({ enabled: form.enabled, hours: form.hours })))
    appStore.showSuccess(t('admin.settings.keyRotationRiskAck.saved'))
  } catch (err) {
    if (isStepUpCancelled(err)) return
    error.value = extractApiErrorMessage(err, t('common.error'))
  } finally {
    saving.value = false
  }
}

onMounted(load)
</script>
