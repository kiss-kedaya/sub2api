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
    </div>

    <!-- TOTP 因子 -->
    <div v-if="phase === 'totp'" class="mb-6">
      <TotpCodeInput
        ref="codeInput"
        v-model="code"
        :label="t('profile.totp.enterCode')"
        :disabled="verifying || !canSubmit"
        @complete="submit"
        @keydown.enter.prevent="submitCode"
      />
      <!-- 账号维度限速：60 秒窗口内第 6 次提交或服务端 429 后禁用并倒计时 -->
      <p
        v-if="remainingSeconds > 0"
        class="mt-3 text-center text-sm font-medium text-amber-600 dark:text-amber-400"
        data-testid="totp-stepup-rate-limit"
      >
        {{ t('stepUp.rateLimited', { seconds: remainingSeconds }) }}
      </p>
      <p
        v-else-if="attemptsRemainingText"
        class="mt-3 text-center text-sm text-muted"
        data-testid="stepup-attempts-remaining"
      >
        {{ attemptsRemainingText }}
      </p>
      <!-- 永远展示确认按钮：输满 6 位自动提交之外，保留可点击/可回车提交的入口。 -->
      <UButton
        class="mt-3"
        color="primary"
        block
        :label="t('stepUp.verify')"
        data-testid="stepup-totp-submit"
        :disabled="!canSubmitTotp"
        :loading="verifying"
        @click="submitCode"
      />
      <UButton
        v-if="passkeyOptionVisible"
        class="mt-2"
        color="neutral"
        variant="subtle"
        block
        :label="t('stepUp.usePasskey')"
        data-testid="stepup-use-passkey"
        :disabled="verifying"
        @click="switchFactor('passkey')"
      />
    </div>

    <!-- Passkey 因子 -->
    <div v-else class="mb-6">
      <div class="space-y-3">
        <div
          v-if="!passkeySupported"
          class="rounded-lg bg-amber-50 p-3 text-sm text-amber-700 dark:bg-amber-900/20 dark:text-amber-300"
          data-testid="stepup-passkey-unsupported"
        >
          {{ t('stepUp.passkeyUnsupported') }}
        </div>
        <div
          v-else-if="passkeyError"
          class="rounded-lg bg-red-50 p-3 text-sm text-red-700 dark:bg-red-900/20 dark:text-red-300"
          data-testid="stepup-passkey-error"
        >
          {{ passkeyError }}
        </div>
        <!-- 确认按钮永远存在：不支持/等待时置灰，而不是只剩「取消」。 -->
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
      <UButton
        v-if="totpOptionVisible"
        class="mt-2"
        color="neutral"
        variant="subtle"
        block
        :label="t('stepUp.useTotp')"
        data-testid="stepup-use-totp"
        :disabled="verifying"
        @click="switchFactor('totp')"
      />
    </div>

    <UButton
      color="neutral"
      variant="subtle"
      block
      :label="t('common.cancel')"
      data-testid="stepup-cancel"
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
/** 账号已绑定的因子（服务端 required_factors）。step-up 任选其一即可，不要求逐个通过。 */
const requiredFactors = ref<StepUpFactor[]>(['totp'])
/** 服务端声明 step_up_mode=any：已绑定因子里验证任意一个就放行。 */
const anyOneFactor = ref(false)
const attemptFeedback = ref(false)
const passkeySupported = ref(false)
const passkeyError = ref('')

const activeFactors = computed<StepUpFactor[]>(() => {
  const factors = requiredFactors.value.filter((factor) => factor === 'totp' || factor === 'passkey')
  return factors.length ? factors : ['totp']
})
const bothFactorsBound = computed(
  () => activeFactors.value.includes('totp') && activeFactors.value.includes('passkey')
)
const busy = computed(() => verifying.value)
const hintText = computed(() => {
  if (props.hint) return props.hint
  // 只有服务端明说「任选其一」才这么提示：旧后端不认这个字段，不能对用户许诺它不兑现的规则。
  if (bothFactorsBound.value && anyOneFactor.value) return t('stepUp.anyOneFactorHint')
  if (phase.value === 'passkey') return t('stepUp.passkeyHint')
  return t('stepUp.hint')
})

