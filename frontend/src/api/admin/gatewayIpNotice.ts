import { apiClient } from '../client'

/** An IP the gateway appended by itself after several accounts shared one source. */
export interface GatewayIPNoticeAutoEntry {
  ip: string
  source: 'auto'
  first_seen_at: string
  distinct_users: number
  window_minutes: number
  key_prefixes: string[]
}

export interface GatewayIPNoticeSettings {
  enabled: boolean
  ips: string[]
  message: string
  rpm: number
  auto_add_enabled: boolean
  sharing_window_minutes: number
  sharing_threshold: number
  /**
   * Opt-in escalation: when true a sharing hit also disables the involved keys.
   * Missing/false means observe-only — the IP is added, key statuses are not.
   */
  key_disable_enabled: boolean
  auto_entries: GatewayIPNoticeAutoEntry[]
}

export async function getGatewayIPNotice(): Promise<GatewayIPNoticeSettings> {
  const { data } = await apiClient.get<GatewayIPNoticeSettings>('/admin/settings/gateway-ip-notice')
  return data
}

export async function updateGatewayIPNotice(settings: GatewayIPNoticeSettings): Promise<GatewayIPNoticeSettings> {
  const { data } = await apiClient.put<GatewayIPNoticeSettings>('/admin/settings/gateway-ip-notice', settings)
  return data
}
