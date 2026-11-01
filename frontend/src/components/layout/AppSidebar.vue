<template>
  <!-- UDashboardSidebar: collapsible to a 4rem rail, resizable, a slideover on phones.
       Root is the sidebar's own fragment (rail + resize handle + drawer). -->
  <UDashboardSidebar
    id="console"
    v-model:open="mobileOpen"
    v-model:collapsed="sidebarCollapsed"
    collapsible
    resizable
    class="console-sidebar bg-(--app-sidebar-bg)"
    :ui="{ body: 'min-h-0 overflow-visible', footer: 'sidebar-footer lg:border-t lg:border-default' }"
  >
    <!-- Header (TeamsMenu in the template): logo, site name and version. -->
    <template #header="{ collapsed }">
      <div class="sidebar-brand" :class="{ 'sidebar-brand--collapsed': collapsed }">
        <router-link :to="homePath" class="sidebar-brand-link" :title="collapsed ? siteName : undefined" @click="closeMobile">
          <span class="sidebar-logo">
            <img v-if="settingsLoaded" :src="siteLogo || '/logo.svg'" alt="Logo" class="size-full object-contain" />
          </span>
          <span v-if="!collapsed" class="sidebar-brand-title">{{ siteName }}</span>
        </router-link>
        <VersionBadge v-if="!collapsed" class="sidebar-version" :version="siteVersion" />
      </div>
    </template>

    <template #default="{ collapsed }">
      <!-- UDashboardSearchButton: opens the UDashboardSearch palette (also Ctrl/⌘ K). -->
      <UDashboardSearchButton
        :collapsed="collapsed"
        tooltip
        class="console-search-trigger bg-transparent ring-default"
      />

      <!-- The links scroll on their own; the bottom group stays in view. -->
      <nav ref="sidebarNavRef" class="sidebar-nav scrollbar-hide" :aria-label="t('common.toggleMenu')">
        <UNavigationMenu
          v-if="mainNavItems.length"
          v-model="expandedGroups"
          :items="toMenuItems(mainNavItems, collapsed)"
          :collapsed="collapsed"
          orientation="vertical"
          tooltip
          :popover="{ mode: 'click' }"
        >
          <!-- Leading icon: the lucide icon, or an admin-uploaded SVG (sanitized). -->
          <template #item-leading="{ item, active, ui }">
            <span
              v-if="item.iconSvg"
              data-slot="linkLeadingIcon"
              class="sidebar-svg-icon"
              :class="ui.linkLeadingIcon({ active })"
              v-html="sanitizeSvg(String(item.iconSvg))"
            ></span>
            <UIcon
              v-else-if="item.icon"
              :name="item.icon"
              data-slot="linkLeadingIcon"
              :class="ui.linkLeadingIcon({ active, disabled: !!item.disabled })"
            />
          </template>

          <!-- Collapsed rail: a group opens its children (with tier headers) beside the rail. -->
          <template #item-content="{ item, close, ui }">
            <ul data-slot="childList" :class="ui.childList({ class: 'console-flyout-list' })">
              <li data-slot="childLabel" :class="ui.childLabel()">{{ item.label }}</li>
              <template v-for="row in flyoutRows(item)" :key="row.key">
                <li v-if="row.section" data-slot="childLabel" :class="ui.childLabel({ class: 'sidebar-flyout-section' })">{{ row.label }}</li>
                <li v-else data-slot="childItem" :class="ui.childItem()">
                  <ULink
                    :to="row.path"
                    :active="row.active"
                    raw
                    data-slot="childLink"
                    :class="ui.childLink({ active: row.active })"
                    @click="onFlyoutSelect(row.path, close)"
                  >
                    <span data-slot="childLinkLabel" :class="ui.childLinkLabel()">{{ row.label }}</span>
                  </ULink>
                </li>
              </template>
            </ul>
          </template>
        </UNavigationMenu>
      </nav>

      <!-- Bottom group: subscription status, docs and model plaza. -->
      <div class="sidebar-secondary">
        <div v-if="showSubscriptions" class="sidebar-subscriptions" :class="{ 'sidebar-subscriptions--collapsed': collapsed }">
          <SubscriptionProgressMini />
          <span v-if="!collapsed" class="sidebar-subscriptions-label" aria-hidden="true">{{ t('nav.mySubscriptions') }}</span>
        </div>
        <UNavigationMenu
          v-if="secondaryNavItems.length"
          :items="secondaryNavItems"
          :collapsed="collapsed"
          orientation="vertical"
          tooltip
        />
      </div>
    </template>

    <!-- Footer: the user menu (account, theme, language, logout). -->
    <template #footer="{ collapsed }">
      <UserMenu v-if="authStore.user" variant="sidebar" :collapsed="collapsed" />
    </template>
  </UDashboardSidebar>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import type { NavigationMenuItem } from '@nuxt/ui'
