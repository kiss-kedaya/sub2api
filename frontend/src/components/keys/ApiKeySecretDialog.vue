<template>
  <BaseDialog
    :show="show"
    :title="t('keys.secretDialog.title')"
    :description="t('keys.secretDialog.description')"
    width="wide"
    :close-on-click-outside="canClose"
    :close-on-escape="canClose"
    :close-button-disabled="!canClose"
    @close="requestClose"
  >
    <div class="space-y-4" data-test="api-key-secret-dialog" :data-can-close="canClose ? 'true' : 'false'">
      <div
        class="flex items-start gap-3 rounded-lg border border-amber-200 bg-amber-50 p-4 dark:border-amber-800 dark:bg-amber-900/20"
      >
        <UIcon name="i-lucide-triangle-alert" class="mt-0.5 size-5 shrink-0 text-amber-500" />
        <div class="space-y-1 text-sm text-amber-900 dark:text-amber-100">
          <p>{{ t('keys.secretDialog.saveNotice') }}</p>
          <p>{{ t('keys.secretDialog.cannotViewAgain') }}</p>
        </div>
      </div>

      <div v-if="apiKey" class="text-sm text-muted">
        <span class="font-medium text-highlighted">{{ apiKey.name }}</span>
        <span v-if="apiKey.key_masked" class="ml-2 font-mono text-xs">{{ apiKey.key_masked }}</span>
      </div>

      <div class="flex items-start gap-2 rounded-lg bg-gray-50 p-3 ring ring-inset ring-default dark:bg-dark-900">
        <code
          class="min-w-0 flex-1 break-all font-mono text-xs leading-5 text-highlighted"
          data-test="api-key-secret-value"
        >{{ secret }}</code>
        <UButton
          color="neutral"
          variant="soft"
          size="xs"
          :icon="copied ? 'i-lucide-check' : 'i-lucide-copy'"
          :label="copied ? t('keys.secretDialog.copied') : t('keys.secretDialog.copy')"
          class="shrink-0"
          data-test="api-key-secret-copy"
          @click="copySecret"
        />
      </div>

      <!-- Opt-in: keep a password-encrypted copy on the server so this key can be
           viewed again after a refresh. The password never leaves the browser. -->
      <div
        v-if="apiKey && canSeal"
        class="space-y-2 rounded-lg border border-default p-3"
        data-test="api-key-seal-optin"
      >
        <UCheckbox
          v-model="sealEnabled"
          :disabled="sealing"
          data-test="api-key-seal-checkbox"
          :label="t('keys.seal.optIn')"
          :description="t('keys.seal.optInHint')"
        />
        <div v-if="sealEnabled" class="space-y-1.5 pl-6">
          <label for="api-key-seal-password" class="block text-xs font-medium text-default">
            {{ t('keys.seal.passwordLabel') }}
          </label>
          <!-- 浏览器自动填充串扰修复：这是「本地加密口令」不是登录表单，
               用 new-password + 非凭据 name + 密码管理器忽略标记，避免把账号
               填进本页搜索框 / Key 名称框。 -->
          <UInput
            id="api-key-seal-password"
            v-model="sealPassword"
            type="password"
            name="apikey-seal-passphrase"
            autocomplete="new-password"
            data-lpignore="true"
            data-1p-ignore="true"
            :disabled="sealing"
            class="w-full"
            data-test="api-key-seal-password"
          />
          <p class="text-xs text-muted">{{ t('keys.seal.passwordHint') }}</p>
        </div>
      </div>
      <p
        v-else-if="apiKey && !canSeal"
        class="rounded-lg bg-gray-50 p-3 text-xs text-muted ring ring-inset ring-default dark:bg-dark-900"
        data-test="api-key-seal-unavailable"
      >
        {{ t('keys.seal.unavailable') }}
      </p>

      <p
        v-if="sealError"
        class="text-xs text-red-600 dark:text-red-400"
        role="alert"
        data-test="api-key-seal-error"
      >
        {{ sealError }}
      </p>

      <p
        v-if="!canClose"
        class="text-xs text-muted"
        data-test="api-key-secret-wait-hint"
        role="status"
      >
        {{ t('keys.secretDialog.waitHint') }}
      </p>

      <div class="flex flex-wrap items-center justify-end gap-2">
        <UButton
          v-if="sealError && !sealed"
          color="neutral"
          variant="soft"
          :label="t('keys.seal.later')"
          data-test="api-key-seal-later"
          @click="skipSeal"
        />
        <UButton
          v-if="!hideCcsImport"
          color="neutral"
          variant="soft"
          icon="i-lucide-upload"
          :label="t('keys.secretDialog.importCcs')"
          @click="emit('import-ccs')"
        />
        <UButton
          color="neutral"
          variant="soft"
          icon="i-lucide-square-terminal"
          :label="t('keys.secretDialog.use')"
          @click="emit('use')"
        />
        <UButton
          color="primary"
          :label="t('keys.secretDialog.done')"
          :disabled="!canClose || sealing"
          :loading="sealing"
          data-test="api-key-secret-done"
          @click="onDone"
        />
      </div>
    </div>
  </BaseDialog>
</template>

