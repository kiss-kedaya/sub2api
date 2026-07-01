<template>
  <AppLayout>
    <div class="mx-auto flex max-w-[1600px] flex-col gap-5">
      <section class="card overflow-hidden">
        <div class="flex flex-col gap-4 border-b border-gray-100 p-5 dark:border-dark-700 md:flex-row md:items-start md:justify-between">
          <div class="min-w-0">
            <div class="flex flex-wrap items-center gap-2">
              <div class="flex h-10 w-10 items-center justify-center rounded-xl bg-primary-500/10 text-primary-600 dark:bg-primary-500/15 dark:text-primary-300">
                <Icon name="beaker" size="lg" />
              </div>
              <div>
                <h1 class="text-xl font-semibold text-gray-950 dark:text-white">{{ t('llmTester.title') }}</h1>
                <p class="mt-1 text-sm text-gray-500 dark:text-dark-300">{{ t('llmTester.description') }}</p>
              </div>
            </div>
          </div>
          <div class="flex flex-wrap gap-2">
            <button type="button" class="btn btn-secondary btn-sm" @click="startNewProfile('openrouter')">
              <Icon name="sparkles" size="sm" />
              OpenRouter
            </button>
            <button type="button" class="btn btn-secondary btn-sm" @click="startNewProfile('sub2api')">
              <Icon name="server" size="sm" />
              Sub2API
            </button>
            <button type="button" class="btn btn-primary btn-sm" @click="saveProfile">
              <Icon name="check" size="sm" />
              {{ t('common.save') }}
            </button>
          </div>
        </div>

        <div class="grid gap-4 p-5 xl:grid-cols-[minmax(280px,0.8fr)_minmax(0,1.8fr)]">
          <div class="grid gap-4 sm:grid-cols-2 xl:grid-cols-1">
            <label class="block">
              <span class="input-label">{{ t('llmTester.profile') }}</span>
              <select v-model="selectedProfileId" class="input" @change="handleProfileSelect">
                <option value="">{{ t('llmTester.newProfile') }}</option>
                <option v-for="profile in profiles" :key="profile.id" :value="profile.id">
                  {{ profile.name }}
                </option>
              </select>
            </label>

            <label class="block">
              <span class="input-label">{{ t('llmTester.provider') }}</span>
              <select v-model="form.provider" class="input" @change="applyProviderPreset">
                <option value="openrouter">OpenRouter</option>
                <option value="sub2api">Sub2API</option>
                <option value="custom">{{ t('llmTester.customProvider') }}</option>
              </select>
            </label>
          </div>

          <div class="grid gap-4 md:grid-cols-2 2xl:grid-cols-4">
            <label class="block">
              <span class="input-label">{{ t('common.name') }}</span>
              <input v-model.trim="form.name" type="text" class="input" :placeholder="t('llmTester.profileNamePlaceholder')" />
            </label>

            <label class="block md:col-span-2 2xl:col-span-1">
              <span class="input-label">{{ t('llmTester.baseUrl') }}</span>
              <input v-model="form.baseUrl" type="url" class="input font-mono" placeholder="/v1 or https://openrouter.ai/api/v1" />
            </label>

            <label class="block">
              <span class="input-label">{{ t('llmTester.apiKey') }}</span>
              <div class="relative">
                <input
                  v-model.trim="form.apiKey"
                  :type="showApiKey ? 'text' : 'password'"
                  class="input pr-11 font-mono"
                  placeholder="sk-..."
                />
                <button
                  type="button"
                  class="absolute right-2 top-1/2 -translate-y-1/2 rounded-lg p-1.5 text-gray-400 transition-colors hover:bg-gray-100 hover:text-gray-700 dark:hover:bg-dark-700 dark:hover:text-gray-200"
                  :title="showApiKey ? t('llmTester.hideKey') : t('llmTester.showKey')"
                  @click="showApiKey = !showApiKey"
                >
                  <Icon :name="showApiKey ? 'eyeOff' : 'eye'" size="sm" />
                </button>
              </div>
            </label>
          </div>
        </div>

        <div class="flex flex-col gap-4 border-t border-gray-100 p-5 dark:border-dark-700 lg:flex-row lg:items-end">
          <label class="block flex-1">
            <span class="input-label">{{ t('llmTester.model') }}</span>
            <select v-model="form.selectedModel" class="input">
              <option value="">{{ t('llmTester.selectModel') }}</option>
              <option v-for="model in filteredModels" :key="model.id" :value="model.id">
                {{ model.id }} · {{ modelCapabilityLabel(model) }}
              </option>
            </select>
          </label>

          <label class="block flex-1">
            <span class="input-label">{{ t('common.search') }}</span>
            <div class="relative">
              <Icon name="search" size="md" class="absolute left-3 top-1/2 -translate-y-1/2 text-gray-400" />
              <input v-model.trim="modelSearch" type="text" class="input pl-10" :placeholder="t('llmTester.searchModels')" />
            </div>
          </label>

          <div class="flex flex-wrap items-center gap-2">
            <button type="button" class="btn btn-secondary" :disabled="loadingModels" @click="loadModels">
              <Icon name="refresh" size="md" :class="loadingModels ? 'animate-spin' : ''" />
              {{ t('llmTester.fetchModels') }}
            </button>
            <button v-if="selectedProfileId" type="button" class="btn btn-danger" @click="deleteProfile">
              <Icon name="trash" size="md" />
              {{ t('common.delete') }}
            </button>
          </div>
        </div>

        <div class="flex flex-wrap gap-2 border-t border-gray-100 px-5 py-4 dark:border-dark-700">
          <span class="rounded-full bg-gray-100 px-3 py-1 text-xs font-medium text-gray-600 dark:bg-dark-700 dark:text-dark-200">
            {{ t('llmTester.modelCount', { count: supportedModels.length }) }}
          </span>
          <span v-if="activeProfile?.lastFetchedAt" class="rounded-full bg-gray-100 px-3 py-1 text-xs font-medium text-gray-600 dark:bg-dark-700 dark:text-dark-200">
            {{ t('llmTester.lastFetched', { time: formatTime(activeProfile.lastFetchedAt) }) }}
          </span>
          <span class="rounded-full bg-amber-50 px-3 py-1 text-xs font-medium text-amber-700 dark:bg-amber-500/10 dark:text-amber-200">
            {{ t('llmTester.localStorageNotice') }}
          </span>
        </div>
      </section>

      <section class="grid min-h-[620px] gap-5 xl:grid-cols-[340px_minmax(0,1fr)]">
        <aside class="flex min-h-0 flex-col gap-5">
          <div class="card p-4">
            <div class="mb-3 flex items-center justify-between">
              <h2 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('llmTester.savedProfiles') }}</h2>
              <button type="button" class="btn btn-ghost btn-sm" @click="startNewProfile()">
                <Icon name="plus" size="sm" />
                {{ t('common.add') }}
              </button>
            </div>
            <div v-if="profiles.length" class="space-y-2">
              <button
                v-for="profile in profiles"
                :key="profile.id"
                type="button"
                class="w-full rounded-xl border px-3 py-2 text-left transition-colors"
                :class="profile.id === selectedProfileId
                  ? 'border-primary-300 bg-primary-50 text-primary-900 dark:border-primary-700 dark:bg-primary-500/10 dark:text-primary-100'
                  : 'border-gray-200 bg-white text-gray-700 hover:bg-gray-50 dark:border-dark-700 dark:bg-dark-800/70 dark:text-dark-200 dark:hover:bg-dark-700'"
                @click="selectProfile(profile.id)"
              >
                <span class="block truncate text-sm font-medium">{{ profile.name }}</span>
                <span class="mt-1 block truncate text-xs opacity-70">{{ profile.baseUrl }}</span>
                <span class="mt-1 block truncate font-mono text-xs opacity-70">{{ maskKey(profile.apiKey) }}</span>
              </button>
            </div>
            <div v-else class="rounded-xl border border-dashed border-gray-200 p-4 text-center text-sm text-gray-500 dark:border-dark-700 dark:text-dark-300">
              {{ t('llmTester.noProfiles') }}
            </div>
          </div>

          <div class="card p-4">
            <h2 class="mb-3 text-sm font-semibold text-gray-900 dark:text-white">{{ t('llmTester.requestOptions') }}</h2>
            <div class="space-y-4">
              <label class="block">
                <span class="input-label">{{ t('llmTester.temperature') }}</span>
                <input v-model.number="temperature" type="range" min="0" max="2" step="0.1" class="w-full accent-primary-500" />
                <div class="mt-1 text-xs text-gray-500 dark:text-dark-300">{{ temperature.toFixed(1) }}</div>
              </label>
              <label class="block">
                <span class="input-label">{{ t('llmTester.maxTokens') }}</span>
                <input v-model.number="maxTokens" type="number" min="1" class="input" />
              </label>
              <label class="block">
                <span class="input-label">{{ t('llmTester.systemInstruction') }}</span>
                <textarea v-model="systemInstruction" rows="5" class="input resize-none" :placeholder="t('llmTester.systemInstructionPlaceholder')"></textarea>
              </label>
            </div>
          </div>
        </aside>

        <div class="card flex min-h-0 flex-col overflow-hidden">
          <div class="flex flex-wrap items-center justify-between gap-3 border-b border-gray-100 px-5 py-4 dark:border-dark-700">
            <div class="min-w-0">
              <h2 class="truncate text-base font-semibold text-gray-900 dark:text-white">{{ t('llmTester.chat') }}</h2>
              <div class="mt-1 flex flex-wrap items-center gap-2 text-xs text-gray-500 dark:text-dark-300">
                <span class="truncate">{{ form.selectedModel || t('llmTester.noModelSelected') }}</span>
                <span v-if="selectedRequestModeLabel" class="rounded-full bg-gray-100 px-2 py-0.5 font-medium text-gray-600 dark:bg-dark-700 dark:text-dark-200">
                  {{ selectedRequestModeLabel }}
                </span>
              </div>
            </div>
            <div class="flex flex-wrap items-center gap-2">
              <button type="button" class="btn btn-secondary btn-sm" :disabled="messages.length === 0 || sending" @click="clearChat">
                <Icon name="trash" size="sm" />
                {{ t('llmTester.clearChat') }}
              </button>
              <button v-if="sending" type="button" class="btn btn-warning btn-sm" @click="cancelChat">
                <Icon name="ban" size="sm" />
                {{ t('llmTester.cancel') }}
              </button>
            </div>
          </div>

          <div ref="chatScrollRef" class="flex-1 space-y-5 overflow-y-auto bg-gray-50/60 p-5 dark:bg-dark-950/50">
            <div v-if="messages.length === 0" class="flex h-full min-h-[320px] items-center justify-center">
              <div class="max-w-md rounded-2xl border border-dashed border-gray-200 bg-white/70 p-6 text-center dark:border-dark-700 dark:bg-dark-800/60">
                <Icon name="chat" size="xl" class="mx-auto text-primary-500" />
                <h3 class="mt-3 text-base font-semibold text-gray-900 dark:text-white">{{ t('llmTester.emptyChatTitle') }}</h3>
                <p class="mt-2 text-sm text-gray-500 dark:text-dark-300">{{ t('llmTester.emptyChatDescription') }}</p>
              </div>
            </div>

            <div
              v-for="message in messages"
              :key="message.id"
              class="flex"
              :class="message.role === 'user' ? 'justify-end' : 'justify-start'"
            >
              <article
                class="max-w-[92%] rounded-2xl border px-4 py-3 shadow-sm md:max-w-[82%]"
                :class="message.role === 'user'
                  ? 'border-primary-200 bg-primary-50 text-primary-950 dark:border-primary-700 dark:bg-primary-900/40 dark:text-primary-50'
                  : message.status === 'error'
                    ? 'border-red-200 bg-red-50 text-red-950 dark:border-red-800 dark:bg-red-500/10 dark:text-red-100'
                    : 'border-gray-200 bg-white text-gray-900 dark:border-dark-700 dark:bg-dark-800 dark:text-gray-100'"
              >
                <div class="mb-2 flex flex-wrap items-center gap-2 text-xs opacity-70">
                  <span class="font-medium">{{ message.role === 'user' ? t('llmTester.you') : t('llmTester.assistant') }}</span>
                  <span>{{ formatTime(message.createdAt) }}</span>
                  <span v-if="message.model" class="font-mono">{{ message.model }}</span>
                  <Icon v-if="message.status === 'sending'" name="refresh" size="xs" class="animate-spin" />
                </div>

                <div class="llm-markdown" v-html="renderMarkdown(message.content || (message.status === 'sending' ? t('llmTester.thinking') : ''))"></div>
                <AttachmentPreview
                  v-if="message.attachments?.length"
                  :attachments="message.attachments"
                  class="mt-3"
                  @open="openAttachmentPreview"
                  @download="downloadAttachment"
                />
              </article>
            </div>
          </div>

          <div class="border-t border-gray-100 bg-white p-4 dark:border-dark-700 dark:bg-dark-800">
            <AttachmentPreview
              v-if="pendingAttachments.length"
              :attachments="pendingAttachments"
              removable
              class="mb-3"
              @open="openAttachmentPreview"
              @download="downloadAttachment"
              @remove="removePendingAttachment"
            />

            <div class="flex items-end gap-3">
              <input
                ref="fileInputRef"
                type="file"
                class="hidden"
                multiple
                accept="image/*,audio/*,video/*,.txt,.md,.json,.js,.jsx,.ts,.tsx,.vue,.py,.go,.rs,.java,.c,.cpp,.cs,.html,.css,.sql,.yaml,.yml,.xml,.csv,.log,.sh,.toml,.env"
                @change="handleFileInput"
              />
              <button type="button" class="btn btn-secondary btn-icon flex-shrink-0" :title="t('llmTester.attachFiles')" @click="fileInputRef?.click()">
                <Icon name="upload" size="md" />
              </button>

              <textarea
                v-model="prompt"
                rows="3"
                class="input min-h-[76px] flex-1 resize-none"
                :placeholder="selectedPromptPlaceholder"
                @keydown.enter.exact.prevent="sendMessage"
              ></textarea>

              <button type="button" class="btn btn-primary min-h-[44px] flex-shrink-0" :disabled="!canSend || sending" @click="sendMessage">
                <Icon :name="sending ? 'refresh' : 'arrowRight'" size="md" :class="sending ? 'animate-spin' : ''" />
                <span class="hidden sm:inline">{{ t('llmTester.send') }}</span>
              </button>
            </div>
          </div>
        </div>
      </section>
    </div>

    <div
      v-if="previewAttachment?.dataUrl"
      class="fixed inset-0 z-50 flex items-center justify-center bg-black/80 p-4"
      role="dialog"
      aria-modal="true"
      :aria-label="previewAttachment.name"
      @click.self="previewAttachment = null"
    >
      <div class="flex max-h-full w-full max-w-6xl flex-col overflow-hidden rounded-2xl border border-dark-700 bg-dark-950 shadow-2xl">
        <div class="flex flex-wrap items-center justify-between gap-3 border-b border-dark-700 px-4 py-3">
          <div class="min-w-0">
            <h2 class="truncate text-sm font-semibold text-white">{{ previewAttachment.name }}</h2>
            <p class="mt-1 text-xs text-dark-300">{{ formatAttachmentSize(previewAttachment.size) }}</p>
          </div>
          <div class="flex items-center gap-2">
            <button type="button" class="btn btn-secondary btn-sm" @click="openAttachmentInNewTab(previewAttachment)">
              <Icon name="externalLink" size="sm" />
              {{ t('llmTester.openAttachment') }}
            </button>
            <button type="button" class="btn btn-primary btn-sm" @click="downloadAttachment(previewAttachment)">
              <Icon name="download" size="sm" />
              {{ t('llmTester.downloadAttachment') }}
            </button>
            <button
              type="button"
              class="rounded-lg p-2 text-dark-200 transition-colors hover:bg-dark-800 hover:text-white"
              :title="t('common.close')"
              @click="previewAttachment = null"
            >
              <Icon name="x" size="md" />
            </button>
          </div>
        </div>
        <div class="min-h-0 flex-1 overflow-auto bg-black p-4">
          <img
            v-if="previewAttachment.kind === 'image'"
            :src="previewAttachment.dataUrl"
            :alt="previewAttachment.name"
            class="mx-auto max-h-[78vh] max-w-full rounded-xl object-contain"
          />
          <video
            v-else-if="previewAttachment.type.startsWith('video/')"
            :src="previewAttachment.dataUrl"
            class="mx-auto max-h-[78vh] max-w-full rounded-xl"
            controls
          ></video>
          <div v-else class="flex min-h-[40vh] items-center justify-center text-sm text-dark-200">
            <a :href="previewAttachment.dataUrl" target="_blank" rel="noreferrer" class="underline">
              {{ t('llmTester.openAttachment') }}
            </a>
          </div>
        </div>
      </div>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, defineComponent, h, nextTick, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { marked } from 'marked'
