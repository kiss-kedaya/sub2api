<template>
  <div class="fixed inset-0 z-50 overflow-y-auto" @click.self="handleClose">
    <div class="flex min-h-full items-center justify-center p-4">
      <div class="fixed inset-0 bg-black/50 transition-opacity" @click="handleClose"></div>

      <div class="relative w-full max-w-md transform rounded-xl bg-white p-6 shadow-xl transition-all dark:bg-dark-800">
        <!-- Header -->
        <div class="mb-6">
          <div class="mx-auto flex h-12 w-12 items-center justify-center rounded-full bg-red-100 dark:bg-red-900/30">
            <svg class="h-6 w-6 text-red-600 dark:text-red-400" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.5">
              <path stroke-linecap="round" stroke-linejoin="round" d="M12 9v3.75m-9.303 3.376c-.866 1.5.217 3.374 1.948 3.374h14.71c1.73 0 2.813-1.874 1.948-3.374L13.949 3.378c-.866-1.5-3.032-1.5-3.898 0L2.697 16.126zM12 15.75h.007v.008H12v-.008z" />
            </svg>
          </div>
          <h3 class="mt-4 text-center text-xl font-semibold text-gray-900 dark:text-white">
            {{ t('profile.totp.disableTitle') }}
          </h3>
          <p class="mt-2 text-center text-sm text-gray-500 dark:text-gray-400">
            {{ t('profile.totp.disableWarning') }}
          </p>
        </div>

        <!-- Loading verification method -->
        <div v-if="methodLoading" class="flex items-center justify-center py-8">
          <div class="animate-spin rounded-full h-8 w-8 border-b-2 border-primary-500"></div>
        </div>

        <form v-else @submit.prevent="handleDisable" class="space-y-4">
          <fieldset v-if="isAdmin" :disabled="loading" class="space-y-2">
            <legend class="input-label">{{ t('profile.totp.disableVerificationMethod') }}</legend>
            <label class="flex items-center gap-2 text-sm text-gray-700 dark:text-gray-300">
              <input v-model="adminVerificationMethod" type="radio" value="totp" name="disable-verification" data-testid="totp-disable-method-authenticator" />
              {{ t('profile.totp.authenticatorCode') }}
            </label>
            <label class="flex items-center gap-2 text-sm text-gray-700 dark:text-gray-300">
              <input v-model="adminVerificationMethod" type="radio" value="server" name="disable-verification" data-testid="totp-disable-method-server" />
              {{ t('profile.totp.useServerAuthorization') }}
            </label>
          </fieldset>

          <div v-if="usesServerAuthorization">
            <label for="disable-server-authorization-code" class="input-label">{{ t('profile.totp.serverAuthorizationCode') }}</label>
            <input
              id="disable-server-authorization-code"
              v-model="form.serverAuthorizationCode"
              type="password"
              maxlength="64"
              pattern="[a-fA-F0-9]{64}"
              autocomplete="off"
              spellcheck="false"
              data-testid="totp-disable-server-code"
              class="input"
              required
              :disabled="loading"
              aria-describedby="disable-server-authorization-hint"
            />
            <p id="disable-server-authorization-hint" class="mt-2 text-sm text-gray-500 dark:text-gray-400">{{ t('profile.totp.disableServerAuthorizationHint') }}</p>
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
            />
            <p id="disable-totp-hint" class="mt-2 text-sm text-gray-500 dark:text-gray-400">{{ t('profile.totp.disableTotpHint') }}</p>
          </div>

          <!-- Actions -->
          <div class="flex justify-end gap-3 pt-4">
            <button type="button" class="btn btn-secondary" @click="handleClose">
              {{ t('common.cancel') }}
            </button>
            <button
              type="submit"
              class="btn btn-danger"
              :disabled="loading || !canSubmit"
            >
              {{ loading ? t('common.processing') : t('profile.totp.confirmDisable') }}
            </button>
          </div>
        </form>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted, onUnmounted, computed, watch, nextTick } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { useAuthStore } from '@/stores/auth'
import { totpAPI } from '@/api'
import type { TotpDisableRequest } from '@/types'
import { extractApiErrorMessage } from '@/utils/apiError'
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
