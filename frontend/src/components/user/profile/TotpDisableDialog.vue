<template>
  <BaseDialog
    :show="true"
    :title="t('profile.totp.disableTitle')"
    width="narrow"
    :close-on-click-outside="!loading"
    :close-on-escape="!loading"
    :show-close-button="!loading"
    @close="handleClose"
  >
    <div class="space-y-4">
      <UAlert
        color="error"
        variant="subtle"
        icon="i-lucide-triangle-alert"
        :description="t('profile.totp.disableWarning')"
      />

      <!-- Loading verification method -->
      <div v-if="methodLoading" class="flex justify-center py-6">
        <UIcon name="i-lucide-loader-circle" class="size-6 animate-spin text-dimmed" />
      </div>

      <form v-else id="totp-disable-form" class="space-y-4" @submit.prevent="handleDisable">
        <URadioGroup
          v-if="isAdmin"
          v-model="adminVerificationMethod"
          data-testid="totp-disable-method"
          name="disable-verification"
          :legend="t('profile.totp.disableVerificationMethod')"
          :disabled="loading"
          :items="[
            { value: 'totp', label: t('profile.totp.authenticatorCode') },
            { value: 'server', label: t('profile.totp.useServerAuthorization') }
          ]"
        />

        <UFormField
          v-if="usesServerAuthorization"
          :label="t('profile.totp.serverAuthorizationCode')"
          :help="t('profile.totp.disableServerAuthorizationHint')"
          required
        >
          <UInput
            id="disable-server-authorization-code"
            v-model="form.serverAuthorizationCode"
            type="password"
            maxlength="64"
            pattern="[a-fA-F0-9]{64}"
            autocomplete="off"
            spellcheck="false"
            data-testid="totp-disable-server-code"
            class="w-full"
            required
            :disabled="loading"
          />
        </UFormField>

        <UFormField
          v-else
          :label="t('profile.totp.authenticatorCode')"
          required
        >
          <!-- TotpCodeInput is a shared six-cell input, so its hint is linked explicitly. -->
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
          <template #help>
            <span id="disable-totp-hint">{{ t('profile.totp.disableTotpHint') }}</span>
          </template>
        </UFormField>
      </form>
    </div>

    <template v-if="!methodLoading" #footer>
      <div class="flex justify-end gap-2">
        <UButton data-testid="totp-disable-cancel" color="neutral" variant="subtle" :disabled="loading" @click="handleClose">
          {{ t('common.cancel') }}
        </UButton>
        <UButton
          type="submit"
          form="totp-disable-form"
          color="error"
          :disabled="loading || !canSubmit"
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
