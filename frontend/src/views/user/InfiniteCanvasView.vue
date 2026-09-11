<template>
  <AppLayout>
    <div class="canvas-page-layout">
      <div class="card flex-1 min-h-0 overflow-hidden">
        <div v-if="loading" class="flex h-full flex-col items-center justify-center gap-3 py-12">
          <div class="h-8 w-8 animate-spin rounded-full border-2 border-primary-500 border-t-transparent"></div>
          <p class="text-sm text-gray-500 dark:text-dark-400">{{ t('infiniteCanvas.preparing') }}</p>
        </div>

        <div v-else-if="errorMessage" class="flex h-full items-center justify-center p-10 text-center">
          <div class="max-w-md">
            <div class="mx-auto mb-4 flex h-12 w-12 items-center justify-center rounded-full bg-gray-100 dark:bg-dark-700">
              <Icon name="exclamationTriangle" size="lg" class="text-amber-500" />
            </div>
            <h3 class="text-lg font-semibold text-gray-900 dark:text-white">{{ t('infiniteCanvas.errorTitle') }}</h3>
            <p class="mt-2 text-sm text-gray-500 dark:text-dark-400">{{ errorMessage }}</p>
            <button type="button" class="btn btn-primary mt-6" @click="prepareSession">
              {{ t('infiniteCanvas.retry') }}
            </button>
          </div>
        </div>

        <div v-else class="canvas-embed-shell">
          <a
            v-if="embedUrl"
            :href="embedUrl"
            target="_blank"
            rel="noopener noreferrer"
            class="btn btn-secondary btn-sm canvas-open-fab"
          >
            <Icon name="externalLink" size="sm" class="mr-1.5" :stroke-width="2" />
            {{ t('infiniteCanvas.openInNewTab') }}
          </a>
          <iframe
            v-if="embedUrl"
            :src="embedUrl"
            class="canvas-embed-frame"
            allow="clipboard-read; clipboard-write; fullscreen"
            allowfullscreen
            referrerpolicy="no-referrer"
            :title="t('infiniteCanvas.title')"
          ></iframe>
        </div>
      </div>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import { useAppStore } from '@/stores'
import { detectTheme } from '@/utils/embedded-url'
import {
  buildInfiniteCanvasImportUrl,
  resolveHttpBaseUrl,
  resolveInfiniteCanvasBaseUrl,
} from '@/utils/infiniteCanvas'
import { InfiniteCanvasSetupError, ensureInfiniteCanvasApiKey } from '@/utils/infiniteCanvasSession'

const { t, locale } = useI18n()
const appStore = useAppStore()

const loading = ref(true)
const errorCode = ref<'no-groups' | 'missing-key' | 'request-failed' | null>(null)
const embedUrl = ref('')

const errorMessage = computed(() => {
  if (errorCode.value === 'no-groups') return t('infiniteCanvas.noGroups')
  if (errorCode.value === 'missing-key') return t('infiniteCanvas.missingKey')
  if (errorCode.value === 'request-failed') return t('infiniteCanvas.requestFailed')
  return ''
})

function canvasLang(value: string): string {
  return value.toLowerCase().startsWith('zh') ? 'zh-CN' : 'en'
}

async function prepareSession() {
  loading.value = true
  errorCode.value = null
  embedUrl.value = ''
  try {
    if (!appStore.publicSettingsLoaded) {
      await appStore.fetchPublicSettings()
    }
    const session = await ensureInfiniteCanvasApiKey()
    const pageOrigin = window.location.origin
    const openaiBaseUrl = resolveHttpBaseUrl(appStore.cachedPublicSettings?.api_base_url, pageOrigin)
    const canvasBaseUrl = resolveInfiniteCanvasBaseUrl(import.meta.env.VITE_INFINITE_CANVAS_URL, pageOrigin)
    embedUrl.value = buildInfiniteCanvasImportUrl({
      canvasBaseUrl,
      apiKey: session.apiKey,
      openaiBaseUrl,
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
</script>

<style scoped>
.canvas-page-layout {
  @apply flex flex-col;
  height: calc(100vh - 64px - 4rem);
}

.canvas-embed-shell {
  @apply relative h-full min-h-0 overflow-hidden bg-gray-50 dark:bg-dark-900;
}

.canvas-embed-frame {
  @apply h-full w-full border-0 bg-white dark:bg-dark-900;
}

.canvas-open-fab {
  position: absolute;
  top: 12px;
  right: 12px;
  z-index: 2;
}
</style>
