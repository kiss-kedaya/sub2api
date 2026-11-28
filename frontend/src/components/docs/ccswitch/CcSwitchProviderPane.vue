<template>
  <div class="flex h-full min-h-0 flex-col" data-testid="ccs-demo-provider-pane">
    <!-- Top Header (lr) -->
    <header class="flex h-14 shrink-0 items-center gap-3 border-b border-[var(--ccs-border)] px-4 sm:px-6">
      <CcSwitchAppIcon
        :app="app"
        :size="22"
        badge-class="border-[var(--ccs-bg)] bg-[var(--ccs-bg)]"
      />
      <h3 class="shrink-0 truncate text-lg font-semibold text-[var(--ccs-fg)]">{{ app.label }}</h3>

      <!-- Live Status Pill (dr) -->
      <span
        v-if="app.kind === 'switch'"
        tabindex="0"
        :aria-label="statusTooltip"
        class="hidden min-w-0 items-center gap-1 rounded-full border border-[var(--ccs-border)] bg-[var(--app-surface)] px-2.5 py-1 text-[13px] sm:inline-flex"
        @mouseenter="tooltip.show($event, statusTooltip)"
        @mouseleave="tooltip.hide()"
      >
        <span aria-hidden="true" class="me-1 h-1.5 w-1.5 shrink-0 rounded-full" :class="modeDotBg[state.active]" />
        <span class="shrink-0 text-[var(--app-fg2)]">{{ msg.window.modes[state.active] }}</span>
        <span aria-hidden="true" class="text-[var(--app-fg3)]">·</span>
        <span class="min-w-0 truncate font-medium text-[var(--ccs-fg)]">{{ activeStatusName }}</span>
      </span>

      <span class="flex-1" />

      <!-- Primary Orange CTA: + Add Provider -->
      <button
        type="button"
        data-testid="demo-add-provider-btn"
        class="flex h-8 shrink-0 items-center gap-1.5 rounded-lg bg-[var(--app-action)] px-2.5 text-sm font-medium text-white transition-colors hover:bg-[var(--app-action-hover)] sm:px-3"
        :class="guidedStep === 1 || guidedStep === 2 ? 'ccs-spotlight-ring' : ''"
        @mouseenter="tooltip.show($event, msg.window.addProvider)"
        @mouseleave="tooltip.hide()"
        @click="onAddProviderClick"
      >
        <CcSwitchLucideIcon name="plus" class-name="h-4 w-4" :stroke-width="2.25" />
        <span class="hidden sm:inline">{{ msg.window.addProvider }}</span>
      </button>

      <CcSwitchMenuButton
        large
        :label="msg.window.more"
        :items="headerMoreItems"
        class="hidden sm:inline-flex"
      />
    </header>

    <!-- Switchable Apps: Mode Switcher + Provider List (ur) -->
    <template v-if="app.kind === 'switch'">
      <div class="flex min-h-12 shrink-0 items-center gap-3 px-4 pt-3 sm:px-6">
        <div
          class="relative inline-flex h-9 shrink-0 items-center gap-0.5 rounded-[10px] bg-[var(--app-subtle)] p-[3px]"
          role="tablist"
        >
          <button
            v-for="mode in availableModes"
            :key="mode"
            type="button"
            role="tab"
            :data-testid="`demo-mode-tab-${mode}`"
            :aria-selected="state.view === mode"
            :aria-pressed="state.view === mode"
            class="relative inline-flex h-[30px] items-center justify-center gap-1.5 rounded-[7px] px-3 text-sm transition-colors sm:min-w-[88px]"
            :class="[
              state.view === mode ? 'bg-[var(--ccs-bg)] shadow-sm' : '',
              state.active === mode
                ? modeTextColor[mode]
                : state.view === mode
                  ? 'text-[var(--ccs-fg)]'
                  : 'text-[var(--ccs-muted-fg)] hover:text-[var(--ccs-fg)]',
              state.view === mode || state.active === mode ? 'font-semibold' : 'font-medium',
            ]"
            @click="updateState({ view: mode })"
          >
            <CcSwitchLucideIcon :name="modeIconName[mode]" class-name="relative h-3.5 w-3.5" :stroke-width="2" />
            <span class="relative">{{ msg.window.modes[mode] }}</span>
            <span
              v-if="state.active === mode"
              class="relative h-1.5 w-1.5 rounded-full"
              :class="modeDotBg[mode]"
            />
          </button>
        </div>

        <button
          type="button"
          :aria-label="msg.window.modeHelp.title"
          class="-ml-1 hidden text-[var(--ccs-muted-fg)] hover:text-[var(--ccs-fg)] sm:block"
          @mouseenter="tooltip.show($event, modeHelpText, msg.window.modeHelp.title)"
          @mouseleave="tooltip.hide()"
        >
          <CcSwitchLucideIcon name="helpCircle" class-name="h-4 w-4" :stroke-width="1.75" />
        </button>
      </div>

      <div class="min-h-0 flex-1 overflow-y-auto px-4 pb-6 pt-3 sm:px-6">
        <!-- Beginner Guided Banners (Step 1/2/3) -->
        <div
          v-if="guidedBanner"
          data-testid="ccs-guided-banner-provider"
          class="mb-3 flex flex-wrap items-center justify-between gap-2.5 rounded-xl border border-[var(--app-action)]/40 bg-[var(--app-warning-soft)] px-3.5 py-2.5 text-xs"
        >
          <div class="flex min-w-0 flex-1 items-center gap-2">
            <span class="inline-flex shrink-0 items-center gap-1.5 rounded-full bg-[var(--app-action)] px-2 py-0.5 text-[11px] font-semibold text-white">
              <span class="h-1.5 w-1.5 rounded-full bg-white" />
              {{ guidedBanner.badge }}
            </span>
            <span class="min-w-0 text-[var(--ccs-fg)]">{{ guidedBanner.text }}</span>
          </div>
          <div class="flex shrink-0 items-center gap-2">
            <button
              v-if="guidedBanner.ctaLabel"
              type="button"
              :data-testid="guidedBanner.ctaTestId"
              class="h-7 rounded-md bg-[var(--app-action)] px-2.5 text-xs font-semibold text-white transition-colors hover:bg-[var(--app-action-hover)]"
              @click="guidedBanner.onCta()"
            >
              {{ guidedBanner.ctaLabel }}
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

        <!-- Mismatch Activation Banner -->
        <div
          v-if="state.view !== state.active"
          class="mb-3 flex flex-col gap-3 rounded-xl border border-[var(--ccs-border)] bg-[var(--app-subtle)] px-4 py-3 sm:flex-row sm:items-center"
        >
          <p class="min-w-0 flex-1 text-sm text-[var(--ccs-fg)]">
            {{
              fmt(msg.window.activate.viewing, {
                view: msg.window.modes[state.view],
                app: app.label,
                active: msg.window.modes[state.active],
              })
            }}
          </p>
          <button
            type="button"
            class="h-8 shrink-0 self-start rounded-lg px-3.5 text-sm font-medium text-white transition-opacity hover:opacity-90 sm:self-auto"
            :class="modeDotBg[state.view]"
            @click="activateViewedMode"
          >
            {{ msg.window.activate[state.view] }}
          </button>
        </div>

        <!-- Direct View -->
        <TransitionGroup
          v-if="state.view === 'direct'"
          name="ccs-list"
          tag="div"
          class="flex flex-col gap-2.5"
        >
          <CcSwitchProviderCard
            v-for="item in directCards"
            :key="item.provider.name"
            :provider="item.provider"
            :current="item.current"
            :chips="item.chips"
            :status="item.status"
            :action-button="item.actionButton"
            :spotlight-action="item.spotlightAction"
            :highlight-ring="item.highlightRing"
            @edit="emit('edit-provider', $event)"
          />
        </TransitionGroup>

        <!-- Route View -->
        <TransitionGroup
          v-else-if="state.view === 'route'"
          name="ccs-list"
          tag="div"
          class="flex flex-col gap-2.5"
        >
          <CcSwitchProviderCard
            v-for="item in routeCards"
            :key="item.provider.name"
            :provider="item.provider"
            :current="item.current"
            :chips="item.chips"
            :status="item.status"
            :action-button="item.actionButton"
            :spotlight-action="item.spotlightAction"
            @edit="emit('edit-provider', $event)"
          />
        </TransitionGroup>

        <!-- Stack / Aggregation View -->
        <template v-else>
          <div class="mb-2 mt-1 flex items-center gap-1.5 text-[13px] font-medium text-[var(--ccs-muted-fg)]">
            <span>
              {{ state.active === 'stack' ? msg.window.section.stackDefault : msg.window.section.stackDefaultAfterSwitch }}
            </span>
            <span
              class="inline-flex"
              @mouseenter="tooltip.show($event, msg.window.modeHelp.stack)"
              @mouseleave="tooltip.hide()"
            >
              <CcSwitchLucideIcon name="helpCircle" class-name="h-3.5 w-3.5" :stroke-width="1.75" />
            </span>
          </div>
          <div v-if="stackDefaultProvider" class="flex flex-col gap-2.5">
            <CcSwitchProviderCard
              :provider="stackDefaultProvider"
              :current="state.active === 'stack' ? 'stack' : undefined"
              :chips="stackDefaultChips"
              :status="
                state.active === 'stack'
                  ? { label: msg.window.cardStatus.currentDefault, tone: 'stack' }
                  : { label: msg.window.cardStatus.defaultAfterSwitch, tone: 'muted' }
              "
              @edit="emit('edit-provider', $event)"
            />
          </div>

          <div class="mb-2 mt-4 flex items-center gap-1.5 text-[13px] font-medium text-[var(--ccs-muted-fg)]">
            <span>{{ fmt(msg.window.section.added, { count: stackMemberProviders.length }) }}</span>
          </div>
          <div class="flex flex-col gap-2.5">
            <CcSwitchProviderCard
              v-for="p in stackMemberProviders"
              :key="p.name"
              :provider="p"
              :chips="[{ label: fmt(msg.window.chip.models, { count: state.stack[p.name] ?? 2 }) }]"
              :action-button="{
                key: 'remove',
                label: msg.window.action.remove,
                onClick: () => removeStackMember(p.name),
              }"
              @edit="emit('edit-provider', $event)"
            />
          </div>

          <template v-if="stackAvailableProviders.length > 0">
            <div class="mb-2 mt-4 flex items-center gap-1.5 text-[13px] font-medium text-[var(--ccs-muted-fg)]">
              <span>{{ fmt(msg.window.section.available, { count: stackAvailableProviders.length }) }}</span>
            </div>
            <div class="flex flex-col gap-2.5">
              <CcSwitchProviderCard
                v-for="p in stackAvailableProviders"
                :key="p.name"
                :provider="p"
                :chips="baseChipsFor(p)"
                :action-button="{
                  key: 'add',
                  label: msg.window.action.add,
                  disabledReason: stackBlockedReason(p),
                  onClick: () => addStackMember(p.name),
                }"
                @edit="emit('edit-provider', $event)"
              />
            </div>
          </template>
        </template>
      </div>
    </template>

    <!-- Launch App (Antigravity) -->
    <div v-else-if="app.kind === 'launch'" class="min-h-0 flex-1 overflow-y-auto px-4 pb-6 pt-4 sm:px-6">
      <div class="flex flex-col gap-2.5">
        <CcSwitchProviderCard
          v-for="p in providers"
          :key="p.name"
          :provider="p"
          :chips="baseChipsFor(p)"
          :action-button="{
            key: 'openTerminal',
            label: msg.window.action.openTerminal,
            onClick: () => workbench.toast({ text: `${p.name} · Terminal` }),
          }"
          @edit="emit('edit-provider', $event)"
        />
      </div>
    </div>

    <!-- Additive Apps (OpenCode, OpenClaw, Hermes, Pi, etc.) -->
    <div v-else class="min-h-0 flex-1 overflow-y-auto px-4 pb-6 pt-1 sm:px-6">
      <!-- Beginner Guided Banner for Additive Apps -->
      <div
        v-if="guidedBanner"
        data-testid="ccs-guided-banner-provider"
        class="mb-2 mt-2 flex flex-wrap items-center justify-between gap-2.5 rounded-xl border border-[var(--app-action)]/40 bg-[var(--app-warning-soft)] px-3.5 py-2.5 text-xs"
      >
        <div class="flex min-w-0 flex-1 items-center gap-2">
          <span class="inline-flex shrink-0 items-center gap-1.5 rounded-full bg-[var(--app-action)] px-2 py-0.5 text-[11px] font-semibold text-white">
            <span class="h-1.5 w-1.5 rounded-full bg-white" />
            {{ guidedBanner.badge }}
          </span>
          <span class="min-w-0 text-[var(--ccs-fg)]">{{ guidedBanner.text }}</span>
        </div>
        <div class="flex shrink-0 items-center gap-2">
          <button
            v-if="guidedBanner.ctaLabel"
            type="button"
            :data-testid="guidedBanner.ctaTestId"
            class="h-7 rounded-md bg-[var(--app-action)] px-2.5 text-xs font-semibold text-white transition-colors hover:bg-[var(--app-action-hover)]"
            @click="guidedBanner.onCta()"
          >
            {{ guidedBanner.ctaLabel }}
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

      <div class="mb-2 mt-4 flex items-center gap-1.5 text-[13px] font-medium text-[var(--ccs-muted-fg)] first:mt-1">
        <span>{{ fmt(msg.window.section.added, { count: additiveAddedProviders.length }) }}</span>
        <span
          v-if="app.sharedList"
          class="inline-flex"
          @mouseenter="tooltip.show($event, msg.window.section.sharedListHelp)"
          @mouseleave="tooltip.hide()"
        >
          <CcSwitchLucideIcon name="helpCircle" class-name="h-3.5 w-3.5" :stroke-width="1.75" />
        </span>
        <span v-if="app.sharedList" class="truncate font-normal text-[var(--app-fg3)]">
          {{ msg.window.section.sharedListNote }}
        </span>
      </div>

      <div class="flex flex-col gap-2.5">
        <CcSwitchProviderCard
          v-for="p in additiveAddedProviders"
          :key="p.name"
          :provider="p"
          :current="p.isSiteProvider ? 'direct' : undefined"
          :chips="baseChipsFor(p)"
          :status="p.isSiteProvider ? { label: msg.window.cardStatus.inUse, tone: 'direct' } : undefined"
          :action-button="additiveAddedAction(p)"
          @edit="emit('edit-provider', $event)"
        />
      </div>

      <template v-if="additiveAvailableProviders.length > 0">
        <div class="mb-2 mt-4 flex items-center gap-1.5 text-[13px] font-medium text-[var(--ccs-muted-fg)]">
          <span>{{ fmt(msg.window.section.available, { count: additiveAvailableProviders.length }) }}</span>
        </div>
        <div class="flex flex-col gap-2.5">
          <CcSwitchProviderCard
            v-for="p in additiveAvailableProviders"
            :key="p.name"
            :provider="p"
            :chips="baseChipsFor(p)"
            :spotlight-action="Boolean(p.isSiteProvider && guidedStep === 3)"
            :action-button="{
              key: app.enableLabel ? 'enable' : 'add',
              label: app.enableLabel ? msg.window.action.enable : msg.window.action.add,
              onClick: () => addAdditiveProvider(p),
            }"
            @edit="emit('edit-provider', $event)"
          />
        </div>
      </template>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { fmt, useCcSwitchI18n, useCcSwitchTooltip, useCcSwitchWorkbench } from './ccswitchContext'
