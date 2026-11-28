<template>
  <div class="w-full" data-testid="ccswitch-interactive-demo">
    <!-- Outer Scene Tabs matching ccswitch.io/zh/ (Ki) -->
    <div class="mb-3 flex flex-wrap items-center justify-between gap-2">
      <div
        class="inline-flex max-w-full gap-1 overflow-x-auto rounded-xl border border-slate-200/90 bg-white/90 p-1 shadow-2xs dark:border-dark-700 dark:bg-dark-900/90"
        role="tablist"
      >
        <button
          v-for="tab in sceneTabs"
          :key="tab.id"
          type="button"
          role="tab"
          :data-testid="`demo-scene-tab-${tab.id}`"
          :aria-selected="activeScene === tab.id"
          class="flex shrink-0 items-center gap-1.5 rounded-lg px-3 py-1.5 text-xs font-medium transition-colors sm:px-3.5 sm:text-[13px]"
          :class="
            activeScene === tab.id
              ? 'bg-blue-600 text-white shadow-xs'
              : 'text-slate-600 hover:bg-slate-100 hover:text-slate-950 dark:text-slate-300 dark:hover:bg-dark-800 dark:hover:text-white'
          "
          @click="jumpToScene(tab.id)"
        >
          <CcSwitchLucideIcon :name="tab.icon" class-name="h-3.5 w-3.5" :stroke-width="1.9" />
          <span>{{ tab.label }}</span>
        </button>
      </div>

      <button
        v-if="guidedMode && guideDismissed"
        type="button"
        class="inline-flex h-8 items-center gap-1.5 rounded-full border border-slate-200 bg-white px-3 text-xs font-medium text-slate-600 transition hover:bg-slate-50 dark:border-dark-700 dark:bg-dark-900 dark:text-slate-300 dark:hover:bg-dark-800"
        @click="guideDismissed = false"
      >
        <span class="h-1.5 w-1.5 rounded-full bg-orange-500" />
        <span>{{ msg.window.guided.reopen }}</span>
      </button>
    </div>

    <!-- 1:1 CC Switch Interactive Desktop Window (.ccswitch-app-demo) -->
    <div class="relative mx-auto w-full">
      <div
        class="pointer-events-none absolute -inset-2 rounded-3xl bg-gradient-to-r from-blue-500/10 to-sky-500/10 opacity-60 blur-2xl"
        aria-hidden="true"
      />
      <div
        class="ccswitch-app-demo relative flex h-[600px] w-full overflow-hidden rounded-xl border border-[var(--ccs-border)] bg-[var(--ccs-bg)] text-[var(--ccs-fg)] shadow-2xl sm:h-[640px] sm:rounded-2xl md:h-[650px]"
        :data-ccs-theme="effectiveThemeAttr"
        data-testid="ccs-demo-app-window"
      >
        <CcSwitchSidebar
          :page-kind="pageKind"
          :active-app-id="activeAppId"
          :visible-apps="visibleApps"
          :app-states="appStates"
          :collapsed="sidebarCollapsed"
          @toggle-collapse="sidebarCollapsed = !sidebarCollapsed"
          @select-app="selectApp"
          @open-page="openPage"
        />

        <main class="relative min-w-0 flex-1 bg-[var(--ccs-bg)]">
          <CcSwitchProviderPane
            v-if="pageKind === 'app'"
            :app="activeAppSpec"
            :providers="activeAppProviders"
            :state="appStates[activeAppId]"
            :site-name="siteName"
            :masked-key-preview="maskedKeyPreview"
            :has-live-key="hasLiveKey"
            :imported-to-ccs="importedToCcs"
            :guided-step="effectiveGuidedStep"
            @update:state="updateAppState(activeAppId, $event)"
            @add-provider="openAddEditor"
            @edit-provider="openEditEditor"
            @request-create-key="emit('request-create-key')"
            @request-import-ccs="emit('request-import-ccs', activeAppId)"
            @provider-activated="onSiteProviderActivated"
            @dismiss-guide="guideDismissed = true"
          />

          <CcSwitchAppsPane
            v-else-if="pageKind === 'apps'"
            :guided-active="effectiveGuidedStep === 0"
            :spotlight-app-id="appsSpotlightId"
            @dismiss-guide="guideDismissed = true"
            @app-installed="onAppInstalled"
          />

          <CcSwitchUsagePane v-else-if="pageKind === 'usage'" />

          <CcSwitchExtraPanes v-else :page-kind="pageKind" />

          <!-- Inline Provider Add / Edit Modal inside Workbench -->
          <div
            v-if="editorDraft"
            class="absolute inset-0 z-30 flex items-center justify-center bg-black/40 p-4"
            @click.self="editorDraft = null"
          >
            <form
              class="w-full max-w-md rounded-2xl border border-[var(--ccs-border)] bg-[var(--app-surface)] p-5 shadow-xl"
              @submit.prevent="saveEditorDraft"
            >
              <div class="flex items-center justify-between gap-2 border-b border-[var(--ccs-border)] pb-3">
                <h4 class="text-sm font-semibold text-[var(--ccs-fg)]">
                  {{
                    editorDraft.mode === 'add'
                      ? fmt(msg.window.editor.titleAdd, { app: activeAppSpec.label })
                      : fmt(msg.window.editor.titleEdit, { name: editorDraft.originalName || editorDraft.name })
                  }}
                </h4>
                <button
                  type="button"
                  class="flex h-7 w-7 items-center justify-center rounded-md text-[var(--app-fg2)] hover:bg-[var(--app-subtle)]"
                  @click="editorDraft = null"
                >
                  <CcSwitchLucideIcon name="x" class-name="h-4 w-4" />
                </button>
              </div>

              <div class="mt-4 space-y-3 text-xs">
                <div>
                  <label class="mb-1 block font-medium text-[var(--app-fg2)]">{{ msg.window.editor.nameLabel }}</label>
                  <input
                    v-model="editorDraft.name"
                    type="text"
                    required
                    class="h-8 w-full rounded-md border border-[var(--app-border-strong)] bg-[var(--ccs-bg)] px-2.5 text-[13px] text-[var(--ccs-fg)] outline-none focus:border-[var(--app-action)]"
                  />
                </div>
                <div>
                  <label class="mb-1 block font-medium text-[var(--app-fg2)]">{{ msg.window.editor.endpointLabel }}</label>
                  <input
                    v-model="editorDraft.endpoint"
                    type="text"
                    required
                    class="h-8 w-full rounded-md border border-[var(--app-border-strong)] bg-[var(--ccs-bg)] px-2.5 font-mono text-[12.5px] text-[var(--ccs-fg)] outline-none focus:border-[var(--app-action)]"
                  />
                </div>
                <div>
                  <label class="mb-1 block font-medium text-[var(--app-fg2)]">{{ msg.window.editor.apiKeyLabel }}</label>
                  <input
                    v-model="editorDraft.apiKey"
                    type="text"
                    class="h-8 w-full rounded-md border border-[var(--app-border-strong)] bg-[var(--ccs-bg)] px-2.5 font-mono text-[12.5px] text-[var(--ccs-fg)] outline-none focus:border-[var(--app-action)]"
                  />
                </div>
                <div>
                  <label class="mb-1 block font-medium text-[var(--app-fg2)]">{{ msg.window.editor.modelLabel }}</label>
                  <input
                    v-model="editorDraft.model"
                    type="text"
                    class="h-8 w-full rounded-md border border-[var(--app-border-strong)] bg-[var(--ccs-bg)] px-2.5 font-mono text-[12.5px] text-[var(--ccs-fg)] outline-none focus:border-[var(--app-action)]"
                  />
                </div>
                <p class="rounded-lg bg-[var(--app-subtle)] px-3 py-2 text-[11.5px] text-[var(--app-fg2)]">
                  {{ hasLiveKey ? msg.window.editor.keyReadyHint : msg.window.editor.keyPendingHint }}
                </p>
              </div>

              <div class="mt-4 flex justify-end gap-2 border-t border-[var(--ccs-border)] pt-3">
                <button
                  type="button"
                  class="h-8 rounded-md border border-[var(--app-border-strong)] bg-[var(--app-surface)] px-3 text-xs font-medium text-[var(--ccs-fg)] hover:bg-[var(--app-subtle)]"
                  @click="editorDraft = null"
                >
                  {{ msg.window.editor.cancel }}
                </button>
                <button
                  type="submit"
                  class="h-8 rounded-md bg-[var(--app-action)] px-3.5 text-xs font-semibold text-white hover:bg-[var(--app-action-hover)]"
                >
                  {{ msg.window.editor.save }}
                </button>
              </div>
            </form>
          </div>

          <!-- Interactive Toast inside Workbench -->
          <div
            v-if="toastState"
            role="status"
            data-testid="ccs-demo-toast"
            class="absolute bottom-4 end-4 z-30 flex max-w-[calc(100%-2rem)] items-center gap-3 rounded-[10px] border border-[var(--ccs-border)] bg-[var(--app-surface)] py-2.5 pe-2.5 ps-3.5 text-[13px] shadow-[0_4px_12px_rgba(16,24,40,0.12),0_1px_3px_rgba(16,24,40,0.08)] sm:max-w-[420px]"
          >
            <div class="min-w-0 flex-1">
              <p class="text-[var(--ccs-fg)]">{{ toastState.text }}</p>
              <p v-if="toastState.detail" class="text-xs text-[var(--app-fg2)]">{{ toastState.detail }}</p>
            </div>
            <button
              v-if="toastState.undo"
              type="button"
              class="h-7 shrink-0 rounded-md border border-[var(--app-border-strong)] px-2.5 text-xs font-medium hover:bg-[var(--app-subtle)]"
              @click="runToastUndo"
            >
              {{ msg.window.pages.common.undo }}
            </button>
          </div>
        </main>
      </div>
    </div>

    <CcSwitchTooltip :tooltip="tooltipPayload" :theme-attr="effectiveThemeAttr" />
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, provide, ref, watch } from 'vue'
import './ccswitch/ccswitchTokens.css'
import {
  CCS_TOOLTIP_KEY,
  CCS_WORKBENCH_KEY,
  fmt,
  useCcSwitchI18n,
  type CcsTooltipPayload,
} from './ccswitch/ccswitchContext'
import {
  AGENT_TO_DEMO_APP,
  CC_SWITCH_APP_MAP,
  CC_SWITCH_DEMO_APPS,
  INITIAL_APPS_INVENTORY,
  createInitialAppStates,
  createSiteProvider,
  getAppDefaultEndpoint,
  getAppDefaultModel,
} from './ccswitch/ccswitchDemoData'
import CcSwitchAppsPane from './ccswitch/CcSwitchAppsPane.vue'
import CcSwitchExtraPanes from './ccswitch/CcSwitchExtraPanes.vue'
import CcSwitchLucideIcon from './ccswitch/CcSwitchLucideIcon.vue'
import CcSwitchProviderPane from './ccswitch/CcSwitchProviderPane.vue'
import CcSwitchSidebar from './ccswitch/CcSwitchSidebar.vue'
import CcSwitchTooltip from './ccswitch/CcSwitchTooltip.vue'
import CcSwitchUsagePane from './ccswitch/CcSwitchUsagePane.vue'
import type {
  CcsAppRuntimeState,
  CcsEditorDraft,
  CcsPageKind,
  CcsProviderItem,
  CcsScene,
  CcsThemePref,
  CcsToastState,
} from './ccswitch/ccswitchTypes'

