<template>
  <BaseDialog :show="show" :title="t('admin.crypto.signer.shareInput.scanTitle')" @close="close">
    <div class="space-y-3 text-sm">
      <p class="text-muted">{{ t('admin.crypto.signer.shareInput.scanHint') }}</p>
      <div class="relative aspect-square w-full overflow-hidden rounded-lg bg-black">
        <video
          ref="video"
          class="h-full w-full object-cover"
          playsinline
          webkit-playsinline
          muted
          autoplay
          :aria-label="t('admin.crypto.signer.shareInput.scanTitle')"
        />
        <div v-if="state === 'starting'" class="absolute inset-0 flex items-center justify-center bg-black/60 text-white">
          <span class="flex items-center gap-2">
            <UIcon name="i-lucide-loader-circle" class="size-4 animate-spin" />
            {{ t('admin.crypto.signer.shareInput.scanStarting') }}
          </span>
        </div>
      </div>
      <UAlert
        v-if="errorKey"
        role="alert"
        color="warning"
        variant="subtle"
        icon="i-lucide-triangle-alert"
        :title="t(errorKey)"
      />
      <p v-if="state === 'scanning'" class="text-xs text-muted">{{ t('admin.crypto.signer.shareInput.scanHint') }}</p>
    </div>
    <template #footer>
      <UButton color="neutral" variant="subtle" data-testid="qr-camera-cancel" @click="close">
        {{ t('admin.crypto.signer.shareInput.scanStop') }}
      </UButton>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import {
  decodeQrFromVideoFrame,
  detectQrCameraSupport,
  openQrCamera,
  QrCameraError,
  stopMediaStream
} from '@/utils/signer/qr'
import { signerErrorI18nKey } from '@/utils/signer/errors'

const props = defineProps<{ show: boolean }>()
const emit = defineEmits<{
  (event: 'decoded', value: string): void
  (event: 'close'): void
}>()
const { t } = useI18n()
const video = ref<HTMLVideoElement | null>(null)
const canvas = ref<HTMLCanvasElement | null>(null)
const state = ref<'idle' | 'starting' | 'scanning' | 'failed'>('idle')
const errorKey = ref('')
let stream: MediaStream | null = null
let poller: ReturnType<typeof setInterval> | null = null

function stop(): void {
  if (poller) {
    clearInterval(poller)
    poller = null
  }
  stopMediaStream(stream)
  stream = null
}

async function start(): Promise<void> {
  stop()
  state.value = 'starting'
  errorKey.value = ''
  const support = detectQrCameraSupport()
  if (!support.supported) {
    state.value = 'failed'
    errorKey.value = `admin.crypto.signer.errors.${support.reason === 'insecure' ? 'cameraInsecure' : 'cameraUnsupported'}`
    return
  }
  try {
    stream = await openQrCamera()
  } catch (error) {
    state.value = 'failed'
    errorKey.value = signerErrorI18nKey(error) ?? 'admin.crypto.signer.errors.cameraDenied'
    if (!(error instanceof QrCameraError)) errorKey.value = 'admin.crypto.signer.errors.cameraUnavailable'
    return
  }
  if (!props.show) {
    stop()
    return
  }
  await nextTick()
  const element = video.value
  if (!element) {
    stop()
    state.value = 'failed'
    errorKey.value = 'admin.crypto.signer.errors.cameraUnavailable'
    return
  }
  element.srcObject = stream
  try {
    await element.play()
  } catch {
    // Autoplay is muted, but locked-down browsers can still reject; scanning
    // continues via the already attached stream once the user taps play.
  }
  state.value = 'scanning'
  if (!canvas.value) canvas.value = document.createElement('canvas')
  poller = setInterval(() => {
    const frame = video.value
    const surface = canvas.value
    if (!frame || !surface || frame.readyState < 2) return
    const decoded = decodeQrFromVideoFrame(frame, surface)
    if (!decoded) return
    stop()
    emit('decoded', decoded)
  }, 250)
}

function close(): void {
  stop()
  state.value = 'idle'
  emit('close')
}

watch(() => props.show, show => {
  if (show) void start()
  else stop()
}, { immediate: true })

onBeforeUnmount(stop)
</script>
