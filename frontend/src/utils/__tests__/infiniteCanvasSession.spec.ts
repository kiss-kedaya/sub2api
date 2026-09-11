import { beforeEach, describe, expect, it, vi } from 'vitest'

import type { ApiKey, Group } from '@/types'
import { INFINITE_CANVAS_KEY_NAME } from '../infiniteCanvas'
import { InfiniteCanvasSetupError, ensureInfiniteCanvasApiKey } from '../infiniteCanvasSession'

function group(id: number): Group {
  return { id, name: `g${id}` } as Group
}

function key(partial: Partial<ApiKey>): ApiKey {
  return {
    id: 1,
    user_id: 1,
    key: 'sk-live',
    name: INFINITE_CANVAS_KEY_NAME,
    group_id: 2,
    group_ids: [2, 5],
    status: 'active',
    ip_whitelist: [],
    ip_blacklist: [],
    last_used_at: null,
    last_used_ip: null,
    quota: 0,
    quota_used: 0,
    expires_at: null,
    created_at: '',
    updated_at: '',
    current_concurrency: 0,
    rate_limit_5h: 0,
    rate_limit_1d: 0,
    rate_limit_7d: 0,
    usage_5h: 0,
    usage_1d: 0,
    usage_7d: 0,
    window_5h_start: null,
    window_1d_start: null,
    window_7d_start: null,
    reset_5h_at: null,
    reset_1d_at: null,
    reset_7d_at: null,
    ...partial,
  }
}

describe('ensureInfiniteCanvasApiKey', () => {
  const list = vi.fn()
  const create = vi.fn()
  const update = vi.fn()
  const getAvailableGroups = vi.fn()

  beforeEach(() => {
    list.mockReset()
    create.mockReset()
    update.mockReset()
    getAvailableGroups.mockReset()
  })

  it('creates a smart-routing key with every available group in order', async () => {
    getAvailableGroups.mockResolvedValue([group(4), group(1), group(9)])
    list.mockResolvedValue({ items: [], pages: 1 })
    create.mockResolvedValue(key({ key: 'sk-new', group_id: 4, group_ids: [4, 1, 9] }))

    const session = await ensureInfiniteCanvasApiKey({ list, create, update, getAvailableGroups })

    expect(create).toHaveBeenCalledWith(
      INFINITE_CANVAS_KEY_NAME,
      4,
      undefined,
      undefined,
      undefined,
      undefined,
      undefined,
      undefined,
      [4, 1, 9],
    )
    expect(session).toMatchObject({ apiKey: 'sk-new', created: true, groupIds: [4, 1, 9] })
  })

  it('reuses the named key and refreshes group_ids when membership changed', async () => {
    getAvailableGroups.mockResolvedValue([group(4), group(1)])
    list.mockResolvedValue({ items: [key({ id: 7, group_ids: [2] })] })
    update.mockResolvedValue(key({ id: 7, key: 'sk-live', group_ids: [4, 1] }))

    const session = await ensureInfiniteCanvasApiKey({ list, create, update, getAvailableGroups })

    expect(create).not.toHaveBeenCalled()
    expect(update).toHaveBeenCalledWith(7, { group_id: 4, group_ids: [4, 1], status: 'active' })
    expect(session.created).toBe(false)
    expect(session.keyId).toBe(7)
  })

  it('fails when the user has no bindable groups', async () => {
    getAvailableGroups.mockResolvedValue([])
    await expect(ensureInfiniteCanvasApiKey({ list, create, update, getAvailableGroups })).rejects.toBeInstanceOf(InfiniteCanvasSetupError)
    expect(create).not.toHaveBeenCalled()
  })
})
