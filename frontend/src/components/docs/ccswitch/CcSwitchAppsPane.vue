<template>
  <div class="flex h-full min-h-0 flex-col" data-testid="ccs-demo-apps-pane">
    <!-- Top Page Header (Zt) -->
    <header class="flex h-14 shrink-0 items-center gap-3 border-b border-[var(--ccs-border)] px-4 sm:pe-4 sm:ps-6">
      <div class="flex min-w-0 items-center gap-2.5">
        <span class="flex h-[22px] w-[22px] shrink-0 items-center justify-center text-[var(--ccs-fg)]">
          <CcSwitchLucideIcon name="layoutGrid" class-name="h-5 w-5" :stroke-width="1.5" />
        </span>
        <h3 class="truncate text-lg font-semibold text-[var(--ccs-fg)]">{{ msg.window.nav.apps }}</h3>
        <button
          type="button"
          :aria-label="pages.helpTitle"
          class="inline-flex h-5 w-5 items-center justify-center rounded-full text-[var(--app-fg3)] hover:bg-[var(--app-subtle)] hover:text-[var(--ccs-fg)]"
          @mouseenter="tooltip.show($event, pages.help, pages.helpTitle)"
          @mouseleave="tooltip.hide()"
        >
          <CcSwitchLucideIcon name="helpCircle" class-name="h-3.5 w-3.5" :stroke-width="1.5" />
        </button>
      </div>

      <span class="flex-1" />

      <div class="flex shrink-0 items-center justify-end gap-2">
        <span class="hidden whitespace-nowrap text-xs text-[var(--app-fg3)] lg:inline">
          {{ fmt(pages.lastChecked, { time: lastChecked }) }}
        </span>
        <button
          v-if="updatableIds.length > 0"
          type="button"
          class="hidden h-8 shrink-0 items-center gap-1.5 whitespace-nowrap rounded-md border border-[var(--app-border-strong)] bg-[var(--app-surface)] px-3.5 text-[13px] font-medium text-[var(--ccs-fg)] transition-colors hover:bg-[var(--app-subtle)] md:inline-flex"
          @click="startBusy(updatableIds)"
        >
          <CcSwitchLucideIcon name="circleArrowUp" class-name="h-4 w-4" :stroke-width="2" />
          <span>{{ fmt(pages.updateAll, { count: updatableIds.length }) }}</span>
        </button>
        <button
          type="button"
          class="inline-flex h-8 shrink-0 items-center gap-1.5 whitespace-nowrap rounded-md border border-[var(--app-border-strong)] bg-[var(--app-surface)] px-3.5 text-[13px] font-medium text-[var(--ccs-fg)] transition-colors hover:bg-[var(--app-subtle)]"
          @click="triggerCheck"
        >
          <CcSwitchLucideIcon
            :name="checking ? 'loaderCircle' : 'refreshCw'"
            class-name="h-3.5 w-3.5"
            :class="checking ? 'animate-spin' : ''"
            :stroke-width="2"
          />
          <span class="hidden sm:inline">{{ checking ? pages.checking : pages.checkUpdates }}</span>
        </button>
        <CcSwitchMenuButton
          large
          :label="pages.moreActions"
          :items="[{ label: pages.diagnose }, { label: pages.manualCommands }]"
        />
      </div>
    </header>

    <!-- Beginner Guided Banner for Step 0 (Install & Pin to Sidebar) -->
    <div
      v-if="showGuidedBanner"
      data-testid="ccs-guided-banner-apps"
      class="mx-4 mt-3 flex flex-wrap items-center justify-between gap-2.5 rounded-xl border border-[var(--app-action)]/40 bg-[var(--app-warning-soft)] px-3.5 py-2.5 text-xs sm:mx-6"
    >
      <div class="flex min-w-0 flex-1 items-center gap-2">
        <span class="inline-flex shrink-0 items-center gap-1.5 rounded-full bg-[var(--app-action)] px-2 py-0.5 text-[11px] font-semibold text-white">
          <span class="h-1.5 w-1.5 rounded-full bg-white" />
          {{ msg.window.guided.step0Badge }}
        </span>
        <span class="min-w-0 text-[var(--ccs-fg)]">
          {{ fmt(msg.window.guided.step0Text, { app: spotlightAppLabel }) }}
        </span>
      </div>
      <div class="flex shrink-0 items-center gap-2">
        <button
          v-if="!isSpotlightInstalled"
          type="button"
          class="h-7 rounded-md bg-[var(--app-action)] px-2.5 text-xs font-semibold text-white transition-colors hover:bg-[var(--app-action-hover)]"
          @click="startBusy([spotlightAppId])"
        >
          {{ msg.window.guided.step0ActionInstall }}
        </button>
        <button
          v-else-if="!isSpotlightVisible"
          type="button"
          class="h-7 rounded-md bg-[var(--app-action)] px-2.5 text-xs font-semibold text-white transition-colors hover:bg-[var(--app-action-hover)]"
          @click="workbench.setAppVisible(spotlightAppId, true)"
        >
          {{ msg.window.guided.step0ActionToggle }}
        </button>
        <button
          type="button"
          class="rounded px-1.5 py-1 text-[11px] text-[var(--app-fg2)] hover:text-[var(--ccs-fg)]"
          @click="emit('dismiss-guide')"
        >
          {{ msg.window.guided.dismiss }}
        </button>
      </div>
    </div>

    <!-- Scrollable Apps Table -->
    <div ref="scrollContainerRef" class="min-h-0 flex-1 overflow-y-auto px-3.5 pb-10 pt-3 sm:px-5">
      <!-- Column Headings -->
      <div class="flex h-8 items-center gap-2.5 px-3 text-xs font-medium text-[var(--app-fg2)] sm:gap-3 sm:px-4">
        <span class="flex-1">{{ sections[0]?.title }}</span>
        <span class="hidden w-[96px] text-end sm:block md:w-[112px]">{{ pages.columnVersion }}</span>
        <span class="w-[74px]" />
        <span class="flex w-[48px] items-center justify-end gap-0.5">
          <span>{{ pages.columnVisible }}</span>
          <button
            type="button"
            :aria-label="pages.columnVisible"
            class="inline-flex h-5 w-5 items-center justify-center rounded-full text-[var(--app-fg3)] hover:bg-[var(--app-subtle)] hover:text-[var(--ccs-fg)]"
            @mouseenter="tooltip.show($event, pages.visibleHelp, pages.columnVisible)"
            @mouseleave="tooltip.hide()"
          >
            <CcSwitchLucideIcon name="helpCircle" class-name="h-3.5 w-3.5" :stroke-width="1.5" />
          </button>
        </span>
      </div>

      <section
        v-for="(section, sIdx) in sections"
        :key="section.key"
        :aria-label="section.title"
      >
        <div v-if="sIdx > 0" class="mt-4 flex h-8 items-center px-3 text-xs font-medium text-[var(--app-fg2)] sm:px-4">
          {{ section.title }}
        </div>
        <div class="divide-y divide-[var(--ccs-border)] overflow-hidden rounded-[10px] border border-[var(--ccs-border)] bg-[var(--app-surface)]">
          <div
            v-for="row in section.rows"
            :key="row.app"
            :ref="(el) => setRowRef(row.app, el as HTMLElement | null)"
            class="px-3 py-3 transition-colors sm:px-4"
            :class="row.app === spotlightAppId && guidedActive ? 'bg-[var(--app-warning-soft)]/35' : ''"
          >
            <div class="flex items-center gap-2.5 sm:gap-3">
              <CcSwitchAppIcon
                :app="appSpec(row.app)"
                :size="20"
                badge-class="border-[var(--app-surface)] bg-[var(--app-surface)]"
              />
              <div class="flex min-w-0 flex-1 flex-col">
                <div class="flex min-w-0 items-center gap-1.5 text-sm font-medium text-[var(--ccs-fg)]">
                  <span class="truncate">{{ appSpec(row.app).label }}</span>
                  <span
                    v-if="row.source"
                    class="inline-flex h-[18px] shrink-0 items-center whitespace-nowrap rounded-full border border-[var(--app-border-strong)] px-1.5 text-[11px] font-medium leading-4 text-[var(--app-fg2)]"
                  >
                    {{ row.source }}
                  </span>
                  <button
                    v-if="desktopHelpKey(row.app)"
                    type="button"
                    :aria-label="appSpec(row.app).label"
                    class="inline-flex h-5 w-5 items-center justify-center rounded-full text-[var(--app-fg3)] hover:bg-[var(--app-subtle)] hover:text-[var(--ccs-fg)]"
                    @mouseenter="tooltip.show($event, desktopHelpBody(row.app), appSpec(row.app).label)"
                    @mouseleave="tooltip.hide()"
                  >
                    <CcSwitchLucideIcon name="helpCircle" class-name="h-3.5 w-3.5" :stroke-width="1.5" />
                  </button>
                </div>

                <div
                  v-if="!desktopHelpKey(row.app)"
                  class="mt-0.5 flex min-w-0 items-center gap-2 text-xs text-[var(--app-fg2)]"
                >
                  <span v-if="row.path" class="truncate font-mono">{{ row.path }}</span>
                  <span v-else class="truncate">
                    {{ row.installHint === 'script' ? pages.installHintScript : pages.installHintNpm }}
                  </span>
                  <button
                    v-if="row.conflict"
                    type="button"
                    class="inline-flex shrink-0 items-center gap-0.5 font-medium text-[var(--app-warning-text)]"
                    @click="conflictOpenApp = conflictOpenApp === row.app ? null : row.app"
                  >
                    <span>{{ fmt(pages.otherInstalls, { count: 1 }) }}</span>
                    <CcSwitchLucideIcon
                      name="chevronDown"
                      class-name="h-3 w-3 transition-transform"
                      :class="conflictOpenApp === row.app ? 'rotate-180' : ''"
                    />
                  </button>
                </div>
              </div>

              <!-- Version Column -->
              <div class="hidden w-[96px] shrink-0 flex-col items-end sm:flex md:w-[112px]">
                <span v-if="desktopHelpKey(row.app)" class="text-xs text-[var(--app-fg2)]">
                  {{ pages.desktopConnected }}
                </span>
                <CcSwitchLucideIcon
                  v-else-if="busyApps.includes(row.app)"
                  name="loaderCircle"
                  class-name="h-4 w-4 animate-spin text-[var(--app-fg3)]"
                />
                <template v-else-if="row.version">
                  <span class="font-mono text-[13px] font-medium tabular-nums text-[var(--ccs-fg)]">{{ row.version }}</span>
                  <span v-if="row.latest" class="text-xs text-[var(--app-fg2)]">
                    {{ fmt(pages.newVersion, { version: row.latest }) }}
                  </span>
                </template>
                <span v-else class="text-xs text-[var(--app-fg2)]">{{ pages.notInstalled }}</span>
              </div>

              <!-- Install / Update Action Column -->
              <div class="flex w-[74px] shrink-0 justify-end">
                <button
                  v-if="!desktopHelpKey(row.app) && !busyApps.includes(row.app) && !row.version"
                  type="button"
                  :data-testid="`demo-install-app-${row.app}`"
                  class="inline-flex h-7 shrink-0 items-center gap-1 whitespace-nowrap rounded-md border border-[var(--app-border-strong)] bg-[var(--app-surface)] px-2.5 text-[12.5px] font-medium text-[var(--ccs-fg)] transition-colors hover:bg-[var(--app-subtle)]"
                  :class="guidedActive && row.app === spotlightAppId ? 'ccs-spotlight-ring border-[var(--app-action)]' : ''"
                  @click="startBusy([row.app])"
                >
                  <CcSwitchLucideIcon name="download" class-name="h-3.5 w-3.5" :stroke-width="2" />
                  <span>{{ pages.install }}</span>
                </button>
                <button
                  v-else-if="!desktopHelpKey(row.app) && !busyApps.includes(row.app) && row.latest"
                  type="button"
                  :data-testid="`demo-update-app-${row.app}`"
                  class="inline-flex h-7 shrink-0 items-center gap-1 whitespace-nowrap rounded-md border border-[var(--app-border-strong)] bg-[var(--app-surface)] px-2.5 text-[12.5px] font-medium text-[var(--ccs-fg)] transition-colors hover:bg-[var(--app-subtle)]"
                  @click="startBusy([row.app])"
                >
                  <CcSwitchLucideIcon name="circleArrowUp" class-name="h-3.5 w-3.5" :stroke-width="2" />
                  <span>{{ pages.update }}</span>
                </button>
              </div>

              <!-- Sidebar Visibility Switch -->
              <div class="flex w-[48px] shrink-0 justify-end">
                <button
                  type="button"
                  role="switch"
                  :data-testid="`demo-toggle-app-${row.app}`"
                  :aria-checked="workbench.visibleApps.value.includes(row.app)"
                  :aria-label="fmt(pages.showInSidebar, { name: appSpec(row.app).label })"
                  :disabled="workbench.visibleApps.value.includes(row.app) && workbench.visibleApps.value.length <= 1"
                  class="relative inline-flex h-4 w-[26px] shrink-0 items-center rounded-full transition-colors duration-150 disabled:cursor-not-allowed disabled:opacity-50"
                  :class="[
                    workbench.visibleApps.value.includes(row.app) ? 'bg-[var(--app-fg2)]' : 'bg-[var(--ccs-switch-off)]',
                    guidedActive && row.app === spotlightAppId && isSpotlightInstalled && !isSpotlightVisible ? 'ccs-spotlight-ring-pill' : '',
                  ]"
                  @click="toggleVisibility(row.app)"
                >
                  <span
                    class="absolute left-0.5 h-3 w-3 rounded-full shadow-sm transition-transform duration-150"
                    :class="[
                      workbench.visibleApps.value.includes(row.app) ? 'translate-x-2.5 bg-[var(--app-surface)]' : 'translate-x-0 bg-white',
                    ]"
                  />
                </button>
              </div>
            </div>

            <!-- Conflict Expansion Panel -->
            <div v-if="row.conflict && conflictOpenApp === row.app" class="ms-9 mt-2 rounded-md bg-[var(--app-warning-soft)] p-2.5 text-xs">
              <p class="mb-1 font-medium text-[var(--app-warning-text)]">{{ fmt(pages.installsFound, { count: 2 }) }}</p>
              <p class="flex gap-2 font-mono text-[var(--ccs-fg)]">
                <span class="truncate">{{ row.path }}</span>
                <span class="text-[var(--app-fg2)]">{{ row.version }}</span>
              </p>
              <p class="flex gap-2 font-mono text-[var(--ccs-fg)]">
                <span class="truncate">{{ row.conflict.path }}</span>
                <span class="text-[var(--app-fg2)]">{{ row.conflict.version }}</span>
              </p>
            </div>
          </div>
        </div>
      </section>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { fmt, useCcSwitchI18n, useCcSwitchTooltip, useCcSwitchWorkbench } from './ccswitchContext'
