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
import { resolveDocsUrl } from '@/utils/docsUrl'
import { FeatureFlags, isFeatureFlagEnabled, makeSidebarFlag } from '@/utils/featureFlags'
import { useBatchImageAccess } from '@/composables/useBatchImageAccess'
import { SIDEBAR_TOUR_ANCHORS, SIDEBAR_TOUR_SELECTORS } from '@/components/Guide/steps'
import UserMenu from './UserMenu.vue'

interface NavItem {
  path: string
  label: string
  /** Iconify name (lucide set, bundled at build time). */
  icon?: string
  /** Admin-uploaded custom menu icon, sanitized before rendering. */
  iconSvg?: string
  hideInSimpleMode?: boolean
  children?: NavItem[]
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

// applyFeatureFlags 递归过滤掉 featureFlag() === false 的节点（含子节点）。
// 使用 `!== false` 宽容语义：undefined（设置未加载）或 true 都视为显示。
function applyFeatureFlags(items: NavItem[]): NavItem[] {
  const out: NavItem[] = []
  for (const item of items) {
    if (item.featureFlag && item.featureFlag() === false) continue
    if (item.children) {
      out.push({ ...item, children: applyFeatureFlags(item.children) })
    } else {
      out.push(item)
    }
  }
  return out
}

const { t, locale } = useI18n()

const route = useRoute()
const appStore = useAppStore()
const authStore = useAuthStore()
const onboardingStore = useOnboardingStore()
const adminSettingsStore = useAdminSettingsStore()
const subscriptionStore = useSubscriptionStore()
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
const flagOpsMonitoring = () => adminSettingsStore.opsMonitoringEnabled
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
    { path: '/deposit', label: t('nav.cryptoDeposit'), icon: 'i-lucide-wallet', featureFlag: flagCrypto },
    { path: '/batch-image', label: t('nav.batchImage'), icon: 'i-lucide-images', hideInSimpleMode: true, featureFlag: flagBatchImageAccess },
    { path: '/usage', label: t('nav.usage'), icon: 'i-lucide-chart-column', hideInSimpleMode: true },
    { path: '/available-channels', label: t('nav.availableChannels'), icon: 'i-lucide-layers', hideInSimpleMode: true, featureFlag: flagAvailableChannels },
    { path: '/monitor', label: t('nav.channelStatus'), icon: 'i-lucide-activity', featureFlag: flagChannelMonitor },
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
      label: locale.value === 'zh' ? '运营中心' : 'Operations center',
      icon: 'i-lucide-gauge',
      expandOnly: true,
      children: [
        { path: '/admin/ops-center', label: locale.value === 'zh' ? '运营总览' : 'Operations overview' },
        { path: '#ops-tier-accounts', label: locale.value === 'zh' ? '账号运营' : 'Account operations', section: true },
        { path: '/admin/account-ops', label: locale.value === 'zh' ? '账号通知' : 'Account notifications' },
        { path: '/admin/ops-automation', label: locale.value === 'zh' ? '账号自动化' : 'Account automation' },
        { path: '/admin/credential-guard', label: locale.value === 'zh' ? '凭证巡检' : 'Credential inspection' },
        { path: '#ops-tier-channels', label: locale.value === 'zh' ? '渠道运营' : 'Channel operations', section: true },
        { path: '/admin/channel-operations', label: locale.value === 'zh' ? '渠道运维' : 'Channel operations' },
        { path: '/admin/model-comparison', label: locale.value === 'zh' ? '模型比价' : 'Model comparison' },
        { path: '/admin/mihomo', label: locale.value === 'zh' ? '代理出口' : 'Managed proxies' },
        { path: '#ops-tier-diagnostics', label: locale.value === 'zh' ? '监控诊断' : 'Monitoring and diagnostics', section: true },
        { path: '/admin/ops', label: locale.value === 'zh' ? '渠道状态' : 'Channel status', featureFlag: flagOpsMonitoring },
        { path: '/admin/ops?alertRules=1', label: locale.value === 'zh' ? '告警阈值' : 'Alert thresholds', featureFlag: flagOpsMonitoring },
        { path: '/admin/connections', label: locale.value === 'zh' ? '连接诊断' : 'Connections' },
        { path: '/admin/group-cache-tests', label: locale.value === 'zh' ? '分组缓存测试' : 'Cache tests' },
        { path: '/admin/request-captures', label: locale.value === 'zh' ? '请求采集' : 'Request capture' },
        { path: '/admin/traffic', label: locale.value === 'zh' ? '用户流量' : 'User traffic' },
        { path: '#ops-tier-platform', label: locale.value === 'zh' ? '平台工具' : 'Platform tools', section: true },
        { path: '/admin/excel-grants', label: locale.value === 'zh' ? 'Excel 协议授权' : 'Excel authorization' },
        { path: '/admin/serverless', label: locale.value === 'zh' ? '地区路由' : 'Regional routing' },
      ],
    },
    { path: '/admin/users', label: t('nav.users'), icon: 'i-lucide-users', hideInSimpleMode: true },
    { path: '/admin/groups', label: t('nav.groups'), icon: 'i-lucide-folder' },
    {
      path: '/admin/channels',
      label: t('nav.channelManagement'),
      icon: 'i-lucide-layers',
      hideInSimpleMode: true,
      expandOnly: true,
      children: [
        { path: '/admin/channels/pricing', label: t('nav.channelPricing') },
        { path: '/admin/channels/monitor', label: t('nav.channelMonitor'), featureFlag: flagChannelMonitor },
      ],
    },
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
  return item.children.some(child => !child.section && route.path === child.path)
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
              active: route.path === child.path,
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
      'aria-label': t('nav.modelPlaza')
    })
  }
  if (docUrl.value) {
    items.push({ label: t('nav.docs'), icon: 'i-lucide-book-open', to: docUrl.value, target: '_blank', 'aria-label': t('nav.docs') })
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
    void nextTick(() => {
      const nav = sidebarNavRef.value
      const link = nav?.querySelector<HTMLElement>('[aria-current="page"]')
      if (!nav || !link) return
      const top = link.getBoundingClientRect().top - nav.getBoundingClientRect().top
      if (top + link.offsetHeight > nav.clientHeight) nav.scrollTop += top - nav.clientHeight / 2
    })
  }
})

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
