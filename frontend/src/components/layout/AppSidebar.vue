<template>
  <!-- UDashboardSidebar: a fixed 16rem desktop rail (the main sidebar's reading width),
       collapsible to a 4rem rail, and a slideover on phones. -->
  <UDashboardSidebar
    id="console"
    v-model:open="mobileOpen"
    v-model:collapsed="sidebarCollapsed"
    collapsible
    class="console-sidebar bg-(--app-sidebar-bg)"
    :ui="{
      body: 'min-h-0 overflow-visible',
      footer: 'sidebar-footer lg:border-t lg:border-default',
      content: 'console-sidebar-drawer w-[86vw] max-w-[20rem] sm:w-80 sm:max-w-80'
    }"
  >
    <!-- Header (TeamsMenu in the template): logo, site name and version. -->
    <template #header="{ collapsed }">
      <div class="sidebar-brand" :class="{ 'sidebar-brand--collapsed': collapsed }">
        <router-link :to="homePath" class="sidebar-brand-link" :title="siteName" @click="closeMobile">
          <span class="sidebar-logo">
            <img v-if="settingsLoaded" :src="siteLogo || '/logo.svg'" alt="Logo" class="size-full object-contain" />
          </span>
        </router-link>
        <div v-if="!collapsed" class="sidebar-brand-meta">
          <router-link :to="homePath" class="sidebar-brand-title-link" :title="siteName" @click="closeMobile">
            <span class="sidebar-brand-title">{{ siteName }}</span>
          </router-link>
          <VersionBadge class="sidebar-version" :version="siteVersion" />
        </div>
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
import { useTicketNotificationStore, type TicketScope } from '@/stores/ticketNotifications'
import VersionBadge from '@/components/common/VersionBadge.vue'
import SubscriptionProgressMini from '@/components/common/SubscriptionProgressMini.vue'
import { sanitizeSvg } from '@/utils/sanitize'
import { sanitizeUrl } from '@/utils/url'
import { isExternalDocsUrl, resolveDocsUrl } from '@/utils/docsUrl'
import { FeatureFlags, isFeatureFlagEnabled, makeSidebarFlag } from '@/utils/featureFlags'
import { useBatchImageAccess } from '@/composables/useBatchImageAccess'
import { SIDEBAR_TOUR_ANCHORS, SIDEBAR_TOUR_SELECTORS } from '@/components/Guide/steps'
import UserMenu from './UserMenu.vue'
import { applyFeatureFlags, isOperationsPageActive, useOperationsNavigation } from '@/components/admin/operations/opsNavigation'

interface NavItem {
  path: string
  label: string
  /** Iconify name (lucide set, bundled at build time). */
  icon?: string
  /** Admin-uploaded custom menu icon, sanitized before rendering. */
  iconSvg?: string
  hideInSimpleMode?: boolean
  children?: NavItem[]
  active?: boolean
  /**
   * Section header rendered inside an expanded group. The item does not
   * navigate; `path` is only a stable key.
   */
  section?: boolean
  /**
   * When true, the parent item only toggles the expand/collapse state and
   * does NOT navigate to its `path`. The `path` is purely a stable key.
   */
  expandOnly?: boolean
  /**
   * 可选的功能开关 getter。返回 false 时菜单项被隐藏；返回 undefined/true 时显示。
   * 宽容策略（undefined → 显示）避免 public settings 未加载完成时菜单闪烁消失。
   * Getter 里访问的 reactive 来源（store / composable）会被 computed 自动追踪，
   * 开关切换时菜单自动更新。
   */
  featureFlag?: () => boolean | undefined
}

const { t, locale } = useI18n()

const route = useRoute()
const appStore = useAppStore()
const authStore = useAuthStore()
const onboardingStore = useOnboardingStore()
const adminSettingsStore = useAdminSettingsStore()
const subscriptionStore = useSubscriptionStore()
const ticketStore = useTicketNotificationStore()
const { canUseBatchImage, refreshBatchImageAccess } = useBatchImageAccess()

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

