import { beforeEach, describe, expect, it, vi } from 'vitest'

const { post, get } = vi.hoisted(() => ({ post: vi.fn(), get: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: { post, get } }))

import { refreshCredentials } from '@/api/admin/accounts'

const account = { id: 42, name: 'refreshed account', status: 'active', credentials: {}, extra: {} }
const warning = {
  message: 'Token refreshed successfully, but project_id could not be retrieved (will retry automatically)',
  warning: 'missing_project_id_temporary',
}

describe('admin account refresh response', () => {
  beforeEach(() => { post.mockReset(); get.mockReset() })

  it('returns normal refresh responses without fetching the account again', async () => {
    post.mockResolvedValue({ data: account })
    await expect(refreshCredentials(42)).resolves.toEqual(account)
    expect(post).toHaveBeenCalledWith('/admin/accounts/42/refresh')
    expect(get).not.toHaveBeenCalled()
  })

  it('fetches the refreshed account by the requested id when only a warning is returned', async () => {
    post.mockResolvedValue({ data: warning })
    get.mockResolvedValue({ data: account })
    await expect(refreshCredentials(42)).resolves.toEqual({ ...account, ...warning })
    expect(post).toHaveBeenCalledTimes(1)
    expect(get).toHaveBeenCalledTimes(1)
    expect(get).toHaveBeenCalledWith('/admin/accounts/42')
  })

  it('does not return an incomplete account when the follow-up fetch fails', async () => {
    post.mockResolvedValue({ data: warning })
    get.mockRejectedValue(new Error('account unavailable'))
    await expect(refreshCredentials(42)).rejects.toThrow('account unavailable')
    expect(post).toHaveBeenCalledTimes(1)
  })

  it('does not fetch account details after a failed refresh', async () => {
    post.mockRejectedValue(new Error('invalid credentials'))
    await expect(refreshCredentials(42)).rejects.toThrow('invalid credentials')
    expect(get).not.toHaveBeenCalled()
  })
})
