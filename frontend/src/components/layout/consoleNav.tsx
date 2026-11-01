import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { useAdminSettingsStore, useAppStore, useAuthStore } from '@/stores'
import { resolveDocsUrl } from '@/utils/docsUrl'
import { FeatureFlags, isFeatureFlagEnabled, makeSidebarFlag } from '@/utils/featureFlags'
import { useBatchImageAccess } from '@/composables/useBatchImageAccess'
import { routeParentTrail } from './routeTrail'

// .tsx only so Nuxt UI's icon bundler scans this file (it reads .vue/.tsx, not .ts): the
// i-lucide-* names below are bundled at build time. There is no JSX here.

/**
 * The console navigation in one place: the sidebar (AppSidebar) and the Ctrl/⌘K palette
 * (ConsoleNavigationSearch) both read this tree, so every page in the sidebar is also in
 * the palette. Two levels like the template: a group, then its pages. An area's sub-pages
 * are reached through its PageSubnav; the sidebar entry points at the area's top route and
 * stays lit on all of them (`match`, a path prefix).
 */
export interface NavLeaf {
  kind: 'link'
  path: string
  label: string
  /** Iconify name (lucide set, bundled at build time). Top-level entries only in the sidebar. */
  icon?: string
  /** Admin-uploaded custom menu icon, sanitized before rendering. */
  iconSvg?: string
  /** Path prefix that keeps this entry active on the area's sub-pages; defaults to `path`. */
  match?: string
  /** In the Ctrl/⌘K palette only (e.g. a sub-page reached through the area's sub-nav). */
  searchOnly?: boolean
  hideInSimpleMode?: boolean
  /**
   * 可选的功能开关 getter。返回 false 时菜单项被隐藏；返回 undefined/true 时显示。
   * 宽容策略（undefined → 显示）避免 public settings 未加载完成时菜单闪烁消失。
   */
  featureFlag?: () => boolean | undefined
  external?: boolean
}

/** Header inside an expanded group (and in the rail flyout); it does not navigate. */
export interface NavSection {
  kind: 'section'
  key: string
  label: string
}

export interface NavGroup {
  kind: 'group'
  /** Stable key (also the accordion value). */
  key: string
  label: string
  icon: string
  children: Array<NavLeaf | NavSection>
  /** Open unless the visitor closed it (user side: few entries). Admin groups open when active. */
  defaultOpen?: boolean
  hideInSimpleMode?: boolean
  featureFlag?: () => boolean | undefined
}

export type NavNode = NavGroup | NavLeaf

function visible(entry: { hideInSimpleMode?: boolean; featureFlag?: () => boolean | undefined }, simple: boolean): boolean {
  if (entry.featureFlag && entry.featureFlag() === false) return false
  return !(simple && entry.hideInSimpleMode)
}

// Drops hidden entries, sections left without links, and groups left empty.
function finalize(nodes: NavNode[], simple: boolean): NavNode[] {
  const out: NavNode[] = []
  for (const node of nodes) {
    if (!visible(node, simple)) continue
    if (node.kind === 'link') {
      out.push(node)
      continue
    }
    const children: Array<NavLeaf | NavSection> = []
    for (const child of node.children) {
      if (child.kind === 'link' && !visible(child, simple)) continue
      if (child.kind === 'section' && children.at(-1)?.kind === 'section') children.pop()
      children.push(child)
    }
    while (children.at(-1)?.kind === 'section') children.pop()
    if (children.some((child) => child.kind === 'link')) out.push({ ...node, children })
  }
  return out
}

/** Every page in the tree (groups flattened), in order. */
export function navLeaves(nodes: NavNode[]): NavLeaf[] {
  return nodes.flatMap((node) => (node.kind === 'link' ? [node] : node.children.filter((child): child is NavLeaf => child.kind === 'link')))
}

function prefixOf(leaf: NavLeaf): string {
  return (leaf.match ?? leaf.path).split('?')[0].replace(/\/+$/, '') || '/'
}

/** The sidebar entry for a path: the deepest prefix that contains it (first one on a tie). */
export function matchNavLeaf(nodes: NavNode[], path: string): NavLeaf | null {
  const current = path.replace(/\/+$/, '') || '/'
  let best: NavLeaf | null = null
  let bestLength = -1
  for (const leaf of navLeaves(nodes)) {
    if (leaf.searchOnly || leaf.external) continue
    const prefix = prefixOf(leaf)
    const hit = current === prefix || current.startsWith(prefix === '/' ? '/' : `${prefix}/`)
    if (hit && prefix.length > bestLength) {
      best = leaf
      bestLength = prefix.length
    }
  }
  return best
}