import DOMPurify from 'dompurify'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import { useAppStore } from '@/stores/app'
import {
  OPENROUTER_BASE_URL,
  defaultSub2APIBaseUrl,
  fetchLLMModels,
  getLLMTesterModelCapabilities,
  isLLMTesterSupportedModel,
  normalizeBaseUrl,
  sendLLMChatCompletion,
  sendLLMImageGeneration,
  sendLLMVideoGeneration,
  type LLMTesterAttachment,
  type LLMTesterMessage,
  type LLMTesterModel,
  type LLMTesterModelCapability,
  type LLMTesterProfile,
} from '@/api/llmTester'

type Provider = LLMTesterProfile['provider']
type ChatStatus = 'sent' | 'sending' | 'error'
type RequestMode = 'chat' | 'image_generation' | 'video_generation'

interface ChatViewMessage extends LLMTesterMessage {
  createdAt: string
  status: ChatStatus
  model?: string
}

const AttachmentPreview = defineComponent({
  name: 'AttachmentPreview',
  props: {
    attachments: {
      type: Array as () => LLMTesterAttachment[],
      required: true,
    },
    removable: {
      type: Boolean,
      default: false,
    },
  },
  emits: ['remove', 'open', 'download'],
  setup(props, { emit, attrs }) {
    const { t } = useI18n()

    return () => h('div', { ...attrs, class: ['flex flex-wrap gap-2', attrs.class] }, props.attachments.map((attachment) => {
      const canOpen = (attachment.kind === 'image' || attachment.kind === 'media') && Boolean(attachment.dataUrl)
      const canDownload = Boolean(attachment.dataUrl || attachment.text)
      const preview = canOpen && attachment.kind === 'image'
        ? h('button', {
          type: 'button',
          class: 'h-12 w-12 overflow-hidden rounded-lg ring-1 ring-gray-200 transition hover:ring-primary-400 focus:outline-none focus:ring-2 focus:ring-primary-500 dark:ring-dark-700',
          title: t('llmTester.openAttachment'),
          onClick: () => emit('open', attachment),
        }, [
          h('img', {
            src: attachment.dataUrl,
            alt: attachment.name,
            class: 'h-full w-full object-cover',
          }),
        ])
        : canOpen
          ? h('button', {
            type: 'button',
            class: 'flex h-12 w-12 items-center justify-center rounded-lg bg-gray-100 text-[10px] font-semibold text-gray-500 ring-1 ring-gray-200 transition hover:ring-primary-400 focus:outline-none focus:ring-2 focus:ring-primary-500 dark:bg-dark-700 dark:text-dark-300 dark:ring-dark-600',
            title: t('llmTester.openAttachment'),
            onClick: () => emit('open', attachment),
          }, 'AV')
        : h('span', {
          class: 'flex h-12 w-12 items-center justify-center rounded-lg bg-gray-100 text-[10px] font-semibold text-gray-500 dark:bg-dark-700 dark:text-dark-300',
        }, attachment.kind === 'text' ? '{}' : attachment.kind === 'media' ? 'AV' : 'FILE')

      return h('div', {
        key: attachment.id,
        class: 'flex max-w-full items-center gap-2 rounded-xl border border-gray-200 bg-white px-2.5 py-2 text-xs text-gray-700 dark:border-dark-700 dark:bg-dark-900 dark:text-dark-200',
      }, [
        preview,
        h('span', { class: 'min-w-0 max-w-[220px] flex-1 truncate' }, attachment.name),
        h('div', { class: 'ml-auto flex shrink-0 items-center gap-1' }, [
          canOpen
            ? h('button', {
              type: 'button',
              class: 'rounded-md p-1.5 text-gray-400 transition-colors hover:bg-gray-100 hover:text-gray-700 focus:outline-none focus:ring-2 focus:ring-primary-500 dark:hover:bg-dark-700 dark:hover:text-gray-100',
              title: t('llmTester.openAttachment'),
              onClick: () => emit('open', attachment),
            }, [h(Icon, { name: 'externalLink', size: 'xs' })])
            : null,
          canDownload
            ? h('button', {
              type: 'button',
              class: 'rounded-md p-1.5 text-gray-400 transition-colors hover:bg-gray-100 hover:text-gray-700 focus:outline-none focus:ring-2 focus:ring-primary-500 dark:hover:bg-dark-700 dark:hover:text-gray-100',
              title: t('llmTester.downloadAttachment'),
              onClick: () => emit('download', attachment),
            }, [h(Icon, { name: 'download', size: 'xs' })])
            : null,
          props.removable
            ? h('button', {
              type: 'button',
              class: 'rounded-md p-1.5 text-gray-400 transition-colors hover:bg-gray-100 hover:text-gray-700 focus:outline-none focus:ring-2 focus:ring-primary-500 dark:hover:bg-dark-700 dark:hover:text-gray-100',
              title: t('llmTester.removeAttachment'),
              onClick: () => emit('remove', attachment.id),
            }, [h(Icon, { name: 'x', size: 'xs' })])
            : null,
        ]),
      ])
    }))
  },
})

