import { apiClient } from '../client'

/**
 * 密钥轮换门禁的「风险确认跳过」设置（settings 表
 * security.key_rotation_risk_ack_*）：
 * - enabled=false：用户不能再写确认，已写的确认也立即不再参与门禁评估；
 * - hours：一次确认的有效期，后端限定 1..720。
 */
export interface KeyRotationRiskAckSettings {
  enabled: boolean
  hours: number
}

export async function getKeyRotationRiskAckSettings(): Promise<KeyRotationRiskAckSettings> {
  const { data } = await apiClient.get<KeyRotationRiskAckSettings>('/admin/settings/key-rotation-risk-ack')
  return data
}

export async function updateKeyRotationRiskAckSettings(
  settings: KeyRotationRiskAckSettings
): Promise<KeyRotationRiskAckSettings> {
  const { data } = await apiClient.put<KeyRotationRiskAckSettings>('/admin/settings/key-rotation-risk-ack', settings)
  return data
}
