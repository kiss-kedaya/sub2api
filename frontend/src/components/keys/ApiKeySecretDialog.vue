<template>
  <BaseDialog
    :show="show"
    :title="t('keys.secretDialog.title')"
    :description="t('keys.secretDialog.description')"
    width="wide"
    :close-on-click-outside="false"
    @close="emit('close')"
  >
    <div class="space-y-4" data-test="api-key-secret-dialog">
      <div
        class="flex items-start gap-3 rounded-lg border border-amber-200 bg-amber-50 p-4 dark:border-amber-800 dark:bg-amber-900/20"
      >
        <UIcon name="i-lucide-triangle-alert" class="mt-0.5 size-5 shrink-0 text-amber-500" />
        <p class="text-sm text-amber-900 dark:text-amber-100">
          {{ t('keys.secretDialog.saveNotice') }}
        </p>
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
          @click="copySecret"
        />
      </div>

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
        <UButton color="primary" :label="t('keys.secretDialog.done')" @click="emit('close')" />
      </div>
    </div>
  </BaseDialog>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { ApiKey } from '@/types'
import BaseDialog from '@/components/common/BaseDialog.vue'
import { useClipboard } from '@/composables/useClipboard'

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
