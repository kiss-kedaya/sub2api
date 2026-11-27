<template>
  <div class="share-input space-y-2">
    <UFormField :label="labelText" :help="hintText">
      <UTextarea
        :id="inputId"
        :model-value="modelValue"
        :placeholder="t('admin.crypto.signer.shareInput.placeholder')"
        :disabled="disabled"
        :rows="3"
        :autoresize="true"
        autocomplete="off"
        autocapitalize="off"
        autocorrect="off"
        spellcheck="false"
        class="w-full"
        :ui="{ base: 'font-mono wrap-anywhere' }"
        :aria-invalid="invalid || undefined"
        @update:model-value="onInput"
        @paste="onPaste"
        @blur="onBlur"
        @change="onBlur"
      />
    </UFormField>
    <div class="flex flex-wrap items-center gap-2">
      <UButton
        color="neutral"
        variant="outline"
        size="sm"
        icon="i-lucide-qr-code"
        :disabled="disabled"
        data-testid="share-input-scan"
        @click="openScanner"
      >{{ t('admin.crypto.signer.shareInput.scan') }}</UButton>
      <UButton
        color="neutral"
        variant="outline"
        size="sm"
        icon="i-lucide-image-up"
        :disabled="disabled"
        data-testid="share-input-upload"
        @click="fileInput?.click()"
      >{{ t('admin.crypto.signer.shareInput.upload') }}</UButton>
      <input
        ref="fileInput"
        type="file"
        accept="image/*"
        class="hidden"
        data-testid="share-input-file"
        @change="onUpload"
      />
    </div>
    <p v-if="statusKey" role="status" class="text-xs text-success">{{ t(statusKey) }}</p>
    <p v-if="messageKey" role="alert" class="text-xs text-error">{{ t(messageKey) }}</p>
    <p v-else-if="parsedSummary" role="status" class="text-xs text-muted">{{ parsedSummary }}</p>
    <QrCameraScanner :show="scannerOpen" @decoded="onScanned" @close="scannerOpen = false" />
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import QrCameraScanner from '@/components/crypto/signer/QrCameraScanner.vue'
import type { SignerCrypto } from '@/utils/signer/cryptoProvider'
import { signerErrorI18nKey } from '@/utils/signer/errors'
import { decodeQrFromImageFile, detectQrCameraSupport } from '@/utils/signer/qr'
import { normalizeShareText, parseShare, type ShardShare } from '@/utils/signer/shardFormat'

const props = withDefaults(defineProps<{
  modelValue: string
  crypto: SignerCrypto | null
  label?: string
  hint?: string
  inputId?: string
  disabled?: boolean
  invalid?: boolean
}>(), {
  label: '',
  hint: '',
  inputId: 'signer-share-input',
  disabled: false,
  invalid: false
})

const emit = defineEmits<{
  (event: 'update:modelValue', value: string): void
  (event: 'parsed', share: ShardShare | null): void
}>()

const { t } = useI18n()
const labelText = computed(() => props.label || t('admin.crypto.signer.shareInput.label'))
const hintText = computed(() => props.hint || t('admin.crypto.signer.shareInput.hint'))
const statusKey = ref('')
const messageKey = ref('')
const scannerOpen = ref(false)
const fileInput = ref<HTMLInputElement | null>(null)
const parsedInfo = ref<ShardShare | null>(null)
let touched = false

const parsedSummary = computed(() => {
  if (!parsedInfo.value || messageKey.value) return ''
  return t('admin.crypto.signer.shareInput.parsed', {
    index: parsedInfo.value.index,
    total: parsedInfo.value.total,
    threshold: parsedInfo.value.threshold,
    length: parsedInfo.value.data.length
  })
})

async function validate(value: string): Promise<void> {
  if (!value.trim() || !props.crypto) {
    parsedInfo.value = null
    messageKey.value = ''
    emit('parsed', null)
    return
  }
  try {
    const share = await parseShare(value, props.crypto)
    parsedInfo.value = share
    messageKey.value = ''
    emit('parsed', share)
  } catch (error) {
    parsedInfo.value = null
    if (touched) messageKey.value = signerErrorI18nKey(error) ?? 'admin.crypto.signer.errors.shareFormat'
    emit('parsed', null)
  }
}

function onInput(value: string): void {
  emit('update:modelValue', value)
  statusKey.value = ''
  void validate(value)
}

function applyText(raw: string, status = ''): void {
  touched = true
  statusKey.value = status
  let normalized = raw
  try {
    normalized = normalizeShareText(raw)
  } catch (error) {
    messageKey.value = signerErrorI18nKey(error) ?? 'admin.crypto.signer.errors.shareFormat'
    emit('update:modelValue', raw)
    emit('parsed', null)
    return
  }
  emit('update:modelValue', normalized)
  statusKey.value = status
  void validate(normalized)
}

function onPaste(event: ClipboardEvent): void {
  const text = event.clipboardData?.getData('text') ?? ''
  if (!text) return
  event.preventDefault()
  applyText(text, 'admin.crypto.signer.shareInput.pasteNormalized')
}

function onBlur(event: Event): void {
  const value = (event.target as HTMLTextAreaElement | null)?.value ?? props.modelValue
  if (!value.trim()) return
  touched = true
  applyText(value)
}

function openScanner(): void {
  const support = detectQrCameraSupport()
  if (!support.supported) {
    messageKey.value = support.reason === 'insecure'
      ? 'admin.crypto.signer.errors.cameraInsecure'
      : 'admin.crypto.signer.errors.cameraUnsupported'
    return
  }
  messageKey.value = ''
  scannerOpen.value = true
}

function onScanned(value: string): void {
  scannerOpen.value = false
  applyText(value, 'admin.crypto.signer.shareInput.uploaded')
}

async function onUpload(event: Event): Promise<void> {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = ''
  if (!file) return
  messageKey.value = ''
  try {
    const decoded = await decodeQrFromImageFile(file)
    if (!decoded) {
      messageKey.value = 'admin.crypto.signer.shareInput.uploadFailed'
      return
    }
    applyText(decoded, 'admin.crypto.signer.shareInput.uploaded')
  } catch {
    messageKey.value = 'admin.crypto.signer.shareInput.uploadFailed'
  }
}

// Expose for tests and parent focus control.
defineExpose({ validate })
</script>