<script setup lang="ts">
import { onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { ApiKey } from '@/types'
import BaseDialog from '@/components/common/BaseDialog.vue'
import { useClipboard } from '@/composables/useClipboard'
import { keysAPI } from '@/api'
import { ApiKeySealUnsupportedError, sealApiKey } from '@/utils/apikeySeal'

/** The plaintext receipt is forced on screen for at least this long. */
const MIN_VISIBLE_MS = 1000

const props = withDefaults(
  defineProps<{
    show: boolean
    apiKey?: ApiKey | null
    secret: string
    hideCcsImport?: boolean
    /** Account can type a login password (hidden for passwordless OAuth-only accounts). */
    canSeal?: boolean
    /** 创建这次 Key 时使用的身份校验因子；封存密文时原样记录，二次查看要求同一因子。 */
    authFactor?: string
  }>(),
  { apiKey: null, hideCcsImport: false, canSeal: false, authFactor: '' },
)

const emit = defineEmits<{ close: []; use: []; 'import-ccs': []; sealed: [] }>()

const { t } = useI18n()
const { copyToClipboard } = useClipboard()
const copied = ref(false)

// Until the minimum display window elapses every dismissal path is unavailable: the header X is
// rendered disabled, Escape and veil clicks are turned off, and the footer button is disabled.
// The guard below is the single authority so no path can close the receipt early.
const canClose = ref(false)
let unlockTimer: ReturnType<typeof setTimeout> | null = null

const clearUnlockTimer = () => {
  if (unlockTimer) {
    clearTimeout(unlockTimer)
    unlockTimer = null
  }
}

const startVisibleWindow = () => {
  clearUnlockTimer()
  canClose.value = false
  unlockTimer = setTimeout(() => {
    unlockTimer = null
    canClose.value = true
  }, MIN_VISIBLE_MS)
}

// ---- "view again with login password" opt-in ---------------------------------
const sealEnabled = ref(false)
const sealPassword = ref('')
const sealing = ref(false)
const sealed = ref(false)
const sealError = ref('')

const resetSealState = () => {
  // 默认不勾选：密码框只在用户主动选择「以后可查看」时渲染，
  // 避免 Key 列表页常驻 password 输入框触发浏览器自动填充。
  sealEnabled.value = false
  sealPassword.value = ''
  sealing.value = false
  sealed.value = false
  sealError.value = ''
}

const requestClose = () => {
  if (!canClose.value) return
  // Header X / Escape / veil dismiss the receipt without sealing: the key still
  // stays readable in this tab until refresh. Only "I've saved it" seals.
  emit('close')
}

const onDone = () => {
  if (!canClose.value) return
  // A checked opt-in with a password must finish (or explicitly be postponed) first.
  if (sealEnabled.value && !sealed.value) {
    void saveSeal()
    return
  }
  emit('close')
}

const saveSeal = async () => {
  const id = props.apiKey?.id
  if (!id) {
    emit('close')
    return
  }
  if (!sealPassword.value) {
    sealError.value = t('keys.seal.passwordRequired')
    return
  }
  sealing.value = true
  sealError.value = ''
  try {
    const payload = await sealApiKey(props.secret, sealPassword.value)
    // 服务端会先 bcrypt 校验登录密码，再保存密文：密码错了以后就再也解不开，
    // 所以这里必须让它成为一次显式失败，而不是「保存成功、以后才报错」。
    await keysAPI.putSecretSeal(id, payload, {
      password: sealPassword.value,
      authFactor: props.authFactor || 'password',
    })
    sealed.value = true
    sealPassword.value = ''
    emit('sealed')
    emit('close')
  } catch (error) {
    // 密码错误有细分机器码；其它失败按浏览器不支持 / 保存失败处理，可重试或稍后再说。
    const reason =
      error && typeof error === 'object'
        ? String((error as Record<string, unknown>).reason ?? '')
        : ''
    if (reason === 'VERIFY_PASSWORD_INVALID') {
      sealError.value = t('keys.seal.wrongPassword')
    } else if (reason === 'VERIFY_PASSWORD_RATE_LIMITED') {
      sealError.value = t('keys.factor.passwordRateLimited')
    } else if (reason === 'VERIFY_FACTOR_UNAVAILABLE') {
      sealError.value = t('keys.seal.unavailable')
    } else {
      sealError.value =
        error instanceof ApiKeySealUnsupportedError ? t('keys.seal.unsupported') : t('keys.seal.failed')
    }
  } finally {
    sealing.value = false
  }
}

const skipSeal = () => {
  sealError.value = ''
  emit('close')
}

watch(
  () => props.show,
  (show) => {
    if (show) {
      resetSealState()
      startVisibleWindow()
    } else {
      // Closing drops the unlock timer; the next receipt starts a fresh window.
      clearUnlockTimer()
      canClose.value = false
    }
  },
  { immediate: true },
)

watch(
  () => props.canSeal,
  (canSeal) => {
    if (!canSeal) sealEnabled.value = false
  },
)

onUnmounted(clearUnlockTimer)

watch(
  () => props.secret,
  () => {
    copied.value = false
  },
)

const copySecret = async () => {
  if (!props.secret) return
  const ok = await copyToClipboard(props.secret, t('keys.secretDialog.copied'))
  if (ok) {
    copied.value = true
    setTimeout(() => {
      copied.value = false
    }, 1500)
  }
}
</script>
