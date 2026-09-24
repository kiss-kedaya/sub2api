<template>
  <BaseDialog
    :show="show"
    :title="t('admin.accounts.testAccountConnection')"
    width="extra-wide"
    @close="handleClose"
  >
    <div class="account-test">
      <div v-if="account" class="account-test-head">
        <div>
          <div class="account-test-name">{{ account.name }}</div>
          <div class="account-test-meta">
            <span class="account-test-type">{{ account.type }}</span>
            <span>{{ t('admin.accounts.account') }}</span>
          </div>
        </div>
        <span class="account-test-status" :class="{ 'is-active': account.status === 'active' }">
          {{ account.status }}
        </span>
      </div>

      <div class="account-test-advanced">
        <div v-if="isGrokAccount" class="account-test-field">
          <label>{{ t('admin.accounts.grok.testMode') }}</label>
          <Select
            v-model="grokTestMode"
            :options="grokTestModeOptions"
            :disabled="standaloneBusy"
          />
          <p>{{ t('admin.accounts.grok.testModeHint') }}</p>
        </div>

        <div v-if="isOpenAIAccount" class="account-test-field">
          <label>{{ t('admin.accounts.openai.testMode') }}</label>
          <Select
            v-model="testMode"
            :options="openAITestModeOptions"
            data-test="openai-test-mode"
          />
        </div>

        <TextArea
          v-model="testPrompt"
          :label="promptInputLabel"
          :placeholder="promptInputPlaceholder"
          :hint="promptInputHint"
          :disabled="standaloneBusy"
          rows="3"
        />

        <div v-if="supportsImageUpload" class="account-test-field">
          <label>{{ imageUploadLabel }}</label>
          <div class="account-test-file">
            <button type="button" class="btn btn-secondary btn-sm" :disabled="standaloneBusy" @click="imageFileInput?.click()">
              {{ t('admin.accounts.grok.chooseImageFile') }}
            </button>
            <span>{{ uploadImageName || t('common.noFileSelected') }}</span>
            <input
              ref="imageFileInput"
              type="file"
              accept="image/png,image/jpeg,image/webp,image/gif"
              class="hidden"
              @change="onImageFileChange"
            />
          </div>
          <img v-if="uploadImagePreview" :src="uploadImagePreview" :alt="t('admin.accounts.grok.uploadPreviewAlt')" class="account-test-upload-preview" />
        </div>

        <div v-if="supportsAudioUpload" class="account-test-field">
          <label>{{ t('admin.accounts.grok.audioUploadLabel') }}</label>
          <div class="account-test-file">
            <button type="button" class="btn btn-secondary btn-sm" :disabled="standaloneBusy" @click="audioFileInput?.click()">
              {{ t('admin.accounts.grok.chooseAudioFile') }}
            </button>
            <span>{{ uploadAudioName || t('common.noFileSelected') }}</span>
            <input
              ref="audioFileInput"
              type="file"
              accept="audio/*,.wav,.mp3,.m4a,.ogg,.webm"
              class="hidden"
              @change="onAudioFileChange"
            />
          </div>
        </div>

        <button
          v-if="isGrokStandaloneMode"
          type="button"
          class="btn btn-secondary"
          data-test="standalone-start"
          :disabled="standaloneBusy"
          @click="startStandalone"
        >
          {{ standaloneBusy ? t('admin.accounts.testing') : t('admin.accounts.startTest') }}
        </button>
        <pre v-if="standaloneOutput" class="account-test-standalone">{{ standaloneOutput }}</pre>
        <p v-if="standaloneDuration !== undefined" data-test="standalone-duration">
          {{ t('admin.accounts.batchTest.duration') }}: {{ formatAccountModelTestDuration(standaloneDuration) }}
        </p>
        <audio v-for="(audio, index) in standaloneResult.audios" :key="'audio-' + index" :src="audio.url" controls />
        <video v-for="(video, index) in standaloneResult.videos" :key="'video-' + index" :src="video.url" controls />
      </div>

      <AccountModelTestPanel
        ref="panel"
        v-if="!isGrokStandaloneMode"
        :show="show"
        :accounts="panelAccounts"
        variant="single"
        :build-body="buildRequestBody"
      />
    </div>

    <template #footer>
      <button type="button" class="btn btn-secondary" @click="handleClose">
        {{ t('common.close') }}
      </button>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Select from '@/components/common/Select.vue'