import { CC_SWITCH_APP_MAP, INITIAL_APPS_INVENTORY } from './ccswitchDemoData'
import CcSwitchAppIcon from './CcSwitchAppIcon.vue'
import CcSwitchLucideIcon from './CcSwitchLucideIcon.vue'
import CcSwitchMenuButton from './CcSwitchMenuButton.vue'
import type { CcsInstalledAppRow } from './ccswitchTypes'

const props = withDefaults(
  defineProps<{
    guidedActive?: boolean
    spotlightAppId?: string
  }>(),
  {
    guidedActive: false,
    spotlightAppId: 'openclaw',
  },
)

const emit = defineEmits<{
  'dismiss-guide': []
  'app-installed': [appId: string]
}>()

const { msg } = useCcSwitchI18n()
const tooltip = useCcSwitchTooltip()
const workbench = useCcSwitchWorkbench()

const pages = computed(() => msg.value.window.pages.apps)
const rows = ref<CcsInstalledAppRow[]>(INITIAL_APPS_INVENTORY.map((r) => ({ ...r, conflict: r.conflict ? { ...r.conflict } : undefined })))
const busyApps = ref<string[]>([])
const checking = ref(false)
const lastChecked = ref('14:32')
const conflictOpenApp = ref<string | null>(null)
const scrollContainerRef = ref<HTMLElement | null>(null)
const rowRefs = new Map<string, HTMLElement>()

