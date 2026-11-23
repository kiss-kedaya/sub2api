<template>
  <BaseDialog
    :show="show"
    :title="t('keys.reveal.title')"
    :description="t('keys.reveal.description')"
    width="normal"
    @close="close"
  >
    <form class="space-y-4" data-test="api-key-reveal-dialog" @submit.prevent="submit">
      <div class="text-sm text-muted">
        <span class="font-medium text-highlighted">{{ apiKey?.name || '-' }}</span>
        <span v-if="apiKey?.key_masked" class="ml-2 font-mono text-xs">{{ apiKey.key_masked }}</span>
      </div>

      <!-- 身份校验与创建时同一套组件：只渲染账号实际可用的因子，
           并用密文元数据里的 seal_auth_factor 预选创建时使用的方式。 -->
      <div v-if="!plaintext" class="space-y-2">
        <div
          v-if="factorsLoading"
          class="rounded-lg bg-gray-50 p-3 text-xs text-muted ring ring-inset ring-default dark:bg-dark-900"
          data-test="api-key-reveal-factors-loading"
        >
          {{ t('keys.reveal.factorsLoading') }}
        </div>
        <template v-else>
          <KeyFactorVerification
            ref="verificationRef"
            :factors="factors"
            mode="reveal"
            :preferred-factor="preferredFactor"
            :loading="loading"
          />
          <p
            v-if="factorUnavailable"
            class="rounded-lg border border-amber-200 bg-amber-50 p-3 text-xs text-amber-900 dark:border-amber-800 dark:bg-amber-900/20 dark:text-amber-100"
            data-test="api-key-reveal-factor-unavailable"
          >
            {{ t('keys.reveal.factorUnavailable', { factor: factorLabel(apiKey?.seal_auth_factor || '') }) }}
          </p>
        </template>
      </div>

      <p
        v-if="error"
        class="text-xs text-red-600 dark:text-red-400"
        role="alert"
        data-test="api-key-reveal-error"
      >
        {{ error }}
      </p>

      <div v-if="plaintext" class="space-y-2">
        <div class="flex items-start gap-2 rounded-lg bg-gray-50 p-3 ring ring-inset ring-default dark:bg-dark-900">
          <code class="min-w-0 flex-1 break-all font-mono text-xs leading-5 text-highlighted" data-test="api-key-reveal-value">{{ plaintext }}</code>
          <UButton
            color="neutral"
            variant="soft"
            size="xs"
            :icon="copied ? 'i-lucide-check' : 'i-lucide-copy'"
            :label="copied ? t('keys.reveal.copied') : t('keys.reveal.copy')"
            class="shrink-0"
            data-test="api-key-reveal-copy"
            @click="copyRevealed"
          />
        </div>
        <p class="text-xs text-muted" data-test="api-key-reveal-only-this-time">{{ t('keys.reveal.onlyThisTime') }}</p>
      </div>

      <div class="flex flex-wrap items-center justify-end gap-2">
        <UButton
          type="button"
          color="neutral"
          variant="soft"
          :label="t('keys.reveal.cancel')"
          data-test="api-key-reveal-cancel"
          @click="close"
        />
        <UButton
          v-if="notFound"
          type="button"
          color="primary"
          icon="i-lucide-refresh-cw"
          :label="t('keys.reveal.rotate')"
          data-test="api-key-reveal-rotate"
          @click="emit('rotate')"
        />
        <UButton
          v-else-if="!plaintext"
          type="submit"
          color="primary"
          :loading="loading"
          :disabled="loading || factorsLoading || !hasAvailableFactor"
          :label="t('keys.reveal.submit')"
          data-test="api-key-reveal-submit"
        />
        <UButton
          v-else
          type="button"
          color="primary"
          :label="t('keys.reveal.done')"
          data-test="api-key-reveal-done"
          @click="close"
        />
      </div>
    </form>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { ApiKey, ApiKeyVerificationFactors, ApiKeyVerificationMethod } from '@/types'
import BaseDialog from '@/components/common/BaseDialog.vue'
import KeyFactorVerification from './KeyFactorVerification.vue'
import { useClipboard } from '@/composables/useClipboard'
import { keysAPI } from '@/api'
import {
  ApiKeySealDecryptError,
  ApiKeySealFormatError,
  ApiKeySealUnsupportedError,
  openApiKeySeal,
} from '@/utils/apikeySeal'
import { extractApiErrorMessage } from '@/utils/apiError'

const props = withDefaults(
  defineProps<{
    show: boolean
    apiKey?: ApiKey | null
  }>(),
  { apiKey: null },
)

const emit = defineEmits<{ close: []; rotate: [] }>()

const { t } = useI18n()
const { copyToClipboard } = useClipboard()

const verificationRef = ref<InstanceType<typeof KeyFactorVerification> | null>(null)
const factors = ref<ApiKeyVerificationFactors | null>(null)
const factorsLoading = ref(false)
const plaintext = ref('')
const loading = ref(false)
const error = ref('')
const notFound = ref(false)
const copied = ref(false)