import TextArea from '@/components/common/TextArea.vue'
import AccountModelTestPanel from '@/components/admin/account/AccountModelTestPanel.vue'
import type { Account } from '@/types'
import {
  applyAccountModelTestEvent,
  createAccountModelTestOutput,
  formatAccountModelTestDuration,
  inferGrokTestMode,
  isMediaHeavyModel,
  streamAccountModelTest,
  type BatchTestAccount
} from '@/utils/accountModelTest'

const props = defineProps<{
  show: boolean
  account: Account | null
}>()

const emit = defineEmits<{
  (e: 'close'): void
}>()

const { t } = useI18n()

const testPrompt = ref('')
const testMode = ref<'default' | 'compact'>('default')
const grokTestMode = ref<'text' | 'image' | 'video' | 'search' | 'tts' | 'stt' | 'realtime'>('text')
const uploadImageDataURL = ref('')
const uploadImagePreview = ref('')
const uploadImageName = ref('')
const uploadAudioDataURL = ref('')
const uploadAudioName = ref('')
const imageFileInput = ref<HTMLInputElement | null>(null)
const audioFileInput = ref<HTMLInputElement | null>(null)
const standaloneBusy = ref(false)
const standaloneOutput = ref('')
const standaloneResult = ref(createAccountModelTestOutput())
const standaloneDuration = ref<number>()
const panel = ref<InstanceType<typeof AccountModelTestPanel> | null>(null)
let standaloneAbort: AbortController | null = null
let imageReadVersion = 0
let audioReadVersion = 0

const isOpenAIAccount = computed(() => props.account?.platform === 'openai')
const isGrokAccount = computed(() => props.account?.platform === 'grok')
const openAITestModeOptions = computed(() => [
  { value: 'default', label: t('admin.accounts.openai.testModeDefault') },
  { value: 'compact', label: t('admin.accounts.openai.testModeCompact') }
])
const grokTestModeOptions = computed(() => [
  { value: 'text', label: t('admin.accounts.grok.testModeText') },
  { value: 'image', label: t('admin.accounts.grok.testModeImage') },
  { value: 'video', label: t('admin.accounts.grok.testModeVideo') },
  { value: 'search', label: t('admin.accounts.grok.testModeSearch') },
  { value: 'tts', label: t('admin.accounts.grok.testModeTTS') },
  { value: 'stt', label: t('admin.accounts.grok.testModeSTT') },
  { value: 'realtime', label: t('admin.accounts.grok.testModeRealtime') }
])

const isGrokStandaloneMode = computed(() =>
  isGrokAccount.value &&
  (grokTestMode.value === 'search' ||
    grokTestMode.value === 'tts' ||
    grokTestMode.value === 'stt' ||
    grokTestMode.value === 'realtime')
)

const panelAccounts = computed<BatchTestAccount[]>(() => {
  if (!props.account) return []
  return [{
    id: props.account.id,
    name: props.account.name,
    platform: props.account.platform,
    type: props.account.type
  }]
})

const supportsImageUpload = computed(
  () => isGrokAccount.value && (grokTestMode.value === 'image' || grokTestMode.value === 'video')
)
const supportsAudioUpload = computed(() => isGrokAccount.value && grokTestMode.value === 'stt')
const imageUploadLabel = computed(() =>
  grokTestMode.value === 'video'
    ? t('admin.accounts.grok.videoFirstFrameLabel')
    : t('admin.accounts.grok.imageUploadLabel')
)

const promptInputLabel = computed(() => {
  if (grokTestMode.value === 'video') return t('admin.accounts.videoPromptLabel')
  if (grokTestMode.value === 'search') return t('admin.accounts.grok.searchQueryLabel')
  if (grokTestMode.value === 'tts') return t('admin.accounts.grok.ttsTextLabel')
  if (grokTestMode.value === 'image' || isMediaHeavyModel(testPrompt.value)) return t('admin.accounts.imagePromptLabel')
  return t('admin.accounts.imagePromptLabel')
})

const promptInputPlaceholder = computed(() => {
  if (grokTestMode.value === 'video') return t('admin.accounts.videoPromptPlaceholder')
  if (grokTestMode.value === 'search') return t('admin.accounts.grok.searchQueryPlaceholder')
  if (grokTestMode.value === 'tts') return t('admin.accounts.grok.ttsTextPlaceholder')
  return t('admin.accounts.imagePromptPlaceholder')
})

