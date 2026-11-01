<template>
  <!-- Notification section of the dashboard template: title card with the save actions, then a
       subtle card with the device key and push switch rows. Also used on the admin crypto page. -->
  <section>
    <UPageCard
      :description="description || t('profile.bark.description')"
      variant="naked"
      orientation="horizontal"
      class="mb-4"
    >
      <template #title>
        <h2>{{ title || t('profile.bark.title') }}</h2>
      </template>
      <div class="flex w-fit gap-2 lg:ms-auto">
        <UButton
          v-if="status.configured"
          color="neutral"
          variant="outline"
          :disabled="loading || saving || !!loadError"
          @click="clear"
        >
          {{ t('profile.bark.clear') }}
        </UButton>
        <UButton
          data-testid="bark-save"
          :disabled="loading || saving || !!loadError || (!deviceKey.trim() && !status.configured)"
          @click="save"
        >
          {{ saving ? t('common.saving') : t('profile.bark.save') }}
        </UButton>
      </div>
    </UPageCard>

    <UPageCard variant="subtle">
      <UAlert
        v-if="loadError"
        role="alert"
        color="error"
        variant="subtle"
        icon="i-lucide-circle-alert"
        :description="loadError"
        orientation="horizontal"
      >
        <template #actions>
          <UButton size="xs" color="neutral" variant="outline" :disabled="loading" @click="refresh">{{ t('common.refresh') }}</UButton>
        </template>
      </UAlert>
      <UAlert
        v-if="needsTotp"
        role="alert"
        color="warning"
        variant="subtle"
        icon="i-lucide-shield-alert"
      >
        <template #description>
          {{ t('profile.bark.totpRequired') }}
          <a href="/profile" class="font-medium underline">{{ t('profile.bark.configureTotp') }}</a>
        </template>
      </UAlert>

      <UFormField
        :label="t('profile.bark.deviceKey')"
        class="flex justify-between gap-4 max-sm:flex-col sm:items-start"
        :ui="{ wrapper: 'min-w-0 flex-1' }"
      >
        <template #description>
          <span class="block">{{ t(status.configured ? 'profile.bark.keepKeyHint' : 'profile.bark.deviceKeyHint') }}</span>
          <span class="mt-1 block">{{ t('profile.bark.changeVerificationHint') }}</span>
        </template>
        <template #hint>
          <UBadge :color="status.configured ? 'success' : 'neutral'" variant="subtle" size="sm">
            {{ status.configured ? t('profile.bark.configured') : t('profile.bark.notConfigured') }}
          </UBadge>
        </template>
        <UInput
          id="bark-device-key"
          v-model="deviceKey"
          type="password"
          autocomplete="off"
          spellcheck="false"
          :disabled="loading || saving || !!loadError"
          class="w-full sm:w-64"
          :placeholder="t('profile.bark.deviceKeyPlaceholder')"
        />
      </UFormField>
      <USeparator />
      <UFormField
        :label="t('profile.bark.enabled')"
        class="flex items-center justify-between gap-2"
      >
        <USwitch
          v-model="enabled"
          :disabled="loading || saving || !!loadError"
          :aria-label="t('profile.bark.enabled')"
        />
      </UFormField>
    </UPageCard>
    <TotpStepUpDialog :controller="stepUp" :verify="verifyChange" :hint="t('profile.bark.verifyChangeHint')" />
  </section>
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