import { isRouteOrStackBlocked, providerNeedsRoute } from './ccswitchDemoData'
import CcSwitchAppIcon from './CcSwitchAppIcon.vue'
import CcSwitchLucideIcon from './CcSwitchLucideIcon.vue'
import CcSwitchMenuButton from './CcSwitchMenuButton.vue'
import CcSwitchProviderCard, { type ProviderCardAction, type ProviderCardChip } from './CcSwitchProviderCard.vue'
import type { CcsAppRuntimeState, CcsAppSpec, CcsMenuItem, CcsMode, CcsProviderItem } from './ccswitchTypes'

const props = withDefaults(
  defineProps<{
    app: CcsAppSpec
    providers: CcsProviderItem[]
    state: CcsAppRuntimeState
    siteName: string
    maskedKeyPreview?: string
    hasLiveKey?: boolean
    importedToCcs?: boolean
    guidedStep?: number | null
  }>(),
  {
    maskedKeyPreview: '',
    hasLiveKey: false,
    importedToCcs: false,
    guidedStep: null,
  },
)

const emit = defineEmits<{
  'update:state': [next: CcsAppRuntimeState]
  'add-provider': []
  'edit-provider': [provider: CcsProviderItem]
  'request-create-key': []
  'request-import-ccs': []
  'provider-activated': [appId: string]
  'dismiss-guide': []
}>()