const homePath = computed(() => authStore.isAdminObserver ? '/admin/usage' : isAdmin.value ? '/admin/dashboard' : '/dashboard')

// Site settings from appStore (cached, no flicker)
const siteName = computed(() => appStore.siteName)
const siteLogo = computed(() => sanitizeUrl(appStore.siteLogo || '', { allowRelative: true, allowDataUrl: true }))
const siteVersion = computed(() => appStore.siteVersion)
const settingsLoaded = computed(() => appStore.publicSettingsLoaded)
const showSubscriptions = computed(() => !!authStore.user && subscriptionStore.hasActiveSubscriptions)

// Public-settings flags go through the registry in utils/featureFlags.ts,
// which handles the opt-in vs opt-out fallback when settings haven't loaded
// yet. Admin-only flags (not in public settings) stay inline below.
const flagChannelMonitor = makeSidebarFlag(FeatureFlags.channelMonitor)
const flagPayment = makeSidebarFlag(FeatureFlags.payment)
const flagCrypto = makeSidebarFlag(FeatureFlags.crypto)
const flagAvailableChannels = makeSidebarFlag(FeatureFlags.availableChannels)
const flagAffiliate = makeSidebarFlag(FeatureFlags.affiliate)
const flagRiskControl = makeSidebarFlag(FeatureFlags.riskControl)
const flagPluginManagement = makeSidebarFlag(FeatureFlags.pluginManagement)
const operationsNavigation = useOperationsNavigation()
const flagAdminPayment = () => adminSettingsStore.paymentEnabled
const flagBatchImageAccess = () => canUseBatchImage.value

// This custom menu is embedded into /purchase and should not appear as a second sidebar entry.
const RECHARGE_CENTER_MENU_ID = '322273f5aaa4d036'

// buildSelfNavItems 构造用户自己的导航项（用户端主菜单和管理员的"我的账户"子菜单共享这组声明）。
// withDashboard=true 时包含仪表盘（用户端），false 时不含（管理员的个人区已经有独立仪表盘入口）。
//
// 条目顺序：密钥 → 用量 → 可用渠道 → 渠道状态 → 支付 → 兑换/资料。
// 可用渠道紧挨渠道状态之上，让用户"先看自己能用什么、再看对应状态"。
function buildSelfNavItems(withDashboard: boolean): NavItem[] {
  const items: NavItem[] = []
  if (withDashboard) {
    items.push({ path: '/dashboard', label: t('nav.dashboard'), icon: 'i-lucide-layout-dashboard' })
  }
  items.push(
    { path: '/keys', label: t('nav.apiKeys'), icon: 'i-lucide-key-round' },
    { path: '/infinite-canvas', label: t('nav.infiniteCanvas'), icon: 'i-lucide-frame' },
    { path: '/batch-image', label: t('nav.batchImage'), icon: 'i-lucide-images', hideInSimpleMode: true, featureFlag: flagBatchImageAccess },
    { path: '/usage', label: t('nav.usage'), icon: 'i-lucide-chart-column', hideInSimpleMode: true },
    { path: '/available-channels', label: t('nav.availableChannels'), icon: 'i-lucide-layers', hideInSimpleMode: true, featureFlag: flagAvailableChannels },
    { path: '/monitor', label: t('nav.channelStatus'), icon: 'i-lucide-activity', featureFlag: flagChannelMonitor },
    { path: '/deposit', label: t('nav.cryptoDeposit'), icon: 'i-lucide-wallet', featureFlag: flagCrypto },
    { path: '/purchase', label: t('nav.buySubscription'), icon: 'i-lucide-circle-dollar-sign', hideInSimpleMode: true, featureFlag: flagPayment },
    { path: '/orders', label: t('nav.myOrders'), icon: 'i-lucide-receipt-text', hideInSimpleMode: true, featureFlag: flagPayment },
    { path: '/redeem', label: t('nav.redeem'), icon: 'i-lucide-gift', hideInSimpleMode: true },
    { path: '/affiliate', label: t('nav.affiliate'), icon: 'i-lucide-user-plus', hideInSimpleMode: true, featureFlag: flagAffiliate },
    { path: '/tickets', label: t('tickets.title'), icon: 'i-lucide-message-circle' },
    { path: '/profile', label: t('nav.profile'), icon: 'i-lucide-user' },
    { path: '/security', label: locale.value === 'zh' ? '安全记录' : 'Security records', icon: 'i-lucide-shield-check' },
    ...customMenuItemsForUser.value.map((item): NavItem => ({
      path: `/custom/${item.id}`,
      label: item.label,
      icon: 'i-lucide-file-text',
      iconSvg: item.icon_svg,
    })),
  )
  return items
}

