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
    localStorage.clear()
    list.mockReset()
    create.mockReset()
    update.mockReset()
    getAvailableGroups.mockReset()
  })

  it('never creates a key merely by entering the canvas', async () => {
    list.mockResolvedValue({ items: [], pages: 1 })
    await expect(ensureInfiniteCanvasApiKey({ list, create, getAvailableGroups }))
      .rejects.toMatchObject({ code: 'not-configured' })
    expect(create).not.toHaveBeenCalled()
    expect(getAvailableGroups).not.toHaveBeenCalled()
  })

  it('creates a smart-routing key with every available group only after an explicit action', async () => {
    getAvailableGroups.mockResolvedValue([group(4), group(1), group(9)])
    list.mockResolvedValue({ items: [], pages: 1 })
    create.mockResolvedValue(key({ key: 'sk-new', group_id: 4, group_ids: [4, 1, 9] }))

    const session = await ensureInfiniteCanvasApiKey({ list, create, getAvailableGroups }, { createIfMissing: true })

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
      { idempotencyKey: expect.any(String) },
    )
    expect(session).toMatchObject({ apiKey: 'sk-new', created: true, groupIds: [4, 1, 9] })
  })

  it('deduplicates concurrent first-entry setup requests', async () => {
    getAvailableGroups.mockResolvedValue([group(4), group(1)])
    list.mockResolvedValue({ items: [], pages: 1 })
    create.mockImplementation(async () => {
      await new Promise((resolve) => setTimeout(resolve, 5))
      return key({ key: 'sk-new', group_id: 4, group_ids: [4, 1] })
    })

    const client = { list, create, update, getAvailableGroups }
    const [first, second] = await Promise.all([
      ensureInfiniteCanvasApiKey(client, { createIfMissing: true }),
      ensureInfiniteCanvasApiKey(client, { createIfMissing: true }),
    ])

    expect(create).toHaveBeenCalledTimes(1)
    expect(first.apiKey).toBe('sk-new')
    expect(second.apiKey).toBe('sk-new')
  })

  it('reuses the named key without changing its routing groups', async () => {
    getAvailableGroups.mockResolvedValue([group(4), group(1)])
    list.mockResolvedValue({ items: [key({ id: 7, group_ids: [2] })] })
    update.mockResolvedValue(key({ id: 7, key: 'sk-live', group_ids: [4, 1] }))

    const session = await ensureInfiniteCanvasApiKey({ list, create, getAvailableGroups })

    expect(create).not.toHaveBeenCalled()
    expect(update).not.toHaveBeenCalled()
    expect(getAvailableGroups).not.toHaveBeenCalled()
    expect(session.groupIds).toEqual([2])
    expect(session.created).toBe(false)
    expect(session.keyId).toBe(7)
  })

  it('fails when the user has no bindable groups', async () => {
    list.mockResolvedValue({ items: [] })
    getAvailableGroups.mockResolvedValue([])
    await expect(ensureInfiniteCanvasApiKey({ list, create, getAvailableGroups }, { createIfMissing: true })).rejects.toBeInstanceOf(InfiniteCanvasSetupError)
    expect(create).not.toHaveBeenCalled()
  })

  it.each([
    { status: 'inactive' },
    { expires_at: '2020-01-01T00:00:00Z' },
    { quota: 1, quota_used: 1 },
  ])('does not re-enable or replace an unusable key: %j', async (state) => {
    list.mockResolvedValue({ items: [key(state)] })
    await expect(ensureInfiniteCanvasApiKey({ list, create, getAvailableGroups }, { createIfMissing: true }))
      .rejects.toMatchObject({ code: 'key-unavailable' })
    expect(update).not.toHaveBeenCalled()
    expect(create).not.toHaveBeenCalled()
  })

  it('checks current key state again after deletion instead of reusing cached credentials', async () => {
    localStorage.setItem('auth_user', JSON.stringify({ id: 1 }))
    const client = { list, create, getAvailableGroups }
    list.mockResolvedValue({ items: [] })
    getAvailableGroups.mockResolvedValue([group(2)])
    create.mockResolvedValue(key({}))
    await ensureInfiniteCanvasApiKey(client, { createIfMissing: true })
    await expect(ensureInfiniteCanvasApiKey(client)).rejects.toMatchObject({ code: 'not-configured' })
    expect(create).toHaveBeenCalledTimes(1)
    expect(list).toHaveBeenCalledTimes(2)
  })

  it('does not reuse an in-flight session after switching users', async () => {
    let finishOldList!: (value: { items: ApiKey[] }) => void
    list.mockImplementationOnce(() => new Promise((resolve) => { finishOldList = resolve }))
      .mockResolvedValueOnce({ items: [key({ user_id: 2, key: 'sk-second-user' })] })
    const client = { list, create, getAvailableGroups }
    localStorage.setItem('auth_user', JSON.stringify({ id: 1 }))
    const oldSession = ensureInfiniteCanvasApiKey(client)
    const oldRejection = expect(oldSession).rejects.toMatchObject({ code: 'request-failed' })
    localStorage.setItem('auth_user', JSON.stringify({ id: 2 }))
    const newSession = await ensureInfiniteCanvasApiKey(client)
    finishOldList({ items: [key({ key: 'sk-first-user' })] })
    await oldRejection
    expect(newSession.apiKey).toBe('sk-second-user')
    expect(create).not.toHaveBeenCalled()
  })

  it('does not create a key when the user changes while groups are loading', async () => {
    list.mockResolvedValue({ items: [] })
    localStorage.setItem('auth_user', JSON.stringify({ id: 1 }))
    getAvailableGroups.mockImplementation(async () => {
      localStorage.setItem('auth_user', JSON.stringify({ id: 2 }))
      return [group(2)]
    })
    await expect(ensureInfiniteCanvasApiKey({ list, create, getAvailableGroups }, { createIfMissing: true }))
      .rejects.toMatchObject({ code: 'request-failed' })
    expect(create).not.toHaveBeenCalled()
  })

  it('does not create a replacement when listing keys fails', async () => {
    list.mockRejectedValue(new Error('network error'))
    await expect(ensureInfiniteCanvasApiKey({ list, create, getAvailableGroups }, { createIfMissing: true }))
      .rejects.toMatchObject({ code: 'request-failed' })
    expect(create).not.toHaveBeenCalled()
  })
})