const { msg } = useCcSwitchI18n()
const tooltip = useCcSwitchTooltip()
const workbench = useCcSwitchWorkbench()

const availableModes = computed<CcsMode[]>(() => props.app.modes ?? ['direct'])

const modeIconName: Record<CcsMode, string> = {
  direct: 'shieldCheck',
  route: 'route',
  stack: 'layers',
}

const modeTextColor: Record<CcsMode, string> = {
  direct: 'text-[var(--app-direct-text)]',
  route: 'text-[var(--app-route-text)]',
  stack: 'text-[var(--app-stack-text)]',
}

const modeDotBg: Record<CcsMode, string> = {
  direct: 'bg-[var(--app-direct)]',
  route: 'bg-[var(--app-route)]',
  stack: 'bg-[var(--app-stack)]',
}

const modeHelpText = computed(() =>
  [msg.value.window.modeHelp.title, ...availableModes.value.map((m) => msg.value.window.modeHelp[m])].join('\n'),
)

const activeStatusName = computed(() => {
  if (props.state.active === 'direct') return props.state.direct
  if (props.state.active === 'route') return props.state.route
  return props.state.stackDefault
})

const statusTooltip = computed(() => {
  const st = msg.value.window.status
  if (props.state.active === 'direct') return `${st.directLead} ${props.state.direct}`
  if (props.state.active === 'route') return `${st.routeLead} → ${props.state.route}`
  const count = Object.keys(props.state.stack).length
  const models = Object.values(props.state.stack).reduce((a, b) => a + b, 0)
  return `${st.stackLead} ${fmt(st.stackValue, { name: props.state.stackDefault, count, models })}`
})

