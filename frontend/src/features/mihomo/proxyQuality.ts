// 代理质量检测结果展示逻辑(静态代理列表与 IP 管理的 Mihomo 节点共用)。
import type { ProxyQualityCheckResult } from '@/types'

export type ProxyQualityOverallStatus = 'healthy' | 'warn' | 'challenge' | 'failed'

// 总体状态优先级:挑战 > 失败 > 告警 > 优质。
export function summarizeProxyQuality(
  result: Pick<ProxyQualityCheckResult, 'challenge_count' | 'failed_count' | 'warn_count'>,
): ProxyQualityOverallStatus {
  if (result.challenge_count > 0) return 'challenge'
  if (result.failed_count > 0) return 'failed'
  if (result.warn_count > 0) return 'warn'
  return 'healthy'
}

// 总体状态徽章的 CSS class(纯函数,无 i18n 依赖)。
export function proxyQualityOverallClass(status?: string): string {
  if (status === 'healthy') return 'badge-success'
  if (status === 'warn') return 'badge-warning'
  return 'badge-danger'
}

// 总体状态文案的 i18n key(返回 key 而非已翻译文本,便于单测且不耦合 i18n)。
export function proxyQualityOverallLabelKey(status?: string): string {
  if (status === 'healthy') return 'admin.proxies.qualityStatusHealthy'
  if (status === 'warn') return 'admin.proxies.qualityStatusWarn'
  if (status === 'challenge') return 'admin.proxies.qualityStatusChallenge'
  return 'admin.proxies.qualityStatusFail'
}
