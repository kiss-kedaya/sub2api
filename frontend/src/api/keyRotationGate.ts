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
}

/** GET /api/v1/user/key-rotation-gate (never gated itself). */
export async function getKeyRotationGateStatus(): Promise<KeyRotationGateStatus> {
  const { data } = await apiClient.get<KeyRotationGateStatus>('/user/key-rotation-gate')
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
  isRequiredError: isKeyRotationRequiredError,
  requiredCode: KEY_ROTATION_REQUIRED_CODE,
  remediationPath: KEY_ROTATION_REMEDIATION_PATH,
}

export default keyRotationGateAPI
