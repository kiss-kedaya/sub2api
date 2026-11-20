<template>
  <BaseDialog
    :show="controller.visible.value"
    :title="t('stepUp.title')"
    width="narrow"
    :show-close-button="false"
    :close-on-escape="!verifying"
    :close-on-click-outside="!verifying"
    @close="handleCancel"
  >
    <div class="mb-6 text-center">
      <div class="mx-auto flex size-12 items-center justify-center rounded-full bg-primary/10 ring ring-inset ring-primary/25">
        <UIcon name="i-lucide-lock-keyhole" class="size-6 text-primary" />
      </div>
      <p class="mt-3 text-sm text-muted">
        {{ hint || t('stepUp.hint') }}
      </p>
    </div>

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
        data-testid="totp-stepup-rate-limit"
      >
        {{ t('stepUp.rateLimited', { seconds: remainingSeconds }) }}
      </p>
      <div v-if="verifying" class="mt-3 flex items-center justify-center gap-2 text-sm text-muted">
        <UIcon name="i-lucide-loader-circle" class="size-4 animate-spin" />
        {{ t('common.verifying') }}
      </div>
    </div>

    <UButton
      color="neutral"
      variant="subtle"
      block
      :label="t('common.cancel')"
      :disabled="verifying"
      @click="handleCancel"
    />
  </BaseDialog>
</template>

<script setup lang="ts">
import { ref, watch, nextTick } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore, useAuthStore } from '@/stores'
import { totpAPI } from '@/api'
import TotpCodeInput from '@/components/common/TotpCodeInput.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import { useTotpAttemptLimit } from '@/composables/useTotpAttemptLimit'
import type { StepUpController } from '@/composables/useStepUp'

const props = defineProps<{
  controller: StepUpController
  verify?: (code: string) => Promise<void>
  hint?: string
}>()

const { t } = useI18n()
const appStore = useAppStore()

/** 当前登录账号 key；测试/无 store 环境下退化为当前会话单桶。 */
function currentAccountKey(): string {
  try {
    const store = (useAuthStore as unknown as () => { user?: { id?: number } } | undefined)?.()
    const id = store?.user?.id
    return id ? `user:${id}` : 'user:current'
  } catch {
    return 'user:current'
  }
}

const verifying = ref(false)
const code = ref('')
const codeInput = ref<InstanceType<typeof TotpCodeInput> | null>(null)

// 同一账号 60 秒滚动窗口最多 5 次校验提交；服务端 429 时按 Retry-After 继续禁用。
const { canSubmit, remainingSeconds, recordAttempt, applyServerRetryAfter, reset } =
  useTotpAttemptLimit({ accountKey: currentAccountKey })

// Focus the first cell whenever the dialog opens.
watch(
  () => props.controller.visible.value,
  (open) => {
    if (open) {
      resetInputs()
      nextTick(() => codeInput.value?.focus())
    }
  }
)

async function submit(otp: string) {
  if (!props.controller.visible.value || verifying.value || !canSubmit.value || !/^[0-9]{6}$/.test(otp)) return
  // 本地账号维度计数：60 秒窗口第 6 次提交直接拦下（服务端仍有同样限速）。
  if (!recordAttempt()) return
  verifying.value = true
  try {
    if (props.verify) await props.verify(otp)
    else await totpAPI.stepUp(otp)
    verifying.value = false
    reset()
    resetInputs()
    props.controller.onVerified()
  } catch (err: any) {
    verifying.value = false
    const retryAfterSeconds = Number(err?.retryAfter) || 0
    if (retryAfterSeconds > 0) applyServerRetryAfter(retryAfterSeconds)
    appStore.showError(err?.message || t('stepUp.verifyFailed'))
    resetInputs()
    nextTick(() => {
      if (canSubmit.value) codeInput.value?.focus()
    })
  }
}

function resetInputs() {
  code.value = ''
}

function handleCancel() {
  if (verifying.value) return
  props.controller.onCancel()
}

</script>
