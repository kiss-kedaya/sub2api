/**
 * API Keys management endpoints
 * Handles CRUD operations for user API keys
 */

import { apiClient } from './client'
import type {
  ApiKey,
  ApiKeyPasskeyVerificationBegin,
  ApiKeyPasskeyVerificationTicket,
  ApiKeyRevealGrant,
  ApiKeySecret,
  ApiKeySecretSeal,
  ApiKeyUpdateResult,
  ApiKeyVerificationFactors,
  ApiKeyVerificationPayload,
  CreateApiKeyRequest,
  UpdateApiKeyRequest,
  PaginatedResponse,
} from '@/types'

/** Only the initial create response includes the plaintext key. */
type ApiKeyCreateResult = ApiKey & Partial<Pick<ApiKeySecret, 'key'>>

/**
 * List all API keys for current user
 * @param page - Page number (default: 1)
 * @param pageSize - Items per page (default: 10)
 * @param filters - Optional filter parameters
 * @param options - Optional request options
 * @returns Paginated list of API keys
 */
export async function list(
  page: number = 1,
  pageSize: number = 10,
  filters?: {
    search?: string
    status?: string
    group_id?: number | string
    sort_by?: string
    sort_order?: 'asc' | 'desc'
  },
  options?: {
    signal?: AbortSignal
  }
): Promise<PaginatedResponse<ApiKey>> {
  const { data } = await apiClient.get<PaginatedResponse<ApiKey>>('/keys', {
    params: { page, page_size: pageSize, ...filters },
    signal: options?.signal
  })
  return data
}

/**
 * Get API key by ID
 * @param id - API key ID
 * @returns API key details
 */
export async function getById(id: number): Promise<ApiKey> {
  const { data } = await apiClient.get<ApiKey>(`/keys/${id}`)
  return data
}

/**
 * Create new API key
 * @param name - Key name
 * @param groupId - Optional group ID
 * @param customKey - Optional custom key value
 * @param ipWhitelist - Optional IP whitelist
 * @param ipBlacklist - Optional IP blacklist
 * @param quota - Optional quota limit in USD (0 = unlimited)
 * @param expiresInDays - Optional days until expiry (undefined = never expires)
 * @param rateLimitData - Optional rate limit fields
 * @param anthropicCacheTtlMode - Optional Anthropic cache_control TTL override
 * @param options - Optional idempotency key and DeepSeek tool injection setting (enabled by default)
 * @returns Created API key, with plaintext omitted on an idempotent replay
 */
export async function create(
  name: string,
  groupId?: number | null,
  customKey?: string,
  ipWhitelist?: string[],
  ipBlacklist?: string[],
  quota?: number,
  expiresInDays?: number,
  rateLimitData?: { rate_limit_5h?: number; rate_limit_1d?: number; rate_limit_7d?: number },
  groupIds?: number[],
  anthropicCacheTtlMode?: 'inherit' | 'off' | '1h' | '5m',
  options?: {
    idempotencyKey?: string
    deepseekToolInjectionEnabled?: boolean
    concurrencyLimit?: number
    autoBlockDatacenterIPs?: boolean
    /** 三选一身份校验；账号存在可用因子时服务端强制要求。 */
    verification?: ApiKeyVerificationPayload
  }
): Promise<ApiKeyCreateResult> {
  const payload: CreateApiKeyRequest = {
    name,
    deepseek_tool_injection_enabled: options?.deepseekToolInjectionEnabled ?? true,
  }
  if (options?.autoBlockDatacenterIPs) payload.auto_block_datacenter_ips = true
  if (options?.verification) payload.verification = options.verification
  if (options?.concurrencyLimit !== undefined && options.concurrencyLimit > 0) payload.concurrency_limit = options.concurrencyLimit
  if (groupId !== undefined) {
    payload.group_id = groupId
  }
  if (groupIds && groupIds.length > 0) {
    payload.group_ids = groupIds
  }
  if (customKey) {
    payload.custom_key = customKey
  }
  if (ipWhitelist && ipWhitelist.length > 0) {
    payload.ip_whitelist = ipWhitelist
  }
  if (ipBlacklist && ipBlacklist.length > 0) {
    payload.ip_blacklist = ipBlacklist
  }
  if (quota !== undefined && quota > 0) {
    payload.quota = quota
  }
  if (expiresInDays !== undefined && expiresInDays > 0) {
    payload.expires_in_days = expiresInDays
  }
  if (rateLimitData?.rate_limit_5h && rateLimitData.rate_limit_5h > 0) {
    payload.rate_limit_5h = rateLimitData.rate_limit_5h
  }
  if (rateLimitData?.rate_limit_1d && rateLimitData.rate_limit_1d > 0) {
    payload.rate_limit_1d = rateLimitData.rate_limit_1d
  }
  if (rateLimitData?.rate_limit_7d && rateLimitData.rate_limit_7d > 0) {
    payload.rate_limit_7d = rateLimitData.rate_limit_7d
  }
  // 密钥级 Anthropic cache TTL 覆盖；inherit 是服务端默认值，不必发送。
  if (anthropicCacheTtlMode && anthropicCacheTtlMode !== 'inherit') {
    payload.anthropic_cache_ttl_mode = anthropicCacheTtlMode
  }

  const { data } = await apiClient.post<ApiKeyCreateResult>('/keys', payload, options?.idempotencyKey
    ? { headers: { 'Idempotency-Key': options.idempotencyKey } }
    : undefined)
  return data
}

