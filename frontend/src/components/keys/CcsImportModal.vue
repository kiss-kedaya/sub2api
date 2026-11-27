<template>
  <BaseDialog
    :show="show"
    :title="t('keys.ccsImport.title')"
    width="narrow"
    @close="closeModal"
  >
    <div class="space-y-4" data-test="ccs-import-modal">
      <p class="text-sm text-gray-600 dark:text-gray-400">
        {{ t('keys.ccsImport.description') }}
      </p>

      <div>
        <label class="input-label" for="ccs-import-platform">{{ t('keys.ccsImport.platform') }}</label>
        <Select
          id="ccs-import-platform"
          :model-value="ccsImportApp"
          :options="ccsAppOptions"
          :aria-label="t('keys.ccsImport.platform')"
          @update:model-value="onCcsAppChange"
        />
      </div>

      <div>
        <div class="mb-1.5 flex items-center justify-between gap-2">
          <label class="input-label mb-0" for="ccs-import-model">{{ t('keys.ccsImport.model') }}</label>
          <button
            v-if="apiKey && !ccsModelsLoading"
            type="button"
            data-test="ccs-import-model-reload"
            class="inline-flex items-center gap-1 text-xs font-medium text-primary hover:underline"
            @click="loadCcsModels"
          >
            <UIcon name="i-lucide-refresh-cw" class="size-3" aria-hidden="true" />
            <span>{{ ccsModelsError || ccsAvailableModels.length === 0 ? t('keys.ccsImport.modelRetry') : t('keys.ccsImport.modelRefresh') }}</span>
          </button>
        </div>
        <Select
          id="ccs-import-model"
          :model-value="ccsImportModel"
          :options="ccsModelOptions"
          :loading="ccsModelsLoading"
          :disabled="ccsModelsLoading"
          :placeholder="ccsModelsLoading ? t('keys.ccsImport.modelLoading') : t('keys.ccsImport.modelPlaceholder')"
          :empty-text="ccsModelsError ? t('keys.ccsImport.modelLoadFailed') : t('keys.ccsImport.modelEmpty')"
          :searchable="true"
          :creatable="ccsModelsError || (ccsModelsLoaded && ccsAvailableModels.length === 0)"
          :clearable="true"
          :aria-label="t('keys.ccsImport.model')"
          @update:model-value="onCcsModelChange"
        />
        <p
          class="input-hint mt-1"
          :class="ccsModelsError ? 'text-error' : ''"
          data-test="ccs-import-model-hint"
        >
          {{ ccsModelHintText }}
        </p>
      </div>

      <UAlert
        v-if="ccsImportDeeplinkUnsupported"
        color="warning"
        variant="subtle"
        icon="i-lucide-triangle-alert"
        :description="t('keys.ccsImport.deeplinkUnsupported')"
        :ui="{ description: 'text-xs leading-5' }"
      />

      <div
        v-if="launchHintVisible && lastDeeplink"
        data-test="ccs-import-fallback-notice"
        class="space-y-2.5 rounded-xl border border-sky-200/80 bg-sky-50/75 p-3 text-xs text-slate-800 dark:border-sky-500/30 dark:bg-sky-500/10 dark:text-slate-100"
      >
        <p class="leading-5">{{ t('keys.ccsImport.fallbackHint') }}</p>
        <div class="flex flex-wrap items-center gap-2">
          <a
            :href="lastDeeplink"
            data-test="ccs-import-retry-link"
            class="inline-flex items-center gap-1 rounded-md border border-sky-300 bg-white px-2.5 py-1 text-xs font-medium text-sky-900 transition hover:bg-sky-50 dark:border-sky-500/40 dark:bg-dark-900 dark:text-sky-100 dark:hover:bg-dark-800"
            @click="handleRetryLinkClick"
          >
            <UIcon name="i-lucide-rotate-cw" class="size-3" aria-hidden="true" />
            <span>{{ t('keys.ccsImport.retryOpen') }}</span>
          </a>
          <UButton
            type="button"
            color="neutral"
            variant="soft"
            size="xs"
            :icon="copiedDeeplink ? 'i-lucide-check' : 'i-lucide-copy'"
            :label="copiedDeeplink ? t('keys.ccsImport.deeplinkCopied') : t('keys.ccsImport.copyDeeplink')"
            data-test="ccs-import-copy-deeplink"
            @click="copyLastDeeplink"
          />
          <a
            href="https://ccswitch.io/zh/"
            target="_blank"
            rel="noopener noreferrer"
            data-test="ccs-import-download-link"
            class="inline-flex items-center gap-1 rounded-md border border-slate-200 bg-white px-2.5 py-1 text-xs font-medium text-slate-700 transition hover:bg-slate-50 dark:border-dark-700 dark:bg-dark-900 dark:text-slate-200 dark:hover:bg-dark-800"
          >
            <span>{{ t('keys.ccsImport.downloadApp') }}</span>
            <UIcon name="i-lucide-external-link" class="size-3" aria-hidden="true" />
          </a>
        </div>
      </div>
    </div>

    <template #footer>
      <div class="flex justify-end gap-2">
        <UButton color="neutral" variant="outline" size="lg" @click="closeModal">
          {{ t('common.cancel') }}
        </UButton>
        <a
          v-if="canConfirmImport && currentDeeplink"
          :href="currentDeeplink"
          data-test="ccs-import-confirm"
          class="inline-flex items-center justify-center gap-2 rounded-md bg-primary px-3 py-2 text-sm font-medium text-inverted transition-colors hover:bg-primary/75 active:bg-primary/75 focus-visible:outline-3 outline-primary/25"
          @click="handleImportLinkClick"
        >
          {{ t('common.import') }}
        </a>
        <UButton
          v-else
          size="lg"
          disabled
          data-test="ccs-import-confirm"
        >
          {{ t('common.import') }}
        </UButton>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Select from '@/components/common/Select.vue'