const { t } = useI18n()
const appStore = useAppStore()

const PROFILE_STORAGE_KEY = 'sub2api.llmTester.profiles.v1'
const ACTIVE_PROFILE_STORAGE_KEY = 'sub2api.llmTester.activeProfile.v1'
const MAX_TEXT_ATTACHMENT_BYTES = 240 * 1024
const MAX_IMAGE_ATTACHMENT_BYTES = 5 * 1024 * 1024
const TEXT_FILE_EXTENSIONS = new Set([
  'txt',
  'md',
  'json',
  'js',
  'jsx',
  'ts',
  'tsx',
  'vue',
  'py',
  'go',
  'rs',
  'java',
  'c',
  'cpp',
  'cs',
  'html',
  'css',
  'sql',
  'yaml',
  'yml',
  'xml',
  'csv',
  'log',
  'sh',
  'toml',
  'env',
])

const profiles = ref<LLMTesterProfile[]>([])
const selectedProfileId = ref('')
const models = ref<LLMTesterModel[]>([])
const modelSearch = ref('')
const loadingModels = ref(false)
const showApiKey = ref(false)
const prompt = ref('')
const pendingAttachments = ref<LLMTesterAttachment[]>([])
const messages = ref<ChatViewMessage[]>([])
const previewAttachment = ref<LLMTesterAttachment | null>(null)
const sending = ref(false)
const systemInstruction = ref('')
const temperature = ref(0.7)
const maxTokens = ref(2048)
const fileInputRef = ref<HTMLInputElement | null>(null)
const chatScrollRef = ref<HTMLElement | null>(null)
let modelAbortController: AbortController | null = null
let chatAbortController: AbortController | null = null

