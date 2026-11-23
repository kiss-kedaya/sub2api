import { keysAPI } from '@/api/keys'
import { userGroupsAPI } from '@/api/groups'
import type { ApiKey, ApiKeyVerificationPayload } from '@/types'
import {
  INFINITE_CANVAS_KEY_NAME,
  findReusableCanvasKey,
  isUsableCanvasKey,
  selectSmartRoutingGroupIds,
} from '@/utils/infiniteCanvas'

/** 需要用户确认轮换时，界面展示用的密钥信息（只有掩码，没有任何明文）。 */
export interface CanvasRotationTarget {
  keyId: number
  keyMasked: string
  groupCount: number
}

export class InfiniteCanvasSetupError extends Error {
  constructor(
    readonly code:
      | 'not-configured'
      | 'key-unavailable'
      | 'no-groups'
      | 'missing-key'
      | 'request-failed'
      /** 明文已无法找回，继续需要轮换这把密钥：必须由用户在界面上明确确认，绝不自动发生。 */
      | 'rotation-required'
      /** 创建/轮换要求的三选一身份校验没通过（密码错、验证码错、Passkey 失败、缺材料……），reason 是服务端错误码。 */
      | 'verification-failed',
    message: string,
    readonly rotationTarget?: CanvasRotationTarget,
    readonly reason?: string,
  ) {
    super(message)
    this.name = 'InfiniteCanvasSetupError'
  }
}

export interface InfiniteCanvasSession {
  apiKey: string
  groupIds: number[]
  truncated: boolean
  created: boolean
  /** 旧画布密钥的明文已不可回读，用户确认后本次重新签发了凭据（旧密钥已失效）。 */
  rotated: boolean
  keyId: number
}

export interface InfiniteCanvasKeyClient {
  list: typeof keysAPI.list
  create: typeof keysAPI.create
  rotate: typeof keysAPI.rotateKey
  getAvailableGroups: typeof userGroupsAPI.getAvailable
}

const defaultClient: InfiniteCanvasKeyClient = {
  list: keysAPI.list,
  create: keysAPI.create,
  rotate: keysAPI.rotateKey,
  getAvailableGroups: userGroupsAPI.getAvailable,
}

interface CachedCanvasSecret {
  owner: number
  keyMasked: string
  keyRotatedAt: string | null
  secret: string
}

// SPA 生命周期内的内存缓存：只保存创建/轮换回执里的明文，不写持久化存储。
const sessionSecrets = new Map<number, CachedCanvasSecret>()

/** 仅供单测隔离：清空 SPA 内存中的明文缓存。 */
export function __resetInfiniteCanvasSecretsForTests(): void {
  sessionSecrets.clear()
}

// Only share pending work within the same user and operation. Every later
// entry reads current key state so revoked or deleted keys cannot stay cached.
const inFlightSetups = new WeakMap<InfiniteCanvasKeyClient, Map<string, Promise<InfiniteCanvasSession>>>()

function currentUserScope(): string | null {
  try {
    const raw = window.localStorage.getItem('auth_user')
    if (!raw) return null
    const user = JSON.parse(raw) as { id?: number | string }
    return user.id === undefined || user.id === null ? null : String(user.id)
  } catch {
    return null
  }
}

function assertUserScope(scope: string | null): void {
  if (currentUserScope() !== scope) {
    throw new InfiniteCanvasSetupError('request-failed', 'The authenticated user changed')
  }
}

function extractErrorMessage(error: unknown): string {
  if (error instanceof Error && error.message) return error.message
  const response = (error as { response?: { data?: { detail?: string; message?: string } } })?.response
  return response?.data?.detail || response?.data?.message || ''
}

function readCachedSecret(key: ApiKey): string | undefined {
  const cached = sessionSecrets.get(key.id)
  if (!cached) return undefined
  // 直接比较服务端的原始时间字符串；Date 会丢失微秒精度。
  if (cached.owner === key.user_id
    && cached.keyMasked === key.key_masked
    && cached.keyRotatedAt === (key.key_rotated_at ?? null)) {
    return cached.secret
  }
  sessionSecrets.delete(key.id)
}

function toSession(key: ApiKey, secret: string | undefined, created: boolean, rotated = false): InfiniteCanvasSession {
  if (!secret) throw new InfiniteCanvasSetupError('missing-key', 'API key value is not available')
  if (created || rotated) {
    sessionSecrets.set(key.id, {
      owner: key.user_id,
      keyMasked: key.key_masked,
      keyRotatedAt: key.key_rotated_at ?? null,
      secret,
    })
  }
  return {
    apiKey: secret,
    groupIds: key.group_ids?.length ? key.group_ids : key.group_id ? [key.group_id] : [],
    truncated: false,
    created,
    rotated,
    keyId: key.id,
  }
}

function listCanvasKeys(client: InfiniteCanvasKeyClient) {
  return client.list(1, 100, { search: INFINITE_CANVAS_KEY_NAME, sort_by: 'created_at', sort_order: 'desc' })
}

/**
 * 取回这把画布密钥的明文。这是整个文件里唯一会调用 client.rotate 的地方，
 * 而且只有 allowRotate === true（用户在界面上点了确认）才会走到。
 *
 * 为什么不能自动轮换：密钥明文只在创建/轮换回执里出现一次，刷新页面后内存缓存就没了；
 * 此前「没有缓存就轮换」会让用户每刷新一次再进画布就把「Infinite Canvas」密钥换掉一次，
 * 凡是把这把密钥复制到别的软件里用的人，都会被悄悄踢下线。
 */