function setRowRef(appId: string, el: HTMLElement | null) {
  if (el) rowRefs.set(appId, el)
  else rowRefs.delete(appId)
}

async function scrollSpotlightIntoView() {
  if (!props.guidedActive || !props.spotlightAppId) return
  await nextTick()
  const container = scrollContainerRef.value
  const target = rowRefs.get(props.spotlightAppId)
  if (!container || !target) return
  const containerRect = container.getBoundingClientRect()
  const targetRect = target.getBoundingClientRect()
  const offsetTop = targetRect.top - containerRect.top + container.scrollTop
  const targetBottom = offsetTop + targetRect.height
  if (targetBottom > container.scrollTop + container.clientHeight || offsetTop < container.scrollTop) {
    container.scrollTop = Math.max(0, offsetTop - Math.max(24, (container.clientHeight - targetRect.height) / 2))
  }
}

onMounted(() => {
  void scrollSpotlightIntoView()
})

watch([() => props.guidedActive, () => props.spotlightAppId], () => {
  void scrollSpotlightIntoView()
})

let checkTimer: ReturnType<typeof setTimeout> | undefined
let installTimer: ReturnType<typeof setTimeout> | undefined

function appSpec(id: string) {
  return CC_SWITCH_APP_MAP[id] ?? CC_SWITCH_APP_MAP.claude
}