const props = withDefaults(
  defineProps<{
    origin: string
    siteName: string
    activeStep?: number
    focusAgentId?: string
    maskedKeyPreview?: string
    hasLiveKey?: boolean
    importedToCcs?: boolean
    guidedMode?: boolean
  }>(),
  {
    activeStep: 0,
    focusAgentId: '',
    maskedKeyPreview: '',
    hasLiveKey: false,
    importedToCcs: false,
    guidedMode: true,
  },
)

const emit = defineEmits<{
  'step-change': [step: number]
  'request-create-key': []
  'request-import-ccs': [appId: string]
  'provider-activated': [appId: string]
}>()

const { msg } = useCcSwitchI18n()

const pageKind = ref<CcsPageKind>('apps')
const activeAppId = ref<string>('claude')
const sidebarCollapsed = ref(false)
const appStates = ref<Record<string, CcsAppRuntimeState>>(createInitialAppStates())
const installedApps = ref<string[]>(
  INITIAL_APPS_INVENTORY.filter((r) => r.app === 'claude-desktop' || r.app === 'workbuddy' || Boolean(r.version)).map((r) => r.app),
)
const visibleApps = ref<string[]>(
  CC_SWITCH_DEMO_APPS.filter((a) => installedApps.value.includes(a.id)).map((a) => a.id),
)
const themePref = ref<CcsThemePref>('system')
const guideDismissed = ref(false)
const activatedByUser = ref(false)
const customSiteProviders = ref<Record<string, CcsProviderItem>>({})
const customEditedProviders = ref<Record<string, CcsProviderItem[]>>({})
const editorDraft = ref<CcsEditorDraft | null>(null)

