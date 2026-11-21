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
          :disabled="!canClose"
          data-test="api-key-secret-done"
          @click="requestClose"
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

/** The plaintext receipt is forced on screen for at least this long. */
const MIN_VISIBLE_MS = 1000

const props = withDefaults(
  defineProps<{
    show: boolean
    apiKey?: ApiKey | null
    secret: string
    hideCcsImport?: boolean
  }>(),
  { apiKey: null, hideCcsImport: false },
)

const emit = defineEmits<{ close: []; use: []; 'import-ccs': [] }>()

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

const requestClose = () => {
  if (!canClose.value) return
  emit('close')
}

watch(
  () => props.show,
  (show) => {
    if (show) {
      startVisibleWindow()
    } else {
      // Closing drops the unlock timer; the next receipt starts a fresh window.
      clearUnlockTimer()
      canClose.value = false
    }
  },
  { immediate: true },
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