// This custom menu is embedded into /purchase and should not appear as a second sidebar entry.
const RECHARGE_CENTER_MENU_ID = '322273f5aaa4d036'

export function useConsoleNav() {
  const { t, locale } = useI18n()
  const route = useRoute()
  const router = useRouter()
  const appStore = useAppStore()
  const authStore = useAuthStore()
  const adminSettingsStore = useAdminSettingsStore()
  const { canUseBatchImage } = useBatchImageAccess()

  const zh = () => locale.value === 'zh'
  const text = (zhText: string, enText: string) => (zh() ? zhText : enText)
  const link = (path: string, label: string, extra: Partial<NavLeaf> = {}): NavLeaf => ({ kind: 'link', path, label, ...extra })
  const section = (key: string, label: string): NavSection => ({ kind: 'section', key, label })

  // Public-settings flags go through the registry in utils/featureFlags.ts, which handles the
  // opt-in vs opt-out fallback when settings haven't loaded yet. Admin-only flags stay inline.
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

  const isAdmin = computed(() => authStore.isAdmin)
  const homePath = computed(() => authStore.isAdminObserver ? '/admin/usage' : isAdmin.value ? '/admin/dashboard' : '/dashboard')

  // The visitor's own pages, in four groups: overview, API and usage, recharge and orders,
  // help and account. 可用渠道紧挨渠道状态，让用户"先看自己能用什么、再看对应状态"。
  function selfGroups(withDashboard: boolean): NavGroup[] {
    return [
      {
        kind: 'group', key: 'user-overview', label: t('nav.groupOverview'), icon: 'i-lucide-layout-dashboard', defaultOpen: true,
        children: [
          ...(withDashboard ? [link('/dashboard', t('nav.dashboard'))] : []),
          link('/available-channels', t('nav.availableChannels'), { hideInSimpleMode: true, featureFlag: flagAvailableChannels }),
          link('/monitor', t('nav.channelStatus'), { featureFlag: flagChannelMonitor })
        ]
      },
      {
        kind: 'group', key: 'user-api', label: t('nav.groupApiUsage'), icon: 'i-lucide-key-round', defaultOpen: true,
        children: [
          link('/keys', t('nav.apiKeys')),
          link('/infinite-canvas', t('nav.infiniteCanvas')),
          link('/batch-image', t('nav.batchImage'), { hideInSimpleMode: true, featureFlag: flagBatchImageAccess }),
          link('/usage', t('nav.usage'), { hideInSimpleMode: true })
        ]
      },
      {
        kind: 'group', key: 'user-billing', label: t('nav.groupRecharge'), icon: 'i-lucide-wallet', defaultOpen: true,
        children: [
          link('/purchase', t('nav.buySubscription'), { hideInSimpleMode: true, featureFlag: flagPayment }),
          link('/deposit', t('nav.cryptoDeposit'), { featureFlag: flagCrypto }),
          link('/orders', t('nav.myOrders'), { hideInSimpleMode: true, featureFlag: flagPayment }),
          link('/redeem', t('nav.redeem'), { hideInSimpleMode: true })
        ]
      },
      {
        kind: 'group', key: 'user-help', label: t('nav.groupHelpAccount'), icon: 'i-lucide-life-buoy', defaultOpen: true,
        children: [
          link('/affiliate', t('nav.affiliate'), { hideInSimpleMode: true, featureFlag: flagAffiliate }),
          link('/tickets', t('tickets.title')),
          link('/profile', t('nav.profile')),
          link('/security', text('安全记录', 'Security records'))
        ]
      }
    ]
  }

  // Admin: "My account" folds the visitor's four groups into one, with their names as headers.
  function personalGroup(): NavGroup {
    const children: Array<NavLeaf | NavSection> = [link('/dashboard', t('nav.dashboard'), { searchOnly: true })]
    for (const group of selfGroups(false)) {
      children.push(section(`#${group.key}`, group.label), ...group.children)
    }
    return { kind: 'group', key: 'my-account', label: t('nav.myAccount'), icon: 'i-lucide-user', children }
  }

  function adminGroups(): NavNode[] {
    const simple = authStore.isSimpleMode
    return [
      {
        kind: 'group', key: 'admin-overview', label: t('nav.groupOverview'), icon: 'i-lucide-layout-dashboard',
        children: [
          link('/admin/dashboard', t('nav.dashboard')),
          link('/admin/usage', t('nav.usage')),
          link('/admin/traffic', text('用户流量', 'User traffic'))
        ]
      },
      {
        kind: 'group', key: 'admin-accounts', label: t('nav.groupAccountsChannels'), icon: 'i-lucide-globe',
        children: [
          link('/admin/accounts', t('nav.accounts')),
          link('/admin/groups', t('nav.groups')),
          link('/admin/channels/pricing', t('nav.channelPricing'), { hideInSimpleMode: true }),
          link('/admin/model-comparison', text('模型比价', 'Model comparison')),
          link('/admin/proxies', t('nav.proxies')),
          link('/admin/mihomo', text('代理出口', 'Managed proxies')),
          link('/admin/excel-grants', text('Excel 协议授权', 'Excel authorization')),
          link('/admin/serverless', text('地区路由', 'Regional routing'))
        ]
      },
      {
        kind: 'group', key: 'admin-billing', label: t('nav.groupUsersBilling'), icon: 'i-lucide-users',
        children: [
          link('/admin/users', t('nav.users'), { hideInSimpleMode: true }),
          link('/admin/subscriptions', t('nav.subscriptions'), { hideInSimpleMode: true }),
          section('#admin-billing-payment', t('nav.sectionPayment')),
          link('/admin/orders', t('nav.orderManagement'), { hideInSimpleMode: true, featureFlag: flagAdminPayment, match: '/admin/orders' }),
          link('/admin/orders/dashboard', t('nav.paymentDashboard'), { searchOnly: true, hideInSimpleMode: true, featureFlag: flagAdminPayment }),
          link('/admin/orders/plans', t('nav.paymentPlans'), { searchOnly: true, hideInSimpleMode: true, featureFlag: flagAdminPayment }),
          link('/admin/crypto', t('nav.cryptoDeposits'), { hideInSimpleMode: true }),
          link('/admin/redeem', t('nav.redeemCodes'), { hideInSimpleMode: true }),
          section('#admin-billing-promotion', t('nav.sectionPromotion')),
          link('/admin/promo-codes', t('nav.promoCodes'), { hideInSimpleMode: true }),
          link('/admin/affiliates/invites', t('nav.affiliateManagement'), { hideInSimpleMode: true, featureFlag: flagAffiliate, match: '/admin/affiliates' }),
          link('/admin/affiliates/rebates', t('nav.affiliateRebateRecords'), { searchOnly: true, hideInSimpleMode: true, featureFlag: flagAffiliate }),
          link('/admin/affiliates/transfers', t('nav.affiliateTransferRecords'), { searchOnly: true, hideInSimpleMode: true, featureFlag: flagAffiliate })
        ]
      },
      {
        kind: 'group', key: 'admin-operations', label: t('nav.groupOperations'), icon: 'i-lucide-gauge',
        children: [
          link('/admin/ops-center', text('运营总览', 'Operations overview')),
          link('/admin/account-ops', text('账号通知', 'Account notifications')),
          link('/admin/ops-automation', text('账号自动化', 'Account automation')),
          link('/admin/credential-guard', text('凭证巡检', 'Credential inspection')),
          link('/admin/channel-operations', text('渠道运维', 'Channel operations')),
          link('/admin/announcements', t('nav.announcements')),
          link('/admin/tickets', t('tickets.adminTitle'))
        ]
      },
      {
        kind: 'group', key: 'admin-monitoring', label: t('nav.groupMonitoring'), icon: 'i-lucide-activity',
        children: [
          link('/admin/ops', text('渠道状态', 'Channel status'), { featureFlag: flagOpsMonitoring }),
          link('/admin/ops?alertRules=1', text('告警阈值', 'Alert thresholds'), { featureFlag: flagOpsMonitoring }),
          link('/admin/channels/monitor', t('nav.channelMonitor'), { hideInSimpleMode: true, featureFlag: flagChannelMonitor }),
          link('/admin/connections', text('连接诊断', 'Connections')),
          link('/admin/group-cache-tests', text('分组缓存测试', 'Cache tests')),
          link('/admin/request-captures', text('请求采集', 'Request capture'))
        ]
      },
      {
        kind: 'group', key: 'admin-security', label: t('nav.groupSecurity'), icon: 'i-lucide-shield', featureFlag: flagRiskControl,
        children: [
          link('/admin/risk-control', t('nav.contentModeration')),
          link('/admin/ip-risk', text('IP风控', 'IP policies')),
          link('/admin/ip-audit', text('IP审计', 'IP audit')),
          link('/admin/prompt-audit', t('nav.promptAudit')),
          link('/admin/security-penalties', text('分层安全限制', 'Layered API restrictions'))
        ]
      },
      {
        kind: 'group', key: 'admin-system', label: t('nav.groupSystem'), icon: 'i-lucide-settings',
        children: [
          // 简单模式下，在系统设置前插入 API密钥
          ...(simple ? [link('/keys', t('nav.apiKeys'))] : []),
          link('/admin/settings', t('nav.settings')),
          link('/admin/plugins', t('nav.plugins'), { featureFlag: flagPluginManagement }),
          link('/admin/audit-logs', t('nav.auditLogs'), { hideInSimpleMode: true })
        ]
      }
    ]
  }

  // Custom menu items filtered by visibility; they keep their own icon at the top level.
  const customUserLeaves = computed((): NavLeaf[] => (appStore.cachedPublicSettings?.custom_menu_items ?? [])
    .filter((item) => item.visibility === 'user' && item.id !== RECHARGE_CENTER_MENU_ID)
    .sort((a, b) => a.sort_order - b.sort_order)
    .map((item) => link(`/custom/${item.id}`, item.label, { icon: 'i-lucide-file-text', iconSvg: item.icon_svg })))

  const customAdminLeaves = computed((): NavLeaf[] => (authStore.isAdminObserver ? [] : adminSettingsStore.customMenuItems)
    .filter((item) => item.visibility === 'admin')
    .sort((a, b) => a.sort_order - b.sort_order)
    .map((item) => link(`/custom/${item.id}`, item.label, { icon: 'i-lucide-file-text', iconSvg: item.icon_svg })))

  /** The whole tree, search-only entries included (the palette lists them all). */
  const tree = computed((): NavNode[] => {
    const simple = authStore.isSimpleMode
    if (!isAdmin.value) {
      return appStore.backendModeEnabled ? [] : finalize([...selfGroups(true), ...customUserLeaves.value], simple)
    }
    if (authStore.isAdminObserver) {
      return finalize([link('/admin/usage', t('nav.usage'), { icon: 'i-lucide-chart-column' }), ...(simple ? [] : [personalGroup(), ...customUserLeaves.value])], simple)
    }
    return finalize([...adminGroups(), ...customAdminLeaves.value, ...(simple ? [] : [personalGroup(), ...customUserLeaves.value])], simple)
  })

  /** The tree as the sidebar shows it: without the search-only entries. */
  const sidebarTree = computed((): NavNode[] => tree.value.flatMap((node): NavNode[] => {
    if (node.kind === 'link') return node.searchOnly ? [] : [node]
    const children = node.children.filter((child) => child.kind === 'section' || !child.searchOnly)
    while (children.at(-1)?.kind === 'section') children.pop()
    return children.some((child) => child.kind === 'link') ? [{ ...node, children }] : []
  }))

  // Bottom group, like the template's Feedback / Help links: the model plaza and docs.
  const docUrl = computed(() => resolveDocsUrl(appStore.docUrl))
  const modelPlazaEnabled = computed(() => isFeatureFlagEnabled(FeatureFlags.modelPlaza))
  const secondary = computed((): NavLeaf[] => {
    const items: NavLeaf[] = []
    if (authStore.user && modelPlazaEnabled.value) {
      items.push(link('/model-plaza?embedded=1', t('nav.modelPlaza'), { icon: 'i-lucide-layout-grid', match: '/model-plaza' }))
    }
    if (docUrl.value) items.push(link(docUrl.value, t('nav.docs'), { icon: 'i-lucide-book-open', external: true }))
    return items
  })

  /** The sidebar entry of the current page; detail pages fall back to their meta.parent. */
  const activeLeaf = computed((): NavLeaf | null => {
    const direct = matchNavLeaf(sidebarTree.value, route.path)
    if (direct) return direct
    for (const parent of routeParentTrail(router, route).reverse()) {
      const hit = matchNavLeaf(sidebarTree.value, parent.path)
      if (hit) return hit
    }
    return null
  })

  return { tree, sidebarTree, secondary, activeLeaf, homePath }
}