import { useAdminSettingsStore, useAppStore, useAuthStore, useOnboardingStore, useSubscriptionStore } from '@/stores'
import VersionBadge from '@/components/common/VersionBadge.vue'
import SubscriptionProgressMini from '@/components/common/SubscriptionProgressMini.vue'
import { sanitizeSvg } from '@/utils/sanitize'
import { sanitizeUrl } from '@/utils/url'
import { useBatchImageAccess } from '@/composables/useBatchImageAccess'
import { SIDEBAR_TOUR_ANCHORS, SIDEBAR_TOUR_SELECTORS } from '@/components/Guide/steps'
import UserMenu from './UserMenu.vue'
import { useConsoleNav, type NavGroup, type NavLeaf, type NavNode, type NavSection } from './consoleNav'

const { t } = useI18n()

const route = useRoute()
const appStore = useAppStore()
const authStore = useAuthStore()
const onboardingStore = useOnboardingStore()
const adminSettingsStore = useAdminSettingsStore()
const subscriptionStore = useSubscriptionStore()
const { refreshBatchImageAccess } = useBatchImageAccess()
// The groups and pages (shared with the Ctrl/⌘K palette): components/layout/consoleNav.tsx.
const { sidebarTree, secondary, activeLeaf, homePath } = useConsoleNav()

// The store stays the source of truth (UDashboardGroup persists the collapsed state too).
const sidebarCollapsed = computed({
  get: () => appStore.sidebarCollapsed,
  set: (value: boolean) => appStore.setSidebarCollapsed(value)
})
const mobileOpen = computed({
  get: () => appStore.mobileOpen,
  set: (value: boolean) => appStore.setMobileOpen(value)
})
const isAdmin = computed(() => authStore.isAdmin)
const sidebarNavRef = ref<HTMLElement | null>(null)

// Site settings from appStore (cached, no flicker)
const siteName = computed(() => appStore.siteName)
const siteLogo = computed(() => sanitizeUrl(appStore.siteLogo || '', { allowRelative: true, allowDataUrl: true }))
const siteVersion = computed(() => appStore.siteVersion)
const settingsLoaded = computed(() => appStore.publicSettingsLoaded)
const showSubscriptions = computed(() => !!authStore.user && subscriptionStore.hasActiveSubscriptions)

const mainNavItems = computed(() => sidebarTree.value)

// Expanded groups (UNavigationMenu's accordion v-model, keyed by group). A group with no
// entry follows the automatic behavior (open while it holds the current page, or when it is
// open by default); a chevron click records the user's choice, which wins over the
// automatic state so an active group can still be collapsed manually.
const groupExpandOverrides = ref<Map<string, boolean>>(new Map())

// While the onboarding tour runs, the groups holding its sidebar anchors stay open.
const tourRunning = computed(() => onboardingStore.getDriverInstance() !== null)
const tourPaths = new Set(Object.keys(SIDEBAR_TOUR_ANCHORS))

function groupLeaves(group: NavGroup): NavLeaf[] {
  return group.children.filter((child): child is NavLeaf => child.kind === 'link')
}

function isGroupActive(group: NavGroup): boolean {
  return groupLeaves(group).some((leaf) => leaf.path === activeLeaf.value?.path)
}

function isGroupExpanded(group: NavGroup): boolean {
  if (tourRunning.value && groupLeaves(group).some((leaf) => tourPaths.has(leaf.path))) return true
  const override = groupExpandOverrides.value.get(group.key)
  if (override !== undefined) return override
  return !!group.defaultOpen || isGroupActive(group)
}

const expandedGroups = computed<string[]>({
  get: () => mainNavItems.value.filter((node): node is NavGroup => node.kind === 'group' && isGroupExpanded(node)).map((group) => group.key),
  set: (open) => {
    for (const node of mainNavItems.value) {
      if (node.kind === 'group') groupExpandOverrides.value.set(node.key, open.includes(node.key))
    }
  }
})

// A tour shown on the collapsed rail needs the labelled sidebar.
watch(tourRunning, (running) => {
  if (running && appStore.sidebarCollapsed) appStore.setSidebarCollapsed(false)
})

// Onboarding tour anchors (components/Guide/steps.ts) on the matching links.
function tourAttrs(path: string): Record<string, string> {
  const anchor = SIDEBAR_TOUR_ANCHORS[path]
  return anchor ? { 'data-tour': anchor } : {}
}

