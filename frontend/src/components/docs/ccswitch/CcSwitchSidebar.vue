<template>
  <!-- Settings Sub-Sidebar (ji) -->
  <aside
    v-if="pageKind === 'settings'"
    class="flex shrink-0 flex-col border-r border-[var(--ccs-border)] bg-[var(--app-sidebar)] text-[13px] transition-[width] duration-200"
    :class="collapsed ? 'w-14' : 'w-14 md:w-[176px]'"
  >
    <!-- Traffic lights + Logo Header (Oi) -->
    <div
      aria-hidden="true"
      class="flex h-8 shrink-0 items-center gap-2 px-3"
      :class="collapsed ? 'justify-center' : 'justify-center md:justify-start'"
    >
      <div class="flex gap-1.5 sm:gap-2">
        <span class="h-2.5 w-2.5 rounded-full bg-red-500" />
        <span class="h-2.5 w-2.5 rounded-full bg-yellow-500" />
        <span class="h-2.5 w-2.5 rounded-full bg-green-500" />
      </div>
    </div>
    <div
      class="flex h-9 shrink-0 items-center gap-2 px-3"
      :class="collapsed ? 'justify-center' : 'justify-center md:justify-start'"
    >
      <img
        :src="CCS_LOCAL_ASSETS.ccSwitchLogo"
        alt="CC Switch"
        class="h-5 w-5 shrink-0 rounded-[4px] object-contain"
        :class="collapsed ? 'hidden' : ''"
      />
      <span
        class="me-auto truncate text-[15px] font-semibold text-[var(--ccs-fg)]"
        :class="collapsed ? 'hidden' : 'hidden md:inline'"
      >
        CC Switch
      </span>
      <button
        type="button"
        :aria-label="collapsed ? msg.window.expandSidebar : msg.window.collapseSidebar"
        class="h-6 w-6 items-center justify-center rounded-md text-[var(--ccs-muted-fg)] hover:bg-[var(--app-subtle)] hover:text-[var(--ccs-fg)]"
        :class="collapsed ? 'flex' : 'hidden md:flex'"
        @mouseenter="tooltip.show($event, collapsed ? msg.window.expandSidebar : msg.window.collapseSidebar)"
        @mouseleave="tooltip.hide()"
        @click="emit('toggle-collapse')"
      >
        <CcSwitchLucideIcon :name="collapsed ? 'chevronsRight' : 'chevronsLeft'" class-name="h-4 w-4" />
      </button>
    </div>

    <div class="mt-2 flex min-h-0 flex-1 flex-col gap-px px-2">
      <button
        type="button"
        :aria-label="msg.window.pages.settings.back"
        class="flex h-7 shrink-0 items-center gap-2 rounded-md px-2 text-[var(--app-fg2)] transition-colors hover:bg-[var(--app-subtle)] hover:text-[var(--ccs-fg)]"
        :class="collapsed ? 'justify-center' : 'justify-center md:justify-start'"
        @mouseenter="tooltip.show($event, msg.window.pages.settings.back)"
        @mouseleave="tooltip.hide()"
        @click="emit('select-app', activeAppId)"
      >
        <CcSwitchLucideIcon name="arrowLeft" class-name="h-4 w-4 shrink-0" :stroke-width="1.5" />
        <span :class="collapsed ? 'hidden' : 'hidden md:inline'">{{ msg.window.pages.settings.back }}</span>
      </button>

      <p class="mt-3 px-2 pb-1 text-[11px] text-[var(--app-fg3)]" :class="collapsed ? 'hidden' : 'hidden md:inline'">
        {{ msg.window.pages.settings.title }}
      </p>

      <button
        v-for="sec in settingsSections"
        :key="sec.key"
        type="button"
        :aria-label="sec.label"
        :aria-current="sec.key === 'general' ? 'page' : undefined"
        class="flex h-7 shrink-0 items-center gap-2 rounded-md px-2 text-[var(--ccs-fg)] transition-colors hover:bg-[var(--app-subtle)]"
        :class="[
          collapsed ? 'justify-center' : 'justify-center md:justify-start',
          sec.key === 'general' ? 'bg-[var(--app-selected)] font-medium hover:bg-[var(--app-selected)]' : '',
        ]"
        @mouseenter="tooltip.show($event, sec.key === 'general' ? sec.label : `${sec.label}\n${msg.window.pages.settings.onlyGeneral}`)"
        @mouseleave="tooltip.hide()"
      >
        <CcSwitchLucideIcon :name="sec.icon" class-name="h-4 w-4 shrink-0 text-[var(--app-fg2)]" :stroke-width="1.6" />
        <span class="truncate" :class="collapsed ? 'hidden' : 'hidden md:inline'">{{ sec.label }}</span>
      </button>
    </div>

    <p class="px-4 pb-4 text-xs text-[var(--app-fg3)]" :class="collapsed ? 'hidden' : 'hidden md:inline'">
      CC Switch v4.0.43
    </p>
  </aside>

  <!-- Main Workbench Sidebar (Ai) -->
  <aside
    v-else
    class="flex shrink-0 flex-col border-r border-[var(--ccs-border)] bg-[var(--app-sidebar)] text-[13px] transition-[width] duration-200"
    :class="collapsed ? 'w-14' : 'w-14 md:w-[176px]'"
  >
    <!-- Traffic lights + Logo Header (Oi) -->
    <div
      aria-hidden="true"
      class="flex h-8 shrink-0 items-center gap-2 px-3"
      :class="collapsed ? 'justify-center' : 'justify-center md:justify-start'"
    >
      <div class="flex gap-1.5 sm:gap-2">
        <span class="h-2.5 w-2.5 rounded-full bg-red-500" />
        <span class="h-2.5 w-2.5 rounded-full bg-yellow-500" />
        <span class="h-2.5 w-2.5 rounded-full bg-green-500" />
      </div>
    </div>
    <div
      class="flex h-9 shrink-0 items-center gap-2 px-3"
      :class="collapsed ? 'justify-center' : 'justify-center md:justify-start'"
    >
      <img
        :src="CCS_LOCAL_ASSETS.ccSwitchLogo"
        alt="CC Switch"
        class="h-5 w-5 shrink-0 rounded-[4px] object-contain"
        :class="collapsed ? 'hidden' : ''"
      />
      <span
        class="me-auto truncate text-[15px] font-semibold text-[var(--ccs-fg)]"
        :class="collapsed ? 'hidden' : 'hidden md:inline'"
      >
        CC Switch
      </span>
      <button
        type="button"
        :aria-label="collapsed ? msg.window.expandSidebar : msg.window.collapseSidebar"
        class="h-6 w-6 items-center justify-center rounded-md text-[var(--ccs-muted-fg)] hover:bg-[var(--app-subtle)] hover:text-[var(--ccs-fg)]"
        :class="collapsed ? 'flex' : 'hidden md:flex'"
        @mouseenter="tooltip.show($event, collapsed ? msg.window.expandSidebar : msg.window.collapseSidebar)"
        @mouseleave="tooltip.hide()"
        @click="emit('toggle-collapse')"
      >
        <CcSwitchLucideIcon :name="collapsed ? 'chevronsRight' : 'chevronsLeft'" class-name="h-4 w-4" />
      </button>
    </div>

    <!-- Scrollable Apps Nav (Kt) -->
    <nav class="mt-2 flex min-h-0 flex-1 flex-col gap-px overflow-y-auto px-2" aria-label="CC Switch Apps">
      <button
        v-for="app in sidebarApps"
        :key="app.id"
        type="button"
        :data-testid="`demo-sidebar-app-${app.id}`"
        :aria-current="pageKind === 'app' && activeAppId === app.id ? 'page' : undefined"
        :aria-label="app.label"
        class="relative flex h-7 shrink-0 items-center gap-2 rounded-md px-2 text-start transition-colors hover:bg-[var(--app-subtle)]"
        :class="[
          collapsed ? 'justify-center' : 'justify-center md:justify-start',
          pageKind === 'app' && activeAppId === app.id
            ? 'bg-[var(--app-selected)] font-medium text-[var(--ccs-fg)] hover:bg-[var(--app-selected)]'
            : 'text-[var(--ccs-fg)]',
        ]"
        @mouseenter="collapsed ? tooltip.show($event, appModeTip(app)) : undefined"
        @mouseleave="tooltip.hide()"
        @click="emit('select-app', app.id)"
      >
        <CcSwitchAppIcon
          :app="app"
          :size="16"
          :badge-class="
            pageKind === 'app' && activeAppId === app.id
              ? 'border-[var(--app-selected)] bg-[var(--app-selected)]'
              : 'border-[var(--app-sidebar)] bg-[var(--app-sidebar)]'
          "
        />
        <span class="min-w-0 flex-1 truncate" :class="collapsed ? 'hidden' : 'hidden md:inline'">
          {{ app.label }}
        </span>
        <span
          v-if="appModePill(app) && !(pageKind === 'app' && activeAppId === app.id)"
          class="h-[18px] whitespace-nowrap rounded-full px-1.5 text-[11px] leading-[18px]"
          :class="[
            collapsed ? 'hidden' : 'hidden md:inline-block',
            appStates[app.id]?.active === 'stack'
              ? 'bg-[var(--app-stack-soft)] text-[var(--app-stack-text)]'
              : 'bg-[var(--app-route-soft)] text-[var(--app-route-text)]',
          ]"
        >
          {{ appModePill(app) }}
        </span>
      </button>
    </nav>

    <div class="mx-3 mt-2 border-t border-[var(--ccs-border)]" />

    <!-- 6 Utility Links -->
    <div class="flex shrink-0 flex-col gap-px px-2 py-2">
      <button
        v-for="item in utilityItems"
        :key="item.key"
        type="button"
        :data-testid="`demo-sidebar-${item.key}`"
        :aria-label="item.label"
        :aria-current="pageKind === item.key ? 'page' : undefined"
        class="flex h-7 shrink-0 items-center gap-2 rounded-md px-2 text-[var(--ccs-fg)] transition-colors hover:bg-[var(--app-subtle)]"
        :class="[
          collapsed ? 'justify-center' : 'justify-center md:justify-start',
          pageKind === item.key ? 'bg-[var(--app-selected)] font-medium hover:bg-[var(--app-selected)]' : '',
        ]"
        @mouseenter="collapsed ? tooltip.show($event, item.label) : undefined"
        @mouseleave="tooltip.hide()"
        @click="emit('open-page', item.key)"
      >
        <CcSwitchLucideIcon :name="item.icon" class-name="h-4 w-4 shrink-0" :stroke-width="1.6" />
        <span class="min-w-0 flex-1 truncate text-start" :class="collapsed ? 'hidden' : 'hidden md:inline'">
          {{ item.label }}
        </span>
        <span
          v-if="item.key === 'usage'"
          class="shrink-0 text-[11px] font-normal tabular-nums text-[var(--ccs-muted-fg)]"
          :class="collapsed ? 'hidden' : 'hidden lg:inline'"
        >
          {{ fmt(msg.window.nav.todayCost, { cost: todayCostLabel }) }}
        </span>
      </button>
    </div>

    <!-- Bottom Footer: Apps + Settings -->
    <div
      class="flex shrink-0 gap-1 border-t border-[var(--ccs-border)] px-2 py-2"
      :class="collapsed ? 'flex-col items-center' : 'flex-col items-center md:flex-row'"
    >
      <button
        v-for="foot in footerItems"
        :key="foot.key"
        type="button"
        :data-testid="`demo-sidebar-${foot.key}`"
        :aria-label="foot.label"
        :aria-current="pageKind === foot.key ? 'page' : undefined"
        class="flex h-7 flex-1 items-center justify-center gap-2 rounded-md px-2 text-[var(--ccs-fg)] transition-colors hover:bg-[var(--app-subtle)] md:justify-start"
        :class="pageKind === foot.key ? 'bg-[var(--app-selected)] font-medium hover:bg-[var(--app-selected)]' : ''"
        @mouseenter="tooltip.show($event, foot.label)"
        @mouseleave="tooltip.hide()"
        @click="emit('open-page', foot.key)"
      >
        <CcSwitchLucideIcon :name="foot.icon" class-name="h-4 w-4 shrink-0" :stroke-width="1.6" />
        <span class="truncate" :class="collapsed ? 'hidden' : 'hidden md:inline'">{{ foot.label }}</span>
      </button>
    </div>
  </aside>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { fmt, useCcSwitchI18n, useCcSwitchTooltip } from './ccswitchContext'