// Toast state
const toastState = ref<CcsToastState | null>(null)
let toastNonce = 0
let toastTimer: ReturnType<typeof setTimeout> | undefined

function showToast(payload: { text: string; detail?: string; undo?: () => void }) {
  toastNonce += 1
  const id = toastNonce
  toastState.value = { id, ...payload }
  if (toastTimer) clearTimeout(toastTimer)
  toastTimer = setTimeout(() => {
    if (toastState.value?.id === id) toastState.value = null
  }, payload.undo ? 6000 : 3200)
}

function runToastUndo() {
  toastState.value?.undo?.()
  toastState.value = null
}

// Tooltip provider (<Teleport to="body">)
const tooltipPayload = ref<CcsTooltipPayload | null>(null)

function showTooltip(event: MouseEvent | FocusEvent, text: string, title?: string) {
  if (!text) return
  let clientX = 0
  let clientY = 0
  if ('clientX' in event) {
    clientX = event.clientX
    clientY = event.clientY
  } else if (event.target instanceof HTMLElement) {
    const r = event.target.getBoundingClientRect()
    clientX = r.left + r.width / 2
    clientY = r.bottom
  }
  tooltipPayload.value = {
    text,
    title,
    x: clientX,
    y: clientY,
    left: typeof window !== 'undefined' ? clientX > window.innerWidth - 260 : false,
    up: typeof window !== 'undefined' ? clientY > window.innerHeight - 90 : false,
  }
}

