<template>
  <BaseDialog
    :show="controller.visible.value"
    :title="t('stepUp.title')"
    width="narrow"
    :show-close-button="false"
    :close-on-escape="!busy"
    :close-on-click-outside="!busy"
    @close="handleCancel"
  >
    <div class="mb-6 text-center">
      <div class="icon-orb-primary mx-auto flex size-12 items-center justify-center">
        <UIcon :name="phase === 'passkey' ? 'i-lucide-fingerprint' : 'i-lucide-lock-keyhole'" class="size-6 text-primary" />
      </div>
      <p class="mt-3 text-sm text-muted">
        {{ hintText }}
      </p>
      <p v-if="progressText" class="mt-1 text-xs text-muted" data-testid="stepup-factor-progress">
        {{ progressText }}
      </p>
    </div>

    <!-- TOTP 因子 -->
    <div v-if="phase === 'totp'" class="mb-6">
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

    <!-- Passkey 因子 -->
    <div v-else class="mb-6">
      <div
        v-if="!passkeySupported"
        class="rounded-lg bg-amber-50 p-3 text-sm text-amber-700 dark:bg-amber-900/20 dark:text-amber-300"
        data-testid="stepup-passkey-unsupported"
      >
        {{ t('stepUp.passkeyUnsupported') }}
      </div>
      <div v-else class="space-y-3">
        <div
          v-if="passkeyError"
          class="rounded-lg bg-red-50 p-3 text-sm text-red-700 dark:bg-red-900/20 dark:text-red-300"
          data-testid="stepup-passkey-error"
        >
          {{ passkeyError }}
        </div>
        <button
          type="button"
          class="btn btn-primary w-full"
          :disabled="verifying || !passkeySupported"
          data-testid="stepup-passkey-verify"
          @click="submitPasskey"
        >
          {{ verifying ? t('stepUp.passkeyVerifying') : t('stepUp.passkeyVerify') }}
        </button>
      </div>
    </div>

    <UButton
      color="neutral"
      variant="subtle"
      block
      :label="t('common.cancel')"
      :disabled="busy"
      @click="handleCancel"
    />
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, ref, watch, nextTick } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore, useAuthStore } from '@/stores'
import { totpAPI } from '@/api'
import { passkeyAPI } from '@/api/passkey'
import TotpCodeInput from '@/components/common/TotpCodeInput.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import { useTotpAttemptLimit } from '@/composables/useTotpAttemptLimit'
import type { StepUpController } from '@/composables/useStepUp'

type StepUpFactor = 'totp' | 'passkey'

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
const phase = ref<StepUpFactor>('totp')
const requiredFactors = ref<StepUpFactor[]>(['totp'])
const passkeySupported = ref(false)
const passkeyError = ref('')

const activeFactors = computed<StepUpFactor[]>(() => {
  const factors = requiredFactors.value.filter((factor) => factor === 'totp' || factor === 'passkey')
  return factors.length ? factors : ['totp']
})
const busy = computed(() => verifying.value)
const hintText = computed(() => {
  if (props.hint) return props.hint
  if (phase.value === 'passkey') return t('stepUp.passkeyHint')
  if (activeFactors.value.includes('passkey')) return t('stepUp.totpThenPasskeyHint')
  return t('stepUp.hint')
})
const progressText = computed(() => {
  if (activeFactors.value.length < 2) return ''
  if (phase.value === 'passkey') return t('stepUp.totpVerifiedContinuePasskey')
  return t('stepUp.twoFactorProgress', { current: t('stepUp.factorTotp'), next: t('stepUp.factorPasskey') })
})

// 同一账号 60 秒滚动窗口最多 5 次校验提交；服务端 429 时按 Retry-After 继续禁用。
const { canSubmit, remainingSeconds, recordAttempt, applyServerRetryAfter, reset } =
  useTotpAttemptLimit({ accountKey: currentAccountKey })

// Focus the first cell whenever the dialog opens.
watch(
  () => props.controller.visible.value,
  (open) => {
    if (open) {
      resetInputs()
      passkeyError.value = ''
      passkeySupported.value = passkeyAPI.isSupported()
      void loadPolicy()
    }
  }
)

/**
 * 读取当前账号必须完成的因子，决定弹窗阶段：
 * 只绑 TOTP → TOTP；只绑 Passkey → Passkey；两者都绑 → 先 TOTP 后 Passkey。
 * 读取失败时按 TOTP 兜底（与旧行为一致），不因策略接口抖动卡死用户。
 */
async function loadPolicy(): Promise<void> {
  let factors: StepUpFactor[] = ['totp']
  try {
    const policy = await totpAPI.getTwoFactorPolicy()
    const fromServer = (policy.required_factors || []).filter(
      (factor): factor is StepUpFactor => factor === 'totp' || factor === 'passkey'
    )
    if (!policy.step_up_enabled) {
      // 全局门控关闭时不存在 step-up 弹窗场景；防御性按 TOTP 兜底。
      factors = ['totp']
    } else if (fromServer.length > 0) {
      factors = fromServer
    } else {
      factors = ['totp']
    }
  } catch {
    factors = ['totp']
  }
  requiredFactors.value = factors
  phase.value = factors.includes('totp') ? 'totp' : 'passkey'
  if (phase.value === 'totp') {
    nextTick(() => codeInput.value?.focus())
  }
}

async function submit(otp: string) {
  if (phase.value !== 'totp' || !props.controller.visible.value || verifying.value || !canSubmit.value) return
  if (!/^[0-9]{6}$/.test(otp)) return
  // 本地账号维度计数：60 秒窗口第 6 次提交直接拦下（服务端仍有同样限速）。
  if (!recordAttempt()) return
  verifying.value = true
  try {
    if (props.verify) await props.verify(otp)
    else await totpAPI.stepUp(otp)
    reset()
    resetInputs()
    // 两者都绑定时 TOTP 只是第一个因子：继续 Passkey 阶段，全部通过才重试原请求。
    if (activeFactors.value.includes('passkey')) {
      phase.value = 'passkey'
      verifying.value = false
      return
    }
    verifying.value = false
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

/** Passkey 阶段：浏览器断言成功后，两个因子才算全部通过。 */
async function submitPasskey() {
  if (phase.value !== 'passkey' || verifying.value) return
  if (!passkeySupported.value) {
    passkeyError.value = t('stepUp.passkeyUnsupported')
    return
  }
  verifying.value = true
  passkeyError.value = ''
  try {
    await passkeyAPI.stepUp()
    verifying.value = false
    props.controller.onVerified()
  } catch (err: any) {
    verifying.value = false
    passkeyError.value =
      err instanceof DOMException && err.name === 'NotAllowedError'
        ? t('stepUp.passkeyCancelled')
        : err?.message || t('stepUp.verifyFailed')
    appStore.showError(passkeyError.value)
  }
}

function resetInputs() {
  code.value = ''
  verifying.value = false
  passkeyError.value = ''
}

function handleCancel() {
  if (verifying.value) return
  props.controller.onCancel()
}
</script>
