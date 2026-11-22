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

      <div v-if="!plaintext" class="space-y-1.5">
        <label for="api-key-reveal-password" class="block text-sm font-medium text-default">
          {{ t('keys.reveal.passwordLabel') }}
        </label>
        <UInput
          id="api-key-reveal-password"
          v-model="password"
          type="password"
          autocomplete="current-password"
          :disabled="loading"
          class="w-full"
          data-test="api-key-reveal-password"
        />
        <p class="text-xs text-muted">{{ t('keys.reveal.passwordHint') }}</p>
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
          :disabled="loading"
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
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { ApiKey } from '@/types'
import BaseDialog from '@/components/common/BaseDialog.vue'
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

const password = ref('')
const plaintext = ref('')
const loading = ref(false)
const error = ref('')
const notFound = ref(false)
const copied = ref(false)

const reset = () => {
  password.value = ''
  plaintext.value = ''
  loading.value = false
  error.value = ''
  notFound.value = false
  copied.value = false
}

watch(
  () => props.show,
  (show) => {
    if (show) reset()
  },
)

const close = () => {
  reset()
  emit('close')
}

const readErrorField = (error: unknown, field: 'reason' | 'status' | 'message'): unknown =>
  error && typeof error === 'object' ? (error as Record<string, unknown>)[field] : undefined

const submit = async () => {
  if (!props.apiKey || loading.value) return
  error.value = ''
  notFound.value = false
  loading.value = true
  try {
    // Only the ciphertext travels over the network; the password stays here.
    const seal = await keysAPI.getSecretSeal(props.apiKey.id)
    const revealed = await openApiKeySeal(seal, password.value)
    plaintext.value = revealed
    // Copy immediately so the user can paste it before the dialog is dismissed.
    await copyToClipboard(revealed, t('keys.reveal.copied'))
    copied.value = true
  } catch (err) {
    if (err instanceof ApiKeySealDecryptError) {
      error.value = t('keys.reveal.wrongPassword')
    } else if (err instanceof ApiKeySealUnsupportedError) {
      error.value = t('keys.reveal.unsupported')
    } else if (err instanceof ApiKeySealFormatError) {
      error.value = t('keys.reveal.broken')
    } else if (
      readErrorField(err, 'reason') === 'API_KEY_SECRET_NOT_FOUND' ||
      readErrorField(err, 'status') === 404
    ) {
      notFound.value = true
      error.value = t('keys.reveal.notFound')
    } else {
      error.value = extractApiErrorMessage(err, t('keys.reveal.failed'))
    }
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