import { CC_SWITCH_DEMO_APPS, calcTodayTotalCost, formatUsd } from './ccswitchDemoData'
import { CCS_LOCAL_ASSETS } from './ccswitchIcons'
import CcSwitchAppIcon from './CcSwitchAppIcon.vue'
import CcSwitchLucideIcon from './CcSwitchLucideIcon.vue'
import type { CcsAppRuntimeState, CcsAppSpec, CcsPageKind } from './ccswitchTypes'

const props = defineProps<{
  pageKind: CcsPageKind
  activeAppId: string
  visibleApps: string[]
  appStates: Record<string, CcsAppRuntimeState>
  collapsed: boolean
}>()

const emit = defineEmits<{
  'toggle-collapse': []
  'select-app': [appId: string]
  'open-page': [kind: CcsPageKind]
}>()

const { msg } = useCcSwitchI18n()
const tooltip = useCcSwitchTooltip()

const todayCostLabel = formatUsd(calcTodayTotalCost())

const sidebarApps = computed(() => CC_SWITCH_DEMO_APPS.filter((a) => props.visibleApps.includes(a.id)))

function appModePill(app: CcsAppSpec): string | null {
  const mode = props.appStates[app.id]?.active
  if (mode === 'route') return msg.value.window.modes.route
  if (mode === 'stack') return msg.value.window.modes.stack
  return null
}