/**
 * Update API key
 * @param id - API key ID
 * @param updates - Fields to update
 * @returns Updated API key
 */
export async function update(id: number, updates: UpdateApiKeyRequest): Promise<ApiKeyUpdateResult> {
  const { data } = await apiClient.put<ApiKeyUpdateResult>(`/keys/${id}`, updates)
  return data
}

/**
 * Delete API key
 * @param id - API key ID
 * @returns Success confirmation
 */
export async function deleteKey(id: number): Promise<{ message: string }> {
  const { data } = await apiClient.delete<{ message: string }>(`/keys/${id}`)
  return data
}

export interface BulkDeleteKeysResult {
  succeeded: number[]
  failed: number[]
}

/**
 * Delete several API keys in one round trip.
 *
 * The backend only exposes a per-key DELETE, so this fans out and reports which
 * ids failed. The caller removes rows optimistically and puts the failures back,
 * which keeps a bulk delete from refetching the whole list.
 */
export async function deleteMany(ids: number[]): Promise<BulkDeleteKeysResult> {
  const unique = [...new Set(ids)]
  if (unique.length === 0) return { succeeded: [], failed: [] }
  const results = await Promise.allSettled(unique.map((id) => deleteKey(id)))
  const succeeded: number[] = []
  const failed: number[] = []
  results.forEach((result, index) => {
    if (result.status === 'fulfilled') succeeded.push(unique[index])
    else failed.push(unique[index])
  })
  return { succeeded, failed }
}

/**
 * Toggle API key status (active/inactive)
 * @param id - API key ID
 * @param status - New status
 * @returns Updated API key
 */
export async function toggleStatus(id: number, status: 'active' | 'inactive'): Promise<ApiKey> {
  return update(id, { status })
}

export interface BlockDatacenterIPsResult {
  added: string[]
  added_count: number
  checked_ip_count: number
}

/** Append recently used hosting/proxy IPs to this key's IP blacklist. */
export async function blockDatacenterIPs(id: number): Promise<BlockDatacenterIPsResult> {
  const { data } = await apiClient.post<BlockDatacenterIPsResult>(`/keys/${id}/block-datacenter-ips`)
  return data
}

/** Location data for one client IP. Empty strings mean "unknown". */
export interface IPWhitelistAssistGeo {
  country: string
  region: string
  city: string
  isp: string
  hosting: boolean
}

export interface IPWhitelistAssistRecentIP {
  ip: string
  count: number
  geo: IPWhitelistAssistGeo
}

export interface IPWhitelistAssistResponse {
  current_ip: string
  current_geo: IPWhitelistAssistGeo
  recent_ips: IPWhitelistAssistRecentIP[]
}

