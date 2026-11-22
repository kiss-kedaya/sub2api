import { apiClient } from '@/api/client'
import type { CryptoVerification } from '@/api/admin/crypto'

/**
 * Wallet signer shard endpoints (backend contract from PR #519).
 *
 *   GET  /admin/crypto/signer/status        public shard configuration
 *   GET  /admin/crypto/signer/risk-notice   risk points + acknowledgement phrase
 *   POST /admin/crypto/signer/setup         v2 -> v3 migration (body TOTP)
 *   POST /admin/crypto/signer/resplit       replace share A (body TOTP)
 *   POST /admin/crypto/signer/verify-share  prove one share opens this wallet (header TOTP)
 *   POST /admin/crypto/signer/auto-sweep    server-held automatic-sweep share
 *
 * The setup/resplit bodies carry the browser-generated data key exactly once;
 * shares B/C are never sent to the server.
 */

export interface SignerRiskScheme {
  id: string
  threshold: number
  total: number
  share_prefix: string
}

export interface SignerRiskNotice {
  points: { zh: string[]; en: string[] }
  phrases: { zh: string; en: string }
  display_once: boolean
  schemes: SignerRiskScheme[]
}

export interface SignerShardStatus {
  configured: boolean
  scheme?: string
  threshold?: number
  total?: number
  vault_version: number
  locked: boolean
  /**
   * RFC3339 end of the resident data-key window. Present only while a recovery
   * share has unlocked the signer; the console shows the remaining time and
   * disables the sweep button again when it lapses.
   */
  unlocked_until?: string
  /** Same window as whole seconds remaining, immune to client clock skew. */
  unlock_lease_seconds?: number
  auto_enabled: boolean
  created_at?: string
  created_by?: number
  updated_at?: string
  dk_commitment?: string
}

export interface SignerSetupPayload {
  totp_code: string
  risk_phrase: string
  data_key: string
  share_a: string
  confirm_share: string
  dk_commitment: string
}

export interface SignerAutoSweepPayload {
  enabled: boolean
  totp_code: string
  risk_phrase?: string
  share?: string
  acknowledge_risk?: boolean
}

const NO_STORE_HEADERS = { 'Cache-Control': 'no-store', Pragma: 'no-cache' } as const

function unwrap<T>(body: unknown): T {
  if (body && typeof body === 'object' && 'data' in body) return (body as { data: T }).data
  return body as T
}

export async function getSignerRiskNotice(): Promise<SignerRiskNotice> {
  const { data } = await apiClient.get('/admin/crypto/signer/risk-notice', { headers: { ...NO_STORE_HEADERS } })
  return unwrap<SignerRiskNotice>(data)
}

export async function getSignerShardStatus(): Promise<SignerShardStatus> {
  const { data } = await apiClient.get('/admin/crypto/signer/status', { headers: { ...NO_STORE_HEADERS } })
  return unwrap<SignerShardStatus>(data)
}

// The migration re-seals the vault and pre-generates the deposit address pool
// before it returns; the server allows up to 90s for that work.
export async function setupSignerShards(payload: SignerSetupPayload): Promise<SignerShardStatus> {
  const { data } = await apiClient.post('/admin/crypto/signer/setup', payload, {
    headers: { ...NO_STORE_HEADERS },
    timeout: 180_000
  })
  return unwrap<SignerShardStatus>(data)
}

export async function resplitSignerShards(payload: SignerSetupPayload): Promise<SignerShardStatus> {
  const { data } = await apiClient.post('/admin/crypto/signer/resplit', payload, {
    headers: { ...NO_STORE_HEADERS },
    timeout: 120_000
  })
  return unwrap<SignerShardStatus>(data)
}

// The signer group middleware still requires a second factor header for this
// POST; the handler itself only reads the share.
export async function verifySignerShare(share: string, totpCode: string): Promise<{ valid: true }> {
  const { data } = await apiClient.post('/admin/crypto/signer/verify-share', { share }, {
    headers: { ...NO_STORE_HEADERS, 'X-Crypto-TOTP': totpCode },
    timeout: 30_000
  })
  return unwrap<{ valid: true }>(data)
}

/**
 * Verifies a recovery share and keeps the data key resident for the bounded
 * 15-minute lease, so the sweep button works right away. Requires a freshly
 * typed code (not the five-minute grant): the request body carries it.
 */
export async function unlockSignerWithShare(share: string, totpCode: string): Promise<SignerShardStatus> {
  const { data } = await apiClient.post('/admin/crypto/signer/unlock', { share, totp_code: totpCode }, {
    headers: { ...NO_STORE_HEADERS },
    timeout: 30_000
  })
  return unwrap<SignerShardStatus>(data)
}

export async function setSignerAutoSweep(payload: SignerAutoSweepPayload): Promise<SignerShardStatus> {
  const { data } = await apiClient.post('/admin/crypto/signer/auto-sweep', payload, {
    headers: { ...NO_STORE_HEADERS },
    timeout: 60_000
  })
  return unwrap<SignerShardStatus>(data)
}

/** Header proof for endpoints that accept the shared crypto grant. */
export function signerVerificationHeaders(verification?: CryptoVerification): Record<string, string> {
  if (!verification) return {}
  if (verification.credential) return { 'X-Crypto-Grant': verification.credential }
  if (verification.totpCode) return { 'X-Crypto-TOTP': verification.totpCode }
  return {}
}