const METHOD_LABEL_KEYS: Record<ApiKeyVerificationMethod, string> = {
  password: 'keys.factor.methodPassword',
  totp: 'keys.factor.methodTotp',
  passkey: 'keys.factor.methodPasskey',
}

const factorLabel = (method: string): string => {
  const key = METHOD_LABEL_KEYS[method as ApiKeyVerificationMethod]
  return key ? t(key) : method || '-'
}

/** 创建时授权因子优先；旧密文（无记录）用推荐因子。 */
const preferredFactor = computed(() => props.apiKey?.seal_auth_factor || factors.value?.preferred || '')

const hasAvailableFactor = computed(() => (factors.value?.available_methods.length ?? 0) > 0)

/**
 * 创建时用的因子现在不可用（例如 Passkey 被解绑）：提示具体原因，
 * 不允许换别的因子绕过——服务端也会按同一因子拒绝。
 */
const factorUnavailable = computed(() => {
  const sealed = props.apiKey?.seal_auth_factor
  if (!sealed || !factors.value) return false
  return !factors.value.available_methods.includes(sealed as ApiKeyVerificationMethod)
})

const reset = () => {
  factors.value = null
  factorsLoading.value = false
  plaintext.value = ''
  loading.value = false
  error.value = ''
  notFound.value = false
  copied.value = false
}

const loadFactors = async () => {
  factorsLoading.value = true
  try {
    factors.value = await keysAPI.getVerificationFactors()
  } catch (err) {
    error.value = extractApiErrorMessage(err, t('keys.reveal.failed'))
  } finally {
    factorsLoading.value = false
  }
}

const open = () => {
  reset()
  void loadFactors()
}

watch(
  () => props.show,
  (show) => {
    if (show) open()
  },
  { immediate: true },
)

const close = () => {
  reset()
  emit('close')
}

const reasonOf = (error: unknown): string =>
  error && typeof error === 'object'
    ? String((error as Record<string, unknown>).reason ?? '')
    : ''

const submit = async () => {
  if (!props.apiKey || loading.value) return
  error.value = ''
  notFound.value = false

  const verification = verificationRef.value?.buildVerification() ?? null
  if (!verification) return
  const decryptPassword = verificationRef.value?.getDecryptPassword() ?? ''
  if (!decryptPassword) {
    error.value = t('keys.factor.decryptPasswordHint')
    return
  }

  loading.value = true
  try {
    // 1) 用同一因子完成服务端身份校验，换取 5 分钟一次性 reveal grant；
    // 2) 带 grant 取密文；
    // 3) 只在浏览器本地用登录密码解密。
    const grant = await keysAPI.authorizeReveal(props.apiKey.id, verification)
    const seal = await keysAPI.getSecretSeal(props.apiKey.id, grant.grant)
    const revealed = await openApiKeySeal(seal, decryptPassword)
    plaintext.value = revealed
    await copyToClipboard(revealed, t('keys.reveal.copied'))
    copied.value = true
  } catch (err) {
    const reason = reasonOf(err)
    if (err instanceof ApiKeySealDecryptError) {
      error.value = t('keys.reveal.wrongPassword')
    } else if (err instanceof ApiKeySealUnsupportedError) {
      error.value = t('keys.reveal.unsupported')
    } else if (err instanceof ApiKeySealFormatError) {
      error.value = t('keys.reveal.broken')
    } else if (reason === 'API_KEY_SECRET_NOT_FOUND' || reason === 'API_KEY_NOT_FOUND') {
      notFound.value = true
      error.value = t('keys.reveal.notFound')
    } else if (reason === 'VERIFY_PASSWORD_INVALID') {
      error.value = t('keys.reveal.wrongPassword')
    } else if (reason === 'VERIFY_TOTP_INVALID') {
      error.value = t('keys.reveal.wrongTotp')
    } else if (reason === 'VERIFY_PASSKEY_FAILED') {
      error.value = t('keys.reveal.passkeyFailed')
    } else if (reason === 'VERIFY_FACTOR_MISMATCH') {
      error.value = t('keys.reveal.factorMismatch', {
        factor: factorLabel(props.apiKey?.seal_auth_factor || ''),
      })
    } else if (reason === 'VERIFY_FACTOR_UNAVAILABLE') {
      error.value = t('keys.reveal.factorUnavailable', {
        factor: factorLabel(props.apiKey?.seal_auth_factor || ''),
      })
    } else if (reason === 'REVEAL_GRANT_REQUIRED' || reason === 'REVEAL_GRANT_INVALID') {
      error.value = t('keys.reveal.reverify')
    } else {
      error.value = extractApiErrorMessage(err, t('keys.reveal.failed'))
    }
    // 校验材料一次性：失败后清掉已用过的 Passkey 票据与动态码，避免重放。
    verificationRef.value?.reset()
  } finally {
    loading.value = false
  }
}

const copyRevealed = async () => {
  if (!plaintext.value) return
  const ok = await copyToClipboard(plaintext.value, t('keys.reveal.copied'))
  if (ok) {
    copied.value = true
    setTimeout(() => {
      copied.value = false
    }, 1500)
  }
}
</script>