function appModeTip(app: CcsAppSpec): string {
  const pill = appModePill(app)
  return pill ? `${app.label} · ${pill}` : app.label
}

const utilityItems = computed<Array<{ key: CcsPageKind; label: string; icon: string }>>(() => [
  { key: 'mcp', label: msg.value.window.nav.mcp, icon: 'server' },
  { key: 'skills', label: msg.value.window.nav.skills, icon: 'skills' },
  { key: 'prompts', label: msg.value.window.nav.prompts, icon: 'bookOpen' },
  { key: 'sessions', label: msg.value.window.nav.sessions, icon: 'history' },
  { key: 'auth', label: msg.value.window.nav.auth, icon: 'keyRound' },
  { key: 'usage', label: msg.value.window.nav.usage, icon: 'chartColumn' },
])

const footerItems = computed<Array<{ key: CcsPageKind; label: string; icon: string }>>(() => [
  { key: 'apps', label: msg.value.window.nav.apps, icon: 'layoutGrid' },
  { key: 'settings', label: msg.value.window.nav.settings, icon: 'settings' },
])

const settingsSections = computed(() => [
  { key: 'general', label: msg.value.window.pages.settings.sections.general, icon: 'slidersHorizontal' },
  { key: 'appConfig', label: msg.value.window.pages.settings.sections.appConfig, icon: 'folder' },
  { key: 'routing', label: msg.value.window.pages.settings.sections.routing, icon: 'route' },
  { key: 'network', label: msg.value.window.pages.settings.sections.network, icon: 'globe' },
  { key: 'data', label: msg.value.window.pages.settings.sections.data, icon: 'database' },
  { key: 'about', label: msg.value.window.pages.settings.sections.about, icon: 'info' },
])
</script>