function leafItem(leaf: NavLeaf, withIcon: boolean): NavigationMenuItem {
  return {
    label: leaf.label,
    icon: withIcon && !leaf.iconSvg ? leaf.icon : undefined,
    iconSvg: withIcon ? leaf.iconSvg : undefined,
    to: leaf.path,
    target: leaf.external ? '_blank' : undefined,
    value: leaf.path,
    active: leaf.path === activeLeaf.value?.path,
    'aria-label': leaf.label,
    ...tourAttrs(leaf.path),
    onSelect: () => handleMenuItemClick(leaf.path)
  }
}

function sectionItem(child: NavSection): NavigationMenuItem {
  return { type: 'label', label: child.label, value: child.key, class: 'sidebar-child-section' }
}

/** Maps the nav tree onto UNavigationMenu items: groups become triggers, pages links. */
function toMenuItems(nodes: NavNode[], collapsed: boolean): NavigationMenuItem[] {
  return nodes.map((node): NavigationMenuItem => {
    if (node.kind === 'link') return leafItem(node, true)
    return {
      label: node.label,
      icon: node.icon,
      type: 'trigger',
      value: node.key,
      'aria-label': node.label,
      // The group button is highlighted when it hides the current page (rail, or closed).
      active: isGroupActive(node) && (collapsed || !isGroupExpanded(node)),
      children: node.children.map((child): NavigationMenuItem => child.kind === 'section'
        ? sectionItem(child)
        : leafItem(child, false))
    }
  })
}

interface FlyoutRow {
  key: string
  label: string
  path: string
  section: boolean
  active: boolean
}

/** Rows of a group's flyout on the collapsed rail: section headers and child links. */
function flyoutRows(item: NavigationMenuItem): FlyoutRow[] {
  return (item.children ?? []).map((child, index) => ({
    key: String(child.value ?? index),
    label: String(child.label ?? ''),
    path: typeof child.to === 'string' ? child.to : '',
    section: child.type === 'label',
    active: !!child.active
  }))
}

function onFlyoutSelect(path: string, close?: () => void) {
  close?.()
  handleMenuItemClick(path)
}

// Bottom group, like the template's Feedback / Help links: the model plaza and docs.
const secondaryNavItems = computed((): NavigationMenuItem[] => secondary.value.map((leaf) => ({
  ...leafItem(leaf, true),
  active: !leaf.external && route.path === (leaf.match ?? leaf.path)
})))

function closeMobile() {
  appStore.setMobileOpen(false)
}

function handleMenuItemClick(itemPath: string) {
  // The next page mounts a fresh layout, so close the phone drawer right away.
  closeMobile()

  const selector = SIDEBAR_TOUR_SELECTORS[itemPath]
  if (selector && onboardingStore.isCurrentStep(selector)) {
    onboardingStore.nextStep(500)
  }
}

// Fetch admin settings (for feature-gated nav items like Ops).
watch(
  () => isAdmin.value && !authStore.isAdminObserver,
  (v) => {
    if (v) {
      adminSettingsStore.fetch()
    }
  },
  { immediate: true }
)

onMounted(() => {
  void refreshBatchImageAccess()
  if (isAdmin.value && !authStore.isAdminObserver) {
    adminSettingsStore.fetch()
  }
  // Restore sidebar scroll position after route change re-mounts the component
  if (appStore.sidebarScrollTop > 0 && sidebarNavRef.value) {
    void nextTick(() => {
      if (sidebarNavRef.value) {
        sidebarNavRef.value.scrollTop = appStore.sidebarScrollTop
      }
    })
  } else {
    // First visit: bring the current page's link into view (long admin lists).
    void nextTick(() => revealActiveLink(sidebarNavRef.value))
  }
})

// The phone drawer mounts its own copy of the list each time it opens.
watch(sidebarNavRef, (nav) => {
  if (nav?.closest('[role="dialog"]')) void nextTick(() => revealActiveLink(nav))
})

function revealActiveLink(nav: HTMLElement | null) {
  const link = nav?.querySelector<HTMLElement>('[aria-current="page"]')
  if (!nav || !link) return
  const top = link.getBoundingClientRect().top - nav.getBoundingClientRect().top
  if (top + link.offsetHeight > nav.clientHeight) nav.scrollTop += top - nav.clientHeight / 2
}

onBeforeUnmount(() => {
  if (sidebarNavRef.value) {
    appStore.sidebarScrollTop = sidebarNavRef.value.scrollTop
  }
})
</script>