const form = reactive({
  provider: 'sub2api' as Provider,
  name: 'Current Sub2API',
  baseUrl: defaultSub2APIBaseUrl(),
  apiKey: '',
  selectedModel: '',
})

const activeProfile = computed(() => profiles.value.find((profile) => profile.id === selectedProfileId.value))

const supportedModels = computed(() => models.value.filter(isLLMTesterSupportedModel))

const filteredModels = computed(() => {
  const q = modelSearch.value.trim().toLowerCase()
  if (!q) return supportedModels.value
  return supportedModels.value.filter((model) => {
    return (
      model.id.toLowerCase().includes(q) ||
      model.name.toLowerCase().includes(q) ||
      (model.ownedBy || '').toLowerCase().includes(q)
    )
  })
})

const selectedModel = computed<LLMTesterModel | null>(() => {
  if (!form.selectedModel) return null
  return models.value.find((model) => model.id === form.selectedModel) || {
    id: form.selectedModel,
    name: form.selectedModel,
  }
})

const selectedCapabilities = computed<LLMTesterModelCapability[]>(() => {
  return selectedModel.value ? getLLMTesterModelCapabilities(selectedModel.value) : []
})

const selectedRequestMode = computed<RequestMode | null>(() => {
  if (selectedCapabilities.value.includes('chat')) return 'chat'
  if (selectedCapabilities.value.includes('image_generation')) return 'image_generation'
  if (selectedCapabilities.value.includes('video_generation')) return 'video_generation'
  return null
})

