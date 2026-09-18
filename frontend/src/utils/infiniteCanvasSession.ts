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
  keyId: number
}

export interface InfiniteCanvasKeyClient {
  list: typeof keysAPI.list
  create: typeof keysAPI.create
  getAvailableGroups: typeof userGroupsAPI.getAvailable
}

const defaultClient: InfiniteCanvasKeyClient = {
  list: keysAPI.list,
  create: keysAPI.create,
  getAvailableGroups: userGroupsAPI.getAvailable,
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

function toSession(key: ApiKey, created: boolean): InfiniteCanvasSession {
  if (!key.key) throw new InfiniteCanvasSetupError('missing-key', 'API key value is empty')
  return {
    apiKey: key.key,
    groupIds: key.group_ids?.length ? key.group_ids : key.group_id ? [key.group_id] : [],
    truncated: false,
    created,
    keyId: key.id,
  }
}

async function prepareInfiniteCanvasApiKey(
  client: InfiniteCanvasKeyClient,
  createIfMissing: boolean,
  userScope: string | null,
): Promise<InfiniteCanvasSession> {
  try {
    const listed = await client.list(1, 100, { search: INFINITE_CANVAS_KEY_NAME, sort_by: 'created_at', sort_order: 'desc' })
    assertUserScope(userScope)
    const existing = findReusableCanvasKey(listed.items || [])
    if (existing) {
      if (!isUsableCanvasKey(existing)) {
        throw new InfiniteCanvasSetupError('key-unavailable', 'Canvas key is disabled, expired or exhausted')
      }
      return toSession(existing, false)
    }
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
      { idempotencyKey: crypto.randomUUID() },
    )
    assertUserScope(userScope)
    return toSession(created, true)
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