function desktopHelpKey(appId: string): 'desktopHelp' | 'workbuddyHelp' | null {
  if (appId === 'claude-desktop') return 'desktopHelp'
  if (appId === 'workbuddy') return 'workbuddyHelp'
  return null
}

function desktopHelpBody(appId: string): string {
  const key = desktopHelpKey(appId)
  return key ? pages.value[key] : ''
}

function isRowInstalled(row: CcsInstalledAppRow): boolean {
  return Boolean(desktopHelpKey(row.app)) || Boolean(row.version)
}

const updatableIds = computed(() => rows.value.filter((r) => r.latest).map((r) => r.app))

const sections = computed(() => {
  const installed = rows.value.filter(isRowInstalled)
  const notInstalled = rows.value.filter((r) => !isRowInstalled(r))
  return [
    { key: 'installed', title: fmt(pages.value.sectionInstalled, { count: installed.length }), rows: installed },
    { key: 'notInstalled', title: fmt(pages.value.sectionNotInstalled, { count: notInstalled.length }), rows: notInstalled },
  ].filter((s) => s.rows.length > 0)
})

const spotlightRow = computed(() => rows.value.find((r) => r.app === props.spotlightAppId))
const spotlightAppLabel = computed(() => appSpec(props.spotlightAppId).label)
const isSpotlightInstalled = computed(() => (spotlightRow.value ? isRowInstalled(spotlightRow.value) : true))
const isSpotlightVisible = computed(() => workbench.visibleApps.value.includes(props.spotlightAppId))
const showGuidedBanner = computed(() => props.guidedActive && (!isSpotlightInstalled.value || !isSpotlightVisible.value))