const selectedRequestModeLabel = computed(() => {
  if (selectedRequestMode.value === 'video_generation') return t('llmTester.capabilities.videoGeneration')
  if (selectedRequestMode.value === 'image_generation') return t('llmTester.capabilities.imageGeneration')
  if (selectedCapabilities.value.includes('vision')) return t('llmTester.capabilities.vision')
  if (selectedRequestMode.value === 'chat') return t('llmTester.capabilities.chat')
  return ''
})

const selectedPromptPlaceholder = computed(() => {
  if (selectedRequestMode.value === 'video_generation') return t('llmTester.videoPromptPlaceholder')
  return selectedRequestMode.value === 'image_generation'
    ? t('llmTester.imagePromptPlaceholder')
    : t('llmTester.promptPlaceholder')
})

const hasGenerationPrompt = computed(() => {
  return Boolean(
    prompt.value.trim() ||
    pendingAttachments.value.some((attachment) => attachment.kind === 'text' && attachment.text?.trim())
  )
})

const canSend = computed(() => {
  const mode = selectedRequestMode.value
  return Boolean(
    normalizeBaseUrl(form.baseUrl) &&
    form.apiKey.trim() &&
    form.selectedModel &&
    mode &&
    (mode === 'image_generation' || mode === 'video_generation'
      ? hasGenerationPrompt.value
      : (prompt.value.trim() || pendingAttachments.value.length > 0))
  )
})

marked.setOptions({
  gfm: true,
  breaks: true,
})

function createId(prefix: string): string {
  if (typeof crypto !== 'undefined' && 'randomUUID' in crypto) {
    return `${prefix}-${crypto.randomUUID()}`
  }
  return `${prefix}-${Date.now()}-${Math.random().toString(36).slice(2)}`
}

function persistProfiles() {
  try {
    localStorage.setItem(PROFILE_STORAGE_KEY, JSON.stringify(profiles.value))
    localStorage.setItem(ACTIVE_PROFILE_STORAGE_KEY, selectedProfileId.value)
  } catch (error) {
    console.error('Failed to persist LLM tester profiles:', error)
    appStore.showError(t('llmTester.errors.saveFailed'))
  }
}

