<template>
  <BaseDialog
    :show="show"
    :title="t('keys.secretDialog.title')"
    :description="t('keys.secretDialog.description')"
    width="normal"
    :close-on-click-outside="canClose"
    :close-on-escape="canClose"
    :close-button-disabled="!canClose"
    @close="requestClose"
  >
    <!-- One short line under the title says the only thing that matters: shown once, copy it now. -->
    <template #description>
      <span class="inline-flex items-center gap-1.5" data-test="api-key-secret-notice">
        <UIcon name="i-lucide-triangle-alert" class="size-3.5 shrink-0 text-amber-500" aria-hidden="true" />
        {{ t('keys.secretDialog.description') }}
      </span>
    </template>

    <div class="space-y-3" data-test="api-key-secret-dialog" :data-can-close="canClose ? 'true' : 'false'">
      <div v-if="apiKey" class="flex min-w-0 items-center gap-2 text-sm">
        <UIcon name="i-lucide-key-round" class="size-4 shrink-0 text-dimmed" aria-hidden="true" />
        <span class="truncate font-medium text-highlighted" data-test="api-key-secret-name">{{ apiKey.name }}</span>
      </div>

      <div class="flex items-start gap-2 rounded-lg bg-muted p-3 ring ring-inset ring-default">
        <code
          class="min-w-0 flex-1 wrap-anywhere font-mono text-xs leading-5 text-highlighted"
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

      <!-- Opt-in: keep a password-encrypted copy on the server so this key can be viewed again after a
           refresh. A single switch; the explanation sits behind the (i) button instead of on screen. When the
           login password was already typed to create or rotate this key it is reused from memory, so the user
           is never asked twice. Accounts without a usable login password (third-party sign-in) do not see the
           row at all: a control they cannot use needs no explaining. -->
      <div v-if="apiKey && canSeal" class="space-y-2" data-test="api-key-seal-optin">
        <div class="flex min-h-8 items-center gap-2">
          <UIcon name="i-lucide-lock-keyhole" class="size-4 shrink-0 text-dimmed" aria-hidden="true" />
          <label for="api-key-seal-switch" class="cursor-pointer text-sm text-default">{{ t('keys.seal.optIn') }}</label>
          <button
            type="button"
            class="seal-info"
            :aria-expanded="hintOpen ? 'true' : 'false'"
            aria-controls="api-key-seal-hint"
            :aria-label="t('keys.seal.info')"
            data-test="api-key-seal-info"
            @click="hintOpen = !hintOpen"
          >
            <UIcon name="i-lucide-info" class="size-3.5" aria-hidden="true" />
          </button>
          <Toggle
            id="api-key-seal-switch"
            :model-value="sealEnabled"
            class="ml-auto"
            :disabled="sealing"
            :aria-label="t('keys.seal.optIn')"
            data-test="api-key-seal-checkbox"
            @update:model-value="onSealToggle"
          />
        </div>
        <p v-show="hintOpen" id="api-key-seal-hint" class="text-xs leading-5 text-muted" data-test="api-key-seal-hint">
          {{ t('keys.seal.optInHint') }}
        </p>

        <Transition name="seal-field">
          <UInput
            v-if="sealEnabled && !hasVerifiedPassword"
            id="api-key-seal-password"
            ref="passwordInput"
            v-model="sealPassword"
            type="password"
            name="apikey-seal-passphrase"
            autocomplete="new-password"
            data-lpignore="true"
            data-1p-ignore="true"
            icon="i-lucide-key-round"
            :placeholder="t('keys.seal.passwordLabel')"
            :aria-label="t('keys.seal.passwordLabel')"
            :disabled="sealing"
            class="w-full"
            data-test="api-key-seal-password"
            @keydown.enter.prevent="onDone"
          />
        </Transition>
      </div>

      <p
        v-if="sealError"
        class="flex items-center gap-1.5 text-xs text-error"
        role="alert"
        data-test="api-key-seal-error"
      >
        <UIcon name="i-lucide-circle-alert" class="size-3.5 shrink-0" aria-hidden="true" />
        {{ sealError }}
      </p>

      <!-- Spoken only: the visible cue is the disabled button for the first second. -->
      <p
        v-if="!canClose"
        class="sr-only"
        data-test="api-key-secret-wait-hint"
        role="status"
      >
        {{ t('keys.secretDialog.waitHint') }}
      </p>
    </div>

    <template #footer>
      <div class="flex flex-wrap items-center justify-between gap-x-3 gap-y-2" data-test="api-key-secret-footer">
        <div class="flex items-center gap-1">
          <UButton
            v-if="!hideCcsImport"
            color="neutral"
            variant="ghost"
            size="sm"
            icon="i-lucide-upload"
            :label="t('keys.secretDialog.importCcs')"
            data-test="api-key-secret-import-ccs"
            @click="emit('import-ccs')"
          />
          <UButton
            color="neutral"
            variant="ghost"
            size="sm"
            icon="i-lucide-square-terminal"
            :label="t('keys.secretDialog.use')"
            data-test="api-key-secret-use"
            @click="emit('use')"
          />
        </div>
        <div class="flex items-center gap-2">
          <UButton
            v-if="sealError && !sealed"
            color="neutral"
            variant="ghost"
            :label="t('keys.seal.later')"
            data-test="api-key-seal-later"
            @click="skipSeal"
          />
          <UButton
            color="primary"
            :ui="quietPrimaryUi"
            icon="i-lucide-check"
            :label="t('keys.secretDialog.done')"
            :disabled="!canClose || sealing"
            :loading="sealing"
            data-test="api-key-secret-done"
            @click="onDone"
          />
        </div>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, nextTick, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { ApiKey } from '@/types'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Toggle from '@/components/common/Toggle.vue'
