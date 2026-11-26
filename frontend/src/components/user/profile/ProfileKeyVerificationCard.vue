<template>
  <div
    class="overflow-hidden rounded-lg bg-default shadow-xs ring ring-default dark:shadow-none"
    data-test="key-verification-card"
  >
    <div class="border-b border-default px-5 py-4 sm:px-6">
      <h2 class="text-base font-semibold text-highlighted">
        {{ t('profile.keyVerification.title') }}
      </h2>
      <p class="mt-1 text-sm text-muted">
        {{ t('profile.keyVerification.description') }}
      </p>
    </div>

    <div class="space-y-3 p-5 sm:p-6">
      <div v-if="loading" class="flex items-center justify-center py-6" data-test="key-verification-loading">
        <div class="h-8 w-8 animate-spin rounded-full border-b-2 border-primary-500"></div>
      </div>

      <div v-else-if="loadError" role="alert" class="text-sm text-red-600 dark:text-red-400" data-test="key-verification-load-error">
        <p>{{ loadError }}</p>
        <UButton
          type="button"
          color="neutral"
          variant="outline"
          size="sm"
          class="mt-2"
          data-test="key-verification-refresh"
          @click="load"
        >
          {{ t('common.refresh') }}
        </UButton>
      </div>

      <template v-else-if="factors">
        <div class="flex items-start justify-between gap-4">
          <div class="min-w-0">
            <label
              for="key-verification-switch"
              class="text-sm font-medium text-highlighted"
              data-test="key-verification-label"
            >
              {{ t('profile.keyVerification.switchLabel') }}
            </label>
            <p id="key-verification-hint" class="mt-1 text-sm text-muted">
              {{ t('profile.keyVerification.switchHint') }}
            </p>
          </div>
          <USwitch
            id="key-verification-switch"
            :model-value="required"
            :disabled="!configurable || saving"
            :loading="saving"
            size="xl"
            aria-describedby="key-verification-hint key-verification-status"
            data-test="key-verification-switch"
            @update:model-value="onToggle"
          />
        </div>

        <p
          id="key-verification-status"
          role="status"
          aria-live="polite"
          class="text-xs"
          :class="configurable && !required ? 'text-amber-600 dark:text-amber-400' : 'text-muted'"
          data-test="key-verification-status"
        >
          {{ statusText }}
        </p>
      </template>
    </div>

    <!-- 把保护关掉（降低保护强度）要先验证一次身份：与创建 Key 是同一个校验组件，同一套三选一。 -->
    <BaseDialog
      :show="showVerify"
      :title="t('profile.keyVerification.verifyTitle')"
      @close="closeVerify"
    >
      <div class="space-y-3" data-test="key-verification-dialog">
        <p class="text-sm text-muted">{{ t('profile.keyVerification.verifyHint') }}</p>
        <KeyFactorVerification
          ref="verificationRef"
          :factors="factors"
          mode="create"
          hide-description
          :disabled="saving"
        />
        <p
          v-if="verifyError"
          class="text-xs text-red-600 dark:text-red-400"
          role="alert"
          data-test="key-verification-error"
        >
          {{ verifyError }}
        </p>
      </div>
      <template #footer>
        <div class="flex justify-end gap-2">
          <UButton
            color="neutral"
            variant="outline"
            size="lg"
            :disabled="saving"
            data-test="key-verification-cancel"
            @click="closeVerify"
          >
            {{ t('common.cancel') }}
          </UButton>
          <UButton
            color="primary"
            size="lg"
            :loading="saving"
            :disabled="saving"
            data-test="key-verification-confirm"
            @click="confirmOff"
          >
            {{ t('profile.keyVerification.confirmOff') }}
          </UButton>
        </div>
      </template>
    </BaseDialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { keysAPI } from '@/api'
import BaseDialog from '@/components/common/BaseDialog.vue'
import KeyFactorVerification from '@/components/keys/KeyFactorVerification.vue'
import { useAppStore } from '@/stores/app'
import type { ApiKeyCreateVerificationMode, ApiKeyVerificationFactors, ApiKeyVerificationPayload } from '@/types'
import { extractApiErrorCode, extractApiErrorMessage } from '@/utils/apiError'

// 「创建 API Key 时需要验证身份」开关。显示的永远是服务端算好的**有效值**
// （create_verification_required）：默认规则下开了两步验证 / 通行密钥的账号为开，仅有登录密码的账号为关；
// 用户自己改过之后以改后的为准。
//   - 从关拨到开（变严）：写 on，直接保存，不弹校验；
//   - 从开拨到关（降低保护）：先弹出三选一身份校验，通过后才保存（被劫持的会话没法静默把保护关掉）；
//   - 账号没有任何可用因子（configurable=false）：无处可验，开关置灰并说明原因。
// 状态只在服务端确认之后才变化（不做乐观更新），所以界面和真实状态不会错开。
//
// 拨回「不需要」时写什么（见 offMode）：默认本来就不要求校验的账号（没有 TOTP / Passkey）写 auto = 恢复默认，
// 只有默认本来就要求校验的账号才写 off。这样正常使用不会留下潜伏的 off——否则这个账号以后开了 2FA，
// 那个旧 off 会让创建 Key 悄悄不受保护（评审 PR #688）。

