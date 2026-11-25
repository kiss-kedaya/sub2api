<template>
  <!-- 登录第二步（TOTP）：卡片内的一格，不再是盖住整页的弹窗。标题、「第 2 步 / 共 2 步」和「上一步」由登录页给。 -->
  <div class="space-y-3" data-testid="login-totp-pane">
    <!-- 账号在左，「校验中」在右：同一行、固定高度，出现/消失不会把下面的内容顶来顶去，也不多占一行。 -->
    <div class="flex h-5 items-center justify-between gap-3 text-[13px] text-muted">
      <span v-if="userEmailMasked" class="min-w-0 truncate" data-testid="login-factor-account">
        {{ userEmailMasked }}
      </span>
      <span v-else></span>
      <span v-if="verifying" role="status" class="flex shrink-0 items-center gap-1.5">
        <span class="h-3.5 w-3.5 animate-spin rounded-full border-b-2 border-primary-500"></span>
        {{ t('common.verifying') }}
      </span>
    </div>

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
      class="text-center text-sm font-medium text-amber-600 dark:text-amber-400"
      data-testid="totp-login-rate-limit"
    >
      {{ t('profile.totp.loginRateLimited', { seconds: remainingSeconds }) }}
    </p>

    <!-- 登录页把「没绑验证器？返回上一步」的提示放在这里，和输入框之间只隔一个正常间距。 -->
    <slot />
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