// finalizeNav 合并三重过滤：featureFlag 过滤 + simple 模式过滤。
function finalizeNav(items: NavItem[]): NavItem[] {
  const visible = applyFeatureFlags(items)
  return authStore.isSimpleMode ? visible.filter(item => !item.hideInSimpleMode) : visible
}

// User navigation items (for regular users)
const userNavItems = computed((): NavItem[] => finalizeNav(buildSelfNavItems(true)))

// Personal navigation items (for admin's "My Account" section, without Dashboard).
// Admins access 可用渠道 from this section just like regular users — there is no
// separate admin entry, since the page is purely a user-facing view.
const personalNavItems = computed((): NavItem[] => finalizeNav(buildSelfNavItems(false)))

// Custom menu items filtered by visibility
const customMenuItemsForUser = computed(() => {
  const items = appStore.cachedPublicSettings?.custom_menu_items ?? []
  return items
    .filter((item) => item.visibility === 'user' && item.id !== RECHARGE_CENTER_MENU_ID)
    .sort((a, b) => a.sort_order - b.sort_order)
})

const customMenuItemsForAdmin = computed(() => {
  if (authStore.isAdminObserver) return []
  return adminSettingsStore.customMenuItems
    .filter((item) => item.visibility === 'admin')
    .sort((a, b) => a.sort_order - b.sort_order)
})