import { useClipboard } from '@/composables/useClipboard'
import {
  buildCcSwitchImportDeeplink,
  CC_SWITCH_APP_LABEL_KEYS,
  CC_SWITCH_APPS,
  ccSwitchProviderDeeplinkSupported,
  defaultCcSwitchModel,
  fetchCcSwitchModels,
  isCcSwitchApp,
  pickCcSwitchModel,
  resolveCcSwitchImportConfig,
  type CcSwitchApp,
} from '@/utils/ccswitchImport'
import type { GroupPlatform } from '@/types'

const props = withDefaults(
  defineProps<{
    show: boolean
    apiKey: string
    platform?: GroupPlatform | null
    baseUrl?: string
    siteName?: string
    defaultApp?: CcSwitchApp
    allowedApps?: CcSwitchApp[]
  }>(),
  {
    platform: 'anthropic',
    baseUrl: '',
    siteName: 'sub2api',
    defaultApp: undefined,
    allowedApps: undefined,
  },
)

const emit = defineEmits<{
  close: []
  imported: [payload: { app: CcSwitchApp; model: string; deeplink: string }]
}>()

const { t } = useI18n()
const { copyToClipboard } = useClipboard()

const LAUNCH_HINT_DELAY_MS = 1300

const ccsImportApp = ref<CcSwitchApp>('claude')
const ccsImportModel = ref('')
const ccsAvailableModels = ref<string[]>([])
const ccsModelsLoading = ref(false)
const ccsModelsLoaded = ref(false)
const ccsModelsError = ref(false)
const launchHintVisible = ref(false)
const lastDeeplink = ref('')
const copiedDeeplink = ref(false)
const pendingImportedPayload = ref<{ app: CcSwitchApp; model: string; deeplink: string } | null>(null)

const PI_FAMILY_APPS = new Set<CcSwitchApp>(['pi', 'mcode', 'kimi', 'dsh', 'omp'])
const requiresModelForConfirm = computed(() => PI_FAMILY_APPS.has(ccsImportApp.value))

let ccsModelsAbort: AbortController | null = null
let launchHintTimer: ReturnType<typeof setTimeout> | null = null
let copiedTimer: ReturnType<typeof setTimeout> | null = null
let launchListenersCleanup: (() => void) | null = null

const effectiveBaseUrl = computed(() => {
  const trimmed = props.baseUrl.trim()
  if (trimmed) return trimmed
  return typeof window !== 'undefined' ? window.location.origin : ''
})

const effectiveApps = computed<CcSwitchApp[]>(() => {
  if (props.allowedApps && props.allowedApps.length > 0) {
    const filtered = CC_SWITCH_APPS.filter((app) => props.allowedApps!.includes(app))
    if (filtered.length > 0) return filtered
  }
  return CC_SWITCH_APPS
})

const ccsAppOptions = computed(() =>
  effectiveApps.value.map((value) => ({
    value,
    label: t(`keys.ccsImport.${CC_SWITCH_APP_LABEL_KEYS[value]}`),
  })),
)

const ccsImportDeeplinkUnsupported = computed(
  () => !ccSwitchProviderDeeplinkSupported(ccsImportApp.value),
)

const canConfirmImport = computed(
  () =>
    !ccsImportDeeplinkUnsupported.value &&
    !ccsModelsLoading.value &&
    Boolean(props.apiKey.trim()) &&
    (!requiresModelForConfirm.value || Boolean(ccsImportModel.value.trim())),
)

