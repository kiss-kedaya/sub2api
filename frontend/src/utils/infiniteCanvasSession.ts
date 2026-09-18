import { keysAPI } from '@/api/keys'
import { userGroupsAPI } from '@/api/groups'
import type { ApiKey, Group } from '@/types'
import {
  INFINITE_CANVAS_KEY_NAME,
  findReusableCanvasKey,
  groupIdsEqual,
  selectSmartRoutingGroupIds,
} from '@/utils/infiniteCanvas'

export class InfiniteCanvasSetupError extends Error {
  constructor(
    readonly code: 'no-groups' | 'missing-key' | 'request-failed',
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
  update: typeof keysAPI.update
  getAvailableGroups: typeof userGroupsAPI.getAvailable
}

const defaultClient: InfiniteCanvasKeyClient = {
  list: keysAPI.list,
  create: keysAPI.create,
  update: keysAPI.update,
  getAvailableGroups: userGroupsAPI.getAvailable,
}

// Route changes can mount the canvas more than once before the first setup
// request finishes. Keep one request per client and remember a newly-created
// session for the current authenticated browser session.
const inFlightSetups = new WeakMap<InfiniteCanvasKeyClient, Promise<InfiniteCanvasSession>>()
let cachedCreatedSession: { userKey: string; session: InfiniteCanvasSession } | null = null

function currentUserCacheKey(): string | null {
  if (typeof window === 'undefined') return null
  try {
    const raw = window.localStorage.getItem('auth_user')
    if (!raw) return null
    const user = JSON.parse(raw) as { id?: number | string }
    return user.id === undefined || user.id === null ? null : String(user.id)
  } catch {
    return null
  }
}

function extractErrorMessage(error: unknown): string {
  if (error instanceof Error && error.message) return error.message
  const response = (error as { response?: { data?: { detail?: string; message?: string } } })?.response
  return response?.data?.detail || response?.data?.message || ''
}

async function prepareInfiniteCanvasApiKey(client: InfiniteCanvasKeyClient): Promise<InfiniteCanvasSession> {
  let groups: Group[]
  try {
    groups = await client.getAvailableGroups()
  } catch (error) {
    throw new InfiniteCanvasSetupError('request-failed', extractErrorMessage(error) || 'Failed to load groups')
  }

  const groupIds = selectSmartRoutingGroupIds(groups)
  if (groupIds.length === 0) {
    throw new InfiniteCanvasSetupError('no-groups', 'No available groups')
  }

  let existing: ApiKey | undefined
  try {
    const listed = await client.list(1, 100, { search: INFINITE_CANVAS_KEY_NAME, sort_by: 'created_at', sort_order: 'desc' })
    existing = findReusableCanvasKey(listed.items || [])
  } catch (error) {
    throw new InfiniteCanvasSetupError('request-failed', extractErrorMessage(error) || 'Failed to list API keys')
  }

  const truncated = groups.length > groupIds.length
  const needsGroupSync = existing
    ? !groupIdsEqual(existing.group_ids && existing.group_ids.length > 0 ? existing.group_ids : existing.group_id ? [existing.group_id] : [], groupIds)
    : false

  try {
    if (existing) {
      if (needsGroupSync || existing.status !== 'active') {
        existing = await client.update(existing.id, {
          group_id: groupIds[0],
          group_ids: groupIds,
          status: 'active',
        })
      }
      if (!existing.key) {
        throw new InfiniteCanvasSetupError('missing-key', 'API key value is empty')
      }
      return {
        apiKey: existing.key,
        groupIds,
        truncated,
        created: false,
        keyId: existing.id,
      }
    }

    const userKey = currentUserCacheKey()
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
      userKey ? { idempotencyKey: `infinite-canvas-key-${userKey}` } : undefined,
    )
    if (!created.key) {
      throw new InfiniteCanvasSetupError('missing-key', 'API key value is empty')
    }
    const session = {
      apiKey: created.key,
      groupIds,
      truncated,
      created: true,
      keyId: created.id,
    }
    if (client === defaultClient) {
      const userKey = currentUserCacheKey()
      if (userKey) cachedCreatedSession = { userKey, session }
    }
    return session
  } catch (error) {
    if (error instanceof InfiniteCanvasSetupError) throw error
    throw new InfiniteCanvasSetupError('request-failed', extractErrorMessage(error) || 'Failed to create API key')
  }
}

export function ensureInfiniteCanvasApiKey(
  client: InfiniteCanvasKeyClient = defaultClient,
): Promise<InfiniteCanvasSession> {
  if (client === defaultClient) {
    const userKey = currentUserCacheKey()
    if (userKey && cachedCreatedSession?.userKey === userKey) {
      return Promise.resolve({ ...cachedCreatedSession.session, created: false })
    }
  }

  const pending = inFlightSetups.get(client)
  if (pending) return pending

  const operation = prepareInfiniteCanvasApiKey(client)
  inFlightSetups.set(client, operation)
  const clearInFlight = () => {
    if (inFlightSetups.get(client) === operation) inFlightSetups.delete(client)
  }
  void operation.then(clearInFlight, clearInFlight)
  return operation
}