// Admin navigation items
const adminNavItems = computed((): NavItem[] => {
  if (authStore.isAdminObserver) {
    return [{ path: '/admin/usage', label: t('nav.usage'), icon: 'i-lucide-chart-column' }]
  }
  const baseItems: NavItem[] = [
    { path: '/admin/dashboard', label: t('nav.dashboard'), icon: 'i-lucide-layout-dashboard' },
    {
      path: '/admin/ops-center',
      label: t('opsCenter.title'),
      icon: 'i-lucide-gauge',
      expandOnly: true,
      children: operationsNavigation.value.map(area => ({
        path: area.items[0].path,
        label: t(area.labelKey),
        active: area.items.some(item => isOperationsPageActive(item.path, route.path)),
      })),
    },
    { path: '/admin/users', label: t('nav.users'), icon: 'i-lucide-users', hideInSimpleMode: true },
    { path: '/admin/groups', label: t('nav.groups'), icon: 'i-lucide-folder' },
    { path: '/admin/subscriptions', label: t('nav.subscriptions'), icon: 'i-lucide-credit-card', hideInSimpleMode: true },
    { path: '/admin/accounts', label: t('nav.accounts'), icon: 'i-lucide-globe' },
    { path: '/admin/plugins', label: t('nav.plugins'), icon: 'i-lucide-box', featureFlag: flagPluginManagement },
    { path: '/admin/announcements', label: t('nav.announcements'), icon: 'i-lucide-megaphone' },
    { path: '/admin/tickets', label: t('tickets.adminTitle'), icon: 'i-lucide-message-circle' },
    { path: '/admin/proxies', label: t('nav.proxies'), icon: 'i-lucide-server' },
    {
      path: '/admin/security-audit',
      label: t('nav.securityAudit'),
      icon: 'i-lucide-shield',
      expandOnly: true,
      featureFlag: flagRiskControl,
      children: [
        { path: '/admin/risk-control', label: t('nav.contentModeration') },
        { path: '/admin/ip-risk', label: locale.value === 'zh' ? 'IP风控' : 'IP policies' },
        { path: '/admin/ip-audit', label: locale.value === 'zh' ? 'IP审计' : 'IP audit' },
        { path: '/admin/prompt-audit', label: t('nav.promptAudit') },
        { path: '/admin/security-penalties', label: locale.value === 'zh' ? '分层安全限制' : 'Layered API restrictions' },
      ],
    },
    { path: '/admin/redeem', label: t('nav.redeemCodes'), icon: 'i-lucide-ticket', hideInSimpleMode: true },
    { path: '/admin/crypto', label: t('nav.cryptoDeposits'), icon: 'i-lucide-wallet', hideInSimpleMode: true },
    { path: '/admin/promo-codes', label: t('nav.promoCodes'), icon: 'i-lucide-badge-percent', hideInSimpleMode: true },
    {
      path: '/admin/affiliates',
      label: t('nav.affiliateManagement'),
      icon: 'i-lucide-user-plus',
      hideInSimpleMode: true,
      expandOnly: true,
      featureFlag: flagAffiliate,
      children: [
        { path: '/admin/affiliates/invites', label: t('nav.affiliateInviteRecords') },
        { path: '/admin/affiliates/rebates', label: t('nav.affiliateRebateRecords') },
        { path: '/admin/affiliates/transfers', label: t('nav.affiliateTransferRecords') },
      ],
    },
    {
      path: '/admin/orders',
      label: t('nav.orderManagement'),
      icon: 'i-lucide-clipboard-list',
      hideInSimpleMode: true,
      expandOnly: true,
      featureFlag: flagAdminPayment,
      children: [
        { path: '/admin/orders/dashboard', label: t('nav.paymentDashboard') },
        { path: '/admin/orders', label: t('nav.orderManagement') },
        { path: '/admin/orders/plans', label: t('nav.paymentPlans') },
      ],
    },
    { path: '/admin/usage', label: t('nav.usage'), icon: 'i-lucide-chart-column' },
    { path: '/admin/audit-logs', label: t('nav.auditLogs'), icon: 'i-lucide-scroll-text', hideInSimpleMode: true }
  ]

  const visible = applyFeatureFlags(baseItems)

  // 简单模式下，在系统设置前插入 API密钥
  if (authStore.isSimpleMode) {
    const filtered = visible.filter(item => !item.hideInSimpleMode)
    filtered.push({ path: '/keys', label: t('nav.apiKeys'), icon: 'i-lucide-key-round' })
    filtered.push({ path: '/admin/settings', label: t('nav.settings'), icon: 'i-lucide-settings' })
    for (const cm of customMenuItemsForAdmin.value) {
      filtered.push({ path: `/custom/${cm.id}`, label: cm.label, icon: 'i-lucide-file-text', iconSvg: cm.icon_svg })
    }
    return filtered
  }

  visible.push({ path: '/admin/settings', label: t('nav.settings'), icon: 'i-lucide-settings' })
  for (const cm of customMenuItemsForAdmin.value) {
    visible.push({ path: `/custom/${cm.id}`, label: cm.label, icon: 'i-lucide-file-text', iconSvg: cm.icon_svg })
  }
  return visible
})

/** Admins: admin menu, then "My account" (hidden in simple mode). Users: their own menu. */
const mainNavItems = computed((): NavItem[] => {
  if (isAdmin.value) {
    if (authStore.isSimpleMode) return adminNavItems.value
    return [
      ...adminNavItems.value,
      { path: '#my-account', label: t('nav.myAccount'), section: true },
      ...personalNavItems.value
    ]
  }
  return appStore.backendModeEnabled ? [] : userNavItems.value
})

