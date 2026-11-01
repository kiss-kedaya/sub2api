<template>
  <AppLayout>
    <div class="canvas-page-layout">
      <div class="flex-1 min-h-0 overflow-hidden rounded-lg bg-default ring ring-default">
        <div v-if="loading" class="flex h-full flex-col items-center justify-center gap-3 py-12">
          <UIcon name="i-lucide-loader-circle" class="size-8 animate-spin text-muted" />
          <p class="text-sm text-muted">{{ t('infiniteCanvas.preparing') }}</p>
        </div>

        <div v-else-if="errorCode === 'not-configured'" class="flex h-full items-center justify-center p-6 text-center">
          <div class="max-w-md">
            <h3 class="text-lg font-semibold text-highlighted">{{ t('infiniteCanvas.notConfigured') }}</h3>
            <UButton class="mt-6" @click="prepareSession(true)">
              <Icon name="plus" size="sm" class="mr-2" />
              {{ t('infiniteCanvas.createAndEnter') }}
            </UButton>
          </div>
        </div>

        <div v-else-if="errorMessage" class="flex h-full items-center justify-center p-10 text-center">
          <div class="max-w-md">
            <div class="mx-auto mb-4 flex h-12 w-12 items-center justify-center rounded-full bg-gray-100 dark:bg-dark-700">
              <Icon name="exclamationTriangle" size="lg" class="text-amber-500" />
            </div>
            <h3 class="text-lg font-semibold text-gray-900 dark:text-white">{{ t('infiniteCanvas.errorTitle') }}</h3>
            <p class="mt-2 text-sm text-gray-500 dark:text-dark-400">{{ errorMessage }}</p>
            <RouterLink v-if="errorCode === 'key-unavailable'" to="/keys" class="btn btn-primary mt-6">
              {{ t('infiniteCanvas.manageKeys') }}
            </RouterLink>
            <UButton class="mt-6" v-else @click="prepareSession()">
              {{ t('infiniteCanvas.retry') }}
            </UButton>
          </div>
        </div>

        <div v-else class="canvas-embed-shell">
          <iframe
            v-if="embedUrl"
            :src="embedUrl"
            class="canvas-embed-frame"
            allow="clipboard-read; clipboard-write; fullscreen"
            allowfullscreen
            :title="t('infiniteCanvas.title')"
          ></iframe>
        </div>
      </div>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import { useAppStore } from '@/stores'
import { detectTheme } from '@/utils/embedded-url'
import { buildInfiniteCanvasImportUrl, resolveHttpBaseUrl, resolveInfiniteCanvasBaseUrl } from '@/utils/infiniteCanvas'
import { InfiniteCanvasSetupError, ensureInfiniteCanvasApiKey } from '@/utils/infiniteCanvasSession'

const { t, locale } = useI18n()
const appStore = useAppStore()

const loading = ref(true)
const errorCode = ref<InfiniteCanvasSetupError['code'] | null>(null)
const embedUrl = ref('')
let disposed = false

const errorMessage = computed(() => {
  if (errorCode.value === 'no-groups') return t('infiniteCanvas.noGroups')
  if (errorCode.value === 'key-unavailable') return t('infiniteCanvas.keyUnavailable')
  if (errorCode.value === 'missing-key') return t('infiniteCanvas.missingKey')
  if (errorCode.value === 'request-failed') return t('infiniteCanvas.requestFailed')
  return ''
})

function canvasLang(value: string): string {
  return value.toLowerCase().startsWith('zh') ? 'zh-CN' : 'en'
}

async function prepareSession(createIfMissing = false) {
  loading.value = true
  errorCode.value = null
  embedUrl.value = ''
  try {
    if (!appStore.publicSettingsLoaded) {
      await appStore.fetchPublicSettings()
    }
    if (disposed) return
    const session = await ensureInfiniteCanvasApiKey(undefined, { createIfMissing })
    if (disposed) return
    const pageOrigin = window.location.origin
    const openaiBaseUrl = resolveHttpBaseUrl(pageOrigin)
    const canvasBaseUrl = resolveInfiniteCanvasBaseUrl(pageOrigin)
    embedUrl.value = buildInfiniteCanvasImportUrl({
      canvasBaseUrl,
      apiKey: session.apiKey,
      openaiBaseUrl,
      pageOrigin,
      theme: detectTheme(),
      lang: canvasLang(String(locale.value || '')),
    })
    if (session.created) {
      appStore.showSuccess(t('infiniteCanvas.keyCreated'))
    }
    if (session.truncated) {
      appStore.showWarning(t('infiniteCanvas.groupsTruncated'))
    }
  } catch (error) {
    errorCode.value = error instanceof InfiniteCanvasSetupError ? error.code : 'request-failed'
  } finally {
    loading.value = false
  }
}

onMounted(() => {
  void prepareSession()
})

onUnmounted(() => { disposed = true })
</script>

<style scoped>
@reference "../../style.css";
.canvas-page-layout { display: flex; flex-direction: column; height: calc(100dvh - 2rem); min-height: 480px; }

.canvas-embed-shell {
  @apply relative h-full min-h-0 overflow-hidden bg-muted;
}

.canvas-embed-frame {
  @apply h-full w-full border-0 bg-default;
}
</style>

<style>
/* Fill the console panel body (it scrolls, not the window), like the table pages do. */
.console-frame .signal-main:has(> .canvas-page-layout) { display: flex; flex: 1 1 auto; min-height: 0; flex-direction: column; }
.console-frame .signal-main > .canvas-page-layout { flex: 1 1 auto; height: auto; }
</style>