const headerMoreItems = computed<CcsMenuItem[]>(() => [
  {
    label: msg.value.window.addProvider,
    icon: 'plus',
    onSelect: () => emit('add-provider'),
  },
  {
    label: msg.value.window.nav.usage,
    icon: 'chartColumn',
    onSelect: () => workbench.openPage('usage'),
  },
])

function updateState(patch: Partial<CcsAppRuntimeState>) {
  emit('update:state', { ...props.state, ...patch })
}

function baseChipsFor(p: CcsProviderItem): ProviderCardChip[] {
  const chips: ProviderCardChip[] = []
  if (p.official) chips.push({ label: msg.value.window.chip.official })
  if (p.isSiteProvider) {
    chips.push({ label: msg.value.window.chip.siteRecommended, tone: 'direct' })
    chips.push({
      label: props.importedToCcs ? msg.value.window.chip.siteImported : msg.value.window.chip.siteOneClickImport,
      tone: props.importedToCcs ? 'direct' : 'route',
    })
  }
  return chips
}

function needsRouteReason(p: CcsProviderItem): string | undefined {
  if (!providerNeedsRoute(props.app, p)) return undefined
  return fmt(msg.value.window.reason.needsRoute, {
    reason: props.app.id === 'claude-desktop' ? msg.value.window.reason.desktopMapping : msg.value.window.reason.openaiChat,
  })
}