function loadProfilesFromStorage() {
  try {
    const raw = localStorage.getItem(PROFILE_STORAGE_KEY)
    if (raw) {
      const parsed = JSON.parse(raw)
      if (Array.isArray(parsed)) {
        profiles.value = parsed.filter((profile): profile is LLMTesterProfile => (
          profile &&
          typeof profile === 'object' &&
          typeof profile.id === 'string' &&
          typeof profile.name === 'string' &&
          typeof profile.baseUrl === 'string' &&
          typeof profile.apiKey === 'string'
        ))
      }
    }
    const activeId = localStorage.getItem(ACTIVE_PROFILE_STORAGE_KEY)
    if (activeId && profiles.value.some((profile) => profile.id === activeId)) {
      selectProfile(activeId)
    }
  } catch (error) {
    console.error('Failed to load LLM tester profiles:', error)
    appStore.showError(t('llmTester.errors.loadFailed'))
  }
}

function applyProfileToForm(profile: LLMTesterProfile) {
  form.provider = profile.provider || 'custom'
  form.name = profile.name
  form.baseUrl = profile.baseUrl
  form.apiKey = profile.apiKey
  form.selectedModel = profile.selectedModel || ''
}

function handleProfileSelect() {
  if (selectedProfileId.value) {
    selectProfile(selectedProfileId.value)
  } else {
    startNewProfile()
  }
}

function selectProfile(profileId: string) {
  const profile = profiles.value.find((item) => item.id === profileId)
  if (!profile) return
  selectedProfileId.value = profile.id
  applyProfileToForm(profile)
  models.value = []
  modelSearch.value = ''
  persistProfiles()
}

function startNewProfile(provider: Provider = 'sub2api') {
  selectedProfileId.value = ''
  form.provider = provider
  form.name = provider === 'openrouter' ? 'OpenRouter' : provider === 'sub2api' ? 'Current Sub2API' : ''
  form.baseUrl = provider === 'openrouter' ? OPENROUTER_BASE_URL : provider === 'sub2api' ? defaultSub2APIBaseUrl() : ''
  form.apiKey = ''
  form.selectedModel = ''
  models.value = []
  modelSearch.value = ''
  persistProfiles()
}

function applyProviderPreset() {
  if (form.provider === 'openrouter') {
    form.baseUrl = OPENROUTER_BASE_URL
    if (!form.name || form.name === 'Current Sub2API') form.name = 'OpenRouter'
  } else if (form.provider === 'sub2api') {
    form.baseUrl = defaultSub2APIBaseUrl()
    if (!form.name || form.name === 'OpenRouter') form.name = 'Current Sub2API'
  }
}

function saveProfile() {
  const name = form.name.trim()
  const baseUrl = normalizeBaseUrl(form.baseUrl)
  const apiKey = form.apiKey.trim()

  if (!name) {
    appStore.showError(t('llmTester.errors.nameRequired'))
    return
  }
  if (!baseUrl) {
    appStore.showError(t('llmTester.errors.baseUrlRequired'))
    return
  }
  if (!apiKey) {
    appStore.showError(t('llmTester.errors.apiKeyRequired'))
    return
  }

  const current: LLMTesterProfile = {
    id: selectedProfileId.value || createId('profile'),
    name,
    provider: form.provider,
    baseUrl,
    apiKey,
    selectedModel: form.selectedModel,
    lastFetchedAt: activeProfile.value?.lastFetchedAt,
  }

  const index = profiles.value.findIndex((profile) => profile.id === current.id)
  if (index >= 0) {
    profiles.value.splice(index, 1, current)
  } else {
    profiles.value.push(current)
  }

  selectedProfileId.value = current.id
  persistProfiles()
  appStore.showSuccess(t('llmTester.profileSaved'))
}

function deleteProfile() {
  if (!selectedProfileId.value) return
  profiles.value = profiles.value.filter((profile) => profile.id !== selectedProfileId.value)
  startNewProfile()
  appStore.showSuccess(t('llmTester.profileDeleted'))
}

function updateActiveProfile(updates: Partial<LLMTesterProfile>) {
  if (!selectedProfileId.value) return
  const index = profiles.value.findIndex((profile) => profile.id === selectedProfileId.value)
  if (index < 0) return
  profiles.value.splice(index, 1, { ...profiles.value[index], ...updates })
  persistProfiles()
}

async function loadModels() {
  const baseUrl = normalizeBaseUrl(form.baseUrl)
  const apiKey = form.apiKey.trim()
  if (!baseUrl) {
    appStore.showError(t('llmTester.errors.baseUrlRequired'))
    return
  }
  if (!apiKey) {
    appStore.showError(t('llmTester.errors.apiKeyRequired'))
    return
  }

  modelAbortController?.abort()
  modelAbortController = new AbortController()
  loadingModels.value = true

  try {
    const list = await fetchLLMModels(baseUrl, apiKey, modelAbortController.signal)
    models.value = list
    if (list.length && !list.some((model) => model.id === form.selectedModel)) {
      form.selectedModel = (list.find((model) => getLLMTesterModelCapabilities(model).includes('chat')) || list[0]).id
    }
    updateActiveProfile({
      baseUrl,
      apiKey,
      selectedModel: form.selectedModel,
      lastFetchedAt: new Date().toISOString(),
    })
    appStore.showSuccess(t('llmTester.modelsFetched', { count: list.length }))
  } catch (error) {
    if ((error as { name?: string }).name === 'AbortError') return
    console.error('Failed to fetch LLM models:', error)
    appStore.showError((error as { message?: string }).message || t('llmTester.errors.modelsFailed'))
  } finally {
    loadingModels.value = false
    modelAbortController = null
  }
}

