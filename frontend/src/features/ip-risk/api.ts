import { apiClient } from '@/api/client'
import type { PaginatedResponse } from '@/types'

export interface IPThreshold { limit: number; window_seconds: number }
export interface IPBlockRule { network: string; note: string; enabled: boolean; expires_at: number }
export interface AnonymousAPIConfig { enabled: boolean; threshold: number; window_seconds: number; ban_seconds: number; max_tracked_ips: number; reset_on_success: boolean }
export interface IPRiskConfig {
  anonymous_api: AnonymousAPIConfig
  enabled: boolean
  observe_only: boolean
  whitelist: string[]
  blacklist: IPBlockRule[]
  allowed_countries: string[]
  blocked_countries: string[]
  block_unknown_country: boolean
  deny_user_agents: string[]
  block_empty_ua: boolean
  ipv6_prefix: number
  ban_seconds: number
  login_attempts: IPThreshold
  login_failures: IPThreshold
  register_attempts: IPThreshold
  register_failures: IPThreshold
  verification_attempts: IPThreshold
  event_retention_hours: number
}
export interface IPRiskEvent {
  id: string
  at: number
  ip: string
  network: string
  peer: string
  ip_source: string
  country: string
  method: string
  category: string
  path: string
  user_agent: string
  status: number
  reason: string
  error_reason: string
  decision: string
}
export interface IPBan { network: string; reason: string; created_at: number; expires_at: number; observe_only: boolean }
export interface IPRiskSnapshot {
  config: IPRiskConfig
  health: { redis_available: boolean; emergency_disabled: boolean; config_error: string }
  current_ip: string
  trusted_proxies_configured: boolean
}
const base = '/admin/settings/ip-risk'
export const ipRiskAPI = {
  async get() { return (await apiClient.get<IPRiskSnapshot>(base)).data },
  async save(config: IPRiskConfig) { return (await apiClient.put<IPRiskConfig>(base, config)).data },
  async events(ip = '', reason = '', category = '', page = 1, pageSize = 20) { return (await apiClient.get<PaginatedResponse<IPRiskEvent>>(base + '/events', { params: { ip, reason, category, page, page_size: pageSize } })).data },
  async bans() { return (await apiClient.get<{ items: IPBan[] }>(base + '/bans')).data.items },
  async anonymousBans() { return (await apiClient.get<{ items: IPBan[] }>(base + '/anonymous-bans')).data.items },
  async unbanAnonymous(network: string) { await apiClient.post(base + '/anonymous-unban', { network }) },
  async unban(network: string) { await apiClient.post(base + '/unban', { network }) },
  async clearEvents() { await apiClient.delete(base + '/events') },
  async check(ip: string, user_agent: string, auth: boolean) { return (await apiClient.post<IPRiskEvent>(base + '/check', { ip, user_agent, auth })).data }
}
