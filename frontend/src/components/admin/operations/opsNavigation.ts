import { computed } from 'vue'
import { useAuthStore } from '@/stores/auth'
import { useAdminSettingsStore } from '@/stores/adminSettings'
import { FeatureFlags, makeSidebarFlag } from '@/utils/featureFlags'

export interface OperationsPage {
  path: string
  name: string
  labelKey: string
}

interface OperationsItem extends OperationsPage {
  feature?: 'channelMonitor' | 'opsMonitoring'
  featureFlag?: () => boolean | undefined
  hideInSimpleMode?: boolean
  children?: OperationsItem[]
}

export interface OperationsArea {
  id: string
  labelKey: string
  descriptionKey: string
  icon: string
  items: OperationsItem[]
}

// Existing URLs only. Sub-pages stay inside their original page's own navigation.
export const operationsAreas: OperationsArea[] = [
  {
    id: 'overview', labelKey: 'opsCenter.areas.overview', descriptionKey: 'opsCenter.descriptions.overview', icon: 'i-lucide-gauge',
    items: [{ path: '/admin/ops-center', name: 'AdminOpsCenter', labelKey: 'opsCenter.areas.overview' }],
  },
  {
    id: 'accounts', labelKey: 'opsCenter.areas.accounts', descriptionKey: 'opsCenter.descriptions.accounts', icon: 'i-lucide-server',
    items: [
      { path: '/admin/upstream-monitor', name: 'AdminUpstreamMonitor', labelKey: 'nav.upstreamMonitor' },
      { path: '/admin/account-ops', name: 'AdminAccountOps', labelKey: 'accountOps.title', children: [
        { path: '/admin/account-ops/channels', name: 'AdminAccountOpsChannels', labelKey: 'accountOps.notificationChannels' },
        { path: '/admin/account-ops/rules', name: 'AdminAccountOpsRules', labelKey: 'accountOps.accountRules' },
        { path: '/admin/account-ops/records', name: 'AdminAccountOpsRecords', labelKey: 'accountOps.recordsTab' },
      ] },
      { path: '/admin/ops-automation', name: 'AdminOpsAutomation', labelKey: 'opsAutomation.title' },
      { path: '/admin/credential-guard', name: 'AdminCredentialGuard', labelKey: 'opsCenter.pages.credentials' },
      { path: '/admin/excel-grants', name: 'AdminExcelGrants', labelKey: 'opsCenter.pages.excel' },
    ],
  },
  {
    id: 'channels', labelKey: 'opsCenter.areas.channels', descriptionKey: 'opsCenter.descriptions.channels', icon: 'i-lucide-layers',
    items: [
      { path: '/admin/channel-operations', name: 'AdminChannelOperations', labelKey: 'channelOps.title' },
      { path: '/admin/channels/pricing', name: 'AdminChannels', labelKey: 'nav.channelPricing', hideInSimpleMode: true },
      { path: '/admin/channels/monitor', name: 'AdminChannelMonitor', labelKey: 'nav.channelMonitor', feature: 'channelMonitor', hideInSimpleMode: true },
      { path: '/admin/model-comparison', name: 'AdminModelComparison', labelKey: 'opsCenter.pages.comparison' },
    ],
  },
  {
    id: 'diagnostics', labelKey: 'opsCenter.areas.diagnostics', descriptionKey: 'opsCenter.descriptions.diagnostics', icon: 'i-lucide-scan-search',
    items: [
      { path: '/admin/request-captures', name: 'AdminRequestCaptures', labelKey: 'admin.requestCapture.title', children: [
        { path: '/admin/request-captures/settings', name: 'AdminRequestCaptureSettings', labelKey: 'opsCenter.pages.captureSettings' },
      ] },
      { path: '/admin/ops', name: 'AdminOps', labelKey: 'admin.ops.title', feature: 'opsMonitoring' },
      { path: '/admin/connections', name: 'AdminConnections', labelKey: 'opsCenter.pages.connections' },
      { path: '/admin/group-cache-tests', name: 'AdminGroupCacheTests', labelKey: 'admin.groupCacheTest.title' },
      { path: '/admin/traffic', name: 'AdminTraffic', labelKey: 'opsCenter.pages.traffic' },
    ],
  },
  {
    id: 'network', labelKey: 'opsCenter.areas.network', descriptionKey: 'opsCenter.descriptions.network', icon: 'i-lucide-globe',
    items: [
      { path: '/admin/mihomo', name: 'AdminMihomo', labelKey: 'opsCenter.pages.proxies', children: [
        { path: '/admin/mihomo/nodes', name: 'AdminMihomoNodes', labelKey: 'opsCenter.pages.proxyNodes' },
        { path: '/admin/mihomo/dynamic', name: 'AdminMihomoDynamic', labelKey: 'opsCenter.pages.dynamicProxies' },
        { path: '/admin/mihomo/kernel', name: 'AdminMihomoKernel', labelKey: 'opsCenter.pages.proxyKernel' },
      ] },
      { path: '/admin/serverless', name: 'AdminServerless', labelKey: 'serverless.title' },
    ],
  },
]

/** The sidebar's existing opt-out filter, shared by every operations entry point. */
export function applyFeatureFlags<T extends { featureFlag?: () => boolean | undefined; children?: T[] }>(items: T[]): T[] {
  return items.filter(item => item.featureFlag?.() !== false).map(item => item.children
    ? { ...item, children: applyFeatureFlags(item.children) }
    : item)
}

export function isOperationsPageActive(path: string, currentPath: string): boolean {
  return currentPath === path || currentPath.startsWith(path + '/')
}

export function getOperationsPages(areas: OperationsArea[]): OperationsPage[] {
  return areas.flatMap(area => area.items.flatMap(item => [item, ...(item.children ?? [])]))
}

export function useOperationsNavigation() {
  const auth = useAuthStore()
  const settings = useAdminSettingsStore()
  const flags = {
    channelMonitor: makeSidebarFlag(FeatureFlags.channelMonitor),
    opsMonitoring: () => settings.opsMonitoringEnabled,
  }
  return computed(() => {
    if (!auth.isAdmin || auth.isAdminObserver) return []
    return operationsAreas.map(area => ({
      ...area,
      items: applyFeatureFlags<OperationsItem>(area.items.map(item => ({ ...item, featureFlag: item.feature ? flags[item.feature] : undefined })))
        .filter(item => !auth.isSimpleMode || !item.hideInSimpleMode),
    })).filter(area => area.items.length)
  })
}
