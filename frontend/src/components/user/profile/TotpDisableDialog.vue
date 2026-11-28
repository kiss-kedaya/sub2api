<template>
  <BaseDialog
    :show="true"
    :title="t('profile.totp.disableTitle')"
    :description="t('profile.totp.disableWarning')"
    width="narrow"
    :close-on-click-outside="!loading"
    :close-on-escape="!loading"
    @close="handleClose"
  >
    <div class="space-y-4">
      <div class="mx-auto flex h-12 w-12 items-center justify-center rounded-full bg-error/10">
        <svg class="h-6 w-6 text-error" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.5">
          <path stroke-linecap="round" stroke-linejoin="round" d="M12 9v3.75m-9.303 3.376c-.866 1.5.217 3.374 1.948 3.374h14.71c1.73 0 2.813-1.874 1.948-3.374L13.949 3.378c-.866-1.5-3.032-1.5-3.898 0L2.697 16.126zM12 15.75h.007v.008H12v-.008z" />
        </svg>
      </div>

      <!-- Loading verification method -->
      <div v-if="methodLoading" class="flex items-center justify-center py-8">
        <div class="animate-spin rounded-full h-8 w-8 border-b-2 border-primary-500"></div>
      </div>

      <form v-else id="totp-disable-form" @submit.prevent="handleDisable" class="space-y-4">
        <fieldset v-if="isAdmin" :disabled="loading" class="space-y-2">
          <legend class="input-label">{{ t('profile.totp.disableVerificationMethod') }}</legend>
          <URadioGroup
            v-model="adminVerificationMethod"
            :items="verificationMethodItems"
            :disabled="loading"
            :ui="{ item: 'min-h-11 items-center' }"
          >
            <template #label="{ item }">
              <span :data-testid="item.value === 'totp' ? 'totp-disable-method-authenticator' : 'totp-disable-method-server'">
                {{ item.label }}
              </span>
            </template>
          </URadioGroup>
        </fieldset>

        <div v-if="usesServerAuthorization">
          <label for="disable-server-authorization-code" class="input-label">{{ t('profile.totp.serverAuthorizationCode') }}</label>
          <UInput
            id="disable-server-authorization-code"
            v-model="form.serverAuthorizationCode"
            type="password"
            maxlength="64"
            pattern="[a-fA-F0-9]{64}"
            autocomplete="off"
            :spellcheck="false"
            data-testid="totp-disable-server-code"
            class="w-full"
            required
            :disabled="loading"
            aria-describedby="disable-server-authorization-hint"
          />
          <p id="disable-server-authorization-hint" class="mt-2 text-sm text-muted">{{ t('profile.totp.disableServerAuthorizationHint') }}</p>
        </div>

        <div v-else>
          <label for="disable-totp-code" class="input-label">{{ t('profile.totp.authenticatorCode') }}</label>
          <TotpCodeInput
            id="disable-totp-code"
            ref="codeInput"
            v-model="form.totpCode"
            :label="t('profile.totp.authenticatorCode')"
            aria-describedby="disable-totp-hint"
            data-testid="totp-disable-code"
            required
            :disabled="loading || methodLoading"
            @complete="handleDisable"
          />
          <p id="disable-totp-hint" class="mt-2 text-sm text-muted">{{ t('profile.totp.disableTotpHint') }}</p>
        </div>
      </form>
    </div>

    <template #footer>
      <div class="flex justify-end gap-3">
        <UButton type="button" color="neutral" variant="outline" :disabled="loading" data-testid="totp-disable-cancel" @click="handleClose">
          {{ t('common.cancel') }}
        </UButton>
        <UButton
          v-if="!methodLoading"
          type="submit"
          form="totp-disable-form"
          color="error"
          :disabled="loading || !canSubmit"
          :loading="loading"
        >
          {{ loading ? t('common.processing') : t('profile.totp.confirmDisable') }}
        </UButton>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { ref, onMounted, onUnmounted, computed, watch, nextTick } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { useAuthStore } from '@/stores/auth'
import { totpAPI } from '@/api'
import type { TotpDisableRequest } from '@/types'
import { extractApiErrorMessage } from '@/utils/apiError'
import BaseDialog from '@/components/common/BaseDialog.vue'
import TotpCodeInput from '@/components/common/TotpCodeInput.vue'

const emit = defineEmits<{
  close: []
  success: []
}>()

const { t } = useI18n()
const appStore = useAppStore()
const authStore = useAuthStore()
const isAdmin = computed(() => authStore.user?.role === 'admin')

const methodLoading = ref(true)
const adminVerificationMethod = ref<'totp' | 'server'>('totp')
const verificationMethodItems = computed(() => [
  { value: 'totp', label: t('profile.totp.authenticatorCode') },
  { value: 'server', label: t('profile.totp.useServerAuthorization') }
])
const usesServerAuthorization = computed(() => isAdmin.value && adminVerificationMethod.value === 'server')
const loading = ref(false)
const codeInput = ref<InstanceType<typeof TotpCodeInput> | null>(null)
const form = ref({
  totpCode: '',
  serverAuthorizationCode: ''
})

watch(isAdmin, () => {
  adminVerificationMethod.value = 'totp'
  clearForm()
})

watch(adminVerificationMethod, async () => {
  form.value.totpCode = ''
  form.value.serverAuthorizationCode = ''
  if (!usesServerAuthorization.value) {
    await nextTick()
    codeInput.value?.focus()
  }
})

const canSubmit = computed(() => {
  if (methodLoading.value) return false
  if (usesServerAuthorization.value) return /^[a-fA-F0-9]{64}$/.test(form.value.serverAuthorizationCode.trim())
  return /^[0-9]{6}$/.test(form.value.totpCode)
})

function clearForm() {
  form.value = { totpCode: '', serverAuthorizationCode: '' }
}

function handleClose() {
  if (loading.value) return
  clearForm()
  emit('close')
}

const loadVerificationMethod = async () => {
  methodLoading.value = true
  try {
    const method = await totpAPI.getVerificationMethod('disable')
    if (!['email', 'password', 'totp'].includes(method.method)) throw new Error(t('profile.totp.disableFailed'))
  } catch (err: any) {
    appStore.showError(extractApiErrorMessage(err, t('common.error')))
    emit('close')
  } finally {
    methodLoading.value = false
  }
}

const handleDisable = async () => {
  if (loading.value || !canSubmit.value) return

  loading.value = true

  try {
    const request: TotpDisableRequest = usesServerAuthorization.value
      ? { server_authorization_code: form.value.serverAuthorizationCode.trim() }
      : { totp_code: form.value.totpCode }

    await totpAPI.disable(request)
    appStore.showSuccess(t('profile.totp.disableSuccess'))
    emit('success')
  } catch (err: any) {
    appStore.showError(extractApiErrorMessage(err, t('profile.totp.disableFailed')))
  } finally {
    clearForm()
    loading.value = false
    await nextTick()
    codeInput.value?.focus()
  }
}

onMounted(() => {
  loadVerificationMethod()
})

onUnmounted(() => {
  clearForm()
})
</script>
