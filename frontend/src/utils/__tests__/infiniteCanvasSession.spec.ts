import { beforeEach, describe, expect, it, vi } from 'vitest'

import type { ApiKey, ApiKeySecret, Group } from '@/types'
import { INFINITE_CANVAS_KEY_NAME } from '../infiniteCanvas'
import {
  InfiniteCanvasSetupError,
  __resetInfiniteCanvasSecretsForTests,
  ensureInfiniteCanvasApiKey,
} from '../infiniteCanvasSession'

function group(id: number): Group {
  return { id, name: `g${id}` } as Group
}

function key(partial: Partial<ApiKey>): ApiKey {
  return {
    id: 1,
    user_id: 1,
    key_prefix: 'sk-live',
    key_masked: 'sk-live****',
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

function secretKey(partial: Partial<ApiKey>, secret: string): ApiKeySecret {
  return { ...key(partial), key: secret }
}

describe('ensureInfiniteCanvasApiKey', () => {
  const list = vi.fn()
  const create = vi.fn()
  const rotate = vi.fn()
  const getAvailableGroups = vi.fn()

  beforeEach(() => {
    localStorage.clear()
    __resetInfiniteCanvasSecretsForTests()
    list.mockReset()
    create.mockReset()
    rotate.mockReset()
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
    create.mockResolvedValue(secretKey({ group_id: 4, group_ids: [4, 1, 9] }, 'sk-new'))

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
      undefined,
      { idempotencyKey: expect.any(String) },
    )
    expect(session).toMatchObject({ apiKey: 'sk-new', created: true, groupIds: [4, 1, 9] })
  })

  it('passes every available group when the user can bind more than ten', async () => {
    const groups = Array.from({ length: 36 }, (_, index) => group(100 + index))
    const ids = groups.map((item) => item.id)
    getAvailableGroups.mockResolvedValue(groups)
    list.mockResolvedValue({ items: [], pages: 1 })
    create.mockResolvedValue(secretKey({ id: 9, group_id: ids[0], group_ids: ids }, 'sk-many'))

    const session = await ensureInfiniteCanvasApiKey({ list, create, getAvailableGroups }, { createIfMissing: true })

    expect(create).toHaveBeenCalledWith(
      INFINITE_CANVAS_KEY_NAME,
      ids[0],
      undefined,
      undefined,
      undefined,
      undefined,
      undefined,
      undefined,
      ids,
      undefined,
      { idempotencyKey: expect.any(String) },
    )
    expect(session).toMatchObject({ apiKey: 'sk-many', created: true, groupIds: ids })
  })

  it('does not rotate on a redacted creation replay until the user consents', async () => {
    localStorage.setItem('auth_user', JSON.stringify({ id: 1 }))
    getAvailableGroups.mockResolvedValue([group(2)])
    const current = { id: 7, group_id: 9, group_ids: [9] }
    list.mockResolvedValueOnce({ items: [] })
      .mockResolvedValueOnce({ items: [key({ id: 8 }), key(current)] })
    create.mockResolvedValue(key({ id: 7 }))

    await expect(ensureInfiniteCanvasApiKey({ list, create, rotate, getAvailableGroups }, { createIfMissing: true }))
      .rejects.toMatchObject({ code: 'rotation-required', rotationTarget: { keyId: 7, groupCount: 1 } })

    expect(create).toHaveBeenCalledTimes(1)
    expect(rotate).not.toHaveBeenCalled()
  })

  it('rereads a redacted creation replay and rotates only the matching usable canvas key after consent', async () => {
    localStorage.setItem('auth_user', JSON.stringify({ id: 1 }))
    getAvailableGroups.mockResolvedValue([group(2)])
    const current = { id: 7, group_id: 9, group_ids: [9] }
    list.mockResolvedValueOnce({ items: [] })
      .mockResolvedValueOnce({ items: [key({ id: 8 }), key(current)] })
    create.mockResolvedValue(key({ id: 7 }))
    rotate.mockResolvedValue(secretKey(current, 'fixture-only-rotated-secret'))

    const session = await ensureInfiniteCanvasApiKey(
      { list, create, rotate, getAvailableGroups },
      { createIfMissing: true, allowRotate: true },
    )

    expect(list).toHaveBeenCalledTimes(2)
    expect(create).toHaveBeenCalledTimes(1)
    expect(rotate).toHaveBeenCalledTimes(1)
    expect(rotate).toHaveBeenCalledWith(7)
    expect(session).toMatchObject({
      keyId: 7, apiKey: 'fixture-only-rotated-secret', groupIds: [9], created: false, rotated: true,
    })
  })

  it.each([
    { reason: 'missing', current: undefined, code: 'missing-key' },
    { reason: 'renamed', current: { name: 'Not a canvas key' }, code: 'missing-key' },
    { reason: 'owned by another user', current: { user_id: 2 }, code: 'missing-key' },
    { reason: 'disabled', current: { status: 'inactive' }, code: 'key-unavailable' },
    { reason: 'expired', current: { expires_at: '2020-01-01T00:00:00Z' }, code: 'key-unavailable' },
    { reason: 'out of quota', current: { quota: 1, quota_used: 1 }, code: 'key-unavailable' },
  ] satisfies Array<{ reason: string; current?: Partial<ApiKey>; code: string }>)('does not rotate or create again on a redacted replay when the original key is $reason', async ({ current, code }) => {
    localStorage.setItem('auth_user', JSON.stringify({ id: 1 }))
    list.mockResolvedValueOnce({ items: [] })
      .mockResolvedValueOnce({ items: [key({ id: 8 }), ...(current ? [key({ id: 7, ...current })] : [])] })
    getAvailableGroups.mockResolvedValue([group(2)])
    create.mockResolvedValue(key({ id: 7 }))

    await expect(ensureInfiniteCanvasApiKey({ list, create, rotate, getAvailableGroups }, { createIfMissing: true }))
      .rejects.toMatchObject({ code })
    expect(list).toHaveBeenCalledTimes(2)
    expect(rotate).not.toHaveBeenCalled()
    expect(create).toHaveBeenCalledTimes(1)
  })

  it('does not rotate after the account changes during the redacted replay lookup', async () => {
    localStorage.setItem('auth_user', JSON.stringify({ id: 1 }))
    list.mockResolvedValueOnce({ items: [] }).mockImplementationOnce(async () => {
      localStorage.setItem('auth_user', JSON.stringify({ id: 2 }))
      return { items: [key({ id: 7 })] }
    })
    getAvailableGroups.mockResolvedValue([group(2)])
    create.mockResolvedValue(key({ id: 7 }))

    await expect(ensureInfiniteCanvasApiKey({ list, create, rotate, getAvailableGroups }, { createIfMissing: true }))
      .rejects.toMatchObject({ code: 'request-failed' })
    expect(rotate).not.toHaveBeenCalled()
    expect(create).toHaveBeenCalledTimes(1)
  })

  it('deduplicates concurrent first-entry setup requests', async () => {
    getAvailableGroups.mockResolvedValue([group(4), group(1)])
    list.mockResolvedValue({ items: [], pages: 1 })
    create.mockImplementation(async () => {
      await new Promise((resolve) => setTimeout(resolve, 5))
      return secretKey({ group_id: 4, group_ids: [4, 1] }, 'sk-new')
    })

    const client = { list, create, rotate, getAvailableGroups }
    const [first, second] = await Promise.all([
      ensureInfiniteCanvasApiKey(client, { createIfMissing: true }),
      ensureInfiniteCanvasApiKey(client, { createIfMissing: true }),
    ])

    expect(create).toHaveBeenCalledTimes(1)
    expect(first.apiKey).toBe('sk-new')
    expect(second.apiKey).toBe('sk-new')
  })

  it('never rotates or creates merely because the canvas is entered without a cached plaintext', async () => {
    getAvailableGroups.mockResolvedValue([group(4), group(1)])
    list.mockResolvedValue({ items: [key({ id: 7, group_ids: [2, 5], key_masked: 'sk-abcd****wxyz' })] })

    const error = await ensureInfiniteCanvasApiKey({ list, create, rotate, getAvailableGroups }).catch((e) => e)

    expect(error).toBeInstanceOf(InfiniteCanvasSetupError)
    expect(error.code).toBe('rotation-required')
    // 界面只拿到掩码和分组数，用来告诉用户要轮换的是哪一把。
    expect(error.rotationTarget).toEqual({ keyId: 7, keyMasked: 'sk-abcd****wxyz', groupCount: 2 })
    expect(rotate).not.toHaveBeenCalled()
    expect(create).not.toHaveBeenCalled()
    expect(getAvailableGroups).not.toHaveBeenCalled()
  })

  it('keeps asking on every later entry until the user consents (a refresh never rotates)', async () => {
    list.mockResolvedValue({ items: [key({ id: 7 })] })
    const client = { list, create, rotate, getAvailableGroups }

    for (let i = 0; i < 3; i += 1) {
      await expect(ensureInfiniteCanvasApiKey(client)).rejects.toMatchObject({ code: 'rotation-required' })
    }
    expect(rotate).not.toHaveBeenCalled()
  })

  it('reports a missing key instead of rotating when the client cannot rotate at all', async () => {
    list.mockResolvedValue({ items: [key({ id: 7 })] })
    await expect(ensureInfiniteCanvasApiKey({ list, create, getAvailableGroups }))
      .rejects.toMatchObject({ code: 'missing-key' })
    expect(create).not.toHaveBeenCalled()
  })

  it('reuses the named key without changing its routing groups once the user consents to rotation', async () => {
    getAvailableGroups.mockResolvedValue([group(4), group(1)])
    list.mockResolvedValue({ items: [key({ id: 7, group_ids: [2] })] })
    // 列表不再返回明文：用户在界面上确认后，才对这把画布专用 key 做一次就地轮换拿新明文。
    rotate.mockResolvedValue(secretKey({ id: 7, group_ids: [2] }, 'sk-rotated'))

    const session = await ensureInfiniteCanvasApiKey({ list, create, rotate, getAvailableGroups }, { allowRotate: true })

    expect(create).not.toHaveBeenCalled()
    expect(rotate).toHaveBeenCalledWith(7)
    expect(getAvailableGroups).not.toHaveBeenCalled()
    expect(session.groupIds).toEqual([2])
    expect(session.created).toBe(false)
    expect(session.rotated).toBe(true)
    expect(session.apiKey).toBe('sk-rotated')
    expect(session.keyId).toBe(7)
  })

  it('reuses the in-memory plaintext when the rotation time is omitted or null', async () => {
    list.mockResolvedValueOnce({ items: [key({ id: 7, group_ids: [2] })] })
      .mockResolvedValueOnce({ items: [key({ id: 7, group_ids: [2], key_rotated_at: null })] })
    rotate.mockResolvedValue(secretKey({ id: 7, group_ids: [2] }, 'sk-rotated'))

    const client = { list, create, rotate, getAvailableGroups }
    const first = await ensureInfiniteCanvasApiKey(client, { allowRotate: true })
    // 同一页面生命周期内已经有明文：再次进入既不再询问，也不会再轮换。
    const second = await ensureInfiniteCanvasApiKey(client)

    expect(rotate).toHaveBeenCalledTimes(1)
    expect(first.apiKey).toBe('sk-rotated')
    expect(second.apiKey).toBe('sk-rotated')
  })

  it('rotates once for concurrent confirmations (a double click)', async () => {
    list.mockResolvedValue({ items: [key({ id: 7 })] })
    rotate.mockImplementation(async () => {
      await new Promise((resolve) => setTimeout(resolve, 5))
      return secretKey({ id: 7 }, 'sk-rotated-once')
    })
    const client = { list, create, rotate, getAvailableGroups }

    const [first, second] = await Promise.all([
      ensureInfiniteCanvasApiKey(client, { allowRotate: true }),
      ensureInfiniteCanvasApiKey(client, { allowRotate: true }),
    ])

    expect(rotate).toHaveBeenCalledTimes(1)
    expect(first.apiKey).toBe('sk-rotated-once')
    expect(second.apiKey).toBe('sk-rotated-once')
  })

  it('rotates the canvas key found at confirmation time, not the one shown earlier', async () => {
    list.mockResolvedValueOnce({ items: [key({ id: 7 })] })
      .mockResolvedValueOnce({ items: [key({ id: 9 }), key({ id: 7 })] })
    rotate.mockResolvedValue(secretKey({ id: 9 }, 'sk-newest'))
    const client = { list, create, rotate, getAvailableGroups }

    await expect(ensureInfiniteCanvasApiKey(client)).rejects.toMatchObject({ code: 'rotation-required', rotationTarget: { keyId: 7 } })
    await ensureInfiniteCanvasApiKey(client, { allowRotate: true })

    expect(rotate.mock.calls).toEqual([[9]])
  })

  it.each([
    { entry: 're-entry', replay: false },
    { entry: 'redacted creation replay', replay: true },
  ])('recovers once on $entry after a rotation with the same mask', async ({ replay }) => {
    localStorage.setItem('auth_user', JSON.stringify({ id: 1 }))
    const original = key({ id: 7, key_rotated_at: '2026-01-01T00:00:00.123456Z' })
    const rotatedElsewhere = { ...original, key_rotated_at: '2026-01-01T00:00:00.123457Z' }
    list.mockResolvedValueOnce({ items: [original] })
    rotate.mockResolvedValueOnce(secretKey(original, 'fixture-before-rotation'))
      .mockResolvedValueOnce(secretKey({ ...original, key_rotated_at: '2026-01-01T00:00:00.123458Z' }, 'fixture-after-recovery'))
    const client = { list, create, rotate, getAvailableGroups }
    await ensureInfiniteCanvasApiKey(client, { allowRotate: true })

    if (replay) {
      list.mockResolvedValueOnce({ items: [] })
      getAvailableGroups.mockResolvedValue([group(2)])
      create.mockResolvedValue(original)
    }
    list.mockResolvedValueOnce({ items: [rotatedElsewhere] })

    const session = await ensureInfiniteCanvasApiKey(client, { createIfMissing: replay, allowRotate: true })

    expect(session).toMatchObject({ keyId: 7, apiKey: 'fixture-after-recovery', created: false, rotated: true })
    expect(rotate.mock.calls).toEqual([[7], [7]])
    expect(create).toHaveBeenCalledTimes(replay ? 1 : 0)
  })

  it('reuses the same credential after routing or usage metadata changes', async () => {
    const original = key({ id: 7, key_rotated_at: '2026-01-01T00:00:00.123456Z' })
    list.mockResolvedValueOnce({ items: [original] }).mockResolvedValueOnce({
      items: [{
        ...original,
        group_ids: [9],
        updated_at: '2026-01-02T00:00:00Z',
        last_used_at: '2026-01-03T00:00:00Z',
        quota_used: 4,
      }],
    })
    rotate.mockResolvedValueOnce(secretKey(original, 'fixture-unchanged'))
    const client = { list, create, rotate, getAvailableGroups }
    await ensureInfiniteCanvasApiKey(client, { allowRotate: true })

    await expect(ensureInfiniteCanvasApiKey(client))
      .resolves.toMatchObject({ apiKey: 'fixture-unchanged', groupIds: [9], rotated: false })
    expect(rotate).toHaveBeenCalledTimes(1)
  })

  it('recovers when the mask changes in an older response without a rotation time', async () => {
    const original = key({ id: 7 })
    const changedMask = { ...original, key_masked: 'sk-live****next' }
    list.mockResolvedValueOnce({ items: [original] }).mockResolvedValueOnce({ items: [changedMask] })
    rotate.mockResolvedValueOnce(secretKey(original, 'fixture-original'))
      .mockResolvedValueOnce(secretKey(changedMask, 'fixture-legacy-recovery'))
    const client = { list, create, rotate, getAvailableGroups }
    await ensureInfiniteCanvasApiKey(client, { allowRotate: true })

    // 掩码变了说明别处轮换过：缓存作废，但没有用户确认就不再自动轮换。
    list.mockResolvedValueOnce({ items: [changedMask] })
    await expect(ensureInfiniteCanvasApiKey(client)).rejects.toMatchObject({ code: 'rotation-required' })
    expect(rotate).toHaveBeenCalledTimes(1)

    await expect(ensureInfiniteCanvasApiKey(client, { allowRotate: true }))
      .resolves.toMatchObject({ apiKey: 'fixture-legacy-recovery', rotated: true })
    expect(rotate.mock.calls).toEqual([[7], [7]])
  })

  it('fails without returning stale plaintext when recovery supplies none', async () => {
    const original = key({ id: 7 })
    const rotated = { ...original, key_rotated_at: '2026-01-01T00:00:00.123456Z' }
    list.mockResolvedValueOnce({ items: [original] }).mockResolvedValueOnce({ items: [rotated] })
    rotate.mockResolvedValueOnce(secretKey(original, 'fixture-original')).mockResolvedValueOnce(rotated)
    const client = { list, create, rotate, getAvailableGroups }
    await ensureInfiniteCanvasApiKey(client, { allowRotate: true })

    await expect(ensureInfiniteCanvasApiKey(client, { allowRotate: true })).rejects.toMatchObject({ code: 'missing-key' })
    expect(rotate.mock.calls).toEqual([[7], [7]])
    expect(create).not.toHaveBeenCalled()
  })

  it('does not reuse a cached credential belonging to a different owner', async () => {
    const firstOwner = key({ id: 7, user_id: 1 })
    const secondOwner = { ...firstOwner, user_id: 2 }
    list.mockResolvedValueOnce({ items: [firstOwner] })
      .mockResolvedValueOnce({ items: [secondOwner] })
      .mockResolvedValueOnce({ items: [secondOwner] })
    rotate.mockResolvedValueOnce(secretKey(firstOwner, 'fixture-first-owner'))
      .mockResolvedValueOnce(secretKey(secondOwner, 'fixture-second-owner'))
    const client = { list, create, rotate, getAvailableGroups }
    localStorage.setItem('auth_user', JSON.stringify({ id: 1 }))
    await ensureInfiniteCanvasApiKey(client, { allowRotate: true })

    // 换了账号：第一个账号的明文不能复用，也不能替第二个账号自动轮换。
    localStorage.setItem('auth_user', JSON.stringify({ id: 2 }))
    await expect(ensureInfiniteCanvasApiKey(client)).rejects.toMatchObject({ code: 'rotation-required' })
    expect(rotate.mock.calls).toEqual([[7]])

    await expect(ensureInfiniteCanvasApiKey(client, { allowRotate: true }))
      .resolves.toMatchObject({ apiKey: 'fixture-second-owner', rotated: true })
    expect(rotate.mock.calls).toEqual([[7], [7]])
  })

  describe('identity verification (key creation and rotation hand out plaintext)', () => {
    const verification = { method: 'password' as const, password: 'fixture-password' }

    it('forwards the verification to create and does not add the key when there is none', async () => {
      getAvailableGroups.mockResolvedValue([group(4)])
      list.mockResolvedValue({ items: [] })
      create.mockResolvedValue(secretKey({ group_id: 4, group_ids: [4] }, 'sk-new'))

      await ensureInfiniteCanvasApiKey({ list, create, getAvailableGroups }, { createIfMissing: true, verification })
      expect(create.mock.calls[0][10]).toEqual({ idempotencyKey: expect.any(String), verification })

      create.mockClear()
      await ensureInfiniteCanvasApiKey({ list, create, getAvailableGroups }, { createIfMissing: true })
      expect(Object.keys(create.mock.calls[0][10])).toEqual(['idempotencyKey'])
    })

    it('forwards the verification to the confirmed rotation', async () => {
      list.mockResolvedValue({ items: [key({ id: 7 })] })
      rotate.mockResolvedValue(secretKey({ id: 7 }, 'sk-rotated'))

      await ensureInfiniteCanvasApiKey({ list, create, rotate, getAvailableGroups }, { allowRotate: true, verification })

      expect(rotate).toHaveBeenCalledWith(7, verification)
    })

    // 三类账号的校验材料形状不同（密码 / TOTP 码 / Passkey 一次性票据），创建和轮换都必须原样交给接口：
    // 码和票据都是一次性的，这里既不能改、不能缓存，也不能在失败后用同一份再试一次。
    it.each([
      { name: 'password', payload: { method: 'password' as const, password: 'fixture-password' } },
      { name: 'TOTP', payload: { method: 'totp' as const, totp_code: '123456' } },
      { name: 'Passkey', payload: { method: 'passkey' as const, passkey_session_token: 'fixture-ticket' } },
    ])('hands the $name verification to create and to rotate unchanged', async ({ payload }) => {
      getAvailableGroups.mockResolvedValue([group(4)])
      list.mockResolvedValue({ items: [] })
      create.mockResolvedValue(secretKey({ group_id: 4, group_ids: [4] }, 'sk-new'))
      await ensureInfiniteCanvasApiKey({ list, create, getAvailableGroups }, { createIfMissing: true, verification: payload })
      expect(create.mock.calls[0][10]).toEqual({ idempotencyKey: expect.any(String), verification: payload })

      __resetInfiniteCanvasSecretsForTests()
      list.mockResolvedValue({ items: [key({ id: 7 })] })
      rotate.mockResolvedValue(secretKey({ id: 7 }, 'sk-rotated'))
      await ensureInfiniteCanvasApiKey({ list, create, rotate, getAvailableGroups }, { allowRotate: true, verification: payload })
      expect(rotate).toHaveBeenCalledTimes(1)
      expect(rotate).toHaveBeenCalledWith(7, payload)
    })

    it('never rotates just because a verification payload is present', async () => {
      list.mockResolvedValue({ items: [key({ id: 7 })] })
      await expect(ensureInfiniteCanvasApiKey({ list, create, rotate, getAvailableGroups }, { verification }))
        .rejects.toMatchObject({ code: 'rotation-required' })
      expect(rotate).not.toHaveBeenCalled()
    })

    it.each([
      'VERIFY_REQUIRED',
      'VERIFY_PASSWORD_INVALID',
      // 同一账号密码输错次数用尽（429）：和其它校验失败同一类，页面据此留在确认卡上提示稍后再试。
      'VERIFY_PASSWORD_RATE_LIMITED',
      'VERIFY_TOTP_INVALID',
      'VERIFY_PASSKEY_FAILED',
      'VERIFY_FACTOR_UNAVAILABLE',
    ])('reports %s as a verification failure and does not retry or fall back', async (reason) => {
      list.mockResolvedValue({ items: [key({ id: 7 })] })
      rotate.mockRejectedValue(Object.assign(new Error('rejected'), { reason }))

      await expect(ensureInfiniteCanvasApiKey({ list, create, rotate, getAvailableGroups }, { allowRotate: true, verification }))
        .rejects.toMatchObject({ code: 'verification-failed', reason })
      expect(rotate).toHaveBeenCalledTimes(1)
      expect(create).not.toHaveBeenCalled()
    })

    it('reports a rejected creation verification the same way and never recreates the key', async () => {
      getAvailableGroups.mockResolvedValue([group(4)])
      list.mockResolvedValue({ items: [] })
      create.mockRejectedValue(Object.assign(new Error('rejected'), { reason: 'VERIFY_TOTP_INVALID' }))

      await expect(ensureInfiniteCanvasApiKey({ list, create, getAvailableGroups }, { createIfMissing: true, verification: { method: 'totp', totp_code: '123456' } }))
        .rejects.toMatchObject({ code: 'verification-failed', reason: 'VERIFY_TOTP_INVALID' })
      expect(create).toHaveBeenCalledTimes(1)
    })

    it('does not mistake an ordinary server error for a verification failure', async () => {
      list.mockResolvedValue({ items: [key({ id: 7 })] })
      rotate.mockRejectedValue(Object.assign(new Error('boom'), { reason: 'INTERNAL_ERROR' }))

      await expect(ensureInfiniteCanvasApiKey({ list, create, rotate, getAvailableGroups }, { allowRotate: true }))
        .rejects.toMatchObject({ code: 'request-failed' })
    })

    it('does not keep the secret material anywhere after the call', async () => {
      list.mockResolvedValue({ items: [key({ id: 7 })] })
      rotate.mockResolvedValue(secretKey({ id: 7 }, 'sk-rotated'))
      await ensureInfiniteCanvasApiKey({ list, create, rotate, getAvailableGroups }, { allowRotate: true, verification })

      // 下一次进入（没有 verification）既不复用也不回放这份校验材料：明文缓存命中，rotate 不会再被调用。
      await ensureInfiniteCanvasApiKey({ list, create, rotate, getAvailableGroups })
      expect(rotate).toHaveBeenCalledTimes(1)
      expect(JSON.stringify(localStorage)).not.toContain('fixture-password')
      expect(JSON.stringify(sessionStorage)).not.toContain('fixture-password')
    })
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
    expect(rotate).not.toHaveBeenCalled()
    expect(create).not.toHaveBeenCalled()
  })

  it('checks current key state again after deletion instead of reusing cached credentials', async () => {
    localStorage.setItem('auth_user', JSON.stringify({ id: 1 }))
    const client = { list, create, getAvailableGroups }
    list.mockResolvedValue({ items: [] })
    getAvailableGroups.mockResolvedValue([group(2)])
    create.mockResolvedValue(secretKey({}, 'sk-created'))
    await ensureInfiniteCanvasApiKey(client, { createIfMissing: true })
    await expect(ensureInfiniteCanvasApiKey(client)).rejects.toMatchObject({ code: 'not-configured' })
    expect(create).toHaveBeenCalledTimes(1)
    expect(list).toHaveBeenCalledTimes(2)
  })

  it('does not reuse an in-flight session after switching users', async () => {
    let finishOldList!: (value: { items: ApiKey[] }) => void
    list.mockImplementationOnce(() => new Promise((resolve) => { finishOldList = resolve }))
      .mockResolvedValueOnce({ items: [key({ user_id: 2 })] })
    rotate.mockResolvedValue(secretKey({ user_id: 2 }, 'sk-second-user'))
    const client = { list, create, rotate, getAvailableGroups }
    localStorage.setItem('auth_user', JSON.stringify({ id: 1 }))
    const oldSession = ensureInfiniteCanvasApiKey(client, { allowRotate: true })
    const oldRejection = expect(oldSession).rejects.toMatchObject({ code: 'request-failed' })
    localStorage.setItem('auth_user', JSON.stringify({ id: 2 }))
    const newSession = await ensureInfiniteCanvasApiKey(client, { allowRotate: true })
    finishOldList({ items: [key({ user_id: 1 })] })
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
