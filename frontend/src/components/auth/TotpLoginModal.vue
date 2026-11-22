<template>
  <div class="fixed inset-0 z-50 overflow-y-auto">
    <div class="flex min-h-full items-start justify-center p-4 pb-[calc(1rem+env(safe-area-inset-bottom))] sm:items-center">
      <div class="fixed inset-0 bg-black/50 transition-opacity"></div>

      <div class="relative w-full max-w-md transform rounded-xl bg-white p-6 shadow-xl dark:bg-dark-800">
        <!-- Header -->
        <div class="mb-6 text-center">
          <div class="mx-auto flex h-12 w-12 items-center justify-center rounded-full bg-primary-100 dark:bg-primary-900/30">
            <svg class="h-6 w-6 text-primary-600 dark:text-primary-400" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.5">
              <path stroke-linecap="round" stroke-linejoin="round" d="M9 12.75L11.25 15 15 9.75m-3-7.036A11.959 11.959 0 013.598 6 11.99 11.99 0 003 9.749c0 5.592 3.824 10.29 9 11.623 5.176-1.332 9-6.03 9-11.622 0-1.31-.21-2.571-.598-3.751h-.152c-3.196 0-6.1-1.248-8.25-3.285z" />
            </svg>
          </div>
          <h3 class="mt-4 text-xl font-semibold text-gray-900 dark:text-white">
            {{ t('profile.totp.loginTitle') }}
          </h3>
          <p class="mt-2 text-sm text-gray-500 dark:text-gray-400">
            {{ t('profile.totp.loginHint') }}
          </p>
          <p v-if="userEmailMasked" class="mt-1 text-sm font-medium text-gray-700 dark:text-gray-300">
            {{ userEmailMasked }}
          </p>
        </div>

        <!-- Code Input -->
        <div class="mb-6">
          <TotpCodeInput
            ref="codeInput"
            v-model="code"
            :label="t('profile.totp.enterCode')"
            :disabled="verifying || !canSubmit"
            @complete="submit"
          />
          <!-- 账号维度限速：60 秒窗口内第 6 次提交或服务端 429 后禁用并倒计时 -->
          <p
            v-if="remainingSeconds > 0"
            class="mt-3 text-center text-sm font-medium text-amber-600 dark:text-amber-400"
            data-testid="totp-login-rate-limit"
          >
            {{ t('profile.totp.loginRateLimited', { seconds: remainingSeconds }) }}
          </p>
          <!-- Loading indicator -->
          <div v-if="verifying" class="mt-3 flex items-center justify-center gap-2 text-sm text-gray-500">
            <div class="animate-spin rounded-full h-4 w-4 border-b-2 border-primary-500"></div>
            {{ t('common.verifying') }}
          </div>
        </div>

        <!-- Cancel button only -->
        <button
          type="button"
          class="btn btn-secondary w-full"
          :disabled="verifying"
          @click="$emit('cancel')"
        >
          {{ t('common.cancel') }}
        </button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, nextTick } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores'
import TotpCodeInput from '@/components/common/TotpCodeInput.vue'
import { useTotpAttemptLimit } from '@/composables/useTotpAttemptLimit'

const props = defineProps<{
  tempToken: string
  userEmailMasked?: string
  /** 账号维度限速 key（规范化邮箱）；缺省时用脱敏邮箱/临时 token 退化计数。 */
  accountKey?: string
}>()

const emit = defineEmits<{
  verify: [code: string]
  cancel: []
}>()

const { t } = useI18n()
const appStore = useAppStore()

const verifying = ref(false)
const code = ref('')
const codeInput = ref<InstanceType<typeof TotpCodeInput> | null>(null)

const { canSubmit, remainingSeconds, recordAttempt, applyServerRetryAfter, reset } =
  useTotpAttemptLimit({
    accountKey: () => props.accountKey || props.userEmailMasked || props.tempToken
  })

function submit(otp: string) {
  if (verifying.value || !canSubmit.value || !/^[0-9]{6}$/.test(otp)) return
  // 本地账号维度计数：60 秒窗口第 6 次提交直接拦下，不发请求（服务端仍有同样限速）。
  if (!recordAttempt()) return
  verifying.value = true
  emit('verify', otp)
}

defineExpose({
  setVerifying: (value: boolean) => { verifying.value = value },
  // retryAfterSeconds: 服务端 429 的 Retry-After，与本地计数取更大者继续禁用。
  setError: (message: string, retryAfterSeconds = 0) => {
    verifying.value = false
    if (retryAfterSeconds > 0) {
      applyServerRetryAfter(retryAfterSeconds)
    }
    if (message) {
      appStore.showError(message)
    }
    code.value = ''
    nextTick(() => {
      if (canSubmit.value) codeInput.value?.focus()
    })
  },
  resetLimit: () => reset()
})

</script>