function triggerCheck() {
  if (checking.value) return
  checking.value = true
  if (checkTimer) clearTimeout(checkTimer)
  checkTimer = setTimeout(() => {
    checking.value = false
    const now = new Date()
    lastChecked.value = `${String(now.getHours()).padStart(2, '0')}:${String(now.getMinutes()).padStart(2, '0')}`
  }, 900)
}

function startBusy(ids: string[]) {
  if (!ids.length) return
  busyApps.value = Array.from(new Set([...busyApps.value, ...ids]))
  if (installTimer) clearTimeout(installTimer)
  installTimer = setTimeout(() => {
    const doneIds = [...busyApps.value]
    rows.value = rows.value.map((row) => {
      if (!doneIds.includes(row.app)) return row
      if (row.latest) {
        workbench.toast({ text: fmt(pages.value.updatedToast, { app: appSpec(row.app).label, version: row.latest }) })
        return { ...row, version: row.latest, latest: undefined }
      }
      const nextVer = row.app === 'openclaw' ? '2026.9.30' : '0.6.2'
      workbench.markAppInstalled(row.app)
      workbench.setAppVisible(row.app, true)
      workbench.toast({ text: fmt(pages.value.installedToast, { app: appSpec(row.app).label }) })
      emit('app-installed', row.app)
      return {
        ...row,
        version: nextVer,
        source: 'npm',
        path: `~/.npm-global/bin/${row.app}`,
        installHint: undefined,
      }
    })
    busyApps.value = []
  }, 450)
}

function toggleVisibility(appId: string) {
  const currentlyVisible = workbench.visibleApps.value.includes(appId)
  if (currentlyVisible && workbench.visibleApps.value.length <= 1) return
  workbench.setAppVisible(appId, !currentlyVisible)
}

onBeforeUnmount(() => {
  if (checkTimer) clearTimeout(checkTimer)
  if (installTimer) clearTimeout(installTimer)
})
</script>
