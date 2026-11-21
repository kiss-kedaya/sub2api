import { keysAPI } from '@/api/keys'
import { userGroupsAPI } from '@/api/groups'
import type { ApiKey } from '@/types'
import {
  INFINITE_CANVAS_KEY_NAME,
  findReusableCanvasKey,
  isUsableCanvasKey,
  selectSmartRoutingGroupIds,
} from '@/utils/infiniteCanvas'

export class InfiniteCanvasSetupError extends Error {
  constructor(
    readonly code: 'not-configured' | 'key-unavailable' | 'no-groups' | 'missing-key' | 'request-failed',
    message: string,
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
  /** 旧画布密钥的明文已不可回读，本次为进入画布重新签发了凭据。 */
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

// SPA 生命周期内的内存缓存：明文 key 不再从服务端回读，创建/轮换后先记在这里，
// 供同一标签页内再次进入画布时复用；刷新页面即消失，不写 localStorage/sessionStorage。
const sessionSecrets = new Map<number, string>()

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

interface SecretCapableKey extends ApiKey {
  key?: string
}

function toSession(key: SecretCapableKey, created: boolean, rotated = false): InfiniteCanvasSession {
  const secret = key.key || sessionSecrets.get(key.id) || ''
  if (!secret) throw new InfiniteCanvasSetupError('missing-key', 'API key value is not available')
  sessionSecrets.set(key.id, secret)
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

async function restoreCanvasSession(
  client: InfiniteCanvasKeyClient,
  key: ApiKey,
  userScope: string | null,
): Promise<InfiniteCanvasSession> {
  if (!isUsableCanvasKey(key)) {
    throw new InfiniteCanvasSetupError('key-unavailable', 'Canvas key is disabled, expired or exhausted')
  }
  // 没有本标签页的明文缓存时，只轮换已验证的画布专用 key。
  if (!sessionSecrets.has(key.id) && typeof client.rotate === 'function') {
    const rotated = await client.rotate(key.id)
    assertUserScope(userScope)
    return toSession(rotated, false, true)
  }
  return toSession(key, false)
}

async function prepareInfiniteCanvasApiKey(
  client: InfiniteCanvasKeyClient,
  createIfMissing: boolean,
  userScope: string | null,
): Promise<InfiniteCanvasSession> {
  try {
    const listed = await listCanvasKeys(client)
    assertUserScope(userScope)
    const existing = findReusableCanvasKey(listed.items || [])
    if (existing) return await restoreCanvasSession(client, existing, userScope)
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
      { idempotencyKey: crypto.randomUUID() },
    )
    assertUserScope(userScope)
    if (created.key) return toSession(created, true)

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
    return await restoreCanvasSession(client, replayed, userScope)
  } catch (error) {
    if (error instanceof InfiniteCanvasSetupError) throw error
    throw new InfiniteCanvasSetupError('request-failed', extractErrorMessage(error) || 'Failed to prepare API key')
  }
}

export function ensureInfiniteCanvasApiKey(
  client: InfiniteCanvasKeyClient = defaultClient,
  options: { createIfMissing?: boolean } = {},
): Promise<InfiniteCanvasSession> {
  const scope = currentUserScope()
  const createIfMissing = options.createIfMissing === true
  const operationKey = JSON.stringify([scope, createIfMissing])
  let operations = inFlightSetups.get(client)
  if (!operations) {
    operations = new Map()
    inFlightSetups.set(client, operations)
  }
  const pending = operations.get(operationKey)
  if (pending) return pending

  const operation = prepareInfiniteCanvasApiKey(client, createIfMissing, scope)
  operations.set(operationKey, operation)
  const clearInFlight = () => {
    if (operations.get(operationKey) === operation) operations.delete(operationKey)
  }
  void operation.then(clearInFlight, clearInFlight)
  return operation
}