// Expanded groups (UNavigationMenu's accordion v-model, keyed by group path). A group with
// no entry follows the automatic behavior (expanded while the active route is one of its
// children); a chevron click records the user's choice, which wins over the automatic
// state so an active group can still be collapsed manually.
const groupExpandOverrides = ref<Map<string, boolean>>(new Map())

function isGroupExpanded(item: NavItem): boolean {
  const override = groupExpandOverrides.value.get(item.path)
  if (override !== undefined) return override
  return isGroupActive(item)
}

const expandedGroups = computed<string[]>({
  get: () => mainNavItems.value.filter((item) => item.children?.length && isGroupExpanded(item)).map((item) => item.path),
  set: (open) => {
    for (const item of mainNavItems.value) {
      if (item.children?.length) groupExpandOverrides.value.set(item.path, open.includes(item.path))
    }
  }
})

function isActive(path: string): boolean {
  return route.path === path || route.path.startsWith(path + '/')
}

function isGroupActive(item: NavItem): boolean {
  if (!item.children) return false
  return item.children.some(child => !child.section && (child.active ?? isActive(child.path)))
}

/**
 * Counts come from the existing ticket stats endpoints (authenticated, counts
 * only). A failed fetch keeps the last number and shows a warning badge, so the
 * failure is visible instead of being reported as zero.
 */
function ticketBadge(path: string): NavigationMenuItem['badge'] | undefined {
  const scope: TicketScope | null = path === '/admin/tickets' ? 'admin' : path === '/tickets' ? 'user' : null
  if (!scope) return undefined
  if (ticketStore.errorFor(scope)) {
    return { label: '!', color: 'warning', variant: 'subtle', size: 'sm' }
  }
  const unread = ticketStore.unreadFor(scope)
  if (!unread) return undefined
  return { label: unread > 99 ? '99+' : unread, color: 'neutral', variant: 'subtle', size: 'sm' }
}

// Onboarding tour anchors (components/Guide/steps.ts) on the matching links.
function tourAttrs(path: string): Record<string, string> {
  const anchor = SIDEBAR_TOUR_ANCHORS[path]
  return anchor ? { 'data-tour': anchor } : {}
}