function activateSiteProvider() {
  const siteProvider = props.providers.find((p) => p.isSiteProvider)
  if (!siteProvider) return
  if (props.app.kind === 'switch') {
    updateState({ active: 'direct', view: 'direct', direct: siteProvider.name })
  } else if (!props.state.added.includes(siteProvider.name)) {
    updateState({ added: [siteProvider.name, ...props.state.added] })
  }
  workbench.toast({
    text: fmt(msg.value.window.guided.step3DoneToast, { app: props.app.label, site: props.siteName }),
  })
  emit('provider-activated', props.app.id)
}

function onAddProviderClick() {
  if (props.guidedStep === 1 && !props.hasLiveKey) {
    emit('request-create-key')
    return
  }
  if (props.guidedStep === 2 && props.hasLiveKey && !props.importedToCcs) {
    emit('request-import-ccs')
    return
  }
  emit('add-provider')
}

const isSiteActivated = computed(() => {
  const siteProvider = props.providers.find((p) => p.isSiteProvider)
  if (!siteProvider) return false
  if (props.app.kind === 'switch') {
    return props.state.active === 'direct' && props.state.direct === siteProvider.name
  }
  return props.state.added.includes(siteProvider.name)
})

const guidedBanner = computed<{
  badge: string
  text: string
  ctaLabel?: string
  ctaTestId?: string
  onCta: () => void
} | null>(() => {
  if (props.guidedStep === null || props.guidedStep === undefined) return null
  const g = msg.value.window.guided
  if (props.guidedStep === 1 && !props.hasLiveKey) {
    return {
      badge: g.step1Badge,
      text: fmt(g.step1Text, { site: props.siteName }),
      ctaLabel: g.step1Action,
      ctaTestId: 'ccs-guided-create-key-btn',
      onCta: () => emit('request-create-key'),
    }
  }
  if ((props.guidedStep === 1 || props.guidedStep === 2) && props.hasLiveKey && !props.importedToCcs) {
    return {
      badge: g.step2Badge,
      text: fmt(g.step2Text, { key: props.maskedKeyPreview || 'API Key', site: props.siteName }),
      ctaLabel: g.step2Action,
      ctaTestId: 'ccs-guided-import-ccs-btn',
      onCta: () => emit('request-import-ccs'),
    }
  }
  if (props.guidedStep === 3 || (props.importedToCcs && !isSiteActivated.value)) {
    if (isSiteActivated.value) {
      return {
        badge: g.step3Badge,
        text: fmt(g.step3DoneText, { site: props.siteName }),
        onCta: () => undefined,
      }
    }
    return {
      badge: g.step3Badge,
      text: fmt(g.step3Text, { site: props.siteName, app: props.app.label }),
      ctaLabel: g.step3Action,
      ctaTestId: 'ccs-guided-activate-btn',
      onCta: activateSiteProvider,
    }
  }
  return null
})