function hideTooltip() {
  tooltipPayload.value = null
}

provide(CCS_TOOLTIP_KEY, {
  show: showTooltip,
  hide: hideTooltip,
})

const effectiveThemeAttr = computed<'light' | 'dark' | undefined>(() => {
  if (themePref.value === 'system') return undefined
  return themePref.value
})

function setAppVisible(appId: string, visible: boolean) {
  if (visible) {
    visibleApps.value = CC_SWITCH_DEMO_APPS.map((a) => a.id).filter(
      (id) => id === appId || visibleApps.value.includes(id),
    )
  } else {
    if (visibleApps.value.length <= 1) return
    visibleApps.value = visibleApps.value.filter((id) => id !== appId)
  }
}

function markAppInstalled(appId: string) {
  if (!installedApps.value.includes(appId)) {
    installedApps.value = [...installedApps.value, appId]
  }
}

function openPage(kind: CcsPageKind) {
  hideTooltip()
  pageKind.value = kind
}

function selectApp(appId: string) {
  hideTooltip()
  if (!visibleApps.value.includes(appId)) {
    setAppVisible(appId, true)
  }
  activeAppId.value = appId
  pageKind.value = 'app'
  const cur = appStates.value[appId]
  if (cur) {
    appStates.value = {
      ...appStates.value,
      [appId]: { ...cur, view: cur.active },
    }
  }
}

provide(CCS_WORKBENCH_KEY, {
  siteName: computed(() => props.siteName.trim() || 'kedaya.ai'),
  visibleApps,
  installedApps,
  setAppVisible,
  markAppInstalled,
  toast: showToast,
  openPage,
  themePref,
  setThemePref: (pref) => {
    themePref.value = pref
  },
  effectiveThemeAttr,
})

// Target app & step synchronization
const mappedAgentApp = computed(() => AGENT_TO_DEMO_APP[props.focusAgentId] ?? '')

const appsSpotlightId = computed(() => {
  const mapped = mappedAgentApp.value
  if (mapped && !installedApps.value.includes(mapped)) return mapped
  if (!installedApps.value.includes('openclaw')) return 'openclaw'
  return mapped || 'openclaw'
})

const effectiveGuidedStep = computed<number | null>(() => {
  if (!props.guidedMode || guideDismissed.value) return null
  if (props.activeStep === 3 && activatedByUser.value) return null
  return props.activeStep
})

watch(
  [() => props.activeStep, mappedAgentApp],
  ([step, mapped]) => {
    hideTooltip()
    if (step === 0) {
      pageKind.value = 'apps'
      return
    }
    const targetApp = mapped || activeAppId.value || 'claude'
    if (!visibleApps.value.includes(targetApp)) {
      setAppVisible(targetApp, true)
    }
    activeAppId.value = targetApp
    pageKind.value = 'app'
    const cur = appStates.value[targetApp]
    if (cur) {
      appStates.value = {
        ...appStates.value,
        [targetApp]: { ...cur, view: 'direct' },
      }
    }
  },
  { immediate: true },
)

watch(
  () => props.importedToCcs,
  (imported) => {
    if (imported) {
      const target = mappedAgentApp.value || activeAppId.value || 'claude'
      activeAppId.value = target
      pageKind.value = 'app'
    }
  },
)

const activeAppSpec = computed(() => CC_SWITCH_APP_MAP[activeAppId.value] ?? CC_SWITCH_DEMO_APPS[0])

