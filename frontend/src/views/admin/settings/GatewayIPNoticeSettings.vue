<template>
  <SettingsSection
    :title="t('admin.settings.gatewayIPNotice.title')"
    :description="t('admin.settings.gatewayIPNotice.description')"
    :loading="loading"
    :aria-busy="loading || saving"
  >
    <template #actions>
      <UButton v-if="loadFailed" color="neutral" variant="outline" data-testid="gateway-ip-notice-retry" @click="load">
        {{ t('admin.settings.gatewayIPNotice.retry') }}
      </UButton>
      <UButton data-testid="gateway-ip-notice-save" :loading="saving" :disabled="saving || loadFailed" @click="save">
        {{ saving ? t('common.saving') : t('common.save') }}
      </UButton>
    </template>
    <SettingPanel hide-summary>
      <UAlert v-if="error" role="alert" color="error" variant="subtle" icon="i-lucide-circle-alert" :description="error" />
      <SettingGroup>
        <SettingRow :label="t('admin.settings.gatewayIPNotice.enabled')" :on="form.enabled">
          <template #control>
            <Toggle id="gateway-ip-notice-enabled" v-model="form.enabled" :disabled="saving || loadFailed" />
          </template>
        </SettingRow>
        <!-- The list and the message are long text: the row shows how much is there, the editor opens on click. -->
        <SettingRow
          :label="t('admin.settings.gatewayIPNotice.ips')"
          :hint="t('admin.settings.gatewayIPNotice.ipHint')"
          :summary="t('common.settingsSummary.itemCount', { count: ipText.split(/[\s,，;；]+/).filter(Boolean).length })"
          :on="true"
          :in-summary="false"
          persistent-summary
        >
          <template #fields>
            <UTextarea
              id="gateway-ip-notice-ips"
              v-model="ipText"
              :rows="5"
              autoresize
              :disabled="saving || loadFailed"
              spellcheck="false"
              class="w-full font-mono"
            />
          </template>
        </SettingRow>
        <SettingRow
          :label="t('admin.settings.gatewayIPNotice.message')"
          :summary="form.message.trim() ? form.message : t('common.settingsSummary.notSet')"
          :on="true"
          :in-summary="false"
          persistent-summary
        >
          <template #fields>
            <UTextarea id="gateway-ip-notice-message" v-model="form.message" :rows="3" autoresize maxlength="2048" :disabled="saving || loadFailed" class="w-full" />
            <div v-if="form.message" class="grid gap-1">
              <span class="block text-xs font-medium text-muted">{{ t('admin.settings.gatewayIPNotice.preview') }}</span>
              <span class="block whitespace-pre-wrap wrap-break-word text-sm text-highlighted">{{ form.message }}</span>
            </div>
          </template>
        </SettingRow>
        <SettingRow
          :label="t('admin.settings.gatewayIPNotice.rpm')"
          :hint="[t('admin.settings.gatewayIPNotice.rpmHint'), t('admin.settings.gatewayIPNotice.effectiveHint')]"
          :on="true"
          :in-summary="false"
        >
          <template #control>
            <UInput id="gateway-ip-notice-rpm" v-model.number="form.rpm" type="number" min="1" max="100000" step="1" :disabled="saving || loadFailed" class="w-28" />
          </template>
        </SettingRow>
      </SettingGroup>
    </SettingPanel>
  </SettingsSection>
  <TotpStepUpDialog :controller="stepUp" />
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { getGatewayIPNotice, updateGatewayIPNotice, type GatewayIPNoticeSettings } from '@/api/admin/gatewayIpNotice'
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
const ipText = ref('')
const form = reactive<GatewayIPNoticeSettings>({ enabled: false, ips: [], message: '', rpm: 100 })

async function load() {
  loading.value = true
  error.value = ''
  try {
    Object.assign(form, await getGatewayIPNotice())
    ipText.value = form.ips.join('\n')
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
  const ips = [...new Set(ipText.value.split(/[\s,，;；]+/).filter(Boolean))]
  if (!Number.isInteger(form.rpm) || form.rpm < 1 || form.rpm > 100000 || ips.length > 256 || (form.enabled && (!ips.length || !form.message.trim()))) {
    error.value = t('admin.settings.gatewayIPNotice.invalid')
    return
  }
  saving.value = true
  error.value = ''
  try {
    Object.assign(form, await stepUp.run(() => updateGatewayIPNotice({ ...form, ips })))
    ipText.value = form.ips.join('\n')
    appStore.showSuccess(t('admin.settings.gatewayIPNotice.saved'))
  } catch (err) {
    if (isStepUpCancelled(err)) return
    error.value = extractApiErrorMessage(err, t('common.error'))
  } finally {
    saving.value = false
  }
}

onMounted(load)
</script>