interface DirectOrRouteCardVM {
  provider: CcsProviderItem
  current?: CcsMode
  chips: ProviderCardChip[]
  status?: { label: string; tone: CcsMode | 'muted' }
  actionButton?: ProviderCardAction
  spotlightAction?: boolean
  highlightRing?: boolean
}

const directCards = computed<DirectOrRouteCardVM[]>(() =>
  props.providers.map((p) => {
    const isDirectTarget = p.name === props.state.direct
    const routeReason = needsRouteReason(p)
    const chips: ProviderCardChip[] = [...baseChipsFor(p)]
    if (routeReason) chips.push({ label: msg.value.window.chip.needsRoute })

    if (props.state.active === 'direct') {
      if (isDirectTarget) {
        return {
          provider: p,
          current: 'direct',
          chips,
          status: { label: msg.value.window.cardStatus.inUse, tone: 'direct' },
        }
      }
      const isSiteSpotlight = Boolean(p.isSiteProvider && props.guidedStep === 3)
      return {
        provider: p,
        chips,
        spotlightAction: isSiteSpotlight,
        highlightRing: Boolean(p.isSiteProvider && (props.importedToCcs || props.guidedStep === 2 || props.guidedStep === 3)),
        actionButton: {
          key: 'switch',
          label: msg.value.window.action.switch,
          disabledReason: routeReason,
          onClick: () => {
            updateState({ direct: p.name })
            if (p.isSiteProvider) {
              workbench.toast({
                text: fmt(msg.value.window.guided.step3DoneToast, { app: props.app.label, site: props.siteName }),
              })
              emit('provider-activated', props.app.id)
            }
          },
        },
      }
    }

    if (isDirectTarget) {
      chips.push({ label: msg.value.window.chip.directWhenDirect })
    }
    return {
      provider: p,
      chips,
      spotlightAction: Boolean(p.isSiteProvider && props.guidedStep === 3),
      actionButton: {
        key: 'exitAndUse',
        label: msg.value.window.action.exitAndUse,
        disabledReason: routeReason,
        onClick: () => {
          updateState({ active: 'direct', view: 'direct', direct: p.name })
          if (p.isSiteProvider) {
            emit('provider-activated', props.app.id)
          }
        },
      },
    }
  }),
)