const activeAppProviders = computed<CcsProviderItem[]>(() => {
  const app = activeAppSpec.value
  const edited = customEditedProviders.value[app.id]
  const baseList = edited ? [...edited] : [...app.providers]
  const customSite = customSiteProviders.value[app.id]
  const siteItem =
    customSite ??
    createSiteProvider(props.origin, props.siteName, app.id, props.maskedKeyPreview)
  const filtered = baseList.filter((p) => !p.isSiteProvider && p.name !== siteItem.name)
  return [siteItem, ...filtered]
})

function updateAppState(appId: string, next: CcsAppRuntimeState) {
  appStates.value = { ...appStates.value, [appId]: next }
}

function onAppInstalled(_appId: string) {
  // Keep user on Apps pane after installing so they can inspect/toggle sidebar visibility freely.
}

function onSiteProviderActivated(appId: string) {
  activatedByUser.value = true
  emit('provider-activated', appId)
}

// Inline Editor
function openAddEditor() {
  const appId = activeAppId.value
  editorDraft.value = {
    mode: 'add',
    appId,
    name: props.siteName.trim() || 'kedaya.ai',
    endpoint: getAppDefaultEndpoint(appId, props.origin),
    apiKey: props.maskedKeyPreview || 'demo-••••-8a9f',
    model: getAppDefaultModel(appId),
  }
}

function openEditEditor(provider: CcsProviderItem) {
  const appId = activeAppId.value
  editorDraft.value = {
    mode: 'edit',
    appId,
    originalName: provider.name,
    name: provider.name,
    endpoint: provider.subtitle,
    apiKey: provider.maskedKeyPreview || props.maskedKeyPreview || 'demo-••••-8a9f',
    model: provider.modelBadge || getAppDefaultModel(appId),
  }
}

function saveEditorDraft() {
  const d = editorDraft.value
  if (!d) return
  const app = CC_SWITCH_APP_MAP[d.appId]
  if (!app) return
  const cleanName = d.name.trim() || props.siteName.trim() || 'kedaya.ai'
  const isSite = d.mode === 'add' || cleanName === props.siteName.trim()
  if (isSite) {
    customSiteProviders.value = {
      ...customSiteProviders.value,
      [d.appId]: createSiteProvider(
        props.origin,
        cleanName,
        d.appId,
        d.apiKey.trim(),
        d.endpoint.trim(),
        d.model.trim(),
      ),
    }
  } else {
    const existing = customEditedProviders.value[d.appId] ?? [...app.providers]
    customEditedProviders.value = {
      ...customEditedProviders.value,
      [d.appId]: existing.map((p) =>
        p.name === d.originalName
          ? {
              ...p,
              name: cleanName,
              subtitle: d.endpoint.trim(),
              modelBadge: d.model.trim() || p.modelBadge,
              maskedKeyPreview: d.apiKey.trim() || p.maskedKeyPreview,
            }
          : p,
      ),
    }
  }
  showToast({ text: fmt(msg.value.window.editor.savedToast, { name: cleanName }) })
  editorDraft.value = null
}

// Outer Scene Tabs (matching ccswitch.io/zh/ Ki)
const sceneTabs = computed<Array<{ id: CcsScene; label: string; icon: string }>>(() => [
  { id: 'provider', label: msg.value.tabs.provider, icon: 'shieldCheck' },
  { id: 'proxy', label: msg.value.tabs.proxy, icon: 'layers' },
  { id: 'stats', label: msg.value.tabs.stats, icon: 'chartColumn' },
])

const activeScene = computed<CcsScene>(() => {
  if (pageKind.value === 'usage') return 'stats'
  const st = appStates.value[activeAppId.value]
  if (pageKind.value === 'app' && st && (st.view !== 'direct' || st.active !== 'direct')) return 'proxy'
  return 'provider'
})

function jumpToScene(scene: CcsScene) {
  hideTooltip()
  if (scene === 'stats') {
    pageKind.value = 'usage'
    return
  }
  const targetApp = scene === 'proxy' ? 'codex' : mappedAgentApp.value || 'claude'
  setAppVisible(targetApp, true)
  activeAppId.value = targetApp
  pageKind.value = 'app'
  const cur = appStates.value[targetApp]
  if (cur) {
    appStates.value = {
      ...appStates.value,
      [targetApp]: {
        ...cur,
        view: scene === 'proxy' ? (cur.active === 'direct' ? 'route' : cur.active) : 'direct',
      },
    }
  }
}

onBeforeUnmount(() => {
  if (toastTimer) clearTimeout(toastTimer)
})
</script>
