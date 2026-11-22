import { apiClient } from '@/api/client'
import {
  KEY_ROTATION_REQUIRED_CODE,
  KEY_ROTATION_REMEDIATION_PATH,
  isKeyRotationRequiredPayload,
} from '@/api/keyRotationGateCodes'

export {
  KEY_ROTATION_REQUIRED_CODE,
  KEY_ROTATION_REMEDIATION_PATH,
  KEY_ROTATION_REQUIRED_EVENT,
} from '@/api/keyRotationGateCodes'

/**
 * Status of the key-rotation gate for the signed-in user.
 *
 * - `required` is true while the user has at least one active API key created
 *   before the cutoff without an IP allowlist, i.e. redeem / recharge / crypto
 *   deposit are blocked until they remediate.
 * - `risk_ack_enabled` / `risk_ack_hours` describe the optional "I know the
 *   risk, continue" escape hatch (security.key_rotation_risk_ack_*).
 * - `risk_ack_expires_at` is present while a valid risk acknowledgement lets the
 *   user recharge anyway; `required` is then false with reason
 *   `risk_acknowledged`.
 */
export interface KeyRotationGateStatus {
  enabled: boolean
  required: boolean
  cutoff: string
  active_key_count: number
  compliant_key_count: number
  pending_key_count: number
  reason: string
  error_code?: string
  message?: string
  remediation_path: string
  risk_ack_enabled: boolean
  risk_ack_hours: number
  risk_ack_expires_at?: string
}

/** GET /api/v1/user/key-rotation-gate (never gated itself). */
export async function getKeyRotationGateStatus(): Promise<KeyRotationGateStatus> {
  const { data } = await apiClient.get<KeyRotationGateStatus>('/user/key-rotation-gate')
  return data
}

export interface KeyRotationRiskAckResult {
  expires_at: string
  hours: number
}

/**
 * POST /api/v1/user/security/key-rotation-risk-ack
 *
 * Explicitly acknowledges the key-leak risk for a limited time instead of
 * rotating keys / adding IP allowlists. The backend rejects the request unless
 * `acknowledge_risk` is true and returns 403 KEY_ROTATION_RISK_ACK_DISABLED when
 * the operator turned the feature off.
 */
export async function acknowledgeKeyRotationRisk(): Promise<KeyRotationRiskAckResult> {
  const { data } = await apiClient.post<KeyRotationRiskAckResult>('/user/security/key-rotation-risk-ack', {
    acknowledge_risk: true,
  })
  return data
}

/**
 * True when an API error comes from the key-rotation gate.
 *
 * The backend returns HTTP 403 with reason/metadata code
 * KEY_ROTATION_REQUIRED; the axios interceptor surfaces both `reason` and
 * `metadata.error_code`.
 */
export function isKeyRotationRequiredError(error: unknown): boolean {
  return isKeyRotationRequiredPayload(error)
}

export const keyRotationGateAPI = {
  getStatus: getKeyRotationGateStatus,
  acknowledgeRisk: acknowledgeKeyRotationRisk,
  isRequiredError: isKeyRotationRequiredError,
  requiredCode: KEY_ROTATION_REQUIRED_CODE,
  remediationPath: KEY_ROTATION_REMEDIATION_PATH,
}

export default keyRotationGateAPI