const routeCards = computed<DirectOrRouteCardVM[]>(() =>
  props.providers.map((p) => {
    const blocked = isRouteOrStackBlocked(props.app.id, p)
    const chips = baseChipsFor(p)
    if (props.state.active === 'route' && p.name === props.state.route) {
      return {
        provider: p,
        current: 'route',
        chips,
        status: { label: msg.value.window.cardStatus.routing, tone: 'route' },
      }
    }
    if (props.state.active === 'route') {
      return {
        provider: p,
        chips,
        actionButton: {
          key: 'routeHere',
          label: msg.value.window.action.routeHere,
          disabledReason: blocked ? msg.value.window.reason.noRoute : undefined,
          onClick: () => updateState({ route: p.name }),
        },
      }
    }
    return { provider: p, chips }
  }),
)

const stackDefaultProvider = computed(() => props.providers.find((p) => p.name === props.state.stackDefault))
const stackDefaultChips = computed<ProviderCardChip[]>(() => {
  const chips: ProviderCardChip[] = [{ label: msg.value.window.chip.default, tone: 'stack' }]
  if (stackDefaultProvider.value) chips.push(...baseChipsFor(stackDefaultProvider.value))
  return chips
})
const stackMemberProviders = computed(() =>
  Object.keys(props.state.stack)
    .map((name) => props.providers.find((p) => p.name === name))
    .filter((p): p is CcsProviderItem => Boolean(p)),
)
const stackAvailableProviders = computed(() =>
  props.providers.filter((p) => p.name !== props.state.stackDefault && !(p.name in props.state.stack)),
)

function stackBlockedReason(p: CcsProviderItem): string | undefined {
  if (isRouteOrStackBlocked(props.app.id, p)) return msg.value.window.reason.noStack
  if (p.official) return msg.value.window.reason.officialStack
  return undefined
}

function addStackMember(name: string) {
  updateState({ stack: { ...props.state.stack, [name]: 2 } })
}

function removeStackMember(name: string) {
  const next = { ...props.state.stack }
  delete next[name]
  updateState({ stack: next })
}

function activateViewedMode() {
  if (props.state.view === 'direct') updateState({ active: 'direct' })
  else if (props.state.view === 'route') updateState({ active: 'route', route: props.state.route })
  else updateState({ active: 'stack', route: props.state.stackDefault })
}

// Additive app helpers
const additiveAddedProviders = computed(() => {
  const pinned = props.app.pinned ?? {}
  const names = [...Object.keys(pinned), ...props.state.added.filter((n) => !(n in pinned))]
  return names
    .map((name) => props.providers.find((p) => p.name === name))
    .filter((p): p is CcsProviderItem => Boolean(p))
})

const additiveAvailableProviders = computed(() => {
  const pinned = props.app.pinned ?? {}
  const addedSet = new Set([...Object.keys(pinned), ...props.state.added])
  return props.providers.filter((p) => !addedSet.has(p.name))
})

function additiveAddedAction(p: CcsProviderItem): ProviderCardAction | undefined {
  const pin = props.app.pinned?.[p.name]
  if (pin === 'none') return undefined
  if (pin === 'openTerminal') {
    return {
      key: 'openTerminal',
      label: msg.value.window.action.openTerminal,
      onClick: () => workbench.toast({ text: `${p.name} · Terminal` }),
    }
  }
  const exclusive = props.app.exclusive ?? []
  return {
    key: 'remove',
    label: msg.value.window.action.remove,
    disabledReason: exclusive.includes(p.name) ? msg.value.window.reason.workbuddyOnlyOne : undefined,
    onClick: () => updateState({ added: props.state.added.filter((n) => n !== p.name) }),
  }
}

function addAdditiveProvider(p: CcsProviderItem) {
  const exclusive = props.app.exclusive ?? []
  const isEx = exclusive.includes(p.name)
  const nextAdded = isEx
    ? props.state.added.map((n) => (exclusive.includes(n) ? p.name : n))
    : [p.name, ...props.state.added]
  updateState({ added: nextAdded })
  if (p.isSiteProvider) {
    workbench.toast({
      text: fmt(msg.value.window.guided.step3DoneToast, { app: props.app.label, site: props.siteName }),
    })
    emit('provider-activated', props.app.id)
  }
}
</script>

<style scoped>
.ccs-list-move {
  transition: transform 220ms cubic-bezier(0.23, 1, 0.32, 1);
}

@media (prefers-reduced-motion: reduce) {
  .ccs-list-move {
    transition: none;
  }
}
</style>