/** 显式「验证」按钮：TOTP 必须满 6 位、未在验证中且未被限速才可点。 */
const canSubmitTotp = computed(
  () => phase.value === 'totp' && !verifying.value && canSubmit.value && /^[0-9]{6}$/.test(code.value)
)
/** 绑定 Passkey 的账号在 TOTP 阶段并列展示「用 Passkey 验证」（任选其一）。 */
const passkeyOptionVisible = computed(() => phase.value === 'totp' && activeFactors.value.includes('passkey'))
/** 双因子账号在 Passkey 阶段仍可用「用 TOTP 验证」切回动态码（Passkey 失败/取消后也不会被卡住）。 */
const totpOptionVisible = computed(() => phase.value === 'passkey' && activeFactors.value.includes('totp'))
const attemptsRemainingText = computed(() => {
  if (!attemptFeedback.value || remainingSeconds.value > 0 || remainingAttempts.value <= 0) return ''
  return t('stepUp.attemptsRemaining', { count: remainingAttempts.value })
})

// 同一账号 60 秒滚动窗口最多 5 次校验提交；服务端 429 时按 Retry-After 继续禁用。
const { canSubmit, remainingSeconds, remainingAttempts, recordAttempt, applyServerRetryAfter, reset } =
  useTotpAttemptLimit({ accountKey: currentAccountKey })

// Focus the first cell whenever the dialog opens.
watch(
  () => props.controller.visible.value,
  (open) => {
    if (open) {
      resetState()
      passkeySupported.value = passkeyAPI.isSupported()
      void loadPolicy()
    }
  }
)

/**
 * 读取当前账号已绑定的因子，决定弹窗展示哪些验证方式：
 * 只绑 TOTP → TOTP；只绑 Passkey → Passkey；两者都绑 → 并列展示 TOTP 输入框与
 * 「用 Passkey 验证」，验证其中任意一个就完成 step-up（服务端 step_up_mode=any），默认先 TOTP。
 * 读取失败时按 TOTP 兜底（与旧行为一致），不因策略接口抖动卡死用户。
 */
async function loadPolicy(): Promise<void> {
  let factors: StepUpFactor[] = ['totp']
  let anyOne = false
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
      anyOne = policy.step_up_mode === 'any'
    } else {
      factors = ['totp']
    }
  } catch {
    factors = ['totp']
  }
  requiredFactors.value = factors
  anyOneFactor.value = anyOne
  phase.value = factors.includes('totp') ? 'totp' : 'passkey'
  if (phase.value === 'totp') {
    nextTick(() => codeInput.value?.focus())
  }
}

/** 重置整次挑战状态（弹窗每次打开时调用）。 */
function resetState(): void {
  code.value = ''
  verifying.value = false
  attemptFeedback.value = false
  passkeyError.value = ''
}

/** 手动切换当前要验证的因子（两个因子并列可选，哪个失败都不影响改用另一个）。 */
function switchFactor(factor: StepUpFactor): void {
  if (verifying.value || phase.value === factor) return
  if (!activeFactors.value.includes(factor)) return
  phase.value = factor
  passkeyError.value = ''
  attemptFeedback.value = false
  if (factor === 'totp') nextTick(() => codeInput.value?.focus())
}

/** 「验证」按钮 / Enter 键入口：语义等同输满 6 位后的自动提交。 */
function submitCode(): void {
  void submit(code.value)
}

async function submit(otp: string) {
  if (phase.value !== 'totp' || !props.controller.visible.value || verifying.value || !canSubmit.value) return
  if (!/^[0-9]{6}$/.test(otp)) return
  // 本地账号维度计数：60 秒窗口第 6 次提交直接拦下（服务端仍有同样限速）。
  if (!recordAttempt()) return
  verifying.value = true
  attemptFeedback.value = false
  try {
    if (props.verify) await props.verify(otp)
    else await totpAPI.stepUp(otp)
    reset()
    resetInputs()
    verifying.value = false
    // 任选其一：TOTP 通过就够了，直接放行原请求（两者都绑定时也不再要求继续验 Passkey）。
    props.controller.onVerified()
  } catch (err: any) {
    verifying.value = false
    attemptFeedback.value = true
    const retryAfterSeconds = Number(err?.retryAfter) || 0
    if (retryAfterSeconds > 0) applyServerRetryAfter(retryAfterSeconds)
    appStore.showError(err?.message || t('stepUp.verifyFailed'))
    resetInputs()
    nextTick(() => {
      if (canSubmit.value) codeInput.value?.focus()
    })
  }
}

/** Passkey 阶段：浏览器断言通过就放行原请求（任选其一，不再要求另一个因子）。 */
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
}

function handleCancel() {
  if (verifying.value) return
  props.controller.onCancel()
}
</script>
