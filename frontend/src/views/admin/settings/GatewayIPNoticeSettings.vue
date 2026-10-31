<template>
  <section class="card" aria-labelledby="gateway-ip-notice-title">
    <div class="border-b border-gray-100 px-6 py-4 dark:border-dark-700">
      <h2 id="gateway-ip-notice-title" class="text-lg font-semibold text-gray-900 dark:text-white">
        {{ t('admin.settings.gatewayIPNotice.title') }}
      </h2>
      <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">{{ t('admin.settings.gatewayIPNotice.description') }}</p>
    </div>
    <div class="space-y-5 p-6" :aria-busy="loading || saving">
      <p v-if="loading" role="status" class="text-sm text-gray-500">{{ t('common.loading') }}</p>
      <template v-else>
        <div class="flex items-center justify-between gap-4">
          <label for="gateway-ip-notice-enabled" class="font-medium text-gray-900 dark:text-white">
            {{ t('admin.settings.gatewayIPNotice.enabled') }}
          </label>
          <input id="gateway-ip-notice-enabled" v-model="form.enabled" type="checkbox" :disabled="saving || loadFailed" class="h-4 w-4 rounded-sm" />
        </div>
        <div>
          <label for="gateway-ip-notice-ips" class="input-label">{{ t('admin.settings.gatewayIPNotice.ips') }}</label>
          <textarea id="gateway-ip-notice-ips" v-model="ipText" rows="5" class="input w-full font-mono" :disabled="saving || loadFailed" :aria-describedby="'gateway-ip-notice-ip-hint'" spellcheck="false" />
          <p id="gateway-ip-notice-ip-hint" class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.settings.gatewayIPNotice.ipHint') }}</p>
        </div>
        <div>
          <label for="gateway-ip-notice-message" class="input-label">{{ t('admin.settings.gatewayIPNotice.message') }}</label>
          <textarea id="gateway-ip-notice-message" v-model="form.message" rows="3" maxlength="2048" class="input w-full" :disabled="saving || loadFailed" />
        </div>
        <div>
          <label for="gateway-ip-notice-rpm" class="input-label">{{ t('admin.settings.gatewayIPNotice.rpm') }}</label>
          <input id="gateway-ip-notice-rpm" v-model.number="form.rpm" type="number" min="1" max="100000" step="1" class="input w-36" :disabled="saving || loadFailed" />
          <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.settings.gatewayIPNotice.rpmHint') }}</p>
        </div>
        <div v-if="form.message" class="rounded-lg border border-gray-200 bg-gray-50 p-4 dark:border-dark-600 dark:bg-dark-800">
          <p class="mb-2 text-xs font-medium text-gray-500">{{ t('admin.settings.gatewayIPNotice.preview') }}</p>
          <p class="whitespace-pre-wrap wrap-break-word text-sm text-gray-900 dark:text-gray-100">{{ form.message }}</p>
        </div>
        <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.settings.gatewayIPNotice.effectiveHint') }}</p>
        <p v-if="error" role="alert" class="text-sm text-red-600 dark:text-red-400">{{ error }}</p>
        <div class="flex justify-end gap-3 border-t border-gray-100 pt-4 dark:border-dark-700">
          <button v-if="loadFailed" type="button" class="btn btn-secondary btn-sm" @click="load">{{ t('admin.settings.gatewayIPNotice.retry') }}</button>
          <button type="button" class="btn btn-primary btn-sm" :disabled="saving || loadFailed" @click="save">
            {{ saving ? t('common.saving') : t('common.save') }}
          </button>
        </div>
      </template>
    </div>
    <TotpStepUpDialog :controller="stepUp" />
  </section>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { getGatewayIPNotice, updateGatewayIPNotice, type GatewayIPNoticeSettings } from '@/api/admin/gatewayIpNotice'
import { useAppStore } from '@/stores'
import { extractApiErrorMessage } from '@/utils/apiError'
import { isStepUpCancelled, useStepUp } from '@/composables/useStepUp'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'

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