/** Backend-derived answer for the IP whitelist helper (current IP + location). */
export async function getIPWhitelistAssist(): Promise<IPWhitelistAssistResponse> {
  const { data } = await apiClient.get<IPWhitelistAssistResponse>('/user/ip-whitelist-assist')
  return data
}


/**
 * Rotate the key value (reset/regenerate).
 *
 * Reuses the existing update endpoint with `regenerate_key`: the server keeps
 * the key row, group, quota and upstream accounts, replaces only the credential
 * and reactivates a key that the gateway risk control disabled. The old key
 * stops authenticating immediately.
 */
export async function rotateKey(
  id: number,
  verification?: ApiKeyVerificationPayload,
): Promise<ApiKeySecret> {
  // regenerate_key 让服务端在这一次响应里返回新明文，之后列表只回 key_masked。
  // 明文回执与创建同级，账号有可用因子时服务端要求三选一身份校验。
  const updates: UpdateApiKeyRequest = { regenerate_key: true }
  if (verification) updates.verification = verification
  return update(id, updates) as Promise<ApiKeySecret>
}

/**
 * Save / replace the local ciphertext that lets the user view this key later.
 *
 * The browser encrypts the plaintext with the login password before calling
 * this, so the request carries the ciphertext, not the key. The login password
 * is also sent in the same request body (over TLS) so the server can bcrypt-check
 * it — a wrong password would make the key undecryptable later; the server never
 * stores, logs or audits it.
 * `PUT /api/v1/user/api-keys/:id/secret-seal`
 */
export interface PutSecretSealOptions {
  /** 登录密码：服务端只做 bcrypt 校验，确认封存用的确实是登录密码。 */
  password?: string
  /** 本次封存授权的因子（创建时的三选一），二次查看要求同一因子。 */
  authFactor?: string
}

export async function putSecretSeal(
  id: number,
  seal: ApiKeySecretSeal,
  options?: PutSecretSealOptions,
): Promise<void> {
  const body: Record<string, unknown> = { ...seal }
  if (options?.password) body.password = options.password
  if (options?.authFactor) body.auth_factor = options.authFactor
  await apiClient.put(`/user/api-keys/${id}/secret-seal`, body)
}

/**
 * Fetch the opaque ciphertext for local decryption.
 * `GET /api/v1/user/api-keys/:id/secret-seal`
 * Rejects with `reason: 'API_KEY_SECRET_NOT_FOUND'` (HTTP 404) when nothing was saved.
 */
export async function getSecretSeal(id: number, grant: string): Promise<ApiKeySecretSeal> {
  const { data } = await apiClient.get<ApiKeySecretSeal>(`/user/api-keys/${id}/secret-seal`, {
    headers: { 'X-Reveal-Grant': grant },
  })
  return data
}

/**
 * 三选一身份校验：当前账号实际可用的因子。
 * `GET /api/v1/user/security/verification-factors`
 */
export async function getVerificationFactors(): Promise<ApiKeyVerificationFactors> {
  const { data } = await apiClient.get<ApiKeyVerificationFactors>('/user/security/verification-factors')
  return data
}

/** Passkey 校验挑战（创建 / 二次查看 API Key 用，与登录 2FA、step-up 分开）。 */
export async function beginKeyPasskeyVerification(): Promise<ApiKeyPasskeyVerificationBegin> {
  const { data } = await apiClient.post<ApiKeyPasskeyVerificationBegin>(
    '/user/security/passkey/verify/begin',
  )
  return data
}

/**
 * 完成一次 Passkey 断言并换取 5 分钟一次性票据；票据再作为
 * `verification.passkey_session_token` 提交给创建 / 二次查看授权接口。
 */
export async function finishKeyPasskeyVerification(
  sessionToken: string,
  credential: Record<string, unknown>,
): Promise<ApiKeyPasskeyVerificationTicket> {
  const { data } = await apiClient.post<ApiKeyPasskeyVerificationTicket>(
    '/user/security/passkey/verify/finish',
    { session_token: sessionToken, credential },
  )
  return data
}

/**
 * 二次查看授权：用创建时的同一因子完成身份校验，换取 5 分钟一次性 reveal grant。
 * `POST /api/v1/user/api-keys/:id/reveal-authorize`
 */
