<template>
  <BaseDialog
    :show="show"
    :title="t('admin.crypto.signer.wizard.riskTitle')"
    :close-on-escape="false"
    :close-on-click-outside="false"
    :show-close-button="false"
    @close="emit('close')"
  >
    <div class="signer-risk space-y-4 text-sm">
      <UAlert color="error" variant="subtle" icon="i-lucide-triangle-alert" :title="t('admin.crypto.signer.wizard.riskHint')" />
      <ol class="signer-risk__points space-y-2">
        <li v-for="(point, index) in displayPoints" :key="index" class="flex gap-3">
          <UBadge color="error" variant="outline" class="mt-0.5 h-6 shrink-0 font-mono">{{ index + 1 }}</UBadge>
          <span class="min-w-0 leading-relaxed text-default">{{ point }}</span>
        </li>
      </ol>
      <div class="rounded-lg bg-elevated/60 p-3">
        <p class="text-xs text-muted">{{ t('admin.crypto.signer.wizard.riskPhraseLabel') }}</p>
        <p class="signer-risk__phrase mt-1 font-mono text-base font-semibold wrap-anywhere text-highlighted" data-testid="signer-risk-phrase">{{ phrase }}</p>
      </div>
      <UFormField :label="t('admin.crypto.signer.wizard.riskPhraseLabel')" :error="mismatch ? t('admin.crypto.signer.wizard.riskPhraseMismatch') : undefined">
        <UInput
          id="signer-risk-phrase-input"
          v-model="typed"
          type="text"
          autocomplete="off"
          autocapitalize="off"
          autocorrect="off"
          spellcheck="false"
          :disabled="busy"
          :placeholder="t('admin.crypto.signer.wizard.riskPhrasePlaceholder')"
          class="w-full"
          data-testid="signer-risk-input"
        />
      </UFormField>
    </div>
    <template #footer>
      <div class="flex flex-wrap justify-end gap-2">
        <UButton color="neutral" variant="subtle" :disabled="busy" data-testid="signer-risk-cancel" @click="emit('close')">
          {{ t('admin.crypto.signer.wizard.riskCancel') }}
        </UButton>
        <UButton
          color="error"
          :disabled="busy || !matches"
          data-testid="signer-risk-confirm"
          @click="confirm"
        >{{ t('admin.crypto.signer.wizard.riskConfirm') }}</UButton>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'

const props = withDefaults(defineProps<{
  show: boolean
  phrase: string
  points?: string[]
  busy?: boolean
}>(), {
  points: () => [],
  busy: false
})

const emit = defineEmits<{
  (event: 'confirm', phrase: string): void
  (event: 'close'): void
}>()

const { t } = useI18n()
const typed = ref('')
const mismatch = ref(false)

const displayPoints = computed(() => {
  if (props.points.length > 0) return props.points
  return [
    t('admin.crypto.signer.wizard.riskPointFallback1'),
    t('admin.crypto.signer.wizard.riskPointFallback2'),
    t('admin.crypto.signer.wizard.riskPointFallback3')
  ]
})

/** Same rule as the server: whitespace inside the phrase is ignored. */
function compact(value: string): string {
  return value.replace(/\s+/g, '')
}

const matches = computed(() => compact(typed.value) === compact(props.phrase) && compact(props.phrase).length > 0)

watch(() => props.show, show => {
  if (show) {
    typed.value = ''
    mismatch.value = false
  }
})

watch(typed, () => {
  if (mismatch.value && matches.value) mismatch.value = false
})

function confirm(): void {
  if (!matches.value) {
    mismatch.value = true
    return
  }
  emit('confirm', typed.value)
}

defineExpose({ typed })
</script>