function renderMarkdown(content: string): string {
  const html = marked.parse(content || '') as string
  return DOMPurify.sanitize(html)
}

function formatTime(value: string): string {
  try {
    return new Intl.DateTimeFormat(undefined, {
      month: 'short',
      day: 'numeric',
      hour: '2-digit',
      minute: '2-digit',
    }).format(new Date(value))
  } catch {
    return value
  }
}

function maskKey(key: string): string {
  if (!key) return ''
  if (key.length <= 12) return `${key.slice(0, 3)}...`
  return `${key.slice(0, 7)}...${key.slice(-4)}`
}

function formatAttachmentSize(size: number): string {
  if (!size) return ''
  if (size < 1024) return `${size} B`
  if (size < 1024 * 1024) return `${(size / 1024).toFixed(1)} KB`
  return `${(size / (1024 * 1024)).toFixed(1)} MB`
}

function modelCapabilityLabel(model: LLMTesterModel): string {
  const capabilities = getLLMTesterModelCapabilities(model)
  const labels: string[] = []

  if (capabilities.includes('image_generation')) {
    labels.push(t('llmTester.capabilities.imageGeneration'))
  }
  if (capabilities.includes('video_generation')) {
    labels.push(t('llmTester.capabilities.videoGeneration'))
  }
  if (capabilities.includes('vision')) {
    labels.push(t('llmTester.capabilities.vision'))
  } else if (capabilities.includes('chat')) {
    labels.push(t('llmTester.capabilities.chat'))
  }

  return labels.join(' / ')
}

function isTextFile(file: File): boolean {
  if (file.type.startsWith('text/')) return true
  const lower = file.name.toLowerCase()
  const ext = lower.includes('.') ? lower.split('.').pop() || '' : ''
  return TEXT_FILE_EXTENSIONS.has(ext)
}

function readFileAsDataUrl(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => resolve(String(reader.result || ''))
    reader.onerror = () => reject(reader.error || new Error('File read failed'))
    reader.readAsDataURL(file)
  })
}

async function readAttachment(file: File): Promise<LLMTesterAttachment | null> {
  if (file.type.startsWith('image/')) {
    if (file.size > MAX_IMAGE_ATTACHMENT_BYTES) {
      appStore.showError(t('llmTester.errors.imageTooLarge', { name: file.name }))
      return null
    }
    return {
      id: createId('attachment'),
      name: file.name,
      type: file.type,
      size: file.size,
      kind: 'image',
      dataUrl: await readFileAsDataUrl(file),
    }
  }

  if (isTextFile(file)) {
    if (file.size > MAX_TEXT_ATTACHMENT_BYTES) {
      appStore.showError(t('llmTester.errors.textTooLarge', { name: file.name }))
      return null
    }
    return {
      id: createId('attachment'),
      name: file.name,
      type: file.type,
      size: file.size,
      kind: 'text',
      text: await file.text(),
    }
  }

  return {
    id: createId('attachment'),
    name: file.name,
    type: file.type,
    size: file.size,
    kind: file.type.startsWith('audio/') || file.type.startsWith('video/') ? 'media' : 'file',
  }
}

async function handleFileInput(event: Event) {
  const input = event.target as HTMLInputElement
  const files = Array.from(input.files || [])
  input.value = ''
  if (!files.length) return

  for (const file of files) {
    try {
      const attachment = await readAttachment(file)
      if (attachment) pendingAttachments.value.push(attachment)
    } catch (error) {
      console.error('Failed to read attachment:', error)
      appStore.showError(t('llmTester.errors.fileReadFailed', { name: file.name }))
    }
  }
}

function removePendingAttachment(id: string) {
  pendingAttachments.value = pendingAttachments.value.filter((attachment) => attachment.id !== id)
}

function openAttachmentPreview(attachment: LLMTesterAttachment) {
  if ((attachment.kind === 'image' || attachment.kind === 'media') && attachment.dataUrl) {
    previewAttachment.value = attachment
    return
  }
  void openAttachmentInNewTab(attachment)
}

function buildAttachmentDownloadUrl(attachment: LLMTesterAttachment): string {
  if (attachment.dataUrl) return attachment.dataUrl
  if (typeof attachment.text === 'string') {
    const type = attachment.type || 'text/plain'
    return `data:${type};charset=utf-8,${encodeURIComponent(attachment.text)}`
  }
  return ''
}

function downloadAttachment(attachment: LLMTesterAttachment) {
  const href = buildAttachmentDownloadUrl(attachment)
  if (!href || typeof document === 'undefined') {
    appStore.showError(t('llmTester.errors.downloadUnavailable'))
    return
  }

  const link = document.createElement('a')
  link.href = href
  link.download = attachment.name || 'attachment'
  link.rel = 'noopener noreferrer'
  document.body.appendChild(link)
  link.click()
  link.remove()
}

async function openAttachmentInNewTab(attachment: LLMTesterAttachment) {
  if (!attachment.dataUrl || typeof window === 'undefined') {
    appStore.showError(t('llmTester.errors.openUnavailable'))
    return
  }

  const popup = window.open('', '_blank')
  if (popup) popup.opener = null

  let objectUrl = ''
  try {
    if (attachment.dataUrl.startsWith('data:')) {
      const response = await fetch(attachment.dataUrl)
      const blob = await response.blob()
      objectUrl = URL.createObjectURL(blob)
    }
    const href = objectUrl || attachment.dataUrl
    if (popup) {
      popup.location.href = href
    } else {
      window.open(href, '_blank', 'noopener,noreferrer')
    }
  } catch (error) {
    console.error('Failed to open attachment:', error)
    if (popup) popup.close()
    appStore.showError(t('llmTester.errors.openUnavailable'))
  } finally {
    if (objectUrl) {
      window.setTimeout(() => URL.revokeObjectURL(objectUrl), 60_000)
    }
  }
}