<style>
/* Brand row in the sidebar header (where the template has TeamsMenu). */
.sidebar-brand {
  display: flex;
  min-width: 0;
  flex: 1 1 auto;
  align-items: center;
  gap: 8px;
}

.sidebar-brand--collapsed {
  justify-content: center;
}

.sidebar-brand-link {
  display: flex;
  min-width: 0;
  flex: 1 1 auto;
  align-items: center;
  gap: 8px;
  padding: 6px;
  border-radius: calc(var(--ui-radius) * 1.5);
  color: var(--ui-text-highlighted);
  transition: background-color 150ms ease;
}

.sidebar-brand--collapsed .sidebar-brand-link {
  flex: 0 0 auto;
}

.sidebar-brand-link:hover {
  background: color-mix(in oklab, var(--ui-bg-elevated) 50%, transparent);
}

.sidebar-brand-link:focus-visible {
  outline: 2px solid var(--ui-border-inverted);
  outline-offset: -2px;
}

.sidebar-logo {
  display: flex;
  width: 20px;
  height: 20px;
  flex: 0 0 20px;
  overflow: hidden;
  border-radius: var(--ui-radius);
}

.sidebar-brand-title {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 14px;
  line-height: 20px;
  font-weight: 600;
}

/* VersionBadge: a small neutral tag after the name (its amber update state stays). */
.sidebar-version {
  flex-shrink: 0;
}

/* Read-only users see the plain version text; a long one gives way to the site name. */
.sidebar-version:has(> span) {
  min-width: 0;
  flex-shrink: 1;
  max-width: 45%;
}

.sidebar-version > span {
  display: block;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.sidebar-version > button {
  gap: 4px;
  padding: 1px 6px;
  border-radius: var(--ui-radius);
  font-size: 11px;
  line-height: 18px;
}

html:not(.dark) .sidebar-version > button:not([class*='bg-amber']) {
  background: var(--ui-bg-elevated);
  color: var(--ui-text-muted);
}

/* The link list scrolls by itself; the search button and the bottom group stay put. */
.sidebar-nav {
  min-height: 0;
  flex: 1 1 auto;
  margin-inline: -16px;
  padding-inline: 16px;
  overflow-y: auto;
}

.sidebar-section-label {
  padding-top: 14px;
}

.sidebar-child-section,
.sidebar-flyout-section {
  padding-top: 8px;
  color: var(--ui-text-muted);
  font-weight: 500;
}

/* Custom SVG icon in sidebar: constrain size without overriding uploaded SVG colors */
.sidebar-svg-icon {
  color: currentColor;
  display: block;
}

.sidebar-svg-icon > svg {
  display: block;
  width: 100%;
  height: 100%;
}

.sidebar-secondary {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

/* Subscription status (SubscriptionProgressMini) drawn as a nav row: icon, label, dots. */
.sidebar-subscriptions {
  position: relative;
}

.sidebar-subscriptions > div > button {
  display: flex;
  width: 100%;
  min-height: 32px;
  align-items: center;
  justify-content: space-between;
  gap: 6px;
  padding: 6px 10px;
  border-radius: calc(var(--ui-radius) * 1.5);
  background: transparent;
  color: var(--ui-text-muted);
}

.sidebar-subscriptions > div > button:hover {
  background: color-mix(in oklab, var(--ui-bg-elevated) 50%, transparent);
  color: var(--ui-text-highlighted);
}

.sidebar-subscriptions > div > button > svg {
  width: 20px;
  height: 20px;
  color: var(--ui-text-dimmed);
}

.sidebar-subscriptions > div > button span {
  color: var(--ui-text-muted);
}

.sidebar-subscriptions-label {
  position: absolute;
  top: 50%;
  left: 36px;
  translate: 0 -50%;
  overflow: hidden;
  max-width: calc(100% - 96px);
  text-overflow: ellipsis;
  white-space: nowrap;
  pointer-events: none;
  color: var(--ui-text-muted);
  font-size: 14px;
  font-weight: 500;
}

.sidebar-subscriptions > div > div {
  top: auto;
  right: auto;
  bottom: 100%;
  left: 0;
  margin: 0 0 8px;
}

.sidebar-subscriptions--collapsed > div > button {
  justify-content: center;
  padding-inline: 6px;
}

.sidebar-subscriptions--collapsed > div > button > div {
  display: none;
}

.sidebar-subscriptions--collapsed > div > div {
  bottom: 0;
  left: 100%;
  margin: 0 0 0 10px;
}

.console-flyout-list {
  max-height: min(70vh, 560px);
  overflow-y: auto;
}
</style>