const currentDeeplink = computed(() => {
  if (!canConfirmImport.value) return ''
  const key = props.apiKey.trim()
  const platform = props.platform || 'anthropic'
  const usageScript = `({
    request: {
      url: "{{baseUrl}}".replace(/\\/+$/, "").replace(/\\/v1$/, "") + "/v1/usage",
      method: "GET",
      headers: { "Authorization": "Bearer {{apiKey}}" }
    },
    extractor: function(response) {
      const remaining = response?.remaining ?? response?.quota?.remaining ?? response?.balance;
      const unit = response?.unit ?? response?.quota?.unit ?? "USD";
      return {
        isValid: response?.is_active ?? response?.isValid ?? true,
        remaining,
        unit
      };
    }
  })`
  const providerName = (props.siteName || 'sub2api').trim() || 'sub2api'
  const model = ccsImportModel.value.trim()
  return buildCcSwitchImportDeeplink({
    baseUrl: effectiveBaseUrl.value,
    platform,
    providerName,
    apiKey: key,
    usageScript,
    app: ccsImportApp.value,
    model,
  })
})

const ccsModelOptions = computed(() => {
  const values = [...ccsAvailableModels.value]
  const current = ccsImportModel.value.trim()
  if (
    (ccsModelsError.value || (ccsModelsLoaded.value && ccsAvailableModels.value.length === 0)) &&
    current &&
    !values.includes(current)
  ) {
    values.unshift(current)
  }
  return values.map((value) => ({ value, label: value }))
})

const ccsModelHintText = computed(() => {
  if (ccsModelsLoading.value) return t('keys.ccsImport.modelLoading')
  if (ccsModelsError.value) return t('keys.ccsImport.modelLoadFailed')
  if (ccsModelsLoaded.value && ccsAvailableModels.value.length === 0) {
    if (requiresModelForConfirm.value && !ccsImportModel.value.trim()) {
      return `${t('keys.ccsImport.modelEmpty')} ${t('keys.ccsImport.modelRequiredHint')}`
    }
    return t('keys.ccsImport.modelEmpty')
  }
  if (requiresModelForConfirm.value && !ccsImportModel.value.trim()) {
    return t('keys.ccsImport.modelRequiredHint')
  }
  if (ccsAvailableModels.value.length > 0) {
    return t('keys.ccsImport.modelLoadedHint', { count: ccsAvailableModels.value.length })
  }
  return t('keys.ccsImport.modelHint')
})

const applyCcsModelDefault = () => {
  if (ccsAvailableModels.value.length > 0) {
    ccsImportModel.value = pickCcSwitchModel(
      defaultCcSwitchModel(ccsImportApp.value),
      ccsAvailableModels.value,
      ccsImportApp.value,
    )
    return
  }
  if (!ccsModelsError.value) {
    ccsImportModel.value = ''
  }
}

const fetchRealModelsForKey = async (
  baseUrl: string,
  apiKey: string,
  signal: AbortSignal,
): Promise<string[]> => {
  try {
    return await fetchCcSwitchModels(baseUrl, apiKey, signal)
  } catch (err) {
    if (signal.aborted) throw err
    const sameOrigin = typeof window !== 'undefined' ? window.location.origin : ''
    let differs = false
    if (sameOrigin) {
      try {
        differs = new URL(baseUrl).origin !== sameOrigin
      } catch {
        differs = false
      }
    }
    if (differs) {
      return await fetchCcSwitchModels(sameOrigin, apiKey, signal)
    }
    throw err
  }
}

const loadCcsModels = async () => {
  const key = props.apiKey.trim()
  if (!key) return
  ccsModelsAbort?.abort()
  const abort = new AbortController()
  ccsModelsAbort = abort
  ccsAvailableModels.value = []
  ccsModelsLoaded.value = false
  ccsModelsError.value = false
  ccsModelsLoading.value = true
  try {
    const ids = await fetchRealModelsForKey(effectiveBaseUrl.value, key, abort.signal)
    if (abort.signal.aborted) return
    ccsAvailableModels.value = ids
    ccsModelsLoaded.value = true
    applyCcsModelDefault()
  } catch {
    if (abort.signal.aborted) return
    ccsModelsError.value = true
    ccsModelsLoaded.value = true
    ccsImportModel.value = ''
  } finally {
    if (ccsModelsAbort === abort) {
      ccsModelsLoading.value = false
    }
  }
}

