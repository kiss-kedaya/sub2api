<template>
  <BaseDialog
    :show="true"
    :title="t('profile.totp.setupTitle')"
    :description="stepDescription"
    width="narrow"
    @close="$emit('close')"
  >
    <!-- Step 0: Identity Verification -->
    <div v-if="step === 0" class="space-y-6">
      <!-- Loading verification method -->
      <div v-if="methodLoading" class="flex items-center justify-center py-8">
        <div class="animate-spin rounded-full h-8 w-8 border-b-2 border-primary-500"></div>
      </div>

      <template v-else>
        <!-- Email verification -->
        <div v-if="verificationMethod === 'email'" class="space-y-4">
          <div>
            <label class="input-label">{{ t('profile.totp.emailCode') }}</label>
            <div class="flex gap-2">
              <UInput
                v-model="verifyForm.emailCode"
                type="text"
                maxlength="6"
                inputmode="numeric"
                class="flex-1"
                :placeholder="t('profile.totp.enterEmailCode')"
              />
              <UButton
                type="button"
                color="neutral"
                variant="outline"
                class="whitespace-nowrap tabular-nums"
                :disabled="sendingCode || codeCooldown > 0"
                @click="handleSendCode"
              >
                {{ codeCooldown > 0 ? `${codeCooldown}s` : (sendingCode ? t('common.sending') : t('profile.totp.sendCode')) }}
              </UButton>
            </div>
          </div>
        </div>

        <!-- Password verification -->
        <div v-else-if="verificationMethod === 'password'" class="space-y-4">
          <div>
            <label class="input-label">{{ t('profile.currentPassword') }}</label>
            <UInput
              v-model="verifyForm.password"
              type="password"
              autocomplete="current-password"
              class="w-full"
              :placeholder="t('profile.totp.enterPassword')"
            />
          </div>
        </div>
      </template>
    </div>

    <!-- Step 1: Show QR Code -->
    <div v-if="step === 1" class="space-y-6">
      <!-- QR Code and Secret -->
      <template v-if="setupData">
        <div class="flex justify-center">
          <div class="rounded-lg border border-default p-4 bg-white">
            <img :src="qrCodeDataUrl" alt="QR Code" class="h-48 w-48" />
          </div>
        </div>

        <div class="text-center">
          <p class="text-sm text-muted mb-2">
            {{ t('profile.totp.manualEntry') }}
          </p>
          <div class="flex items-center justify-center gap-2">
            <code class="rounded-md bg-elevated px-3 py-2 font-mono text-sm tabular-nums text-highlighted wrap-anywhere">
              {{ setupData.secret }}
            </code>
            <UButton
              type="button"
              color="neutral"
              variant="ghost"
              size="sm"
              icon="i-lucide-copy"
              class="relative min-h-11 min-w-11 sm:min-h-0 sm:min-w-0 justify-center"
              data-testid="totp-setup-copy"
              @click="copySecret"
            />
          </div>
        </div>
      </template>
    </div>

    <!-- Step 2: Verify Code -->
    <div v-if="step === 2" class="space-y-6">
      <form id="totp-setup-verify-form" @submit.prevent="handleVerify">
        <div class="mb-2">
          <label class="input-label text-center block mb-3">
            {{ t('profile.totp.enterCode') }}
          </label>
          <TotpCodeInput
            ref="codeInput"
            v-model="code"
            :label="t('profile.totp.enterCode')"
            :disabled="verifying"
            @complete="handleVerify"
          />
        </div>
      </form>
    </div>

    <template #footer>
      <div v-if="step === 0 && !methodLoading" class="flex justify-end gap-3">
        <UButton type="button" color="neutral" variant="outline" data-testid="totp-setup-cancel" @click="$emit('close')">
          {{ t('common.cancel') }}
        </UButton>
        <UButton
          type="button"
          data-testid="totp-setup-next"
          :disabled="!canProceedFromVerify || setupLoading"
          :loading="setupLoading"
          @click="handleVerifyAndSetup"
        >
          {{ setupLoading ? t('common.loading') : t('common.next') }}
        </UButton>
      </div>
      <div v-else-if="step === 1" class="flex justify-end gap-3">
        <UButton type="button" color="neutral" variant="outline" data-testid="totp-setup-cancel" @click="$emit('close')">
          {{ t('common.cancel') }}
        </UButton>
        <UButton
          type="button"
          data-testid="totp-setup-next"
          :disabled="!setupData"
          @click="step = 2"
        >
          {{ t('common.next') }}
        </UButton>
      </div>
      <div v-else-if="step === 2" class="flex justify-end gap-3">
        <UButton type="button" color="neutral" variant="outline" data-testid="totp-setup-back" :disabled="verifying" @click="step = 1">
          {{ t('common.back') }}
        </UButton>
        <UButton
          type="submit"
          form="totp-setup-verify-form"
          data-testid="totp-setup-verify"
          :disabled="verifying || code.length !== 6"
          :loading="verifying"
        >
          {{ verifying ? t('common.verifying') : t('profile.totp.verify') }}
        </UButton>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { ref, onMounted, onUnmounted, nextTick, watch, computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { totpAPI } from '@/api'
import { extractApiErrorMessage } from '@/utils/apiError'
import type { TotpSetupResponse } from '@/types'
import QRCode from 'qrcode'
import BaseDialog from '@/components/common/BaseDialog.vue'
import TotpCodeInput from '@/components/common/TotpCodeInput.vue'

const emit = defineEmits<{
  close: []
  success: []
}>()

const { t } = useI18n()
const appStore = useAppStore()

// Step: 0 = verify identity, 1 = QR code, 2 = verify TOTP code
const step = ref(0)
const methodLoading = ref(true)
const verificationMethod = ref<'email' | 'password' | null>(null)
const verifyForm = ref({ emailCode: '', password: '' })
const sendingCode = ref(false)
const codeCooldown = ref(0)
const cooldownTimer = ref<ReturnType<typeof setInterval> | null>(null)

const setupLoading = ref(false)
const setupData = ref<TotpSetupResponse | null>(null)
const verifying = ref(false)
const code = ref('')
const codeInput = ref<InstanceType<typeof TotpCodeInput> | null>(null)
const qrCodeDataUrl = ref('')

const stepDescription = computed(() => {
  switch (step.value) {
    case 0:
      return verificationMethod.value === 'email'
        ? t('profile.totp.verifyEmailFirst')
        : t('profile.totp.verifyPasswordFirst')
    case 1:
      return t('profile.totp.setupStep1')
    case 2:
      return t('profile.totp.setupStep2')
    default:
      return ''
  }
})

const canProceedFromVerify = computed(() => {
  if (methodLoading.value) return false
  if (verificationMethod.value === 'email') {
    return verifyForm.value.emailCode.length === 6
  }
  return verificationMethod.value === 'password' && verifyForm.value.password.length > 0
})

// Generate QR code as base64 when setupData changes
watch(
  () => setupData.value?.qr_code_url,
  async (url) => {
    if (url) {
      try {
        qrCodeDataUrl.value = await QRCode.toDataURL(url, {
          width: 200,
          margin: 2,
          color: {
            dark: '#000000',
            light: '#ffffff'
          }
        })
      } catch (err) {
        console.error('Failed to generate QR code:', err)
      }
    }
  },
  { immediate: true }
)

const copySecret = async () => {
  if (setupData.value) {
    try {
      await navigator.clipboard.writeText(setupData.value.secret)
      appStore.showSuccess(t('common.copied'))
    } catch {
      appStore.showError(t('common.copyFailed'))
    }
  }
}

const loadVerificationMethod = async () => {
  methodLoading.value = true
  try {
    const method = await totpAPI.getVerificationMethod('enable')
    if (method.method !== 'email' && method.method !== 'password') throw new Error(t('profile.totp.setupFailed'))
    verificationMethod.value = method.method
  } catch (err: any) {
    appStore.showError(extractApiErrorMessage(err, t('common.error')))
    emit('close')
  } finally {
    methodLoading.value = false
  }
}

const handleSendCode = async () => {
  if (verificationMethod.value !== 'email') return
  sendingCode.value = true
  try {
    await totpAPI.sendVerifyCode()
    appStore.showSuccess(t('profile.totp.codeSent'))
    // Start cooldown
    codeCooldown.value = 60
    if (cooldownTimer.value) {
      clearInterval(cooldownTimer.value)
      cooldownTimer.value = null
    }
    cooldownTimer.value = setInterval(() => {
      codeCooldown.value--
      if (codeCooldown.value <= 0) {
        if (cooldownTimer.value) {
          clearInterval(cooldownTimer.value)
          cooldownTimer.value = null
        }
      }
    }, 1000)
  } catch (err: any) {
    appStore.showError(extractApiErrorMessage(err, t('profile.totp.sendCodeFailed')))
  } finally {
    sendingCode.value = false
  }
}

const handleVerifyAndSetup = async () => {
  if (!canProceedFromVerify.value || setupLoading.value) return
  setupLoading.value = true

  try {
    const request = verificationMethod.value === 'email'
      ? { email_code: verifyForm.value.emailCode }
      : { password: verifyForm.value.password }

    setupData.value = await totpAPI.initiateSetup(request)
    step.value = 1
  } catch (err: any) {
    appStore.showError(extractApiErrorMessage(err, t('profile.totp.setupFailed')))
  } finally {
    setupLoading.value = false
  }
}

const handleVerify = async () => {
  const totpCode = code.value
  if (verifying.value || step.value !== 2 || !/^[0-9]{6}$/.test(totpCode) || !setupData.value) return

  verifying.value = true

  try {
    await totpAPI.enable({
      totp_code: totpCode,
      setup_token: setupData.value.setup_token
    })
    appStore.showSuccess(t('profile.totp.enableSuccess'))
    emit('success')
  } catch (err: any) {
    appStore.showError(extractApiErrorMessage(err, t('profile.totp.verifyFailed')))
    code.value = ''
    nextTick(() => {
      codeInput.value?.focus()
    })
  } finally {
    code.value = ''
    verifying.value = false
  }
}

onMounted(() => {
  loadVerificationMethod()
})

onUnmounted(() => {
  if (cooldownTimer.value) {
    clearInterval(cooldownTimer.value)
    cooldownTimer.value = null
  }
})
</script>
