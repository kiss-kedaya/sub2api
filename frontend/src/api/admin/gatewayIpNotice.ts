import { apiClient } from '../client'

export interface GatewayIPNoticeSettings {
  enabled: boolean
  ips: string[]
  message: string
  rpm: number
}

export async function getGatewayIPNotice(): Promise<GatewayIPNoticeSettings> {
  const { data } = await apiClient.get<GatewayIPNoticeSettings>('/admin/settings/gateway-ip-notice')
  return data
}

export async function updateGatewayIPNotice(settings: GatewayIPNoticeSettings): Promise<GatewayIPNoticeSettings> {
  const { data } = await apiClient.put<GatewayIPNoticeSettings>('/admin/settings/gateway-ip-notice', settings)
  return data
}
