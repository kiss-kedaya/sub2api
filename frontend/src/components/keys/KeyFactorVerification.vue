<template>
  <div class="space-y-4" data-test="key-factor-verification">
    <p class="text-sm text-muted">{{ t('keys.factor.description') }}</p>

    <fieldset v-if="availableMethods.length > 0">
      <legend class="mb-2 text-sm font-medium text-default">{{ t('keys.factor.methodLabel') }}</legend>
      <URadioGroup
        v-model="method"
        :items="methodItems"
        orientation="horizontal"
        :disabled="disabled || loading"
        data-test="key-factor-methods"
      />
    </fieldset>

    <!-- 登录密码：创建时只做身份校验；二次查看时同一输入还负责本地解密。 -->
    <div v-if="method === 'password'" class="space-y-1.5" data-test="factor-password">
      <label for="key-factor-password" class="block text-sm font-medium text-default">
        {{ mode === 'reveal' ? t('keys.factor.decryptPasswordLabel') : t('keys.factor.passwordLabel') }}
      </label>
      <UInput
        id="key-factor-password"
        v-model="password"
        type="password"
        name="key-factor-password"
        autocomplete="new-password"
        data-lpignore="true"
        data-1p-ignore="true"
        :disabled="disabled || loading"
        class="w-full"
        :placeholder="t('keys.factor.passwordPlaceholder')"
        data-test="key-factor-password-input"
      />
      <p class="text-xs text-muted">{{ t('keys.factor.passwordHint') }}</p>
    </div>

    <!-- TOTP 动态验证码。 -->
    <div v-else-if="method === 'totp'" class="space-y-1.5" data-test="factor-totp">
      <label for="key-factor-totp" class="block text-sm font-medium text-default">
        {{ t('keys.factor.totpLabel') }}
      </label>
      <UInput
        id="key-factor-totp"
        v-model="totpCode"
        type="text"
        name="key-factor-totp"
        inputmode="numeric"
        maxlength="6"
        autocomplete="one-time-code"
        data-lpignore="true"
        data-1p-ignore="true"
        :disabled="disabled || loading"
        class="w-full max-w-48"
        :placeholder="t('keys.factor.totpPlaceholder')"
        data-test="key-factor-totp-input"
      />
    </div>

    <!-- Passkey：先完成一次 assertion，换取 5 分钟一次性票据。 -->
    <div v-else-if="method === 'passkey'" class="space-y-2" data-test="factor-passkey">
      <p class="text-xs text-muted">{{ t('keys.factor.passkeyHint') }}</p>
      <div class="flex flex-wrap items-center gap-2">
        <UButton
          type="button"
          color="neutral"
          variant="outline"
          icon="i-lucide-fingerprint"
          :loading="passkeyBusy"
          :disabled="disabled || loading"
          data-test="key-factor-passkey-verify"
          @click="verifyPasskey"
        >
          {{ passkeyToken ? t('keys.factor.passkeyReverify') : t('keys.factor.passkeyVerify') }}
        </UButton>
        <UBadge
          v-if="passkeyToken"
          color="success"
          variant="subtle"
          data-test="key-factor-passkey-verified"
        >
          {{ t('keys.factor.passkeyVerified', { seconds: passkeyRemaining }) }}
        </UBadge>
      </div>
    </div>

    <!-- 二次查看除了身份校验，还要用登录密码在本地解密；TOTP/Passkey 时单独输入。 -->
    <div
      v-if="mode === 'reveal' && method !== 'password'"
      class="space-y-1.5"
      data-test="factor-decrypt-password"
    >
      <label for="key-factor-decrypt-password" class="block text-sm font-medium text-default">
        {{ t('keys.factor.decryptPasswordLabel') }}
      </label>
      <UInput
        id="key-factor-decrypt-password"
        v-model="password"
        type="password"
        name="key-factor-decrypt-password"
        autocomplete="new-password"
        data-lpignore="true"
        data-1p-ignore="true"
        :disabled="disabled || loading"
        class="w-full"
        :placeholder="t('keys.factor.passwordPlaceholder')"
        data-test="key-factor-decrypt-password-input"
      />
      <p class="text-xs text-muted">{{ t('keys.factor.decryptPasswordHint') }}</p>
    </div>

    <p
      v-if="errorMessage"
      class="text-xs text-red-600 dark:text-red-400"
      role="alert"
      data-test="key-factor-error"
    >
      {{ errorMessage }}
    </p>
  </div>
</template>

<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type {
  ApiKeyVerificationFactors,
  ApiKeyVerificationMethod,
  ApiKeyVerificationPayload,
} from '@/types'
import { keysAPI } from '@/api'
import { requestOptionsFromJSON, serializeAssertionCredential } from '@/api/passkey'

const props = withDefaults(
  defineProps<{
    /** 服务端返回的可用因子；null 表示还没加载到，只渲染占位。 */
    factors: ApiKeyVerificationFactors | null
    /** create：创建前的身份校验；reveal：二次查看授权 + 本地解密密码。 */
    mode?: 'create' | 'reveal'
    /** 预选因子（二次查看时来自密文的 seal_auth_factor）。 */
    preferredFactor?: string
    disabled?: boolean
    loading?: boolean
  }>(),
  { mode: 'create', preferredFactor: '', disabled: false, loading: false },
)

const { t } = useI18n()