import { useClipboard } from '@/composables/useClipboard'
import { keysAPI } from '@/api'
import { ApiKeySealUnsupportedError, sealApiKey } from '@/utils/apikeySeal'
import { quietPrimaryUi } from './quietUi'

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
    /**
     * The login password the user already typed (and the server already checked) to create or rotate this
     * key. Used for "view later" so the user is not asked for the same password a second time. Memory only:
     * it is never rendered or stored, and the parent clears it when the receipt closes.
     */
    verifiedPassword?: string
  }>(),
  { apiKey: null, hideCcsImport: false, canSeal: false, authFactor: '', verifiedPassword: '' },
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

// ---- "view again with login password" opt-in (enabled by default when account can seal) ---
const sealEnabled = ref(false)
const userToggledSeal = ref(false)
const sealPassword = ref('')
const sealing = ref(false)
const sealed = ref(false)
const sealError = ref('')
const hintOpen = ref(false)
// The remembered password was refused (it should never be, but a password can change between the two steps):
// stop reusing it and let the user type it.
const typeItInstead = ref(false)
const passwordInput = ref<{ inputRef?: HTMLInputElement | null } | null>(null)

// The password for "view later" is already known: no field, nothing to type.
const hasVerifiedPassword = computed(() => props.verifiedPassword !== '' && !typeItInstead.value)

const resetSealState = () => {
  // 账号支持登录密码封存时默认开启「以后可查看」；回执关闭时弹窗会从 DOM 卸载，不影响列表页。
  sealEnabled.value = props.canSeal
  userToggledSeal.value = false
  sealPassword.value = ''
  sealing.value = false
  sealed.value = false
  sealError.value = ''
  hintOpen.value = false
  typeItInstead.value = false
}

const onSealToggle = (enabled: boolean) => {
  userToggledSeal.value = true
  sealEnabled.value = enabled
}

const requestClose = () => {
  if (!canClose.value) return
  // Header X / Escape / veil dismiss the receipt without sealing: the key still
  // stays readable in this tab until refresh. Only "I've saved it" seals.
  emit('close')
}

const onDone = () => {
  if (!canClose.value) return
  // A checked opt-in must finish (or explicitly be postponed) first.
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
  const password = hasVerifiedPassword.value ? props.verifiedPassword : sealPassword.value
  if (!password) {
    sealError.value = t('keys.seal.passwordRequired')
    return
  }
  sealing.value = true
  sealError.value = ''
  try {
    const payload = await sealApiKey(props.secret, password)
    // 服务端会先 bcrypt 校验登录密码，再保存密文：密码错了以后就再也解不开，
    // 所以这里必须让它成为一次显式失败，而不是「保存成功、以后才报错」。
    await keysAPI.putSecretSeal(id, payload, {
      password,
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
      if (hasVerifiedPassword.value) {
        typeItInstead.value = true
        await nextTick()
        passwordInput.value?.inputRef?.focus()
      }
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
    if (!canSeal) {
      sealEnabled.value = false
    } else if (props.show && !userToggledSeal.value) {
      sealEnabled.value = true
    }
  },
)

// Turning the switch on puts the cursor straight into the password field (when there is one).
watch(sealEnabled, async (enabled) => {
  if (!enabled) {
    sealError.value = ''
    return
  }
  if (!userToggledSeal.value || hasVerifiedPassword.value) return
  await nextTick()
  passwordInput.value?.inputRef?.focus()
})

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

<style scoped>
/* The (i) button: a quiet round target; the explanation it opens is one sentence. */
.seal-info {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 20px;
  height: 20px;
  border-radius: 9999px;
  color: var(--ui-text-dimmed);
  transition: color 120ms var(--ease-out, cubic-bezier(0.23, 1, 0.32, 1)), background-color 120ms var(--ease-out, cubic-bezier(0.23, 1, 0.32, 1));
}

@media (hover: hover) and (pointer: fine) {
  .seal-info:hover {
    color: var(--ui-text-toned);
    background: var(--ui-bg-elevated);
  }
}

.seal-info[aria-expanded='true'] {
  color: var(--ui-text-toned);
  background: var(--ui-bg-elevated);
}

.seal-info:focus-visible {
  outline: 2px solid var(--ui-primary);
  outline-offset: 1px;
}

@media (pointer: coarse) {
  .seal-info {
    width: 44px;
    height: 44px;
    margin: -12px -10px;
  }
}

/* The password field slides in a few pixels and fades; transform + opacity only. */
.seal-field-enter-active,
.seal-field-leave-active {
  transition: opacity 160ms var(--ease-out, cubic-bezier(0.23, 1, 0.32, 1)), transform 160ms var(--ease-out, cubic-bezier(0.23, 1, 0.32, 1));
}

.seal-field-enter-from,
.seal-field-leave-to {
  opacity: 0;
  transform: translateY(-4px);
}

@media (prefers-reduced-motion: reduce) {
  .seal-field-enter-active,
  .seal-field-leave-active {
    transition: opacity 120ms var(--ease-out, cubic-bezier(0.23, 1, 0.32, 1));
  }

  .seal-info {
    transition: none;
  }

  .seal-field-enter-from,
  .seal-field-leave-to {
    transform: none;
  }
}
</style>