const resolveInitialApp = (): CcSwitchApp => {
  const allowed = effectiveApps.value
  if (props.defaultApp && isCcSwitchApp(props.defaultApp) && allowed.includes(props.defaultApp)) {
    return props.defaultApp
  }
  const platform = props.platform || 'anthropic'
  const fallback = resolveCcSwitchImportConfig(
    platform,
    platform === 'gemini' ? 'gemini' : 'claude',
    effectiveBaseUrl.value,
  ).app
  if (allowed.includes(fallback)) return fallback
  return allowed[0] ?? 'claude'
}

const clearLaunchDetection = () => {
  if (launchHintTimer) {
    clearTimeout(launchHintTimer)
    launchHintTimer = null
  }
  if (launchListenersCleanup) {
    launchListenersCleanup()
    launchListenersCleanup = null
  }
}

const clearTimers = () => {
  clearLaunchDetection()
  if (copiedTimer) {
    clearTimeout(copiedTimer)
    copiedTimer = null
  }
}

const closeModal = () => {
  clearTimers()
  ccsModelsAbort?.abort()
  ccsModelsAbort = null
  ccsModelsLoading.value = false
  ccsModelsLoaded.value = false
  ccsModelsError.value = false
  ccsAvailableModels.value = []
  launchHintVisible.value = false
  lastDeeplink.value = ''
  copiedDeeplink.value = false
  pendingImportedPayload.value = null
  emit('close')
}

watch(
  () => props.show,
  (show) => {
    if (show) {
      clearTimers()
      launchHintVisible.value = false
      lastDeeplink.value = ''
      copiedDeeplink.value = false
      pendingImportedPayload.value = null
      ccsImportApp.value = resolveInitialApp()
      ccsImportModel.value = ''
      void loadCcsModels()
    } else {
      clearTimers()
      pendingImportedPayload.value = null
      ccsModelsAbort?.abort()
      ccsModelsAbort = null
    }
  },
  { immediate: true },
)

const onCcsAppChange = (value: string | number | boolean | null) => {
  const app = String(value)
  if (!isCcSwitchApp(app)) return
  ccsImportApp.value = app
  applyCcsModelDefault()
}

const onCcsModelChange = (value: string | number | boolean | null) => {
  ccsImportModel.value = value == null ? '' : String(value)
}

const copyLastDeeplink = async () => {
  if (!lastDeeplink.value) return
  const ok = await copyToClipboard(lastDeeplink.value, t('keys.ccsImport.deeplinkCopied'))
  if (ok) {
    copiedDeeplink.value = true
    if (pendingImportedPayload.value) {
      const imported = pendingImportedPayload.value
      pendingImportedPayload.value = null
      clearLaunchDetection()
      emit('imported', imported)
    }
    if (copiedTimer) clearTimeout(copiedTimer)
    copiedTimer = setTimeout(() => {
      copiedDeeplink.value = false
    }, 1500)
  }
}

const completeImported = () => {
  const imported = pendingImportedPayload.value
  if (!imported) return
  pendingImportedPayload.value = null
  clearLaunchDetection()
  emit('imported', imported)
  closeModal()
}

const attachLaunchListeners = () => {
  if (typeof window === 'undefined' || typeof document === 'undefined') return
  const onWindowBlur = () => {
    completeImported()
  }
  const onVisibilityChange = () => {
    if (document.hidden) {
      completeImported()
    }
  }
  window.addEventListener('blur', onWindowBlur, { once: true })
  document.addEventListener('visibilitychange', onVisibilityChange)
  launchListenersCleanup = () => {
    window.removeEventListener('blur', onWindowBlur)
    document.removeEventListener('visibilitychange', onVisibilityChange)
  }
}

const startLaunchDetection = (deeplink: string, model: string) => {
  lastDeeplink.value = deeplink
  pendingImportedPayload.value = {
    app: ccsImportApp.value,
    model,
    deeplink,
  }
  clearLaunchDetection()
  attachLaunchListeners()
  launchHintTimer = setTimeout(() => {
    launchHintTimer = null
    if (props.show && pendingImportedPayload.value) {
      launchHintVisible.value = true
    }
  }, LAUNCH_HINT_DELAY_MS)
}

const handleImportLinkClick = () => {
  const deeplink = currentDeeplink.value
  if (!deeplink) return
  startLaunchDetection(deeplink, ccsImportModel.value.trim())
}

const handleRetryLinkClick = () => {
  const deeplink = lastDeeplink.value
  if (!deeplink) return
  const model = pendingImportedPayload.value?.model ?? ccsImportModel.value.trim()
  startLaunchDetection(deeplink, model)
}

onBeforeUnmount(() => {
  clearTimers()
  ccsModelsAbort?.abort()
})
</script>