const METHOD_LABEL_KEYS: Record<ApiKeyVerificationMethod, string> = {
  password: 'keys.factor.methodPassword',
  totp: 'keys.factor.methodTotp',
  passkey: 'keys.factor.methodPasskey',
}

const availableMethods = computed<ApiKeyVerificationMethod[]>(
  () => props.factors?.available_methods ?? [],
)

const method = ref<ApiKeyVerificationMethod | undefined>(undefined)
const password = ref('')
const totpCode = ref('')
const passkeyToken = ref('')
const passkeyExpiresAt = ref(0)
const passkeyBusy = ref(false)
const errorMessage = ref('')
const nowTick = ref(Date.now())
let ticker: ReturnType<typeof setInterval> | null = null

const stopTicker = () => {
  if (ticker) {
    clearInterval(ticker)
    ticker = null
  }
}

const startTicker = () => {
  stopTicker()
  nowTick.value = Date.now()
  ticker = setInterval(() => {
    nowTick.value = Date.now()
    if (passkeyExpiresAt.value > 0 && passkeyExpiresAt.value <= nowTick.value) {
      passkeyToken.value = ''
      passkeyExpiresAt.value = 0
      stopTicker()
    }
  }, 1000)
}

onUnmounted(stopTicker)

const passkeyRemaining = computed(() => {
  if (!passkeyExpiresAt.value) return 0
  return Math.max(0, Math.ceil((passkeyExpiresAt.value - nowTick.value) / 1000))
})

const methodItems = computed(() => {
  const preferred = props.factors?.preferred ?? ''
  return availableMethods.value.map((value) => {
    const label = t(METHOD_LABEL_KEYS[value])
    return {
      value,
      label: value === preferred ? `${label}（${t('keys.factor.preferred')}）` : label,
    }
  })
})

const applyDefaultMethod = () => {
  const available = availableMethods.value
  if (available.length === 0) {
    method.value = undefined
    return
  }
  const wanted = props.preferredFactor || props.factors?.preferred || ''
  method.value = available.includes(wanted as ApiKeyVerificationMethod)
    ? (wanted as ApiKeyVerificationMethod)
    : available[0]
}

watch(
  () => [props.factors, props.preferredFactor] as const,
  () => {
    applyDefaultMethod()
    if (method.value) errorMessage.value = ''
  },
  { immediate: true },
)

watch(method, () => {
  errorMessage.value = ''
})

const hasPasskeySupport = (): boolean =>
  typeof window !== 'undefined' &&
  typeof navigator !== 'undefined' &&
  Boolean(window.PublicKeyCredential && navigator.credentials)

const verifyPasskey = async () => {
  if (props.disabled || props.loading || passkeyBusy.value) return
  errorMessage.value = ''
  if (!hasPasskeySupport()) {
    errorMessage.value = t('keys.factor.passkeyUnsupported')
    return
  }
  passkeyBusy.value = true
  try {
    const begin = await keysAPI.beginKeyPasskeyVerification()
    const credential = await navigator.credentials.get({
      publicKey: requestOptionsFromJSON(begin.options.publicKey),
    })
    if (!credential) {
      errorMessage.value = t('keys.factor.passkeyFailed')
      return
    }
    const ticket = await keysAPI.finishKeyPasskeyVerification(
      begin.session_token,
      serializeAssertionCredential(
        credential as unknown as Parameters<typeof serializeAssertionCredential>[0],
      ),
    )
    passkeyToken.value = ticket.passkey_session_token
    passkeyExpiresAt.value = Date.now() + Math.max(1, ticket.expires_in) * 1000
    startTicker()
  } catch (error: unknown) {
    const reason =
      error && typeof error === 'object'
        ? String((error as Record<string, unknown>).reason ?? '')
        : ''
    errorMessage.value =
      reason === 'VERIFY_FACTOR_UNAVAILABLE'
        ? t('keys.factor.factorUnavailable')
        : t('keys.factor.passkeyFailed')
  } finally {
    passkeyBusy.value = false
  }
}

/**
 * 生成提交给后端（创建 / reveal-authorize）的 verification 载荷。
 * 任一项不满足返回 null，并在组件内展示可执行的具体提示。
 */
const buildVerification = (): ApiKeyVerificationPayload | null => {
  errorMessage.value = ''
  const selected = method.value
  if (!selected) {
    errorMessage.value = availableMethods.value.length
      ? t('keys.factor.requiredAll')
      : t('keys.factor.unavailable')
    return null
  }
  if (selected === 'password') {
    if (!password.value) {
      errorMessage.value = t('keys.factor.passwordRequired')
      return null
    }
    return { method: 'password', password: password.value }
  }
  if (selected === 'totp') {
    const code = totpCode.value.trim()
    if (!/^\d{6}$/.test(code)) {
      errorMessage.value = t('keys.factor.totpRequired')
      return null
    }
    return { method: 'totp', totp_code: code }
  }
  if (!passkeyToken.value) {
    errorMessage.value = t('keys.factor.passkeyRequired')
    return null
  }
  return { method: 'passkey', passkey_session_token: passkeyToken.value }
}

/** 本地解密用的登录密码（TOTP/Passkey 模式下由用户单独输入）。 */
const getDecryptPassword = (): string => password.value

const reset = () => {
  password.value = ''
  totpCode.value = ''
  passkeyToken.value = ''
  passkeyExpiresAt.value = 0
  errorMessage.value = ''
  stopTicker()
  applyDefaultMethod()
}

defineExpose({ buildVerification, getDecryptPassword, reset, verifyPasskey })
</script>