export async function authorizeReveal(
  id: number,
  verification: ApiKeyVerificationPayload,
): Promise<ApiKeyRevealGrant> {
  const { data } = await apiClient.post<ApiKeyRevealGrant>(
    `/user/api-keys/${id}/reveal-authorize`,
    verification,
  )
  return data
}

/**
 * Drop the saved ciphertext so the key can no longer be viewed later.
 * `DELETE /api/v1/user/api-keys/:id/secret-seal`
 */
export async function deleteSecretSeal(id: number): Promise<void> {
  await apiClient.delete(`/user/api-keys/${id}/secret-seal`)
}

/**
 * Rotate several keys in one round trip.
 *
 * `POST /api/v1/keys/batch-rotate` only accepts the caller's own keys (a foreign
 * or missing id fails the whole request with 404), caps the batch at 50 ids and
 * rotates each key in its own transaction. Every successful item carries the new
 * plaintext exactly once; failures carry an `error_code` and never block the
 * other items.
 *
 * The batch emits plaintext, so it is gated exactly like creation: the caller
 * must prove identity (password / TOTP / passkey) first. The backend answers
 * 400 `VERIFY_REQUIRED` when the account has TOTP or a passkey enabled and the
 * request carries no verification. An account with only a login password is not
 * forced to verify (a verification it does send is still checked).
 */
export interface BatchRotateKeyItem {
  id: number
  status: 'ok' | 'failed'
  /** New plaintext, present only for `status === 'ok'`. Never persisted. */
  key?: string
  error_code?: string
}

export interface BatchRotateKeysResult {
  results: BatchRotateKeyItem[]
  requested_count: number
  succeeded_count: number
  failed_count: number
}

export async function batchRotateKeys(
  ids: number[],
  verification?: ApiKeyVerificationPayload,
): Promise<BatchRotateKeysResult> {
  const unique = [...new Set(ids.filter((id) => Number.isFinite(id) && id > 0))]
  const body: { ids: number[]; verification?: ApiKeyVerificationPayload } = { ids: unique }
  if (verification) body.verification = verification
  const { data } = await apiClient.post<BatchRotateKeysResult>('/keys/batch-rotate', body)
  return data
}

/** Rolling 24h overview for one key (owner-only, no cost/model/IP breakdown). */
export interface ApiKeyOverview24h {
  id: number
  requests_24h: number
  cost_24h: number
}

export interface ApiKeysOverviewResponse {
  items: ApiKeyOverview24h[]
  window_hours: number
}

/**
 * One batched aggregate for a page of keys.
 *
 * `GET /api/v1/keys/overview?ids=1,2,3` returns 24h call counts and billed cost
 * for the caller's own keys in a single grouped query, so a table row never
 * triggers its own aggregate request (N+1).
 */
export async function getOverview(ids: number[]): Promise<ApiKeysOverviewResponse> {
  const unique = [...new Set(ids.filter((id) => Number.isFinite(id) && id > 0))]
  if (unique.length === 0) return { items: [], window_hours: 24 }
  const { data } = await apiClient.get<ApiKeysOverviewResponse>('/keys/overview', {
    params: { ids: unique.join(',') },
  })
  return data
}

export const keysAPI = {
	getConcurrencyQueue,
  list,
  getById,
  create,
  update,
  delete: deleteKey,
  deleteMany,
  toggleStatus,
  blockDatacenterIPs,
  getIPWhitelistAssist,
  rotateKey,
  batchRotateKeys,
  getOverview,
  getVerificationFactors,
  beginKeyPasskeyVerification,
  finishKeyPasskeyVerification,
  authorizeReveal,
  putSecretSeal,
  getSecretSeal,
  deleteSecretSeal,
}

export default keysAPI

export interface KeyQueueSnapshot {
  queue_policy: { max_waiting: number; timeout_seconds: number }
  items: Array<{ id: number; current_concurrency: number; current_waiting: number }>
}

export async function getConcurrencyQueue(ids: number[], signal?: AbortSignal): Promise<KeyQueueSnapshot> {
  const { data } = await apiClient.get<KeyQueueSnapshot>('/keys/concurrency', { params: { ids: ids.join(',') }, signal })
  return data
}