async function scrollChatToBottom() {
  await nextTick()
  const el = chatScrollRef.value
  if (el) el.scrollTop = el.scrollHeight
}

function requestMessagesForAPI(): LLMTesterMessage[] {
  return messages.value
    .filter((message) => message.status !== 'error' && message.status !== 'sending')
    .map((message) => ({
      id: message.id,
      role: message.role,
      content: message.content,
      attachments: message.attachments,
    }))
}

async function sendMessage() {
  if (!canSend.value || sending.value) return

  const requestMode = selectedRequestMode.value
  if (!requestMode) {
    appStore.showError(t('llmTester.errors.unsupportedModel'))
    return
  }
  if ((requestMode === 'image_generation' || requestMode === 'video_generation') && !hasGenerationPrompt.value) {
    appStore.showError(
      requestMode === 'video_generation'
        ? t('llmTester.errors.videoPromptRequired')
        : t('llmTester.errors.imagePromptRequired')
    )
    return
  }

  if (!selectedProfileId.value) {
    saveProfile()
    if (!selectedProfileId.value) return
  } else {
    updateActiveProfile({
      baseUrl: normalizeBaseUrl(form.baseUrl),
      apiKey: form.apiKey.trim(),
      selectedModel: form.selectedModel,
    })
  }

  const userMessage: ChatViewMessage = {
    id: createId('message'),
    role: 'user',
    content: prompt.value.trim(),
    attachments: [...pendingAttachments.value],
    createdAt: new Date().toISOString(),
    status: 'sent',
  }
  const assistantMessage: ChatViewMessage = {
    id: createId('message'),
    role: 'assistant',
    content: '',
    createdAt: new Date().toISOString(),
    status: 'sending',
    model: form.selectedModel,
  }

  messages.value.push(userMessage, assistantMessage)
  prompt.value = ''
  pendingAttachments.value = []
  await scrollChatToBottom()

  chatAbortController?.abort()
  chatAbortController = new AbortController()
  sending.value = true

  try {
    if (requestMode === 'image_generation') {
      const result = await sendLLMImageGeneration({
        baseUrl: form.baseUrl,
        apiKey: form.apiKey,
        model: form.selectedModel,
        messages: requestMessagesForAPI(),
        systemInstruction: systemInstruction.value,
        signal: chatAbortController.signal,
      })
      assistantMessage.content = result.text
      assistantMessage.attachments = result.attachments
    } else if (requestMode === 'video_generation') {
      const result = await sendLLMVideoGeneration({
        baseUrl: form.baseUrl,
        apiKey: form.apiKey,
        model: form.selectedModel,
        messages: requestMessagesForAPI(),
        systemInstruction: systemInstruction.value,
        signal: chatAbortController.signal,
      })
      assistantMessage.content = result.text
      assistantMessage.attachments = result.attachments
    } else {
      const result = await sendLLMChatCompletion({
        baseUrl: form.baseUrl,
        apiKey: form.apiKey,
        model: form.selectedModel,
        messages: requestMessagesForAPI(),
        systemInstruction: systemInstruction.value,
        temperature: temperature.value,
        maxTokens: maxTokens.value,
        signal: chatAbortController.signal,
      })
      assistantMessage.content = result.text
    }
    assistantMessage.status = 'sent'
  } catch (error) {
    console.error('LLM chat request failed:', error)
    assistantMessage.status = 'error'
    assistantMessage.content = (error as { name?: string }).name === 'AbortError'
      ? t('llmTester.errors.cancelled')
      : ((error as { message?: string }).message || t('llmTester.errors.chatFailed'))
  } finally {
    sending.value = false
    chatAbortController = null
    await scrollChatToBottom()
  }
}

function cancelChat() {
  chatAbortController?.abort()
}

function clearChat() {
  messages.value = []
}

onMounted(() => {
  loadProfilesFromStorage()
})
</script>

<style scoped>
.llm-markdown {
  @apply max-w-none text-sm leading-relaxed;
}

.llm-markdown :deep(p) {
  @apply my-2 first:mt-0 last:mb-0;
}

.llm-markdown :deep(ul),
.llm-markdown :deep(ol) {
  @apply my-2 pl-5;
}

.llm-markdown :deep(ul) {
  @apply list-disc;
}

.llm-markdown :deep(ol) {
  @apply list-decimal;
}

.llm-markdown :deep(pre) {
  @apply my-3 overflow-x-auto rounded-xl bg-gray-950 p-4 text-gray-100;
}

.llm-markdown :deep(code) {
  @apply rounded bg-gray-100 px-1 py-0.5 font-mono text-xs text-primary-700 dark:bg-dark-700 dark:text-primary-200;
}

.llm-markdown :deep(pre code) {
  @apply bg-transparent p-0 text-gray-100;
}

.llm-markdown :deep(a) {
  @apply text-primary-600 underline underline-offset-2 dark:text-primary-300;
}

.llm-markdown :deep(blockquote) {
  @apply my-3 border-l-4 border-gray-300 pl-3 text-gray-600 dark:border-dark-600 dark:text-dark-200;
}

.llm-markdown :deep(img) {
  @apply my-3 max-h-[420px] max-w-full rounded-xl border border-gray-200 object-contain dark:border-dark-700;
}
</style>
