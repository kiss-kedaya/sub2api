<template>
  <div class="card">
    <div class="border-b border-gray-100 px-6 py-4 dark:border-dark-700">
      <h2 class="text-lg font-medium text-gray-900 dark:text-white">
        {{ title || t('profile.bark.title') }}
      </h2>
      <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
        {{ description || t('profile.bark.description') }}
      </p>
    </div>
    <div class="px-6 py-6 space-y-6">
      <div v-if="loadError" role="alert" class="text-sm text-red-600 dark:text-red-400">
        <p>{{ loadError }}</p>
        <button type="button" class="btn btn-secondary btn-sm mt-2" :disabled="loading" @click="refresh">{{ t('common.refresh') }}</button>
      </div>
      <p v-if="needsTotp" role="alert" class="text-sm text-amber-700 dark:text-amber-300">
        {{ t('profile.bark.totpRequired') }}
        <a href="/profile" class="underline">{{ t('profile.bark.configureTotp') }}</a>
      </p>
      <p class="text-sm text-gray-500 dark:text-gray-400">{{ t('profile.bark.changeVerificationHint') }}</p>
      <div class="flex items-center gap-2 text-sm">
        <span
          class="inline-block h-2 w-2 rounded-full"
          :class="status.configured ? 'bg-green-500' : 'bg-gray-400'"
        ></span>
        <span class="text-gray-500 dark:text-gray-400">
          {{ status.configured ? t('profile.bark.configured') : t('profile.bark.notConfigured') }}
        </span>
      </div>

      <div>
        <label for="bark-device-key" class="input-label">{{ t('profile.bark.deviceKey') }}</label>
        <input
          id="bark-device-key"
          v-model="deviceKey"
          type="password"
          autocomplete="off"
          spellcheck="false"
          :disabled="loading || saving || !!loadError"
          class="input"
          :placeholder="t('profile.bark.deviceKeyPlaceholder')"
        />
        <p class="mt-1 text-xs text-gray-400">{{ t(status.configured ? 'profile.bark.keepKeyHint' : 'profile.bark.deviceKeyHint') }}</p>
      </div>

      <div class="flex items-center justify-between">
        <label class="input-label mb-0">{{ t('profile.bark.enabled') }}</label>
        <label class="relative inline-flex items-center cursor-pointer">
          <input type="checkbox" v-model="enabled" :disabled="loading || saving || !!loadError" class="sr-only peer" :aria-label="t('profile.bark.enabled')" />
          <div class="w-11 h-6 bg-gray-200 peer-focus:outline-none peer-focus:ring-4 peer-focus:ring-primary-300 dark:peer-focus:ring-primary-800 rounded-full peer dark:bg-gray-700 peer-checked:after:translate-x-full peer-checked:after:border-white after:content-[''] after:absolute after:top-[2px] after:left-[2px] after:bg-white after:border-gray-300 after:border after:rounded-full after:h-5 after:w-5 after:transition-all dark:after:border-gray-600 peer-checked:bg-primary-600"></div>
        </label>
      </div>

      <div class="flex items-center gap-2">
        <button type="button" class="btn btn-primary btn-sm" :disabled="loading || saving || !!loadError || (!deviceKey.trim() && !status.configured)" @click="save">
          {{ saving ? t('common.saving') : t('profile.bark.save') }}
        </button>
        <button
          v-if="status.configured"
          type="button"
          class="btn btn-secondary btn-sm"
          :disabled="loading || saving || !!loadError"
          @click="clear"
        >
          {{ t('profile.bark.clear') }}
        </button>
      </div>
    </div>
    <TotpStepUpDialog :controller="stepUp" :verify="verifyChange" :hint="t('profile.bark.verifyChangeHint')" />
  </div>
</template>

<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { userAPI, type BarkSettings } from '@/api/user'
import { totpAPI } from '@/api'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
import { useStepUp } from '@/composables/useStepUp'
import { extractApiErrorMessage } from '@/utils/apiError'

const { t } = useI18n()
const appStore = useAppStore()
defineProps<{ title?: string; description?: string }>()

const status = ref<BarkSettings>({ configured: false, enabled: false })
const deviceKey = ref('')
const enabled = ref(false)
const saving = ref(false)
const loading = ref(true)
const loadError = ref('')
const needsTotp = ref(false)
const stepUp = useStepUp()
let pendingChange: { key: string | undefined; enabled: boolean } | null = null
let disposed = false

async function refresh() {
  loading.value = true
  loadError.value = ''
  try {
    status.value = await userAPI.getBarkSettings()
    enabled.value = status.value.enabled
  } catch {
    loadError.value = t('profile.bark.loadFailed')
  } finally {
    loading.value = false
  }
}
onMounted(refresh)
onUnmounted(() => {
  disposed = true
  pendingChange = null
  deviceKey.value = ''
  stepUp.onCancel()
})

async function verifyChange(code: string) {
  const change = pendingChange
  if (!change || disposed) throw new Error(t('common.error'))
  await userAPI.updateBarkSettings(change.key, change.enabled, code)
}

async function requestChange(key: string | undefined, nextEnabled: boolean, successKey: string) {
  saving.value = true
  needsTotp.value = false
  try {
    const totp = await totpAPI.getStatus()
    if (disposed) return
    if (totp.enabled !== true) {
      needsTotp.value = true
      appStore.showError(t('profile.bark.totpRequired'))
      return
    }
    pendingChange = { key, enabled: nextEnabled }
    const verified = await stepUp.prompt()
    if (!verified || disposed) return
    // The dialog's verifier already performed the single authenticated PUT.
    // Never resend the update after it consumes the fresh TOTP code.
    deviceKey.value = ''
    await refresh()
    if (!disposed) appStore.showSuccess(t(successKey))
  } catch (err: unknown) {
    if (!disposed) appStore.showError(extractApiErrorMessage(err, t('common.error')))
  } finally {
    pendingChange = null
    saving.value = false
  }
}

async function save() {
  if (loading.value || saving.value || loadError.value || (!deviceKey.value.trim() && !status.value.configured)) return
  await requestChange(deviceKey.value.trim() || undefined, enabled.value, 'profile.bark.saved')
}

async function clear() {
  if (loading.value || saving.value || loadError.value || !status.value.configured) return
  await requestChange('', false, 'profile.bark.cleared')
}
</script>