/** Maps our nav declarations onto UNavigationMenu items (the declarations stay the source). */
function toMenuItems(items: NavItem[], collapsed: boolean): NavigationMenuItem[] {
  return items.map((item): NavigationMenuItem => {
    if (item.section) {
      return { type: 'label', label: item.label, value: item.path, class: 'sidebar-section-label' }
    }
    if (item.children?.length) {
      const active = isGroupActive(item)
      return {
        label: item.label,
        icon: item.icon,
        type: 'trigger',
        value: item.path,
        'aria-label': item.label,
        // The group button is highlighted when it hides the current page (rail, or closed).
        active: active && (collapsed || !isGroupExpanded(item)),
        children: item.children.map((child): NavigationMenuItem => child.section
          ? { type: 'label', label: child.label, value: child.path, class: 'sidebar-child-section' }
          : {
              label: child.label,
              to: child.path,
              value: child.path,
              active: child.active ?? isActive(child.path),
              badge: ticketBadge(child.path),
              onSelect: () => handleMenuItemClick(child.path)
            })
      }
    }
    return {
      label: item.label,
      icon: item.iconSvg ? undefined : item.icon,
      iconSvg: item.iconSvg,
      to: item.path,
      value: item.path,
      active: isActive(item.path),
      badge: ticketBadge(item.path),
      'aria-label': item.label,
      ...tourAttrs(item.path),
      onSelect: () => handleMenuItemClick(item.path)
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

/** Rows of a group's flyout on the collapsed rail: tier headers and child links. */
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

// Bottom group, like the template's Feedback / Help links: docs and the model plaza.
const docUrl = computed(() => resolveDocsUrl(appStore.docUrl))
const modelPlazaEnabled = computed(() => isFeatureFlagEnabled(FeatureFlags.modelPlaza))
const secondaryNavItems = computed((): NavigationMenuItem[] => {
  const items: NavigationMenuItem[] = []
  if (authStore.user && modelPlazaEnabled.value) {
    items.push({
      label: t('nav.modelPlaza'),
      icon: 'i-lucide-layout-grid',
      to: { path: '/model-plaza', query: { embedded: '1' } },
      active: route.path === '/model-plaza',
      onSelect: closeMobile,
      'aria-label': t('nav.modelPlaza')
    })
  }
  if (docUrl.value) {
    const external = isExternalDocsUrl(docUrl.value)
    items.push({
      label: t('nav.docs'),
      icon: 'i-lucide-book-open',
      to: docUrl.value,
      ...(external ? { target: '_blank' as const } : { onSelect: closeMobile }),
      'aria-label': t('nav.docs')
    })
  }
  return items
})

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
/* Brand lockup in the sidebar header: fixed logo on the left, stacked brand title + compact
   version metadata on the right so long version labels never squeeze or clip the site name. */
.sidebar-brand {
  display: flex;
  min-width: 0;
  flex: 1 1 auto;
  align-items: center;
  gap: 10px;
}

.sidebar-brand--collapsed {
  justify-content: center;
}

.sidebar-brand-link {
  display: inline-flex;
  flex: 0 0 auto;
  align-items: center;
  justify-content: center;
  padding: 2px;
  border-radius: calc(var(--ui-radius) * 1.5);
  color: var(--ui-text-highlighted);
  transition: transform 140ms var(--ease-out, cubic-bezier(0.23, 1, 0.32, 1)), background-color 150ms ease;
}

.sidebar-brand-link:active {
  transform: scale(0.96);
}

@media (hover: hover) and (pointer: fine) {
  .sidebar-brand-link:hover {
    background: color-mix(in oklab, var(--ui-bg-elevated) 55%, transparent);
  }
}

.sidebar-brand-link:focus-visible,
.sidebar-brand-title-link:focus-visible {
  outline: 2px solid var(--ui-border-inverted);
  outline-offset: 1px;
}

.sidebar-brand-meta {
  display: flex;
  min-width: 0;
  flex: 1 1 auto;
  flex-direction: column;
  align-items: flex-start;
  gap: 1px;
}

.sidebar-brand-title-link {
  display: block;
  max-width: 100%;
  border-radius: 4px;
  color: var(--ui-text-highlighted);
  transition: color 150ms ease;
}

.sidebar-logo {
  display: flex;
  width: 28px;
  height: 28px;
  flex: 0 0 28px;
  overflow: hidden;
  border-radius: calc(var(--ui-radius) * 1.5);
  border: 1px solid var(--ui-border-muted);
  background: var(--ui-bg);
}

.sidebar-brand-title {
  display: block;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 14px;
  line-height: 18px;
  font-weight: 600;
  letter-spacing: -0.015em;
}

/* VersionBadge: quiet metadata line tucked under the site name (amber update state stays). */
.sidebar-version {
  max-width: 100%;
  min-width: 0;
}

.sidebar-version > button {
  gap: 4px;
  max-width: 100%;
  padding: 0 5px;
  border-radius: var(--ui-radius);
  font-size: 11px;
  line-height: 16px;
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

/* Section headers read as quiet labels, never as links. */
.sidebar-section-label {
  padding-top: 14px;
  color: var(--ui-text-muted);
  font-size: 11px;
  font-weight: 600;
  letter-spacing: 0.04em;
}

.sidebar-child-section,
.sidebar-flyout-section {
  padding-top: 10px;
  padding-bottom: 2px;
  color: var(--ui-text-dimmed);
  font-size: 11px;
  font-weight: 500;
  letter-spacing: 0.02em;
  pointer-events: none;
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

/* Desktop width: UDashboardSidebar is resizable with a 15rem default and a 10rem
   minimum, and the stored size survives reloads - that is why the console sidebar
   could read as a narrow strip. Pin the expanded rail to the main sidebar's
   reading width (16rem = 256px) and let collapse alone change it.
   Unlayered CSS beats the component's `w-(--width)` utility, so this wins. */
@media (min-width: 1024px) {
  .console-sidebar:not([data-collapsed='true']) {
    width: 16rem;
  }
}

/* Nav density & Studio quiet active pill (aiforui / Antigravity style):
   clean neutral active surface + hairline edge + crisp highlighted text + primary leading icon. */
.console-sidebar:not([data-collapsed='true']) [data-slot='link'] {
  min-height: 2.375rem;
  gap: 0.7rem;
  padding: 0.5rem 0.75rem;
  border-radius: 0.625rem;
  font-size: 0.875rem;
  font-weight: 500;
  transition: background-color 140ms cubic-bezier(0.23, 1, 0.32, 1), color 140ms cubic-bezier(0.23, 1, 0.32, 1), transform 120ms cubic-bezier(0.23, 1, 0.32, 1);
}

.console-sidebar [data-slot='link']:active,
.console-sidebar [data-slot='childLink']:active {
  transform: scale(0.985);
}

.console-sidebar [data-slot='link'][aria-current='page'],
.console-sidebar [data-slot='link'][data-active] {
  background-color: var(--signal-nav-active, var(--ui-bg-elevated)) !important;
  color: var(--signal-nav-active-text, var(--ui-text-highlighted)) !important;
  box-shadow: inset 0 0 0 1px var(--ui-border-muted);
}

html.dark .console-sidebar [data-slot='link'][aria-current='page'],
html.dark .console-sidebar [data-slot='link'][data-active] {
  background-color: #22252e !important;
  color: #f4f6fa !important;
  box-shadow: inset 0 1px 0 rgb(255 255 255 / 0.06), inset 0 0 0 1px rgb(255 255 255 / 0.05);
}

.console-sidebar [data-slot='link'][aria-current='page']::before,
.console-sidebar [data-slot='link'][data-active]::before {
  background-color: transparent !important;
}

.console-sidebar [data-slot='link'][aria-current='page'] [data-slot='linkLeadingIcon'],
.console-sidebar [data-slot='link'][data-active] [data-slot='linkLeadingIcon'] {
  color: var(--ui-primary) !important;
}

.console-sidebar [data-slot='childLink'][aria-current='page'] {
  background-color: var(--signal-nav-active, var(--ui-bg-elevated)) !important;
  color: var(--signal-nav-active-text, var(--ui-text-highlighted)) !important;
  font-weight: 500;
}

.console-sidebar:not([data-collapsed='true']) [data-slot='childLink'] {
  gap: 0.5rem;
  padding: 0.45rem 0.75rem;
  border-radius: 0.5rem;
  font-size: 0.875rem;
}

/* Touch: finger-sized rows in the phone drawer (40px is a mouse target). */
@media (pointer: coarse) {
  .console-sidebar [data-slot='link'],
  .console-sidebar [data-slot='childLink'] {
    min-height: 2.75rem;
  }
}

.console-sidebar [data-slot='label'] {
  padding-inline: 0.75rem;
}

/* Phone / tablet drawer. Nuxt UI leaves the slideover at w-full below sm, so the drawer
   covered the whole viewport; cap it and leave a tappable strip of the page behind. */
.console-sidebar-drawer {
  padding-top: env(safe-area-inset-top, 0px);
  padding-bottom: env(safe-area-inset-bottom, 0px);
  padding-left: env(safe-area-inset-left, 0px);
}

/* Touch screens: nav rows and the footer account button get a 44px target. */
@media (pointer: coarse) {
  .console-sidebar-drawer [data-slot='body'] :is(a, button) {
    min-height: 44px;
  }

  .console-sidebar-drawer [data-slot='header'] button {
    min-width: 40px;
    min-height: 40px;
  }
}
</style>