async function restoreCanvasSession(
  client: InfiniteCanvasKeyClient,
  key: ApiKey,
  userScope: string | null,
  allowRotate: boolean,
  verification?: ApiKeyVerificationPayload,
): Promise<InfiniteCanvasSession> {
  if (!isUsableCanvasKey(key)) {
    throw new InfiniteCanvasSetupError('key-unavailable', 'Canvas key is disabled, expired or exhausted')
  }
  const secret = readCachedSecret(key)
  if (secret) return toSession(key, secret, false)
  // 不能轮换（旧客户端没有 rotate）：保持原有的 missing-key 语义。
  if (typeof client.rotate !== 'function') return toSession(key, secret, false)
  if (!allowRotate) {
    throw new InfiniteCanvasSetupError(
      'rotation-required',
      'Canvas key plaintext is unavailable; rotating it needs explicit confirmation',
      {
        keyId: key.id,
        keyMasked: key.key_masked,
        groupCount: key.group_ids?.length ? key.group_ids.length : key.group_id ? 1 : 0,
      },
    )
  }
  // 只轮换刚刚在当前列表里验证过的画布专用 key。轮换会下发新明文，账号有可用因子时服务端要求身份校验。
  const rotated = verification ? await client.rotate(key.id, verification) : await client.rotate(key.id)
  assertUserScope(userScope)
  return toSession(rotated, rotated.key, false, true)
}

async function prepareInfiniteCanvasApiKey(
  client: InfiniteCanvasKeyClient,
  createIfMissing: boolean,
  allowRotate: boolean,
  verification: ApiKeyVerificationPayload | undefined,
  userScope: string | null,
): Promise<InfiniteCanvasSession> {
  try {
    const listed = await listCanvasKeys(client)
    assertUserScope(userScope)
    const existing = findReusableCanvasKey(listed.items || [])
    if (existing) return await restoreCanvasSession(client, existing, userScope, allowRotate, verification)
    if (!createIfMissing) {
      throw new InfiniteCanvasSetupError('not-configured', 'No canvas key configured')
    }

    const groups = await client.getAvailableGroups()
    assertUserScope(userScope)
    const groupIds = selectSmartRoutingGroupIds(groups)
    if (groupIds.length === 0) {
      throw new InfiniteCanvasSetupError('no-groups', 'No available groups')
    }
    const created = await client.create(
      INFINITE_CANVAS_KEY_NAME,
      groupIds[0],
      undefined,
      undefined,
      undefined,
      undefined,
      undefined,
      undefined,
      groupIds,
      undefined,
      // 创建同样会下发明文：账号有可用因子时必须带三选一身份校验（只在有材料时才带这个键）。
      { idempotencyKey: crypto.randomUUID(), ...(verification ? { verification } : {}) },
    )
    assertUserScope(userScope)
    if (created.key) return toSession(created, created.key, true)

    // 重试回执可能过时：只在当前列表中重新核对同 ID 的画布专用 key，不换目标也不重建。
    const latest = await listCanvasKeys(client)
    assertUserScope(userScope)
    const replayed = latest.items?.find((key) =>
      key.id === created.id
      && key.name === INFINITE_CANVAS_KEY_NAME
      && key.user_id === created.user_id
      && (userScope === null || String(key.user_id) === userScope),
    )
    if (!replayed) throw new InfiniteCanvasSetupError('missing-key', 'Could not verify the replayed canvas key')
    return await restoreCanvasSession(client, replayed, userScope, allowRotate, verification)
  } catch (error) {
    if (error instanceof InfiniteCanvasSetupError) throw error
    // 身份校验没过（密码错/验证码错/缺材料/因子不可用）：单独报出来，界面要留在校验步骤让用户重试，而不是糊成「请求失败」。
    const reason = (error as { reason?: unknown } | null)?.reason
    if (typeof reason === 'string' && reason.startsWith('VERIFY_')) {
      throw new InfiniteCanvasSetupError('verification-failed', extractErrorMessage(error) || reason, undefined, reason)
    }
    throw new InfiniteCanvasSetupError('request-failed', extractErrorMessage(error) || 'Failed to prepare API key')
  }
}

export function ensureInfiniteCanvasApiKey(
  client: InfiniteCanvasKeyClient = defaultClient,
  options: {
    createIfMissing?: boolean
    /** 仅在用户明确点了「轮换并进入」后才传 true；默认 false，永远不会自动轮换。 */
    allowRotate?: boolean
    /** 三选一身份校验载荷（创建/轮换会下发明文）；账号没有任何可用因子时不传。只在本次调用里使用，不缓存、不进 in-flight 的键。 */
    verification?: ApiKeyVerificationPayload
  } = {},
): Promise<InfiniteCanvasSession> {
  const scope = currentUserScope()
  const createIfMissing = options.createIfMissing === true
  const allowRotate = options.allowRotate === true
  const operationKey = JSON.stringify([scope, createIfMissing, allowRotate])
  let operations = inFlightSetups.get(client)
  if (!operations) {
    operations = new Map()
    inFlightSetups.set(client, operations)
  }
  const pending = operations.get(operationKey)
  if (pending) return pending

  const operation = prepareInfiniteCanvasApiKey(client, createIfMissing, allowRotate, options.verification, scope)
  operations.set(operationKey, operation)
  const clearInFlight = () => {
    if (operations.get(operationKey) === operation) operations.delete(operationKey)
  }
  void operation.then(clearInFlight, clearInFlight)
  return operation
}