const { t } = useI18n()
const appStore = useAppStore()

const loading = ref(true)
const loadError = ref('')
const factors = ref<ApiKeyVerificationFactors | null>(null)
const saving = ref(false)
const showVerify = ref(false)
const verifyError = ref('')
const verificationRef = ref<InstanceType<typeof KeyFactorVerification> | null>(null)
let disposed = false

const required = computed(() => factors.value?.create_verification_required === true)
const configurable = computed(() => factors.value?.create_verification_configurable === true)
// 拨回「不需要」要写的模式：默认就要求校验的账号写 off；其余（含旧服务端没有该字段）写 auto。
const offMode = computed<ApiKeyCreateVerificationMode>(() =>
  factors.value?.create_verification_default_required === true ? 'off' : 'auto',
)

const statusText = computed(() => {
  if (!configurable.value) return t('profile.keyVerification.unavailable')
  return required.value ? t('profile.keyVerification.stateOn') : t('profile.keyVerification.stateOff')
})

async function load() {
  loading.value = true
  loadError.value = ''
  try {
    const next = await keysAPI.getVerificationFactors()
    // 老服务端没有这两个字段（也没有修改接口）：不展示一个点了必然失败的开关。
    if (typeof next.create_verification_configurable !== 'boolean' || typeof next.create_verification_required !== 'boolean') {
      throw new Error('verification switch is not supported by this server')
    }
    factors.value = next
  } catch {
    factors.value = null
    loadError.value = t('profile.keyVerification.loadFailed')
  } finally {
    loading.value = false
  }
}

onMounted(load)
onUnmounted(() => {
  disposed = true
})

async function onToggle(next: boolean) {
  if (saving.value || !configurable.value || next === required.value) return
  if (next) {
    // 升高保护：不需要校验。
    await save('on')
    return
  }
  // 降低保护：先验证身份。
  verifyError.value = ''
  showVerify.value = true
}

function closeVerify() {
  if (saving.value) return
  showVerify.value = false
  verifyError.value = ''
  verificationRef.value?.reset()
}

async function confirmOff() {
  if (saving.value) return
  const payload = verificationRef.value?.buildVerification() ?? null
  // 材料不全时校验组件自己会在里面提示具体缺什么。
  if (!payload) return
  await save(offMode.value, payload)
}

const verificationErrorText = (reason: string): string => {
  switch (reason) {
    case 'VERIFY_PASSWORD_INVALID': return t('keys.factor.passwordInvalid')
    case 'VERIFY_PASSWORD_RATE_LIMITED': return t('keys.factor.passwordRateLimited')
    case 'VERIFY_TOTP_INVALID': return t('keys.factor.totpInvalid')
    case 'VERIFY_PASSKEY_FAILED': return t('keys.factor.passkeyFailed')
    case 'VERIFY_FACTOR_UNAVAILABLE': return t('keys.factor.factorUnavailable')
    case 'VERIFY_REQUIRED': return t('keys.factor.requiredAll')
    default: return ''
  }
}

async function save(mode: ApiKeyCreateVerificationMode, verification?: ApiKeyVerificationPayload) {
  saving.value = true
  verifyError.value = ''
  try {
    factors.value = await keysAPI.setCreateVerificationMode(mode, verification)
    showVerify.value = false
    verificationRef.value?.reset()
    if (!disposed) {
      // 提示按服务端确认后的有效状态说，不按我们请求的模式说（旧服务端 / 默认就要求的账号写 auto 时两者会不同）。
      appStore.showSuccess(t(required.value ? 'profile.keyVerification.savedOn' : 'profile.keyVerification.savedOff'))
    }
  } catch (err: unknown) {
    const reason = extractApiErrorCode(err) ?? ''
    if (reason === 'API_KEY_CREATE_VERIFICATION_CONFLICT' || reason === 'API_KEY_CREATE_VERIFICATION_NOT_CONFIGURABLE') {
      // 服务端状态和界面错开了（并发修改 / 账号已经没有可用因子）：收起对话框、提示原因，
      // 并重新读一次，开关回到真实值。
      showVerify.value = false
      verificationRef.value?.reset()
      if (!disposed) {
        appStore.showError(
          reason === 'API_KEY_CREATE_VERIFICATION_CONFLICT'
            ? t('profile.keyVerification.conflict')
            : t('profile.keyVerification.unavailable'),
        )
      }
      await load()
      return
    }
    const text =
      verificationErrorText(reason) || extractApiErrorMessage(err, t('profile.keyVerification.saveFailed'))
    if (showVerify.value) {
      // 校验材料一次性：失败后清掉已用过的动态码 / Passkey 票据，避免重放；对话框留着让用户重试。
      verifyError.value = text
      verificationRef.value?.reset()
    } else if (!disposed) {
      appStore.showError(text)
    }
  } finally {
    saving.value = false
  }
}
</script>