const promptInputHint = computed(() => {
  if (grokTestMode.value === 'video') return t('admin.accounts.videoTestHint')
  if (grokTestMode.value === 'search') return t('admin.accounts.grok.searchTestHint')
  if (grokTestMode.value === 'tts') return t('admin.accounts.grok.ttsTestHint')
  if (grokTestMode.value === 'stt') return t('admin.accounts.grok.sttTestHint')
  if (grokTestMode.value === 'realtime') return t('admin.accounts.grok.realtimeTestHint')
  if (grokTestMode.value === 'image') return t('admin.accounts.imageTestHint')
  return t('admin.accounts.batchTest.promptHint')
})

const readFileAsDataURL = (file: File): Promise<string> =>
  new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => resolve(String(reader.result || ''))
    reader.onerror = () => reject(new Error(t('admin.accounts.grok.fileReadFailed')))
    reader.readAsDataURL(file)
  })

const onImageFileChange = async (event: Event) => {
  const version = ++imageReadVersion
  uploadImageDataURL.value = ''
  uploadImagePreview.value = ''
  uploadImageName.value = ''
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  if (!file) {
    uploadImageDataURL.value = ''
    uploadImagePreview.value = ''
    uploadImageName.value = ''
    return
  }
  if (file.size > 6 * 1024 * 1024) {
    input.value = ''
    return
  }
  try {
    const dataURL = await readFileAsDataURL(file)
    if (version !== imageReadVersion) return
    uploadImageDataURL.value = dataURL
    uploadImagePreview.value = dataURL
    uploadImageName.value = file.name
  } catch {
    if (version !== imageReadVersion) return
    uploadImageDataURL.value = ''
    uploadImagePreview.value = ''
    uploadImageName.value = ''
  }
}

const onAudioFileChange = async (event: Event) => {
  const version = ++audioReadVersion
  uploadAudioDataURL.value = ''
  uploadAudioName.value = ''
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  if (!file) {
    uploadAudioDataURL.value = ''
    uploadAudioName.value = ''
    return
  }
  if (file.size > 6 * 1024 * 1024) {
    input.value = ''
    return
  }
  try {
    const dataURL = await readFileAsDataURL(file)
    if (version !== audioReadVersion) return
    uploadAudioDataURL.value = dataURL
    uploadAudioName.value = file.name
  } catch {
    if (version !== audioReadVersion) return
    uploadAudioDataURL.value = ''
    uploadAudioName.value = ''
  }
}

const applyDefaultPrompt = () => {
  if (testPrompt.value.trim()) return
  if (grokTestMode.value === 'video') {
    testPrompt.value = t('admin.accounts.videoPromptDefault')
    return
  }
  if (grokTestMode.value === 'search') {
    testPrompt.value = t('admin.accounts.grok.searchQueryDefault')
    return
  }
  if (grokTestMode.value === 'tts') {
    testPrompt.value = t('admin.accounts.grok.ttsTextDefault')
    return
  }
  if (grokTestMode.value === 'image' || !isGrokAccount.value) {
    testPrompt.value = t('admin.accounts.imagePromptDefault')
  }
}

const promptForModel = (modelId: string) => {
  if (isGrokStandaloneMode.value) return testPrompt.value.trim()
  const mode = isGrokAccount.value ? inferGrokTestMode(modelId) : ''
  if (mode === 'image' || mode === 'video' || isMediaHeavyModel(modelId)) {
    return testPrompt.value.trim()
  }
  return ''
}

const buildRequestBody = (modelId: string, _account?: BatchTestAccount) => {
  const requestBody: {
    model_id: string
    prompt: string
    mode?: string
    image_data_url?: string
    audio_data_url?: string
  } = {
    model_id: modelId,
    prompt: promptForModel(modelId)
  }
  if (isOpenAIAccount.value) {
    requestBody.mode = testMode.value
  }
  if (isGrokAccount.value) {
    requestBody.mode = isGrokStandaloneMode.value ? grokTestMode.value : inferGrokTestMode(modelId)
    if (uploadImageDataURL.value && (requestBody.mode === 'image' || requestBody.mode === 'video')) {
      requestBody.image_data_url = uploadImageDataURL.value
    }
    if (uploadAudioDataURL.value && grokTestMode.value === 'stt') {
      requestBody.audio_data_url = uploadAudioDataURL.value
    }
  }
  return requestBody
}

const startStandalone = async () => {
  if (!props.show || !props.account || !isGrokStandaloneMode.value || standaloneBusy.value) return
  standaloneBusy.value = true
  standaloneOutput.value = ''
  standaloneAbort = new AbortController()
  const controller = standaloneAbort
  standaloneResult.value = createAccountModelTestOutput()
  standaloneDuration.value = undefined
  const state = standaloneResult.value
  const started = performance.now()
  const updateOutput = () => {
    standaloneOutput.value = [state.error, state.output.trim()].filter(Boolean).join('\n\n')
  }
  try {
    await streamAccountModelTest({
      accountId: props.account.id,
      body: {
        ...buildRequestBody(''),
        model_id: '',
        mode: grokTestMode.value
      },
      signal: controller.signal,
      onEvent: (event) => {
        if (controller.signal.aborted) return
        applyAccountModelTestEvent(state, event)
        updateOutput()
      }
    })
    if (controller.signal.aborted) return
    if (state.success !== true && !state.error) state.error = t('admin.accounts.testFailed')
    updateOutput()
    if (!standaloneOutput.value) standaloneOutput.value = t('admin.accounts.testCompleted')
  } catch (error) {
    if (!controller.signal.aborted) {
      state.error = error instanceof Error ? error.message : t('common.unknownError')
      updateOutput()
    }
  } finally {
    if (standaloneAbort === controller) {
      standaloneDuration.value = Math.round(performance.now() - started)
      standaloneBusy.value = false
      standaloneAbort = null
    }
  }
}

const stopStandalone = () => {
  standaloneAbort?.abort()
  standaloneAbort = null
  standaloneBusy.value = false
  standaloneOutput.value = ''
  standaloneResult.value = createAccountModelTestOutput()
  standaloneDuration.value = undefined
  imageReadVersion += 1
  audioReadVersion += 1
}

const handleClose = () => {
  stopStandalone()
  panel.value?.stopRun()
  emit('close')
}

watch(
  () => [props.show, props.account?.id, props.account?.platform, props.account?.type] as const,
  ([show]) => {
    stopStandalone()
    if (!show) return
    testMode.value = 'default'
    grokTestMode.value = 'text'
    testPrompt.value = ''
    standaloneOutput.value = ''
    uploadImageDataURL.value = ''
    uploadImagePreview.value = ''
    uploadImageName.value = ''
    uploadAudioDataURL.value = ''
    uploadAudioName.value = ''
    applyDefaultPrompt()
  },
  { immediate: true }
)

watch([grokTestMode, testMode], () => {
  stopStandalone()
  panel.value?.stopRun()
  if (props.show) applyDefaultPrompt()
})

onBeforeUnmount(stopStandalone)

defineExpose({
  testMode,
  grokTestMode,
  testPrompt,
  buildRequestBody
})
</script>

<style scoped>
.account-test { display: grid; gap: 12px; color: var(--signal-text); }
.account-test-head,
.account-test-file {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}
.account-test-head {
  padding: 10px 12px;
  border: 1px solid var(--signal-line);
  border-radius: 8px;
  background: var(--signal-surface);
}
.account-test-name { font-weight: 650; }
.account-test-meta { display: flex; gap: 8px; font-size: 12px; color: var(--signal-muted); }
.account-test-type {
  text-transform: uppercase;
  font-size: 10px;
  padding: 1px 6px;
  border-radius: 999px;
  background: var(--signal-bg);
}
.account-test-status { font-size: 12px; color: var(--signal-muted); }
.account-test-status.is-active { color: #1f9d55; }
.account-test-advanced {
  display: grid;
  gap: 10px;
  padding: 10px 12px;
  border: 1px solid var(--signal-line);
  border-radius: 8px;
  background: var(--signal-surface);
}
.account-test-field { display: grid; gap: 6px; }
.account-test-field label { font-size: 13px; font-weight: 600; }
.account-test-field p,
.account-test-file span { margin: 0; font-size: 12px; color: var(--signal-muted); }
.account-test-upload-preview {
  max-height: 120px;
  object-fit: contain;
  border-radius: 8px;
  border: 1px solid var(--signal-line);
}
.account-test-standalone {
  margin: 0;
  max-height: 160px;
  overflow: auto;
  white-space: pre-wrap;
  font-size: 12px;
}
.hidden { display: none; }
</style>
